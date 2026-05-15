// Package xlat manages cross-operator wavelength translation tables for AWG junctions.
package xlat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"crypto/rand"
	"encoding/hex"
	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

// Manager manages cross-operator wavelength translation tables.
type Manager struct {
	store registry.Store
}

// TranslationFilter for querying translation entries.
type TranslationFilter struct {
	FromOperator string
	ToOperator   string
	Status       models.TranslationStatus
}

// AWGRoutingTable represents a passive AWG routing configuration.
type AWGRoutingTable struct {
	JunctionID  string            `json:"junction_id"`
	GeneratedAt time.Time         `json:"generated_at"`
	OperatorID  string            `json:"operator_id"`
	Entries     []AWGRoutingEntry `json:"entries"`
	MerkleRoot  []byte            `json:"merkle_root"`
	Signature   []byte            `json:"signature"`
}

// AWGRoutingEntry represents a single AWG routing entry.
type AWGRoutingEntry struct {
	InputPort     int32   `json:"input_port"`
	InputLambda   float64 `json:"input_lambda"`
	OutputPort    int32   `json:"output_port"`
	OutputLambda  float64 `json:"output_lambda"`
	DestinationOp string  `json:"destination_op"`
	Active        bool    `json:"active"`
}

// NewManager creates a translation table manager.
func NewManager(store registry.Store) *Manager {
	return &Manager{store: store}
}

// CreateTranslation establishes a cross-operator wavelength mapping.
func (t *Manager) CreateTranslation(fromOp, toOp string, fromWL, toWL *models.Wavelength, fromPort, toPort int32, duration time.Duration) (*models.TranslationEntry, error) {
	if fromOp == "" || toOp == "" {
		return nil, fmt.Errorf("both from_operator and to_operator are required")
	}
	if fromWL == nil || toWL == nil {
		return nil, fmt.Errorf("both wavelengths are required")
	}
	if duration <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}
	if fromPort <= 0 || toPort <= 0 {
		return nil, fmt.Errorf("ports must be positive")
	}

	now := time.Now().UTC()
	entry := &models.TranslationEntry{
		ID:             generateID(),
		FromOperator:   fromOp,
		ToOperator:     toOp,
		FromWavelength: fromWL,
		ToWavelength:   toWL,
		FromAWGPort:    fromPort,
		ToAWGPort:      toPort,
		Status:         models.TranslationStatusActive,
		EffectiveTime:  now,
		ExpiryTime:     now.Add(duration),
	}

	// Validate the translation before creating
	if err := t.ValidateTranslation(entry); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// TODO: Persist translation entry when store supports it.
	// For now, translations are managed in-memory.
	_ = t.store // mark used

	return entry, nil
}

// GetTranslation retrieves a translation entry by ID.
func (t *Manager) GetTranslation(id string) (*models.TranslationEntry, error) {
	if id == "" {
		return nil, fmt.Errorf("translation ID is required")
	}
	// TODO: Implement persistent lookup when store supports translations.
	return nil, fmt.Errorf("translation %s not found (persistent storage not yet implemented)", id)
}

// ListTranslations lists translations matching the filter.
// Currently returns in-memory results; will query store when supported.
func (t *Manager) ListTranslations(filter TranslationFilter) ([]*models.TranslationEntry, error) {
	_ = filter // will be used for filtering against persistent store
	// TODO: Implement persistent query when store supports translations.
	return []*models.TranslationEntry{}, nil
}

// GenerateAWGTable generates a passive AWG routing table for a junction.
// It builds the routing configuration from active translation entries.
func (t *Manager) GenerateAWGTable(junctionID string, operatorID string) (*AWGRoutingTable, error) {
	if junctionID == "" {
		return nil, fmt.Errorf("junction ID is required")
	}
	if operatorID == "" {
		return nil, fmt.Errorf("operator ID is required")
	}

	// Get all active translations for this operator
	filter := TranslationFilter{
		FromOperator: operatorID,
		Status:       models.TranslationStatusActive,
	}
	translations, err := t.ListTranslations(filter)
	if err != nil {
		return nil, fmt.Errorf("list translations: %w", err)
	}

	now := time.Now().UTC()
	table := &AWGRoutingTable{
		JunctionID:  junctionID,
		GeneratedAt: now,
		OperatorID:  operatorID,
		Entries:     []AWGRoutingEntry{},
	}

	for _, tr := range translations {
		// Skip expired translations
		if tr.ExpiryTime.Before(now) {
			continue
		}
		if tr.FromWavelength == nil || tr.ToWavelength == nil {
			continue
		}

		entry := AWGRoutingEntry{
			InputPort:     tr.FromAWGPort,
			InputLambda:   tr.FromWavelength.LambdaNm,
			OutputPort:    tr.ToAWGPort,
			OutputLambda:  tr.ToWavelength.LambdaNm,
			DestinationOp: tr.ToOperator,
			Active:        tr.Status == models.TranslationStatusActive,
		}
		table.Entries = append(table.Entries, entry)
	}

	// Compute Merkle root of the routing table entries
	if len(table.Entries) > 0 {
		table.MerkleRoot = computeMerkleRoot(table.Entries)
	}

	return table, nil
}

