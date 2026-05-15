package federation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

func TestNewClient(t *testing.T) {
	c := NewClient(10 * time.Second)
	if c == nil { t.Fatal("expected non-nil, got nil") }
	if c.httpClient == nil { t.Fatal("expected non-nil, got nil") }
	if 10*time.Second != c.timeout { t.Errorf("expected %v, got %v", 10*time.Second, c.timeout) }
}

func TestClient_GetCommitment(t *testing.T) {
	expected := &models.MerkleCommitment{
		OperatorID:  "op-002",
		RootHash:    []byte("root-hash"),
		BlockHeight: 42,
		LeaseCount:  10,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "/v1/commitments/op-002/42" != r.URL.Path { t.Errorf("expected %v, got %v", "/v1/commitments/op-002/42", r.URL.Path) }
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	commitment, err := client.GetCommitment(server.URL, "op-002", 42)

	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if expected.OperatorID != commitment.OperatorID { t.Errorf("expected %v, got %v", expected.OperatorID, commitment.OperatorID) }
	if expected.BlockHeight != commitment.BlockHeight { t.Errorf("expected %v, got %v", expected.BlockHeight, commitment.BlockHeight) }
}

func TestClient_GetCommitment_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	commitment, err := client.GetCommitment(server.URL, "op-002", 99)

	if err == nil { t.Error("expected error, got nil") }
	if commitment != nil { t.Errorf("expected nil, got %v", commitment) }
	if !strings.Contains(err.Error(), "failed") { t.Errorf("expected error containing %q, got %q", "failed", err.Error()) }
}

func TestClient_GetCommitment_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	commitment, err := client.GetCommitment(server.URL, "op-002", 1)

	if err == nil { t.Error("expected error, got nil") }
	if commitment != nil { t.Errorf("expected nil, got %v", commitment) }
}

func TestClient_GetLeases(t *testing.T) {
	expected := []*models.Lease{
		{
			ID:         "lease-1",
			OperatorID: "op-002",
			Status:     models.LeaseStatusActive,
			Wavelength: &models.Wavelength{
				LambdaNm:   1550.12,
				ChannelNum: 32,
				Band:       models.BandCBand,
				GridGHz:    25.0,
			},
			StartTime: time.Now().UTC().Add(-time.Hour),
			EndTime:   time.Now().UTC().Add(time.Hour),
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "/v1/leases" != r.URL.Path { t.Errorf("expected %v, got %v", "/v1/leases", r.URL.Path) }
		if "op-002" != r.URL.Query().Get("operator_id") { t.Errorf("expected %v, got %v", "op-002", r.URL.Query().Get("operator_id")) }
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	leases, err := client.GetLeases(server.URL, registry.LeaseFilter{OperatorID: "op-002"})

	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(leases) != 1 { t.Errorf("expected length %d, got %d", 1, len(leases)) }
	if "lease-1" != leases[0].ID { t.Errorf("expected %v, got %v", "lease-1", leases[0].ID) }
}

func TestClient_GetLeases_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]*models.Lease{})
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	leases, err := client.GetLeases(server.URL, registry.LeaseFilter{})

	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(leases) != 0 { t.Errorf("expected empty, got %d items", len(leases)) }
}

func TestClient_PushCommitment(t *testing.T) {
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "/v1/commitments" != r.URL.Path { t.Errorf("expected %v, got %v", "/v1/commitments", r.URL.Path) }
		if "application/json" != r.Header.Get("Content-Type" ) { t.Errorf("expected %v, got %v", "application/json", r.Header.Get("Content-Type") )}

		var commitment models.MerkleCommitment
		err := json.NewDecoder(r.Body).Decode(&commitment)
		if err != nil { t.Fatalf("unexpected error: %v", err) }
		if "op-001" != commitment.OperatorID { t.Errorf("expected %v, got %v", "op-001", commitment.OperatorID) }
		received = true

		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	commitment := &models.MerkleCommitment{
		OperatorID:  "op-001",
		RootHash:    []byte("root-hash"),
		BlockHeight: 7,
	}

	client := NewClient(5 * time.Second)
	err := client.PushCommitment(server.URL, commitment)

	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if !received { t.Error("expected true, got false") }
}

