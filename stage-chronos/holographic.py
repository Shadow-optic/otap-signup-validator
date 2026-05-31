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
"""

import math
from typing import List, Optional

import numpy as np

from .spacetime import SpacetimePoint
from .coherence import GeometricCoherenceKernel


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

        z ∈ [0, 2π]; x = r·cos(l·z); y = r·sin(l·z)
        """
        l_charge = 1 + data_symbol
        manifold: List[SpacetimePoint] = []
        for i in range(num_points):
            z = (i / num_points) * 2 * math.pi
            x = radius * math.cos(l_charge * z)
            y = radius * math.sin(l_charge * z)
            manifold.append(SpacetimePoint(t=0.0, x=x, y=y, z=z))
        return manifold


# ---------------------------------------------------------------------------
# Spatial Light Modulator
# ---------------------------------------------------------------------------

class SpatialLightModulator:
    """
    Converts a 3D manifold to/from a 1D complex hologram.

    Forward:  E[i] = |r_i| · exp(i · (atan2(y_i, x_i) + z_i))
              (simplified Fourier-plane proxy)
    Inverse:  (|E|, arg(E) − z) → (x, y)  via polar reconstruction
    """

    @staticmethod
    def create_hologram(manifold: List[SpacetimePoint]) -> np.ndarray:
        """Map 3D geometry onto a complex hologram array."""
        n = len(manifold)
        hologram = np.empty(n, dtype=np.complex128)
        for i, p in enumerate(manifold):
            phase = math.atan2(p.y, p.x) + p.z
            amplitude = math.sqrt(p.x ** 2 + p.y ** 2)
            hologram[i] = amplitude * complex(math.cos(phase), math.sin(phase))
        return hologram

    @staticmethod
    def reconstruct_manifold(
        hologram: np.ndarray,
        z_coords: np.ndarray,
    ) -> List[SpacetimePoint]:
        """Reconstruct 3D manifold from hologram + known z-coordinates."""
        manifold: List[SpacetimePoint] = []
        for i, E in enumerate(hologram):
            amplitude = abs(E)
            phase = math.atan2(E.imag, E.real) - z_coords[i]
            x = amplitude * math.cos(phase)
            y = amplitude * math.sin(phase)
            manifold.append(SpacetimePoint(t=0.0, x=x, y=y, z=float(z_coords[i])))
        return manifold


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
        # QR gives a Haar-uniform unitary
        ph = np.diag(R) / np.abs(np.diag(R))
        self.transmission_matrix: np.ndarray = Q * ph
        # T† is the exact inverse (time-reversal key)
        self.conjugation_matrix: np.ndarray = np.conj(self.transmission_matrix.T)

    def transmit(self, hologram: np.ndarray) -> np.ndarray:
        """Apply modal dispersion: scatter hologram into speckle pattern."""
        return self.transmission_matrix @ hologram

    def phase_conjugate_recovery(self, speckle: np.ndarray) -> np.ndarray:
        """Apply DPC: multiply by T† to time-reverse the modal scrambling."""
        return self.conjugation_matrix @ speckle


# ---------------------------------------------------------------------------
# Convenience: measure Phi using the package kernel
# ---------------------------------------------------------------------------

def measure_holographic_coherence(
    original: List[SpacetimePoint],
    recovered: List[SpacetimePoint],
    sample_size: int = 20,
) -> float:
    """Return Phi in [0,1] comparing interval structure of two manifolds."""
    gck = GeometricCoherenceKernel()
    phi, _ = gck.measure_coherence(original, recovered, sample_size=sample_size)
    return phi


# ---------------------------------------------------------------------------
# Full pipeline helper
# ---------------------------------------------------------------------------

def run_holographic_pipeline(
    data_symbol: int = 2,
    resolution: int = 128,
    seed: int = 42,
    sample_size: int = 20,
) -> dict:
    """
    Run the full encode → transmit → adversary / DPC-recover pipeline.

    Returns a dict with keys:
      phi_adversary  — Phi of adversary's speckle-based reconstruction
      phi_receiver   — Phi of DPC-recovered manifold
      tx_manifold    — original transmitted manifold
      rx_manifold    — DPC-recovered manifold
      adv_manifold   — adversary's (failed) reconstruction
    """
    np.random.seed(seed)

    tx_manifold = STAGEHelixEncoder.generate_helix(data_symbol, num_points=resolution)
    z_coords = np.array([p.z for p in tx_manifold])

    tx_hologram = SpatialLightModulator.create_hologram(tx_manifold)
    fiber = MultimodeFiber(modes=resolution, seed=seed)

    rx_speckle = fiber.transmit(tx_hologram)

    # Adversary: reconstruct directly from speckle (no T†)
    adv_manifold = SpatialLightModulator.reconstruct_manifold(rx_speckle, z_coords)
    phi_adversary = measure_holographic_coherence(tx_manifold, adv_manifold, sample_size)

    # Authorized receiver: apply DPC then reconstruct
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
