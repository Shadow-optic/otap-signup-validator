//! Mock-FLR integration test.
//!
//! Spins up a minimal HTTP server in a background thread that serves
//! canned `/v1/schemas` JSON responses, then exercises `FlrClient` against
//! it. This lets us verify the wire protocol (URL shape, JSON format, CSR
//! programming) without requiring a live Go `flrd` process.
//!
//! Implementation: hand-rolled TcpListener that parses the request line,
//! matches on path, and emits a hardcoded JSON body. Sufficient for the
//! happy-path coverage we need. For tests that need more sophisticated
//! request matching (auth, query-param assertions), add `wiremock` as a
//! dev-dep — but the dep cost wasn't worth it for this one case.

use std::io::{BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::Duration;

use otap_core::Wavelength;
use otap_csr::{view::CsrView, RegisterFile};
use otap_flr_client::{FlrClient, FlrClientConfig};
use otap_schema::Schema;

/// Canned schemas response: three schemas matching the canonical OAM modes.
const SCHEMAS_RESPONSE: &str = r#"{
  "schemas": [
    {
      "id": "schema-equity-001",
      "oam_mode": 1,
      "payload_bytes": 256,
      "name": "EquityTradeOrder",
      "version": 1,
      "layout": {
        "fields": [
          {"name": "symbol",          "offset": 0,  "length": 4,   "type": 11},
          {"name": "side",            "offset": 4,  "length": 1,   "type": 13},
          {"name": "_pad0",           "offset": 5,  "length": 3,   "type": 12},
          {"name": "quantity",        "offset": 8,  "length": 4,   "type": 3},
          {"name": "_pad1",           "offset": 12, "length": 4,   "type": 12},
          {"name": "price_cents",     "offset": 16, "length": 8,   "type": 10},
          {"name": "timestamp_ns",    "offset": 24, "length": 8,   "type": 4},
          {"name": "client_order_id", "offset": 32, "length": 8,   "type": 4},
          {"name": "firm_uuid",       "offset": 40, "length": 16,  "type": 12},
          {"name": "_reserved",       "offset": 56, "length": 200, "type": 12}
        ]
      },
      "operator_id": "op-mock",
      "status": "ACTIVE",
      "created_at": "2024-01-01T00:00:00Z"
    },
    {
      "id": "schema-tick-002",
      "oam_mode": 3,
      "payload_bytes": 64,
      "name": "MarketTick",
      "version": 1,
      "layout": {
        "fields": [
          {"name": "symbol",       "offset": 0,  "length": 8,  "type": 11},
          {"name": "bid_cents",    "offset": 8,  "length": 8,  "type": 10},
          {"name": "ask_cents",    "offset": 16, "length": 8,  "type": 10},
          {"name": "last_cents",   "offset": 24, "length": 8,  "type": 10},
          {"name": "volume",       "offset": 32, "length": 8,  "type": 4},
          {"name": "timestamp_ns", "offset": 40, "length": 8,  "type": 4},
          {"name": "_reserved",    "offset": 48, "length": 16, "type": 12}
        ]
      },
      "operator_id": "op-mock",
      "status": "ACTIVE",
      "created_at": "2024-01-01T00:00:00Z"
    },
    {
      "id": "schema-hb-003",
      "oam_mode": 4,
      "payload_bytes": 32,
      "name": "Heartbeat",
      "version": 1,
      "layout": {
        "fields": [
          {"name": "node_id",         "offset": 0,  "length": 8, "type": 4},
          {"name": "clock_offset_ns", "offset": 8,  "length": 8, "type": 8},
          {"name": "heartbeat_seq",   "offset": 16, "length": 8, "type": 4},
          {"name": "_reserved",       "offset": 24, "length": 8, "type": 12}
        ]
      },
      "operator_id": "op-mock",
      "status": "ACTIVE",
      "created_at": "2024-01-01T00:00:00Z"
    }
  ]
}"#;

/// Canned response for the "wrong payload size" test.
const SCHEMAS_RESPONSE_MISMATCHED: &str = r#"{
  "schemas": [
    {
      "id": "schema-equity-bad",
      "oam_mode": 1,
      "payload_bytes": 999,
      "name": "EquityTradeOrder",
      "version": 1,
      "layout": {
        "fields": [
          {"name": "wrong", "offset": 0, "length": 999, "type": 12}
        ]
      },
      "operator_id": "op-mock",
      "status": "ACTIVE",
      "created_at": "2024-01-01T00:00:00Z"
    }
  ]
}"#;

/// A minimal HTTP server that serves a fixed body for any GET request that
/// starts with a registered path prefix. Returns the bound port so the test
/// can construct an FlrClient URL.
struct MockFlr {
    port: u16,
    shutdown: Arc<AtomicBool>,
}

impl MockFlr {
    fn start(body: &'static str) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
        let port = listener.local_addr().unwrap().port();
        listener
            .set_nonblocking(true)
            .expect("set non-blocking on listener");

        let shutdown = Arc::new(AtomicBool::new(false));
        let shutdown_clone = shutdown.clone();

        thread::spawn(move || loop {
            if shutdown_clone.load(Ordering::Relaxed) {
                return;
            }
            match listener.accept() {
                Ok((stream, _)) => {
                    let body = body.to_string();
                    thread::spawn(move || handle_request(stream, &body));
                }
                Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                    thread::sleep(Duration::from_millis(20));
                }
                Err(_) => return,
            }
        });

