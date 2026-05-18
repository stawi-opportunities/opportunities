package authsession_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/authsession"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// fakeRepo is an in-memory authsession.SessionRepo used by Store tests.
// It keeps live rows in a map keyed by (candidate_id, source_type) so the
// uniqueness invariant the production repository enforces via the partial
// index is preserved here.
type fakeRepo struct {
	mu        sync.Mutex
	rows      map[string]*domain.CandidateSession
	markUsed  []string
	upsertErr error
	getErr    error
	markErr   error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[string]*domain.CandidateSession{}}
}

func (f *fakeRepo) key(cid string, st domain.SourceType) string { return cid + "|" + string(st) }

func (f *fakeRepo) Upsert(_ context.Context, s *domain.CandidateSession) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	if s.ID == "" {
		s.ID = domain.NewID()
	}
	f.rows[f.key(s.CandidateID, s.SourceType)] = s
	return nil
}

func (f *fakeRepo) GetActive(_ context.Context, cid string, st domain.SourceType) (*domain.CandidateSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	r, ok := f.rows[f.key(cid, st)]
	if !ok {
		return nil, nil
	}
	if r.RevokedAt != nil {
		return nil, nil
	}
	return r, nil
}

func (f *fakeRepo) ListForCandidate(_ context.Context, cid string) ([]*domain.CandidateSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.CandidateSession
	for _, r := range f.rows {
		if r.CandidateID == cid && r.RevokedAt == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRepo) MarkUsed(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.markUsed = append(f.markUsed, id)
	return nil
}

func (f *fakeRepo) Revoke(_ context.Context, cid string, st domain.SourceType) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[f.key(cid, st)]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	r.RevokedAt = &now
	return nil
}

func (f *fakeRepo) RevokeAll(_ context.Context, cid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	for _, r := range f.rows {
		if r.CandidateID == cid {
			r.RevokedAt = &now
		}
	}
	return nil
}

// newStore returns a Store wired to a fresh in-memory fakeRepo and a
// fresh LocalWrapper. Tests reuse the helper so the master key never
// leaks across tests.
func newStore(t *testing.T) (*authsession.Store, *fakeRepo) {
	t.Helper()
	repo := newFakeRepo()
	w, err := authsession.NewLocalWrapper(mustMaster(t), "v1")
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}
	return authsession.NewStore(repo, w), repo
}

func samplePayload() domain.SessionPayload {
	expires := time.Now().UTC().Add(24 * time.Hour)
	return domain.SessionPayload{
		Cookies: []domain.SessionCookie{
			{Name: "laravel_session", Value: "abc", Domain: ".brightermonday.co.ke", Path: "/", Expires: &expires, HTTPOnly: true},
			{Name: "XSRF-TOKEN", Value: "xyz", Domain: ".brightermonday.co.ke"},
		},
		Headers: map[string]string{"Accept-Language": "en-US,en;q=0.9"},
	}
}

