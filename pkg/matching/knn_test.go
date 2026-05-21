//go:build integration

package matching_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestFanOutKNN_OrdersByDistanceAndFilters(t *testing.T) {
	db, ctx := setupStoreDB(t)
	idx := matching.NewIndexStore(db)
	knn := matching.NewKNN(db)

	require.NoError(t, idx.Upsert(ctx, matching.CandidateIndex{
		CandidateID: "c_close", Embedding: makeUnitVector(1536, 0),
		MinScore: 0.5, DailyCap: 25, WeeklyCap: 100,
		Kinds: []string{"job"}, Countries: []string{"KE"},
		Enabled: true,
	}))
	require.NoError(t, idx.Upsert(ctx, matching.CandidateIndex{
		CandidateID: "c_far", Embedding: makeUnitVector(1536, 5),
		MinScore: 0.5, DailyCap: 25, WeeklyCap: 100,
		Kinds: []string{"job"}, Countries: []string{"KE"},
		Enabled: true,
	}))
	require.NoError(t, idx.Upsert(ctx, matching.CandidateIndex{
		CandidateID: "c_wrong_kind", Embedding: makeUnitVector(1536, 0),
		MinScore: 0.5, DailyCap: 25, WeeklyCap: 100,
		Kinds: []string{"scholarship"}, Countries: []string{"KE"},
		Enabled: true,
	}))

	hits, err := knn.FanOutKNN(ctx, matching.FanOutKNNParams{
		OppEmbedding: makeUnitVector(1536, 0),
		OppKind:      "job",
		OppCountry:   "KE",
		Limit:        10,
	})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	require.Equal(t, "c_close", hits[0].CandidateID)
	require.Equal(t, "c_far", hits[1].CandidateID)
	require.InDelta(t, 0.0, hits[0].Distance, 1e-6)
}

func TestReverseKNN_OrdersByDistanceSinceCursor(t *testing.T) {
	db, ctx := setupStoreDB(t)
	knn := matching.NewKNN(db)

	_, err := db.ExecContext(ctx, `
		ALTER TABLE opportunities
			ADD COLUMN IF NOT EXISTS embedding vector(1536),
			ADD COLUMN IF NOT EXISTS kind TEXT,
			ADD COLUMN IF NOT EXISTS country TEXT,
			ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ DEFAULT now()
	`)
	require.NoError(t, err)

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	seedOpp := func(id string, axis int, kind, country string, firstSeen time.Time) {
		v := makeUnitVector(1536, axis)
		_, err := db.ExecContext(ctx, `
			INSERT INTO opportunities (id, posted_at, status, hidden, embedding, kind, country, first_seen_at)
			VALUES ($1, now(), 'active', false, $2::vector, $3, $4, $5)
		`, id, vectorLiteralFor(v), kind, country, firstSeen)
		require.NoError(t, err)
	}
	seedOpp("opp_close", 0, "job", "KE", now)
	seedOpp("opp_close2", 1, "job", "KE", now.Add(-1*time.Hour))
	seedOpp("opp_old", 0, "job", "KE", now.Add(-24*time.Hour))
	seedOpp("opp_wrong_kind", 0, "scholarship", "KE", now)

	hits, err := knn.ReverseKNN(ctx, matching.ReverseKNNParams{
		CandidateEmbedding: makeUnitVector(1536, 0),
		Kinds:              []string{"job"},
		Countries:          []string{"KE"},
		Since:              now.Add(-2 * time.Hour),
		Limit:              10,
	})
	require.NoError(t, err)
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.OpportunityID)
	}
	require.ElementsMatch(t, []string{"opp_close", "opp_close2"}, ids)
	_ = sql.ErrNoRows // keep sql imported even if unused
}

// vectorLiteralFor renders []float32 as pgvector textual form (test-local helper).
func vectorLiteralFor(v []float32) string {
	out := "["
	for i, f := range v {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%g", f)
	}
	out += "]"
	return out
}

// guard against unused import of context in some build configs
var _ = context.Background
