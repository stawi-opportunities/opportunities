//go:build integration

package matching_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestIndexStore_UpsertAndGet(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewIndexStore(db)

	floor := 30000
	err := s.Upsert(ctx, matching.CandidateIndex{
		CandidateID:    "c1",
		Embedding:      makeUnitVector(1536, 0),
		MinScore:       0.62,
		DailyCap:       25,
		WeeklyCap:      100,
		Kinds:          []string{"job", "scholarship"},
		Countries:      []string{"KE", "UG", "remote"},
		SalaryFloorUSD: &floor,
		RemoteOnly:     false,
		Enabled:        true,
	})
	require.NoError(t, err)

	got, err := s.Get(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, "c1", got.CandidateID)
	require.Equal(t, 1536, len(got.Embedding))
	require.InDelta(t, 0.62, got.MinScore, 1e-9)
	require.ElementsMatch(t, []string{"job", "scholarship"}, got.Kinds)
}

func makeUnitVector(dim, axis int) []float32 {
	v := make([]float32, dim)
	if axis >= 0 && axis < dim {
		v[axis] = 1
	} else {
		v[0] = 1
	}
	return v
}
