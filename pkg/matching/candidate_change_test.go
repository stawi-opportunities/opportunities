package matching_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeDebouncer struct{ allow bool }

func (f *fakeDebouncer) Acquire(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return f.allow, nil
}

func TestRunCandidateChange_DebouncedReturnsErrDebounced(t *testing.T) {
	_, err := matching.RunCandidateChange(context.Background(),
		matching.CandidateChange{CandidateID: "u"},
		matching.CandidateChangeDeps{
			Debouncer:   &fakeDebouncer{allow: false},
			DebounceTTL: time.Second,
			GapFill: matching.GapFillDeps{
				KNN:      &fakeRevKNN{},
				Store:    &fakeStore{},
				EventLog: &fakeEventLog{},
				Reranker: matching.NoopReranker{},
				Weights:  matching.DefaultWeights(),
			},
		})
	require.True(t, errors.Is(err, matching.ErrDebounced))
}

func TestRunCandidateChange_AllowedRunsGapFill(t *testing.T) {
	knn := &fakeRevKNN{hits: []matching.OppHit{
		{OpportunityID: "o1", Distance: 0.2, FirstSeenAt: time.Now()},
	}}
	res, err := matching.RunCandidateChange(context.Background(),
		matching.CandidateChange{
			CandidateID: "u",
			Embedding:   unitVec(1024, 0),
			MinScore:    0.3,
		},
		matching.CandidateChangeDeps{
			Debouncer:   &fakeDebouncer{allow: true},
			DebounceTTL: time.Second,
			GapFill: matching.GapFillDeps{
				KNN: knn, Store: &fakeStore{}, EventLog: &fakeEventLog{},
				Reranker: matching.NoopReranker{},
				Weights:  matching.DefaultWeights(),
			},
		})
	require.NoError(t, err)
	require.Equal(t, 1, res.MatchesWritten)
}
