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
		Embedding:   unitVec(1024, 0),
		Since:       time.Now().Add(-time.Hour),
		// With neutral skills/geo/salary, floor total ≈ 0.40 + 0.6·cos.
		// MinScore 0.45 rejects pure-orthogonal neighbors (cos≈0).
		MinScore: 0.45,
	}, matching.GapFillDeps{
		KNN: knn, Store: store, EventLog: el,
		Reranker: matching.NoopReranker{},
		Weights:  matching.DefaultWeights(),
	})
	require.NoError(t, err)
	require.Equal(t, 2, res.OppsScanned)
	require.Equal(t, 1, res.MatchesWritten)
	require.Equal(t, matching.GapReasonOK, res.Reason)
	require.Equal(t, "good", store.ms[0].OpportunityID)
}

type fixedWeekCount struct{ n int }

func (f fixedWeekCount) CountNonOverflowThisWeek(_ context.Context, _ string) (int, error) {
	return f.n, nil
}

func TestGapFill_WeeklyCapBlocksWhenExhausted(t *testing.T) {
	knn := &fakeRevKNN{hits: []matching.OppHit{
		{OpportunityID: "good", Distance: 0.1, FirstSeenAt: time.Now()},
	}}
	store := &fakeStore{}
	el := &fakeEventLog{}
	res, err := matching.GapFill(context.Background(), matching.GapFillInput{
		CandidateID: "u",
		Embedding:   unitVec(1024, 0),
		Since:       time.Now().Add(-time.Hour),
		MinScore:    0.45,
		WeeklyCap:   3,
	}, matching.GapFillDeps{
		KNN: knn, Store: store, EventLog: el,
		Reranker:  matching.NoopReranker{},
		Weights:   matching.DefaultWeights(),
		WeekCount: fixedWeekCount{n: 3},
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.MatchesWritten)
	require.Equal(t, matching.GapReasonWeeklyCap, res.Reason)
	require.Empty(t, store.ms)
}

func TestGapFill_NoInventoryReason(t *testing.T) {
	knn := &fakeRevKNN{hits: nil}
	store := &fakeStore{}
	el := &fakeEventLog{}
	res, err := matching.GapFill(context.Background(), matching.GapFillInput{
		CandidateID: "u",
		Embedding:   unitVec(1024, 0),
		Since:       time.Now().Add(-time.Hour),
		MinScore:    0.45,
	}, matching.GapFillDeps{
		KNN: knn, Store: store, EventLog: el,
		Reranker: matching.NoopReranker{},
		Weights:  matching.DefaultWeights(),
	})
	require.NoError(t, err)
	require.Equal(t, matching.GapReasonNoInventory, res.Reason)
	require.Equal(t, 0, res.MatchesWritten)
}
