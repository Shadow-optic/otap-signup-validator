//! MODQ Throughput Benchmark
//!
//! Measures the maximum sustained submit rate of the MODQ ring buffer.
//! Two modes:
//!   - `--loopback`: Uses shared memory with a simulated consumer (no FPGA needed)
//!   - `--pci <path>`: Uses real PCIe BAR (requires FPGA hardware)
//!
//! Usage:
//!   cargo run --release --bin modq_bench -- --loopback
//!   cargo run --release --bin modq_bench -- --pci /sys/bus/pci/devices/0000:01:00.0/resource2

use otap_modq::bar::{self, BarMapping};
use otap_modq::ring::SLOT_FREE;
use otap_modq::{ModqQueue, ModqConfig, TransportMode};

use std::env;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::Instant;

const SHM_NAME: &str = "/otap_modq_bench";
const RING_DEPTH: u32 = 1024;
const SLOT_SIZE: u32 = 4096;

fn main() {
    let args: Vec<String> = env::args().collect();

    let (transport, use_sim) = if args.iter().any(|a| a == "--loopback") {
        (
            TransportMode::Loopback { shm_name: SHM_NAME.to_string() },
            true,
        )
    } else if let Some(idx) = args.iter().position(|a| a == "--pci") {
        let path = args.get(idx + 1).expect("--pci requires a path argument");
        (
            TransportMode::PciBar { resource_path: path.into() },
            false,
        )
    } else {
        eprintln!("Usage: modq_bench --loopback | --pci <resource_path>");
        std::process::exit(1);
    };

    println!("╔══════════════════════════════════════════════════╗");
    println!("║         OTAP MODQ Throughput Benchmark           ║");
    println!("╚══════════════════════════════════════════════════╝");
    println!();

    let running = Arc::new(AtomicBool::new(true));

    // Start simulated consumer if in loopback mode
    let sim_thread = if use_sim {
        let r = running.clone();
        Some(thread::spawn(move || simulated_consumer(r)))
    } else {
        None
    };

    if use_sim {
        thread::sleep(std::time::Duration::from_millis(50));
    }

    let config = ModqConfig {
        transport,
        ring_depth: RING_DEPTH,
        slot_size: SLOT_SIZE,
    };

    let mut queue = ModqQueue::open(&config).expect("Failed to open MODQ queue");

    println!("Ring depth:     {} slots", queue.ring_depth());
    println!("Slot capacity:  {} bytes", queue.slot_payload_capacity());
    println!();

    // Benchmark with multiple payload sizes
    for &payload_size in &[64, 128, 256, 512, 1024, 4096] {
        if payload_size > queue.slot_payload_capacity() {
            continue;
        }

        let payload = vec![0xAA_u8; payload_size];
        let iterations: u64 = match payload_size {
            s if s <= 128 => 10_000_000,
            s if s <= 1024 => 5_000_000,
            _ => 1_000_000,
        };

        // Warm-up
        for _ in 0..1000 {
            queue.submit(&payload).expect("warmup submit failed");
        }

        // Timed run
        let start = Instant::now();
        for _ in 0..iterations {
            queue.submit(&payload).expect("submit failed");
        }
        let elapsed = start.elapsed();

        let total_bytes = iterations * payload_size as u64;
        let throughput_gbps = (total_bytes * 8) as f64 / elapsed.as_secs_f64() / 1e9;
        let msg_rate = iterations as f64 / elapsed.as_secs_f64();
        let avg_ns = elapsed.as_nanos() as f64 / iterations as f64;

        println!(
            "  {:>5} B payload │ {:>8.2} Gbps │ {:>7.2} M msg/s │ {:>7.1} ns/submit",
            payload_size, throughput_gbps, msg_rate / 1e6, avg_ns
        );
    }

    println!();
    println!("Note: Loopback throughput is bounded by memory bandwidth, not PCIe.");
    println!("On real hardware, PCIe Gen5 x16 sustains ~64 GB/s = ~512 Gbps.");

    // Shutdown
    running.store(false, Ordering::Release);
    if let Some(t) = sim_thread {
        t.join().expect("sim thread panicked");
    }

    if use_sim {
        unsafe {
            let c_name = std::ffi::CString::new(SHM_NAME).unwrap();
            libc::shm_unlink(c_name.as_ptr());
        }
    }
}

fn simulated_consumer(running: Arc<AtomicBool>) {
    let slot_stride = ((SLOT_SIZE as usize + 16 + 63) & !63) as usize;
    let total_size = bar::regs::RING_BASE + slot_stride * RING_DEPTH as usize;

    let bar = BarMapping::open_shm(SHM_NAME, total_size)
        .expect("sim consumer: failed to open shm");

    let mut head: u64 = 0;

    while running.load(Ordering::Relaxed) {
        let index = (head % RING_DEPTH as u64) as usize;
        let slot_base = bar::regs::RING_BASE + index * slot_stride;

        if bar.read32(slot_base) == otap_modq::ring::SLOT_READY {
            bar.write32(slot_base, SLOT_FREE);
            head += 1;
        } else {
            #[cfg(target_arch = "x86_64")]
            unsafe { std::arch::x86_64::_mm_pause(); }
            #[cfg(not(target_arch = "x86_64"))]
            std::hint::spin_loop();
        }
    }
}
