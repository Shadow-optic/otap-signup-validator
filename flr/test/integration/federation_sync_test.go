package integration

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
	testutil "github.com/otap/flr/test"
)

// TestFederationSync tests syncing between two operators
func TestFederationSync(t *testing.T) {
	// Create two separate stores representing two operators
	storeA := testutil.NewMemoryStore()
	storeB := testutil.NewMemoryStore()

	// Operator A setup
	opA := testutil.CreateTestOperator("op-federation-a", "Federation Operator A")
	err := storeA.CreateOperator(opA)
	require.NoError(t, err)
	err = storeB.CreateOperator(opA) // Register A in B's view
	require.NoError(t, err)

	// Operator B setup
	opB := testutil.CreateTestOperator("op-federation-b", "Federation Operator B")
	err = storeB.CreateOperator(opB)
	require.NoError(t, err)
	err = storeA.CreateOperator(opB) // Register B in A's view
	require.NoError(t, err)

	// Create leases on operator A
	wlA1 := testutil.Wavelength1550()
	leaseA1 := testutil.CreateTestLease(opA.ID, "ep-a-001", wlA1)
	leaseA1.ID = "lease-sync-a-001"
	err = storeA.CreateLease(leaseA1)
	require.NoError(t, err)

	wlA2 := testutil.Wavelength1530()
	leaseA2 := testutil.CreateTestLease(opA.ID, "ep-a-002", wlA2)
	leaseA2.ID = "lease-sync-a-002"
	err = storeA.CreateLease(leaseA2)
	require.NoError(t, err)

	// Create leases on operator B
	wlB1 := testutil.Wavelength1560()
	leaseB1 := testutil.CreateTestLease(opB.ID, "ep-b-001", wlB1)
	leaseB1.ID = "lease-sync-b-001"
	err = storeB.CreateLease(leaseB1)
	require.NoError(t, err)

	// Simulate sync: Operator A pushes its leases to B
	aLeases, err := storeA.ListLeases(registry.LeaseFilter{OperatorID: opA.ID})
	require.NoError(t, err)

	for _, lease := range aLeases {
		// In a real federation, this would be a gRPC call
		// Here we simulate by creating a copy in B's store
		leaseCopy := *lease
		leaseCopy.ID = lease.ID + "-synced"
		err := storeB.CreateLease(&leaseCopy)
		require.NoError(t, err)
	}

	// Verify B now has both its own and A's synced leases
	bAllLeases, err := storeB.ListLeases(registry.LeaseFilter{})
	require.NoError(t, err)
	assert.Len(t, bAllLeases, 3, "B should have 1 own + 2 synced leases")
	t.Logf("Operator B has %d leases after sync", len(bAllLeases))

	// Verify synced lease data integrity
	for _, l := range bAllLeases {
		if l.OperatorID == opA.ID {
			assert.Contains(t, l.ID, "-synced")
		}
	}

	// Simulate commitment exchange
	leaseIDsA := []string{"lease-sync-a-001", "lease-sync-a-002"}
	sort.Strings(leaseIDsA)
	rootA, _, _ := buildMerkleTree(leaseIDsA)

	commitmentA := &models.MerkleCommitment{
		OperatorID:  opA.ID,
		RootHash:    rootA,
		LeaseCount:  int32(len(leaseIDsA)),
		BlockHeight: 1,
		Timestamp:   time.Now().UTC(),
	}
	err = storeA.SaveCommitment(commitmentA)
	require.NoError(t, err)
	err = storeB.SaveCommitment(commitmentA) // B receives A's commitment
	require.NoError(t, err)

	// Verify B can retrieve A's commitment
	retrievedCommit, err := storeB.GetLatestCommitment(opA.ID)
	require.NoError(t, err)
	require.NotNil(t, retrievedCommit)
	assert.Equal(t, rootA, retrievedCommit.RootHash)
	t.Logf("Commitment sync verified: root %x", retrievedCommit.RootHash)
}

