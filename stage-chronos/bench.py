#!/usr/bin/env python3
"""
Comprehensive STAGE-CHRONOS benchmark suite.

Sections
--------
1. encode_torus         — resolution scaling (5 → 100)
2. apply_lorentz_boost  — velocity scaling (0.1c → 0.999c)
3. apply_decoherence_noise — noise level and manifold size
4. measure_coherence    — sample_size and manifold size
5. Fiber channel: CD sweep   — DL 0 → 8500 ps/nm (0–500 km SMF-28)
6. Fiber channel: DGD sweep  — 0 → 1000 ps
7. Fiber channel: PDL sweep  — 0 → 6 dB
8. Fiber channel vs Lorentz  — why PMD passes and CD fails
9. End-to-end pipeline  — resolution × velocity grid
10. Throughput summary  — ops/s for each operation class
"""

import math
import time
from typing import Callable, Any

import numpy as np

from .coherence import GeometricCoherenceKernel
from .encoder import STAGEManifoldEncoder
from .fiber_channel import FiberSpec, OtapFiberChannel
from .transport import SpacetimeTransport
from .drift import DynamicCoherenceTracker, LinkState, run_drift_scenario, chronos_slowramp_sweep
from .layered import LayeredTracker, run_layered_sweep
from .pdl_sweep import measure_normalized_deviation, run_pdl_sweep, crossing
from .holographic import (
    STAGEHelixEncoder, SpatialLightModulator, MultimodeFiber,
    measure_holographic_coherence, run_holographic_pipeline,
)


# ---------------------------------------------------------------------------
# Timing helpers
# ---------------------------------------------------------------------------

