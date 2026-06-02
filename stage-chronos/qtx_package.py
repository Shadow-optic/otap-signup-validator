#!/usr/bin/env python3
"""
QTX: Quantum-Enhanced Optical Transmitter
==========================================
A complete simulation framework for non-classical encoding in fiber communication.

Breaking tradition: encodes data in squeezed quantum states instead of classical
coherent states. Uses 1D PAM constellations on the squeezed quadrature with
phase-sensitive amplification to preserve squeezing through the channel.

Modules:
  A. quantum_states     — Non-classical state generation (Fock basis)
  B. fock_encoder       — Squeezed-PAM constellation design
  C. quantum_channel    — Loss + noise + PSA channel models
  D. quantum_receiver   — Homodyne ML decoder
  E. capacity_estimator — Mutual information + Holevo bound
  F. benchmark          — End-to-end validation suite

Author: OTAP Network Contributors
License: MIT
"""

import numpy as np
from scipy.special import factorial, erfc

# ============================================================
# MODULE A: Non-Classical Quantum State Generation
# ============================================================

class QuantumState:
    """Base class for quantum optical states in the Fock basis."""
    
    def __init__(self, n_max=50):
        self.n_max = n_max
        self.coeffs = np.zeros(n_max, dtype=complex)
        
    def photon_number(self):
        n = np.arange(self.n_max)
        return np.sum(n * np.abs(self.coeffs)**2)
    
    def quadrature_variance(self, theta=0):
        """Var(X_theta) — computed from ladder operators."""
        x2 = 0.0
        for n in range(self.n_max):
            p_n = abs(self.coeffs[n])**2
            x2 += p_n * (2*n + 1)
        for n in range(self.n_max - 2):
            c_n = self.coeffs[n]
            c_np2 = self.coeffs[n+2]
            x2 += np.sqrt((n+1)*(n+2)) * (c_n.conj() * c_np2 + c_np2.conj() * c_n).real
        x2 /= 2.0
        x = self.quadrature_expectation(theta)
        return x2 - x**2
    
    def quadrature_expectation(self, theta=0):
        x = 0.0
        for n in range(self.n_max - 1):
            x += np.sqrt(n+1) * (self.coeffs[n].conj() * self.coeffs[n+1] * np.exp(-1j*theta)).real
        return x * np.sqrt(2)


class CoherentState(QuantumState):
    """Coherent state |α>. Baseline classical state."""
    
    def __init__(self, alpha, n_max=50):
        super().__init__(n_max)
        self.alpha = alpha
        for n in range(n_max):
            self.coeffs[n] = np.exp(-abs(alpha)**2/2) * (alpha**n) / np.sqrt(factorial(n))


class SqueezedState(QuantumState):
    """Squeezed vacuum S(r,φ)|0>. Reduces noise in one quadrature."""
    
    def __init__(self, r, phi=0, n_max=50):
        super().__init__(n_max)
        self.r = r
        self.phi = phi
        sech_r = 1.0 / np.cosh(r)
        tanh_r = np.tanh(r)
        self.coeffs[0] = np.sqrt(sech_r)
        for n in range(0, n_max-2, 2):
            self.coeffs[n+2] = self.coeffs[n] * (-np.exp(1j*phi)*tanh_r) * np.sqrt((n+1)*(n+2))/(n+2)
    
    def photon_number(self):
        return np.sinh(self.r)**2
    
    def quadrature_variance(self, theta=0):
        return 0.5 * (np.cosh(2*self.r) - np.sinh(2*self.r)*np.cos(2*(theta - self.phi/2)))


