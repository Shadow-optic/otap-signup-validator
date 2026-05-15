//! Heartbeat / clock sync — OAM ℓ=+4.
//!
//! Minimum-overhead 32-byte payload. Sent on a regular interval to maintain
//! the wavelength reservation (via the keepalive guard interval, see
//! [`otap_core::GuardInterval::Keepalive`]) and report local clock offsets
//! for distributed-system time synchronization.

use crate::Schema;
use otap_core::{OamMode, ProtocolError, Result};

/// Heartbeat / clock sync.
#[derive(Debug, Clone, PartialEq)]
pub struct Heartbeat {
    /// Originating node identifier.
    pub node_id: u64,
    /// Local clock offset from a reference (e.g., PTP grandmaster), in ns.
    /// Signed to allow negative offsets.
    pub clock_offset_ns: i64,
    /// Heartbeat sequence (independent of Transient sequence).
    pub heartbeat_seq: u64,
    /// Reserved.
    pub _reserved: [u8; 8],
}

impl Schema for Heartbeat {
    const OAM_MODE: OamMode = OamMode::new_const(4);

    const PAYLOAD_BYTES: usize = 32;

    fn encode(&self) -> Vec<u8> {
        let mut buf = vec![0u8; Self::PAYLOAD_BYTES];
        buf[0..8].copy_from_slice(&self.node_id.to_be_bytes());
        buf[8..16].copy_from_slice(&self.clock_offset_ns.to_be_bytes());
        buf[16..24].copy_from_slice(&self.heartbeat_seq.to_be_bytes());
        buf[24..32].copy_from_slice(&self._reserved);
        buf
    }

    fn decode(bytes: &[u8]) -> Result<Self> {
        if bytes.len() != Self::PAYLOAD_BYTES {
            return Err(ProtocolError::SchemaMismatch {
                oam_mode: Self::OAM_MODE.charge(),
                expected: Self::PAYLOAD_BYTES,
                actual: bytes.len(),
            });
        }
        let mut reserved = [0u8; 8];
        reserved.copy_from_slice(&bytes[24..32]);
        Ok(Self {
            node_id: u64::from_be_bytes(bytes[0..8].try_into().unwrap()),
            clock_offset_ns: i64::from_be_bytes(bytes[8..16].try_into().unwrap()),
            heartbeat_seq: u64::from_be_bytes(bytes[16..24].try_into().unwrap()),
            _reserved: reserved,
        })
    }
}
