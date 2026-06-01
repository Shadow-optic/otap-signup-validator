#!/usr/bin/env python3
"""
Comprehensive performance benchmark for the stage-chronos geometric encoding stack.

Measures latency (ns/call), throughput (Mops/s and Gbps-equivalent), and memory
for all six geometric sub-encoding schemes:

  A  TPPEncoder          – Toroidal Phase-Polarisation (T² product-point grid)
  B  HopfKnotEncoder     – OAM-polarisation torus knots (30 coprime types)
  C  NPMEncoder          – Nested Polarisation Microconstellation (Fibonacci sphere)
  D  E8Encoder           – E₈ lattice 240-vector kissing constellation
  E  BerryPhaseEncoder   – Berry / Pancharatnam geometric phase (circular path)
  F  GIEEncoder          – Unified Geometric Integrity Encoding (all DoFs)
  G  capacity_projection – Multi-scheme capacity scaling vs baseline 115 Tbps
"""

import sys
import os
import time
import math
import statistics
import tracemalloc
from typing import Callable, Dict, List, Tuple, Any

# ── Import path setup ─────────────────────────────────────────────────────────
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if REPO_ROOT not in sys.path:
    sys.path.insert(0, REPO_ROOT)

from stage_chronos import (
    TPPEncoder,
    HopfKnotEncoder,
    NPMEncoder,
    E8Encoder,
    BerryPhaseEncoder,
    GIEEncoder,
    GIESymbol,
    capacity_projection,
    SCHEME_BITS,
    SCHEME_TRL,
    GeometricCoherenceKernel,
)
from stage_chronos.geometric_encoding import (
    _build_e8_kissing_vectors,
    _e8_vectors,
)

# ── Measurement helpers ───────────────────────────────────────────────────────

WARMUP_ITERS = 200
MEASURE_ITERS = 2_000


def _bench_fn(fn: Callable, n: int = MEASURE_ITERS, warmup: int = WARMUP_ITERS) -> Dict:
    """Time `fn()` for `n` iterations; return statistics dict (all in nanoseconds)."""
    # Warmup
    for _ in range(warmup):
        fn()

    times_ns = []
    for _ in range(n):
        t0 = time.perf_counter_ns()
        fn()
        times_ns.append(time.perf_counter_ns() - t0)

    times_ns.sort()
    mean = statistics.mean(times_ns)
    stdev = statistics.stdev(times_ns) if len(times_ns) > 1 else 0.0
    p50 = times_ns[int(0.50 * n)]
    p90 = times_ns[int(0.90 * n)]
    p95 = times_ns[int(0.95 * n)]
    p99 = times_ns[int(0.99 * n)]
    p999 = times_ns[min(int(0.999 * n), n - 1)]
    ops_per_sec = 1e9 / mean if mean > 0 else float("inf")
    return {
        "n": n,
        "min_ns": times_ns[0],
        "max_ns": times_ns[-1],
        "mean_ns": mean,
        "stdev_ns": stdev,
        "p50_ns": p50,
        "p90_ns": p90,
        "p95_ns": p95,
        "p99_ns": p99,
        "p999_ns": p999,
        "ops_per_sec": ops_per_sec,
        "mops_per_sec": ops_per_sec / 1e6,
    }


def _mem_bytes(fn: Callable) -> int:
    """Return peak memory allocated (bytes) during one call to fn()."""
    tracemalloc.start()
    fn()
    _, peak = tracemalloc.get_traced_memory()
    tracemalloc.stop()
    return peak


# ── Formatting ────────────────────────────────────────────────────────────────

def _fmt_ns(ns: float) -> str:
    if ns < 1_000:
        return f"{ns:.1f} ns"
    elif ns < 1_000_000:
        return f"{ns/1e3:.2f} µs"
    else:
        return f"{ns/1e6:.3f} ms"


def _hr(ch: str = "─", n: int = 76) -> str:
    return ch * n


def _section(title: str) -> None:
    print()
    print(_hr("═"))
    print(f"  {title}")
    print(_hr("═"))


