//! Criterion micro-benchmarks for the OTAP ring-buffer stack.
//!
//! Each benchmark group isolates one operation with proper warmup and
//! statistical analysis (confidence intervals, outlier detection).
//!
//! Groups:
//!   ring/submit_drain_roundtrip  — pure submit + immediate drain (no thread)
//!   ring/try_submit_empty        — submit into an empty ring
//!   ring/try_drain_full          — drain from a pre-filled ring
//!   batch/submit_N               — batch submit, N = 1,4,16,64
//!   batch/drain_N                — batch drain, N = 1,4,16,64
//!   mpmc/steal_Kworkers          — WorkStealingQueue full-cycle, K = 1,2,4
//!   adaptive/submit_decomposed   — AdaptiveBackpressure submit_decomposed (zero-wait)

use criterion::{criterion_group, criterion_main, BatchSize, BenchmarkId, Criterion, Throughput};
use otap_bench::adaptive::{AdaptiveBackpressure, AdaptiveConfig};
use otap_bench::{BatchRingBuffer, RingBuffer, WorkStealingQueue};
use std::hint::black_box;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Barrier};
use std::thread;

// ─────────────────────────────────────────────────────────────────────────────
// 1. Ring buffer — round-trip (submit then drain, single thread)
// ─────────────────────────────────────────────────────────────────────────────
fn ring_roundtrip(c: &mut Criterion) {
    let mut group = c.benchmark_group("ring");
    group.throughput(Throughput::Elements(1));

    let ring: RingBuffer<u64, 4096> = RingBuffer::new();

    group.bench_function("submit_drain_roundtrip", |b| {
        b.iter(|| {
            black_box(ring.try_submit(black_box(0xDEAD_BEEF_u64)));
            black_box(ring.try_drain());
        });
    });

    group.finish();
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Ring buffer — isolated submit (ring always has room)
// ─────────────────────────────────────────────────────────────────────────────
fn ring_submit_empty(c: &mut Criterion) {
    let mut group = c.benchmark_group("ring");
    group.throughput(Throughput::Elements(1));

    group.bench_function("try_submit_empty", |b| {
        // Use iter_batched: refill the ring state between batches
        b.iter_batched(
            || {
                let ring: RingBuffer<u64, 4096> = RingBuffer::new();
                ring
            },
            |ring| {
                // Submit one item into an empty ring
                black_box(ring.try_submit(black_box(42_u64)));
                ring // keep ring alive so it's not dropped mid-bench
            },
            BatchSize::SmallInput,
        );
    });

    group.finish();
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Ring buffer — isolated drain (ring always has items)
// ─────────────────────────────────────────────────────────────────────────────
fn ring_drain_full(c: &mut Criterion) {
    let mut group = c.benchmark_group("ring");
    group.throughput(Throughput::Elements(1));

    group.bench_function("try_drain_full", |b| {
        b.iter_batched(
            || {
                let ring: RingBuffer<u64, 4096> = RingBuffer::new();
                // Pre-fill ring to 50% capacity
                for i in 0..2048_u64 {
                    ring.try_submit(i);
                }
                ring
            },
            |ring| {
                black_box(ring.try_drain());
                ring
            },
            BatchSize::SmallInput,
        );
    });

    group.finish();
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Batch operations — submit and drain at various batch sizes
// ─────────────────────────────────────────────────────────────────────────────
fn batch_ops(c: &mut Criterion) {
    let mut group = c.benchmark_group("batch");

    for &bs in &[1usize, 4, 16, 64] {
        group.throughput(Throughput::Elements(bs as u64));

        group.bench_with_input(BenchmarkId::new("submit_batch", bs), &bs, |b, &batch_size| {
            let items: Vec<u64> = (0..batch_size as u64).collect();
            b.iter_batched(
                || {
                    // Always start with an empty ring so there's room
                    let ring: BatchRingBuffer<u64, 4096> = BatchRingBuffer::new(batch_size);
                    ring
                },
                |ring| {
                    let n = ring.submit_batch(black_box(&items));
                    black_box(n);
                    ring
                },
                BatchSize::SmallInput,
            );
        });

        group.bench_with_input(BenchmarkId::new("drain_batch", bs), &bs, |b, &batch_size| {
            let mut out = vec![0u64; batch_size];
            b.iter_batched(
                || {
                    let ring: BatchRingBuffer<u64, 4096> = BatchRingBuffer::new(batch_size);
                    // Pre-fill to ensure items are available
                    let fill: Vec<u64> = (0..batch_size as u64).collect();
                    ring.submit_batch(&fill);
                    ring
                },
                |ring| {
                    let n = ring.drain_batch(black_box(&mut out));
                    black_box(n);
                    ring
                },
                BatchSize::SmallInput,
            );
        });
    }

    group.finish();
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. MPMC work-stealing — throughput per worker count
// ─────────────────────────────────────────────────────────────────────────────
fn mpmc_steal(c: &mut Criterion) {
    let mut group = c.benchmark_group("mpmc");

    for &nw in &[1usize, 2, 4] {
        group.throughput(Throughput::Elements(1000)); // 1000 items per bench iteration

        group.bench_with_input(
            BenchmarkId::new("steal_Nworkers", nw),
            &nw,
            |b, &num_workers| {
                b.iter_batched(
                    || {
                        let q: Arc<WorkStealingQueue<u64>> =
                            Arc::new(WorkStealingQueue::new(num_workers));
                        // Pre-load 1000 items so workers have something to steal
                        for i in 0..1000_u64 {
                            q.submit(i);
                        }
                        q
                    },
                    |q| {
                        let done = Arc::new(AtomicBool::new(false));
                        let barrier = Arc::new(Barrier::new(num_workers + 1));
                        let mut handles = vec![];

                        for wid in 0..num_workers {
                            let q2 = q.clone();
                            let d2 = done.clone();
                            let b2 = barrier.clone();
                            handles.push(thread::spawn(move || {
                                b2.wait();
                                let mut count = 0u64;
                                loop {
                                    match q2.steal(wid) {
                                        Some(_) => count += 1,
                                        None => {
                                            if d2.load(Ordering::Acquire) && q2.is_empty() {
                                                break;
                                            }
                                        }
                                    }
                                }
                                count
                            }));
                        }

                        barrier.wait();
                        done.store(true, Ordering::Release);

                        let total: u64 = handles.into_iter().map(|h| h.join().unwrap()).sum();
                        black_box(total);
                    },
                    BatchSize::SmallInput,
                );
            },
        );
    }

    group.finish();
}

// ─────────────────────────────────────────────────────────────────────────────
// 6. Adaptive backpressure — submit_decomposed with zero-contention ring
// ─────────────────────────────────────────────────────────────────────────────
fn adaptive_submit(c: &mut Criterion) {
    let mut group = c.benchmark_group("adaptive");
    group.throughput(Throughput::Elements(1));

    let ring: Arc<RingBuffer<u64, 65536>> = Arc::new(RingBuffer::new());

    // Consumer keeps ring empty so submit_decomposed never waits
    let shutdown = Arc::new(AtomicBool::new(false));
    let r_c = ring.clone();
    let s_c = shutdown.clone();
    thread::spawn(move || {
        while !s_c.load(Ordering::Relaxed) {
            while r_c.try_drain().is_some() {}
            thread::yield_now();
        }
    });

    let adaptive = AdaptiveBackpressure::new(AdaptiveConfig {
        spin_limit: 1000,
        yield_limit: 100,
        sleep_nanos: 1,
    });

    group.bench_function("submit_decomposed_zero_wait", |b| {
        b.iter(|| {
            let lat =
                adaptive.submit_decomposed(|item| ring.try_submit(item), black_box(99_u64));
            black_box(lat);
        });
    });

    shutdown.store(true, Ordering::Relaxed);
    group.finish();
}

// ─────────────────────────────────────────────────────────────────────────────
criterion_group!(
    benches,
    ring_roundtrip,
    ring_submit_empty,
    ring_drain_full,
    batch_ops,
    mpmc_steal,
    adaptive_submit,
);
criterion_main!(benches);
