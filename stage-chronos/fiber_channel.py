"""
OtapFiberChannel — STAGE manifold transport through the OTAP fiber model.

Maps the physical impairments from otap-sim/src/lib.rs and
otap-core/src/dimensions.rs onto their effect on a STAGE torus manifold.

Physical-to-manifold coordinate mapping
----------------------------------------
  t     : propagation time coordinate  (natural units, c = 1)
  x     : s1 Stokes axis (horizontal vs vertical polarization)
  y     : s2 Stokes axis (+45° vs −45° linear polarization)
  z     : s3 Stokes axis (right vs left circular polarization)

Impairment models
-----------------
PMD
  SO(3) rotation on (x, y, z) — mirrors otap-sim::PoincareRotation.
  Applied via R = R_s1(α) · R_s2(β) · R_s3(γ), identical to
  otap-sim::Channel::random_pmd's three-axis composition.
  Isometric in Euclidean space → ds² = −dt² + dx² + dy² + dz² preserved
  (all t_i equal, so dt = 0 and the rotation leaves Σ dxi² unchanged).
  Φ = 1.0 exactly.  This is the PMD-invariance property the topological
  D3 authenticator in otap-crypto relies on.

CD  (Chromatic Dispersion)
  Different spectral components travel at different group velocities.
  In manifold coordinates the x-axis encodes spectral position; the
  resulting group-delay walk-off is modelled as:

      t' = t + (D · L) · ξ · x

  where D [ps/(nm·km)] is the dispersion coefficient, L [km] is the
  link length, and ξ = 4 × 10⁻⁵ nm per manifold spatial unit (calibrated
  so that SMF-28 at 80 km, 25 GHz channel bandwidth yields ~1 e-fold
  coherence decay: Φ ≈ 0.3 at DL = 1360 ps/nm).

  This is a t-x shear.  Unlike a Lorentz boost (t' = γ(t − βx), which
  also shears x' = γ(x − βt)), CD only displaces t with no reciprocal
  change to x.  It is NOT a Lorentz transform, so it changes pairwise
  spacetime intervals and Φ < 1.

DGD  (Differential Group Delay)
  The two principal polarization states (PSPs) arrive at different times.
  Identified with the ±s1 axis, the time split is projected continuously:

      t' = t + (τ_DGD / 2) · (x / |r|)

  where |r| = sqrt(x² + y² + z²) is the Poincaré-sphere radius.
  Position-dependent time shear → NOT a Lorentz transform → Φ < 1.

PDL  (Polarization Dependent Loss)
  A rogue optical element (e.g. mis-aligned ROADM, attacker's tap) causes
  unequal loss on each Stokes axis:

      x' = α₁ x,   y' = α₂ y,   z' = α₃ z
      αᵢ = 10^(−PDL_i / 20)   (field amplitude, not power)

  Non-unitary: breaks SO(3) symmetry, changes pairwise Euclidean distances,
  hence changes spacetime intervals → Φ < 1.

EDFA  (Amplifier ASE noise)
  Isotropic Gaussian perturbation on (x, y, z):
      σ = _EDFA_NOISE_SCALE · sqrt(F_linear · N_amp)
  where F_linear = 10^(NF_dB / 10).  Stochastic → Φ decreases with
  noise power.
"""

import math
from dataclasses import dataclass
from typing import List

import numpy as np

from .spacetime import SpacetimePoint

# ---------------------------------------------------------------------------
# Physical constants (from otap-core/src/dimensions.rs)
# ---------------------------------------------------------------------------
ITU_ANCHOR_THZ = 193.1      # channel-0 reference frequency (THz)
CHANNEL_SPACING_GHZ = 25.0  # ITU-T C-band grid spacing (GHz)

# ---------------------------------------------------------------------------
# Coordinate-scale constants
# ---------------------------------------------------------------------------
# CD: ξ = 4e-5 nm / manifold-spatial-unit.
# Calibrated so that DL = 1360 ps/nm (SMF-28, 80 km) on a symbol-1 torus
# (x_max ≈ 4) produces ~1 e-fold coherence decay (Φ ≈ 0.3).
_CD_NM_PER_UNIT: float = 4e-5

# DGD: 1 ps DGD → 1e-3 manifold time units at unit PSP projection.
_DGD_MANIFOLD_PER_PS: float = 1e-3

# EDFA: noise amplitude scale per sqrt(F_linear · N_amp).
_EDFA_NOISE_SCALE: float = 0.02


