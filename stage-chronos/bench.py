#!/usr/bin/env python3
"""
Comprehensive STAGE-CHRONOS benchmark suite.

Covers:
  1. encode_torus         — resolution scaling (10 → 100)
  2. apply_lorentz_boost  — velocity scaling (0.1c → 0.999c)
  3. apply_decoherence_noise — noise level and manifold size
  4. measure_coherence    — sample_size and manifold size
  5. End-to-end pipeline  — resolution × velocity grid
  6. Throughput summary   — ops/s for each operation class
"""

import math
import sys
import time
from typing import Callable, Any

from .coherence import GeometricCoherenceKernel
from .encoder import STAGEManifoldEncoder
from .transport import SpacetimeTransport


# ---------------------------------------------------------------------------
# Timing helpers
# ---------------------------------------------------------------------------

def _time_it(fn: Callable[[], Any], repeat: int = 5, warmup: int = 1) -> float:
    """Return median wall-clock time (seconds) over `repeat` calls."""
    for _ in range(warmup):
        fn()
    times = []
    for _ in range(repeat):
        t0 = time.perf_counter()
        fn()
        times.append(time.perf_counter() - t0)
    times.sort()
    return times[len(times) // 2]  # median


def _fmt_time(seconds: float) -> str:
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
    encoder = STAGEManifoldEncoder()
    print(f"  {'Resolution':>12}  {'Points':>8}  {'Time':>10}  {'ns/point':>10}")
    print(f"  {'-'*12}  {'-'*8}  {'-'*10}  {'-'*10}")
    for res in (5, 10, 20, 30, 50, 75, 100):
        n_pts = res * res
        t = _time_it(lambda r=res: encoder.encode_torus(1, 0.0, r))
        ns_per = t / n_pts * 1e9
        print(f"  {res:>12}  {n_pts:>8}  {_fmt_time(t)}  {ns_per:>10.1f}")


# ---------------------------------------------------------------------------
# 2. apply_lorentz_boost — velocity scaling
# ---------------------------------------------------------------------------

def bench_lorentz_boost() -> None:
    _hdr("2. SpacetimeTransport.apply_lorentz_boost  (resolution=20)")
    encoder = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    manifold = encoder.encode_torus(1, 0.0, resolution=20)
    n_pts = len(manifold)

    velocities = [0.1, 0.3, 0.5, 0.7, 0.85, 0.9, 0.95, 0.99, 0.999]
    print(f"  {'velocity (c)':>14}  {'gamma':>8}  {'Time':>10}  {'ns/point':>10}")
    print(f"  {'-'*14}  {'-'*8}  {'-'*10}  {'-'*10}")
    for v in velocities:
        gamma = 1.0 / math.sqrt(1.0 - v ** 2)
        t = _time_it(lambda vel=v: transport.apply_lorentz_boost(manifold, vel))
        ns_per = t / n_pts * 1e9
        print(f"  {v:>14.3f}  {gamma:>8.3f}  {_fmt_time(t)}  {ns_per:>10.1f}")


# ---------------------------------------------------------------------------
# 3. apply_decoherence_noise — noise level × manifold size
# ---------------------------------------------------------------------------

def bench_decoherence_noise() -> None:
    _hdr("3. SpacetimeTransport.apply_decoherence_noise")
    encoder = STAGEManifoldEncoder()
    transport = SpacetimeTransport()

    noise_levels = [0.001, 0.01, 0.1, 1.0]
    resolutions = [10, 20, 50]

    print(f"  {'res':>5}  {'points':>7}  {'noise σ':>9}  {'Time':>10}  {'ns/pt':>8}")
    print(f"  {'-'*5}  {'-'*7}  {'-'*9}  {'-'*10}  {'-'*8}")
    for res in resolutions:
        manifold = encoder.encode_torus(1, 0.0, resolution=res)
        n_pts = len(manifold)
        for noise in noise_levels:
            t = _time_it(
                lambda m=manifold, n=noise: transport.apply_decoherence_noise(m, n, seed=0)
            )
            ns_per = t / n_pts * 1e9
            print(f"  {res:>5}  {n_pts:>7}  {noise:>9.3f}  {_fmt_time(t)}  {ns_per:>8.1f}")


# ---------------------------------------------------------------------------
# 4. measure_coherence — sample_size × manifold size
# ---------------------------------------------------------------------------

def bench_coherence_kernel() -> None:
    _hdr("4. GeometricCoherenceKernel.measure_coherence")
    encoder = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    gck = GeometricCoherenceKernel()

    print(f"  {'res':>5}  {'pts':>6}  {'sample':>8}  {'mode':>9}  {'Time':>10}  {'Phi':>8}")
    print(f"  {'-'*5}  {'-'*6}  {'-'*8}  {'-'*9}  {'-'*10}  {'-'*8}")

    for res in (10, 20, 50):
        tx = encoder.encode_torus(1, 0.0, resolution=res)
        n_pts = len(tx)
        rx_boost = transport.apply_lorentz_boost(tx, 0.85)
        rx_noise = transport.apply_decoherence_noise(tx, 0.1, seed=0)

        for sample in (20, 50, 100):
            for label, rx in [("boost", rx_boost), ("noise", rx_noise)]:
                phi, _ = gck.measure_coherence(tx, rx, sample_size=sample)
                t = _time_it(
                    lambda a=tx, b=rx, s=sample: gck.measure_coherence(a, b, s)
                )
                print(
                    f"  {res:>5}  {n_pts:>6}  {sample:>8}  {label:>9}"
                    f"  {_fmt_time(t)}  {phi:>8.5f}"
                )


# ---------------------------------------------------------------------------
# 5. End-to-end pipeline — resolution × velocity grid
# ---------------------------------------------------------------------------

def bench_pipeline() -> None:
    _hdr("5. End-to-end pipeline: encode → boost → coherence")
    encoder = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    gck = GeometricCoherenceKernel()

    print(f"  {'res':>5}  {'pts':>6}  {'v (c)':>8}  {'γ':>6}  {'Time':>10}  {'Φ':>8}")
    print(f"  {'-'*5}  {'-'*6}  {'-'*8}  {'-'*6}  {'-'*10}  {'-'*8}")

    for res in (10, 20, 50):
        for v in (0.1, 0.5, 0.85, 0.99):
            gamma = 1.0 / math.sqrt(1.0 - v ** 2)

            def pipeline(r=res, vel=v):
                tx = encoder.encode_torus(1, 0.0, r)
                rx = transport.apply_lorentz_boost(tx, vel)
                phi, _ = gck.measure_coherence(tx, rx, sample_size=50)
                return phi

            phi = pipeline()
            t = _time_it(pipeline)
            n_pts = res * res
            print(
                f"  {res:>5}  {n_pts:>6}  {v:>8.3f}  {gamma:>6.2f}"
                f"  {_fmt_time(t)}  {phi:>8.5f}"
            )


# ---------------------------------------------------------------------------
# 6. Throughput summary
# ---------------------------------------------------------------------------

def bench_throughput() -> None:
    _hdr("6. Throughput summary  (resolution=20, v=0.85c, σ=0.1)")
    encoder = STAGEManifoldEncoder()
    transport = SpacetimeTransport()
    gck = GeometricCoherenceKernel()

    res = 20
    manifold = encoder.encode_torus(1, 0.0, resolution=res)
    n_pts = len(manifold)
    boosted = transport.apply_lorentz_boost(manifold, 0.85)

    cases = [
        ("encode_torus (res=20)",
         lambda: encoder.encode_torus(1, 0.0, resolution=res)),
        ("apply_lorentz_boost (res=20, v=0.85c)",
         lambda: transport.apply_lorentz_boost(manifold, 0.85)),
        ("apply_decoherence_noise (res=20, σ=0.1)",
         lambda: transport.apply_decoherence_noise(manifold, 0.1, seed=0)),
        ("measure_coherence (res=20, boost, sample=50)",
         lambda: gck.measure_coherence(manifold, boosted, sample_size=50)),
        ("full pipeline (encode+boost+coherence)",
         lambda: gck.measure_coherence(
             encoder.encode_torus(1, 0.0, resolution=res),
             transport.apply_lorentz_boost(encoder.encode_torus(1, 0.0, resolution=res), 0.85),
             sample_size=50,
         )),
    ]

    print(f"  {'Operation':<46}  {'Time':>10}  {'ops/s':>10}")
    print(f"  {'-'*46}  {'-'*10}  {'-'*10}")
    for label, fn in cases:
        t = _time_it(fn, repeat=10, warmup=2)
        ops_per_s = 1.0 / t
        print(f"  {label:<46}  {_fmt_time(t)}  {ops_per_s:>10,.0f}")


# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------

def run_all() -> None:
    print("╔══════════════════════════════════════════════════════════════════╗")
    print("║         STAGE-CHRONOS  Comprehensive Benchmark Suite            ║")
    print("╚══════════════════════════════════════════════════════════════════╝")

    bench_encode_torus()
    bench_lorentz_boost()
    bench_decoherence_noise()
    bench_coherence_kernel()
    bench_pipeline()
    bench_throughput()

    print(f"\n{'═' * 68}")
    print("  Done.")
    print(f"{'═' * 68}\n")


if __name__ == "__main__":
    run_all()
