//! Module D: Multi-Producer Multi-Consumer Work-Stealing Queue
//!
//! A hybrid MPMC architecture combining:
//! - A global SPSC ring buffer for producer → worker staging
//! - Per-worker Chase-Lev-style work-stealing deques
//! - A three-tier steal protocol: local → global → other workers
//!
//! This design eliminates the single-consumer bottleneck that caused
//! 160× inflated latency in the original OTAP benchmark.

use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use crossbeam_utils::CachePadded;
use std::cell::UnsafeCell;

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/// Capacity of the global staging ring buffer.
const GLOBAL_CAP: usize = 4096;

/// Capacity of each worker's local Chase-Lev deque.
const LOCAL_CAP: usize = 256;

/// Maximum number of items to drain from the global ring in one steal attempt.
const BATCH_DRAIN_SIZE: usize = 16;

// ---------------------------------------------------------------------------
// LocalDeque — Chase-Lev style work-stealing deque (fixed capacity)
// ---------------------------------------------------------------------------

/// A Chase-Lev-style work-stealing deque with fixed capacity.
///
/// Each worker owns one instance. The owner pushes and pops from the bottom;
/// thieves steal from the top.  All operations are lock-free.
///
/// # Memory layout
///
/// ```text
///   top (AtomicUsize)     -- thieves read/write via CAS
///   bottom (AtomicUsize)  -- owner reads/writes
///   buffer: [T; N]        -- the ring buffer storage
/// ```
///
/// The `top` counter is the index of the next element to steal.
/// The `bottom` counter is one past the last pushed element.
/// When `top == bottom` the deque is empty.
#[repr(align(64))]
pub struct LocalDeque<T: Copy, const N: usize> {
    buffer: UnsafeCell<Box<[T; N]>>,
    /// Thieves read this, owner writes it (single-element race).
    top: CachePadded<AtomicUsize>,
    /// Owner reads and writes this.
    bottom: CachePadded<AtomicUsize>,
}

// Safety: LocalDeque is Send + Sync when T: Send.
// The owner thread may send the deque to another thread (Send),
// and multiple threads may attempt to steal concurrently (Sync).
unsafe impl<T: Send + Copy, const N: usize> Send for LocalDeque<T, N> {}
unsafe impl<T: Send + Copy, const N: usize> Sync for LocalDeque<T, N> {}

impl<T: Copy + Default, const N: usize> LocalDeque<T, N> {
    /// Create a new empty local deque.
    pub fn new() -> Self {
        assert!(N.is_power_of_two(), "LocalDeque capacity must be a power of 2");
        let default_array: Box<[T; N]> = Box::new([T::default(); N]);
        Self {
            buffer: UnsafeCell::new(default_array),
            top: CachePadded::new(AtomicUsize::new(0)),
            bottom: CachePadded::new(AtomicUsize::new(0)),
        }
    }

    /// Push an item to the bottom of the deque (owner only).
    /// Returns `true` on success, `false` if the deque is full.
    #[inline]
    pub fn push_bottom(&self, item: T) -> bool {
        let bottom = self.bottom.load(Ordering::Relaxed);
        let top = self.top.load(Ordering::Acquire);

        // Check capacity: bottom - top == N means full
        if bottom.wrapping_sub(top) >= N {
            return false;
        }

        // Write item and publish
        let idx = bottom & (N - 1);
        unsafe {
            let buf = &mut *self.buffer.get();
            buf[idx] = item;
        }
        self.bottom.store(bottom.wrapping_add(1), Ordering::Release);
        true
    }

