# OTAP RTL — SystemVerilog Implementation

This directory holds the SystemVerilog hooks that integrate with the Rust
reference codec. The architecture decision in Phase 1 was to make the
*CSR register file* the shared contract between hardware and software:
both sides read identical 32-bit register layouts, so most of the protocol
logic can be exercised purely in Rust before any FPGA work begins.

## Current contents

- **`otap_csr_map.svh`** — register-offset constants, bit-field layouts,
  and table-indexing macros. Mirrors `crates/otap-csr/src/lib.rs` byte-for-
  byte. The `OTAP_CSR_MAP_VERSION` localparam is the version-skew detector
  for host software at boot.
- **`otap_csr_block.sv`** — synthesizable CSR storage (256 × 32-bit words)
  with combinational decode for the schema-payload and OAM-to-schema
  lookup tables. Designed for AXI4-Lite-style write/read ports.

Together these are the minimum spec required to begin TX/RX pipeline RTL
work. Everything outside `otap_csr_*` (the actual encode/decode pipelines)
is still TBD.

## Migration plan

1. **Freeze the reference.** Achieve test coverage > 95% in the Rust
   codebase for all encode/decode paths and at least three schemas.
2. **Emit golden vectors.** Add a `tools/gen-vectors` binary that emits
   pair files of `(input_bytes, expected_output_bytes)` for every
   parameterized scenario.
3. **Cosim harness.** A Python or Rust tool that feeds vectors to the RTL
   testbench (via cocotb) and diffs against the reference output.
4. **Bring-up order:**
   1. `traj_derive.sv` — HMAC-SHA256 in hardware. Verify against
      `otap_crypto::derive_trajectory` test vectors.
   2. `schema_<name>_codec.sv` — schema encoders/decoders per OAM mode.
   3. `otap_rx_pipeline.sv` — full receive-side parallel decode.
   4. `otap_tx_pipeline.sv` — full transmit-side encode.
   5. Top-level integration with photonic frontend stubs.

## Toolchain assumptions

- Vivado 2024.1+ for synthesis.
- Verilator for fast simulation.
- cocotb for testbench integration.
- Xilinx Versal Premium VP1902 evaluation board (or equivalent) for hardware
  bring-up.

## Hardware bill of materials (Phase 1)

Per the project plan in the protocol risk-mitigation document:
- Versal Premium VP1902 FPGA (hardened crypto, HBM, 112G SerDes)
- 800ZR coherent pluggables (custom DSP firmware, not stock OFEC)
- Polarization controller (General Photonics PolaRITE or equivalent)
- Reference probe laser (narrow-linewidth tunable, any vendor)
- 100–150 km leased metro dark fiber for the bring-up testbed

