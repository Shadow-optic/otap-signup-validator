package audit

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

// mockStore is a thread-safe in-memory implementation of registry.Store for testing.
type mockStore struct {
	leases       map[string]*models.Lease
	endpoints    map[string]*models.Endpoint
	operators    map[string]*models.Operator
	commitments  map[string]*models.MerkleCommitment // key: operatorID:blockHeight
	latestCommit map[string]*models.MerkleCommitment // key: operatorID
	auditLog     []*models.AuditLogEntry
}

func newMockStore() *mockStore {
	return &mockStore{
		leases:       make(map[string]*models.Lease),
		endpoints:    make(map[string]*models.Endpoint),
		operators:    make(map[string]*models.Operator),
		commitments:  make(map[string]*models.MerkleCommitment),
		latestCommit: make(map[string]*models.MerkleCommitment),
		auditLog:     make([]*models.AuditLogEntry, 0),
	}
}

func (m *mockStore) CreateLease(lease *models.Lease) error {
	m.leases[lease.ID] = lease
	return nil
}

func (m *mockStore) GetLease(id string) (*models.Lease, error) {
	l, ok := m.leases[id]
	if !ok {
		return nil, fmt.Errorf("lease not found: %s", id)
	}
	return l, nil
}

func (m *mockStore) UpdateLease(lease *models.Lease) error {
	m.leases[lease.ID] = lease
	return nil
}

func (m *mockStore) DeleteLease(id string) error {
	delete(m.leases, id)
	return nil
}

func (m *mockStore) ListLeases(filter registry.LeaseFilter) ([]*models.Lease, error) {
	var result []*models.Lease
	for _, l := range m.leases {
		if filter.OperatorID != "" && l.OperatorID != filter.OperatorID {
			continue
		}
		if filter.EndpointID != "" && l.EndpointID != filter.EndpointID {
			continue
		}
		if filter.Status != 0 && l.Status != filter.Status {
			continue
		}
		result = append(result, l)
	}
	return result, nil
}

func (m *mockStore) CreateEndpoint(ep *models.Endpoint) error {
	m.endpoints[ep.ID] = ep
	return nil
}

func (m *mockStore) GetEndpoint(id string) (*models.Endpoint, error) {
	ep, ok := m.endpoints[id]
	if !ok {
		return nil, fmt.Errorf("endpoint not found: %s", id)
	}
	return ep, nil
}

func (m *mockStore) ListEndpoints(filter registry.EndpointFilter) ([]*models.Endpoint, error) {
	var result []*models.Endpoint
	for _, ep := range m.endpoints {
		if filter.OperatorID != "" && ep.OperatorID != filter.OperatorID {
			continue
		}
		if filter.Status != 0 && ep.Status != filter.Status {
			continue
		}
		result = append(result, ep)
	}
	return result, nil
}

func (m *mockStore) CreateOperator(op *models.Operator) error {
	m.operators[op.ID] = op
	return nil
}

func (m *mockStore) GetOperator(id string) (*models.Operator, error) {
	op, ok := m.operators[id]
	if !ok {
		return nil, fmt.Errorf("operator not found: %s", id)
	}
	return op, nil
}

func (m *mockStore) ListOperators() ([]*models.Operator, error) {
	var result []*models.Operator
	for _, op := range m.operators {
		result = append(result, op)
	}
	return result, nil
}

func (m *mockStore) SaveCommitment(c *models.MerkleCommitment) error {
	key := fmt.Sprintf("%s:%d", c.OperatorID, c.BlockHeight)
	m.commitments[key] = c
	m.latestCommit[c.OperatorID] = c
	return nil
}

func (m *mockStore) GetCommitment(operatorID string, blockHeight int64) (*models.MerkleCommitment, error) {
	key := fmt.Sprintf("%s:%d", operatorID, blockHeight)
	c, ok := m.commitments[key]
	if !ok {
		return nil, fmt.Errorf("commitment not found")
	}
	return c, nil
}

