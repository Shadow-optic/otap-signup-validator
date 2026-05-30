//! # otap-flr-client
//!
//! Rust client for the Federated Lambda Registry (FLR). Talks to the FLR's
//! REST gateway and produces the configuration the OBG and codec need:
//!
//!  - Active wavelength leases (this OBG's TX channel).
//!  - Active peer endpoints and their wavelengths.
//!  - ApplicationSchema definitions and their (operator, OAM) → schema-id
//!    bindings.
//!
//! This is the bridge that converts FLR state into [`RegisterFile`]
//! contents — the "FPGA bitstream compiler" referenced in the FLR design
//! doc, except it produces CSR writes rather than RTL.
//!
//! ## REST surface consumed
//!
//! - `GET  /v1/schemas?operator_id=<id>&status=ACTIVE`
//! - `GET  /v1/schemas/active/{operator}/{oam}`
//! - `GET  /v1/schemas/{id}`
//! - `GET  /v1/leases?operator_id=<id>&status=ACTIVE`  (via grpc-gateway)
//!
//! ## Modes
//!
//! Two execution modes are supported:
//!
//! 1. **One-shot programming** ([`FlrClient::program_register_file`]) — fetch
//!    current FLR state and produce CSR writes. Used at OBG startup.
//!
//! 2. **Continuous refresh** ([`FlrClient::refresh_loop`]) — run a blocking
//!    loop that polls the FLR every `refresh_interval` and re-programs the
//!    register file when something changes. Used by long-running OBG hosts
//!    to pick up schema deprecations, lease rotations, etc.
//!
//! For the demo, the one-shot path is sufficient.

#![forbid(unsafe_op_in_unsafe_fn)]
#![warn(missing_docs)]

use std::time::Duration;

use serde::{Deserialize, Serialize};
use thiserror::Error;

use otap_core::Wavelength;
use otap_csr::{writer, RegisterFile};
use otap_schema::{Schema, SchemaId};

// ============================================================================
// Errors
// ============================================================================

