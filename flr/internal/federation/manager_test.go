package federation

import (
	"encoding/json"
	"fmt"
	"strings"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/otap/flr/internal/crypto"
	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

// mockStore implements registry.Store for testing.
type mockStore struct {
	mu           sync.RWMutex
	leases       map[string]*models.Lease
	operators    map[string]*models.Operator
	commitments  map[string]*models.MerkleCommitment
	auditLog     []*models.AuditLogEntry
	listLeasesFn func(filter registry.LeaseFilter) ([]*models.Lease, error)
}

func newMockStore() *mockStore {
	return &mockStore{
		leases:      make(map[string]*models.Lease),
		operators:   make(map[string]*models.Operator),
		commitments: make(map[string]*models.MerkleCommitment),
		auditLog:    make([]*models.AuditLogEntry, 0),
	}
}

func (s *mockStore) CreateLease(lease *models.Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leases[lease.ID] = lease
	return nil
}

func (s *mockStore) GetLease(id string) (*models.Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leases[id], nil
}

func (s *mockStore) UpdateLease(lease *models.Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leases[lease.ID] = lease
	return nil
}

func (s *mockStore) DeleteLease(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.leases, id)
	return nil
}

func (s *mockStore) ListLeases(filter registry.LeaseFilter) ([]*models.Lease, error) {
	if s.listLeasesFn != nil {
		return s.listLeasesFn(filter)
	}
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

func (s *mockStore) CreateEndpoint(ep *models.Endpoint) error  { return nil }
func (s *mockStore) GetEndpoint(id string) (*models.Endpoint, error) { return nil, nil }
func (s *mockStore) ListEndpoints(filter registry.EndpointFilter) ([]*models.Endpoint, error) {
	return nil, nil
}

func (s *mockStore) CreateOperator(op *models.Operator) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operators[op.ID] = op
	return nil
}

func (s *mockStore) GetOperator(id string) (*models.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.operators[id], nil
}

func (s *mockStore) ListOperators() ([]*models.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*models.Operator
	for _, op := range s.operators {
		result = append(result, op)
	}
	return result, nil
}

func (s *mockStore) SaveCommitment(c *models.MerkleCommitment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s-%d", c.OperatorID, c.BlockHeight)
	s.commitments[key] = c
	return nil
}

func (s *mockStore) GetCommitment(operatorID string, blockHeight int64) (*models.MerkleCommitment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := fmt.Sprintf("%s-%d", operatorID, blockHeight)
	return s.commitments[key], nil
}

func (s *mockStore) GetLatestCommitment(operatorID string) (*models.MerkleCommitment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest *models.MerkleCommitment
	for _, c := range s.commitments {
		if c.OperatorID == operatorID {
			if latest == nil || c.BlockHeight > latest.BlockHeight {
				latest = c
			}
		}
	}
	return latest, nil
}

func (s *mockStore) AppendAuditLog(entry *models.AuditLogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLog = append(s.auditLog, entry)
	return nil
}

func (s *mockStore) GetAuditLog(from, to time.Time) ([]*models.AuditLogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.auditLog, nil
}

// newTestServer creates a configurable HTTP mock server for federation tests.
// The handlerFunc can inspect requests and send back appropriate responses.
type testPeer struct {
	server       *httptest.Server
	leases       []*models.Lease
	commitments  map[string]*models.MerkleCommitment
	registerOp   *models.Operator
	translationTable []*models.TranslationEntry
}

func newTestPeer() *testPeer {
	p := &testPeer{
		commitments: make(map[string]*models.MerkleCommitment),
	}
	p.server = httptest.NewServer(http.HandlerFunc(p.handler))
	return p
}

func (p *testPeer) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/leases":
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(p.leases)
	case r.URL.Path == "/v1/commitments" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusCreated)
	case r.URL.Path == "/v1/commitments/op-002/0":
		c := &models.MerkleCommitment{
			OperatorID:  "op-002",
			RootHash:    []byte("root-hash"),
			BlockHeight: 42,
			LeaseCount:  10,
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(c)
	case r.URL.Path == "/v1/commitments/op-003/0":
		c := &models.MerkleCommitment{
			OperatorID:  "op-003",
			RootHash:    []byte("root-hash"),
			BlockHeight: 42,
			LeaseCount:  10,
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(c)
	case r.URL.Path == "/v1/operators" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusCreated)
	case r.URL.Path == "/v1/invalidity" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusAccepted)
	case r.URL.Path == "/v1/translations":
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(p.translationTable)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (p *testPeer) URL() string {
	return p.server.URL
}

