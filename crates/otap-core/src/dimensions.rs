//! The five physical dimensions of an OTAP Transient.
//!
//! Each dimension corresponds to an orthogonal physical property of the
//! optical signal. In the reference model these are typed values; in RTL
//! they are wires or registers driven directly from the photonic frontend.
//!
//! ## Dimensional decoupling invariant
//!
//! No dimension's interpretation depends on another's value. This is the
//! core protocol property that enables single-clock-cycle parallel decode.
//! Encoders and decoders must preserve this.

use crate::error::{ProtocolError, Result};

// ============================================================================
// D2 — Wavelength (destination addressing)
// ============================================================================

/// DWDM channel index on the ITU-T C-band grid (25 GHz spacing).
///
/// The C-band (1530–1565 nm) at 25 GHz provides 176 channels. With L-band
/// extension this doubles to 352. The wavelength selects the destination via
/// passive optical routing (AWG); the FPGA sees only the channel index.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct Wavelength(u16);

impl Wavelength {
    /// Maximum supported channel index (C+L band).
    pub const MAX_CHANNEL: u16 = 351;

    /// Channel spacing in GHz on the ITU-T grid.
    pub const SPACING_GHZ: f64 = 25.0;

    /// Reference frequency for channel 0 (193.1 THz, ITU-T anchor).
    pub const ANCHOR_THZ: f64 = 193.1;

    /// Construct a wavelength from its channel index.
    pub const fn new(channel: u16) -> Result<Self> {
        if channel > Self::MAX_CHANNEL {
            Err(ProtocolError::WavelengthOutOfRange(channel, Self::MAX_CHANNEL))
        } else {
            Ok(Self(channel))
        }
    }

    /// Raw channel index (used as the destination address by passive routing).
    pub const fn channel(&self) -> u16 {
        self.0
    }

    /// Nominal optical frequency for this channel in THz.
    pub fn frequency_thz(&self) -> f64 {
        Self::ANCHOR_THZ + (self.0 as f64) * (Self::SPACING_GHZ / 1000.0)
    }

    /// Nominal wavelength in nm (c / f).
    pub fn wavelength_nm(&self) -> f64 {
        299_792.458 / self.frequency_thz()
    }
}

// ============================================================================
// D3 — Polarization (authentication + integrity)
// ============================================================================

/// A point on the Poincaré sphere expressed as a normalized Stokes vector.
///
/// `(s1, s2, s3)` lies on the unit sphere for fully-polarized light.
/// The receiver tracks these as floats; the FPGA computes them from in-phase
/// and quadrature photodetector currents.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct StokesVector {
    /// Horizontal vs vertical linear polarization axis.
    pub s1: f32,
    /// +45° vs -45° linear polarization axis.
    pub s2: f32,
    /// Right vs left circular polarization axis.
    pub s3: f32,
}

impl StokesVector {
    /// Construct from raw components. Normalizes to the unit sphere.
    pub fn new(s1: f32, s2: f32, s3: f32) -> Self {
        let mag = (s1 * s1 + s2 * s2 + s3 * s3).sqrt().max(1e-9);
        Self {
            s1: s1 / mag,
            s2: s2 / mag,
            s3: s3 / mag,
        }
    }

    /// Horizontal linear polarization (canonical reference state).
    pub const H: Self = Self { s1: 1.0, s2: 0.0, s3: 0.0 };
    /// Vertical linear polarization.
    pub const V: Self = Self { s1: -1.0, s2: 0.0, s3: 0.0 };
    /// +45° diagonal linear polarization.
    pub const D: Self = Self { s1: 0.0, s2: 1.0, s3: 0.0 };
    /// Right circular polarization.
    pub const R: Self = Self { s1: 0.0, s2: 0.0, s3: 1.0 };

    /// Angular distance on the Poincaré sphere (radians).
    pub fn angular_distance(&self, other: &Self) -> f32 {
        let dot = (self.s1 * other.s1 + self.s2 * other.s2 + self.s3 * other.s3)
            .clamp(-1.0, 1.0);
        dot.acos()
    }
}

/// A polarization trajectory: a sequence of Stokes states traced over the
/// duration of one Transient.
///
/// This is the authentication primitive. The trajectory is derived from a
/// shared secret + payload + sequence number via HMAC (see `otap-crypto`).
/// At the receiver, the observed trajectory is compared to the expected one.
///
/// The number of samples per Transient is fixed by the protocol (currently
/// 16 — one per QAM symbol group). This is the `N` in N-sample trajectory.
pub const TRAJECTORY_SAMPLES: usize = 16;

/// A polarization trajectory of fixed length [`TRAJECTORY_SAMPLES`].
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct PolarizationTrajectory {
    /// The sequence of Stokes states. Index 0 is the first sample.
    pub samples: [StokesVector; TRAJECTORY_SAMPLES],
}