// TestConflictDetection tests detecting cross-operator double-allocation
func TestConflictDetection(t *testing.T) {
	// Create stores for three operators
	storeA := testutil.NewMemoryStore()
	storeB := testutil.NewMemoryStore()
	storeC := testutil.NewMemoryStore()

	// Setup operators
	opA := testutil.CreateTestOperator("op-conflict-a", "Operator A")
	opB := testutil.CreateTestOperator("op-conflict-b", "Operator B")
	opC := testutil.CreateTestOperator("op-conflict-c", "Operator C")

	for _, store := range []*testutil.MemoryStore{storeA, storeB, storeC} {
		err := store.CreateOperator(opA)
		require.NoError(t, err)
		err = store.CreateOperator(opB)
		require.NoError(t, err)
		err = store.CreateOperator(opC)
		require.NoError(t, err)
	}

	// All operators allocate the SAME wavelength - this is a conflict
	conflictWavelength := testutil.Wavelength1550()

	// Operator A allocates 1550.12
	leaseA := testutil.CreateTestLease(opA.ID, "ep-a-001", conflictWavelength)
	leaseA.ID = "lease-conflict-a-001"
	err := storeA.CreateLease(leaseA)
	require.NoError(t, err)

	// Operator B also allocates 1550.12 (cross-operator conflict)
	leaseB := testutil.CreateTestLease(opB.ID, "ep-b-001", conflictWavelength)
	leaseB.ID = "lease-conflict-b-001"
	err = storeB.CreateLease(leaseB)
	require.NoError(t, err)

	// Operator C allocates a different wavelength (no conflict)
	leaseC := testutil.CreateTestLease(opC.ID, "ep-c-001", testutil.Wavelength1530())
	leaseC.ID = "lease-conflict-c-001"
	err = storeC.CreateLease(leaseC)
	require.NoError(t, err)

	// Simulate federation-wide conflict detection
	// Collect all active leases from all operators
	type leaseInfo struct {
		Lease      *models.Lease
		OperatorID string
	}

	allLeases := make(map[string][]leaseInfo) // key: wavelength key

	// Collect from A
	aLeases, err := storeA.ListLeases(registry.LeaseFilter{Status: models.LeaseStatusActive})
	require.NoError(t, err)
	for _, l := range aLeases {
		if l.Wavelength != nil {
			key := l.Wavelength.ToKey()
			allLeases[key] = append(allLeases[key], leaseInfo{Lease: l, OperatorID: opA.ID})
		}
	}

	// Collect from B
	bLeases, err := storeB.ListLeases(registry.LeaseFilter{Status: models.LeaseStatusActive})
	require.NoError(t, err)
	for _, l := range bLeases {
		if l.Wavelength != nil {
			key := l.Wavelength.ToKey()
			allLeases[key] = append(allLeases[key], leaseInfo{Lease: l, OperatorID: opB.ID})
		}
	}

	// Collect from C
	cLeases, err := storeC.ListLeases(registry.LeaseFilter{Status: models.LeaseStatusActive})
	require.NoError(t, err)
	for _, l := range cLeases {
		if l.Wavelength != nil {
			key := l.Wavelength.ToKey()
			allLeases[key] = append(allLeases[key], leaseInfo{Lease: l, OperatorID: opC.ID})
		}
	}

	// Detect conflicts
	var conflicts []struct {
		WavelengthKey string
		Leases        []leaseInfo
	}
	for wlKey, leases := range allLeases {
		if len(leases) > 1 {
			conflicts = append(conflicts, struct {
				WavelengthKey string
				Leases        []leaseInfo
			}{
				WavelengthKey: wlKey,
				Leases:        leases,
			})
		}
	}

	// Should have exactly one conflict on 1550.12
	require.Len(t, conflicts, 1, "should detect exactly one wavelength conflict")
	assert.Equal(t, conflictWavelength.ToKey(), conflicts[0].WavelengthKey)
	assert.Len(t, conflicts[0].Leases, 2, "should have 2 conflicting leases")

	// Verify the conflict involves operators A and B
	var conflictOpIDs []string
	for _, li := range conflicts[0].Leases {
		conflictOpIDs = append(conflictOpIDs, li.OperatorID)
	}
	sort.Strings(conflictOpIDs)
	assert.Equal(t, []string{opA.ID, opB.ID}, conflictOpIDs)

	t.Logf("Conflict detected on %s between operators: %v", conflicts[0].WavelengthKey, conflictOpIDs)

	// Generate proof of invalidity for the conflict
	poi := &models.ProofOfInvalidity{
		Type:      models.InvalidityDoubleAllocation,
		LeaseA:    conflicts[0].Leases[0].Lease,
		LeaseB:    conflicts[0].Leases[1].Lease,
		Timestamp: time.Now().UTC(),
	}

	// Compute a Merkle proof for the conflict
	conflictLeaseIDs := []string{conflicts[0].Leases[0].Lease.ID, conflicts[0].Leases[1].Lease.ID}
	rootHash, _, conflictProofs := buildMerkleTree(conflictLeaseIDs)
	poi.Commitment = &models.MerkleCommitment{
		OperatorID:  "federation-auditor",
		RootHash:    rootHash,
		LeaseCount:  2,
		BlockHeight: 1,
		Timestamp:   time.Now().UTC(),
	}
	poi.MerkleProof = conflictProofs[conflicts[0].Leases[0].Lease.ID]

	assert.NotNil(t, poi)
	assert.Equal(t, models.InvalidityDoubleAllocation, poi.Type)
	t.Logf("Proof of invalidity generated for double-allocation")

	// Verify C's lease is NOT in conflicts
	for _, c := range conflicts {
		for _, li := range c.Leases {
			assert.NotEqual(t, opC.ID, li.OperatorID, "Operator C should not be in conflicts")
		}
	}
}

