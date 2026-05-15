package registry

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/otap/flr/internal/crypto"
	"github.com/otap/flr/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inMemoryStore is an in-memory implementation of Store for unit testing.
type inMemoryStore struct {
	mu           sync.RWMutex
	leases       map[string]*models.Lease
	endpoints    map[string]*models.Endpoint
	operators    map[string]*models.Operator
	commitments  map[string]*models.MerkleCommitment
	latestCommit map[string]*models.MerkleCommitment
	auditLog     []*models.AuditLogEntry
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{
		leases:       make(map[string]*models.Lease),
		endpoints:    make(map[string]*models.Endpoint),
		operators:    make(map[string]*models.Operator),
		commitments:  make(map[string]*models.MerkleCommitment),
		latestCommit: make(map[string]*models.MerkleCommitment),
		auditLog:     make([]*models.AuditLogEntry, 0),
	}
}

func (s *inMemoryStore) CreateLease(lease *models.Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.leases[lease.ID]; exists {
		return fmt.Errorf("lease already exists: %s", lease.ID)
	}
	s.leases[lease.ID] = lease
	return nil
}

func (s *inMemoryStore) GetLease(id string) (*models.Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, exists := s.leases[id]
	if !exists {
		return nil, fmt.Errorf("lease not found: %s", id)
	}
	return lease, nil
}

func (s *inMemoryStore) UpdateLease(lease *models.Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.leases[lease.ID]; !exists {
		return fmt.Errorf("lease not found: %s", lease.ID)
	}
	s.leases[lease.ID] = lease
	return nil
}

func (s *inMemoryStore) DeleteLease(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.leases, id)
	return nil
}

func (s *inMemoryStore) ListLeases(filter LeaseFilter) ([]*models.Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*models.Lease
	for _, lease := range s.leases {
		if filter.OperatorID != "" && lease.OperatorID != filter.OperatorID {
			continue
		}
		if filter.EndpointID != "" && lease.EndpointID != filter.EndpointID {
			continue
		}
		if filter.Status != models.LeaseStatusUnspecified && lease.Status != filter.Status {
			continue
		}
		result = append(result, lease)
	}
	return result, nil
}

func (s *inMemoryStore) CreateEndpoint(ep *models.Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.endpoints[ep.ID]; exists {
		return fmt.Errorf("endpoint already exists: %s", ep.ID)
	}
	s.endpoints[ep.ID] = ep
	return nil
}

func (s *inMemoryStore) GetEndpoint(id string) (*models.Endpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ep, exists := s.endpoints[id]
	if !exists {
		return nil, fmt.Errorf("endpoint not found: %s", id)
	}
	return ep, nil
}

func (s *inMemoryStore) ListEndpoints(filter EndpointFilter) ([]*models.Endpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*models.Endpoint
	for _, ep := range s.endpoints {
		if filter.OperatorID != "" && ep.OperatorID != filter.OperatorID {
			continue
		}
		if filter.Status != models.EndpointStatusUnspecified && ep.Status != filter.Status {
			continue
		}
		result = append(result, ep)
	}
	return result, nil
}

func (s *inMemoryStore) CreateOperator(op *models.Operator) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.operators[op.ID]; exists {
		return fmt.Errorf("operator already exists: %s", op.ID)
	}
	s.operators[op.ID] = op
	return nil
}

func (s *inMemoryStore) GetOperator(id string) (*models.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	op, exists := s.operators[id]
	if !exists {
		return nil, fmt.Errorf("operator not found: %s", id)
	}
	return op, nil
}

func (s *inMemoryStore) ListOperators() ([]*models.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*models.Operator
	for _, op := range s.operators {
		result = append(result, op)
	}
	return result, nil
}

func (s *inMemoryStore) SaveCommitment(c *models.MerkleCommitment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s:%d", c.OperatorID, c.BlockHeight)
	s.commitments[key] = c
	s.latestCommit[c.OperatorID] = c
	return nil
}

func (s *inMemoryStore) GetCommitment(operatorID string, blockHeight int64) (*models.MerkleCommitment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := fmt.Sprintf("%s:%d", operatorID, blockHeight)
	c, exists := s.commitments[key]
	if !exists {
		return nil, fmt.Errorf("commitment not found")
	}
	return c, nil
}