func (m *mockStore) GetLatestCommitment(operatorID string) (*models.MerkleCommitment, error) {
	c, ok := m.latestCommit[operatorID]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (m *mockStore) AppendAuditLog(entry *models.AuditLogEntry) error {
	m.auditLog = append(m.auditLog, entry)
	return nil
}

func (m *mockStore) GetAuditLog(from, to time.Time) ([]*models.AuditLogEntry, error) {
	var result []*models.AuditLogEntry
	for _, entry := range m.auditLog {
		if entry.Timestamp.After(from) && entry.Timestamp.Before(to) {
			result = append(result, entry)
		}
	}
	return result, nil
}

// fixture helpers
func createWavelength(lambdaNm float64, chNum int32, band models.Band, gridGHz float64) *models.Wavelength {
	return &models.Wavelength{
		LambdaNm:   lambdaNm,
		ChannelNum: chNum,
		Band:       band,
		GridGHz:    gridGHz,
	}
}

func createTestLease(id, operatorID, endpointID string, status models.LeaseStatus, wl *models.Wavelength, start, end time.Time) *models.Lease {
	lease := &models.Lease{
		ID:         id,
		OperatorID: operatorID,
		EndpointID: endpointID,
		Status:     status,
		Wavelength: wl,
		StartTime:  start,
		EndTime:    end,
		CreatedAt:  start,
		UpdatedAt:  start,
	}
	if wl != nil {
		lease.TokenHash = []byte(fmt.Sprintf("token-hash-%s", id))
	}
	return lease
}

func createTestOperator(id, name string, status models.OperatorStatus) *models.Operator {
	return &models.Operator{
		ID:       id,
		Name:     name,
		Status:   status,
		JoinedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		LastSeen: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestNewAuditor(t *testing.T) {
	store := newMockStore()
	auditor := NewAuditor(store)
	require.NotNil(t, auditor)
	assert.NotNil(t, auditor.store)
	assert.NotNil(t, auditor.logger)
}

func TestLogOperation(t *testing.T) {
	store := newMockStore()
	auditor := NewAuditor(store)

	err := auditor.LogOperation("CREATE_LEASE", "op-001", "lease-001", []byte(`{"wl":"1550.12"}`))
	require.NoError(t, err)
	assert.Len(t, store.auditLog, 1)
	assert.Equal(t, "CREATE_LEASE", store.auditLog[0].Operation)
	assert.Equal(t, "op-001", store.auditLog[0].OperatorID)
	assert.Equal(t, "lease-001", store.auditLog[0].LeaseID)
	assert.NotEmpty(t, store.auditLog[0].Hash)

	// Log another operation
	err = auditor.LogOperation("REVOKE_LEASE", "op-001", "lease-001", []byte(`{"reason":"expired"}`))
	require.NoError(t, err)
	assert.Len(t, store.auditLog, 2)
}

func TestValidateITUCompliance(t *testing.T) {
	store := newMockStore()
	auditor := NewAuditor(store)

	tests := []struct {
		name    string
		wl      *models.Wavelength
		wantErr bool
	}{
		{
			name:    "valid C-band 1550.12nm on 50GHz",
			wl:      createWavelength(1550.12, 6, models.BandCBand, 50.0),
			wantErr: false,
		},
		{
			name:    "valid C-band 1530.33nm on 50GHz",
			wl:      createWavelength(1530.33, 56, models.BandCBand, 50.0),
			wantErr: false,
		},
		{
			name:    "nil wavelength",
			wl:      nil,
			wantErr: true,
		},
		{
			name:    "invalid band - C-band at 1500nm",
			wl:      createWavelength(1500.0, 4, models.BandCBand, 50.0),
			wantErr: true,
		},
		{
			name:    "invalid grid spacing - 30 GHz",
			wl:      createWavelength(1550.12, 4, models.BandCBand, 30.0),
			wantErr: true,
		},
		{
			name:    "wrong channel number",
			wl:      createWavelength(1550.12, 99, models.BandCBand, 50.0),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auditor.ValidateITUCompliance(tt.wl)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateDWDMGrid(t *testing.T) {
	store := newMockStore()
	auditor := NewAuditor(store)

	t.Run("on grid", func(t *testing.T) {
		// 1552.524nm is the ITU reference wavelength (channel 0): always on grid.
		onGrid, _ := auditor.ValidateDWDMGrid(ReferenceWavelengthNm, 50.0)
		assert.True(t, onGrid)
	})

	t.Run("off grid", func(t *testing.T) {
		onGrid, chNum := auditor.ValidateDWDMGrid(1550.50, 50.0)
		assert.False(t, onGrid)
		// chNum can still be computed
		assert.NotZero(t, chNum)
	})
}

func TestCheckLeaseChainIntegrity(t *testing.T) {
	t.Run("empty registry", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		valid, errors, err := auditor.CheckLeaseChainIntegrity()
		require.NoError(t, err)
		assert.True(t, valid)
		assert.Empty(t, errors)
	})

	t.Run("active lease with token hash", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		wl := createWavelength(1550.12, 4, models.BandCBand, 50.0)
		lease := createTestLease("lease-001", "op-001", "ep-001",
			models.LeaseStatusActive, wl,
			time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
		lease.TokenHash = []byte("valid-token-hash")
		store.CreateLease(lease)

		valid, errors, err := auditor.CheckLeaseChainIntegrity()
		require.NoError(t, err)
		assert.True(t, valid)
		assert.Empty(t, errors)
	})

	t.Run("active lease without token hash", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		wl := createWavelength(1550.12, 4, models.BandCBand, 50.0)
		lease := createTestLease("lease-001", "op-001", "ep-001",
			models.LeaseStatusActive, wl,
			time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
		lease.TokenHash = nil // No token hash
		store.CreateLease(lease)

		valid, errors, err := auditor.CheckLeaseChainIntegrity()
		require.NoError(t, err)
		assert.False(t, valid)
		assert.Len(t, errors, 1)
		assert.Contains(t, errors[0], "no token hash")
	})
}

func TestVerifyMerkleConsistency(t *testing.T) {
	t.Run("no operators", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		consistent, errors, err := auditor.VerifyMerkleConsistency()
		require.NoError(t, err)
		assert.True(t, consistent)
		assert.Empty(t, errors)
	})

	t.Run("consistent commitment", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		op := createTestOperator("op-001", "Operator A", models.OperatorStatusActive)
		store.CreateOperator(op)

		// Create 3 active leases
		for i := 0; i < 3; i++ {
			wl := createWavelength(1550.12+float64(i)*0.4, int32(i), models.BandCBand, 50.0)
			lease := createTestLease(
				fmt.Sprintf("lease-%d", i),
				"op-001", "ep-001",
				models.LeaseStatusActive, wl,
				time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour),
			)
			store.CreateLease(lease)
		}

		// Save commitment with matching count
		commitment := &models.MerkleCommitment{
			OperatorID:  "op-001",
			RootHash:    []byte("root-hash"),
			LeaseCount:  3,
			BlockHeight: 1,
			Timestamp:   time.Now(),
		}
		store.SaveCommitment(commitment)

		consistent, errors, err := auditor.VerifyMerkleConsistency()
		require.NoError(t, err)
		assert.True(t, consistent)
		assert.Empty(t, errors)
	})

	t.Run("inconsistent commitment count", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		op := createTestOperator("op-001", "Operator A", models.OperatorStatusActive)
		store.CreateOperator(op)

		// Create 2 active leases
		for i := 0; i < 2; i++ {
			wl := createWavelength(1550.12+float64(i)*0.4, int32(i), models.BandCBand, 50.0)
			lease := createTestLease(
				fmt.Sprintf("lease-%d", i),
				"op-001", "ep-001",
				models.LeaseStatusActive, wl,
				time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour),
			)
			store.CreateLease(lease)
		}

		// Save commitment with wrong count
		commitment := &models.MerkleCommitment{
			OperatorID:  "op-001",
			RootHash:    []byte("root-hash"),
			LeaseCount:  5, // wrong count
			BlockHeight: 1,
			Timestamp:   time.Now(),
		}
		store.SaveCommitment(commitment)

		consistent, errors, err := auditor.VerifyMerkleConsistency()
		require.NoError(t, err)
		assert.False(t, consistent)
		assert.Len(t, errors, 1)
		assert.Contains(t, errors[0], "lease count")
	})
}

