package registry

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/otap/flr/internal/models"
	"golang.org/x/crypto/sha3"
)

// key prefixes for different data types
const (
	prefixLease      = "lease:"
	prefixEndpoint   = "endpoint:"
	prefixOperator   = "operator:"
	prefixCommitment = "commitment:"
	prefixAudit      = "audit:"
)

// BadgerStore implements the Store interface using BadgerDB as the backend.
type BadgerStore struct {
	db *badger.DB
	mu sync.RWMutex
}

// NewBadgerStore creates or opens a BadgerDB database at the given path.
func NewBadgerStore(path string) (*BadgerStore, error) {
	opts := badger.DefaultOptions(path).
		WithLogger(nil) // Suppress Badger's verbose logging

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db at %s: %w", path, err)
	}

	return &BadgerStore{db: db}, nil
}

// Close closes the BadgerDB database.
func (s *BadgerStore) Close() error {
	return s.db.Close()
}

// --- Lease Operations ---

// CreateLease stores a new lease in the database.
func (s *BadgerStore) CreateLease(lease *models.Lease) error {
	if lease == nil {
		return fmt.Errorf("lease cannot be nil")
	}
	if lease.ID == "" {
		return fmt.Errorf("lease ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := []byte(prefixLease + lease.ID)

	return s.db.Update(func(txn *badger.Txn) error {
		// Check if lease already exists
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("lease already exists: %s", lease.ID)
		}
		if err != badger.ErrKeyNotFound {
			return fmt.Errorf("failed to check lease existence: %w", err)
		}

		data, err := json.Marshal(lease)
		if err != nil {
			return fmt.Errorf("failed to marshal lease: %w", err)
		}

		return txn.Set(key, data)
	})
}

// GetLease retrieves a lease by its ID.
func (s *BadgerStore) GetLease(id string) (*models.Lease, error) {
	if id == "" {
		return nil, fmt.Errorf("lease ID cannot be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := []byte(prefixLease + id)

	var lease models.Lease
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("lease not found: %s", id)
		}
		if err != nil {
			return fmt.Errorf("failed to get lease: %w", err)
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &lease)
		})
	})

	if err != nil {
		return nil, err
	}

	return &lease, nil
}

// UpdateLease updates an existing lease in the database.
func (s *BadgerStore) UpdateLease(lease *models.Lease) error {
	if lease == nil {
		return fmt.Errorf("lease cannot be nil")
	}
	if lease.ID == "" {
		return fmt.Errorf("lease ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := []byte(prefixLease + lease.ID)

	return s.db.Update(func(txn *badger.Txn) error {
		// Verify lease exists
		_, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("lease not found: %s", lease.ID)
		}
		if err != nil {
			return fmt.Errorf("failed to check lease existence: %w", err)
		}

		lease.UpdatedAt = time.Now().UTC()
		data, err := json.Marshal(lease)
		if err != nil {
			return fmt.Errorf("failed to marshal lease: %w", err)
		}

		return txn.Set(key, data)
	})
}

// DeleteLease removes a lease from the database.
func (s *BadgerStore) DeleteLease(id string) error {
	if id == "" {
		return fmt.Errorf("lease ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := []byte(prefixLease + id)

	return s.db.Update(func(txn *badger.Txn) error {
		err := txn.Delete(key)
		if err != nil {
			return fmt.Errorf("failed to delete lease: %w", err)
		}
		return nil
	})
}

// ListLeases returns all leases matching the given filter.
func (s *BadgerStore) ListLeases(filter LeaseFilter) ([]*models.Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var leases []*models.Lease

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixLease)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()

			var lease models.Lease
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &lease)
			})
			if err != nil {
				return fmt.Errorf("failed to unmarshal lease: %w", err)
			}

			// Apply filters
			if filter.OperatorID != "" && lease.OperatorID != filter.OperatorID {
				continue
			}
			if filter.EndpointID != "" && lease.EndpointID != filter.EndpointID {
				continue
			}
			if filter.Status != models.LeaseStatusUnspecified && lease.Status != filter.Status {
				continue
			}

			leases = append(leases, &lease)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return leases, nil
}

// --- Endpoint Operations ---

// CreateEndpoint stores a new endpoint in the database.
func (s *BadgerStore) CreateEndpoint(ep *models.Endpoint) error {
	if ep == nil {
		return fmt.Errorf("endpoint cannot be nil")
	}
	if ep.ID == "" {
		return fmt.Errorf("endpoint ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := []byte(prefixEndpoint + ep.ID)

	return s.db.Update(func(txn *badger.Txn) error {
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("endpoint already exists: %s", ep.ID)
		}
		if err != badger.ErrKeyNotFound {
			return fmt.Errorf("failed to check endpoint existence: %w", err)
		}

		data, err := json.Marshal(ep)
		if err != nil {
			return fmt.Errorf("failed to marshal endpoint: %w", err)
		}

		return txn.Set(key, data)
	})
}

// GetEndpoint retrieves an endpoint by its ID.
func (s *BadgerStore) GetEndpoint(id string) (*models.Endpoint, error) {
	if id == "" {
		return nil, fmt.Errorf("endpoint ID cannot be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := []byte(prefixEndpoint + id)

	var ep models.Endpoint
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("endpoint not found: %s", id)
		}
		if err != nil {
			return fmt.Errorf("failed to get endpoint: %w", err)
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &ep)
		})
	})

	if err != nil {
		return nil, err
	}

	return &ep, nil
}

