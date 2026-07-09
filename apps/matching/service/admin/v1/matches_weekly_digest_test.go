package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/frametests"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeActiveLister struct {
	ids []string
	err error
}

func (f *fakeActiveLister) ListActive(context.Context) ([]string, error) { return f.ids, f.err }

type fakeIndexReader struct {
	byID map[string]*matching.CandidateIndex
}

func (f *fakeIndexReader) Get(_ context.Context, id string) (*matching.CandidateIndex, error) {
	if ci, ok := f.byID[id]; ok {
		return ci, nil
	}
	return nil, matching.ErrNotFound
}

func newAdminSvc(t *testing.T) (context.Context, *frame.Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("matches-digest-test"),
		frametests.WithNoopDriver(),
	)
	t.Cleanup(func() { svc.Stop(ctx) })
	return ctx, svc
}

// Candidates without an index row (no embedding yet) are skipped, not
// failed — GapFill is never invoked so the nil KNN/Store is safe.
func TestMatchesWeeklyDigestSkipsCandidatesWithoutIndex(t *testing.T) {
	_, svc := newAdminSvc(t)
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:    svc,
		Active: &fakeActiveLister{ids: []string{"cnd_1", "cnd_2"}},
		Index:  &fakeIndexReader{byID: map[string]*matching.CandidateIndex{}},
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp matchesWeeklyDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Audience != 2 || resp.Skipped != 2 || resp.Matched != 0 || resp.Failed != 0 {
		t.Fatalf("resp=%+v, want audience=2 skipped=2 matched=0 failed=0", resp)
	}
}

// An index row with an empty embedding is also skipped.
func TestMatchesWeeklyDigestSkipsEmptyEmbedding(t *testing.T) {
	_, svc := newAdminSvc(t)
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:    svc,
		Active: &fakeActiveLister{ids: []string{"cnd_1"}},
		Index: &fakeIndexReader{byID: map[string]*matching.CandidateIndex{
			"cnd_1": {CandidateID: "cnd_1", Embedding: nil},
		}},
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp matchesWeeklyDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Skipped != 1 || resp.Matched != 0 {
		t.Fatalf("resp=%+v, want skipped=1 matched=0", resp)
	}
}

func TestMatchesWeeklyDigestListActiveError(t *testing.T) {
	_, svc := newAdminSvc(t)
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:    svc,
		Active: &fakeActiveLister{err: errors.New("db wedged")},
		Index:  &fakeIndexReader{},
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", rec.Code)
	}
}

func TestMatchesWeeklyDigestRejectsNonPost(t *testing.T) {
	_, svc := newAdminSvc(t)
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:    svc,
		Active: &fakeActiveLister{},
		Index:  &fakeIndexReader{},
	})
	req := httptest.NewRequest(http.MethodGet, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405", rec.Code)
	}
}

func TestNewRepoActiveCandidateLister(t *testing.T) {
	called := false
	l := NewRepoActiveCandidateLister(func(_ context.Context, limit int) ([]string, error) {
		called = true
		if limit != 5000 {
			t.Fatalf("limit=%d, want default 5000", limit)
		}
		return []string{"a", "b"}, nil
	}, 0)
	ids, err := l.ListActive(context.Background())
	if err != nil || !called || len(ids) != 2 {
		t.Fatalf("ListActive: ids=%v err=%v called=%v", ids, err, called)
	}
}
