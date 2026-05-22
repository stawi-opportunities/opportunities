//go:build integration

package matching_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestRulesStore_UpsertAndGet(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewRulesStore(db)

	doc := applications.DefaultRules()
	doc.MinScore = 0.7
	doc.Autoapply.Enabled = true
	doc.Autoapply.RequireMinScore = 0.85
	doc.Autoapply.Kinds = []string{"job"}
	out, err := s.Upsert(ctx, "cand_1", doc)
	require.NoError(t, err)
	require.Equal(t, 1, out.Version)
	require.True(t, out.Enabled)
	require.True(t, out.Autoapply)

	got, err := s.Get(ctx, "cand_1")
	require.NoError(t, err)
	require.InDelta(t, 0.7, got.Document.MinScore, 1e-9)

	// Upsert again — version bumps.
	doc.MinScore = 0.75
	out2, err := s.Upsert(ctx, "cand_1", doc)
	require.NoError(t, err)
	require.Equal(t, 2, out2.Version)
}

func TestRulesStore_GetMissingReturnsErrNotFound(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewRulesStore(db)
	_, err := s.Get(ctx, "nobody")
	require.ErrorIs(t, err, matching.ErrNotFound)
}