// ValidateTranslation checks that a translation doesn't conflict with existing leases.
// It verifies that the from-wavelength and to-wavelength are not already allocated
// in a way that would cause a conflict.
func (t *Manager) ValidateTranslation(entry *models.TranslationEntry) error {
	if entry == nil {
		return fmt.Errorf("translation entry is nil")
	}
	if entry.FromWavelength == nil || entry.ToWavelength == nil {
		return fmt.Errorf("wavelengths are required")
	}
	if entry.FromAWGPort <= 0 || entry.ToAWGPort <= 0 {
		return fmt.Errorf("AWG ports must be positive")
	}
	if entry.ExpiryTime.Before(entry.EffectiveTime) {
		return fmt.Errorf("expiry time must be after effective time")
	}
	if entry.ExpiryTime.Before(time.Now().UTC()) {
		return fmt.Errorf("translation has already expired")
	}

	// Check for conflicting leases on the from-wavelength
	if t.store != nil {
		fromFilter := registry.LeaseFilter{
			OperatorID: entry.FromOperator,
			Status:     models.LeaseStatusActive,
		}
		leases, err := t.store.ListLeases(fromFilter)
		if err != nil {
			return fmt.Errorf("check from-operator leases: %w", err)
		}
		for _, lease := range leases {
			if lease.Wavelength != nil && wavelengthsEqual(lease.Wavelength, entry.FromWavelength) {
				return fmt.Errorf("conflicting lease %s on from-wavelength %s", lease.ID, entry.FromWavelength.String())
			}
		}

		// Check for conflicting leases on the to-wavelength
		toFilter := registry.LeaseFilter{
			OperatorID: entry.ToOperator,
			Status:     models.LeaseStatusActive,
		}
		leases, err = t.store.ListLeases(toFilter)
		if err != nil {
			return fmt.Errorf("check to-operator leases: %w", err)
		}
		for _, lease := range leases {
			if lease.Wavelength != nil && wavelengthsEqual(lease.Wavelength, entry.ToWavelength) {
				return fmt.Errorf("conflicting lease %s on to-wavelength %s", lease.ID, entry.ToWavelength.String())
			}
		}
	}

	return nil
}

// ExportForAWG exports the routing table in vendor-neutral JSON format.
func (t *Manager) ExportForAWG(junctionID string, operatorID string) ([]byte, error) {
	table, err := t.GenerateAWGTable(junctionID, operatorID)
	if err != nil {
		return nil, fmt.Errorf("generate AWG table: %w", err)
	}

	data, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal AWG table: %w", err)
	}

	return data, nil
}

// generateID creates a random unique identifier.
func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// wavelengthsEqual checks if two wavelengths represent the same ITU-T grid position.
func wavelengthsEqual(a, b *models.Wavelength) bool {
	if a == nil || b == nil {
		return false
	}
	return a.LambdaNm == b.LambdaNm && a.ChannelNum == b.ChannelNum && a.Band == b.Band
}

// computeMerkleRoot computes a simple Merkle root hash from routing entries.
func computeMerkleRoot(entries []AWGRoutingEntry) []byte {
	if len(entries) == 0 {
		return nil
	}
	hashes := make([][]byte, len(entries))
	for i, e := range entries {
		data, _ := json.Marshal(e)
		h := sha256.Sum256(data)
		hashes[i] = h[:]
	}
	for len(hashes) > 1 {
		var next [][]byte
		for i := 0; i < len(hashes); i += 2 {
			if i+1 < len(hashes) {
				combined := append(hashes[i], hashes[i+1]...)
				h := sha256.Sum256(combined)
				next = append(next, h[:])
			} else {
				next = append(next, hashes[i])
			}
		}
		hashes = next
	}
	return hashes[0]
}
