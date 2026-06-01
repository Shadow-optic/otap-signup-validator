//! OTAP MODQ Loopback Demo
//!
//! Exercises the MODQ zero-copy ring buffer path using shared-memory loopback
//! (no FPGA hardware required). Submits OTAP schema payloads through the same
//! ring protocol the FPGA DMA engine reads.
//!
//! This demo proves:
//! 1. otap-modq compiles and links within the otap workspace
//! 2. The ModqQueue submit/poll lifecycle works end-to-end
//! 3. Schema payloads from otap-schema flow through the ring correctly
//!
//! Run: `cargo run --release -p otap-cli --bin otap-modq-loopback`

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::Instant;

use anyhow::Result;
use otap_modq::{ModqQueue, ModqConfig, TransportMode};
use otap_modq::bar::{self, BarMapping};
use otap_modq::ring::{SLOT_FREE, SLOT_READY};
use otap_schema::{Schema, EquityTradeOrder, MarketTick, Heartbeat};
use otap_schema::equity_order::Side;

const SHM_NAME: &str = "/otap_modq_demo";
const RING_DEPTH: u32 = 1024;
const SLOT_SIZE: u32 = 512; // Fits all three schemas

fn main() -> Result<()> {
    println!("╔══════════════════════════════════════════════════╗");
    println!("║    OTAP MODQ Loopback — Zero-Copy Ring Demo     ║");
    println!("╚══════════════════════════════════════════════════╝");
    println!();

    let running = Arc::new(AtomicBool::new(true));

    // Spawn simulated FPGA consumer
    let fpga_running = running.clone();
    let fpga_thread = thread::spawn(move || simulated_fpga(fpga_running));

    thread::sleep(std::time::Duration::from_millis(50));

    let config = ModqConfig {
        transport: TransportMode::Loopback { shm_name: SHM_NAME.to_string() },
        ring_depth: RING_DEPTH,
        slot_size: SLOT_SIZE,
    };

    let mut queue = ModqQueue::open(&config)?;

    println!("Ring depth:    {} slots", queue.ring_depth());
    println!("Slot capacity: {} bytes", queue.slot_payload_capacity());
    println!();

    // === Schema payload test ===
    println!("── Schema Payload Submission ─────────────────────");

    // EquityTradeOrder (256 bytes)
    let order = EquityTradeOrder {
        symbol: *b"AAPL",
        side: Side::Buy,
        quantity: 1000,
        price_cents: 198_50.0,
        timestamp_ns: 1_700_000_000_000_000_000,
        client_order_id: 42,
        firm_uuid: [0xAB; 16],
    };
    let payload = order.encode();
    let sid = queue.submit(&payload)?;
    println!("  EquityTradeOrder: {} bytes, SID={}", payload.len(), sid);

    // MarketTick (64 bytes)
    let tick = MarketTick {
        symbol: *b"AAPL\0\0\0\0",
        bid_cents: 198_40.0,
        ask_cents: 198_60.0,
        last_cents: 198_50.0,
        volume: 1_000_000,
        timestamp_ns: 1_700_000_000_000_000_001,
        _reserved: [0; 16],
    };
    let payload = tick.encode();
    let sid = queue.submit(&payload)?;
    println!("  MarketTick:       {} bytes, SID={}", payload.len(), sid);

    // Heartbeat (32 bytes)
    let hb = Heartbeat {
        node_id: 7,
        clock_offset_ns: -42,
        heartbeat_seq: 999,
        _reserved: [0; 8],
    };
    let payload = hb.encode();
    let sid = queue.submit(&payload)?;
    println!("  Heartbeat:        {} bytes, SID={}", payload.len(), sid);
    println!();

    // === Throughput test ===
    println!("── Throughput Test (EquityTradeOrder × 1M) ───────");

    let order_payload = EquityTradeOrder {
        symbol: *b"MSFT",
        side: Side::Sell,
        quantity: 500,
        price_cents: 425_00.0,
        timestamp_ns: 0,
        client_order_id: 0,
        firm_uuid: [0; 16],
    }.encode();

    let iterations: u64 = 1_000_000;
    let start = Instant::now();
    for _ in 0..iterations {
        queue.submit(&order_payload)?;
    }
    let elapsed = start.elapsed();

    let total_bytes = iterations * order_payload.len() as u64;
    let gbps = (total_bytes * 8) as f64 / elapsed.as_secs_f64() / 1e9;
    let msg_rate = iterations as f64 / elapsed.as_secs_f64();
    let avg_ns = elapsed.as_nanos() as f64 / iterations as f64;

    println!("  Messages:    {}", iterations);
    println!("  Payload:     {} bytes (EquityTradeOrder)", order_payload.len());
    println!("  Elapsed:     {:.4} s", elapsed.as_secs_f64());
    println!("  Throughput:  {:.2} Gbps", gbps);
    println!("  Msg rate:    {:.2} M msg/s", msg_rate / 1e6);
    println!("  Avg latency: {:.1} ns/submit", avg_ns);
    println!();

    // Cleanup
    running.store(false, Ordering::Release);
    fpga_thread.join().expect("FPGA thread panicked");
    let c_name = std::ffi::CString::new(SHM_NAME)?;
    unsafe {
        libc::shm_unlink(c_name.as_ptr());
    }

    println!("== MODQ loopback demo complete. ==");
    println!("   Next: replace Loopback with PciBar transport on Versal hardware.");
    Ok(())
}

/// Simulated FPGA consumer — reads SLOT_READY, marks SLOT_FREE.
fn simulated_fpga(running: Arc<AtomicBool>) {
    let slot_stride = (SLOT_SIZE as usize + 16 + 63) & !63;
    let total_size = bar::regs::RING_BASE + slot_stride * RING_DEPTH as usize;

    let bar = BarMapping::open_shm(SHM_NAME, total_size)
        .expect("FPGA sim: failed to open shm");

    let mut head: u64 = 0;
    while running.load(Ordering::Relaxed) {
        let index = (head % RING_DEPTH as u64) as usize;
        let slot_base = bar::regs::RING_BASE + index * slot_stride;

        if bar.read32(slot_base) == SLOT_READY {
            bar.write32(slot_base, SLOT_FREE);
            head += 1;
        } else {
            std::hint::spin_loop();
        }
    }
}
