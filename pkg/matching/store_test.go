//go:build integration

package matching_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupStoreDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, db))
	testhelpers.ApplyMigrationsDir(t, ctx, db, "../../db/migrations")
	return db, ctx
}

func TestStore_UpsertMatch_InsertsNewRow(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewStore(db)

	m := matching.Match{
		MatchID:       "m_001",
		CandidateID:   "cand_001",
		OpportunityID: "opp_001",
		Status:        matching.StatusNew,
		Score:         0.82,
		LastEventID:   "evt_001",
		Metadata:      map[string]any{"path": "fanout"},
	}
	created, err := s.UpsertMatch(ctx, m)
	require.NoError(t, err)
	require.True(t, created)

	got, err := s.GetByPair(ctx, "cand_001", "opp_001")
	require.NoError(t, err)
	require.Equal(t, "m_001", got.MatchID)
	require.InDelta(t, 0.82, got.Score, 1e-9)
	require.Equal(t, matching.StatusNew, got.Status)
}

func TestStore_UpsertMatch_IsIdempotent(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewStore(db)

	m := matching.Match{
		MatchID: "m_002", CandidateID: "cand_002", OpportunityID: "opp_002",
		Status: matching.StatusNew, Score: 0.7, LastEventID: "evt_a",
	}
	_, err := s.UpsertMatch(ctx, m)
	require.NoError(t, err)
	_, err = s.UpsertMatch(ctx, m) // exact replay
	require.NoError(t, err)

	got, err := s.GetByPair(ctx, "cand_002", "opp_002")
	require.NoError(t, err)
	require.InDelta(t, 0.7, got.Score, 1e-9)
}

func TestStore_UpsertMatch_MonotonicScore(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewStore(db)

	base := matching.Match{
		MatchID: "m_003", CandidateID: "c", OpportunityID: "o",
		Status: matching.StatusNew, Score: 0.6, LastEventID: "evt_1",
	}
	_, _ = s.UpsertMatch(ctx, base)

	// Lower score should NOT downgrade the row.
	lower := base
	lower.Score = 0.4
	lower.LastEventID = "evt_2"
	_, err := s.UpsertMatch(ctx, lower)
	require.NoError(t, err)
	got, _ := s.GetByPair(ctx, "c", "o")
	require.InDelta(t, 0.6, got.Score, 1e-9)

	// Higher score should win.
	higher := base
	higher.Score = 0.9
	higher.LastEventID = "evt_3"
	_, err = s.UpsertMatch(ctx, higher)
	require.NoError(t, err)
	got, _ = s.GetByPair(ctx, "c", "o")
	require.InDelta(t, 0.9, got.Score, 1e-9)
	require.Equal(t, "evt_3", got.LastEventID)
}

func TestStore_UpsertMatch_TerminalProtected(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewStore(db)

	_, _ = s.UpsertMatch(ctx, matching.Match{
		MatchID: "m_004", CandidateID: "c", OpportunityID: "o",
		Status: matching.StatusNew, Score: 0.5, LastEventID: "e1",
	})

	// Transition to terminal via a direct UPDATE (mirrors what
	// /api/me/matches/{id}/dismiss does in Phase 4).
	_, err := db.ExecContext(ctx,
		`UPDATE candidate_matches
         SET status='dismissed', dismissed_at=now()
         WHERE candidate_id='c' AND opportunity_id='o'`)
	require.NoError(t, err)

	// A higher-scoring fan-out replay must NOT resurrect the row.
	_, err = s.UpsertMatch(ctx, matching.Match{
		MatchID: "m_004", CandidateID: "c", OpportunityID: "o",
		Status: matching.StatusNew, Score: 0.95, LastEventID: "e2",
	})
	require.NoError(t, err)

	got, _ := s.GetByPair(ctx, "c", "o")
	require.Equal(t, matching.StatusDismissed, got.Status)
	require.InDelta(t, 0.5, got.Score, 1e-9) // score preserved
}

func TestStore_UpsertMatches_Bulk(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewStore(db)

	ms := []matching.Match{
		{MatchID: "b1", CandidateID: "c", OpportunityID: "o1", Status: matching.StatusNew, Score: 0.71, LastEventID: "e"},
		{MatchID: "b2", CandidateID: "c", OpportunityID: "o2", Status: matching.StatusNew, Score: 0.66, LastEventID: "e"},
		{MatchID: "b3", CandidateID: "c", OpportunityID: "o3", Status: matching.StatusNew, Score: 0.51, LastEventID: "e"},
	}
	require.NoError(t, s.UpsertMatches(ctx, ms))

	var cnt int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM candidate_matches WHERE candidate_id='c'`).Scan(&cnt))
	require.Equal(t, 3, cnt)
}
