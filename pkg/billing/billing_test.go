package billing_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	require.Len(t, c, 2)
	byID := map[billing.PlanID]billing.Plan{}
	for _, p := range c {
		byID[p.ID] = p
	}
	require.Equal(t, 1000, byID[billing.PlanStarter].USDCents)
	require.Equal(t, 20000, byID[billing.PlanManaged].USDCents)
	_, hasPro := byID[billing.PlanPro]
	require.False(t, hasPro, "pro is no longer sold")
}

func TestPlanByID_And_Normalize(t *testing.T) {
	t.Parallel()
	_, ok := billing.PlanByID("pro")
	require.False(t, ok, "pro not in sellable catalog")
	_, ok = billing.PlanByID("managed")
	require.True(t, ok)
	_, ok = billing.PlanByID("free")
	require.False(t, ok)

	// Legacy pro checkouts map to managed.
	id, ok := billing.NormalizePlan("  Pro ")
	require.True(t, ok)
	require.Equal(t, billing.PlanManaged, id)

	_, ok = billing.NormalizePlan("free")
	require.False(t, ok)
}

// --- route selection ---------------------------------------------------

func TestRouteForCountry_AlwaysFlutterwave(t *testing.T) {
	t.Parallel()
	// No geo-routing / multi-rail evaluation — always Flutterwave.
	for _, country := range []string{"", "KE", "UG", "US", "GH"} {
		require.Equal(t, billing.RouteFlutterwave, billing.RouteForCountry(country, ""))
		require.Equal(t, billing.RouteFlutterwave, billing.RouteForCountry(country, "MTN"))
		require.Equal(t, billing.RouteFlutterwave, billing.RouteForCountry(country, "POLAR"))
	}
}

// --- payment gateway adapter ------------------------------------------

type fakePayment struct {
	linkResp   *commonv1.StatusResponse
	promptResp *commonv1.StatusResponse
	statusResp *commonv1.StatusResponse
	// statusSequence lets short-poll tests return evolving statuses.
	statusSequence []*commonv1.StatusResponse
	statusCalls    int
	err            error

	sawLink    bool
	sawPrompt  bool
	lastPrompt *paymentv1.InitiatePromptRequest
	lastStatus *commonv1.StatusRequest
}

func (f *fakePayment) CreatePaymentLink(_ context.Context, _ *connect.Request[paymentv1.CreatePaymentLinkRequest]) (*connect.Response[paymentv1.CreatePaymentLinkResponse], error) {
	f.sawLink = true
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(paymentv1.CreatePaymentLinkResponse_builder{Data: f.linkResp}.Build()), nil
}

func (f *fakePayment) InitiatePrompt(_ context.Context, req *connect.Request[paymentv1.InitiatePromptRequest]) (*connect.Response[paymentv1.InitiatePromptResponse], error) {
	f.sawPrompt = true
	f.lastPrompt = req.Msg
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(paymentv1.InitiatePromptResponse_builder{Data: f.promptResp}.Build()), nil
}

func (f *fakePayment) Status(_ context.Context, req *connect.Request[commonv1.StatusRequest]) (*connect.Response[commonv1.StatusResponse], error) {
	f.lastStatus = req.Msg
	f.statusCalls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.statusSequence) > 0 {
		idx := f.statusCalls - 1
		if idx >= len(f.statusSequence) {
			idx = len(f.statusSequence) - 1
		}
		return connect.NewResponse(f.statusSequence[idx]), nil
	}
	return connect.NewResponse(f.statusResp), nil
}

func proPlan(t *testing.T) billing.Plan {
	t.Helper()
	// historical name; checkout tests use Managed ($200).
	p, ok := billing.PlanByID(billing.PlanManaged)
	require.True(t, ok)
	return p
}

func preferCardFalse() *bool {
	f := false
	return &f
}