impl PolarizationTrajectory {
    /// Construct from a raw sample array.
    pub fn new(samples: [StokesVector; TRAJECTORY_SAMPLES]) -> Self {
        Self { samples }
    }

    /// Compute the discrete winding number of the trajectory around its own
    /// principal axis.
    ///
    /// This is the *topological* authenticator: it is invariant under
    /// arbitrary unitary transforms of the Poincaré sphere (PMD). The
    /// principal axis — the signed sum of consecutive cross products — is
    /// covariant under SO(3), and the winding measured in the plane
    /// perpendicular to it is therefore preserved.
    ///
    /// The original implementation projected onto the s1-s2 plane and counted
    /// revolutions around the *fixed* s3 axis. That is only invariant under
    /// rotations about s3, not under arbitrary PMD.
    ///
    /// Returns 0 for degenerate trajectories (no well-defined axis).
    pub fn winding_number(&self) -> i32 {
        let n = TRAJECTORY_SAMPLES;

        let mut axis = [0.0f32; 3];
        for i in 0..n {
            let a = &self.samples[i];
            let b = &self.samples[(i + 1) % n];
            axis[0] += a.s2 * b.s3 - a.s3 * b.s2;
            axis[1] += a.s3 * b.s1 - a.s1 * b.s3;
            axis[2] += a.s1 * b.s2 - a.s2 * b.s1;
        }
        let axis_norm =
            (axis[0] * axis[0] + axis[1] * axis[1] + axis[2] * axis[2]).sqrt();
        if axis_norm < 1e-6 {
            return 0;
        }
        let axis = [axis[0] / axis_norm, axis[1] / axis_norm, axis[2] / axis_norm];

        // Pick a world axis that's least aligned with `axis` to get a numerically
        // stable cross product. The choice is deterministic but axis-dependent;
        // the winding number itself is invariant to which world axis we pick
        // because changing it just rotates (u, v) within the perpendicular plane
        // by a fixed offset.
        let ax_abs = [axis[0].abs(), axis[1].abs(), axis[2].abs()];
        let world = if ax_abs[0] <= ax_abs[1] && ax_abs[0] <= ax_abs[2] {
            [1.0f32, 0.0, 0.0]
        } else if ax_abs[1] <= ax_abs[2] {
            [0.0f32, 1.0, 0.0]
        } else {
            [0.0f32, 0.0, 1.0]
        };
        let mut u = [
            axis[1] * world[2] - axis[2] * world[1],
            axis[2] * world[0] - axis[0] * world[2],
            axis[0] * world[1] - axis[1] * world[0],
        ];
        let un = (u[0] * u[0] + u[1] * u[1] + u[2] * u[2]).sqrt().max(1e-9);
        u[0] /= un;
        u[1] /= un;
        u[2] /= un;
        let v = [
            axis[1] * u[2] - axis[2] * u[1],
            axis[2] * u[0] - axis[0] * u[2],
            axis[0] * u[1] - axis[1] * u[0],
        ];

        let mut total_angle: f32 = 0.0;
        for i in 0..n {
            let a = &self.samples[i];
            let b = &self.samples[(i + 1) % n];
            let au = a.s1 * u[0] + a.s2 * u[1] + a.s3 * u[2];
            let av = a.s1 * v[0] + a.s2 * v[1] + a.s3 * v[2];
            let bu = b.s1 * u[0] + b.s2 * u[1] + b.s3 * u[2];
            let bv = b.s1 * v[0] + b.s2 * v[1] + b.s3 * v[2];
            let theta_a = av.atan2(au);
            let theta_b = bv.atan2(bu);
            let mut d = theta_b - theta_a;
            while d > core::f32::consts::PI {
                d -= 2.0 * core::f32::consts::PI;
            }
            while d <= -core::f32::consts::PI {
                d += 2.0 * core::f32::consts::PI;
            }
            total_angle += d;
        }
        (total_angle / (2.0 * core::f32::consts::PI)).round() as i32
    }
}

// ============================================================================
// D4 — OAM (application schema selector)
// ============================================================================

/// Orbital Angular Momentum mode (topological charge ℓ).
///
/// Each mode maps to a pre-registered application schema. The receiver uses
/// this to select a decode pipeline *before* parsing the payload — eliminating
/// content-type negotiation entirely.
///
/// Range ℓ = -16..=+16 reflects what has been demonstrated stably in fiber.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct OamMode(i8);

impl OamMode {
    /// Minimum supported topological charge.
    pub const MIN: i8 = -16;
    /// Maximum supported topological charge.
    pub const MAX: i8 = 16;

    /// Raw binary payload (no schema).
    pub const RAW: Self = Self(0);

