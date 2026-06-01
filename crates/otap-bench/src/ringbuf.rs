//! Lock-Free Single-Producer Single-Consumer (SPSC) Ring Buffer
//!
//! Core design principles:
//! - **Cache-line isolation**: Each slot is 64-byte aligned; producer and consumer
//!   cursors reside on separate cache lines via [`CachePadded`].
//! - **Zero modulo operations**: Power-of-2 sizing with mask-based indexing.
//! - **Minimal memory ordering**: `Relaxed` for data within a slot (each side owns
//!   its access); `Release`/`Acquire` for cursor publication.
//! - **Zero heap allocations after construction**.
//!
//! This module eliminates the backpressure artifact that caused a 160x performance
//! discrepancy in the original OTAP benchmark (documented 41 ns vs measured
//! ~6,600 ns). The discrepancy arose because a single-threaded consumer created
//! backpressure, causing producers to spin-wait on `SLOT_FREE`. This ring buffer
//! is the foundational data structure that, combined with batching and MPMC
//! work-stealing, removes that bottleneck.

use crossbeam_utils::CachePadded;
use std::mem::MaybeUninit;
use std::sync::atomic::{AtomicUsize, Ordering};

// ---------------------------------------------------------------------------
// Compile-time assertions
// ---------------------------------------------------------------------------

/// Assert that `N` is a power of two at compile time.
struct AssertPowerOf2<const N: usize>;
impl<const N: usize> AssertPowerOf2<N> {
    const OK: () = assert!(
        N.is_power_of_two(),
        "RingBuffer capacity N must be a power of 2"
    );
}

/// Assert that `T` fits inside a single cache line (minus state overhead).
struct AssertFitsCacheLine<T>(std::marker::PhantomData<T>);
impl<T> AssertFitsCacheLine<T> {
    const OK: () = assert!(
        std::mem::size_of::<T>() <= 56,
        "T too large for cache-line-sized slot (max 56 bytes)"
    );
}

// ---------------------------------------------------------------------------
// Slot state
// ---------------------------------------------------------------------------

/// Slot lifecycle state.
///
/// ```text
/// Free ──(producer writes item)──> Full ──(consumer reads item)──> Free ...
/// ```
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(u8)]
pub enum SlotState {
    /// Slot is ready for the producer to write into.
    Free = 0,
    /// Slot contains a valid item ready for the consumer to read.
    Full = 1,
}

// ---------------------------------------------------------------------------
// Slot
// ---------------------------------------------------------------------------

/// A single ring-buffer slot, sized to exactly one 64-byte cache line.
///
/// The layout is:
/// ```text
/// |  item: MaybeUninit<T>  |  state: AtomicUsize  |  (implicit pad to 64)  |
/// |<--- size_of<T>() --->  |<--- 8 bytes --->     | rest                 |
/// |<-------------------- 64 bytes total ------------------->|
/// ```
///
/// `#[repr(C, align(64))]` gives predictable field ordering and guarantees:
/// - The struct starts on a 64-byte boundary (cache-line aligned).
/// - The struct size is always a multiple of 64 bytes, so adjacent slots in an
///   array never share a cache line — the compiler inserts tail padding as needed.
#[repr(C, align(64))]
pub struct Slot<T> {
    /// Item storage — uninitialized until `state` transitions to [`SlotState::Full`].
    pub item: MaybeUninit<T>,
    /// Current state of this slot: [`SlotState::Free`] (0) or [`SlotState::Full`] (1).
    pub state: AtomicUsize,
}

impl<T> Slot<T> {
    /// Create a new slot in the [`SlotState::Free`] state.
    fn new() -> Self {
        Slot {
            item: MaybeUninit::uninit(),
            state: AtomicUsize::new(SlotState::Free as usize),
        }
    }
}

// Verify at compile time that Slot<T> is exactly 64 bytes for reasonably-sized T.
const _: () = assert!(
    std::mem::size_of::<Slot<u64>>() == 64,
    "Slot<u64> must be exactly 64 bytes"
);

// ---------------------------------------------------------------------------
// RingBuffer
// ---------------------------------------------------------------------------