func (s *inMemoryStore) GetLatestCommitment(operatorID string) (*models.MerkleCommitment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, exists := s.latestCommit[operatorID]
	if !exists {
		return nil, fmt.Errorf("no commitment found for operator %s", operatorID)
	}
	return c, nil
}

func (s *inMemoryStore) AppendAuditLog(entry *models.AuditLogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLog = append(s.auditLog, entry)
	return nil
}

func (s *inMemoryStore) GetAuditLog(from, to time.Time) ([]*models.AuditLogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*models.AuditLogEntry
	for _, entry := range s.auditLog {
		if (entry.Timestamp.Equal(from) || entry.Timestamp.After(from)) &&
			(entry.Timestamp.Equal(to) || entry.Timestamp.Before(to)) {
			result = append(result, entry)
		}
	}
	return result, nil
}

// --- Test Setup ---

func createTestRegistryEngine(t *testing.T) (*Engine, *inMemoryStore) {
	t.Helper()
	_, privPEM, _ := crypto.GenerateKeyPair()
	cryptoEngine, err := crypto.NewEngine("test-operator", privPEM)
	require.NoError(t, err)

	store := newInMemoryStore()
	engine := NewEngine(store, cryptoEngine, "test-operator")
	return engine, store
}

func createTestWavelength() *models.Wavelength {
	return &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 1,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}
}

// --- NewEngine Tests ---

func TestNewEngine(t *testing.T) {
	_, privPEM, _ := crypto.GenerateKeyPair()
	cryptoEngine, err := crypto.NewEngine("test-operator", privPEM)
	require.NoError(t, err)

	store := newInMemoryStore()
	engine := NewEngine(store, cryptoEngine, "test-operator")

	assert.NotNil(t, engine)
	assert.Equal(t, "test-operator", engine.operatorID)
	assert.Equal(t, int64(0), engine.blockHeight)
	assert.NotNil(t, engine.store)
	assert.NotNil(t, engine.crypto)
}

// --- AllocateLease Tests ---

