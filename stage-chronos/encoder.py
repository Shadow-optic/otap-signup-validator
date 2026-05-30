import math
from typing import List

import numpy as np

from .spacetime import SpacetimePoint


class STAGEManifoldEncoder:
    """Encodes digital symbols into 4D Riemannian torus manifolds."""

    @staticmethod
    def encode_torus(
        symbol: int,
        time_t: float,
        resolution: int = 10,
    ) -> List[SpacetimePoint]:
        """
        Embed a data symbol as a torus surface in spacetime.

        Symbol modulates major radius R and minor radius r:
          symbol 0 → R=2.0, r=0.5
          symbol 1 → R=3.0, r=1.0
          symbol n → R=2.0+n, r=0.5+0.5n

        resolution² points are generated on the (θ, φ) grid.
        """
        R = 2.0 + symbol * 1.0
        r = 0.5 + symbol * 0.5

        thetas = np.linspace(0, 2 * np.pi, resolution)
        phis = np.linspace(0, 2 * np.pi, resolution)

        manifold: List[SpacetimePoint] = []
        for theta in thetas:
            cos_theta = math.cos(theta)
            sin_theta = math.sin(theta)
            for phi in phis:
                x = (R + r * cos_theta) * math.cos(phi)
                y = (R + r * cos_theta) * math.sin(phi)
                z = r * sin_theta
                manifold.append(SpacetimePoint(t=time_t, x=x, y=y, z=z))

        return manifold
