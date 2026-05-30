"""Unit tests for STAGE-CHRONOS components."""
import math
import sys
import os

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from stage_chronos import (
    SpacetimePoint,
    GeometricCoherenceKernel,
    STAGEManifoldEncoder,
    SpacetimeTransport,
)


# ---------------------------------------------------------------------------
# SpacetimePoint
# ---------------------------------------------------------------------------

def test_interval_same_point():
    p = SpacetimePoint(1.0, 2.0, 3.0, 4.0)
    assert p.interval(p) == 0.0


def test_interval_spacelike():
    origin = SpacetimePoint(0, 0, 0, 0)
    space = SpacetimePoint(0, 3, 4, 0)
    assert math.isclose(origin.interval(space), 25.0)


def test_interval_timelike():
    origin = SpacetimePoint(0, 0, 0, 0)
    future = SpacetimePoint(5, 0, 0, 0)
    assert math.isclose(origin.interval(future), -25.0)


# ---------------------------------------------------------------------------
# STAGEManifoldEncoder
# ---------------------------------------------------------------------------

def test_encode_torus_point_count():
    enc = STAGEManifoldEncoder()
    m = enc.encode_torus(0, 0.0, resolution=10)
    assert len(m) == 100


def test_encode_torus_time_coordinate():
    enc = STAGEManifoldEncoder()
    m = enc.encode_torus(1, 3.14, resolution=5)
    for p in m:
        assert math.isclose(p.t, 3.14), f"expected t=3.14, got {p.t}"


def test_encode_torus_symbol_modulates_radius():
    enc = STAGEManifoldEncoder()
    m0 = enc.encode_torus(0, 0.0, resolution=10)
    m1 = enc.encode_torus(1, 0.0, resolution=10)
    # Symbol 1 has larger R and r so max spatial extent is larger
    max_r0 = max(math.sqrt(p.x ** 2 + p.y ** 2 + p.z ** 2) for p in m0)
    max_r1 = max(math.sqrt(p.x ** 2 + p.y ** 2 + p.z ** 2) for p in m1)
    assert max_r1 > max_r0


# ---------------------------------------------------------------------------
# SpacetimeTransport — Lorentz boost
# ---------------------------------------------------------------------------

def test_lorentz_boost_preserves_intervals():
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    tx = enc.encode_torus(1, 0.0, resolution=8)
    rx = transport.apply_lorentz_boost(tx, 0.6)
    for i in range(0, len(tx) - 1, 10):
        ds2_tx = tx[i].interval(tx[i + 1])
        ds2_rx = rx[i].interval(rx[i + 1])
        assert math.isclose(ds2_tx, ds2_rx, rel_tol=1e-9), (
            f"Interval not preserved at i={i}: {ds2_tx} vs {ds2_rx}"
        )


def test_lorentz_boost_point_count():
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    tx = enc.encode_torus(0, 0.0, resolution=5)
    rx = transport.apply_lorentz_boost(tx, 0.5)
    assert len(rx) == len(tx)


def test_lorentz_boost_invalid_velocity():
    transport = SpacetimeTransport()
    try:
        transport.apply_lorentz_boost([], 1.0)
        assert False, "expected ValueError"
    except ValueError:
        pass


def test_lorentz_boost_identity_at_zero():
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    tx = enc.encode_torus(0, 1.0, resolution=5)
    rx = transport.apply_lorentz_boost(tx, 0.0)
    for a, b in zip(tx, rx):
        assert math.isclose(a.t, b.t, abs_tol=1e-12)
        assert math.isclose(a.x, b.x, abs_tol=1e-12)


# ---------------------------------------------------------------------------
# SpacetimeTransport — decoherence noise
# ---------------------------------------------------------------------------

def test_decoherence_preserves_time():
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    tx = enc.encode_torus(0, 5.0, resolution=5)
    rx = transport.apply_decoherence_noise(tx, 0.1, seed=0)
    for p in rx:
        assert math.isclose(p.t, 5.0)


def test_decoherence_changes_spatial():
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    tx = enc.encode_torus(1, 0.0, resolution=10)
    rx = transport.apply_decoherence_noise(tx, 1.0, seed=7)
    diffs = [
        abs(a.x - b.x) + abs(a.y - b.y) + abs(a.z - b.z)
        for a, b in zip(tx, rx)
    ]
    assert any(d > 1e-6 for d in diffs), "noise should perturb spatial coordinates"


# ---------------------------------------------------------------------------
# GeometricCoherenceKernel
# ---------------------------------------------------------------------------

def test_coherence_lorentz_boost_near_one():
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=15)
    rx = transport.apply_lorentz_boost(tx, 0.85)
    phi, mse = gck.measure_coherence(tx, rx)
    assert phi > 0.99, f"Lorentz boost should give Phi ≈ 1, got {phi}"
    assert mse < 1e-20, f"MSE should be near zero, got {mse}"


def test_coherence_noise_below_half():
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=15)
    rx = transport.apply_decoherence_noise(tx, 0.5, seed=99)
    phi, _ = gck.measure_coherence(tx, rx)
    assert phi < 0.5, f"Heavy noise should give Phi < 0.5, got {phi}"


def test_coherence_identical_manifolds():
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(0, 0.0, resolution=10)
    phi, mse = gck.measure_coherence(tx, tx[:])
    assert math.isclose(phi, 1.0, abs_tol=1e-12)
    assert math.isclose(mse, 0.0, abs_tol=1e-30)


# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------

def _run_all():
    tests = [v for k, v in globals().items() if k.startswith("test_")]
    passed = failed = 0
    for fn in tests:
        try:
            fn()
            print(f"  [PASS] {fn.__name__}")
            passed += 1
        except Exception as exc:
            print(f"  [FAIL] {fn.__name__}: {exc}")
            failed += 1
    print(f"\n  {passed} passed, {failed} failed")
    if failed:
        sys.exit(1)


if __name__ == "__main__":
    print("Running STAGE-CHRONOS unit tests...\n")
    _run_all()
