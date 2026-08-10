package matching_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeInvokeCounter struct {
	used int
	err  error
}

func (f *fakeInvokeCounter) CountUserInvokesToday(_ context.Context, _ string, _ time.Time) (int, error) {
	return f.used, f.err
}

// countingRevKNN records ReverseKNN calls so rate-limit tests can assert KNN was skipped.
type countingRevKNN struct {
	hits  []matching.OppHit
	calls int
}

func (f *countingRevKNN) ReverseKNN(_ context.Context, _ matching.ReverseKNNParams) ([]matching.OppHit, error) {
	f.calls++
	return f.hits, nil
}

func fiveHighHits() []matching.OppHit {
	now := time.Now()
	hits := make([]matching.OppHit, 5)
	for i := range hits {
		hits[i] = matching.OppHit{
			OpportunityID: fmt.Sprintf("o-%d", i),
			Distance:      0.1,
			FirstSeenAt:   now,
		}
	}
	return hits
}

func TestMatchInvoke_ForcesZeroRowCaps(t *testing.T) {
	t.Parallel()
	knn := &fakeRevKNN{hits: fiveHighHits()}
	store := &fakeStore{}
	el := &fakeEventLog{}
	counter := &fakeInvokeCounter{used: 0}

	res, err := matching.MatchInvoke(context.Background(), matching.InvokeInput{
		CandidateID: "u",
		Embedding:   unitVec(1024, 0),
		MinScore:    0.45,
		Reason:      matching.InvokeUserRefresh,
		InvokeLimit: 100,
	}, matching.InvokeDeps{
		GapFill: matching.GapFillDeps{
			KNN: knn, Store: store, EventLog: el,
			Reranker: matching.NoopReranker{},
			Weights:  matching.DefaultWeights(),
		},
		Invokes: counter,
	})
	require.NoError(t, err)
	require.Equal(t, matching.GapReasonOK, res.Reason)
	require.Equal(t, 5, res.MatchesWritten)
	require.Len(t, store.ms, 5)
	for _, m := range store.ms {
		require.Equal(t, matching.StatusNew, m.Status, "expected no overflow under zero row caps")
	}
	require.Equal(t, matching.InvokeUserRefresh, el.runEvents[0].TriggeredBy)
}

func TestMatchInvoke_RateLimited(t *testing.T) {
	t.Parallel()
	knn := &countingRevKNN{hits: fiveHighHits()}
	store := &fakeStore{}
	el := &fakeEventLog{}
	counter := &fakeInvokeCounter{used: 1}

	res, err := matching.MatchInvoke(context.Background(), matching.InvokeInput{
		CandidateID: "u",
		Embedding:   unitVec(1024, 0),
		MinScore:    0.45,
		Reason:      matching.InvokeUserRefresh,
		InvokeLimit: 1,
	}, matching.InvokeDeps{
		GapFill: matching.GapFillDeps{
			KNN: knn, Store: store, EventLog: el,
			Reranker: matching.NoopReranker{},
			Weights:  matching.DefaultWeights(),
		},
		Invokes: counter,
	})
	require.NoError(t, err)
	require.Equal(t, matching.GapReasonRateLimited, res.Reason)
	require.Equal(t, 0, res.MatchesWritten)
	require.Empty(t, store.ms)
	require.Equal(t, 0, knn.calls, "KNN must not run when rate limited")
	require.Empty(t, el.runEvents)
}

func TestMatchInvoke_DigestSkipsRateLimit(t *testing.T) {
	t.Parallel()
	knn := &fakeRevKNN{hits: fiveHighHits()}
	store := &fakeStore{}
	el := &fakeEventLog{}
	counter := &fakeInvokeCounter{used: 100}

	res, err := matching.MatchInvoke(context.Background(), matching.InvokeInput{
		CandidateID: "u",
		Embedding:   unitVec(1024, 0),
		MinScore:    0.45,
		Reason:      matching.InvokeDigest,
		InvokeLimit: 1,
	}, matching.InvokeDeps{
		GapFill: matching.GapFillDeps{
			KNN: knn, Store: store, EventLog: el,
			Reranker: matching.NoopReranker{},
			Weights:  matching.DefaultWeights(),
		},
		Invokes: counter,
	})
	require.NoError(t, err)
	require.Equal(t, matching.GapReasonOK, res.Reason)
	require.Equal(t, 5, res.MatchesWritten)
	require.Len(t, store.ms, 5)
	require.Equal(t, matching.InvokeDigest, el.runEvents[0].TriggeredBy)
}

func TestMatchInvoke_SemanticFirstKNNParams(t *testing.T) {
	t.Parallel()
	knn := &fakeRevKNN{hits: fiveHighHits()}
	store := &fakeStore{}
	el := &fakeEventLog{}

	_, err := matching.MatchInvoke(context.Background(), matching.InvokeInput{
		CandidateID: "u",
		Embedding:   unitVec(1024, 0),
		Countries:   []string{"KE", "UG"},
		Kinds:       []string{"job"},
		MinScore:    0.70,
		Reason:      matching.InvokeUserRefresh,
	}, matching.InvokeDeps{
		GapFill: matching.GapFillDeps{
			KNN: knn, Store: store, EventLog: el,
			Reranker: matching.NoopReranker{},
			Weights:  matching.DefaultWeights(),
		},
	})
	require.NoError(t, err)
	// Soft geo: countries passed for score-time GeoMatch, not hard SQL.
	require.False(t, knn.last.HardCountries)
	require.Equal(t, []string{"KE", "UG"}, knn.last.Countries)
	require.Equal(t, matching.DefaultReverseKNNLimit, knn.last.Limit)
	require.InDelta(t, matching.DefaultSemanticMaxDistance, knn.last.MaxDistance, 1e-9)
	require.GreaterOrEqual(t, len(store.ms), 1)
}