func TestPaymentGateway_CardRequiresHostedCheckout(t *testing.T) {
	t.Parallel()
	// PreferCard (default) without CheckoutService must not InitiatePrompt —
	// OAuth-only Flutterwave cannot multipay without FLWSECK / encrypted card.
	fp := &fakePayment{}
	g := billing.NewPaymentGateway(fp, billing.GatewayOptions{
		PublicSiteURL: "https://opportunities.stawi.org",
	})
	res, err := g.CreateCheckout(context.Background(), billing.CheckoutRequest{
		CandidateID: "cand_1", Plan: proPlan(t), Email: "a@b.co",
	})
	require.NoError(t, err)
	require.False(t, fp.sawPrompt)
	require.Equal(t, billing.StatusFailed, res.Status)
	require.Contains(t, res.Error, "CHECKOUT_SERVICE_URI")
}

func TestPaymentGateway_LegacyPromptWhenPreferCardDisabled(t *testing.T) {
	t.Parallel()
	queued := commonv1.StatusResponse_builder{
		Id:     "chk_fw_1",
		Status: commonv1.STATUS_QUEUED,
	}.Build()
	urlFields, err := structFields(map[string]string{"checkout_url": "https://checkout.flutterwave.com/v3/hosted/pay/abc"})
	require.NoError(t, err)
	ready := commonv1.StatusResponse_builder{
		Id:     "chk_fw_1",
		Status: commonv1.STATUS_IN_PROCESS,
		Extras: urlFields,
	}.Build()
	fp := &fakePayment{
		promptResp:     queued,
		statusSequence: []*commonv1.StatusResponse{queued, ready},
	}
	g := billing.NewPaymentGateway(fp, billing.GatewayOptions{
		PublicSiteURL:        "https://opportunities.stawi.org",
		PreferCard:           preferCardFalse(),
		RedirectPollAttempts: 5,
		RedirectPollInterval: time.Millisecond,
	})

	res, err := g.CreateCheckout(context.Background(), billing.CheckoutRequest{
		CandidateID: "cand_1", Plan: proPlan(t), Country: "KE",
		Email: "a@b.co",
	})
	require.NoError(t, err)
	require.True(t, fp.sawPrompt)
	require.Equal(t, "flutterwave", fp.lastPrompt.GetRoute())
	require.Equal(t, billing.StatusRedirect, res.Status)
	require.Equal(t, "https://checkout.flutterwave.com/v3/hosted/pay/abc", res.RedirectURL)
	require.Equal(t, "chk_fw_1", res.PromptID)
}

func TestPaymentGateway_NoPayURLBecomesFailed(t *testing.T) {
	t.Parallel()
	// Worker never materialises checkout_url → fail closed so the SPA
	// does not land on a stranded pending poller with nowhere to go.
	fp := &fakePayment{
		promptResp: commonv1.StatusResponse_builder{
			Id: "chk_stuck", Status: commonv1.STATUS_QUEUED,
		}.Build(),
		statusResp: commonv1.StatusResponse_builder{
			Id: "chk_stuck", Status: commonv1.STATUS_IN_PROCESS,
		}.Build(),
	}
	g := billing.NewPaymentGateway(fp, billing.GatewayOptions{
		PublicSiteURL:        "https://opportunities.stawi.org",
		PreferCard:           preferCardFalse(),
		RedirectPollAttempts: 3,
		RedirectPollInterval: time.Millisecond,
	})
	res, err := g.CreateCheckout(context.Background(), billing.CheckoutRequest{
		CandidateID: "cand_x", Plan: proPlan(t), Email: "a@b.co",
	})
	require.NoError(t, err)
	require.Equal(t, billing.StatusFailed, res.Status)
	require.Empty(t, res.RedirectURL)
	require.Contains(t, res.Error, "not ready")
}