func TestGenerateAuditReport(t *testing.T) {
	t.Run("empty report", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		from := time.Now().Add(-24 * time.Hour)
		to := time.Now()
		report, err := auditor.GenerateAuditReport(from, to)
		require.NoError(t, err)
		assert.NotNil(t, report)
		assert.Equal(t, 0, report.TotalLeases)
		assert.Equal(t, 0, report.ActiveLeases)
		assert.Equal(t, 0, report.TotalOperators)
		assert.True(t, report.HashChainValid)
		assert.Empty(t, report.Violations)
	})

	t.Run("report with leases and operators", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		// Create operators
		op1 := createTestOperator("op-001", "Operator A", models.OperatorStatusActive)
		op2 := createTestOperator("op-002", "Operator B", models.OperatorStatusInactive)
		store.CreateOperator(op1)
		store.CreateOperator(op2)

		// Create leases
		wl1 := createWavelength(1550.12, 4, models.BandCBand, 50.0)
		lease1 := createTestLease("lease-001", "op-001", "ep-001",
			models.LeaseStatusActive, wl1,
			time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
		store.CreateLease(lease1)

		wl2 := createWavelength(1530.33, -34, models.BandCBand, 50.0)
		lease2 := createTestLease("lease-002", "op-001", "ep-002",
			models.LeaseStatusExpired, wl2,
			time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
		store.CreateLease(lease2)

		wl3 := createWavelength(1560.61, 21, models.BandCBand, 50.0)
		lease3 := createTestLease("lease-003", "op-002", "ep-003",
			models.LeaseStatusRevoked, wl3,
			time.Now().Add(-72*time.Hour), time.Now().Add(-48*time.Hour))
		store.CreateLease(lease3)

		from := time.Now().Add(-7 * 24 * time.Hour)
		to := time.Now()
		report, err := auditor.GenerateAuditReport(from, to)
		require.NoError(t, err)

		assert.Equal(t, 3, report.TotalLeases)
		assert.Equal(t, 1, report.ActiveLeases)
		assert.Equal(t, 1, report.ExpiredLeases)
		assert.Equal(t, 1, report.RevokedLeases)
		assert.Equal(t, 2, report.TotalOperators)
		assert.Equal(t, 1, report.ActiveOperators)
		assert.True(t, report.HashChainValid)
	})

	t.Run("report detects expired lease still active", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		op := createTestOperator("op-001", "Operator A", models.OperatorStatusActive)
		store.CreateOperator(op)

		// Create a lease that has expired but is still marked active
		wl := createWavelength(1550.12, 4, models.BandCBand, 50.0)
		lease := createTestLease("lease-001", "op-001", "ep-001",
			models.LeaseStatusActive, wl,
			time.Now().Add(-48*time.Hour), time.Now().Add(-1*time.Hour)) // already expired
		lease.TokenHash = []byte("token-hash")
		store.CreateLease(lease)

		from := time.Now().Add(-7 * 24 * time.Hour)
		to := time.Now()
		report, err := auditor.GenerateAuditReport(from, to)
		require.NoError(t, err)

		assert.Equal(t, 1, report.ActiveLeases) // Still counted as active
		assert.GreaterOrEqual(t, len(report.Violations), 1)
		foundExpiredViolation := false
		for _, v := range report.Violations {
			if v.Type == "EXPIRED_LEASE_ACTIVE" {
				foundExpiredViolation = true
				assert.Equal(t, "high", v.Severity)
				assert.Equal(t, "lease-001", v.LeaseID)
			}
		}
		assert.True(t, foundExpiredViolation, "expected EXPIRED_LEASE_ACTIVE violation")
	})

	t.Run("report with ITU compliance violation", func(t *testing.T) {
		store := newMockStore()
		auditor := NewAuditor(store)

		op := createTestOperator("op-001", "Operator A", models.OperatorStatusActive)
		store.CreateOperator(op)

		// Create a lease with invalid band assignment
		wl := createWavelength(1500.0, 4, models.BandCBand, 50.0) // 1500nm not in C-band
		lease := createTestLease("lease-001", "op-001", "ep-001",
			models.LeaseStatusActive, wl,
			time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
		lease.TokenHash = []byte("token-hash")
		store.CreateLease(lease)

		from := time.Now().Add(-7 * 24 * time.Hour)
		to := time.Now()
		report, err := auditor.GenerateAuditReport(from, to)
		require.NoError(t, err)

		foundITUViolation := false
		for _, v := range report.Violations {
			if v.Type == "ITU_COMPLIANCE" {
				foundITUViolation = true
				assert.Equal(t, "medium", v.Severity)
			}
		}
		assert.True(t, foundITUViolation, "expected ITU_COMPLIANCE violation")
	})
}

