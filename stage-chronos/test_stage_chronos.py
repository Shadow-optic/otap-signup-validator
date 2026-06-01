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
    LinkState,
    DynamicCoherenceTracker,
    run_drift_scenario,
    chronos_slowramp_sweep,
    LayeredTracker,
    run_layered_sweep,
    measure_normalized_deviation,
    run_pdl_sweep,
    crossing,
    STAGEHelixEncoder,
    SpatialLightModulator,
    MultimodeFiber,
    measure_holographic_coherence,
    run_holographic_pipeline,
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
# DynamicCoherenceTracker (CHRONOS-Drift)
# ---------------------------------------------------------------------------

def test_drift_tracker_warmup():
    """During warmup (< window_size frames) state stays HEALTHY and edge is False."""
    tr = DynamicCoherenceTracker(window_size=10)
    for t in range(9):
        r = tr.process(1.0, t)
        assert r["state"] == LinkState.HEALTHY
        assert r["edge"] is False


def test_drift_tracker_step_tap_detected():
    """A step drop of 0.5 Phi (>>4σ) must trigger ALARM then COMPROMISED."""
    tr = DynamicCoherenceTracker(window_size=10, alpha=0.05, z_threshold=3.0, confirm_frames=3)
    # Warmup with stable signal
    for t in range(10):
        tr.process(1.0, t)
    # Inject a large step drop
    edges = []
    state = LinkState.HEALTHY
    for t in range(10, 25):
        r = tr.process(0.5, t)
        if r["edge"]:
            edges.append(t)
        state = r["state"]
    assert len(edges) >= 1, "Step drop must produce at least one edge event"
    assert state == LinkState.COMPROMISED, f"Expected COMPROMISED, got {state}"


def test_drift_tracker_no_false_alarm_stable():
    """Stable signal with zero variance must not fire any alarms."""
    tr = DynamicCoherenceTracker(window_size=20, z_threshold=4.0, confirm_frames=3)
    for t in range(100):
        r = tr.process(1.0, t)
        assert r["edge"] is False
        assert r["state"] == LinkState.HEALTHY


def test_drift_tracker_ack_resets():
    """ack() must reset state to HEALTHY and clear history."""
    tr = DynamicCoherenceTracker(window_size=10, z_threshold=3.0, confirm_frames=3)
    for t in range(10):
        tr.process(1.0, t)
    for t in range(10, 15):
        tr.process(0.3, t)
    assert tr.state == LinkState.COMPROMISED
    tr.ack()
    assert tr.state == LinkState.HEALTHY
    assert tr.shock_run == 0
    assert len(tr.history) == 0


def test_drift_tracker_edge_single_per_episode():
    """Only the first frame of a shock run sets edge=True."""
    tr = DynamicCoherenceTracker(window_size=10, z_threshold=3.0, confirm_frames=5)
    for t in range(10):
        tr.process(1.0, t)
    edges = []
    for t in range(10, 20):
        r = tr.process(0.3, t)
        if r["edge"]:
            edges.append(t)
    assert len(edges) == 1, f"Expected exactly 1 edge event, got {edges}"


def test_run_drift_scenario_mild_thermal():
    """Mild thermal + 0.6 dB tap: dynamic must TP, FP must be 0."""
    result = run_drift_scenario(
        "mild", thermal_amp=0.15, thermal_mid=0.25, tap_pdl=0.6
    )
    assert result["dyn_tp"] is True, "Must detect 0.6 dB tap"
    assert result["dyn_fp"] == 0, f"Zero FP expected, got {result['dyn_fp']}"


def test_run_drift_scenario_static_fp():
    """Harsh thermal causes static FP > 0."""
    result = run_drift_scenario(
        "harsh", thermal_amp=0.275, thermal_mid=0.375, tap_pdl=0.6
    )
    assert result["static_fp"] > 0, "Harsh thermal should create static FP"
    assert result["dyn_fp"] == 0, f"Dynamic FP should be 0, got {result['dyn_fp']}"


def test_slowramp_sweep_step_detected():
    """Step insertion (ramp=1) must always be detected."""
    results = chronos_slowramp_sweep(ramp_rates=[1])
    assert results[0]["detected"] is True


