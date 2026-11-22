//go:build integration

package billing_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	require.NoError(t, s.MarkActivated(ctx, "a1", billing.OutcomeApplied))
	require.NoError(t, s.MarkActivated(ctx, "a1", billing.OutcomeApplied))
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
	_, err := db.ExecContext(ctx, `UPDATE candidate_checkouts SET status='paid', activated_at=NULL, paid_at=NULL, activation_outcome='' WHERE prompt_id='old'`)
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
	old, err := s.GetByPromptID(ctx, "old")
	require.NoError(t, err)
	require.NotNil(t, old.PaidAt, "backfill approximates paid_at")
	var outcome string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT activation_outcome FROM candidate_checkouts WHERE prompt_id='old'`).Scan(&outcome))
	require.Equal(t, billing.OutcomeApplied, outcome)
	pend, err := s.GetByPromptID(ctx, "pend")
	require.NoError(t, err)
	require.Nil(t, pend.ActivatedAt)
}

func paidCheckout(t *testing.T, s *billing.Store, ctx context.Context, id, cand, plan string) {
	t.Helper()
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: id, CandidateID: cand, PlanID: plan}))
	_, _, err := s.ResolveStatus(ctx, id, billing.StatusPaid, "", "")
	require.NoError(t, err)
}

type atomicFulfiller struct{ calls atomic.Int32 }

func (f *atomicFulfiller) FulfillOneTime(context.Context, string, string, string) error {
	f.calls.Add(1)
	time.Sleep(50 * time.Millisecond) // a slow report: widen the race window
	return nil
}

type noSubs struct{}

func (noSubs) ActivateSubscription(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (noSubs) HoldsSubscription(context.Context, string, string) (bool, error) { return false, nil }

// The status poll, the webhook and the reconciler can all call Activate for
// the same payment at once; the report must be produced exactly once.
func TestActivator_ConcurrentActivationsFulfilOnce(t *testing.T) {
	s, ctx := setupStore(t)
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "race", CandidateID: "c", PlanID: string(billing.ProductATSReport)}))
	ful := &atomicFulfiller{}
	act := billing.NewActivator(s, noSubs{}).WithOneTimeFulfiller(ful)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, act.Activate(ctx, "race", billing.StatusPaid, "", ""))
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, ful.calls.Load())
	got, err := s.GetByPromptID(ctx, "race")
	require.NoError(t, err)
	require.NotNil(t, got.ActivatedAt)
}

func TestStore_ActivationClaimLeaseBackoffAndLimit(t *testing.T) {
	s, db, ctx := setupStoreDB(t)
	paidCheckout(t, s, ctx, "a", "c", "pro")

	claim, ok, err := s.ClaimActivation(ctx, "a")
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = s.ClaimActivation(ctx, "a")
	require.NoError(t, err)
	require.False(t, ok, "a live claim is exclusive")
	due, err := s.ListPaidUnactivated(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, due, "a claimed row is not offered to the reconciler")

	// A claim older than the lease (crashed activator) can be taken over,
	// and the old holder can then no longer release the new claim.
	_, err = db.ExecContext(ctx, `UPDATE candidate_checkouts SET activation_claimed_at = activation_claimed_at - interval '11 minutes' WHERE prompt_id='a'`)
	require.NoError(t, err)
	staleClaim := billing.ActivationClaim{At: claim.At.Add(-11 * time.Minute)}
	newClaim, ok, err := s.ClaimActivation(ctx, "a")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, s.ReleaseActivation(ctx, "a", staleClaim, errBoom))
	_, ok, err = s.ClaimActivation(ctx, "a")
	require.NoError(t, err)
	require.False(t, ok, "a stale holder's release must not free the new claim")

	// Failure: claim released, retry pushed into the future, error recorded.
	require.NoError(t, s.ReleaseActivation(ctx, "a", newClaim, errBoom))
	_, ok, err = s.ClaimActivation(ctx, "a")
	require.NoError(t, err)
	require.False(t, ok, "webhook/poll claims wait out the backoff too")
	due, err = s.ListPaidUnactivated(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, due, "backoff not elapsed")
	var attempts int
	var errText string
	var delay float64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT activation_attempts, activation_error,
		EXTRACT(epoch FROM next_activation_at - now()) FROM candidate_checkouts WHERE prompt_id='a'`).Scan(&attempts, &errText, &delay))
	require.Equal(t, 2, attempts)
	require.Equal(t, "boom", errText)
	require.InDelta(t, 60, delay, 5, "30s doubled after the second attempt")

	_, err = db.ExecContext(ctx, `UPDATE candidate_checkouts SET next_activation_at = now() - interval '1 second' WHERE prompt_id='a'`)
	require.NoError(t, err)
	due, err = s.ListPaidUnactivated(ctx, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)

	// Attempt limit: no longer claimable or offered, counted as stuck.
	_, err = db.ExecContext(ctx, `UPDATE candidate_checkouts SET activation_attempts = $1 WHERE prompt_id='a'`, billing.MaxActivationAttempts)
	require.NoError(t, err)
	_, ok, err = s.ClaimActivation(ctx, "a")
	require.NoError(t, err)
	require.False(t, ok)
	due, err = s.ListPaidUnactivated(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, due)
	stuck, err := s.CountStuckActivations(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, stuck)

	require.NoError(t, s.MarkActivated(ctx, "a", billing.OutcomeApplied))
	stuck, err = s.CountStuckActivations(ctx)
	require.NoError(t, err)
	require.Zero(t, stuck)
}

