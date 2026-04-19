package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	billingv1 "buf.build/gen/go/antinvestor/billing/protocolbuffers/go/billing/v1"
	"buf.build/gen/go/antinvestor/billing/connectrpc/go/billing/v1/billingv1connect"
	commonv1 "buf.build/gen/go/antinvestor/common/protocolbuffers/go/common/v1"
	paymentv1 "buf.build/gen/go/antinvestor/payment/protocolbuffers/go/v1"
	"buf.build/gen/go/antinvestor/payment/connectrpc/go/v1/paymentv1connect"
	"connectrpc.com/connect"
	"github.com/pitabwire/util"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/structpb"
)

// Client orchestrates checkout across service_billing (subscription
// lifecycle) and service_payment (provider integrations). It does
// NOT talk to Polar.sh / M-Pesa / etc. directly — those providers
// are wired inside service_payment's per-integration apps.
//
// Typical boot path:
//
//	cli := billing.NewClient(billing.ClientConfig{
//	    CatalogVersionID: cfg.BillingCatalogVersionID,
//	    RecipientProfileID: cfg.BillingRecipientProfileID,
//	    PollInterval: 500 * time.Millisecond,
//	    PollTimeout:  8 * time.Second,
//	    PolarProducts: map[billing.PlanID]string{
//	        billing.PlanStarter: cfg.PolarProductStarter,
//	        ...
//	    },
//	}, svcClients.Billing, svcClients.Payment)
//
// A nil Billing or Payment client leaves the service in "not
// configured" mode — OpenCheckout returns ErrNotConfigured.
type Client struct {
	cfg     ClientConfig
	billing billingv1connect.BillingServiceClient
	payment paymentv1connect.PaymentServiceClient
}

// ClientConfig holds environment-specific settings for the Client.
// Every field except PollInterval / PollTimeout is required in
// production; missing fields cause OpenCheckout to return
// ErrNotConfigured.
type ClientConfig struct {
	// CatalogVersionID is the service_billing CatalogVersion to use
	// when creating subscriptions. Bootstrapped once per environment
	// via the billing-catalog seed job (docs/billing/provisioning.md).
	CatalogVersionID string
	// RecipientProfileID identifies the stawi.jobs merchant profile
	// in service_payment. Required so InitiatePrompt's `recipient`
	// contact link resolves.
	RecipientProfileID string
	// SuccessURL / CancelURL are the absolute URLs Polar redirects
	// the user to after checkout completes.
	SuccessURL string
	CancelURL  string
	// PolarProducts maps a local PlanID to the Polar product_id set
	// up in the Polar dashboard. Populated from env per tier.
	PolarProducts map[PlanID]string
	// PollInterval and PollTimeout bound how long OpenCheckout
	// waits for service_payment to produce a Polar checkout URL
	// (or for an STK push to succeed) before returning "pending"
	// so the frontend can long-poll.
	PollInterval time.Duration
	PollTimeout  time.Duration
}

// NewClient wires a Client. Either service client may be nil; the
// returned Client reports not-configured on OpenCheckout in that case.
func NewClient(
	cfg ClientConfig,
	billing billingv1connect.BillingServiceClient,
	payment paymentv1connect.PaymentServiceClient,
) *Client {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = 8 * time.Second
	}
	return &Client{cfg: cfg, billing: billing, payment: payment}
}

// CheckoutRequest is the input to OpenCheckout.
type CheckoutRequest struct {
	// ProfileID is the authenticated user. Propagated as:
	//   - service_billing.CreateSubscriptionRequest.profile_id
	//   - service_payment.InitiatePromptRequest.source.profile_id
	//   - Polar metadata (round-trips through webhook)
	ProfileID string
	// PlanID picks a tier from our local Catalog — resolved to an
	// amount + service_billing plan id in OpenCheckout.
	PlanID PlanID
	// Country is the CF-IPCountry value. Used only for route
	// selection (RouteForCountry) and the display price line.
	Country string
	// Phone is the user's phone number (E.164). Required for any
	// mobile-money route; ignored for RoutePolar.
	Phone string
	// Email is handed to Polar so the hosted-checkout page is
	// pre-filled. Ignored for mobile-money routes.
	Email string
	// RouteHint overrides country-based route selection when the
	// user explicitly ticked a payment method ("mpesa", "card"…).
	// Empty → fall back to RouteForCountry(Country, "").
	RouteHint string
}

// CheckoutResult is what OpenCheckout returns.
type CheckoutResult struct {
	// Route names the rail that was dispatched ("POLAR", "M-PESA"…).
	Route Route
	// Status is one of:
	//   - "redirect"  — RedirectURL ready, caller should 303 user there
	//   - "pending"   — STK push fired / Polar session still queuing;
	//                   caller should poll /billing/checkout/status
	//   - "failed"    — provider flat-out refused; see Error field
	Status string
	// RedirectURL is non-empty for hosted-checkout flows once the
	// polar handler has finished creating the session. Callers MUST
	// 303-redirect the browser.
	RedirectURL string
	// PromptID is the service_payment prompt id; opaque handle for
	// subsequent Status() calls.
	PromptID string
	// SubscriptionID is the service_billing subscription the user
	// just enrolled in (state = SUBSCRIPTION_PENDING until webhook).
	SubscriptionID string
	// AmountCents + Currency echo back the charged amount for the
	// confirmation UI line. Always USD in MVP — service_payment
	// does any FX locally.
	AmountCents int64
	Currency    string
	// Error carries the provider failure message when Status=="failed".
	Error string
}