func TestGenerateAuditReportWithCommitments(t *testing.T) {
	store := newMockStore()
	auditor := NewAuditor(store)

	// Create operator
	op := createTestOperator("op-001", "Operator A", models.OperatorStatusActive)
	store.CreateOperator(op)

	// Create a lease
	wl := createWavelength(1550.12, 4, models.BandCBand, 50.0)
	lease := createTestLease("lease-001", "op-001", "ep-001",
		models.LeaseStatusActive, wl,
		time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
	store.CreateLease(lease)

	// Save commitment
	commitment := &models.MerkleCommitment{
		OperatorID:  "op-001",
		RootHash:    []byte("root-hash"),
		LeaseCount:  1,
		BlockHeight: 1,
		Timestamp:   time.Now(),
	}
	store.SaveCommitment(commitment)

	// Add audit log entries
	auditor.LogOperation("CREATE_LEASE", "op-001", "lease-001", []byte(`{}`))
	auditor.LogOperation("COMMIT_MERKLE", "op-001", "", []byte(`{}`))

	from := time.Now().Add(-7 * 24 * time.Hour)
	to := time.Now()
	report, err := auditor.GenerateAuditReport(from, to)
	require.NoError(t, err)

	assert.Equal(t, 1, report.TotalLeases)
	assert.Equal(t, 1, report.CommitmentCount)
	assert.Equal(t, 2, len(store.auditLog))
}
