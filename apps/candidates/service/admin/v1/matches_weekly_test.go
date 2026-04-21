package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

type fakeCandidateLister struct {
	ids []string
}

func (f *fakeCandidateLister) ListActive(_ context.Context) ([]string, error) {
	return f.ids, nil
}

type fakeMatchRunner struct{ called []string }

func (f *fakeMatchRunner) RunMatch(_ context.Context, candidateID string) error {
	f.called = append(f.called, candidateID)
	return nil
}

func TestMatchesWeeklyHandlerRunsMatchPerCandidate(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("weekly-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	runner := &fakeMatchRunner{}
	handler := MatchesWeeklyHandler(MatchesWeeklyDeps{
		Lister: &fakeCandidateLister{ids: []string{"cnd_1", "cnd_2", "cnd_3"}},
		Runner: runner,
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp matchesWeeklyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Processed != 3 {
		t.Fatalf("processed=%d, want 3", resp.Processed)
	}
	if len(runner.called) != 3 {
		t.Fatalf("RunMatch called %d times, want 3", len(runner.called))
	}

	_ = eventsv1.TopicCandidateMatchesReady // keep import referenced
	_ = time.Now
}
