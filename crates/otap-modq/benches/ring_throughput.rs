//! Criterion benchmarks for MODQ ring buffer submit latency.

use criterion::{criterion_group, criterion_main, Criterion, Throughput, BenchmarkId};
use otap_modq::{ModqQueue, ModqConfig, TransportMode};
use otap_modq::bar::{self, BarMapping};
use otap_modq::ring::{SLOT_READY, SLOT_FREE};

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;

const SHM_NAME: &str = "/otap_modq_criterion";
const RING_DEPTH: u32 = 1024;
const SLOT_SIZE: u32 = 4096;

fn start_consumer() -> (Arc<AtomicBool>, thread::JoinHandle<()>) {
    let running = Arc::new(AtomicBool::new(true));
    let r = running.clone();

    let handle = thread::spawn(move || {
        let slot_stride = ((SLOT_SIZE as usize + 16 + 63) & !63) as usize;
        let total_size = bar::regs::RING_BASE + slot_stride * RING_DEPTH as usize;
        let bar = BarMapping::open_shm(SHM_NAME, total_size).unwrap();
        let mut head: u64 = 0;

        while r.load(Ordering::Relaxed) {
            let index = (head % RING_DEPTH as u64) as usize;
            let slot_base = bar::regs::RING_BASE + index * slot_stride;

            if bar.read32(slot_base) == SLOT_READY {
                bar.write32(slot_base, SLOT_FREE);
                head += 1;
            } else {
                std::hint::spin_loop();
            }
        }
    });

    // Let consumer initialize
    thread::sleep(std::time::Duration::from_millis(20));

    (running, handle)
}

fn bench_submit(c: &mut Criterion) {
    let (running, handle) = start_consumer();

    let config = ModqConfig {
        transport: TransportMode::Loopback { shm_name: SHM_NAME.to_string() },
        ring_depth: RING_DEPTH,
        slot_size: SLOT_SIZE,
    };

    let mut queue = ModqQueue::open(&config).unwrap();

    let mut group = c.benchmark_group("modq_submit");

    for size in [64, 128, 256, 1024] {
        let payload = vec![0xBB_u8; size];

        group.throughput(Throughput::Bytes(size as u64));
        group.bench_with_input(
            BenchmarkId::from_parameter(format!("{}B", size)),
            &payload,
            |b, p| {
                b.iter(|| {
                    queue.submit(p).unwrap();
                });
            },
        );
    }

    group.finish();

    running.store(false, Ordering::Release);
    handle.join().unwrap();

    if let Err(e) = BarMapping::unlink_shm(SHM_NAME) {
        eprintln!("Warning: failed to unlink shm: {}", e);
    }
}

criterion_group!(benches, bench_submit);
criterion_main!(benches);
