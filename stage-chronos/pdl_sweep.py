"""
PDL Detection Margin Sweep — calibrated coherence mapping.

Physical model
--------------
PDL is applied via OtapFiberChannel(FiberSpec.pdl_impaired(pdl_db)), which
implements the full 3-axis Jones-matrix amplitude attenuation:

    x' = α_s1 · x,   α_s1 = 10^(−PDL_s1 / 20)
    y' = α_s2 · y,   α_s2 = 10^(−PDL_s2 / 20)  [PDL_s2 = PDL_s1 / 2]
    z' = α_s3 · z,   α_s3 = 10^(−PDL_s3 / 20)  [PDL_s3 = PDL_s1 / 4]

This models a realistic PMD-coupled fiber where the principal-state axes
have unequal PDL magnitudes.  The 3-axis scaling is non-unitary and cannot
be compensated by any SO(3) rotation — it irrevocably distorts spacetime
intervals, making it detectable by the STAGE-CHRONOS coherence kernel.

Metric
------
  rms_rel  = sqrt(mean((ds²_rx − ds²_tx)²)) / sqrt(mean(ds²_tx²))
             Dimensionless; ≈0 for intact geometry, grows monotonically.

  phi_cal  = 1 / (1 + rms_rel)
             Monotonic, gentle.  No saturation over the 0–3 dB PDL range.
             Directly comparable to GeometricCoherenceKernel.measure_coherence
             which uses the same interval-pair sampling.

Detection threshold
-------------------
  phi_cal > 0.95  PASS   (negligible distortion)
  phi_cal < 0.90  ALARM  (≈1.0 dB PDL, within rogue-tap floor)
  phi_cal < 0.80  FAIL

This makes the alarm threshold a defensible engineering choice tied to a
measured physical quantity (PDL in dB) rather than an artefact of a gain
constant k in exp(−MSE·k).
"""

import math
from typing import List, Tuple, Optional

import numpy as np

from .spacetime import SpacetimePoint
from .encoder import STAGEManifoldEncoder
from .fiber_channel import FiberSpec, OtapFiberChannel


def _apply_pdl_reference(
    manifold: List[SpacetimePoint], pdl_db: float
) -> List[SpacetimePoint]:
    """
    Reference single-axis PDL model (y-axis only).

    Kept for unit-test comparison only.  Production sweeps use
    OtapFiberChannel(FiberSpec.pdl_impaired(pdl_db)) which applies a
    realistic 3-axis model tied to the documented Jones-matrix parameters.
    """
    ratio = 10 ** (-pdl_db / 20.0)
    return [SpacetimePoint(p.t, p.x, p.y * ratio, p.z) for p in manifold]


def apply_pdl_calibrated(
    manifold: List[SpacetimePoint], pdl_db: float
) -> List[SpacetimePoint]:
    """
    Apply PDL via OtapFiberChannel with FiberSpec.pdl_impaired.

    Physical model (from fiber_channel.py FiberSpec.pdl_impaired):
        PDL_s1 = pdl_db
        PDL_s2 = pdl_db / 2
        PDL_s3 = pdl_db / 4
        α_i    = 10^(−PDL_i / 20)

    This is the citable model: any change to the Jones-matrix mapping in
    fiber_channel.py automatically propagates here, ensuring the calibration
    curve stays consistent with the CHRONOS detection numbers.
    """
    if pdl_db == 0.0:
        return manifold
    spec = FiberSpec.pdl_impaired(pdl_db)
    ch = OtapFiberChannel(spec)
    return ch.apply(manifold)


def measure_normalized_deviation(
    tx: List[SpacetimePoint],
    rx: List[SpacetimePoint],
    sample_size: int = 50,
) -> Tuple[float, float]:
    """
    Compute normalized RMS interval deviation and calibrated coherence.

    Samples `sample_size` pairs from the manifolds using a uniform stride.
    Uses the same interval formula as GeometricCoherenceKernel so that
    phi_cal and the main Φ metric are directly comparable.

    Returns
    -------
    rms_rel : float
        RMS interval deviation normalized by the RMS interval magnitude of
        the reference (tx) manifold.  Dimensionless.  ≈0 for intact geometry;
        grows monotonically with distortion.
    phi_cal : float
        Calibrated coherence in (0, 1].  phi_cal = 1 / (1 + rms_rel).
        Monotonic, gentle; no saturation over the 0–3 dB PDL range.
    """
    n = len(tx)
    step = max(1, n // sample_size)

    sq_dev = 0.0
    sq_ref = 0.0
    comparisons = 0
    for i in range(0, n, step):
        for j in range(i + step, n, step):
            ds2_b = tx[i].interval(tx[j])
            ds2_a = rx[i].interval(rx[j])
            sq_dev += (ds2_a - ds2_b) ** 2
            sq_ref += ds2_b ** 2
            comparisons += 1

    rms_dev = math.sqrt(sq_dev / max(1, comparisons))
    rms_ref = math.sqrt(sq_ref / max(1, comparisons))
    rms_rel = rms_dev / max(1e-12, rms_ref)
    phi_cal = 1.0 / (1.0 + rms_rel)
    return rms_rel, phi_cal


def run_pdl_sweep(
    resolution: int = 20,
    sample_size: int = 50,
) -> List[Tuple[float, float, float]]:
    """
    Sweep PDL from 0 to 3 dB using the calibrated OtapFiberChannel model.

    Each point applies FiberSpec.pdl_impaired(pdl_db) — the full 3-axis
    Jones-matrix attenuation — to a res×res torus manifold, then measures
    the calibrated coherence phi_cal.

    Returns
    -------
    List of (pdl_db, phi_cal, rms_rel) tuples, sorted by pdl_db ascending.
    """
    encoder = STAGEManifoldEncoder()
    tx = encoder.encode_torus(symbol=1, time_t=0.0, resolution=resolution)

    pdl_points = (
        list(np.linspace(0.0, 0.5, 26)) +
        list(np.linspace(0.6, 1.5, 10)) +
        list(np.linspace(1.75, 3.0, 6))
    )

    results = []
    for pdl_db in pdl_points:
        rx = apply_pdl_calibrated(tx, pdl_db)
        rms_rel, phi_cal = measure_normalized_deviation(tx, rx, sample_size)
        results.append((pdl_db, phi_cal, rms_rel))
    return results


def crossing(
    results: List[Tuple[float, float, float]],
    thr_phi: float,
) -> Optional[float]:
    """Return the first PDL value (dB) where phi_cal drops below thr_phi."""
    for pdl_db, phi, _ in results:
        if phi < thr_phi:
            return pdl_db
    return None
