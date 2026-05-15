package xlat

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

// mockLeaseStore implements a minimal registry.Store for xlat testing.
type mockLeaseStore struct {
	mu        sync.RWMutex
	leases    map[string]*models.Lease
	operators map[string]*models.Operator
}

func newMockLeaseStore() *mockLeaseStore {
	return &mockLeaseStore{
		leases:    make(map[string]*models.Lease),
		operators: make(map[string]*models.Operator),
	}
}

func (s *mockLeaseStore) CreateLease(lease *models.Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leases[lease.ID] = lease
	return nil
}

func (s *mockLeaseStore) GetLease(id string) (*models.Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leases[id], nil
}

func (s *mockLeaseStore) UpdateLease(lease *models.Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leases[lease.ID] = lease
	return nil
}

func (s *mockLeaseStore) DeleteLease(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.leases, id)
	return nil
}

func (s *mockLeaseStore) ListLeases(filter registry.LeaseFilter) ([]*models.Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*models.Lease
	for _, l := range s.leases {
		if filter.OperatorID != "" && l.OperatorID != filter.OperatorID {
			continue
		}
		if filter.Status != 0 && l.Status != filter.Status {
			continue
		}
		result = append(result, l)
	}
	return result, nil
}

func (s *mockLeaseStore) CreateEndpoint(ep *models.Endpoint) error { return nil }
func (s *mockLeaseStore) GetEndpoint(id string) (*models.Endpoint, error) { return nil, nil }
func (s *mockLeaseStore) ListEndpoints(filter registry.EndpointFilter) ([]*models.Endpoint, error) {
	return nil, nil
}

func (s *mockLeaseStore) CreateOperator(op *models.Operator) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operators[op.ID] = op
	return nil
}

func (s *mockLeaseStore) GetOperator(id string) (*models.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.operators[id], nil
}

func (s *mockLeaseStore) ListOperators() ([]*models.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*models.Operator
	for _, op := range s.operators {
		result = append(result, op)
	}
	return result, nil
}

func (s *mockLeaseStore) SaveCommitment(c *models.MerkleCommitment) error { return nil }
func (s *mockLeaseStore) GetCommitment(operatorID string, blockHeight int64) (*models.MerkleCommitment, error) {
	return nil, nil
}
func (s *mockLeaseStore) GetLatestCommitment(operatorID string) (*models.MerkleCommitment, error) {
	return nil, nil
}

func (s *mockLeaseStore) AppendAuditLog(entry *models.AuditLogEntry) error { return nil }
func (s *mockLeaseStore) GetAuditLog(from, to time.Time) ([]*models.AuditLogEntry, error) {
	return nil, nil
}

func setupTestManager(t *testing.T) (*Manager, *mockLeaseStore) {
	store := newMockLeaseStore()
	mgr := NewManager(store)
	return mgr, store
}

func TestNewManager(t *testing.T) {
	store := newMockLeaseStore()
	mgr := NewManager(store)

	if mgr == nil { t.Fatal("expected non-nil, got nil") }
	if mgr.store == nil { t.Fatal("expected non-nil, got nil") }
}

func TestManager_CreateTranslation(t *testing.T) {
	mgr, _ := setupTestManager(t)

	fromWL := &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 32,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}
	toWL := &models.Wavelength{
		LambdaNm:   1550.52,
		ChannelNum: 34,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}

	entry, err := mgr.CreateTranslation("op-001", "op-002", fromWL, toWL, 1, 2, time.Hour)

	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(entry.ID) == 0 { t.Error("expected non-empty, got empty") }
	if "op-001" != entry.FromOperator { t.Errorf("expected %v, got %v", "op-001", entry.FromOperator) }
	if "op-002" != entry.ToOperator { t.Errorf("expected %v, got %v", "op-002", entry.ToOperator) }
	if int32(1) != entry.FromAWGPort { t.Errorf("expected %v, got %v", int32(1), entry.FromAWGPort) }
	if int32(2) != entry.ToAWGPort { t.Errorf("expected %v, got %v", int32(2), entry.ToAWGPort) }
	if models.TranslationStatusActive != entry.Status { t.Errorf("expected %v, got %v", models.TranslationStatusActive, entry.Status) }
	if entry.FromWavelength == nil { t.Fatal("expected non-nil, got nil") }
	if entry.ToWavelength == nil { t.Fatal("expected non-nil, got nil") }
	if 1550.12 != entry.FromWavelength.LambdaNm { t.Errorf("expected %v, got %v", 1550.12, entry.FromWavelength.LambdaNm) }
	if 1550.52 != entry.ToWavelength.LambdaNm { t.Errorf("expected %v, got %v", 1550.52, entry.ToWavelength.LambdaNm) }
	if entry.ExpiryTime.Before(entry.EffectiveTime) { t.Error("expected ExpiryTime >= EffectiveTime") }
}

