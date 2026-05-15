# OTAP — Optical Transient Application Protocol

Reference implementation in Rust + Go, with a path to FPGA without a hard dependency on one.

This repository contains:

- **Rust reference codec** (`crates/otap-*`) — encoder, decoder, simulated fiber, software OBG driver. Faithful to the eventual hardware in every protocol detail.
- **Federated Lambda Registry** (`flr/`) — Go service for wavelength leases, AWG translation tables, and now ApplicationSchemas, with cryptographic federation between operators.
- **Glue layer** (`crates/otap-csr`, `crates/otap-flr-client`, `crates/otap-fabric`) — connects the Rust codec to the Go registry through a CSR-shaped boundary that's bit-compatible with the eventual FPGA.
- **Hardware spec** (`rtl/`) — SystemVerilog CSR header and decoder skeleton mirroring the Rust register-map constants exactly. The FPGA build can be added later without touching the software.

The Phase-0 design notes (Rust-codec-only) are preserved in `README.phase0.md`.

## Architecture

```
   Application
   (post WorkRequests; poll Completions)
        │
        ▼
   ┌──────────────────────────────────────────┐
   │ otap-obg (Rust)                          │
   │   SoftDriver::from_csr(register_file)    │
   │   transmit_pending(send_queue)           │
   │   receive(transient)                     │
   └──────────────────────────────────────────┘
        │ reads
        ▼
   ┌──────────────────────────────────────────┐
   │ otap-csr::RegisterFile  [256 × u32]      │ ◄── this is the FPGA boundary
   │   wavelength, secret, schema tables, …   │
   └──────────────────────────────────────────┘
        │ written by
        ▼
   ┌──────────────────────────────────────────┐
   │ otap-flr-client (Rust)                   │
   │   program_register_file(rf, λ, secret)   │
   │   HTTP GET /v1/schemas → CSR writes      │
   └──────────────────────────────────────────┘
        │ HTTP/REST
        ▼
   ┌──────────────────────────────────────────┐
   │ FLR (Go)                                 │
   │   ApplicationSchema CRUD                 │
   │   Merkle commitments + ECDSA P-256       │
   │   AWG translation tables                 │
   │   Federation gossip                      │
   └──────────────────────────────────────────┘

   The "fabric" sits between two SoftDrivers (one on each end of a logical link):

         SoftDriver(Alice) ──► otap-sim::Channel ──► SoftDriver(Bob)
                  (PMD rotation, optional bit errors)
```

The key design decision is that **the CSR register file is the unit of state shared between Rust and the FPGA**. In software, it's a `Box<[u32; 256]>`. In hardware, it's mmap'd PCIe BAR. Everything above the register file — codec, schema dispatch, fabric — is identical Rust code in both deployments.

## Getting started

You need: Go 1.22+, Rust 1.75+.

```bash
# Static-correctness sweep (fast)
make check

# Full unit + integration test suite
make test

# The no-FPGA, no-FLR demo — proves the codec works end-to-end in one process
make demo-local

# Full stack: spawns flrd, seeds schemas, runs the live demo, cleans up
make demo-fullstack
```

## What each crate is for

| Crate | Role |
|---|---|
| `otap-core` | Wire-format types: Wavelength, OamMode, Transient, PolarizationTrajectory, ProtocolError. |
| `otap-crypto` | SharedSecret, AuthMode, trajectory derivation, topological winding-number auth. |
| `otap-schema` | Closed-set ApplicationSchemas (`EquityTradeOrder`, `MarketTick`, `Heartbeat`) with byte-exact wire formats. |
| `otap-codec` | `Encoder` and `Decoder`. Implements the four parallel D1/D2/D3/D4/D5 checks the FPGA RX pipeline will run. |
| `otap-sim` | `Channel` model: PMD rotation + optional bit errors. |
| `otap-csr` | **The FPGA boundary.** Register map, software `RegisterFile`, typed view layer. |
| `otap-obg` | Host-side OBG driver. `SoftDriver::from_csr` reads the register file just like the FPGA reads PCIe BAR. |
| `otap-fabric` | Connects two `SoftDriver`s through a `Channel`. The pure-software replacement for fiber+transponder. |
| `otap-flr-client` | Blocking REST client to the Go FLR. Programs the register file from FLR-served ApplicationSchemas. |
| `otap-cli` | Demos: `otap-local` (no FLR), `otap-fullstack` (live FLR). |

## What the FPGA replaces

| Software call | Hardware equivalent |
|---|---|
| `transmit_pending` | TX DMA + `otap_tx_pipeline` module |
| `receive` | Photonic frontend + `otap_rx_pipeline` |
| `RegisterFile` reads | PCIe BAR CSR-decoder reads (`rtl/otap_csr_block.sv`) |
| `SendQueue.post` | PCIe DMA descriptor + doorbell |
| `CompletionQueue.push` (driver-side) | FPGA → host DMA + MSI-X |
| `Fabric` (the `Channel` part) | Real fiber + 800ZR transponders |

