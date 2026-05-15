//! Lux line code: a 10B/12B construction proven at Zig comptime.
//!
//! At comptime we:
//!   1. Enumerate all 4096 candidate 12-bit codewords.
//!   2. Filter by five constraints (see `passes`).
//!   3. Pair each disparity-+2 word with its disparity--2 complement.
//!   4. Include disparity-0 words as self-pairs.
//!   5. `@compileError` if the total pair count is below `REQUIRED_PAIRS`.
//!
//! If this file compiles, the spec is proven: there exists a Lux codebook with
//! at least REQUIRED_PAIRS valid ± pairs. The Rust side never re-checks; it
//! consumes the generated `codebook.rs`.
//!
//! Building: `zig run zig/luxcode.zig > crates/otap-luxcode/src/codebook.rs`
//! or use the orchestrating `make zig-codegen` target.

const std = @import("std");

// --- Codebook parameters ---------------------------------------------------

const WORD_BITS: u8 = 12;
const TOTAL_CODEWORDS: usize = 1 << WORD_BITS; // 4096

/// Required count of pairs in the final codebook.
///
/// 1024 data symbols (10-bit) plus a 63-entry K-control reservation, rounded
/// up to 1087 to leave headroom for runtime substitution under sustained
/// running-disparity imbalance.
const REQUIRED_PAIRS: usize = 1087;

const Pair = struct {
    plus: u12, // disparity-+2 codeword, OR disparity-0 self-pair
    minus: u12, // disparity--2 (1's complement of plus), OR equals plus for self-pairs
};

// --- Constraint primitives -------------------------------------------------

fn popcount12(w: u12) u8 {
    return @popCount(w);
}

/// Maximum run length of consecutive identical bits (0-runs and 1-runs alike).
fn maxRun(w: u12) u8 {
    var max_r: u8 = 1;
    var cur: u8 = 1;
    var prev: u1 = @truncate(w);
    var ww: u12 = w >> 1;
    var i: u8 = 1;
    while (i < WORD_BITS) : (i += 1) {
        const bit: u1 = @truncate(ww);
        if (bit == prev) {
            cur += 1;
        } else {
            if (cur > max_r) max_r = cur;
            cur = 1;
            prev = bit;
        }
        ww >>= 1;
    }
    if (cur > max_r) max_r = cur;
    return max_r;
}

/// Number of 0->1 and 1->0 transitions within the codeword.
fn transitions(w: u12) u8 {
    var t: u8 = 0;
    var prev: u1 = @truncate(w);
    var ww: u12 = w >> 1;
    var i: u8 = 1;
    while (i < WORD_BITS) : (i += 1) {
        const bit: u1 = @truncate(ww);
        if (bit != prev) t += 1;
        prev = bit;
        ww >>= 1;
    }
    return t;
}

/// K-control reservation: eight codewords reserved for OAM commas, scrambler
/// reset, and channel-establishment. Excluded from the data codebook.
fn isReservedK(w: u12) bool {
    return switch (w) {
        0xFC0, 0xF6C, 0xF3F, 0xCF3, 0x3FC, 0x3F3, 0x33C, 0x0FF => true,
        else => false,
    };
}

/// The K28.5 comma (and its complement). Used for word alignment; not data.
const K28_5: u12 = 0x3EB; // 0b0011_1110_1011
const K28_5_INV: u12 = ~K28_5;

/// Five constraints on a 12-bit codeword:
///   C1: popcount ∈ {5, 6, 7} (disparity ∈ {-2, 0, +2})
///   C2: max run length ≤ 5    (PLL pull-in margin)
///   C3: transitions ≥ 4       (clock recovery)
///   C4: not K28.5 or its complement
///   C5: not in the K-control reservation set
fn passes(w: u12) bool {
    const p = popcount12(w);
    if (p < 5 or p > 7) return false; // C1
    if (maxRun(w) > 5) return false; // C2
    if (transitions(w) < 4) return false; // C3
    if (w == K28_5 or w == K28_5_INV) return false; // C4
    if (isReservedK(w)) return false; // C5
    return true;
}

// --- Comptime codebook construction ----------------------------------------

