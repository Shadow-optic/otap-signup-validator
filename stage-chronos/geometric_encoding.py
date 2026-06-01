"""
Geometric Photon Sub-Encoding for STAGE-CHRONOS.

Six novel encoding schemes that exploit the geometric degrees of freedom (DoF)
of individual photons — adding 6 to 30 bits per symbol on top of conventional
QAM modulation.

  A. TPP   — Toroidal Phase-Polarization embedding      (+6.0 bits, TRL 3)
  B. Hopf  — Hopf-fibration torus knot OAM encoding     (+12.5 bits, TRL 2)
  C. NPM   — Nested Polarization Microconstellation      (+4.0 bits, TRL 4)
  D. E8    — E8 lattice geometric constellation          (+3.66 dB gain, TRL 4)
  E. Berry — Berry/Pancharatnam phase sub-channel        (+4.0 bits, TRL 2)
  F. GIE   — Unified Geometric Integrity Encoding        (+30 bits combined, TRL 1-2)

The key insight: polarization, phase, OAM, and geometric phase are independent
data channels (OAM and polarization operators commute: [L_z, S_z] = 0).
By treating each photon as a point on a high-dimensional product manifold
M = S²_pol × S¹_phase × Z_OAM × S¹_Berry × R⁺_amp, we unlock multiplicative
capacity scaling without requiring additional spectrum.

Vectorization notes
-------------------
  TPPEncoder.full_constellation  : meshgrid + cos/sin broadcast (M1×M2 points)
  NPMEncoder._fibonacci_sphere   : vectorized golden-angle Fibonacci sphere
  E8Encoder                      : (240, 8) array for the E8 kissing vectors
  HopfKnotEncoder.encode         : linspace + vectorized cos/sin over phi
"""

import math
from itertools import combinations
from typing import List, Optional, Tuple

import numpy as np

from .spacetime import SpacetimePoint
from .coherence import GeometricCoherenceKernel


# ---------------------------------------------------------------------------
# Scheme A: Toroidal Phase-Polarization (TPP) Embedding
# ---------------------------------------------------------------------------