def _time_it(fn: Callable[[], Any], repeat: int = 5, warmup: int = 1) -> float:
    for _ in range(warmup):
        fn()
    times = []
    for _ in range(repeat):
        t0 = time.perf_counter()
        fn()
        times.append(time.perf_counter() - t0)
    times.sort()
    return times[len(times) // 2]


def _fmt(seconds: float) -> str:
    if seconds < 1e-6:
        return f"{seconds * 1e9:7.1f} ns"
    if seconds < 1e-3:
        return f"{seconds * 1e6:7.1f} µs"
    if seconds < 1.0:
        return f"{seconds * 1e3:7.1f} ms"
    return f"{seconds:7.3f}  s"


def _hdr(title: str) -> None:
    print(f"\n{'─' * 68}")
    print(f"  {title}")
    print(f"{'─' * 68}")


# ---------------------------------------------------------------------------
# 1. encode_torus — resolution scaling
# ---------------------------------------------------------------------------

def bench_encode_torus() -> None:
    _hdr("1. STAGEManifoldEncoder.encode_torus  (symbol=1, t=0)")
    enc = STAGEManifoldEncoder()
    print(f"  {'Resolution':>10}  {'Points':>8}  {'Time':>10}  {'ns/point':>10}")
    print(f"  {'-'*10}  {'-'*8}  {'-'*10}  {'-'*10}")
    for res in (5, 10, 20, 30, 50, 75, 100):
        t = _time_it(lambda r=res: enc.encode_torus(1, 0.0, r))
        n = res * res
        print(f"  {res:>10}  {n:>8}  {_fmt(t)}  {t / n * 1e9:>10.1f}")


# ---------------------------------------------------------------------------
# 2. apply_lorentz_boost — velocity scaling
# ---------------------------------------------------------------------------

def bench_lorentz_boost() -> None:
    _hdr("2. SpacetimeTransport.apply_lorentz_boost  (resolution=20)")
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    m = enc.encode_torus(1, 0.0, resolution=20)
    n = len(m)
    print(f"  {'v/c':>8}  {'γ':>7}  {'Time':>10}  {'ns/point':>10}")
    print(f"  {'-'*8}  {'-'*7}  {'-'*10}  {'-'*10}")
    for v in (0.1, 0.3, 0.5, 0.7, 0.85, 0.95, 0.99, 0.999):
        gamma = 1.0 / math.sqrt(1.0 - v ** 2)
        t = _time_it(lambda vel=v: transport.apply_lorentz_boost(m, vel))
        print(f"  {v:>8.3f}  {gamma:>7.3f}  {_fmt(t)}  {t / n * 1e9:>10.1f}")


# ---------------------------------------------------------------------------
# 3. apply_decoherence_noise — size × noise level
# ---------------------------------------------------------------------------

def bench_decoherence_noise() -> None:
    _hdr("3. SpacetimeTransport.apply_decoherence_noise")
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    print(f"  {'res':>5}  {'pts':>7}  {'σ':>8}  {'Time':>10}  {'ns/pt':>8}")
    print(f"  {'-'*5}  {'-'*7}  {'-'*8}  {'-'*10}  {'-'*8}")
    for res in (10, 20, 50):
        m = enc.encode_torus(1, 0.0, resolution=res)
        for noise in (0.001, 0.01, 0.1, 1.0):
            t = _time_it(lambda mf=m, n=noise: transport.apply_decoherence_noise(mf, n, seed=0))
            print(f"  {res:>5}  {len(m):>7}  {noise:>8.3f}  {_fmt(t)}  {t/len(m)*1e9:>8.1f}")


# ---------------------------------------------------------------------------
# 4. measure_coherence — sample_size × manifold size
# ---------------------------------------------------------------------------

def bench_coherence_kernel() -> None:
    _hdr("4. GeometricCoherenceKernel.measure_coherence")
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    gck = GeometricCoherenceKernel()
    print(f"  {'res':>5}  {'pts':>6}  {'sample':>8}  {'mode':>9}  {'Time':>10}  {'Φ':>8}")
    print(f"  {'-'*5}  {'-'*6}  {'-'*8}  {'-'*9}  {'-'*10}  {'-'*8}")
    for res in (10, 20, 50):
        tx = enc.encode_torus(1, 0.0, resolution=res)
        rx_boost = transport.apply_lorentz_boost(tx, 0.85)
        rx_noise = transport.apply_decoherence_noise(tx, 0.1, seed=0)
        for samp in (20, 50, 100):
            for label, rx in [("boost", rx_boost), ("noise", rx_noise)]:
                phi, _ = gck.measure_coherence(tx, rx, sample_size=samp)
                t = _time_it(lambda a=tx, b=rx, s=samp: gck.measure_coherence(a, b, s))
                print(f"  {res:>5}  {len(tx):>6}  {samp:>8}  {label:>9}  {_fmt(t)}  {phi:>8.5f}")


# ---------------------------------------------------------------------------
# 5. Fiber channel: CD sweep (DL = D×L, ps/nm)
# ---------------------------------------------------------------------------

def bench_fiber_cd() -> None:
    _hdr("5. OtapFiberChannel — Chromatic Dispersion sweep  (SMF-28, D=17 ps/(nm·km))")
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=20)

    print(f"  CD is a t-x shear (NOT a Lorentz transform) → Φ decays with D·L.")
    print(f"  Threshold: Φ > 0.90 PASS, Φ < 0.50 FAIL\n")
    print(f"  {'Length(km)':>12}  {'DL(ps/nm)':>10}  {'Φ':>9}  {'MSE':>12}  {'Status':>9}  {'Time':>10}")
    print(f"  {'-'*12}  {'-'*10}  {'-'*9}  {'-'*12}  {'-'*9}  {'-'*10}")

    lengths = [0, 1, 5, 10, 20, 40, 80, 160, 300, 500]
    for km in lengths:
        spec = FiberSpec.uncompensated_smf(float(km)) if km > 0 else FiberSpec.ideal()
        ch = OtapFiberChannel(spec)
        rx = ch.apply(tx)
        phi, mse = gck.measure_coherence(tx, rx, sample_size=50)
        dl = spec.cd_ps_per_nm_per_km * spec.length_km
        status = "PASS" if phi > 0.90 else ("FAIL" if phi < 0.50 else "MARGINAL")
        t = _time_it(lambda s=spec: OtapFiberChannel(s).apply(
            enc.encode_torus(1, 0.0, resolution=20)))
        print(f"  {km:>12}  {dl:>10.0f}  {phi:>9.5f}  {mse:>12.4e}  {status:>9}  {_fmt(t)}")


