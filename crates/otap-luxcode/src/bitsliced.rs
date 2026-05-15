//! Bit-sliced GF(2^10) syndrome evaluator for batches of 512 codewords.
//!
//! Each GF(2^10) value is decomposed into 10 separate *bit planes*. One bit
//! plane is a single `__m512i` (512 bits) holding the k-th bit of the
//! corresponding value across 512 codewords. So:
//!
//!   * "the value at position i in codeword c, bit k" lives in
//!     `received_bits[k]` at lane c.
//!   * "accumulator j (for root α^j), bit k, across all 512 codewords"
//!     lives in `accs[j * 10 + k]`.
//!
//! Multiplication by the constant α^j becomes a fixed 10×10 GF(2) linear
//! map: `out_plane[k] = XOR over { in_plane[i] : MUL_BY_ALPHA[j][k] has
//! bit i set }`. Those matrices are computed at Zig comptime by
//! `zig/gf1024.zig` and emitted as the `MUL_BY_ALPHA` const in
//! `gf1024.rs`. Because every row mask is a compile-time constant in the
//! generated code, the optimizer prunes the unused XOR branches: each
//! row turns into between 1 and 10 `vpxorq` ops on `__m512i` —
//! amortized over 512 codewords.
//!
//! State is 160 ZMMs (16 accumulators × 10 planes ≈ 10 KB), spills to L1.

use crate::{MUL_BY_ALPHA, RS_PARITY_LEN};

#[cfg(target_arch = "x86_64")]
use std::arch::x86_64::*;

/// Codewords per batch. Fixed at 512 because that's exactly one ZMM of
/// bits → every bit plane fits in one `__m512i` and every plane-XOR is a
/// single `vpxorq`.
pub const BITSLICED_BATCH: usize = 512;

// ============================================================================
// Inner macros
// ============================================================================
//
// `row!` and `step!` are macros (rather than generic functions) so the
// row-mask lookup `MUL_BY_ALPHA[j][k]` happens with literal `j` and `k`
// during macro expansion. That gives the optimizer a `const` mask in scope,
// and the 10 `if mask & (1 << i) != 0` branches fold away wherever the bit
// is zero — leaving just the XOR fan-in that is actually needed.

#[cfg(target_arch = "x86_64")]
macro_rules! row {
    ($mask:expr, $in:expr, $r:expr) => {{
        const M: u16 = $mask;
        let mut out = $r;
        if (M & (1 << 0)) != 0 { out = _mm512_xor_si512(out, $in[0]); }
        if (M & (1 << 1)) != 0 { out = _mm512_xor_si512(out, $in[1]); }
        if (M & (1 << 2)) != 0 { out = _mm512_xor_si512(out, $in[2]); }
        if (M & (1 << 3)) != 0 { out = _mm512_xor_si512(out, $in[3]); }
        if (M & (1 << 4)) != 0 { out = _mm512_xor_si512(out, $in[4]); }
        if (M & (1 << 5)) != 0 { out = _mm512_xor_si512(out, $in[5]); }
        if (M & (1 << 6)) != 0 { out = _mm512_xor_si512(out, $in[6]); }
        if (M & (1 << 7)) != 0 { out = _mm512_xor_si512(out, $in[7]); }
        if (M & (1 << 8)) != 0 { out = _mm512_xor_si512(out, $in[8]); }
        if (M & (1 << 9)) != 0 { out = _mm512_xor_si512(out, $in[9]); }
        out
    }};
}