// OpenCheckout runs the full flow: CreateSubscription → InitiatePrompt
// → poll briefly for provider handoff.
func (c *Client) OpenCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	if c == nil || c.billing == nil || c.payment == nil {
		return CheckoutResult{}, ErrNotConfigured
	}
	if c.cfg.CatalogVersionID == "" || c.cfg.RecipientProfileID == "" {
		return CheckoutResult{}, fmt.Errorf("%w: CatalogVersionID or RecipientProfileID empty", ErrNotConfigured)
	}

	plan, err := LookupPlan(string(req.PlanID))
	if err != nil {
		return CheckoutResult{}, err
	}

	route := RouteForCountry(req.Country, req.RouteHint)
	if !route.IsHostedCheckout() && strings.TrimSpace(req.Phone) == "" {
		return CheckoutResult{}, fmt.Errorf("route %s requires a phone number", route)
	}

	// 1. Create a PENDING subscription. The id is our hook to
	//    reconcile later once service_payment confirms the charge.
	sub, err := c.createSubscription(ctx, req, plan)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("create subscription: %w", err)
	}

	// 2. Initiate the payment prompt. service_payment queues the
	//    prompt to the right integration based on `route` and the
	//    integration emits a status update with the provider handoff
	//    (checkout_url for Polar, "pushed" confirmation for MPESA).
	promptID, err := c.initiatePrompt(ctx, req, plan, route, sub.GetId())
	if err != nil {
		return CheckoutResult{
			Status:         "failed",
			SubscriptionID: sub.GetId(),
			Route:          route,
			Error:          err.Error(),
		}, fmt.Errorf("initiate prompt: %w", err)
	}

	// 3. Poll briefly for a redirect_url (hosted checkout) or
	//    success confirmation (STK push completed synchronously).
	result := CheckoutResult{
		Route:          route,
		PromptID:       promptID,
		SubscriptionID: sub.GetId(),
		AmountCents:    plan.USDCents,
		Currency:       "USD",
	}
	result = c.awaitHandoff(ctx, result)
	return result, nil
}

