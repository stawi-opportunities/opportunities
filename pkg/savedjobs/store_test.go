//go:build integration

package savedjobs_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/savedjobs"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setup(t *testing.T) (*savedjobs.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyGreenfieldSchema(t, ctx, db)
	return savedjobs.NewStore(db), ctx
}

func TestStore_StarUnstarRoundTrip(t *testing.T) {
	s, ctx := setup(t)
	require.NoError(t, s.Star(ctx, "cand_a", "opp_1"))
	require.NoError(t, s.Star(ctx, "cand_a", "opp_2"))
	require.NoError(t, s.Star(ctx, "cand_b", "opp_1"))

	ids, err := s.ListByCandidate(ctx, "cand_a")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"opp_1", "opp_2"}, ids)

	require.NoError(t, s.Unstar(ctx, "cand_a", "opp_1"))
	ids, err = s.ListByCandidate(ctx, "cand_a")
	require.NoError(t, err)
	require.Equal(t, []string{"opp_2"}, ids)
}

func TestStore_StarIsIdempotent(t *testing.T) {
	s, ctx := setup(t)
	require.NoError(t, s.Star(ctx, "cand_c", "opp_x"))
	// Same pair again — no error (ON CONFLICT DO NOTHING).
	require.NoError(t, s.Star(ctx, "cand_c", "opp_x"))
	ids, err := s.ListByCandidate(ctx, "cand_c")
	require.NoError(t, err)
	require.Equal(t, []string{"opp_x"}, ids)
}

func TestStore_UnstarUnknownPairIsNoop(t *testing.T) {
	s, ctx := setup(t)
	require.NoError(t, s.Unstar(ctx, "cand_d", "opp_never_starred"))
}