#[cfg(target_arch = "x86_64")]
macro_rules! step {
    ($j:literal, $accs:expr, $received:expr) => {{
        let base: usize = $j * 10;
        // Snapshot input bit planes — writes below need the pre-update values
        // across all k.
        let in_planes: [__m512i; 10] = [
            $accs[base], $accs[base + 1], $accs[base + 2], $accs[base + 3], $accs[base + 4],
            $accs[base + 5], $accs[base + 6], $accs[base + 7], $accs[base + 8], $accs[base + 9],
        ];
        $accs[base + 0] = row!(MUL_BY_ALPHA[$j][0], in_planes, $received[0]);
        $accs[base + 1] = row!(MUL_BY_ALPHA[$j][1], in_planes, $received[1]);
        $accs[base + 2] = row!(MUL_BY_ALPHA[$j][2], in_planes, $received[2]);
        $accs[base + 3] = row!(MUL_BY_ALPHA[$j][3], in_planes, $received[3]);
        $accs[base + 4] = row!(MUL_BY_ALPHA[$j][4], in_planes, $received[4]);
        $accs[base + 5] = row!(MUL_BY_ALPHA[$j][5], in_planes, $received[5]);
        $accs[base + 6] = row!(MUL_BY_ALPHA[$j][6], in_planes, $received[6]);
        $accs[base + 7] = row!(MUL_BY_ALPHA[$j][7], in_planes, $received[7]);
        $accs[base + 8] = row!(MUL_BY_ALPHA[$j][8], in_planes, $received[8]);
        $accs[base + 9] = row!(MUL_BY_ALPHA[$j][9], in_planes, $received[9]);
    }};
}

#[cfg(target_arch = "x86_64")]
macro_rules! bit_plane {
    ($shift:literal, $zmms:expr) => {{
        let bp: [u32; 16] = [
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[0])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[1])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[2])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[3])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[4])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[5])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[6])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[7])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[8])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[9])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[10])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[11])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[12])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[13])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[14])) as u32,
            _mm512_movepi16_mask(_mm512_slli_epi16::<$shift>($zmms[15])) as u32,
        ];
        _mm512_loadu_si512(bp.as_ptr() as *const __m512i)
    }};
}

// ============================================================================
// Public API
// ============================================================================

/// Evaluate RS syndromes for exactly [`BITSLICED_BATCH`] codewords in
/// parallel using bit-sliced GF(2^10) arithmetic.
///
/// # Input layout
///
/// `codewords_columnar` must be column-major: the symbol at position `i`
/// in codeword `c` lives at index `i * BITSLICED_BATCH + c`. Each column
/// is then a contiguous 1024-byte span — exactly 16 ZMM loads, no gathers.
///
/// If your codewords are row-major, see [`transpose_to_columnar`].
///
/// # Output
///
/// `syndromes[c][j]` gets the j-th syndrome of codeword `c`, identical
/// bit-for-bit to `rs_syndromes_scalar(&codewords[c])`.
///
/// # Safety
///
/// Requires `avx512f` + `avx512bw`. The dispatcher
/// [`rs_syndromes_bitsliced_dispatched`] runtime-checks before calling.
#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "avx512f,avx512bw")]
pub unsafe fn rs_syndromes_bitsliced(
    codewords_columnar: &[u16],
    cw_len: usize,
    syndromes: &mut [[u16; RS_PARITY_LEN]],
) {
    assert_eq!(codewords_columnar.len(), BITSLICED_BATCH * cw_len);
    assert_eq!(syndromes.len(), BITSLICED_BATCH);
    debug_assert_eq!(RS_PARITY_LEN, 16);

    // 16 accumulators × 10 bit planes.
    let mut accs = [_mm512_setzero_si512(); 160];

    for i in 0..cw_len {
        let received = load_position_as_bitplanes(codewords_columnar, i);

        step!(0, accs, received);
        step!(1, accs, received);
        step!(2, accs, received);
        step!(3, accs, received);
        step!(4, accs, received);
        step!(5, accs, received);
        step!(6, accs, received);
        step!(7, accs, received);
        step!(8, accs, received);
        step!(9, accs, received);
        step!(10, accs, received);
        step!(11, accs, received);
        step!(12, accs, received);
        step!(13, accs, received);
        step!(14, accs, received);
        step!(15, accs, received);
    }

    transpose_accumulators(&accs, syndromes);
}

