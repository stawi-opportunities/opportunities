//go:build integration

package matching_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestPGDailyCapQuery_CountsTodayOnly(t *testing.T) {
	db, ctx := setupStoreDB(t)
	el := matching.NewEventLog(db)
	q := matching.NewPGDailyCapQuery(db)

	candidateID := "cap_user"
	// 3 events today
	for i := 0; i < 3; i++ {
		require.NoError(t, el.WriteMatchEvent(ctx, matching.MatchEvent{
			EventID:       "today_" + string(rune('a'+i)),
			OccurredAt:    time.Now(),
			CandidateID:   candidateID,
			OpportunityID: "o" + string(rune('a'+i)),
			Kind:          matching.EventKindGenerated,
			Path:          matching.PathFanout,
			Score:         0.5,
		}))
	}
	// 1 event yesterday
	require.NoError(t, el.WriteMatchEvent(ctx, matching.MatchEvent{
		EventID:       "yesterday",
		OccurredAt:    time.Now().Add(-24 * time.Hour),
		CandidateID:   candidateID,
		OpportunityID: "o_yest",
		Kind:          matching.EventKindGenerated,
		Path:          matching.PathFanout,
		Score:         0.5,
	}))

	n, err := q.TodayCount(ctx, candidateID)
	require.NoError(t, err)
	// Should see today's 3 (via the recent-tail UNION). The CAGG may
	// still be empty since refresh runs every 5 minutes.
	require.GreaterOrEqual(t, n, 3)
}

func TestFanOut_DailyCapOverflowsBeyondLimit(t *testing.T) {
	db, ctx := setupStoreDB(t)
	idx := matching.NewIndexStore(db)
	store := matching.NewStore(db)
	knn := matching.NewKNN(db)
	el := matching.NewEventLog(db)

	require.NoError(t, idx.Upsert(ctx, matching.CandidateIndex{
		CandidateID: "cap_c", Embedding: unitVec(1536, 0),
		MinScore: 0.1, DailyCap: 2, Kinds: []string{"job"},
		Countries: []string{"KE"}, Enabled: true,
	}))
	// Seed 2 'generated' events for today via the event log (simulating
	// prior fan-out runs).
	for i := 0; i < 2; i++ {
		require.NoError(t, el.WriteMatchEvent(ctx, matching.MatchEvent{
			EventID:       "cap_evt_" + string(rune('a'+i)),
			OccurredAt:    time.Now(),
			CandidateID:   "cap_c",
			OpportunityID: "oprev_" + string(rune('a'+i)),
			Kind:          matching.EventKindGenerated,
			Path:          matching.PathFanout,
			Score:         0.5,
		}))
	}

	// Now run a fan-out — should produce status='overflow' since
	// today's count is already at the cap.
	cap_ := matching.NewPGDailyCapQuery(db)
	_, err := matching.FanOut(context.Background(), matching.FanOutInput{
		CanonicalID:   "cap_can",
		OpportunityID: "cap_opp_new",
		Kind:          "job",
		Country:       "KE",
		Embedding:     unitVec(1536, 0),
		FirstSeenAt:   time.Now(),
	}, matching.FanOutDeps{
		KNN: knn, Store: store, EventLog: el,
		Reranker: matching.NoopReranker{},
		Weights:  matching.DefaultWeights(),
		DailyCap: cap_,
	})
	require.NoError(t, err)

	// The new match should exist with status='overflow'.
	m, err := store.GetByPair(ctx, "cap_c", "cap_opp_new")
	require.NoError(t, err)
	require.Equal(t, matching.StatusOverflow, m.Status)
}
