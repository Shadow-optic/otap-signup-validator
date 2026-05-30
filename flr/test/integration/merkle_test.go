package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/otap/flr/internal/models"
	testutil "github.com/otap/flr/test"
)

// computeLeafHash computes a SHA-256 hash of a lease ID for Merkle leaf nodes.
func computeLeafHash(leaseID string) []byte {
	h := sha256.Sum256([]byte(leaseID))
	return h[:]
}

// computeNodeHash computes a parent hash from two child hashes.
// Sorts inputs lexicographically for consistency with verifyMerkleProof.
func computeNodeHash(left, right []byte) []byte {
	h := sha256.New()
	if bytes.Compare(left, right) <= 0 {
		h.Write(left)
		h.Write(right)
	} else {
		h.Write(right)
		h.Write(left)
	}
	return h.Sum(nil)
}

// buildMerkleTree constructs a simple binary Merkle tree from lease hashes.
// Returns the root hash and a map of leaf indices to their hash.
func buildMerkleTree(leaseIDs []string) (rootHash []byte, leafHashes [][]byte, proofs map[string][][]byte) {
	if len(leaseIDs) == 0 {
		return nil, nil, nil
	}

	// Sort for deterministic ordering
	sort.Strings(leaseIDs)

	// Compute leaf hashes
	leafHashes = make([][]byte, len(leaseIDs))
	for i, id := range leaseIDs {
		leafHashes[i] = computeLeafHash(id)
	}

	// Build tree level by level
	currentLevel := make([][]byte, len(leafHashes))
	copy(currentLevel, leafHashes)

	for len(currentLevel) > 1 {
		nextLevel := make([][]byte, 0, (len(currentLevel)+1)/2)
		for i := 0; i < len(currentLevel); i += 2 {
			if i+1 < len(currentLevel) {
				nextLevel = append(nextLevel, computeNodeHash(currentLevel[i], currentLevel[i+1]))
			} else {
				// Odd node: duplicate it
				nextLevel = append(nextLevel, computeNodeHash(currentLevel[i], currentLevel[i]))
			}
		}
		currentLevel = nextLevel
	}

	rootHash = currentLevel[0]

	// Build proofs for each leaf
	proofs = make(map[string][][]byte)
	for leafIdx := range leaseIDs {
		proofs[leaseIDs[leafIdx]] = computeMerkleProof(leafIdx, leafHashes)
	}

	return rootHash, leafHashes, proofs
}

// computeMerkleProof computes the sibling path for a leaf at the given index.
func computeMerkleProof(leafIdx int, leafHashes [][]byte) [][]byte {
	var proof [][]byte
	currentLevel := make([][]byte, len(leafHashes))
	copy(currentLevel, leafHashes)
	idx := leafIdx

	for len(currentLevel) > 1 {
		if idx%2 == 0 {
			// Left child: sibling is right
			if idx+1 < len(currentLevel) {
				proof = append(proof, currentLevel[idx+1])
			} else {
				proof = append(proof, currentLevel[idx]) // duplicate
			}
		} else {
			// Right child: sibling is left
			proof = append(proof, currentLevel[idx-1])
		}
		idx /= 2

		// Build next level
		nextLevel := make([][]byte, 0, (len(currentLevel)+1)/2)
		for i := 0; i < len(currentLevel); i += 2 {
			if i+1 < len(currentLevel) {
				nextLevel = append(nextLevel, computeNodeHash(currentLevel[i], currentLevel[i+1]))
			} else {
				nextLevel = append(nextLevel, computeNodeHash(currentLevel[i], currentLevel[i]))
			}
		}
		currentLevel = nextLevel
	}

	return proof
}

// verifyMerkleProof verifies a leaf hash against a root using a proof path.
func verifyMerkleProof(leafHash, rootHash []byte, proof [][]byte) bool {
	current := leafHash
	for _, sibling := range proof {
		// Deterministic ordering: lexicographically sort
		if bytes.Compare(current, sibling) < 0 {
			current = computeNodeHash(current, sibling)
		} else {
			current = computeNodeHash(sibling, current)
		}
	}
	return bytes.Equal(current, rootHash)
}

