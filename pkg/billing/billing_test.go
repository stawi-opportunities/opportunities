package billing_test

import (
	"context"
	"errors"
	"testing"

	commonv1 "buf.build/gen/go/antinvestor/common/protocolbuffers/go/common/v1"
	paymentv1 "buf.build/gen/go/antinvestor/payment/protocolbuffers/go/v1"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
)

// structFields builds a structpb.Struct from a flat string map for the
// payment provider's Extras field (e.g. redirect_url).
func structFields(m map[string]string) (*structpb.Struct, error) {
	fields := make(map[string]any, len(m))
	for k, v := range m {
		fields[k] = v
	}
	return structpb.NewStruct(fields)
}

// --- catalog -----------------------------------------------------------

func TestCatalog_MirrorsPlansTs(t *testing.T) {
	t.Parallel()
	c := billing.Catalog()
	require.Len(t, c, 3)
	byID := map[billing.PlanID]billing.Plan{}
	for _, p := range c {
		byID[p.ID] = p
	}
	require.Equal(t, 1000, byID[billing.PlanStarter].USDCents)
	require.Equal(t, 5000, byID[billing.PlanPro].USDCents)
	require.Equal(t, 20000, byID[billing.PlanManaged].USDCents)
}

func TestPlanByID_And_Normalize(t *testing.T) {
	t.Parallel()
	_, ok := billing.PlanByID("pro")
	require.True(t, ok)
	_, ok = billing.PlanByID("free")
	require.False(t, ok)

	id, ok := billing.NormalizePlan("  Pro ")
	require.True(t, ok)
	require.Equal(t, billing.PlanPro, id)

	_, ok = billing.NormalizePlan("free")
	require.False(t, ok)
}

// --- route selection ---------------------------------------------------

func TestRouteForCountry(t *testing.T) {
	t.Parallel()
	require.Equal(t, billing.RouteMpesa, billing.RouteForCountry("KE", ""))
	require.Equal(t, billing.RouteMTN, billing.RouteForCountry("ug", ""))
	require.Equal(t, billing.RoutePolar, billing.RouteForCountry("US", ""))
	// explicit hint overrides country
	require.Equal(t, billing.RoutePolar, billing.RouteForCountry("KE", "POLAR"))
	// invalid hint falls back to country mapping
	require.Equal(t, billing.RouteMpesa, billing.RouteForCountry("KE", "garbage"))
}

// --- payment gateway adapter ------------------------------------------

type fakePayment struct {
	linkResp   *commonv1.StatusResponse
	promptResp *commonv1.StatusResponse
	statusResp *commonv1.StatusResponse
	err        error

	sawLink   bool
	sawPrompt bool
}

func (f *fakePayment) CreatePaymentLink(_ context.Context, _ *connect.Request[paymentv1.CreatePaymentLinkRequest]) (*connect.Response[paymentv1.CreatePaymentLinkResponse], error) {
	f.sawLink = true
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(paymentv1.CreatePaymentLinkResponse_builder{Data: f.linkResp}.Build()), nil
}

func (f *fakePayment) InitiatePrompt(_ context.Context, _ *connect.Request[paymentv1.InitiatePromptRequest]) (*connect.Response[paymentv1.InitiatePromptResponse], error) {
	f.sawPrompt = true
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(paymentv1.InitiatePromptResponse_builder{Data: f.promptResp}.Build()), nil
}

func (f *fakePayment) Status(_ context.Context, _ *connect.Request[commonv1.StatusRequest]) (*connect.Response[commonv1.StatusResponse], error) {
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.statusResp), nil
}

func proPlan(t *testing.T) billing.Plan {
	t.Helper()
	p, ok := billing.PlanByID(billing.PlanPro)
	require.True(t, ok)
	return p
}

func TestPaymentGateway_CardRouteCreatesRedirect(t *testing.T) {
	t.Parallel()
	fields, err := structFields(map[string]string{"redirect_url": "https://pay.example/checkout/abc"})
	require.NoError(t, err)
	fp := &fakePayment{linkResp: commonv1.StatusResponse_builder{
		Id:     "provider-link-1",
		Status: commonv1.STATUS_QUEUED,
		Extras: fields,
	}.Build()}
	g := billing.NewPaymentGateway(fp)

	res, err := g.CreateCheckout(context.Background(), billing.CheckoutRequest{
		CandidateID: "cand_1", Plan: proPlan(t), Country: "US",
	})
	require.NoError(t, err)
	require.True(t, fp.sawLink)
	require.False(t, fp.sawPrompt)
	require.Equal(t, billing.RoutePolar, res.Route)
	require.Equal(t, billing.StatusRedirect, res.Status)
	require.Equal(t, "https://pay.example/checkout/abc", res.RedirectURL)
	require.Equal(t, "provider-link-1", res.PromptID)
}

