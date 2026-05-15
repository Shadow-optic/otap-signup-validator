//! Market data tick — OAM ℓ=+3.
//!
//! 64-byte fixed payload for top-of-book updates.

use crate::Schema;
use otap_core::{OamMode, ProtocolError, Result};

/// Top-of-book market data update.
#[derive(Debug, Clone, PartialEq)]
pub struct MarketTick {
    /// Ticker symbol (8 bytes, null-padded).
    pub symbol: [u8; 8],
    /// Best bid price in cents.
    pub bid_cents: f64,
    /// Best ask price in cents.
    pub ask_cents: f64,
    /// Last trade price in cents.
    pub last_cents: f64,
    /// Cumulative volume.
    pub volume: u64,
    /// Exchange timestamp (ns since Unix epoch).
    pub timestamp_ns: u64,
    /// Reserved (16 bytes, zeroed).
    pub _reserved: [u8; 16],
}

impl Schema for MarketTick {
    const OAM_MODE: OamMode = OamMode::new_const(3);

    const PAYLOAD_BYTES: usize = 64;

    fn encode(&self) -> Vec<u8> {
        let mut buf = vec![0u8; Self::PAYLOAD_BYTES];
        buf[0..8].copy_from_slice(&self.symbol);
        buf[8..16].copy_from_slice(&self.bid_cents.to_be_bytes());
        buf[16..24].copy_from_slice(&self.ask_cents.to_be_bytes());
        buf[24..32].copy_from_slice(&self.last_cents.to_be_bytes());
        buf[32..40].copy_from_slice(&self.volume.to_be_bytes());
        buf[40..48].copy_from_slice(&self.timestamp_ns.to_be_bytes());
        buf[48..64].copy_from_slice(&self._reserved);
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
        let mut symbol = [0u8; 8];
        symbol.copy_from_slice(&bytes[0..8]);
        let mut reserved = [0u8; 16];
        reserved.copy_from_slice(&bytes[48..64]);
        Ok(Self {
            symbol,
            bid_cents: f64::from_be_bytes(bytes[8..16].try_into().unwrap()),
            ask_cents: f64::from_be_bytes(bytes[16..24].try_into().unwrap()),
            last_cents: f64::from_be_bytes(bytes[24..32].try_into().unwrap()),
            volume: u64::from_be_bytes(bytes[32..40].try_into().unwrap()),
            timestamp_ns: u64::from_be_bytes(bytes[40..48].try_into().unwrap()),
            _reserved: reserved,
        })
    }
}