class SqueezedCoherentState(QuantumState):
    """Displaced squeezed state D(α)S(ξ)|0>. General Gaussian state."""
    
    def __init__(self, alpha, r, phi=0, n_max=60):
        super().__init__(n_max)
        self.alpha = complex(alpha)
        self.r = r
        self.phi = phi
        
        # Generate by displacing squeezed vacuum
        sq = SqueezedState(r, phi, n_max)
        a = self.alpha
        
        for n_out in range(n_max):
            coeff = 0.0 + 0.0j
            for n_in in range(0, n_max, 2):
                if abs(sq.coeffs[n_in]) < 1e-14:
                    continue
                coeff += self._displacement_me(n_out, n_in, a) * sq.coeffs[n_in]
            self.coeffs[n_out] = coeff
        
        # Normalize
        norm = np.sqrt(np.sum(np.abs(self.coeffs)**2))
        if norm > 0:
            self.coeffs /= norm
    
    def _displacement_me(self, m, n, alpha):
        """<m|D(α)|n>"""
        if m >= n:
            return (np.sqrt(factorial(n)/factorial(m)) * alpha**(m-n) *
                    np.exp(-abs(alpha)**2/2) * self._laguerre(n, m-n, abs(alpha)**2))
        else:
            return (np.sqrt(factorial(m)/factorial(n)) * (-alpha.conjugate())**(n-m) *
                    np.exp(-abs(alpha)**2/2) * self._laguerre(m, n-m, abs(alpha)**2))
    
    def _laguerre(self, n, k, x):
        result = 0.0
        for j in range(n+1):
            result += (factorial(n+k)/(factorial(n-j)*factorial(k+j)*factorial(j)))*((-x)**j)
        return result
    
    def photon_number(self):
        return abs(self.alpha)**2 + np.sinh(self.r)**2
    
    def quadrature_variance(self, theta=0):
        return 0.5 * (np.cosh(2*self.r) - np.sinh(2*self.r)*np.cos(2*(theta - self.phi/2)))


# ============================================================
# MODULE B: Squeezed-PAM Constellation Encoder
# ============================================================

class SqueezedPAMConstellation:
    """1D PAM constellation on the SQUEEZED quadrature.
    
    Breaks tradition: not 2D QAM but 1D PAM where ALL constellation
    points lie on the squeezed quadrature line. Homodyne detection
    measures only this quadrature, benefiting fully from squeezing.
    """
    
    def __init__(self, n_points, mean_photon, r_squeeze):
        self.n_points = n_points
        self.mean_photon = mean_photon
        self.r_squeeze = r_squeeze
        self.bits_per_symbol = np.log2(n_points)
        
        avg_alpha_sq = max(mean_photon - np.sinh(r_squeeze)**2, n_points * 0.5)
        
        n = n_points
        alphas = np.array([-(n-1) + 2*i for i in range(n)], dtype=float)
        scale = np.sqrt(avg_alpha_sq / np.mean(alphas**2))
        alphas = alphas * scale
        
        self.points = [{'alpha': complex(a, 0), 'r': r_squeeze, 'phi': 0} for a in alphas]
        self.alphas_real = alphas
    
    def encode(self, idx):
        p = self.points[idx]
        return SqueezedCoherentState(p['alpha'], p['r'], p['phi'])


# ============================================================
# MODULE C: Quantum Channel Models
# ============================================================

class QuantumChannel:
    """Phase-insensitive channel (EDFA). Loss destroys squeezing."""
    
    def __init__(self, eta=0.8, noise_var=0.01):
        self.eta = eta
        self.noise_var = noise_var
    
    def transmit_quadratures(self, alpha, r, theta):
        sqrt_eta = np.sqrt(self.eta)
        tanh_r_eff = self.eta * np.tanh(r)
        r_eff = 0.5 * np.log((1+tanh_r_eff)/(1-tanh_r_eff)) if tanh_r_eff < 0.999 else r + 0.5*np.log(self.eta)
        
        alpha_out = sqrt_eta * alpha
        mean_out = (alpha_out * np.exp(-1j*theta)).real * np.sqrt(2)
        
        var_squeezed = 0.5 * (np.cosh(2*r_eff) - np.sinh(2*r_eff)*np.cos(2*theta))
        var_out = var_squeezed + self.noise_var
        
        return mean_out, var_out


class PSAChannel:
    """Phase-Sensitive Amplification channel. PRESERVES squeezing.
    
    Unlike EDFA which destroys squeezing, PSA amplifies the signal
    quadrature while de-amplifying the noise quadrature. Squeezing
    parameter r is preserved through the link.
    """
    
    def __init__(self, eta=0.8, excess_noise=0.01):
        self.eta = eta
        self.G = 1.0 / eta
        self.excess_noise = excess_noise
    
    def transmit_quadratures(self, alpha, r, theta):
        alpha_out = alpha * np.sqrt(self.G * self.eta)
        mean_out = (alpha_out * np.exp(-1j*theta)).real * np.sqrt(2)
        
        var_squeezed = 0.5 * (np.cosh(2*r) - np.sinh(2*r)*np.cos(2*theta))
        var_out = var_squeezed + self.excess_noise
        
        return mean_out, var_out