func TestClient_PushCommitment_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("storage full"))
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	err := client.PushCommitment(server.URL, &models.MerkleCommitment{
		OperatorID: "op-001",
		RootHash:   []byte("hash"),
	})

	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "failed") { t.Errorf("expected error containing %q, got %q", "failed", err.Error()) }
}

func TestClient_PushCommitment_NilCommitment(t *testing.T) {
	// Marshalling nil should still work but produce "null"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	err := client.PushCommitment(server.URL, nil)
	if err == nil { t.Error("expected error, got nil") }
}

func TestClient_SubmitProofOfInvalidity(t *testing.T) {
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "/v1/invalidity" != r.URL.Path { t.Errorf("expected %v, got %v", "/v1/invalidity", r.URL.Path) }
		if "application/json" != r.Header.Get("Content-Type" ) { t.Errorf("expected %v, got %v", "application/json", r.Header.Get("Content-Type") )}

		var poi models.ProofOfInvalidity
		err := json.NewDecoder(r.Body).Decode(&poi)
		if err != nil { t.Fatalf("unexpected error: %v", err) }
		if models.InvalidityDoubleAllocation != poi.Type { t.Errorf("expected %v, got %v", models.InvalidityDoubleAllocation, poi.Type) }
		received = true

		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	poi := &models.ProofOfInvalidity{
		Type: models.InvalidityDoubleAllocation,
		LeaseA: &models.Lease{
			ID: "lease-a",
		},
		LeaseB: &models.Lease{
			ID: "lease-b",
		},
	}

	client := NewClient(5 * time.Second)
	err := client.SubmitProofOfInvalidity(server.URL, poi)

	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if !received { t.Error("expected true, got false") }
}

func TestClient_SubmitProofOfInvalidity_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	err := client.SubmitProofOfInvalidity(server.URL, &models.ProofOfInvalidity{
		Type: models.InvalidityExpiredLease,
	})

	if err == nil { t.Error("expected error, got nil") }
}

func TestClient_RegisterOperator(t *testing.T) {
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "/v1/operators" != r.URL.Path { t.Errorf("expected %v, got %v", "/v1/operators", r.URL.Path) }
		if "application/json" != r.Header.Get("Content-Type" ) { t.Errorf("expected %v, got %v", "application/json", r.Header.Get("Content-Type") )}

		var op models.Operator
		err := json.NewDecoder(r.Body).Decode(&op)
		if err != nil { t.Fatalf("unexpected error: %v", err) }
		if "op-003" != op.ID { t.Errorf("expected %v, got %v", "op-003", op.ID) }
		if "Operator C" != op.Name { t.Errorf("expected %v, got %v", "Operator C", op.Name) }
		received = true

		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	op := &models.Operator{
		ID:       "op-003",
		Name:     "Operator C",
		Endpoint: "https://op-c.example.com",
		PublicKey: []byte("pubkey"),
	}

	client := NewClient(5 * time.Second)
	err := client.RegisterOperator(server.URL, op)

	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if !received { t.Error("expected true, got false") }
}

func TestClient_RegisterOperator_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte("operator already exists"))
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	err := client.RegisterOperator(server.URL, &models.Operator{
		ID:   "op-003",
		Name: "Operator C",
	})

	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "failed") { t.Errorf("expected error containing %q, got %q", "failed", err.Error()) }
}

func TestClient_GetOperator(t *testing.T) {
	expected := &models.Operator{
		ID:       "op-002",
		Name:     "Operator B",
		Endpoint: "https://op-b.example.com",
		PublicKey: []byte("pubkey"),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "/v1/operators/op-002" != r.URL.Path { t.Errorf("expected %v, got %v", "/v1/operators/op-002", r.URL.Path) }
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	op, err := client.GetOperator(server.URL, "op-002")

	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if "op-002" != op.ID { t.Errorf("expected %v, got %v", "op-002", op.ID) }
	if "Operator B" != op.Name { t.Errorf("expected %v, got %v", "Operator B", op.Name) }
}

func TestClient_GetOperator_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	op, err := client.GetOperator(server.URL, "op-missing")

	if err == nil { t.Error("expected error, got nil") }
	if op != nil { t.Errorf("expected nil, got %v", op) }
	if !strings.Contains(err.Error(), "not found") { t.Errorf("expected error containing %q, got %q", "not found", err.Error()) }
}