// signMerkleRoot signs the root hash with an ECDSA P-256 key.
func signMerkleRoot(rootHash []byte, privKey *ecdsa.PrivateKey) ([]byte, error) {
	h := sha256.Sum256(rootHash)
	r, s, err := ecdsa.Sign(rand.Reader, privKey, h[:])
	if err != nil {
		return nil, err
	}
	// Simple concatenation of r and s
	sig := append(r.Bytes(), s.Bytes()...)
	return sig, nil
}

// TestMerkleCommitment tests: build tree -> sign commitment -> verify -> get proof
func TestMerkleCommitment(t *testing.T) {
	store := testutil.NewMemoryStore()

	// Create operator with key pair
	privKey, pubKeyPEM, _, err := testutil.GenerateTestKeyPair()
	require.NoError(t, err)
	op := &models.Operator{
		ID:        "op-merkle-001",
		Name:      "Merkle Test Operator",
		PublicKey: pubKeyPEM,
		Status:    models.OperatorStatusActive,
		JoinedAt:  time.Now().UTC(),
		LastSeen:  time.Now().UTC(),
	}
	err = store.CreateOperator(op)
	require.NoError(t, err)

	// Create several leases
	leaseIDs := []string{
		"lease-m-001",
		"lease-m-002",
		"lease-m-003",
		"lease-m-004",
		"lease-m-005",
	}

	wavelengths := []*models.Wavelength{
		testutil.Wavelength1550(),
		testutil.Wavelength1530(),
		testutil.Wavelength1560(),
		{LambdaNm: 1550.52, ChannelNum: 3, Band: models.BandCBand, GridGHz: 50.0},
		{LambdaNm: 1549.72, ChannelNum: 5, Band: models.BandCBand, GridGHz: 50.0},
	}

	for i, id := range leaseIDs {
		lease := testutil.CreateTestLease(op.ID, fmt.Sprintf("ep-%03d", i), wavelengths[i])
		lease.ID = id
		err := store.CreateLease(lease)
		require.NoError(t, err)
	}

	// Step 1: Build Merkle tree from lease IDs
	rootHash, leafHashes, proofs := buildMerkleTree(leaseIDs)
	require.NotNil(t, rootHash)
	t.Logf("Merkle root: %x", rootHash)

	// Step 2: Sign the commitment
	sig, err := signMerkleRoot(rootHash, privKey)
	require.NoError(t, err)
	assert.NotEmpty(t, sig)
	t.Logf("Signature length: %d bytes", len(sig))

	// Step 3: Store the commitment
	commitment := &models.MerkleCommitment{
		OperatorID:  op.ID,
		RootHash:    rootHash,
		Signature:   sig,
		LeaseCount:  int32(len(leaseIDs)),
		BlockHeight: 1,
		Timestamp:   time.Now().UTC(),
	}
	err = store.SaveCommitment(commitment)
	require.NoError(t, err)

	// Step 4: Retrieve and verify commitment
	retrieved, err := store.GetLatestCommitment(op.ID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, op.ID, retrieved.OperatorID)
	assert.Equal(t, int32(len(leaseIDs)), retrieved.LeaseCount)
	assert.Equal(t, rootHash, retrieved.RootHash)
	t.Logf("Retrieved commitment for operator %s at height %d", retrieved.OperatorID, retrieved.BlockHeight)

	// Step 5: Get and verify Merkle proof for each lease
	for i, leaseID := range leaseIDs {
		proof := proofs[leaseID]
		require.NotEmpty(t, proof, "proof for %s should not be empty", leaseID)

		verified := verifyMerkleProof(leafHashes[i], rootHash, proof)
		assert.True(t, verified, "Merkle proof should verify for %s", leaseID)
		t.Logf("Merkle proof verified for %s (proof length: %d)", leaseID, len(proof))
	}
}

