// otap_rob_bitweave.v — Pipelined Bit-Woven ROB Commit Scanner

module otap_rob_bitweave #(
    parameter DATA_WIDTH  = 1024,
    parameter STRB_WIDTH  = DATA_WIDTH / 8,
    parameter SID_WIDTH   = 16,
    parameter ROB_DEPTH   = 256,
    parameter ADDR_WIDTH  = $clog2(ROB_DEPTH)
) (
    input wire clk,
    input wire rst,

    input  wire [DATA_WIDTH-1:0] s_axis_tdata,
    input  wire [STRB_WIDTH-1:0] s_axis_tkeep,
    input  wire                  s_axis_tvalid,
    input  wire                  s_axis_tlast,
    output wire                  s_axis_tready,

    output reg  [DATA_WIDTH-1:0] m_axis_tdata,
    output reg  [STRB_WIDTH-1:0] m_axis_tkeep,
    output reg                   m_axis_tvalid,
    output reg                   m_axis_tlast,
    input  wire                  m_axis_tready
);

    reg [SID_WIDTH-1:0] ingress_sid;

    always @(posedge clk) begin
        if (rst)
            ingress_sid <= 0;
        else if (s_axis_tvalid && s_axis_tready && s_axis_tlast)
            ingress_sid <= ingress_sid + 1;
    end

    reg [DATA_WIDTH-1:0] rob_data [0:ROB_DEPTH-1];
    reg [STRB_WIDTH-1:0] rob_keep [0:ROB_DEPTH-1];
    reg                  rob_valid[0:ROB_DEPTH-1];
    reg [ROB_DEPTH-1:0]  sid_plane [0:SID_WIDTH-1];

    wire [ADDR_WIDTH-1:0] write_idx = ingress_sid[ADDR_WIDTH-1:0];

    integer b;

    always @(posedge clk) begin
        if (rst) begin
            for (integer i = 0; i < ROB_DEPTH; i = i + 1) begin
                rob_valid[i] <= 1'b0;
            end
            for (b = 0; b < SID_WIDTH; b = b + 1) begin
                sid_plane[b] <= {ROB_DEPTH{1'b0}};
            end
        end else if (s_axis_tvalid && s_axis_tready && s_axis_tlast) begin
            rob_data[write_idx] <= s_axis_tdata;
            rob_keep[write_idx] <= s_axis_tkeep;
            rob_valid[write_idx] <= 1'b1;

            for (b = 0; b < SID_WIDTH; b = b + 1) begin
                if (ingress_sid[b])
                    sid_plane[b][write_idx] <= 1'b1;
                else
                    sid_plane[b][write_idx] <= 1'b0;
            end
        end
    end

    // =========================================================================
    // PIPELINED Bit-woven parallel commit scan
    // =========================================================================
    reg [SID_WIDTH-1:0] commit_ptr;
    
    // Stage 1: Pipelined Match Vector
    reg [ROB_DEPTH-1:0] match_vector_pipe;
    reg                 match_valid_pipe;
    
    integer j_idx, p_idx;
    reg [ROB_DEPTH-1:0] temp_match;
    reg [ROB_DEPTH-1:0] valid_mask;
    
    always @(posedge clk) begin
        if (rst) begin
            match_vector_pipe <= {ROB_DEPTH{1'b0}};
            match_valid_pipe  <= 1'b0;
        end else begin
            // Build valid mask
            for (j_idx = 0; j_idx < ROB_DEPTH; j_idx = j_idx + 1)
                valid_mask[j_idx] = rob_valid[j_idx];

            // Combinational AND/ANDNOT over bit-planes
            temp_match = {ROB_DEPTH{1'b1}};
            for (p_idx = 0; p_idx < SID_WIDTH; p_idx = p_idx + 1) begin
                if (commit_ptr[p_idx])
                    temp_match = temp_match & sid_plane[p_idx];
                else
                    temp_match = temp_match & ~sid_plane[p_idx];
            end
            
            // Register the result
            match_vector_pipe <= temp_match & valid_mask;
            match_valid_pipe  <= |(temp_match & valid_mask);
        end
    end

    // Stage 2: Priority Encoder (Combinational from Pipeline Register)
    reg [ADDR_WIDTH-1:0] match_idx;
    integer k;
    always @(*) begin
        match_idx = 0;
        for (k = ROB_DEPTH - 1; k >= 0; k = k - 1) begin
            if (match_vector_pipe[k])
                match_idx = k[ADDR_WIDTH-1:0];
        end
    end

    // =========================================================================
    // Commit logic
    // =========================================================================
    always @(posedge clk) begin
        if (rst) begin
            commit_ptr    <= 0;
            m_axis_tvalid <= 1'b0;
            m_axis_tlast  <= 1'b0;
        end else if (m_axis_tready || !m_axis_tvalid) begin
            if (match_valid_pipe) begin
                m_axis_tdata  <= rob_data[match_idx];
                m_axis_tkeep  <= rob_keep[match_idx];
                m_axis_tvalid <= 1'b1;
                m_axis_tlast  <= 1'b1;
                
                rob_valid[match_idx] <= 1'b0;
                for (b = 0; b < SID_WIDTH; b = b + 1)
                    sid_plane[b][match_idx] <= 1'b0;
                
                commit_ptr <= commit_ptr + 1;
            end else begin
                m_axis_tvalid <= 1'b0;
                m_axis_tlast  <= 1'b0;
            end
        end
    end

    assign s_axis_tready = !rob_valid[write_idx];

endmodule