def _row(label: str, r: Dict, extra: str = "") -> None:
    print(
        f"  {label:<38}  "
        f"p50={_fmt_ns(r['p50_ns']):<12}  "
        f"p99={_fmt_ns(r['p99_ns']):<12}  "
        f"mean={_fmt_ns(r['mean_ns']):<12}  "
        f"{r['mops_per_sec']:.3f} Mops/s"
        + (f"  {extra}" if extra else "")
    )


def _table_header() -> None:
    print(
        f"  {'Benchmark':<38}  "
        f"{'P50':^18}  "
        f"{'P99':^18}  "
        f"{'Mean':^18}  "
        f"{'Throughput':^14}"
    )
    print(f"  {_hr('─', 38)}  {_hr('─', 18)}  {_hr('─', 18)}  {_hr('─', 18)}  {_hr('─', 14)}")


# ═════════════════════════════════════════════════════════════════════════════
# Section A: TPPEncoder
# ═════════════════════════════════════════════════════════════════════════════

def bench_tpp() -> List[Tuple[str, Dict]]:
    results = []

    tpp_8 = TPPEncoder(R=2.0, r=0.5, M1=8, M2=8)
    tpp_16 = TPPEncoder(R=2.0, r=0.5, M1=16, M2=16)
    tpp_32 = TPPEncoder(R=2.0, r=0.5, M1=32, M2=32)

    results.append(("tpp.encode(single point)", _bench_fn(lambda: tpp_8.encode(3, 5))))
    results.append(("tpp.full_constellation(8×8=64 pts)", _bench_fn(lambda: tpp_8.full_constellation())))
    results.append(("tpp.full_constellation(16×16=256 pts)", _bench_fn(lambda: tpp_16.full_constellation())))
    results.append(("tpp.full_constellation(32×32=1024 pts)", _bench_fn(lambda: tpp_32.full_constellation())))
    results.append(("tpp.min_distance()", _bench_fn(lambda: tpp_8.min_distance())))
    results.append(("tpp.capacity_bits()", _bench_fn(lambda: tpp_8.capacity_bits())))
    return results


# ═════════════════════════════════════════════════════════════════════════════
# Section B: HopfKnotEncoder
# ═════════════════════════════════════════════════════════════════════════════

def bench_hopf() -> List[Tuple[str, Dict]]:
    results = []
    enc = HopfKnotEncoder(num_points=100)

    results.append(("hopf.encode(2,3) — T(2,3) trefoil", _bench_fn(lambda: enc.encode(2, 3))))
    results.append(("hopf.encode(3,5) — T(3,5) torus", _bench_fn(lambda: enc.encode(3, 5))))
    results.append(("hopf.encode(2,7) — T(2,7) torus", _bench_fn(lambda: enc.encode(2, 7))))
    results.append(("hopf.encode_by_index(0)", _bench_fn(lambda: enc.encode_by_index(0))))
    results.append(("hopf.encode_by_index(15)", _bench_fn(lambda: enc.encode_by_index(15))))
    # Full sweep over all valid knot types
    results.append((
        f"hopf.encode all {len(enc.VALID_KNOTS)} valid knots",
        _bench_fn(lambda: [enc.encode(p, q) for p, q in enc.VALID_KNOTS]),
    ))
    results.append(("hopf.topology_bits()", _bench_fn(lambda: enc.topology_bits())))
    return results


# ═════════════════════════════════════════════════════════════════════════════
# Section C: NPMEncoder
# ═════════════════════════════════════════════════════════════════════════════

def bench_npm() -> List[Tuple[str, Dict]]:
    results = []

    for K in [8, 16, 32, 64]:
        npm = NPMEncoder(K=K, epsilon=0.1)
        results.append((
            f"npm._fibonacci_sphere(K={K})",
            _bench_fn(lambda k=K, n=npm: n._fibonacci_sphere(k)),
        ))
        results.append((
            f"npm.full_microconstellation(K={K})",
            _bench_fn(lambda n=npm: n.full_microconstellation(1.0, 0.0)),
        ))

    npm16 = NPMEncoder(K=16)
    results.append(("npm.encode(single point, K=16)", _bench_fn(lambda: npm16.encode(0.5, -0.5, 8))))
    results.append(("npm.capacity_bits(K=16)", _bench_fn(lambda: npm16.capacity_bits())))
    return results


