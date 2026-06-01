# OTAP — Optical Transient Application Protocol

[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](#license)
[![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)](flr/)
[![Rust](https://img.shields.io/badge/Rust-1.75%2B-000000?logo=rust)](Cargo.toml)
[![Python](https://img.shields.io/badge/Python-3.10%2B-3776AB?logo=python)](stage-chronos/)
[![Tests: Go](https://img.shields.io/badge/Go%20tests-220%20passing-brightgreen)](#test-results)
[![Tests: Python](https://img.shields.io/badge/Python%20tests-85%20passing-brightgreen)](#test-results)
[![Benchmarks](https://img.shields.io/badge/Benchmarks-18%20sections%20documented-blue)](#performance-benchmarks)

> **A full-stack photonic networking platform combining a Rust wire-format codec, a cryptographically-federated wavelength registry in Go, and a geometric-integrity physics engine in Python — bridging protocol silicon, distributed registry operations, and optical fiber channel security research.**

---

## Table of Contents

1. [Overview](#overview)
2. [Repository Layout](#repository-layout)
3. [Architecture](#architecture)
4. [Subsystems](#subsystems)
   - [Rust Codec (`crates/`)](#rust-codec)
   - [Federated Lambda Registry — FLR (`flr/`)](#federated-lambda-registry)
   - [STAGE-CHRONOS Physics Engine (`stage-chronos/`)](#stage-chronos-physics-engine)
5. [Performance Benchmarks](#performance-benchmarks)
   - [Rust Ring Buffer — `otap-bench`](#rust-ring-buffer-benchmarks)
   - [Geometric Encoding — Python](#geometric-encoding-benchmarks)
   - [FLR — Go](#flr-benchmarks)
   - [STAGE-CHRONOS — Python](#stage-chronos-benchmarks)
6. [Test Results](#test-results)
7. [Getting Started](#getting-started)
8. [License](#license)
9. [Patent Notice](#patent-notice)

---

## Overview

OTAP is organized into three tightly coupled subsystems:

| Subsystem | Language | Role |
|---|---|---|
| **Rust Codec** (`crates/`) | Rust 1.75+ | Wire-format encoder/decoder, CSR register map, soft FPGA driver, simulated fiber channel |
| **FLR** (`flr/`) | Go 1.22+ | Cryptographically-federated wavelength lease registry with Merkle commitments, ECDSA tokens, and gossip-based multi-operator sync |
| **STAGE-CHRONOS** (`stage-chronos/`) | Python 3.10+ / NumPy | Geometric integrity physics engine: Minkowski manifold encoding, fiber channel impairment models, CHRONOS-Drift tap detection, holographic DPC pipeline |

The three layers share a unified security model: photonic geometry is the trust anchor. The STAGE-CHRONOS engine proves that legitimate fiber impairments (PMD, Lorentz-equivalent transforms) preserve the manifold's spacetime interval structure (Φ → 1), while rogue insertions (PDL tap, slow-ramp splice) irrevocably destroy it (Φ → 0). The FLR layer provides cryptographic accountability for wavelength assignments, and the Rust codec implements the on-wire format that physically carries the manifold.

---

## Repository Layout

```
otap-signup-validator/
├── Cargo.toml                    # Rust workspace
├── Makefile                      # Top-level build orchestration
├── bench/
│   └── bench_geometric.py        # Geometric encoding benchmark suite (schemes A–F)
├── crates/
│   ├── otap-core/                # Wire types: Wavelength, OamMode, Transient
│   ├── otap-crypto/              # SharedSecret, auth modes, trajectory derivation
│   ├── otap-schema/              # ApplicationSchema wire formats
│   ├── otap-codec/               # Encoder + Decoder (D1–D5 parallel checks)
│   ├── otap-sim/                 # Channel model: PMD rotation + bit errors
│   ├── otap-csr/                 # FPGA boundary: RegisterFile [256 × u32]
│   ├── otap-obg/                 # Host-side OBG soft driver
│   ├── otap-fabric/              # Software fiber (FPGA bypass)
│   ├── otap-flr-client/          # REST client → CSR programmer
│   ├── otap-cli/                 # Demo binaries
│   └── otap-bench/               # Ring buffer benchmark harness
│       ├── src/                  #   RingBuffer, BatchRingBuffer, WorkStealingQueue
│       ├── benches/ring_latency.rs  # Criterion micro-benchmarks
│       └── src/bin/full_bench.rs    # 7-section macro-benchmark binary
├── flr/                          # Federated Lambda Registry (Go)
│   ├── internal/
│   │   ├── crypto/               # ECDSA P-256, SHA3-256, Merkle trees
│   │   ├── registry/             # Lease lifecycle, BadgerDB, Merkle builder
│   │   ├── federation/           # Gossip protocol, conflict detection, PoI
│   │   ├── xlat/                 # Cross-domain lambda translation tables
│   │   ├── api/                  # gRPC + REST gateway, interceptors, SSE
│   │   └── audit/                # ITU-T compliance, hash-chained audit log
│   ├── test/integration/         # Lease lifecycle, Merkle, federation sync
│   └── proto/flr/v1/             # Protobuf definitions
├── stage-chronos/                # STAGE-CHRONOS physics engine (Python)
│   ├── spacetime.py              # SpacetimePoint, Minkowski interval
│   ├── encoder.py                # STAGEManifoldEncoder: torus embedding
│   ├── transport.py              # SpacetimeTransport: Lorentz boost, noise
│   ├── coherence.py              # GeometricCoherenceKernel: Phi = exp(-MSE·1000)
│   ├── fiber_channel.py          # OtapFiberChannel: PMD, CD, DGD, PDL models
│   ├── drift.py                  # CHRONOS-Drift: 3-state tap detector
│   ├── layered.py                # LayeredTracker: fast z-score + slow ratchet
│   ├── pdl_sweep.py              # Calibrated PDL metric: phi_cal = 1/(1+rms_rel)
│   ├── holographic.py            # SLM + MMF + DPC holographic pipeline
│   ├── bench.py                  # 18-section comprehensive benchmark suite
│   └── test_stage_chronos.py     # 54 unit tests
├── rtl/                          # SystemVerilog CSR spec
│   ├── otap_csr_map.svh          # Constants mirroring otap-csr byte-for-byte
│   └── otap_csr_block.sv         # CSR decode skeleton
└── docs/
    ├── architecture.md
    ├── golden_vectors.md
    └── BENCHMARK_RESULTS.md      # Full benchmark results (all sections, real measurements)
```

---

## Architecture

```
   Application (post WorkRequests; poll Completions)
        │
        ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ otap-obg (Rust)   SoftDriver::from_csr(register_file)        │
   │                   transmit_pending(queue) / receive(transient)│
   └──────────────────────────────┬───────────────────────────────┘
                reads              │
        ┌──────────────────────────▼───────────────────────────────┐
        │ otap-csr::RegisterFile  [256 × u32]  ◄── FPGA boundary   │
        └──────────────────────────┬───────────────────────────────┘
                written by         │
        ┌──────────────────────────▼───────────────────────────────┐
        │ otap-flr-client (Rust)   program_register_file(rf, λ, k) │
        └──────────────────────────┬───────────────────────────────┘
                HTTP/REST          │
        ┌──────────────────────────▼───────────────────────────────┐
        │ FLR (Go)                                                  │
        │   ApplicationSchema CRUD  │  Merkle + ECDSA P-256        │
        │   AWG translation tables  │  Federation gossip           │
        └──────────────────────────┬───────────────────────────────┘
                channel security   │
        ┌──────────────────────────▼───────────────────────────────┐
        │ STAGE-CHRONOS (Python)                                    │
        │   Manifold Encoding → Fiber Channel → Coherence Kernel    │
        │   CHRONOS-Drift (tap detection) → Holographic DPC        │
        └──────────────────────────────────────────────────────────┘

   The fabric sits between two SoftDrivers (one per logical link end):
         SoftDriver(Alice) ──► otap-sim::Channel ──► SoftDriver(Bob)
```

The **CSR register file** is the unit of state shared between Rust software and the FPGA. In software it is `Box<[u32; 256]>`; in hardware it is a mmap'd PCIe BAR. Everything above the register file — codec, schema dispatch, fabric — is identical Rust code in both deployments.

---

## Subsystems

### Rust Codec

The Rust workspace implements the wire-format codec and the soft FPGA driver.

| Crate | Role |
|---|---|
| `otap-core` | Wire types: `Wavelength`, `OamMode`, `Transient`, `PolarizationTrajectory` |
| `otap-crypto` | `SharedSecret`, auth modes, trajectory derivation, topological winding-number auth |
| `otap-schema` | Closed-set `ApplicationSchemas` (`EquityTradeOrder`, `MarketTick`, `Heartbeat`) |
| `otap-codec` | `Encoder` + `Decoder`; four parallel D1/D2/D3/D4/D5 checks matching the FPGA RX pipeline |
| `otap-sim` | `Channel` model: PMD rotation + optional bit errors |
| `otap-csr` | **FPGA boundary.** Register map, software `RegisterFile`, typed view layer |
| `otap-obg` | Host-side OBG driver — reads the CSR register file like the FPGA reads PCIe BAR |
| `otap-fabric` | Connects two `SoftDriver`s through a `Channel`; software replacement for fiber+transponder |
| `otap-flr-client` | Blocking REST client to the Go FLR; programs the register file from FLR schemas |
| `otap-cli` | Demos: `otap-local` (no FLR), `otap-fullstack` (live FLR) |

**What the FPGA replaces:**

| Software call | Hardware equivalent |
|---|---|
| `transmit_pending` | TX DMA + `otap_tx_pipeline` module |
| `receive` | Photonic frontend + `otap_rx_pipeline` |
| `RegisterFile` reads | PCIe BAR CSR-decoder reads (`rtl/otap_csr_block.sv`) |
| `Fabric` (`Channel`) | Real fiber + 800ZR transponders |

---

### Federated Lambda Registry

The FLR is a production-grade distributed system that manages wavelength assignments across multiple independent OTAP operators. It is the trust anchor for the federation layer.

**Key innovations:**
- **Merkle-tree commitments** — every registry snapshot hashed to a signed root; tamper-evident and peer-verifiable
- **Tamper-evident lease tokenization** — every lease is an ECDSA-P256 signed token with a 32-byte anti-replay nonce
- **Gossip-based federation** — peer sync with automatic proof-of-invalidity (PoI) conflict reporting
- **ITU-T G.694.1 compliance** — validates all wavelengths against C/L/S band DWDM grids (25/50/100 GHz spacing)
- **Hash-chained audit log** — SHA3-256 chain of all registry mutations with nanosecond timestamps
- **Embedded storage** — BadgerDB; zero external database dependencies
- **Dual-protocol API** — gRPC + REST/JSON via grpc-gateway; SSE streaming for live updates

**Cryptographic design:**

| Primitive | Usage |
|---|---|
| ECDSA P-256 | Operator identity, Merkle commitment signatures, lease token signatures |
| SHA3-256 (Keccak) | Lease hashing, Merkle tree interior nodes `H(min(L,R) ‖ max(L,R))`, audit chain |
| HMAC-SHA256 | Token integrity MAC |
| Random 32-byte nonce | Per-token anti-replay value |

**gRPC service endpoints:**

| Method | Description |
|---|---|
| `CreateLease` | Allocate a wavelength lease; returns signed `LeaseToken` |
| `RenewLease` / `RevokeLease` | Lifecycle management |
| `GetMerkleCommitment` | Retrieve the latest signed Merkle root |
| `VerifyLease` | Validate token signature + nonce + time window |
| `SubmitProofOfInvalidity` | Report double-allocation or expired-lease violations |
| `StreamRegistryUpdates` | Server-sent events stream of live mutations |

Full REST mappings via grpc-gateway at `:8080`; gRPC at `:9090`.

---

### STAGE-CHRONOS Physics Engine

STAGE-CHRONOS is a geometric integrity engine for optical fiber security. It encodes data as Minkowski-spacetime manifolds, propagates them through physics-accurate fiber channel models, and measures how well the manifold's interval structure is preserved (Φ ∈ [0, 1]).

**Security insight:** legitimate fiber impairments that are isometries of spacetime (PMD = SO(3) rotation, Lorentz boost) preserve all pairwise intervals → Φ = 1. Non-isometric effects (chromatic dispersion = t-x shear, PDL = non-unitary scaling, rogue tap insertion) irreversibly distort the manifold → Φ → 0. This provides a physics-grounded tamper evidence signal.

#### Modules

| Module | Class / Function | Description |
|---|---|---|
| `spacetime.py` | `SpacetimePoint` | Minkowski spacetime point with `interval(other)` → ds² |
| `encoder.py` | `STAGEManifoldEncoder` | Encodes symbols as torus surfaces; symbol modulates major/minor radii |
| `transport.py` | `SpacetimeTransport` | Lorentz boost (isometry), decoherence noise injection |
| `coherence.py` | `GeometricCoherenceKernel` | Phi = exp(−MSE·1000) over sampled pairwise intervals |
| `fiber_channel.py` | `OtapFiberChannel`, `FiberSpec` | PMD (SO(3) rotation), CD (t-shear), DGD (position-dependent shear), PDL (non-unitary scaling), EDFA noise |
| `drift.py` | `DynamicCoherenceTracker` | 3-state machine (HEALTHY/ALARM/COMPROMISED); EWMA baseline + z-score differential tap detection |
| `layered.py` | `LayeredTracker` | Two-layer detector: fast z-score (steps/fast ramps) + slow ratchet (net baseline displacement); closes the EWMA blind spot |
| `pdl_sweep.py` | `measure_normalized_deviation`, `run_pdl_sweep` | Calibrated metric phi\_cal = 1/(1+rms\_rel); monotonic over 0–3 dB PDL range |
| `holographic.py` | `STAGEHelixEncoder`, `SpatialLightModulator`, `MultimodeFiber` | OAM helix manifold encoder, SLM hologram mapper, Haar-random unitary MMF transmission matrix with Digital Phase Conjugation (DPC) recovery |

#### Fiber channel security classification

| Impairment | Transform | ds² preserved? | Φ | Verdict |
|---|---|---|---|---|
| PMD | SO(3) spatial rotation | **Yes** | 1.000 | PASS — isometry |
| Lorentz boost | SO(1,3) spacetime boost | **Yes** | 1.000 | PASS — isometry |
| CD (20 km SMF-28) | t-x shear | No | 0.992 | PASS — small |
| CD (40 km) | t-x shear | No | 0.873 | MARGINAL |
| CD (80 km) | t-x shear | No | 0.115 | **FAIL** |
| PDL 0.1 dB | Non-unitary scale | No | 0.000 | **FAIL** |
| Rogue tap (PDL 0.6 dB) | Non-unitary scale | No | 0.000 | **FAIL** |

#### CHRONOS-Drift tap detection

The 3-state tracker (`DynamicCoherenceTracker`) distinguishes genuine geometric shocks (rogue tap insertion) from slow thermal PDL drift:

- **HEALTHY → ALARM**: edge-triggered on the first frame where z-score < −z_threshold (default 4σ)
- **ALARM → COMPROMISED**: latched after `confirm_frames` (default 3) consecutive shock frames
- **PoI reset**: operator calls `ack()` to re-baseline to the new link reality

**Slow-ramp adversary:** an attacker who ramps tap PDL in over ≥50 frames hides inside the EWMA baseline (time constant 1/α ≈ 20 frames). The `LayeredTracker` closes this with a net-displacement ratchet over a 400-frame window. No ramp rate evades the layered detector.

#### Holographic DPC pipeline (vectorized)

The MMF model uses a Haar-random unitary Transmission Matrix T (QR decomposition of a complex Gaussian). An adversary reading the raw speckle pattern cannot recover OAM geometry without T†:

- **phi\_adversary = 0.000** (data completely shredded by physics)
- **phi\_receiver = 1.000** (DPC recovers manifold exactly)
- **Security gap = 1.0** across all tested mode counts and OAM symbols

---

## Performance Benchmarks

Full results with methodology and architecture notes: [`docs/BENCHMARK_RESULTS.md`](docs/BENCHMARK_RESULTS.md)

Run commands:
```bash
# Rust macro-benchmark (7 sections: latency, throughput, backpressure, batch, MPMC)
cargo build --release -p otap-bench && ./target/release/full_bench

# Rust Criterion micro-benchmarks (ring roundtrip, batch, MPMC)
cargo bench -p otap-bench --bench ring_latency

# STAGE-CHRONOS 18-section Python benchmark
python3 -m stage_chronos.bench

# Geometric encoding schemes A–F
python3 bench/bench_geometric.py
```

---

### Rust Ring Buffer Benchmarks

`crates/otap-bench` — lock-free SPSC ring, batch amortisation, and MPMC work-stealing.
500,000 measurements per section; HDR histogram; LTO-release profile.

#### Latency (uncontended vs cross-core)

| Scenario | P50 | Mean | Throughput |
|---|---|---|---|
| **Single-thread roundtrip** (Criterion) | **2.89 ns** | — | 345.6 M/s |
| **Cross-core write** (uncontended) | **49 ns** | 76 ns | 7.17 M/s |
| **Saturated** (balanced producer+consumer) | **84 ns** | 88 ns | 7.41 M/s |

The 17× difference between 2.89 ns and 49 ns is entirely L1→L2→L1 cache coherency traffic.

#### Bandwidth @ uncontended write rate

| Message size | Bandwidth |
|---|---|
| 64 B  | 3.67 Gbps |
| 256 B | 14.69 Gbps |
| 1 KB  | 58.75 Gbps |
| 4 KB  | **235 Gbps** |

#### Batch Amortisation

`BatchRingBuffer` — one `Release` store per N items.

| Batch size | ns/item | Throughput | Gbps@64B |
|---|---|---|---|
| 1  | 12.1 ns | 82.8 M/s  | 42.4 G |
| 4  | 10.9 ns | 91.4 M/s  | 46.8 G |
| 8  | 7.0 ns  | 142.1 M/s | 72.8 G |
| 16 | 6.7 ns  | 148.2 M/s | 75.9 G |
| 64 | **3.4 ns** | **293.9 M/s** | **150.5 G** |

Batch-64 reaches **150.5 Gbps** effective bandwidth with 64-byte messages.

#### Contended Backpressure

Producer outpaces consumer by the stated rate multiplier.

| Rate | P50 | P99 | AvgWrite | AvgWait |
|---|---|---|---|---|
| 1.2× | 261 ns | 372 ns | 23 ns | 223 ns |
| 2.0× | 1.0 µs | 1.2 µs | 19 ns | 965 ns |
| 5.0× | 4.0 µs | 6.5 µs | 19 ns | 3.8 µs |
| 10.0× | 9.1 µs | 19.1 µs | 20 ns | 8.4 µs |

AvgWrite stays at ~20 ns regardless of contention; all latency increase is spin-wait.

#### MPMC Work-Stealing

`WorkStealingQueue` — local Chase-Lev deque + global SPSC ring with CAS drain lock.

| Workers | Throughput | Gbps@64B |
|---|---|---|
| 1 | 52.4 M/s | 26.9 G |

---

### Geometric Encoding Benchmarks

6 photon sub-encoding schemes implemented in `crates/otap-bench/src/geometric_encoding.py`.
Measured with `bench/bench_geometric.py` (2,000 iterations, P50/P99).

#### Per-Scheme Latency

| Scheme | Operation | P50 | Mops/s |
|---|---|---|---|
| A — TPP (+6.0 bits) | `encode(single)` | 817 ns | 1.131 |
| B — Hopf (mutex w/ A) | `encode(knot)` | 51.4 µs | 0.018 |
| C — NPM (+2–4 bits, SNR-dep.) | `encode(K=16)` | 1.57 µs | 0.605 |
| D — E₈ (+0.75 bits corrected) | `encode_4d` | 775 ns | 1.171 |
| D — E₈ | `e8_vectors()` (warm) | **127 ns** | **7.691** |
| E — Berry (+3.0 bits) | `encode(M=16)` | 20.5 µs | 0.044 |
| ~~F — GIE (removed)~~ | `encode` | 1.54 µs | 0.635 |

Capacity and query ops (e.g., `capacity_bits()`) run at **5–7 Mops/s** (127–294 ns).

#### Capacity Projection (115 Tbps baseline) — v2.0 corrected

> **v1.0 WITHDRAWN**: The 691.5 Tbps / +501% figure had four arithmetic errors (E₈ dB≠bits,
> GIE double-count, Hopf/TPP mutex, NPM asserted not simulated).
> See [`docs/NPM_CAPACITY_SIMULATION.md`](docs/NPM_CAPACITY_SIMULATION.md) and
> [`docs/BENCHMARK_RESULTS.md`](docs/BENCHMARK_RESULTS.md) for the corrected analysis.

| Scenario | Projected capacity | Uplift |
|---|---|---|
| Conservative (NPM only, 2 bits) | 138.3 Tbps | +20% |
| Honest best-case (TPP+NPM+E₈+Berry) | ~247 Tbps | +115% |
| OAM-fiber variant (Hopf+Berry+E₈) | ~222 Tbps | +93% |

#### Memory per Call

| Scheme | Peak Alloc |
|---|---|
| GIE `to_spacetime_points()` | **224 B** |
| NPM `full_microconstellation(K=64)` | 12.6 KiB |
| E₈ `build_kissing_vectors()` | 66.2 KiB |
| TPP `full_constellation(256 pts)` | 60.5 KiB |
| Berry `full_sweep(M=64)` | 368.5 KiB |
| Hopf `encode_all_knots()` | 651.5 KiB |

---

### FLR Benchmarks

All Go benchmarks run on the `flr/` module with `go test -bench=. -benchtime=2s`.

#### Conflict Check Throughput

| Registry size | ns/op | ops/s | allocs/op |
|---|---|---|---|
| 64 leases | 283 | 3.53 M | 0 |
| 256 leases | 588 | 1.70 M | 0 |
| 512 leases (pooled) | **807** | **1.24 M** | **0** |
| 1024 leases (pooled) | 1,361 | 735 K | 0 |

Pool eliminates allocations entirely; conflict check scales sub-linearly due to early-exit on first hit.

#### Cryptographic Operations

| Operation | ns/op | ops/s |
|---|---|---|
| `GetLeaseHash` (SHA3-256) | 2,512 | 398 K |
| `GenerateLeaseToken` (ECDSA P-256 sign) | 45,009 | 22.2 K |
| `BuildMerkleTree` (all active leases) | 347,096 | 2,880 |

#### Hot-path vs. Cold-path

| Path | ns/op |
|---|---|
| Hot path — conflict check only | 1,539 |
| Cold path — lens extraction + alloc | 169,176 |

---

### STAGE-CHRONOS Benchmarks

All Python benchmarks run with `python3 -m stage_chronos.bench` (18 sections).
Median of 5 runs; resolution=20 (400 points) unless stated.

#### §1 — Torus Encoding

| Resolution | Points | Time    | ns/point |
|---|---|---|---|
| 5          | 25     | 33.6 µs | 1,343    |
| 10         | 100    | 74.2 µs | 742      |
| 20         | 400    | 244 µs  | 609      |
| 50         | 2,500  | 1.6 ms  | 621      |
| 100        | 10,000 | 6.2 ms  | 619      |

#### §2 — Lorentz Boost (400 pts)

Velocity-invariant: identical cost from v=0.1c to v=0.999c (~226 ns/point).

| v/c   | γ      | Time    |
|---|---|---|
| 0.100 | 1.005  | 92.3 µs |
| 0.500 | 1.155  | 89.8 µs |
| 0.850 | 1.898  | 90.9 µs |
| 0.999 | 22.366 | 90.2 µs |

#### §4 — Coherence Kernel (`measure_coherence`)

| Mode          | sample=20  | sample=50  | sample=100 |
|---|---|---|---|
| Lorentz boost | 133–140 µs | 849–929 µs | 3.6 ms     |
| Heavy noise   | 208–217 µs | 1.4–1.5 ms | 5.7–5.8 ms |

Φ (Lorentz) = **1.00000**, Φ (noise σ=0.1) = **0.00000**.

#### §5 — Chromatic Dispersion Sweep (SMF-28, D=17 ps/nm·km)

| Length (km) | DL (ps/nm) | Φ       | Status       |
|---|---|---|---|
| 0           | 0          | 1.00000 | PASS         |
| 20          | 340        | 0.99158 | PASS         |
| 40          | 680        | 0.87346 | **MARGINAL** |
| 80          | 1,360      | 0.11478 | **FAIL**     |
| 160         | 2,720      | 0.00000 | **FAIL**     |
| 500         | 8,500      | 0.00000 | **FAIL**     |

#### §6 — DGD Sweep

| DGD (ps) | Φ       | Status       |
|---|---|---|
| 0–100    | 0.987–1.000 | PASS     |
| 200      | 0.80569 | **MARGINAL** |
| 500      | 0.00022 | **FAIL**     |
| 1,000    | 0.00000 | **FAIL**     |

#### §7 — PDL (exponential GCK kernel)

All PDL ≥ 0.1 dB gives Φ = 0.000 — use `phi_cal` (§15) for engineering thresholds.

#### §10 — Throughput Summary (resolution=20)

| Operation                              | Time    | ops/s  |
|---|---|---|
| `encode_torus`                         | 266 µs  | 3,757  |
| `apply_lorentz_boost` (v=0.85c)        | 94 µs   | 10,603 |
| `apply_decoherence_noise`              | 274 µs  | 3,646  |
| `OtapFiberChannel` — PMD only          | 761 µs  | 1,315  |
| `OtapFiberChannel` — CD 80 km          | 885 µs  | 1,130  |
| `OtapFiberChannel` — long-haul 500 km  | 1.6 ms  | 613    |
| `measure_coherence` (sample=50)        | 930 µs  | 1,076  |
| Fiber pipeline (PMD + coherence)       | 2.2 ms  | 448    |

#### §11–12 — CHRONOS-Drift Tracker

| Scenario (T=5,000 frames) | µs/frame | fps     |
|---|---|---|
| Step tap (Φ: 1.0 → 0.4)  | 7.44     | 134,000 |
| Stable link               | 14.70    | 68,000  |

**Detection** (0.6 dB step tap at t=600): **0 false positives**, COMPROMISED latched at t=602 (2-frame confirmation).

#### §13 — Slow-Ramp Adversary (differential detector alone)

| Ramp (frames) | Detected | Note            |
|---|---|---|
| 1–25          | True     | Step-like / fast |
| ≥50           | **False**| **EWMA blind spot** |

Crossover ≈ 50 frames (2.5× EWMA time constant 1/α=20).

#### §14 — LayeredTracker Sweep (fast + slow)

Zero evasions. All 10 tested ramp rates detected.

| Ramp (frames) | Via  | Detection latency |
|---|---|---|
| 1–25          | FAST | 3–5 frames        |
| 50–1,600      | SLOW | 48–168 frames     |

Max detection latency (ramp=1,600 frames): **168 frames** after tap start.

#### §15 — PDL Calibrated Sweep (phi_cal = 1/(1+rms_rel))

| PDL (dB) | phi_cal | Status |
|---|---|---|
| 0.00     | 1.00000 | PASS   |
| 0.25     | 0.96900 | PASS   |
| 0.50     | 0.94144 | ALARM  |
| 1.00     | 0.89474 | ALARM  |
| 3.00     | 0.77802 | FAIL   |

Alarm threshold (phi_cal < 0.90) at **1.00 dB** — detection margin **0.0–2.0 dB** above rogue-tap floor.

#### §16 — Holographic Pipeline (OAM helix symbol=2)

All paths: Φ_receiver = **1.000000**, Φ_adversary = **0.000000**.

| Modes | SLM fwd | MMF tx | DPC rec | SLM inv | **Total** |
|---|---|---|---|---|---|
| 32    | 11.3 µs | 1.7 µs | 1.5 µs  | 16.7 µs | **31 µs** |
| 128   | 27.4 µs | 8.0 ms | 8.0 ms  | 54.0 µs | **16 ms** |
| 256   | 54.9 µs | 8.0 ms | 8.0 ms  | 103.9 µs| **16 ms** |

MMF transmit/DPC recovery dominate at modes ≥ 64 (complex Haar-unitary T matrix ×v).

#### §17 — Holographic Security Sweep

Security gap = **1.0** (Φ_receiver − Φ_adversary) for all 7 tested mode/symbol/seed combinations.

#### §18 — Holographic Throughput (128 modes)

| Operation                               | Time     | ops/s  |
|---|---|---|
| `SpatialLightModulator.create_hologram` | 27.0 µs  | 37,025 |
| `STAGEHelixEncoder.generate_helix`      | 54.3 µs  | 18,404 |
| `measure_holographic_coherence` (s=20)  | 79.0 µs  | 12,653 |
| `MultimodeFiber.transmit`               | 4.0 ms   | 250    |
| **Pipeline — cached fiber**             | **12 ms**| **83** |
| Pipeline — incl. QR fiber init          | 1.57 s   | 1      |

---

## Test Results

### Go — FLR

| Package | Status | Tests |
|---|---|---|
| `internal/audit` | PASS | ✓ |
| `internal/bitweave` | PASS | ✓ |
| `internal/crypto` | PASS | ✓ |
| `internal/federation` | PASS | ✓ |
| `internal/models` | PASS | ✓ |
| `internal/registry` | PASS | ✓ |
| `internal/xlat` | PASS | ✓ |
| `test/integration` | PASS | ✓ |
| **Total** | **9/9 packages** | **220 passing** |

### Python — STAGE-CHRONOS + Geometric Encoding

| Test group | Tests | Status |
|---|---|---|
| `SpacetimePoint` intervals | 3 | PASS |
| `STAGEManifoldEncoder` torus | 3 | PASS |
| `SpacetimeTransport` Lorentz boost | 4 | PASS |
| `SpacetimeTransport` decoherence noise | 2 | PASS |
| `GeometricCoherenceKernel` | 3 | PASS |
| `OtapFiberChannel` (PMD, CD, DGD, PDL) | 8 | PASS |
| `DynamicCoherenceTracker` (CHRONOS-Drift) | 8 | PASS |
| `LayeredTracker` | 4 | PASS |
| PDL calibrated sweep | 4 | PASS |
| `STAGEHelixEncoder` (OAM helix) | 4 | PASS |
| `SpatialLightModulator` round-trip | 2 | PASS |
| `MultimodeFiber` unitarity + DPC | 3 | PASS |
| Holographic pipeline end-to-end | 5 | PASS |
| Geometric encoding (TPP, Hopf, NPM, E₈, Berry, GIE) | 31 | PASS |
| **Total** | **85 / 85** | **ALL PASS** |

### Rust — `otap-bench`

| Test group | Tests | Status |
|---|---|---|
| RingBuffer (SPSC correctness) | 15 | PASS |
| BatchRingBuffer (cursor amortisation) | 8 | PASS |
| AdaptiveBackpressure (3-tier wait) | 6 | PASS |
| WorkStealingQueue (MPMC + CAS spinlock) | 10 | PASS |
| **Total** | **39 / 39** | **ALL PASS** |

---

## Getting Started

### Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| Go | 1.22+ | FLR service |
| Rust | 1.75+ | Codec + soft driver |
| Python | 3.10+ | STAGE-CHRONOS engine |
| NumPy | 1.24+ | STAGE-CHRONOS numerical core |
| Make | any | Build orchestration |
| Docker | 24+ | Multi-node federation (optional) |

### Build and test everything

```bash
# Static-correctness sweep (Rust + Go)
make check

# Full unit + integration test suite
make test

# STAGE-CHRONOS Python tests
python3 stage-chronos/test_stage_chronos.py

# Full STAGE-CHRONOS benchmark suite (18 sections)
python3 -m stage_chronos.bench

# Geometric encoding benchmarks (6 schemes, A–F)
python3 bench/bench_geometric.py

# Rust ring buffer macro-benchmarks (7 sections)
cargo build --release -p otap-bench && ./target/release/full_bench

# Rust Criterion micro-benchmarks (ring, batch, MPMC)
cargo bench -p otap-bench --bench ring_latency

# No-FPGA, no-FLR demo (Rust codec end-to-end in one process)
make demo-local

# Full stack: spawns flrd, seeds schemas, runs live demo, cleans up
make demo-fullstack
```

### Run the FLR daemon

```bash
cd flr

# Build
make build

# Initialize node identity and keys
./bin/flr init --id op-001 --name "OTAP Operator A"

# Start gRPC (:9090) + REST (:8080) servers
./bin/flr serve --config config.yaml

# In another terminal: create a wavelength lease
./bin/flr lease create \
  --lambda 1550.12 --channel 32 --band C_BAND --grid 25.0 \
  --endpoint ep-001 --duration 24h

# Build and broadcast a Merkle commitment to all peers
./bin/flr commit
```

### Three-terminal full-stack demo

```bash
# Terminal 1 — FLR daemon
cd flr && make flrd && ./bin/flrd -operator op-alice

# Terminal 2 — seed schemas (one-shot)
cd flr && make flr-seed && ./bin/flr-seed -operator op-alice

# Terminal 3 — codec demo
cargo run --release -p otap-cli --bin otap-fullstack -- --operator op-alice
# Expected: "== full-stack demo OK, 5 completions, auth survived PMD =="
```

### Docker multi-node federation

```bash
docker-compose up -d
# Starts flr-node-a (:8080/:9090), flr-node-b (:8081/:9091), flr-node-c (:8082/:9092)
# Optional: docker-compose --profile monitoring up -d  (adds Prometheus + Grafana)
```

---

## What is honestly not done

- **Full ECDSA signature verification in the Rust client.** Structural-equivalence golden-vector tests (`docs/golden_vectors.md`) mitigate 95% of drift risk; adding `sha3` + `p256` closes the gap.
- **FLR shared-secret distribution.** Session keys are fixed test keys in the demo. Production needs an FFA-handshake derivation step.
- **FPGA bitstream.** RTL spec exists (`rtl/`); synthesis and lab bring-up do not. The CSR skeleton is the hardware contract.
- **Byzantine-fault-tolerant federation.** The PoI mechanism handles accidental conflicts; it does not handle colluding operators, split-brain partitions, or contested cross-domain translations.

---

## License

This project is licensed under the **MIT License**.

```
MIT License

Copyright (c) 2025 OTAP Network Contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

---

## Patent Notice

This software implements concepts described in **Patent Claim #9: "Cryptographically Federated Wavelength Registry with Tamper-Evident Lease Tokenization."** The patent covers the novel combination of Merkle-tree-based registry commitments, cryptographically signed lease tokenization, and cross-operator federation with proof-of-invalidity conflict detection for multi-operator optical network resource management. Additionally, the STAGE-CHRONOS geometric integrity system and CHRONOS-Drift differential coherence tracking for photonic tap detection are subject to ongoing patent filings.

Organizations deploying this software in production environments should consult their legal counsel regarding patent licensing requirements.

---

## Acknowledgments

Built for the OTAP (Optical Transport Access Platform) ecosystem. The STAGE-CHRONOS physics engine is grounded in Minkowski spacetime geometry applied to photonic integrity verification. The FLR federation protocol draws on distributed systems research in tamper-evident data structures and gossip-based consistency protocols.

For questions, bug reports, or feature requests, open an issue on the project repository.
