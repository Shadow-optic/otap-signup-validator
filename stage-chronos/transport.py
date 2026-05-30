import math
from typing import List

import numpy as np

from .spacetime import SpacetimePoint


class SpacetimeTransport:
    """Simulates physical transport through spacetime."""

    @staticmethod
    def apply_lorentz_boost(
        manifold: List[SpacetimePoint],
        velocity: float,
    ) -> List[SpacetimePoint]:
        """
        Apply a Lorentz boost in the X-direction.

        velocity is a fraction of c (|v| < 1).
        Preserves spacetime intervals exactly (Lorentz invariance).
        """
        if not (-1.0 < velocity < 1.0):
            raise ValueError(
                f"velocity must satisfy |v| < 1 (c=1), got {velocity}"
            )
        gamma = 1.0 / math.sqrt(1.0 - velocity ** 2)
        boosted: List[SpacetimePoint] = []
        for p in manifold:
            t_new = gamma * (p.t - velocity * p.x)
            x_new = gamma * (p.x - velocity * p.t)
            boosted.append(SpacetimePoint(t_new, x_new, p.y, p.z))
        return boosted

    @staticmethod
    def apply_decoherence_noise(
        manifold: List[SpacetimePoint],
        noise_level: float,
        seed: int = None,
    ) -> List[SpacetimePoint]:
        """
        Simulate environmental decoherence by perturbing spatial coordinates
        with Gaussian noise N(0, noise_level).  Time coordinate is untouched.
        """
        rng = np.random.default_rng(seed)
        n = len(manifold)
        dx = rng.normal(0, noise_level, n)
        dy = rng.normal(0, noise_level, n)
        dz = rng.normal(0, noise_level, n)
        return [
            SpacetimePoint(p.t, p.x + dx[i], p.y + dy[i], p.z + dz[i])
            for i, p in enumerate(manifold)
        ]
