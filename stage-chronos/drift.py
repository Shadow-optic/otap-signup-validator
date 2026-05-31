"""
CHRONOS-Drift — dynamic coherence tracking for OTAP fiber links.

Three-state machine (HEALTHY → ALARM → COMPROMISED) built on top of
a per-frame Phi stream from GeometricCoherenceKernel.  Designed to
distinguish genuine geometric shocks (rogue tap insertion) from slow
thermal PDL drift that benign links experience.

State machine
-------------
HEALTHY     : baseline tracks slow drift via EWMA.
ALARM       : edge-triggered on the first frame a z-shock is detected.
              Only one edge event is emitted per shock episode.
COMPROMISED : latched once `confirm_frames` consecutive shock frames are
              seen.  No further per-frame alarms; operator must call
              ack() to reset.

Adversary modelling
-------------------
chronos_slowramp_sweep() sweeps the tap insertion ramp rate to find the
differential-layer blind spot (the EWMA absorbs ramps slower than ~1/alpha).
"""

from collections import deque
from enum import Enum
from typing import Dict, Any, Optional, List

import numpy as np


class LinkState(Enum):
    HEALTHY = "HEALTHY"
    ALARM = "ALARM"
    COMPROMISED = "COMPROMISED"


class DynamicCoherenceTracker:
    """
    Differential coherence tracker — fast (step/shock) detection layer.

    Maintains an EWMA baseline and rolling variance; fires when the
    current Phi is more than z_threshold standard deviations below the
    baseline.  Requires confirm_frames consecutive shock frames before
    latching to COMPROMISED (reduces false positives from transient dips).
    """

    def __init__(
        self,
        window_size: int = 50,
        alpha: float = 0.05,
        z_threshold: float = 4.0,
        confirm_frames: int = 3,
    ) -> None:
        self.window_size = window_size
        self.alpha = alpha
        self.z_threshold = z_threshold
        self.confirm_frames = confirm_frames
        self.history: deque = deque(maxlen=window_size)
        self.baseline_phi: float = 1.0
        self.phi_variance: float = 1e-4
        self.state = LinkState.HEALTHY
        self.shock_run: int = 0
        self.event_frame: Optional[int] = None

    def ack(self) -> None:
        """Operator acknowledgement: reset to HEALTHY and re-baseline."""
        self.state = LinkState.HEALTHY
        self.shock_run = 0
        self.history.clear()
        self.baseline_phi = 1.0
        self.phi_variance = 1e-4

    def process(self, current_phi: float, t: int) -> Dict[str, Any]:
        """
        Ingest one Phi sample at frame t.

        Returns a dict with keys:
          state    — current LinkState
          baseline — current EWMA baseline
          z        — z-score of current_phi vs baseline
          edge     — True only on the single rising-edge frame of a shock
        """
        if len(self.history) < self.window_size:
            self.history.append(current_phi)
            self.baseline_phi = float(np.mean(self.history))
            if len(self.history) > 1:
                self.phi_variance = float(np.var(self.history))
            return {"state": self.state, "baseline": self.baseline_phi, "z": 0.0, "edge": False}

        if self.state == LinkState.COMPROMISED:
            return {"state": self.state, "baseline": self.baseline_phi, "z": 0.0, "edge": False}

        std = np.sqrt(self.phi_variance)
        z = (current_phi - self.baseline_phi) / std if std > 0 else 0.0

        if z < -self.z_threshold:
            self.shock_run += 1
            edge = (self.shock_run == 1)
            if self.shock_run == 1:
                self.state = LinkState.ALARM
                self.event_frame = t
            if self.shock_run >= self.confirm_frames:
                self.state = LinkState.COMPROMISED
            return {"state": self.state, "baseline": self.baseline_phi, "z": z, "edge": edge}
        else:
            self.shock_run = 0
            if self.state == LinkState.ALARM:
                self.state = LinkState.HEALTHY
            self.baseline_phi = self.alpha * current_phi + (1 - self.alpha) * self.baseline_phi
            self.history.append(current_phi)
            self.phi_variance = float(np.var(self.history))
            return {"state": self.state, "baseline": self.baseline_phi, "z": z, "edge": False}


