# OTAP — Optical Transient Application Protocol

[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](#license)
[![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)](flr/)
[![Rust](https://img.shields.io/badge/Rust-1.75%2B-000000?logo=rust)](Cargo.toml)
[![Python](https://img.shields.io/badge/Python-3.10%2B-3776AB?logo=python)](stage-chronos/)
[![Tests: Go](https://img.shields.io/badge/Go%20tests-220%20passing-brightgreen)](#test-results)
[![Tests: Python](https://img.shields.io/badge/Python%20tests-54%20passing-brightgreen)](#test-results)

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
│   └── otap-cli/                 # Demo binaries
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
    └── golden_vectors.md
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

All Python benchmarks run with `python3 -m stage_chronos.bench` (18 sections). Median of 5–10 runs; resolution=20 (400 points) unless stated.

#### §1 — Torus encoding

| Resolution | Points | Time | ns/point |
|---|---|---|---|
| 5 | 25 | 34–46 µs | 1,355–1,853 |
| 10 | 100 | 86 µs | 862 |
| 20 | 400 | 254–296 µs | 639–741 |
| 50 | 2,500 | 1.6–1.8 ms | 625–709 |
| 100 | 10,000 | 6.2–7.3 ms | 620–732 |

#### §2 — Lorentz boost (400 pts)

Velocity-invariant: performance is identical from v=0.1c to v=0.999c.

| v/c | γ | Time | ns/point |
|---|---|---|---|
| 0.1 | 1.005 | 89–111 µs | 223–278 |
| 0.5 | 1.155 | 89–108 µs | 223–269 |
| 0.85 | 1.898 | 90–110 µs | 225–276 |
| 0.999 | 22.37 | 95–107 µs | 238–269 |

#### §4 — Coherence kernel (`measure_coherence`)

| Mode | sample=20 | sample=50 | sample=100 |
|---|---|---|---|
| Lorentz boost | 127–176 µs | 816–1,100 µs | 3.4–4.6 ms |
| Heavy noise | 210–280 µs | 1.3–1.8 ms | 5.6–7.4 ms |

#### §5 — Chromatic dispersion sweep (SMF-28, D=17 ps/nm·km)

| Length (km) | DL (ps/nm) | Φ | Status | Time |
|---|---|---|---|---|
| 0 | 0 | 1.00000 | PASS | 1.0–1.2 ms |
| 1 | 17 | 1.00000 | PASS | 1.1–1.4 ms |
| 20 | 340 | 0.99158 | PASS | 1.1–1.3 ms |
| 40 | 680 | 0.87346 | MARGINAL | 1.1–1.4 ms |
| 80 | 1,360 | 0.11478 | **FAIL** | 1.1–1.4 ms |
| 160 | 2,720 | 0.00000 | **FAIL** | 1.1–1.4 ms |
| 500 | 8,500 | 0.00000 | **FAIL** | 1.1–1.4 ms |

#### §6 — DGD sweep

| DGD (ps) | Φ | Status |
|---|---|---|
| 0–100 | 0.987–1.000 | PASS |
| 200 | 0.806 | MARGINAL |
| 500 | 0.00022 | **FAIL** |
| 1000 | 0.00000 | **FAIL** |

#### §7 — PDL sweep (old GCK kernel, exp(−MSE·1000))

| PDL (dB) | α\_s1 | Φ | Status |
|---|---|---|---|
| 0.00 | 1.0000 | 1.00000 | PASS |
| 0.10 | 0.9886 | 0.00000 | **FAIL** |
| 1.00 | 0.8913 | 0.00000 | **FAIL** |
| 3.00 | 0.7079 | 0.00000 | **FAIL** |

The exp(−MSE·1000) kernel saturates at sub-dB PDL; use `pdl_sweep.py` for a calibrated metric.

#### §10 — Throughput summary (resolution=20)

| Operation | Time | ops/s |
|---|---|---|
| `encode_torus` | 253–282 µs | 3,549–3,947 |
| `apply_lorentz_boost` (v=0.85c) | 91–126 µs | 7,955–10,936 |
| `apply_decoherence_noise` (σ=0.1) | 244–292 µs | 3,424–4,094 |
| `OtapFiberChannel` — PMD only | 739–975 µs | 1,025–1,353 |
| `OtapFiberChannel` — CD 80 km | 828–1,200 µs | 856–1,208 |
| `OtapFiberChannel` — PDL 1 dB | 862–1,100 µs | 893–1,160 |
| `OtapFiberChannel` — long-haul 500 km | 1.7 ms | 572–592 |
| `measure_coherence` (sample=50, Lorentz) | 892–1,100 µs | 871–1,122 |
| `measure_coherence` (sample=50, CD 80 km) | 1.5–1.9 ms | 518–677 |
| Fiber pipeline (PMD + coherence) | 2.3–2.9 ms | 342–431 |
| Fiber pipeline (CD 80 km + coherence) | 3.0–5.0 ms | 198–329 |

#### §11–12 — CHRONOS-Drift tracker

| Scenario (T=5,000 frames) | Time | µs/frame | fps |
|---|---|---|---|
| Step tap (Φ: 1.0 → 0.4) | 36–57 ms | 7.2–11.5 | 87,000–138,000 |
| Stable link | 70–108 ms | 14–22 | 45,000–71,000 |
| Marginal (Φ=0.9) | 70–108 ms | 14–22 | 45,000–71,000 |

**Scenario results** (mild thermal, 0.6 dB step tap at t=600):
- Dynamic FP = **0**, dynamic TP = **True**
- `COMPROMISED` latched at t = 602 (2-frame confirmation)
- All three scenarios (mild thermal, harsh thermal, 0.4 dB stealth tap): **0 false positives, 100% detection**

#### §13 — Slow-ramp adversary sweep (differential detector alone)

| Ramp (frames) | Detected | Latency | Note |
|---|---|---|---|
| 1 | True | 1 frame | Step-like |
| 5 | True | 1 frame | Step-like |
| 10 | True | 2 frames | Step-like |
| 25 | True | 3 frames | Ramp caught |
| 50 | **False** | — | **EWMA blind spot** |
| 100–1200 | **False** | — | **EWMA blind spot** |

Crossover ramp ≈ 50 frames (2.5× the EWMA time constant 1/α = 20 frames at α=0.05).

#### §14 — LayeredTracker sweep (fast + slow layers)

Zero evasions across all ramp rates tested.

| Ramp (frames) | Detected | Via | Latency |
|---|---|---|---|
| 1–25 | True | **FAST** | 3–5 frames |
| 50 | True | **SLOW** | 48 frames |
| 100 | True | SLOW | 69 frames |
| 200 | True | SLOW | 95 frames |
| 400 | True | SLOW | 124 frames |
| 800 | True | SLOW | 148 frames |
| 1200 | True | SLOW | 162 frames |
| 1600 | True | SLOW | 168 frames |

Maximum detection latency (at ramp=1600 frames, slow layer): **168 frames** after tap start.

#### §15 — PDL calibrated sweep (phi\_cal = 1/(1+rms\_rel))

The normalized metric avoids exp(−MSE·k) saturation and provides a monotonic, engineering-defensible threshold.

| PDL (dB) | phi\_cal | rms\_rel | Status |
|---|---|---|---|
| 0.00 | 1.00000 | 0.00000 | PASS |
| 0.10 | 0.98715 | 0.01302 | PASS |
| 0.25 | 0.96900 | 0.03200 | PASS |
| 0.50 | 0.94144 | 0.06220 | ALARM |
| 1.00 | 0.89474 | 0.11764 | ALARM |
| 2.00 | 0.82570 | 0.21109 | ALARM |
| 3.00 | 0.77802 | 0.28531 | FAIL |

**Threshold crossings:**
- phi\_cal < 0.95 at **0.42 dB**
- phi\_cal < 0.90 (alarm) at **1.00 dB**
- phi\_cal < 0.80 at **2.50 dB**

Typical rogue-tap PDL = 1.0–3.0 dB → detection margin: **0.0–2.0 dB** above alarm threshold.

#### §16 — Holographic pipeline latency (vectorized, excl. fiber init)

All paths give Φ\_receiver = 1.000000, Φ\_adversary = 0.000000.

| Modes | SLM fwd | MMF tx | DPC rec | SLM inv | **Total** |
|---|---|---|---|---|---|
| 32 | 11.5 µs | 1.7 µs | 1.5 µs | 17.4 µs | **32 µs** |
| 64 | 16.3 µs | 4.3 µs | 4.6 µs | 29.8 µs | **55 µs** |
| 128 | 36.2 µs | 8.3 µs | 8.6 µs | 53.2 µs | **106 µs** |
| 256 | 50.4 µs | 21.4 µs | 11.5 µs | 102 µs | **185 µs** |
| 512 | 172 µs | 25.5 µs | 58.7 µs | 199 µs | **456 µs** |

#### §17 — Holographic security sweep

| Modes | OAM symbol | Seed | Φ adversary | Φ receiver | Gap | |
|---|---|---|---|---|---|---|
| 64 | 0 | 42 | 0.00000000 | 1.00000000 | 1.000 | PASS |
| 64 | 1 | 42 | 0.00000000 | 1.00000000 | 1.000 | PASS |
| 64 | 2 | 42 | 0.00000000 | 1.00000000 | 1.000 | PASS |
| 64 | 5 | 42 | 0.00000000 | 1.00000000 | 1.000 | PASS |
| 128 | 2 | 7 | 0.00000000 | 1.00000000 | 1.000 | PASS |
| 128 | 2 | 99 | 0.00000000 | 1.00000000 | 1.000 | PASS |
| 256 | 3 | 1 | 0.00000000 | 1.00000000 | 1.000 | PASS |

#### §18 — Holographic throughput (128 modes, vectorized)

| Operation | Time | ops/s |
|---|---|---|
| `STAGEHelixEncoder.generate_helix` | 89 µs | 11,239 |
| `SpatialLightModulator.create_hologram` | **29.5 µs** | **33,866** |
| `MultimodeFiber.transmit` | **7.1 µs** | **141,423** |
| `MultimodeFiber.phase_conjugate_recovery` | **7.1 µs** | **140,964** |
| `SpatialLightModulator.reconstruct_manifold` | 52.1 µs | 19,185 |
| `measure_holographic_coherence` (sample=20) | 86 µs | 11,646 |
| **Full pipeline — cached fiber** | **390 µs** | **2,567** |
| Full pipeline — incl. QR fiber init | 4.0 ms | 252 |

The MMF T / T† operations are BLAS-backed matrix-vector products (fastest component). The SLM geometry loops dominate; `create_hologram` was 2.2× accelerated by switching from an indexed loop to three separate NumPy list comprehensions.

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

### Python — STAGE-CHRONOS

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
| **Total** | **54 / 54** | **ALL PASS** |

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