func TestPaymentGateway_MobileMoneyRouteIsPending(t *testing.T) {
	t.Parallel()
	fp := &fakePayment{promptResp: commonv1.StatusResponse_builder{
		Id:     "stk-1",
		Status: commonv1.STATUS_IN_PROCESS,
	}.Build()}
	g := billing.NewPaymentGateway(fp)

	res, err := g.CreateCheckout(context.Background(), billing.CheckoutRequest{
		CandidateID: "cand_2", Plan: proPlan(t), Country: "KE", Phone: "+254700000000",
	})
	require.NoError(t, err)
	require.True(t, fp.sawPrompt)
	require.False(t, fp.sawLink)
	require.Equal(t, billing.RouteMpesa, res.Route)
	require.Equal(t, billing.StatusPending, res.Status)
	require.Equal(t, "stk-1", res.PromptID)
}

func TestPaymentGateway_StatusMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   commonv1.STATUS
		want billing.Status
	}{
		{commonv1.STATUS_SUCCESSFUL, billing.StatusPaid},
		{commonv1.STATUS_FAILED, billing.StatusFailed},
		{commonv1.STATUS_QUEUED, billing.StatusPending},
		{commonv1.STATUS_IN_PROCESS, billing.StatusPending},
		{commonv1.STATUS_UNKNOWN, billing.StatusPending},
	}
	for _, tc := range cases {
		fp := &fakePayment{statusResp: commonv1.StatusResponse_builder{
			Id: "p", Status: tc.in, ExternalId: "sub-9",
		}.Build()}
		g := billing.NewPaymentGateway(fp)
		got, err := g.CheckoutStatus(context.Background(), "p")
		require.NoError(t, err)
		require.Equal(t, tc.want, got.Status)
		require.Equal(t, "sub-9", got.SubscriptionID)
	}
}

func TestEntitlementsFor(t *testing.T) {
	starter := billing.EntitlementsFor(billing.PlanStarter)
	require.Equal(t, 5, starter.WeeklyCap)
	require.False(t, starter.AutoApply)

	pro := billing.EntitlementsFor(billing.PlanPro)
	require.Equal(t, 25, pro.WeeklyCap)
	require.True(t, pro.AutoApply)

	managed := billing.EntitlementsFor(billing.PlanManaged)
	require.Equal(t, 0, managed.WeeklyCap)
	require.True(t, managed.AutoApply)

	// Unknown → starter-safe defaults
	unknown := billing.EntitlementsFor(billing.PlanID("free"))
	require.Equal(t, 5, unknown.WeeklyCap)
	require.False(t, unknown.AutoApply)
}

func TestNopGateway(t *testing.T) {
	t.Parallel()
	_, err := billing.NopGateway{}.CreateCheckout(context.Background(), billing.CheckoutRequest{})
	require.ErrorIs(t, err, billing.ErrGatewayUnavailable)
	st, err := billing.NopGateway{}.CheckoutStatus(context.Background(), "x")
	require.NoError(t, err)
	require.Equal(t, billing.StatusPending, st.Status)
}

// --- activator (idempotent flip) --------------------------------------

type fakeStore struct {
	rows map[string]billing.Checkout
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]billing.Checkout{}} }

func (s *fakeStore) GetByPromptID(_ context.Context, id string) (billing.Checkout, error) {
	c, ok := s.rows[id]
	if !ok {
		return billing.Checkout{}, billing.ErrNotFound
	}
	return c, nil
}

func (s *fakeStore) UpdateStatus(_ context.Context, id string, status billing.Status, subID, errMsg string) (billing.Checkout, error) {
	c, ok := s.rows[id]
	if !ok {
		return billing.Checkout{}, billing.ErrNotFound
	}
	c.Status = status
	if subID != "" {
		c.SubscriptionID = subID
	}
	c.Error = errMsg
	s.rows[id] = c
	return c, nil
}