func TestPaymentGateway_DoesNotTreatSuccessURLAsPayURL(t *testing.T) {
	t.Parallel()
	// Provider echoes our success_url under redirect_url — must not navigate there.
	successFields, err := structFields(map[string]string{
		"redirect_url": "https://opportunities.stawi.org/dashboard/?billing=success&prompt_id=chk_echo",
	})
	require.NoError(t, err)
	fp := &fakePayment{
		promptResp: commonv1.StatusResponse_builder{
			Id: "chk_echo", Status: commonv1.STATUS_QUEUED,
		}.Build(),
		statusResp: commonv1.StatusResponse_builder{
			Id: "chk_echo", Status: commonv1.STATUS_IN_PROCESS, Extras: successFields,
		}.Build(),
	}
	g := billing.NewPaymentGateway(fp, billing.GatewayOptions{
		PublicSiteURL:        "https://opportunities.stawi.org",
		PreferCard:           preferCardFalse(),
		RedirectPollAttempts: 2,
		RedirectPollInterval: time.Millisecond,
	})
	res, err := g.CreateCheckout(context.Background(), billing.CheckoutRequest{
		CandidateID: "cand_e", Plan: proPlan(t), Email: "a@b.co",
	})
	require.NoError(t, err)
	require.Empty(t, res.RedirectURL)
	require.Equal(t, billing.StatusFailed, res.Status)
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
		g := billing.NewPaymentGateway(fp, billing.GatewayOptions{})
		got, err := g.CheckoutStatus(context.Background(), "p")
		require.NoError(t, err)
		require.Equal(t, tc.want, got.Status)
		require.Equal(t, "sub-9", got.SubscriptionID)
		// Status requests must carry entity_type=prompt for service-payment.
		require.Equal(t, "prompt", fp.lastStatus.GetExtras().GetFields()["entity_type"].GetStringValue())
	}
}

func TestPaymentGateway_StatusReadsCheckoutURL(t *testing.T) {
	t.Parallel()
	fields, err := structFields(map[string]string{"checkout_url": "https://polar.sh/c/1"})
	require.NoError(t, err)
	fp := &fakePayment{statusResp: commonv1.StatusResponse_builder{
		Id: "p", Status: commonv1.STATUS_IN_PROCESS, Extras: fields,
	}.Build()}
	g := billing.NewPaymentGateway(fp, billing.GatewayOptions{})
	got, err := g.CheckoutStatus(context.Background(), "p")
	require.NoError(t, err)
	require.Equal(t, "https://polar.sh/c/1", got.RedirectURL)
}

func TestPaymentGateway_HostedCheckoutPreferred(t *testing.T) {
	t.Parallel()
	// When CheckoutService is configured, open pay.stawi.org instead of FW multipay.
	fc := &fakeCheckout{
		create: billing.CreateHostedSessionResult{
			Ref:     "sess_abc",
			PageURL: "https://pay.stawi.org/c/sess_abc",
		},
	}
	fp := &fakePayment{}
	g := billing.NewPaymentGatewayWithCheckout(fp, fc, billing.GatewayOptions{
		PublicSiteURL: "https://opportunities.stawi.org",
	})
	res, err := g.CreateCheckout(context.Background(), billing.CheckoutRequest{
		CandidateID: "cand_h",
		ProfileID:   "prof_platform_1",
		Plan:        proPlan(t),
		Email:       "h@b.co",
		Phone:       "254700000000",
	})
	require.NoError(t, err)
	require.False(t, fp.sawPrompt, "must not InitiatePrompt when hosted checkout succeeds")
	require.Equal(t, billing.StatusRedirect, res.Status)
	require.Equal(t, "https://pay.stawi.org/c/sess_abc", res.RedirectURL)
	require.True(t, strings.HasPrefix(res.PromptID, "chk_"), "ledger/return/poll key must be minted chk_* order_ref")
	require.Equal(t, fc.last.OrderRef, res.PromptID, "OrderRef and PromptID must match")
	require.Equal(t, []string{"card"}, fc.last.Methods)
	// Platform profile id is what checkout uses for GetById (contacts + au_name).
	require.Equal(t, "prof_platform_1", fc.last.ProfileID)
	require.Contains(t, fc.last.ReturnURL, "billing=success")
}

func TestPaymentGateway_HostedCheckout_ProfileIDFallsBackToCandidate(t *testing.T) {
	fc := &fakeCheckout{
		create: billing.CreateHostedSessionResult{
			Ref: "sess_x", PageURL: "https://pay.stawi.org/c/sess_x",
		},
	}
	g := billing.NewPaymentGatewayWithCheckout(&fakePayment{}, fc, billing.GatewayOptions{
		PublicSiteURL: "https://opportunities.stawi.org",
	})
	_, err := g.CreateCheckout(context.Background(), billing.CheckoutRequest{
		CandidateID: "jwt_sub_as_profile", Plan: proPlan(t),
	})
	require.NoError(t, err)
	require.Equal(t, "jwt_sub_as_profile", fc.last.ProfileID)
}

