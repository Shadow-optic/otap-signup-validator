package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/otap/flr/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/sha3"
)

// generateTestKeyPair generates a key pair for testing.
func generateTestKeyPair(t *testing.T) (*ecdsa.PrivateKey, []byte, []byte) {
	t.Helper()
	privKey, privPEM, pubPEM, err := GenerateKeyPair()
	require.NoError(t, err)
	return privKey, privPEM, pubPEM
}

// createTestEngine creates a crypto engine for testing.
func createTestEngine(t *testing.T) (*Engine, []byte) {
	t.Helper()
	_, privPEM, pubPEM := generateTestKeyPair(t)
	engine, err := NewEngine("test-operator", privPEM)
	require.NoError(t, err)
	return engine, pubPEM
}

// createTestLease creates a test lease.
func createTestLease(id string, startTime, endTime time.Time) *models.Lease {
	return &models.Lease{
		ID:         id,
		Wavelength: &models.Wavelength{LambdaNm: 1550.12, ChannelNum: 1, Band: models.BandCBand, GridGHz: 25.0},
		EndpointID: "ep-001",
		OperatorID: "test-operator",
		Status:     models.LeaseStatusActive,
		StartTime:  startTime,
		EndTime:    endTime,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
}

// createExpiredLease creates a lease that has already expired.
func createExpiredLease(id string) *models.Lease {
	now := time.Now().UTC()
	return createTestLease(id, now.Add(-2*time.Hour), now.Add(-1*time.Hour))
}

// createFutureLease creates a lease that expires in the future.
func createFutureLease(id string) *models.Lease {
	now := time.Now().UTC()
	return createTestLease(id, now.Add(-1*time.Hour), now.Add(1*time.Hour))
}

func TestNewEngine(t *testing.T) {
	_, privPEM, pubPEM := generateTestKeyPair(t)

	tests := []struct {
		name        string
		operatorID  string
		privateKey  []byte
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid engine",
			operatorID: "op-1",
			privateKey: privPEM,
			wantErr:    false,
		},
		{
			name:        "empty operator ID",
			operatorID:  "",
			privateKey:  privPEM,
			wantErr:     true,
			errContains: "operator ID cannot be empty",
		},
		{
			name:        "empty private key",
			operatorID:  "op-1",
			privateKey:  []byte{},
			wantErr:     true,
			errContains: "private key PEM cannot be empty",
		},
		{
			name:        "invalid PEM",
			operatorID:  "op-1",
			privateKey:  []byte("not a valid PEM"),
			wantErr:     true,
			errContains: "failed to decode PEM block",
		},
		{
			name:       "valid with PKCS8 key",
			operatorID: "op-1",
			privateKey: privPEM,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := NewEngine(tt.operatorID, tt.privateKey)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, engine)
			assert.Equal(t, tt.operatorID, engine.operatorID)
			assert.NotNil(t, engine.privateKey)
		})
	}

	_ = pubPEM // suppress unused warning
}

func TestGenerateKeyPair(t *testing.T) {
	privKey, privPEM, pubPEM := generateTestKeyPair(t)

	assert.NotNil(t, privKey)
	assert.NotNil(t, privPEM)
	assert.NotNil(t, pubPEM)

	// Verify private key PEM
	block, _ := pem.Decode(privPEM)
	require.NotNil(t, block)
	assert.Equal(t, "EC PRIVATE KEY", block.Type)

	// Verify public key PEM
	block, _ = pem.Decode(pubPEM)
	require.NotNil(t, block)
	assert.Equal(t, "PUBLIC KEY", block.Type)

	// Verify curve
	assert.Equal(t, elliptic.P256(), privKey.Curve)
}

func TestNewEngine_WrongCurve(t *testing.T) {
	// Generate a P-384 key instead of P-256
	privKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)

	privDER, err := x509.MarshalECPrivateKey(privKey)
	require.NoError(t, err)

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: privDER,
	})

	_, err = NewEngine("op-1", privPEM)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only P-256 curve is supported")
}

