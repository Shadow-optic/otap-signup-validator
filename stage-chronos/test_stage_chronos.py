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
    FiberSpec,
    OtapFiberChannel,
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
# OtapFiberChannel
# ---------------------------------------------------------------------------

def test_pmd_preserves_intervals():
    """SO(3) PMD rotation is an isometry — Φ must equal 1.0 for any rotation."""
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=10)
    for seed in (1, 7, 42, 99):
        ch = OtapFiberChannel(FiberSpec.pmd_only(seed))
        rx = ch.apply(tx)
        phi, mse = gck.measure_coherence(tx, rx, sample_size=30)
        assert phi > 0.9999, (
            f"PMD (seed={seed}) broke interval invariance: Φ={phi}, MSE={mse}"
        )


def test_cd_degrades_coherence():
    """Uncompensated CD reduces Φ below the clean-fiber threshold."""
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=15)

    # 1 km: negligible — still within pass threshold
    ch_clean = OtapFiberChannel(FiberSpec.uncompensated_smf(1.0))
    phi_clean, _ = gck.measure_coherence(tx, ch_clean.apply(tx), sample_size=30)
    assert phi_clean > 0.99, f"1 km SMF should be clean: Φ={phi_clean}"

    # 80 km: clear failure
    ch_bad = OtapFiberChannel(FiberSpec.uncompensated_smf(80.0))
    phi_bad, _ = gck.measure_coherence(tx, ch_bad.apply(tx), sample_size=30)
    assert phi_bad < 0.50, f"80 km uncompensated SMF should fail: Φ={phi_bad}"


def test_cd_monotonic_with_length():
    """Φ decreases monotonically as fiber length increases (more CD = worse)."""
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=12)
    prev_phi = 1.0
    for km in (0, 5, 20, 80, 200):
        spec = FiberSpec.uncompensated_smf(float(km)) if km > 0 else FiberSpec.ideal()
        rx = OtapFiberChannel(spec).apply(tx)
        phi, _ = gck.measure_coherence(tx, rx, sample_size=30)
        assert phi <= prev_phi + 1e-10, (
            f"Φ should not increase with more CD: {km}km Φ={phi} > prev {prev_phi}"
        )
        prev_phi = phi


def test_pdl_degrades_coherence():
    """PDL is non-unitary — even small values destroy manifold geometry."""
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=12)
    ch = OtapFiberChannel(FiberSpec.pdl_impaired(1.0))
    phi, _ = gck.measure_coherence(tx, ch.apply(tx), sample_size=30)
    assert phi < 0.50, f"1 dB PDL should fail coherence check: Φ={phi}"


def test_ideal_fiber_perfect_coherence():
    """Ideal fiber (no impairments) → Φ = 1.0 exactly."""
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=10)
    ch = OtapFiberChannel(FiberSpec.ideal())
    phi, mse = gck.measure_coherence(tx, ch.apply(tx), sample_size=30)
    assert math.isclose(phi, 1.0, abs_tol=1e-10), f"Ideal fiber: Φ={phi}"
    assert math.isclose(mse, 0.0, abs_tol=1e-20), f"Ideal fiber: MSE={mse}"


def test_pmd_strictly_better_than_cd():
    """PMD gives higher Φ than uncompensated CD at any distance."""
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=12)
    phi_pmd, _ = gck.measure_coherence(
        tx, OtapFiberChannel(FiberSpec.pmd_only(42)).apply(tx), sample_size=30
    )
    phi_cd, _ = gck.measure_coherence(
        tx, OtapFiberChannel(FiberSpec.uncompensated_smf(80.0)).apply(tx), sample_size=30
    )
    assert phi_pmd > phi_cd, (
        f"PMD should preserve geometry better than 80 km CD: "
        f"Φ_pmd={phi_pmd}, Φ_cd={phi_cd}"
    )


def test_long_haul_fails():
    """Long-haul link (CD + DGD + EDFA noise combined) definitively fails."""
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=12)
    ch = OtapFiberChannel(FiberSpec.long_haul(500, 6))
    phi, _ = gck.measure_coherence(tx, ch.apply(tx), sample_size=30)
    assert phi < 0.10, f"500 km long-haul should completely fail: Φ={phi}"


def test_fiber_channel_preserves_point_count():
    """All impairments must return the same number of points."""
    enc = STAGEManifoldEncoder()
    tx = enc.encode_torus(1, 0.0, resolution=10)
    specs = [
        FiberSpec.ideal(),
        FiberSpec.pmd_only(0),
        FiberSpec.uncompensated_smf(80),
        FiberSpec.high_dgd(50),
        FiberSpec.pdl_impaired(2.0),
        FiberSpec.long_haul(200, 3),
    ]
    for spec in specs:
        rx = OtapFiberChannel(spec).apply(tx)
        assert len(rx) == len(tx), (
            f"Point count changed after {spec}: {len(rx)} != {len(tx)}"
        )


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