    /// Pop an item from the bottom of the deque (owner only).
    /// Returns `Some(T)` if the deque was non-empty, `None` otherwise.
    #[inline]
    pub fn pop_bottom(&self) -> Option<T> {
        let bottom = self.bottom.load(Ordering::Relaxed);

        // Empty check: if bottom == 0 and nothing was ever pushed
        // We need to handle wrapping, but since we check against top:
        let bottom = bottom.wrapping_sub(1);
        self.bottom.store(bottom, Ordering::Relaxed);

        let top = self.top.load(Ordering::Acquire);

        if bottom.wrapping_sub(top) < N {
            // There is at least one element
            let idx = bottom & (N - 1);
            let item = unsafe {
                let buf = &*self.buffer.get();
                buf[idx]
            };

            if bottom != top {
                // Multiple elements: no contention, just return
                Some(item)
            } else {
                // Exactly one element: race with thieves
                // Try to claim it by moving top forward
                match self.top.compare_exchange(
                    top,
                    top.wrapping_add(1),
                    Ordering::SeqCst,
                    Ordering::Relaxed,
                ) {
                    Ok(_) => {
                        // Won the race: element is ours
                        self.bottom.store(top.wrapping_add(1), Ordering::Relaxed);
                        Some(item)
                    }
                    Err(_) => {
                        // Lost the race: thief got it
                        self.bottom.store(top.wrapping_add(1), Ordering::Relaxed);
                        None
                    }
                }
            }
        } else {
            // Empty: restore bottom and return None
            self.bottom.store(bottom.wrapping_add(1), Ordering::Relaxed);
            None
        }
    }

    /// Steal an item from the top of the deque (other workers).
    /// Returns `Some(T)` if theft succeeded, `None` if deque was empty
    /// or lost a race with another thief.
    #[inline]
    pub fn steal_top(&self) -> Option<T> {
        let top = self.top.load(Ordering::Acquire);
        let bottom = self.bottom.load(Ordering::Acquire);

        // Empty check
        if bottom.wrapping_sub(top) == 0 || bottom.wrapping_sub(top) > N {
            // Note: wrapping_sub > N indicates the deque is in an inconsistent
            // state (bottom < top after wrapping) or truly empty
            return None;
        }

        // Read the item at top
        let idx = top & (N - 1);
        let item = unsafe {
            let buf = &*self.buffer.get();
            buf[idx]
        };

        // Try to claim this element by incrementing top
        match self.top.compare_exchange(
            top,
            top.wrapping_add(1),
            Ordering::SeqCst,
            Ordering::Relaxed,
        ) {
            Ok(_) => Some(item),
            Err(_) => None, // Lost race with another thief or owner
        }
    }

    /// Number of items currently in the deque (approximate).
    #[inline]
    pub fn len(&self) -> usize {
        let bottom = self.bottom.load(Ordering::Acquire);
        let top = self.top.load(Ordering::Acquire);
        // bottom >= top when non-empty (for normal non-wrapping case)
        let diff = bottom.wrapping_sub(top);
        if diff > N { 0 } else { diff }
    }

    /// Check if the deque is empty.
    #[inline]
    pub fn is_empty(&self) -> bool {
        let bottom = self.bottom.load(Ordering::Acquire);
        let top = self.top.load(Ordering::Acquire);
        bottom == top
    }

    /// Check if the deque is full.
    #[inline]
    pub fn is_full(&self) -> bool {
        let bottom = self.bottom.load(Ordering::Relaxed);
        let top = self.top.load(Ordering::Acquire);
        bottom.wrapping_sub(top) >= N
    }
}

// ---------------------------------------------------------------------------
// WorkStealingQueue — Global ring + local deques
// ---------------------------------------------------------------------------

