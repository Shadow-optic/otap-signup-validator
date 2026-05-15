//! # otap-obg
//!
//! Host-side library for the OTAP Bridge Gateway (OBG).
//!
//! Applications interact with OTAP through this crate as if it were an
//! RDMA NIC: post a work request to a send queue, poll completions off a
//! completion queue. Schema dispatch (D4 → schema selection) is
//! pre-registered, so no run-time content-type negotiation happens.
//!
//! ## CSR-driven configuration (revised)
//!
//! All configuration — wavelength binding, shared secret, schema tables,
//! auth mode — is read from a [`RegisterFile`](otap_csr::RegisterFile). This
//! is the same surface the FPGA exposes over PCIe BAR. In software, the
//! register file is an in-memory `[u32; N]`; in hardware, it is mmap'd.
//! `SoftDriver` is agnostic to which.
//!
//! ## TX / RX split
//!
//! Earlier versions of this crate had `tick()` perform an internal loopback.
//! That was useful for one-host demos but didn't model the real two-endpoint
//! topology. The current API exposes:
//!
//! - [`SoftDriver::transmit_pending`] — drain the send queue, encode, return
//!   a `Vec<Transient>` for the caller (typically a `Fabric`) to deliver.
//! - [`SoftDriver::receive`] — accept a Transient (post-channel), decode,
//!   push to the completion queue.
//!
//! A `Fabric` (in the `otap-fabric` crate) wires two `SoftDriver`s together
//! through an `otap_sim::Channel`, modeling the simulated fiber.
//!
//! ## What the FPGA replaces
//!
//! | Software call                  | Hardware equivalent                         |
//! |--------------------------------|---------------------------------------------|
//! | `transmit_pending`             | TX DMA engine + `otap_tx_pipeline` module   |
//! | `receive`                      | Photonic frontend + `otap_rx_pipeline` mod  |
//! | `RegisterFile` reads           | PCIe BAR CSR-decoder reads                  |
//! | `SendQueue.post`               | PCIe DMA descriptor write + doorbell        |
//! | `CompletionQueue.push` (drv)   | FPGA → host DMA write + MSI-X interrupt     |

#![forbid(unsafe_op_in_unsafe_fn)]
#![warn(missing_docs)]

use std::collections::VecDeque;
use thiserror::Error;

use otap_codec::{Decoder, DecoderConfig, Encoder};
use otap_core::{OamMode, Transient, Wavelength};
use otap_crypto::AuthMode;
use otap_csr::{view::CsrView, CsrError, RegisterFile, CTRL_AUTH_TOPOLOGICAL};
use otap_schema::{AnySchemaValue, Schema, SchemaId};

/// MODQ hardware backend (PCIe BAR ring buffer → FPGA DMA → photon).
/// Enable with `cargo build --features modq`.
#[cfg(feature = "modq")]
pub mod modq_backend;

// ============================================================================
// Errors
// ============================================================================