func TestManager_CreateTranslation_MissingFromOp(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.CreateTranslation("", "op-002", &models.Wavelength{}, &models.Wavelength{}, 1, 2, time.Hour)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "from_operator") { t.Errorf("expected error containing %q, got %q", "from_operator", err.Error()) }
}

func TestManager_CreateTranslation_MissingToOp(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.CreateTranslation("op-001", "", &models.Wavelength{}, &models.Wavelength{}, 1, 2, time.Hour)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "to_operator") { t.Errorf("expected error containing %q, got %q", "to_operator", err.Error()) }
}

func TestManager_CreateTranslation_NilWavelength(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.CreateTranslation("op-001", "op-002", nil, &models.Wavelength{}, 1, 2, time.Hour)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "wavelengths") { t.Errorf("expected error containing %q, got %q", "wavelengths", err.Error()) }
}

func TestManager_CreateTranslation_ZeroDuration(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.CreateTranslation("op-001", "op-002", &models.Wavelength{}, &models.Wavelength{}, 1, 2, 0)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "duration") { t.Errorf("expected error containing %q, got %q", "duration", err.Error()) }
}

func TestManager_CreateTranslation_InvalidPort(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.CreateTranslation("op-001", "op-002", &models.Wavelength{}, &models.Wavelength{}, 0, 2, time.Hour)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "ports") { t.Errorf("expected error containing %q, got %q", "ports", err.Error()) }
}

func TestManager_CreateTranslation_Conflict(t *testing.T) {
	mgr, store := setupTestManager(t)

	fromWL := &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 32,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}
	toWL := &models.Wavelength{
		LambdaNm:   1550.52,
		ChannelNum: 34,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}

	// Pre-create a conflicting lease on the from-wavelength
	lease := &models.Lease{
		ID:         "lease-1",
		OperatorID: "op-001",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
			GridGHz:    25.0,
		},
	}
	if err := store.CreateLease(lease); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := mgr.CreateTranslation("op-001", "op-002", fromWL, toWL, 1, 2, time.Hour)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "conflict") { t.Errorf("expected error containing %q, got %q", "conflict", err.Error()) }
}

func TestManager_GetTranslation(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.GetTranslation("nonexistent")
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "not found") { t.Errorf("expected error containing %q, got %q", "not found", err.Error()) }
}

func TestManager_GetTranslation_EmptyID(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.GetTranslation("")
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "required") { t.Errorf("expected error containing %q, got %q", "required", err.Error()) }
}

func TestManager_ListTranslations(t *testing.T) {
	mgr, _ := setupTestManager(t)

	entries, err := mgr.ListTranslations(TranslationFilter{})
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(entries) != 0 { t.Errorf("expected empty, got %d items", len(entries)) }
}

func TestManager_ListTranslations_WithFilter(t *testing.T) {
	mgr, _ := setupTestManager(t)

	entries, err := mgr.ListTranslations(TranslationFilter{
		FromOperator: "op-001",
		Status:       models.TranslationStatusActive,
	})
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(entries) != 0 { t.Errorf("expected empty, got %d items", len(entries)) } // No persistent storage yet
}

func TestManager_GenerateAWGTable(t *testing.T) {
	mgr, _ := setupTestManager(t)

	table, err := mgr.GenerateAWGTable("junc-1", "op-001")
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if "junc-1" != table.JunctionID { t.Errorf("expected %v, got %v", "junc-1", table.JunctionID) }
	if "op-001" != table.OperatorID { t.Errorf("expected %v, got %v", "op-001", table.OperatorID) }
	if table.GeneratedAt == nil { t.Fatal("expected non-nil, got nil") }
	if len(table.Entries) != 0 { t.Errorf("expected empty, got %d items", len(table.Entries)) } // No translations yet
	if table.MerkleRoot != nil { t.Errorf("expected nil, got %v", table.MerkleRoot) }
}