func (p *testPeer) Close() {
	p.server.Close()
}

func setupTestManager(t *testing.T) (*Manager, *mockStore) {
	store := newMockStore()
	cryptoEng, err := crypto.NewEngine("op-001")
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	regEngine := registry.NewEngine(store, "op-001")
	client := NewClient(5 * time.Second)
	mgr := NewManager(regEngine, cryptoEng, client, "op-001")
	return mgr, store
}

func TestNewManager(t *testing.T) {
	cryptoEng, err := crypto.NewEngine("op-001")
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	store := newMockStore()
	regEngine := registry.NewEngine(store, "op-001")
	client := NewClient(5 * time.Second)
	mgr := NewManager(regEngine, cryptoEng, client, "op-001")

	if mgr == nil { t.Fatal("expected non-nil, got nil") }
	if "op-001" != mgr.operatorID { t.Errorf("expected %v, got %v", "op-001", mgr.operatorID) }
	if mgr.operators == nil { t.Fatal("expected non-nil, got nil") }
	if len(mgr.ListOperators() != 0 { t.Errorf("expected empty, got %d items", len(mgr.ListOperators()) })
}

func TestManager_RegisterOperator(t *testing.T) {
	mgr, store := setupTestManager(t)

	op := &models.Operator{
		ID:        "op-002",
		Name:      "Operator B",
		Endpoint:  "https://op-b.example.com",
		PublicKey: []byte("pubkey"),
	}

	err := mgr.RegisterOperator(op)
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	result, err := mgr.GetOperator("op-002")
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if "op-002" != result.ID { t.Errorf("expected %v, got %v", "op-002", result.ID) }
	if "Operator B" != result.Name { t.Errorf("expected %v, got %v", "Operator B", result.Name) }
	if models.OperatorStatusActive != result.Status { t.Errorf("expected %v, got %v", models.OperatorStatusActive, result.Status) }

	stored, _ := store.GetOperator("op-002")
	if stored == nil { t.Fatalf("expected non-nil, got nil") }
	if "Operator B" != stored.Name { t.Errorf("expected %v, got %v", "Operator B", stored.Name) }
}

func TestManager_RegisterOperator_Nil(t *testing.T) {
	mgr, _ := setupTestManager(t)

	err := mgr.RegisterOperator(nil)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "nil") { t.Errorf("expected error containing %q, got %q", "nil", err.Error()) }
}

func TestManager_RegisterOperator_MissingID(t *testing.T) {
	mgr, _ := setupTestManager(t)

	err := mgr.RegisterOperator(&models.Operator{
		Name:     "Operator B",
		Endpoint: "https://op-b.example.com",
	})
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "ID") { t.Errorf("expected error containing %q, got %q", "ID", err.Error()) }
}

func TestManager_RegisterOperator_MissingEndpoint(t *testing.T) {
	mgr, _ := setupTestManager(t)

	err := mgr.RegisterOperator(&models.Operator{
		ID:   "op-002",
		Name: "Operator B",
	})
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "endpoint") { t.Errorf("expected error containing %q, got %q", "endpoint", err.Error()) }
}

func TestManager_RegisterOperator_Duplicate(t *testing.T) {
	mgr, _ := setupTestManager(t)

	op := &models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "https://op-b.example.com",
	}

	if mgr.RegisterOperator(op != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(op) })
	err := mgr.RegisterOperator(op)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "already registered") { t.Errorf("expected error containing %q, got %q", "already registered", err.Error()) }
}

func TestManager_GetOperator_NotFound(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.GetOperator("op-missing")
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "not found") { t.Errorf("expected error containing %q, got %q", "not found", err.Error()) }
}

func TestManager_ListOperators(t *testing.T) {
	mgr, _ := setupTestManager(t)

	ops := mgr.ListOperators()
	if len(ops) != 0 { t.Errorf("expected empty, got %d items", len(ops)) }

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "https://op-b.example.com",
	} != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "https://op-b.example.com",
	}) })
	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-003",
		Name:     "Operator C",
		Endpoint: "https://op-c.example.com",
	} != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-003",
		Name:     "Operator C",
		Endpoint: "https://op-c.example.com",
	}) })

	ops = mgr.ListOperators()
	if len(ops) != 2 { t.Errorf("expected length %d, got %d", 2, len(ops)) }
}