/// Runtime-dispatched entry. AVX-512 path when available; scalar loop
/// fallback otherwise. Safe to call on any target.
pub fn rs_syndromes_bitsliced_dispatched(
    codewords_columnar: &[u16],
    cw_len: usize,
    syndromes: &mut [[u16; RS_PARITY_LEN]],
) {
    assert_eq!(codewords_columnar.len(), BITSLICED_BATCH * cw_len);
    assert_eq!(syndromes.len(), BITSLICED_BATCH);

    #[cfg(target_arch = "x86_64")]
    {
        if is_x86_feature_detected!("avx512f") && is_x86_feature_detected!("avx512bw") {
            // SAFETY: runtime check just succeeded.
            unsafe { rs_syndromes_bitsliced(codewords_columnar, cw_len, syndromes) };
            return;
        }
    }
    // Fallback: re-row-major each codeword and run scalar.
    let mut tmp = vec![0u16; cw_len];
    for c in 0..BITSLICED_BATCH {
        for i in 0..cw_len {
            tmp[i] = codewords_columnar[i * BITSLICED_BATCH + c];
        }
        syndromes[c] = crate::rs_syndromes_scalar(&tmp);
    }
}

/// Convert row-major `rows[c][i]` to the column-major layout the bit-sliced
/// kernel expects. Single pass.
pub fn transpose_to_columnar(rows: &[Vec<u16>], out: &mut Vec<u16>) {
    assert_eq!(rows.len(), BITSLICED_BATCH);
    let cw_len = rows[0].len();
    out.clear();
    out.resize(BITSLICED_BATCH * cw_len, 0);
    for c in 0..BITSLICED_BATCH {
        debug_assert_eq!(rows[c].len(), cw_len);
        for i in 0..cw_len {
            out[i * BITSLICED_BATCH + c] = rows[c][i];
        }
    }
}

// ============================================================================
// Internals
// ============================================================================

