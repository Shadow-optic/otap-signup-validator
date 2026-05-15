//! HTTP integration test against a live Workers (or Go) FLR.
//!
//! Fetches `/v1/golden_vectors` over HTTP and applies the same structural
//! validation rules as the on-disk golden-vector test
//! (`tests/golden_vectors.rs`). Closes the loop on:
//!
//!   Rust client  →  Workers FLR  →  cross-checked
//!
//! …without requiring a local Go toolchain or a checked-in JSON file.
//!
//! The Workers JSON format differs slightly from the Go-generated file
//! (no per-vector PEM private/public keys; instead a single top-level
//! `public_key_hex` and a `note` field documenting the deterministic
//! ECDSA behavior). We model that shape explicitly here.
//!
//! Skipped unless `OTAP_FLR_URL` is set. Run as:
//!
//! ```bash
//! OTAP_FLR_URL=https://otap-flr-worker.your-subdomain.workers.dev \
//!   cargo test -p otap-flr-client --test golden_vectors_http -- --nocapture
//! ```

use std::time::Duration;

use serde::Deserialize;

use otap_flr_client::{
    csr_handle_for_schema_id, map_oam_to_schema_id, ApplicationSchema,
};

// ---------------------------------------------------------------------------
// Workers response shape
// ---------------------------------------------------------------------------

#[derive(Debug, Deserialize)]
struct WorkerGoldenFile {
    #[allow(dead_code)]
    generated_at: String,
    hash_algo: String,
    sig_algo: String,
    public_key_hex: String,
    vectors: Vec<WorkerVectorEntry>,
    merkle_root_hex: String,
    #[allow(dead_code)]
    #[serde(default)]
    note: Option<String>,
}

#[derive(Debug, Deserialize)]
struct WorkerVectorEntry {
    description: String,
    schema: ApplicationSchema,
    canonical_bytes_hex: String,
    sha3_256_hex: String,
    signature_hex: String,
}

// ---------------------------------------------------------------------------
// Fetching
// ---------------------------------------------------------------------------

/// Fetch the golden-vectors endpoint from the Worker URL in `OTAP_FLR_URL`.
/// Returns `None` if the env var is unset (test skips gracefully).
fn fetch_golden() -> Option<(WorkerGoldenFile, String)> {
    let url = match std::env::var("OTAP_FLR_URL") {
        Ok(u) => u,
        Err(_) => {
            eprintln!(
                "OTAP_FLR_URL not set; skipping. \
                 Set it to a deployed Workers FLR URL to run this test."
            );
            return None;
        }
    };
    let endpoint = format!("{}/v1/golden_vectors", url.trim_end_matches('/'));
    eprintln!("fetching {}", endpoint);

    let client = reqwest::blocking::Client::builder()
        .timeout(Duration::from_secs(10))
        .build()
        .expect("build http client");

    let resp = client
        .get(&endpoint)
        .send()
        .unwrap_or_else(|e| panic!("HTTP error talking to {}: {}", endpoint, e));

    let status = resp.status();
    let body = resp.text().unwrap_or_default();
    assert!(
        status.is_success(),
        "Worker returned {} for {}\nbody: {}",
        status,
        endpoint,
        body
    );

    let parsed: WorkerGoldenFile = serde_json::from_str(&body)
        .unwrap_or_else(|e| panic!("parse worker response: {}\nbody:\n{}", e, body));
    Some((parsed, body))
}

// ---------------------------------------------------------------------------
// Tests (structural-equivalence — same rules as the on-disk version)
// ---------------------------------------------------------------------------

#[test]
fn worker_metadata_is_sane() {
    let Some((g, _)) = fetch_golden() else { return };
    assert_eq!(g.hash_algo, "SHA3-256", "expected SHA3-256 hash algo");
    assert!(
        g.sig_algo.starts_with("ECDSA-P256"),
        "expected ECDSA-P256 sig algo, got {:?}",
        g.sig_algo
    );
    assert!(
        !g.vectors.is_empty(),
        "worker returned zero vectors — is the canonical-schema set empty?"
    );
    // Top-level pubkey should be uncompressed P-256: 0x04 || X(32) || Y(32) = 65 bytes = 130 hex chars.
    assert_eq!(
        g.public_key_hex.len(),
        130,
        "expected uncompressed P-256 pubkey (130 hex chars), got {} chars",
        g.public_key_hex.len()
    );
    assert!(
        g.public_key_hex.starts_with("04"),
        "uncompressed P-256 pubkey must start with 0x04"
    );
    // Merkle root: SHA3-256 = 32 bytes = 64 hex chars.
    assert_eq!(
        g.merkle_root_hex.len(),
        64,
        "merkle root must be 32 bytes (64 hex chars)"
    );
}

#[test]
fn every_vector_has_a_local_schema_mapping() {
    let Some((g, _)) = fetch_golden() else { return };
    for v in &g.vectors {
        let oam = v.schema.oam_mode;
        let mapped = map_oam_to_schema_id(oam).unwrap_or_else(|| {
            panic!(
                "vector {:?} (oam {}) has no local SchemaId mapping — \
                 either the Worker added a schema the Rust enum doesn't know about, \
                 or the Rust enum was updated without redeploying the Worker",
                v.description, oam,
            )
        });
        let handle = csr_handle_for_schema_id(mapped);
        assert!(
            (1..=31).contains(&handle),
            "CSR handle for {:?} out of valid range: {}",
            mapped,
            handle,
        );
    }
}

