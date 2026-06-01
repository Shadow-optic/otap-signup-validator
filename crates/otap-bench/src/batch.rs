//! Batch submit/drain operations for amortizing synchronization cost.
//!
//! # Novel Innovation #1: Batch Operations
//!
//! Instead of one atomic cursor update per message, `BatchRingBuffer` performs
//! a single cursor update for an entire batch of N messages. This amortizes
//! the synchronization overhead across the batch, dramatically improving
//! throughput at the cost of slightly higher per-batch latency.
//!
//! ## Example
//! ```
//! use otap_bench::batch::BatchRingBuffer;
//!
//! let batch: BatchRingBuffer<u64, 1024> = BatchRingBuffer::new(64);
//! let items: Vec<u64> = (0..100).collect();
//! let submitted = batch.submit_batch(&items);
//! assert_eq!(submitted, 100);
//! ```

use crate::ringbuf::RingBuffer;
use std::sync::atomic::Ordering;

/// Batch-capable ring buffer.
///
/// Wraps a [`RingBuffer`] and provides batch submit/drain operations that
/// amortize cursor update cost across multiple messages.
///
/// # Design
///
/// - `submit_batch`: Reads `p_cursor` once, computes the maximum contiguous
///   range that can be filled, writes all items with `core::ptr::write`,
///   then publishes with a **single** `AtomicUsize::store`.
/// - `drain_batch`: Reads `c_cursor` once, computes the maximum contiguous
///   range that can be read, reads all items with `core::ptr::read`,
///   then publishes with a **single** `AtomicUsize::store`.
/// - Wraparound: If the ring wraps mid-batch, the operation fills the tail,
///   wraps to the head, and continues — still with a single cursor update.
pub struct BatchRingBuffer<T: Copy, const N: usize> {
    inner: RingBuffer<T, N>,
    /// Target batch size. The batch operations will attempt to process
    /// up to this many items at once, but may process fewer if the ring
    /// state doesn't permit a full batch.
    batch_size: usize,
}

impl<T: Copy, const N: usize> BatchRingBuffer<T, N> {
    /// Create a new batch ring buffer with the specified target batch size.
    ///
    /// # Panics
    /// Panics if `batch_size` is 0 or exceeds the ring capacity.
    pub fn new(batch_size: usize) -> Self {
        assert!(batch_size > 0, "batch_size must be > 0");
        assert!(
            batch_size <= N - 1,
            "batch_size ({}) must not exceed ring capacity ({})",
            batch_size,
            N - 1
        );
        BatchRingBuffer {
            inner: RingBuffer::new(),
            batch_size,
        }
    }

    /// Submit as many items from the slice as possible.
    ///
    /// Returns the count actually submitted (0..=items.len()). Uses a single
    /// cursor update for the entire batch, even when the ring wraps around.
    ///
    /// # Algorithm
    /// 1. Load `p_cursor` (Relaxed — producer owns this).
    /// 2. Load `c_cursor` (Acquire — observe consumer progress).
    /// 3. Compute available slots: `min(capacity - in_fight, batch_size, items.len())`.
    /// 4. Write items to slots (handling wraparound if needed).
    /// 5. Store updated `p_cursor` with Release to publish.
    pub fn submit_batch(&self, items: &[T]) -> usize {
        let p = self.inner.p_cursor.load(Ordering::Relaxed);
        let c = self.inner.c_cursor.load(Ordering::Acquire);

        // Compute available slots (N-1 usable, as in RingBuffer)
        let in_flight = p.wrapping_sub(c);
        let capacity = N - 1;
        let available = capacity.saturating_sub(in_flight);
        if available == 0 {
            return 0;
        }

        let to_submit = std::cmp::min(available, items.len());
        if to_submit == 0 {
            return 0;
        }

        let mask = N - 1;
        let start_idx = p & mask;

        // Check if we wrap around
        if start_idx + to_submit <= N {
            // No wrap — contiguous write
            for (i, &item) in items[..to_submit].iter().enumerate() {
                let slot = &self.inner.slots[(start_idx + i) & mask];
                unsafe {
                    std::ptr::write(slot.item.as_ptr() as *mut T, item);
                }
                slot.state.store(crate::ringbuf::SlotState::Full as usize, Ordering::Relaxed);
            }
        } else {
            // Wraparound: write tail, then head
            let tail_len = N - start_idx;
            // Tail portion
            for (i, &item) in items[..tail_len].iter().enumerate() {
                let slot = &self.inner.slots[(start_idx + i) & mask];
                unsafe {
                    std::ptr::write(slot.item.as_ptr() as *mut T, item);
                }
                slot.state.store(crate::ringbuf::SlotState::Full as usize, Ordering::Relaxed);
            }
            // Head portion
            for (i, &item) in items[tail_len..to_submit].iter().enumerate() {
                let slot = &self.inner.slots[i & mask];
                unsafe {
                    std::ptr::write(slot.item.as_ptr() as *mut T, item);
                }
                slot.state.store(crate::ringbuf::SlotState::Full as usize, Ordering::Relaxed);
            }
        }

        // Single cursor update publishes the entire batch
        self.inner.p_cursor.store(p.wrapping_add(to_submit), Ordering::Release);

        to_submit
    }

