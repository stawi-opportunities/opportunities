package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
)

// fakeCandidateStore implements the reader interface used by the match handler.
type fakeCandidateStore struct {
	emb   eventsv1.CandidateEmbeddingV1
	prefs eventsv1.PreferencesUpdatedV1
	err   error
}

func (f *fakeCandidateStore) LatestEmbedding(_ context.Context, _ string) (eventsv1.CandidateEmbeddingV1, error) {
	return f.emb, f.err
}
func (f *fakeCandidateStore) LatestPreferences(_ context.Context, _ string) (eventsv1.PreferencesUpdatedV1, error) {
	return f.prefs, nil
}

// fakeSearchIndex returns canned results.
type fakeSearchIndex struct {
	rows []SearchHit
	err  error
}

func (f *fakeSearchIndex) KNNWithFilters(_ context.Context, _ SearchRequest) ([]SearchHit, error) {
	return f.rows, f.err
}

type matchReadyCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.MatchesReadyV1]
}

func (c *matchReadyCollector) Name() string     { return eventsv1.TopicCandidateMatchesReady }
func (c *matchReadyCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *matchReadyCollector) Validate(context.Context, any) error { return nil }
func (c *matchReadyCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.MatchesReadyV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *matchReadyCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestMatchHandlerReturnsScoredTopK(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("match-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &matchReadyCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	jobBlob, _ := json.Marshal(map[string]any{
		"target_roles": []string{"backend-engineer"},
		"salary_min":   70000,
		"currency":     "USD",
		"locations":    map[string]any{"cities": []string{"KE"}, "remote_ok": true},
	})
	handler := MatchHandler(MatchDeps{
		Svc: svc,
		Store: &fakeCandidateStore{
			emb:   eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_1", CVVersion: 2, Vector: []float32{0.1, 0.2, 0.3}},
			prefs: eventsv1.PreferencesUpdatedV1{CandidateID: "cnd_1", OptIns: map[string]json.RawMessage{"job": jobBlob}},
		},
		Search: &fakeSearchIndex{rows: []SearchHit{
			{CanonicalID: "can_a", Slug: "job-a", Title: "Senior Backend", Score: 0.92},
			{CanonicalID: "can_b", Slug: "job-b", Title: "Staff Backend", Score: 0.81},
			{CanonicalID: "can_c", Slug: "job-c", Title: "Mid Backend", Score: 0.70},
		}},
		TopK: 2,
	})

	req := httptest.NewRequest(http.MethodGet, "/candidates/match?candidate_id=cnd_1", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp matchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Matches) != 2 {
		t.Fatalf("matches=%d, want 2", len(resp.Matches))
	}
	if resp.Matches[0].CanonicalID != "can_a" {
		t.Fatalf("order wrong: %+v", resp.Matches)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("MatchesReadyV1 not emitted")
	}
}

func TestMatchHandlerMissingEmbeddingReturns404(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("match-404"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	handler := MatchHandler(MatchDeps{
		Svc:   svc,
		Store: &fakeCandidateStore{err: candidatestore.ErrNotFound},
	})

	req := httptest.NewRequest(http.MethodGet, "/candidates/match?candidate_id=cnd_missing", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}
