// Package stawitok implements opaque, Stawi-issued bearer tokens used
// by the browser extension to authenticate against the candidate API.
//
// The token wire format is "<prefix>.<mac>":
//   - prefix is 16 random bytes, base64url-encoded
//   - mac is HMAC-SHA256(prefix, signing key), base64url-encoded
//
// On verify, the MAC is recomputed in constant time, then prefix is
// looked up in a KV store (Valkey in prod, in-memory in tests). The
// payload behind the prefix carries {candidate_id, kind, issued_at,
// expires_at}. The MAC alone is not sufficient to authenticate — it
// only proves the prefix was minted with our signing key; the KV
// lookup is what gives us revocation. Without the KV row a presented
// token fails to verify even if its MAC checks out.
//
// Two kinds of token exist:
//   - "access" — short-lived (15m default), carried as
//     Authorization: Bearer <token> on capture uploads.
//   - "refresh" — long-lived (30d default), exchanged for a fresh
//     access token via /pairings/refresh.
//
// Revoke deletes a single prefix from the KV. RevokeAllForCandidate
// writes a "tombstone:<candidate_id>" row whose value is a Unix
// timestamp; Verify rejects tokens whose IssuedAt is at or before the
// tombstone time, so one click can invalidate every token a candidate
// holds without enumerating them.
package stawitok

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind discriminates access vs refresh tokens.
type Kind string

const (
	// KindAccess — short-lived, used on every API call.
	KindAccess Kind = "access"
	// KindRefresh — long-lived, used only to mint access tokens.
	KindRefresh Kind = "refresh"
)

// SigningKeySize is the required length of the signing key in bytes.
const SigningKeySize = 32

const (
	prefixBytes      = 16
	tokKeyPrefix     = "tok:"
	tombstonePrefix  = "tombstone:"
	tombstoneMaxTTL  = 30 * 24 * time.Hour
)

var (
	// ErrInvalidToken is returned for any malformed or unverifiable
	// presented token (bad shape, bad MAC, missing KV row, tombstoned,
	// expired). The caller MUST NOT distinguish these to the client.
	ErrInvalidToken = errors.New("stawitok: invalid token")
	// ErrExpired is the internal-only signal that a token's
	// ExpiresAt is in the past. The HTTP layer maps this to 401 the
	// same way it maps ErrInvalidToken.
	ErrExpired = errors.New("stawitok: token expired")
)

// KV is the minimum cache surface the issuer needs. Frame's
// cache.RawCache satisfies this; tests substitute an in-memory fake.
type KV interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// Clock abstracts time for tests.
type Clock func() time.Time