func TestEngine_AllocateLease(t *testing.T) {
	engine, store := createTestRegistryEngine(t)

	t.Run("allocate new lease", func(t *testing.T) {
		wl := createTestWavelength()
		lease, token, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)
		assert.NotNil(t, lease)
		assert.NotNil(t, token)
		assert.NotEmpty(t, lease.ID)
		assert.Equal(t, "test-operator", lease.OperatorID)
		assert.Equal(t, "ep-001", lease.EndpointID)
		assert.Equal(t, models.LeaseStatusActive, lease.Status)
		assert.Equal(t, wl, lease.Wavelength)

		// Verify token
		assert.Equal(t, lease.ID, token.LeaseID)
		assert.Equal(t, int32(1), token.Version)
		assert.NotEmpty(t, token.Signature)

		// Verify lease was stored
		stored, err := store.GetLease(lease.ID)
		require.NoError(t, err)
		assert.Equal(t, lease.ID, stored.ID)
	})

	t.Run("nil wavelength", func(t *testing.T) {
		_, _, err := engine.AllocateLease(nil, "ep-001", 1*time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wavelength cannot be nil")
	})

	t.Run("empty endpoint ID", func(t *testing.T) {
		wl := createTestWavelength()
		_, _, err := engine.AllocateLease(wl, "", 1*time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "endpoint ID cannot be empty")
	})

	t.Run("zero duration", func(t *testing.T) {
		wl := createTestWavelength()
		_, _, err := engine.AllocateLease(wl, "ep-001", 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duration must be positive")
	})

	t.Run("negative duration", func(t *testing.T) {
		wl := createTestWavelength()
		_, _, err := engine.AllocateLease(wl, "ep-001", -1*time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duration must be positive")
	})

	t.Run("double allocation conflict", func(t *testing.T) {
		wl := createTestWavelength()
		_, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)

		// Try to allocate the same wavelength again
		_, _, err = engine.AllocateLease(wl, "ep-002", 1*time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wavelength conflict detected")
	})
}

// --- RenewLease Tests ---

func TestEngine_RenewLease(t *testing.T) {
	engine, _ := createTestRegistryEngine(t)

	t.Run("renew active lease", func(t *testing.T) {
		wl := createTestWavelength()
		lease, token, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)
		_ = token

		originalEndTime := lease.EndTime

		renewedLease, renewedToken, err := engine.RenewLease(lease.ID, 2*time.Hour)
		require.NoError(t, err)
		assert.NotNil(t, renewedLease)
		assert.NotNil(t, renewedToken)
		assert.True(t, renewedLease.EndTime.After(originalEndTime))
		assert.Equal(t, lease.ID, renewedLease.ID)
	})

	t.Run("renew non-existent lease", func(t *testing.T) {
		_, _, err := engine.RenewLease("non-existent", 1*time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get lease")
	})

	t.Run("empty lease ID", func(t *testing.T) {
		_, _, err := engine.RenewLease("", 1*time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lease ID cannot be empty")
	})

	t.Run("negative extension", func(t *testing.T) {
		wl := createTestWavelength()
		lease, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)

		_, _, err = engine.RenewLease(lease.ID, -1*time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extension must be positive")
	})

	t.Run("renew revoked lease", func(t *testing.T) {
		wl := &models.Wavelength{LambdaNm: 1550.13, ChannelNum: 2, Band: models.BandCBand, GridGHz: 25.0}
		lease, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)

		err = engine.RevokeLease(lease.ID)
		require.NoError(t, err)

		_, _, err = engine.RenewLease(lease.ID, 1*time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot renew lease")
	})
}

// --- RevokeLease Tests ---

func TestEngine_RevokeLease(t *testing.T) {
	engine, _ := createTestRegistryEngine(t)

	t.Run("revoke active lease", func(t *testing.T) {
		wl := &models.Wavelength{LambdaNm: 1550.14, ChannelNum: 3, Band: models.BandCBand, GridGHz: 25.0}
		lease, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)

		err = engine.RevokeLease(lease.ID)
		require.NoError(t, err)

		// Verify lease is revoked
		revoked, err := engine.store.GetLease(lease.ID)
		require.NoError(t, err)
		assert.Equal(t, models.LeaseStatusRevoked, revoked.Status)
	})

	t.Run("revoke already revoked lease", func(t *testing.T) {
		wl := &models.Wavelength{LambdaNm: 1550.15, ChannelNum: 4, Band: models.BandCBand, GridGHz: 25.0}
		lease, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)

		err = engine.RevokeLease(lease.ID)
		require.NoError(t, err)

		err = engine.RevokeLease(lease.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already revoked")
	})

	t.Run("revoke non-existent lease", func(t *testing.T) {
		err := engine.RevokeLease("non-existent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get lease")
	})

	t.Run("empty lease ID", func(t *testing.T) {
		err := engine.RevokeLease("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lease ID cannot be empty")
	})
}

// --- CheckConflict Tests ---

func TestEngine_CheckConflict(t *testing.T) {
	engine, _ := createTestRegistryEngine(t)

	t.Run("no conflict for single lease", func(t *testing.T) {
		wl := &models.Wavelength{LambdaNm: 1550.16, ChannelNum: 5, Band: models.BandCBand, GridGHz: 25.0}
		_, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)

		conflict, found := engine.CheckConflict(wl, "")
		assert.False(t, found)
		assert.Nil(t, conflict)
	})

	t.Run("conflict for double allocation", func(t *testing.T) {
		wl := &models.Wavelength{LambdaNm: 1550.17, ChannelNum: 6, Band: models.BandCBand, GridGHz: 25.0}
		lease, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)

		// Bypass AllocateLease conflict check to create a conflicting lease directly
		// by manipulating the store directly
		conflictLease := &models.Lease{
			ID:         "conflict-lease-001",
			Wavelength: wl,
			EndpointID: "ep-002",
			OperatorID: "test-operator",
			Status:     models.LeaseStatusActive,
			StartTime:  time.Now().UTC(),
			EndTime:    time.Now().UTC().Add(2 * time.Hour),
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
		err = engine.store.CreateLease(conflictLease)
		require.NoError(t, err)

		foundLease, found := engine.CheckConflict(wl, "")
		assert.True(t, found)
		assert.NotNil(t, foundLease)
		assert.Equal(t, lease.ID, foundLease.ID)
	})

	t.Run("exclude lease ID", func(t *testing.T) {
		wl := &models.Wavelength{LambdaNm: 1550.18, ChannelNum: 7, Band: models.BandCBand, GridGHz: 25.0}
		lease, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)

		// Excluding the lease itself should return no conflict
		conflict, found := engine.CheckConflict(wl, lease.ID)
		assert.False(t, found)
		assert.Nil(t, conflict)
	})

	t.Run("nil wavelength", func(t *testing.T) {
		conflict, found := engine.CheckConflict(nil, "")
		assert.False(t, found)
		assert.Nil(t, conflict)
	})
}

