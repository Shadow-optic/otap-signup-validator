# OTAP MODQ — Memory-Optical Direct Queue

Zero-copy, kernel-bypass host library for OTAP FPGA endpoints.
Bridges application payloads directly to the `otap_app_core` Verilog module
via Corundum's PCIe BAR-mapped memory, with no kernel networking stack involvement.

## Architecture

```
APPLICATION PROCESS                     CORUNDUM / otap_app_core (FPGA)
━━━━━━━━━━━━━━━━━━━                     ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  modq_submit(payload)                  PCIe BAR2 (memory-mapped)
        │                               ┌──────────────────────────┐
        ▼                               │ Ring Control Registers   │
  ┌─────────────┐   mmap BAR2           │  0x0000: head (FPGA ++)  │
  │ ModqQueue   │◄─────────────────────►│  0x0008: tail (host ++)  │
  │             │                       │  0x0010: depth           │
  │ ring_base ──┼───────────────────────│  0x0018: slot_size       │
  │ tail (local)│                       ├──────────────────────────┤
  │ doorbell ───┼──── MMIO store ──────►│  0x0040: doorbell (WO)   │
  └─────────────┘                       ├──────────────────────────┤
                                        │ Slot 0: [status][len][..│
                                        │         payload........]│
                                        │ Slot 1: [status][len][..│
                                        │         payload........]│
                                        │ ...                      │
                                        │ Slot N-1                 │
                                        └──────────────────────────┘
                                                    │
                                          FPGA DMA reads slot
                                          ROB reorders
                                          Schema encode
                                          OAM + polarization
                                          ──► PHOTON LAUNCH
```

## Latency Budget

| Stage | Target | Mechanism |
|-------|--------|-----------|
| App write to ring slot | < 50 ns | `ptr::copy_nonoverlapping` to mmap'd BAR |
| Doorbell MMIO write | < 10 ns | Single PCIe posted write |
| PCIe TLP to FPGA | < 150 ns | Gen5 x16, no kernel |
| FPGA DMA read + ROB | < 50 ns | otap_app_core pipeline |
| Schema encode + optical | < 50 ns | Parallel DSP |
| **Total host-to-photon** | **< 310 ns** | |

## Usage

```rust
use otap_modq::{ModqQueue, ModqConfig};

let config = ModqConfig {
    pci_resource: "/sys/bus/pci/devices/0000:01:00.0/resource2".into(),
    ring_depth: 1024,
    slot_size: 4096,
};

let mut queue = ModqQueue::open(&config).expect("Failed to open MODQ queue");

// Submit a trade order — goes straight to FPGA, no kernel
let payload = build_equity_order("AAPL", Side::Buy, 1000, 198_50);
queue.submit(&payload).expect("submit failed");

// Poll completions (FPGA writes back to completion ring)
while let Some(completion) = queue.poll() {
    // completion.sequence_id matches the SID from otap_app_core
}
```

## Build

```bash
cargo build --release
cargo test
cargo bench  # Runs latency + throughput benchmarks
```

## Files

- `src/lib.rs` — Core library: ring buffer, BAR mapping, doorbell
- `src/bar.rs` — PCIe BAR mmap and register access
- `src/ring.rs` — Lock-free ring buffer with per-slot atomics
- `src/bin/modq_bench.rs` — Throughput + latency benchmark
- `src/bin/modq_loopback.rs` — Loopback test (works without FPGA via shared memory)