func TestManager_RemoveOperator(t *testing.T) {
	mgr, _ := setupTestManager(t)

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "https://op-b.example.com",
	} != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "https://op-b.example.com",
	}) })

	err := mgr.RemoveOperator("op-002")
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	_, err = mgr.GetOperator("op-002")
	if err == nil { t.Error("expected error, got nil") }
}

func TestManager_RemoveOperator_Self(t *testing.T) {
	mgr, _ := setupTestManager(t)

	err := mgr.RemoveOperator("op-001")
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "self") { t.Errorf("expected error containing %q, got %q", "self", err.Error()) }
}

func TestManager_RemoveOperator_NotFound(t *testing.T) {
	mgr, _ := setupTestManager(t)

	err := mgr.RemoveOperator("op-missing")
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "not found") { t.Errorf("expected error containing %q, got %q", "not found", err.Error()) }
}

func TestManager_DetectConflicts_DoubleAllocation(t *testing.T) {
	mgr, store := setupTestManager(t)
	peer := newTestPeer()
	defer peer.Close()

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL( != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL() },
	}))

	localLease := &models.Lease{
		ID:         "lease-1",
		OperatorID: "op-001",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
		},
		StartTime: time.Now().UTC().Add(-time.Hour),
		EndTime:   time.Now().UTC().Add(time.Hour),
	}
	if store.CreateLease(localLease != nil { t.Fatalf("unexpected error: %v", store.CreateLease(localLease) })

	remoteLease := &models.Lease{
		ID:         "lease-2",
		OperatorID: "op-002",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
		},
		StartTime: time.Now().UTC().Add(-time.Hour),
		EndTime:   time.Now().UTC().Add(time.Hour),
	}
	peer.leases = []*models.Lease{remoteLease}

	store.listLeasesFn = func(filter registry.LeaseFilter) ([]*models.Lease, error) {
		if filter.OperatorID == "op-001" {
			return []*models.Lease{localLease}, nil
		}
		return nil, nil
	}

	proofs, err := mgr.DetectConflicts()
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(proofs) != 1 { t.Errorf("expected length %d, got %d", 1, len(proofs)) }
	if models.InvalidityDoubleAllocation != proofs[0].Type { t.Errorf("expected %v, got %v", models.InvalidityDoubleAllocation, proofs[0].Type) }
	if "lease-1" != proofs[0].LeaseA.ID { t.Errorf("expected %v, got %v", "lease-1", proofs[0].LeaseA.ID) }
	if "lease-2" != proofs[0].LeaseB.ID { t.Errorf("expected %v, got %v", "lease-2", proofs[0].LeaseB.ID) }
}

func TestManager_DetectConflicts_NoOverlap(t *testing.T) {
	mgr, store := setupTestManager(t)
	peer := newTestPeer()
	defer peer.Close()

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL( != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL() },
	}))

	localLease := &models.Lease{
		ID:         "lease-1",
		OperatorID: "op-001",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
		},
		StartTime: time.Now().UTC().Add(-time.Hour),
		EndTime:   time.Now().UTC().Add(time.Hour),
	}
	if store.CreateLease(localLease != nil { t.Fatalf("unexpected error: %v", store.CreateLease(localLease) })

	remoteLease := &models.Lease{
		ID:         "lease-2",
		OperatorID: "op-002",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.52,
			ChannelNum: 34,
			Band:       models.BandCBand,
		},
		StartTime: time.Now().UTC().Add(-time.Hour),
		EndTime:   time.Now().UTC().Add(time.Hour),
	}
	peer.leases = []*models.Lease{remoteLease}

	store.listLeasesFn = func(filter registry.LeaseFilter) ([]*models.Lease, error) {
		if filter.OperatorID == "op-001" {
			return []*models.Lease{localLease}, nil
		}
		return nil, nil
	}

	proofs, err := mgr.DetectConflicts()
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(proofs) != 0 { t.Errorf("expected empty, got %d items", len(proofs)) }
}

func TestManager_DetectConflicts_NoPeers(t *testing.T) {
	mgr, _ := setupTestManager(t)

	proofs, err := mgr.DetectConflicts()
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(proofs) != 0 { t.Errorf("expected empty, got %d items", len(proofs)) }
}