type fakeCheckout struct {
	create billing.CreateHostedSessionResult
	last   billing.CreateHostedSessionRequest
	err    error
}

func (f *fakeCheckout) CreateSession(_ context.Context, req billing.CreateHostedSessionRequest) (billing.CreateHostedSessionResult, error) {
	f.last = req
	if f.err != nil {
		return billing.CreateHostedSessionResult{}, f.err
	}
	return f.create, nil
}

func (f *fakeCheckout) GetSession(_ context.Context, ref string) (billing.HostedSessionStatus, error) {
	return billing.HostedSessionStatus{Ref: ref, Status: "pending"}, nil
}

func TestEntitlementsFor(t *testing.T) {
	starter := billing.EntitlementsFor(billing.PlanStarter)
	require.Equal(t, 30, starter.InvokeDailyLimit)
	require.Equal(t, 0, starter.DailyCap)
	require.Equal(t, 0, starter.WeeklyCap)
	require.False(t, starter.AutoApply)

	// Legacy pro inherits managed entitlements (invoke limit, unlimited rows).
	// AutoApply stays false until real apply automation ships.
	pro := billing.EntitlementsFor(billing.PlanPro)
	require.Equal(t, 100, pro.InvokeDailyLimit)
	require.Equal(t, 0, pro.WeeklyCap)
	require.False(t, pro.AutoApply)

	managed := billing.EntitlementsFor(billing.PlanManaged)
	require.Equal(t, 100, managed.InvokeDailyLimit)
	require.Equal(t, 0, managed.WeeklyCap)
	require.False(t, managed.AutoApply)

	// Unknown / free → proof invoke limit (value before pay); unlimited match rows.
	unknown := billing.EntitlementsFor(billing.PlanID("free"))
	require.Equal(t, 1, unknown.InvokeDailyLimit)
	require.Equal(t, 0, unknown.DailyCap)
	require.Equal(t, 0, unknown.WeeklyCap)
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
	rows       map[string]billing.Checkout
	claims     map[string]bool
	attempts   int
	released   int
	stuck      int64
	superseded int64
	outcomes   map[string]string
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]billing.Checkout{}} }

func (s *fakeStore) GetByPromptID(_ context.Context, id string) (billing.Checkout, error) {
	c, ok := s.rows[id]
	if !ok {
		return billing.Checkout{}, billing.ErrNotFound
	}
	return c, nil
}

// ResolveStatus mirrors Store.ResolveStatus's transition rules.
func (s *fakeStore) ResolveStatus(_ context.Context, id string, status billing.Status, subID, errMsg string) (billing.Checkout, bool, error) {
	c, ok := s.rows[id]
	if !ok {
		return billing.Checkout{}, false, billing.ErrNotFound
	}
	if !billing.CanTransition(c.Status, status) {
		return c, false, nil
	}
	c.Status = status
	if subID != "" {
		c.SubscriptionID = subID
	}
	c.Error = errMsg
	s.rows[id] = c
	return c, true, nil
}

func (s *fakeStore) MarkActivated(_ context.Context, id, outcome string) error {
	c := s.rows[id]
	if c.ActivatedAt == nil {
		now := time.Now()
		c.ActivatedAt = &now
	}
	s.rows[id] = c
	if s.outcomes == nil {
		s.outcomes = map[string]string{}
	}
	if s.outcomes[id] == "" {
		s.outcomes[id] = outcome
	}
	delete(s.claims, id)
	return nil
}

// ClaimActivation mirrors Store.ClaimActivation: paid, unactivated, no claim.
func (s *fakeStore) ClaimActivation(_ context.Context, id string) (billing.ActivationClaim, bool, error) {
	if s.claims == nil {
		s.claims = map[string]bool{}
	}
	c := s.rows[id]
	if c.Status != billing.StatusPaid || c.ActivatedAt != nil || s.claims[id] {
		return billing.ActivationClaim{}, false, nil
	}
	s.claims[id] = true
	s.attempts++
	return billing.ActivationClaim{At: time.Now()}, true, nil
}

