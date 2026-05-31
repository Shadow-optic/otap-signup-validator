"""
Holographic Waveguide OS & Digital Phase Conjugation (DPC).

Three-layer pipeline:
  1. STAGEHelixEncoder  — Encodes data symbols as helical OAM (Orbital Angular
                           Momentum) manifolds.  Topological charge l = 1+symbol
                           determines the twist frequency of the helix.

  2. SpatialLightModulator (SLM) — Forward: maps 3D helix geometry onto a 1D
                           complex amplitude/phase array (Fourier proxy hologram).
                           Inverse: unscrambles phase back to 3D points.

  3. MultimodeFiber (MMF) — Models modal dispersion via a Haar-random unitary
                           Transmission Matrix T.  ``transmit()`` applies T;
                           ``phase_conjugate_recovery()`` applies T† (conjugate
                           transpose = perfect time-reversal).

Security property
-----------------
An adversary who reads the raw speckle pattern (after T) sees a uniformly
random complex vector; without T† they cannot recover the OAM geometry.  The
Geometric Coherence Phi quantifies whether a recovered manifold matches the
original.  Adversary Phi → 0; authorized receiver Phi → 1.

Vectorization notes
-------------------
All hot paths use numpy ufuncs and broadcasting — no Python-level scalar loops.

  generate_helix        : np.cos / np.sin over a linspace array
  create_hologram       : np.arctan2 / np.hypot / np.exp on coordinate arrays
  reconstruct_manifold  : np.abs / np.angle / np.cos / np.sin on hologram array
  measure_holographic_coherence : O(m²) pairwise Minkowski intervals via
                          broadcasting (m, m, 4) diff tensor → upper-tri mask
"""

import math
from typing import List, Optional

import numpy as np

from .spacetime import SpacetimePoint


# ---------------------------------------------------------------------------
# Internal: fast pairwise Minkowski interval kernel
# ---------------------------------------------------------------------------

def _pairwise_intervals(coords: np.ndarray) -> np.ndarray:
    """
    Compute all pairwise Minkowski intervals for an (n, 4) coordinate array.

    coords columns: [t, x, y, z]
    ds²[i,j] = -(t_i − t_j)² + (x_i − x_j)² + (y_i − y_j)² + (z_i − z_j)²

    Returns an (n, n) float64 array.
    """
    diff = coords[:, np.newaxis, :] - coords[np.newaxis, :, :]  # (n, n, 4)
    return -diff[..., 0] ** 2 + diff[..., 1] ** 2 + diff[..., 2] ** 2 + diff[..., 3] ** 2


def _manifold_to_coords(manifold: List[SpacetimePoint], idx: np.ndarray) -> np.ndarray:
    """Extract (len(idx), 4) coordinate array for sampled indices."""
    out = np.empty((len(idx), 4), dtype=np.float64)
    for k, i in enumerate(idx):
        p = manifold[i]
        out[k, 0] = p.t
        out[k, 1] = p.x
        out[k, 2] = p.y
        out[k, 3] = p.z
    return out


# ---------------------------------------------------------------------------
# OAM helix encoder
# ---------------------------------------------------------------------------

class STAGEHelixEncoder:
    """
    Encodes a data symbol as a helical manifold (OAM basis).

    Symbol ``s`` sets the topological charge l = 1 + s, which determines how
    many full twists the helix makes over a single 2π propagation segment.
    Higher charge → finer twist → distinct geometric signature.
    """

    @staticmethod
    def generate_helix(
        data_symbol: int,
        num_points: int = 100,
        radius: float = 1.0,
    ) -> List[SpacetimePoint]:
        """
        Return ``num_points`` points uniformly sampled on an OAM helix.

        z ∈ [0, 2π); x = r·cos(l·z); y = r·sin(l·z)

        All trig computed in a single numpy vectorized pass.
        """
        l_charge = 1 + data_symbol
        z = np.arange(num_points) * (2.0 * np.pi / num_points)
        x = radius * np.cos(l_charge * z)
        y = radius * np.sin(l_charge * z)
        return [
            SpacetimePoint(0.0, float(xi), float(yi), float(zi))
            for xi, yi, zi in zip(x, y, z)
        ]


# ---------------------------------------------------------------------------
# Spatial Light Modulator
# ---------------------------------------------------------------------------

class SpatialLightModulator:
    """
    Converts a 3D manifold to/from a 1D complex hologram — fully vectorized.

    Forward:  E[i] = |r_i| · exp(i · (atan2(y_i, x_i) + z_i))
    Inverse:  amplitudes = |E|; phases = angle(E) − z → (x, y) via polar
    """

    @staticmethod
    def create_hologram(manifold: List[SpacetimePoint]) -> np.ndarray:
        """
        Map 3D geometry onto a complex hologram array.

        Three separate list comprehensions (one per coordinate axis) are
        faster than a 2D structured loop: they avoid per-element tuple
        allocation and let numpy build contiguous float64 buffers directly.
        """
        xs = np.array([p.x for p in manifold])
        ys = np.array([p.y for p in manifold])
        zs = np.array([p.z for p in manifold])
        phases = np.arctan2(ys, xs) + zs
        amplitudes = np.hypot(xs, ys)
        return amplitudes * np.exp(1j * phases)

    @staticmethod
    def reconstruct_manifold(
        hologram: np.ndarray,
        z_coords: np.ndarray,
    ) -> List[SpacetimePoint]:
        """
        Reconstruct 3D manifold from hologram + known z-coordinates.

        All complex→polar→cartesian math is done with numpy ufuncs.
        Only the final list comprehension wrapping into SpacetimePoint
        objects remains at the Python level.
        """
        amplitudes = np.abs(hologram)
        phases = np.angle(hologram) - z_coords
        xs = amplitudes * np.cos(phases)
        ys = amplitudes * np.sin(phases)
        return [
            SpacetimePoint(0.0, float(x), float(y), float(z))
            for x, y, z in zip(xs, ys, z_coords)
        ]


