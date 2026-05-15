//! # otap-codec
//!
//! Encoder and decoder for OTAP Transients.
//!
//! ## Encode pipeline (transmitter)
//!
//! ```text
//!  application data
//!         │
//!         ▼
//!   [schema encode]   ──► payload bytes
//!         │
//!         ├─ wavelength (from registry)         ──┐
//!         ├─ OAM mode (from schema)              ──┤
//!         ├─ sequence (from session state)       ──┼──► Transient
//!         └─ polarization (HMAC-derived)          ──┘
//! ```
//!
//! ## Decode pipeline (receiver)
//!
//! ```text
//!     ┌───────────── Transient ─────────────┐
//!     ▼              ▼              ▼        ▼              ▼
//!  λ-check       OAM-dispatch    Pol-verify  μT-sequence   Amp-decode
//!     │              │              │           │              │
//!     └──────────────┴──────────────┴───────────┴──────────────┘
//!                                ▼
//!                       AnySchemaValue
//! ```
//!
//! The four right-hand operations are *independent*: none takes another's
//! output as input. In the FPGA they execute in the same clock cycle. Here
//! they are arranged as a struct so the parallelism is structurally visible
//! and the eventual RTL maps line-for-line.

#![forbid(unsafe_op_in_unsafe_fn)]
#![warn(missing_docs)]

use otap_core::{
    MicroStructure, OamMode, ProtocolError, Result, Transient, Wavelength,
};
use otap_crypto::{
    derive_trajectory, verify_trajectory, AuthMode, SharedSecret, TrajectoryContext,
};
use otap_schema::{AnySchemaValue, Schema};

// ============================================================================
// Encoder — transmit side
// ============================================================================

/// Encoder for an OTAP transmit session.
///
/// Holds the shared secret and the auto-incrementing sequence counter.
/// One [`Encoder`] instance per (destination, OAM mode) pair, since the
/// receiver demuxes on (destination, OAM) and expects monotonic sequences.
pub struct Encoder {
    secret: SharedSecret,
    next_sequence: u16,
    wavelength: Wavelength,
}

impl Encoder {
    /// Construct an encoder bound to a destination wavelength.
    pub fn new(secret: SharedSecret, wavelength: Wavelength) -> Self {
        Self {
            secret,
            next_sequence: 0,
            wavelength,
        }
    }

    /// Encode an application value into a Transient ready for the optical line.
    ///
    /// Returns the Transient *and* increments the internal sequence counter.
    /// If the caller needs the sequence number (e.g., for SNAK tracking), it
    /// is available on the returned Transient.
    pub fn encode<S: Schema>(&mut self, value: &S) -> Transient {
        let payload = value.encode();
        debug_assert_eq!(payload.len(), S::PAYLOAD_BYTES);
        self.encode_raw(S::OAM_MODE, payload)
    }

    /// Encode a pre-serialized payload at a given OAM mode.
    ///
    /// This is the path used by the OBG, where the application has already
    /// serialized the schema into bytes (typically into DMA-pinned memory
    /// directly addressed by the FPGA). The caller is responsible for
    /// ensuring the payload bytes match the schema for `oam`.
    pub fn encode_raw(&mut self, oam: OamMode, payload: Vec<u8>) -> Transient {
        let sequence = self.next_sequence;
        self.next_sequence = self.next_sequence.wrapping_add(1);

        let micro = MicroStructure::new(sequence);

        let trajectory = derive_trajectory(
            &self.secret,
            &TrajectoryContext {
                wavelength: self.wavelength,
                oam,
                sequence,
                payload: &payload,
            },
        );

        Transient::new(self.wavelength, trajectory, oam, micro, payload)
    }
}

// ============================================================================
// Decoder — receive side
// ============================================================================

/// Configuration for a receive session.
pub struct DecoderConfig {
    /// Pre-shared secret with the expected source.
    ///
    /// In production this comes from a peer registry; for the reference model
    /// it is loaded directly.
    pub secret: SharedSecret,

    /// Wavelength this decoder is bound to.
    ///
    /// Cross-wavelength delivery is impossible on a passively-routed network
    /// (the AWG physically separates wavelengths to different fibers). The
    /// check below catches misconfiguration, not attacks.
    pub expected_wavelength: Wavelength,

    /// Authentication mode (trajectory-match or topological).
    pub auth_mode: AuthMode,
}

/// Result of decoding a single Transient.
///
/// Each field is computed *in parallel* on the FPGA. In the reference model
/// they are populated by the [`Decoder::decode`] function; the struct exists
/// to make the parallel structure explicit.
#[derive(Debug)]
pub struct DecodeReport {
    /// The schema-typed value (D1 + D4 path).
    pub value: AnySchemaValue,
    /// The destination wavelength as received (D2 path, redundant check).
    pub wavelength: Wavelength,
    /// OAM mode that was dispatched on (D4).
    pub oam: OamMode,
    /// Sequence number and flow state (D5).
    pub micro: MicroStructure,
    /// Authentication result (D3).
    pub auth_ok: bool,
}

/// Decoder for an OTAP receive session.
pub struct Decoder {
    config: DecoderConfig,
}

impl Decoder {
    /// Construct a decoder.
    pub fn new(config: DecoderConfig) -> Self {
        Self { config }
    }

