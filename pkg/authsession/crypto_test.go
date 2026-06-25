package authsession_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/authsession"
)

func mustMaster(t *testing.T) []byte {
	t.Helper()
	m := make([]byte, authsession.MasterKeySize)
	if _, err := rand.Read(m); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return m
}

func TestLocalWrapper_RoundTrip(t *testing.T) {
	w, err := authsession.NewLocalWrapper(mustMaster(t), "v1")
	if err != nil {
		t.Fatalf("new wrapper: %v", err)
	}
	dek, err := authsession.NewDEK()
	if err != nil {
		t.Fatalf("new dek: %v", err)
	}
	wrapped, nonce, keyID, err := w.Wrap(dek)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if keyID != "v1" {
		t.Fatalf("keyID = %q, want v1", keyID)
	}
	got, err := w.Unwrap(wrapped, nonce, keyID)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Fatalf("round-trip mismatch")
	}
}

func TestLocalWrapper_UnknownKeyID(t *testing.T) {
	w, _ := authsession.NewLocalWrapper(mustMaster(t), "v1")
	dek, _ := authsession.NewDEK()
	wrapped, nonce, _, _ := w.Wrap(dek)
	if _, err := w.Unwrap(wrapped, nonce, "v2"); err == nil {
		t.Fatal("expected error for unknown key id")
	}
}

func TestLocalWrapper_TamperedCiphertext(t *testing.T) {
	w, _ := authsession.NewLocalWrapper(mustMaster(t), "v1")
	dek, _ := authsession.NewDEK()
	wrapped, nonce, keyID, _ := w.Wrap(dek)
	wrapped[0] ^= 0xFF
	if _, err := w.Unwrap(wrapped, nonce, keyID); err == nil {
		t.Fatal("expected authentication failure on tampered ciphertext")
	}
}

func TestLocalWrapper_InvalidMasterKeyLength(t *testing.T) {
	short := make([]byte, authsession.MasterKeySize-1)
	if _, err := authsession.NewLocalWrapper(short, "v1"); err == nil {
		t.Fatal("expected error for short master key")
	}
}

func TestLocalWrapper_EmptyKeyID(t *testing.T) {
	if _, err := authsession.NewLocalWrapper(mustMaster(t), ""); err == nil {
		t.Fatal("expected error for empty key id")
	}
}

func TestNewLocalWrapperFromBase64(t *testing.T) {
	master := mustMaster(t)
	b64 := base64.StdEncoding.EncodeToString(master)
	w, err := authsession.NewLocalWrapperFromBase64(b64, "v1")
	if err != nil {
		t.Fatalf("from base64: %v", err)
	}
	dek, _ := authsession.NewDEK()
	wrapped, nonce, _, _ := w.Wrap(dek)
	got, err := w.Unwrap(wrapped, nonce, "v1")
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Fatal("round-trip mismatch via base64 constructor")
	}
}

func TestNewLocalWrapperFromBase64_Invalid(t *testing.T) {
	if _, err := authsession.NewLocalWrapperFromBase64("!!!not-base64!!!", "v1"); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestSealOpenPayload_RoundTrip(t *testing.T) {
	dek, _ := authsession.NewDEK()
	plaintext := []byte(`{"cookies":[{"name":"sid","value":"abc"}]}`)
	nonce, ct, err := authsession.SealPayload(dek, plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	got, err := authsession.OpenPayload(dek, nonce, ct)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch")
	}
}

func TestOpenPayload_ShortInputs(t *testing.T) {
	dek, _ := authsession.NewDEK()
	if _, err := authsession.OpenPayload(dek, []byte{1, 2}, []byte{3}); !errors.Is(err, authsession.ErrCipherTooShort) {
		t.Fatalf("want ErrCipherTooShort, got %v", err)
	}
	if _, err := authsession.OpenPayload(dek, make([]byte, authsession.GCMNonceSize), nil); !errors.Is(err, authsession.ErrCipherTooShort) {
		t.Fatalf("want ErrCipherTooShort for empty ciphertext, got %v", err)
	}
}

func TestOpenPayload_WrongKey(t *testing.T) {
	dek1, _ := authsession.NewDEK()
	dek2, _ := authsession.NewDEK()
	plaintext := []byte("hello")
	nonce, ct, _ := authsession.SealPayload(dek1, plaintext)
	if _, err := authsession.OpenPayload(dek2, nonce, ct); err == nil {
		t.Fatal("expected auth failure with wrong key")
	}
}

func TestSealPayload_NoncesAreUnique(t *testing.T) {
	dek, _ := authsession.NewDEK()
	pt := []byte("plaintext")
	seen := map[string]struct{}{}
	for i := 0; i < 64; i++ {
		nonce, _, err := authsession.SealPayload(dek, pt)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		k := string(nonce)
		if _, dup := seen[k]; dup {
			t.Fatalf("nonce reuse at iteration %d", i)
		}
		seen[k] = struct{}{}
	}
}