// --- BuildMerkleTree Tests ---

func TestEngine_BuildMerkleTree(t *testing.T) {
	t.Run("empty tree", func(t *testing.T) {
		engine, _ := createTestRegistryEngine(t)

		tree, err := engine.BuildMerkleTree()
		require.NoError(t, err)
		assert.NotNil(t, tree)
		assert.NotNil(t, tree.Hash)
	})

	t.Run("tree with leases", func(t *testing.T) {
		engine, _ := createTestRegistryEngine(t)

		// Allocate some leases
		for i := 0; i < 5; i++ {
			wl := &models.Wavelength{
				LambdaNm:   1550.12 + float64(i)*0.01,
				ChannelNum: int32(i + 1),
				Band:       models.BandCBand,
				GridGHz:    25.0,
			}
			_, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
			require.NoError(t, err)
		}

		tree, err := engine.BuildMerkleTree()
		require.NoError(t, err)
		assert.NotNil(t, tree)
		assert.NotNil(t, tree.Hash)
		assert.Equal(t, 32, len(tree.Hash))
	})
}

// --- CommitMerkleTree Tests ---

func TestEngine_CommitMerkleTree(t *testing.T) {
	t.Run("commit empty tree", func(t *testing.T) {
		engine, _ := createTestRegistryEngine(t)

		commitment, err := engine.CommitMerkleTree()
		require.NoError(t, err)
		assert.NotNil(t, commitment)
		assert.Equal(t, "test-operator", commitment.OperatorID)
		assert.Equal(t, int64(1), commitment.BlockHeight)
		assert.Equal(t, int32(0), commitment.LeaseCount)
		assert.NotEmpty(t, commitment.Signature)
	})

	t.Run("commit with leases", func(t *testing.T) {
		engine, _ := createTestRegistryEngine(t)

		// Allocate some leases
		for i := 0; i < 3; i++ {
			wl := &models.Wavelength{
				LambdaNm:   1550.12 + float64(i)*0.01,
				ChannelNum: int32(i + 1),
				Band:       models.BandCBand,
				GridGHz:    25.0,
			}
			_, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
			require.NoError(t, err)
		}

		commitment, err := engine.CommitMerkleTree()
		require.NoError(t, err)
		assert.NotNil(t, commitment)
		assert.Equal(t, int64(1), commitment.BlockHeight)
		assert.Equal(t, int32(3), commitment.LeaseCount)
	})

	t.Run("multiple commits increment block height", func(t *testing.T) {
		engine, _ := createTestRegistryEngine(t)

		c1, err := engine.CommitMerkleTree()
		require.NoError(t, err)
		assert.Equal(t, int64(1), c1.BlockHeight)

		c2, err := engine.CommitMerkleTree()
		require.NoError(t, err)
		assert.Equal(t, int64(2), c2.BlockHeight)

		c3, err := engine.CommitMerkleTree()
		require.NoError(t, err)
		assert.Equal(t, int64(3), c3.BlockHeight)
	})
}

// --- GetLatestCommitment Tests ---

func TestEngine_GetLatestCommitment(t *testing.T) {
	engine, _ := createTestRegistryEngine(t)

	t.Run("get latest after commits", func(t *testing.T) {
		_, err := engine.GetLatestCommitment()
		require.Error(t, err) // No commitment yet

		c1, err := engine.CommitMerkleTree()
		require.NoError(t, err)

		latest, err := engine.GetLatestCommitment()
		require.NoError(t, err)
		assert.Equal(t, c1.BlockHeight, latest.BlockHeight)
		assert.Equal(t, c1.RootHash, latest.RootHash)
	})
}

// --- VerifyLease Tests ---