    /// Construct an OAM mode. Returns error if outside the supported range.
    pub const fn new(charge: i8) -> Result<Self> {
        if charge < Self::MIN || charge > Self::MAX {
            Err(ProtocolError::UnknownOamMode(charge))
        } else {
            Ok(Self(charge))
        }
    }

    /// Const-context constructor: panics at compile time if the charge is out of range.
    ///
    /// Use this in `const` items where the value is known statically.
    pub const fn new_const(charge: i8) -> Self {
        assert!(
            charge >= Self::MIN && charge <= Self::MAX,
            "OAM topological charge out of supported range"
        );
        Self(charge)
    }

    /// The topological charge ℓ.
    pub const fn charge(&self) -> i8 {
        self.0
    }
}

// ============================================================================
// D5 — Temporal Microstructure (sequence + flow control)
// ============================================================================

/// Discrete guard interval categories encoded in temporal microstructure.
///
/// The guard interval is the gap between QAM symbols. By modulating its
/// duration in discrete steps, OTAP signals flow-control state at zero
/// byte overhead.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum GuardInterval {
    /// Normal: next sequential Transient expected.
    Nominal = 0,
    /// Priority elevation: process before queued Transients.
    Priority = 1,
    /// Final Transient in a logical group (end-of-message).
    EndOfMessage = 2,
    /// Retransmission of a previously corrupted Transient.
    Retransmission = 3,
    /// Keepalive: no payload, maintain wavelength reservation.
    Keepalive = 4,
}

/// Temporal microstructure: sequence number + guard interval signal.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct MicroStructure {
    /// 16-bit sequence counter encoded as chirp pattern in the preamble.
    pub sequence: u16,
    /// Flow-control state encoded as guard interval duration.
    pub guard: GuardInterval,
}

impl MicroStructure {
    /// Construct a nominal-priority sequence stamp.
    pub fn new(sequence: u16) -> Self {
        Self { sequence, guard: GuardInterval::Nominal }
    }

    /// Mark this Transient as the last in a logical group.
    pub fn with_eom(mut self) -> Self {
        self.guard = GuardInterval::EndOfMessage;
        self
    }

    /// Mark this Transient as high-priority.
    pub fn with_priority(mut self) -> Self {
        self.guard = GuardInterval::Priority;
        self
    }
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn wavelength_range() {
        assert!(Wavelength::new(0).is_ok());
        assert!(Wavelength::new(Wavelength::MAX_CHANNEL).is_ok());
        assert!(Wavelength::new(Wavelength::MAX_CHANNEL + 1).is_err());
    }

    #[test]
    fn wavelength_frequency_matches_itu_grid() {
        // Channel 0 is the ITU anchor at 193.1 THz (~1553.33 nm)
        let w = Wavelength::new(0).unwrap();
        assert!((w.frequency_thz() - 193.1).abs() < 1e-6);
        // Channel 40 -> 193.1 + 40 * 0.025 = 194.1 THz (~1544.53 nm)
        let w = Wavelength::new(40).unwrap();
        assert!((w.frequency_thz() - 194.1).abs() < 1e-6);
    }

    #[test]
    fn stokes_normalization() {
        let s = StokesVector::new(2.0, 0.0, 0.0);
        assert!((s.s1 - 1.0).abs() < 1e-6);
        assert_eq!(s.s2, 0.0);
        assert_eq!(s.s3, 0.0);
    }

    #[test]
    fn winding_number_simple_loop() {
        // A trajectory that winds once around the s3 axis in the s1-s2 plane.
        let mut samples = [StokesVector::H; TRAJECTORY_SAMPLES];
        for i in 0..TRAJECTORY_SAMPLES {
            let theta = (i as f32) * 2.0 * core::f32::consts::PI / (TRAJECTORY_SAMPLES as f32);
            samples[i] = StokesVector::new(theta.cos(), theta.sin(), 0.0);
        }
        let traj = PolarizationTrajectory::new(samples);
        assert_eq!(traj.winding_number(), 1);
    }

    #[test]
    fn winding_number_double_loop() {
        // Two loops -> winding number 2.
        let mut samples = [StokesVector::H; TRAJECTORY_SAMPLES];
        for i in 0..TRAJECTORY_SAMPLES {
            let theta = (i as f32) * 4.0 * core::f32::consts::PI / (TRAJECTORY_SAMPLES as f32);
            samples[i] = StokesVector::new(theta.cos(), theta.sin(), 0.0);
        }
        let traj = PolarizationTrajectory::new(samples);
        assert_eq!(traj.winding_number(), 2);
    }

    #[test]
    fn oam_range() {
        assert!(OamMode::new(0).is_ok());
        assert!(OamMode::new(16).is_ok());
        assert!(OamMode::new(-16).is_ok());
        assert!(OamMode::new(17).is_err());
    }
}