func (s *fakeStore) ReleaseActivation(_ context.Context, id string, _ billing.ActivationClaim, _ error) error {
	delete(s.claims, id)
	s.released++
	return nil
}

// HasNewerActivatedSubscription mirrors the store: another subscription
// checkout activated (not superseded) after c was paid.
func (s *fakeStore) HasNewerActivatedSubscription(_ context.Context, c billing.Checkout) (bool, error) {
	paidAt := c.CreatedAt
	if c.PaidAt != nil {
		paidAt = *c.PaidAt
	}
	for _, o := range s.rows {
		if o.CandidateID == c.CandidateID && o.PromptID != c.PromptID && o.ActivatedAt != nil &&
			s.outcomes[o.PromptID] != billing.OutcomeSuperseded &&
			newerPaid(o).After(paidAt) && !billing.IsOneTimeProduct(billing.PlanID(o.PlanID)) {
			return true, nil
		}
	}
	return false, nil
}

func newerPaid(o billing.Checkout) time.Time {
	if o.PaidAt != nil {
		return *o.PaidAt
	}
	return *o.ActivatedAt
}

func (s *fakeStore) CountStuckActivations(context.Context) (int64, error) { return s.stuck, nil }

func (s *fakeStore) CountSupersededSince(context.Context, time.Time) (int64, error) {
	return s.superseded, nil
}

func (s *fakeStore) ListPending(_ context.Context, limit int) ([]billing.Checkout, error) {
	return s.list(limit, func(c billing.Checkout) bool { return c.Status == billing.StatusPending }), nil
}

func (s *fakeStore) ListPaidUnactivated(_ context.Context, limit int) ([]billing.Checkout, error) {
	return s.list(limit, func(c billing.Checkout) bool {
		return c.Status == billing.StatusPaid && c.ActivatedAt == nil
	}), nil
}

func (s *fakeStore) list(limit int, keep func(billing.Checkout) bool) []billing.Checkout {
	out := []billing.Checkout{}
	for _, c := range s.rows {
		if keep(c) {
			out = append(out, c)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

type fakeActivator struct {
	holds   string // subscription id the candidate already holds
	calls   int
	changed bool
	lastSub string
	lastCID string
	err     error
}

func (a *fakeActivator) HoldsSubscription(_ context.Context, _ string, subID string) (bool, error) {
	return subID != "" && subID == a.holds, nil
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

	require.NotNil(t, store.rows["chk_1"].ActivatedAt)

	// Re-fire (double webhook or replayed body): no second grant.
	require.NoError(t, act.Activate(context.Background(), "chk_1", billing.StatusPaid, "sub-77", ""))
	require.Equal(t, 1, subs.calls)
}

func TestActivator_LateFailedDoesNotOverwritePaid(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["chk_p"] = billing.Checkout{PromptID: "chk_p", CandidateID: "cand", PlanID: "pro", Status: billing.StatusPending}
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)

	require.NoError(t, act.Activate(context.Background(), "chk_p", billing.StatusPaid, "s", ""))
	require.NoError(t, act.Activate(context.Background(), "chk_p", billing.StatusFailed, "", "late failure"))
	require.Equal(t, billing.StatusPaid, store.rows["chk_p"].Status)
	require.Empty(t, store.rows["chk_p"].Error)
}

func TestActivator_PaidAfterFailedIsHonoured(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["chk_f"] = billing.Checkout{PromptID: "chk_f", CandidateID: "cand", PlanID: "pro", Status: billing.StatusFailed}
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)

	require.NoError(t, act.Activate(context.Background(), "chk_f", billing.StatusPaid, "s", ""))
	require.Equal(t, billing.StatusPaid, store.rows["chk_f"].Status)
	require.Equal(t, 1, subs.calls)
}

func TestActivator_RetriesActivationThatFailedAfterPaid(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["chk_r"] = billing.Checkout{PromptID: "chk_r", CandidateID: "cand", PlanID: "pro", Status: billing.StatusPending}
	subs := &fakeActivator{changed: true, err: errors.New("db blip")}
	act := billing.NewActivator(store, subs)

	require.Error(t, act.Activate(context.Background(), "chk_r", billing.StatusPaid, "s", ""))
	require.Equal(t, billing.StatusPaid, store.rows["chk_r"].Status)
	require.Nil(t, store.rows["chk_r"].ActivatedAt)

	subs.err = nil
	require.NoError(t, act.Activate(context.Background(), "chk_r", billing.StatusPaid, "s", ""))
	require.Equal(t, 2, subs.calls)
	require.NotNil(t, store.rows["chk_r"].ActivatedAt)
}

func TestActivator_ClaimHeldElsewhereRunsNoSideEffects(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["chk"] = billing.Checkout{PromptID: "chk", CandidateID: "c", PlanID: "pro", Status: billing.StatusPaid}
	store.claims = map[string]bool{"chk": true} // e.g. the status poll is mid-activation
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)

	require.NoError(t, act.Activate(context.Background(), "chk", billing.StatusPaid, "s", ""))
	require.Equal(t, 0, subs.calls)
	require.Nil(t, store.rows["chk"].ActivatedAt, "the claim holder records activation, not us")
}

