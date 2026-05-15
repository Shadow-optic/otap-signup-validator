//! Unified transport abstraction over software and MODQ hardware paths.
//!
//! `OtapEndpoint` lets applications submit schema payloads and poll completions
//! through the same interface regardless of transport:
//!
//! - **Software**: `SoftDriver` encodes in Rust, `Fabric` delivers via simulated fiber
//! - **Hardware**: `ModqQueue` copies to PCIe BAR, FPGA DMA reads + encodes
//!
//! ```text
//! Application
//!     │
//!     ▼
//! OtapEndpoint::submit(schema_id, payload)
//!     │
//!     ├── Software: tracks SID only (caller drives SoftDriver externally)
//!     │
//!     └── Hardware: ModqQueue::submit → PCIe BAR → FPGA → photon
//! ```

use otap_schema::SchemaId;
use otap_schema::Schema; // for EquityTradeOrder::PAYLOAD_BYTES

/// A completion returned by either transport path.
#[derive(Debug, Clone)]
pub struct EndpointCompletion {
    /// Sequence ID assigned at submission time.
    pub sequence_id: u32,
    /// Schema that was submitted.
    pub schema_id: SchemaId,
}

/// Errors from endpoint operations.
#[derive(Debug, thiserror::Error)]
pub enum EndpointError {
    #[error("payload too large: {got} bytes, max {max}")]
    PayloadTooLarge { got: usize, max: usize },

    #[error("ring timeout: FPGA not consuming slots")]
    RingTimeout,

    #[error("software driver error: {0}")]
    SoftDriver(String),

    #[error("MODQ error: {0}")]
    Modq(String),
}

/// Configuration for opening an endpoint.
pub enum EndpointConfig {
    /// Pure-software path (demo/testing, no FPGA).
    Software,

    /// Hardware path via MODQ ring → FPGA PCIe BAR.
    Hardware {
        /// MODQ queue configuration.
        modq_config: otap_modq::ModqConfig,
    },
}

/// Unified transport endpoint.
pub enum OtapEndpoint {
    /// Software path — SID tracking only; the caller drives
    /// SoftDriver::transmit_pending externally via Fabric.
    Software {
        next_sid: u32,
    },
    /// Hardware path — submits directly to FPGA via MODQ ring.
    Hardware {
        queue: otap_modq::ModqQueue,
    },
}

impl OtapEndpoint {
    /// Open an endpoint with the given configuration.
    pub fn open(config: EndpointConfig) -> Result<Self, EndpointError> {
        match config {
            EndpointConfig::Software => {
                Ok(OtapEndpoint::Software { next_sid: 0 })
            }
            EndpointConfig::Hardware { modq_config } => {
                let queue = otap_modq::ModqQueue::open(&modq_config)
                    .map_err(|e| EndpointError::Modq(e.to_string()))?;
                Ok(OtapEndpoint::Hardware { queue })
            }
        }
    }

    /// Submit a payload for transmission. Returns the assigned SID.
    ///
    /// - **Software**: assigns a SID for tracking. The actual encode happens
    ///   when the caller drives `SoftDriver::transmit_pending`.
    /// - **Hardware**: copies payload into a BAR-mapped ring slot and rings
    ///   the FPGA doorbell. The FPGA handles encoding.
    pub fn submit(&mut self, _schema_id: SchemaId, payload: &[u8]) -> Result<u32, EndpointError> {
        match self {
            OtapEndpoint::Software { next_sid } => {
                let sid = *next_sid;
                *next_sid = next_sid.wrapping_add(1);
                Ok(sid)
            }
            OtapEndpoint::Hardware { queue } => {
                queue.submit(payload).map_err(|e| match e {
                    otap_modq::ring::RingError::PayloadTooLarge { got, max } => {
                        EndpointError::PayloadTooLarge { got, max }
                    }
                    otap_modq::ring::RingError::Timeout { .. } => {
                        EndpointError::RingTimeout
                    }
                })
            }
        }
    }

    /// Poll for the next completion.
    ///
    /// - **Software**: returns `None` (completions come through the Fabric).
    /// - **Hardware**: polls the MODQ completion ring.
    pub fn poll_completion(&mut self) -> Option<EndpointCompletion> {
        match self {
            OtapEndpoint::Software { .. } => None,
            OtapEndpoint::Hardware { queue } => {
                queue.poll().map(|c| EndpointCompletion {
                    sequence_id: c.sequence_id,
                    schema_id: SchemaId::EquityTradeOrder, // default until FPGA reports schema
                })
            }
        }
    }

    /// Maximum payload bytes per submission.
    pub fn max_payload(&self) -> usize {
        match self {
            OtapEndpoint::Software { .. } => {
                otap_schema::EquityTradeOrder::PAYLOAD_BYTES
            }
            OtapEndpoint::Hardware { queue } => {
                queue.slot_payload_capacity()
            }
        }
    }

    /// Whether this endpoint is backed by real FPGA hardware.
    pub fn is_hardware(&self) -> bool {
        matches!(self, OtapEndpoint::Hardware { .. })
    }
}