/// The codebook itself, computed at comptime.
///
/// If the pair count is below `REQUIRED_PAIRS`, this declaration fails to
/// compile — there is no runtime check.
const Codebook = struct {
    pairs: [REQUIRED_PAIRS]Pair,
    plus2_count: u16, // disparity-+2 pairs (popcount 7 ↔ 5)
    zero_count: u16, // disparity-0 self-pairs (popcount 6)
};

const codebook: Codebook = blk: {
    @setEvalBranchQuota(4_000_000);

    var pairs: [REQUIRED_PAIRS * 2]Pair = undefined;
    var n_pairs: usize = 0;
    var n_plus2: u16 = 0;
    var n_zero: u16 = 0;

    // Disparity-+2 / -2 pairs.
    var i: u32 = 0;
    while (i < TOTAL_CODEWORDS) : (i += 1) {
        const w: u12 = @intCast(i);
        if (popcount12(w) != 7) continue;
        if (!passes(w)) continue;
        const c: u12 = ~w;
        if (!passes(c)) continue;
        if (n_pairs >= pairs.len) break;
        pairs[n_pairs] = .{ .plus = w, .minus = c };
        n_pairs += 1;
        n_plus2 += 1;
    }

    // Disparity-0 self-pairs.
    i = 0;
    while (i < TOTAL_CODEWORDS) : (i += 1) {
        const w: u12 = @intCast(i);
        if (popcount12(w) != 6) continue;
        if (!passes(w)) continue;
        if (n_pairs >= pairs.len) break;
        pairs[n_pairs] = .{ .plus = w, .minus = w };
        n_pairs += 1;
        n_zero += 1;
    }

    if (n_pairs < REQUIRED_PAIRS) {
        @compileError(std.fmt.comptimePrint(
            "luxcode: only {d} valid pairs, need {d}",
            .{ n_pairs, REQUIRED_PAIRS },
        ));
    }

    var final: [REQUIRED_PAIRS]Pair = undefined;
    var k: usize = 0;
    var final_plus2: u16 = 0;
    var final_zero: u16 = 0;
    while (k < REQUIRED_PAIRS) : (k += 1) {
        final[k] = pairs[k];
        if (final[k].plus == final[k].minus) {
            final_zero += 1;
        } else {
            final_plus2 += 1;
        }
    }
    break :blk .{
        .pairs = final,
        .plus2_count = final_plus2,
        .zero_count = final_zero,
    };
};

// --- Rust emitter ----------------------------------------------------------

pub fn main() !void {
    const out = std.io.getStdOut().writer();
    try out.print(
        \\// Generated by zig/luxcode.zig. DO NOT EDIT.
        \\//
        \\// 10B/12B Lux line code: {[count]d} disparity pairs.
        \\//
        \\// Existence proven at Zig comptime under five constraints
        \\// (see zig/luxcode.zig::passes). If this file is stale, run
        \\// `make zig-codegen` to regenerate from the comptime proof.
        \\
        \\#![allow(dead_code)]
        \\
        \\#[repr(C)]
        \\#[derive(Clone, Copy, Debug, PartialEq, Eq)]
        \\pub struct LuxPair {{
        \\    pub plus: u16,
        \\    pub minus: u16,
        \\}}
        \\
        \\pub const LUX_PAIR_COUNT: usize = {[count]d};
        \\
        \\/// Disparity-+2 / -2 pairs (popcount-7 word ↔ popcount-5 complement).
        \\pub const LUX_PLUS2_PAIR_COUNT: usize = {[p2]d};
        \\
        \\/// Disparity-0 self-pairs (popcount-6 words; plus == minus).
        \\pub const LUX_ZERO_PAIR_COUNT: usize = {[z]d};
        \\
        \\pub const LUX_CODEBOOK: [LuxPair; LUX_PAIR_COUNT] = [
        \\
    , .{
        .count = codebook.pairs.len,
        .p2 = codebook.plus2_count,
        .z = codebook.zero_count,
    });

    for (codebook.pairs) |p| {
        try out.print(
            "    LuxPair {{ plus: 0x{X:0>3}, minus: 0x{X:0>3} }},\n",
            .{ p.plus, p.minus },
        );
    }
    try out.print("];\n", .{});
}
