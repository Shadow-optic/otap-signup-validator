// Golden-vector generator. Run with `go test ./internal/crypto -run TestEmitGoldenVectors`
// to (re)generate /home/claude/otap/testdata/golden_vectors.json.
//
// The vector file is then consumed by the Rust side (see
// crates/otap-flr-client/tests/golden_vectors.rs) to verify byte-for-byte
// correspondence between the two language implementations.
//
// Determinism note: ECDSA P-256 signing is *not* deterministic by default
// (uses fresh random k per signature). We pin the private key here but
// document that the signature value will differ across runs. The canonical
// bytes and SHA3-256 hash *are* deterministic and are the primary cross-
// language assertion targets.

package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/otap/flr/internal/models"
)

// goldenVectorPath is relative to the FLR module root.
const goldenVectorPath = "../../../testdata/golden_vectors.json"

// emitGolden controls whether this test writes the output file. When
// false (the default for CI), the test instead verifies that the on-disk
// file matches the freshly-computed canonical bytes and hash.
var emitGolden = flag.Bool("emit-golden", false, "regenerate golden_vectors.json")

// vectorEntry is what we serialize to disk.
type vectorEntry struct {
	Description     string                     `json:"description"`
	Schema          *models.ApplicationSchema  `json:"schema"`
	CanonicalBytes  string                     `json:"canonical_bytes_hex"`
	Sha3Hash        string                     `json:"sha3_256_hex"`
	PrivateKeyPEM   string                     `json:"private_key_pem"`
	PublicKeyPEM    string                     `json:"public_key_pem"`
	SignatureHex    string                     `json:"signature_hex"`
}

type goldenFile struct {
	GeneratedAt  string         `json:"generated_at"`
	GoVersion    string         `json:"go_version,omitempty"`
	HashAlgo     string         `json:"hash_algo"`
	SigAlgo      string         `json:"sig_algo"`
	Vectors      []vectorEntry  `json:"vectors"`
	MerkleRoot   string         `json:"merkle_root_hex"`
}

// goldenPrivateKey returns a deterministic ECDSA P-256 key for golden vectors.
//
// We construct it from a fixed scalar D rather than calling GenerateKey, so
// the public key (and therefore the value of every signed commitment) is
// stable across runs and across hosts.
func goldenPrivateKey() *ecdsa.PrivateKey {
	// Fixed scalar; chosen arbitrarily but kept under the curve order.
	dBytes, _ := hex.DecodeString("4242424242424242424242424242424242424242424242424242424242424242")
	d := new(big.Int).SetBytes(dBytes)
	curve := elliptic.P256()
	// Compute the public key Q = d*G.
	x, y := curve.ScalarBaseMult(dBytes)
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y},
		D:         d,
	}
}

// goldenEngine produces a crypto.Engine wrapping the deterministic key.
func goldenEngine(t *testing.T, operatorID string) *Engine {
	t.Helper()
	priv := goldenPrivateKey()
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	eng, err := NewEngine(operatorID, keyPEM)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	return eng
}

// goldenEquityOrder returns the canonical equity-trade schema used as the
// primary cross-language vector. Note: timestamps are pinned to a fixed
// epoch so canonical bytes are stable.
func goldenEquityOrder() *models.ApplicationSchema {
	return &models.ApplicationSchema{
		ID:           "00000000-0000-4000-8000-000000000001",
		OamMode:      1,
		PayloadBytes: 256,
		Name:         "EquityTradeOrder",
		Version:      1,
		OperatorID:   "op-golden",
		Status:       models.SchemaStatusActive,
		CreatedAt:    time.Unix(1_700_000_000, 0).UTC(),
		Layout: &models.FieldLayout{
			Fields: []models.FieldDef{
				{Name: "symbol", Offset: 0, Length: 4, Type: models.FieldTypeAscii},
				{Name: "side", Offset: 4, Length: 1, Type: models.FieldTypeEnumU8},
				{Name: "_pad0", Offset: 5, Length: 3, Type: models.FieldTypeBytes},
				{Name: "quantity", Offset: 8, Length: 4, Type: models.FieldTypeU32},
				{Name: "_pad1", Offset: 12, Length: 4, Type: models.FieldTypeBytes},
				{Name: "price_cents", Offset: 16, Length: 8, Type: models.FieldTypeF64},
				{Name: "timestamp_ns", Offset: 24, Length: 8, Type: models.FieldTypeU64},
				{Name: "client_order_id", Offset: 32, Length: 8, Type: models.FieldTypeU64},
				{Name: "firm_uuid", Offset: 40, Length: 16, Type: models.FieldTypeBytes},
				{Name: "_reserved", Offset: 56, Length: 200, Type: models.FieldTypeBytes},
			},
		},
	}
}

func goldenMarketTick() *models.ApplicationSchema {
	return &models.ApplicationSchema{
		ID:           "00000000-0000-4000-8000-000000000002",
		OamMode:      3,
		PayloadBytes: 64,
		Name:         "MarketTick",
		Version:      1,
		OperatorID:   "op-golden",
		Status:       models.SchemaStatusActive,
		CreatedAt:    time.Unix(1_700_000_001, 0).UTC(),
		Layout: &models.FieldLayout{
			Fields: []models.FieldDef{
				{Name: "symbol", Offset: 0, Length: 8, Type: models.FieldTypeAscii},
				{Name: "bid_cents", Offset: 8, Length: 8, Type: models.FieldTypeF64},
				{Name: "ask_cents", Offset: 16, Length: 8, Type: models.FieldTypeF64},
				{Name: "last_cents", Offset: 24, Length: 8, Type: models.FieldTypeF64},
				{Name: "volume", Offset: 32, Length: 8, Type: models.FieldTypeU64},
				{Name: "timestamp_ns", Offset: 40, Length: 8, Type: models.FieldTypeU64},
				{Name: "_reserved", Offset: 48, Length: 16, Type: models.FieldTypeBytes},
			},
		},
	}
}

