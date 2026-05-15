// Package registry provides the core registry engine for the FLR system,
// managing lease lifecycle, storage, queries, and Merkle commitment tracking.
package registry

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/otap/flr/internal/crypto"
	"github.com/otap/flr/internal/models"
	"golang.org/x/crypto/sha3"
)

// Engine manages the local registry state for an FLR operator.
// It coordinates between the Store and the crypto Engine to provide
// a complete registry management system.
type Engine struct {
	store       Store
	crypto      *crypto.Engine
	operatorID  string
	blockHeight int64
	mu          sync.RWMutex
}

// NewEngine creates a new registry engine.
func NewEngine(store Store, cryptoEngine *crypto.Engine, operatorID string) *Engine {
	return &Engine{
		store:       store,
		crypto:      cryptoEngine,
		operatorID:  operatorID,
		blockHeight: 0,
	}
}

// AllocateLease creates a new wavelength lease with a signed token.
func (e *Engine) AllocateLease(wavelength *models.Wavelength, endpointID string, duration time.Duration) (*models.Lease, *models.LeaseToken, error) {
	if wavelength == nil {
		return nil, nil, fmt.Errorf("wavelength cannot be nil")
	}
	if endpointID == "" {
		return nil, nil, fmt.Errorf("endpoint ID cannot be empty")
	}
	if duration <= 0 {
		return nil, nil, fmt.Errorf("duration must be positive")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().UTC()

	// Check for conflicts
	conflict, found := e.checkConflictLocked(wavelength, "")
	if found {
		return nil, nil, fmt.Errorf("wavelength conflict detected with lease %s", conflict.ID)
	}

	// Create the lease
	lease := &models.Lease{
		ID:         uuid.New().String(),
		Wavelength: wavelength,
		EndpointID: endpointID,
		OperatorID: e.operatorID,
		Status:     models.LeaseStatusActive,
		StartTime:  now,
		EndTime:    now.Add(duration),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Generate the lease token
	token, err := e.crypto.GenerateLeaseToken(lease)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate lease token: %w", err)
	}

	// Store token hash on the lease
	lease.TokenHash = hashToken(token)

	// Store the lease
	if err := e.store.CreateLease(lease); err != nil {
		return nil, nil, fmt.Errorf("failed to store lease: %w", err)
	}

	// Append audit log
	if err := e.appendAuditLog("CREATE_LEASE", lease.ID); err != nil {
		// Log but don't fail the operation
		// In production, this should be handled by a proper logger
		_ = err
	}

	return lease, token, nil
}

// RenewLease extends an existing lease's end time.
func (e *Engine) RenewLease(leaseID string, extension time.Duration) (*models.Lease, *models.LeaseToken, error) {
	if leaseID == "" {
		return nil, nil, fmt.Errorf("lease ID cannot be empty")
	}
	if extension <= 0 {
		return nil, nil, fmt.Errorf("extension must be positive")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Get the existing lease
	lease, err := e.store.GetLease(leaseID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get lease: %w", err)
	}

	// Check if lease can be renewed
	if lease.Status != models.LeaseStatusActive {
		return nil, nil, fmt.Errorf("cannot renew lease with status %s", lease.Status.String())
	}

	// Extend the lease
	lease.EndTime = lease.EndTime.Add(extension)
	lease.UpdatedAt = time.Now().UTC()

	// Generate a new token for the renewed lease
	token, err := e.crypto.GenerateLeaseToken(lease)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate lease token: %w", err)
	}

	lease.TokenHash = hashToken(token)

	// Update the lease in storage
	if err := e.store.UpdateLease(lease); err != nil {
		return nil, nil, fmt.Errorf("failed to update lease: %w", err)
	}

	// Append audit log
	if err := e.appendAuditLog("RENEW_LEASE", lease.ID); err != nil {
		_ = err
	}

	return lease, token, nil
}

// RevokeLease terminates a lease.
func (e *Engine) RevokeLease(leaseID string) error {
	if leaseID == "" {
		return fmt.Errorf("lease ID cannot be empty")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Get the existing lease
	lease, err := e.store.GetLease(leaseID)
	if err != nil {
		return fmt.Errorf("failed to get lease: %w", err)
	}

	// Check if lease can be revoked
	if lease.Status == models.LeaseStatusRevoked {
		return fmt.Errorf("lease is already revoked")
	}

	// Update the lease status
	lease.Status = models.LeaseStatusRevoked
	lease.UpdatedAt = time.Now().UTC()

	if err := e.store.UpdateLease(lease); err != nil {
		return fmt.Errorf("failed to update lease: %w", err)
	}

	// Append audit log
	if err := e.appendAuditLog("REVOKE_LEASE", lease.ID); err != nil {
		_ = err
	}

	return nil
}

// CheckConflict checks for double-allocation of a wavelength.
// It searches for any active lease on the same wavelength that overlaps in time.
// If excludeLeaseID is provided, that lease is excluded from the check (for renewals).
func (e *Engine) CheckConflict(wavelength *models.Wavelength, excludeLeaseID string) (*models.Lease, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.checkConflictLocked(wavelength, excludeLeaseID)
}

// checkConflictLocked is the lock-free body of CheckConflict, callable from
// methods that already hold e.mu (which doesn't support recursive locking).
func (e *Engine) checkConflictLocked(wavelength *models.Wavelength, excludeLeaseID string) (*models.Lease, bool) {
	if wavelength == nil {
		return nil, false
	}

	leases, err := e.store.ListLeases(LeaseFilter{
		OperatorID: e.operatorID,
		Status:     models.LeaseStatusActive,
	})
	if err != nil {
		return nil, false
	}

	now := time.Now().UTC()
	wlKey := wavelength.ToKey()

	for _, lease := range leases {
		if lease.ID == excludeLeaseID {
			continue
		}
		if lease.Wavelength != nil && lease.Wavelength.ToKey() == wlKey {
			if now.Before(lease.EndTime) {
				return lease, true
			}
		}
	}

	return nil, false
}

// BuildMerkleTree builds a Merkle tree from all active leases.
func (e *Engine) BuildMerkleTree() (*models.MerkleNode, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	leases, err := e.store.ListLeases(LeaseFilter{
		OperatorID: e.operatorID,
		Status:     models.LeaseStatusActive,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list active leases: %w", err)
	}

	return e.crypto.BuildMerkleTree(leases)
}

// CommitMerkleTree builds a Merkle tree, signs the root, and stores the commitment.
func (e *Engine) CommitMerkleTree() (*models.MerkleCommitment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Get active leases for the count
	leases, err := e.store.ListLeases(LeaseFilter{
		OperatorID: e.operatorID,
		Status:     models.LeaseStatusActive,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list active leases: %w", err)
	}

	// Build the Merkle tree
	tree, err := e.crypto.BuildMerkleTree(leases)
	if err != nil {
		return nil, fmt.Errorf("failed to build Merkle tree: %w", err)
	}

	// Increment block height
	e.blockHeight++

	// Sign the commitment
	commitment, err := e.crypto.SignMerkleCommitment(tree.Hash, e.blockHeight)
	if err != nil {
		return nil, fmt.Errorf("failed to sign commitment: %w", err)
	}

	commitment.LeaseCount = int32(len(leases))

	// Store the commitment
	if err := e.store.SaveCommitment(commitment); err != nil {
		return nil, fmt.Errorf("failed to save commitment: %w", err)
	}

	// Append audit log
	if err := e.appendAuditLog("COMMIT_MERKLE", ""); err != nil {
		_ = err
	}

	return commitment, nil
}

// GetLatestCommitment returns the most recent commitment for this operator.
func (e *Engine) GetLatestCommitment() (*models.MerkleCommitment, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.store.GetLatestCommitment(e.operatorID)
}

// VerifyLease checks a lease token against stored state.
func (e *Engine) VerifyLease(token *models.LeaseToken) error {
	if token == nil {
		return fmt.Errorf("token cannot be nil")
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	// Get the stored lease
	lease, err := e.store.GetLease(token.LeaseID)
	if err != nil {
		return fmt.Errorf("lease not found in registry: %w", err)
	}

	// Check lease status
	if lease.Status != models.LeaseStatusActive {
		return fmt.Errorf("lease is not active, status: %s", lease.Status.String())
	}

	// Check expiry
	if time.Now().UTC().After(lease.EndTime) {
		return fmt.Errorf("lease has expired")
	}

	// Verify the token signature
	pubKey, err := e.crypto.GetPublicKey()
	if err != nil {
		return fmt.Errorf("failed to get public key: %w", err)
	}

	if err := e.crypto.ValidateLeaseToken(token, pubKey); err != nil {
		return fmt.Errorf("token validation failed: %w", err)
	}

	// Verify token hash matches stored hash
	computedHash := hashToken(token)
	if string(computedHash) != string(lease.TokenHash) {
		return fmt.Errorf("token hash mismatch")
	}

	return nil
}

// ExpireLeases marks expired leases and returns the count of expired leases.
func (e *Engine) ExpireLeases() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Get all active leases
	leases, err := e.store.ListLeases(LeaseFilter{
		OperatorID: e.operatorID,
		Status:     models.LeaseStatusActive,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list active leases: %w", err)
	}

	now := time.Now().UTC()
	expiredCount := 0

	for _, lease := range leases {
		if now.After(lease.EndTime) {
			lease.Status = models.LeaseStatusExpired
			lease.UpdatedAt = now

			if err := e.store.UpdateLease(lease); err != nil {
				return expiredCount, fmt.Errorf("failed to mark lease %s as expired: %w", lease.ID, err)
			}

			expiredCount++

			if err := e.appendAuditLog("EXPIRE_LEASE", lease.ID); err != nil {
				_ = err
			}
		}
	}

	return expiredCount, nil
}

// GetBlockHeight returns the current block height.
func (e *Engine) GetBlockHeight() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.blockHeight
}

// Store returns the underlying store. Used by federation/audit packages that
// need read access to leases, commitments, and operators that aren't exposed
// as engine methods.
func (e *Engine) Store() Store {
	return e.store
}

// ListLeases is a thin pass-through to the store's ListLeases.
func (e *Engine) ListLeases(filter LeaseFilter) ([]*models.Lease, error) {
	return e.store.ListLeases(filter)
}

// GetLease is a thin pass-through to the store.
func (e *Engine) GetLease(leaseID string) (*models.Lease, error) {
	return e.store.GetLease(leaseID)
}

// GetCommitment is a thin pass-through to the store.
func (e *Engine) GetCommitment(operatorID string, blockHeight int64) (*models.MerkleCommitment, error) {
	return e.store.GetCommitment(operatorID, blockHeight)
}

// CreateOperator is a thin pass-through to the store.
func (e *Engine) CreateOperator(op *models.Operator) error {
	return e.store.CreateOperator(op)
}

// GetOperator is a thin pass-through to the store.
func (e *Engine) GetOperator(operatorID string) (*models.Operator, error) {
	return e.store.GetOperator(operatorID)
}

// ListOperators is a thin pass-through to the store.
func (e *Engine) ListOperators() ([]*models.Operator, error) {
	return e.store.ListOperators()
}

// hashToken returns a hash of the token's canonical fields.
func hashToken(token *models.LeaseToken) []byte {
	// Simple hash of the token fields for storage comparison
	data := fmt.Sprintf("%s:%s:%s:%s:%d:%s:%s",
		token.LeaseID,
		token.OperatorID,
		token.EndpointID,
		token.Wavelength.ToKey(),
		token.Version,
		token.StartTime.UTC().Format(time.RFC3339Nano),
		token.EndTime.UTC().Format(time.RFC3339Nano),
	)

	h := sha3.Sum256([]byte(data))
	return h[:]
}

// appendAuditLog appends an entry to the audit log.
func (e *Engine) appendAuditLog(operation, leaseID string) error {
	entry := &models.AuditLogEntry{
		Timestamp:  time.Now().UTC(),
		Operation:  operation,
		OperatorID: e.operatorID,
		LeaseID:    leaseID,
		Details:    []byte(fmt.Sprintf(`{"block_height":%d}`, e.blockHeight)),
	}

	return e.store.AppendAuditLog(entry)
}
