package federation

import (
	"time"

	"github.com/otap/flr/internal/models"
)

// conflictPair represents a detected conflict between two leases.
type conflictPair struct {
	Local  *models.Lease
	Remote *models.Lease
	Type   models.InvalidityType
}

// checkDoubleAllocation checks if two leases allocate the same wavelength
// with overlapping time periods.
func checkDoubleAllocation(a, b *models.Lease) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Wavelength == nil || b.Wavelength == nil {
		return false
	}

	// Check if wavelengths match by comparing key attributes
	if a.Wavelength.LambdaNm != b.Wavelength.LambdaNm {
		return false
	}
	if a.Wavelength.ChannelNum != b.Wavelength.ChannelNum {
		return false
	}
	if a.Wavelength.Band != b.Wavelength.Band {
		return false
	}

	// Check time overlap
	now := time.Now().UTC()
	if !a.StartTime.Before(now) || !b.StartTime.Before(now) {
		// Both leases should have started for a double-allocation to be detected
		return false
	}
	if a.EndTime.Before(now) || b.EndTime.Before(now) {
		// Both leases should still be active
		return false
	}

	// Check if time windows overlap
	if a.EndTime.Before(b.StartTime) || b.EndTime.Before(a.StartTime) {
		return false // No time overlap
	}

	return true
}

// checkExpiredLease checks if a lease has expired but is still marked active.
func checkExpiredLease(lease *models.Lease) bool {
	if lease == nil {
		return false
	}
	now := time.Now().UTC()
	return lease.EndTime.Before(now) && lease.Status == models.LeaseStatusActive
}

// findConflicts finds all conflicts between local and remote lease sets.
// It returns conflict pairs for double-allocations and expired leases.
func findConflicts(local, remote []*models.Lease) []*conflictPair {
	var conflicts []*conflictPair
	now := time.Now().UTC()

	// Check for expired leases in local set
	for _, l := range local {
		if checkExpiredLease(l) {
			conflicts = append(conflicts, &conflictPair{
				Local: l,
				Type:  models.InvalidityExpiredLease,
			})
		}
	}

	// Check for expired leases in remote set
	for _, r := range remote {
		if checkExpiredLease(r) {
			conflicts = append(conflicts, &conflictPair{
				Remote: r,
				Type:   models.InvalidityExpiredLease,
			})
		}
	}

	// Check for double-allocations between local and remote
	for _, l := range local {
		if l.Wavelength == nil {
			continue
		}
		// Skip if local lease is not active
		if l.Status != models.LeaseStatusActive {
			continue
		}
		// Skip if local lease has expired
		if l.EndTime.Before(now) {
			continue
		}
		for _, r := range remote {
			if r.Wavelength == nil {
				continue
			}
			// Skip if remote lease is not active
			if r.Status != models.LeaseStatusActive {
				continue
			}
			// Skip if remote lease has expired
			if r.EndTime.Before(now) {
				continue
			}

			if checkDoubleAllocation(l, r) {
				conflicts = append(conflicts, &conflictPair{
					Local:  l,
					Remote: r,
					Type:   models.InvalidityDoubleAllocation,
				})
			}
		}
	}

	return conflicts
}
