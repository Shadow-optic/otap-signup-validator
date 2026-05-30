import math
from typing import List, Tuple

from .spacetime import SpacetimePoint


class GeometricCoherenceKernel:
    """
    Evaluates preservation of spacetime topology (Integrated Coherence).

    Phi_geom = exp(-MSE * 1000)
      - 1.0  → perfect invariance (Lorentz boost, no topology distortion)
      - → 0  → decoherence / structural corruption detected
    """

    @staticmethod
    def measure_coherence(
        points_before: List[SpacetimePoint],
        points_after: List[SpacetimePoint],
        sample_size: int = 50,
    ) -> Tuple[float, float]:
        """
        Compare spacetime intervals before and after transport.

        Returns: (Phi_geom, MSE_deviation)
        """
        assert len(points_before) == len(points_after), (
            "point lists must have equal length"
        )
        num_points = len(points_before)
        step = max(1, num_points // sample_size)

        mse = 0.0
        comparisons = 0

        for i in range(0, num_points, step):
            for j in range(i + step, num_points, step):
                ds2_before = points_before[i].interval(points_before[j])
                ds2_after = points_after[i].interval(points_after[j])
                mse += (ds2_after - ds2_before) ** 2
                comparisons += 1

        mse /= max(1, comparisons)
        phi_geom = math.exp(-mse * 1000)
        return phi_geom, mse