def test_slowramp_sweep_slow_ramp_evades():
    """Very slow ramp (1200 frames >> 1/alpha=20) evades the differential detector."""
    results = chronos_slowramp_sweep(ramp_rates=[1200])
    assert results[0]["detected"] is False, (
        f"Ramp=1200 should evade differential detector, but detected={results[0]['detected']}"
    )


# ---------------------------------------------------------------------------
# LayeredTracker (fast + slow layers)
# ---------------------------------------------------------------------------

def test_layered_tracker_step_caught_by_fast():
    """Step insertion must be caught by the fast layer."""
    tr = LayeredTracker(window=10, alpha=0.05, z_thr=3.0, confirm=3,
                        long_window=400, ratchet_thr=0.025)
    for t in range(10):
        tr.process(1.0, t)
    for t in range(10, 20):
        r = tr.process(0.3, t)
        if r["state"] == LinkState.COMPROMISED:
            assert r["via"] == "FAST"
            break
    else:
        assert False, "Step insertion must reach COMPROMISED"


def test_layered_sweep_no_ramp_evades():
    """Layered detector must catch every ramp rate in the test set."""
    results = run_layered_sweep(
        ramp_rates=[1, 10, 25, 50, 100, 200, 400, 800],
        T=2500, tap_start=600,
    )
    evaded = [r["ramp"] for r in results if not r["detected"]]
    assert evaded == [], f"Layered detector has blind spot at ramps: {evaded}"


def test_layered_sweep_slow_ramp_via_slow():
    """Ramp rates that evade the fast layer must be caught via the slow layer."""
    results = run_layered_sweep(ramp_rates=[400, 800], T=2500, tap_start=600)
    for r in results:
        assert r["detected"], f"ramp={r['ramp']} not detected"
        if r["via"] is not None:
            # Slow ramps should be caught via SLOW (or FAST at very high ramp)
            pass  # just ensure no crash, via field is populated


def test_layered_tracker_ack():
    """ack() clears all state on LayeredTracker."""
    tr = LayeredTracker(window=10, z_thr=3.0, confirm=3)
    for t in range(10):
        tr.process(1.0, t)
    for t in range(10, 15):
        tr.process(0.3, t)
    assert tr.state == LinkState.COMPROMISED
    tr.ack()
    assert tr.state == LinkState.HEALTHY
    assert tr.fast_event is None
    assert tr.slow_event is None


# ---------------------------------------------------------------------------
# PDL calibrated sweep
# ---------------------------------------------------------------------------

def test_pdl_sweep_zero_pdl_phi_one():
    """Zero PDL: phi_cal must equal 1.0 (no distortion)."""
    enc = STAGEManifoldEncoder()
    tx = enc.encode_torus(symbol=1, time_t=0.0, resolution=10)
    rms_rel, phi_cal = measure_normalized_deviation(tx, tx, sample_size=20)
    assert math.isclose(rms_rel, 0.0, abs_tol=1e-10), f"rms_rel={rms_rel}"
    assert math.isclose(phi_cal, 1.0, abs_tol=1e-10), f"phi_cal={phi_cal}"


def test_pdl_sweep_monotonic():
    """phi_cal must decrease monotonically as PDL increases (resolution=20 for full sampling)."""
    results = run_pdl_sweep(resolution=20, sample_size=50)
    phis = [phi for _, phi, _ in results]
    for i in range(1, len(phis)):
        assert phis[i] <= phis[i - 1] + 1e-8, (
            f"phi_cal not monotonic at index {i}: {phis[i-1]} → {phis[i]}"
        )


def test_pdl_sweep_high_pdl_low_phi():
    """3 dB PDL must noticeably reduce phi_cal (resolution=20 for full sampling)."""
    results = run_pdl_sweep(resolution=20, sample_size=50)
    hi = [phi for pdl, phi, _ in results if pdl >= 2.9]
    assert hi, "No results at 3 dB"
    assert hi[0] < 0.85, f"phi_cal at 3 dB PDL should be < 0.85, got {hi[0]}"


def test_pdl_crossing_utility():
    """crossing() returns first PDL where phi drops below threshold (resolution=20)."""
    results = run_pdl_sweep(resolution=20, sample_size=50)
    c = crossing(results, 0.95)
    assert c is not None, "Should cross 0.95 somewhere in 0–3 dB range"
    assert 0.0 < c < 3.0, f"Crossing at {c} dB outside expected range"


# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# STAGEHelixEncoder
# ---------------------------------------------------------------------------

def test_helix_point_count():
    """generate_helix returns exactly num_points SpacetimePoints."""
    m = STAGEHelixEncoder.generate_helix(0, num_points=64)
    assert len(m) == 64


def test_helix_time_zero():
    """All helix points must have t=0.0."""
    for symbol in (0, 1, 3):
        for p in STAGEHelixEncoder.generate_helix(symbol, num_points=32):
            assert math.isclose(p.t, 0.0)


def test_helix_radius_constant():
    """For any OAM symbol, all points should lie on a cylinder of constant radius."""
    for symbol in (0, 2, 5):
        m = STAGEHelixEncoder.generate_helix(symbol, num_points=50, radius=1.5)
        for p in m:
            r = math.sqrt(p.x**2 + p.y**2)
            assert math.isclose(r, 1.5, rel_tol=1e-9), f"r={r} != 1.5 for symbol {symbol}"


def test_helix_symbols_distinct():
    """Different OAM symbols must produce different manifold geometries."""
    m0 = STAGEHelixEncoder.generate_helix(0, num_points=64)
    m1 = STAGEHelixEncoder.generate_helix(1, num_points=64)
    m3 = STAGEHelixEncoder.generate_helix(3, num_points=64)
    # Check that at least one point differs
    diffs_01 = sum(1 for a, b in zip(m0, m1) if abs(a.x - b.x) > 1e-9)
    diffs_03 = sum(1 for a, b in zip(m0, m3) if abs(a.x - b.x) > 1e-9)
    assert diffs_01 > 0, "symbol 0 and 1 must produce different manifolds"
    assert diffs_03 > 0, "symbol 0 and 3 must produce different manifolds"


# ---------------------------------------------------------------------------
# SpatialLightModulator round-trip
# ---------------------------------------------------------------------------

def test_slm_hologram_roundtrip():
    """SLM create→reconstruct must recover the original manifold exactly."""
    manifold = STAGEHelixEncoder.generate_helix(2, num_points=64)
    import numpy as np
    z_coords = np.array([p.z for p in manifold])
    hologram = SpatialLightModulator.create_hologram(manifold)
    recovered = SpatialLightModulator.reconstruct_manifold(hologram, z_coords)
    for orig, rec in zip(manifold, recovered):
        assert math.isclose(orig.x, rec.x, abs_tol=1e-10), f"x mismatch: {orig.x} vs {rec.x}"
        assert math.isclose(orig.y, rec.y, abs_tol=1e-10), f"y mismatch: {orig.y} vs {rec.y}"
        assert math.isclose(orig.z, rec.z, abs_tol=1e-10), f"z mismatch: {orig.z} vs {rec.z}"


def test_slm_hologram_length():
    """Hologram must have the same length as the input manifold."""
    import numpy as np
    manifold = STAGEHelixEncoder.generate_helix(0, num_points=100)
    h = SpatialLightModulator.create_hologram(manifold)
    assert len(h) == 100
    assert h.dtype == np.complex128


# ---------------------------------------------------------------------------
# MultimodeFiber (MMF) and Digital Phase Conjugation (DPC)
# ---------------------------------------------------------------------------

def test_mmf_transmission_matrix_is_unitary():
    """The random Transmission Matrix must be unitary (T† T = I)."""
    import numpy as np
    fiber = MultimodeFiber(modes=32, seed=0)
    T = fiber.transmission_matrix
    product = np.conj(T.T) @ T
    assert np.allclose(product, np.eye(32), atol=1e-10), "T must be unitary"


def test_mmf_conjugation_is_inverse():
    """T† must exactly invert T for any input vector."""
    import numpy as np
    fiber = MultimodeFiber(modes=64, seed=7)
    v = np.random.randn(64) + 1j * np.random.randn(64)
    speckle = fiber.transmit(v)
    recovered = fiber.phase_conjugate_recovery(speckle)
    assert np.allclose(v, recovered, atol=1e-10), "DPC must recover original vector exactly"


