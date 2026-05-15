// Package crypto provides all cryptographic operations for the FLR system.
// It handles ECDSA P-256 signatures, SHA3-256 hashing, Merkle tree construction,
// lease token generation/validation, and proof-of-invalidity operations.
package crypto

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/otap/flr/internal/models"
	"golang.org/x/crypto/sha3"
)

// canonicalLeaseFields represents the canonical JSON structure for lease hashing.
type canonicalLeaseFields struct {
	ID         string  `json:"id"`
	Wavelength string  `json:"wavelength_key"`
	EndpointID string  `json:"endpoint_id"`
	OperatorID string  `json:"operator_id"`
	StartTime  string  `json:"start_time"`
	EndTime    string  `json:"end_time"`
}

// canonicalTokenFields represents the canonical JSON structure for token signing.
type canonicalTokenFields struct {
	Version    int32           `json:"version"`
	LeaseID    string          `json:"lease_id"`
	OperatorID string          `json:"operator_id"`
	Wavelength *models.Wavelength `json:"wavelength"`
	EndpointID string          `json:"endpoint_id"`
	StartTime  string          `json:"start_time"`
	EndTime    string          `json:"end_time"`
	Nonce      []byte          `json:"nonce"`
}

// Engine provides cryptographic operations for an FLR operator.
type Engine struct {
	operatorID string
	privateKey *ecdsa.PrivateKey
}

// NewEngine creates a new crypto engine from an operator ID and PEM-encoded private key.
func NewEngine(operatorID string, privateKeyPEM []byte) (*Engine, error) {
	if operatorID == "" {
		return nil, fmt.Errorf("operator ID cannot be empty")
	}
	if len(privateKeyPEM) == 0 {
		return nil, fmt.Errorf("private key PEM cannot be empty")
	}

	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	var privateKey *ecdsa.PrivateKey
	var err error

	switch block.Type {
	case "EC PRIVATE KEY":
		privateKey, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("failed to parse PKCS8 private key: %w", err2)
		}
		var ok bool
		privateKey, ok = key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not ECDSA")
		}
	default:
		return nil, fmt.Errorf("unsupported PEM type: %s", block.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	if privateKey.Curve != elliptic.P256() {
		return nil, fmt.Errorf("only P-256 curve is supported, got %v", privateKey.Curve.Params().Name)
	}

	return &Engine{
		operatorID: operatorID,
		privateKey: privateKey,
	}, nil
}

// GenerateLeaseToken creates a signed LeaseToken for the given lease.
func (e *Engine) GenerateLeaseToken(lease *models.Lease) (*models.LeaseToken, error) {
	if lease == nil {
		return nil, fmt.Errorf("lease cannot be nil")
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	token := &models.LeaseToken{
		Version:    1,
		LeaseID:    lease.ID,
		OperatorID: e.operatorID,
		Wavelength: lease.Wavelength,
		EndpointID: lease.EndpointID,
		StartTime:  lease.StartTime,
		EndTime:    lease.EndTime,
		Nonce:      nonce,
		IssuedAt:   time.Now().UTC(),
	}

	sig, err := e.signToken(token)
	if err != nil {
		return nil, fmt.Errorf("failed to sign token: %w", err)
	}
	token.Signature = sig

	return token, nil
}

// signToken creates a signature over the canonical token fields (excluding signature).
func (e *Engine) signToken(token *models.LeaseToken) ([]byte, error) {
	cf := canonicalTokenFields{
		Version:    token.Version,
		LeaseID:    token.LeaseID,
		OperatorID: token.OperatorID,
		Wavelength: token.Wavelength,
		EndpointID: token.EndpointID,
		StartTime:  token.StartTime.UTC().Format(time.RFC3339Nano),
		EndTime:    token.EndTime.UTC().Format(time.RFC3339Nano),
		Nonce:      token.Nonce,
	}

	data, err := json.Marshal(cf)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal token fields: %w", err)
	}

	hash := sha3.Sum256(data)
	r, s, err := ecdsa.Sign(rand.Reader, e.privateKey, hash[:])
	if err != nil {
		return nil, fmt.Errorf("ECDSA sign failed: %w", err)
	}

	return marshalSignature(r, s)
}

