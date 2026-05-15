//! # otap-crypto
//!
//! Polarization-trajectory authentication and integrity for OTAP.
//!
//! ## Two authentication modes
//!
//! ### Trajectory-match (classical)
//! The polarization samples are derived deterministically from
//! `HMAC-SHA256(shared_secret, sequence ‖ payload)`. The receiver re-derives the
//! expected trajectory and compares sample-by-sample. Requires the receiver to
//! compensate the fiber's Jones matrix (standard coherent-DSP capability).
//!
//! ### Topological (PMD-invariant)
//! The authenticator is the *winding number* of the trajectory around the s3
//! axis (and optionally s1, s2 axes). Winding numbers are topological invariants
//! of closed loops on the Poincaré sphere — they survive arbitrary unitary
//! transforms applied by the fiber. This eliminates the PMD-compensation
//! requirement for authentication while preserving security strength
//! proportional to the integer-encoding space.
//!
//! Both modes share the same shared-secret HMAC; they differ in what feature
//! of the trajectory is checked.
//!
//! ## Threat model
//!
//! - **Passive eavesdropper on the data channel**: cannot forge trajectories
//!   without the shared secret.
//! - **Active attacker modifying the payload**: detected because the trajectory
//!   is a function of the payload — modification produces a trajectory mismatch.
//! - **Replay**: prevented by sequence-number inclusion in the HMAC.
//! - **Out-of-scope**: shared-secret distribution (handled via reference-channel
//!   FFA or out-of-band provisioning).

#![forbid(unsafe_op_in_unsafe_fn)]
#![warn(missing_docs)]

use hmac::{Hmac, Mac};
use otap_core::{
    OamMode, PolarizationTrajectory, ProtocolError, Result, StokesVector,
    TRAJECTORY_SAMPLES, Wavelength,
};
use sha2::Sha256;
use zeroize::{Zeroize, ZeroizeOnDrop};

type HmacSha256 = Hmac<Sha256>;

// ============================================================================
// Shared secret
// ============================================================================

/// A 256-bit shared secret between two OTAP endpoints.
///
/// Zeroized on drop. In production this is derived from the reference-channel
/// FFA handshake (see `docs/architecture.md` §6); for the reference model it
/// is loaded from a config file or generated for testing.
#[derive(Clone, Zeroize, ZeroizeOnDrop)]
pub struct SharedSecret([u8; 32]);

impl SharedSecret {
    /// Construct from a 32-byte key. Caller is responsible for ensuring the
    /// key has sufficient entropy.
    pub fn new(key: [u8; 32]) -> Self {
        Self(key)
    }

    /// For testing only — derive a deterministic key from a string label.
    /// **Never** use in production.
    pub fn test_from_label(label: &str) -> Self {
        use sha2::Digest;
        let mut hasher = Sha256::new();
        hasher.update(b"OTAP-TEST-KEY-");
        hasher.update(label.as_bytes());
        let digest = hasher.finalize();
        let mut key = [0u8; 32];
        key.copy_from_slice(&digest);
        Self(key)
    }

    /// Internal key bytes. Only the trajectory derivation should touch this.
    fn bytes(&self) -> &[u8; 32] {
        &self.0
    }
}

impl core::fmt::Debug for SharedSecret {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        // Never print the key material.
        f.write_str("SharedSecret(<redacted>)")
    }
}

// ============================================================================
// Trajectory derivation
// ============================================================================

/// Inputs that bind the trajectory to a specific Transient.
///
/// Any change to *any* of these fields produces a completely different
/// trajectory at the receiver. This is what gives D3 its payload-integrity
/// property: modifying the payload without the shared secret cannot produce
/// the matching trajectory.
pub struct TrajectoryContext<'a> {
    /// Destination address (so a stolen trajectory can't be replayed elsewhere).
    pub wavelength: Wavelength,
    /// Schema (so trajectories from one app can't be replayed as another).
    pub oam: OamMode,
    /// Sequence number (anti-replay).
    pub sequence: u16,
    /// Payload bytes (integrity binding).
    pub payload: &'a [u8],
}

/// Derive the expected polarization trajectory from key material + context.
///
/// This function is deterministic: same key + same context = same trajectory.
/// The encoder calls it to generate the trajectory to transmit; the receiver
/// calls it (after recovering the payload) to verify.
pub fn derive_trajectory(
    secret: &SharedSecret,
    ctx: &TrajectoryContext<'_>,
) -> PolarizationTrajectory {
    let mut mac = HmacSha256::new_from_slice(secret.bytes())
        .expect("HMAC-SHA256 accepts any key length");

    // Domain separator — protects against cross-protocol HMAC reuse.
    mac.update(b"OTAP-D3-TRAJECTORY-v1");
    mac.update(&ctx.wavelength.channel().to_be_bytes());
    mac.update(&(ctx.oam.charge() as u8).to_be_bytes());
    mac.update(&ctx.sequence.to_be_bytes());
    mac.update(&(ctx.payload.len() as u32).to_be_bytes());
    mac.update(ctx.payload);

    let tag = mac.finalize().into_bytes();

    // Expand 32 bytes of HMAC output to TRAJECTORY_SAMPLES Stokes vectors.
    // Each sample needs 3 floats; we re-key with a counter for >32 bytes.
    let mut samples = [StokesVector::H; TRAJECTORY_SAMPLES];
    for (i, slot) in samples.iter_mut().enumerate() {
        let mut sample_mac = HmacSha256::new_from_slice(secret.bytes())
            .expect("HMAC-SHA256 accepts any key length");
        sample_mac.update(b"OTAP-D3-SAMPLE-v1");
        sample_mac.update(&tag);
        sample_mac.update(&(i as u16).to_be_bytes());
        let sample_bytes = sample_mac.finalize().into_bytes();
        *slot = stokes_from_bytes(&sample_bytes[..6]);
    }

    PolarizationTrajectory::new(samples)
}

