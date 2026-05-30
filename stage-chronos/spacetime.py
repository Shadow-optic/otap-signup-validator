from dataclasses import dataclass


@dataclass
class SpacetimePoint:
    """A point in 4D Minkowski spacetime (t, x, y, z), c=1 units."""
    t: float
    x: float
    y: float
    z: float

    def interval(self, other: "SpacetimePoint") -> float:
        """
        Spacetime interval ds² = -dt² + dx² + dy² + dz²
        Signature (-, +, +, +).  Returns the same value in all
        inertial frames (Lorentz invariant).
        """
        dt = self.t - other.t
        dx = self.x - other.x
        dy = self.y - other.y
        dz = self.z - other.z
        return -(dt ** 2) + (dx ** 2) + (dy ** 2) + (dz ** 2)