# ---------------------------------------------------------------------------
# Multimode Fiber with Digital Phase Conjugation
# ---------------------------------------------------------------------------

class MultimodeFiber:
    """
    Multimode fiber modal dispersion model.

    The Transmission Matrix T is drawn from the Haar measure (QR decomposition
    of a complex Gaussian matrix), giving a unitary model of chaotic mode mixing.

    DPC recovery: T† = conj(T.T) — the exact mathematical inverse.  In real
    fiber DPC systems this matrix is measured interferometrically; here it is
    computed exactly, giving an upper bound on recovery fidelity.
    """

    def __init__(self, modes: int, seed: Optional[int] = None) -> None:
        self.modes = modes
        rng = np.random.default_rng(seed)
        raw = rng.standard_normal((modes, modes)) + 1j * rng.standard_normal((modes, modes))
        Q, R = np.linalg.qr(raw)
        ph = np.diag(R) / np.abs(np.diag(R))
        self.transmission_matrix: np.ndarray = Q * ph
        self.conjugation_matrix: np.ndarray = np.conj(self.transmission_matrix.T)

    def transmit(self, hologram: np.ndarray) -> np.ndarray:
        """Apply modal dispersion: scatter hologram into speckle pattern."""
        return self.transmission_matrix @ hologram

    def phase_conjugate_recovery(self, speckle: np.ndarray) -> np.ndarray:
        """Apply DPC: multiply by T† to time-reverse the modal scrambling."""
        return self.conjugation_matrix @ speckle


# ---------------------------------------------------------------------------
# Fast coherence measurement — vectorized pairwise intervals
# ---------------------------------------------------------------------------

def measure_holographic_coherence(
    original: List[SpacetimePoint],
    recovered: List[SpacetimePoint],
    sample_size: int = 20,
) -> float:
    """
    Compute Phi ∈ [0,1] comparing Minkowski interval structure of two manifolds.

    Vectorized: builds (m, 4) coordinate arrays for sampled points, computes
    all m² pairwise intervals via broadcasting in a single numpy pass, then
    selects the upper-triangular pairs with a boolean mask.

    Same formula as GeometricCoherenceKernel: Phi = exp(-MSE * 1000).
    """
    n = len(original)
    step = max(1, n // sample_size)
    idx = np.arange(0, n, step)

    orig_coords = _manifold_to_coords(original, idx)   # (m, 4)
    rec_coords  = _manifold_to_coords(recovered, idx)  # (m, 4)

    ds2_orig = _pairwise_intervals(orig_coords)         # (m, m)
    ds2_rec  = _pairwise_intervals(rec_coords)          # (m, m)

    # Upper-triangular pairs correspond to the original loop's j > i condition
    m = len(idx)
    mask = np.triu(np.ones((m, m), dtype=bool), k=1)

    mse = float(np.mean((ds2_orig[mask] - ds2_rec[mask]) ** 2))
    return math.exp(-mse * 1000)


# ---------------------------------------------------------------------------
# Full pipeline helper
# ---------------------------------------------------------------------------

def run_holographic_pipeline(
    data_symbol: int = 2,
    resolution: int = 128,
    seed: int = 42,
    sample_size: int = 20,
    fiber: Optional[MultimodeFiber] = None,
) -> dict:
    """
    Run the full encode → transmit → adversary / DPC-recover pipeline.

    Parameters
    ----------
    fiber : MultimodeFiber, optional
        Pre-built fiber instance.  Pass one to exclude the O(N³) QR
        construction from timing (useful for throughput benchmarks).
        When None a new fiber is constructed from ``seed``.

    Returns a dict with keys:
      phi_adversary  — Phi of adversary's speckle-based reconstruction
      phi_receiver   — Phi of DPC-recovered manifold
      tx_manifold    — original transmitted manifold
      rx_manifold    — DPC-recovered manifold
      adv_manifold   — adversary's (failed) reconstruction
    """
    if fiber is None:
        fiber = MultimodeFiber(modes=resolution, seed=seed)

    tx_manifold = STAGEHelixEncoder.generate_helix(data_symbol, num_points=resolution)
    z_coords = np.arange(resolution) * (2.0 * np.pi / resolution)

    tx_hologram = SpatialLightModulator.create_hologram(tx_manifold)
    rx_speckle = fiber.transmit(tx_hologram)

    adv_manifold = SpatialLightModulator.reconstruct_manifold(rx_speckle, z_coords)
    phi_adversary = measure_holographic_coherence(tx_manifold, adv_manifold, sample_size)

    rx_recovered = fiber.phase_conjugate_recovery(rx_speckle)
    rx_manifold = SpatialLightModulator.reconstruct_manifold(rx_recovered, z_coords)
    phi_receiver = measure_holographic_coherence(tx_manifold, rx_manifold, sample_size)

    return dict(
        phi_adversary=phi_adversary,
        phi_receiver=phi_receiver,
        tx_manifold=tx_manifold,
        rx_manifold=rx_manifold,
        adv_manifold=adv_manifold,
    )