# ═════════════════════════════════════════════════════════════════════════════
# Section D: E8Encoder
# ═════════════════════════════════════════════════════════════════════════════

def bench_e8() -> List[Tuple[str, Dict]]:
    results = []
    enc = E8Encoder()

    # Cold build (first call constructs 240 vectors)
    from stage_chronos import geometric_encoding as ge
    results.append((
        "e8._build_kissing_vectors() [cold]",
        _bench_fn(lambda: _build_e8_kissing_vectors(), n=200, warmup=5),
    ))

    # Warm singleton
    _e8_vectors()  # prime the cache
    results.append((
        "e8._e8_vectors() [warm singleton]",
        _bench_fn(lambda: _e8_vectors()),
    ))

    # Per-symbol encode
    results.append(("e8.encode_4d(symbol=42)", _bench_fn(lambda: enc.encode_4d(42))))
    results.append(("e8.encode_8d(symbol=42)", _bench_fn(lambda: enc.encode_8d(42))))

    # Full constellation
    results.append((
        "e8.full_constellation_4d() [240 pts]",
        _bench_fn(lambda: enc.full_constellation_4d()),
    ))

    # All 240 encode_8d calls
    results.append((
        "e8.encode_8d × 240 symbols",
        _bench_fn(lambda: [enc.encode_8d(i) for i in range(240)]),
    ))

    results.append(("e8.coding_gain_db()", _bench_fn(lambda: enc.coding_gain_db())))
    results.append(("e8.total_gain_db()", _bench_fn(lambda: enc.total_gain_db())))
    results.append(("e8.capacity_bits()", _bench_fn(lambda: enc.capacity_bits())))
    return results


# ═════════════════════════════════════════════════════════════════════════════
# Section E: BerryPhaseEncoder
# ═════════════════════════════════════════════════════════════════════════════