func TestClient_GetTranslationTable(t *testing.T) {
	expected := []*models.TranslationEntry{
		{
			ID:           "tr-1",
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
			FromAWGPort: 1,
			ToAWGPort:   2,
			Status:      models.TranslationStatusActive,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "/v1/translations" != r.URL.Path { t.Errorf("expected %v, got %v", "/v1/translations", r.URL.Path) }
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	entries, err := client.GetTranslationTable(server.URL, "op-002")

	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if len(entries) != 1 { t.Errorf("expected length %d, got %d", 1, len(entries)) }
	if "tr-1" != entries[0].ID { t.Errorf("expected %v, got %v", "tr-1", entries[0].ID) }
}

func TestClient_GetTranslationTable_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	entries, err := client.GetTranslationTable(server.URL, "op-002")

	if err == nil { t.Error("expected error, got nil") }
	if entries != nil { t.Errorf("expected nil, got %v", entries) }
}

func TestClient_HealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "/health" != r.URL.Path { t.Errorf("expected %v, got %v", "/health", r.URL.Path) }
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	err := client.HealthCheck(server.URL)

	if err != nil { t.Fatalf("unexpected error: %v", err) }
}

func TestClient_HealthCheck_Unhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	err := client.HealthCheck(server.URL)

	if err == nil { t.Error("expected error, got nil") }
	if !strings.Contains(err.Error(), "unhealthy") { t.Errorf("expected error containing %q, got %q", "unhealthy", err.Error()) }
}

func TestClient_HealthCheck_Unreachable(t *testing.T) {
	client := NewClient(100 * time.Millisecond)
	// Use a port that should be closed
	err := client.HealthCheck("http://127.0.0.1:1")

	if err == nil { t.Error("expected error, got nil") }
}

func TestClient_StreamUpdates(t *testing.T) {
	updates := []models.RegistryUpdate{
		{
			Operation:   "CREATE_LEASE",
			BlockHeight: 10,
			Lease: &models.Lease{
				ID: "lease-stream-1",
			},
		},
		{
			Operation:   "CREATE_LEASE",
			BlockHeight: 11,
			Lease: &models.Lease{
				ID: "lease-stream-2",
			},
		},
	}

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "/v1/stream" != r.URL.Path { t.Errorf("expected %v, got %v", "/v1/stream", r.URL.Path) }
		if "text/event-stream" != r.Header.Get("Accept" ) { t.Errorf("expected %v, got %v", "text/event-stream", r.Header.Get("Accept") )}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer must support Flusher")
		}

		for _, u := range updates {
			data, _ := json.Marshal(u)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			atomic.AddInt32(&callCount, 1)
		}
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	updCh, errCh := client.StreamUpdates(server.URL, 0)

	// Collect updates with timeout
	var received []*models.RegistryUpdate
	done := make(chan struct{})
	go func() {
		for u := range updCh {
			received = append(received, u)
		}
		close(done)
	}()

	// Drain errCh in the background; it's expected to surface EOF when the
	// server closes the connection, which in turn closes updCh and signals
	// `done`. We wait specifically on `done` so all updates have been
	// appended before we inspect the slice.
	go func() {
		for err := range errCh {
			if err != nil && !strings.Contains(err.Error(), "EOF") {
				t.Logf("stream error: %v", err)
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for stream updates")
	}

	if len(received) != 2 { t.Errorf("expected length %d, got %d", 2, len(received)) }
	if len(received) >= 2 {
		if "lease-stream-1" != received[0].Lease.ID { t.Errorf("expected %v, got %v", "lease-stream-1", received[0].Lease.ID) }
		if int64(10) != received[0].BlockHeight { t.Errorf("expected %v, got %v", int64(10), received[0].BlockHeight) }
		if "lease-stream-2" != received[1].Lease.ID { t.Errorf("expected %v, got %v", "lease-stream-2", received[1].Lease.ID) }
		if int64(11) != received[1].BlockHeight { t.Errorf("expected %v, got %v", int64(11), received[1].BlockHeight) }
	}
}

func TestClient_StreamUpdates_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	updCh, errCh := client.StreamUpdates(server.URL, 0)

	select {
	case err := <-errCh:
		if err == nil { t.Error("expected error, got nil") }
	case <-updCh:
		t.Fatal("expected error, got update")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for error")
	}
}