func TestStore_RecordThenSession_RoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	captured := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	want := samplePayload()
	if err := s.Record(ctx, authsession.Capture{
		CandidateID: "cnd_1",
		SourceType:  domain.SourceBrighterMonday,
		Payload:     want,
		CapturedAt:  captured,
		UserAgent:   "Mozilla/5.0",
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := s.Session(ctx, "cnd_1", domain.SourceBrighterMonday)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if got.CandidateID != "cnd_1" {
		t.Fatalf("candidate_id = %q", got.CandidateID)
	}
	if got.SourceType != domain.SourceBrighterMonday {
		t.Fatalf("source_type = %q", got.SourceType)
	}
	if !got.CapturedAt.Equal(captured) {
		t.Fatalf("captured_at = %v, want %v", got.CapturedAt, captured)
	}
	if len(got.Payload.Cookies) != 2 || got.Payload.Cookies[0].Name != "laravel_session" {
		t.Fatalf("payload cookies mismatch: %+v", got.Payload.Cookies)
	}
	if got.Payload.Headers["Accept-Language"] != "en-US,en;q=0.9" {
		t.Fatalf("payload headers lost on round-trip")
	}
}

func TestStore_Session_NoRow(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.Session(context.Background(), "cnd_missing", domain.SourceBrighterMonday)
	if !errors.Is(err, authsession.ErrSessionRequired) {
		t.Fatalf("want ErrSessionRequired, got %v", err)
	}
}

func TestStore_Session_Expired(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Hour)
	if err := s.Record(ctx, authsession.Capture{
		CandidateID: "cnd_2",
		SourceType:  domain.SourceBrighterMonday,
		Payload:     domain.SessionPayload{Cookies: []domain.SessionCookie{{Name: "x", Value: "y"}}},
		ExpiresAt:   &past,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.Session(ctx, "cnd_2", domain.SourceBrighterMonday); !errors.Is(err, authsession.ErrSessionExpired) {
		t.Fatalf("want ErrSessionExpired, got %v", err)
	}
}

func TestStore_Record_UpsertReplaces(t *testing.T) {
	s, repo := newStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := s.Record(ctx, authsession.Capture{
			CandidateID: "cnd_3",
			SourceType:  domain.SourceBrighterMonday,
			Payload:     domain.SessionPayload{Cookies: []domain.SessionCookie{{Name: "n", Value: "v"}}},
		}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	// fakeRepo keeps one row per (candidate, source). The real repository
	// soft-deletes prior rows; that behaviour is exercised in the
	// integration tests.
	if got := len(repo.rows); got != 1 {
		t.Fatalf("rows after 3 records = %d, want 1", got)
	}
}

func TestStore_Revoke(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, authsession.Capture{
		CandidateID: "cnd_4",
		SourceType:  domain.SourceBrighterMonday,
		Payload:     domain.SessionPayload{Cookies: []domain.SessionCookie{{Name: "n", Value: "v"}}},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := s.Revoke(ctx, "cnd_4", domain.SourceBrighterMonday); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.Session(ctx, "cnd_4", domain.SourceBrighterMonday); !errors.Is(err, authsession.ErrSessionRequired) {
		t.Fatalf("after revoke want ErrSessionRequired, got %v", err)
	}
}

func TestStore_Record_RejectsEmptyIDs(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Record(context.Background(), authsession.Capture{SourceType: domain.SourceBrighterMonday}); err == nil {
		t.Fatal("want error for empty candidate id")
	}
	if err := s.Record(context.Background(), authsession.Capture{CandidateID: "cnd"}); err == nil {
		t.Fatal("want error for empty source type")
	}
}

func TestStore_Session_MarkUsedFailureDoesNotBlock(t *testing.T) {
	s, repo := newStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, authsession.Capture{
		CandidateID: "cnd_5",
		SourceType:  domain.SourceBrighterMonday,
		Payload:     domain.SessionPayload{Cookies: []domain.SessionCookie{{Name: "n", Value: "v"}}},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	repo.markErr = errors.New("simulated mark_used failure")
	got, err := s.Session(ctx, "cnd_5", domain.SourceBrighterMonday)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if got == nil || len(got.Payload.Cookies) == 0 {
		t.Fatal("expected payload despite mark-used failure")
	}
}

func TestStore_WithClock(t *testing.T) {
	base, _ := newStore(t)
	pinned := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s := base.WithClock(func() time.Time { return pinned })
	ctx := context.Background()
	past := pinned.Add(-time.Hour)
	if err := s.Record(ctx, authsession.Capture{
		CandidateID: "cnd_6",
		SourceType:  domain.SourceBrighterMonday,
		Payload:     domain.SessionPayload{Cookies: []domain.SessionCookie{{Name: "n", Value: "v"}}},
		ExpiresAt:   &past,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.Session(ctx, "cnd_6", domain.SourceBrighterMonday); !errors.Is(err, authsession.ErrSessionExpired) {
		t.Fatalf("pinned clock should see expired row; got %v", err)
	}
}