/// Work-stealing queue with 1 global ring + N local deques.
///
/// # Architecture
///
/// ```text
/// Producer → Global RingBuffer<T, GLOBAL_CAP>
///                → Worker 1: LocalDeque<T, LOCAL_CAP>
///                → Worker 2: LocalDeque<T, LOCAL_CAP>
///                → Worker 3: LocalDeque<T, LOCAL_CAP>
/// ```
///
/// The producer submits to the global ring. Workers first try to pop from
/// their local deque, then drain the global ring into their local deque,
/// and finally steal from other workers.
pub struct WorkStealingQueue<T: Copy> {
    /// Number of consumer workers.
    num_workers: usize,
    /// Global ring for producer → worker staging.
    global: crate::ringbuf::RingBuffer<T, GLOBAL_CAP>,
    /// Per-worker local deques.
    workers: Vec<LocalDeque<T, LOCAL_CAP>>,
    /// Spinlock protecting tier-2 global ring drains.
    /// The global ring is SPSC (single consumer at a time); without this lock
    /// multiple workers draining concurrently would violate that invariant and
    /// return the same items to more than one consumer.
    global_drain_lock: CachePadded<AtomicBool>,
}

// Safety: WorkStealingQueue is Send + Sync when T: Send.
// Each worker only accesses its own local deque directly.
// Stealing from other deques uses atomic operations.
unsafe impl<T: Send + Copy> Send for WorkStealingQueue<T> {}
unsafe impl<T: Send + Copy> Sync for WorkStealingQueue<T> {}

impl<T: Copy + Default> WorkStealingQueue<T> {
    /// Create a new work-stealing queue.
    ///
    /// # Arguments
    ///
    /// * `num_workers` — Number of consumer threads.
    ///
    /// # Panics
    ///
    /// Panics if `num_workers` is 0.
    pub fn new(num_workers: usize) -> Self {
        assert!(num_workers > 0, "WorkStealingQueue requires at least 1 worker");

        let mut workers = Vec::with_capacity(num_workers);
        for _ in 0..num_workers {
            workers.push(LocalDeque::new());
        }

        Self {
            num_workers,
            global: crate::ringbuf::RingBuffer::new(),
            workers,
            global_drain_lock: CachePadded::new(AtomicBool::new(false)),
        }
    }

    /// Producer submits an item to the global ring.
    ///
    /// Returns `true` if successful, `false` if the global ring is full.
    #[inline]
    pub fn submit(&self, item: T) -> bool {
        self.global.try_submit(item)
    }

    /// Consumer `worker_id` attempts to get work.
    ///
    /// The steal protocol follows a three-tier strategy:
    /// 1. **Local work** — Try to pop from the worker's own deque (no contention).
    /// 2. **Global drain** — Try to drain items from the global ring into the
    ///    local deque, then retry step 1.
    /// 3. **Worker-to-worker theft** — Try to steal from other workers' deques
    ///    in random order.
    ///
    /// Returns `Some(T)` if work was found, `None` if all sources are empty.
    pub fn steal(&self, worker_id: usize) -> Option<T> {
        assert!(
            worker_id < self.num_workers,
            "worker_id {} out of range ({} workers)",
            worker_id,
            self.num_workers
        );

        // --- Tier 1: Try local deque ---
        if let Some(item) = self.pop_local(worker_id) {
            return Some(item);
        }

        // --- Tier 2: Drain from global ring into local deque ---
        // The global ring is SPSC — at most one thread may consume at a time.
        // Use a CAS spinlock so only one worker drains per attempt; others skip
        // straight to tier 3 rather than busy-spinning (avoids convoy effects).
        if self.global_drain_lock
            .compare_exchange(false, true, Ordering::Acquire, Ordering::Relaxed)
            .is_ok()
        {
            let mut drained = 0;
            for _ in 0..BATCH_DRAIN_SIZE {
                match self.global.try_drain() {
                    Some(item) => {
                        if self.push_local(worker_id, item) {
                            drained += 1;
                        } else {
                            // Local deque full: we can't push more
                            break;
                        }
                    }
                    None => break,
                }
            }
            self.global_drain_lock.store(false, Ordering::Release);

            if drained > 0 {
                return self.pop_local(worker_id);
            }
        }

        // --- Tier 3: Steal from other workers ---
        // Random iteration order over workers
        let mut indices: Vec<usize> = (0..self.num_workers)
            .filter(|&i| i != worker_id)
            .collect();

        // Simple randomization using a hash of worker_id and a counter
        // This is deterministic but scatters access patterns across workers
        if indices.len() > 1 {
            // Fisher-Yates shuffle with a simple seed based on worker_id
            let mut seed = worker_id.wrapping_mul(0x9E3779B97F4A7C15);
            for i in (1..indices.len()).rev() {
                seed = seed.wrapping_mul(1103515245).wrapping_add(12345);
                let j = (seed % (i as usize + 1)) as usize;
                indices.swap(i, j);
            }
        }

        for &other_id in &indices {
            if let Some(item) = self.workers[other_id].steal_top() {
                return Some(item);
            }
        }

        // All sources empty
        None
    }

