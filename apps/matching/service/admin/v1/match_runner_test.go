package v1

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	httpv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

type matchesReadyCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.MatchesReadyV1]
}

func (c *matchesReadyCollector) Name() string     { return eventsv1.TopicCandidateMatchesReady }
func (c *matchesReadyCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *matchesReadyCollector) Validate(context.Context, any) error { return nil }
func (c *matchesReadyCollector) Execute(_ context.Context, payload any) error {
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
func (c *matchesReadyCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

type fakeStore struct{ emb eventsv1.CandidateEmbeddingV1 }

func (f *fakeStore) LatestEmbedding(_ context.Context, _ string) (eventsv1.CandidateEmbeddingV1, error) {
	return f.emb, nil
}
func (f *fakeStore) LatestPreferences(_ context.Context, _ string) (eventsv1.PreferencesUpdatedV1, error) {
	return eventsv1.PreferencesUpdatedV1{}, nil
}

type fakeSearch struct{ rows []httpv1.SearchHit }

func (f *fakeSearch) KNNWithFilters(_ context.Context, _ httpv1.SearchRequest) ([]httpv1.SearchHit, error) {
	return f.rows, nil
}

func TestServiceMatchRunnerEmitsMatchesReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("runner-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &matchesReadyCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	store := &fakeStore{emb: eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_1", Vector: []float32{0.1}}}
	search := &fakeSearch{rows: []httpv1.SearchHit{{CanonicalID: "can_a", Score: 0.9}}}
	matchSvc := httpv1.NewMatchService(store, search, 5)

	runner := NewServiceMatchRunner(svc, matchSvc)
	if err := runner.RunMatch(ctx, "cnd_1"); err != nil {
		t.Fatalf("RunMatch: %v", err)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	if col.got[0].Payload.CandidateID != "cnd_1" {
		t.Fatalf("bad payload: %+v", col.got[0].Payload)
	}
}

func TestServiceMatchRunnerSwallowsMissingEmbedding(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("runner-missing"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	// store returns ErrNoEmbedding
	store := &fakeStoreErrNoEmbedding{}
	search := &fakeSearch{}
	matchSvc := httpv1.NewMatchService(store, search, 5)

	runner := NewServiceMatchRunner(svc, matchSvc)
	if err := runner.RunMatch(ctx, "cnd_x"); err != nil {
		t.Fatalf("RunMatch should swallow ErrNoEmbedding, got: %v", err)
	}
}

type fakeStoreErrNoEmbedding struct{}

func (f *fakeStoreErrNoEmbedding) LatestEmbedding(_ context.Context, _ string) (eventsv1.CandidateEmbeddingV1, error) {
	return eventsv1.CandidateEmbeddingV1{}, httpv1.ErrNoEmbedding
}
func (f *fakeStoreErrNoEmbedding) LatestPreferences(_ context.Context, _ string) (eventsv1.PreferencesUpdatedV1, error) {
	return eventsv1.PreferencesUpdatedV1{}, nil
}