// ValidateLeaseToken verifies a token's ECDSA signature and checks that it has not expired.
func (e *Engine) ValidateLeaseToken(token *models.LeaseToken, operatorPubKey []byte) error {
	if token == nil {
		return fmt.Errorf("token cannot be nil")
	}
	if len(operatorPubKey) == 0 {
		return fmt.Errorf("operator public key cannot be empty")
	}

	// Check expiry
	if time.Now().UTC().After(token.EndTime.UTC()) {
		return fmt.Errorf("token has expired")
	}

	// Parse public key
	pubKey, err := parsePublicKey(operatorPubKey)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	// Verify signature
	cf := canonicalTokenFields{
		Version:    token.Version,
		LeaseID:    token.LeaseID,
		OperatorID: token.OperatorID,
		Wavelength: token.Wavelength,
		EndpointID: token.EndpointID,
		StartTime:  token.StartTime.UTC().Format(time.RFC3339Nano),
		EndTime:    token.EndTime.UTC().Format(time.RFC3339Nano),
		Nonce:      token.Nonce,
	}

	data, err := json.Marshal(cf)
	if err != nil {
		return fmt.Errorf("failed to marshal token fields: %w", err)
	}

	hash := sha3.Sum256(data)

	r, s, err := unmarshalSignature(token.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature format: %w", err)
	}

	if !ecdsa.Verify(pubKey, hash[:], r, s) {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}

// GetLeaseHash returns the SHA3-256 hash of canonicalized lease data.
func GetLeaseHash(lease *models.Lease) []byte {
	if lease == nil {
		return make([]byte, 32)
	}

	wlKey := ""
	if lease.Wavelength != nil {
		wlKey = lease.Wavelength.ToKey()
	}

	cf := canonicalLeaseFields{
		ID:         lease.ID,
		Wavelength: wlKey,
		EndpointID: lease.EndpointID,
		OperatorID: lease.OperatorID,
		StartTime:  lease.StartTime.UTC().Format(time.RFC3339Nano),
		EndTime:    lease.EndTime.UTC().Format(time.RFC3339Nano),
	}

	data, err := json.Marshal(cf)
	if err != nil {
		h := sha3.Sum256([]byte{})
		return h[:]
	}

	h := sha3.Sum256(data)
	return h[:]
}

// BuildMerkleTree constructs a deterministic binary Merkle tree from lease hashes.
// Leases are sorted by ID for determinism. The tree is padded with zero hashes to a power of 2.
func (e *Engine) BuildMerkleTree(leases []*models.Lease) (*models.MerkleNode, error) {
	if len(leases) == 0 {
		// Return a tree with a single zero-hash leaf
		zeroHash := make([]byte, 32)
		return &models.MerkleNode{
			Hash:   zeroHash,
			IsLeaf: true,
		}, nil
	}

	// Sort leases by ID for deterministic ordering
	sorted := make([]*models.Lease, len(leases))
	copy(sorted, leases)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})

	// Build leaf nodes
	leaves := make([]*models.MerkleNode, len(sorted))
	for i, lease := range sorted {
		leaves[i] = &models.MerkleNode{
			Hash:    GetLeaseHash(lease),
			LeaseID: lease.ID,
			IsLeaf:  true,
		}
	}

	// Pad with zero-hash leaves to reach a power of 2
	targetCount := 1
	for targetCount < len(leaves) {
		targetCount <<= 1
	}
	zeroHash := make([]byte, 32)
	for len(leaves) < targetCount {
		leaves = append(leaves, &models.MerkleNode{
			Hash:   zeroHash,
			IsLeaf: true,
		})
	}

	// Build tree bottom-up
	return buildMerkleTreeFromLeaves(leaves), nil
}

// buildMerkleTreeFromLeaves recursively builds a Merkle tree from leaf nodes.
func buildMerkleTreeFromLeaves(nodes []*models.MerkleNode) *models.MerkleNode {
	if len(nodes) == 1 {
		return nodes[0]
	}

	parents := make([]*models.MerkleNode, 0, len(nodes)/2)
	for i := 0; i < len(nodes); i += 2 {
		// Hash siblings in deterministic lexicographic order so the
		// content-addressed verifier (VerifyMerkleProof) — which sorts
		// without knowing the original left/right structure — gets the
		// same byte stream as the builder.
		left, right := nodes[i].Hash, nodes[i+1].Hash
		var combined []byte
		if bytes.Compare(left, right) <= 0 {
			combined = append(combined, left...)
			combined = append(combined, right...)
		} else {
			combined = append(combined, right...)
			combined = append(combined, left...)
		}
		h := sha3.Sum256(combined)
		parents = append(parents, &models.MerkleNode{
			Hash:  h[:],
			Left:  nodes[i],
			Right: nodes[i+1],
		})
	}

	return buildMerkleTreeFromLeaves(parents)
}

