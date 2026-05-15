// Schema storage operations. Extends the Store interface and BadgerStore
// implementation with ApplicationSchema CRUD.
//
// Keying strategy:
//   - "schema:<id>"           -> ApplicationSchema (canonical by ID)
//   - "schema_oam:<op>:<oam>" -> Schema ID active for that operator+OAM pair
//
// The secondary index lets the OBG query "give me operator O's active
// schema for OAM mode ℓ" in one round trip.
package registry

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dgraph-io/badger/v4"
	"github.com/otap/flr/internal/models"
)

const (
	prefixSchema       = "schema:"
	prefixSchemaOamIdx = "schema_oam:"
)

// SchemaFilter provides filtering criteria for schema queries.
type SchemaFilter struct {
	OperatorID string
	OamMode    *int32 // nil = any
	Status     models.SchemaStatus
	NameLike   string
}

// SchemaStore extends Store with schema operations.
//
// This is a *separate* interface so existing code that only needs lease
// operations doesn't have to be modified. The BadgerStore implements both.
type SchemaStore interface {
	// CreateSchema stores a new schema. Returns error if ID already exists.
	CreateSchema(s *models.ApplicationSchema) error

	// GetSchema retrieves a schema by ID. Returns error if not found.
	GetSchema(id string) (*models.ApplicationSchema, error)

	// UpdateSchema modifies an existing schema (e.g., status change).
	// Returns error if ID does not exist.
	UpdateSchema(s *models.ApplicationSchema) error

	// DeleteSchema removes a schema by ID.
	DeleteSchema(id string) error

	// ListSchemas returns schemas matching the filter.
	ListSchemas(filter SchemaFilter) ([]*models.ApplicationSchema, error)

	// GetActiveSchemaForOam returns the active schema for an (operator, oam)
	// pair. Returns error if no active schema is registered.
	GetActiveSchemaForOam(operatorID string, oamMode int32) (*models.ApplicationSchema, error)
}

// --- BadgerStore implementation ---

