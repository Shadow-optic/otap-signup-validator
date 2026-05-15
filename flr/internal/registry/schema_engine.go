// Schema operations on the Engine. Mirrors lease-engine patterns:
// the engine wraps the Store with crypto, lifecycle, and Merkle integration.
package registry

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/otap/flr/internal/models"
)

// schemaStore returns the store typed as SchemaStore. Panics if the
// configured store doesn't implement the schema operations — this is a
// programmer error (BadgerStore implements both).
func (e *Engine) schemaStore() SchemaStore {
	ss, ok := e.store.(SchemaStore)
	if !ok {
		panic("registry.Engine: configured Store does not implement SchemaStore")
	}
	return ss
}

// RegisterSchema validates, signs, and stores a new ApplicationSchema.
// The schema's OperatorID must match this engine's operator ID; this is
// the only registration path — operators publish their own schemas.
func (e *Engine) RegisterSchema(s *models.ApplicationSchema) (*models.ApplicationSchema, error) {
	if s == nil {
		return nil, fmt.Errorf("schema cannot be nil")
	}
	if s.OperatorID == "" {
		s.OperatorID = e.operatorID
	}
	if s.OperatorID != e.operatorID {
		return nil, fmt.Errorf("cannot register schema for operator %q (this engine: %q)",
			s.OperatorID, e.operatorID)
	}

	now := time.Now().UTC()
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	s.UpdatedAt = now
	if s.Status == models.SchemaStatusUnspecified {
		s.Status = models.SchemaStatusActive
	}

	// Structural validation.
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("invalid schema: %w", err)
	}

	// Sign over canonical bytes.
	sig, err := e.crypto.SignSchema(s)
	if err != nil {
		return nil, fmt.Errorf("sign schema: %w", err)
	}
	s.Signature = sig

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.schemaStore().CreateSchema(s); err != nil {
		return nil, fmt.Errorf("store schema: %w", err)
	}
	return s, nil
}

// GetSchema fetches a schema by ID.
func (e *Engine) GetSchema(id string) (*models.ApplicationSchema, error) {
	return e.schemaStore().GetSchema(id)
}

// GetActiveSchemaForOam returns the currently-active schema bound to a
// specific (operator, OAM-mode) pair.
func (e *Engine) GetActiveSchemaForOam(operatorID string, oamMode int32) (*models.ApplicationSchema, error) {
	return e.schemaStore().GetActiveSchemaForOam(operatorID, oamMode)
}

// ListSchemas applies a filter and returns matching schemas.
func (e *Engine) ListSchemas(filter SchemaFilter) ([]*models.ApplicationSchema, error) {
	return e.schemaStore().ListSchemas(filter)
}

// DeprecateSchema marks a schema as deprecated. A deprecated schema is still
// valid for decoding existing in-flight Transients but should not be selected
// for new transmissions.
func (e *Engine) DeprecateSchema(id string) (*models.ApplicationSchema, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sch, err := e.schemaStore().GetSchema(id)
	if err != nil {
		return nil, err
	}
	if sch.OperatorID != e.operatorID {
		return nil, fmt.Errorf("cannot deprecate schema owned by operator %q", sch.OperatorID)
	}
	sch.Status = models.SchemaStatusDeprecated
	sch.UpdatedAt = time.Now().UTC()

	// Re-sign because the signed canonical form does not include Status, but
	// regenerate the signature anyway for consistency. (Status changes do not
	// invalidate the original signature; this is defensive only.)
	sig, err := e.crypto.SignSchema(sch)
	if err != nil {
		return nil, fmt.Errorf("re-sign schema: %w", err)
	}
	sch.Signature = sig

	if err := e.schemaStore().UpdateSchema(sch); err != nil {
		return nil, fmt.Errorf("update schema: %w", err)
	}
	return sch, nil
}

// RevokeSchema marks a schema as fully revoked. Receivers should reject
// Transients claiming this schema after revocation propagates.
func (e *Engine) RevokeSchema(id string) (*models.ApplicationSchema, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sch, err := e.schemaStore().GetSchema(id)
	if err != nil {
		return nil, err
	}
	if sch.OperatorID != e.operatorID {
		return nil, fmt.Errorf("cannot revoke schema owned by operator %q", sch.OperatorID)
	}
	sch.Status = models.SchemaStatusRevoked
	sch.UpdatedAt = time.Now().UTC()

	sig, err := e.crypto.SignSchema(sch)
	if err != nil {
		return nil, fmt.Errorf("re-sign schema: %w", err)
	}
	sch.Signature = sig

	if err := e.schemaStore().UpdateSchema(sch); err != nil {
		return nil, fmt.Errorf("update schema: %w", err)
	}
	return sch, nil
}

// VerifySchema verifies signature using the operator's public key.
// Caller looks up the public key separately (typically from the operator
// registry).
func (e *Engine) VerifySchema(s *models.ApplicationSchema, operatorPubKey []byte) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("structural: %w", err)
	}
	return e.crypto.VerifySchemaSignature(s, operatorPubKey)
}

// BuildSchemaCommitment builds a Merkle tree over all ACTIVE schemas owned
// by this operator and returns the root + serialized commitment, mirroring
// the lease commitment pattern.
//
// Note: the v1 design keeps schema commitments separate from lease
// commitments. A future revision may roll them into a unified registry-state
// commitment with subtree roots for leases and schemas.
func (e *Engine) BuildSchemaCommitment() (*models.MerkleCommitment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	schemas, err := e.schemaStore().ListSchemas(SchemaFilter{
		OperatorID: e.operatorID,
		Status:     models.SchemaStatusActive,
	})
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}

	root, err := e.crypto.BuildSchemaMerkleTree(schemas)
	if err != nil {
		return nil, fmt.Errorf("build merkle: %w", err)
	}

	commitment := &models.MerkleCommitment{
		OperatorID:  e.operatorID,
		BlockHeight: e.blockHeight + 1,
		RootHash:    root.Hash,
		Timestamp:   time.Now().UTC(),
		LeaseCount:  int32(len(schemas)), // overloaded here as schema count; v2 will split.
	}

	// Sign the commitment's root over the operator ID + block height + root.
	commitmentBytes := commitmentSignableBytes(commitment)
	sig, err := e.crypto.SignBytes(commitmentBytes)
	if err != nil {
		return nil, fmt.Errorf("sign commitment: %w", err)
	}
	commitment.Signature = sig
	return commitment, nil
}

// commitmentSignableBytes mirrors the lease-commitment signing input.
// Implemented here as a shim so this file doesn't depend on private helpers
// in engine.go that may evolve.
func commitmentSignableBytes(c *models.MerkleCommitment) []byte {
	// Schema-commitment-specific domain separator + canonical fields.
	// We use a fixed prefix so a lease commitment cannot be confused for a
	// schema commitment under signature reuse.
	const domain = "OTAP-SCHEMA-COMMIT-v1\x00"
	buf := make([]byte, 0, len(domain)+len(c.OperatorID)+8+len(c.RootHash))
	buf = append(buf, []byte(domain)...)
	buf = append(buf, []byte(c.OperatorID)...)
	// Big-endian block height for stability across platforms.
	var bh [8]byte
	v := uint64(c.BlockHeight)
	for i := 7; i >= 0; i-- {
		bh[i] = byte(v & 0xFF)
		v >>= 8
	}
	buf = append(buf, bh[:]...)
	buf = append(buf, c.RootHash...)
	return buf
}
