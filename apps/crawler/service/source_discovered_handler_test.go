package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

type fakeSourceUpserter struct {
	upserts []string
	rows    map[string]*domain.Source
}

func newFakeUpserter() *fakeSourceUpserter {
	return &fakeSourceUpserter{rows: map[string]*domain.Source{}}
}

func (r *fakeSourceUpserter) GetByID(_ context.Context, id string) (*domain.Source, error) {
	s, ok := r.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}
func (r *fakeSourceUpserter) Upsert(_ context.Context, s *domain.Source) error {
	r.upserts = append(r.upserts, s.BaseURL)
	r.rows[s.BaseURL] = s
	return nil
}

func TestSourceDiscoveredUpsertsNewURL(t *testing.T) {
	repo := newFakeUpserter()
	repo.rows["s-origin"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s-origin"},
		BaseURL:   "https://example.com",
	}
	h := NewSourceDiscoveredHandler(repo, nil)

	env := eventsv1.NewEnvelope(eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
		DiscoveredURL: "https://anotherboard.example/careers",
		SourceID:      "s-origin",
		Country:       "KE",
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	if err := h.Execute(context.Background(), &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(repo.upserts) != 1 {
		t.Fatalf("upserts=%v, want one", repo.upserts)
	}

	// New source must land in SourcePending so the scheduler does not
	// pick it up before an operator (or the verifier auto-promotion path)
	// has approved it.
	saved := repo.rows["https://anotherboard.example"]
	if saved == nil {
		t.Fatalf("expected upsert to record under base URL key")
	}
	if saved.Status != domain.SourcePending {
		t.Errorf("Status=%q want %q", saved.Status, domain.SourcePending)
	}
	if saved.AutoApprove {
		t.Errorf("AutoApprove=true on a discovered source; should default to false")
	}
}

func TestSourceDiscoveredSkipsSameDomain(t *testing.T) {
	repo := newFakeUpserter()
	repo.rows["s-origin"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s-origin"},
		BaseURL:   "https://example.com",
	}
	h := NewSourceDiscoveredHandler(repo, nil)

	env := eventsv1.NewEnvelope(eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
		DiscoveredURL: "https://example.com/another-page",
		SourceID:      "s-origin",
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	_ = h.Execute(context.Background(), &rm)

	if len(repo.upserts) != 0 {
		t.Fatalf("same-domain URL must be skipped, got upserts=%v", repo.upserts)
	}
}

func TestSourceDiscoveredSkipsBlocklistedDomain(t *testing.T) {
	repo := newFakeUpserter()
	repo.rows["s-origin"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s-origin"},
		BaseURL:   "https://example.com",
	}
	h := NewSourceDiscoveredHandler(repo, nil)

	env := eventsv1.NewEnvelope(eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
		DiscoveredURL: "https://linkedin.com/jobs/apply/foo",
		SourceID:      "s-origin",
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	_ = h.Execute(context.Background(), &rm)

	if len(repo.upserts) != 0 {
		t.Fatalf("blocked domain must be skipped, got upserts=%v", repo.upserts)
	}
}

func TestSourceDiscoveredDropsEventIfOriginMissing(t *testing.T) {
	// No rows seeded in the repo, so GetByID returns (nil, nil) for any id.
	repo := newFakeUpserter()
	h := NewSourceDiscoveredHandler(repo, nil)

	env := eventsv1.NewEnvelope(eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
		DiscoveredURL: "https://newboard.example/careers",
		SourceID:      "origin-that-does-not-exist",
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)

	if err := h.Execute(context.Background(), &rm); err != nil {
		t.Fatalf("Execute should return nil when origin is missing, got: %v", err)
	}
	if len(repo.upserts) != 0 {
		t.Fatalf("missing origin must not upsert, got upserts=%v", repo.upserts)
	}
}