/// Map 6 bytes of HMAC output to a normalized Stokes vector.
///
/// Uses two bytes per axis, interpreted as a signed value in [-1, 1].
fn stokes_from_bytes(bytes: &[u8]) -> StokesVector {
    debug_assert!(bytes.len() >= 6);
    let to_signed = |hi: u8, lo: u8| -> f32 {
        let raw = i16::from_be_bytes([hi, lo]);
        (raw as f32) / 32_768.0
    };
    StokesVector::new(
        to_signed(bytes[0], bytes[1]),
        to_signed(bytes[2], bytes[3]),
        to_signed(bytes[4], bytes[5]),
    )
}

// ============================================================================
// Trajectory verification — two modes
// ============================================================================

/// Authentication mode selected by the receiver.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum AuthMode {
    /// Sample-by-sample trajectory match.
    ///
    /// Requires the receiver to have compensated the fiber's Jones matrix.
    /// Stricter check; recommended when DSP polarization tracking is reliable.
    TrajectoryMatch {
        /// Max allowed angular distance per sample (radians on the Poincaré sphere).
        tolerance_rad: f32,
    },
    /// Topological winding-number match.
    ///
    /// PMD-invariant. Recommended for noisy or long-haul links where Jones
    /// matrix tracking is unreliable.
    Topological,
}

impl Default for AuthMode {
    fn default() -> Self {
        AuthMode::TrajectoryMatch { tolerance_rad: 0.1 }
    }
}

/// Verify an observed trajectory against the expected derivation.
///
/// Returns `Ok(())` on match, `Err(AuthenticationFailure)` on mismatch.
/// This function does not distinguish authentication failure from integrity
/// failure — both produce trajectory mismatch and both are equally fatal.
pub fn verify_trajectory(
    secret: &SharedSecret,
    ctx: &TrajectoryContext<'_>,
    observed: &PolarizationTrajectory,
    mode: AuthMode,
) -> Result<()> {
    let expected = derive_trajectory(secret, ctx);

    match mode {
        AuthMode::TrajectoryMatch { tolerance_rad } => {
            for i in 0..TRAJECTORY_SAMPLES {
                let d = expected.samples[i].angular_distance(&observed.samples[i]);
                if d > tolerance_rad {
                    return Err(ProtocolError::AuthenticationFailure);
                }
            }
            Ok(())
        }
        AuthMode::Topological => {
            if expected.winding_number_s3() == observed.winding_number_s3() {
                Ok(())
            } else {
                Err(ProtocolError::AuthenticationFailure)
            }
        }
    }
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use otap_core::OamMode;

    fn fixture_ctx<'a>(payload: &'a [u8]) -> TrajectoryContext<'a> {
        TrajectoryContext {
            wavelength: Wavelength::new(40).unwrap(),
            oam: OamMode::new(1).unwrap(),
            sequence: 0x4A2F,
            payload,
        }
    }

    #[test]
    fn derivation_is_deterministic() {
        let secret = SharedSecret::test_from_label("alice-bob");
        let payload = b"hello transient";
        let t1 = derive_trajectory(&secret, &fixture_ctx(payload));
        let t2 = derive_trajectory(&secret, &fixture_ctx(payload));
        assert_eq!(t1, t2);
    }

    #[test]
    fn round_trip_verifies() {
        let secret = SharedSecret::test_from_label("alice-bob");
        let payload = b"hello transient";
        let traj = derive_trajectory(&secret, &fixture_ctx(payload));
        verify_trajectory(
            &secret,
            &fixture_ctx(payload),
            &traj,
            AuthMode::TrajectoryMatch { tolerance_rad: 0.001 },
        ).expect("round trip must verify");
    }

    #[test]
    fn payload_tampering_breaks_auth() {
        let secret = SharedSecret::test_from_label("alice-bob");
        let original = b"buy 1000 AAPL";
        let tampered = b"buy 9999 AAPL";
        let traj = derive_trajectory(&secret, &fixture_ctx(original));
        // Receiver thinks the payload is `tampered` and tries to verify.
        let err = verify_trajectory(
            &secret,
            &fixture_ctx(tampered),
            &traj,
            AuthMode::TrajectoryMatch { tolerance_rad: 0.1 },
        ).unwrap_err();
        assert!(matches!(err, ProtocolError::AuthenticationFailure));
    }

    #[test]
    fn wrong_secret_breaks_auth() {
        let alice = SharedSecret::test_from_label("alice-bob");
        let mallory = SharedSecret::test_from_label("mallory");
        let payload = b"hello transient";
        let traj = derive_trajectory(&alice, &fixture_ctx(payload));
        let err = verify_trajectory(
            &mallory,
            &fixture_ctx(payload),
            &traj,
            AuthMode::TrajectoryMatch { tolerance_rad: 0.1 },
        ).unwrap_err();
        assert!(matches!(err, ProtocolError::AuthenticationFailure));
    }

    #[test]
    fn sequence_replay_breaks_auth() {
        let secret = SharedSecret::test_from_label("alice-bob");
        let payload = b"hello transient";
        let mut ctx = fixture_ctx(payload);
        let traj = derive_trajectory(&secret, &ctx);
        // Replay attempt: same payload + auth, different sequence number.
        ctx.sequence = ctx.sequence.wrapping_add(1);
        let err = verify_trajectory(
            &secret,
            &ctx,
            &traj,
            AuthMode::TrajectoryMatch { tolerance_rad: 0.1 },
        ).unwrap_err();
        assert!(matches!(err, ProtocolError::AuthenticationFailure));
    }
}
