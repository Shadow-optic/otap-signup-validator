//! Lux 10B/12B line code + GF(1024) Reed-Solomon FEC.
//!
//! The codebook and Galois-field tables are produced at *Zig* comptime by
//! `zig/luxcode.zig` and `zig/gf1024.zig`. If the existence proof fails
//! (fewer than 1087 valid disparity pairs), the Zig file does not compile,
//! and `make zig-codegen` errors. The Rust side here just consumes the
//! resulting `.rodata` tables.
//!
//! The runtime API has three layers:
//!
//! * **Codebook lookup** (`encode_pair`) — pure table indexing.
//! * **Scalar Reed-Solomon syndrome evaluator** (`rs_syndromes_scalar`) —
//!   one root at a time, portable to any target.
//! * **AVX2 syndrome evaluator** (`rs_syndromes_avx2`) — eight roots at a
//!   time via `_mm256_i32gather_epi32` over the comptime exp/log tables.
//!   Requires `target_feature = "avx2"`; gated on
//!   `is_x86_feature_detected!("avx2")` at call time via
//!   [`rs_syndromes`].
//!
//! Throughput motivation: the inner loop of RS decoding is the per-root
//! Horner evaluation. Doing eight roots in parallel collapses the 16-symbol
//! syndrome computation from 16 scalar passes to 2 vector passes.

#![allow(clippy::needless_range_loop)]

mod codebook;
mod gf1024;

pub use codebook::{LuxPair, LUX_CODEBOOK, LUX_PAIR_COUNT, LUX_PLUS2_PAIR_COUNT, LUX_ZERO_PAIR_COUNT};
pub use gf1024::{
    GF1024_EXP, GF1024_EXP_U32, GF1024_FIELD_BITS, GF1024_LOG, GF1024_LOG_U32, GF1024_NONZERO,
    GF1024_ORDER, GF1024_PRIM_POLY, RS_GENERATOR, RS_PARITY_LEN,
};

// ============================================================================
// Codebook
// ============================================================================

/// Encode a 10-bit data symbol to a `(plus, minus)` 12-bit line-code pair.
///
/// `symbol` must be `< LUX_PAIR_COUNT`. Production code should also pick
/// between `.plus` and `.minus` based on the current running disparity;
/// this helper just hands you the pair.
#[inline]
pub fn encode_pair(symbol: u16) -> LuxPair {
    LUX_CODEBOOK[symbol as usize]
}

// ============================================================================
// GF(1024) primitives
// ============================================================================

/// GF(1024) multiplication via log/exp tables.
#[inline]
pub fn gf_mul(a: u16, b: u16) -> u16 {
    if a == 0 || b == 0 {
        return 0;
    }
    let s = GF1024_LOG[a as usize] as u32 + GF1024_LOG[b as usize] as u32;
    GF1024_EXP[(s % GF1024_NONZERO as u32) as usize]
}

/// `α^i` accessor with modular wrap.
#[inline]
pub fn gf_exp(i: u32) -> u16 {
    GF1024_EXP[(i % GF1024_NONZERO as u32) as usize]
}

// ============================================================================
// Reed-Solomon syndromes — scalar reference
// ============================================================================

/// Evaluate the 16-syndrome polynomial of `received` via scalar Horner.
///
/// `S_j = r(α^j)` for `j ∈ [0, 16)`. Index 0 in `received` is the most
/// significant coefficient (highest degree).
pub fn rs_syndromes_scalar(received: &[u16]) -> [u16; RS_PARITY_LEN] {
    let mut s = [0u16; RS_PARITY_LEN];
    for (j, sj) in s.iter_mut().enumerate() {
        let alpha_j = GF1024_EXP[j];
        let mut acc: u16 = 0;
        for &r in received {
            acc = gf_mul(acc, alpha_j) ^ r;
        }
        *sj = acc;
    }
    s
}

// ============================================================================
// Reed-Solomon syndromes — AVX2 SIMD (eight roots at a time)
// ============================================================================

