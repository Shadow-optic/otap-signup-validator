"""
PDL Detection Margin Sweep — calibrated coherence mapping.

Uses a normalized RMS interval deviation metric that avoids the saturation
problem of exp(-mse*k) mappings:

  rms_rel  = sqrt(mean((ds²_rx - ds²_tx)²)) / sqrt(mean(ds²_tx²))
             Dimensionless; ~0 for intact geometry, grows with distortion.

  phi_cal  = 1 / (1 + rms_rel)
             Monotonic, gentle mapping; no saturation over 0–3 dB PDL range.

This makes the detection threshold a defensible engineering choice rather
than an artifact of a gain constant.
"""

import math
from typing import List, Tuple, Optional

import numpy as np

from .spacetime import SpacetimePoint
from .encoder import STAGEManifoldEncoder


def _apply_pdl(manifold: List[SpacetimePoint], pdl_db: float) -> List[SpacetimePoint]:
    """Apply simple single-axis PDL: attenuate y by 10^(-pdl_db/20)."""
    ratio = 10 ** (-pdl_db / 20.0)
    return [SpacetimePoint(p.t, p.x, p.y * ratio, p.z) for p in manifold]


def measure_normalized_deviation(
    tx: List[SpacetimePoint],
    rx: List[SpacetimePoint],
    sample_size: int = 50,
) -> Tuple[float, float]:
    """
    Compute normalized RMS interval deviation and calibrated coherence.

    Returns
    -------
    rms_rel : float
        RMS interval deviation normalized by the RMS interval magnitude of
        the reference (tx) manifold.  Dimensionless.  ~0 for intact; grows
        monotonically with distortion.
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
    Sweep PDL from 0 to 3 dB and measure calibrated coherence at each point.

    Returns list of (pdl_db, phi_cal, rms_rel) tuples.
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
        rx = _apply_pdl(tx, pdl_db)
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