// ListEndpoints returns all endpoints matching the given filter.
func (s *BadgerStore) ListEndpoints(filter EndpointFilter) ([]*models.Endpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var endpoints []*models.Endpoint

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixEndpoint)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()

			var ep models.Endpoint
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &ep)
			})
			if err != nil {
				return fmt.Errorf("failed to unmarshal endpoint: %w", err)
			}

			// Apply filters
			if filter.OperatorID != "" && ep.OperatorID != filter.OperatorID {
				continue
			}
			if filter.Status != models.EndpointStatusUnspecified && ep.Status != filter.Status {
				continue
			}

			endpoints = append(endpoints, &ep)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return endpoints, nil
}

// --- Operator Operations ---

// CreateOperator stores a new operator in the database.
func (s *BadgerStore) CreateOperator(op *models.Operator) error {
	if op == nil {
		return fmt.Errorf("operator cannot be nil")
	}
	if op.ID == "" {
		return fmt.Errorf("operator ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := []byte(prefixOperator + op.ID)

	return s.db.Update(func(txn *badger.Txn) error {
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("operator already exists: %s", op.ID)
		}
		if err != badger.ErrKeyNotFound {
			return fmt.Errorf("failed to check operator existence: %w", err)
		}

		data, err := json.Marshal(op)
		if err != nil {
			return fmt.Errorf("failed to marshal operator: %w", err)
		}

		return txn.Set(key, data)
	})
}

// GetOperator retrieves an operator by its ID.
func (s *BadgerStore) GetOperator(id string) (*models.Operator, error) {
	if id == "" {
		return nil, fmt.Errorf("operator ID cannot be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := []byte(prefixOperator + id)

	var op models.Operator
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("operator not found: %s", id)
		}
		if err != nil {
			return fmt.Errorf("failed to get operator: %w", err)
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &op)
		})
	})

	if err != nil {
		return nil, err
	}

	return &op, nil
}

// ListOperators returns all registered operators.
func (s *BadgerStore) ListOperators() ([]*models.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var operators []*models.Operator

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixOperator)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()

			var op models.Operator
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &op)
			})
			if err != nil {
				return fmt.Errorf("failed to unmarshal operator: %w", err)
			}

			operators = append(operators, &op)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return operators, nil
}

// --- Commitment Operations ---

// SaveCommitment stores a signed Merkle commitment.
func (s *BadgerStore) SaveCommitment(c *models.MerkleCommitment) error {
	if c == nil {
		return fmt.Errorf("commitment cannot be nil")
	}
	if c.OperatorID == "" {
		return fmt.Errorf("operator ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal commitment: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		// Store by operatorID:blockHeight
		heightKey := []byte(fmt.Sprintf("%s%s:%d", prefixCommitment, c.OperatorID, c.BlockHeight))
		if err := txn.Set(heightKey, data); err != nil {
			return fmt.Errorf("failed to store commitment: %w", err)
		}

		// Update "latest" pointer
		latestKey := []byte(fmt.Sprintf("%s%s:latest", prefixCommitment, c.OperatorID))
		return txn.Set(latestKey, data)
	})
}

// GetCommitment retrieves a commitment by operator ID and block height.
func (s *BadgerStore) GetCommitment(operatorID string, blockHeight int64) (*models.MerkleCommitment, error) {
	if operatorID == "" {
		return nil, fmt.Errorf("operator ID cannot be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := []byte(fmt.Sprintf("%s%s:%d", prefixCommitment, operatorID, blockHeight))

	var c models.MerkleCommitment
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("commitment not found for operator %s at height %d", operatorID, blockHeight)
		}
		if err != nil {
			return fmt.Errorf("failed to get commitment: %w", err)
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &c)
		})
	})

	if err != nil {
		return nil, err
	}

	return &c, nil
}

// GetLatestCommitment retrieves the most recent commitment for an operator.
func (s *BadgerStore) GetLatestCommitment(operatorID string) (*models.MerkleCommitment, error) {
	if operatorID == "" {
		return nil, fmt.Errorf("operator ID cannot be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := []byte(fmt.Sprintf("%s%s:latest", prefixCommitment, operatorID))

	var c models.MerkleCommitment
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("no commitment found for operator %s", operatorID)
		}
		if err != nil {
			return fmt.Errorf("failed to get latest commitment: %w", err)
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &c)
		})
	})

	if err != nil {
		return nil, err
	}

	return &c, nil
}

// --- Audit Log Operations ---

// AppendAuditLog adds a new entry to the audit log.
func (s *BadgerStore) AppendAuditLog(entry *models.AuditLogEntry) error {
	if entry == nil {
		return fmt.Errorf("audit log entry cannot be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Compute hash of the entry
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal audit log entry: %w", err)
	}
	h := sha3.Sum256(data)
	entry.Hash = h[:]

	key := []byte(prefixAudit + entry.Timestamp.UTC().Format(time.RFC3339Nano))

	return s.db.Update(func(txn *badger.Txn) error {
		entryData, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("failed to marshal audit log entry: %w", err)
		}

		return txn.Set(key, entryData)
	})
}

// GetAuditLog retrieves audit log entries within a time range.
func (s *BadgerStore) GetAuditLog(from, to time.Time) ([]*models.AuditLogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var entries []*models.AuditLogEntry

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()

		fromKey := []byte(prefixAudit + from.UTC().Format(time.RFC3339Nano))
		toKey := []byte(prefixAudit + to.UTC().Format(time.RFC3339Nano))

		for it.Seek(fromKey); it.Valid(); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Check if key is still within our prefix and time range
			if !strings.HasPrefix(key, prefixAudit) {
				break
			}
			if key > string(toKey) {
				break
			}

			var entry models.AuditLogEntry
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &entry)
			})
			if err != nil {
				return fmt.Errorf("failed to unmarshal audit log entry: %w", err)
			}

			entries = append(entries, &entry)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return entries, nil
}