/// Public entry point: picks the fastest available syndrome evaluator. On
/// Sapphire-Rapids-class x86_64 (AVX-512 + VPCLMULQDQ) the path is gather-
/// free and uses parallel carryless multiplication. All paths produce
/// bit-identical results.
pub fn rs_syndromes(received: &[u16]) -> [u16; RS_PARITY_LEN] {
    #[cfg(target_arch = "x86_64")]
    {
        if is_x86_feature_detected!("avx512f")
            && is_x86_feature_detected!("avx512bw")
            && is_x86_feature_detected!("avx512vl")
            && is_x86_feature_detected!("vpclmulqdq")
        {
            // SAFETY: runtime feature check just succeeded.
            return unsafe { rs_syndromes_avx512_clmul(received) };
        }
        if is_x86_feature_detected!("avx2") {
            // SAFETY: runtime feature check just succeeded.
            return unsafe { rs_syndromes_avx2(received) };
        }
    }
    rs_syndromes_scalar(received)
}

/// AVX2 syndrome evaluator. Processes eight RS roots per pass and runs
/// twice (j=0..8, j=8..16).
///
/// # Safety
/// Requires AVX2. Call only after `is_x86_feature_detected!("avx2")` or
/// when compiled with `-C target-feature=+avx2`.
#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "avx2")]
pub unsafe fn rs_syndromes_avx2(received: &[u16]) -> [u16; RS_PARITY_LEN] {
    use std::arch::x86_64::*;

    debug_assert_eq!(RS_PARITY_LEN, 16);

    // `exp_ptr` and `log_ptr` are read-only base pointers for AVX2 gathers.
    // VPGATHERDD only addresses 32-bit elements, so we use the u32-widened
    // mirrors emitted by the Zig codegen (the values themselves fit in u16
    // but live in u32 lanes here for the gather).
    let exp_ptr = GF1024_EXP_U32.as_ptr() as *const i32;
    let log_ptr = GF1024_LOG_U32.as_ptr() as *const i32;
    let nonzero = _mm256_set1_epi32(GF1024_NONZERO as i32);

    let mut out = [0u16; RS_PARITY_LEN];

    for half in 0..2 {
        // Roots α^{base..base+8}. Loaded as the *log* values base..base+7
        // (which is just the offsets themselves, since log[α^k] == k).
        let base = (half * 8) as i32;
        let log_alpha = _mm256_setr_epi32(
            base,
            base + 1,
            base + 2,
            base + 3,
            base + 4,
            base + 5,
            base + 6,
            base + 7,
        );

        let mut acc = _mm256_setzero_si256();

        for &r in received {
            // Horner step: acc = (acc * α^j) ⊕ r
            //   step A: detect acc == 0 (in which case mul result is 0)
            //   step B: gather log(acc) per lane
            //   step C: log_prod = log(acc) + log_alpha (mod NONZERO)
            //   step D: gather exp(log_prod) per lane
            //   step E: xor with r (broadcast scalar)

            // Lanes where acc is zero produce zero; keep a mask.
            let zero_mask = _mm256_cmpeq_epi32(acc, _mm256_setzero_si256());

            // Gather log[acc]. acc fits in 0..1023 so the &0x3FF guard is
            // belt-and-suspenders.
            let acc_idx = _mm256_and_si256(acc, _mm256_set1_epi32(0x3FF));
            let log_acc = _mm256_i32gather_epi32::<4>(log_ptr, acc_idx);

            // log_acc + log_alpha
            let mut sum = _mm256_add_epi32(log_acc, log_alpha);
            // Modulo NONZERO (==1023). The sum is at most 2*1022 = 2044, so
            // one conditional subtract suffices.
            let ge_n = _mm256_cmpgt_epi32(sum, _mm256_sub_epi32(nonzero, _mm256_set1_epi32(1)));
            sum = _mm256_sub_epi32(sum, _mm256_and_si256(ge_n, nonzero));

            // exp[sum]
            let prod = _mm256_i32gather_epi32::<4>(exp_ptr, sum);

            // Mask off lanes that had zero accumulator (prod := 0 there).
            let masked_prod = _mm256_andnot_si256(zero_mask, prod);

            // XOR with the scalar r (broadcast).
            let r_vec = _mm256_set1_epi32(r as i32);
            acc = _mm256_xor_si256(masked_prod, r_vec);
        }

        // Store 8 syndromes from this half. AVX2 has no clean 32→16 narrowing
        // store, so we extract to a stack buffer.
        let mut tmp = [0i32; 8];
        _mm256_storeu_si256(tmp.as_mut_ptr() as *mut __m256i, acc);
        for i in 0..8 {
            out[half * 8 + i] = tmp[i] as u16;
        }
    }

    out
}