        Self { port, shutdown }
    }

    fn url(&self) -> String {
        format!("http://127.0.0.1:{}", self.port)
    }
}

impl Drop for MockFlr {
    fn drop(&mut self) {
        self.shutdown.store(true, Ordering::Relaxed);
    }
}

fn handle_request(mut stream: TcpStream, body: &str) {
    // Drain the request — we don't care about headers/method beyond the
    // first line, but the client expects a complete HTTP response so we
    // must read the request fully before writing.
    let _ = stream.set_read_timeout(Some(Duration::from_secs(1)));
    let mut reader = BufReader::new(stream.try_clone().expect("clone stream"));
    let mut request_line = String::new();
    if reader.read_line(&mut request_line).is_err() {
        return;
    }

    // Consume the rest of the headers so the kernel doesn't RST us.
    loop {
        let mut line = String::new();
        if reader.read_line(&mut line).is_err() || line == "\r\n" || line.is_empty() {
            break;
        }
    }
    // Drain any pending body bytes (we never receive a body for GETs but be safe).
    let mut buf = [0u8; 1024];
    let _ = stream.set_read_timeout(Some(Duration::from_millis(50)));
    let _ = stream.read(&mut buf);

    // Decide response by URL path.
    let serve_404 = !(request_line.contains("/v1/schemas"));
    let resp = if serve_404 {
        format!(
            "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
        )
    } else {
        format!(
            "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
            body.len(),
            body,
        )
    };
    let _ = stream.write_all(resp.as_bytes());
    let _ = stream.flush();
}

// ============================================================================
// Tests
// ============================================================================

#[test]
fn lists_schemas_from_mock_flr() {
    let mock = MockFlr::start(SCHEMAS_RESPONSE);
    let cfg = FlrClientConfig {
        base_url: mock.url(),
        operator_id: "op-mock".into(),
        request_timeout: Duration::from_secs(2),
        refresh_interval: Duration::from_secs(60),
    };
    let client = FlrClient::new(cfg).unwrap();
    let schemas = client.list_active_schemas().expect("list ok");
    assert_eq!(schemas.len(), 3, "expected three canonical schemas");
    let oams: Vec<i32> = schemas.iter().map(|s| s.oam_mode).collect();
    assert!(oams.contains(&1));
    assert!(oams.contains(&3));
    assert!(oams.contains(&4));
}

#[test]
fn programs_register_file_from_mock_flr() {
    let mock = MockFlr::start(SCHEMAS_RESPONSE);
    let cfg = FlrClientConfig {
        base_url: mock.url(),
        operator_id: "op-mock".into(),
        request_timeout: Duration::from_secs(2),
        refresh_interval: Duration::from_secs(60),
    };
    let client = FlrClient::new(cfg).unwrap();

    let mut rf = RegisterFile::new();
    let key = [0x77u8; 32];
    let mapped = client
        .program_register_file(&mut rf, Wavelength::new(40).unwrap(), &key, true)
        .expect("program ok");
    assert_eq!(mapped, 3, "all three schemas should map");

    // Verify the CSR contents.
    let view = CsrView::new(&rf);
    assert_eq!(view.wavelength().unwrap().channel(), 40);
    assert!(view.secret_valid().unwrap());

    // Schema-payload table: handle 1 → 256 bytes, 2 → 64, 3 → 32.
    assert_eq!(view.schema_payload_bytes(1).unwrap(), 256);
    assert_eq!(view.schema_payload_bytes(2).unwrap(), 64);
    assert_eq!(view.schema_payload_bytes(3).unwrap(), 32);

    // OAM table: charge 1 → handle 1, charge 3 → handle 2, charge 4 → handle 3.
    let eq_oam = otap_schema::EquityTradeOrder::OAM_MODE;
    let tk_oam = otap_schema::MarketTick::OAM_MODE;
    let hb_oam = otap_schema::Heartbeat::OAM_MODE;
    assert_eq!(view.schema_id_for_oam(eq_oam).unwrap(), 1);
    assert_eq!(view.schema_id_for_oam(tk_oam).unwrap(), 2);
    assert_eq!(view.schema_id_for_oam(hb_oam).unwrap(), 3);
}

#[test]
fn rejects_payload_size_mismatch() {
    let mock = MockFlr::start(SCHEMAS_RESPONSE_MISMATCHED);
    let cfg = FlrClientConfig {
        base_url: mock.url(),
        operator_id: "op-mock".into(),
        request_timeout: Duration::from_secs(2),
        refresh_interval: Duration::from_secs(60),
    };
    let client = FlrClient::new(cfg).unwrap();
    let mut rf = RegisterFile::new();
    let key = [0u8; 32];
    let result = client.program_register_file(&mut rf, Wavelength::new(40).unwrap(), &key, false);
    assert!(
        result.is_err(),
        "expected payload-size mismatch error but program_register_file succeeded"
    );
}