# ---------------------------------------------------------------------------
# Scenario runners
# ---------------------------------------------------------------------------

def _pdl_to_phi_simple(pdl: float) -> float:
    """Lightweight linear map used in scenario tests: Phi = 1.0 - 0.10*pdl."""
    return 1.0 - 0.10 * pdl


def run_drift_scenario(
    name: str,
    thermal_amp: float,
    thermal_mid: float,
    tap_pdl: float,
    tap_at: int = 600,
    T: int = 1000,
    seed: int = 42,
) -> Dict[str, Any]:
    """
    Simulate one scenario: thermal drift + step tap insertion.

    Returns metrics dict with static/dynamic FP and TP counts.
    """
    np.random.seed(seed)
    thermal = thermal_mid + thermal_amp * np.sin(np.linspace(0, 4 * np.pi, T))
    jitter = np.random.normal(0, 0.02, T)
    base_pdl = thermal + jitter
    tap = np.zeros(T)
    tap[tap_at:] = tap_pdl
    phi_stream = _pdl_to_phi_simple(base_pdl + tap)

    static_thr = 0.95
    tracker = DynamicCoherenceTracker(50, 0.05, 4.0, confirm_frames=3)

    static_alarms: List[int] = []
    edge_events: List[int] = []
    states = []
    first_edge: Optional[int] = None

    for t in range(T):
        phi = phi_stream[t]
        static_alarms.append(1 if phi < static_thr else 0)
        r = tracker.process(phi, t)
        if r["edge"]:
            edge_events.append(t)
            if first_edge is None:
                first_edge = t
        states.append(r["state"])

    static_fp = sum(static_alarms[:tap_at])
    static_tp = bool(sum(static_alarms[tap_at:]) > 0)
    dyn_fp = sum(1 for t in edge_events if t < tap_at)
    dyn_tp = any(t >= tap_at for t in edge_events)
    compromised_at = next((t for t in range(T) if states[t] == LinkState.COMPROMISED), None)

    return dict(
        name=name,
        static_fp=static_fp,
        static_tp=static_tp,
        dyn_fp=dyn_fp,
        dyn_tp=dyn_tp,
        edges=len(edge_events),
        first_edge=first_edge,
        compromised_at=compromised_at,
    )


def chronos_slowramp_sweep(
    ramp_rates: Optional[List[int]] = None,
    total_tap: float = 0.6,
    T: int = 2000,
    tap_start: int = 600,
    seed: int = 42,
) -> List[Dict[str, Any]]:
    """
    Sweep tap-insertion ramp rates to characterise the differential
    detector's evasion boundary.

    Returns a list of result dicts, one per ramp rate.
    """
    if ramp_rates is None:
        ramp_rates = [1, 5, 10, 25, 50, 100, 200, 400, 800, 1200]

    results = []
    for ramp_frames in ramp_rates:
        np.random.seed(seed)
        thermal = 0.25 + 0.15 * np.sin(np.linspace(0, 6 * np.pi, T))
        jitter = np.random.normal(0, 0.02, T)
        base_pdl = thermal + jitter
        tap = np.zeros(T)
        for t in range(tap_start, T):
            prog = min(1.0, (t - tap_start) / max(1, ramp_frames))
            tap[t] = total_tap * prog
        phi_stream = _pdl_to_phi_simple(base_pdl + tap)

        tracker = DynamicCoherenceTracker(50, 0.05, 4.0, 3)
        first_edge: Optional[int] = None
        compromised_at: Optional[int] = None
        edges = 0
        for t in range(T):
            r = tracker.process(phi_stream[t], t)
            if r["edge"]:
                edges += 1
                if first_edge is None:
                    first_edge = t
            if r["state"] == LinkState.COMPROMISED and compromised_at is None:
                compromised_at = t

        detected = first_edge is not None
        latency = (first_edge - tap_start) if detected else None
        results.append(dict(
            ramp=ramp_frames,
            detected=detected,
            first_edge=first_edge,
            latency=latency,
            compromised_at=compromised_at,
            edges=edges,
        ))
    return results