func TestActivator_OneTimeFulfilledOnceAcrossDuplicateCalls(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["rep"] = billing.Checkout{PromptID: "rep", CandidateID: "c", PlanID: string(billing.ProductATSReport), Status: billing.StatusPending}
	ful := &countingFulfiller{}
	act := billing.NewActivator(store, &fakeActivator{}).WithOneTimeFulfiller(ful)

	for i := 0; i < 3; i++ { // status poll, webhook, reconciler
		require.NoError(t, act.Activate(context.Background(), "rep", billing.StatusPaid, "", ""))
	}
	require.Equal(t, 1, ful.calls)
}

func TestActivator_OlderCheckoutDoesNotDowngradeNewerPlan(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Now()
	paidOld := now.Add(-2 * time.Hour) // starter paid, activation failed
	store.rows["old"] = billing.Checkout{PromptID: "old", CandidateID: "c", PlanID: "starter", Status: billing.StatusPaid, CreatedAt: now.Add(-3 * time.Hour), PaidAt: &paidOld}
	store.rows["new"] = billing.Checkout{PromptID: "new", CandidateID: "c", PlanID: "pro", Status: billing.StatusPaid, CreatedAt: now.Add(-time.Hour), ActivatedAt: &now}
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)

	require.NoError(t, act.Activate(context.Background(), "old", billing.StatusPaid, "sub-old", ""))
	require.Equal(t, 0, subs.calls, "starter must not overwrite the pro plan bought after it")
	require.NotNil(t, store.rows["old"].ActivatedAt, "handled; not retried forever")
	require.Equal(t, billing.OutcomeSuperseded, store.outcomes["old"], "visible for refund review")
}

// Reviewer scenario: the user abandons a Pro checkout, buys Starter, then pays
// the old Pro link. Pro was paid last, so it must be applied.
func TestActivator_PayingAnOlderCheckoutLinkLastStillApplies(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Now()
	starterActivated := now.Add(-time.Hour)
	store.rows["pro"] = billing.Checkout{PromptID: "pro", CandidateID: "c", PlanID: "pro", Status: billing.StatusPending, CreatedAt: now.Add(-48 * time.Hour)}
	store.rows["starter"] = billing.Checkout{PromptID: "starter", CandidateID: "c", PlanID: "starter", Status: billing.StatusPaid, CreatedAt: now.Add(-2 * time.Hour), ActivatedAt: &starterActivated}
	paidNow := now
	row := store.rows["pro"]
	row.PaidAt = &paidNow
	store.rows["pro"] = row
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)

	require.NoError(t, act.Activate(context.Background(), "pro", billing.StatusPaid, "sub-pro", ""))
	require.Equal(t, 1, subs.calls)
	require.Equal(t, billing.OutcomeApplied, store.outcomes["pro"])
}

