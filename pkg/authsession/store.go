package authsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// SessionRepo is the slice of repository.CandidateSessionRepository the
// Store depends on. Defining the interface here lets tests substitute an
// in-memory fake without pulling GORM into pkg/authsession.
type SessionRepo interface {
	Upsert(ctx context.Context, s *domain.CandidateSession) error
	GetActive(ctx context.Context, candidateID string, sourceType domain.SourceType) (*domain.CandidateSession, error)
	ListForCandidate(ctx context.Context, candidateID string) ([]*domain.CandidateSession, error)
	MarkUsed(ctx context.Context, id string) error
	Revoke(ctx context.Context, candidateID string, sourceType domain.SourceType) error
	RevokeAll(ctx context.Context, candidateID string) error
}

// Clock abstracts time so tests can pin "now". The default (nil) uses
// time.Now().UTC().
type Clock func() time.Time

// Store is the production SessionProvider. It is the only place in the
// codebase that holds a plaintext SessionPayload alongside the master
// key — keep the surface small.
type Store struct {
	repo    SessionRepo
	wrapper Wrapper
	clock   Clock
}

// NewStore constructs a Store. The Wrapper must already be configured
// (typically a LocalWrapper loaded from SESSION_MASTER_KEY at startup).
func NewStore(repo SessionRepo, wrapper Wrapper) *Store {
	if repo == nil {
		panic("authsession: nil repo")
	}
	if wrapper == nil {
		panic("authsession: nil wrapper")
	}
	return &Store{repo: repo, wrapper: wrapper}
}

// WithClock returns a copy of the Store using the supplied clock. For
// tests.
func (s *Store) WithClock(c Clock) *Store {
	cp := *s
	cp.clock = c
	return &cp
}

func (s *Store) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now().UTC()
}

// Record encrypts and persists a fresh capture from the extension. Any
// prior live row for the same (candidate, source) is soft-deleted by the
// repository in the same transaction (see CandidateSessionRepository.Upsert).
func (s *Store) Record(ctx context.Context, c Capture) error {
	if c.CandidateID == "" || c.SourceType == "" {
		return errors.New("authsession: candidate id and source type are required")
	}

	plaintext, err := json.Marshal(c.Payload)
	if err != nil {
		return fmt.Errorf("authsession: marshal payload: %w", err)
	}

	dek, err := NewDEK()
	if err != nil {
		return err
	}
	payloadNonce, payloadCT, err := SealPayload(dek, plaintext)
	if err != nil {
		return fmt.Errorf("authsession: seal payload: %w", err)
	}
	dekWrapped, dekNonce, keyID, err := s.wrapper.Wrap(dek)
	if err != nil {
		return fmt.Errorf("authsession: wrap dek: %w", err)
	}

	row := &domain.CandidateSession{
		CandidateID:   c.CandidateID,
		SourceType:    c.SourceType,
		PayloadEnc:    payloadCT,
		PayloadNonce:  payloadNonce,
		DEKWrapped:    dekWrapped,
		DEKNonce:      dekNonce,
		KeyID:         keyID,
		CapturedAt:    nonZeroOr(c.CapturedAt, s.now()),
		ExpiresAt:     c.ExpiresAt,
		UserAgent:     c.UserAgent,
		CaptureOrigin: defaultOrigin(c.CaptureOrigin),
	}
	return s.repo.Upsert(ctx, row)
}

// Session implements SessionProvider. Returns ErrSessionRequired or
// ErrSessionExpired when no usable row exists; otherwise decrypts and
// returns the plaintext payload. MarkUsed is best-effort and failures
// are logged-by-caller via the returned Session (kept side-effect-free
// here so the replay leg can decide whether to swallow MarkUsed errors).
func (s *Store) Session(ctx context.Context, candidateID string, sourceType domain.SourceType) (*Session, error) {
	row, err := s.repo.GetActive(ctx, candidateID, sourceType)
	if err != nil {
		return nil, fmt.Errorf("authsession: lookup: %w", err)
	}
	if row == nil {
		return nil, ErrSessionRequired
	}
	if IsRowExpired(row, s.now()) {
		return nil, ErrSessionExpired
	}

	dek, err := s.wrapper.Unwrap(row.DEKWrapped, row.DEKNonce, row.KeyID)
	if err != nil {
		return nil, fmt.Errorf("authsession: unwrap dek: %w", err)
	}
	plaintext, err := OpenPayload(dek, row.PayloadNonce, row.PayloadEnc)
	if err != nil {
		return nil, fmt.Errorf("authsession: open payload: %w", err)
	}

	var payload domain.SessionPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, fmt.Errorf("authsession: unmarshal payload: %w", err)
	}

	if err := s.repo.MarkUsed(ctx, row.ID); err != nil {
		// Best-effort: a missing last_used_at stamp is not a reason
		// to skip an apply. Log so operators see flapping DB writes,
		// then proceed with the decrypted session.
		util.Log(ctx).WithError(err).Warn("authsession: mark used failed")
	}

	return &Session{
		CandidateID: row.CandidateID,
		SourceType:  row.SourceType,
		Payload:     payload,
		CapturedAt:  row.CapturedAt,
		ExpiresAt:   row.ExpiresAt,
	}, nil
}

// Revoke implements SessionProvider.
func (s *Store) Revoke(ctx context.Context, candidateID string, sourceType domain.SourceType) error {
	return s.repo.Revoke(ctx, candidateID, sourceType)
}

func nonZeroOr(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

func defaultOrigin(o string) string {
	if o == "" {
		return domain.SessionOriginExtension
	}
	return o
}
