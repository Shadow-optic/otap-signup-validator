//! Protocol-level errors.
//!
//! These map to specific hardware-detectable conditions in the FPGA receiver.
//! The variants are deliberately enumerated to match what a real receiver can
//! distinguish — see `docs/architecture.md` §5.

use thiserror::Error;

/// Errors raised during OTAP encode/decode operations.
#[derive(Debug, Error, Clone, PartialEq, Eq)]
pub enum ProtocolError {
    /// Wavelength channel index is outside the supported address space.
    #[error("wavelength channel {0} out of range (max {1})")]
    WavelengthOutOfRange(u16, u16),

    /// OAM mode does not map to any registered application schema.
    #[error("unregistered OAM mode {0}")]
    UnknownOamMode(i8),

    /// Polarization trajectory failed authentication.
    /// The receiver computed an expected trajectory from the shared secret and
    /// sequence number; the observed trajectory did not match.
    #[error("polarization authentication failed")]
    AuthenticationFailure,

    /// Payload integrity check failed.
    /// The polarization trajectory is bound to the payload; mismatch indicates
    /// tampering OR transmission error (the receiver cannot distinguish).
    #[error("payload integrity check failed")]
    IntegrityFailure,

    /// Sequence number gap detected.
    /// Receiver tracks the expected next sequence per (source, OAM-mode) pair.
    #[error("sequence gap: expected {expected}, got {actual}")]
    SequenceGap { expected: u16, actual: u16 },

    /// Payload bit-length does not match the schema for this OAM mode.
    #[error("schema {oam_mode} expects {expected} bits, got {actual}")]
    SchemaMismatch {
        oam_mode: i8,
        expected: usize,
        actual: usize,
    },

    /// Wire format error during deserialization.
    #[error("malformed wire format: {0}")]
    WireFormat(&'static str),
}

/// Convenience alias.
pub type Result<T> = core::result::Result<T, ProtocolError>;