# ---------------------------------------------------------------------------
# Poincaré-sphere rotation helpers — mirrors otap-sim::PoincareRotation
# ---------------------------------------------------------------------------

def _rot_s1(a: float) -> np.ndarray:
    c, s = math.cos(a), math.sin(a)
    return np.array([[1, 0, 0], [0, c, -s], [0, s, c]], dtype=float)

def _rot_s2(a: float) -> np.ndarray:
    c, s = math.cos(a), math.sin(a)
    return np.array([[c, 0, s], [0, 1, 0], [-s, 0, c]], dtype=float)

def _rot_s3(a: float) -> np.ndarray:
    c, s = math.cos(a), math.sin(a)
    return np.array([[c, -s, 0], [s, c, 0], [0, 0, 1]], dtype=float)


# ---------------------------------------------------------------------------
# Fiber link specification
# ---------------------------------------------------------------------------

@dataclass
class FiberSpec:
    """
    Physical parameters for an OTAP DWDM fiber link.

    Angles in radians, delays in picoseconds, dispersion in ps/(nm·km),
    lengths in km, loss in dB.
    """
    # PMD — three-axis Euler angles matching otap-sim::PoincareRotation composition
    pmd_s1: float = 0.0
    pmd_s2: float = 0.0
    pmd_s3: float = 0.0

    # Chromatic dispersion
    cd_ps_per_nm_per_km: float = 0.0   # dispersion coefficient D
    length_km: float = 0.0              # fiber length L → DL = D·L ps/nm

    # Differential Group Delay
    dgd_ps: float = 0.0

    # Polarization Dependent Loss per Stokes axis
    pdl_s1_db: float = 0.0
    pdl_s2_db: float = 0.0
    pdl_s3_db: float = 0.0

    # EDFA amplifier chain
    edfa_noise_figure_db: float = 0.0
    n_amplifiers: int = 0

    # ---- factory presets ------------------------------------------------

    @staticmethod
    def ideal() -> "FiberSpec":
        """Perfect fiber: no impairments of any kind."""
        return FiberSpec()

    @staticmethod
    def pmd_only(seed: int = 42) -> "FiberSpec":
        """
        Random PMD rotation, matching otap-sim::Channel::random_pmd.
        Angles drawn uniformly from [−π, π] using NumPy with the same seed.
        No CD, DGD, or PDL — the scenario where OTAP's PMD invariance shines.
        """
        rng = np.random.default_rng(seed)
        return FiberSpec(
            pmd_s1=float(rng.uniform(-math.pi, math.pi)),
            pmd_s2=float(rng.uniform(-math.pi, math.pi)),
            pmd_s3=float(rng.uniform(-math.pi, math.pi)),
        )

    @staticmethod
    def metro_link(length_km: float = 40.0, seed: int = 1) -> "FiberSpec":
        """
        Short metro link (≤ 80 km) with modest PMD and no dispersion
        accumulation (DCM in place, well within ITU-T G.691 tolerance).
        Should PASS the coherence test.
        """
        rng = np.random.default_rng(seed)
        return FiberSpec(
            pmd_s1=float(rng.uniform(-0.4, 0.4)),
            pmd_s2=float(rng.uniform(-0.4, 0.4)),
            pmd_s3=float(rng.uniform(-0.4, 0.4)),
        )

    @staticmethod
    def uncompensated_smf(length_km: float = 80.0) -> "FiberSpec":
        """
        Uncompensated SMF-28 (D = 17 ps/(nm·km)).
        CD accumulates without DSP equalization or DCM.
        DL = 17 × 80 = 1360 ps/nm exceeds the coherence threshold:
        Φ ≈ 0.3 at 80 km, Φ ≈ 0 at 160 km.
        """
        return FiberSpec(
            cd_ps_per_nm_per_km=17.0,
            length_km=length_km,
        )

    @staticmethod
    def high_dgd(dgd_ps: float = 50.0) -> "FiberSpec":
        """
        High DGD — exceeds ITU-T G.691 2.5 ps tolerance for 100G coherent.
        50 ps represents an impaired or tampered link.
        """
        return FiberSpec(dgd_ps=dgd_ps)

    @staticmethod
    def pdl_impaired(pdl_db: float = 3.0) -> "FiberSpec":
        """
        Polarization-dependent loss as introduced by a rogue optical element
        (mis-aligned ROADM port, attacker's tap, faulty connector).
        PDL is non-unitary: it cannot be compensated by any unitary rotation,
        so the winding-number invariant and the manifold geometry are both
        broken.
        """
        return FiberSpec(
            pdl_s1_db=pdl_db,
            pdl_s2_db=pdl_db * 0.5,
            pdl_s3_db=pdl_db * 0.25,
        )

    @staticmethod
    def long_haul(length_km: float = 500.0, n_spans: int = 6) -> "FiberSpec":
        """
        Multi-span long-haul link (500–1000 km).
        Combines uncompensated CD, accumulated DGD, and EDFA noise — all
        three non-unitary / stochastic degradations stack up.
        Will definitively fail the coherence test.
        """
        return FiberSpec(
            cd_ps_per_nm_per_km=17.0,
            length_km=length_km,
            dgd_ps=length_km * 0.1,    # 0.1 ps/km typical SMF PMD coefficient
            edfa_noise_figure_db=5.0,
            n_amplifiers=n_spans,
        )


