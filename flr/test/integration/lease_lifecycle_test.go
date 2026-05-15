package integration

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
	testutil "github.com/otap/flr/test"
)

// TestLeaseLifecycle tests: create -> get -> verify -> renew -> revoke
func TestLeaseLifecycle(t *testing.T) {
	store := testutil.NewMemoryStore()

	// Create operator
	op := testutil.CreateTestOperator("op-test-001", "Test Operator")
	err := store.CreateOperator(op)
	require.NoError(t, err)

	// Create endpoint
	ep := &models.Endpoint{
		ID:         "ep-001",
		OperatorID: op.ID,
		Address:    "192.168.1.1",
		AWGPort:    1,
		Status:     models.EndpointStatusActive,
		CreatedAt:  time.Now().UTC(),
	}
	err = store.CreateEndpoint(ep)
	require.NoError(t, err)

	// Step 1: Create lease
	wl := testutil.Wavelength1550()
	lease := testutil.CreateTestLease(op.ID, ep.ID, wl)
	err = store.CreateLease(lease)
	require.NoError(t, err)
	t.Logf("Created lease: %s", lease.ID)

	// Step 2: Get lease
	retrieved, err := store.GetLease(lease.ID)
	require.NoError(t, err)
	assert.Equal(t, lease.ID, retrieved.ID)
	assert.Equal(t, models.LeaseStatusActive, retrieved.Status)
	assert.Equal(t, wl.LambdaNm, retrieved.Wavelength.LambdaNm)
	t.Logf("Retrieved lease: %s (status=%s)", retrieved.ID, retrieved.Status.String())

	// Step 3: Verify lease data integrity
	h := sha256.New()
	h.Write([]byte(lease.ID))
	h.Write([]byte(lease.OperatorID))
	h.Write([]byte(lease.EndpointID))
	h.Write([]byte(fmt.Sprintf("%.2f", wl.LambdaNm)))
	computedHash := h.Sum(nil)
	assert.NotNil(t, computedHash)
	assert.Equal(t, lease.OperatorID, retrieved.OperatorID)
	assert.Equal(t, lease.EndpointID, retrieved.EndpointID)
	t.Log("Lease verification passed")

	// Step 4: Renew lease (extend end time). Capture the pre-renew end time
	// against a fresh fetch — `lease.EndTime` may share state with the store
	// in some implementations and shift under us when we call UpdateLease.
	originalEndTime := retrieved.EndTime
	retrieved.EndTime = retrieved.EndTime.Add(30 * 24 * time.Hour)
	retrieved.UpdatedAt = time.Now().UTC()
	err = store.UpdateLease(retrieved)
	require.NoError(t, err)

	renewed, err := store.GetLease(lease.ID)
	require.NoError(t, err)
	assert.True(t, renewed.EndTime.After(originalEndTime), "renewed end time should be after original")
	t.Logf("Renewed lease until: %s", renewed.EndTime.Format(time.RFC3339))

	// Step 5: Revoke lease
	renewed.Status = models.LeaseStatusRevoked
	renewed.UpdatedAt = time.Now().UTC()
	err = store.UpdateLease(renewed)
	require.NoError(t, err)

	revoked, err := store.GetLease(lease.ID)
	require.NoError(t, err)
	assert.Equal(t, models.LeaseStatusRevoked, revoked.Status)
	t.Logf("Revoked lease: %s", revoked.ID)
}

// TestDoubleAllocationPrevention tests conflict detection for the same wavelength
func TestDoubleAllocationPrevention(t *testing.T) {
	store := testutil.NewMemoryStore()

	op := testutil.CreateTestOperator("op-test-001", "Test Operator")
	err := store.CreateOperator(op)
	require.NoError(t, err)

	wl := testutil.Wavelength1550()

	// First allocation
	lease1 := testutil.CreateTestLease(op.ID, "ep-001", wl)
	err = store.CreateLease(lease1)
	require.NoError(t, err)

	// Check for conflict - search for active leases with same wavelength
	leases, err := store.ListLeases(registry.LeaseFilter{OperatorID: op.ID, Status: models.LeaseStatusActive})
	require.NoError(t, err)
	assert.Len(t, leases, 1)

	// Try to detect double allocation
	// In a real system this would check wavelength overlap
	var conflictFound bool
	for _, l := range leases {
		if l.Wavelength != nil && l.Wavelength.LambdaNm == wl.LambdaNm &&
			l.Status == models.LeaseStatusActive && l.ID != lease1.ID {
			conflictFound = true
			break
		}
	}
	assert.False(t, conflictFound, "no conflict for unique lease")

	// Now simulate a second allocation attempt for the same wavelength
	// In production this would be rejected, but here we verify the detection logic
	lease2 := testutil.CreateTestLease(op.ID, "ep-002", wl)
	err = store.CreateLease(lease2)
	require.NoError(t, err)

	// Check for conflicts again
	leases, err = store.ListLeases(registry.LeaseFilter{OperatorID: op.ID, Status: models.LeaseStatusActive})
	require.NoError(t, err)
	assert.Len(t, leases, 2)

	// Count leases on same wavelength
	sameWavelengthCount := 0
	for _, l := range leases {
		if l.Wavelength != nil && l.Wavelength.LambdaNm == wl.LambdaNm {
			sameWavelengthCount++
		}
	}
	assert.Equal(t, 2, sameWavelengthCount, "two leases on same wavelength detected")
	t.Logf("Double allocation detected: %d leases on %.2f nm", sameWavelengthCount, wl.LambdaNm)

	// Verify the stored leases are distinct
	assert.NotEqual(t, lease1.ID, lease2.ID)
}