# ---------------------------------------------------------------------------
# 6. Fiber channel: DGD sweep
# ---------------------------------------------------------------------------

def bench_fiber_dgd() -> None:
    _hdr("6. OtapFiberChannel — DGD sweep  (PSP time split)")
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=20)

    print(f"  DGD is a position-dependent t-shear (NOT Lorentz) → Φ ∝ τ⁴.")
    print(f"  ITU-T G.691 tolerance at 100G: ≤ 2.5 ps.  Typical impaired: > 50 ps.\n")
    print(f"  {'DGD(ps)':>10}  {'Φ':>9}  {'MSE':>12}  {'Status':>9}")
    print(f"  {'-'*10}  {'-'*9}  {'-'*12}  {'-'*9}")
    for dgd in (0, 1, 5, 10, 25, 50, 100, 200, 500, 1000):
        spec = FiberSpec.high_dgd(float(dgd)) if dgd > 0 else FiberSpec.ideal()
        ch = OtapFiberChannel(spec)
        rx = ch.apply(tx)
        phi, mse = gck.measure_coherence(tx, rx, sample_size=50)
        status = "PASS" if phi > 0.90 else ("FAIL" if phi < 0.50 else "MARGINAL")
        print(f"  {dgd:>10}  {phi:>9.5f}  {mse:>12.4e}  {status:>9}")


# ---------------------------------------------------------------------------
# 7. Fiber channel: PDL sweep
# ---------------------------------------------------------------------------

def bench_fiber_pdl() -> None:
    _hdr("7. OtapFiberChannel — PDL sweep  (per-axis amplitude asymmetry)")
    enc = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=20)

    print(f"  PDL is non-unitary (cannot be SO(3)) → Φ collapses even at sub-dB levels.")
    print(f"  Typical rogue tap: 1–3 dB PDL.  FFA detects at < 1 dB.\n")
    print(f"  {'PDL_s1(dB)':>12}  {'α_s1':>8}  {'Φ':>9}  {'MSE':>12}  {'Status':>9}")
    print(f"  {'-'*12}  {'-'*8}  {'-'*9}  {'-'*12}  {'-'*9}")
    for pdl in (0.0, 0.1, 0.25, 0.5, 1.0, 2.0, 3.0, 6.0):
        spec = FiberSpec.pdl_impaired(pdl) if pdl > 0 else FiberSpec.ideal()
        ch = OtapFiberChannel(spec)
        rx = ch.apply(tx)
        phi, mse = gck.measure_coherence(tx, rx, sample_size=50)
        alpha = 10 ** (-pdl / 20)
        status = "PASS" if phi > 0.90 else ("FAIL" if phi < 0.50 else "MARGINAL")
        print(f"  {pdl:>12.2f}  {alpha:>8.4f}  {phi:>9.5f}  {mse:>12.4e}  {status:>9}")


# ---------------------------------------------------------------------------
# 8. Fiber channel vs Lorentz — the key contrast
# ---------------------------------------------------------------------------