/// Lock-free single-producer single-consumer ring buffer.
///
/// # Type Parameters
/// - `T`: The element type. Must be `Copy` so that items can be moved out of
///   [`MaybeUninit`] storage without drop glue.
/// - `N`: The total number of slots. Must be a power of two. Only `N - 1`
///   slots are usable so that "full" and "empty" can be distinguished.
///
/// # Cache Layout
/// ```text
/// |  cache line 0  |  cache line 1  |  cache line 2  |  ...  |
/// |  p_cursor      |  c_cursor      |  slots[0]      |  ...  |
/// ```
///
/// # Safety
/// `RingBuffer` is `Send + Sync` **only** when the single-producer /
/// single-consumer discipline is respected:
/// - Exactly one thread calls [`try_submit`] (and no other methods).
/// - Exactly one thread calls [`try_drain`] (and no other methods).
pub struct RingBuffer<T, const N: usize> {
    /// Producer cursor — **only** written by the producer thread.
    ///
    /// Incremented with [`Ordering::Release`] after a slot is filled.
    /// Loaded with [`Ordering::Acquire`] by the consumer to observe progress.
    pub p_cursor: CachePadded<AtomicUsize>,
    /// Consumer cursor — **only** written by the consumer thread.
    ///
    /// Incremented with [`Ordering::Release`] after a slot is emptied.
    /// Loaded with [`Ordering::Acquire`] by the producer to observe progress.
    pub c_cursor: CachePadded<AtomicUsize>,
    /// Slot storage — heap-allocated array of exactly `N` cache-line-padded slots.
    pub slots: Box<[Slot<T>; N]>,
}

impl<T: Copy, const N: usize> RingBuffer<T, N> {
    /// Create a new ring buffer. All slots start in the [`SlotState::Free`] state.
    ///
    /// # Panics
    /// - If `N` is not a power of two (compile-time or runtime check).
    /// - If `size_of::<T>()` exceeds 56 bytes (item too large for cache-line slot).
    pub fn new() -> Self {
        // Trigger compile-time assertions.
        let _ = AssertPowerOf2::<N>::OK;
        let _ = AssertFitsCacheLine::<T>::OK;

        // Runtime guards (redundant with const asserts, but provide clear messages
        // when the const generics are supplied from non-const contexts).
        assert!(N.is_power_of_two(), "RingBuffer capacity N must be a power of 2");
        assert!(
            std::mem::size_of::<T>() <= 56,
            "T ({} bytes) too large for cache-line-sized slot (max 56 bytes)",
            std::mem::size_of::<T>()
        );
        assert_eq!(
            std::mem::size_of::<Slot<T>>(),
            64,
            "Slot<T> must be exactly 64 bytes, got {} bytes",
            std::mem::size_of::<Slot<T>>()
        );

        // Heap-allocate N slots without touching the stack.
        // Box::new([T; N]) would create the array on the stack first,
        // then move it to the heap — causing stack overflow for large N.
        let mut v = Vec::with_capacity(N);
        for _ in 0..N {
            v.push(Slot::new());
        }
        let boxed_slice: Box<[Slot<T>]> = v.into_boxed_slice();
        // SAFETY: boxed_slice has length N, identical layout to [Slot<T>; N].
        let ptr = Box::into_raw(boxed_slice);
        let slots = unsafe { Box::from_raw(ptr as *mut [Slot<T>; N]) };

        RingBuffer {
            p_cursor: CachePadded::new(AtomicUsize::new(0)),
            c_cursor: CachePadded::new(AtomicUsize::new(0)),
            slots,
        }
    }

    /// Try to submit an item into the ring.
    ///
    /// Returns `true` if the item was successfully enqueued, `false` if the
    /// ring is full. This method **never blocks**.
    ///
    /// # Sequence
    /// 1. Load producer cursor (Relaxed — own data).
    /// 2. Load consumer cursor (Acquire — observe consumer progress).
    /// 3. Check full: `p.wrapping_sub(c) >= N`.
    /// 4. Write item into the selected slot (Relaxed — exclusive access).
    /// 5. Mark slot [`SlotState::Full`] (Relaxed — still before publication).
    /// 6. Increment producer cursor with [`Ordering::Release`] to publish.
    ///
    /// # Thread safety
    /// Must be called from **exactly one** producer thread.
    pub fn try_submit(&self, item: T) -> bool {
        let p = self.p_cursor.load(Ordering::Relaxed);
        let c = self.c_cursor.load(Ordering::Acquire);

        // Full when the distance between cursors reaches capacity (N - 1).
        // We reserve one slot so that full and empty are distinguishable:
        // empty == p == c, full == p == c + (N - 1) items in the ring.
        if p.wrapping_sub(c) >= N - 1 {
            return false;
        }

        let slot = &self.slots[p & (N - 1)];

        // Write item — we have exclusive access to this slot until we publish
        // the updated producer cursor. We use a raw pointer write because we
        // only hold &self (the producer has exclusive logical access to this
        // particular slot by cursor ownership, not by Rust borrowing rules).
        unsafe {
            std::ptr::write(slot.item.as_ptr() as *mut T, item);
        }

        // Mark slot as full (ordering is Relaxed because the Release store
        // to p_cursor below provides the necessary synchronization).
        slot.state.store(SlotState::Full as usize, Ordering::Relaxed);

        // Publish: the Release ordering guarantees the consumer sees the item
        // write and state transition when it Acquire-loads p_cursor.
        self.p_cursor.store(p.wrapping_add(1), Ordering::Release);

        true
    }