    /// Push work to a worker's local deque.
    ///
    /// Called by a worker after stealing from the global ring or another worker.
    ///
    /// Returns `true` on success, `false` if the local deque is full.
    #[inline]
    pub fn push_local(&self, worker_id: usize, item: T) -> bool {
        assert!(
            worker_id < self.num_workers,
            "worker_id {} out of range ({} workers)",
            worker_id,
            self.num_workers
        );
        self.workers[worker_id].push_bottom(item)
    }

    /// Pop from a worker's local deque for self-consumption.
    ///
    /// Returns `Some(T)` if work was available, `None` if the deque is empty.
    #[inline]
    pub fn pop_local(&self, worker_id: usize) -> Option<T> {
        assert!(
            worker_id < self.num_workers,
            "worker_id {} out of range ({} workers)",
            worker_id,
            self.num_workers
        );
        self.workers[worker_id].pop_bottom()
    }

    /// Total items across all deques plus the global ring (approximate).
    ///
    /// This is an estimate: concurrent operations may shift counts
    /// between the global ring and local deques during the scan.
    pub fn len(&self) -> usize {
        let mut total = self.global.readable();
        for i in 0..self.num_workers {
            total += self.workers[i].len();
        }
        total
    }

    /// Check if all queues (global + all local deques) are empty.
    ///
    /// This is conservative: it may return `false` even if all work
    /// has been consumed, due to concurrent in-flight operations.
    pub fn is_empty(&self) -> bool {
        if self.global.readable() != 0 {
            return false;
        }
        for i in 0..self.num_workers {
            if !self.workers[i].is_empty() {
                return false;
            }
        }
        true
    }
}

