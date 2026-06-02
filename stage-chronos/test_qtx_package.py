"""
Tests for qtx_package — quantum-enhanced optical transmitter simulation.

Covers:
  Module A: QuantumState subclasses (CoherentState, SqueezedState, SqueezedCoherentState)
  Module B: SqueezedPAMConstellation
  Module C: PSAChannel + QuantumChannel
  Module D: simulate_ber_homodyne
  Module E: holevo_bound_coherent, achievable_rate
  Module G: CorkscrewConstellation + OAMChannel
"""

import math
import numpy as np
import pytest

from stage_chronos.qtx_package import (
    CoherentState,
    SqueezedState,
    SqueezedCoherentState,
    SqueezedPAMConstellation,
    QuantumChannel,
    PSAChannel,
    OAMChannel,
    CorkscrewConstellation,
    simulate_ber_homodyne,
    simulate_corkscrew,
    achievable_rate,
    holevo_bound_coherent,
)


# ---------------------------------------------------------------------------
# Module A: Quantum states
# ---------------------------------------------------------------------------

class TestCoherentState:
    def test_photon_number(self):
        s = CoherentState(alpha=3.0)
        assert abs(s.photon_number() - 9.0) < 0.01

    def test_zero_amplitude(self):
        s = CoherentState(alpha=0.0)
        assert s.photon_number() < 1e-6

    def test_quadrature_expectation(self):
        s = CoherentState(alpha=2.0)
        x = s.quadrature_expectation(theta=0)
        assert abs(x - 2.0 * math.sqrt(2)) < 0.1


class TestSqueezedState:
    def test_photon_number(self):
        r = 1.0
        s = SqueezedState(r=r)
        expected = math.sinh(r) ** 2
        assert abs(s.photon_number() - expected) < 1e-3

    def test_quadrature_variance_below_shot_noise(self):
        r = 1.0
        s = SqueezedState(r=r)
        var_squeezed = s.quadrature_variance(theta=0)
        shot_noise = 0.5
        assert var_squeezed < shot_noise, "Squeezed quadrature must be below shot noise"

    def test_quadrature_variance_exact(self):
        r = 1.0
        s = SqueezedState(r=r)
        expected = 0.5 * math.exp(-2.0 * r)
        assert abs(s.quadrature_variance(theta=0) - expected) < 1e-4

    def test_anti_squeezed_quadrature(self):
        r = 1.0
        s = SqueezedState(r=r)
        var_anti = s.quadrature_variance(theta=math.pi / 2)
        var_squeezed = s.quadrature_variance(theta=0)
        assert var_anti > var_squeezed

    def test_heisenberg(self):
        r = 1.5
        s = SqueezedState(r=r)
        v_x = s.quadrature_variance(theta=0)
        v_p = s.quadrature_variance(theta=math.pi / 2)
        assert v_x * v_p >= 0.25 - 1e-4


class TestSqueezedCoherentState:
    def test_photon_number(self):
        alpha, r = 2.0, 0.5
        s = SqueezedCoherentState(alpha, r)
        expected = alpha ** 2 + math.sinh(r) ** 2
        assert abs(s.photon_number() - expected) < 0.5

    def test_normalized(self):
        s = SqueezedCoherentState(alpha=1.5, r=0.8)
        norm = np.sum(np.abs(s.coeffs) ** 2)
        assert abs(norm - 1.0) < 1e-6

    def test_variance_inherits_squeezing(self):
        r = 1.0
        s = SqueezedCoherentState(alpha=2.0, r=r)
        var = s.quadrature_variance(theta=0)
        expected = 0.5 * math.exp(-2.0 * r)
        assert abs(var - expected) < 0.05


# ---------------------------------------------------------------------------
# Module B: SqueezedPAMConstellation
# ---------------------------------------------------------------------------

class TestSqueezedPAMConstellation:
    def test_point_count(self):
        c = SqueezedPAMConstellation(n_points=8, mean_photon=50, r_squeeze=0.5)
        assert len(c.points) == 8

    def test_bits_per_symbol(self):
        c = SqueezedPAMConstellation(n_points=16, mean_photon=100, r_squeeze=1.0)
        assert abs(c.bits_per_symbol - 4.0) < 1e-9

    def test_points_on_real_axis(self):
        c = SqueezedPAMConstellation(n_points=4, mean_photon=20, r_squeeze=0.5)
        for p in c.points:
            assert abs(p['alpha'].imag) < 1e-9

    def test_points_symmetric(self):
        c = SqueezedPAMConstellation(n_points=4, mean_photon=20, r_squeeze=0.5)
        real_parts = sorted([p['alpha'].real for p in c.points])
        assert abs(real_parts[0] + real_parts[-1]) < 1e-9
        assert abs(real_parts[1] + real_parts[-2]) < 1e-9

    def test_encode_returns_squeezed_coherent(self):
        c = SqueezedPAMConstellation(n_points=4, mean_photon=20, r_squeeze=0.5)
        state = c.encode(0)
        assert isinstance(state, SqueezedCoherentState)

    def test_squeezing_applies(self):
        r = 1.0
        c = SqueezedPAMConstellation(n_points=4, mean_photon=20, r_squeeze=r)
        for p in c.points:
            assert p['r'] == r


# ---------------------------------------------------------------------------
# Module C: Channel models
# ---------------------------------------------------------------------------

class TestQuantumChannel:
    def test_loss_reduces_amplitude(self):
        ch = QuantumChannel(eta=0.5, noise_var=0.0)
        m, v = ch.transmit_quadratures(alpha=4.0, r=0.0, theta=0)
        assert m < 4.0 * math.sqrt(2)

    def test_lossless_preserves(self):
        ch = QuantumChannel(eta=1.0, noise_var=0.0)
        m, v = ch.transmit_quadratures(alpha=2.0, r=0.0, theta=0)
        assert abs(m - 2.0 * math.sqrt(2)) < 0.2


