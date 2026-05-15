# OTAP Reference Codec — Architecture

This document describes how the Rust codebase maps to the OTAP protocol
specification and how it will migrate to RTL.

## 1. Goals

The reference codec exists to:

1. **Eliminate ambiguity in the protocol spec.** Prose specifications are
   imprecise; running code is not. Where the spec and this code disagree,
   either the spec or the code is wrong, and we fix one of them.
2. **Generate test vectors for RTL.** Once stable, this codebase emits the
   golden vectors that the SystemVerilog implementation must match.
3. **Provide the OBG host-side library.** The OTAP Bridge Gateway has a
   software side that does FIX↔OTAP transcoding, session management, and
   schema dispatch. That code links against `otap-codec`.
4. **Make the architecture inspectable.** Customers and patent reviewers can
   read Rust; they cannot read RTL. The reference codec is the artifact we
   show them.

## 2. Crate graph

```
                       ┌─────────────────┐
                       │   otap-core     │   Transient, dimensions, errors
                       └────────┬────────┘
                                │
        ┌───────────────────────┼───────────────────────┐
        │                       │                       │
        ▼                       ▼                       ▼
 ┌─────────────┐       ┌─────────────┐         ┌─────────────┐
 │ otap-crypto │       │ otap-schema │         │  otap-sim   │
 │  (HMAC,     │       │  (OAM →     │         │ (Channel    │
 │ topo auth)  │       │   schemas)  │         │  + PMD)     │
 └──────┬──────┘       └──────┬──────┘         └──────┬──────┘
        │                     │                       │
        └──────────┬──────────┘                       │
                   ▼                                  │
            ┌─────────────┐                           │
            │ otap-codec  │ ◄──────────── otap-sim ───┤
            │  Encoder    │                           │
            │  Decoder    │                           │
            └──────┬──────┘                           │
                   │                                  │
                   ▼                                  │
            ┌─────────────┐                           │
            │  otap-obg   │ ◄────────────────────────┘
            │  WorkRequest│
            │  SoftDriver │
            └──────┬──────┘
                   ▼
            ┌─────────────┐
            │  otap-cli   │   end-to-end demo
            └─────────────┘
```

Dependencies flow downward only. `otap-core` has no dependencies on the rest
of the workspace and could be replaced with a `no_std`-clean version for
embedded targets.

## 3. The Transient as the unit of correctness

A `Transient` is the smallest meaningful object in the protocol. Every
correctness property of OTAP is expressible as a property of Transients:

- **Routability:** A Transient's `wavelength` field uniquely identifies its
  destination on a passively-routed network.
- **Authenticity:** A Transient's `polarization` field, given the shared
  secret and the rest of the Transient, must verify under
  `derive_trajectory == observed`.
- **Type-correctness:** A Transient's `payload` length must equal
  `SchemaId::resolve(oam).payload_bytes()`.
- **Causality:** A session's Transients arrive in monotonic sequence order
  on `(wavelength, oam)`.

The reference codec enforces all of these at the type level where possible
and at runtime where not.

## 4. The parallel-decode structure

### 4.1 Why it matters

In a conventional protocol stack, each layer's output is the next layer's
input. TCP cannot start until IP has computed the destination; TLS cannot
start until TCP has reassembled the segment. This is the source of the
"OSI tax" — N layers × N μs per layer.

OTAP's design property is that the five decode operations are *independent*.
Given the input Transient, none of them takes another's output. They can run
concurrently on the FPGA, terminating in the same clock edge.

### 4.2 How the Rust codec preserves the property

`Decoder::decode` reads the five dimensions in textual order, but its
*data-flow graph* is a fan-out from the Transient to five independent
operations. Compilers (including LLVM that backs rustc) will schedule these
freely as long as no operation reads another's result.

The structural witness: `DecodeReport` has one field per dimension, each
populated by an expression that reads only from the input `Transient` and
the decoder's static config. There are no chained `?` operators that thread
the result of one check into another.

This is not just a Rust idiom — it is the design constraint that the RTL
must inherit. The SystemVerilog `decoder` module will have five concurrent
`always_comb` blocks, one per dimension, producing five outputs that meet
at the same register.