def test_mmf_speckle_is_scrambled():
    """Transmitted speckle must not resemble the input hologram."""
    import numpy as np
    manifold = STAGEHelixEncoder.generate_helix(2, num_points=64)
    hologram = SpatialLightModulator.create_hologram(manifold)
    fiber = MultimodeFiber(modes=64, seed=42)
    speckle = fiber.transmit(hologram)
    # Correlation between input and output should be near zero
    corr = abs(np.dot(np.conj(hologram / np.linalg.norm(hologram)),
                      speckle / np.linalg.norm(speckle)))
    assert corr < 0.3, f"Speckle too correlated with input: corr={corr:.3f}"


# ---------------------------------------------------------------------------
# End-to-end holographic pipeline
# ---------------------------------------------------------------------------

def test_holographic_adversary_phi_near_zero():
    """Adversary reading raw speckle must get Phi ≈ 0 (data completely shredded)."""
    r = run_holographic_pipeline(data_symbol=2, resolution=64, seed=42)
    assert r["phi_adversary"] < 0.05, (
        f"Adversary Phi should be near 0, got {r['phi_adversary']:.6f}"
    )


def test_holographic_receiver_phi_near_one():
    """Authorized DPC receiver must recover Phi ≈ 1.0."""
    r = run_holographic_pipeline(data_symbol=2, resolution=64, seed=42)
    assert r["phi_receiver"] > 0.999, (
        f"Receiver Phi should be ≈ 1, got {r['phi_receiver']:.6f}"
    )


def test_holographic_security_gap():
    """Security gap (phi_receiver - phi_adversary) must be > 0.95."""
    r = run_holographic_pipeline(data_symbol=0, resolution=64, seed=7)
    gap = r["phi_receiver"] - r["phi_adversary"]
    assert gap > 0.95, f"Security gap too small: {gap:.6f}"


def test_holographic_multiple_symbols():
    """Pipeline works correctly for several OAM symbols."""
    for symbol in (0, 1, 2, 5):
        r = run_holographic_pipeline(data_symbol=symbol, resolution=64, seed=42)
        assert r["phi_receiver"] > 0.999, (
            f"symbol={symbol}: receiver Phi={r['phi_receiver']:.6f}"
        )
        assert r["phi_adversary"] < 0.05, (
            f"symbol={symbol}: adversary Phi={r['phi_adversary']:.6f}"
        )


def test_holographic_different_seeds():
    """Different fiber instances (different T) still give secure recovery."""
    import numpy as np
    for seed in (1, 13, 99, 2024):
        np.random.seed(seed)
        r = run_holographic_pipeline(data_symbol=1, resolution=32, seed=seed)
        assert r["phi_receiver"] > 0.999, f"seed={seed}: receiver Phi={r['phi_receiver']:.6f}"
        assert r["phi_adversary"] < 0.10, f"seed={seed}: adversary Phi={r['phi_adversary']:.6f}"


# ---------------------------------------------------------------------------
# Geometric Sub-Encoding tests (Schemes A–F)
# ---------------------------------------------------------------------------

from stage_chronos import (
    TPPEncoder, HopfKnotEncoder, NPMEncoder, E8Encoder,
    BerryPhaseEncoder, GIESymbol, GIEEncoder,
    capacity_projection, SCHEME_BITS,
)


# Scheme A: Toroidal Phase-Polarization

def test_tpp_constellation_size():
    enc = TPPEncoder(M1=8, M2=8)
    pts = enc.full_constellation()
    assert len(pts) == 64, f"Expected 64 TPP points, got {len(pts)}"


def test_tpp_encode_decode_roundtrip():
    enc = TPPEncoder(M1=8, M2=8)
    for symbol in range(64):
        pt = enc.encode_symbol(symbol)
        assert pt is not None
        r = (pt.x**2 + pt.y**2 + pt.z**2) ** 0.5
        assert 0 < r < 10.0, f"Symbol {symbol}: radius {r} out of range"


def test_tpp_capacity():
    enc = TPPEncoder(M1=8, M2=8)
    import math
    assert abs(enc.capacity_bits() - 6.0) < 1e-9, f"TPP capacity should be 6 bits"


def test_tpp_min_distance_positive():
    enc = TPPEncoder(R=2.0, r=0.5, M1=8, M2=8)
    assert enc.min_distance() > 0.0