// SignMerkleCommitment signs a Merkle root hash, creating a commitment.
func (e *Engine) SignMerkleCommitment(root []byte, blockHeight int64) (*models.MerkleCommitment, error) {
	if len(root) == 0 {
		return nil, fmt.Errorf("root hash cannot be empty")
	}

	commitment := &models.MerkleCommitment{
		OperatorID:  e.operatorID,
		RootHash:    root,
		Timestamp:   time.Now().UTC(),
		BlockHeight: blockHeight,
	}

	data, err := json.Marshal(struct {
		OperatorID  string `json:"operator_id"`
		RootHash    []byte `json:"root_hash"`
		Timestamp   string `json:"timestamp"`
		BlockHeight int64  `json:"block_height"`
		LeaseCount  int32  `json:"lease_count"`
	}{
		OperatorID:  commitment.OperatorID,
		RootHash:    commitment.RootHash,
		Timestamp:   commitment.Timestamp.UTC().Format(time.RFC3339Nano),
		BlockHeight: commitment.BlockHeight,
		LeaseCount:  commitment.LeaseCount,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal commitment: %w", err)
	}

	sig, err := e.Sign(data)
	if err != nil {
		return nil, fmt.Errorf("failed to sign commitment: %w", err)
	}
	commitment.Signature = sig

	return commitment, nil
}

// VerifyMerkleCommitment verifies a signed Merkle commitment.
func VerifyMerkleCommitment(commitment *models.MerkleCommitment, pubKey []byte) error {
	if commitment == nil {
		return fmt.Errorf("commitment cannot be nil")
	}
	if len(pubKey) == 0 {
		return fmt.Errorf("public key cannot be empty")
	}
	if len(commitment.RootHash) == 0 {
		return fmt.Errorf("root hash cannot be empty")
	}

	data, err := json.Marshal(struct {
		OperatorID  string `json:"operator_id"`
		RootHash    []byte `json:"root_hash"`
		Timestamp   string `json:"timestamp"`
		BlockHeight int64  `json:"block_height"`
		LeaseCount  int32  `json:"lease_count"`
	}{
		OperatorID:  commitment.OperatorID,
		RootHash:    commitment.RootHash,
		Timestamp:   commitment.Timestamp.UTC().Format(time.RFC3339Nano),
		BlockHeight: commitment.BlockHeight,
		LeaseCount:  commitment.LeaseCount,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal commitment: %w", err)
	}

	return Verify(pubKey, data, commitment.Signature)
}

// VerifyMerkleProof verifies a Merkle proof path against a leaf hash and expected root hash.
func VerifyMerkleProof(leafHash, rootHash []byte, proof [][]byte) bool {
	if len(leafHash) == 0 || len(rootHash) == 0 {
		return false
	}

	hash := make([]byte, len(leafHash))
	copy(hash, leafHash)

	for _, sibling := range proof {
		if len(sibling) == 0 {
			return false
		}
		// Deterministic ordering: lexicographically sort concatenation
		if string(hash) < string(sibling) {
			combined := append(hash, sibling...)
			h := sha3.Sum256(combined)
			hash = h[:]
		} else {
			combined := append(sibling, hash...)
			h := sha3.Sum256(combined)
			hash = h[:]
		}
	}

	return string(hash) == string(rootHash)
}

// GenerateProofOfInvalidity creates a cryptographic proof of a registry violation.
// For double-allocation, it includes both leases A and B with a Merkle proof for lease A.
func (e *Engine) GenerateProofOfInvalidity(invType models.InvalidityType, leaseA, leaseB *models.Lease, tree *models.MerkleNode) (*models.ProofOfInvalidity, error) {
	if invType == models.InvalidityUnspecified {
		return nil, fmt.Errorf("invalidity type cannot be unspecified")
	}
	if leaseA == nil {
		return nil, fmt.Errorf("lease A cannot be nil")
	}
	if tree == nil {
		return nil, fmt.Errorf("merkle tree cannot be nil")
	}

	leafHash := GetLeaseHash(leaseA)
	proof := findMerkleProof(tree, leafHash)

	// Build commitment from tree root
	rootHash := tree.Hash
	commitment, err := e.SignMerkleCommitment(rootHash, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to sign commitment for proof: %w", err)
	}

	poi := &models.ProofOfInvalidity{
		Type:        invType,
		LeaseA:      leaseA,
		LeaseB:      leaseB,
		Commitment:  commitment,
		MerkleProof: proof,
		Timestamp:   time.Now().UTC(),
	}

	return poi, nil
}

// VerifyProofOfInvalidity checks a proof of invalidity by verifying the Merkle proof.
func VerifyProofOfInvalidity(poi *models.ProofOfInvalidity, rootHash []byte) bool {
	if poi == nil || poi.Commitment == nil {
		return false
	}

	leafHash := GetLeaseHash(poi.LeaseA)
	if len(leafHash) == 0 {
		return false
	}

	// Verify the Merkle proof path
	if !VerifyMerkleProof(leafHash, rootHash, poi.MerkleProof) {
		return false
	}

	// Verify commitment signature if we have a public key context
	// (The commitment signature is verified separately by the caller)

	return true
}

// findMerkleProof finds the sibling path from a leaf hash to the root.
func findMerkleProof(root *models.MerkleNode, leafHash []byte) [][]byte {
	if root == nil {
		return nil
	}
	if root.IsLeaf {
		if string(root.Hash) == string(leafHash) {
			return [][]byte{}
		}
		return nil
	}

	// Search left subtree
	if root.Left != nil {
		leftProof := findMerkleProof(root.Left, leafHash)
		if leftProof != nil {
			if root.Right != nil {
				return append(leftProof, root.Right.Hash)
			}
			return append(leftProof, make([]byte, 32))
		}
	}

	// Search right subtree
	if root.Right != nil {
		rightProof := findMerkleProof(root.Right, leafHash)
		if rightProof != nil {
			if root.Left != nil {
				return append(rightProof, root.Left.Hash)
			}
			return append(rightProof, make([]byte, 32))
		}
	}

	return nil
}

// Sign signs arbitrary data using the operator's private key.
func (e *Engine) Sign(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("data to sign cannot be empty")
	}

	hash := sha3.Sum256(data)
	r, s, err := ecdsa.Sign(rand.Reader, e.privateKey, hash[:])
	if err != nil {
		return nil, fmt.Errorf("ECDSA sign failed: %w", err)
	}

	return marshalSignature(r, s)
}

// Verify verifies an ECDSA signature using the provided public key.
func Verify(pubKey []byte, data, sig []byte) error {
	if len(pubKey) == 0 {
		return fmt.Errorf("public key cannot be empty")
	}
	if len(data) == 0 {
		return fmt.Errorf("data cannot be empty")
	}
	if len(sig) == 0 {
		return fmt.Errorf("signature cannot be empty")
	}

	key, err := parsePublicKey(pubKey)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	hash := sha3.Sum256(data)

	r, s, err := unmarshalSignature(sig)
	if err != nil {
		return fmt.Errorf("invalid signature format: %w", err)
	}

	if !ecdsa.Verify(key, hash[:], r, s) {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}

// GenerateKeyPair generates a new ECDSA P-256 key pair.
// Returns the private key, private key PEM, and public key PEM.
func GenerateKeyPair() (*ecdsa.PrivateKey, []byte, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Marshal private key to PKCS8 PEM
	privDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: privDER,
	})

	// Marshal public key to PKIX PEM
	pubDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal public key: %w", err)
	}

	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})

	return privateKey, privPEM, pubPEM, nil
}

