# SPEC: Quantum-Enhanced Optical Transmitter (QTX)
## Breaking Tradition — Non-Classical Encoding for the Holevo Gap

**Version**: 1.0  
**Date**: 2026-06-02  
**Status**: Ready for implementation

---

## Problem Statement

Conventional optical communication encodes data as points in the IQ (quadrature) plane using coherent states of light — this is QAM. At practical power levels ($\bar{n} \sim 100$–$1,000$ photons per mode), this achieves ~3–5 bits per mode. The Holevo bound for the same power level is ~8–11 bits per mode. **A 3–6 bit gap per mode remains untapped.**

This gap exists because coherent states are not optimal for information transmission. **Squeezed states** — quantum states with reduced noise in one quadrature — and **non-Gaussian states** (cat states, photon-subtracted states) can approach the Holevo bound.

## What We Build

A complete simulation framework for quantum-enhanced optical communication, consisting of:

### Module A: `quantum_states` — Non-Classical State Generation
- Coherent states $|\alpha\rangle$ (baseline)
- Squeezed states $|\xi\rangle = S(\xi)|0\rangle$ with squeezing parameter $r$
- Approximate cat states $|\psi_{\text{cat}}\rangle \propto |\alpha\rangle + |-\alpha\rangle$
- Photon-subtracted squeezed states (non-Gaussian)
- Wigner function computation for all states

### Module B: `fock_encoder` — Fock-State Bit Encoding
- Map bit sequences to quantum state parameters $(\alpha, r, \theta)$
- Constellation design on the quadrature plane with squeezed noise
- Probabilistic shaping: Maxwell-Boltzmann distribution over constellation points
- Support for: uniform, Gaussian, and custom shaping distributions

### Module C: `quantum_channel` — Fiber Channel with Quantum Noise
- Linear loss (beam splitter model)
- Dispersion (split-step Fourier)
- Additive Gaussian noise (phase-insensitive amplifier model)
- Squeezing degradation (loss destroys squeezing)

### Module D: `quantum_receiver` — Quantum-Limited Detection
- Homodyne detection (single quadrature, shot-noise limited)
- Heterodyne detection (both quadratures, 3 dB penalty)
- Maximum likelihood decoder with non-Gaussian likelihoods
- Soft-output LLR computation for FEC

### Module E: `capacity_estimator` — Information-Theoretic Bounds
- Mutual information I(X;Y) via Monte Carlo
- Holevo bound computation
- Gap-to-capacity analysis
- Comparison: classical vs squeezed vs non-Gaussian

### Module F: `benchmark` — End-to-End Validation
- BER vs SNR curves for all encoding schemes
- Achievable information rate vs distance
- Comparison with conventional DP-64QAM
- Honest reporting: what is simulated vs what is theoretical

## Key Design Decisions

1. **All simulation, no hardware required** — This is a classical simulation of quantum optics using the Wigner function formalism
2. **Honest about limitations** — Every result labeled as: simulated / theoretical / experimental / aspirational
3. **Breaks tradition**: 
   - No rectangular QAM constellations
   - No assumption of Gaussian noise (squeezed states have non-Gaussian marginals)
   - Uses full quantum state description, not just amplitude/phase
   - Optimizes over quantum state parameters, not just constellation points

## Deliverables

1. Python package `qtx/` with all 6 modules
2. Jupyter notebook demonstrating capacity gains
3. Benchmark report with honest gap-to-capacity analysis
4. All code tested and validated against known theoretical results