/// Errors arising in the OBG driver layer.
#[derive(Debug, Error)]
pub enum ObgError {
    /// Send queue has no slots available.
    #[error("send queue full")]
    SendQueueFull,
    /// Completion queue had no entry to dequeue.
    #[error("completion queue empty")]
    CompletionQueueEmpty,
    /// Payload size mismatch.
    #[error("payload size mismatch: expected {expected}, got {got}")]
    PayloadSize { expected: usize, got: usize },
    /// Underlying protocol error.
    #[error(transparent)]
    Protocol(#[from] otap_core::ProtocolError),
    /// CSR-related error.
    #[error(transparent)]
    Csr(#[from] CsrError),
    /// Driver not armed (configuration missing).
    #[error("OBG not armed: {0}")]
    NotArmed(&'static str),
}

/// Convenience alias.
pub type ObgResult<T> = std::result::Result<T, ObgError>;

// ============================================================================
// Work request — what an application posts to the send queue
// ============================================================================

/// A descriptor posted by the application to request a Transient transmission.
///
/// In hardware this lives in DMA-pinned memory and is read by the FPGA over
/// PCIe. The byte layout matches what the FPGA expects.
#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct WorkRequest {
    /// Wire-format descriptor version.
    pub version: u8,
    /// Schema (and thus OAM mode) for this transmission.
    pub schema_id: SchemaId,
    /// Destination wavelength channel.
    pub wavelength: Wavelength,
    /// Payload length in bytes — must match the schema.
    pub payload_len: u32,
    /// User-defined cookie echoed back on completion.
    pub cookie: u64,
    /// Flags reserved for future use.
    pub flags: u32,
}

impl WorkRequest {
    /// Construct a new work request.
    pub fn new(schema_id: SchemaId, wavelength: Wavelength, payload_len: u32, cookie: u64) -> Self {
        Self {
            version: 0x01,
            schema_id,
            wavelength,
            payload_len,
            cookie,
            flags: 0,
        }
    }

    /// Validate the descriptor against its schema's payload size.
    pub fn validate(&self) -> ObgResult<()> {
        let expected = self.schema_id.payload_bytes();
        if expected != self.payload_len as usize {
            return Err(ObgError::Protocol(
                otap_core::ProtocolError::SchemaMismatch {
                    oam_mode: 0,
                    expected,
                    actual: self.payload_len as usize,
                },
            ));
        }
        Ok(())
    }
}

// ============================================================================
// Completion
// ============================================================================

/// A single received-Transient completion.
#[derive(Debug, Clone)]
pub struct Completion {
    /// The decoded schema value.
    pub value: AnySchemaValue,
    /// Whether D3 authentication verified.
    pub auth_ok: bool,
    /// Sequence number from D5.
    pub sequence: u16,
    /// User cookie (zero on RX path).
    pub cookie: u64,
}

// ============================================================================
// Queues
// ============================================================================

/// Send queue — work requests posted by the application, drained by the driver.
pub struct SendQueue {
    requests: VecDeque<(WorkRequest, Vec<u8>)>,
    capacity: usize,
}

impl SendQueue {
    /// New queue with the given capacity.
    pub fn new(capacity: usize) -> Self {
        Self {
            requests: VecDeque::with_capacity(capacity),
            capacity,
        }
    }
    /// Number of requests currently queued.
    pub fn len(&self) -> usize { self.requests.len() }
    /// True if no requests are queued.
    pub fn is_empty(&self) -> bool { self.requests.is_empty() }

    /// Post a new request.
    pub fn post(&mut self, wr: WorkRequest, payload: Vec<u8>) -> ObgResult<()> {
        wr.validate()?;
        if self.requests.len() >= self.capacity {
            return Err(ObgError::SendQueueFull);
        }
        self.requests.push_back((wr, payload));
        Ok(())
    }

    fn pop(&mut self) -> Option<(WorkRequest, Vec<u8>)> { self.requests.pop_front() }
}

/// Completion queue — completions written by the driver, polled by the application.
pub struct CompletionQueue {
    completions: VecDeque<Completion>,
    capacity: usize,
}

impl CompletionQueue {
    /// New queue with the given capacity.
    pub fn new(capacity: usize) -> Self {
        Self {
            completions: VecDeque::with_capacity(capacity),
            capacity,
        }
    }
    /// Pop the next completion, if any.
    pub fn poll(&mut self) -> Option<Completion> { self.completions.pop_front() }
    /// Number of pending completions.
    pub fn len(&self) -> usize { self.completions.len() }
    /// True when empty.
    pub fn is_empty(&self) -> bool { self.completions.is_empty() }

    /// Driver-side: push a completion (public so `Fabric` and tests can push).
    pub fn push(&mut self, c: Completion) {
        if self.completions.len() >= self.capacity {
            self.completions.pop_front();
        }
        self.completions.push_back(c);
    }
}

// ============================================================================
// SoftDriver
// ============================================================================

/// Snapshot of configuration latched from the register file at arm-time.
#[derive(Clone)]
struct DriverConfig {
    wavelength: Wavelength,
    #[allow(dead_code)]
    auth_mode: AuthMode,
}

/// A software OBG that consumes work requests, produces Transients,
/// and accepts incoming Transients to produce completions.
pub struct SoftDriver {
    encoder: Encoder,
    decoder: Decoder,
    config: DriverConfig,
}

impl SoftDriver {
    /// Build a driver from the current contents of a register file.
    ///
    /// The register file must be armed: secret must be loaded, wavelength
    /// must be set. Returns [`ObgError::NotArmed`] otherwise.
    pub fn from_csr(rf: &RegisterFile) -> ObgResult<Self> {
        let v = CsrView::new(rf);

        let wavelength = v.wavelength()?;
        let secret = v
            .shared_secret()?
            .ok_or(ObgError::NotArmed("shared secret not loaded"))?;
        let ctrl = v.ctrl()?;
        let tolerance = v.auth_tolerance()?;

        let auth_mode = if ctrl & CTRL_AUTH_TOPOLOGICAL != 0 {
            AuthMode::Topological
        } else {
            AuthMode::TrajectoryMatch {
                tolerance_rad: tolerance.max(1e-4),
            }
        };

        let encoder = Encoder::new(secret.clone(), wavelength);
        let decoder = Decoder::new(DecoderConfig {
            secret,
            expected_wavelength: wavelength,
            auth_mode,
        });

        Ok(Self {
            encoder,
            decoder,
            config: DriverConfig { wavelength, auth_mode },
        })
    }

    /// Re-read the register file and reinitialize. Used after a CSR write.
    pub fn refresh_from_csr(&mut self, rf: &RegisterFile) -> ObgResult<()> {
        *self = Self::from_csr(rf)?;
        Ok(())
    }

    /// The wavelength this driver is bound to.
    pub fn wavelength(&self) -> Wavelength {
        self.config.wavelength
    }

    /// Drain the send queue and produce Transients.
    ///
    /// Returns a `Vec` of `(Transient, cookie)` pairs. Callers (typically a
    /// `Fabric`) deliver the Transients to the peer; the cookie is preserved
    /// for tracking purposes.
    pub fn transmit_pending(&mut self, send: &mut SendQueue) -> Vec<(Transient, u64)> {
        let mut out = Vec::new();
        while let Some((wr, payload)) = send.pop() {
            // Resolve the OAM mode for this schema. In hardware this is a
            // CSR-table lookup; here it's a static dispatch because the
            // schema set is closed at compile time.
            let oam = oam_for_schema(wr.schema_id);
            let transient = self.encoder.encode_raw(oam, payload);
            out.push((transient, wr.cookie));
        }
        out
    }

    /// Accept an incoming Transient (post-channel) and push a completion.
    pub fn receive(&self, t: &Transient, cq: &mut CompletionQueue) {
        match self.decoder.decode(t) {
            Ok(report) => cq.push(Completion {
                value: report.value,
                auth_ok: report.auth_ok,
                sequence: report.micro.sequence,
                cookie: 0,
            }),
            Err(_) => {
                // Drop malformed Transients silently; the real OBG would
                // increment a diagnostic counter visible in the STATUS reg.
            }
        }
    }
}

/// Static schema → OAM-mode resolver. Mirrored in CSR's OAM table when used.
fn oam_for_schema(id: SchemaId) -> OamMode {
    match id {
        SchemaId::EquityTradeOrder => otap_schema::EquityTradeOrder::OAM_MODE,
        SchemaId::MarketTick => otap_schema::MarketTick::OAM_MODE,
        SchemaId::Heartbeat => otap_schema::Heartbeat::OAM_MODE,
    }
}

/// Helper that arms a register file with a basic configuration.
///
/// This is a convenience for tests and the local demo. Production callers
/// build the register file from FLR-supplied configuration via the
/// `otap-flr-client` crate.
pub fn arm_register_file(
    rf: &mut RegisterFile,
    wavelength: Wavelength,
    secret_bytes: &[u8; 32],
    topological_auth: bool,
) -> ObgResult<()> {
    otap_csr::writer::set_wavelength(rf, wavelength)?;
    otap_csr::writer::set_secret(rf, secret_bytes)?;
    let ctrl = otap_csr::CTRL_ENABLE_TX
        | otap_csr::CTRL_ENABLE_RX
        | if topological_auth { CTRL_AUTH_TOPOLOGICAL } else { 0 };
    otap_csr::writer::set_ctrl(rf, ctrl)?;
    otap_csr::writer::set_auth_tolerance(rf, 0.01)?;

    // Pre-register the canonical three schemas with arbitrary IDs that match
    // what `flr-seed` registers in the FLR. Schema IDs must agree between
    // TX and RX endpoints; we choose 1, 2, 3.
    otap_csr::writer::set_schema_payload(
        rf,
        1,
        otap_schema::EquityTradeOrder::PAYLOAD_BYTES as u16,
    )?;
    otap_csr::writer::set_schema_payload(
        rf,
        2,
        otap_schema::MarketTick::PAYLOAD_BYTES as u16,
    )?;
    otap_csr::writer::set_schema_payload(
        rf,
        3,
        otap_schema::Heartbeat::PAYLOAD_BYTES as u16,
    )?;
    otap_csr::writer::set_oam_to_schema(rf, otap_schema::EquityTradeOrder::OAM_MODE, 1)?;
    otap_csr::writer::set_oam_to_schema(rf, otap_schema::MarketTick::OAM_MODE, 2)?;
    otap_csr::writer::set_oam_to_schema(rf, otap_schema::Heartbeat::OAM_MODE, 3)?;
    Ok(())
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use otap_schema::{equity_order::Side, EquityTradeOrder};

    fn armed_rf(wavelength: Wavelength) -> RegisterFile {
        let mut rf = RegisterFile::new();
        let key = [0x42u8; 32];
        arm_register_file(&mut rf, wavelength, &key, false).unwrap();
        rf
    }

    #[test]
    fn driver_arms_from_csr() {
        let rf = armed_rf(Wavelength::new(40).unwrap());
        let driver = SoftDriver::from_csr(&rf).expect("must arm");
        assert_eq!(driver.wavelength().channel(), 40);
    }

    #[test]
    fn unarmed_driver_rejects() {
        let mut rf = RegisterFile::new();
        // Wavelength is in range (channel 0) but secret is not loaded.
        otap_csr::writer::set_wavelength(&mut rf, Wavelength::new(40).unwrap()).unwrap();
        let err = SoftDriver::from_csr(&rf).unwrap_err();
        assert!(matches!(err, ObgError::NotArmed(_)));
    }

    #[test]
    fn round_trip_clean_channel() {
        // Two drivers, manual loopback (no fabric yet).
        let wavelength = Wavelength::new(40).unwrap();
        let rf = armed_rf(wavelength);
        let mut tx_driver = SoftDriver::from_csr(&rf).unwrap();
        let rx_driver = SoftDriver::from_csr(&rf).unwrap();

        let mut sq = SendQueue::new(8);
        let mut cq = CompletionQueue::new(8);

        let order = EquityTradeOrder::new("AAPL", Side::Buy, 1000, 19850.0, 0, 1, [0u8; 16]);
        let payload = order.encode();
        sq.post(
            WorkRequest::new(
                SchemaId::EquityTradeOrder,
                wavelength,
                payload.len() as u32,
                0xABCD,
            ),
            payload,
        )
        .unwrap();

        for (t, _cookie) in tx_driver.transmit_pending(&mut sq) {
            rx_driver.receive(&t, &mut cq);
        }
        let completion = cq.poll().expect("should have completion");
        assert!(completion.auth_ok);
        assert_eq!(completion.value.as_equity_order().unwrap(), &order);
    }
}