// marshalSignature encodes an ECDSA signature (r, s) as a fixed-length byte slice.
// Each component is padded to 32 bytes (P-256 curve size).
func marshalSignature(r, s *big.Int) ([]byte, error) {
	rBytes := r.Bytes()
	sBytes := s.Bytes()

	// Pad each to 32 bytes
	paddedR := make([]byte, 32)
	paddedS := make([]byte, 32)
	copy(paddedR[32-len(rBytes):], rBytes)
	copy(paddedS[32-len(sBytes):], sBytes)

	return append(paddedR, paddedS...), nil
}

// unmarshalSignature decodes a 64-byte ECDSA signature into (r, s).
func unmarshalSignature(sig []byte) (*big.Int, *big.Int, error) {
	if len(sig) != 64 {
		return nil, nil, fmt.Errorf("invalid signature length: expected 64, got %d", len(sig))
	}

	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	return r, s, nil
}

// parsePublicKey parses a public key from DER or PEM format.
func parsePublicKey(pubKey []byte) (*ecdsa.PublicKey, error) {
	if len(pubKey) == 0 {
		return nil, fmt.Errorf("empty public key")
	}

	// Try PEM first
	block, _ := pem.Decode(pubKey)
	if block != nil {
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PEM public key: %w", err)
		}
		ecKey, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("public key is not ECDSA")
		}
		return ecKey, nil
	}

	// Try raw DER
	key, err := x509.ParsePKIXPublicKey(pubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	ecKey, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not ECDSA")
	}

	return ecKey, nil
}

// GetPublicKey returns the PEM-encoded public key of this engine.
func (e *Engine) GetPublicKey() ([]byte, error) {
	if e.privateKey == nil {
		return nil, fmt.Errorf("no private key available")
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&e.privateKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	}), nil
}

// GenerateLeaseID generates a new unique lease ID.
func GenerateLeaseID() string {
	return uuid.New().String()
}