def bench_fiber_vs_lorentz() -> None:
    _hdr("8. Fiber PMD vs Lorentz boost  (why PMD passes and CD fails)")
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=20)

    print(f"  Both PMD and a Lorentz boost are SO(3)/SO(1,3) transforms that mix")
    print(f"  spatial/spacetime coordinates.  The key difference:")
    print(f"    Lorentz boost: t' = γ(t−βx), x' = γ(x−βt) → ds² conserved.")
    print(f"    PMD rotation:  x',y',z' = R·(x,y,z)         → ds² conserved (all t_i=0).")
    print(f"    CD shear:      t' = t + k·x, x unchanged     → ds² NOT conserved.\n")

    print(f"  {'Transform':>32}  {'param':>10}  {'Φ':>9}  {'isometric?':>12}")
    print(f"  {'-'*32}  {'-'*10}  {'-'*9}  {'-'*12}")

    boosts = [(0.3, "v=0.30c"), (0.7, "v=0.70c"), (0.85, "v=0.85c"), (0.99, "v=0.99c")]
    for v, lbl in boosts:
        rx = transport.apply_lorentz_boost(tx, v)
        phi, _ = gck.measure_coherence(tx, rx, sample_size=50)
        print(f"  {'Lorentz boost  ' + lbl:>32}  {v:>10.2f}  {phi:>9.5f}  {'YES (SO(1,3))':>12}")

    for seed in (1, 7, 42, 99):
        spec = FiberSpec.pmd_only(seed)
        ch = OtapFiberChannel(spec)
        rx = ch.apply(tx)
        phi, _ = gck.measure_coherence(tx, rx, sample_size=50)
        angle = math.sqrt(spec.pmd_s1**2 + spec.pmd_s2**2 + spec.pmd_s3**2)
        lbl = f"PMD rotation (seed={seed})"
        print(f"  {lbl:>32}  {angle:>10.2f}  {phi:>9.5f}  {'YES (SO(3))':>12}")

    for km in (20, 40, 80):
        spec = FiberSpec.uncompensated_smf(float(km))
        ch = OtapFiberChannel(spec)
        rx = ch.apply(tx)
        phi, _ = gck.measure_coherence(tx, rx, sample_size=50)
        lbl = f"CD shear  {km} km SMF-28"
        dl = spec.cd_ps_per_nm_per_km * spec.length_km
        print(f"  {lbl:>32}  {dl:>10.0f}  {phi:>9.5f}  {'NO (t-shear)':>12}")


# ---------------------------------------------------------------------------
# 9. End-to-end pipeline — resolution × velocity
# ---------------------------------------------------------------------------

def bench_pipeline() -> None:
    _hdr("9. End-to-end pipeline  (encode → Lorentz boost → coherence)")
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    gck = GeometricCoherenceKernel()
    print(f"  {'res':>5}  {'pts':>6}  {'v/c':>7}  {'γ':>6}  {'Time':>10}  {'Φ':>8}")
    print(f"  {'-'*5}  {'-'*6}  {'-'*7}  {'-'*6}  {'-'*10}  {'-'*8}")
    for res in (10, 20, 50):
        for v in (0.1, 0.5, 0.85, 0.99):
            gamma = 1.0 / math.sqrt(1.0 - v ** 2)

            def pipeline(r=res, vel=v):
                tx = enc.encode_torus(1, 0.0, r)
                rx = transport.apply_lorentz_boost(tx, vel)
                return gck.measure_coherence(tx, rx, sample_size=50)[0]

            phi = pipeline()
            t = _time_it(pipeline)
            print(f"  {res:>5}  {res*res:>6}  {v:>7.3f}  {gamma:>6.2f}  {_fmt(t)}  {phi:>8.5f}")


# ---------------------------------------------------------------------------
# 10. Throughput summary
# ---------------------------------------------------------------------------

