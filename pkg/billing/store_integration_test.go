//go:build integration

package billing_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupStore(t *testing.T) (*billing.Store, context.Context) {
	s, _, ctx := setupStoreDB(t)
	return s, ctx
}

func setupStoreDB(t *testing.T) (*billing.Store, *sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyGreenfieldSchema(t, ctx, db)
	return billing.NewStore(db), db, ctx
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

	updated, changed, err := s.ResolveStatus(ctx, "chk_int_1", billing.StatusPaid, "sub_99", "")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, billing.StatusPaid, updated.Status)
	require.Equal(t, "sub_99", updated.SubscriptionID)
}

func TestStore_ListPendingOldestFirst(t *testing.T) {
	s, ctx := setupStore(t)
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "p1", CandidateID: "c", PlanID: "starter", Status: billing.StatusPending}))
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "p2", CandidateID: "c", PlanID: "starter", Status: billing.StatusPending}))
	// Resolve one so it drops out of the pending sweep.
	_, _, err := s.ResolveStatus(ctx, "p2", billing.StatusPaid, "", "")
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

func TestStore_ResolveStatusIsForwardOnly(t *testing.T) {
	s, ctx := setupStore(t)
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "f1", CandidateID: "c", PlanID: "pro"}))

	_, changed, err := s.ResolveStatus(ctx, "f1", billing.StatusPaid, "sub", "")
	require.NoError(t, err)
	require.True(t, changed)

	// Replayed PAID and late FAILED leave the paid row untouched.
	for _, st := range []billing.Status{billing.StatusPaid, billing.StatusFailed, billing.StatusPending} {
		row, changed, err := s.ResolveStatus(ctx, "f1", st, "", "late")
		require.NoError(t, err)
		require.False(t, changed, "status %s", st)
		require.Equal(t, billing.StatusPaid, row.Status)
		require.Empty(t, row.Error)
	}

	// failed -> paid is allowed (late successful charge).
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "f2", CandidateID: "c", PlanID: "pro"}))
	_, _, err = s.ResolveStatus(ctx, "f2", billing.StatusFailed, "", "declined")
	require.NoError(t, err)
	row, changed, err := s.ResolveStatus(ctx, "f2", billing.StatusPaid, "sub", "")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, billing.StatusPaid, row.Status)

	_, _, err = s.ResolveStatus(ctx, "missing", billing.StatusPaid, "", "")
	require.ErrorIs(t, err, billing.ErrNotFound)
}

func TestStore_ActivationTracking(t *testing.T) {
	s, ctx := setupStore(t)
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "a1", CandidateID: "c", PlanID: "pro"}))
	_, _, err := s.ResolveStatus(ctx, "a1", billing.StatusPaid, "", "")
	require.NoError(t, err)

	un, err := s.ListPaidUnactivated(ctx, 10)
	require.NoError(t, err)
	require.Len(t, un, 1)
	require.Nil(t, un[0].ActivatedAt)

	require.NoError(t, s.MarkActivated(ctx, "a1"))
	require.NoError(t, s.MarkActivated(ctx, "a1"))
	got, err := s.GetByPromptID(ctx, "a1")
	require.NoError(t, err)
	require.NotNil(t, got.ActivatedAt)

	un, err = s.ListPaidUnactivated(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, un)
}

// Rows paid before activated_at existed must not look unactivated, or the
// reconciler would re-grant historical (possibly expired) subscriptions.
func TestMigration_BackfillsActivatedAtForExistingPaidRows(t *testing.T) {
	s, db, ctx := setupStoreDB(t)
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "old", CandidateID: "c", PlanID: "pro"}))
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "pend", CandidateID: "c", PlanID: "pro"}))
	_, err := db.ExecContext(ctx, `UPDATE candidate_checkouts SET status='paid', activated_at=NULL WHERE prompt_id='old'`)
	require.NoError(t, err)

	_, file, _, _ := runtime.Caller(0)
	sqlPath := filepath.Join(filepath.Dir(file), "..", "..", "apps", "matching", "migrations", "0001",
		"20261122_0033_checkout_activated_at.sql")
	body, err := os.ReadFile(sqlPath)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	un, err := s.ListPaidUnactivated(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, un)
	pend, err := s.GetByPromptID(ctx, "pend")
	require.NoError(t, err)
	require.Nil(t, pend.ActivatedAt)
}
