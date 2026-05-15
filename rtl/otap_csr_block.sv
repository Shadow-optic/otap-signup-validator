// SPDX-License-Identifier: Proprietary
// otap_csr_block.sv — OTAP CSR storage + AXI4-Lite-style decode.
//
// Reference implementation. Implements the register file described in
// otap_csr_map.svh as flip-flop storage with combinational decode.
// At only 256 × 32-bit words (1 KiB), this is well within LUT-RAM budget on
// Versal/Kintex and does not require BRAM. For higher-throughput register
// banks (e.g., per-flow counters), upgrade to BRAM with the standard
// (* ram_style = "block" *) attribute as documented in the FPGA dev notes.
//
// Synthesizable. Verified by simulation against the Rust reference register
// file (see test/csr_xref_test.py — golden vectors).
//
// Not Vivado-specific; should also synthesize cleanly under Quartus and
// Yosys-nextpnr.

`include "otap_csr_map.svh"

module otap_csr_block #(
    parameter int ADDR_WIDTH = 12  // 4 KiB max addressable; we use 1 KiB.
) (
    input  logic                       clk,
    input  logic                       rstn,

    // Host write port (AXI4-Lite-style, simplified — no AW/W split).
    input  logic                       wr_en,
    input  logic [ADDR_WIDTH-1:0]      wr_addr,
    input  logic [31:0]                wr_data,

    // Host read port.
    input  logic                       rd_en,
    input  logic [ADDR_WIDTH-1:0]      rd_addr,
    output logic [31:0]                rd_data,

    // Decoded outputs to the TX/RX pipelines.
    output logic [15:0]                cfg_wavelength,
    output logic [31:0]                cfg_ctrl,
    output logic [31:0]                cfg_auth_tolerance,
    output logic                       cfg_secret_valid,
    output logic [255:0]               cfg_secret,

    // Schema/OAM table read ports (combinational; one read per cycle per port).
    input  logic [4:0]                 schema_id_lookup,    // 0..31
    output logic [15:0]                schema_payload_bytes,

    input  logic signed [4:0]          oam_lookup,          // -16..+16
    output logic [7:0]                 oam_to_schema_id
);

  // ============================================================================
  // Storage — flat 32-bit word array.
  // ============================================================================
  logic [31:0] reg_file [OTAP_CSR_SPACE_WORDS-1:0];

  // ============================================================================
  // Write path — synchronous, alignment-checked at compile time.
  // ============================================================================
  always_ff @(posedge clk) begin
    if (!rstn) begin
      for (int i = 0; i < OTAP_CSR_SPACE_WORDS; i++) begin
        reg_file[i] <= 32'h0;
      end
    end else if (wr_en && wr_addr[1:0] == 2'b00 && wr_addr < OTAP_CSR_SPACE_BYTES) begin
      reg_file[wr_addr[ADDR_WIDTH-1:2]] <= wr_data;
    end
  end

  // ============================================================================
  // Read path — synchronous (1-cycle latency).
  // ============================================================================
  always_ff @(posedge clk) begin
    if (rd_en && rd_addr[1:0] == 2'b00 && rd_addr < OTAP_CSR_SPACE_BYTES) begin
      rd_data <= reg_file[rd_addr[ADDR_WIDTH-1:2]];
    end else begin
      rd_data <= 32'h0;
    end
  end

  // ============================================================================
  // Decoded outputs to pipelines.
  // ============================================================================
  assign cfg_wavelength       = reg_file[OTAP_REG_WAVELENGTH      >> 2][15:0];
  assign cfg_ctrl             = reg_file[OTAP_REG_CTRL            >> 2];
  assign cfg_auth_tolerance   = reg_file[OTAP_REG_AUTH_TOLERANCE  >> 2];
  assign cfg_secret_valid     = reg_file[OTAP_REG_SECRET_VALID    >> 2][0];

  // Pack the 256-bit secret. Word 0 → bits [255:224] (big-endian).
  genvar k;
  generate
    for (k = 0; k < OTAP_SECRET_WORDS; k++) begin : g_secret_pack
      assign cfg_secret[(255 - k*32) -: 32] = reg_file[(OTAP_REG_SECRET_BASE + k*4) >> 2];
    end
  endgenerate

  // ============================================================================
  // Schema-payload table lookup.
  // ============================================================================
  // schema_id_lookup is the 1..31 handle; entry 2k → low half, 2k+1 → high half.
  logic [31:0] schema_word;
  always_comb begin
    schema_word = reg_file[(OTAP_REG_SCHEMA_PAYLOAD_BASE >> 2) + (schema_id_lookup >> 1)];
    if (schema_id_lookup[0]) begin
      schema_payload_bytes = schema_word[31:16];
    end else begin
      schema_payload_bytes = schema_word[15:0];
    end
  end

  // ============================================================================
  // OAM → schema-id lookup.
  // ============================================================================
  // Index = oam_lookup + 16. 4 entries per word, 8 bits per entry.
  logic [5:0] oam_idx;
  logic [31:0] oam_word;
  logic [1:0]  oam_byte_in_word;
  always_comb begin
    oam_idx          = $unsigned(oam_lookup + 6'sd16);
    oam_word         = reg_file[(OTAP_REG_OAM_TABLE_BASE >> 2) + (oam_idx >> 2)];
    oam_byte_in_word = oam_idx[1:0];
    case (oam_byte_in_word)
      2'd0: oam_to_schema_id = oam_word[ 7: 0];
      2'd1: oam_to_schema_id = oam_word[15: 8];
      2'd2: oam_to_schema_id = oam_word[23:16];
      2'd3: oam_to_schema_id = oam_word[31:24];
    endcase
  end

endmodule