// ============================================================================
// Reed-Solomon syndromes — AVX-512 + VPCLMULQDQ (gather-free, all 16 roots)
// ============================================================================

/// Single-pass syndrome evaluator using VPCLMULQDQ for parallel carryless
/// multiplication and Barrett-style reduction mod x^10 + x^3 + 1.
///
/// Eliminates the AVX2 path's per-step `_mm256_i32gather_epi32` (two of them,
/// one per log+exp roundtrip) in favour of:
///
/// * Four `vpclmulqdq` per Horner step (4 carryless mults per instruction
///   on Sapphire-Rapids = 16 GF(2^10) products per step, throughput-bound
///   on the CLMUL port).
/// * Two folds of `(p_hi << 3) ⊕ p_hi ⊕ p_lo` to reduce each 19-bit
///   product down to the canonical 10-bit field element.
///
/// All 16 RS roots ride in 4 ZMM accumulators (4 × 128-bit lanes each, one
/// accumulator per lane), so the codeword is traversed exactly once.
///
/// # Safety
/// Requires `avx512f`, `avx512bw`, `avx512vl`, `vpclmulqdq`. Call only after
/// `is_x86_feature_detected!` confirms all four, or under matching
/// `target_feature`.
#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "avx512f,avx512bw,avx512vl,vpclmulqdq")]
pub unsafe fn rs_syndromes_avx512_clmul(received: &[u16]) -> [u16; RS_PARITY_LEN] {
    use std::arch::x86_64::*;

    debug_assert_eq!(RS_PARITY_LEN, 16);

    // Pack each α^j into the low 64 bits of one 128-bit lane of a ZMM. The
    // four ZMMs together carry α^0..α^15. We pair them with accumulator
    // ZMMs of identical shape and call VPCLMULQDQ with imm=0x00 (low × low).
    let alpha_a = _mm512_setr_epi64(
        GF1024_EXP[0] as i64, 0,
        GF1024_EXP[1] as i64, 0,
        GF1024_EXP[2] as i64, 0,
        GF1024_EXP[3] as i64, 0,
    );
    let alpha_b = _mm512_setr_epi64(
        GF1024_EXP[4] as i64, 0,
        GF1024_EXP[5] as i64, 0,
        GF1024_EXP[6] as i64, 0,
        GF1024_EXP[7] as i64, 0,
    );
    let alpha_c = _mm512_setr_epi64(
        GF1024_EXP[8] as i64, 0,
        GF1024_EXP[9] as i64, 0,
        GF1024_EXP[10] as i64, 0,
        GF1024_EXP[11] as i64, 0,
    );
    let alpha_d = _mm512_setr_epi64(
        GF1024_EXP[12] as i64, 0,
        GF1024_EXP[13] as i64, 0,
        GF1024_EXP[14] as i64, 0,
        GF1024_EXP[15] as i64, 0,
    );

    let mask10 = _mm512_set1_epi64(0x3FF);

    let mut acc_a = _mm512_setzero_si512();
    let mut acc_b = _mm512_setzero_si512();
    let mut acc_c = _mm512_setzero_si512();
    let mut acc_d = _mm512_setzero_si512();

    for &r in received {
        let r_vec = _mm512_set1_epi64(r as i64);

        // 4 × VPCLMULQDQ — 16 GF(2^10) products in flight.
        let p_a = _mm512_clmulepi64_epi128::<0x00>(acc_a, alpha_a);
        let p_b = _mm512_clmulepi64_epi128::<0x00>(acc_b, alpha_b);
        let p_c = _mm512_clmulepi64_epi128::<0x00>(acc_c, alpha_c);
        let p_d = _mm512_clmulepi64_epi128::<0x00>(acc_d, alpha_d);

        // Reduce mod x^10 + x^3 + 1. Each product is at most a 19-bit
        // polynomial; two folds by (x^3+1) bring it into [0, 1023].
        let r_a = reduce_gf1024(p_a, mask10);
        let r_b = reduce_gf1024(p_b, mask10);
        let r_c = reduce_gf1024(p_c, mask10);
        let r_d = reduce_gf1024(p_d, mask10);

        acc_a = _mm512_xor_si512(r_a, r_vec);
        acc_b = _mm512_xor_si512(r_b, r_vec);
        acc_c = _mm512_xor_si512(r_c, r_vec);
        acc_d = _mm512_xor_si512(r_d, r_vec);
    }

    // Each accumulator ZMM holds 4 values at u64 positions 0, 2, 4, 6.
    let mut out = [0u16; RS_PARITY_LEN];
    let mut buf = [0u64; 8];

    _mm512_storeu_si512(buf.as_mut_ptr() as *mut __m512i, acc_a);
    out[0] = buf[0] as u16;
    out[1] = buf[2] as u16;
    out[2] = buf[4] as u16;
    out[3] = buf[6] as u16;

    _mm512_storeu_si512(buf.as_mut_ptr() as *mut __m512i, acc_b);
    out[4] = buf[0] as u16;
    out[5] = buf[2] as u16;
    out[6] = buf[4] as u16;
    out[7] = buf[6] as u16;

    _mm512_storeu_si512(buf.as_mut_ptr() as *mut __m512i, acc_c);
    out[8] = buf[0] as u16;
    out[9] = buf[2] as u16;
    out[10] = buf[4] as u16;
    out[11] = buf[6] as u16;

    _mm512_storeu_si512(buf.as_mut_ptr() as *mut __m512i, acc_d);
    out[12] = buf[0] as u16;
    out[13] = buf[2] as u16;
    out[14] = buf[4] as u16;
    out[15] = buf[6] as u16;

    out
}