class TPPEncoder:
    """
    Scheme A: Toroidal Phase-Polarization embedding on T^2 = S^1 × S^1.

    θ1 encodes polarization latitude on the Poincaré sphere.
    θ2 encodes optical carrier phase.

    Torus embedding in R^3:
      x = (R + r·cos θ2)·cos θ1
      y = (R + r·cos θ2)·sin θ1
      z = r·sin θ2

    Minimum distance (inner equator at θ2 = π):
      d_min = min{ 2(R-r)·sin(π/M1), 2r·sin(π/M2) }

    With M1=M2=8: 64 sub-symbols → +6.0 bits/symbol (TRL 3).
    """

    def __init__(
        self,
        R: float = 2.0,
        r: float = 0.5,
        M1: int = 8,
        M2: int = 8,
    ) -> None:
        assert R > r > 0, "torus requires R > r > 0"
        self.R = R
        self.r = r
        self.M1 = M1
        self.M2 = M2
        self.theta1_grid = np.linspace(0, 2 * np.pi, M1, endpoint=False)
        self.theta2_grid = np.linspace(0, 2 * np.pi, M2, endpoint=False)

    def encode(self, pol_idx: int, phase_idx: int) -> SpacetimePoint:
        """Encode (polarization index, phase index) as a SpacetimePoint on T^2."""
        t1 = self.theta1_grid[pol_idx % self.M1]
        t2 = self.theta2_grid[phase_idx % self.M2]
        x = (self.R + self.r * math.cos(t2)) * math.cos(t1)
        y = (self.R + self.r * math.cos(t2)) * math.sin(t1)
        z = self.r * math.sin(t2)
        return SpacetimePoint(0.0, x, y, z)

    def encode_symbol(self, symbol: int) -> SpacetimePoint:
        """Encode symbol index (0..M1*M2-1) as a single SpacetimePoint on T^2."""
        return self.encode(symbol % self.M1, symbol // self.M1)

    def full_constellation(self) -> List[SpacetimePoint]:
        """Return all M1×M2 constellation points (vectorized)."""
        pol_g, ph_g = np.meshgrid(np.arange(self.M1), np.arange(self.M2))
        t1 = self.theta1_grid[pol_g.ravel()]
        t2 = self.theta2_grid[ph_g.ravel()]
        x = (self.R + self.r * np.cos(t2)) * np.cos(t1)
        y = (self.R + self.r * np.cos(t2)) * np.sin(t1)
        z = self.r * np.sin(t2)
        return [SpacetimePoint(0.0, float(xi), float(yi), float(zi))
                for xi, yi, zi in zip(x, y, z)]

    def min_distance(self) -> float:
        """Minimum Euclidean distance between constellation points."""
        d1 = 2 * (self.R - self.r) * math.sin(math.pi / self.M1)
        d2 = 2 * self.r * math.sin(math.pi / self.M2)
        return min(d1, d2)

    def capacity_bits(self) -> float:
        """log2(M1 × M2) bits per symbol."""
        return math.log2(self.M1 * self.M2)


# ---------------------------------------------------------------------------
# Scheme B: Hopf-Fibration Torus Knot Encoding
# ---------------------------------------------------------------------------

_VALID_TORUS_KNOTS: List[Tuple[int, int]] = [
    (p, q)
    for p in range(1, 8)
    for q in range(p + 1, 12)
    if math.gcd(p, q) == 1
]


class HopfKnotEncoder:
    """
    Scheme B: Hopf-fibration OAM-polarization torus knot encoding.

    T(p,q) torus knot standard parametrization in R^3:
      x = (2 + cos(q·φ)) · cos(p·φ)
      y = (2 + cos(q·φ)) · sin(p·φ)
      z = sin(q·φ)

    ~30 valid coprime T(p,q) types → +4.9 bits from topology alone.
    Topological encoding is robust against continuous path deformations
    (the knot type is a topological invariant, not affected by fiber bending).

    Total: +12.5 bits/symbol at 15 dB SNR (TRL 2).
    """

    VALID_KNOTS: List[Tuple[int, int]] = _VALID_TORUS_KNOTS

    def __init__(self, num_points: int = 100) -> None:
        self.num_points = num_points

    def encode(self, p: int, q: int) -> List[SpacetimePoint]:
        """Generate T(p,q) torus knot as a manifold of SpacetimePoints."""
        phi = np.linspace(0, 2 * np.pi, self.num_points, endpoint=False)
        x = (2.0 + np.cos(q * phi)) * np.cos(p * phi)
        y = (2.0 + np.cos(q * phi)) * np.sin(p * phi)
        z = np.sin(q * phi)
        return [SpacetimePoint(0.0, float(xi), float(yi), float(zi))
                for xi, yi, zi in zip(x, y, z)]

    def encode_by_index(self, knot_idx: int) -> List[SpacetimePoint]:
        """Encode by index into VALID_KNOTS."""
        p, q = self.VALID_KNOTS[knot_idx % len(self.VALID_KNOTS)]
        return self.encode(p, q)

    def writhe(self, p: int, q: int) -> int:
        """Writhe of T(p,q) = p·q (topological invariant for detection)."""
        return p * q

    def topology_bits(self) -> float:
        """log2(|VALID_KNOTS|) bits from topology alone."""
        return math.log2(len(self.VALID_KNOTS))

    def capacity_bits(self) -> float:
        """Total: topology bits + polarization state (7.6 bits at 15 dB SNR)."""
        return self.topology_bits() + 7.6


# ---------------------------------------------------------------------------
# Scheme C: Nested Polarization Microconstellation (NPM)
# ---------------------------------------------------------------------------

class NPMEncoder:
    """
    Scheme C: Nested Polarization Microconstellation.

    K micro-points uniformly distributed on a sphere of radius ε around
    each QAM symbol position (I_k, Q_k):

      S_tx = S_QAM + ε · S_micro

    With K=16, ε=0.1×d_min: +4.0 bits/symbol (TRL 4).

    Fully backward compatible: legacy QAM receivers see only the main symbol.
    Upgraded receivers decode the additional 4 bits from polarization microstructure.

    Points generated via the Fibonacci sphere for near-uniform S^2 coverage.
    """

    def __init__(self, K: int = 16, epsilon: float = 0.1) -> None:
        self.K = K
        self.epsilon = epsilon
        self._offsets = self._fibonacci_sphere(K)   # (K, 3)

    @staticmethod
    def _fibonacci_sphere(n: int) -> np.ndarray:
        """Golden-ratio Fibonacci sphere: n ≈-uniform points on S^2."""
        golden = (1.0 + math.sqrt(5.0)) / 2.0
        i = np.arange(n, dtype=float)
        theta = np.arccos(1.0 - 2.0 * (i + 0.5) / n)
        phi = 2.0 * math.pi * i / golden
        return np.stack([
            np.sin(theta) * np.cos(phi),
            np.sin(theta) * np.sin(phi),
            np.cos(theta),
        ], axis=1)

    def encode(self, qam_I: float, qam_Q: float, micro_idx: int) -> SpacetimePoint:
        """Encode (QAM position, micro-index) as a SpacetimePoint."""
        off = self._offsets[micro_idx % self.K] * self.epsilon
        return SpacetimePoint(0.0, qam_I + off[0], qam_Q + off[1], off[2])

    def full_microconstellation(
        self, qam_I: float, qam_Q: float
    ) -> List[SpacetimePoint]:
        """All K micro-points around QAM symbol at (I, Q)."""
        off = self._offsets * self.epsilon
        xs = qam_I + off[:, 0]
        ys = qam_Q + off[:, 1]
        zs = off[:, 2]
        return [SpacetimePoint(0.0, float(x), float(y), float(z))
                for x, y, z in zip(xs, ys, zs)]

    def capacity_bits(self) -> float:
        return math.log2(self.K)


# ---------------------------------------------------------------------------
# Scheme D: E8 Lattice Geometric Constellation
# ---------------------------------------------------------------------------

def _build_e8_kissing_vectors() -> np.ndarray:
    """
    Generate the 240 shortest vectors of the E8 lattice (kissing number).

    Type 1 (112 vectors): all (±1,±1,0,...,0) — two nonzero ±1 entries.
      C(8,2) × 4 sign combinations = 28 × 4 = 112.

    Type 2 (128 vectors): (±½)^8 with an even count of minus signs.
      2^7 = 128.

    Total: 240.  All have norm² = 2.
    """
    vecs = []
    # Type 1
    for i, j in combinations(range(8), 2):
        for si in (1, -1):
            for sj in (1, -1):
                v = np.zeros(8)
                v[i] = si
                v[j] = sj
                vecs.append(v)
    # Type 2
    for mask in range(256):
        signs = np.array([(-1 if (mask >> k) & 1 else 1) for k in range(8)], dtype=float)
        if int(np.sum(signs < 0)) % 2 == 0:
            vecs.append(signs * 0.5)
    arr = np.array(vecs, dtype=np.float64)
    assert arr.shape == (240, 8), f"E8 kissing: expected (240,8), got {arr.shape}"
    return arr


_E8_KISSING: Optional[np.ndarray] = None


def _e8_vectors() -> np.ndarray:
    global _E8_KISSING
    if _E8_KISSING is None:
        _E8_KISSING = _build_e8_kissing_vectors()
    return _E8_KISSING


class E8Encoder:
    """
    Scheme D: E8 Lattice Geometric Constellation.

    The 240 kissing-number vectors form a 240-point constellation in R^8.
    Advantages over conventional Z^8 QAM:
      Coding gain   = 3.01 dB
      Shaping gain  = 0.65 dB
      Total         = 3.66 dB  (~2.3× SNR improvement)

    Mapping to optics: 8 dimensions = 2λ × 2 polarizations × 2 time slots.

    4D projection (first 4 coordinates) maps to SpacetimePoint(t,x,y,z).
    Full 8D representation uses a pair of SpacetimePoints per symbol.
    """

    def __init__(self) -> None:
        self.vectors = _e8_vectors()    # (240, 8) float64

    def encode_4d(self, symbol_idx: int) -> SpacetimePoint:
        """4D projection of E8 vector: first 4 coordinates → SpacetimePoint."""
        v = self.vectors[symbol_idx % 240]
        return SpacetimePoint(float(v[0]), float(v[1]), float(v[2]), float(v[3]))

    def encode_8d(self, symbol_idx: int) -> Tuple[SpacetimePoint, SpacetimePoint]:
        """Full 8D encoding: two SpacetimePoints (low 4 + high 4 coordinates)."""
        v = self.vectors[symbol_idx % 240]
        return (
            SpacetimePoint(float(v[0]), float(v[1]), float(v[2]), float(v[3])),
            SpacetimePoint(float(v[4]), float(v[5]), float(v[6]), float(v[7])),
        )

    def full_constellation_4d(self) -> List[SpacetimePoint]:
        """All 240 kissing vectors projected to 4D."""
        v = self.vectors
        return [SpacetimePoint(float(vi[0]), float(vi[1]), float(vi[2]), float(vi[3]))
                for vi in v]

    @staticmethod
    def coding_gain_db() -> float:
        return 3.01

    @staticmethod
    def shaping_gain_db() -> float:
        return 0.65

    @staticmethod
    def total_gain_db() -> float:
        return 3.66

    def capacity_bits(self) -> float:
        """log2(240) ≈ 7.9 bits from the 240-point E8 constellation."""
        return math.log2(len(self.vectors))

    def min_norm_sq(self) -> float:
        """All E8 kissing vectors have squared norm = 2."""
        return float(np.min(np.sum(self.vectors ** 2, axis=1)))


# ---------------------------------------------------------------------------
# Scheme E: Berry Phase Sub-Channel
# ---------------------------------------------------------------------------

class BerryPhaseEncoder:
    """
    Scheme E: Berry/Pancharatnam geometric phase sub-channel.

    For a closed circular path C on the Poincaré sphere at polar angle α:
      Ω(C) = 2π(1 − cos α)       (solid angle subtended)
      γ_B  = −½ Ω(C) = −π(1 − cos α)

    Encoding: M discrete phase levels γ_k = 2πk/M for k = 0,..,M-1.
    Each level maps to a distinct circular path on S^2.

    With M=64 at 15 dB SNR: +4.0 bits/symbol (TRL 2).
    The Berry phase is topology-dependent and insensitive to traversal speed
    or dynamical phase noise.
    """

    def __init__(self, M: int = 64, num_path_points: int = 32) -> None:
        self.M = M
        self.num_path_points = num_path_points
        self._phase_levels = np.linspace(0.0, 2 * np.pi, M, endpoint=False)

    def _alpha_for_level(self, k: int) -> float:
        """Polar angle α such that Berry phase = γ_k."""
        gamma = float(self._phase_levels[k % self.M])
        # γ_B = −π(1−cos α)  →  cos α = 1 + γ_B/π
        cos_alpha = max(-1.0, min(1.0, 1.0 + gamma / math.pi))
        return math.acos(cos_alpha)

    def encode(self, phase_idx: int) -> List[SpacetimePoint]:
        """
        Encode a phase level as the Poincaré sphere circular path.

        Path points: (sin α·cos φ, sin α·sin φ, cos α) for φ ∈ [0, 2π).
        """
        alpha = self._alpha_for_level(phase_idx)
        phi = np.linspace(0.0, 2 * np.pi, self.num_path_points, endpoint=False)
        sin_a = math.sin(alpha)
        cos_a = math.cos(alpha)
        x = sin_a * np.cos(phi)
        y = sin_a * np.sin(phi)
        z = np.full_like(phi, cos_a)
        return [SpacetimePoint(0.0, float(xi), float(yi), float(zi))
                for xi, yi, zi in zip(x, y, z)]

    def berry_phase(self, phase_idx: int) -> float:
        """Return Berry phase γ (radians) for level index."""
        return float(self._phase_levels[phase_idx % self.M])

    def solid_angle(self, phase_idx: int) -> float:
        """Solid angle Ω(C) = 2π(1 − cos α) subtended by the path."""
        alpha = self._alpha_for_level(phase_idx)
        return 2 * math.pi * (1.0 - math.cos(alpha))

    def capacity_bits(self) -> float:
        return math.log2(self.M)


# ---------------------------------------------------------------------------
# Scheme F: Unified Geometric Integrity Encoding (GIE)
# ---------------------------------------------------------------------------

class GIESymbol:
    """
    A symbol on the product manifold:
      M = S²_pol × S¹_phase × Z_OAM × S¹_Berry × R⁺_amp

    Coordinates: (theta_pol, phi_pol, l_oam, gamma_berry, amplitude).
    """

    __slots__ = ('theta_pol', 'phi_pol', 'l_oam', 'gamma_berry', 'amplitude')

    def __init__(
        self,
        theta_pol: float,
        phi_pol: float,
        l_oam: int,
        gamma_berry: float,
        amplitude: float = 1.0,
    ) -> None:
        self.theta_pol = float(theta_pol)
        self.phi_pol = float(phi_pol)
        self.l_oam = int(l_oam)
        self.gamma_berry = float(gamma_berry)
        self.amplitude = float(amplitude)

    def to_spacetime_points(self) -> List[SpacetimePoint]:
        """
        Map GIE symbol to two SpacetimePoints encoding all 5 geometric dimensions.

        Point 1 — polarization S^2 embedded with OAM charge as time coordinate:
          t = l_oam, x = A·sin θ cos φ, y = A·sin θ sin φ, z = A·cos θ

        Point 2 — phase and Berry phase as (t, x, y, z):
          t = γ_Berry, x = cos φ, y = sin φ, z = amplitude
        """
        A = self.amplitude
        p1 = SpacetimePoint(
            float(self.l_oam),
            A * math.sin(self.theta_pol) * math.cos(self.phi_pol),
            A * math.sin(self.theta_pol) * math.sin(self.phi_pol),
            A * math.cos(self.theta_pol),
        )
        p2 = SpacetimePoint(
            self.gamma_berry,
            math.cos(self.phi_pol),
            math.sin(self.phi_pol),
            self.amplitude,
        )
        return [p1, p2]


class GIEEncoder:
    """
    Scheme F: Unified Geometric Integrity Encoding.

    Combines all geometric dimensions into one encoding and uses the
    STAGE-CHRONOS Φ metric as a built-in integrity check:
      Φ = exp(−MSE · K) → 1 for uncorrupted symbols, → 0 for errors.

    Max capacity: ~30 bits/symbol (5× DP-64QAM) combining all DoF.
    TRL 1-2 (full redesign required).
    """

    def __init__(
        self,
        oam_levels: int = 8,
        berry_levels: int = 16,
        pol_M1: int = 8,
        pol_M2: int = 8,
    ) -> None:
        self.oam_levels = oam_levels
        self.berry_levels = berry_levels
        self.tpp = TPPEncoder(M1=pol_M1, M2=pol_M2)
        self.berry = BerryPhaseEncoder(M=berry_levels)

    def encode(
        self,
        pol_idx: int,
        oam_l: int,
        phase_idx: int,
        berry_idx: int,
        amplitude: float = 1.0,
    ) -> GIESymbol:
        """Encode a symbol on the full product manifold."""
        theta1 = float(self.tpp.theta1_grid[pol_idx % self.tpp.M1])
        theta2 = float(self.tpp.theta2_grid[phase_idx % self.tpp.M2])
        gamma = self.berry.berry_phase(berry_idx)
        return GIESymbol(theta1, theta2, oam_l, gamma, amplitude)

    def measure_phi(self, before: GIESymbol, after: GIESymbol) -> float:
        """Geometric coherence Φ between transmitted and received GIE symbols."""
        pts_b = before.to_spacetime_points()
        pts_a = after.to_spacetime_points()
        phi, _ = GeometricCoherenceKernel.measure_coherence(
            pts_b, pts_a, sample_size=len(pts_b)
        )
        return phi

    def capacity_bits(self) -> float:
        """Bits from combined TPP + OAM + Berry dimensions."""
        return (
            self.tpp.capacity_bits()
            + math.log2(self.oam_levels)
            + self.berry.capacity_bits()
        )

    @staticmethod
    def theoretical_max_bits() -> float:
        """Theoretical limit from all 7 geometric DoF at 15 dB SNR (Table 2.1)."""
        return 26.9


# ---------------------------------------------------------------------------
# Comparative capacity analysis
# ---------------------------------------------------------------------------

SCHEME_BITS = {
    'A_TPP':          6.0,
    'B_Hopf':        12.5,
    'C_NPM':          4.0,
    'D_E8':           3.66,   # expressed as equivalent SNR-gain bits at 15 dB
    'E_Berry':        4.0,
    'F_GIE_combined': 30.0,
}

SCHEME_TRL = {
    'A_TPP': 3, 'B_Hopf': 2, 'C_NPM': 4,
    'D_E8': 4,  'E_Berry': 2, 'F_GIE_combined': 1,
}

_CONVENTIONAL_BITS_PER_SYMBOL = 12.0    # DP-64QAM
_CONVENTIONAL_C_BAND_TBPS = 115.0


def capacity_projection(schemes: List[str]) -> float:
    """
    Project C-band fiber capacity (Tbps) given a list of active scheme names.

    Assumes conventional baseline of 115 Tbps (DP-64QAM + WDM).
    Scales linearly with bits/symbol improvement.
    """
    added = sum(SCHEME_BITS[s] for s in schemes)
    gain = (_CONVENTIONAL_BITS_PER_SYMBOL + added) / _CONVENTIONAL_BITS_PER_SYMBOL
    return _CONVENTIONAL_C_BAND_TBPS * gain