    /// Try to drain an item from the ring.
    ///
    /// Returns `Some(T)` if an item was available, `None` if the ring is empty.
    /// This method **never blocks**.
    ///
    /// # Sequence
    /// 1. Load consumer cursor (Relaxed — own data).
    /// 2. Load producer cursor (Acquire — observe producer progress).
    /// 3. Check empty: `p == c`.
    /// 4. Read item from the selected slot (Relaxed — exclusive access after cursor check).
    /// 5. Mark slot [`SlotState::Free`] (Relaxed — before publication).
    /// 6. Increment consumer cursor with [`Ordering::Release`] to publish.
    ///
    /// # Thread safety
    /// Must be called from **exactly one** consumer thread.
    pub fn try_drain(&self) -> Option<T> {
        let c = self.c_cursor.load(Ordering::Relaxed);
        let p = self.p_cursor.load(Ordering::Acquire);

        // Empty when both cursors are equal.
        if p == c {
            return None;
        }

        let slot = &self.slots[c & (N - 1)];

        // Read the item. The Acquire load of p_cursor above guarantees we see
        // the producer's writes to this slot.
        let item = unsafe { slot.item.assume_init_read() };

        // Mark slot as free. Relaxed is sufficient because the Release store
        // to c_cursor below provides the synchronization.
        slot.state.store(SlotState::Free as usize, Ordering::Relaxed);

        // Publish: the Release ordering guarantees the producer sees the slot
        // is free when it Acquire-loads c_cursor.
        self.c_cursor.store(c.wrapping_add(1), Ordering::Release);

        Some(item)
    }

    /// Estimated number of slots available for the producer.
    ///
    /// This is a snapshot that may race with the consumer; the true value
    /// can only decrease by the time the caller acts on it.
    pub fn available(&self) -> usize {
        let p = self.p_cursor.load(Ordering::Relaxed);
        let c = self.c_cursor.load(Ordering::Acquire);
        // Usable capacity is N - 1 (one slot reserved to distinguish full from empty).
        (N - 1).saturating_sub(p.wrapping_sub(c))
    }

    /// Estimated number of items readable by the consumer.
    ///
    /// This is a snapshot that may race with the producer; the true value
    /// can only decrease by the time the caller acts on it.
    pub fn readable(&self) -> usize {
        let p = self.p_cursor.load(Ordering::Acquire);
        let c = self.c_cursor.load(Ordering::Relaxed);
        p.wrapping_sub(c)
    }

    /// Usable capacity of the ring.
    ///
    /// Always `N - 1` because one slot is reserved to distinguish "full"
    /// from "empty".
    pub fn capacity(&self) -> usize {
        N - 1
    }
}