// TestFederationOperatorRegistration tests operator registration across federation
func TestFederationOperatorRegistration(t *testing.T) {
	store := testutil.NewMemoryStore()

	// Register multiple operators
	operators := []*models.Operator{
		testutil.CreateTestOperator("op-fed-001", "Federation Node 1"),
		testutil.CreateTestOperator("op-fed-002", "Federation Node 2"),
		testutil.CreateTestOperator("op-fed-003", "Federation Node 3"),
	}

	for _, op := range operators {
		err := store.CreateOperator(op)
		require.NoError(t, err)
	}

	// List all operators
	listed, err := store.ListOperators()
	require.NoError(t, err)
	assert.Len(t, listed, 3)

	// Verify each operator
	for _, op := range operators {
		retrieved, err := store.GetOperator(op.ID)
		require.NoError(t, err)
		assert.Equal(t, op.Name, retrieved.Name)
		assert.Equal(t, op.Status, retrieved.Status)
		assert.NotEmpty(t, retrieved.PublicKey)
	}

	t.Logf("All %d federation operators registered and verified", len(operators))
}

// TestCrossOperatorLeaseLookup tests looking up leases across operators
func TestCrossOperatorLeaseLookup(t *testing.T) {
	store := testutil.NewMemoryStore()

	opA := testutil.CreateTestOperator("op-lookup-a", "Lookup Operator A")
	opB := testutil.CreateTestOperator("op-lookup-b", "Lookup Operator B")
	err := store.CreateOperator(opA)
	require.NoError(t, err)
	err = store.CreateOperator(opB)
	require.NoError(t, err)

	// Create leases for both operators
	for i := 0; i < 5; i++ {
		wl := &models.Wavelength{
			LambdaNm:   1550.12 - float64(i)*0.4,
			ChannelNum: int32(4 - i),
			Band:       models.BandCBand,
			GridGHz:    50.0,
		}
		lease := testutil.CreateTestLease(opA.ID, fmt.Sprintf("ep-a-%03d", i), wl)
		lease.ID = fmt.Sprintf("lease-lookup-a-%03d", i)
		err := store.CreateLease(lease)
		require.NoError(t, err)
	}

	for i := 0; i < 3; i++ {
		wl := &models.Wavelength{
			LambdaNm:   1530.33 + float64(i)*0.4,
			ChannelNum: int32(-34 + i),
			Band:       models.BandCBand,
			GridGHz:    50.0,
		}
		lease := testutil.CreateTestLease(opB.ID, fmt.Sprintf("ep-b-%03d", i), wl)
		lease.ID = fmt.Sprintf("lease-lookup-b-%03d", i)
		err := store.CreateLease(lease)
		require.NoError(t, err)
	}

	// Lookup A's leases
	aLeases, err := store.ListLeases(registry.LeaseFilter{OperatorID: opA.ID})
	require.NoError(t, err)
	assert.Len(t, aLeases, 5)

	// Lookup B's leases
	bLeases, err := store.ListLeases(registry.LeaseFilter{OperatorID: opB.ID})
	require.NoError(t, err)
	assert.Len(t, bLeases, 3)

	// All leases
	allLeases, err := store.ListLeases(registry.LeaseFilter{})
	require.NoError(t, err)
	assert.Len(t, allLeases, 8)

	t.Logf("Operator A: %d leases, Operator B: %d leases, Total: %d", len(aLeases), len(bLeases), len(allLeases))
}


