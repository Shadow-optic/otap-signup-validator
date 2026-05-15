// SPDX-License-Identifier: Proprietary
// otap_csr_map.svh — OTAP OBG CSR register map (v1)
//
// This header is the SystemVerilog mirror of `otap-csr/src/lib.rs`.
// ANY change here MUST be replicated in lib.rs and vice versa. The
// `OTAP_CSR_MAP_VERSION` define is bumped on every layout change so that
// host software can detect skew at boot via REG_VERSION reads.
//
// Conventions:
//   - All offsets are byte-addressed, 32-bit aligned.
//   - Multi-byte values that span multiple registers are big-endian when
//     viewed as a flat byte stream (word 0 → high-order bytes).
//   - Reads from out-of-range offsets return 0; writes are dropped silently.
//     The host driver is expected to never issue such accesses; production
//     debug builds should assert at the AXI4-Lite slave.

`ifndef OTAP_CSR_MAP_SVH
`define OTAP_CSR_MAP_SVH

  // ==========================================================================
  // Map version
  // ==========================================================================
  localparam logic [7:0] OTAP_CSR_MAP_VERSION = 8'h01;

  // ==========================================================================
  // Register offsets (byte-addressed)
  // ==========================================================================
  localparam int OTAP_REG_WAVELENGTH        = 'h000;  // 16-bit channel in low half
  localparam int OTAP_REG_CTRL              = 'h004;  // control flags
  localparam int OTAP_REG_AUTH_TOLERANCE    = 'h008;  // milliradians
  localparam int OTAP_REG_STATUS            = 'h00C;  // read-only flags

  // Shared secret: 8 × 32-bit words, 256 bits total, big-endian.
  localparam int OTAP_REG_SECRET_BASE       = 'h010;
  localparam int OTAP_SECRET_WORDS          = 8;

  // Bit 0 set when the SECRET registers are loaded.
  localparam int OTAP_REG_SECRET_VALID      = 'h030;

  // Schema-ID → payload-bytes table.
  // 32 entries × 16 bits = 512 bits = 16 × 32-bit words.
  // Entry layout per word: bits [15:0]  = entry 2k;
  //                        bits [31:16] = entry 2k+1.
  localparam int OTAP_REG_SCHEMA_PAYLOAD_BASE = 'h040;
  localparam int OTAP_SCHEMA_TABLE_ENTRIES    = 32;

  // OAM-mode → schema-ID table.
  // 33 entries × 8 bits = 264 bits → 8 × 32-bit words (last word's upper byte is unused).
  // Index = oam_charge + 16 (so charge range -16..+16 maps to 0..32).
  // Entry layout per word: byte i (i in 0..4) at bits [(i*8)+:8].
  localparam int OTAP_REG_OAM_TABLE_BASE     = 'h100;
  localparam int OTAP_OAM_TABLE_ENTRIES      = 33;

  // Peer wavelength table (host bookkeeping; not consulted by the TX/RX path).
  localparam int OTAP_REG_PEER_WAVELENGTH_BASE = 'h200;
  localparam int OTAP_PEER_TABLE_ENTRIES        = 16;

  // Total CSR space.
  localparam int OTAP_CSR_SPACE_BYTES = 'h400;
  localparam int OTAP_CSR_SPACE_WORDS = OTAP_CSR_SPACE_BYTES / 4;

  // ==========================================================================
  // CTRL register bits
  // ==========================================================================
  localparam logic [31:0] OTAP_CTRL_ENABLE_TX          = 32'h0000_0001;
  localparam logic [31:0] OTAP_CTRL_ENABLE_RX          = 32'h0000_0002;
  // Bit 2 selects authentication mode:
  //   0 = trajectory-match (compare polarization samples to derived trajectory)
  //   1 = topological (compare winding numbers; PMD-invariant)
  localparam logic [31:0] OTAP_CTRL_AUTH_TOPOLOGICAL   = 32'h0000_0004;

  // ==========================================================================
  // STATUS register bits (read-only)
  // ==========================================================================
  localparam logic [31:0] OTAP_STATUS_ARMED            = 32'h0000_0001;
  localparam logic [31:0] OTAP_STATUS_PEER_READY       = 32'h0000_0002;
  localparam logic [31:0] OTAP_STATUS_ERROR            = 32'h8000_0000;

  // ==========================================================================
  // Helper macros for table indexing
  // ==========================================================================
  // Word offset for schema-payload-table entry `id` (id in 1..32):
  `define OTAP_SCHEMA_PAYLOAD_WORD_OFFSET(id) (OTAP_REG_SCHEMA_PAYLOAD_BASE + ((id) >> 1) * 4)
  // Whether the entry lives in the high or low half of the word:
  `define OTAP_SCHEMA_PAYLOAD_HIGH_HALF(id)   (((id) & 1) == 1)

  // Word offset for OAM-table entry indexed by (charge + 16):
  `define OTAP_OAM_WORD_OFFSET(idx)           (OTAP_REG_OAM_TABLE_BASE + ((idx) >> 2) * 4)
  // Bit position within the word:
  `define OTAP_OAM_BIT_SHIFT(idx)             (((idx) & 3) * 8)

`endif // OTAP_CSR_MAP_SVH