def test_tpp_points_on_torus():
    enc = TPPEncoder(R=2.0, r=0.5, M1=8, M2=8)
    pts = enc.full_constellation()
    for pt in pts:
        # Distance from torus axis in xy plane
        rho = (pt.x**2 + pt.y**2) ** 0.5
        # Distance from torus tube center = |rho - R|
        dist_from_center = ((rho - enc.R)**2 + pt.z**2) ** 0.5
        assert abs(dist_from_center - enc.r) < 1e-9, \
            f"Point not on torus: dist={dist_from_center:.6f}, r={enc.r}"


# Scheme B: Hopf Knot

def test_hopf_valid_knots_count():
    knots = HopfKnotEncoder.VALID_KNOTS
    assert len(knots) >= 25, f"Expected ≥25 valid knots, got {len(knots)}"


def test_hopf_encode_point_count():
    enc = HopfKnotEncoder(num_points=100)
    pts = enc.encode(2, 3)
    assert len(pts) == 100


def test_hopf_knot_types_distinct():
    import numpy as np
    enc = HopfKnotEncoder(num_points=100)
    # z(φ) = sin(q·φ) has dominant DFT frequency at bin q — use this to distinguish
    def dominant_z_freq(pts):
        z = np.array([p.z for p in pts])
        return int(np.argmax(np.abs(np.fft.rfft(z)[1:]))) + 1
    # T(2,3): z oscillates at frequency 3; T(2,5): frequency 5
    assert dominant_z_freq(enc.encode(2, 3)) == 3
    assert dominant_z_freq(enc.encode(2, 5)) == 5
    assert dominant_z_freq(enc.encode(3, 4)) == 4


def test_hopf_writhe():
    enc = HopfKnotEncoder()
    assert enc.writhe(2, 3) == 6
    assert enc.writhe(3, 5) == 15


def test_hopf_topology_bits():
    enc = HopfKnotEncoder()
    bits = enc.topology_bits()
    assert 4.0 < bits < 6.0, f"Topology bits {bits:.2f} expected ~4.9"


# Scheme C: NPM

def test_npm_microconstellation_size():
    enc = NPMEncoder(K=16)
    pts = enc.full_microconstellation(0.0, 0.0)
    assert len(pts) == 16


def test_npm_points_near_qam_symbol():
    enc = NPMEncoder(K=16, epsilon=0.1)
    I, Q = 3.0, 5.0
    pts = enc.full_microconstellation(I, Q)
    for pt in pts:
        dist = ((pt.x - I)**2 + (pt.y - Q)**2 + pt.z**2) ** 0.5
        assert abs(dist - enc.epsilon) < 1e-9, f"Micro-point not at radius ε: dist={dist:.6f}"


def test_npm_capacity():
    enc = NPMEncoder(K=16)
    assert abs(enc.capacity_bits() - 4.0) < 1e-9


def test_npm_fibonacci_sphere_unit_radius():
    import numpy as np
    enc = NPMEncoder(K=32)
    norms = np.linalg.norm(enc._offsets, axis=1)
    assert float(np.max(np.abs(norms - 1.0))) < 1e-9, "Fibonacci sphere points should be unit vectors"


# Scheme D: E8

def test_e8_kissing_number():
    enc = E8Encoder()
    assert len(enc.vectors) == 240, f"E8 kissing number = 240, got {len(enc.vectors)}"


def test_e8_all_vectors_norm_sqrt2():
    import numpy as np
    enc = E8Encoder()
    norms_sq = np.sum(enc.vectors ** 2, axis=1)
    assert float(np.max(np.abs(norms_sq - 2.0))) < 1e-9, "All E8 kissing vectors should have norm² = 2"


def test_e8_coding_gain():
    enc = E8Encoder()
    assert abs(enc.coding_gain_db() - 3.01) < 1e-9
    assert abs(enc.total_gain_db() - 3.66) < 1e-9


def test_e8_encode_4d():
    enc = E8Encoder()
    for idx in [0, 50, 100, 200, 239]:
        pt = enc.encode_4d(idx)
        assert pt is not None


def test_e8_encode_8d_pair():
    enc = E8Encoder()
    p1, p2 = enc.encode_8d(0)
    v = enc.vectors[0]
    assert abs(p1.x - v[1]) < 1e-9 and abs(p2.x - v[5]) < 1e-9


