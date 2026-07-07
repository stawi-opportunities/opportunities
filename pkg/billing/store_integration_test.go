//go:build integration

package billing_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupStore(t *testing.T) (*billing.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyGreenfieldSchema(t, ctx, db)
	return billing.NewStore(db), ctx
}

func TestStore_CreateGetUpdateRoundTrip(t *testing.T) {
	s, ctx := setupStore(t)

	c := billing.Checkout{
		PromptID:    "chk_int_1",
		CandidateID: "cand_int_1",
		PlanID:      "pro",
		Route:       "POLAR",
		Status:      billing.StatusPending,
		AmountCents: 5000,
		Currency:    "USD",
		Country:     "US",
		RedirectURL: "https://pay/x",
	}
	require.NoError(t, s.Create(ctx, c))

	// Create is idempotent on prompt_id.
	require.NoError(t, s.Create(ctx, c))

	got, err := s.GetByPromptID(ctx, "chk_int_1")
	require.NoError(t, err)
	require.Equal(t, "cand_int_1", got.CandidateID)
	require.Equal(t, billing.StatusPending, got.Status)
	require.EqualValues(t, 5000, got.AmountCents)

	updated, err := s.UpdateStatus(ctx, "chk_int_1", billing.StatusPaid, "sub_99", "")
	require.NoError(t, err)
	require.Equal(t, billing.StatusPaid, updated.Status)
	require.Equal(t, "sub_99", updated.SubscriptionID)
}

func TestStore_ListPendingOldestFirst(t *testing.T) {
	s, ctx := setupStore(t)
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "p1", CandidateID: "c", PlanID: "starter", Status: billing.StatusPending}))
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "p2", CandidateID: "c", PlanID: "starter", Status: billing.StatusPending}))
	// Resolve one so it drops out of the pending sweep.
	_, err := s.UpdateStatus(ctx, "p2", billing.StatusPaid, "", "")
	require.NoError(t, err)

	pending, err := s.ListPending(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "p1", pending[0].PromptID)
}

func TestStore_GetMissingIsErrNotFound(t *testing.T) {
	s, ctx := setupStore(t)
	_, err := s.GetByPromptID(ctx, "nope")
	require.ErrorIs(t, err, billing.ErrNotFound)
}