func TestManager_GenerateAWGTable_EmptyJunctionID(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.GenerateAWGTable("", "op-001")
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "junction ID") { t.Errorf("expected error containing %q, got %q", "junction ID", err.Error()) }
}

func TestManager_GenerateAWGTable_EmptyOperatorID(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.GenerateAWGTable("junc-1", "")
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "operator ID") { t.Errorf("expected error containing %q, got %q", "operator ID", err.Error()) }
}

func TestManager_GenerateAWGTable_WithEntries(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Create a translation
	fromWL := &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 32,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}
	toWL := &models.Wavelength{
		LambdaNm:   1550.52,
		ChannelNum: 34,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}

	entry, err := mgr.CreateTranslation("op-001", "op-002", fromWL, toWL, 1, 2, time.Hour)
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if entry == nil { t.Fatal("expected non-nil, got nil") }

	// Note: GenerateAWGTable currently uses ListTranslations which returns empty
	// because persistent storage is not implemented. Test the structure anyway.
	table, err := mgr.GenerateAWGTable("junc-1", "op-001")
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if table == nil { t.Fatal("expected non-nil, got nil") }
	if "junc-1" != table.JunctionID { t.Errorf("expected %v, got %v", "junc-1", table.JunctionID) }
}

func TestManager_ValidateTranslation_Valid(t *testing.T) {
	mgr, _ := setupTestManager(t)

	entry := &models.TranslationEntry{
		FromOperator: "op-001",
		ToOperator:   "op-002",
		FromWavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
			GridGHz:    25.0,
		},
		ToWavelength: &models.Wavelength{
			LambdaNm:   1550.52,
			ChannelNum: 34,
			Band:       models.BandCBand,
			GridGHz:    25.0,
		},
		FromAWGPort:   1,
		ToAWGPort:     2,
		EffectiveTime: time.Now().UTC(),
		ExpiryTime:    time.Now().UTC().Add(time.Hour),
	}

	err := mgr.ValidateTranslation(entry)
	if err != nil { t.Fatalf("unexpected error: %v", err) }
}

func TestManager_ValidateTranslation_Nil(t *testing.T) {
	mgr, _ := setupTestManager(t)

	err := mgr.ValidateTranslation(nil)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "nil") { t.Errorf("expected error containing %q, got %q", "nil", err.Error()) }
}

func TestManager_ValidateTranslation_MissingWavelength(t *testing.T) {
	mgr, _ := setupTestManager(t)

	entry := &models.TranslationEntry{
		FromOperator: "op-001",
		ToOperator:   "op-002",
		FromWavelength: nil,
		ToWavelength: &models.Wavelength{
			LambdaNm:   1550.52,
			ChannelNum: 34,
			Band:       models.BandCBand,
		},
		FromAWGPort:   1,
		ToAWGPort:     2,
		EffectiveTime: time.Now().UTC(),
		ExpiryTime:    time.Now().UTC().Add(time.Hour),
	}

	err := mgr.ValidateTranslation(entry)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "wavelengths") { t.Errorf("expected error containing %q, got %q", "wavelengths", err.Error()) }
}

func TestManager_ValidateTranslation_InvalidPort(t *testing.T) {
	mgr, _ := setupTestManager(t)

	entry := &models.TranslationEntry{
		FromOperator: "op-001",
		ToOperator:   "op-002",
		FromWavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
		},
		ToWavelength: &models.Wavelength{
			LambdaNm:   1550.52,
			ChannelNum: 34,
			Band:       models.BandCBand,
		},
		FromAWGPort:   0,
		ToAWGPort:     2,
		EffectiveTime: time.Now().UTC(),
		ExpiryTime:    time.Now().UTC().Add(time.Hour),
	}

	err := mgr.ValidateTranslation(entry)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "ports") { t.Errorf("expected error containing %q, got %q", "ports", err.Error()) }
}

