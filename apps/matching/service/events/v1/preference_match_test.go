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
	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

type prefMatchesReadyCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.MatchesReadyV1]
}

func (c *prefMatchesReadyCollector) Name() string                       { return eventsv1.TopicCandidateMatchesReady }
func (c *prefMatchesReadyCollector) PayloadType() any                   { var r json.RawMessage; return &r }
func (c *prefMatchesReadyCollector) Validate(context.Context, any) error { return nil }
func (c *prefMatchesReadyCollector) Execute(_ context.Context, payload any) error {
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
func (c *prefMatchesReadyCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

type prefFakeStore struct{ emb eventsv1.CandidateEmbeddingV1 }

func (f *prefFakeStore) LatestEmbedding(_ context.Context, _ string) (eventsv1.CandidateEmbeddingV1, error) {
	return f.emb, nil
}
func (f *prefFakeStore) LatestPreferences(_ context.Context, _ string) (eventsv1.PreferencesUpdatedV1, error) {
	return eventsv1.PreferencesUpdatedV1{}, nil
}

type prefFakeSearch struct{ rows []httpv1.SearchHit }

func (f *prefFakeSearch) KNNWithFilters(_ context.Context, _ httpv1.SearchRequest) ([]httpv1.SearchHit, error) {
	return f.rows, nil
}

// fakeMatcher is a registrable enabled matcher that returns a single
// reason from its Score method.
type fakeMatcher struct {
	kind     string
	disabled bool
}

func (f *fakeMatcher) Kind() string                                { return f.kind }
func (f *fakeMatcher) Disabled() bool                              { return f.disabled }
func (f *fakeMatcher) SearchFilter(_ json.RawMessage) (any, error) { return nil, nil }
func (f *fakeMatcher) Score(_ context.Context, _ json.RawMessage, _ any) (matchers.ScoreResult, error) {
	return matchers.ScoreResult{Score: 0.5, Reasons: []string{"fake reason"}}, nil
}

func TestPreferenceMatchHandler_EmitsMatchesPerEnabledKind(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("preference-match-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &prefMatchesReadyCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	store := &prefFakeStore{emb: eventsv1.CandidateEmbeddingV1{
		CandidateID: "cnd_1", Vector: []float32{0.1},
	}}
	search := &prefFakeSearch{rows: []httpv1.SearchHit{{CanonicalID: "can_a", Score: 0.9}}}
	matchSvc := httpv1.NewMatchService(store, search, 5)

	reg := matchers.NewRegistry()
	reg.Register(&fakeMatcher{kind: "job", disabled: false})
	reg.Register(&fakeMatcher{kind: "tender", disabled: true}) // disabled — should be skipped

	h := NewPreferenceMatchHandler(PreferenceMatchDeps{
		Svc:      svc,
		Match:    matchSvc,
		Matchers: reg,
		TopK:     5,
	})

	in := eventsv1.PreferencesUpdatedV1{
		CandidateID: "cnd_1",
		OptIns: map[string]json.RawMessage{
			"job":    json.RawMessage(`{"target_roles":["Go Engineer"]}`),
			"tender": json.RawMessage(`{}`),
		},
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicCandidatePreferencesUpdated, in)
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)

	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Only the enabled "job" matcher should fire — tender is Disabled().
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1 (tender stub should be skipped)", col.Len())
	}
	if col.got[0].Payload.CandidateID != "cnd_1" {
		t.Fatalf("bad payload: %+v", col.got[0].Payload)
	}
	if len(col.got[0].Payload.Matches) == 0 {
		t.Fatalf("no matches in emitted payload")
	}
}

func TestPreferenceMatchHandler_NoEmitWhenAllMatchersDisabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("preference-match-disabled"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &prefMatchesReadyCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	store := &prefFakeStore{emb: eventsv1.CandidateEmbeddingV1{
		CandidateID: "cnd_1", Vector: []float32{0.1},
	}}
	search := &prefFakeSearch{}
	matchSvc := httpv1.NewMatchService(store, search, 5)

	reg := matchers.NewRegistry()
	reg.Register(&fakeMatcher{kind: "tender", disabled: true})

	h := NewPreferenceMatchHandler(PreferenceMatchDeps{
		Svc:      svc,
		Match:    matchSvc,
		Matchers: reg,
		TopK:     5,
	})

	in := eventsv1.PreferencesUpdatedV1{
		CandidateID: "cnd_1",
		OptIns:      map[string]json.RawMessage{"tender": json.RawMessage(`{}`)},
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicCandidatePreferencesUpdated, in)
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if col.Len() != 0 {
		t.Fatalf("emitted=%d, want 0 (all matchers disabled)", col.Len())
	}
}