// Info is the payload behind a verified token.
type Info struct {
	CandidateID string    `json:"candidate_id"`
	Kind        Kind      `json:"kind"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Prefix      string    `json:"-"` // populated by Verify so callers can Revoke
}

// Issuer mints and verifies Stawi access + refresh tokens.
type Issuer struct {
	kv         KV
	signingKey []byte
	clock      Clock
}

// NewIssuer constructs an Issuer. The signing key must be exactly
// SigningKeySize bytes — short keys are rejected at startup so a
// misconfigured deployment fails immediately.
func NewIssuer(kv KV, signingKey []byte) (*Issuer, error) {
	if kv == nil {
		return nil, errors.New("stawitok: kv is required")
	}
	if len(signingKey) != SigningKeySize {
		return nil, fmt.Errorf("stawitok: signing key must be %d bytes, got %d", SigningKeySize, len(signingKey))
	}
	cp := make([]byte, SigningKeySize)
	copy(cp, signingKey)
	return &Issuer{kv: kv, signingKey: cp}, nil
}

// NewIssuerFromBase64 is a convenience for env-var loading.
func NewIssuerFromBase64(kv KV, b64 string) (*Issuer, error) {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("stawitok: decode signing key: %w", err)
	}
	return NewIssuer(kv, key)
}

// WithClock returns a copy of the Issuer using the supplied clock. For
// tests.
func (i *Issuer) WithClock(c Clock) *Issuer {
	cp := *i
	cp.clock = c
	return &cp
}

func (i *Issuer) now() time.Time {
	if i.clock != nil {
		return i.clock()
	}
	return time.Now().UTC()
}

// Mint creates a token, stores its payload under prefix in KV, and
// returns the wire-format string.
func (i *Issuer) Mint(ctx context.Context, candidateID string, kind Kind, ttl time.Duration) (string, error) {
	if candidateID == "" {
		return "", errors.New("stawitok: candidate id required")
	}
	if kind != KindAccess && kind != KindRefresh {
		return "", fmt.Errorf("stawitok: unknown kind %q", kind)
	}
	if ttl <= 0 {
		return "", errors.New("stawitok: ttl must be positive")
	}

	prefix, err := randPrefix()
	if err != nil {
		return "", err
	}
	now := i.now()
	info := Info{
		CandidateID: candidateID,
		Kind:        kind,
		IssuedAt:    now,
		ExpiresAt:   now.Add(ttl),
	}
	payload, err := json.Marshal(info)
	if err != nil {
		return "", fmt.Errorf("stawitok: marshal: %w", err)
	}
	if err := i.kv.Set(ctx, tokKeyPrefix+prefix, payload, ttl); err != nil {
		return "", fmt.Errorf("stawitok: kv set: %w", err)
	}
	return prefix + "." + i.macFor(prefix), nil
}

// Verify parses, MAC-checks, and looks up a token. Returns ErrInvalidToken
// for any failure mode the client should treat as 401 — bad shape, bad
// MAC, missing or expired KV row, or a candidate-wide tombstone newer
// than the token's IssuedAt.
func (i *Issuer) Verify(ctx context.Context, token string) (*Info, error) {
	prefix, mac, ok := splitToken(token)
	if !ok {
		return nil, ErrInvalidToken
	}
	expected := i.macFor(prefix)
	if !hmac.Equal([]byte(mac), []byte(expected)) {
		return nil, ErrInvalidToken
	}
	raw, found, err := i.kv.Get(ctx, tokKeyPrefix+prefix)
	if err != nil {
		return nil, fmt.Errorf("stawitok: kv get: %w", err)
	}
	if !found {
		return nil, ErrInvalidToken
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, ErrInvalidToken
	}
	now := i.now()
	if !now.Before(info.ExpiresAt) {
		return nil, ErrExpired
	}
	// Candidate-wide revocation tombstone check. The cutoff is stored
	// in nanoseconds so a mint and a revoke that land in the same
	// wall-clock second still produce the correct ordering — second
	// resolution would race the test (and production).
	tombKey := tombstonePrefix + info.CandidateID
	if tsRaw, tsFound, tsErr := i.kv.Get(ctx, tombKey); tsErr == nil && tsFound {
		if cutoffNano, perr := strconv.ParseInt(strings.TrimSpace(string(tsRaw)), 10, 64); perr == nil {
			if info.IssuedAt.UnixNano() <= cutoffNano {
				return nil, ErrInvalidToken
			}
		}
	}
	info.Prefix = prefix
	return &info, nil
}

// Revoke deletes one token by its prefix. Safe to call with a prefix
// for a token that has already expired or been revoked.
func (i *Issuer) Revoke(ctx context.Context, prefix string) error {
	if prefix == "" {
		return errors.New("stawitok: prefix required")
	}
	return i.kv.Delete(ctx, tokKeyPrefix+prefix)
}

// RevokeAllForCandidate writes a tombstone at the current time. Every
// token for this candidate whose IssuedAt is at or before the
// tombstone time is immediately rejected by Verify. Idempotent.
func (i *Issuer) RevokeAllForCandidate(ctx context.Context, candidateID string) error {
	if candidateID == "" {
		return errors.New("stawitok: candidate id required")
	}
	now := i.now()
	v := strconv.FormatInt(now.UnixNano(), 10)
	// TTL chosen to outlive any refresh token. Once every previously
	// issued token has expired naturally, the tombstone has no work
	// to do and can be evicted.
	return i.kv.Set(ctx, tombstonePrefix+candidateID, []byte(v), tombstoneMaxTTL)
}

func (i *Issuer) macFor(prefix string) string {
	h := hmac.New(sha256.New, i.signingKey)
	h.Write([]byte(prefix))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func randPrefix() (string, error) {
	b := make([]byte, prefixBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("stawitok: random prefix: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func splitToken(token string) (prefix, mac string, ok bool) {
	idx := strings.IndexByte(token, '.')
	if idx <= 0 || idx == len(token)-1 {
		return "", "", false
	}
	return token[:idx], token[idx+1:], true
}