def bench_throughput() -> None:
    _hdr("10. Throughput summary  (resolution=20, various impairments)")
    enc = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    gck = GeometricCoherenceKernel()
    tx = enc.encode_torus(1, 0.0, resolution=20)
    rx_boost = transport.apply_lorentz_boost(tx, 0.85)
    rx_cd80  = OtapFiberChannel(FiberSpec.uncompensated_smf(80.0)).apply(tx)

    cases = [
        ("encode_torus (res=20)",
         lambda: enc.encode_torus(1, 0.0, resolution=20)),
        ("apply_lorentz_boost (v=0.85c)",
         lambda: transport.apply_lorentz_boost(tx, 0.85)),
        ("apply_decoherence_noise (σ=0.1)",
         lambda: transport.apply_decoherence_noise(tx, 0.1, seed=0)),
        ("OtapFiberChannel — PMD-only",
         lambda: OtapFiberChannel(FiberSpec.pmd_only(42)).apply(tx)),
        ("OtapFiberChannel — CD 80 km SMF-28",
         lambda: OtapFiberChannel(FiberSpec.uncompensated_smf(80.0)).apply(tx)),
        ("OtapFiberChannel — PDL 1 dB",
         lambda: OtapFiberChannel(FiberSpec.pdl_impaired(1.0)).apply(tx)),
        ("OtapFiberChannel — long-haul 500 km",
         lambda: OtapFiberChannel(FiberSpec.long_haul(500, 6)).apply(tx)),
        ("measure_coherence (sample=50, Lorentz)",
         lambda: gck.measure_coherence(tx, rx_boost, sample_size=50)),
        ("measure_coherence (sample=50, CD 80 km)",
         lambda: gck.measure_coherence(tx, rx_cd80, sample_size=50)),
        ("fiber pipeline (PMD + coherence)",
         lambda: gck.measure_coherence(
             tx,
             OtapFiberChannel(FiberSpec.pmd_only(42)).apply(tx),
             sample_size=50,
         )),
        ("fiber pipeline (CD 80 km + coherence)",
         lambda: gck.measure_coherence(
             tx,
             OtapFiberChannel(FiberSpec.uncompensated_smf(80.0)).apply(tx),
             sample_size=50,
         )),
    ]

    print(f"  {'Operation':<48}  {'Time':>10}  {'ops/s':>10}")
    print(f"  {'-'*48}  {'-'*10}  {'-'*10}")
    for label, fn in cases:
        t = _time_it(fn, repeat=10, warmup=2)
        print(f"  {label:<48}  {_fmt(t)}  {1/t:>10,.0f}")


# ---------------------------------------------------------------------------
# 11. CHRONOS-Drift: DynamicCoherenceTracker throughput
# ---------------------------------------------------------------------------

def bench_drift_tracker() -> None:
    _hdr("11. CHRONOS-Drift — DynamicCoherenceTracker.process() throughput")
    print(f"  Measures per-frame processing latency for the 3-state coherence tracker.")
    print()

    scenarios = [
        ("Step tap (Phi drop from 1.0 to 0.4)", 1.0, 0.4, True),
        ("Stable link (no tap)", 1.0, 1.0, False),
        ("Marginal signal (Phi=0.9)", 0.9, 0.9, False),
    ]

    print(f"  {'T':>6}  {'Scenario':<38}  {'Time':>10}  {'µs/frame':>10}  {'Mframes/s':>11}")
    print(f"  {'-'*6}  {'-'*38}  {'-'*10}  {'-'*10}  {'-'*11}")

    for T in (200, 1000, 5000):
        for label, phi_before, phi_after, has_tap in scenarios:
            tap_at = T // 2
            phi_stream = np.concatenate([
                np.full(tap_at, phi_before),
                np.full(T - tap_at, phi_after),
            ])

            def run_tracker(stream=phi_stream):
                tr = DynamicCoherenceTracker()
                for t, phi in enumerate(stream):
                    tr.process(phi, t)

            t = _time_it(run_tracker, repeat=5, warmup=1)
            print(f"  {T:>6}  {label:<38}  {_fmt(t)}  {t/T*1e6:>10.3f}  {T/t/1e6:>11.3f}")


# ---------------------------------------------------------------------------
# 12. CHRONOS-Drift: scenario analysis
# ---------------------------------------------------------------------------

def bench_drift_scenarios() -> None:
    _hdr("12. CHRONOS-Drift — run_drift_scenario() scenario analysis")
    print(f"  {'Scenario':<38}  {'Time':>10}  {'dyn_tp':>7}  {'dyn_fp':>7}  {'compromised_at':>15}")
    print(f"  {'-'*38}  {'-'*10}  {'-'*7}  {'-'*7}  {'-'*15}")

    cases = [
        ("Mild thermal 0.6 dB tap", dict(name="mild", thermal_amp=0.15, thermal_mid=0.25, tap_pdl=0.6)),
        ("Harsh thermal 0.6 dB tap", dict(name="harsh", thermal_amp=0.275, thermal_mid=0.375, tap_pdl=0.6)),
        ("Harsh thermal 0.4 dB stealth", dict(name="stealth", thermal_amp=0.275, thermal_mid=0.375, tap_pdl=0.4)),
    ]
    for label, kw in cases:
        t = _time_it(lambda k=kw: run_drift_scenario(**k))
        r = run_drift_scenario(**kw)
        print(f"  {label:<38}  {_fmt(t)}  {str(r['dyn_tp']):>7}  {r['dyn_fp']:>7}  {str(r['compromised_at']):>15}")


