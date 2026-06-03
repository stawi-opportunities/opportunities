package matching_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeKNN struct {
	hits []matching.CandidateHit
	err  error
}

func (f *fakeKNN) FanOutKNN(_ context.Context, _ matching.FanOutKNNParams) ([]matching.CandidateHit, error) {
	return f.hits, f.err
}

type fakeStore struct {
	ms  []matching.Match
	err error
}

func (f *fakeStore) UpsertMatches(_ context.Context, ms []matching.Match) error {
	f.ms = append(f.ms, ms...)
	return f.err
}

type fakeEventLog struct {
	matchEvents []matching.MatchEvent
	runEvents   []matching.MatchRunEvent
}

func (f *fakeEventLog) WriteMatchEvent(_ context.Context, e matching.MatchEvent) error {
	f.matchEvents = append(f.matchEvents, e)
	return nil
}
func (f *fakeEventLog) WriteMatchRunEvent(_ context.Context, e matching.MatchRunEvent) error {
	f.runEvents = append(f.runEvents, e)
	return nil
}

func unitVec(dim, axis int) []float32 {
	v := make([]float32, dim)
	if axis >= 0 && axis < dim {
		v[axis] = 1
	} else {
		v[0] = 1
	}
	return v
}

func TestFanOut_FiltersBelowMinScore(t *testing.T) {
	knn := &fakeKNN{hits: []matching.CandidateHit{
		{CandidateID: "above", Distance: 0.2, MinScore: 0.5, Kinds: []string{"job"}},
		{CandidateID: "below", Distance: 1.9, MinScore: 0.5, Kinds: []string{"job"}},
	}}
	store := &fakeStore{}
	el := &fakeEventLog{}

	res, err := matching.FanOut(context.Background(), matching.FanOutInput{
		CanonicalID:   "c-1",
		OpportunityID: "o-1",
		Kind:          "job",
		Country:       "KE",
		Embedding:     unitVec(1024, 0),
		FirstSeenAt:   time.Now(),
	}, matching.FanOutDeps{
		KNN: knn, Store: store, EventLog: el,
		Reranker: matching.NoopReranker{},
		Weights:  matching.DefaultWeights(),
		Now:      func() time.Time { return time.Now() },
	})
	require.NoError(t, err)
	require.Equal(t, 2, res.CandidatesScanned)
	require.Equal(t, 1, res.MatchesWritten)
	require.Len(t, store.ms, 1)
	require.Equal(t, "above", store.ms[0].CandidateID)
	require.Len(t, el.runEvents, 1)
	require.Equal(t, "ok", el.runEvents[0].Status)
}

func TestFanOut_RerankerSkippedReportsStatus(t *testing.T) {
	knn := &fakeKNN{hits: []matching.CandidateHit{
		{CandidateID: "c1", Distance: 0.1, MinScore: 0.3, Kinds: []string{"job"}},
	}}
	res, err := matching.FanOut(context.Background(), matching.FanOutInput{
		CanonicalID: "c-2", OpportunityID: "o-2", Kind: "job",
		Embedding: unitVec(1024, 0), FirstSeenAt: time.Now(),
	}, matching.FanOutDeps{
		KNN: knn, Store: &fakeStore{}, EventLog: &fakeEventLog{},
		Reranker: matching.NoopReranker{},
		Weights:  matching.DefaultWeights(),
	})
	require.NoError(t, err)
	require.Equal(t, "skipped", res.RerankerStatus)
}
