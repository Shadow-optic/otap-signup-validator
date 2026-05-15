// Package audit provides compliance checking, audit trails, and standards validation
// for the Federated Lambda Registry (FLR) system.
package audit

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"time"

	"golang.org/x/crypto/sha3"

	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

// Auditor handles compliance and audit functions for the FLR system.
type Auditor struct {
	store  registry.Store
	logger *slog.Logger
}

// NewAuditor creates a new Auditor instance backed by the given store.
func NewAuditor(store registry.Store) *Auditor {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	return &Auditor{
		store:  store,
		logger: logger,
	}
}

// AuditReport is a compliance summary report.
type AuditReport struct {
	GeneratedAt     time.Time             `json:"generated_at"`
	PeriodStart     time.Time             `json:"period_start"`
	PeriodEnd       time.Time             `json:"period_end"`
	TotalLeases     int                   `json:"total_leases"`
	ActiveLeases    int                   `json:"active_leases"`
	ExpiredLeases   int                   `json:"expired_leases"`
	RevokedLeases   int                   `json:"revoked_leases"`
	TotalOperators  int                   `json:"total_operators"`
	ActiveOperators int                   `json:"active_operators"`
	CommitmentCount int                   `json:"commitment_count"`
	Violations      []ComplianceViolation `json:"violations"`
	HashChainValid  bool                  `json:"hash_chain_valid"`
}