func TestManager_ValidateTranslation_Expired(t *testing.T) {
	mgr, _ := setupTestManager(t)

	entry := &models.TranslationEntry{
		FromOperator: "op-001",
		ToOperator:   "op-002",
		FromWavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
		},
		ToWavelength: &models.Wavelength{
			LambdaNm:   1550.52,
			ChannelNum: 34,
			Band:       models.BandCBand,
		},
		FromAWGPort:   1,
		ToAWGPort:     2,
		EffectiveTime: time.Now().UTC().Add(-2 * time.Hour),
		ExpiryTime:    time.Now().UTC().Add(-time.Hour), // Already expired
	}

	err := mgr.ValidateTranslation(entry)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "expired") { t.Errorf("expected error containing %q, got %q", "expired", err.Error()) }
}

func TestManager_ValidateTranslation_BackwardTime(t *testing.T) {
	mgr, _ := setupTestManager(t)

	entry := &models.TranslationEntry{
		FromOperator: "op-001",
		ToOperator:   "op-002",
		FromWavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
		},
		ToWavelength: &models.Wavelength{
			LambdaNm:   1550.52,
			ChannelNum: 34,
			Band:       models.BandCBand,
		},
		FromAWGPort:   1,
		ToAWGPort:     2,
		EffectiveTime: time.Now().UTC().Add(time.Hour),
		ExpiryTime:    time.Now().UTC(), // Expiry before effective
	}

	err := mgr.ValidateTranslation(entry)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "after effective") { t.Errorf("expected error containing %q, got %q", "after effective", err.Error()) }
}

func TestManager_ValidateTranslation_ConflictingLease(t *testing.T) {
	mgr, store := setupTestManager(t)

	// Create a conflicting lease
	lease := &models.Lease{
		ID:         "lease-1",
		OperatorID: "op-001",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
			GridGHz:    25.0,
		},
	}
	if err := store.CreateLease(lease); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := &models.TranslationEntry{
		FromOperator: "op-001",
		ToOperator:   "op-002",
		FromWavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
			GridGHz:    25.0,
		},
		ToWavelength: &models.Wavelength{
			LambdaNm:   1550.52,
			ChannelNum: 34,
			Band:       models.BandCBand,
			GridGHz:    25.0,
		},
		FromAWGPort:   1,
		ToAWGPort:     2,
		EffectiveTime: time.Now().UTC(),
		ExpiryTime:    time.Now().UTC().Add(time.Hour),
	}

	err := mgr.ValidateTranslation(entry)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "conflict") { t.Errorf("expected error containing %q, got %q", "conflict", err.Error()) }
}

func TestManager_ExportForAWG(t *testing.T) {
	mgr, _ := setupTestManager(t)

	data, err := mgr.ExportForAWG("junc-1", "op-001")
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if data == nil { t.Fatal("expected non-nil, got nil") }

	// Verify it's valid JSON
	var table AWGRoutingTable
	err = json.Unmarshal(data, &table)
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if "junc-1" != table.JunctionID { t.Errorf("expected %v, got %v", "junc-1", table.JunctionID) }
	if "op-001" != table.OperatorID { t.Errorf("expected %v, got %v", "op-001", table.OperatorID) }
	if len(table.Entries) != 0 { t.Errorf("expected empty, got %d items", len(table.Entries)) }
}

func TestManager_ExportForAWG_EmptyJunction(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.ExportForAWG("", "op-001")
	if err == nil { t.Error("expected error, got nil") }
}

func TestManager_ExportForAWG_EmptyOperator(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.ExportForAWG("junc-1", "")
	if err == nil { t.Error("expected error, got nil") }
}

func TestManager_ExportForAWG_RoundTrip(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Create a translation
	fromWL := &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 32,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}
	toWL := &models.Wavelength{
		LambdaNm:   1550.52,
		ChannelNum: 34,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}

	_, err := mgr.CreateTranslation("op-001", "op-002", fromWL, toWL, 1, 2, time.Hour)
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	// Export and verify JSON structure
	data, err := mgr.ExportForAWG("junc-1", "op-001")
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	var table AWGRoutingTable
	err = json.Unmarshal(data, &table)
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	if "junc-1" != table.JunctionID { t.Errorf("expected %v, got %v", "junc-1", table.JunctionID) }
	if "op-001" != table.OperatorID { t.Errorf("expected %v, got %v", "op-001", table.OperatorID) }
	assert.NotZero(t, table.GeneratedAt)
}