# ---------------------------------------------------------------------------
# 13. CHRONOS-Drift: slow-ramp sweep
# ---------------------------------------------------------------------------

def bench_slowramp_sweep() -> None:
    _hdr("13. CHRONOS-Drift — slow-ramp adversary sweep (differential detector)")
    print(f"  Ramp rate 1/alpha = ~20 frames is the blind spot for the fast layer.")
    print()
    print(f"  {'Ramp (fr)':>10}  {'Detected':>8}  {'Latency':>9}  {'comp_at':>9}  Note")
    print(f"  {'-'*10}  {'-'*8}  {'-'*9}  {'-'*9}  ----")

    results = chronos_slowramp_sweep()
    for r in results:
        lat = str(r['latency']) if r['latency'] is not None else "—"
        comp = str(r['compromised_at']) if r['compromised_at'] is not None else "—"
        note = "step-like" if r['ramp'] <= 10 else ("blind spot" if not r['detected'] else "ramp caught")
        print(f"  {r['ramp']:>10}  {str(r['detected']):>8}  {lat:>9}  {comp:>9}  {note}")

    t = _time_it(chronos_slowramp_sweep, repeat=3, warmup=1)
    print(f"\n  Full sweep (10 ramp rates): {_fmt(t)}")


# ---------------------------------------------------------------------------
# 14. LayeredTracker: fast + slow detection sweep
# ---------------------------------------------------------------------------

def bench_layered_sweep() -> None:
    _hdr("14. CHRONOS-Drift — LayeredTracker (fast+slow) sweep")
    print(f"  Fast layer catches steps; slow ratchet closes the slow-ramp blind spot.")
    print()
    print(f"  {'Ramp (fr)':>10}  {'Detected':>8}  {'Via':>5}  {'Latency':>9}  {'comp_at':>9}")
    print(f"  {'-'*10}  {'-'*8}  {'-'*5}  {'-'*9}  {'-'*9}")

    results = run_layered_sweep()
    for r in results:
        lat = str(r['latency']) if r['latency'] is not None else "EVADED"
        comp = str(r['comp_at']) if r['comp_at'] is not None else "—"
        via = str(r['via']) if r['via'] is not None else "—"
        print(f"  {r['ramp']:>10}  {str(r['detected']):>8}  {via:>5}  {lat:>9}  {comp:>9}")

    evaded = [r['ramp'] for r in results if not r['detected']]
    print(f"\n  Evaded: {evaded if evaded else 'none — all ramps detected'}")

    t = _time_it(run_layered_sweep, repeat=3, warmup=1)
    print(f"  Full sweep (10 ramp rates): {_fmt(t)}")


# ---------------------------------------------------------------------------
# 15. PDL calibrated sweep
# ---------------------------------------------------------------------------