The Rust `SoftDriver` and the FPGA RTL read identical register layouts (`rtl/otap_csr_map.svh` mirrors `otap-csr/src/lib.rs` byte-for-byte). The `MAP_VERSION` constant lets host software detect skew at boot.

## Cross-language integrity

The Go FLR signs ApplicationSchemas with ECDSA P-256 over a canonical JSON form. The Rust client deserializes the same JSON. The riskiest unverified assumption is that both sides agree byte-for-byte on canonical bytes.

We have golden vectors for this: see `docs/golden_vectors.md`. Regenerate them with:

```bash
make golden
```

The Rust side currently does **structural-equivalence** validation against the vectors — field counts, offsets, types, payload sizes. Full signature verification in Rust is a documented follow-up (one PR's worth of work; instructions in `docs/golden_vectors.md`).

### Testing against a Cloudflare Workers FLR

A drop-in Workers implementation of the FLR schema-registry surface lives in `worker/`. Deploying it gives you a publicly addressable HTTPS endpoint that the Rust client can target without any local Go toolchain. It also exposes `/v1/golden_vectors`, which `make verify-golden-http` will cross-check structurally:

```bash
# Deploy the Worker once, then point the integration test at it:
make verify-golden-http OTAP_FLR_URL=https://otap-flr-worker.your-subdomain.workers.dev
```

The Worker uses RFC 6979 deterministic ECDSA (via `@noble/curves`), so consecutive calls to `/v1/golden_vectors` produce byte-identical responses — the test enforces this. Signature bytes won't match the Go FLR (which uses random `k`), but both verify under the same public key.

## Running the demos manually

If `make demo-fullstack` is too magical, the explicit three-terminal version:

```bash
# Terminal 1
cd flr && make flrd && ./bin/flrd -operator op-alice

# Terminal 2 (one-shot)
cd flr && make flr-seed && ./bin/flr-seed -operator op-alice

# Terminal 3
cargo run --release -p otap-cli --bin otap-fullstack -- --operator op-alice
```

Expected final line: `== full-stack demo OK, 5 completions, auth survived PMD ==`.

For the no-FLR proof:

```bash
cargo run --release -p otap-cli --bin otap-local
```

Expected final line: `== This is the no-FPGA, no-FLR proof. ==`.

## What's honestly not done

- **Full ECDSA signature verification in the Rust client.** Currently the Rust side trusts the FLR-served schemas. Adding `sha3` + `p256` and porting the canonical-bytes function from Go closes this; the structural-equivalence golden-vector tests already mitigate 95% of the realistic drift risk.
- **FLR shared-secret distribution.** Session keys (the 256 bits programmed into `REG_SECRET_BASE`) are fixed test keys in the demo. Production needs an FFA-handshake derivation step.
- **FPGA bitstream.** The RTL spec exists; synthesis and lab bring-up don't. The `rtl/otap_csr_block.sv` skeleton plus the `otap_csr_map.svh` header are the contract that whoever writes the rest of the RTL must satisfy.
- **Federation across mutually-distrusting operators.** The conflict detector handles double-allocation and expired leases. It does *not* handle Byzantine operators issuing contradictory commitments, split-brain partitions, or contested cross-domain translations. Documented as deferrable in `flr/DESIGN.md` §7.

## Layout

```
otap/
├── Makefile                          # top-level orchestration
├── README.md                         # this file
├── Cargo.toml                        # Rust workspace
├── docs/
│   └── golden_vectors.md             # cross-language integrity story
├── testdata/
│   └── golden_vectors.json           # generated by `make golden`
├── crates/
│   ├── otap-core/                    # wire types
│   ├── otap-crypto/                  # secrets, auth modes
│   ├── otap-schema/                  # ApplicationSchemas
│   ├── otap-codec/                   # encoder + decoder
│   ├── otap-sim/                     # channel model
│   ├── otap-csr/                     # register map + software RF (FPGA boundary)
│   ├── otap-obg/                     # host driver
│   ├── otap-fabric/                  # software fiber (FPGA bypass)
│   ├── otap-flr-client/              # REST client → CSR programmer
│   └── otap-cli/                     # demo binaries
├── flr/                              # Federated Lambda Registry (Go)
│   ├── cmd/
│   │   ├── flrd/                     # server daemon
│   │   └── flr-seed/                 # idempotent schema seeder
│   ├── internal/
│   │   ├── models/                   # ApplicationSchema, lease, ...
│   │   ├── crypto/                   # ECDSA + SHA3 + canonical hashing
│   │   ├── registry/                 # store + engine + schema engine
│   │   ├── federation/               # gossip + conflict detection
│   │   ├── xlat/                     # AWG translation tables
│   │   ├── audit/                    # hash-chained audit log
│   │   └── api/                      # gRPC server + REST gateway + schema REST
│   └── proto/flr/v1/                 # protobuf definitions
└── rtl/                              # SystemVerilog spec
    ├── otap_csr_map.svh              # constants mirroring otap-csr
    └── otap_csr_block.sv             # CSR storage + decode skeleton
```