func goldenHeartbeat() *models.ApplicationSchema {
	return &models.ApplicationSchema{
		ID:           "00000000-0000-4000-8000-000000000003",
		OamMode:      4,
		PayloadBytes: 32,
		Name:         "Heartbeat",
		Version:      1,
		OperatorID:   "op-golden",
		Status:       models.SchemaStatusActive,
		CreatedAt:    time.Unix(1_700_000_002, 0).UTC(),
		Layout: &models.FieldLayout{
			Fields: []models.FieldDef{
				{Name: "node_id", Offset: 0, Length: 8, Type: models.FieldTypeU64},
				{Name: "clock_offset_ns", Offset: 8, Length: 8, Type: models.FieldTypeI64},
				{Name: "heartbeat_seq", Offset: 16, Length: 8, Type: models.FieldTypeU64},
				{Name: "_reserved", Offset: 24, Length: 8, Type: models.FieldTypeBytes},
			},
		},
	}
}

// TestEmitGoldenVectors is a dual-purpose test:
//   - Without `-emit-golden`: verifies that the in-tree golden_vectors.json
//     exactly matches what we'd compute today (catches drift).
//   - With `-emit-golden`: regenerates the file on disk.
func TestEmitGoldenVectors(t *testing.T) {
	eng := goldenEngine(t, "op-golden")
	pubKey, err := eng.GetPublicKey()
	if err != nil {
		t.Fatalf("pubkey: %v", err)
	}
	// Re-marshal the private key so we can include it in the vector file.
	priv := goldenPrivateKey()
	privDER, _ := x509.MarshalECPrivateKey(priv)
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})

	schemas := []*models.ApplicationSchema{
		goldenEquityOrder(),
		goldenMarketTick(),
		goldenHeartbeat(),
	}

	var entries []vectorEntry
	for _, sch := range schemas {
		canon, err := canonicalSchemaBytes(sch)
		if err != nil {
			t.Fatalf("canonicalize: %v", err)
		}
		hashBytes := GetSchemaHash(sch)
		sig, err := eng.SignSchema(sch)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		// We attach the signature to the schema so the file is self-contained
		// for the Rust consumer's structural-equivalence test.
		signedCopy := *sch
		signedCopy.Signature = sig

		entries = append(entries, vectorEntry{
			Description:    sch.Name,
			Schema:         &signedCopy,
			CanonicalBytes: hex.EncodeToString(canon),
			Sha3Hash:       hex.EncodeToString(hashBytes),
			PrivateKeyPEM:  string(privPEM),
			PublicKeyPEM:   string(pubKey),
			SignatureHex:   hex.EncodeToString(sig),
		})
	}

	// Merkle tree over the three schemas.
	root, err := eng.BuildSchemaMerkleTree(schemas)
	if err != nil {
		t.Fatalf("merkle: %v", err)
	}

	out := goldenFile{
		GeneratedAt: time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339Nano),
		HashAlgo:    "SHA3-256",
		SigAlgo:     "ECDSA-P256",
		Vectors:     entries,
		MerkleRoot:  hex.EncodeToString(root.Hash),
	}

	jsonBytes, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	abs, err := filepath.Abs(goldenVectorPath)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	if *emitGolden {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, jsonBytes, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Logf("wrote %d bytes to %s", len(jsonBytes), abs)
		return
	}

	// Verification mode: compare against on-disk file. Skip if absent —
	// first-run-on-clean-checkout shouldn't fail; the developer runs
	// -emit-golden once to seed it.
	existing, err := os.ReadFile(abs)
	if os.IsNotExist(err) {
		t.Skipf("golden vector file %s does not exist; run with -emit-golden to create", abs)
	}
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Compare structural fields (skipping signature, which has fresh k each run).
	var existingFile goldenFile
	if err := json.Unmarshal(existing, &existingFile); err != nil {
		t.Fatalf("unmarshal existing: %v", err)
	}
	if len(existingFile.Vectors) != len(entries) {
		t.Fatalf("vector count mismatch: existing=%d new=%d", len(existingFile.Vectors), len(entries))
	}
	for i := range entries {
		if existingFile.Vectors[i].CanonicalBytes != entries[i].CanonicalBytes {
			t.Errorf("vector[%d] canonical bytes drift:\n  existing=%s\n  new     =%s",
				i, existingFile.Vectors[i].CanonicalBytes, entries[i].CanonicalBytes)
		}
		if existingFile.Vectors[i].Sha3Hash != entries[i].Sha3Hash {
			t.Errorf("vector[%d] hash drift:\n  existing=%s\n  new     =%s",
				i, existingFile.Vectors[i].Sha3Hash, entries[i].Sha3Hash)
		}
	}
	if existingFile.MerkleRoot != out.MerkleRoot {
		t.Errorf("merkle root drift:\n  existing=%s\n  new     =%s",
			existingFile.MerkleRoot, out.MerkleRoot)
	}
}