### 4.3 The schema dispatch caveat

OAM mode (D4) selects which schema is used to decode the amplitude payload
(D1). In that sense D4 *does* gate D1. But:

- D4 reaches its result combinationally from the OAM detector output (a
  3-bit value from the photonic frontend).
- D1 decode is implemented as N parallel pipelines, one per registered
  schema, all running on the same payload bits. The D4 result selects
  which pipeline's output is emitted.

So the apparent dependency is a mux, not a serial step. The longest path is
still one clock at modest frequencies.

## 5. The polarization-trajectory authenticator

### 5.1 Sample-match mode

Given the shared secret K and the Transient's `(λ, OAM, seq, payload)`, the
trajectory is `derive_trajectory(K, ctx)`. This is a deterministic
expansion of `HMAC-SHA256(K, ‖domain-sep ‖ λ ‖ OAM ‖ seq ‖ len ‖ payload)`
into `TRAJECTORY_SAMPLES` Stokes vectors on the unit Poincaré sphere.

The receiver re-computes the expected trajectory from the recovered payload
and compares sample-by-sample. Any modification to the payload — or to any
other context field — produces a uniformly-random trajectory in the receiver
view, which fails the comparison with overwhelming probability.

Security: under the standard HMAC-SHA256 assumptions, an attacker without K
who modifies the payload produces a trajectory that has no better than
2⁻ⁿ chance of matching the expected one, where n ≈ the number of
distinguishable Stokes states across the trajectory. For 16 samples at
even 10-bit Stokes resolution per axis this is well over 2⁻⁴⁸⁰.

### 5.2 Topological mode

The above scheme assumes the receiver can *observe* the trajectory as
transmitted — i.e., that fiber-induced polarization transforms have been
compensated. On long-haul links, PMD applies a slowly-varying unitary
rotation to every Stokes vector. The sample-match check fails not because
of an attack but because the receiver sees `R · trajectory` for some unknown
rotation R.

The fix: authenticate on a *topological invariant* of the trajectory that
survives R. The winding number of a closed loop on the Poincaré sphere
around a fixed axis is such an invariant: unitary rotations of the sphere
preserve homotopy class.

In `dimensions.rs`, `PolarizationTrajectory::winding_number_s3` computes the
discrete winding number of the trajectory's projection onto the s1–s2
equatorial plane. The transmitter chooses the trajectory's winding number
based on the HMAC output (e.g., `tag[0] mod K` for K distinguishable values);
the receiver computes the winding number of the observed trajectory and
compares.

PMD-invariance: any rigid rotation of the Poincaré sphere maps closed loops
to closed loops with the same winding number around the (rotated) axis. The
receiver can either know the rotated axis (from a slow probe channel) or use
a rotation-invariant scalar like the linking number with respect to a
reference loop.

Security tradeoff: the topological scheme has a smaller authenticator space
(K winding-number values vs. 2ⁿ Stokes-resolution values). It is therefore
appropriate when:
- PMD compensation is unreliable, AND
- Multi-Transient aggregation provides the security margin (e.g., 1000
  consecutive Transients with K=32 gives 32^1000 effective space, more than
  enough).

The two modes are complementary, not exclusive. A receiver can require both
to pass.

## 6. Migration to RTL

### 6.1 Module-level mapping

| Rust crate / type             | SystemVerilog module                  |
|-------------------------------|---------------------------------------|
| `Encoder::encode`             | `otap_tx_pipeline`                    |
| `Decoder::decode`             | `otap_rx_pipeline`                    |
| `derive_trajectory`           | `traj_derive` (HMAC-SHA256 macro)     |
| `Schema::encode/decode`       | `schema_<name>_codec` per OAM mode    |
| `AnySchemaValue::decode`      | `schema_mux` (driven by OAM detector) |
| `PoincareRotation::apply`     | Not synthesized — channel model only  |

### 6.2 Bit-exactness contract

For every test vector in `otap-codec/tests/`, the corresponding RTL must
produce the same output bits given the same input bits. The cosim harness
runs both implementations against the same vector set and diffs.