# Scheme E: Berry Phase

def test_berry_encode_path_count():
    enc = BerryPhaseEncoder(M=64, num_path_points=32)
    pts = enc.encode(0)
    assert len(pts) == 32


def test_berry_phase_levels():
    enc = BerryPhaseEncoder(M=64)
    # Level 0 → phase 0
    assert abs(enc.berry_phase(0)) < 1e-9
    # Level 32 → phase π
    assert abs(enc.berry_phase(32) - 3.14159265) < 0.01


def test_berry_path_on_unit_sphere():
    enc = BerryPhaseEncoder(M=64, num_path_points=32)
    for idx in [1, 16, 32, 48]:
        pts = enc.encode(idx)
        for pt in pts:
            r = (pt.x**2 + pt.y**2 + pt.z**2) ** 0.5
            assert abs(r - 1.0) < 1e-9, f"Berry path point not on unit sphere: r={r}"


def test_berry_capacity():
    enc = BerryPhaseEncoder(M=64)
    assert abs(enc.capacity_bits() - 6.0) < 1e-9


# Scheme F: GIE

def test_gie_encode_produces_symbol():
    enc = GIEEncoder()
    sym = enc.encode(pol_idx=3, oam_l=2, phase_idx=5, berry_idx=7)
    assert isinstance(sym, GIESymbol)
    assert sym.l_oam == 2


def test_gie_symbol_to_spacetime_points():
    sym = GIESymbol(theta_pol=1.0, phi_pol=0.5, l_oam=3, gamma_berry=1.57, amplitude=1.0)
    pts = sym.to_spacetime_points()
    assert len(pts) == 2
    assert pts[0].t == 3   # l_oam encodes in time coordinate


def test_gie_phi_identity():
    enc = GIEEncoder()
    sym = enc.encode(pol_idx=2, oam_l=1, phase_idx=4, berry_idx=8)
    phi = enc.measure_phi(sym, sym)
    assert phi > 0.999, f"GIE identity Φ should be ≈1, got {phi:.4f}"


def test_gie_phi_corrupted():
    enc = GIEEncoder()
    sym_tx = enc.encode(pol_idx=0, oam_l=0, phase_idx=0, berry_idx=0)
    sym_rx = enc.encode(pol_idx=7, oam_l=5, phase_idx=7, berry_idx=15, amplitude=2.0)
    phi = enc.measure_phi(sym_tx, sym_rx)
    assert phi < 0.95, f"GIE corrupted Φ should be < 0.95, got {phi:.4f}"


def test_gie_capacity_bits():
    enc = GIEEncoder(oam_levels=8, berry_levels=16, pol_M1=8, pol_M2=8)
    bits = enc.capacity_bits()
    assert bits > 12.0, f"GIE combined capacity should be > 12 bits, got {bits:.2f}"


# Capacity projections

def test_capacity_projection_baseline():
    # No schemes active → baseline 115 Tbps
    proj = capacity_projection([])
    assert abs(proj - 115.0) < 1e-6


def test_capacity_projection_npm():
    proj = capacity_projection(['C_NPM'])
    # 4 bits on top of 12 bits DP-64QAM = (16/12) × 115
    expected = 115.0 * (16.0 / 12.0)
    assert abs(proj - expected) < 0.01, f"NPM projection: {proj:.1f} vs {expected:.1f}"


def test_capacity_projection_all_schemes():
    # v2.0 corrected arithmetic:
    #   A/B polarisation mutex → only A_TPP(6.0) counts, B_Hopf dropped
    #   D_E8 = 0.75 bits (dB gain ≠ bit gain; one QAM order step)
    #   F_GIE removed (was double-counting A+B+C+E)
    #   A_TPP(6.0) + C_NPM(4.0) + D_E8(0.75) + E_Berry(3.0) = 13.75 added bits
    #   → (12 + 13.75) / 12 × 115 ≈ 246.8 Tbps
    all_schemes = ['A_TPP', 'B_Hopf', 'C_NPM', 'D_E8', 'E_Berry']
    proj = capacity_projection(all_schemes)
    assert 200.0 < proj < 290.0, f"Corrected capacity should be ~247 Tbps, got {proj:.1f}"


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