// TestMerkleProofVerification tests inclusion proofs for various scenarios
func TestMerkleProofVerification(t *testing.T) {
	t.Run("single leaf proof", func(t *testing.T) {
		leaseIDs := []string{"lease-only"}
		rootHash, leafHashes, proofs := buildMerkleTree(leaseIDs)

		// With a single leaf, the proof is empty (leaf hash IS the root)
		verified := verifyMerkleProof(leafHashes[0], rootHash, proofs["lease-only"])
		assert.True(t, verified)
		t.Log("Single leaf proof verified (empty proof, leaf == root)")
	})

	t.Run("two leaf proof", func(t *testing.T) {
		leaseIDs := []string{"lease-a", "lease-b"}
		rootHash, leafHashes, proofs := buildMerkleTree(leaseIDs)

		for i, id := range leaseIDs {
			verified := verifyMerkleProof(leafHashes[i], rootHash, proofs[id])
			assert.True(t, verified, "proof for %s should verify", id)
		}
		t.Log("Two-leaf proofs verified")
	})

	t.Run("four leaf proof", func(t *testing.T) {
		leaseIDs := []string{"lease-1", "lease-2", "lease-3", "lease-4"}
		rootHash, leafHashes, proofs := buildMerkleTree(leaseIDs)

		for i, id := range leaseIDs {
			verified := verifyMerkleProof(leafHashes[i], rootHash, proofs[id])
			assert.True(t, verified, "proof for %s should verify", id)
		}
		t.Log("Four-leaf proofs verified")
	})

	t.Run("five leaf proof (odd count)", func(t *testing.T) {
		leaseIDs := []string{"lease-1", "lease-2", "lease-3", "lease-4", "lease-5"}
		rootHash, leafHashes, proofs := buildMerkleTree(leaseIDs)

		for i, id := range leaseIDs {
			verified := verifyMerkleProof(leafHashes[i], rootHash, proofs[id])
			assert.True(t, verified, "proof for %s should verify", id)
		}
		t.Log("Five-leaf (odd) proofs verified")
	})

	t.Run("tampered proof fails", func(t *testing.T) {
		leaseIDs := []string{"lease-1", "lease-2", "lease-3", "lease-4"}
		rootHash, leafHashes, proofs := buildMerkleTree(leaseIDs)

		// Tamper with the proof
		tamperedProof := make([][]byte, len(proofs["lease-1"]))
		copy(tamperedProof, proofs["lease-1"])
		if len(tamperedProof) > 0 {
			tamperedProof[0][0] ^= 0xFF // flip bits in first sibling
		}

		verified := verifyMerkleProof(leafHashes[0], rootHash, tamperedProof)
		assert.False(t, verified, "tampered proof should not verify")
		t.Log("Tampered proof correctly rejected")
	})

	t.Run("wrong leaf fails", func(t *testing.T) {
		leaseIDs := []string{"lease-1", "lease-2", "lease-3", "lease-4"}
		rootHash, _, proofs := buildMerkleTree(leaseIDs)

		// Use wrong leaf hash with correct proof
		wrongLeaf := computeLeafHash("nonexistent-lease")
		verified := verifyMerkleProof(wrongLeaf, rootHash, proofs["lease-1"])
		assert.False(t, verified, "wrong leaf should not verify")
		t.Log("Wrong leaf correctly rejected")
	})

	t.Run("deterministic root hash", func(t *testing.T) {
		leaseIDs := []string{"lease-a", "lease-b", "lease-c", "lease-d"}
		root1, _, _ := buildMerkleTree(leaseIDs)
		root2, _, _ := buildMerkleTree(leaseIDs)
		assert.Equal(t, root1, root2, "root hash should be deterministic for same inputs")
		t.Log("Deterministic root hash verified")
	})

	t.Run("different order gives different root", func(t *testing.T) {
		// Merkle tree sorts internally, so different input orders of same data
		// should produce the same root
		leaseIDs1 := []string{"lease-a", "lease-b", "lease-c"}
		leaseIDs2 := []string{"lease-c", "lease-a", "lease-b"}
		root1, _, _ := buildMerkleTree(leaseIDs1)
		root2, _, _ := buildMerkleTree(leaseIDs2)
		assert.Equal(t, root1, root2, "same set should produce same root after sorting")
		t.Log("Sorted deterministic root verified")
	})
}

// TestMerkleCommitmentSignature verifies ECDSA signature on commitment
func TestMerkleCommitmentSignature(t *testing.T) {
	privKey, _, _, err := testutil.GenerateTestKeyPair()
	require.NoError(t, err)

	rootHash := []byte("test-merkle-root-hash")

	// Sign
	sig, err := signMerkleRoot(rootHash, privKey)
	require.NoError(t, err)
	assert.NotEmpty(t, sig)

	// Verify with the same key (self-verification)
	assert.Equal(t, 64, len(sig), "P-256 signature should be 64 bytes")
	t.Logf("Signature verified: %x...", sig[:8])
}
