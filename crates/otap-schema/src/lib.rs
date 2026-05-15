//! # otap-schema
//!
//! Pre-registered application schemas keyed to OAM modes.
//!
//! ## The schema-as-type pattern
//!
//! Every OAM mode maps to a *type* that implements [`Schema`]. The receiver
//! uses the OAM mode (D4) to select the decoder pipeline before the payload
//! is even parsed — replacing Layer 6/7 content-type negotiation with a
//! physical-dimension dispatch.
//!
//! In RTL this becomes a multiplexer driven by the OAM detector output. In
//! Rust we model it as enum dispatch over the [`SchemaId`] / [`AnySchemaValue`]
//! pair. The two representations are kept bit-exact equivalent so the RTL
//! produced from this reference is conformant.
//!
//! ## Adding a new schema
//!
//! 1. Define a struct (e.g., `OptionsOrder`).
//! 2. Implement [`Schema`] for it, choosing an unused OAM mode.
//! 3. Register it in [`SchemaId::resolve`] below.

#![forbid(unsafe_op_in_unsafe_fn)]
#![warn(missing_docs)]

use otap_core::{OamMode, ProtocolError, Result};

pub mod equity_order;
pub mod market_tick;
pub mod heartbeat;

pub use equity_order::EquityTradeOrder;
pub use market_tick::MarketTick;
pub use heartbeat::Heartbeat;

// ============================================================================
// Schema trait
// ============================================================================

/// A pre-registered application schema for an OTAP Transient payload.
///
/// Schemas have a *fixed* payload size — variable-length payloads would
/// reintroduce length-prefix parsing at decode time, defeating the
/// zero-header property.
pub trait Schema: Sized {
    /// The OAM mode this schema is registered against.
    const OAM_MODE: OamMode;

    /// Total payload size in bytes. Fixed at compile time.
    const PAYLOAD_BYTES: usize;

    /// Encode this value into a fixed-size payload buffer.
    fn encode(&self) -> Vec<u8>;

    /// Decode a payload buffer into this value.
    ///
    /// Returns [`ProtocolError::SchemaMismatch`] if the buffer length is wrong.
    fn decode(bytes: &[u8]) -> Result<Self>;
}

// ============================================================================
// Schema registry — for dynamic dispatch by OAM mode
// ============================================================================

/// Identifier for a registered schema, indexed by OAM mode.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SchemaId {
    /// OAM ℓ=+1: equity trade order.
    EquityTradeOrder,
    /// OAM ℓ=+3: market data tick.
    MarketTick,
    /// OAM ℓ=+4: heartbeat / sync.
    Heartbeat,
}

impl SchemaId {
    /// Resolve an OAM mode to a registered schema, if any.
    ///
    /// This is the receive-side dispatch table. The FPGA implements this as
    /// a 32-entry LUT indexed by the OAM detector output.
    pub fn resolve(oam: OamMode) -> Option<Self> {
        match oam.charge() {
            1 => Some(Self::EquityTradeOrder),
            3 => Some(Self::MarketTick),
            4 => Some(Self::Heartbeat),
            _ => None,
        }
    }

    /// Fixed payload size for this schema.
    pub fn payload_bytes(&self) -> usize {
        match self {
            Self::EquityTradeOrder => EquityTradeOrder::PAYLOAD_BYTES,
            Self::MarketTick => MarketTick::PAYLOAD_BYTES,
            Self::Heartbeat => Heartbeat::PAYLOAD_BYTES,
        }
    }
}

/// A decoded schema value of any registered type.
///
/// Returned by the codec when dispatching by OAM mode. Callers downcast to
/// the concrete schema using `if let Some(order) = value.as_equity_order()`.
#[derive(Debug, Clone, PartialEq)]
pub enum AnySchemaValue {
    /// OAM ℓ=+1.
    EquityTradeOrder(EquityTradeOrder),
    /// OAM ℓ=+3.
    MarketTick(MarketTick),
    /// OAM ℓ=+4.
    Heartbeat(Heartbeat),
}

impl AnySchemaValue {
    /// Decode a payload by OAM mode dispatch.
    pub fn decode(oam: OamMode, payload: &[u8]) -> Result<Self> {
        let id = SchemaId::resolve(oam)
            .ok_or_else(|| ProtocolError::UnknownOamMode(oam.charge()))?;
        match id {
            SchemaId::EquityTradeOrder => Ok(Self::EquityTradeOrder(
                EquityTradeOrder::decode(payload)?,
            )),
            SchemaId::MarketTick => Ok(Self::MarketTick(MarketTick::decode(payload)?)),
            SchemaId::Heartbeat => Ok(Self::Heartbeat(Heartbeat::decode(payload)?)),
        }
    }

    /// View as an equity order if that's what this is.
    pub fn as_equity_order(&self) -> Option<&EquityTradeOrder> {
        match self {
            Self::EquityTradeOrder(o) => Some(o),
            _ => None,
        }
    }
}
