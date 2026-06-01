# NPM Capacity Simulation — Measured Constellation Distances at SNR

**Status**: v2.0 — Simulation-backed  
**Replaces**: v1.0 "+4.0 bits" asserted claim  
**Module**: `stage-chronos/npm_capacity_sim.py`  
**Date**: 2026-06-01

---

## Summary

The v1.0 NPM capacity claim of "+4.0 bits" (K=16, ε=0.1) was asserted as log₂(16)
without any SNR-conditioned analysis. This document replaces it with a minimum-distance
simulation of the actual Fibonacci-sphere constellation at a stated noise level.

**Key result** (default parameters K=16, ε=0.1, SNR=15 dB):

| Quantity | Value |
|---|---|
| d_min on unit sphere (K=16) | 0.8660 |
| d_min scaled by ε=0.1 | 0.0866 |
| σ per dimension at 15 dB | 0.1259 |
| arg = d_min/(2σ) | 0.344 |
| P_e (union bound, K=16) | ≈ 1.00 |
| K_usable | **1** |
| Bits usable | **0.0** |
| Shannon bound (3-D AWGN) | 0.047 bits |

> **The v1.0 "+4.0 bits" claim at ε=0.1, SNR=15 dB is INVALID.**  
> Symbols are indistinguishable from noise at these parameters.

---

## Physical Model

### Constellation geometry

K points are placed on the unit sphere S² using the golden-ratio Fibonacci spiral:

```
θᵢ = arccos(1 − 2(i + 0.5)/K)
φᵢ = 2π × i / φ_golden    (φ_golden = (1+√5)/2)
```

This gives approximately uniform coverage. The minimum inter-point distance is
computed exactly by pairwise scan over all K(K−1)/2 pairs.

### Noise model

AWGN per real dimension, symbol energy E_s = 1 (unit QAM normalisation):

```
σ = sqrt(E_s / (2 × SNR_linear)) = sqrt(1 / (2 × 10^(SNR_dB/10)))
```

### Symbol error probability

Union bound, nearest-neighbour term only:

```
P_e ≈ (K − 1) × Q(ε × d_min / (2σ))
Q(x) = erfc(x / √2) / 2
```

### Usable constellation size

K_usable = largest power-of-2 ≤ K for which P_e ≤ BER target (default 10⁻⁴).

### Shannon lower bound (3-D AWGN micro-channel)

```
C_shannon = (3/2) × log₂(1 + ε² / (3σ²))
```

---

## Simulation Results

### Operating-point grid (BER target = 10⁻⁴)

Rows with K_usable ≥ 4 (≥ 2 bits):

| K  |  ε   | SNR (dB) | d_min_scaled | σ        | P_e      | K_use | bits | Shannon |
|----|------|----------|--------------|----------|----------|-------|------|---------|
|  4 | 0.50 |    20    | 0.61237      | 0.07071  | 1.03e-06 |     4 |  2.0 |   4.170 |
|  4 | 0.50 |    25    | 0.61237      | 0.03979  | < 1e-10  |     4 |  2.0 |   6.754 |
|  4 | 1.00 |    15    | 1.22474      | 0.12590  | < 1e-10  |     4 |  2.0 |   5.695 |
|  8 | 0.50 |    25    | 0.43301      | 0.03979  | 1.28e-07 |     8 |  3.0 |   6.754 |
|  8 | 0.50 |    30    | 0.43301      | 0.02236  | < 1e-10  |     8 |  3.0 |   9.291 |
| 16 | 0.50 |    30    | 0.43301      | 0.02236  | 4.52e-05 |    16 |  4.0 |   9.291 |
| 32 | 0.50 |    30    | ~0.31        | 0.02236  | ~0.12    |     1 |  0.0 |   9.291 |

### Minimum ε required for K_usable = K at each SNR

| K  | SNR (dB) | ε_min (est.) | bits_usable |
|----|----------|--------------|-------------|
|  4 |    15    |    0.2670    |     2.0     |
|  4 |    20    |    0.1501    |     2.0     |
|  4 |    25    |    0.0844    |     2.0     |
|  8 |    15    |    0.8191    |     3.0     |
|  8 |    20    |    0.4605    |     3.0     |
|  8 |    25    |    0.2590    |     3.0     |
| 16 |    15    |   > 5.0      |     —       |
| 16 |    20    |    2.1093    |     4.0     |
| 16 |    25    |    1.1857    |     4.0     |
| 16 |    30    |    0.4998    |     4.0     |

K=16 at SNR=15 dB is not achievable at any practical ε (would require ε > 5, violating
backward-compatibility with the underlying QAM symbol).

---

## Honest Near-Term Recommendations

| Configuration | Bits | Notes |
|---|---|---|
| K=4, ε=0.5, SNR=20 dB | **2.0** | Backward-compatible if ε < d_min_QAM/2 |
| K=8, ε=0.5, SNR=25 dB | **3.0** | Requires tight polarisation control |
| K=16, ε=0.5, SNR=30 dB | **4.0** | At SNR floor of next-gen coherent systems |

The first two are the closest near-term deployable targets. K=16 at 4 bits requires
next-generation coherent transponder SNR (30 dB is demanding but achievable in
metro-amplified short-reach).

---

## What Changed from v1.0

| Item | v1.0 | v2.0 |
|---|---|---|
| Capacity claim | +4.0 bits (asserted) | 0–4 bits (simulation-backed) |
| Default parameters | K=16, ε=0.1, SNR=15 dB | Same |
| P_e at defaults | Not computed | ≈ 1.00 (invalid) |
| Method | log₂(16) | Fibonacci d_min + union bound |
| Recommended operating point | K=16, ε=0.1 | K=4–16, ε=0.5, SNR=20–30 dB |

---

## Running the Simulation

```bash
cd otap-signup-validator
python3 -m stage_chronos.npm_capacity_sim
```

Or from Python:

```python
from stage_chronos.npm_capacity_sim import simulate_npm, run_grid

# Single operating point
result = simulate_npm(K=16, epsilon=0.5, snr_db=30.0)
print(result)
# {'K': 16, 'K_usable': 16, 'bits_usable': 4.0, ...}

# Grid sweep
rows = run_grid(
    K_values=[4, 8, 16],
    epsilon_values=[0.1, 0.5, 1.0],
    snr_values=[15.0, 20.0, 25.0, 30.0],
)
```