/// Errors from talking to the FLR.
#[derive(Debug, Error)]
pub enum FlrClientError {
    /// HTTP layer error.
    #[error("HTTP error: {0}")]
    Http(#[from] reqwest::Error),
    /// JSON (de)serialization error.
    #[error("JSON error: {0}")]
    Json(#[from] serde_json::Error),
    /// FLR returned a non-2xx response.
    #[error("FLR error {status}: {body}")]
    BadResponse {
        /// HTTP status code.
        status: u16,
        /// Body content (for diagnostic purposes).
        body: String,
    },
    /// No active schema found for the requested (operator, oam) pair.
    #[error("no active schema for operator {operator}, oam {oam}")]
    NoActiveSchema {
        /// Operator ID.
        operator: String,
        /// OAM mode.
        oam: i32,
    },
    /// Underlying CSR encoding error.
    #[error(transparent)]
    Csr(#[from] otap_csr::CsrError),
    /// Hex-decoding failure (e.g., shared secret stored as hex string).
    #[error("hex decode error: {0}")]
    Hex(#[from] hex::FromHexError),
    /// FLR schema's OAM mode is not in the static SchemaId enum.
    ///
    /// This is a *compatibility check*: the FLR may serve schemas for OAM
    /// modes the local SchemaId enum doesn't know about. Out-of-enum schemas
    /// are skipped, not fatal — only logged via this error variant when
    /// surfaced.
    #[error("FLR schema for OAM {oam} not in local SchemaId enum")]
    UnknownSchema {
        /// The unmapped OAM mode.
        oam: i32,
    },
}

/// Convenience alias.
pub type FlrResult<T> = std::result::Result<T, FlrClientError>;

// ============================================================================
// FLR REST data types (deserialized from JSON)
// ============================================================================

/// Field type enum mirrored from `flr/internal/models/schema.go`.
///
/// The numeric values must stay synchronized with the Go side.
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
#[repr(i32)]
#[allow(missing_docs)]
pub enum FieldType {
    Unspecified = 0,
    U8 = 1, U16 = 2, U32 = 3, U64 = 4,
    I8 = 5, I16 = 6, I32 = 7, I64 = 8,
    F32 = 9, F64 = 10,
    Ascii = 11, Bytes = 12, EnumU8 = 13,
}

/// Field definition deserialized from the FLR.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FieldDef {
    /// Field name.
    pub name: String,
    /// Byte offset within the payload.
    pub offset: i32,
    /// Byte length.
    pub length: i32,
    /// Type code (numeric, matching `FieldType`).
    #[serde(rename = "type")]
    pub type_code: i32,
    /// Optional doc string.
    #[serde(default)]
    pub description: String,
}

/// Field layout deserialized from the FLR.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FieldLayout {
    /// Ordered list of fields.
    pub fields: Vec<FieldDef>,
}

/// Schema status mirrored from `flr/internal/models/schema.go`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[allow(missing_docs)]
pub enum SchemaStatus {
    UNSPECIFIED,
    ACTIVE,
    DEPRECATED,
    REVOKED,
}

/// Schema as served by the FLR REST API.
///
/// This is the on-the-wire JSON shape; we don't bring the otap-schema crate's
/// strongly-typed enum in here because that would create a circular dep with
/// the FLR (which doesn't know our static enum). The mapping from FLR schemas
/// to local `SchemaId` happens in [`map_oam_to_schema_id`].
#[derive(Debug, Clone, Serialize, Deserialize)]
#[allow(missing_docs)]
pub struct ApplicationSchema {
    pub id: String,
    pub oam_mode: i32,
    pub payload_bytes: i32,
    pub name: String,
    #[serde(default)]
    pub version: i32,
    pub layout: FieldLayout,
    pub operator_id: String,
    #[serde(default)]
    pub status: Option<SchemaStatus>,
    pub created_at: String,
    #[serde(default)]
    pub updated_at: String,
    /// Base64-encoded ECDSA signature.
    #[serde(default)]
    pub signature: Option<String>,
}

/// Response wrapper used by `/v1/schemas`.
#[derive(Debug, Deserialize)]
struct ListSchemasResponse {
    #[serde(default)]
    schemas: Vec<ApplicationSchema>,
}

// ============================================================================
// Map FLR schemas to local SchemaId
// ============================================================================

/// Map an OAM mode reported by the FLR to a static [`SchemaId`].
///
/// Returns `None` if the OAM mode is not in the local enum. Out-of-enum
/// schemas are valid — they may belong to a future schema or another
/// operator's namespace — but the local OBG cannot decode them.
pub fn map_oam_to_schema_id(oam: i32) -> Option<SchemaId> {
    if oam == otap_schema::EquityTradeOrder::OAM_MODE.charge() as i32 {
        Some(SchemaId::EquityTradeOrder)
    } else if oam == otap_schema::MarketTick::OAM_MODE.charge() as i32 {
        Some(SchemaId::MarketTick)
    } else if oam == otap_schema::Heartbeat::OAM_MODE.charge() as i32 {
        Some(SchemaId::Heartbeat)
    } else {
        None
    }
}

/// Mapping from `SchemaId` to the registry-local 1-byte handle used in CSR
/// tables. These IDs must match what `arm_register_file` uses on TX and
/// what RX expects. We pick a fixed assignment here.
pub fn csr_handle_for_schema_id(id: SchemaId) -> u8 {
    match id {
        SchemaId::EquityTradeOrder => 1,
        SchemaId::MarketTick => 2,
        SchemaId::Heartbeat => 3,
    }
}

// ============================================================================
// FlrClient
// ============================================================================

/// Configuration for a [`FlrClient`].
#[derive(Debug, Clone)]
pub struct FlrClientConfig {
    /// FLR REST base URL (e.g., `http://localhost:8080`).
    pub base_url: String,
    /// Operator ID whose schemas to load.
    pub operator_id: String,
    /// HTTP request timeout.
    pub request_timeout: Duration,
    /// Refresh interval for `refresh_loop`.
    pub refresh_interval: Duration,
}

impl FlrClientConfig {
    /// Sensible defaults for the demo: localhost:8080, op-alice, 5s timeouts.
    pub fn local(operator_id: impl Into<String>) -> Self {
        Self {
            base_url: "http://localhost:8080".into(),
            operator_id: operator_id.into(),
            request_timeout: Duration::from_secs(5),
            refresh_interval: Duration::from_secs(15),
        }
    }
}

/// Blocking REST client to the FLR.
pub struct FlrClient {
    cfg: FlrClientConfig,
    http: reqwest::blocking::Client,
}

impl FlrClient {
    /// Construct a new client.
    pub fn new(cfg: FlrClientConfig) -> FlrResult<Self> {
        let http = reqwest::blocking::Client::builder()
            .timeout(cfg.request_timeout)
            .build()?;
        Ok(Self { cfg, http })
    }

    /// Fetch all active schemas for the configured operator.
    pub fn list_active_schemas(&self) -> FlrResult<Vec<ApplicationSchema>> {
        let url = format!(
            "{}/v1/schemas?operator_id={}&status=ACTIVE",
            self.cfg.base_url.trim_end_matches('/'),
            urlencode(&self.cfg.operator_id),
        );
        let resp = self.http.get(&url).send()?;
        let status = resp.status();
        let body = resp.text()?;
        if !status.is_success() {
            return Err(FlrClientError::BadResponse {
                status: status.as_u16(),
                body,
            });
        }
        let parsed: ListSchemasResponse = serde_json::from_str(&body)?;
        Ok(parsed.schemas)
    }

    /// Fetch one schema by ID.
    pub fn get_schema(&self, id: &str) -> FlrResult<ApplicationSchema> {
        let url = format!(
            "{}/v1/schemas/{}",
            self.cfg.base_url.trim_end_matches('/'),
            urlencode(id),
        );
        let resp = self.http.get(&url).send()?;
        let status = resp.status();
        let body = resp.text()?;
        if !status.is_success() {
            return Err(FlrClientError::BadResponse {
                status: status.as_u16(),
                body,
            });
        }
        Ok(serde_json::from_str(&body)?)
    }

    /// Fetch the active schema for an (operator, oam) pair.
    pub fn get_active_schema_for_oam(&self, operator: &str, oam: i32) -> FlrResult<ApplicationSchema> {
        let url = format!(
            "{}/v1/schemas/active/{}/{}",
            self.cfg.base_url.trim_end_matches('/'),
            urlencode(operator),
            oam,
        );
        let resp = self.http.get(&url).send()?;
        let status = resp.status();
        let body = resp.text()?;
        if status.as_u16() == 404 {
            return Err(FlrClientError::NoActiveSchema {
                operator: operator.into(),
                oam,
            });
        }
        if !status.is_success() {
            return Err(FlrClientError::BadResponse {
                status: status.as_u16(),
                body,
            });
        }
        Ok(serde_json::from_str(&body)?)
    }

    /// Program a register file with the OBG's wire configuration:
    /// wavelength binding, shared secret bytes, schema tables, OAM bindings.
    ///
    /// The 32-byte session secret is supplied locally; the FLR does *not*
    /// distribute symmetric keys in v1. In production this comes from an
    /// FFA-handshake key-derivation step. For the demo we use a fixed test
    /// key on both endpoints.
    ///
    /// Returns the count of schemas mapped into the register file.
    pub fn program_register_file(
        &self,
        rf: &mut RegisterFile,
        wavelength: Wavelength,
        secret_bytes: &[u8; 32],
        topological_auth: bool,
    ) -> FlrResult<usize> {
        writer::set_wavelength(rf, wavelength)?;
        writer::set_secret(rf, secret_bytes)?;
        let mut ctrl = otap_csr::CTRL_ENABLE_TX | otap_csr::CTRL_ENABLE_RX;
        if topological_auth {
            ctrl |= otap_csr::CTRL_AUTH_TOPOLOGICAL;
        }
        writer::set_ctrl(rf, ctrl)?;
        writer::set_auth_tolerance(rf, 0.01)?;

        let schemas = self.list_active_schemas()?;
        let mut mapped = 0usize;
        for sch in &schemas {
            let Some(local_id) = map_oam_to_schema_id(sch.oam_mode) else {
                continue;
            };
            let handle = csr_handle_for_schema_id(local_id);
            let local_bytes = local_id.payload_bytes();
            if local_bytes != sch.payload_bytes as usize {
                return Err(FlrClientError::BadResponse {
                    status: 0,
                    body: format!(
                        "schema {} (oam {}) FLR says {} bytes but local enum says {}",
                        sch.id, sch.oam_mode, sch.payload_bytes, local_bytes
                    ),
                });
            }
            writer::set_schema_payload(rf, handle, sch.payload_bytes as u16)?;
            let oam_mode = match local_id {
                SchemaId::EquityTradeOrder => otap_schema::EquityTradeOrder::OAM_MODE,
                SchemaId::MarketTick => otap_schema::MarketTick::OAM_MODE,
                SchemaId::Heartbeat => otap_schema::Heartbeat::OAM_MODE,
            };
            writer::set_oam_to_schema(rf, oam_mode, handle)?;
            mapped += 1;
        }
        Ok(mapped)
    }

    /// Run a blocking loop that re-programs the register file every
    /// `cfg.refresh_interval` until `should_stop()` returns true.
    ///
    /// The caller passes `should_stop` (typically reading an `AtomicBool`
    /// shared with the main thread) to terminate the loop. Transient FLR
    /// errors are swallowed so a brief FLR outage doesn't kill the OBG.
    pub fn refresh_loop<F>(
        &self,
        rf: &mut RegisterFile,
        wavelength: Wavelength,
        secret_bytes: &[u8; 32],
        topological_auth: bool,
        mut should_stop: F,
    ) where
        F: FnMut() -> bool,
    {
        loop {
            if should_stop() {
                return;
            }
            let _ = self.program_register_file(rf, wavelength, secret_bytes, topological_auth);
            std::thread::sleep(self.cfg.refresh_interval);
        }
    }
}

// ============================================================================
// Helpers
// ============================================================================

/// Minimal URL component encoder. The FLR operator IDs and schema IDs are
/// alphanumeric + dashes + UUIDs; we don't need full RFC 3986 percent
/// encoding for that subset.
fn urlencode(s: &str) -> String {
    s.chars()
        .map(|c| match c {
            'a'..='z' | 'A'..='Z' | '0'..='9' | '-' | '_' | '.' | '~' => c.to_string(),
            _ => format!("%{:02X}", c as u32),
        })
        .collect()
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn map_oam_known_modes() {
        assert!(map_oam_to_schema_id(1).is_some()); // EquityTradeOrder
        assert!(map_oam_to_schema_id(3).is_some()); // MarketTick
        assert!(map_oam_to_schema_id(4).is_some()); // Heartbeat
    }

    #[test]
    fn test_csr_handle_for_schema_id() {
        assert_eq!(csr_handle_for_schema_id(SchemaId::EquityTradeOrder), 1);
        assert_eq!(csr_handle_for_schema_id(SchemaId::MarketTick), 2);
        assert_eq!(csr_handle_for_schema_id(SchemaId::Heartbeat), 3);
    }

    #[test]
    fn map_oam_unknown_mode() {
        assert!(map_oam_to_schema_id(99).is_none());
    }

    #[test]
    fn urlencode_passes_alnum() {
        assert_eq!(urlencode("op-alice"), "op-alice");
        assert_eq!(urlencode("a/b"), "a%2Fb");
    }

    #[test]
    fn schema_json_round_trip() {
        let json = r#"{
            "id": "schema-001",
            "oam_mode": 1,
            "payload_bytes": 256,
            "name": "EquityTradeOrder",
            "version": 1,
            "layout": {
                "fields": [
                    {"name": "symbol", "offset": 0, "length": 4, "type": 11, "description": "ticker"}
                ]
            },
            "operator_id": "op-alice",
            "status": "ACTIVE",
            "created_at": "2024-01-01T00:00:00Z"
        }"#;
        let sch: ApplicationSchema = serde_json::from_str(json).expect("parse");
        assert_eq!(sch.id, "schema-001");
        assert_eq!(sch.oam_mode, 1);
        assert_eq!(sch.payload_bytes, 256);
        assert_eq!(sch.status, Some(SchemaStatus::ACTIVE));
        assert_eq!(sch.layout.fields.len(), 1);
        assert_eq!(sch.layout.fields[0].type_code, 11);
    }
}
