// Package authsession provides the abstraction and storage for
// per-candidate, per-source authenticated browser sessions captured by
// the Stawi browser extension.
//
// The package owns three concerns:
//
//   - Envelope encryption (this file). Per-row data encryption keys
//     wrapped under a master key, so master rotation does not require
//     re-encrypting payloads.
//   - The SessionProvider interface (provider.go). The autoapply
//     replay submitter depends on it; tests substitute a fake.
//   - The Store (store.go). Concrete implementation backed by the
//     domain.CandidateSession repository plus the Wrapper from here.
//
// The crypto primitives are intentionally narrow: AES-256-GCM with random
// nonces, no padding modes to choose, no key derivation, no algorithm
// agility. If we ever swap to KMS-backed wrapping the Wrapper interface
// is the seam — payload encryption stays the same.
package authsession

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	// DEKSize is the length in bytes of every data encryption key.
	DEKSize = 32
	// MasterKeySize is the required length of the configured master key.
	MasterKeySize = 32
	// GCMNonceSize is the AES-GCM standard nonce length.
	GCMNonceSize = 12
)

// ErrCipherTooShort is returned when ciphertext / nonce inputs are not
// long enough to be plausibly an AES-GCM output.
var ErrCipherTooShort = errors.New("authsession: cipher input too short")

// Wrapper is the seam between session encryption and key custody. The
// default LocalWrapper holds an in-process master key loaded from
// SESSION_MASTER_KEY; a future KMSWrapper drops in here without
// touching the Store.
//
// Wrap takes a fresh data encryption key and returns (wrapped DEK,
// nonce used for wrapping, key ID, error). The key ID is opaque to
// the caller; the Wrapper uses it on Unwrap to pick the right master.
type Wrapper interface {
	Wrap(dek []byte) (wrapped, nonce []byte, keyID string, err error)
	Unwrap(wrapped, nonce []byte, keyID string) (dek []byte, err error)
}

// LocalWrapper wraps DEKs with an in-process master key. The keyID it
// returns is a short identifier the operator sets via env var (e.g.
// "v1"); on rotation, deploy with both the old and new keys present
// (a future enhancement once we actually rotate — for v1 a single key
// is enough).
type LocalWrapper struct {
	master []byte
	keyID  string
}

// NewLocalWrapper validates and returns a LocalWrapper. The master key
// is verified for length here rather than at first-use so a misconfigured
// process fails on startup, not on the first capture.
func NewLocalWrapper(master []byte, keyID string) (*LocalWrapper, error) {
	if len(master) != MasterKeySize {
		return nil, fmt.Errorf("authsession: master key must be %d bytes, got %d", MasterKeySize, len(master))
	}
	if keyID == "" {
		return nil, errors.New("authsession: key id must not be empty")
	}
	cp := make([]byte, MasterKeySize)
	copy(cp, master)
	return &LocalWrapper{master: cp, keyID: keyID}, nil
}

// NewLocalWrapperFromBase64 is a convenience wrapper for env-var loading
// (`SESSION_MASTER_KEY` is base64 of 32 bytes).
func NewLocalWrapperFromBase64(b64, keyID string) (*LocalWrapper, error) {
	master, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("authsession: master key base64 decode: %w", err)
	}
	return NewLocalWrapper(master, keyID)
}

// Wrap implements Wrapper.
func (w *LocalWrapper) Wrap(dek []byte) ([]byte, []byte, string, error) {
	if len(dek) != DEKSize {
		return nil, nil, "", fmt.Errorf("authsession: dek must be %d bytes, got %d", DEKSize, len(dek))
	}
	nonce, ct, err := gcmSeal(w.master, dek, nil)
	if err != nil {
		return nil, nil, "", err
	}
	return ct, nonce, w.keyID, nil
}

// Unwrap implements Wrapper.
func (w *LocalWrapper) Unwrap(wrapped, nonce []byte, keyID string) ([]byte, error) {
	if keyID != w.keyID {
		return nil, fmt.Errorf("authsession: unknown key id %q (this process holds %q)", keyID, w.keyID)
	}
	return gcmOpen(w.master, nonce, wrapped, nil)
}

// NewDEK returns a fresh DEKSize-byte data encryption key sourced from
// crypto/rand. Each session gets its own DEK so a single-row plaintext
// leak does not implicate other rows.
func NewDEK() ([]byte, error) {
	b := make([]byte, DEKSize)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("authsession: dek: %w", err)
	}
	return b, nil
}

// SealPayload encrypts plaintext under the DEK and returns (nonce, ciphertext).
// The DEK lifecycle is the caller's: typically NewDEK → SealPayload → Wrap,
// then zero the DEK from memory if the caller chooses.
func SealPayload(dek, plaintext []byte) (nonce, ciphertext []byte, err error) {
	return gcmSeal(dek, plaintext, nil)
}

// OpenPayload decrypts ciphertext under the DEK. Returns ErrCipherTooShort
// if nonce/ciphertext lengths are obviously wrong, otherwise propagates
// the underlying AES-GCM error (which collapses authentication and
// integrity failures into a single opaque error by design).
func OpenPayload(dek, nonce, ciphertext []byte) ([]byte, error) {
	return gcmOpen(dek, nonce, ciphertext, nil)
}

// gcmSeal is the internal AES-GCM seal used by both the master-wrap and
// the payload-encrypt paths. Returns (nonce, ciphertext) so callers can
// store them separately.
func gcmSeal(key, plaintext, aad []byte) (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("authsession: new cipher: %w", err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("authsession: new gcm: %w", err)
	}
	nonce = make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("authsession: nonce: %w", err)
	}
	ciphertext = g.Seal(nil, nonce, plaintext, aad)
	return nonce, ciphertext, nil
}

// gcmOpen mirrors gcmSeal. A non-nil error means the data is either
// truncated, tampered with, or encrypted under a different key — by
// design GCM does not distinguish.
func gcmOpen(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	if len(nonce) != GCMNonceSize || len(ciphertext) == 0 {
		return nil, ErrCipherTooShort
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("authsession: new cipher: %w", err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("authsession: new gcm: %w", err)
	}
	plaintext, err := g.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("authsession: open: %w", err)
	}
	return plaintext, nil
}
