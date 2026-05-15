// Package registry provides the core registry engine and storage abstractions
// for the Federated Lambda Registry (FLR) system.
package registry

import (
	"time"

	"github.com/otap/flr/internal/models"
)

// Store abstracts the persistence layer for the FLR registry.
// All implementations must be safe for concurrent use.
type Store interface {
	// CreateLease stores a new lease. Returns an error if the lease already exists.
	CreateLease(lease *models.Lease) error

	// GetLease retrieves a lease by its ID. Returns an error if not found.
	GetLease(id string) (*models.Lease, error)

	// UpdateLease updates an existing lease. Returns an error if the lease does not exist.
	UpdateLease(lease *models.Lease) error

	// DeleteLease removes a lease by its ID.
	DeleteLease(id string) error

	// ListLeases returns leases matching the given filter.
	ListLeases(filter LeaseFilter) ([]*models.Lease, error)

	// CreateEndpoint stores a new endpoint. Returns an error if the endpoint already exists.
	CreateEndpoint(ep *models.Endpoint) error

	// GetEndpoint retrieves an endpoint by its ID. Returns an error if not found.
	GetEndpoint(id string) (*models.Endpoint, error)

	// ListEndpoints returns endpoints matching the given filter.
	ListEndpoints(filter EndpointFilter) ([]*models.Endpoint, error)

	// CreateOperator stores a new operator. Returns an error if the operator already exists.
	CreateOperator(op *models.Operator) error

	// GetOperator retrieves an operator by its ID. Returns an error if not found.
	GetOperator(id string) (*models.Operator, error)

	// ListOperators returns all registered operators.
	ListOperators() ([]*models.Operator, error)

	// SaveCommitment stores a signed Merkle commitment.
	SaveCommitment(c *models.MerkleCommitment) error

	// GetCommitment retrieves a commitment by operator ID and block height.
	GetCommitment(operatorID string, blockHeight int64) (*models.MerkleCommitment, error)

	// GetLatestCommitment retrieves the most recent commitment for an operator.
	GetLatestCommitment(operatorID string) (*models.MerkleCommitment, error)

	// AppendAuditLog adds a new entry to the audit log.
	AppendAuditLog(entry *models.AuditLogEntry) error

	// GetAuditLog retrieves audit log entries within a time range.
	GetAuditLog(from, to time.Time) ([]*models.AuditLogEntry, error)
}

// LeaseFilter provides filtering criteria for lease queries.
type LeaseFilter struct {
	OperatorID string
	EndpointID string
	Status     models.LeaseStatus
}

// EndpointFilter provides filtering criteria for endpoint queries.
type EndpointFilter struct {
	OperatorID string
	Status     models.EndpointStatus
}
