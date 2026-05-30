// Package test provides utilities and helpers for FLR integration tests.
package test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

// MemoryStore is an in-memory implementation of registry.Store for testing.
type MemoryStore struct {
	Leases       map[string]*models.Lease
	Endpoints    map[string]*models.Endpoint
	Operators    map[string]*models.Operator
	Commitments  map[string]*models.MerkleCommitment
	LatestCommit map[string]*models.MerkleCommitment
	AuditLog     []*models.AuditLogEntry
}

// NewMemoryStore creates an in-memory store for tests.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		Leases:       make(map[string]*models.Lease),
		Endpoints:    make(map[string]*models.Endpoint),
		Operators:    make(map[string]*models.Operator),
		Commitments:  make(map[string]*models.MerkleCommitment),
		LatestCommit: make(map[string]*models.MerkleCommitment),
		AuditLog:     make([]*models.AuditLogEntry, 0),
	}
}

// CreateLease stores a lease.
func (m *MemoryStore) CreateLease(lease *models.Lease) error {
	m.Leases[lease.ID] = lease
	return nil
}

// GetLease retrieves a lease by ID.
func (m *MemoryStore) GetLease(id string) (*models.Lease, error) {
	l, ok := m.Leases[id]
	if !ok {
		return nil, fmt.Errorf("lease not found: %s", id)
	}
	c := *l
	return &c, nil
}

// UpdateLease updates a lease.
func (m *MemoryStore) UpdateLease(lease *models.Lease) error {
	m.Leases[lease.ID] = lease
	return nil
}

// DeleteLease removes a lease.
func (m *MemoryStore) DeleteLease(id string) error {
	delete(m.Leases, id)
	return nil
}

// ListLeases lists leases matching a filter.
func (m *MemoryStore) ListLeases(filter registry.LeaseFilter) ([]*models.Lease, error) {
	var result []*models.Lease
	for _, l := range m.Leases {
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

// CreateEndpoint stores an endpoint.
func (m *MemoryStore) CreateEndpoint(ep *models.Endpoint) error {
	m.Endpoints[ep.ID] = ep
	return nil
}

// GetEndpoint retrieves an endpoint.
func (m *MemoryStore) GetEndpoint(id string) (*models.Endpoint, error) {
	ep, ok := m.Endpoints[id]
	if !ok {
		return nil, fmt.Errorf("endpoint not found: %s", id)
	}
	return ep, nil
}

// ListEndpoints lists endpoints.
func (m *MemoryStore) ListEndpoints(filter registry.EndpointFilter) ([]*models.Endpoint, error) {
	var result []*models.Endpoint
	for _, ep := range m.Endpoints {
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

// CreateOperator stores an operator.
func (m *MemoryStore) CreateOperator(op *models.Operator) error {
	m.Operators[op.ID] = op
	return nil
}

// GetOperator retrieves an operator.
func (m *MemoryStore) GetOperator(id string) (*models.Operator, error) {
	op, ok := m.Operators[id]
	if !ok {
		return nil, fmt.Errorf("operator not found: %s", id)
	}
	return op, nil
}

// ListOperators lists all operators.
func (m *MemoryStore) ListOperators() ([]*models.Operator, error) {
	var result []*models.Operator
	for _, op := range m.Operators {
		result = append(result, op)
	}
	return result, nil
}

// SaveCommitment stores a commitment.
func (m *MemoryStore) SaveCommitment(c *models.MerkleCommitment) error {
	key := fmt.Sprintf("%s:%d", c.OperatorID, c.BlockHeight)
	m.Commitments[key] = c
	m.LatestCommit[c.OperatorID] = c
	return nil
}

// GetCommitment retrieves a commitment.
func (m *MemoryStore) GetCommitment(operatorID string, blockHeight int64) (*models.MerkleCommitment, error) {
	key := fmt.Sprintf("%s:%d", operatorID, blockHeight)
	c, ok := m.Commitments[key]
	if !ok {
		return nil, fmt.Errorf("commitment not found")
	}
	return c, nil
}

// GetLatestCommitment retrieves the latest commitment for an operator.
func (m *MemoryStore) GetLatestCommitment(operatorID string) (*models.MerkleCommitment, error) {
	c, ok := m.LatestCommit[operatorID]
	if !ok {
		return nil, nil
	}
	return c, nil
}

// AppendAuditLog appends an entry to the audit log.
func (m *MemoryStore) AppendAuditLog(entry *models.AuditLogEntry) error {
	m.AuditLog = append(m.AuditLog, entry)
	return nil
}

// GetAuditLog retrieves audit log entries in a time range.
func (m *MemoryStore) GetAuditLog(from, to time.Time) ([]*models.AuditLogEntry, error) {
	var result []*models.AuditLogEntry
	for _, entry := range m.AuditLog {
		if entry.Timestamp.After(from) && entry.Timestamp.Before(to) {
			result = append(result, entry)
		}
	}
	return result, nil
}

// GenerateTestKeyPair generates a test ECDSA P-256 key pair.
// Returns the private key, public key PEM, and private key PEM.
func GenerateTestKeyPair() (*ecdsa.PrivateKey, []byte, []byte, error) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate key: %w", err)
	}

	// Encode private key to PEM
	privDER, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: privDER,
	})

	// Encode public key to PEM
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})

	return privKey, pubPEM, privPEM, nil
}

// CreateTestLease creates a lease for testing.
func CreateTestLease(operatorID, endpointID string, wavelength *models.Wavelength) *models.Lease {
	now := time.Now().UTC()
	return &models.Lease{
		ID:         fmt.Sprintf("lease-%s-%d", operatorID, now.UnixNano()),
		OperatorID: operatorID,
		EndpointID: endpointID,
		Wavelength: wavelength,
		Status:     models.LeaseStatusActive,
		StartTime:  now,
		EndTime:    now.Add(24 * time.Hour),
		CreatedAt:  now,
		UpdatedAt:  now,
		TokenHash:  []byte(fmt.Sprintf("token-%d", now.UnixNano())),
	}
}

// CreateTestOperator creates an operator for testing.
func CreateTestOperator(id, name string) *models.Operator {
	now := time.Now().UTC()
	_, pubPEM, _, err := GenerateTestKeyPair()
	if err != nil {
		pubPEM = []byte("test-pubkey")
	}
	return &models.Operator{
		ID:        id,
		Name:      name,
		PublicKey: pubPEM,
		Endpoint:  fmt.Sprintf("https://%s.otap.network:9090", id),
		Status:    models.OperatorStatusActive,
		JoinedAt:  now,
		LastSeen:  now,
	}
}

// MustParseTime parses a time string or panics.
func MustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(fmt.Sprintf("failed to parse time %q: %v", s, err))
	}
	return t
}

// Wavelength1550 creates a standard 1550.12nm C-band test wavelength.
func Wavelength1550() *models.Wavelength {
	return &models.Wavelength{
		LambdaNm:   1550.12,
		ChannelNum: 4,
		Band:       models.BandCBand,
		GridGHz:    50.0,
	}
}

// Wavelength1530 creates a 1530.33nm C-band test wavelength.
func Wavelength1530() *models.Wavelength {
	return &models.Wavelength{
		LambdaNm:   1530.33,
		ChannelNum: -34,
		Band:       models.BandCBand,
		GridGHz:    50.0,
	}
}

// Wavelength1560 creates a 1560.61nm C-band test wavelength.
func Wavelength1560() *models.Wavelength {
	return &models.Wavelength{
		LambdaNm:   1560.61,
		ChannelNum: 21,
		Band:       models.BandCBand,
		GridGHz:    50.0,
	}
}
