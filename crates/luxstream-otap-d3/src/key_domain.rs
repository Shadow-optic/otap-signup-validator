//! Cryptographic Domain Separation.
//!
//! Enforces at the type level that:
//! 1. D3 Data Plane keys (Ed25519) are ONLY used for high-speed frame auth.
//! 2. FLR Control Plane keys (P-256) are ONLY used for schema signing.
//!
//! The compiler prevents cross-domain key use — there is no `sign_schema`
//! on `D3SigningKey` and no `sign_raw` on `FlrSigningKey`.

use ed25519_dalek::{
    Signature as EdSig, Signer as EdSigner, SigningKey as EdSk, Verifier as EdVerifier,
    VerifyingKey as EdVk,
};
use p256::ecdsa::{
    signature::{Signer, Verifier},
    Signature as P256Sig, SigningKey as P256Sk, VerifyingKey as P256Vk,
};

/// Ed25519 public key length (compressed point).
pub const PUBKEY_LEN: usize = 32;
/// Ed25519 nonce length.
pub const NONCE_LEN: usize = 32;
/// Ed25519 signature length.
pub const SIG_LEN: usize = 64;

// ============================================================================
// D3 DATA PLANE DOMAIN (Ed25519)
// ============================================================================

/// Ed25519 signing key for D3 frame authentication.
/// Cannot be used for FLR schema signing — the method doesn't exist.
#[derive(Clone)]
pub struct D3SigningKey(pub(crate) EdSk);

/// Ed25519 verifying key for D3 frame verification.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct D3VerifyingKey(pub(crate) EdVk);

impl D3SigningKey {
    /// Construct from raw 32-byte seed.
    pub fn from_bytes(bytes: &[u8; 32]) -> Self {
        Self(EdSk::from_bytes(bytes))
    }

    /// Derive the corresponding public key.
    pub fn verifying_key(&self) -> D3VerifyingKey {
        D3VerifyingKey(self.0.verifying_key())
    }

    /// Sign arbitrary bytes in the D3 domain.
    pub(crate) fn sign_raw(&self, msg: &[u8]) -> [u8; SIG_LEN] {
        self.0.sign(msg).to_bytes()
    }
}

impl D3VerifyingKey {
    /// Construct from raw 32-byte compressed Edwards point.
    pub fn from_bytes(bytes: &[u8; PUBKEY_LEN]) -> Result<Self, &'static str> {
        EdVk::from_bytes(bytes)
            .map(Self)
            .map_err(|_| "Invalid Ed25519 D3 public key")
    }

    /// Serialize to raw bytes.
    pub fn as_bytes(&self) -> [u8; PUBKEY_LEN] {
        self.0.to_bytes()
    }

    /// Verify a D3-domain signature.
    pub(crate) fn verify_raw(&self, msg: &[u8], sig: &[u8; SIG_LEN]) -> Result<(), &'static str> {
        let s = EdSig::from_bytes(sig);
        self.0
            .verify(msg, &s)
            .map_err(|_| "D3 Ed25519 verification failed")
    }
}

// ============================================================================
// FLR CONTROL PLANE DOMAIN (ECDSA P-256)
// ============================================================================

/// P-256 signing key for FLR schema/commitment signing.
/// Cannot be used for D3 frame auth — the method doesn't exist.
#[derive(Clone)]
pub struct FlrSigningKey(P256Sk);

/// P-256 verifying key for FLR schema verification.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct FlrVerifyingKey(P256Vk);

impl FlrSigningKey {
    /// Construct from raw scalar bytes.
    pub fn from_bytes(bytes: &[u8]) -> Result<Self, &'static str> {
        P256Sk::from_slice(bytes)
            .map(Self)
            .map_err(|_| "Invalid P-256 FLR secret key")
    }

    /// Derive the corresponding public key.
    pub fn verifying_key(&self) -> FlrVerifyingKey {
        FlrVerifyingKey(*self.0.verifying_key())
    }

    /// Sign a schema hash in the FLR domain.
    pub fn sign_schema(&self, schema_hash: &[u8]) -> P256Sig {
        self.0.sign(schema_hash)
    }
}

impl FlrVerifyingKey {
    /// Verify an FLR-domain schema signature.
    pub fn verify_schema(&self, schema_hash: &[u8], sig: &P256Sig) -> Result<(), &'static str> {
        self.0
            .verify(schema_hash, sig)
            .map_err(|_| "FLR P-256 verification failed")
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rand_core::OsRng;

    #[test]
    fn domain_isolation_compile_time() {
        let d3_sk = D3SigningKey::from_bytes(&[42u8; 32]);
        let flr_sk = FlrSigningKey(P256Sk::random(&mut OsRng));

        let frame_data = b"high-speed-luxstream-frame";
        let schema_data = b"json-routing-schema";

        // D3 signs/verifies frames
        let d3_sig = d3_sk.sign_raw(frame_data);
        assert!(d3_sk
            .verifying_key()
            .verify_raw(frame_data, &d3_sig)
            .is_ok());

        // FLR signs/verifies schemas
        let flr_sig = flr_sk.sign_schema(schema_data);
        assert!(flr_sk
            .verifying_key()
            .verify_schema(schema_data, &flr_sig)
            .is_ok());

        // COMPILE-TIME ENFORCEMENT:
        // d3_sk.sign_schema(...)  → does not exist
        // flr_sk.sign_raw(...)    → does not exist
    }
}
