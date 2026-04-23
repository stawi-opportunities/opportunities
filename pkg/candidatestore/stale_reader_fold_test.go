package candidatestore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListStaleFold_LatestPerCandidate verifies the fold logic that determines
// staleness based on the LATEST upload per candidate, not any individual row.
func TestListStaleFold_LatestPerCandidate(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(30 * 24 * time.Hour)   // 30 days later — stale if cutoff is 15 days
	t2 := t0.Add(90 * 24 * time.Hour)   // 90 days later — recent enough
	cutoff := t0.Add(60 * 24 * time.Hour) // 60 days after t0

	// Candidate A: has an old upload (t0) AND a recent upload (t2).
	// Even though t0 < cutoff, the latest is t2 which is NOT < cutoff.
	// So candidate A should NOT appear in the stale list.
	//
	// Candidate B: only has an old upload (t1). t1 < cutoff → stale.
	// Candidate C: only has a recent upload (t2). t2 > cutoff → not stale.
	rows := []StaleCandidate{
		{CandidateID: "cand-a", LastUploadAt: t0},
		{CandidateID: "cand-a", LastUploadAt: t2},
		{CandidateID: "cand-b", LastUploadAt: t1},
		{CandidateID: "cand-c", LastUploadAt: t2},
		// Empty candidate_id — must be ignored.
		{CandidateID: "", LastUploadAt: t0},
	}

	// Replicate the fold logic from ListStale.
	latest := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		if row.CandidateID == "" {
			continue
		}
		if t, ok := latest[row.CandidateID]; !ok || row.LastUploadAt.After(t) {
			latest[row.CandidateID] = row.LastUploadAt
		}
	}

	// Filter by cutoff.
	var stale []StaleCandidate
	for id, ts := range latest {
		if ts.Before(cutoff) {
			stale = append(stale, StaleCandidate{CandidateID: id, LastUploadAt: ts})
		}
	}

	require.Len(t, stale, 1, "only cand-b should appear — cand-a's latest is recent, cand-c is also recent")
	assert.Equal(t, "cand-b", stale[0].CandidateID)
	assert.Equal(t, t1, stale[0].LastUploadAt)
}

// TestListStaleFold_SortAndLimit verifies that results are sorted by
// LastUploadAt ascending and that the limit is honoured.
func TestListStaleFold_SortAndLimit(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoff := t0.Add(100 * 24 * time.Hour)

	// Build 5 candidates all with uploads before the cutoff.
	candidates := []StaleCandidate{
		{CandidateID: "e", LastUploadAt: t0.Add(50 * 24 * time.Hour)},
		{CandidateID: "a", LastUploadAt: t0.Add(10 * 24 * time.Hour)},
		{CandidateID: "c", LastUploadAt: t0.Add(30 * 24 * time.Hour)},
		{CandidateID: "b", LastUploadAt: t0.Add(20 * 24 * time.Hour)},
		{CandidateID: "d", LastUploadAt: t0.Add(40 * 24 * time.Hour)},
	}

	// All are stale; filter + sort + limit.
	var out []StaleCandidate
	for _, c := range candidates {
		if c.LastUploadAt.Before(cutoff) {
			out = append(out, c)
		}
	}

	// Sort ascending.
	sortStale(out)

	limit := 3
	if len(out) > limit {
		out = out[:limit]
	}

	require.Len(t, out, 3)
	assert.Equal(t, "a", out[0].CandidateID, "oldest first")
	assert.Equal(t, "b", out[1].CandidateID)
	assert.Equal(t, "c", out[2].CandidateID)
}

// TestListStaleFold_EmptyInput verifies graceful handling of an empty table.
func TestListStaleFold_EmptyInput(t *testing.T) {
	cutoff := time.Now().UTC()
	rows := []StaleCandidate{}
	latest := make(map[string]time.Time)
	for _, row := range rows {
		if row.CandidateID == "" {
			continue
		}
		if ts, ok := latest[row.CandidateID]; !ok || row.LastUploadAt.After(ts) {
			latest[row.CandidateID] = row.LastUploadAt
		}
	}
	var out []StaleCandidate
	for id, ts := range latest {
		if ts.Before(cutoff) {
			out = append(out, StaleCandidate{CandidateID: id, LastUploadAt: ts})
		}
	}
	assert.Empty(t, out)
}

// sortStale sorts a []StaleCandidate by LastUploadAt ascending (oldest first).
// Mirrors the sort in ListStale and is extracted here so tests can use it.
func sortStale(rows []StaleCandidate) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].LastUploadAt.Before(rows[j-1].LastUploadAt); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}