# ============================================================
# MODULE D: Homodyne ML Receiver
# ============================================================

def simulate_ber_homodyne(constellation, channel, n_symbols=50000):
    """Simulate BER with single-quadrature homodyne + ML decoder."""
    n_points = len(constellation.points)
    
    means, variances = [], []
    for p in constellation.points:
        my, vy = channel.transmit_quadratures(p['alpha'], p['r'], 0)
        means.append(my)
        variances.append(vy)
    means = np.array(means)
    variances = np.array(variances)
    
    errors = 0
    for _ in range(n_symbols):
        idx_tx = np.random.randint(n_points)
        y = np.random.normal(means[idx_tx], np.sqrt(variances[idx_tx]))
        ll = -0.5 * np.log(variances) - 0.5 * (y - means)**2 / variances
        if np.argmax(ll) != idx_tx:
            errors += 1
    
    return errors / n_symbols


def achievable_rate(ber, bits_per_symbol):
    """Achievable rate = (1 - SER) * log2(M)."""
    return max(0, (1 - ber) * bits_per_symbol)


# ============================================================
# MODULE E: Capacity Bounds
# ============================================================

def holevo_bound_coherent(nbar):
    """Holevo bound: C = (1+nbar)log2(1+nbar) - nbar*log2(nbar)."""
    if nbar < 1e-10:
        return 0
    return (1+nbar)*np.log2(1+nbar) - nbar*np.log2(nbar)


def conventional_qam_capacity(snr_db, m_qam):
    """M-QAM capacity in AWGN."""
    snr_lin = 10**(snr_db/10)
    ser = 2*(1-1/np.sqrt(m_qam)) * 0.5*erfc(np.sqrt(3*np.log2(m_qam)*snr_lin/(2*(m_qam-1))))
    return max(0, np.log2(m_qam) * (1 - ser))


# ============================================================
# MODULE F: Benchmark Runner
# ============================================================

def run_benchmark(mean_photon=100, eta=0.8, excess_noise=0.01):
    """Run full benchmark: coherent vs squeezed at various parameters."""
    
    print("=" * 70)
    print("QTX BENCHMARK: Coherent vs Squeezed-State Encoding")
    print(f"Parameters: nbar={mean_photon}, eta={eta}, noise={excess_noise}")
    print("=" * 70)
    
    psa = PSAChannel(eta, excess_noise)
    
    print(f"\n{'Squeezing':>10s} | {'Best M':>6s} | {'SER':>8s} | {'Rate':>6s} | {'bps/ph':>8s} | {'Gain':>6s}")
    print("-" * 62)
    
    baseline = 0
    for r_sq in [0.0, 0.3, 0.5, 0.7, 1.0, 1.5, 2.0]:
        best_rate, best_M, best_ser = 0, 0, 1.0
        for M in [4, 8, 16, 32, 64, 128, 256, 512, 1024]:
            pam = SqueezedPAMConstellation(M, mean_photon, r_sq)
            ser = simulate_ber_homodyne(pam, psa, 20000)
            rate = achievable_rate(ser, np.log2(M))
            if rate > best_rate:
                best_rate, best_M, best_ser = rate, M, ser
        
        if r_sq == 0:
            baseline = best_rate
        
        gain = f"{best_rate/baseline:.2f}x" if baseline > 0 and r_sq > 0 else "ref"
        print(f"  {r_sq:>8.1f} | {best_M:>6d} | {best_ser:>8.4f} | {best_rate:>5.1f} | {best_rate/mean_photon:>7.3f} | {gain:>6s}")
    
    holevo = holevo_bound_coherent(mean_photon)
    print(f"\n  Holevo bound: {holevo:.2f} bits/mode | {holevo/mean_photon:.3f} bits/photon")
    print(f"  Gap to best:  {holevo - best_rate:.2f} bits/mode")
    
    return baseline, best_rate


if __name__ == "__main__":
    run_benchmark()


# ============================================================
# MODULE G: Corkscrew Constellation (Twist + Squeeze)
# ============================================================
# Added 2026-06-02: OAM twist combined with squeezing

