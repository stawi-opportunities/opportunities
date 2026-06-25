package stawitok_test

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/authz/stawitok"
)

// fakeKV is a minimal map-backed KV used by tests. It honours TTL only
// to the extent that the test fixes a clock and explicitly evicts.
type fakeKV struct {
	mu   sync.Mutex
	rows map[string][]byte
}

func newFakeKV() *fakeKV { return &fakeKV{rows: map[string][]byte{}} }

func (f *fakeKV) Get(_ context.Context, k string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.rows[k]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (f *fakeKV) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[k] = append([]byte(nil), v...)
	return nil
}

func (f *fakeKV) Delete(_ context.Context, k string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, k)
	return nil
}

func mustKey(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, stawitok.SigningKeySize)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

func newIssuer(t *testing.T) (*stawitok.Issuer, *fakeKV) {
	t.Helper()
	kv := newFakeKV()
	is, err := stawitok.NewIssuer(kv, mustKey(t))
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	return is, kv
}

func TestIssuer_MintVerify_RoundTrip(t *testing.T) {
	is, _ := newIssuer(t)
	ctx := context.Background()
	tok, err := is.Mint(ctx, "cnd_1", stawitok.KindAccess, 15*time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	info, err := is.Verify(ctx, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if info.CandidateID != "cnd_1" {
		t.Fatalf("candidate_id = %q", info.CandidateID)
	}
	if info.Kind != stawitok.KindAccess {
		t.Fatalf("kind = %q", info.Kind)
	}
	if info.Prefix == "" {
		t.Fatal("prefix not populated")
	}
}

func TestIssuer_Verify_TamperedMAC(t *testing.T) {
	is, _ := newIssuer(t)
	ctx := context.Background()
	tok, _ := is.Mint(ctx, "cnd_1", stawitok.KindAccess, 15*time.Minute)
	// Flip the MAC by appending a character.
	tampered := tok + "x"
	if _, err := is.Verify(ctx, tampered); !errors.Is(err, stawitok.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestIssuer_Verify_MalformedShape(t *testing.T) {
	is, _ := newIssuer(t)
	ctx := context.Background()
	cases := []string{"", "no-dot", ".no-prefix", "no-mac.", "a.b.c.d"}
	for _, c := range cases {
		if _, err := is.Verify(ctx, c); !errors.Is(err, stawitok.ErrInvalidToken) {
			t.Fatalf("input %q: want ErrInvalidToken, got %v", c, err)
		}
	}
}

func TestIssuer_Verify_DeletedFromKV(t *testing.T) {
	is, kv := newIssuer(t)
	ctx := context.Background()
	tok, _ := is.Mint(ctx, "cnd_1", stawitok.KindAccess, 15*time.Minute)
	info, err := is.Verify(ctx, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Simulate revoke by deleting the row directly from the KV.
	_ = kv.Delete(ctx, "tok:"+info.Prefix)
	if _, err := is.Verify(ctx, tok); !errors.Is(err, stawitok.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken after delete, got %v", err)
	}
}

func TestIssuer_Revoke(t *testing.T) {
	is, _ := newIssuer(t)
	ctx := context.Background()
	tok, _ := is.Mint(ctx, "cnd_1", stawitok.KindRefresh, time.Hour)
	info, err := is.Verify(ctx, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := is.Revoke(ctx, info.Prefix); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := is.Verify(ctx, tok); !errors.Is(err, stawitok.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken after revoke, got %v", err)
	}
}

func TestIssuer_Expired(t *testing.T) {
	is, _ := newIssuer(t)
	t0 := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	pinned := is.WithClock(func() time.Time { return t0 })
	tok, err := pinned.Mint(context.Background(), "cnd_1", stawitok.KindAccess, time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	later := is.WithClock(func() time.Time { return t0.Add(2 * time.Minute) })
	if _, err := later.Verify(context.Background(), tok); !errors.Is(err, stawitok.ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestIssuer_RevokeAllForCandidate(t *testing.T) {
	is, _ := newIssuer(t)
	t0 := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	pinned := is.WithClock(func() time.Time { return t0 })
	ctx := context.Background()

	tokA, _ := pinned.Mint(ctx, "cnd_1", stawitok.KindAccess, time.Hour)
	tokR, _ := pinned.Mint(ctx, "cnd_1", stawitok.KindRefresh, 24*time.Hour)
	other, _ := pinned.Mint(ctx, "cnd_2", stawitok.KindAccess, time.Hour)

	// Tombstone written 1 second AFTER issuance — covers IssuedAt
	// because the check is "IssuedAt is at or before tombstone".
	tombClock := is.WithClock(func() time.Time { return t0.Add(time.Second) })
	if err := tombClock.RevokeAllForCandidate(ctx, "cnd_1"); err != nil {
		t.Fatalf("revoke all: %v", err)
	}

	postRevoke := is.WithClock(func() time.Time { return t0.Add(2 * time.Second) })
	if _, err := postRevoke.Verify(ctx, tokA); !errors.Is(err, stawitok.ErrInvalidToken) {
		t.Fatalf("access token after tombstone: want invalid, got %v", err)
	}
	if _, err := postRevoke.Verify(ctx, tokR); !errors.Is(err, stawitok.ErrInvalidToken) {
		t.Fatalf("refresh token after tombstone: want invalid, got %v", err)
	}
	// Other candidate's token unaffected.
	if _, err := postRevoke.Verify(ctx, other); err != nil {
		t.Fatalf("other candidate token broken: %v", err)
	}
}

func TestNewIssuer_Validation(t *testing.T) {
	if _, err := stawitok.NewIssuer(nil, make([]byte, stawitok.SigningKeySize)); err == nil {
		t.Fatal("nil kv should error")
	}
	if _, err := stawitok.NewIssuer(newFakeKV(), make([]byte, 16)); err == nil {
		t.Fatal("short key should error")
	}
}

func TestIssuer_Mint_Validation(t *testing.T) {
	is, _ := newIssuer(t)
	ctx := context.Background()
	if _, err := is.Mint(ctx, "", stawitok.KindAccess, time.Hour); err == nil {
		t.Fatal("empty candidate id should error")
	}
	if _, err := is.Mint(ctx, "c", "bogus", time.Hour); err == nil {
		t.Fatal("bad kind should error")
	}
	if _, err := is.Mint(ctx, "c", stawitok.KindAccess, 0); err == nil {
		t.Fatal("zero ttl should error")
	}
}

func TestNewIssuerFromBase64(t *testing.T) {
	if _, err := stawitok.NewIssuerFromBase64(newFakeKV(), "!!!bad"); err == nil {
		t.Fatal("bad base64 should error")
	}
}
