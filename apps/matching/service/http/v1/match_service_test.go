package v1

import (
	"context"
	"errors"
	"testing"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
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
	store := &fakeCandidateStore{err: candidatestore.ErrNotFound}
	svc := NewMatchService(store, &fakeSearchIndex{}, 5)

	_, err := svc.RunMatch(context.Background(), "cnd_missing")
	if !errors.Is(err, ErrNoEmbedding) {
		t.Fatalf("expected ErrNoEmbedding, got %v", err)
	}
}

func TestMatchServiceRunMatchPropagatesInfraErrors(t *testing.T) {
	store := &fakeCandidateStore{err: errors.New("r2 timeout")}
	svc := NewMatchService(store, &fakeSearchIndex{}, 5)

	_, err := svc.RunMatch(context.Background(), "cnd_x")
	if err == nil {
		t.Fatalf("expected error")
	}
	if errors.Is(err, ErrNoEmbedding) {
		t.Fatalf("infra error should not be wrapped as ErrNoEmbedding, got: %v", err)
	}
}
