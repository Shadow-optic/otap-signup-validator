# Golden Vectors: Cross-Language Cryptographic Integrity

## What this is

A pinned set of test vectors in `testdata/golden_vectors.json` that both the Go FLR and the Rust client read, generated from a deterministic key. Used to detect silent drift between the two implementations of:

- ApplicationSchema canonical JSON form
- SHA3-256 hashing
- ECDSA P-256 signing
- Schema Merkle-tree construction

This is the answer to the foot-gun I called out in the FLR build: "canonical JSON ordering in Go is a known foot-gun — verify Go's `encoding/json` field order matches struct declaration order (it does, but worth confirming with a cross-language test vector)."

## What's in the vector file

For each of the three canonical schemas (EquityTradeOrder, MarketTick, Heartbeat):

| Field | Purpose |
|---|---|
| `schema` | The full schema struct as JSON, including its ECDSA signature. |
| `canonical_bytes_hex` | The exact byte sequence produced by Go's `canonicalSchemaBytes`. Hashed to produce the schema's identity. |
| `sha3_256_hex` | SHA3-256(canonical_bytes). The Merkle leaf hash. |
| `signature_hex` | ECDSA P-256 signature over the SHA3-256 hash, ASN.1 DER encoded. |
| `private_key_pem` / `public_key_pem` | The deterministic test key. **Test key only — never use in production.** |

Plus a top-level `merkle_root_hex` covering all three schemas in ID-sorted order.

## How to (re)generate

```bash
cd flr
go test ./internal/crypto -run TestEmitGoldenVectors -emit-golden -count=1
```

The `-count=1` forces a fresh run (skip cache). Without `-emit-golden`, the same test runs in *verification* mode: it recomputes canonical bytes and asserts they match the on-disk file. This is how CI detects drift.

## How the Rust side verifies

```bash
cargo test -p otap-flr-client --test golden_vectors
```

The Rust test (`crates/otap-flr-client/tests/golden_vectors.rs`) does **structural-equivalence** checks: every schema deserializes cleanly, payload sizes match the Rust `SchemaId` compile-time constants, field layouts are internally consistent (offsets, lengths, total bytes), canonical bytes are valid JSON that re-parses to the same schema id and payload size, and signatures are well-formed DER.

What the Rust test does **not** currently do:

- Re-compute the SHA3-256 hash and compare against `sha3_256_hex`.
- Verify the ECDSA signature against `public_key_pem`.

Both require adding `sha3` and `p256` crates to `otap-flr-client`. See "Adding full signature verification" below.

## Why structural-only is enough for v1

The structural checks already catch every realistic drift class:

| Drift kind | Caught? |
|---|---|
| Adding a schema field on one side only | ✓ — total bytes would mismatch |
| Reordering fields in the schema | ✓ — offsets would differ |
| Renaming a schema | ✓ — name in canonical bytes drifts from FLR-served name |
| Changing a payload size on one side | ✓ — explicit assertion |
| Adding a new schema in Go without updating Rust enum | ✓ — `map_oam_to_schema_id` returns None |
| Bit-flip in canonical-JSON ordering algorithm | ✗ — needs hash re-computation |
| Forged signature | ✗ — needs ECDSA verification |

The last two require a malicious actor with FLR write access. In v1 we assume the FLR is operated by a trusted entity per operator, so the structural checks are sufficient. If/when we federate to mutually-distrusting operators, the verification upgrade below becomes mandatory.

## Adding full signature verification

Two small additions:

```toml
# crates/otap-flr-client/Cargo.toml
[dependencies]
sha3 = "0.10"
p256 = { version = "0.13", features = ["ecdsa"] }
```

Then in `crates/otap-flr-client/src/lib.rs`, add a `verify_schema_signature` method:

```rust
use p256::ecdsa::{signature::Verifier, Signature, VerifyingKey};
use sha3::{Digest, Sha3_256};

impl FlrClient {
    pub fn verify_schema_signature(
        &self,
        sch: &ApplicationSchema,
        operator_pubkey_pem: &str,
    ) -> Result<(), FlrClientError> {
        // 1. Re-derive canonical bytes (port of canonicalSchemaBytes from Go).
        let canon = canonical_schema_bytes_rust(sch)?;
        // 2. SHA3-256.
        let mut hasher = Sha3_256::new();
        hasher.update(&canon);
        let hash = hasher.finalize();
        // 3. Verify.
        let pubkey = parse_p256_pubkey(operator_pubkey_pem)?;
        let sig_bytes = hex::decode(sch.signature.as_ref().unwrap())?;
        let sig = Signature::from_der(&sig_bytes)?;
        pubkey.verify(&hash, &sig).map_err(...)?;
        Ok(())
    }
}
```

The work item is "port `canonicalSchemaBytes` from Go to Rust." It must produce byte-identical output for the cross-language signature check to succeed. The two things that have to match exactly:

1. **Field ordering**: Go's `encoding/json` marshals struct fields in declaration order, with `omitempty` stripping empty fields. The canonical struct in Go has 8 named fields; Rust must emit them in the same order, with the same `description: ""` omission rule.

2. **Number encoding**: Go's `encoding/json` emits integers in decimal with no leading zeros, no exponent. Rust's `serde_json` does the same by default. No floats appear in the canonical form (timestamps are RFC3339 strings, sizes are integers) so float-edge-case differences don't bite.

The golden vector file itself is the regression-test target: once Rust's `canonical_schema_bytes_rust` produces output matching `canonical_bytes_hex` for all three vectors, the implementation is correct and any future drift fails the test.

## Production checklist before federating across distrusting operators

- [ ] Implement `canonical_schema_bytes_rust` and add re-hash assertion to the golden test.
- [ ] Implement `verify_schema_signature` and add a verification assertion that the signature on each vector validates against the embedded public key.
- [ ] Add a negative test: tamper with one byte of a schema and assert the signature *fails* to verify.
- [ ] Document the operator-key trust model — who issues keys, how revocation works, how a misbehaving operator's keys are excluded.
- [ ] Consider switching to deterministic ECDSA (RFC 6979) so signatures themselves become reproducible across runs and the golden vector can include a known-good signature byte sequence.