    /// Decode a Transient.
    ///
    /// In RTL each of the four checks below is a parallel combinational path
    /// terminating in the same clock edge. Here they are sequential statements
    /// for readability, but they have *no data dependencies* among one
    /// another — the same set of inputs feeds all four. The schema dispatch
    /// (D4 → schema selection → D1 decode) is the longest path; the FPGA
    /// implements it as a multiplexer driven by the OAM detector output.
    pub fn decode(&self, t: &Transient) -> Result<DecodeReport> {
        // === D2 check ===
        // On real fiber the AWG enforces this physically; software check is
        // for misconfiguration only.
        if t.wavelength != self.config.expected_wavelength {
            return Err(ProtocolError::WireFormat(
                "wavelength does not match decoder binding",
            ));
        }
        let wavelength = t.wavelength;

        // === D5 read ===
        // Sequence is reported up to the session layer for SNAK tracking; the
        // decoder itself does not gate on it.
        let micro = t.micro;

        // === D4 dispatch ===
        // Select schema pipeline from OAM mode. This is a constant-time table
        // lookup; in RTL, a 32-entry mux driven by the OAM detector.
        let oam = t.oam;

        // === D3 verify ===
        // Trajectory check binds authentication and integrity simultaneously.
        let auth_ok = verify_trajectory(
            &self.config.secret,
            &TrajectoryContext {
                wavelength,
                oam,
                sequence: micro.sequence,
                payload: &t.payload,
            },
            &t.polarization,
            self.config.auth_mode,
        ).is_ok();

        // === D1 decode (dispatched via D4 result) ===
        let value = AnySchemaValue::decode(oam, &t.payload)?;

        Ok(DecodeReport {
            value,
            wavelength,
            oam,
            micro,
            auth_ok,
        })
    }
}

// ============================================================================
// Tests — end-to-end round trips
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use otap_schema::{equity_order::Side, EquityTradeOrder, Heartbeat, MarketTick};

    fn wavelength() -> Wavelength {
        Wavelength::new(40).unwrap()
    }

    fn shared_secret() -> SharedSecret {
        SharedSecret::test_from_label("alice-bob")
    }

    fn decoder() -> Decoder {
        Decoder::new(DecoderConfig {
            secret: shared_secret(),
            expected_wavelength: wavelength(),
            auth_mode: AuthMode::TrajectoryMatch { tolerance_rad: 0.001 },
        })
    }

    #[test]
    fn round_trip_equity_order() {
        let mut enc = Encoder::new(shared_secret(), wavelength());
        let original = EquityTradeOrder::new(
            "AAPL",
            Side::Buy,
            1000,
            19850.0,
            1_716_649_200_000_000_001,
            0xDEADBEEF_CAFEF00D,
            [0x11; 16],
        );
        let transient = enc.encode(&original);

        let report = decoder().decode(&transient).unwrap();
        assert!(report.auth_ok, "auth should verify on clean channel");
        let decoded = report.value.as_equity_order().expect("must be an order");
        assert_eq!(decoded, &original);
        assert_eq!(report.oam.charge(), 1);
        assert_eq!(report.micro.sequence, 0);
    }

    #[test]
    fn sequence_increments_per_transient() {
        let mut enc = Encoder::new(shared_secret(), wavelength());
        let order = EquityTradeOrder::new(
            "MSFT", Side::Sell, 500, 41000.0, 0, 1, [0u8; 16],
        );
        let t0 = enc.encode(&order);
        let t1 = enc.encode(&order);
        let t2 = enc.encode(&order);
        assert_eq!(t0.micro.sequence, 0);
        assert_eq!(t1.micro.sequence, 1);
        assert_eq!(t2.micro.sequence, 2);
    }

    #[test]
    fn payload_tampering_detected() {
        let mut enc = Encoder::new(shared_secret(), wavelength());
        let order = EquityTradeOrder::new(
            "AAPL", Side::Buy, 100, 19850.0, 0, 1, [0u8; 16],
        );
        let mut transient = enc.encode(&order);

        // Adversary modifies a quantity byte without knowing the secret.
        transient.payload[8] ^= 0xFF;

        let report = decoder().decode(&transient).unwrap();
        assert!(!report.auth_ok, "tampered payload must fail auth");
    }

    #[test]
    fn different_oam_modes_dispatch_correctly() {
        let mut enc_order = Encoder::new(shared_secret(), wavelength());
        let mut enc_tick = Encoder::new(shared_secret(), wavelength());
        let mut enc_hb = Encoder::new(shared_secret(), wavelength());

        let order = EquityTradeOrder::new(
            "NVDA", Side::Buy, 50, 90000.0, 0, 1, [0u8; 16],
        );
        let tick = MarketTick {
            symbol: *b"NVDA\0\0\0\0",
            bid_cents: 89950.0,
            ask_cents: 90050.0,
            last_cents: 90000.0,
            volume: 1_000_000,
            timestamp_ns: 0,
            _reserved: [0u8; 16],
        };
        let hb = Heartbeat {
            node_id: 0xABCDEF,
            clock_offset_ns: -123,
            heartbeat_seq: 42,
            _reserved: [0u8; 8],
        };

        let dec = decoder();
        assert!(matches!(
            dec.decode(&enc_order.encode(&order)).unwrap().value,
            AnySchemaValue::EquityTradeOrder(_)
        ));
        assert!(matches!(
            dec.decode(&enc_tick.encode(&tick)).unwrap().value,
            AnySchemaValue::MarketTick(_)
        ));
        assert!(matches!(
            dec.decode(&enc_hb.encode(&hb)).unwrap().value,
            AnySchemaValue::Heartbeat(_)
        ));
    }
}