    /// Drain up to `out.len()` items (or up to `batch_size`) in one operation.
    ///
    /// Writes drained items into `out` slice starting at index 0.
    /// Returns count actually drained.
    ///
    /// # Algorithm
    /// 1. Load `c_cursor` (Relaxed — consumer owns this).
    /// 2. Load `p_cursor` (Acquire — observe producer progress).
    /// 3. Compute readable slots: `min(in_flight, batch_size, out.len())`.
    /// 4. Read items from slots (handling wraparound if needed).
    /// 5. Store updated `c_cursor` with Release to publish.
    pub fn drain_batch(&self, out: &mut [T]) -> usize {
        let c = self.inner.c_cursor.load(Ordering::Relaxed);
        let p = self.inner.p_cursor.load(Ordering::Acquire);

        let in_flight = p.wrapping_sub(c);
        if in_flight == 0 {
            return 0;
        }

        let to_drain = std::cmp::min(in_flight, out.len());
        if to_drain == 0 {
            return 0;
        }

        let mask = N - 1;
        let start_idx = c & mask;

        if start_idx + to_drain <= N {
            // No wrap — contiguous read
            for i in 0..to_drain {
                let slot = &self.inner.slots[(start_idx + i) & mask];
                out[i] = unsafe { slot.item.assume_init_read() };
                slot.state.store(crate::ringbuf::SlotState::Free as usize, Ordering::Relaxed);
            }
        } else {
            // Wraparound: read tail, then head
            let tail_len = N - start_idx;
            // Tail portion
            for i in 0..tail_len {
                let slot = &self.inner.slots[(start_idx + i) & mask];
                out[i] = unsafe { slot.item.assume_init_read() };
                slot.state.store(crate::ringbuf::SlotState::Free as usize, Ordering::Relaxed);
            }
            // Head portion
            for i in tail_len..to_drain {
                let slot = &self.inner.slots[(i - tail_len) & mask];
                out[i] = unsafe { slot.item.assume_init_read() };
                slot.state.store(crate::ringbuf::SlotState::Free as usize, Ordering::Relaxed);
            }
        }

        // Single cursor update publishes the entire drain
        self.inner.c_cursor.store(c.wrapping_add(to_drain), Ordering::Release);

        to_drain
    }

    /// Single-slot submit (delegates to inner ring buffer).
    pub fn try_submit(&self, item: T) -> bool {
        self.inner.try_submit(item)
    }

    /// Single-slot drain (delegates to inner ring buffer).
    pub fn try_drain(&self) -> Option<T> {
        self.inner.try_drain()
    }

    /// Ring capacity (usable slots).
    pub fn capacity(&self) -> usize {
        self.inner.capacity()
    }

    /// Available slots for producer.
    pub fn available(&self) -> usize {
        self.inner.available()
    }

    /// Readable slots for consumer.
    pub fn readable(&self) -> usize {
        self.inner.readable()
    }
}

// Safety: BatchRingBuffer delegates to RingBuffer which is Send + Sync
unsafe impl<T: Send + Copy, const N: usize> Send for BatchRingBuffer<T, N> {}
unsafe impl<T: Send + Copy, const N: usize> Sync for BatchRingBuffer<T, N> {}