class TestPSAChannel:
    def test_preserves_squeezing(self):
        r = 1.0
        ch = PSAChannel(eta=0.8, excess_noise=0.01)
        m, v = ch.transmit_quadratures(alpha=2.0, r=r, theta=0)
        expected_var = 0.5 * (math.cosh(2 * r) - math.sinh(2 * r)) + 0.01
        assert abs(v - expected_var) < 0.05

    def test_eta_less_than_1(self):
        ch = PSAChannel(eta=0.9, excess_noise=0.0)
        m, v = ch.transmit_quadratures(alpha=3.0, r=0.5, theta=0)
        assert v > 0


# ---------------------------------------------------------------------------
# Module D: Homodyne BER simulation
# ---------------------------------------------------------------------------

class TestSimulateBerHomodyne:
    def test_ber_in_range(self):
        pam = SqueezedPAMConstellation(n_points=4, mean_photon=50, r_squeeze=0.5)
        ch = PSAChannel(eta=0.9, excess_noise=0.01)
        ber = simulate_ber_homodyne(pam, ch, n_symbols=2000)
        assert 0.0 <= ber <= 1.0

    def test_more_squeezing_reduces_ber(self):
        ch = PSAChannel(eta=0.8, excess_noise=0.01)
        ber_low = simulate_ber_homodyne(
            SqueezedPAMConstellation(n_points=4, mean_photon=50, r_squeeze=0.1),
            ch, n_symbols=3000
        )
        ber_high = simulate_ber_homodyne(
            SqueezedPAMConstellation(n_points=4, mean_photon=50, r_squeeze=1.5),
            ch, n_symbols=3000
        )
        assert ber_high <= ber_low + 0.1, "More squeezing should not badly hurt BER"


# ---------------------------------------------------------------------------
# Module E: Capacity bounds
# ---------------------------------------------------------------------------

class TestCapacityBounds:
    def test_holevo_zero_photons(self):
        assert holevo_bound_coherent(0.0) == 0.0

    def test_holevo_increases_with_photons(self):
        assert holevo_bound_coherent(100) > holevo_bound_coherent(10)

    def test_holevo_nbar_100(self):
        h = holevo_bound_coherent(100)
        assert 8.0 < h < 9.0

    def test_achievable_rate_zero_ber(self):
        assert abs(achievable_rate(0.0, 4.0) - 4.0) < 1e-9

    def test_achievable_rate_full_ber(self):
        assert achievable_rate(1.0, 4.0) == 0.0

    def test_achievable_rate_partial(self):
        rate = achievable_rate(0.1, 4.0)
        assert abs(rate - 3.6) < 0.01


# ---------------------------------------------------------------------------
# Module G: Corkscrew + OAM channel
# ---------------------------------------------------------------------------

class TestCorkscrewConstellation:
    def test_point_count(self):
        c = CorkscrewConstellation(n_rings=4, m_radial=4, mean_photon=100, r_squeeze=0.5)
        assert len(c.points) == 16

    def test_bits_per_symbol(self):
        c = CorkscrewConstellation(n_rings=4, m_radial=4, mean_photon=100, r_squeeze=0.5)
        assert abs(c.bits_per_symbol - 4.0) < 1e-9

    def test_oam_order_assignment(self):
        c = CorkscrewConstellation(n_rings=3, m_radial=2, mean_photon=50, r_squeeze=0.3,
                                   oam_orders=[0, 2, 4])
        oam_vals = [p['oam'] for p in c.points]
        assert set(oam_vals) == {0, 2, 4}

    def test_ring_assignment(self):
        c = CorkscrewConstellation(n_rings=3, m_radial=2, mean_photon=50, r_squeeze=0.3)
        ring_vals = [p['ring'] for p in c.points]
        assert set(ring_vals) == {0, 1, 2}


class TestOAMChannel:
    def test_ber_in_range(self):
        ch = OAMChannel(eta=0.8, excess_noise=0.01, oam_crosstalk=0.0)
        c = CorkscrewConstellation(n_rings=2, m_radial=2, mean_photon=50, r_squeeze=0.5)
        ber = simulate_corkscrew(c, ch, n_symbols=500)
        assert 0.0 <= ber <= 1.0

    def test_crosstalk_increases_ber(self):
        c = CorkscrewConstellation(n_rings=2, m_radial=2, mean_photon=50, r_squeeze=0.5)
        ch_clean = OAMChannel(eta=0.9, excess_noise=0.01, oam_crosstalk=0.0)
        ch_noisy = OAMChannel(eta=0.9, excess_noise=0.01, oam_crosstalk=0.2)
        ber_clean = simulate_corkscrew(c, ch_clean, n_symbols=1000)
        ber_noisy = simulate_corkscrew(c, ch_noisy, n_symbols=1000)
        assert ber_noisy >= ber_clean - 0.05


if __name__ == "__main__":
    import sys
    tests = [v for k, v in globals().items() if isinstance(v, type) and k.startswith('Test')]
    passed = failed = 0
    for cls in tests:
        obj = cls()
        for name in [m for m in dir(obj) if m.startswith('test_')]:
            try:
                getattr(obj, name)()
                passed += 1
            except Exception as e:
                print(f"FAIL  {cls.__name__}.{name}: {e}")
                failed += 1
    print(f"\n{passed} passed, {failed} failed")
    sys.exit(failed)