func (s *fakeStore) ListPending(_ context.Context, limit int) ([]billing.Checkout, error) {
	out := []billing.Checkout{}
	for _, c := range s.rows {
		if c.Status == billing.StatusPending {
			out = append(out, c)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type fakeActivator struct {
	calls   int
	changed bool
	lastSub string
	lastCID string
	err     error
}

func (a *fakeActivator) ActivateSubscription(_ context.Context, candidateID, subID, _ string) (bool, error) {
	a.calls++
	a.lastCID = candidateID
	a.lastSub = subID
	if a.err != nil {
		return false, a.err
	}
	// Simulate idempotency: first call changes, subsequent ones don't.
	changed := a.changed
	a.changed = false
	return changed, nil
}

func TestActivator_PaidFlipsCandidateOnce(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["chk_1"] = billing.Checkout{PromptID: "chk_1", CandidateID: "cand_x", PlanID: "pro", Status: billing.StatusPending}
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)

	require.NoError(t, act.Activate(context.Background(), "chk_1", billing.StatusPaid, "sub-77", ""))
	require.Equal(t, billing.StatusPaid, store.rows["chk_1"].Status)
	require.Equal(t, 1, subs.calls)
	require.Equal(t, "cand_x", subs.lastCID)
	require.Equal(t, "sub-77", subs.lastSub)

	// Re-fire (double webhook): still safe, no error.
	require.NoError(t, act.Activate(context.Background(), "chk_1", billing.StatusPaid, "sub-77", ""))
	require.Equal(t, 2, subs.calls)
}

func TestActivator_FailedRecordsNoFlip(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["chk_2"] = billing.Checkout{PromptID: "chk_2", CandidateID: "cand_y", Status: billing.StatusPending}
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)

	require.NoError(t, act.Activate(context.Background(), "chk_2", billing.StatusFailed, "", "card declined"))
	require.Equal(t, billing.StatusFailed, store.rows["chk_2"].Status)
	require.Equal(t, 0, subs.calls)
}

func TestActivator_UnknownPromptIsIgnored(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	subs := &fakeActivator{}
	act := billing.NewActivator(store, subs)
	require.NoError(t, act.Activate(context.Background(), "nope", billing.StatusPaid, "s", ""))
	require.Equal(t, 0, subs.calls)
}

func TestActivator_NonTerminalIsNoop(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["chk_3"] = billing.Checkout{PromptID: "chk_3", Status: billing.StatusPending}
	subs := &fakeActivator{}
	act := billing.NewActivator(store, subs)
	require.NoError(t, act.Activate(context.Background(), "chk_3", billing.StatusPending, "", ""))
	require.Equal(t, billing.StatusPending, store.rows["chk_3"].Status)
	require.Equal(t, 0, subs.calls)
}

// --- reconciler --------------------------------------------------------

type fakeGateway struct {
	statuses map[string]billing.StatusResult
	err      error
}

func (g *fakeGateway) CreateCheckout(_ context.Context, _ billing.CheckoutRequest) (billing.CheckoutResult, error) {
	return billing.CheckoutResult{}, nil
}

func (g *fakeGateway) CheckoutStatus(_ context.Context, id string) (billing.StatusResult, error) {
	if g.err != nil {
		return billing.StatusResult{}, g.err
	}
	return g.statuses[id], nil
}

func TestReconciler_ActivatesConfirmedPending(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["a"] = billing.Checkout{PromptID: "a", CandidateID: "c1", Status: billing.StatusPending}
	store.rows["b"] = billing.Checkout{PromptID: "b", CandidateID: "c2", Status: billing.StatusPending}
	store.rows["c"] = billing.Checkout{PromptID: "c", CandidateID: "c3", Status: billing.StatusPending}
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)
	gw := &fakeGateway{statuses: map[string]billing.StatusResult{
		"a": {Status: billing.StatusPaid, SubscriptionID: "s-a"},
		"b": {Status: billing.StatusFailed, Error: "x"},
		"c": {Status: billing.StatusPending},
	}}
	rec := billing.NewReconciler(store, gw, act, 100)

	res, err := rec.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, res.Examined)
	require.Equal(t, 1, res.Activated)
	require.Equal(t, 1, res.Failed)
	require.Equal(t, 0, res.Errors)
	require.Equal(t, billing.StatusPaid, store.rows["a"].Status)
	require.Equal(t, billing.StatusFailed, store.rows["b"].Status)
	require.Equal(t, billing.StatusPending, store.rows["c"].Status)
}

func TestReconciler_GatewayErrorCountsButContinues(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["a"] = billing.Checkout{PromptID: "a", CandidateID: "c1", Status: billing.StatusPending}
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)
	gw := &fakeGateway{err: errors.New("provider down")}
	rec := billing.NewReconciler(store, gw, act, 100)

	res, err := rec.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res.Examined)
	require.Equal(t, 1, res.Errors)
	require.Equal(t, 0, res.Activated)
}