func TestWavelengthsEqual(t *testing.T) {
	a := &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 32,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}
	b := &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 32,
		Band:       models.BandCBand,
		GridGHz:    25.0,
	}
	if !wavelengthsEqual(a, b { t.Error("expected true, got false") })
}

func TestWavelengthsEqual_DifferentLambda(t *testing.T) {
	a := &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 32,
		Band:       models.BandCBand,
	}
	b := &models.Wavelength{
		LambdaNm:   1550.52,
		ChannelNum: 32,
		Band:       models.BandCBand,
	}
	if wavelengthsEqual(a, b { t.Error("expected false, got true") })
}

func TestWavelengthsEqual_Nil(t *testing.T) {
	a := &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 32,
		Band:       models.BandCBand,
	}
	if wavelengthsEqual(a, nil { t.Error("expected false, got true") })
	if wavelengthsEqual(nil, a { t.Error("expected false, got true") })
	if wavelengthsEqual(nil, nil { t.Error("expected false, got true") })
}

func TestComputeMerkleRoot(t *testing.T) {
	entries := []AWGRoutingEntry{
		{InputPort: 1, InputLambda: 1550.12, OutputPort: 2, OutputLambda: 1550.52, DestinationOp: "op-002", Active: true},
		{InputPort: 3, InputLambda: 1550.92, OutputPort: 4, OutputLambda: 1551.32, DestinationOp: "op-003", Active: true},
	}

	root1 := computeMerkleRoot(entries)
	if root1 == nil { t.Fatalf("expected non-nil, got nil") }
	if len(root1) != 32 { t.Errorf("expected length %d, got %d", 32, len(root1)) } // SHA-256 hash size

	// Same entries should produce same root
	root2 := computeMerkleRoot(entries)
	if root1 != root2 { t.Errorf("expected %v, got %v", root1, root2) }

	// Different entries should produce different root
	entries[0].InputPort = 99
	root3 := computeMerkleRoot(entries)
	assert.NotEqual(t, root1, root3)
}

func TestComputeMerkleRoot_Empty(t *testing.T) {
	root := computeMerkleRoot([]AWGRoutingEntry{})
	if root != nil { t.Errorf("expected nil, got %v", root) }
}

func TestComputeMerkleRoot_SingleEntry(t *testing.T) {
	entries := []AWGRoutingEntry{
		{InputPort: 1, InputLambda: 1550.12, OutputPort: 2, OutputLambda: 1550.52},
	}
	root := computeMerkleRoot(entries)
	if root == nil { t.Fatalf("expected non-nil, got nil") }
	if len(root) != 32 { t.Errorf("expected length %d, got %d", 32, len(root)) }
}

func TestComputeMerkleRoot_ThreeEntries(t *testing.T) {
	// Odd number tests the promotion path
	entries := []AWGRoutingEntry{
		{InputPort: 1, InputLambda: 1550.12},
		{InputPort: 2, InputLambda: 1550.52},
		{InputPort: 3, InputLambda: 1550.92},
	}
	root := computeMerkleRoot(entries)
	if root == nil { t.Fatalf("expected non-nil, got nil") }
	if len(root) != 32 { t.Errorf("expected length %d, got %d", 32, len(root)) }
}

func TestAWGRoutingEntry_JSONRoundTrip(t *testing.T) {
	entry := AWGRoutingEntry{
		InputPort:     1,
		InputLambda:   1550.12,
		OutputPort:    2,
		OutputLambda:  1550.52,
		DestinationOp: "op-002",
		Active:        true,
	}

	data, err := json.Marshal(entry)
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	var decoded AWGRoutingEntry
	err = json.Unmarshal(data, &decoded)
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	if entry.InputPort != decoded.InputPort { t.Errorf("expected %v, got %v", entry.InputPort, decoded.InputPort) }
	if entry.InputLambda != decoded.InputLambda { t.Errorf("expected %v, got %v", entry.InputLambda, decoded.InputLambda) }
	if entry.OutputPort != decoded.OutputPort { t.Errorf("expected %v, got %v", entry.OutputPort, decoded.OutputPort) }
	if entry.OutputLambda != decoded.OutputLambda { t.Errorf("expected %v, got %v", entry.OutputLambda, decoded.OutputLambda) }
	if entry.DestinationOp != decoded.DestinationOp { t.Errorf("expected %v, got %v", entry.DestinationOp, decoded.DestinationOp) }
	if entry.Active != decoded.Active { t.Errorf("expected %v, got %v", entry.Active, decoded.Active) }
}

