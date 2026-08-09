package placement

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type memStore struct {
	byID map[string]Document
}

func (m *memStore) Upsert(_ context.Context, doc Document) (int, error) {
	if m.byID == nil {
		m.byID = map[string]Document{}
	}
	doc.Version++
	m.byID[doc.CandidateID] = doc
	return doc.Version, nil
}

func (m *memStore) Get(_ context.Context, candidateID string) (*Document, error) {
	d, ok := m.byID[candidateID]
	if !ok {
		return nil, nil
	}
	cp := d
	return &cp, nil
}

type stubEmbedder struct {
	vec []float32
	err error
}

func (s stubEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.vec, nil
}

func TestRebuild_SoftEmbedFailureKeepsSummary(t *testing.T) {
	svc := &Service{
		Store:    &memStore{},
		Embedder: stubEmbedder{err: errors.New("model down")},
	}
	res, err := svc.Rebuild(context.Background(), RebuildInput{
		CandidateID: "c1",
		Fields: Fields{
			TargetJobTitle: "Engineer",
			ExtraInfo:      strings.Repeat("experience education skills golang backend ", 20),
		},
	})
	if err != nil {
		t.Fatalf("soft rebuild should not fail: %v", err)
	}
	if res.Embedded {
		t.Fatal("expected Embedded=false when embedder fails")
	}
	if strings.TrimSpace(res.Document.SummaryText) == "" {
		t.Fatal("summary must still be stored")
	}
}

func TestRebuild_StrictEmbedSurfacesEmbedderFailure(t *testing.T) {
	svc := &Service{
		Store:    &memStore{},
		Embedder: stubEmbedder{err: errors.New("model down")},
	}
	_, err := svc.Rebuild(context.Background(), RebuildInput{
		CandidateID: "c1",
		Fields: Fields{
			TargetJobTitle: "Engineer",
			ExtraInfo:      strings.Repeat("experience education skills golang backend ", 20),
		},
		StrictEmbed: true,
	})
	if err == nil {
		t.Fatal("strict rebuild must return embedder error")
	}
	if !strings.Contains(err.Error(), "embed failed") {
		t.Fatalf("want embed failed error, got %v", err)
	}
}

func TestRebuild_StrictEmbedRequiresMaterial(t *testing.T) {
	svc := &Service{
		Store:    &memStore{},
		Embedder: stubEmbedder{vec: []float32{0.1, 0.2}},
	}
	_, err := svc.Rebuild(context.Background(), RebuildInput{
		CandidateID: "c1",
		Fields:      Fields{},
		StrictEmbed: true,
	})
	if err == nil {
		t.Fatal("strict rebuild with empty fields must fail")
	}
}

func TestRebuild_ThinExtraInfoStillEmbeds(t *testing.T) {
	// Skills-only ExtraInfo used to skip embedding (looksLikeCV false + no title).
	svc := &Service{
		Store:    &memStore{},
		Embedder: stubEmbedder{vec: make([]float32, 8)},
		// Index left nil: Embedded still set when vec returned.
	}
	res, err := svc.Rebuild(context.Background(), RebuildInput{
		CandidateID: "c1",
		Fields: Fields{
			ExtraInfo: "skills: go, kubernetes, kubernetes",
		},
		StrictEmbed: true,
	})
	if err != nil {
		t.Fatalf("thin ExtraInfo should embed: %v", err)
	}
	if !res.Embedded {
		t.Fatal("expected Embedded=true for thin ExtraInfo")
	}
}

func TestRebuild_ReusesPriorCVWhenTurnIsThin(t *testing.T) {
	store := &memStore{byID: map[string]Document{
		"c1": {
			CandidateID:        "c1",
			QualificationsText: "## Qualifications\n" + strings.Repeat("experience education skills kubernetes platform engineer. ", 30),
		},
	}}
	svc := &Service{
		Store:    store,
		Embedder: stubEmbedder{vec: make([]float32, 4)},
	}
	res, err := svc.Rebuild(context.Background(), RebuildInput{
		CandidateID: "c1",
		Fields: Fields{
			TargetJobTitle: "SRE",
			// Thin ExtraInfo — prior full CV should be preferred.
			ExtraInfo: "skills: k8s",
		},
	})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if !strings.Contains(res.Document.QualificationsText, "platform engineer") {
		t.Fatalf("expected prior CV corpus retained, got %q", res.Document.QualificationsText)
	}
}
