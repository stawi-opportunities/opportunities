package v1

import (
	"context"
	"testing"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

func TestMatchServiceRunMatchReturnsHits(t *testing.T) {
	store := &fakeCandidateStore{
		emb:   eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_1", Vector: []float32{0.1}},
		prefs: eventsv1.PreferencesUpdatedV1{CandidateID: "cnd_1"},
	}
	search := &fakeSearchIndex{rows: []SearchHit{
		{CanonicalID: "can_a", Score: 0.9},
		{CanonicalID: "can_b", Score: 0.8},
	}}
	svc := NewMatchService(store, search, 5)

	res, err := svc.RunMatch(context.Background(), "cnd_1")
	if err != nil {
		t.Fatalf("RunMatch: %v", err)
	}
	if len(res.Matches) != 2 || res.Matches[0].CanonicalID != "can_a" {
		t.Fatalf("bad result: %+v", res)
	}
	if res.CandidateID != "cnd_1" || res.MatchBatchID == "" {
		t.Fatalf("ids wrong: %+v", res)
	}
}

func TestMatchServiceRunMatchMissingEmbeddingReturnsErrNoEmbedding(t *testing.T) {
	store := &fakeCandidateStore{err: errNoEmbedding}
	svc := NewMatchService(store, &fakeSearchIndex{}, 5)

	_, err := svc.RunMatch(context.Background(), "cnd_missing")
	if err == nil {
		t.Fatalf("expected error")
	}
}