/// GF(1024) Barrett reduction on a ZMM whose lanes are 19-bit polynomial
/// products in the low 32 bits of each 128-bit slot.
///
/// p mod (x^10 + x^3 + 1) using two folds by (x^3 + 1):
///   p1 = (p & 0x3FF) ⊕ (p_hi9 << 3) ⊕ p_hi9      // up to 12 bits
///   r  = (p1 & 0x3FF) ⊕ (p1_hi << 3) ⊕ p1_hi    // exactly 10 bits
#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "avx512f,avx512bw,avx512vl")]
#[inline]
unsafe fn reduce_gf1024(p: std::arch::x86_64::__m512i, mask10: std::arch::x86_64::__m512i) -> std::arch::x86_64::__m512i {
    use std::arch::x86_64::*;
    // First fold
    let p_lo = _mm512_and_si512(p, mask10);
    let p_hi = _mm512_srli_epi64::<10>(p);
    let p_hi_sl3 = _mm512_slli_epi64::<3>(p_hi);
    let p1 = _mm512_xor_si512(_mm512_xor_si512(p_lo, p_hi_sl3), p_hi);
    // Second fold
    let p1_lo = _mm512_and_si512(p1, mask10);
    let p1_hi = _mm512_srli_epi64::<10>(p1);
    let p1_hi_sl3 = _mm512_slli_epi64::<3>(p1_hi);
    _mm512_xor_si512(_mm512_xor_si512(p1_lo, p1_hi_sl3), p1_hi)
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn codebook_count_matches_zig_proof() {
        assert_eq!(LUX_PAIR_COUNT, LUX_CODEBOOK.len());
        assert!(LUX_PAIR_COUNT >= 1087, "Zig proved this; sanity check it");
    }

    #[test]
    fn codebook_plus_minus_are_balanced() {
        // For ±2 pairs, popcount(plus) == 7 and popcount(minus) == 5.
        // For self-pairs (disparity-0), plus == minus and popcount == 6.
        let mut plus2 = 0usize;
        let mut zero = 0usize;
        for p in LUX_CODEBOOK.iter() {
            if p.plus == p.minus {
                assert_eq!(p.plus.count_ones(), 6, "self-pair must be popcount-6");
                zero += 1;
            } else {
                assert_eq!(p.plus.count_ones(), 7, "+ word must be popcount-7");
                assert_eq!(p.minus.count_ones(), 5, "- word must be popcount-5");
                assert_eq!(!p.plus & 0xFFF, p.minus, "minus must be the 12-bit complement of plus");
                plus2 += 1;
            }
        }
        assert_eq!(plus2, LUX_PLUS2_PAIR_COUNT);
        assert_eq!(zero, LUX_ZERO_PAIR_COUNT);
    }

    #[test]
    fn codebook_words_are_unique() {
        use std::collections::HashSet;
        let mut seen = HashSet::new();
        for p in LUX_CODEBOOK.iter() {
            assert!(seen.insert(p.plus), "duplicate plus word: 0x{:03X}", p.plus);
            if p.plus != p.minus {
                assert!(
                    seen.insert(p.minus),
                    "duplicate minus word: 0x{:03X}",
                    p.minus
                );
            }
        }
    }

    #[test]
    fn gf1024_field_round_trip() {
        // exp(log(x)) == x for all nonzero x
        for x in 1..GF1024_ORDER {
            let lx = GF1024_LOG[x as usize] as usize;
            assert_eq!(GF1024_EXP[lx], x, "exp(log({})) != {}", x, x);
        }
    }

    #[test]
    fn gf_mul_associative_on_sample() {
        // (a * b) * c == a * (b * c) for a few nontrivial samples.
        let cases = [(2u16, 3u16, 5u16), (7, 11, 13), (255, 511, 1023), (42, 100, 200)];
        for (a, b, c) in cases {
            assert_eq!(
                gf_mul(gf_mul(a, b), c),
                gf_mul(a, gf_mul(b, c)),
                "GF(1024) mul not associative at ({}, {}, {})",
                a,
                b,
                c
            );
        }
    }

    #[test]
    fn rs_generator_has_alpha0_through_alpha15_as_roots() {
        // g(α^j) == 0 for j ∈ [0, 16). The generator is stored low-degree
        // first (RS_GENERATOR[i] is the coefficient of x^i), so Horner runs
        // from high index down to low.
        for j in 0..RS_PARITY_LEN {
            let alpha_j = GF1024_EXP[j];
            let mut acc: u16 = 0;
            for &c in RS_GENERATOR.iter().rev() {
                acc = gf_mul(acc, alpha_j) ^ c;
            }
            assert_eq!(acc, 0, "RS generator does not vanish at α^{}", j);
        }
    }

    #[test]
    fn rs_syndromes_codeword_is_zero() {
        // Build a codeword [data || parity] over a small message and confirm
        // syndromes are all zero (which is the receiver-side check).
        let data: Vec<u16> = (1..=8u16).collect(); // 8-symbol message
        let codeword = encode_with_parity(&data);
        let s = rs_syndromes_scalar(&codeword);
        for (j, sj) in s.iter().enumerate() {
            assert_eq!(*sj, 0, "syndrome[{}] != 0 for clean codeword", j);
        }
    }

    #[test]
    fn rs_syndromes_single_error_is_nonzero() {
        let data: Vec<u16> = (1..=8u16).collect();
        let mut codeword = encode_with_parity(&data);
        // Flip one symbol.
        codeword[3] ^= 0x123;
        let s = rs_syndromes_scalar(&codeword);
        assert!(s.iter().any(|&x| x != 0), "single error not detected");
    }

    #[cfg(target_arch = "x86_64")]
    #[test]
    fn rs_syndromes_avx2_matches_scalar() {
        if !is_x86_feature_detected!("avx2") {
            eprintln!("AVX2 not available — skipping");
            return;
        }
        // Several inputs: clean codeword, single error, random-ish payload.
        let data: Vec<u16> = (1..=32u16).collect();
        let mut codeword = encode_with_parity(&data);

        // SAFETY: runtime check above.
        let scalar = rs_syndromes_scalar(&codeword);
        let avx = unsafe { rs_syndromes_avx2(&codeword) };
        assert_eq!(scalar, avx, "AVX2 and scalar disagree on clean codeword");

        codeword[5] ^= 0x2AB;
        codeword[20] ^= 0x399;
        let scalar2 = rs_syndromes_scalar(&codeword);
        let avx2 = unsafe { rs_syndromes_avx2(&codeword) };
        assert_eq!(scalar2, avx2, "AVX2 and scalar disagree on errored codeword");
    }

    #[cfg(target_arch = "x86_64")]
    #[test]
    fn rs_syndromes_avx512_matches_scalar() {
        if !(is_x86_feature_detected!("avx512f")
            && is_x86_feature_detected!("avx512bw")
            && is_x86_feature_detected!("avx512vl")
            && is_x86_feature_detected!("vpclmulqdq"))
        {
            eprintln!("AVX-512 + VPCLMULQDQ not available — skipping");
            return;
        }
        let data: Vec<u16> = (1..=64u16).collect();
        let mut codeword = encode_with_parity(&data);

        let scalar = rs_syndromes_scalar(&codeword);
        let avx512 = unsafe { rs_syndromes_avx512_clmul(&codeword) };
        assert_eq!(scalar, avx512, "AVX-512 disagrees with scalar (clean)");

        // Inject errors at a couple of positions.
        codeword[3] ^= 0x1A5;
        codeword[40] ^= 0x2F7;
        codeword[71] ^= 0x0C9;
        let scalar2 = rs_syndromes_scalar(&codeword);
        let avx512_2 = unsafe { rs_syndromes_avx512_clmul(&codeword) };
        assert_eq!(scalar2, avx512_2, "AVX-512 disagrees with scalar (errored)");
    }

    #[cfg(target_arch = "x86_64")]
    #[test]
    fn rs_syndromes_avx512_matches_avx2() {
        if !(is_x86_feature_detected!("avx2")
            && is_x86_feature_detected!("avx512f")
            && is_x86_feature_detected!("vpclmulqdq"))
        {
            eprintln!("Need both AVX2 and AVX-512+VPCLMULQDQ — skipping");
            return;
        }
        for n in [16usize, 64, 128, 239, 255] {
            let data: Vec<u16> = (0..n as u16).collect();
            let codeword = encode_with_parity(&data);
            let avx2 = unsafe { rs_syndromes_avx2(&codeword) };
            let avx512 = unsafe { rs_syndromes_avx512_clmul(&codeword) };
            assert_eq!(avx2, avx512, "AVX2 and AVX-512 disagree at n={}", n);
        }
    }

    /// Throughput comparison across scalar / AVX2 / AVX-512+VPCLMULQDQ on
    /// a 255-symbol codeword (the most common RS(255, 239) shape over
    /// GF(1024)). Prints to stderr; run with
    ///   cargo test --release -p otap-luxcode -- --nocapture rs_syndromes_throughput
    #[cfg(target_arch = "x86_64")]
    #[test]
    fn rs_syndromes_throughput() {
        if !is_x86_feature_detected!("avx2") {
            eprintln!("AVX2 not available — skipping throughput test");
            return;
        }
        use std::time::Instant;

        let data: Vec<u16> = (0..239u16).collect();
        let codeword = encode_with_parity(&data);

        let warmup = 1_000;
        let iters = 200_000;
        let bits_per_iter: u64 = (codeword.len() as u64) * GF1024_FIELD_BITS as u64;

        // Warm-up all paths.
        for _ in 0..warmup {
            let _ = rs_syndromes_scalar(&codeword);
            let _ = unsafe { rs_syndromes_avx2(&codeword) };
        }
        let have_avx512 = is_x86_feature_detected!("avx512f")
            && is_x86_feature_detected!("avx512bw")
            && is_x86_feature_detected!("avx512vl")
            && is_x86_feature_detected!("vpclmulqdq");
        if have_avx512 {
            for _ in 0..warmup {
                let _ = unsafe { rs_syndromes_avx512_clmul(&codeword) };
            }
        }

        let mut acc: u16 = 0;

        let t0 = Instant::now();
        for _ in 0..iters {
            let s = rs_syndromes_scalar(&codeword);
            acc ^= s[0];
        }
        let scalar_elapsed = t0.elapsed();
        let scalar_gbps =
            (bits_per_iter * iters as u64) as f64 / scalar_elapsed.as_secs_f64() / 1e9;

        let t1 = Instant::now();
        for _ in 0..iters {
            let s = unsafe { rs_syndromes_avx2(&codeword) };
            acc ^= s[0];
        }
        let avx2_elapsed = t1.elapsed();
        let avx2_gbps = (bits_per_iter * iters as u64) as f64 / avx2_elapsed.as_secs_f64() / 1e9;

        let avx512_gbps = if have_avx512 {
            let t2 = Instant::now();
            for _ in 0..iters {
                let s = unsafe { rs_syndromes_avx512_clmul(&codeword) };
                acc ^= s[0];
            }
            let avx512_elapsed = t2.elapsed();
            (bits_per_iter * iters as u64) as f64 / avx512_elapsed.as_secs_f64() / 1e9
        } else {
            f64::NAN
        };

        eprintln!(
            "syndrome rate (RS(255,239) over GF(1024)):\n  scalar    = {:>8.3} Gbps\n  AVX2      = {:>8.3} Gbps  ({:.2}x scalar)\n  AVX512+   = {:>8.3} Gbps  ({:.2}x scalar, {:.2}x AVX2)\n  (acc={})",
            scalar_gbps,
            avx2_gbps, avx2_gbps / scalar_gbps,
            avx512_gbps, avx512_gbps / scalar_gbps, avx512_gbps / avx2_gbps,
            acc
        );

        // AVX2 should not regress vs scalar. 0.8x tolerates jitter.
        assert!(
            avx2_gbps > scalar_gbps * 0.8,
            "AVX2 ({:.2} Gbps) regressed vs scalar ({:.2} Gbps)",
            avx2_gbps,
            scalar_gbps
        );
        if have_avx512 {
            // The whole point of VPCLMULQDQ is to beat AVX2.
            assert!(
                avx512_gbps > avx2_gbps * 1.2,
                "AVX-512+VPCLMULQDQ ({:.2} Gbps) is not at least 1.2x AVX2 ({:.2} Gbps)",
                avx512_gbps,
                avx2_gbps
            );
        }
    }

    fn encode_with_parity(data: &[u16]) -> Vec<u16> {
        // Systematic RS encoding with codeword[0] = highest-degree symbol
        // (message), codeword[m..m+16] = parity (lowest degrees).
        //
        // Polynomial long division: at each pivot i, peel off the leading
        // coefficient and subtract coef * g(x), aligned so g(x)'s leading
        // term sits at codeword[i]. RS_GENERATOR is stored low-to-high, so
        // RS_GENERATOR[16 - j] is what lands at codeword[i + j].
        let mut codeword = vec![0u16; data.len() + RS_PARITY_LEN];
        codeword[..data.len()].copy_from_slice(data);

        for i in 0..data.len() {
            let coef = codeword[i];
            if coef == 0 {
                continue;
            }
            for j in 0..=RS_PARITY_LEN {
                let g = RS_GENERATOR[RS_PARITY_LEN - j];
                if g == 0 {
                    continue;
                }
                let prod = gf_mul(coef, g);
                codeword[i + j] ^= prod;
            }
        }
        // Restore data (the division left the high-degree positions cleared
        // in our accumulator — we want them back as the original message).
        codeword[..data.len()].copy_from_slice(data);
        codeword
    }
}