// ---------------------------------------------------------------------------
// Unit tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    // --- LocalDeque tests ---

    #[test]
    fn test_local_deque_push_pop() {
        let deque: LocalDeque<u64, 4> = LocalDeque::new();
        assert!(deque.is_empty());
        assert_eq!(deque.len(), 0);

        assert!(deque.push_bottom(10));
        assert!(deque.push_bottom(20));
        assert!(deque.push_bottom(30));
        assert_eq!(deque.len(), 3);

        assert_eq!(deque.pop_bottom(), Some(30));
        assert_eq!(deque.pop_bottom(), Some(20));
        assert_eq!(deque.pop_bottom(), Some(10));
        assert_eq!(deque.pop_bottom(), None);
        assert!(deque.is_empty());
    }

    #[test]
    fn test_local_deque_full() {
        let deque: LocalDeque<u64, 4> = LocalDeque::new();
        assert!(deque.push_bottom(1));
        assert!(deque.push_bottom(2));
        assert!(deque.push_bottom(3));
        assert!(deque.push_bottom(4));
        assert!(!deque.push_bottom(5)); // Full
        assert!(deque.is_full());
    }

    #[test]
    fn test_local_deque_steal_top() {
        let deque: LocalDeque<u64, 8> = LocalDeque::new();
        assert!(deque.push_bottom(10));
        assert!(deque.push_bottom(20));
        assert!(deque.push_bottom(30));

        // Steal from top gets the oldest element
        assert_eq!(deque.steal_top(), Some(10));
        assert_eq!(deque.steal_top(), Some(20));
        assert_eq!(deque.pop_bottom(), Some(30));
        assert_eq!(deque.steal_top(), None);
    }

    #[test]
    fn test_local_deque_empty_pop() {
        let deque: LocalDeque<u64, 4> = LocalDeque::new();
        assert_eq!(deque.pop_bottom(), None);
        assert_eq!(deque.steal_top(), None);
    }

    #[test]
    fn test_local_deque_steal_race() {
        use std::sync::Arc;
        use std::thread;

        let deque: Arc<LocalDeque<u64, 128>> = Arc::new(LocalDeque::new());

        // Owner pushes 100 items
        for i in 0..100 {
            assert!(deque.push_bottom(i));
        }

        // Spawn 4 thieves and the owner all competing
        let mut handles = vec![];
        for _ in 0..4 {
            let d = Arc::clone(&deque);
            handles.push(thread::spawn(move || {
                let mut stolen = 0;
                loop {
                    match d.steal_top() {
                        Some(_) => stolen += 1,
                        None => {
                            // Check if truly empty
                            if d.is_empty() {
                                break;
                            }
                        }
                    }
                }
                stolen
            }));
        }

        // Owner pops from bottom simultaneously
        let d = Arc::clone(&deque);
        let owner_handle = thread::spawn(move || {
            let mut popped = 0;
            loop {
                match d.pop_bottom() {
                    Some(_) => popped += 1,
                    None => {
                        if d.is_empty() {
                            break;
                        }
                    }
                }
            }
            popped
        });

        let mut total_stolen = 0;
        for h in handles {
            total_stolen += h.join().unwrap();
        }
        let total_popped = owner_handle.join().unwrap();

        assert_eq!(
            total_stolen + total_popped,
            100,
            "All 100 items should be accounted for (stolen={}, popped={})",
            total_stolen,
            total_popped
        );
    }

    // --- WorkStealingQueue tests ---

    #[test]
    fn test_ws_queue_basic() {
        let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(4);

        // Submit items
        assert!(queue.submit(10));
        assert!(queue.submit(20));
        assert!(queue.submit(30));
        assert_eq!(queue.len(), 3);

        // Worker 0 steals
        let item = queue.steal(0);
        assert!(item.is_some());

        // Remaining items should be stealable
        let mut remaining = 0;
        for _ in 0..100 {
            if queue.steal(0).is_some() {
                remaining += 1;
            } else {
                break;
            }
        }
        assert_eq!(remaining, 2);
    }

    #[test]
    fn test_ws_queue_local_push_pop() {
        let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(2);

        // Push to worker 0's local deque
        assert!(queue.push_local(0, 42));
        assert!(queue.push_local(0, 43));

        // Pop from worker 0's local deque (LIFO order)
        assert_eq!(queue.pop_local(0), Some(43));
        assert_eq!(queue.pop_local(0), Some(42));
        assert_eq!(queue.pop_local(0), None);
    }

    #[test]
    fn test_ws_queue_steal_from_global() {
        let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(2);

        // Submit to global ring
        for i in 0..20 {
            assert!(queue.submit(i));
        }

        // Worker 0 should drain from global into local and return one
        let item = queue.steal(0);
        assert!(item.is_some());
        assert!(item.unwrap() < 20);
    }

    #[test]
    fn test_ws_queue_worker_to_worker_steal() {
        let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(2);

        // Push items to worker 0's local deque
        for i in 0..10 {
            assert!(queue.push_local(0, i));
        }

        // Worker 1 should be able to steal from worker 0
        let item = queue.steal(1);
        assert!(item.is_some());
        // Stolen from top = oldest element = 0
        assert_eq!(item.unwrap(), 0);
    }

    #[test]
    fn test_ws_queue_is_empty() {
        let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(2);
        assert!(queue.is_empty());

        queue.submit(1);
        assert!(!queue.is_empty());

        // Drain the item
        while queue.steal(0).is_some() {}

        // After draining global, should be empty
        // (may need a few retries due to batching)
        assert!(queue.is_empty());
    }

    #[test]
    fn test_ws_queue_concurrent_submit_steal() {
        use std::sync::atomic::{AtomicBool, Ordering};
        use std::sync::{Arc, Barrier};
        use std::thread;

        let queue: Arc<WorkStealingQueue<u64>> = Arc::new(WorkStealingQueue::new(4));
        const NUM_ITEMS: usize = 10_000;

        // Barrier ensures all threads are ready before any starts work.
        let barrier = Arc::new(Barrier::new(5)); // 1 producer + 4 consumers
        // producer_done signals that all NUM_ITEMS have been submitted.
        let producer_done = Arc::new(AtomicBool::new(false));

        // Producer thread
        let q = Arc::clone(&queue);
        let b = Arc::clone(&barrier);
        let done = Arc::clone(&producer_done);
        let producer = thread::spawn(move || {
            b.wait();
            let mut submitted = 0;
            for i in 0..NUM_ITEMS {
                loop {
                    if q.submit(i as u64) {
                        submitted += 1;
                        break;
                    }
                    thread::yield_now();
                }
            }
            done.store(true, Ordering::Release);
            submitted
        });

        // Consumer threads — exit only when producer is done AND queue is empty.
        let mut consumers = vec![];
        for worker_id in 0..4 {
            let q = Arc::clone(&queue);
            let b = Arc::clone(&barrier);
            let done = Arc::clone(&producer_done);
            consumers.push(thread::spawn(move || {
                b.wait();
                let mut stolen = 0;
                loop {
                    match q.steal(worker_id) {
                        Some(_) => stolen += 1,
                        None => {
                            if done.load(Ordering::Acquire) && q.is_empty() {
                                thread::yield_now();
                                if q.is_empty() {
                                    break;
                                }
                            }
                        }
                    }
                }
                stolen
            }));
        }

        let submitted = producer.join().unwrap();
        let mut consumed = 0;
        for c in consumers {
            consumed += c.join().unwrap();
        }

        assert_eq!(
            submitted, consumed,
            "All submitted items must be consumed (submitted={}, consumed={})",
            submitted, consumed
        );
    }

    #[test]
    fn test_ws_queue_multiple_workers_drain() {
        let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(4);

        // Submit 100 items
        for i in 0..100 {
            assert!(queue.submit(i));
        }

        // All 4 workers compete
        let mut total = 0;
        for worker_id in 0..4 {
            let mut count = 0;
            while let Some(_) = queue.steal(worker_id) {
                count += 1;
            }
            total += count;
        }

        assert_eq!(total, 100, "All 100 items should be drained");
    }

    #[test]
    fn test_ws_queue_len_accuracy() {
        let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(2);

        assert_eq!(queue.len(), 0);

        queue.submit(1);
        queue.submit(2);
        queue.submit(3);
        assert_eq!(queue.len(), 3);

        queue.push_local(0, 10);
        assert_eq!(queue.len(), 4);

        queue.pop_local(0);
        assert_eq!(queue.len(), 3);
    }

    #[test]
    fn test_local_deque_wraparound() {
        // Test that indices wrap correctly
        let deque: LocalDeque<u64, 4> = LocalDeque::new();

        // Fill, then drain, then fill again to test wraparound
        for _ in 0..3 {
            assert!(deque.push_bottom(100));
            assert!(deque.push_bottom(200));
            assert!(deque.push_bottom(300));
            assert!(deque.push_bottom(400));

            assert_eq!(deque.pop_bottom(), Some(400));
            assert_eq!(deque.steal_top(), Some(100));
            assert_eq!(deque.pop_bottom(), Some(300));
            assert_eq!(deque.pop_bottom(), Some(200));
            assert!(deque.is_empty());
        }
    }
}