def bench_pdl_calibrated_sweep() -> None:
    _hdr("15. PDL Calibrated Sweep — phi_cal = 1/(1+rms_rel)")
    print(f"  Normalized metric avoids exp(-mse*k) saturation; gentle & monotonic.")
    print()
    print(f"  {'PDL (dB)':>9}  {'phi_cal':>9}  {'rms_rel':>10}  {'Status':>9}  {'Time':>10}")
    print(f"  {'-'*9}  {'-'*9}  {'-'*10}  {'-'*9}  {'-'*10}")

    enc = STAGEManifoldEncoder()
    tx = enc.encode_torus(symbol=1, time_t=0.0, resolution=20)

    from .pdl_sweep import _apply_pdl
    spot_pdls = [0.0, 0.1, 0.25, 0.5, 1.0, 1.5, 2.0, 3.0]
    for pdl in spot_pdls:
        rx = _apply_pdl(tx, pdl) if pdl > 0 else tx
        rms_rel, phi_cal = measure_normalized_deviation(tx, rx, sample_size=50)
        t = _time_it(lambda r=rx: measure_normalized_deviation(tx, r, 50))
        status = "PASS" if phi_cal > 0.95 else ("ALARM" if phi_cal > 0.80 else "FAIL")
        print(f"  {pdl:>9.2f}  {phi_cal:>9.5f}  {rms_rel:>10.5f}  {status:>9}  {_fmt(t)}")

    results = run_pdl_sweep(resolution=20, sample_size=50)
    print(f"\n  Threshold crossings:")
    for thr in [0.95, 0.90, 0.80]:
        c = crossing(results, thr)
        if c is not None:
            print(f"    phi_cal < {thr:.2f}  at PDL = {c:.3f} dB")
        else:
            print(f"    phi_cal < {thr:.2f}  not reached in 0–3 dB range")

    detect_thr = crossing(results, 0.90)
    if detect_thr is not None:
        margin_lo = 1.0 - detect_thr
        margin_hi = 3.0 - detect_thr
        print(f"\n  Alarm threshold (phi<0.90) at {detect_thr:.3f} dB")
        print(f"  Typical rogue-tap PDL: 1.0–3.0 dB  →  detection margin: {margin_lo:.2f}–{margin_hi:.2f} dB")
        if margin_lo > 0:
            print(f"  [PASS] Alarm fires well below the rogue-tap floor.")


# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# 16. Holographic pipeline — end-to-end latency vs mode count
# ---------------------------------------------------------------------------

def bench_holographic_pipeline() -> None:
    _hdr("16. Holographic MMF+DPC pipeline  (encode→SLM→MMF→DPC→SLM)")
    print(f"  Helix OAM symbol=2; timing for encode+transmit+DPC+reconstruct.")
    print()
    print(f"  {'modes':>7}  {'SLM fwd':>10}  {'MMF tx':>10}  {'DPC rec':>10}  "
          f"{'SLM inv':>10}  {'total':>10}  {'phi_rx':>8}")
    print(f"  {'-'*7}  {'-'*10}  {'-'*10}  {'-'*10}  {'-'*10}  {'-'*10}  {'-'*8}")

    for modes in (32, 64, 128, 256, 512):
        manifold = STAGEHelixEncoder.generate_helix(2, num_points=modes)
        z_coords = np.array([p.z for p in manifold])
        fiber = MultimodeFiber(modes=modes, seed=42)

        t_slm_fwd  = _time_it(lambda m=manifold: SpatialLightModulator.create_hologram(m))
        hologram   = SpatialLightModulator.create_hologram(manifold)
        t_mmf_tx   = _time_it(lambda h=hologram: fiber.transmit(h))
        speckle    = fiber.transmit(hologram)
        t_dpc      = _time_it(lambda s=speckle: fiber.phase_conjugate_recovery(s))
        recovered  = fiber.phase_conjugate_recovery(speckle)
        t_slm_inv  = _time_it(lambda r=recovered, z=z_coords:
                               SpatialLightModulator.reconstruct_manifold(r, z))

        rx_manifold = SpatialLightModulator.reconstruct_manifold(recovered, z_coords)
        phi = measure_holographic_coherence(manifold, rx_manifold, sample_size=20)
        total = t_slm_fwd + t_mmf_tx + t_dpc + t_slm_inv

        print(f"  {modes:>7}  {_fmt(t_slm_fwd):>10}  {_fmt(t_mmf_tx):>10}  "
              f"{_fmt(t_dpc):>10}  {_fmt(t_slm_inv):>10}  {_fmt(total):>10}  {phi:>8.6f}")


# ---------------------------------------------------------------------------
# 17. Holographic security sweep — OAM symbol × fiber seed
# ---------------------------------------------------------------------------

