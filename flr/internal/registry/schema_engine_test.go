package registry

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"path/filepath"
	"testing"

	"github.com/otap/flr/internal/crypto"
	"github.com/otap/flr/internal/models"
)

// schemaTestSetup creates a tmp BadgerStore + Engine for a given operator.
func schemaTestSetup(t *testing.T, operatorID string) (*Engine, *BadgerStore, func()) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "schema-engine")
	store, err := NewBadgerStore(dir)
	if err != nil {
		t.Fatalf("badger store: %v", err)
	}

	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalECPrivateKey(priv)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})

	cryptoEng, err := crypto.NewEngine(operatorID, keyPEM)
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	engine := NewEngine(store, cryptoEng, operatorID)

	cleanup := func() { _ = store.Close() }
	return engine, store, cleanup
}

func mkTestSchema(operatorID string, oam int32, id string) *models.ApplicationSchema {
	return &models.ApplicationSchema{
		ID:           id,
		OamMode:      oam,
		PayloadBytes: 32,
		Name:         "Test",
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
	}
}

func TestRegisterSchema_StoresWithSignature(t *testing.T) {
	eng, _, done := schemaTestSetup(t, "op-alice")
	defer done()

	in := mkTestSchema("op-alice", 1, "")
	out, err := eng.RegisterSchema(in)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if out.ID == "" {
		t.Fatal("expected ID auto-assignment")
	}
	if len(out.Signature) == 0 {
		t.Fatal("expected signature on stored schema")
	}
	if out.Status != models.SchemaStatusActive {
		t.Errorf("expected ACTIVE status, got %s", out.Status)
	}
}

func TestRegisterSchema_RejectsForeignOperator(t *testing.T) {
	eng, _, done := schemaTestSetup(t, "op-alice")
	defer done()

	in := mkTestSchema("op-bob", 1, "")
	if _, err := eng.RegisterSchema(in); err == nil {
		t.Fatal("expected error for foreign-operator schema")
	}
}

func TestGetActiveSchemaForOam(t *testing.T) {
	eng, _, done := schemaTestSetup(t, "op-alice")
	defer done()

	in, _ := eng.RegisterSchema(mkTestSchema("op-alice", 7, ""))

	got, err := eng.GetActiveSchemaForOam("op-alice", 7)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != in.ID {
		t.Errorf("expected ID %s, got %s", in.ID, got.ID)
	}
}

func TestRegisterSchema_RejectsDuplicateActiveOam(t *testing.T) {
	eng, _, done := schemaTestSetup(t, "op-alice")
	defer done()

	if _, err := eng.RegisterSchema(mkTestSchema("op-alice", 3, "schema-a")); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := eng.RegisterSchema(mkTestSchema("op-alice", 3, "schema-b"))
	if err == nil {
		t.Fatal("expected duplicate-active-OAM error")
	}
}

func TestDeprecateSchema_FreesOamSlot(t *testing.T) {
	eng, _, done := schemaTestSetup(t, "op-alice")
	defer done()

	first, _ := eng.RegisterSchema(mkTestSchema("op-alice", 5, "schema-old"))

	if _, err := eng.DeprecateSchema(first.ID); err != nil {
		t.Fatalf("deprecate: %v", err)
	}

	// After deprecate, a new active schema on the same OAM mode must succeed.
	if _, err := eng.RegisterSchema(mkTestSchema("op-alice", 5, "schema-new")); err != nil {
		t.Fatalf("re-register after deprecate: %v", err)
	}
}

func TestListSchemas_FilterByOam(t *testing.T) {
	eng, _, done := schemaTestSetup(t, "op-alice")
	defer done()

	_, _ = eng.RegisterSchema(mkTestSchema("op-alice", 1, "s1"))
	_, _ = eng.RegisterSchema(mkTestSchema("op-alice", 2, "s2"))
	_, _ = eng.RegisterSchema(mkTestSchema("op-alice", 3, "s3"))

	oam := int32(2)
	out, err := eng.ListSchemas(SchemaFilter{OamMode: &oam})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	if out[0].OamMode != 2 {
		t.Errorf("filter leaked")
	}
}

func TestBuildSchemaCommitment_StableAcrossCalls(t *testing.T) {
	eng, _, done := schemaTestSetup(t, "op-alice")
	defer done()

	_, _ = eng.RegisterSchema(mkTestSchema("op-alice", 1, "s1"))
	_, _ = eng.RegisterSchema(mkTestSchema("op-alice", 2, "s2"))

	c1, err := eng.BuildSchemaCommitment()
	if err != nil {
		t.Fatalf("commit 1: %v", err)
	}
	c2, err := eng.BuildSchemaCommitment()
	if err != nil {
		t.Fatalf("commit 2: %v", err)
	}
	// Root hash must be stable (signature varies per call due to ECDSA k).
	if len(c1.RootHash) != 32 || len(c2.RootHash) != 32 {
		t.Fatal("malformed commitment")
	}
	for i := range c1.RootHash {
		if c1.RootHash[i] != c2.RootHash[i] {
			t.Fatalf("root hash differs at byte %d", i)
		}
	}
}
