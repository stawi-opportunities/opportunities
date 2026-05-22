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

func TestStore_ListByCandidate_Pagination(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewStore(db)

	rows := []matching.Match{
		{MatchID: "p1", CandidateID: "u", OpportunityID: "o1", Status: matching.StatusNew, Score: 0.9, LastEventID: "e"},
		{MatchID: "p2", CandidateID: "u", OpportunityID: "o2", Status: matching.StatusViewed, Score: 0.8, LastEventID: "e"},
		{MatchID: "p3", CandidateID: "u", OpportunityID: "o3", Status: matching.StatusApplying, Score: 0.7, LastEventID: "e"},
		{MatchID: "p4", CandidateID: "u", OpportunityID: "o4", Status: matching.StatusDismissed, Score: 0.6, LastEventID: "e"},
		{MatchID: "p5", CandidateID: "u", OpportunityID: "o5", Status: matching.StatusOverflow, Score: 0.5, LastEventID: "e"},
	}
	for _, m := range rows {
		_, err := s.UpsertMatch(ctx, m)
		require.NoError(t, err)
	}

	// Default status filter ([new, viewed, applying]) excludes dismissed/overflow.
	page, err := s.ListByCandidate(ctx, matching.ListByCandidateParams{
		CandidateID: "u",
		Statuses:    []matching.MatchStatus{matching.StatusNew, matching.StatusViewed, matching.StatusApplying},
		Limit:       10,
	})
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	require.Equal(t, "p1", page.Items[0].MatchID)
	require.Equal(t, "p2", page.Items[1].MatchID)
	require.Equal(t, "p3", page.Items[2].MatchID)
	require.False(t, page.HasMore)

	// Pagination: limit=2 → page 1 + cursor.
	page1, err := s.ListByCandidate(ctx, matching.ListByCandidateParams{
		CandidateID: "u",
		Statuses:    []matching.MatchStatus{matching.StatusNew, matching.StatusViewed, matching.StatusApplying},
		Limit:       2,
	})
	require.NoError(t, err)
	require.Len(t, page1.Items, 2)
	require.True(t, page1.HasMore)
	require.NotEmpty(t, page1.NextCursor)

	// Page 2 picks up from cursor.
	page2, err := s.ListByCandidate(ctx, matching.ListByCandidateParams{
		CandidateID: "u",
		Statuses:    []matching.MatchStatus{matching.StatusNew, matching.StatusViewed, matching.StatusApplying},
		Cursor:      page1.NextCursor,
		Limit:       2,
	})
	require.NoError(t, err)
	require.Len(t, page2.Items, 1)
	require.False(t, page2.HasMore)
	// And the page-2 item is the third in the ordered list:
	require.Equal(t, "p3", page2.Items[0].MatchID)
}

func TestStore_SubscriptionSummary_CountsByStatusAndAge(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewStore(db)

	// Two candidates so the WHERE candidate_id = $1 filter is actually
	// exercised (a regression in the filter would otherwise count both).
	const candA = "cand_sub_a"
	const candB = "cand_sub_b"

	// Seed candA: 3 new (all queued), 2 within-week delivered (viewed/applied),
	// 1 within-week dismissed (counted as delivered), 1 within-week overflow
	// (NOT counted — cap-suppressed), 1 nine-day-old viewed (NOT counted —
	// outside the 7-day window).
	seed := []struct {
		matchID string
		status  matching.MatchStatus
		ageDays int
	}{
		{"sub_new_1", matching.StatusNew, 0},
		{"sub_new_2", matching.StatusNew, 0},
		{"sub_new_3", matching.StatusNew, 0},
		{"sub_view_1", matching.StatusViewed, 1},
		{"sub_apply_1", matching.StatusApplied, 2},
		{"sub_dism_1", matching.StatusDismissed, 3},
		{"sub_overflow", matching.StatusOverflow, 1},
		{"sub_old_view", matching.StatusViewed, 9},
	}
	for i, row := range seed {
		// Insert directly so we can control created_at (UpsertMatch
		// always uses NOW(), which would land everything inside the
		// window and hide the boundary-condition bug we want to guard).
		_, err := db.ExecContext(ctx, `
INSERT INTO candidate_matches
  (match_id, candidate_id, opportunity_id, status, score, last_event_id,
   metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, '{}'::jsonb,
        NOW() - make_interval(days => $7),
        NOW() - make_interval(days => $7))
`,
			row.matchID, candA, "opp_"+row.matchID,
			string(row.status), 0.5, "evt_"+row.matchID, row.ageDays)
		require.NoError(t, err, "seed row %d", i)
	}

	// Seed candB with a single new row — must NOT appear in candA's counts.
	_, err := db.ExecContext(ctx, `
INSERT INTO candidate_matches
  (match_id, candidate_id, opportunity_id, status, score, last_event_id, metadata, created_at, updated_at)
VALUES ('sub_other_new', $1, 'opp_other', 'new', 0.5, 'evt_other', '{}'::jsonb, NOW(), NOW())
`, candB)
	require.NoError(t, err)

	queued, delivered, err := s.SubscriptionSummary(ctx, candA)
	require.NoError(t, err)
	require.Equal(t, 3, queued, "queued should count only status='new' rows for candA")
	require.Equal(t, 3, delivered,
		"delivered_this_week should count viewed+applied+dismissed within 7 days; "+
			"overflow and 9-day-old rows must be excluded")

	// candB returns its own isolated counts.
	queuedB, deliveredB, err := s.SubscriptionSummary(ctx, candB)
	require.NoError(t, err)
	require.Equal(t, 1, queuedB)
	require.Equal(t, 0, deliveredB)

	// Unknown candidate yields zeros, not an error.
	queuedX, deliveredX, err := s.SubscriptionSummary(ctx, "cand_nonexistent")
	require.NoError(t, err)
	require.Equal(t, 0, queuedX)
	require.Equal(t, 0, deliveredX)
}