def bench_holographic_security() -> None:
    _hdr("17. Holographic security sweep  (phi_adversary vs phi_receiver)")
    print(f"  Security guarantee: phi_adversary ≈ 0, phi_receiver ≈ 1 for all symbols.")
    print()
    print(f"  {'modes':>7}  {'symbol':>7}  {'seed':>6}  {'phi_adversary':>14}  "
          f"{'phi_receiver':>13}  {'gap':>8}  Security")
    print(f"  {'-'*7}  {'-'*7}  {'-'*6}  {'-'*14}  {'-'*13}  {'-'*8}  --------")

    configs = [
        (64, 0, 42), (64, 1, 42), (64, 2, 42), (64, 5, 42),
        (128, 2, 7), (128, 2, 99),
        (256, 3, 1),
    ]
    for modes, symbol, seed in configs:
        r = run_holographic_pipeline(data_symbol=symbol, resolution=modes, seed=seed)
        gap = r["phi_receiver"] - r["phi_adversary"]
        secure = "[PASS]" if gap > 0.95 else "[WARN]"
        print(f"  {modes:>7}  {symbol:>7}  {seed:>6}  {r['phi_adversary']:>14.8f}  "
              f"{r['phi_receiver']:>13.8f}  {gap:>8.6f}  {secure}")


# ---------------------------------------------------------------------------
# 18. Holographic throughput summary
# ---------------------------------------------------------------------------

def bench_holographic_throughput() -> None:
    _hdr("18. Holographic throughput summary  (modes=128)")
    modes = 128
    manifold = STAGEHelixEncoder.generate_helix(2, num_points=modes)
    z_coords = np.arange(modes) * (2.0 * math.pi / modes)
    fiber = MultimodeFiber(modes=modes, seed=42)
    hologram = SpatialLightModulator.create_hologram(manifold)
    speckle  = fiber.transmit(hologram)
    recovered = fiber.phase_conjugate_recovery(speckle)
    rx_manifold = SpatialLightModulator.reconstruct_manifold(recovered, z_coords)

    cases = [
        ("STAGEHelixEncoder.generate_helix (128 pts)",
         lambda: STAGEHelixEncoder.generate_helix(2, num_points=modes)),
        ("SpatialLightModulator.create_hologram",
         lambda: SpatialLightModulator.create_hologram(manifold)),
        ("MultimodeFiber.transmit (128 modes)",
         lambda: fiber.transmit(hologram)),
        ("MultimodeFiber.phase_conjugate_recovery",
         lambda: fiber.phase_conjugate_recovery(speckle)),
        ("SpatialLightModulator.reconstruct_manifold",
         lambda: SpatialLightModulator.reconstruct_manifold(recovered, z_coords)),
        ("measure_holographic_coherence (sample=20)",
         lambda: measure_holographic_coherence(manifold, rx_manifold, sample_size=20)),
        ("Pipeline  — cached fiber (excl. QR)",
         lambda: run_holographic_pipeline(2, modes, 42, fiber=fiber)),
        ("Pipeline  — incl. fiber init (QR 128×128)",
         lambda: run_holographic_pipeline(2, modes, 42)),
    ]

    print(f"  {'Operation':<54}  {'Time':>10}  {'ops/s':>10}")
    print(f"  {'-'*54}  {'-'*10}  {'-'*10}")
    for label, fn in cases:
        t = _time_it(fn, repeat=5, warmup=1)
        print(f"  {label:<54}  {_fmt(t)}  {1/t:>10,.0f}")


def run_all() -> None:
    print("╔══════════════════════════════════════════════════════════════════╗")
    print("║         STAGE-CHRONOS  Comprehensive Benchmark Suite            ║")
    print("╚══════════════════════════════════════════════════════════════════╝")

    bench_encode_torus()
    bench_lorentz_boost()
    bench_decoherence_noise()
    bench_coherence_kernel()
    bench_fiber_cd()
    bench_fiber_dgd()
    bench_fiber_pdl()
    bench_fiber_vs_lorentz()
    bench_pipeline()
    bench_throughput()
    bench_drift_tracker()
    bench_drift_scenarios()
    bench_slowramp_sweep()
    bench_layered_sweep()
    bench_pdl_calibrated_sweep()
    bench_holographic_pipeline()
    bench_holographic_security()
    bench_holographic_throughput()

    print(f"\n{'═' * 68}")
    print("  Done.")
    print(f"{'═' * 68}\n")


if __name__ == "__main__":
    run_all()
