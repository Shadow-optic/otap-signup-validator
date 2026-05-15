//! # otap-sim
//!
//! Channel impairment simulator for OTAP.
//!
//! Models the physical effects that a Transient experiences in real fiber:
//! polarization-mode dispersion (PMD), additive noise, and bit errors.
//!
//! The PMD model is the load-bearing one: it lets us *demonstrate* that the
//! topological D3 authenticator is PMD-invariant while sample-match auth
//! breaks. This is the core empirical claim of the protocol's auth design.

#![forbid(unsafe_op_in_unsafe_fn)]
#![warn(missing_docs)]

use otap_core::{PolarizationTrajectory, StokesVector, Transient, TRAJECTORY_SAMPLES};

// ============================================================================
// 3D rotation helpers (SO(3) on the Poincaré sphere)
// ============================================================================

/// A 3×3 real rotation matrix acting on the Poincaré sphere.
///
/// PMD is a unitary 2×2 Jones-matrix transform on the field; in Stokes-vector
/// representation it becomes a 3D rotation on the Poincaré sphere. This is
/// the abstraction the channel exposes — internally we never need to compute
/// the complex Jones matrix.
#[derive(Debug, Clone, Copy)]
pub struct PoincareRotation {
    pub(crate) m: [[f32; 3]; 3],
}

impl PoincareRotation {
    /// Identity rotation (no PMD).
    pub const IDENTITY: Self = Self {
        m: [[1.0, 0.0, 0.0], [0.0, 1.0, 0.0], [0.0, 0.0, 1.0]],
    };

    /// Rotation about the s3 axis by `angle` radians.
    pub fn about_s3(angle: f32) -> Self {
        let c = angle.cos();
        let s = angle.sin();
        Self {
            m: [[c, -s, 0.0], [s, c, 0.0], [0.0, 0.0, 1.0]],
        }
    }

    /// Rotation about the s1 axis.
    pub fn about_s1(angle: f32) -> Self {
        let c = angle.cos();
        let s = angle.sin();
        Self {
            m: [[1.0, 0.0, 0.0], [0.0, c, -s], [0.0, s, c]],
        }
    }

    /// Rotation about the s2 axis.
    pub fn about_s2(angle: f32) -> Self {
        let c = angle.cos();
        let s = angle.sin();
        Self {
            m: [[c, 0.0, s], [0.0, 1.0, 0.0], [-s, 0.0, c]],
        }
    }

    /// Compose two rotations (self then other).
    pub fn then(&self, other: &Self) -> Self {
        let mut m = [[0.0f32; 3]; 3];
        for i in 0..3 {
            for j in 0..3 {
                m[i][j] = (0..3).map(|k| other.m[i][k] * self.m[k][j]).sum();
            }
        }
        Self { m }
    }

    /// Apply this rotation to a Stokes vector.
    pub fn apply(&self, v: &StokesVector) -> StokesVector {
        let s = [v.s1, v.s2, v.s3];
        let r = [
            self.m[0][0] * s[0] + self.m[0][1] * s[1] + self.m[0][2] * s[2],
            self.m[1][0] * s[0] + self.m[1][1] * s[1] + self.m[1][2] * s[2],
            self.m[2][0] * s[0] + self.m[2][1] * s[1] + self.m[2][2] * s[2],
        ];
        StokesVector::new(r[0], r[1], r[2])
    }
}

// ============================================================================
// Lightweight RNG (xorshift) — deterministic, no external deps
// ============================================================================

/// Tiny deterministic RNG for reproducible channel simulations.
///
/// Not cryptographically secure; used only for impairment modeling.
#[derive(Debug, Clone)]
pub struct ChannelRng {
    state: u64,
}

impl ChannelRng {
    /// Seed the RNG.
    pub const fn new(seed: u64) -> Self {
        Self {
            state: if seed == 0 { 0xDEAD_BEEF_CAFE_F00D } else { seed },
        }
    }

    fn next_u64(&mut self) -> u64 {
        let mut x = self.state;
        x ^= x << 13;
        x ^= x >> 7;
        x ^= x << 17;
        self.state = x;
        x
    }

    /// Uniform float in [-1, 1).
    pub fn next_signed_unit(&mut self) -> f32 {
        let n = self.next_u64() as u32;
        (n as f32 / u32::MAX as f32) * 2.0 - 1.0
    }

