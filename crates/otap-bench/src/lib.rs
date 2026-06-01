//! OTAP Corrected Benchmark Harness
//!
//! This crate provides a statistically rigorous, artifact-free benchmarking infrastructure
//! for the OTAP (Optical Transient Application Protocol) system.
//!
//! ## The Problem
//!
//! The original OTAP benchmark reported 41 ns P50 latency and 199 Gbps throughput.
//! Real measurements showed ~6,600–6,850 ns per submit (~0.15 M msg/s) — a 160×
//! discrepancy. The root cause: a single-threaded consumer created backpressure,
//! causing the producer to spin-wait on `SLOT_FREE`. The measured time was dominated
//! by backpressure wait, NOT the true cost of the ring write.
//!
//! ## Five Novel Innovations
//!
//! 1. **Batch Operations** (`batch`): Amortize sync cost across N messages with
//!    single cursor update for the entire batch.
//!
//! 2. **Adaptive Backpressure** (`adaptive`): Three-tier strategy
//!    (spin → yield → nanosleep) with latency decomposition separating
//!    true write cost from wait time.
//!
//! 3. **MPMC Work-Stealing** (`mpmc`): Scale consumers to eliminate the
//!    single-consumer bottleneck. Each worker has a local deque + global steal.
//!
//! 4. **Cache-Line Optimization**: All slots padded to 64 bytes;
//!    producer/consumer cursors on separate cache lines.
//!
//! 5. **Decomposed Latency Measurement**: Separately report `write_time`
//!    (true ring cost) vs `wait_time` (backpressure artifact).
//!
//! ## Modules
//!
//! - `ringbuf`: Core lock-free SPSC ring buffer
//! - `batch`: Batch submit/drain operations
//! - `adaptive`: Adaptive backpressure with latency decomposition
//! - `mpmc`: Multi-producer multi-consumer work-stealing queue
//! - `bench`: Benchmark harness with HDR histograms

pub mod ringbuf;
pub mod batch;
pub mod adaptive;
pub mod mpmc;
pub mod bench;

/// Re-exports for convenience
pub use ringbuf::RingBuffer;
pub use batch::BatchRingBuffer;
pub use adaptive::AdaptiveBackpressure;
pub use mpmc::WorkStealingQueue;
pub use bench::{Benchmark, BenchmarkConfig, BenchmarkResult, MeasurementMode};