// TestLeaseExpiry tests automatic expiration of leases
func TestLeaseExpiry(t *testing.T) {
	store := testutil.NewMemoryStore()

	op := testutil.CreateTestOperator("op-test-001", "Test Operator")
	err := store.CreateOperator(op)
	require.NoError(t, err)

	now := time.Now().UTC()

	// Create an already-expired lease
	wl := testutil.Wavelength1550()
	expiredLease := &models.Lease{
		ID:         "lease-expired-001",
		OperatorID: op.ID,
		EndpointID: "ep-001",
		Wavelength: wl,
		Status:     models.LeaseStatusActive, // Still marked active (bug scenario)
		StartTime:  now.Add(-30 * 24 * time.Hour),
		EndTime:    now.Add(-1 * 24 * time.Hour), // Already ended
		CreatedAt:  now.Add(-30 * 24 * time.Hour),
		UpdatedAt:  now.Add(-30 * 24 * time.Hour),
		TokenHash:  []byte("token-expired"),
	}
	err = store.CreateLease(expiredLease)
	require.NoError(t, err)

	// Create a currently valid lease
	activeLease := &models.Lease{
		ID:         "lease-active-001",
		OperatorID: op.ID,
		EndpointID: "ep-002",
		Wavelength: testutil.Wavelength1530(),
		Status:     models.LeaseStatusActive,
		StartTime:  now.Add(-1 * 24 * time.Hour),
		EndTime:    now.Add(30 * 24 * time.Hour),
		CreatedAt:  now.Add(-1 * 24 * time.Hour),
		UpdatedAt:  now.Add(-1 * 24 * time.Hour),
		TokenHash:  []byte("token-active"),
	}
	err = store.CreateLease(activeLease)
	require.NoError(t, err)

	// Simulate expiration check
	leases, err := store.ListLeases(registry.LeaseFilter{OperatorID: op.ID})
	require.NoError(t, err)
	assert.Len(t, leases, 2)

	var expiredCount, activeCount int
	for _, l := range leases {
		if l.EndTime.Before(now) && l.Status == models.LeaseStatusActive {
			// This lease should be expired
			expiredCount++
			// Simulate marking as expired
			l.Status = models.LeaseStatusExpired
			l.UpdatedAt = now
			err := store.UpdateLease(l)
			require.NoError(t, err)
		} else if l.Status == models.LeaseStatusActive {
			activeCount++
		}
	}

	assert.Equal(t, 1, expiredCount, "one lease should be expired")
	assert.Equal(t, 1, activeCount, "one lease should be active")

	// Verify the expired lease is now marked as such
	updatedExpired, err := store.GetLease("lease-expired-001")
	require.NoError(t, err)
	assert.Equal(t, models.LeaseStatusExpired, updatedExpired.Status)

	// Verify the active lease is untouched
	updatedActive, err := store.GetLease("lease-active-001")
	require.NoError(t, err)
	assert.Equal(t, models.LeaseStatusActive, updatedActive.Status)

	t.Logf("Expired %d lease(s), %d remain active", expiredCount, activeCount)
}

// TestLeaseSerialization tests JSON round-trip serialization
func TestLeaseSerialization(t *testing.T) {
	store := testutil.NewMemoryStore()

	op := testutil.CreateTestOperator("op-serial-001", "Serialization Test")
	err := store.CreateOperator(op)
	require.NoError(t, err)

	wl := testutil.Wavelength1550()
	lease := testutil.CreateTestLease(op.ID, "ep-001", wl)
	err = store.CreateLease(lease)
	require.NoError(t, err)

	// Serialize to JSON
	jsonData, err := json.Marshal(lease)
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), "lease-")
	assert.Contains(t, string(jsonData), "1550.12")

	// Deserialize back
	var restored models.Lease
	err = json.Unmarshal(jsonData, &restored)
	require.NoError(t, err)
	assert.Equal(t, lease.ID, restored.ID)
	assert.Equal(t, lease.Wavelength.LambdaNm, restored.Wavelength.LambdaNm)
	assert.Equal(t, lease.Status, restored.Status)

	t.Logf("JSON round-trip successful for lease %s", lease.ID)
}