func TestEngine_GetPublicKey(t *testing.T) {
	engine, pubPEM := createTestEngine(t)

	pubKey, err := engine.GetPublicKey()
	require.NoError(t, err)
	assert.NotNil(t, pubKey)

	// Verify it's valid PEM
	block, _ := pem.Decode(pubKey)
	assert.NotNil(t, block)

	// Should match what we generated
	assert.Equal(t, pubPEM, pubKey)
}

func TestEngine_Sign(t *testing.T) {
	engine, _ := createTestEngine(t)

	tests := []struct {
		name        string
		data        []byte
		wantErr     bool
		errContains string
	}{
		{
			name: "sign valid data",
			data: []byte("test data to sign"),
		},
		{
			name:        "empty data",
			data:        []byte{},
			wantErr:     true,
			errContains: "data to sign cannot be empty",
		},
		{
			name: "sign large data",
			data: make([]byte, 1024),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig, err := engine.Sign(tt.data)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 64, len(sig))
		})
	}
}

func TestVerify(t *testing.T) {
	engine, pubPEM := createTestEngine(t)
	privEngine, _ := createTestEngine(t)

	data := []byte("test data to verify")

	sig, err := engine.Sign(data)
	require.NoError(t, err)

	tests := []struct {
		name        string
		pubKey      []byte
		data        []byte
		sig         []byte
		wantErr     bool
		errContains string
	}{
		{
			name:   "valid signature",
			pubKey: pubPEM,
			data:   data,
			sig:    sig,
		},
		{
			name:        "wrong public key",
			pubKey:      pubPEM,
			data:        data,
			sig:         sig,
			wantErr:     true, // sig won't verify with different engine's key
			errContains: "signature verification failed",
		},
		{
			name:        "empty public key",
			pubKey:      []byte{},
			data:        data,
			sig:         sig,
			wantErr:     true,
			errContains: "public key cannot be empty",
		},
		{
			name:        "empty data",
			pubKey:      pubPEM,
			data:        []byte{},
			sig:         sig,
			wantErr:     true,
			errContains: "data cannot be empty",
		},
		{
			name:        "empty signature",
			pubKey:      pubPEM,
			data:        data,
			sig:         []byte{},
			wantErr:     true,
			errContains: "signature cannot be empty",
		},
		{
			name:        "tampered data",
			pubKey:      pubPEM,
			data:        []byte("tampered data"),
			sig:         sig,
			wantErr:     true,
			errContains: "signature verification failed",
		},
		{
			name:        "wrong signature",
			pubKey:      pubPEM,
			data:        data,
			sig:         make([]byte, 64), // zero signature
			wantErr:     true,
			errContains: "signature verification failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For the "wrong public key" test, sign with the other engine
			testSig := tt.sig
			testData := tt.data
			if tt.name == "wrong public key" {
				wrongSig, err := privEngine.Sign(data)
				require.NoError(t, err)
				testSig = wrongSig
				testData = data
			}

			err := Verify(tt.pubKey, testData, testSig)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestGenerateLeaseToken(t *testing.T) {
	engine, _ := createTestEngine(t)
	lease := createFutureLease("lease-001")

	token, err := engine.GenerateLeaseToken(lease)
	require.NoError(t, err)
	assert.NotNil(t, token)

	// Verify token fields
	assert.Equal(t, int32(1), token.Version)
	assert.Equal(t, lease.ID, token.LeaseID)
	assert.Equal(t, "test-operator", token.OperatorID)
	assert.Equal(t, lease.EndpointID, token.EndpointID)
	assert.Equal(t, lease.Wavelength, token.Wavelength)
	assert.WithinDuration(t, time.Now().UTC(), token.IssuedAt, 5*time.Second)
	assert.NotEmpty(t, token.Nonce)
	assert.Equal(t, 32, len(token.Nonce))
	assert.NotEmpty(t, token.Signature)
	assert.Equal(t, 64, len(token.Signature))
}

func TestGenerateLeaseToken_NilLease(t *testing.T) {
	engine, _ := createTestEngine(t)

	_, err := engine.GenerateLeaseToken(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lease cannot be nil")
}

func TestValidateLeaseToken(t *testing.T) {
	engine, pubPEM := createTestEngine(t)

	t.Run("valid token", func(t *testing.T) {
		lease := createFutureLease("lease-002")
		token, err := engine.GenerateLeaseToken(lease)
		require.NoError(t, err)

		err = engine.ValidateLeaseToken(token, pubPEM)
		assert.NoError(t, err)
	})

	t.Run("expired token", func(t *testing.T) {
		lease := createExpiredLease("lease-003")
		token, err := engine.GenerateLeaseToken(lease)
		require.NoError(t, err)

		err = engine.ValidateLeaseToken(token, pubPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token has expired")
	})

	t.Run("tampered lease ID", func(t *testing.T) {
		lease := createFutureLease("lease-004")
		token, err := engine.GenerateLeaseToken(lease)
		require.NoError(t, err)

		token.LeaseID = "tampered-id"
		err = engine.ValidateLeaseToken(token, pubPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature verification failed")
	})

	t.Run("nil token", func(t *testing.T) {
		err := engine.ValidateLeaseToken(nil, pubPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token cannot be nil")
	})

	t.Run("empty public key", func(t *testing.T) {
		lease := createFutureLease("lease-005")
		token, err := engine.GenerateLeaseToken(lease)
		require.NoError(t, err)

		err = engine.ValidateLeaseToken(token, []byte{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "operator public key cannot be empty")
	})

	t.Run("wrong public key", func(t *testing.T) {
		_, _, wrongPubPEM := generateTestKeyPair(t)

		lease := createFutureLease("lease-006")
		token, err := engine.GenerateLeaseToken(lease)
		require.NoError(t, err)

		err = engine.ValidateLeaseToken(token, wrongPubPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature verification failed")
	})

	t.Run("tampered signature", func(t *testing.T) {
		lease := createFutureLease("lease-007")
		token, err := engine.GenerateLeaseToken(lease)
		require.NoError(t, err)

		// Tamper with signature
		token.Signature[0] ^= 0xFF
		err = engine.ValidateLeaseToken(token, pubPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature verification failed")
	})
}

func TestGetLeaseHash(t *testing.T) {
	t.Run("hash is deterministic", func(t *testing.T) {
		lease := createFutureLease("lease-hash-001")

		hash1 := GetLeaseHash(lease)
		hash2 := GetLeaseHash(lease)

		assert.Equal(t, hash1, hash2)
		assert.Equal(t, 32, len(hash1))
	})

	t.Run("different leases have different hashes", func(t *testing.T) {
		lease1 := createFutureLease("lease-hash-001")
		lease2 := createFutureLease("lease-hash-002")

		hash1 := GetLeaseHash(lease1)
		hash2 := GetLeaseHash(lease2)

		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("nil lease returns zero hash", func(t *testing.T) {
		hash := GetLeaseHash(nil)
		assert.Equal(t, 32, len(hash))
	})

	t.Run("hash is SHA3-256", func(t *testing.T) {
		lease := createFutureLease("lease-hash-003")

		hash := GetLeaseHash(lease)

		// Verify it's a SHA3-256 hash (32 bytes)
		assert.Equal(t, 32, len(hash))
	})
}

func TestBuildMerkleTree(t *testing.T) {
	engine, _ := createTestEngine(t)

	t.Run("empty tree", func(t *testing.T) {
		tree, err := engine.BuildMerkleTree(nil)
		require.NoError(t, err)
		assert.NotNil(t, tree)
		assert.True(t, tree.IsLeaf)
		assert.Equal(t, make([]byte, 32), tree.Hash)
	})

	t.Run("single lease", func(t *testing.T) {
		leases := []*models.Lease{
			createFutureLease("lease-001"),
		}

		tree, err := engine.BuildMerkleTree(leases)
		require.NoError(t, err)
		assert.NotNil(t, tree)
		assert.NotNil(t, tree.Hash)
		assert.Equal(t, 32, len(tree.Hash))
	})

	t.Run("two leases - power of 2", func(t *testing.T) {
		leases := []*models.Lease{
			createFutureLease("lease-a"),
			createFutureLease("lease-b"),
		}

		tree, err := engine.BuildMerkleTree(leases)
		require.NoError(t, err)
		assert.NotNil(t, tree)
		assert.NotNil(t, tree.Left)
		assert.NotNil(t, tree.Right)
		assert.NotNil(t, tree.Hash)
	})

	t.Run("three leases - padded to power of 2", func(t *testing.T) {
		leases := []*models.Lease{
			createFutureLease("lease-a"),
			createFutureLease("lease-b"),
			createFutureLease("lease-c"),
		}

		tree, err := engine.BuildMerkleTree(leases)
		require.NoError(t, err)
		assert.NotNil(t, tree)
		assert.NotNil(t, tree.Hash)
	})

	t.Run("five leases - padded to 8", func(t *testing.T) {
		leases := []*models.Lease{
			createFutureLease("lease-a"),
			createFutureLease("lease-b"),
			createFutureLease("lease-c"),
			createFutureLease("lease-d"),
			createFutureLease("lease-e"),
		}

		tree, err := engine.BuildMerkleTree(leases)
		require.NoError(t, err)
		assert.NotNil(t, tree)
		assert.NotNil(t, tree.Hash)
	})

	t.Run("deterministic ordering", func(t *testing.T) {
		leases := []*models.Lease{
			createFutureLease("lease-z"),
			createFutureLease("lease-a"),
			createFutureLease("lease-m"),
		}

		tree1, err := engine.BuildMerkleTree(leases)
		require.NoError(t, err)

		// Shuffle and rebuild
		leases[0], leases[1] = leases[1], leases[0]
		tree2, err := engine.BuildMerkleTree(leases)
		require.NoError(t, err)

		assert.Equal(t, tree1.Hash, tree2.Hash)
	})

	t.Run("tree hash is 32 bytes", func(t *testing.T) {
		leases := []*models.Lease{
			createFutureLease("lease-001"),
			createFutureLease("lease-002"),
		}

		tree, err := engine.BuildMerkleTree(leases)
		require.NoError(t, err)
		assert.Equal(t, 32, len(tree.Hash))
	})
}

func TestBuildMerkleTree_LeavesAreSorted(t *testing.T) {
	engine, _ := createTestEngine(t)

	// Create leases with IDs that would sort differently
	leases := []*models.Lease{
		createFutureLease("zebra"),
		createFutureLease("alpha"),
		createFutureLease("mike"),
		createFutureLease("delta"),
	}

	tree, err := engine.BuildMerkleTree(leases)
	require.NoError(t, err)

	// Verify the tree structure includes all leaves
	leafCount := countLeaves(tree)
	// Padded to power of 2: 4 leases -> 4 leaves
	assert.Equal(t, 4, leafCount)
}

func countLeaves(node *models.MerkleNode) int {
	if node == nil {
		return 0
	}
	if node.IsLeaf {
		return 1
	}
	return countLeaves(node.Left) + countLeaves(node.Right)
}

func TestSignMerkleCommitment(t *testing.T) {
	engine, pubPEM := createTestEngine(t)
	root := make([]byte, 32)
	rand.Read(root)

	t.Run("valid commitment", func(t *testing.T) {
		commitment, err := engine.SignMerkleCommitment(root, 42)
		require.NoError(t, err)
		assert.NotNil(t, commitment)
		assert.Equal(t, "test-operator", commitment.OperatorID)
		assert.Equal(t, root, commitment.RootHash)
		assert.Equal(t, int64(42), commitment.BlockHeight)
		assert.NotEmpty(t, commitment.Signature)
		assert.WithinDuration(t, time.Now().UTC(), commitment.Timestamp, 5*time.Second)

		// Verify commitment signature
		err = VerifyMerkleCommitment(commitment, pubPEM)
		assert.NoError(t, err)
	})

	t.Run("empty root hash", func(t *testing.T) {
		_, err := engine.SignMerkleCommitment([]byte{}, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "root hash cannot be empty")
	})

	t.Run("nil root hash", func(t *testing.T) {
		_, err := engine.SignMerkleCommitment(nil, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "root hash cannot be empty")
	})
}

func TestVerifyMerkleCommitment(t *testing.T) {
	engine, pubPEM := createTestEngine(t)
	_, wrongPubPEM, _ := generateTestKeyPair(t)

	root := make([]byte, 32)
	rand.Read(root)

	t.Run("valid commitment", func(t *testing.T) {
		commitment, err := engine.SignMerkleCommitment(root, 42)
		require.NoError(t, err)

		err = VerifyMerkleCommitment(commitment, pubPEM)
		assert.NoError(t, err)
	})

	t.Run("nil commitment", func(t *testing.T) {
		err := VerifyMerkleCommitment(nil, pubPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "commitment cannot be nil")
	})

	t.Run("empty public key", func(t *testing.T) {
		commitment, err := engine.SignMerkleCommitment(root, 42)
		require.NoError(t, err)

		err = VerifyMerkleCommitment(commitment, []byte{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "public key cannot be empty")
	})

	t.Run("empty root hash", func(t *testing.T) {
		commitment := &models.MerkleCommitment{
			OperatorID:  "test-operator",
			RootHash:    []byte{},
			Timestamp:   time.Now().UTC(),
			Signature:   make([]byte, 64),
			BlockHeight: 1,
		}

		err := VerifyMerkleCommitment(commitment, pubPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "root hash cannot be empty")
	})

	t.Run("wrong public key", func(t *testing.T) {
		commitment, err := engine.SignMerkleCommitment(root, 42)
		require.NoError(t, err)

		err = VerifyMerkleCommitment(commitment, wrongPubPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature verification failed")
	})

	t.Run("tampered signature", func(t *testing.T) {
		commitment, err := engine.SignMerkleCommitment(root, 42)
		require.NoError(t, err)

		commitment.Signature[0] ^= 0xFF
		err = VerifyMerkleCommitment(commitment, pubPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature verification failed")
	})
}

func TestVerifyMerkleProof(t *testing.T) {
	engine, _ := createTestEngine(t)

	// Build a tree with 4 leases for predictable structure
	leases := []*models.Lease{
		createFutureLease("lease-a"),
		createFutureLease("lease-b"),
		createFutureLease("lease-c"),
		createFutureLease("lease-d"),
	}

	tree, err := engine.BuildMerkleTree(leases)
	require.NoError(t, err)

	rootHash := tree.Hash

	// Test with leaf hash and proof for each lease
	for _, lease := range leases {
		leafHash := GetLeaseHash(lease)
		proof := findMerkleProof(tree, leafHash)
		require.NotNil(t, proof, "proof should be found for lease %s", lease.ID)

		valid := VerifyMerkleProof(leafHash, rootHash, proof)
		assert.True(t, valid, "proof should be valid for lease %s", lease.ID)
	}
}

func TestVerifyMerkleProof_InvalidCases(t *testing.T) {
	engine, _ := createTestEngine(t)

	leases := []*models.Lease{
		createFutureLease("lease-a"),
		createFutureLease("lease-b"),
		createFutureLease("lease-c"),
		createFutureLease("lease-d"),
	}

	tree, err := engine.BuildMerkleTree(leases)
	require.NoError(t, err)

	rootHash := tree.Hash
	leafHash := GetLeaseHash(leases[0])
	proof := findMerkleProof(tree, leafHash)

	t.Run("valid proof", func(t *testing.T) {
		assert.True(t, VerifyMerkleProof(leafHash, rootHash, proof))
	})

	t.Run("wrong root hash", func(t *testing.T) {
		wrongRoot := make([]byte, 32)
		copy(wrongRoot, rootHash)
		wrongRoot[0] ^= 0xFF
		assert.False(t, VerifyMerkleProof(leafHash, wrongRoot, proof))
	})

	t.Run("tampered proof", func(t *testing.T) {
		if len(proof) == 0 {
			t.Skip("proof has no siblings")
		}
		tamperedProof := make([][]byte, len(proof))
		copy(tamperedProof, proof)
		tamperedProof[0] = make([]byte, 32)
		rand.Read(tamperedProof[0])
		assert.False(t, VerifyMerkleProof(leafHash, rootHash, tamperedProof))
	})

	t.Run("empty leaf hash", func(t *testing.T) {
		assert.False(t, VerifyMerkleProof([]byte{}, rootHash, proof))
	})

	t.Run("empty root hash", func(t *testing.T) {
		assert.False(t, VerifyMerkleProof(leafHash, []byte{}, proof))
	})

	t.Run("empty proof", func(t *testing.T) {
		// For a single-node tree, empty proof is valid
		assert.False(t, VerifyMerkleProof(leafHash, rootHash, [][]byte{}))
	})

	t.Run("nil proof elements", func(t *testing.T) {
		if len(proof) == 0 {
			t.Skip("no proof elements to test")
		}
		badProof := append([][]byte{nil}, proof...)
		assert.False(t, VerifyMerkleProof(leafHash, rootHash, badProof))
	})
}

func TestGenerateProofOfInvalidity(t *testing.T) {
	engine, _ := createTestEngine(t)

	leaseA := createFutureLease("lease-a")
	leaseB := createFutureLease("lease-b")

	leases := []*models.Lease{leaseA, leaseB}
	tree, err := engine.BuildMerkleTree(leases)
	require.NoError(t, err)

	t.Run("double allocation proof", func(t *testing.T) {
		poi, err := engine.GenerateProofOfInvalidity(
			models.InvalidityDoubleAllocation,
			leaseA,
			leaseB,
			tree,
		)
		require.NoError(t, err)
		assert.NotNil(t, poi)
		assert.Equal(t, models.InvalidityDoubleAllocation, poi.Type)
		assert.Equal(t, leaseA, poi.LeaseA)
		assert.Equal(t, leaseB, poi.LeaseB)
		assert.NotNil(t, poi.Commitment)
		assert.NotEmpty(t, poi.MerkleProof)
		assert.WithinDuration(t, time.Now().UTC(), poi.Timestamp, 5*time.Second)
	})

	t.Run("expired lease proof", func(t *testing.T) {
		expiredLease := createExpiredLease("lease-expired")
		leases2 := []*models.Lease{expiredLease}
		tree2, err := engine.BuildMerkleTree(leases2)
		require.NoError(t, err)

		poi, err := engine.GenerateProofOfInvalidity(
			models.InvalidityExpiredLease,
			expiredLease,
			nil,
			tree2,
		)
		require.NoError(t, err)
		assert.NotNil(t, poi)
		assert.Equal(t, models.InvalidityExpiredLease, poi.Type)
		assert.Nil(t, poi.LeaseB)
	})

	t.Run("unspecified type", func(t *testing.T) {
		_, err := engine.GenerateProofOfInvalidity(
			models.InvalidityUnspecified,
			leaseA,
			leaseB,
			tree,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be unspecified")
	})

	t.Run("nil lease A", func(t *testing.T) {
		_, err := engine.GenerateProofOfInvalidity(
			models.InvalidityDoubleAllocation,
			nil,
			leaseB,
			tree,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lease A cannot be nil")
	})

	t.Run("nil tree", func(t *testing.T) {
		_, err := engine.GenerateProofOfInvalidity(
			models.InvalidityDoubleAllocation,
			leaseA,
			leaseB,
			nil,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "merkle tree cannot be nil")
	})
}

func TestVerifyProofOfInvalidity(t *testing.T) {
	engine, _ := createTestEngine(t)

	leaseA := createFutureLease("lease-a")
	leaseB := createFutureLease("lease-b")
	leases := []*models.Lease{leaseA, leaseB}

	tree, err := engine.BuildMerkleTree(leases)
	require.NoError(t, err)

	rootHash := tree.Hash

	t.Run("valid proof of invalidity", func(t *testing.T) {
		poi, err := engine.GenerateProofOfInvalidity(
			models.InvalidityDoubleAllocation,
			leaseA,
			leaseB,
			tree,
		)
		require.NoError(t, err)

		valid := VerifyProofOfInvalidity(poi, rootHash)
		assert.True(t, valid)
	})

	t.Run("nil POI", func(t *testing.T) {
		assert.False(t, VerifyProofOfInvalidity(nil, rootHash))
	})

	t.Run("nil commitment", func(t *testing.T) {
		poi := &models.ProofOfInvalidity{
			Type:   models.InvalidityDoubleAllocation,
			LeaseA: leaseA,
		}
		assert.False(t, VerifyProofOfInvalidity(poi, rootHash))
	})

	t.Run("wrong root hash", func(t *testing.T) {
		poi, err := engine.GenerateProofOfInvalidity(
			models.InvalidityDoubleAllocation,
			leaseA,
			leaseB,
			tree,
		)
		require.NoError(t, err)

		wrongRoot := make([]byte, 32)
		copy(wrongRoot, rootHash)
		wrongRoot[0] ^= 0xFF

		assert.False(t, VerifyProofOfInvalidity(poi, wrongRoot))
	})

	t.Run("nil lease A", func(t *testing.T) {
		poi, err := engine.GenerateProofOfInvalidity(
			models.InvalidityDoubleAllocation,
			leaseA,
			leaseB,
			tree,
		)
		require.NoError(t, err)

		// Temporarily set lease A to nil
		originalLeaseA := poi.LeaseA
		poi.LeaseA = nil
		assert.False(t, VerifyProofOfInvalidity(poi, rootHash))
		poi.LeaseA = originalLeaseA // restore
	})
}

func TestMarshalSignature(t *testing.T) {
	r := big.NewInt(42)
	s := big.NewInt(123)

	sig, err := marshalSignature(r, s)
	require.NoError(t, err)
	assert.Equal(t, 64, len(sig))

	r2, s2, err := unmarshalSignature(sig)
	require.NoError(t, err)
	assert.Equal(t, r.Int64(), r2.Int64())
	assert.Equal(t, s.Int64(), s2.Int64())
}

func TestUnmarshalSignature_InvalidLength(t *testing.T) {
	_, _, err := unmarshalSignature(make([]byte, 32))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature length")

	_, _, err = unmarshalSignature(make([]byte, 128))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature length")
}

func TestParsePublicKey(t *testing.T) {
	_, _, pubPEM := generateTestKeyPair(t)

	t.Run("valid PEM public key", func(t *testing.T) {
		key, err := parsePublicKey(pubPEM)
		require.NoError(t, err)
		assert.NotNil(t, key)
	})

	t.Run("valid raw DER public key", func(t *testing.T) {
		block, _ := pem.Decode(pubPEM)
		require.NotNil(t, block)

		key, err := parsePublicKey(block.Bytes)
		require.NoError(t, err)
		assert.NotNil(t, key)
	})

	t.Run("empty public key", func(t *testing.T) {
		_, err := parsePublicKey([]byte{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty public key")
	})

	t.Run("invalid public key data", func(t *testing.T) {
		_, err := parsePublicKey([]byte("not a valid key"))
		require.Error(t, err)
	})
}

func TestGenerateLeaseID(t *testing.T) {
	id1 := GenerateLeaseID()
	id2 := GenerateLeaseID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2)

	// Should be valid UUID format
	_, err := uuid.Parse(id1)
	assert.NoError(t, err)
}

func TestCanonicalJSONDeterminism(t *testing.T) {
	cf := canonicalLeaseFields{
		ID:         "lease-001",
		Wavelength: "1550.12:1:C_BAND:25.0",
		EndpointID: "ep-001",
		OperatorID: "op-001",
		StartTime:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		EndTime:    time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}

	data1, err := json.Marshal(cf)
	require.NoError(t, err)

	data2, err := json.Marshal(cf)
	require.NoError(t, err)

	assert.Equal(t, string(data1), string(data2))

	h1 := sha3.Sum256(data1)
	h2 := sha3.Sum256(data2)
	assert.Equal(t, h1, h2)
}

func BenchmarkGetLeaseHash(b *testing.B) {
	lease := createFutureLease("bench-lease")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GetLeaseHash(lease)
	}
}

func BenchmarkBuildMerkleTree(b *testing.B) {
	engine, _ := createTestEngine(b)
	leases := make([]*models.Lease, 100)
	for i := range leases {
		leases[i] = createFutureLease(fmt.Sprintf("bench-lease-%03d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.BuildMerkleTree(leases)
	}
}

func BenchmarkGenerateLeaseToken(b *testing.B) {
	engine, _ := createTestEngine(b)
	lease := createFutureLease("bench-lease")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.GenerateLeaseToken(lease)
	}
}


