// Schema crypto operations. Mirrors the lease/token pattern in engine.go
// but for ApplicationSchema. Canonical JSON ordering is critical: every node
// must produce bit-identical input bytes to reach the same hash.
package crypto

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/otap/flr/internal/models"
	"golang.org/x/crypto/sha3"
)

// canonicalFieldDef is the on-wire canonical form of a FieldDef used for
// hashing. Description is intentionally omitted (it's free-text doc that
// must not affect the cryptographic identity of the schema).
type canonicalFieldDef struct {
	Name   string `json:"name"`
	Offset int32  `json:"offset"`
	Length int32  `json:"length"`
	Type   int32  `json:"type"`
}

// canonicalSchemaFields is the canonical JSON form of an ApplicationSchema
// used as the signature input. Same ordering rules as canonicalLeaseFields:
// fields appear in struct order, which matches alphabetical declaration order
// at the type level.
type canonicalSchemaFields struct {
	ID           string              `json:"id"`
	OamMode      int32               `json:"oam_mode"`
	PayloadBytes int32               `json:"payload_bytes"`
	Name         string              `json:"name"`
	Version      int32               `json:"version"`
	Fields       []canonicalFieldDef `json:"fields"`
	OperatorID   string              `json:"operator_id"`
	CreatedAt    string              `json:"created_at"`
}

// canonicalSchemaBytes produces the deterministic byte sequence that an
// ApplicationSchema's signature covers. Any signer or verifier MUST use
// exactly this function or its bit-for-bit equivalent in another language.
func canonicalSchemaBytes(s *models.ApplicationSchema) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("schema is nil")
	}
	if s.Layout == nil {
		return nil, fmt.Errorf("schema layout is nil")
	}

	// Sort fields by offset for deterministic canonical form. Authors may
	// pass fields in any order; the canonical form is always offset-sorted.
	sorted := make([]models.FieldDef, len(s.Layout.Fields))
	copy(sorted, s.Layout.Fields)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Offset < sorted[j].Offset
	})

	cf := canonicalSchemaFields{
		ID:           s.ID,
		OamMode:      s.OamMode,
		PayloadBytes: s.PayloadBytes,
		Name:         s.Name,
		Version:      s.Version,
		OperatorID:   s.OperatorID,
		CreatedAt:    s.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	cf.Fields = make([]canonicalFieldDef, len(sorted))
	for i, f := range sorted {
		cf.Fields[i] = canonicalFieldDef{
			Name:   f.Name,
			Offset: f.Offset,
			Length: f.Length,
			Type:   int32(f.Type),
		}
	}

	return json.Marshal(cf)
}

// GetSchemaHash returns the SHA3-256 hash of canonicalized schema data.
// Used as the Merkle-tree leaf hash for schema commitments.
func GetSchemaHash(s *models.ApplicationSchema) []byte {
	if s == nil {
		zero := make([]byte, 32)
		return zero
	}
	data, err := canonicalSchemaBytes(s)
	if err != nil {
		zero := make([]byte, 32)
		return zero
	}
	h := sha3.Sum256(data)
	return h[:]
}

// SignSchema computes the canonical hash and produces an ECDSA P-256
// signature over it using the engine's private key. The caller is
// responsible for setting s.Signature on the returned schema, or replacing
// the field with the returned bytes.
func (e *Engine) SignSchema(s *models.ApplicationSchema) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("schema cannot be nil")
	}
	if s.OperatorID != e.operatorID {
		return nil, fmt.Errorf("cannot sign schema owned by operator %q (this engine: %q)",
			s.OperatorID, e.operatorID)
	}

	data, err := canonicalSchemaBytes(s)
	if err != nil {
		return nil, fmt.Errorf("canonicalize schema: %w", err)
	}
	hash := sha3.Sum256(data)

	r, sgn, err := ecdsa.Sign(rand.Reader, e.privateKey, hash[:])
	if err != nil {
		return nil, fmt.Errorf("ECDSA sign failed: %w", err)
	}
	return marshalSignature(r, sgn)
}

// VerifySchemaSignature verifies the schema's signature against an operator's
// public key. Does not verify schema structural integrity; that is the
// schema.Validate() concern.
func (e *Engine) VerifySchemaSignature(s *models.ApplicationSchema, operatorPubKey []byte) error {
	if s == nil {
		return fmt.Errorf("schema cannot be nil")
	}
	if len(s.Signature) == 0 {
		return fmt.Errorf("schema has no signature")
	}
	if len(operatorPubKey) == 0 {
		return fmt.Errorf("operator public key required")
	}

	pubKey, err := parsePublicKey(operatorPubKey)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	data, err := canonicalSchemaBytes(s)
	if err != nil {
		return fmt.Errorf("canonicalize schema: %w", err)
	}
	hash := sha3.Sum256(data)

	r, sgn, err := unmarshalSignature(s.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature format: %w", err)
	}
	if !ecdsa.Verify(pubKey, hash[:], r, sgn) {
		return fmt.Errorf("schema signature verification failed")
	}
	return nil
}

// BuildSchemaMerkleTree constructs a deterministic Merkle tree from a set of
// schemas. Schemas are sorted by ID for determinism (same convention as the
// lease tree in BuildMerkleTree).
//
// Future: this can be merged with the lease tree into a single registry
// commitment with two children (leases-root, schemas-root). For v1 we keep
// them separate to preserve API stability for existing lease-only consumers.
func (e *Engine) BuildSchemaMerkleTree(schemas []*models.ApplicationSchema) (*models.MerkleNode, error) {
	if len(schemas) == 0 {
		zero := make([]byte, 32)
		return &models.MerkleNode{Hash: zero, IsLeaf: true}, nil
	}

	sorted := make([]*models.ApplicationSchema, len(schemas))
	copy(sorted, schemas)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})

	leaves := make([]*models.MerkleNode, len(sorted))
	for i, s := range sorted {
		leaves[i] = &models.MerkleNode{
			Hash:   GetSchemaHash(s),
			IsLeaf: true,
		}
	}

	// Pad to a power of 2 with zero-hash leaves.
	n := 1
	for n < len(leaves) {
		n <<= 1
	}
	for len(leaves) < n {
		zero := make([]byte, 32)
		leaves = append(leaves, &models.MerkleNode{Hash: zero, IsLeaf: true})
	}

	// Fold pairs until one root remains.
	level := leaves
	for len(level) > 1 {
		next := make([]*models.MerkleNode, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			combined := append([]byte{}, level[i].Hash...)
			combined = append(combined, level[i+1].Hash...)
			h := sha3.Sum256(combined)
			next = append(next, &models.MerkleNode{
				Hash:  h[:],
				Left:  level[i],
				Right: level[i+1],
			})
		}
		level = next
	}
	return level[0], nil
}
