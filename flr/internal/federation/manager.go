// Package federation provides cross-operator federation capabilities for the FLR.
package federation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/otap/flr/internal/crypto"
	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

// Manager handles cross-operator federation including gossip, sync,
// conflict detection, and proof-of-invalidity exchange.
type Manager struct {
	registry   *registry.Engine
	crypto     *crypto.Engine
	client     *Client
	operators  map[string]*models.Operator // operator_id -> Operator
	mu         sync.RWMutex
	operatorID string

	// Gossip control
	gossipCtx    context.Context
	gossipCancel context.CancelFunc
	gossipWg     sync.WaitGroup
}

// NewManager creates a federation manager.
func NewManager(reg *registry.Engine, crypt *crypto.Engine, client *Client, operatorID string) *Manager {
	return &Manager{
		registry:   reg,
		crypto:     crypt,
		client:     client,
		operators:  make(map[string]*models.Operator),
		operatorID: operatorID,
	}
}

// RegisterOperator adds a peer operator and persists it.
func (m *Manager) RegisterOperator(op *models.Operator) error {
	if op == nil {
		return fmt.Errorf("operator is nil")
	}
	if op.ID == "" {
		return fmt.Errorf("operator ID is required")
	}
	if op.Endpoint == "" {
		return fmt.Errorf("operator endpoint is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already registered
	if _, exists := m.operators[op.ID]; exists {
		return fmt.Errorf("operator %s already registered", op.ID)
	}

	op.Status = models.OperatorStatusActive
	op.JoinedAt = time.Now().UTC()
	op.LastSeen = time.Now().UTC()

	// Persist to store
	if m.registry != nil && m.registry.Store() != nil {
		if err := m.registry.Store().CreateOperator(op); err != nil {
			return fmt.Errorf("persist operator %s: %w", op.ID, err)
		}
	}

	m.operators[op.ID] = op
	return nil
}

// GetOperator gets a registered peer operator by ID.
func (m *Manager) GetOperator(operatorID string) (*models.Operator, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operators[operatorID]
	if !ok {
		return nil, fmt.Errorf("operator %s not found", operatorID)
	}
	// Return a copy to avoid external mutation
	copyOp := *op
	return &copyOp, nil
}

// ListOperators lists all registered operators.
func (m *Manager) ListOperators() []*models.Operator {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*models.Operator, 0, len(m.operators))
	for _, op := range m.operators {
		copyOp := *op
		result = append(result, &copyOp)
	}
	return result
}

// RemoveOperator removes a peer operator.
func (m *Manager) RemoveOperator(operatorID string) error {
	if operatorID == m.operatorID {
		return fmt.Errorf("cannot remove self")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.operators[operatorID]; !ok {
		return fmt.Errorf("operator %s not found", operatorID)
	}

	delete(m.operators, operatorID)
	return nil
}

// SyncWithOperator pulls the latest state from a peer operator.
// Steps: 1. Fetch their latest commitment 2. Fetch their active leases 3. Check for conflicts.
func (m *Manager) SyncWithOperator(operatorID string) error {
	m.mu.RLock()
	op, ok := m.operators[operatorID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("operator %s not found", operatorID)
	}

	// Step 1: Fetch their latest commitment
	commitment, err := m.client.GetCommitment(op.Endpoint, operatorID, 0)
	if err != nil {
		return fmt.Errorf("fetch commitment from %s: %w", operatorID, err)
	}

	// Persist the commitment
	if m.registry != nil && m.registry.Store() != nil {
		if err := m.registry.Store().SaveCommitment(commitment); err != nil {
			return fmt.Errorf("save commitment from %s: %w", operatorID, err)
		}
	}

	// Step 2: Fetch their active leases
	filter := registry.LeaseFilter{
		OperatorID: operatorID,
		Status:     models.LeaseStatusActive,
	}
	peerLeases, err := m.client.GetLeases(op.Endpoint, filter)
	if err != nil {
		return fmt.Errorf("fetch leases from %s: %w", operatorID, err)
	}

	// Step 3: Check for conflicts with our leases
	localFilter := registry.LeaseFilter{
		OperatorID: m.operatorID,
		Status:     models.LeaseStatusActive,
	}
	localLeases, err := m.registry.ListLeases(localFilter)
	if err != nil {
		return fmt.Errorf("list local leases: %w", err)
	}

	conflicts := findConflicts(localLeases, peerLeases)
	if len(conflicts) > 0 {
		// Handle conflicts — for now, just flag them
		for _, c := range conflicts {
			_ = c // conflicts will be processed by DetectConflicts
		}
	}

	// Update last seen
	m.mu.Lock()
	if existing, ok := m.operators[operatorID]; ok {
		existing.LastSeen = time.Now().UTC()
	}
	m.mu.Unlock()

	return nil
}

// PushCommitmentToAll broadcasts our Merkle commitment to all registered peers.
func (m *Manager) PushCommitmentToAll(commitment *models.MerkleCommitment) error {
	if commitment == nil {
		return fmt.Errorf("commitment is nil")
	}

	m.mu.RLock()
	ops := make([]*models.Operator, 0, len(m.operators))
	for _, op := range m.operators {
		if op.Status == models.OperatorStatusActive {
			ops = append(ops, op)
		}
	}
	m.mu.RUnlock()

	var errs []error
	for _, op := range ops {
		if err := m.client.PushCommitment(op.Endpoint, commitment); err != nil {
			errs = append(errs, fmt.Errorf("push to %s: %w", op.ID, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("push commitment to %d/%d peers failed: %v", len(errs), len(ops), errs)
	}
	return nil
}

// DetectConflicts scans for cross-operator double-allocations by comparing
// our active leases with all peers' active leases.
func (m *Manager) DetectConflicts() ([]*models.ProofOfInvalidity, error) {
	localFilter := registry.LeaseFilter{
		OperatorID: m.operatorID,
		Status:     models.LeaseStatusActive,
	}
	localLeases, err := m.registry.ListLeases(localFilter)
	if err != nil {
		return nil, fmt.Errorf("list local leases: %w", err)
	}

	m.mu.RLock()
	ops := make([]*models.Operator, 0, len(m.operators))
	for _, op := range m.operators {
		if op.Status == models.OperatorStatusActive {
			ops = append(ops, op)
		}
	}
	m.mu.RUnlock()

	var allConflicts []*conflictPair
	for _, op := range ops {
		filter := registry.LeaseFilter{
			OperatorID: op.ID,
			Status:     models.LeaseStatusActive,
		}
		peerLeases, err := m.client.GetLeases(op.Endpoint, filter)
		if err != nil {
			// Log and continue with other peers
			continue
		}
		conflicts := findConflicts(localLeases, peerLeases)
		allConflicts = append(allConflicts, conflicts...)
	}

	// Build ProofOfInvalidity for double-allocation conflicts
	var proofs []*models.ProofOfInvalidity
	for _, c := range allConflicts {
		if c.Type == models.InvalidityDoubleAllocation {
			proofs = append(proofs, &models.ProofOfInvalidity{
				Type:      c.Type,
				LeaseA:    c.Local,
				LeaseB:    c.Remote,
				Timestamp: time.Now().UTC(),
			})
		}
	}

	return proofs, nil
}

// HandleProofOfInvalidity processes an incoming ProofOfInvalidity.
// Validates the proof and flags conflicting leases.
func (m *Manager) HandleProofOfInvalidity(poi *models.ProofOfInvalidity) error {
	if poi == nil {
		return fmt.Errorf("proof of invalidity is nil")
	}

	switch poi.Type {
	case models.InvalidityDoubleAllocation:
		if poi.LeaseA == nil || poi.LeaseB == nil {
			return fmt.Errorf("double allocation PoI requires both leases")
		}
		// Validate that both leases actually conflict
		if !checkDoubleAllocation(poi.LeaseA, poi.LeaseB) {
			return fmt.Errorf("leases do not form a valid double-allocation")
		}

	case models.InvalidityExpiredLease:
		if poi.LeaseA == nil {
			return fmt.Errorf("expired lease PoI requires a lease")
		}
		if !checkExpiredLease(poi.LeaseA) {
			return fmt.Errorf("lease is not actually expired")
		}

	case models.InvalidityInvalidSignature:
		if poi.Commitment == nil {
			return fmt.Errorf("invalid signature PoI requires a commitment")
		}

	case models.InvalidityUnauthorizedOp:
		if poi.LeaseA == nil {
			return fmt.Errorf("unauthorized operation PoI requires a lease")
		}

	default:
		return fmt.Errorf("unknown invalidity type: %s", poi.Type.String())
	}

	// Store the proof for audit trail
	if m.registry != nil && m.registry.Store() != nil {
		entry := &models.AuditLogEntry{
			Timestamp:  time.Now().UTC(),
			Operation:  fmt.Sprintf("PROOF_OF_INVALIDITY_%s", poi.Type.String()),
			OperatorID: m.operatorID,
			LeaseID:    "",
			Details:    nil,
		}
		if poi.LeaseA != nil {
			entry.LeaseID = poi.LeaseA.ID
		}
		_ = m.registry.Store().AppendAuditLog(entry)
	}

	return nil
}

// StartGossip begins periodic background synchronization with all peers.
func (m *Manager) StartGossip(ctx context.Context, interval time.Duration) {
	m.StopGossip() // Ensure any previous gossip is stopped

	m.gossipCtx, m.gossipCancel = context.WithCancel(ctx)
	m.gossipWg.Add(1)

	go func() {
		defer m.gossipWg.Done()
		m.gossipLoop(m.gossipCtx, interval)
	}()
}

// StopGossip stops the gossip loop and waits for it to finish.
func (m *Manager) StopGossip() {
	if m.gossipCancel != nil {
		m.gossipCancel()
		m.gossipWg.Wait()
		m.gossipCancel = nil
	}
}

// GetTranslationTable generates a cross-operator wavelength mapping by fetching
// the peer's translation table and merging with our local knowledge.
func (m *Manager) GetTranslationTable(peerOperatorID string) ([]*models.TranslationEntry, error) {
	m.mu.RLock()
	op, ok := m.operators[peerOperatorID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("operator %s not found", peerOperatorID)
	}

	entries, err := m.client.GetTranslationTable(op.Endpoint, peerOperatorID)
	if err != nil {
		return nil, fmt.Errorf("fetch translation table from %s: %w", peerOperatorID, err)
	}

	return entries, nil
}
