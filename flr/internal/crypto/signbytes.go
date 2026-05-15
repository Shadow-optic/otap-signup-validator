// SignBytes is a small public helper that signs arbitrary bytes with the
// engine's private key. Used by registry code that needs to sign messages
// outside the lease/token/schema canonical-form path (e.g., Merkle commitments).
package crypto

import (
	"crypto/ecdsa"
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/sha3"
)

// SignBytes hashes input with SHA3-256 and signs the digest with ECDSA P-256.
func (e *Engine) SignBytes(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("cannot sign empty bytes")
	}
	h := sha3.Sum256(b)
	r, s, err := ecdsa.Sign(rand.Reader, e.privateKey, h[:])
	if err != nil {
		return nil, fmt.Errorf("ECDSA sign failed: %w", err)
	}
	return marshalSignature(r, s)
}

// VerifyBytes verifies a SignBytes signature against an operator public key.
func (e *Engine) VerifyBytes(b []byte, sig []byte, operatorPubKey []byte) error {
	pub, err := parsePublicKey(operatorPubKey)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	r, s, err := unmarshalSignature(sig)
	if err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}
	h := sha3.Sum256(b)
	if !ecdsa.Verify(pub, h[:], r, s) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}