/// Load one column (position `i` across 512 codewords) and split it into
/// 10 bit planes. Each plane is a `__m512i` where lane `c` carries the
/// k-th bit of `codeword[c][i]`.
#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "avx512f,avx512bw")]
#[inline]
unsafe fn load_position_as_bitplanes(columnar: &[u16], i: usize) -> [__m512i; 10] {
    let base = columnar.as_ptr().add(i * BITSLICED_BATCH);

    let zmms: [__m512i; 16] = [
        _mm512_loadu_si512(base.add(0 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(1 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(2 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(3 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(4 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(5 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(6 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(7 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(8 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(9 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(10 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(11 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(12 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(13 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(14 * 32) as *const __m512i),
        _mm512_loadu_si512(base.add(15 * 32) as *const __m512i),
    ];

    [
        bit_plane!(15, zmms), // bit 0 → shift left 15 puts it in the sign position
        bit_plane!(14, zmms), // bit 1
        bit_plane!(13, zmms),
        bit_plane!(12, zmms),
        bit_plane!(11, zmms),
        bit_plane!(10, zmms),
        bit_plane!(9, zmms),
        bit_plane!(8, zmms),
        bit_plane!(7, zmms),
        bit_plane!(6, zmms), // bit 9
    ]
}

/// Final transpose: 160 bit planes back into 512 codewords × 16 syndromes.
#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "avx512f")]
#[inline]
unsafe fn transpose_accumulators(
    accs: &[__m512i; 160],
    syndromes: &mut [[u16; RS_PARITY_LEN]],
) {
    let mut planes_u64 = [0u64; 80]; // 10 planes × 8 u64 each
    for j in 0..16 {
        for k in 0..10 {
            let dst = planes_u64.as_mut_ptr().add(k * 8) as *mut __m512i;
            _mm512_storeu_si512(dst, accs[j * 10 + k]);
        }
        for c in 0..BITSLICED_BATCH {
            let word_idx = c / 64;
            let bit_idx = c % 64;
            let mut s: u16 = 0;
            for k in 0..10 {
                let plane_bit = ((planes_u64[k * 8 + word_idx] >> bit_idx) & 1) as u16;
                s |= plane_bit << k;
            }
            syndromes[c][j] = s;
        }
    }
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use crate::rs_syndromes_scalar;

    #[cfg(target_arch = "x86_64")]
    #[test]
    fn bitsliced_matches_scalar() {
        if !(is_x86_feature_detected!("avx512f") && is_x86_feature_detected!("avx512bw")) {
            eprintln!("AVX-512BW not available — skipping bit-sliced test");
            return;
        }
        let cw_len = 64usize;
        let data_len = cw_len - RS_PARITY_LEN;

        let mut rows: Vec<Vec<u16>> = Vec::with_capacity(BITSLICED_BATCH);
        for c in 0..BITSLICED_BATCH {
            let data: Vec<u16> = (0..data_len)
                .map(|i| (c as u16 ^ (i as u16 * 17)) & 0x3FF)
                .collect();
            rows.push(encode_with_parity(&data));
            assert_eq!(rows[c].len(), cw_len);
        }
        for c in (0..BITSLICED_BATCH).step_by(13) {
            rows[c][3] ^= 0x1F1;
        }

        let mut columnar: Vec<u16> = Vec::new();
        transpose_to_columnar(&rows, &mut columnar);

        let mut got = vec![[0u16; RS_PARITY_LEN]; BITSLICED_BATCH];
        unsafe { rs_syndromes_bitsliced(&columnar, cw_len, &mut got) };

        for c in 0..BITSLICED_BATCH {
            let expected = rs_syndromes_scalar(&rows[c]);
            assert_eq!(got[c], expected, "mismatch on codeword {}", c);
        }
    }

    /// Throughput micro-bench. Run with:
    ///   cargo test --release -p otap-luxcode -- --nocapture rs_syndromes_bitsliced_throughput
    #[cfg(target_arch = "x86_64")]
    #[test]
    fn rs_syndromes_bitsliced_throughput() {
        if !(is_x86_feature_detected!("avx512f") && is_x86_feature_detected!("avx512bw")) {
            eprintln!("AVX-512BW not available — skipping");
            return;
        }
        use std::time::Instant;

        let cw_len = 255usize;
        let data_len = cw_len - RS_PARITY_LEN;

        let rows: Vec<Vec<u16>> = (0..BITSLICED_BATCH)
            .map(|c| {
                let data: Vec<u16> = (0..data_len)
                    .map(|i| (c as u16 + i as u16 * 31) & 0x3FF)
                    .collect();
                encode_with_parity(&data)
            })
            .collect();
        let mut columnar = Vec::new();
        transpose_to_columnar(&rows, &mut columnar);

        let mut out = vec![[0u16; RS_PARITY_LEN]; BITSLICED_BATCH];

        for _ in 0..10 {
            unsafe { rs_syndromes_bitsliced(&columnar, cw_len, &mut out) };
        }

        let iters = 200usize;
        let t = Instant::now();
        let mut acc: u16 = 0;
        for _ in 0..iters {
            unsafe { rs_syndromes_bitsliced(&columnar, cw_len, &mut out) };
            acc ^= out[0][0];
        }
        let dt = t.elapsed();

        let bits = (iters as u64) * (BITSLICED_BATCH as u64) * (cw_len as u64)
            * (crate::GF1024_FIELD_BITS as u64);
        let gbps = bits as f64 / dt.as_secs_f64() / 1e9;
        let per_cw_ns = dt.as_nanos() as f64 / (iters as f64) / (BITSLICED_BATCH as f64);

        eprintln!(
            "bit-sliced syndromes (batch=512, RS(255, 239) over GF(2^10)):\n  aggregate    = {:>8.3} Gbps\n  per codeword = {:>6.1} ns\n  (acc={})",
            gbps, per_cw_ns, acc
        );

        // Sanity floor — bit-sliced should beat the single-stream
        // AVX-512+VPCLMULQDQ path (~1.6 Gbps on this host).
        assert!(
            gbps > 8.0,
            "bit-sliced aggregate {:.2} Gbps is below the 8 Gbps regression floor",
            gbps
        );
    }

    fn encode_with_parity(data: &[u16]) -> Vec<u16> {
        use crate::{gf_mul, RS_GENERATOR};
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
                codeword[i + j] ^= gf_mul(coef, g);
            }
        }
        codeword[..data.len()].copy_from_slice(data);
        codeword
    }
}