# ---------------------------------------------------------------------------
# Channel model
# ---------------------------------------------------------------------------

class OtapFiberChannel:
    """
    Apply OTAP fiber impairments to a STAGE torus manifold.

    Extends otap-sim::Channel::transmit beyond PMD to include the three
    impairments that are NOT isometries of the Minkowski metric:
    chromatic dispersion, DGD, and PDL.  These break manifold geometry
    in ways that are physically distinguishable from clean transport and
    serve as the FFA tamper-detection signal.

    Quick reference — which impairments change Φ::

        Impairment   Isometric?   Φ effect
        ─────────────────────────────────────────
        PMD          YES (SO(3))  Φ = 1.0  always
        CD           NO           Φ drops with D·L
        DGD          NO           Φ drops with τ_DGD
        PDL          NO (scales)  Φ drops with PDL_dB
        EDFA noise   NO (random)  Φ drops with F·N_amp

    Usage::

        spec = FiberSpec.uncompensated_smf(80.0)
        ch   = OtapFiberChannel(spec)
        rx   = ch.apply(tx_manifold)
        phi, mse = gck.measure_coherence(tx_manifold, rx)
        # phi < 0.5 → fiber failed coherence check (FFA signal)
    """

    PHI_PASS_THRESHOLD = 0.90   # clean fiber: Φ > 0.90
    PHI_FAIL_THRESHOLD = 0.50   # degraded fiber: Φ < 0.50

    def __init__(self, spec: FiberSpec) -> None:
        self.spec = spec
        self._R = (
            _rot_s1(spec.pmd_s1)
            @ _rot_s2(spec.pmd_s2)
            @ _rot_s3(spec.pmd_s3)
        )

    @property
    def dl_ps_per_nm(self) -> float:
        """Total accumulated dispersion D·L (ps/nm)."""
        return self.spec.cd_ps_per_nm_per_km * self.spec.length_km

    def apply(
        self,
        manifold: List[SpacetimePoint],
        seed: int = 0,
    ) -> List[SpacetimePoint]:
        """
        Apply all fiber impairments in physical propagation order:
        PMD (distributed, lumped approx.) → CD → DGD → PDL → EDFA.
        """
        pts = manifold
        s = self.spec

        pts = self._pmd(pts)                                  # always (identity if zero)

        if s.cd_ps_per_nm_per_km != 0.0 and s.length_km != 0.0:
            pts = self._chromatic_dispersion(pts)

        if s.dgd_ps != 0.0:
            pts = self._dgd(pts)

        if s.pdl_s1_db != 0.0 or s.pdl_s2_db != 0.0 or s.pdl_s3_db != 0.0:
            pts = self._pdl(pts)

        if s.edfa_noise_figure_db > 0.0 and s.n_amplifiers > 0:
            pts = self._edfa_noise(pts, seed)

        return pts

    # ---- impairment kernels (private) ------------------------------------

    def _pmd(self, manifold: List[SpacetimePoint]) -> List[SpacetimePoint]:
        """
        SO(3) rotation on (x, y, z) — mirrors PoincareRotation::apply.

        When all t_i = const (same emission time, which the torus encoder
        guarantees), ds²(i,j) = Δx² + Δy² + Δz².  SO(3) preserves every
        Euclidean distance in R³, so all pairwise intervals are unchanged.
        Φ = 1.0 regardless of rotation angle — this is the PMD-invariance
        that makes OTAP's topological D3 authenticator work.
        """
        R = self._R
        return [
            SpacetimePoint(
                p.t,
                R[0, 0] * p.x + R[0, 1] * p.y + R[0, 2] * p.z,
                R[1, 0] * p.x + R[1, 1] * p.y + R[1, 2] * p.z,
                R[2, 0] * p.x + R[2, 1] * p.y + R[2, 2] * p.z,
            )
            for p in manifold
        ]

    def _chromatic_dispersion(
        self, manifold: List[SpacetimePoint]
    ) -> List[SpacetimePoint]:
        """
        CD group-delay walk-off: t' = t + k_CD · x

        k_CD = D · L · ξ   where ξ = _CD_NM_PER_UNIT

        This is a shear of the (t, x) plane.  A Lorentz boost also shears
        (t, x) but with a reciprocal change x' = γ(x − βt) that preserves
        ds².  CD displaces only t — x is unchanged — so it is NOT a Lorentz
        transform and intervals are not preserved.

        Physical picture: a narrow-band pulse centred at wavelength λ₀
        acquires group delay τ = D·L·Δλ relative to a pulse at λ₀ + Δλ.
        Points on the torus at x > 0 (one spectral edge) arrive earlier or
        later than points at x < 0 (opposite edge), shearing the manifold
        in the t direction.
        """
        k_cd = self.dl_ps_per_nm * _CD_NM_PER_UNIT
        return [
            SpacetimePoint(p.t + k_cd * p.x, p.x, p.y, p.z)
            for p in manifold
        ]

    def _dgd(self, manifold: List[SpacetimePoint]) -> List[SpacetimePoint]:
        """
        DGD: continuous PSP projection onto the s1 axis.

            t' = t + (τ_DGD / 2) · (x / |r|)

        Points with positive s1 projection (x > 0) travel on the fast PSP
        and arrive early; points with x < 0 travel on the slow PSP and
        arrive late.  The split scales with the cosine of the angle between
        the local Stokes vector and the s1 axis.

        This is position-dependent and couples t to the spatial manifold
        without any reciprocal x-change — definitively non-Lorentz.
        """
        half_tau = self.spec.dgd_ps * 0.5 * _DGD_MANIFOLD_PER_PS
        result = []
        for p in manifold:
            r = math.sqrt(p.x * p.x + p.y * p.y + p.z * p.z)
            proj = (p.x / r) if r > 1e-10 else 0.0
            result.append(SpacetimePoint(p.t + half_tau * proj, p.x, p.y, p.z))
        return result

    def _pdl(self, manifold: List[SpacetimePoint]) -> List[SpacetimePoint]:
        """
        PDL: per-axis amplitude attenuation.

            x' = α₁ x,  y' = α₂ y,  z' = α₃ z
            αᵢ = 10^(−PDL_i / 20)

        With α₁ ≠ α₂ ≠ α₃ this is not an element of SO(3): it cannot be
        decomposed into rotations, so it changes pairwise Euclidean distances
        and hence spacetime intervals.  Even a small PDL (0.5 dB ≈ 5.5%
        amplitude reduction) produces a measurable Φ drop.
        """
        ax = 10 ** (-self.spec.pdl_s1_db / 20)
        ay = 10 ** (-self.spec.pdl_s2_db / 20)
        az = 10 ** (-self.spec.pdl_s3_db / 20)
        return [
            SpacetimePoint(p.t, p.x * ax, p.y * ay, p.z * az)
            for p in manifold
        ]

    def _edfa_noise(
        self, manifold: List[SpacetimePoint], seed: int
    ) -> List[SpacetimePoint]:
        """
        EDFA ASE noise: isotropic Gaussian perturbation on (x, y, z).

            σ = _EDFA_NOISE_SCALE · sqrt(F_linear · N_amp)

        F_linear = 10^(NF_dB / 10).  A 5 dB NF EDFA (F = 3.16) over
        6 spans gives σ ≈ 0.02 × sqrt(3.16 × 6) ≈ 0.087 per axis.
        """
        nf = 10 ** (self.spec.edfa_noise_figure_db / 10)
        sigma = _EDFA_NOISE_SCALE * math.sqrt(nf * self.spec.n_amplifiers)
        rng = np.random.default_rng(seed)
        n = len(manifold)
        dx = rng.normal(0, sigma, n)
        dy = rng.normal(0, sigma, n)
        dz = rng.normal(0, sigma, n)
        return [
            SpacetimePoint(p.t, p.x + dx[i], p.y + dy[i], p.z + dz[i])
            for i, p in enumerate(manifold)
        ]
