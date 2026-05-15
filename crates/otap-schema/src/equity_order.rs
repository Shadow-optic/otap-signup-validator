//! Equity trade order — OAM ℓ=+1.
//!
//! This is the canonical example from the OTAP spec §9.2. A 256-byte fixed
//! payload that carries everything a downstream matching engine needs.
//!
//! ## Layout (256 bytes)
//!
//! | Offset | Length | Field              | Encoding              |
//! |--------|--------|--------------------|-----------------------|
//! | 0      | 4      | Symbol             | ASCII, null-padded    |
//! | 4      | 1      | Side               | 0=BUY, 1=SELL         |
//! | 5      | 3      | (reserved padding) | zeroed                |
//! | 8      | 4      | Quantity           | u32 big-endian        |
//! | 12     | 4      | (reserved padding) | zeroed                |
//! | 16     | 8      | Price (cents)      | f64 big-endian        |
//! | 24     | 8      | Timestamp (ns)     | u64 big-endian        |
//! | 32     | 8      | Client Order ID    | u64 big-endian        |
//! | 40     | 16     | Firm UUID          | raw bytes             |
//! | 56     | 200    | Reserved           | zeroed for now        |
//!
//! Note: the spec describes a bit-packed layout that saves ~3 bytes by
//! co-locating the 1-bit Side with adjacent fields. The reference model
//! aligns to bytes for clarity; the FPGA RTL may choose either layout
//! provided it matches the receiver. Both layouts are 256 bytes total.

use crate::Schema;
use otap_core::{OamMode, ProtocolError, Result};

/// Order side.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum Side {
    /// Buy order.
    Buy = 0,
    /// Sell order.
    Sell = 1,
}

impl Side {
    fn from_u8(b: u8) -> Result<Self> {
        match b {
            0 => Ok(Self::Buy),
            1 => Ok(Self::Sell),
            _ => Err(ProtocolError::WireFormat("invalid side byte")),
        }
    }
}

/// Equity trade order — what a matching engine receives and acts on.
#[derive(Debug, Clone, PartialEq)]
pub struct EquityTradeOrder {
    /// Ticker symbol (up to 4 ASCII characters, null-padded).
    pub symbol: [u8; 4],
    /// Buy or sell.
    pub side: Side,
    /// Number of shares.
    pub quantity: u32,
    /// Limit price in cents (use 0 for market order convention; production
    /// systems should use a tagged union but this is the bit-exact wire form).
    pub price_cents: f64,
    /// Transmit timestamp (ns since Unix epoch).
    pub timestamp_ns: u64,
    /// Client-assigned unique order ID.
    pub client_order_id: u64,
    /// Firm UUID (16 bytes, raw).
    pub firm_uuid: [u8; 16],
}

impl EquityTradeOrder {
    /// Construct a new order. Pads/truncates symbol to 4 ASCII bytes.
    pub fn new(
        symbol: &str,
        side: Side,
        quantity: u32,
        price_cents: f64,
        timestamp_ns: u64,
        client_order_id: u64,
        firm_uuid: [u8; 16],
    ) -> Self {
        let mut sym = [0u8; 4];
        for (i, b) in symbol.as_bytes().iter().take(4).enumerate() {
            sym[i] = *b;
        }
        Self {
            symbol: sym,
            side,
            quantity,
            price_cents,
            timestamp_ns,
            client_order_id,
            firm_uuid,
        }
    }

    /// Symbol as a printable string (trimmed of trailing nulls).
    pub fn symbol_str(&self) -> &str {
        let end = self.symbol.iter().position(|&b| b == 0).unwrap_or(self.symbol.len());
        // Symbol is constrained to ASCII at construction; safe.
        core::str::from_utf8(&self.symbol[..end]).unwrap_or("?")
    }
}

impl Schema for EquityTradeOrder {
    // SAFETY: OamMode::new(1) is in range (-16..=16). unwrap is fine in const ctx.
    const OAM_MODE: OamMode = OamMode::new_const(1);

    const PAYLOAD_BYTES: usize = 256;

    fn encode(&self) -> Vec<u8> {
        let mut buf = vec![0u8; Self::PAYLOAD_BYTES];
        buf[0..4].copy_from_slice(&self.symbol);
        buf[4] = self.side as u8;
        // Bytes 5..8 reserved (zero).
        buf[8..12].copy_from_slice(&self.quantity.to_be_bytes());
        // Bytes 12..16 reserved (zero).
        buf[16..24].copy_from_slice(&self.price_cents.to_be_bytes());
        buf[24..32].copy_from_slice(&self.timestamp_ns.to_be_bytes());
        buf[32..40].copy_from_slice(&self.client_order_id.to_be_bytes());
        buf[40..56].copy_from_slice(&self.firm_uuid);
        // Bytes 56..256 reserved (zero).
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
        let mut symbol = [0u8; 4];
        symbol.copy_from_slice(&bytes[0..4]);
        let side = Side::from_u8(bytes[4])?;
        let quantity = u32::from_be_bytes(bytes[8..12].try_into().unwrap());
        let price_cents = f64::from_be_bytes(bytes[16..24].try_into().unwrap());
        let timestamp_ns = u64::from_be_bytes(bytes[24..32].try_into().unwrap());
        let client_order_id = u64::from_be_bytes(bytes[32..40].try_into().unwrap());
        let mut firm_uuid = [0u8; 16];
        firm_uuid.copy_from_slice(&bytes[40..56]);
        Ok(Self {
            symbol,
            side,
            quantity,
            price_cents,
            timestamp_ns,
            client_order_id,
            firm_uuid,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip() {
        let order = EquityTradeOrder::new(
            "AAPL",
            Side::Buy,
            1000,
            19850.0,
            1_716_649_200_000_000_001,
            0xDEADBEEF_CAFEF00D,
            [0x11; 16],
        );
        let bytes = order.encode();
        assert_eq!(bytes.len(), EquityTradeOrder::PAYLOAD_BYTES);
        let decoded = EquityTradeOrder::decode(&bytes).unwrap();
        assert_eq!(order, decoded);
        assert_eq!(decoded.symbol_str(), "AAPL");
    }

    #[test]
    fn size_mismatch_fails() {
        let err = EquityTradeOrder::decode(&[0u8; 100]).unwrap_err();
        assert!(matches!(err, ProtocolError::SchemaMismatch { .. }));
    }

    #[test]
    fn oam_mode_is_plus_one() {
        assert_eq!(EquityTradeOrder::OAM_MODE.charge(), 1);
    }
}
