package registry

import (
	"fmt"
	"testing"
	"time"

	"github.com/otap/flr/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestStore creates a new BadgerStore in a temporary directory.
func createTestStore(t *testing.T) *BadgerStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewBadgerStore(dir)
	require.NoError(t, err)

	t.Cleanup(func() {
		err := store.Close()
		require.NoError(t, err)
	})

	return store
}

// createTestLease creates a test lease.
func createTestLease(id, operatorID string, status models.LeaseStatus) *models.Lease {
	now := time.Now().UTC()
	return &models.Lease{
		ID:         id,
		Wavelength: &models.Wavelength{LambdaNm: 1550.12, ChannelNum: 1, Band: models.BandCBand, GridGHz: 25.0},
		EndpointID: "ep-001",
		OperatorID: operatorID,
		Status:     status,
		StartTime:  now.Add(-1 * time.Hour),
		EndTime:    now.Add(1 * time.Hour),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// createTestEndpoint creates a test endpoint.
func createTestEndpoint(id, operatorID string, status models.EndpointStatus) *models.Endpoint {
	return &models.Endpoint{
		ID:         id,
		NodeID:     "node-001",
		OperatorID: operatorID,
		Address:    "192.168.1.1:8080",
		AWGPort:    1,
		Status:     status,
		CreatedAt:  time.Now().UTC(),
	}
}

// createTestOperator creates a test operator.
func createTestOperator(id string) *models.Operator {
	return &models.Operator{
		ID:        id,
		Name:      fmt.Sprintf("Operator %s", id),
		PublicKey: []byte("test-public-key"),
		Endpoint:  fmt.Sprintf("https://%s.otap.network:9090", id),
		Status:    models.OperatorStatusActive,
		JoinedAt:  time.Now().UTC(),
		LastSeen:  time.Now().UTC(),
	}
}

// createTestCommitment creates a test Merkle commitment.
func createTestCommitment(operatorID string, blockHeight int64) *models.MerkleCommitment {
	return &models.MerkleCommitment{
		OperatorID:  operatorID,
		RootHash:    []byte(fmt.Sprintf("root-hash-%d", blockHeight)),
		Timestamp:   time.Now().UTC(),
		Signature:   []byte("test-signature"),
		LeaseCount:  10,
		BlockHeight: blockHeight,
	}
}

// createTestAuditLogEntry creates a test audit log entry.
func createTestAuditLogEntry(operatorID, leaseID, operation string, ts time.Time) *models.AuditLogEntry {
	return &models.AuditLogEntry{
		Timestamp:  ts.UTC(),
		Operation:  operation,
		OperatorID: operatorID,
		LeaseID:    leaseID,
		Details:    []byte(`{"key":"value"}`),
	}
}

// --- Lease Tests ---

func TestBadgerStore_CreateLease(t *testing.T) {
	store := createTestStore(t)

	t.Run("create new lease", func(t *testing.T) {
		lease := createTestLease("lease-001", "op-001", models.LeaseStatusActive)
		err := store.CreateLease(lease)
		require.NoError(t, err)
	})

	t.Run("create duplicate lease", func(t *testing.T) {
		lease := createTestLease("lease-002", "op-001", models.LeaseStatusActive)
		err := store.CreateLease(lease)
		require.NoError(t, err)

		// Try to create the same lease again
		err = store.CreateLease(lease)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("nil lease", func(t *testing.T) {
		err := store.CreateLease(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
	})

	t.Run("empty lease ID", func(t *testing.T) {
		lease := createTestLease("", "op-001", models.LeaseStatusActive)
		err := store.CreateLease(lease)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestBadgerStore_GetLease(t *testing.T) {
	store := createTestStore(t)
	lease := createTestLease("lease-003", "op-001", models.LeaseStatusActive)

	err := store.CreateLease(lease)
	require.NoError(t, err)

	t.Run("get existing lease", func(t *testing.T) {
		retrieved, err := store.GetLease("lease-003")
		require.NoError(t, err)
		assert.Equal(t, lease.ID, retrieved.ID)
		assert.Equal(t, lease.OperatorID, retrieved.OperatorID)
		assert.Equal(t, lease.EndpointID, retrieved.EndpointID)
	})

	t.Run("get non-existent lease", func(t *testing.T) {
		_, err := store.GetLease("non-existent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("empty lease ID", func(t *testing.T) {
		_, err := store.GetLease("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestBadgerStore_UpdateLease(t *testing.T) {
	store := createTestStore(t)
	lease := createTestLease("lease-004", "op-001", models.LeaseStatusActive)

	err := store.CreateLease(lease)
	require.NoError(t, err)

	t.Run("update existing lease", func(t *testing.T) {
		lease.Status = models.LeaseStatusRevoked
		err := store.UpdateLease(lease)
		require.NoError(t, err)

		retrieved, err := store.GetLease("lease-004")
		require.NoError(t, err)
		assert.Equal(t, models.LeaseStatusRevoked, retrieved.Status)
	})

	t.Run("update non-existent lease", func(t *testing.T) {
		nonExistent := createTestLease("non-existent", "op-001", models.LeaseStatusActive)
		err := store.UpdateLease(nonExistent)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("nil lease", func(t *testing.T) {
		err := store.UpdateLease(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
	})

	t.Run("empty lease ID", func(t *testing.T) {
		lease := createTestLease("", "op-001", models.LeaseStatusActive)
		err := store.UpdateLease(lease)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestBadgerStore_DeleteLease(t *testing.T) {
	store := createTestStore(t)
	lease := createTestLease("lease-005", "op-001", models.LeaseStatusActive)

	err := store.CreateLease(lease)
	require.NoError(t, err)

	t.Run("delete existing lease", func(t *testing.T) {
		err := store.DeleteLease("lease-005")
		require.NoError(t, err)

		_, err = store.GetLease("lease-005")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("delete non-existent lease", func(t *testing.T) {
		// Deleting a non-existent key should not error in Badger
		err := store.DeleteLease("non-existent")
		assert.NoError(t, err)
	})

	t.Run("empty lease ID", func(t *testing.T) {
		err := store.DeleteLease("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestBadgerStore_ListLeases(t *testing.T) {
	store := createTestStore(t)

	// Create multiple leases
	leases := []*models.Lease{
		createTestLease("lease-006", "op-001", models.LeaseStatusActive),
		createTestLease("lease-007", "op-001", models.LeaseStatusActive),
		createTestLease("lease-008", "op-002", models.LeaseStatusActive),
		createTestLease("lease-009", "op-001", models.LeaseStatusExpired),
	}

	for _, lease := range leases {
		err := store.CreateLease(lease)
		require.NoError(t, err)
	}

	t.Run("list all leases", func(t *testing.T) {
		result, err := store.ListLeases(LeaseFilter{})
		require.NoError(t, err)
		assert.Len(t, result, 4)
	})

	t.Run("list by operator", func(t *testing.T) {
		result, err := store.ListLeases(LeaseFilter{OperatorID: "op-001"})
		require.NoError(t, err)
		assert.Len(t, result, 3)
		for _, l := range result {
			assert.Equal(t, "op-001", l.OperatorID)
		}
	})

	t.Run("list by status", func(t *testing.T) {
		result, err := store.ListLeases(LeaseFilter{Status: models.LeaseStatusActive})
		require.NoError(t, err)
		assert.Len(t, result, 3)
		for _, l := range result {
			assert.Equal(t, models.LeaseStatusActive, l.Status)
		}
	})

	t.Run("list by operator and status", func(t *testing.T) {
		result, err := store.ListLeases(LeaseFilter{
			OperatorID: "op-001",
			Status:     models.LeaseStatusActive,
		})
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("list with no matches", func(t *testing.T) {
		result, err := store.ListLeases(LeaseFilter{OperatorID: "op-non-existent"})
		require.NoError(t, err)
		assert.Len(t, result, 0)
	})
}

// --- Endpoint Tests ---

func TestBadgerStore_CreateEndpoint(t *testing.T) {
	store := createTestStore(t)

	t.Run("create new endpoint", func(t *testing.T) {
		ep := createTestEndpoint("ep-001", "op-001", models.EndpointStatusActive)
		err := store.CreateEndpoint(ep)
		require.NoError(t, err)
	})

	t.Run("create duplicate endpoint", func(t *testing.T) {
		ep := createTestEndpoint("ep-002", "op-001", models.EndpointStatusActive)
		err := store.CreateEndpoint(ep)
		require.NoError(t, err)

		err = store.CreateEndpoint(ep)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("nil endpoint", func(t *testing.T) {
		err := store.CreateEndpoint(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
	})

	t.Run("empty endpoint ID", func(t *testing.T) {
		ep := createTestEndpoint("", "op-001", models.EndpointStatusActive)
		err := store.CreateEndpoint(ep)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestBadgerStore_GetEndpoint(t *testing.T) {
	store := createTestStore(t)
	ep := createTestEndpoint("ep-003", "op-001", models.EndpointStatusActive)

	err := store.CreateEndpoint(ep)
	require.NoError(t, err)

	t.Run("get existing endpoint", func(t *testing.T) {
		retrieved, err := store.GetEndpoint("ep-003")
		require.NoError(t, err)
		assert.Equal(t, ep.ID, retrieved.ID)
		assert.Equal(t, ep.OperatorID, retrieved.OperatorID)
	})

	t.Run("get non-existent endpoint", func(t *testing.T) {
		_, err := store.GetEndpoint("non-existent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestBadgerStore_ListEndpoints(t *testing.T) {
	store := createTestStore(t)

	endpoints := []*models.Endpoint{
		createTestEndpoint("ep-004", "op-001", models.EndpointStatusActive),
		createTestEndpoint("ep-005", "op-001", models.EndpointStatusInactive),
		createTestEndpoint("ep-006", "op-002", models.EndpointStatusActive),
	}

	for _, ep := range endpoints {
		err := store.CreateEndpoint(ep)
		require.NoError(t, err)
	}

	t.Run("list all endpoints", func(t *testing.T) {
		result, err := store.ListEndpoints(EndpointFilter{})
		require.NoError(t, err)
		assert.Len(t, result, 3)
	})

	t.Run("list by operator", func(t *testing.T) {
		result, err := store.ListEndpoints(EndpointFilter{OperatorID: "op-001"})
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("list by status", func(t *testing.T) {
		result, err := store.ListEndpoints(EndpointFilter{Status: models.EndpointStatusActive})
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("no matches", func(t *testing.T) {
		result, err := store.ListEndpoints(EndpointFilter{OperatorID: "non-existent"})
		require.NoError(t, err)
		assert.Len(t, result, 0)
	})
}

// --- Operator Tests ---

func TestBadgerStore_CreateOperator(t *testing.T) {
	store := createTestStore(t)

	t.Run("create new operator", func(t *testing.T) {
		op := createTestOperator("op-001")
		err := store.CreateOperator(op)
		require.NoError(t, err)
	})

	t.Run("create duplicate operator", func(t *testing.T) {
		op := createTestOperator("op-002")
		err := store.CreateOperator(op)
		require.NoError(t, err)

		err = store.CreateOperator(op)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("nil operator", func(t *testing.T) {
		err := store.CreateOperator(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
	})
}

func TestBadgerStore_GetOperator(t *testing.T) {
	store := createTestStore(t)
	op := createTestOperator("op-003")

	err := store.CreateOperator(op)
	require.NoError(t, err)

	t.Run("get existing operator", func(t *testing.T) {
		retrieved, err := store.GetOperator("op-003")
		require.NoError(t, err)
		assert.Equal(t, op.ID, retrieved.ID)
		assert.Equal(t, op.Name, retrieved.Name)
	})

	t.Run("get non-existent operator", func(t *testing.T) {
		_, err := store.GetOperator("non-existent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestBadgerStore_ListOperators(t *testing.T) {
	store := createTestStore(t)

	operators := []*models.Operator{
		createTestOperator("op-004"),
		createTestOperator("op-005"),
	}

	for _, op := range operators {
		err := store.CreateOperator(op)
		require.NoError(t, err)
	}

	result, err := store.ListOperators()
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

// --- Commitment Tests ---

func TestBadgerStore_SaveCommitment(t *testing.T) {
	store := createTestStore(t)

	t.Run("save commitment", func(t *testing.T) {
		c := createTestCommitment("op-001", 1)
		err := store.SaveCommitment(c)
		require.NoError(t, err)
	})

	t.Run("nil commitment", func(t *testing.T) {
		err := store.SaveCommitment(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
	})

	t.Run("empty operator ID", func(t *testing.T) {
		c := createTestCommitment("", 1)
		err := store.SaveCommitment(c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestBadgerStore_GetCommitment(t *testing.T) {
	store := createTestStore(t)
	c := createTestCommitment("op-001", 42)

	err := store.SaveCommitment(c)
	require.NoError(t, err)

	t.Run("get existing commitment", func(t *testing.T) {
		retrieved, err := store.GetCommitment("op-001", 42)
		require.NoError(t, err)
		assert.Equal(t, c.OperatorID, retrieved.OperatorID)
		assert.Equal(t, c.BlockHeight, retrieved.BlockHeight)
		assert.Equal(t, c.RootHash, retrieved.RootHash)
	})

	t.Run("get non-existent commitment", func(t *testing.T) {
		_, err := store.GetCommitment("op-001", 999)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("empty operator ID", func(t *testing.T) {
		_, err := store.GetCommitment("", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestBadgerStore_GetLatestCommitment(t *testing.T) {
	store := createTestStore(t)

	t.Run("get latest", func(t *testing.T) {
		c1 := createTestCommitment("op-001", 1)
		err := store.SaveCommitment(c1)
		require.NoError(t, err)

		c2 := createTestCommitment("op-001", 2)
		err = store.SaveCommitment(c2)
		require.NoError(t, err)

		latest, err := store.GetLatestCommitment("op-001")
		require.NoError(t, err)
		assert.Equal(t, int64(2), latest.BlockHeight)
	})

	t.Run("get latest for non-existent operator", func(t *testing.T) {
		_, err := store.GetLatestCommitment("non-existent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no commitment found")
	})

	t.Run("empty operator ID", func(t *testing.T) {
		_, err := store.GetLatestCommitment("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

// --- Audit Log Tests ---

func TestBadgerStore_AppendAuditLog(t *testing.T) {
	store := createTestStore(t)

	t.Run("append audit log", func(t *testing.T) {
		entry := createTestAuditLogEntry("op-001", "lease-001", "CREATE_LEASE", time.Now().UTC())
		err := store.AppendAuditLog(entry)
		require.NoError(t, err)
		assert.NotNil(t, entry.Hash)
		assert.Equal(t, 32, len(entry.Hash))
	})

	t.Run("nil entry", func(t *testing.T) {
		err := store.AppendAuditLog(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
	})
}

func TestBadgerStore_GetAuditLog(t *testing.T) {
	store := createTestStore(t)

	baseTime := time.Now().UTC()

	// Create audit log entries at different times
	entries := []*models.AuditLogEntry{
		createTestAuditLogEntry("op-001", "lease-001", "CREATE_LEASE", baseTime.Add(-2*time.Hour)),
		createTestAuditLogEntry("op-001", "lease-002", "CREATE_LEASE", baseTime.Add(-1*time.Hour)),
		createTestAuditLogEntry("op-001", "lease-001", "REVOKE_LEASE", baseTime),
	}

	for _, entry := range entries {
		err := store.AppendAuditLog(entry)
		require.NoError(t, err)
	}

	t.Run("get all entries in range", func(t *testing.T) {
		result, err := store.GetAuditLog(
			baseTime.Add(-3*time.Hour),
			baseTime.Add(1*time.Hour),
		)
		require.NoError(t, err)
		assert.Len(t, result, 3)
	})

	t.Run("get entries in narrow range", func(t *testing.T) {
		result, err := store.GetAuditLog(
			baseTime.Add(-90*time.Minute),
			baseTime.Add(1*time.Hour),
		)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("get no entries", func(t *testing.T) {
		result, err := store.GetAuditLog(
			baseTime.Add(1*time.Hour),
			baseTime.Add(2*time.Hour),
		)
		require.NoError(t, err)
		assert.Len(t, result, 0)
	})
}

// --- Store Interface Compliance ---

func TestBadgerStore_StoreInterface(t *testing.T) {
	store := createTestStore(t)

	// Verify that BadgerStore implements the Store interface
	var _ Store = store
}
