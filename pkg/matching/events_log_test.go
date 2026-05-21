//go:build integration

package matching_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestEventLog_WriteMatchEvent(t *testing.T) {
	db, ctx := setupStoreDB(t)
	el := matching.NewEventLog(db)

	rerank := 0.78
	err := el.WriteMatchEvent(ctx, matching.MatchEvent{
		EventID:       "evt_001",
		CandidateID:   "c1",
		OpportunityID: "o1",
		CanonicalID:   "c-1",
		Kind:          matching.EventKindGenerated,
		Path:          matching.PathFanout,
		Score:         0.81,
		RerankScore:   &rerank,
		RerankerUsed:  true,
		Data:          map[string]any{"rules_version": 4},
		OccurredAt:    time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	var cnt int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM candidate_match_events WHERE event_id='evt_001'`).Scan(&cnt))
	require.Equal(t, 1, cnt)
}

func TestEventLog_WriteMatchRunEvent(t *testing.T) {
	db, ctx := setupStoreDB(t)
	el := matching.NewEventLog(db)

	err := el.WriteMatchRunEvent(ctx, matching.MatchRunEvent{
		RunID:             "run_001",
		Path:              matching.PathFanout,
		TriggeredBy:       "canonical_upserted",
		CanonicalID:       "c-1",
		CandidatesScanned: 487,
		MatchesWritten:    34,
		Status:            "ok",
		RerankerStatus:    "used",
		LatencyMS:         142,
		StartedAt:         time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	var cnt int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM match_run_events WHERE run_id='run_001'`).Scan(&cnt))
	require.Equal(t, 1, cnt)
}