#[test]
fn payload_bytes_match_local_compile_time_size() {
    let Some((g, _)) = fetch_golden() else { return };
    for v in &g.vectors {
        let Some(id) = map_oam_to_schema_id(v.schema.oam_mode) else {
            continue;
        };
        let local = id.payload_bytes() as i32;
        assert_eq!(
            local, v.schema.payload_bytes,
            "{:?}: Worker says {} bytes, local enum says {}",
            v.description, v.schema.payload_bytes, local,
        );
    }
}

#[test]
fn field_layout_internal_consistency() {
    let Some((g, _)) = fetch_golden() else { return };
    for v in &g.vectors {
        let mut sum_lengths: i32 = 0;
        let mut max_end: i32 = 0;
        for f in &v.schema.layout.fields {
            assert!(
                f.length > 0,
                "{:?} field {:?} has non-positive length {}",
                v.description, f.name, f.length,
            );
            assert!(
                f.offset >= 0,
                "{:?} field {:?} has negative offset {}",
                v.description, f.name, f.offset,
            );
            sum_lengths += f.length;
            let end = f.offset + f.length;
            if end > max_end {
                max_end = end;
            }
        }
        assert_eq!(
            sum_lengths, v.schema.payload_bytes,
            "{:?}: field lengths sum to {} but payload_bytes is {}",
            v.description, sum_lengths, v.schema.payload_bytes,
        );
        assert_eq!(
            max_end, v.schema.payload_bytes,
            "{:?}: max field end {} != payload_bytes {}",
            v.description, max_end, v.schema.payload_bytes,
        );
    }
}

#[test]
fn canonical_bytes_decode_as_valid_json() {
    let Some((g, _)) = fetch_golden() else { return };
    for v in &g.vectors {
        let bytes = hex::decode(&v.canonical_bytes_hex)
            .unwrap_or_else(|e| panic!("hex decode {:?}: {}", v.description, e));
        let parsed: serde_json::Value = serde_json::from_slice(&bytes).unwrap_or_else(|e| {
            panic!(
                "canonical bytes for {:?} are not valid JSON: {}\nbytes: {}",
                v.description, e, v.canonical_bytes_hex
            )
        });
        let canon_id = parsed.get("id").and_then(|x| x.as_str()).unwrap_or("");
        assert_eq!(
            canon_id, v.schema.id,
            "{:?}: canonical id field doesn't match schema id",
            v.description,
        );
        let canon_pb = parsed
            .get("payload_bytes")
            .and_then(|x| x.as_i64())
            .unwrap_or(-1);
        assert_eq!(
            canon_pb as i32, v.schema.payload_bytes,
            "{:?}: canonical payload_bytes mismatch",
            v.description,
        );
    }
}

#[test]
fn sha3_hash_field_is_64_hex_chars() {
    let Some((g, _)) = fetch_golden() else { return };
    for v in &g.vectors {
        assert_eq!(
            v.sha3_256_hex.len(),
            64,
            "{:?}: hash is not 32 bytes (got {} hex chars)",
            v.description,
            v.sha3_256_hex.len(),
        );
        // And valid hex.
        hex::decode(&v.sha3_256_hex)
            .unwrap_or_else(|e| panic!("{:?}: sha3 hex invalid: {}", v.description, e));
    }
}

#[test]
fn vector_signatures_are_well_formed_der() {
    let Some((g, _)) = fetch_golden() else { return };
    for v in &g.vectors {
        let sig = hex::decode(&v.signature_hex)
            .unwrap_or_else(|e| panic!("sig decode {:?}: {}", v.description, e));
        assert!(
            !sig.is_empty() && sig[0] == 0x30,
            "{:?}: signature does not begin with DER SEQUENCE tag (got 0x{:02x})",
            v.description,
            sig.first().copied().unwrap_or(0),
        );
        // ASN.1 DER SEQUENCE: byte 1 is the content length. The total
        // length must equal 2 (tag+len) + content_length. We only check
        // short-form lengths (< 128) since P-256 signatures are always
        // well under that limit (max ~72 bytes).
        let claimed_len = sig[1] as usize;
        assert_eq!(
            sig.len(),
            2 + claimed_len,
            "{:?}: DER length byte ({}) inconsistent with signature size ({})",
            v.description,
            claimed_len,
            sig.len(),
        );
    }
}

// ---------------------------------------------------------------------------
// Determinism check — the killer feature of RFC 6979 signing
// ---------------------------------------------------------------------------

#[test]
fn worker_golden_vectors_are_deterministic_across_calls() {
    // The Worker pins the private key, operator id, and timestamps, and
    // uses RFC 6979 deterministic ECDSA. Two fetches in succession MUST
    // produce byte-identical bodies. If this fails, either the Worker's
    // pinned inputs drifted or @noble/curves's determinism guarantee broke.
    let Some((_, first)) = fetch_golden() else { return };
    let Some((_, second)) = fetch_golden() else { return };

    // The `generated_at` field updates per-request. Strip it before comparing.
    let normalize = |s: &str| -> String {
        let mut v: serde_json::Value = serde_json::from_str(s).expect("normalize parse");
        if let Some(obj) = v.as_object_mut() {
            obj.remove("generated_at");
        }
        serde_json::to_string_pretty(&v).expect("normalize serialize")
    };

    let a = normalize(&first);
    let b = normalize(&second);
    assert_eq!(
        a, b,
        "Worker emitted different golden vectors on consecutive calls — \
         deterministic signing or pinned inputs have drifted"
    );
}