func TestEngine_VerifyLease(t *testing.T) {
	engine, _ := createTestRegistryEngine(t)

	t.Run("verify valid lease", func(t *testing.T) {
		wl := &models.Wavelength{LambdaNm: 1550.20, ChannelNum: 8, Band: models.BandCBand, GridGHz: 25.0}
		_, token, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)

		err = engine.VerifyLease(token)
		require.NoError(t, err)
	})

	t.Run("verify nil token", func(t *testing.T) {
		err := engine.VerifyLease(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token cannot be nil")
	})

	t.Run("verify non-existent lease", func(t *testing.T) {
		token := &models.LeaseToken{
			LeaseID:    "non-existent-lease",
			OperatorID: "test-operator",
			Version:    1,
		}
		err := engine.VerifyLease(token)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lease not found")
	})
}

// --- ExpireLeases Tests ---

func TestEngine_ExpireLeases(t *testing.T) {
	t.Run("expire overdue leases", func(t *testing.T) {
		engine, store := createTestRegistryEngine(t)

		now := time.Now().UTC()

		// Create an already-expired lease directly in the store
		expiredLease := &models.Lease{
			ID:         "expired-001",
			Wavelength: &models.Wavelength{LambdaNm: 1550.30, ChannelNum: 10, Band: models.BandCBand, GridGHz: 25.0},
			EndpointID: "ep-001",
			OperatorID: "test-operator",
			Status:     models.LeaseStatusActive,
			StartTime:  now.Add(-2 * time.Hour),
			EndTime:    now.Add(-1 * time.Hour), // Already expired
			CreatedAt:  now.Add(-2 * time.Hour),
			UpdatedAt:  now.Add(-2 * time.Hour),
		}
		err := store.CreateLease(expiredLease)
		require.NoError(t, err)

		count, err := engine.ExpireLeases()
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Verify lease is marked as expired
		updated, err := store.GetLease("expired-001")
		require.NoError(t, err)
		assert.Equal(t, models.LeaseStatusExpired, updated.Status)
	})

	t.Run("no leases to expire", func(t *testing.T) {
		engine, _ := createTestRegistryEngine(t)

		count, err := engine.ExpireLeases()
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("active leases not expired", func(t *testing.T) {
		engine, _ := createTestRegistryEngine(t)

		wl := &models.Wavelength{LambdaNm: 1550.31, ChannelNum: 11, Band: models.BandCBand, GridGHz: 25.0}
		_, _, err := engine.AllocateLease(wl, "ep-001", 24*time.Hour)
		require.NoError(t, err)

		count, err := engine.ExpireLeases()
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

// --- GetBlockHeight Tests ---

func TestEngine_GetBlockHeight(t *testing.T) {
	engine, _ := createTestRegistryEngine(t)

	assert.Equal(t, int64(0), engine.GetBlockHeight())

	_, err := engine.CommitMerkleTree()
	require.NoError(t, err)

	assert.Equal(t, int64(1), engine.GetBlockHeight())
}

// --- Thread Safety Tests ---

func TestEngine_ConcurrentAllocations(t *testing.T) {
	engine, _ := createTestRegistryEngine(t)

	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			wl := &models.Wavelength{
				LambdaNm:   1550.12 + float64(idx)*0.1,
				ChannelNum: int32(idx + 1),
				Band:       models.BandCBand,
				GridGHz:    25.0,
			}
			_, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
			require.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// Verify all leases were created
	leases, err := engine.store.ListLeases(LeaseFilter{OperatorID: "test-operator"})
	require.NoError(t, err)
	assert.Len(t, leases, numGoroutines)
}

func TestEngine_ConcurrentCommits(t *testing.T) {
	engine, _ := createTestRegistryEngine(t)

	// Create some leases first
	for i := 0; i < 5; i++ {
		wl := &models.Wavelength{
			LambdaNm:   1550.12 + float64(i)*0.1,
			ChannelNum: int32(i + 1),
			Band:       models.BandCBand,
			GridGHz:    25.0,
		}
		_, _, err := engine.AllocateLease(wl, "ep-001", 1*time.Hour)
		require.NoError(t, err)
	}

	// Try concurrent commits - they should serialize due to mutex
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := engine.CommitMerkleTree()
			// Some may fail due to concurrent access, but no panic
			_ = err
		}()
	}
	wg.Wait()
}