The one place we expect the RTL to differ is in the bit-packed wire format
(see equity_order.rs notes). The reference is byte-aligned for readability;
the FPGA can use bit-packing for one-time savings of a few bytes per
Transient. This is a *layout* difference, not a *protocol* difference —
both must produce identical Schema values after decode.

### 6.3 Pipeline staging

The Rust `Decoder::decode` runs as a single function. The FPGA pipeline
will be staged:

- **Stage 1 — Photonic frontend:** ADC samples → I/Q for amplitude,
  Stokes vectors for polarization, OAM detector index, μT preamble matched
  filter. All five dimensions emerge in parallel after ~5 ns.
- **Stage 2 — Authentication and schema dispatch:** Trajectory verification
  and schema lookup run combinationally. Authenticated payload bytes are
  routed to the selected schema decoder. ~10 ns.
- **Stage 3 — Application output:** The decoded schema struct is written
  to a DMA descriptor on the host PCIe bus. ~5 ns.

Total: < 20 ns receive-side latency at endpoint, matching the spec §6.2
budget.

## 7. The host-side OBG library — MODQ

`otap-obg` implements the host-side surface of the OTAP Bridge Gateway: a
Memory-mapped OTAP Descriptor Queue (MODQ) that gives applications an
RDMA-NIC-shaped API for OTAP. The motivation: existing kernel-bypass
stacks (DPDK, RDMA verbs, AWS EFA, NVIDIA Doca) all converge on a
ring-buffer-of-descriptors model. Applications already speak this idiom;
OTAP should not require them to learn a new one.

### 7.1 Surface

Three types form the public surface:

- `WorkRequest` — a 64-byte descriptor an application posts to transmit.
  Carries schema ID, destination wavelength, payload length, and a
  user cookie that round-trips back on completion.
- `SendQueue` — ring buffer of pending work requests.
- `CompletionQueue` — ring buffer of received Transients, each delivered
  as a typed `AnySchemaValue` with auth status.

### 7.2 Software stand-in vs. hardware

`SoftDriver` implements the queue-draining logic in pure software: it
pops requests from the send queue, encodes through `otap-codec`,
transits through `otap-sim::Channel`, decodes, and posts completions.

The application-visible API is identical to what the real PCIe-attached
OBG exposes. Application code written against `SoftDriver` runs unchanged
against silicon when the FPGA driver replaces it. This is the same
pattern used by NVIDIA's BlueField / DOCA development workflow: develop
against a software model, deploy against silicon.

### 7.3 What the FPGA replaces

In hardware, `SoftDriver::tick` is replaced by three independent
hardware paths:

| `SoftDriver` operation        | FPGA equivalent                              |
|-------------------------------|----------------------------------------------|
| Poll `send.pop()`             | PCIe DMA read of head pointer + descriptor    |
| `encoder.encode_raw(...)`     | `otap_tx_pipeline` hardware module            |
| `channel.transmit(...)`       | Physical photonic frontend                    |
| `decoder.decode(...)`         | `otap_rx_pipeline` hardware module            |
| `cq.push(...)`                | PCIe DMA write to completion ring + doorbell  |

The descriptor format defined in `WorkRequest` is the wire format
between host and FPGA. It is `#[repr(C)]` so the byte layout matches
across the PCIe boundary without any marshaling.

## 8. What this codebase deliberately does not solve

- **Confidentiality.** OTAP authenticates and integrity-protects but does
  not encrypt by default. Add `aes-gcm` on D1 if the application requires it.
- **Wavelength provisioning.** The Lambda Registry is configuration that
  lives outside the protocol. A separate management-plane tool will own it.
- **Multi-domain routing.** Cross-operator handoff is a federation problem,
  not a codec problem. See spec §4 for the topology assumptions.
- **Concurrency.** The reference codec is single-threaded. An async wrapper
  for the OBG is a separate crate.
- **Photonics.** Optical modulation, demodulation, and channel physics are
  modeled abstractly in `otap-sim`. The real photonic transmitter and
  receiver are out-of-scope hardware.

