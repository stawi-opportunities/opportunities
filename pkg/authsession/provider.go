package authsession

import (
	"context"
	"errors"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// ErrSessionRequired is returned by SessionProvider.Session when no live
// session exists for (candidateID, sourceType). The autoapply handler
// maps this to a "skipped/session_required" application outcome and
// emits SessionRequiredV1 so the UI prompts the user to reconnect.
var ErrSessionRequired = errors.New("authsession: session required")

// ErrSessionExpired is returned when a row exists but its ExpiresAt is
// in the past or its RevokedAt is non-nil. Treated identically to
// ErrSessionRequired by the autoapply handler — kept distinct here for
// the metrics surface and for tests that want to assert the cause.
var ErrSessionExpired = errors.New("authsession: session expired")

// Session is the decrypted, ready-to-replay view of a stored row. The
// Store hands these out; callers never see the encrypted bytes.
//
// Fields are intentionally a subset of domain.CandidateSession — the
// replay path does not need IDs or audit columns, and limiting the
// surface keeps the plaintext shape small and easy to zero out after
// a single replay.
type Session struct {
	CandidateID string
	SourceType  domain.SourceType
	Payload     domain.SessionPayload
	CapturedAt  time.Time
	ExpiresAt   *time.Time
}

// Capture is the input shape the extension upload endpoint hands to the
// Store. The Store seals the payload, wraps the DEK, and persists.
//
// ExpiresAt is optional — when nil the Store falls back to the source
// manifest's session_ttl or leaves the column NULL (interpreted as
// "rely on the replay leg to detect expiry").
type Capture struct {
	CandidateID   string
	SourceType    domain.SourceType
	Payload       domain.SessionPayload
	CapturedAt    time.Time
	ExpiresAt     *time.Time
	UserAgent     string
	CaptureOrigin string
}

// SessionProvider is the narrow seam the autoapply sessionsubmitter
// depends on. The production implementation is *Store; tests substitute
// a fake.
//
// Implementations must:
//   - Return ErrSessionRequired when no live row exists.
//   - Return ErrSessionExpired when a row exists but has expired or
//     been revoked.
//   - Touch LastUsedAt as a side-effect of Session() so a future
//     stale-session sweep can distinguish dormant from active rows.
type SessionProvider interface {
	Session(ctx context.Context, candidateID string, sourceType domain.SourceType) (*Session, error)
	Record(ctx context.Context, c Capture) error
	Revoke(ctx context.Context, candidateID string, sourceType domain.SourceType) error
}
