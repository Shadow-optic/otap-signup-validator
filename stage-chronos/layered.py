"""
CHRONOS-Drift Layered Tracker — fast (differential) + slow (integral/ratchet).

Two complementary detectors close the slow-ramp blind spot of the pure
differential tracker:

  Fast layer  — z-score of current Phi vs EWMA baseline.  Catches step
                insertions and fast ramps.  Blind to ramps slower than
                ~1/alpha EWMA time constant (~20 frames at alpha=0.05).

  Slow layer  — net monotonic displacement of the EWMA baseline over a long
                window.  Thermal drift oscillates (net ~0 over a cycle).  A
                rogue tap ratchets the baseline monotonically downward (net
                large & sustained).  Catches the slow ramps the fast layer
                misses, with latency bounded by long_window.

Together, no ramp rate is left fully undetected.
"""

from collections import deque
from enum import Enum
from typing import Dict, Any, Optional, List

import numpy as np

from .drift import LinkState


class LayeredTracker:
    """
    Two-layer coherence tracker for OTAP fiber links.

    Parameters
    ----------
    window : int
        Short rolling window for the fast differential layer.
    alpha : float
        EWMA smoothing coefficient (fast baseline).
    z_thr : float
        Z-score threshold for fast-layer shock detection.
    confirm : int
        Consecutive shock frames required to latch COMPROMISED via fast path.
    long_window : int
        Baseline history length for the slow ratchet layer.
    ratchet_thr : float
        Net downward displacement (in Phi units) over long_window to trigger
        the slow-layer alarm.
    """

    def __init__(
        self,
        window: int = 50,
        alpha: float = 0.05,
        z_thr: float = 4.0,
        confirm: int = 3,
        long_window: int = 400,
        ratchet_thr: float = 0.025,
    ) -> None:
        self.window = window
        self.alpha = alpha
        self.z_thr = z_thr
        self.confirm = confirm
        self.long_window = long_window
        self.ratchet_thr = ratchet_thr

        self.history: deque = deque(maxlen=window)
        self.baseline: float = 1.0
        self.var: float = 1e-4
        self.state = LinkState.HEALTHY
        self.shock: int = 0

        self.baseline_hist: deque = deque(maxlen=long_window)
        self.fast_event: Optional[tuple] = None
        self.slow_event: Optional[tuple] = None

    def ack(self) -> None:
        """Reset to HEALTHY and clear all state."""
        self.state = LinkState.HEALTHY
        self.shock = 0
        self.history.clear()
        self.baseline = 1.0
        self.var = 1e-4
        self.baseline_hist.clear()
        self.fast_event = None
        self.slow_event = None

    def process(self, phi: float, t: int) -> Dict[str, Any]:
        """
        Ingest one Phi sample.

        Returns dict with keys:
          state      — current LinkState
          fast_event — ("FAST", t) tuple when fast layer first triggered, else None
          slow_event — ("SLOW", t) tuple when slow layer first triggered, else None
          via        — "FAST" or "SLOW" indicating which layer caused COMPROMISED
        """
        if len(self.history) < self.window:
            self.history.append(phi)
            self.baseline = float(np.mean(self.history))
            if len(self.history) > 1:
                self.var = float(np.var(self.history))
            self.baseline_hist.append(self.baseline)
            return {"state": self.state, "fast_event": self.fast_event,
                    "slow_event": self.slow_event, "via": None}

        if self.state == LinkState.COMPROMISED:
            return {"state": self.state, "fast_event": self.fast_event,
                    "slow_event": self.slow_event, "via": self._via()}

        std = np.sqrt(self.var)
        z = (phi - self.baseline) / std if std > 0 else 0.0

        # --- Fast layer (differential z-score) ---
        if z < -self.z_thr:
            self.shock += 1
            if self.shock == 1:
                self.state = LinkState.ALARM
                if self.fast_event is None:
                    self.fast_event = ("FAST", t)
            if self.shock >= self.confirm:
                self.state = LinkState.COMPROMISED
                return {"state": self.state, "fast_event": self.fast_event,
                        "slow_event": self.slow_event, "via": self._via()}
        else:
            self.shock = 0
            if self.state == LinkState.ALARM:
                self.state = LinkState.HEALTHY
            self.baseline = self.alpha * phi + (1 - self.alpha) * self.baseline
            self.history.append(phi)
            self.var = float(np.var(self.history))

        # --- Slow layer (net monotonic baseline displacement) ---
        self.baseline_hist.append(self.baseline)
        if len(self.baseline_hist) == self.long_window:
            net_drop = self.baseline_hist[0] - self.baseline_hist[-1]
            if net_drop > self.ratchet_thr:
                if self.slow_event is None:
                    self.slow_event = ("SLOW", t)
                self.state = LinkState.COMPROMISED
                return {"state": self.state, "fast_event": self.fast_event,
                        "slow_event": self.slow_event, "via": self._via()}

        return {"state": self.state, "fast_event": self.fast_event,
                "slow_event": self.slow_event, "via": self._via()}

    def _via(self) -> Optional[str]:
        if self.fast_event and self.slow_event:
            return self.fast_event[0] if self.fast_event[1] <= self.slow_event[1] else self.slow_event[0]
        if self.fast_event:
            return self.fast_event[0]
        if self.slow_event:
            return self.slow_event[0]
        return None


# ---------------------------------------------------------------------------
# Scenario runner
# ---------------------------------------------------------------------------

def _pdl_to_phi_simple(pdl: float) -> float:
    return 1.0 - 0.10 * pdl


def run_layered_sweep(
    ramp_rates: Optional[List[int]] = None,
    total_tap: float = 0.6,
    T: int = 2500,
    tap_start: int = 600,
    seed: int = 42,
) -> List[Dict[str, Any]]:
    """
    Sweep tap-insertion ramp rates against the layered detector.

    Returns a list of result dicts (one per ramp rate) with keys:
      ramp, comp_at, via, fast_event, slow_event, latency, detected
    """
    if ramp_rates is None:
        ramp_rates = [1, 10, 25, 50, 100, 200, 400, 800, 1200, 1600]

    results = []
    for ramp in ramp_rates:
        np.random.seed(seed)
        thermal = 0.25 + 0.15 * np.sin(np.linspace(0, 8 * np.pi, T))
        jitter = np.random.normal(0, 0.02, T)
        base = thermal + jitter
        tap = np.zeros(T)
        for t in range(tap_start, T):
            prog = min(1.0, (t - tap_start) / max(1, ramp))
            tap[t] = total_tap * prog
        phi = _pdl_to_phi_simple(base + tap)

        tracker = LayeredTracker()
        comp_at = None
        for t in range(T):
            r = tracker.process(phi[t], t)
            if r["state"] == LinkState.COMPROMISED and comp_at is None:
                comp_at = t

        latency = (comp_at - tap_start) if comp_at is not None else None
        results.append(dict(
            ramp=ramp,
            detected=comp_at is not None,
            comp_at=comp_at,
            via=tracker._via(),
            fast_event=tracker.fast_event,
            slow_event=tracker.slow_event,
            latency=latency,
        ))
    return results