func TestManager_DetectConflicts_PeerOffline(t *testing.T) {
	mgr, store := setupTestManager(t)

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "http://127.0.0.1:1", // Should fail
	} != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "http://127.0.0.1:1", // Should fail
	}) })

	store.listLeasesFn = func(filter registry.LeaseFilter) ([]*models.Lease, error) {
		if filter.OperatorID == "op-001" {
			return []*models.Lease{}, nil
		}
		return nil, nil
	}

	proofs, err := mgr.DetectConflicts()
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(proofs) != 0 { t.Errorf("expected empty, got %d items", len(proofs)) } // Should not error, just return empty
}

func TestManager_HandleProofOfInvalidity_DoubleAllocation(t *testing.T) {
	mgr, store := setupTestManager(t)

	leaseA := &models.Lease{
		ID:         "lease-1",
		OperatorID: "op-001",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
		},
		StartTime: time.Now().UTC().Add(-time.Hour),
		EndTime:   time.Now().UTC().Add(time.Hour),
	}
	leaseB := &models.Lease{
		ID:         "lease-2",
		OperatorID: "op-002",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
		},
		StartTime: time.Now().UTC().Add(-time.Hour),
		EndTime:   time.Now().UTC().Add(time.Hour),
	}

	poi := &models.ProofOfInvalidity{
		Type:      models.InvalidityDoubleAllocation,
		LeaseA:    leaseA,
		LeaseB:    leaseB,
		Timestamp: time.Now().UTC(),
	}

	err := mgr.HandleProofOfInvalidity(poi)
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	logs, _ := store.GetAuditLog(time.Time{}, time.Time{})
	if len(logs) != 1 { t.Errorf("expected length %d, got %d", 1, len(logs)) }
	assert.Contains(t, logs[0].Operation, "PROOF_OF_INVALIDITY")
	assert.Contains(t, logs[0].Operation, "DOUBLE_ALLOCATION")
}

func TestManager_HandleProofOfInvalidity_ExpiredLease(t *testing.T) {
	mgr, _ := setupTestManager(t)

	leaseA := &models.Lease{
		ID:         "lease-1",
		OperatorID: "op-001",
		Status:     models.LeaseStatusActive,
		EndTime:    time.Now().UTC().Add(-time.Hour),
	}

	poi := &models.ProofOfInvalidity{
		Type:      models.InvalidityExpiredLease,
		LeaseA:    leaseA,
		Timestamp: time.Now().UTC(),
	}

	err := mgr.HandleProofOfInvalidity(poi)
	if err != nil { t.Fatalf("unexpected error: %v", err) }
}

func TestManager_HandleProofOfInvalidity_Nil(t *testing.T) {
	mgr, _ := setupTestManager(t)

	err := mgr.HandleProofOfInvalidity(nil)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "nil") { t.Errorf("expected error containing %q, got %q", "nil", err.Error()) }
}

func TestManager_HandleProofOfInvalidity_InvalidDoubleAlloc(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Different wavelengths — no double allocation
	leaseA := &models.Lease{
		ID:         "lease-1",
		OperatorID: "op-001",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 32,
			Band:       models.BandCBand,
		},
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(time.Hour),
	}
	leaseB := &models.Lease{
		ID:         "lease-2",
		OperatorID: "op-002",
		Status:     models.LeaseStatusActive,
		Wavelength: &models.Wavelength{
			LambdaNm:   1550.52,
			ChannelNum: 34,
			Band:       models.BandCBand,
		},
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(time.Hour),
	}

	poi := &models.ProofOfInvalidity{
		Type:      models.InvalidityDoubleAllocation,
		LeaseA:    leaseA,
		LeaseB:    leaseB,
		Timestamp: time.Now().UTC(),
	}

	err := mgr.HandleProofOfInvalidity(poi)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "do not form") { t.Errorf("expected error containing %q, got %q", "do not form", err.Error()) }
}

func TestManager_HandleProofOfInvalidity_InvalidSignature(t *testing.T) {
	mgr, _ := setupTestManager(t)

	poi := &models.ProofOfInvalidity{
		Type: models.InvalidityInvalidSignature,
		Commitment: &models.MerkleCommitment{
			OperatorID: "op-002",
			RootHash:   []byte("root"),
		},
		Timestamp: time.Now().UTC(),
	}

	err := mgr.HandleProofOfInvalidity(poi)
	if err != nil { t.Fatalf("unexpected error: %v", err) }
}