    /// Uniform float in [0, 1).
    pub fn next_unit(&mut self) -> f32 {
        let n = self.next_u64() as u32;
        n as f32 / u32::MAX as f32
    }
}

// ============================================================================
// Channel model
// ============================================================================

/// A fiber channel with configurable impairments.
#[derive(Debug, Clone)]
pub struct Channel {
    /// Per-Transient polarization rotation (models slowly-varying PMD).
    pub pmd_rotation: PoincareRotation,
    /// Probability of a payload bit flip during transit. Zero for clean fiber.
    pub bit_error_rate: f64,
    /// RNG for stochastic effects.
    pub rng: ChannelRng,
}

impl Channel {
    /// A perfect fiber: identity transform, zero BER.
    pub fn perfect() -> Self {
        Self {
            pmd_rotation: PoincareRotation::IDENTITY,
            bit_error_rate: 0.0,
            rng: ChannelRng::new(1),
        }
    }

    /// A fiber with a single fixed PMD rotation and no bit errors.
    ///
    /// Models a stable long-haul link where the slowly-varying Jones matrix
    /// has been characterized but no realtime compensation is applied — the
    /// scenario where topological auth pays for itself.
    pub fn with_pmd(rotation: PoincareRotation) -> Self {
        Self {
            pmd_rotation: rotation,
            bit_error_rate: 0.0,
            rng: ChannelRng::new(42),
        }
    }

    /// Random-PMD fiber with a given seed.
    pub fn random_pmd(seed: u64) -> Self {
        let mut rng = ChannelRng::new(seed);
        let r1 = PoincareRotation::about_s1(rng.next_signed_unit() * core::f32::consts::PI);
        let r2 = PoincareRotation::about_s2(rng.next_signed_unit() * core::f32::consts::PI);
        let r3 = PoincareRotation::about_s3(rng.next_signed_unit() * core::f32::consts::PI);
        Self {
            pmd_rotation: r1.then(&r2).then(&r3),
            bit_error_rate: 0.0,
            rng,
        }
    }

    /// Transit a Transient through this channel.
    ///
    /// The payload may receive bit errors; the polarization trajectory is
    /// rotated by the PMD matrix. Other dimensions (λ, OAM, μT) are not
    /// disturbed in this simple model — wavelength is enforced by the AWG,
    /// OAM is assumed compensated by the receiver SLM/MIMO, and μT is
    /// time-domain and naturally resilient to polarization effects.
    pub fn transmit(&mut self, mut t: Transient) -> Transient {
        // Rotate every polarization sample.
        let mut new_samples = [StokesVector::H; TRAJECTORY_SAMPLES];
        for i in 0..TRAJECTORY_SAMPLES {
            new_samples[i] = self.pmd_rotation.apply(&t.polarization.samples[i]);
        }
        t.polarization = PolarizationTrajectory::new(new_samples);

        // Apply bit errors to payload.
        if self.bit_error_rate > 0.0 {
            for byte in t.payload.iter_mut() {
                for bit in 0..8 {
                    if (self.rng.next_unit() as f64) < self.bit_error_rate {
                        *byte ^= 1 << bit;
                    }
                }
            }
        }

        t
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rotation_preserves_unit_sphere() {
        let r = PoincareRotation::about_s3(0.7);
        let v = StokesVector::new(0.6, 0.6, 0.5);
        let rotated = r.apply(&v);
        let mag = (rotated.s1.powi(2) + rotated.s2.powi(2) + rotated.s3.powi(2)).sqrt();
        assert!((mag - 1.0).abs() < 1e-5);
    }

    #[test]
    fn identity_rotation_is_identity() {
        let v = StokesVector::new(0.3, 0.4, 0.5);
        let r = PoincareRotation::IDENTITY.apply(&v);
        assert!((r.s1 - v.s1).abs() < 1e-5);
        assert!((r.s2 - v.s2).abs() < 1e-5);
        assert!((r.s3 - v.s3).abs() < 1e-5);
    }

    #[test]
    fn composition_order() {
        // Rotate by π/2 about s3 — H should map to D, D should map to V.
        let r = PoincareRotation::about_s3(core::f32::consts::FRAC_PI_2);
        let h_to_d = r.apply(&StokesVector::H);
        assert!((h_to_d.s2 - 1.0).abs() < 1e-5);
    }
}