// CreateSchema stores a new schema with secondary-index update.
func (s *BadgerStore) CreateSchema(sch *models.ApplicationSchema) error {
	if sch == nil {
		return fmt.Errorf("schema cannot be nil")
	}
	if sch.ID == "" {
		return fmt.Errorf("schema ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	primaryKey := []byte(prefixSchema + sch.ID)
	oamIdxKey := []byte(fmt.Sprintf("%s%s:%d", prefixSchemaOamIdx, sch.OperatorID, sch.OamMode))

	return s.db.Update(func(txn *badger.Txn) error {
		// Reject duplicate ID.
		if _, err := txn.Get(primaryKey); err == nil {
			return fmt.Errorf("schema already exists: %s", sch.ID)
		} else if err != badger.ErrKeyNotFound {
			return fmt.Errorf("check schema existence: %w", err)
		}

		// If this schema is ACTIVE, ensure no other ACTIVE schema occupies
		// the same (operator, OAM) slot.
		if sch.Status == models.SchemaStatusActive {
			if existingID, err := getValue(txn, oamIdxKey); err == nil {
				// Read current occupant.
				existing, err := loadSchema(txn, string(existingID))
				if err == nil && existing.Status == models.SchemaStatusActive {
					return fmt.Errorf("operator %s already has active schema %s on OAM mode %d",
						sch.OperatorID, existing.ID, sch.OamMode)
				}
			}
		}

		data, err := json.Marshal(sch)
		if err != nil {
			return fmt.Errorf("marshal schema: %w", err)
		}
		if err := txn.Set(primaryKey, data); err != nil {
			return err
		}
		// Update secondary index only if active.
		if sch.Status == models.SchemaStatusActive {
			if err := txn.Set(oamIdxKey, []byte(sch.ID)); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetSchema retrieves a schema by ID.
func (s *BadgerStore) GetSchema(id string) (*models.ApplicationSchema, error) {
	if id == "" {
		return nil, fmt.Errorf("schema ID cannot be empty")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sch *models.ApplicationSchema
	err := s.db.View(func(txn *badger.Txn) error {
		var err error
		sch, err = loadSchema(txn, id)
		return err
	})
	return sch, err
}

// UpdateSchema modifies an existing schema. The OAM secondary index is
// adjusted if Status crosses the ACTIVE/non-ACTIVE boundary.
func (s *BadgerStore) UpdateSchema(sch *models.ApplicationSchema) error {
	if sch == nil {
		return fmt.Errorf("schema cannot be nil")
	}
	if sch.ID == "" {
		return fmt.Errorf("schema ID cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	primaryKey := []byte(prefixSchema + sch.ID)
	oamIdxKey := []byte(fmt.Sprintf("%s%s:%d", prefixSchemaOamIdx, sch.OperatorID, sch.OamMode))

	return s.db.Update(func(txn *badger.Txn) error {
		// Load existing to detect status transitions.
		existing, err := loadSchema(txn, sch.ID)
		if err != nil {
			return fmt.Errorf("schema not found: %s", sch.ID)
		}

		data, err := json.Marshal(sch)
		if err != nil {
			return fmt.Errorf("marshal schema: %w", err)
		}
		if err := txn.Set(primaryKey, data); err != nil {
			return err
		}

		// Maintain the OAM index across status transitions:
		//  - was ACTIVE, now ACTIVE: leave index alone (still points here).
		//  - was ACTIVE, now non-active: delete the index entry.
		//  - was non-active, now ACTIVE: write the index entry, but only if
		//      no other ACTIVE schema currently holds the slot.
		//  - was non-active, still non-active: nothing to do.
		switch {
		case existing.Status == models.SchemaStatusActive && sch.Status != models.SchemaStatusActive:
			if err := txn.Delete(oamIdxKey); err != nil && err != badger.ErrKeyNotFound {
				return err
			}
		case existing.Status != models.SchemaStatusActive && sch.Status == models.SchemaStatusActive:
			if currentID, err := getValue(txn, oamIdxKey); err == nil {
				if string(currentID) != sch.ID {
					if other, err := loadSchema(txn, string(currentID)); err == nil &&
						other.Status == models.SchemaStatusActive {
						return fmt.Errorf("operator %s already has active schema %s on OAM mode %d",
							sch.OperatorID, other.ID, sch.OamMode)
					}
				}
			}
			if err := txn.Set(oamIdxKey, []byte(sch.ID)); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteSchema removes a schema and its OAM index entry if it held it.
func (s *BadgerStore) DeleteSchema(id string) error {
	if id == "" {
		return fmt.Errorf("schema ID cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	primaryKey := []byte(prefixSchema + id)

	return s.db.Update(func(txn *badger.Txn) error {
		// Load to discover the OAM index key.
		existing, err := loadSchema(txn, id)
		if err != nil {
			return fmt.Errorf("schema not found: %s", id)
		}
		oamIdxKey := []byte(fmt.Sprintf("%s%s:%d", prefixSchemaOamIdx, existing.OperatorID, existing.OamMode))

		if err := txn.Delete(primaryKey); err != nil {
			return err
		}
		// Only delete the index if it points at this ID.
		if currentID, err := getValue(txn, oamIdxKey); err == nil {
			if string(currentID) == id {
				if err := txn.Delete(oamIdxKey); err != nil && err != badger.ErrKeyNotFound {
					return err
				}
			}
		}
		return nil
	})
}

// ListSchemas returns all schemas matching the filter. Linear scan over the
// schema namespace; the schema set is expected to be small (tens to low
// hundreds) so this is acceptable.
func (s *BadgerStore) ListSchemas(filter SchemaFilter) ([]*models.ApplicationSchema, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []*models.ApplicationSchema
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefixSchema)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var sch models.ApplicationSchema
				if err := json.Unmarshal(v, &sch); err != nil {
					return nil // skip malformed entries
				}
				if !schemaMatches(&sch, filter) {
					return nil
				}
				out = append(out, &sch)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// GetActiveSchemaForOam returns the active schema bound to (operator, oam).
func (s *BadgerStore) GetActiveSchemaForOam(operatorID string, oamMode int32) (*models.ApplicationSchema, error) {
	if operatorID == "" {
		return nil, fmt.Errorf("operator ID cannot be empty")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	oamIdxKey := []byte(fmt.Sprintf("%s%s:%d", prefixSchemaOamIdx, operatorID, oamMode))

	var sch *models.ApplicationSchema
	err := s.db.View(func(txn *badger.Txn) error {
		idBytes, err := getValue(txn, oamIdxKey)
		if err != nil {
			return fmt.Errorf("no active schema for operator %s OAM %d: %w",
				operatorID, oamMode, err)
		}
		sch, err = loadSchema(txn, string(idBytes))
		if err != nil {
			return err
		}
		if sch.Status != models.SchemaStatusActive {
			return fmt.Errorf("indexed schema %s is not ACTIVE (status %s)",
				sch.ID, sch.Status)
		}
		return nil
	})
	return sch, err
}

// --- helpers ---

// loadSchema reads and unmarshals a schema within a Badger txn.
func loadSchema(txn *badger.Txn, id string) (*models.ApplicationSchema, error) {
	item, err := txn.Get([]byte(prefixSchema + id))
	if err == badger.ErrKeyNotFound {
		return nil, fmt.Errorf("schema not found: %s", id)
	}
	if err != nil {
		return nil, err
	}
	var sch models.ApplicationSchema
	err = item.Value(func(v []byte) error {
		return json.Unmarshal(v, &sch)
	})
	if err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	return &sch, nil
}

// getValue is a small Badger convenience used by index reads.
func getValue(txn *badger.Txn, key []byte) ([]byte, error) {
	item, err := txn.Get(key)
	if err != nil {
		return nil, err
	}
	var out []byte
	err = item.Value(func(v []byte) error {
		out = append(out[:0:0], v...)
		return nil
	})
	return out, err
}

// schemaMatches applies a SchemaFilter.
func schemaMatches(s *models.ApplicationSchema, f SchemaFilter) bool {
	if f.OperatorID != "" && s.OperatorID != f.OperatorID {
		return false
	}
	if f.OamMode != nil && s.OamMode != *f.OamMode {
		return false
	}
	if f.Status != models.SchemaStatusUnspecified && s.Status != f.Status {
		return false
	}
	if f.NameLike != "" && !strings.Contains(s.Name, f.NameLike) {
		return false
	}
	return true
}