func TestManager_HandleProofOfInvalidity_InvalidSignature_NoCommitment(t *testing.T) {
	mgr, _ := setupTestManager(t)

	poi := &models.ProofOfInvalidity{
		Type:      models.InvalidityInvalidSignature,
		Timestamp: time.Now().UTC(),
	}

	err := mgr.HandleProofOfInvalidity(poi)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "commitment") { t.Errorf("expected error containing %q, got %q", "commitment", err.Error()) }
}

func TestManager_HandleProofOfInvalidity_UnknownType(t *testing.T) {
	mgr, _ := setupTestManager(t)

	poi := &models.ProofOfInvalidity{
		Type:      models.InvalidityType(999),
		LeaseA:    &models.Lease{ID: "lease-1"},
		Timestamp: time.Now().UTC(),
	}

	err := mgr.HandleProofOfInvalidity(poi)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "unknown") { t.Errorf("expected error containing %q, got %q", "unknown", err.Error()) }
}

func TestManager_PushCommitmentToAll(t *testing.T) {
	mgr, _ := setupTestManager(t)
	peerB := newTestPeer()
	defer peerB.Close()
	peerC := newTestPeer()
	defer peerC.Close()

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peerB.URL( != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peerB.URL() },
	}))
	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-003",
		Name:     "Operator C",
		Endpoint: peerC.URL( != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-003",
		Name:     "Operator C",
		Endpoint: peerC.URL() },
	}))

	commitment := &models.MerkleCommitment{
		OperatorID:  "op-001",
		RootHash:    []byte("root"),
		BlockHeight: 10,
	}

	err := mgr.PushCommitmentToAll(commitment)
	if err != nil { t.Fatalf("unexpected error: %v", err) }
}

func TestManager_PushCommitmentToAll_Nil(t *testing.T) {
	mgr, _ := setupTestManager(t)

	err := mgr.PushCommitmentToAll(nil)
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "nil") { t.Errorf("expected error containing %q, got %q", "nil", err.Error()) }
}

func TestManager_PushCommitmentToAll_NoPeers(t *testing.T) {
	mgr, _ := setupTestManager(t)

	commitment := &models.MerkleCommitment{
		OperatorID:  "op-001",
		RootHash:    []byte("root"),
		BlockHeight: 10,
	}

	err := mgr.PushCommitmentToAll(commitment)
	if err != nil { t.Fatalf("unexpected error: %v", err) }
}

func TestManager_PushCommitmentToAll_InactivePeer(t *testing.T) {
	mgr, _ := setupTestManager(t)
	peer := newTestPeer()
	defer peer.Close()

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL( != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL() },
	}))

	// Mark peer as inactive
	mgr.mu.Lock()
	mgr.operators["op-002"].Status = models.OperatorStatusInactive
	mgr.mu.Unlock()

	commitment := &models.MerkleCommitment{
		OperatorID:  "op-001",
		RootHash:    []byte("root"),
		BlockHeight: 10,
	}

	// Should not error even though peer is inactive
	err := mgr.PushCommitmentToAll(commitment)
	if err != nil { t.Fatalf("unexpected error: %v", err) }
}

func TestManager_GetTranslationTable(t *testing.T) {
	mgr, _ := setupTestManager(t)
	peer := newTestPeer()
	defer peer.Close()

	peer.translationTable = []*models.TranslationEntry{
		{
			ID:           "tr-1",
			FromOperator: "op-001",
			ToOperator:   "op-002",
		},
	}

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL( != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL() },
	}))

	entries, err := mgr.GetTranslationTable("op-002")
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(entries) != 1 { t.Errorf("expected length %d, got %d", 1, len(entries)) }
	if "tr-1" != entries[0].ID { t.Errorf("expected %v, got %v", "tr-1", entries[0].ID) }
}

func TestManager_GetTranslationTable_NotFound(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.GetTranslationTable("op-missing")
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "not found") { t.Errorf("expected error containing %q, got %q", "not found", err.Error()) }
}

