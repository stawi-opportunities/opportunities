package service

import (
	"context"
	"encoding/json"
	"testing"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
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
	h := NewSourceDiscoveredHandler(repo)

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
}

func TestSourceDiscoveredSkipsSameDomain(t *testing.T) {
	repo := newFakeUpserter()
	repo.rows["s-origin"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s-origin"},
		BaseURL:   "https://example.com",
	}
	h := NewSourceDiscoveredHandler(repo)

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
	h := NewSourceDiscoveredHandler(repo)

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
