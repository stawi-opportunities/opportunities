package matching_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeRevKNN struct{ hits []matching.OppHit }

func (f *fakeRevKNN) ReverseKNN(_ context.Context, _ matching.ReverseKNNParams) ([]matching.OppHit, error) {
	return f.hits, nil
}

func TestGapFill_FiltersBelowMinScore(t *testing.T) {
	knn := &fakeRevKNN{hits: []matching.OppHit{
		{OpportunityID: "good", Distance: 0.1, FirstSeenAt: time.Now()},
		{OpportunityID: "bad", Distance: 1.9, FirstSeenAt: time.Now()},
	}}
	store := &fakeStore{}
	el := &fakeEventLog{}
	res, err := matching.GapFill(context.Background(), matching.GapFillInput{
		CandidateID: "u",
		Embedding:   unitVec(1536, 0),
		Since:       time.Now().Add(-time.Hour),
		MinScore:    0.4,
	}, matching.GapFillDeps{
		KNN: knn, Store: store, EventLog: el,
		Reranker: matching.NoopReranker{},
		Weights:  matching.DefaultWeights(),
	})
	require.NoError(t, err)
	require.Equal(t, 2, res.OppsScanned)
	require.Equal(t, 1, res.MatchesWritten)
	require.Equal(t, "good", store.ms[0].OpportunityID)
}