// createSubscription calls BillingService.CreateSubscription and
// returns the resulting Subscription. The subscription lands in
// SUBSCRIPTION_PENDING until payment succeeds; the reconciler in
// apps/candidates transitions the candidate row to "paid" once the
// server side flips it to SUBSCRIPTION_ACTIVE.
func (c *Client) createSubscription(
	ctx context.Context,
	req CheckoutRequest,
	plan Plan,
) (*billingv1.Subscription, error) {
	// Propagate CF-IPCountry + route hint so the billing catalog can
	// optionally store a tax-jurisdiction tag.
	data, _ := structpb.NewStruct(map[string]any{
		"country":     req.Country,
		"route_hint":  req.RouteHint,
		"plan_tier":   string(plan.ID),
		"plan_usd":    plan.USDCents,
	})

	resp, err := c.billing.CreateSubscription(ctx, connect.NewRequest(&billingv1.CreateSubscriptionRequest{
		ProfileId:        req.ProfileID,
		CatalogVersionId: c.cfg.CatalogVersionID,
		PlanId:           string(plan.ID),
		Currency:         "USD",
		Data:             data,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetData(), nil
}

// initiatePrompt hands the charge off to service_payment, encoding
// `subscription_id` + hosted-checkout hints in the Extra struct so
// the polar prompt-handler (or mpesa/airtel/mtn equivalent) can pick
// them up off the queue.
func (c *Client) initiatePrompt(
	ctx context.Context,
	req CheckoutRequest,
	plan Plan,
	route Route,
	subscriptionID string,
) (string, error) {
	extraMap := map[string]any{
		"subscription_id": subscriptionID,
		"plan_id":         string(plan.ID),
		"country":         req.Country,
	}
	if route == RoutePolar {
		productID := c.cfg.PolarProducts[plan.ID]
		if productID == "" {
			return "", fmt.Errorf("no Polar product_id configured for plan %s", plan.ID)
		}
		extraMap["product_id"] = productID
		extraMap["customer_email"] = req.Email
		extraMap["success_url"] = withSubQuery(c.cfg.SuccessURL, "subscription_id", subscriptionID)
		extraMap["cancel_url"] = c.cfg.CancelURL
	}
	extra, _ := structpb.NewStruct(extraMap)

	source := &commonv1.ContactLink{
		ProfileId: req.ProfileID,
		Detail:    req.Phone,
	}
	recipient := &commonv1.ContactLink{
		ProfileId: c.cfg.RecipientProfileID,
	}

	resp, err := c.payment.InitiatePrompt(ctx, connect.NewRequest(&paymentv1.InitiatePromptRequest{
		Source:      source,
		Recipient:   recipient,
		Amount:      toMoney(plan.USDCents, "USD"),
		DateCreated: time.Now().UTC().Format(time.RFC3339),
		Route:       string(route),
		Extra:       extra,
	}))
	if err != nil {
		return "", err
	}
	return resp.Msg.GetData().GetId(), nil
}

// awaitHandoff polls PaymentService.Status until the integration app
// writes a checkout_url extra (RoutePolar) or flips to SUCCESSFUL
// (mobile money STK succeeded inline), or PollTimeout elapses.
func (c *Client) awaitHandoff(ctx context.Context, r CheckoutResult) CheckoutResult {
	if c.payment == nil {
		r.Status = "pending"
		return r
	}

	deadline := time.Now().Add(c.cfg.PollTimeout)
	for time.Now().Before(deadline) {
		resp, err := c.payment.Status(ctx, connect.NewRequest(&commonv1.StatusRequest{
			Id: r.PromptID,
		}))
		if err != nil {
			util.Log(ctx).WithError(err).Debug("billing: status poll failed")
			time.Sleep(c.cfg.PollInterval)
			continue
		}
		statusResp := resp.Msg
		extras := statusResp.GetExtras().AsMap()

		if url, ok := extras["checkout_url"].(string); ok && url != "" {
			r.RedirectURL = url
			r.Status = "redirect"
			return r
		}
		switch statusResp.GetStatus() {
		case commonv1.STATUS_SUCCESSFUL:
			r.Status = "redirect"
			return r
		case commonv1.STATUS_FAILED:
			if msg, ok := extras["error"].(string); ok {
				r.Error = msg
			}
			r.Status = "failed"
			return r
		}
		select {
		case <-ctx.Done():
			r.Status = "pending"
			return r
		case <-time.After(c.cfg.PollInterval):
		}
	}
	r.Status = "pending"
	return r
}

// FetchSubscription reads a subscription from service_billing. Used
// by the reconciler to refresh the candidate row when a prompt
// flipped to SUCCESSFUL.
func (c *Client) FetchSubscription(ctx context.Context, subscriptionID string) (*billingv1.Subscription, error) {
	if c == nil || c.billing == nil {
		return nil, ErrNotConfigured
	}
	resp, err := c.billing.GetSubscription(ctx, connect.NewRequest(&billingv1.GetSubscriptionRequest{
		Id: subscriptionID,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetData(), nil
}

// CancelSubscription cancels a subscription via service_billing.
func (c *Client) CancelSubscription(ctx context.Context, subscriptionID string) error {
	if c == nil || c.billing == nil {
		return ErrNotConfigured
	}
	_, err := c.billing.CancelSubscription(ctx, connect.NewRequest(&billingv1.CancelSubscriptionRequest{
		Id: subscriptionID,
	}))
	return err
}

// PollPromptStatus exposes Status for the frontend long-poll path.
// Returns the current canonical status (paid / pending / failed) and
// a redirect_url when the Polar handler has finished.
func (c *Client) PollPromptStatus(ctx context.Context, promptID string) (CheckoutResult, error) {
	if c == nil || c.payment == nil {
		return CheckoutResult{}, ErrNotConfigured
	}
	resp, err := c.payment.Status(ctx, connect.NewRequest(&commonv1.StatusRequest{Id: promptID}))
	if err != nil {
		return CheckoutResult{}, err
	}
	r := CheckoutResult{PromptID: promptID, Status: "pending"}
	extras := resp.Msg.GetExtras().AsMap()
	if url, ok := extras["checkout_url"].(string); ok {
		r.RedirectURL = url
	}
	if sid, ok := extras["subscription_id"].(string); ok {
		r.SubscriptionID = sid
	}
	switch resp.Msg.GetStatus() {
	case commonv1.STATUS_SUCCESSFUL:
		r.Status = "paid"
	case commonv1.STATUS_FAILED:
		r.Status = "failed"
		if msg, ok := extras["error"].(string); ok {
			r.Error = msg
		}
	default:
		if r.RedirectURL != "" {
			r.Status = "redirect"
		}
	}
	return r, nil
}

// toMoney converts minor-unit cents to google.type.Money.
// USD cents → units=cents/100, nanos=(cents%100)*1e7.
func toMoney(cents int64, currency string) *money.Money {
	const centsPerUnit = 100
	const nanosPerCent = 10_000_000
	return &money.Money{
		CurrencyCode: currency,
		Units:        cents / centsPerUnit,
		Nanos:        int32((cents % centsPerUnit) * nanosPerCent),
	}
}

// withSubQuery adds or updates `?key=val` on a URL without parsing —
// good enough for a handful of known-good configured URLs, avoiding
// the net/url import + error-propagation cost on the hot path.
func withSubQuery(base, key, val string) string {
	if base == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + key + "=" + val
}

// ErrNotConfigured means the Client was constructed without one of
// the required dependencies (billing client, payment client, catalog
// id, or recipient profile id). Handlers should map this to 503 —
// ops is still wiring the environment, not a 4xx the user can fix.
var ErrNotConfigured = errors.New("billing: not configured")