// Safety: RingBuffer is Send + Sync because access is partitioned by thread
// role. The producer exclusively owns p_cursor and the set of slots in the
// "free" region; the consumer exclusively owns c_cursor and the set of slots
// in the "full" region. Cursors are published with Release/Acquire, ensuring
// happens-before between slot writes and the opposing thread's observation.
unsafe impl<T: Send, const N: usize> Send for RingBuffer<T, N> {}
unsafe impl<T: Send, const N: usize> Sync for RingBuffer<T, N> {}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::thread;

    #[test]
    fn test_new_ring_is_empty() {
        let ring: RingBuffer<u64, 16> = RingBuffer::new();
        assert_eq!(ring.capacity(), 15);
        assert_eq!(ring.available(), 15);
        assert_eq!(ring.readable(), 0);
        assert!(ring.try_drain().is_none());
    }

    #[test]
    fn test_submit_and_drain_one() {
        let ring: RingBuffer<u64, 16> = RingBuffer::new();
        assert!(ring.try_submit(42));
        assert_eq!(ring.readable(), 1);
        assert_eq!(ring.available(), 14);
        assert_eq!(ring.try_drain(), Some(42));
        assert_eq!(ring.readable(), 0);
    }

    #[test]
    fn test_submit_full_ring() {
        let ring: RingBuffer<u64, 8> = RingBuffer::new();
        // Capacity is 7 usable slots
        for i in 0..7 {
            assert!(ring.try_submit(i), "failed to submit item {}", i);
        }
        assert_eq!(ring.available(), 0);
        assert!(!ring.try_submit(999)); // full
        assert_eq!(ring.readable(), 7);
    }

    #[test]
    fn test_drain_all_then_refill() {
        let ring: RingBuffer<u64, 8> = RingBuffer::new();
        for i in 0..7 {
            assert!(ring.try_submit(i + 10));
        }
        for i in 0..7 {
            assert_eq!(ring.try_drain(), Some(i + 10));
        }
        assert!(ring.try_drain().is_none());
        // Refill
        for i in 0..7 {
            assert!(ring.try_submit(i + 20));
        }
        for i in 0..7 {
            assert_eq!(ring.try_drain(), Some(i + 20));
        }
    }

    #[test]
    fn test_interleaved_submit_drain() {
        let ring: RingBuffer<u64, 8> = RingBuffer::new();
        for i in 0..20 {
            assert!(ring.try_submit(i));
            assert_eq!(ring.try_drain(), Some(i));
        }
    }

    #[test]
    fn test_slot_size_is_64_bytes() {
        assert_eq!(std::mem::size_of::<Slot<u64>>(), 64);
        assert_eq!(std::mem::size_of::<Slot<u32>>(), 64);
        assert_eq!(std::mem::align_of::<Slot<u64>>(), 64);
    }

    #[test]
    fn test_cursor_cache_line_separation() {
        // p_cursor and c_cursor are each CachePadded<AtomicUsize>, so each
        // occupies a full cache line. They should not share a cache line.
        let ring: RingBuffer<u64, 16> = RingBuffer::new();
        let p_addr = &*ring.p_cursor as *const AtomicUsize as usize;
        let c_addr = &*ring.c_cursor as *const AtomicUsize as usize;
        // Each CachePadded is 64 bytes, so they should be at least 64 bytes apart.
        assert!(c_addr.wrapping_sub(p_addr) >= 64, "cursors may share a cache line");
    }

    #[test]
    fn test_spsc_threaded() {
        // Leak the ring buffer to get a 'static reference for the threads.
        let ring: &'static RingBuffer<u64, 1024> = Box::leak(Box::new(RingBuffer::new()));

        let consumer_handle = thread::spawn(move || {
            let mut received = Vec::new();
            while received.len() < 1000 {
                if let Some(v) = ring.try_drain() {
                    received.push(v);
                }
            }
            received
        });

        let producer_handle = thread::spawn(move || {
            for i in 0..1000 {
                while !ring.try_submit(i) {
                    thread::yield_now();
                }
            }
        });

        producer_handle.join().unwrap();
        let received = consumer_handle.join().unwrap();
        assert_eq!(received.len(), 1000);
        for (i, &v) in received.iter().enumerate() {
            assert_eq!(v, i as u64);
        }
    }

    #[test]
    fn test_capacity_returns_n_minus_one() {
        let ring: RingBuffer<u8, 4> = RingBuffer::new();
        assert_eq!(ring.capacity(), 3);
    }

    #[test]
    fn test_empty_after_construction() {
        let ring: RingBuffer<u64, 1024> = RingBuffer::new();
        assert_eq!(ring.readable(), 0);
        assert_eq!(ring.available(), 1023);
    }

    #[test]
    fn test_wrapping_cursors() {
        // Small ring to force cursor wrap-around quickly
        let ring: RingBuffer<u64, 4> = RingBuffer::new();
        // Fill, drain, repeat many times to exercise wrapping arithmetic
        for round in 0..100 {
            for i in 0..3 {
                assert!(ring.try_submit(round * 10 + i));
            }
            for i in 0..3 {
                assert_eq!(ring.try_drain(), Some(round * 10 + i));
            }
        }
    }

    #[test]
    fn test_submit_to_drain_order_preserved() {
        let ring: RingBuffer<u64, 16> = RingBuffer::new();
        for i in 0..10 {
            assert!(ring.try_submit(i * 7 + 3));
        }
        for i in 0..10 {
            assert_eq!(ring.try_drain(), Some(i * 7 + 3));
        }
    }
}