// ComplianceViolation records a single compliance issue found during an audit.
type ComplianceViolation struct {
	Type        string    `json:"type"`
	Severity    string    `json:"severity"` // critical, high, medium, low
	Description string    `json:"description"`
	LeaseID     string    `json:"lease_id,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// LogOperation records a registry mutation to the audit log.
func (a *Auditor) LogOperation(op string, operatorID string, leaseID string, details []byte) error {
	entry := &models.AuditLogEntry{
		Timestamp:  time.Now().UTC(),
		Operation:  op,
		OperatorID: operatorID,
		LeaseID:    leaseID,
		Details:    details,
	}

	// Compute hash of entry
	h := sha3.New256()
	h.Write([]byte(entry.Operation))
	h.Write([]byte(entry.OperatorID))
	h.Write([]byte(entry.LeaseID))
	h.Write(entry.Details)
	entry.Hash = h.Sum(nil)

	return a.store.AppendAuditLog(entry)
}

// GenerateAuditReport creates a compliance report for a time period.
func (a *Auditor) GenerateAuditReport(from, to time.Time) (*AuditReport, error) {
	report := &AuditReport{
		GeneratedAt: time.Now().UTC(),
		PeriodStart: from,
		PeriodEnd:   to,
		Violations:  []ComplianceViolation{},
	}

	// Count all leases
	allLeases, err := a.store.ListLeases(registry.LeaseFilter{})
	if err != nil {
		a.logger.Error("failed to list leases", "error", err)
		return nil, fmt.Errorf("failed to list leases: %w", err)
	}
	report.TotalLeases = len(allLeases)

	for _, lease := range allLeases {
		switch lease.Status {
		case models.LeaseStatusActive:
			report.ActiveLeases++
		case models.LeaseStatusExpired:
			report.ExpiredLeases++
		case models.LeaseStatusRevoked:
			report.RevokedLeases++
		}

		// Check for expired leases still marked as active
		if lease.Status == models.LeaseStatusActive && time.Now().After(lease.EndTime) {
			report.Violations = append(report.Violations, ComplianceViolation{
				Type:        "EXPIRED_LEASE_ACTIVE",
				Severity:    "high",
				Description: fmt.Sprintf("Lease %s has expired but remains active (ended %s)", lease.ID, lease.EndTime.Format(time.RFC3339)),
				LeaseID:     lease.ID,
				Timestamp:   time.Now().UTC(),
			})
		}

		// Check ITU-T compliance
		if lease.Wavelength != nil {
			err := a.ValidateITUCompliance(lease.Wavelength)
			if err != nil {
				report.Violations = append(report.Violations, ComplianceViolation{
					Type:        "ITU_COMPLIANCE",
					Severity:    "medium",
					Description: err.Error(),
					LeaseID:     lease.ID,
					Timestamp:   time.Now().UTC(),
				})
			}
		}
	}

	// Count operators
	operators, err := a.store.ListOperators()
	if err != nil {
		a.logger.Error("failed to list operators", "error", err)
		return nil, fmt.Errorf("failed to list operators: %w", err)
	}
	report.TotalOperators = len(operators)
	for _, op := range operators {
		if op.Status == models.OperatorStatusActive {
			report.ActiveOperators++
		}
	}

	// Check hash chain integrity
	chainValid, chainErrors, err := a.CheckLeaseChainIntegrity()
	if err != nil {
		a.logger.Error("hash chain check failed", "error", err)
		return nil, fmt.Errorf("hash chain check failed: %w", err)
	}
	report.HashChainValid = chainValid
	for _, chainErr := range chainErrors {
		report.Violations = append(report.Violations, ComplianceViolation{
			Type:        "HASH_CHAIN_BROKEN",
			Severity:    "critical",
			Description: chainErr,
			Timestamp:   time.Now().UTC(),
		})
	}

	// Count commitments by checking each operator's latest commitment.
	// (The Store interface doesn't expose a ListCommitments primitive.)
	operators, opErr := a.store.ListOperators()
	if opErr != nil {
		a.logger.Error("failed to list operators", "error", opErr)
		return nil, fmt.Errorf("failed to list operators: %w", opErr)
	}
	for _, op := range operators {
		if c, err := a.store.GetLatestCommitment(op.ID); err == nil && c != nil {
			report.CommitmentCount++
		}
	}

	return report, nil
}

// ValidateITUCompliance checks a wavelength against the ITU-T grid for its band.
func (a *Auditor) ValidateITUCompliance(wavelength *models.Wavelength) error {
	if wavelength == nil {
		return fmt.Errorf("wavelength is nil")
	}

	// Validate band
	err := ValidateBand(wavelength.LambdaNm, wavelength.Band)
	if err != nil {
		return err
	}

	// Validate grid spacing
	validSpacings := GridSpacings()
	hasValidSpacing := false
	for _, spacing := range validSpacings {
		if math.Abs(spacing-wavelength.GridGHz) < 0.01 {
			hasValidSpacing = true
			break
		}
	}
	if !hasValidSpacing {
		return fmt.Errorf("invalid grid spacing %.2f GHz (supported: %v)", wavelength.GridGHz, validSpacings)
	}

	// Validate channel number
	expectedChannel := ChannelNumberFromWavelength(wavelength.LambdaNm, wavelength.GridGHz)
	if expectedChannel != wavelength.ChannelNum {
		return fmt.Errorf("channel number mismatch: expected %d, got %d for %.2f nm on %.1f GHz grid",
			expectedChannel, wavelength.ChannelNum, wavelength.LambdaNm, wavelength.GridGHz)
	}

	return nil
}

// ValidateDWDMGrid checks if a wavelength is on the standard DWDM grid.
func (a *Auditor) ValidateDWDMGrid(lambdaNm float64, gridGHz float64) (bool, int32) {
	onGrid := IsOnGrid(lambdaNm, gridGHz, ReferenceWavelengthNm)
	chNum := ChannelNumberFromWavelength(lambdaNm, gridGHz)
	return onGrid, chNum
}

// CheckLeaseChainIntegrity verifies the hash chain of all leases.
func (a *Auditor) CheckLeaseChainIntegrity() (bool, []string, error) {
	leases, err := a.store.ListLeases(registry.LeaseFilter{})
	if err != nil {
		return false, nil, fmt.Errorf("failed to list leases: %w", err)
	}

	// Sort by creation time
	sort.Slice(leases, func(i, j int) bool {
		return leases[i].CreatedAt.Before(leases[j].CreatedAt)
	})

	var errors []string
	valid := true

	for i, lease := range leases {
		// Check token hash is present for active leases
		if lease.Status == models.LeaseStatusActive && len(lease.TokenHash) == 0 {
			valid = false
			errors = append(errors, fmt.Sprintf("lease %s is active but has no token hash", lease.ID))
		}

		// Check parent hash chain (if not first lease)
		if i > 0 && len(lease.ParentHash) > 0 {
			prevLease := leases[i-1]
			expectedHash := computeLeaseHash(prevLease)
			if string(lease.ParentHash) != string(expectedHash) {
				valid = false
				errors = append(errors, fmt.Sprintf("hash chain broken at lease %s: parent hash mismatch", lease.ID))
			}
		}
	}

	return valid, errors, nil
}

// VerifyMerkleConsistency checks that Merkle commitments are consistent with stored leases.
func (a *Auditor) VerifyMerkleConsistency() (bool, []string, error) {
	operators, err := a.store.ListOperators()
	if err != nil {
		return false, nil, fmt.Errorf("failed to list operators: %w", err)
	}

	var errors []string
	consistent := true

	for _, op := range operators {
		commitment, err := a.store.GetLatestCommitment(op.ID)
		if err != nil {
			// Operator may not have any commitments yet
			continue
		}
		if commitment == nil {
			continue
		}

		// Get active leases for this operator
		leases, err := a.store.ListLeases(registry.LeaseFilter{OperatorID: op.ID})
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to list leases for %s: %v", op.ID, err))
			consistent = false
			continue
		}

		// Verify lease count matches
		activeCount := int32(0)
		for _, l := range leases {
			if l.Status == models.LeaseStatusActive {
				activeCount++
			}
		}
		if activeCount != commitment.LeaseCount {
			errors = append(errors, fmt.Sprintf("operator %s: commitment lease count %d != actual %d",
				op.ID, commitment.LeaseCount, activeCount))
			consistent = false
		}
	}

	return consistent, errors, nil
}

// computeLeaseHash computes a SHA3-256 hash of a lease for chain integrity checks.
func computeLeaseHash(lease *models.Lease) []byte {
	h := sha3.New256()
	data, _ := json.Marshal(struct {
		ID           string    `json:"id"`
		OperatorID   string    `json:"operator_id"`
		WavelengthKey string   `json:"wavelength_key"`
		Status       string    `json:"status"`
		TokenHash    []byte    `json:"token_hash"`
		StartTime    time.Time `json:"start_time"`
		EndTime      time.Time `json:"end_time"`
	}{
		ID:           lease.ID,
		OperatorID:   lease.OperatorID,
		WavelengthKey: lease.Wavelength.ToKey(),
		Status:       lease.Status.String(),
		TokenHash:    lease.TokenHash,
		StartTime:    lease.StartTime,
		EndTime:      lease.EndTime,
	})
	h.Write(data)
	return h.Sum(nil)
}