func TestManager_StartAndStopGossip(t *testing.T) {
	mgr, _ := setupTestManager(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.StartGossip(ctx, 100*time.Millisecond)

	// Let it run for a bit
	time.Sleep(250 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		mgr.StopGossip()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("StopGossip timed out")
	}
}

func TestManager_StartAndStopGossip_Multiple(t *testing.T) {
	mgr, _ := setupTestManager(t)

	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		mgr.StartGossip(ctx, 100*time.Millisecond)
		time.Sleep(100 * time.Millisecond)
		mgr.StopGossip()
		cancel()
	}
	// Should not panic or deadlock
}

func TestManager_SyncWithOperator(t *testing.T) {
	mgr, store := setupTestManager(t)
	peer := newTestPeer()
	defer peer.Close()

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL( != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: peer.URL() },
	}))

	// Pre-create a commitment in our store
	commitment := &models.MerkleCommitment{
		OperatorID:  "op-001",
		RootHash:    []byte("root"),
		BlockHeight: 5,
	}
	if store.SaveCommitment(commitment != nil { t.Fatalf("unexpected error: %v", store.SaveCommitment(commitment) })

	store.listLeasesFn = func(filter registry.LeaseFilter) ([]*models.Lease, error) {
		if filter.OperatorID == "op-001" {
			return []*models.Lease{}, nil
		}
		return nil, nil
	}

	err := mgr.SyncWithOperator("op-002")
	if err != nil { t.Fatalf("unexpected error: %v", err) }
}

func TestManager_SyncWithOperator_NotFound(t *testing.T) {
	mgr, _ := setupTestManager(t)

	err := mgr.SyncWithOperator("op-missing")
	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "not found") { t.Errorf("expected error containing %q, got %q", "not found", err.Error()) }
}

func TestManager_ConcurrentAccess(t *testing.T) {
	mgr, _ := setupTestManager(t)

	if mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "https://op-b.example.com",
	} != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "https://op-b.example.com",
	}) })

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, _ = mgr.GetOperator("op-002")
		}()
		go func() {
			defer wg.Done()
			_ = mgr.ListOperators()
		}()
		go func() {
			defer wg.Done()
			_ = mgr.RegisterOperator(&models.Operator{
				ID:       fmt.Sprintf("op-%d", i+100),
				Name:     fmt.Sprintf("Operator %d", i),
				Endpoint: fmt.Sprintf("https://op-%d.example.com", i),
			})
		}()
	}
	wg.Wait()

	ops := mgr.ListOperators()
	if len(ops) < 1 { t.Errorf("expected %v >= %v", len(ops), 1) }
}

func TestManager_ConcurrentRemove(t *testing.T) {
	mgr, _ := setupTestManager(t)

	for i := 0; i < 10; i++ {
		if mgr.RegisterOperator(&models.Operator{
			ID:       fmt.Sprintf("op-%d", i+10 != nil { t.Fatalf("unexpected error: %v", mgr.RegisterOperator(&models.Operator{
			ID:       fmt.Sprintf("op-%d", i+10) },
			Name:     fmt.Sprintf("Op %d", i),
			Endpoint: fmt.Sprintf("https://op%d.example.com", i),
		}))
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = mgr.RemoveOperator(fmt.Sprintf("op-%d", idx+10))
		}(i)
	}
	wg.Wait()

	ops := mgr.ListOperators()
	if len(ops) != 0 { t.Errorf("expected empty, got %d items", len(ops)) }
}

func TestManager_AuditLogForPoI(t *testing.T) {
	mgr, store := setupTestManager(t)

	leaseA := &models.Lease{
		ID:         "lease-1",
		OperatorID: "op-001",
		Status:     models.LeaseStatusActive,
		EndTime:    time.Now().UTC().Add(-time.Hour),
	}

	poi := &models.ProofOfInvalidity{
		Type:      models.InvalidityExpiredLease,
		LeaseA:    leaseA,
		Timestamp: time.Now().UTC(),
	}

	err := mgr.HandleProofOfInvalidity(poi)
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	logs, _ := store.GetAuditLog(time.Time{}, time.Time{})
	if len(logs) != 1 { t.Fatalf("expected length %d, got %d", 1, len(logs)) }
	if "op-001" != logs[0].OperatorID { t.Errorf("expected %v, got %v", "op-001", logs[0].OperatorID) }
	if "lease-1" != logs[0].LeaseID { t.Errorf("expected %v, got %v", "lease-1", logs[0].LeaseID) }
	assert.Contains(t, logs[0].Operation, "EXPIRED_LEASE")
}