func TestStore_HasNewerActivatedSubscriptionUsesPaymentTime(t *testing.T) {
	s, db, ctx := setupStoreDB(t)
	at := func(prompt, col string, ago time.Duration) {
		t.Helper()
		_, err := db.ExecContext(ctx, `UPDATE candidate_checkouts SET `+col+` = now() - make_interval(secs => $2) WHERE prompt_id=$1`, prompt, ago.Seconds())
		require.NoError(t, err)
	}
	// Reviewer scenario: Pro checkout created first and abandoned; Starter
	// bought and activated; then the old Pro link is paid.
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "pro", CandidateID: "c", PlanID: "pro"}))
	at("pro", "created_at", 48*time.Hour)
	paidCheckout(t, s, ctx, "starter", "c", "starter")
	require.NoError(t, s.MarkActivated(ctx, "starter", billing.OutcomeApplied))
	at("starter", "activated_at", time.Hour)
	_, _, err := s.ResolveStatus(ctx, "pro", billing.StatusPaid, "", "")
	require.NoError(t, err)
	pro, err := s.GetByPromptID(ctx, "pro")
	require.NoError(t, err)
	require.NotNil(t, pro.PaidAt)
	newer, err := s.HasNewerActivatedSubscription(ctx, pro)
	require.NoError(t, err)
	require.False(t, newer, "Pro was paid last: it is the latest purchase")

	// Opposite order: a checkout paid earlier whose activation failed, then a
	// later purchase activated — the earlier one must not overwrite it.
	paidCheckout(t, s, ctx, "early", "c", "starter")
	at("early", "paid_at", 3*time.Hour)
	early, err := s.GetByPromptID(ctx, "early")
	require.NoError(t, err)
	newer, err = s.HasNewerActivatedSubscription(ctx, early)
	require.NoError(t, err)
	require.True(t, newer)

	// A superseded checkout or a one-time purchase never counts as newer.
	_, err = db.ExecContext(ctx, `UPDATE candidate_checkouts SET activation_outcome='superseded' WHERE prompt_id='starter'`)
	require.NoError(t, err)
	paidCheckout(t, s, ctx, "report", "c", string(billing.ProductATSReport))
	require.NoError(t, s.MarkActivated(ctx, "report", billing.OutcomeApplied))
	newer, err = s.HasNewerActivatedSubscription(ctx, early)
	require.NoError(t, err)
	require.False(t, newer)

	// Another candidate's purchases never count.
	paidCheckout(t, s, ctx, "other", "someone-else", "pro")
	require.NoError(t, s.MarkActivated(ctx, "other", billing.OutcomeApplied))
	newer, err = s.HasNewerActivatedSubscription(ctx, early)
	require.NoError(t, err)
	require.False(t, newer)

	n, err := s.CountSupersededSince(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

// Retries can activate an earlier purchase after a later one was paid. The
// later payment must still win: A paid first, B paid second, A's activation
// recorded first — B is not "superseded" by A.
func TestStore_HasNewerActivatedSubscriptionIgnoresActivationOrder(t *testing.T) {
	s, db, ctx := setupStoreDB(t)
	paidCheckout(t, s, ctx, "A", "c", "starter")
	_, err := db.ExecContext(ctx, `UPDATE candidate_checkouts SET paid_at = now() - interval '2 hours' WHERE prompt_id='A'`)
	require.NoError(t, err)
	paidCheckout(t, s, ctx, "B", "c", "pro")
	_, err = db.ExecContext(ctx, `UPDATE candidate_checkouts SET paid_at = now() - interval '1 hour' WHERE prompt_id='B'`)
	require.NoError(t, err)
	require.NoError(t, s.MarkActivated(ctx, "A", billing.OutcomeApplied)) // A's retry ran first

	b, err := s.GetByPromptID(ctx, "B")
	require.NoError(t, err)
	newer, err := s.HasNewerActivatedSubscription(ctx, b)
	require.NoError(t, err)
	require.False(t, newer, "B was paid last; A's later activation does not supersede it")

	a, err := s.GetByPromptID(ctx, "A")
	require.NoError(t, err)
	require.NoError(t, s.MarkActivated(ctx, "B", billing.OutcomeApplied))
	newer, err = s.HasNewerActivatedSubscription(ctx, a)
	require.NoError(t, err)
	require.True(t, newer, "and A, paid first, is superseded by B")
}

func TestStore_PaidAtSetOnceOnPayment(t *testing.T) {
	s, ctx := setupStore(t)
	require.NoError(t, s.Create(ctx, billing.Checkout{PromptID: "p", CandidateID: "c", PlanID: "pro"}))
	_, _, err := s.ResolveStatus(ctx, "p", billing.StatusFailed, "", "declined")
	require.NoError(t, err)
	failed, err := s.GetByPromptID(ctx, "p")
	require.NoError(t, err)
	require.Nil(t, failed.PaidAt, "a failed payment is not paid")
	_, _, err = s.ResolveStatus(ctx, "p", billing.StatusPaid, "", "")
	require.NoError(t, err)
	paid, err := s.GetByPromptID(ctx, "p")
	require.NoError(t, err)
	require.NotNil(t, paid.PaidAt)
}

var errBoom = assertErr("boom")

type assertErr string

func (e assertErr) Error() string { return string(e) }