// The status poll passes its HTTP request context; a disconnect after the
// side effects must not leave the activation unrecorded.
func TestActivator_CancelledCallerContextStillRecordsActivation(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["rep"] = billing.Checkout{PromptID: "rep", CandidateID: "c", PlanID: string(billing.ProductATSReport), Status: billing.StatusPaid}
	ctx, cancel := context.WithCancel(context.Background())
	ful := &cancellingFulfiller{cancel: cancel}
	act := billing.NewActivator(store, &fakeActivator{}).WithOneTimeFulfiller(ful)

	require.NoError(t, act.Activate(ctx, "rep", billing.StatusPaid, "", ""))
	require.NotNil(t, store.rows["rep"].ActivatedAt)
	require.NoError(t, ful.sawCtxErr, "fulfilment ran on a context detached from the caller")
}

type cancellingFulfiller struct {
	cancel    context.CancelFunc
	sawCtxErr error
}

func (f *cancellingFulfiller) FulfillOneTime(ctx context.Context, _, _, _ string) error {
	f.cancel() // the browser disconnects mid-request
	f.sawCtxErr = ctx.Err()
	return nil
}

func TestActivator_NewerOneTimePurchaseDoesNotBlockSubscription(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Now()
	store.rows["sub"] = billing.Checkout{PromptID: "sub", CandidateID: "c", PlanID: "pro", Status: billing.StatusPending, CreatedAt: now.Add(-time.Hour)}
	store.rows["rep"] = billing.Checkout{PromptID: "rep", CandidateID: "c", PlanID: string(billing.ProductATSReport), Status: billing.StatusPaid, CreatedAt: now, ActivatedAt: &now}
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)

	require.NoError(t, act.Activate(context.Background(), "sub", billing.StatusPaid, "s", ""))
	require.Equal(t, 1, subs.calls)
}

func TestActivator_RetrySkipsWhenSubscriptionAlreadyHeld(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	// Paid by the previous release, which never recorded activated_at.
	store.rows["chk"] = billing.Checkout{PromptID: "chk", CandidateID: "c", PlanID: "pro", Status: billing.StatusPaid, SubscriptionID: "sub-1"}
	subs := &fakeActivator{changed: true, holds: "sub-1"}
	act := billing.NewActivator(store, subs)

	require.NoError(t, act.Activate(context.Background(), "chk", billing.StatusPaid, "", ""))
	require.Equal(t, 0, subs.calls, "re-activating would clear a later cancellation")
	require.NotNil(t, store.rows["chk"].ActivatedAt)
}

func TestActivator_FailureReleasesClaimForRetry(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["chk"] = billing.Checkout{PromptID: "chk", CandidateID: "c", PlanID: "pro", Status: billing.StatusPending}
	subs := &fakeActivator{err: errors.New("db blip")}
	act := billing.NewActivator(store, subs)

	require.Error(t, act.Activate(context.Background(), "chk", billing.StatusPaid, "s", ""))
	require.Equal(t, 1, store.released)
	require.False(t, store.claims["chk"], "claim released so a retry can run")
}

func TestReconciler_ReportsStuckAndSupersededActivations(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.stuck = 2
	store.superseded = 1
	rec := billing.NewReconciler(store, &fakeGateway{}, billing.NewActivator(store, &fakeActivator{}), 10)
	res, err := rec.Run(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, res.StuckActivations)
	require.EqualValues(t, 1, res.SupersededLast24h)
}

type countingFulfiller struct{ calls int }

func (f *countingFulfiller) FulfillOneTime(context.Context, string, string, string) error {
	f.calls++
	return nil
}

func TestReconciler_RetriesPaidButUnactivated(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.rows["u"] = billing.Checkout{PromptID: "u", CandidateID: "c1", PlanID: "pro", Status: billing.StatusPaid}
	subs := &fakeActivator{changed: true}
	act := billing.NewActivator(store, subs)
	rec := billing.NewReconciler(store, &fakeGateway{}, act, 100)

	res, err := rec.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res.Activated)
	require.Equal(t, 1, subs.calls)
	require.NotNil(t, store.rows["u"].ActivatedAt)
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