def bench_berry() -> List[Tuple[str, Dict]]:
    results = []

    for M in [16, 32, 64]:
        berry = BerryPhaseEncoder(M=M, num_path_points=32)
        results.append((
            f"berry.encode(phase_idx=0, M={M})",
            _bench_fn(lambda b=berry: b.encode(0)),
        ))
        results.append((
            f"berry.encode(phase_idx=M//2, M={M})",
            _bench_fn(lambda b=berry, m=M: b.encode(m // 2)),
        ))
        results.append((
            f"berry.full sweep M={M} levels",
            _bench_fn(lambda b=berry, m=M: [b.encode(i) for i in range(m)]),
        ))

    berry64 = BerryPhaseEncoder(M=64)
    results.append(("berry.berry_phase(31)", _bench_fn(lambda: berry64.berry_phase(31))))
    results.append(("berry.solid_angle(31)", _bench_fn(lambda: berry64.solid_angle(31))))
    results.append(("berry.capacity_bits()", _bench_fn(lambda: berry64.capacity_bits())))
    return results


# ═════════════════════════════════════════════════════════════════════════════
# Section F: GIEEncoder + GIESymbol
# ═════════════════════════════════════════════════════════════════════════════

def bench_gie() -> List[Tuple[str, Dict]]:
    results = []
    gie = GIEEncoder(oam_levels=8, berry_levels=64, pol_M1=8, pol_M2=8)

    results.append(("gie.encode(single symbol)", _bench_fn(lambda: gie.encode(2, 3, 15, 31, 1.0))))

    # Pre-encode a symbol for sub-benchmarks
    sym = gie.encode(2, 3, 15, 31, 1.0)
    results.append((
        "gie_symbol.to_spacetime_points()",
        _bench_fn(lambda: sym.to_spacetime_points()),
    ))

    # measure_phi: before == after (identity, Φ → 1.0)
    results.append((
        "gie.measure_phi(identity, Φ≈1.0)",
        _bench_fn(lambda: gie.measure_phi(sym, sym), n=500, warmup=50),
    ))

    results.append(("gie.capacity_bits()", _bench_fn(lambda: gie.capacity_bits())))
    results.append(("gie.theoretical_max_bits()", _bench_fn(lambda: gie.theoretical_max_bits())))
    return results


# ═════════════════════════════════════════════════════════════════════════════
# Section G: capacity_projection
# ═════════════════════════════════════════════════════════════════════════════

def bench_capacity() -> List[Tuple[str, Dict]]:
    results = []
    all_schemes = list(SCHEME_BITS.keys())

    results.append(("capacity_projection(no schemes)", _bench_fn(lambda: capacity_projection([]))))
    results.append(("capacity_projection(A_TPP only)", _bench_fn(lambda: capacity_projection(["A_TPP"]))))
    results.append((
        "capacity_projection(A+B+C)",
        _bench_fn(lambda: capacity_projection(["A_TPP", "B_Hopf", "C_NPM"])),
    ))
    results.append((
        f"capacity_projection(all {len(all_schemes)} schemes)",
        _bench_fn(lambda: capacity_projection(all_schemes)),
    ))
    return results


# ═════════════════════════════════════════════════════════════════════════════
# Section H: Memory profiling
# ═════════════════════════════════════════════════════════════════════════════

def bench_memory() -> List[Tuple[str, int]]:
    results = []

    tpp = TPPEncoder(M1=16, M2=16)
    results.append(("tpp.full_constellation(16×16=256 pts)", _mem_bytes(lambda: tpp.full_constellation())))

    enc_hopf = HopfKnotEncoder(num_points=100)
    results.append(("hopf.encode_all_knots()", _mem_bytes(lambda: [enc_hopf.encode(p, q) for p, q in enc_hopf.VALID_KNOTS])))

    results.append(("e8._build_kissing_vectors()", _mem_bytes(lambda: _build_e8_kissing_vectors())))

    npm = NPMEncoder(K=64)
    results.append(("npm.full_microconstellation(K=64)", _mem_bytes(lambda: npm.full_microconstellation(0.0, 0.0))))

    berry = BerryPhaseEncoder(M=64, num_path_points=32)
    results.append(("berry.full_sweep(M=64)", _mem_bytes(lambda: [berry.encode(i) for i in range(64)])))

    gie = GIEEncoder()
    sym = gie.encode(2, 3, 15, 31, 1.0)
    results.append(("gie_symbol.to_spacetime_points()", _mem_bytes(lambda: sym.to_spacetime_points())))

    return results


# ═════════════════════════════════════════════════════════════════════════════
# Capacity summary
# ═════════════════════════════════════════════════════════════════════════════

def print_capacity_summary() -> None:
    _section("G  CAPACITY PROJECTION  (incremental + combined, baseline 115 Tbps)")
    print(f"  {'Scheme':<28}  {'Bits Added':>10}  {'TRL':>4}  {'Projected Capacity':>20}  {'Uplift':>8}")
    print(f"  {_hr('─', 28)}  {_hr('─', 10)}  {_hr('─', 4)}  {_hr('─', 20)}  {_hr('─', 8)}")

    baseline = 115.0  # Tbps
    cumulative_schemes: List[str] = []
    for name, bits in SCHEME_BITS.items():
        cumulative_schemes.append(name)
        tbps = capacity_projection(cumulative_schemes)
        uplift_pct = (tbps - baseline) / baseline * 100.0
        trl = SCHEME_TRL.get(name, "?")
        print(
            f"  {name:<28}  {bits:>10.1f}  {trl:>4}  {tbps:>19.1f}T  {uplift_pct:>+7.1f}%"
        )

    print()
    all_tbps = capacity_projection(list(SCHEME_BITS.keys()))
    print(f"  Baseline (no schemes)  : {baseline:.1f} Tbps")
    print(f"  All schemes combined   : {all_tbps:.1f} Tbps")
    print(f"  Total capacity uplift  : {(all_tbps - baseline)/baseline * 100:.1f}%")


# ═════════════════════════════════════════════════════════════════════════════
# Main
# ═════════════════════════════════════════════════════════════════════════════

def main() -> None:
    print(_hr("═"))
    print("  OTAP Stage-Chronos — Geometric Encoding Benchmark Suite")
    print(_hr("═"))
    print(f"  Python   : {sys.version.split()[0]}")
    print(f"  Warmup   : {WARMUP_ITERS} iters  |  Measurement : {MEASURE_ITERS} iters")
    print(f"  Schemes  : {', '.join(SCHEME_BITS.keys())}")
    print(f"  Capacity : {capacity_projection([]):.1f} Tbps baseline → {capacity_projection(list(SCHEME_BITS.keys())):.1f} Tbps with all schemes")

    sections = [
        ("A  TPPEncoder  (Toroidal Phase-Polarisation, +6.0 bits)", bench_tpp),
        ("B  HopfKnotEncoder  (OAM torus knots, +12.5 bits)", bench_hopf),
        ("C  NPMEncoder  (Fibonacci micro-constellation, +4.0 bits)", bench_npm),
        ("D  E8Encoder  (E₈ lattice 240-kissing, +3.66 dB gain)", bench_e8),
        ("E  BerryPhaseEncoder  (Poincaré circle path, +4.0 bits)", bench_berry),
        ("F  GIEEncoder  (Unified all-DoF encoding + Φ integrity)", bench_gie),
    ]

    all_rows: Dict[str, Dict] = {}

    for title, bench_fn in sections:
        _section(title)
        _table_header()
        rows = bench_fn()
        for label, r in rows:
            _row(label, r)
            all_rows[label] = r
        print()
        # Print section summary
        section_mops = [r["mops_per_sec"] for _, r in rows]
        fastest = max(section_mops)
        slowest = min(section_mops)
        print(f"  Section range: {_fmt_ns(min(r['p50_ns'] for _, r in rows))} – {_fmt_ns(max(r['p50_ns'] for _, r in rows))} P50")
        print(f"  Fastest: {fastest:.3f} Mops/s  |  Slowest: {slowest:.4f} Mops/s")

    # Capacity projection table
    bench_capacity_rows = bench_capacity()
    _section("G  capacity_projection  (multi-scheme 115 Tbps scaling)")
    _table_header()
    for label, r in bench_capacity_rows:
        _row(label, r)

    print_capacity_summary()

    # Memory profiling
    _section("H  MEMORY ALLOCATION PROFILING  (peak bytes per call)")
    print(f"  {'Benchmark':<50}  {'Peak Alloc':>12}")
    print(f"  {_hr('─', 50)}  {_hr('─', 12)}")
    for label, peak_bytes in bench_memory():
        if peak_bytes < 1024:
            mem_str = f"{peak_bytes} B"
        elif peak_bytes < 1024 * 1024:
            mem_str = f"{peak_bytes/1024:.1f} KiB"
        else:
            mem_str = f"{peak_bytes/1024/1024:.2f} MiB"
        print(f"  {label:<50}  {mem_str:>12}")

    # Aggregate summary
    _section("SUMMARY — All Geometric Encoding Benchmarks")
    print(f"  {'Benchmark':<50}  {'P50':>14}  {'P99':>14}  {'Mops/s':>10}")
    print(f"  {_hr('─', 50)}  {_hr('─', 14)}  {_hr('─', 14)}  {_hr('─', 10)}")
    for label, r in sorted(all_rows.items(), key=lambda x: x[1]["p50_ns"]):
        print(
            f"  {label:<50}  {_fmt_ns(r['p50_ns']):>14}  {_fmt_ns(r['p99_ns']):>14}  {r['mops_per_sec']:>9.3f}M"
        )

    print()
    print(_hr("═"))
    print("  Done.")
    print(_hr("═"))


if __name__ == "__main__":
    main()
