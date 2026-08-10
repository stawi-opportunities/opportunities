package matching_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeRevKNN struct {
	hits []matching.OppHit
	last matching.ReverseKNNParams
}

func (f *fakeRevKNN) ReverseKNN(_ context.Context, p matching.ReverseKNNParams) ([]matching.OppHit, error) {
	f.last = p
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
		// Neutral non-cosine terms ≈ 0.18; high cosine clears MinScore,
		// pure-orthogonal neighbours (cos≈0) do not.
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

func TestGapFill_SemanticFirstDefaults(t *testing.T) {
	t.Parallel()
	knn := &fakeRevKNN{hits: nil}
	store := &fakeStore{}
	el := &fakeEventLog{}
	_, err := matching.GapFill(context.Background(), matching.GapFillInput{
		CandidateID: "u",
		Embedding:   unitVec(1024, 0),
		Countries:   []string{"KE"},
		Kinds:       []string{"job"},
		Since:       time.Now().Add(-time.Hour),
		MinScore:    0.70,
		// Leave SemanticRecall / MaxDistance / HardCountries zero → defaults.
	}, matching.GapFillDeps{
		KNN: knn, Store: store, EventLog: el,
		Reranker: matching.NoopReranker{},
		Weights:  matching.DefaultWeights(),
	})
	require.NoError(t, err)
	require.Equal(t, matching.DefaultReverseKNNLimit, knn.last.Limit)
	require.InDelta(t, matching.DefaultSemanticMaxDistance, knn.last.MaxDistance, 1e-9)
	require.False(t, knn.last.HardCountries, "geo must be soft by default")
	require.False(t, knn.last.SoftKinds)
	require.Equal(t, []string{"job"}, knn.last.Kinds)
	require.Equal(t, []string{"KE"}, knn.last.Countries)
}

func TestGapFill_SemanticOverrides(t *testing.T) {
	t.Parallel()
	knn := &fakeRevKNN{hits: nil}
	store := &fakeStore{}
	el := &fakeEventLog{}
	_, err := matching.GapFill(context.Background(), matching.GapFillInput{
		CandidateID:    "u",
		Embedding:      unitVec(1024, 0),
		Since:          time.Now().Add(-time.Hour),
		MinScore:       0.70,
		SemanticRecall: 400,
		MaxDistance:    0.75,
		HardCountries:  true,
		SoftKinds:      true,
	}, matching.GapFillDeps{
		KNN: knn, Store: store, EventLog: el,
		Reranker: matching.NoopReranker{},
		Weights:  matching.DefaultWeights(),
	})
	require.NoError(t, err)
	require.Equal(t, 400, knn.last.Limit)
	require.InDelta(t, 0.75, knn.last.MaxDistance, 1e-9)
	require.True(t, knn.last.HardCountries)
	require.True(t, knn.last.SoftKinds)
}

func TestGapFill_NegativeMaxDistanceDisablesCap(t *testing.T) {
	t.Parallel()
	knn := &fakeRevKNN{hits: nil}
	store := &fakeStore{}
	el := &fakeEventLog{}
	_, err := matching.GapFill(context.Background(), matching.GapFillInput{
		CandidateID: "u",
		Embedding:   unitVec(1024, 0),
		Since:       time.Now().Add(-time.Hour),
		MinScore:    0.70,
		MaxDistance: -1,
	}, matching.GapFillDeps{
		KNN: knn, Store: store, EventLog: el,
		Reranker: matching.NoopReranker{},
		Weights:  matching.DefaultWeights(),
	})
	require.NoError(t, err)
	require.Equal(t, 0.0, knn.last.MaxDistance, "negative MaxDistance → no SQL cap")
}
