# OTAP — Optical Transient Application Protocol

Reference implementation of the Optical Transient Application Protocol in
Rust. This codebase is the executable specification: every bit of the wire
format and every step of the encode/decode pipeline is defined here, and the
eventual RTL implementation must produce bit-exact equivalent output.

## What this is

A telecommunications protocol that encodes addressing, source authentication,
integrity, application typing, and sequencing into the *physical dimensions*
of an optical signal — eliminating header parsing and per-layer processing in
favor of single-clock-cycle parallel decode.

The five dimensions:

| Dim | Physical property             | OTAP function                       |
|-----|-------------------------------|-------------------------------------|
| D1  | Amplitude envelope            | Payload (64-QAM)                    |
| D2  | Wavelength (λ)                | Destination address (passive AWG)   |
| D3  | Polarization trajectory       | Source auth + payload integrity     |
| D4  | OAM topological charge        | Application schema selector         |
| D5  | Temporal microstructure       | Sequence + flow control             |

See the protocol specification document for the full design rationale and the
performance and security claims.

## Status

Reference codec + OBG host driver, in software. **Phase 0 deliverable.**

- ✅ Five-dimension Transient type with parallel decode
- ✅ HMAC-derived polarization-trajectory authentication
- ✅ Topological (winding-number) authentication — PMD-invariant
- ✅ Three pre-registered schemas (equity order, market tick, heartbeat)
- ✅ Channel simulator with PMD modeling
- ✅ Round-trip tests including adversarial tampering
- ✅ MODQ-style host driver (work-request / completion-queue API)
- ⬜ Bit-packed wire format (currently byte-aligned; see equity_order.rs notes)
- ⬜ RTL implementation against this reference (`rtl/`)
- ⬜ Cosim harness: identical inputs to RTL and Rust, diff outputs
- ⬜ Real PCIe driver: this codec linked against an FPGA BAR
- ⬜ Reference-channel Fiber Fingerprint Authentication (FFA) handshake
- ⬜ Selective NAK retransmit session layer

## Building

Requires Rust 1.75+ with Cargo.

```bash
cargo build --release
cargo test --workspace
cargo run --release -p otap-cli
```

The CLI runs three scenarios end-to-end:
1. Clean channel round trip — baseline.
2. PMD-rotated channel — shows topological auth surviving while sample-match
   auth fails.
3. Adversarial payload modification — shows both auth modes detect tampering.

## Repository layout

```
otap/
├── Cargo.toml                  Workspace root
├── crates/
│   ├── otap-core/              Transient, dimensions, errors. The spec in types.
│   ├── otap-crypto/            HMAC trajectory derivation + topological auth.
│   ├── otap-schema/            Pre-registered schemas keyed to OAM modes.
│   ├── otap-codec/             Encoder + Decoder. The parallel-decode core.
│   ├── otap-sim/               Channel simulator: PMD, BER, etc.
│   ├── otap-obg/               OBG host driver: MODQ work-request API.
│   └── otap-cli/               `otap-demo` binary.
├── docs/
│   └── architecture.md         Design rationale + RTL migration plan.
└── rtl/                        SystemVerilog implementation (future).
```

## Design principles

1. **The Rust code is the spec.** Any ambiguity in the prose specification
   is resolved by reference to this code. RTL conformance is defined as
   bit-exact equivalence to the encode/decode output here.
2. **Type system enforces protocol invariants.** Wavelengths and OAM modes are
   range-checked at construction. Schemas have compile-time fixed payload sizes.
   You cannot construct an invalid Transient.
3. **Parallel decode is structural.** The Decoder reads each dimension into a
   separate field of the result struct; no field is on the data dependency
   path of another. This mirrors what the FPGA does in one clock.
4. **Topology over geometry for authentication.** The topological auth mode is
   PMD-invariant — winding numbers survive arbitrary Poincaré-sphere rotations
   without the receiver having to track the fiber's Jones matrix.
5. **No allocator on the hot path.** The Transient payload is heap-allocated
   for ergonomic Rust, but every per-Transient operation that the FPGA must
   perform is allocation-free in the reference path. The OBG-side variant
   will use fixed-size buffers throughout.

## What this is not

- Not a network stack. There is no socket layer, no flow control beyond the
  guard-interval signaling in D5, no congestion control. OTAP runs on
  dedicated provisioned fiber where these concerns are configuration-time
  decisions.
- Not encrypted by default. D3 provides authentication and integrity. For
  payload confidentiality, layer AES-256-GCM on D1 — see spec §8.1.
- Not production code. This is the reference model. The OBG (OTAP Bridge
  Gateway) host driver and FPGA RTL are the artifacts that ship.

## License

Proprietary, pre-patent.

