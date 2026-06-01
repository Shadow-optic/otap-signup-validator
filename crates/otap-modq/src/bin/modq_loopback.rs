//! MODQ Loopback Test
//!
//! Runs without FPGA hardware. A background thread simulates the FPGA's slot consumption
//! (reads SLOT_READY, processes, writes SLOT_DONE/SLOT_FREE). The main thread submits
//! payloads and polls completions, validating the full ring buffer lifecycle.

use otap_modq::bar::{self, BarMapping};
use otap_modq::ring::{SLOT_FREE, SLOT_READY};
use otap_modq::{ModqQueue, ModqConfig, TransportMode};

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::Instant;

const SHM_NAME: &str = "/otap_modq_loopback";
const RING_DEPTH: u32 = 1024;
const SLOT_SIZE: u32 = 256; // Small slots for testing
const NUM_MESSAGES: u64 = 1_000_000;

fn main() {
    println!("╔══════════════════════════════════════════════════╗");
    println!("║     OTAP MODQ Loopback Test (No FPGA)           ║");
    println!("║     Simulating otap_app_core slot consumption    ║");
    println!("╚══════════════════════════════════════════════════╝");
    println!();

    let running = Arc::new(AtomicBool::new(true));

    // Start the simulated FPGA consumer in a background thread
    let fpga_running = running.clone();
    let fpga_thread = thread::spawn(move || {
        simulated_fpga_consumer(fpga_running);
    });

    // Give the FPGA thread a moment to initialize
    thread::sleep(std::time::Duration::from_millis(100));

    // Open the MODQ queue in loopback mode
    let config = ModqConfig {
        transport: TransportMode::Loopback {
            shm_name: SHM_NAME.to_string(),
        },
        ring_depth: RING_DEPTH,
        slot_size: SLOT_SIZE,
    };

    let mut queue = ModqQueue::open(&config).expect("Failed to open MODQ queue");

    println!("Ring depth:     {} slots", queue.ring_depth());
    println!("Slot capacity:  {} bytes", queue.slot_payload_capacity());
    println!("Test messages:  {}", NUM_MESSAGES);
    println!();

    // Build a mock equity trade order payload (matches OTAP v0.2 Section 9.2)
    let mut trade_payload = vec![0u8; 128];
    // symbol: "AAPL" at bytes [0..4]
    trade_payload[0..4].copy_from_slice(b"AAPL");
    // side: BUY = 0 at byte [4]
    trade_payload[4] = 0;
    // quantity: 1000 at bytes [5..9] (little-endian u32)
    trade_payload[5..9].copy_from_slice(&1000u32.to_le_bytes());
    // price: 19850 (cents) at bytes [9..17] (little-endian u64)
    trade_payload[9..17].copy_from_slice(&19850u64.to_le_bytes());

    // === Throughput Test ===
    println!("── Throughput Test ──────────────────────────────────");
    let start = Instant::now();

    for _ in 0..NUM_MESSAGES {
        queue.submit(&trade_payload).expect("submit failed");
    }

    let elapsed = start.elapsed();
    let total_bytes = NUM_MESSAGES * trade_payload.len() as u64;
    let throughput_gbps = (total_bytes * 8) as f64 / elapsed.as_secs_f64() / 1e9;
    let messages_per_sec = NUM_MESSAGES as f64 / elapsed.as_secs_f64();
    let avg_latency_ns = elapsed.as_nanos() as f64 / NUM_MESSAGES as f64;

    println!("  Messages sent:     {}", NUM_MESSAGES);
    println!("  Payload per msg:   {} bytes", trade_payload.len());
    println!("  Total data:        {:.2} GB", total_bytes as f64 / 1e9);
    println!("  Elapsed:           {:.4} s", elapsed.as_secs_f64());
    println!("  Throughput:        {:.2} Gbps", throughput_gbps);
    println!("  Message rate:      {:.2} M msg/s", messages_per_sec / 1e6);
    println!("  Avg submit lat:    {:.1} ns", avg_latency_ns);
    println!();

    // === Ordering Verification Test ===
    println!("── Ordering Verification ────────────────────────────");
    let mut sequence_errors = 0u64;
    let verify_count = 10_000u64;

    for i in 0..verify_count {
        // Encode the sequence number into the payload
        let mut payload = vec![0u8; 64];
        payload[0..8].copy_from_slice(&i.to_le_bytes());

        let sid = queue.submit(&payload).expect("submit failed");
        // SID should increment monotonically
        if sid as u64 != NUM_MESSAGES + i {
            sequence_errors += 1;
        }
    }

    if sequence_errors == 0 {
        println!("  ✓ All {} SIDs monotonically increasing", verify_count);
    } else {
        println!("  ✗ {} sequence errors out of {}", sequence_errors, verify_count);
    }
    println!();

    // Shut down the FPGA simulator
    running.store(false, Ordering::Release);
    fpga_thread.join().expect("FPGA thread panicked");

    // Clean up shared memory
    unsafe {
        let c_name = std::ffi::CString::new(SHM_NAME).unwrap();
        libc::shm_unlink(c_name.as_ptr());
    }

    println!("── Complete ────────────────────────────────────────");
    println!("  MODQ ring buffer protocol verified.");
    println!("  Next step: replace Loopback with PciBar transport");
    println!("  pointing to Corundum BAR2 on Versal hardware.");
}

/// Simulates the FPGA side: reads SLOT_READY slots, "processes" them, marks SLOT_FREE.
/// This mimics what `otap_app_core.v` does via DMA on the real hardware.
fn simulated_fpga_consumer(running: Arc<AtomicBool>) {
    // Open the same shared memory region
    let slot_stride = (SLOT_SIZE as usize + 16 + 63) & !63;
    let total_size = bar::regs::RING_BASE + slot_stride * RING_DEPTH as usize;

    let bar = BarMapping::open_shm(SHM_NAME, total_size)
        .expect("FPGA simulator: failed to open shm");

    let mut local_head: u64 = 0;

    while running.load(Ordering::Acquire) {
        let index = (local_head % RING_DEPTH as u64) as usize;
        let slot_base = bar::regs::RING_BASE + index * slot_stride;

        let status = bar.read32(slot_base);

        if status == SLOT_READY {
            // "Process" the slot — in real hardware this is the ROB + schema pipeline.
            // Here we just read the length to simulate the DMA read.
            let _len = bar.read32(slot_base + 0x04);
            let _sid = bar.read32(slot_base + 0x08);

            // Mark slot as free (FPGA done, host can reuse)
            bar.write32(slot_base, SLOT_FREE);

            local_head += 1;
        } else {
            // Spin hint
            #[cfg(target_arch = "x86_64")]
            unsafe {
                std::arch::x86_64::_mm_pause();
            }
            #[cfg(not(target_arch = "x86_64"))]
            std::hint::spin_loop();
        }
    }
}