class CorkscrewConstellation:
    """Corkscrew constellation: OAM twist + squeezing combined.
    
    Each ring uses a different OAM order for ring-to-ring orthogonality.
    Within each ring: squeezed PAM on the radial amplitude.
    
    Best for: free-space, data centers, ring-core fiber (< 0.5% OAM crosstalk).
    Avoid for: standard SMF long-haul (> 1% OAM crosstalk degrades performance).
    """
    
    def __init__(self, n_rings, m_radial, mean_photon, r_squeeze, oam_orders=None):
        self.n_rings = n_rings
        self.m_radial = m_radial
        self.mean_photon = mean_photon
        self.r_squeeze = r_squeeze
        self.n_points = n_rings * m_radial
        self.bits_per_symbol = np.log2(self.n_points)
        self.oam_orders = oam_orders if oam_orders is not None else list(range(n_rings))
        
        photon_per_ring = mean_photon / n_rings
        self.points = []
        
        for ring_idx in range(n_rings):
            avg_r_sq = max(photon_per_ring - np.sinh(r_squeeze)**2 / n_rings, m_radial * 0.1)
            radii = np.array([-(m_radial-1) + 2*i for i in range(m_radial)], dtype=float)
            scale = np.sqrt(avg_r_sq / max(np.mean(radii**2), 0.1))
            radii = radii * scale
            base_phase = 2 * np.pi * ring_idx / n_rings
            
            for radius in radii:
                alpha = radius * np.exp(1j * base_phase)
                self.points.append({
                    'alpha': complex(alpha), 'r': r_squeeze, 'phi': 0,
                    'oam': self.oam_orders[ring_idx], 'ring': ring_idx
                })


class OAMChannel:
    """Channel with configurable OAM crosstalk. PSA preserves squeezing."""
    
    def __init__(self, eta=0.8, excess_noise=0.01, oam_crosstalk=0.0):
        self.eta = eta
        self.G = 1.0 / eta
        self.excess_noise = excess_noise
        self.oam_xtalk = oam_crosstalk
    
    def transmit_quadratures(self, alpha, r, theta, oam_order=0):
        alpha_out = alpha * np.sqrt(self.G * self.eta)
        mean_out = (alpha_out * np.exp(-1j*theta)).real * np.sqrt(2)
        var_squeezed = 0.5 * (np.cosh(2*r) - np.sinh(2*r)*np.cos(2*theta))
        oam_noise = self.oam_xtalk * abs(alpha)**2
        var_out = var_squeezed + self.excess_noise + oam_noise
        return mean_out, var_out


def simulate_corkscrew(constellation, channel, n_symbols=20000):
    """Joint OAM + quadrature ML detection for corkscrew constellation."""
    n_points = len(constellation.points)
    points = constellation.points
    n_rings = constellation.n_rings
    
    # Pre-compute channel stats
    means, variances = [], []
    for p in points:
        my, vy = channel.transmit_quadratures(p['alpha'], p['r'], 0, p['oam'])
        means.append(my); variances.append(vy)
    means, variances = np.array(means), np.array(variances)
    
    # OAM likelihood matrix
    xtalk = channel.oam_xtalk
    oam_ll = np.zeros((n_rings, n_rings))
    for j in range(n_rings):
        for i in range(n_rings):
            if i == j: oam_ll[i,j] = np.log(max(1e-10, 1 - xtalk))
            elif abs(i-j) == 1: oam_ll[i,j] = np.log(max(1e-10, xtalk/2))
            else: oam_ll[i,j] = -20
    
    errors = 0
    for _ in range(n_symbols):
        idx_tx = np.random.randint(n_points)
        ring_tx = points[idx_tx]['ring']
        y = np.random.normal(means[idx_tx], np.sqrt(variances[idx_tx]))
        
        best_ll, idx_rx = -np.inf, 0
        for k, p_k in enumerate(points):
            ll_quad = -0.5*np.log(variances[k]) - 0.5*(y-means[k])**2/variances[k]
            ll_total = ll_quad + oam_ll[p_k['ring'], ring_tx]
            if ll_total > best_ll:
                best_ll, idx_rx = ll_total, k
        
        if idx_rx != idx_tx: errors += 1
    
    return errors / n_symbols