func TestAWGRoutingTable_JSONRoundTrip(t *testing.T) {
	table := AWGRoutingTable{
		JunctionID:  "junc-1",
		GeneratedAt: time.Now().UTC(),
		OperatorID:  "op-001",
		Entries: []AWGRoutingEntry{
			{InputPort: 1, InputLambda: 1550.12, OutputPort: 2, OutputLambda: 1550.52, DestinationOp: "op-002", Active: true},
		},
		MerkleRoot: []byte("root-hash"),
		Signature:  []byte("signature"),
	}

	data, err := json.Marshal(table)
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	var decoded AWGRoutingTable
	err = json.Unmarshal(data, &decoded)
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	if table.JunctionID != decoded.JunctionID { t.Errorf("expected %v, got %v", table.JunctionID, decoded.JunctionID) }
	if table.OperatorID != decoded.OperatorID { t.Errorf("expected %v, got %v", table.OperatorID, decoded.OperatorID) }
	if len(decoded.Entries) != 1 { t.Errorf("expected length %d, got %d", 1, len(decoded.Entries)) }
	if table.Entries[0].InputPort != decoded.Entries[0].InputPort { t.Errorf("expected %v, got %v", table.Entries[0].InputPort, decoded.Entries[0].InputPort) }
	if table.MerkleRoot != decoded.MerkleRoot { t.Errorf("expected %v, got %v", table.MerkleRoot, decoded.MerkleRoot) }
	if table.Signature != decoded.Signature { t.Errorf("expected %v, got %v", table.Signature, decoded.Signature) }
}

func TestTranslationFilter_Usage(t *testing.T) {
	filter := TranslationFilter{
		FromOperator: "op-001",
		ToOperator:   "op-002",
		Status:       models.TranslationStatusActive,
	}

	if "op-001" != filter.FromOperator { t.Errorf("expected %v, got %v", "op-001", filter.FromOperator) }
	if "op-002" != filter.ToOperator { t.Errorf("expected %v, got %v", "op-002", filter.ToOperator) }
	if models.TranslationStatusActive != filter.Status { t.Errorf("expected %v, got %v", models.TranslationStatusActive, filter.Status) }
}

func TestManager_CreateTranslation_ManyTranslations(t *testing.T) {
	mgr, _ := setupTestManager(t)

	for i := 0; i < 50; i++ {
		fromWL := &models.Wavelength{
			LambdaNm:   1550.12 + float64(i)*0.4,
			ChannelNum: int32(32 + i),
			Band:       models.BandCBand,
			GridGHz:    25.0,
		}
		toWL := &models.Wavelength{
			LambdaNm:   1550.52 + float64(i)*0.4,
			ChannelNum: int32(34 + i),
			Band:       models.BandCBand,
			GridGHz:    25.0,
		}

		entry, err := mgr.CreateTranslation(
			fmt.Sprintf("op-%03d", i),
			fmt.Sprintf("op-%03d", i+1),
			fromWL, toWL,
			int32(i), int32(i+1),
			time.Hour,
		)
		if err != nil { t.Fatalf("unexpected error: %v", err) }
		if len(entry.ID) == 0 { t.Error("expected non-empty, got empty") }
		if models.TranslationStatusActive != entry.Status { t.Errorf("expected %v, got %v", models.TranslationStatusActive, entry.Status) }
	}
}

func TestManager_ConcurrentCreateTranslation(t *testing.T) {
	mgr, _ := setupTestManager(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			fromWL := &models.Wavelength{
				LambdaNm:   1550.12 + float64(idx)*0.4,
				ChannelNum: int32(32 + idx),
				Band:       models.BandCBand,
				GridGHz:    25.0,
			}
			toWL := &models.Wavelength{
				LambdaNm:   1550.52 + float64(idx)*0.4,
				ChannelNum: int32(34 + idx),
				Band:       models.BandCBand,
				GridGHz:    25.0,
			}

			_, err := mgr.CreateTranslation(
				fmt.Sprintf("op-%03d", idx),
				fmt.Sprintf("op-%03d", idx+1),
				fromWL, toWL,
				int32(idx), int32(idx+1),
				time.Hour,
			)
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()
}
