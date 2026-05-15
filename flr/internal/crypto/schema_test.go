package crypto

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/otap/flr/internal/models"
)

// helper: generate a fresh ECDSA P-256 key for testing.
func genKeyPEM(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func testEngine(t *testing.T, operatorID string) *Engine {
	t.Helper()
	eng, err := NewEngine(operatorID, genKeyPEM(t))
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	return eng
}

func testSchema(operatorID string) *models.ApplicationSchema {
	return &models.ApplicationSchema{
		ID:           "schema-test-001",
		OamMode:      1,
		PayloadBytes: 32,
		Name:         "TestSchema",
		Version:      1,
		Layout: &models.FieldLayout{
			Fields: []models.FieldDef{
				{Name: "a", Offset: 0, Length: 8, Type: models.FieldTypeU64},
				{Name: "b", Offset: 8, Length: 8, Type: models.FieldTypeU64},
				{Name: "c", Offset: 16, Length: 16, Type: models.FieldTypeBytes},
			},
		},
		OperatorID: operatorID,
		Status:     models.SchemaStatusActive,
		CreatedAt:  time.Unix(1700000000, 0).UTC(),
	}
}

func TestSchemaHash_Deterministic(t *testing.T) {
	s := testSchema("op-alice")
	h1 := GetSchemaHash(s)
	h2 := GetSchemaHash(s)
	if !bytes.Equal(h1, h2) {
		t.Fatalf("hash not deterministic: %x vs %x", h1, h2)
	}
	if len(h1) != 32 {
		t.Errorf("expected 32-byte hash, got %d", len(h1))
	}
}

func TestSchemaHash_OrderIndependent(t *testing.T) {
	// Fields permuted in input — canonical form sorts by offset so hash must match.
	s1 := testSchema("op-alice")
	s2 := testSchema("op-alice")
	s2.Layout.Fields = []models.FieldDef{
		s1.Layout.Fields[2], s1.Layout.Fields[0], s1.Layout.Fields[1],
	}
	h1 := GetSchemaHash(s1)
	h2 := GetSchemaHash(s2)
	if !bytes.Equal(h1, h2) {
		t.Fatalf("hash should be order-invariant, got %x vs %x", h1, h2)
	}
}

func TestSchemaHash_ChangesOnMutation(t *testing.T) {
	s1 := testSchema("op-alice")
	s2 := testSchema("op-alice")
	s2.Layout.Fields[0].Length = 4 // mutate
	s2.Layout.Fields[0].Type = models.FieldTypeU32
	// Adjust subsequent offsets to keep layout valid (not strictly necessary for
	// hash test but keeps the schema sensible).
	s2.Layout.Fields[1].Offset = 4
	s2.Layout.Fields[1].Length = 12
	s2.Layout.Fields[1].Type = models.FieldTypeBytes
	s2.Layout.Fields[2].Offset = 16

	h1 := GetSchemaHash(s1)
	h2 := GetSchemaHash(s2)
	if bytes.Equal(h1, h2) {
		t.Fatal("hash should change after layout mutation")
	}
}

func TestSchemaHash_ChangesOnOperatorID(t *testing.T) {
	s1 := testSchema("op-alice")
	s2 := testSchema("op-bob")
	if bytes.Equal(GetSchemaHash(s1), GetSchemaHash(s2)) {
		t.Fatal("hash should change with operator ID")
	}
}

func TestSchemaHash_DescriptionIgnored(t *testing.T) {
	// Description must not affect the canonical hash — it's free-text doc.
	s1 := testSchema("op-alice")
	s2 := testSchema("op-alice")
	s2.Layout.Fields[0].Description = "this should be ignored"
	if !bytes.Equal(GetSchemaHash(s1), GetSchemaHash(s2)) {
		t.Fatal("description must not affect canonical hash")
	}
}

func TestSignSchema_RoundTrip(t *testing.T) {
	eng := testEngine(t, "op-alice")
	s := testSchema("op-alice")
	sig, err := eng.SignSchema(s)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(sig) == 0 {
		t.Fatal("empty signature")
	}
	s.Signature = sig

	// Verify with this engine's public key.
	pubBytes, err := eng.GetPublicKey()
	if err != nil {
		t.Fatalf("pubkey: %v", err)
	}
	if err := eng.VerifySchemaSignature(s, pubBytes); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSignSchema_RejectsForeignOperator(t *testing.T) {
	eng := testEngine(t, "op-alice")
	s := testSchema("op-bob")
	if _, err := eng.SignSchema(s); err == nil {
		t.Fatal("expected error signing foreign-operator schema")
	}
}

func TestVerifySchemaSignature_DetectsTamper(t *testing.T) {
	eng := testEngine(t, "op-alice")
	s := testSchema("op-alice")
	sig, err := eng.SignSchema(s)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	s.Signature = sig

	// Tamper with a structural field.
	s.PayloadBytes = 64

	pubBytes, _ := eng.GetPublicKey()
	if err := eng.VerifySchemaSignature(s, pubBytes); err == nil {
		t.Fatal("expected verification failure after tamper")
	}
}

func TestVerifySchemaSignature_DetectsWrongKey(t *testing.T) {
	eng1 := testEngine(t, "op-alice")
	eng2 := testEngine(t, "op-alice") // different keypair, same operator id
	s := testSchema("op-alice")
	sig, _ := eng1.SignSchema(s)
	s.Signature = sig
	pubBytes2, _ := eng2.GetPublicKey()
	if err := eng1.VerifySchemaSignature(s, pubBytes2); err == nil {
		t.Fatal("expected verification failure with wrong pubkey")
	}
}

func TestBuildSchemaMerkleTree_EmptyAndSingle(t *testing.T) {
	eng := testEngine(t, "op-alice")
	// Empty → single zero leaf.
	root, err := eng.BuildSchemaMerkleTree(nil)
	if err != nil {
		t.Fatalf("empty tree: %v", err)
	}
	if !root.IsLeaf {
		t.Fatal("empty tree must be a leaf")
	}

	// Single schema.
	root, err = eng.BuildSchemaMerkleTree([]*models.ApplicationSchema{testSchema("op-alice")})
	if err != nil {
		t.Fatalf("single: %v", err)
	}
	if root == nil || len(root.Hash) != 32 {
		t.Fatal("invalid single-schema root")
	}
}

func TestBuildSchemaMerkleTree_Deterministic(t *testing.T) {
	eng := testEngine(t, "op-alice")
	s1 := testSchema("op-alice")
	s2 := testSchema("op-alice")
	s2.ID = "schema-test-002"
	s3 := testSchema("op-alice")
	s3.ID = "schema-test-003"

	// Order A.
	rootA, _ := eng.BuildSchemaMerkleTree([]*models.ApplicationSchema{s1, s2, s3})
	// Order B (permuted).
	rootB, _ := eng.BuildSchemaMerkleTree([]*models.ApplicationSchema{s3, s1, s2})

	if !bytes.Equal(rootA.Hash, rootB.Hash) {
		t.Fatal("schema merkle root must be order-invariant")
	}
}
