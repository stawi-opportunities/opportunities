package billing

import (
	"context"
	"fmt"
	"strings"
	"time"

	commonv1 "buf.build/gen/go/antinvestor/common/protocolbuffers/go/common/v1"
	"buf.build/gen/go/antinvestor/payment/connectrpc/go/v1/paymentv1connect"
	paymentv1 "buf.build/gen/go/antinvestor/payment/protocolbuffers/go/v1"
	"connectrpc.com/connect"
	"github.com/rs/xid"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/structpb"
)

// PaymentClient is the subset of the antinvestor PaymentServiceClient the
// gateway uses. Declaring it locally lets tests substitute a fake without
// the full Connect client surface; the real client satisfies it.
type PaymentClient interface {
	CreatePaymentLink(ctx context.Context, req *connect.Request[paymentv1.CreatePaymentLinkRequest]) (*connect.Response[paymentv1.CreatePaymentLinkResponse], error)
	InitiatePrompt(ctx context.Context, req *connect.Request[paymentv1.InitiatePromptRequest]) (*connect.Response[paymentv1.InitiatePromptResponse], error)
	Status(ctx context.Context, req *connect.Request[commonv1.StatusRequest]) (*connect.Response[commonv1.StatusResponse], error)
}

// Compile-time assertion that the generated client satisfies our subset.
var _ PaymentClient = (paymentv1connect.PaymentServiceClient)(nil)

// GatewayOptions configures the production payment gateway.
type GatewayOptions struct {
	// PublicSiteURL is the candidate-facing origin (e.g.
	// https://opportunities.stawi.org). Used to build Polar success_url.
	PublicSiteURL string
	// PolarProducts maps plan id → Polar product id (prod_…). Required for
	// card/Polar checkout; when empty Polar initiate still runs but the
	// integration will fail until products are provisioned.
	PolarProducts map[PlanID]string
	// RedirectPollAttempts is how many times to short-poll Status after a
	// Polar InitiatePrompt for checkout_url materialisation. 0 uses default.
	RedirectPollAttempts int
	// RedirectPollInterval is the sleep between short-polls. 0 uses default.
	RedirectPollInterval time.Duration
}

// paymentGateway is the production Gateway. It maps our gateway-neutral
// CheckoutRequest onto the antinvestor payment service via InitiatePrompt
// for every rail (docs + polar/mpesa integrations both consume prompts):
//
//   - POLAR              → InitiatePrompt(route=polar) + short-poll checkout_url
//   - M-PESA / Airtel / MTN → InitiatePrompt(route=mpesa|airtel|mtn), STK pending
//
// Route keys are normalised to the service-payment INITIATE_PROMPT_ROUTE_URIS
// map (mpesa, polar, mtn, airtel) — not the UI display strings (M-PESA).
type paymentGateway struct {
	pay  PaymentClient
	opts GatewayOptions
}

// NewPaymentGateway builds a Gateway backed by a PaymentServiceClient.
func NewPaymentGateway(pay PaymentClient, opts GatewayOptions) Gateway {
	if opts.PolarProducts == nil {
		opts.PolarProducts = map[PlanID]string{}
	}
	if opts.RedirectPollAttempts <= 0 {
		opts.RedirectPollAttempts = 15
	}
	if opts.RedirectPollInterval <= 0 {
		opts.RedirectPollInterval = 200 * time.Millisecond
	}
	return &paymentGateway{pay: pay, opts: opts}
}

func (g *paymentGateway) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	route := RouteForCountry(req.Country, req.RouteHint)
	// Our correlation id. Threaded into InitiatePrompt.id so Status +
	// webhook + reconciler all key the same ledger row.
	promptID := "chk_" + xid.New().String()
	amount := &money.Money{
		CurrencyCode: req.Plan.Currency,
		Units:        int64(req.Plan.USDCents / 100),
		Nanos:        int32((req.Plan.USDCents % 100) * 10_000_000),
	}

	res, err := g.initiatePrompt(ctx, req, route, promptID, amount)
	if err != nil {
		return CheckoutResult{}, err
	}
	// Polar creates a hosted checkout asynchronously; short-poll Status so
	// the onboarding button can redirect in one hop when the URL is ready.
	if route == RoutePolar && res.Status == StatusPending && res.RedirectURL == "" {
		return g.awaitRedirect(ctx, route, res)
	}
	return res, nil
}

func (g *paymentGateway) initiatePrompt(
	ctx context.Context,
	req CheckoutRequest,
	route Route,
	promptID string,
	amount *money.Money,
) (CheckoutResult, error) {
	phone := strings.TrimSpace(req.Phone)
	email := strings.TrimSpace(req.Email)
	// M-Pesa (and peers) read phone from ContactId, not Detail.
	sourceDetail := phone
	if sourceDetail == "" {
		sourceDetail = email
	}

	extras := map[string]any{
		"plan_id": string(req.Plan.ID),
	}
	if email != "" {
		extras["customer_email"] = email
	}
	if site := strings.TrimRight(strings.TrimSpace(g.opts.PublicSiteURL), "/"); site != "" {
		extras["success_url"] = site + "/dashboard/?billing=success"
	}
	if route == RoutePolar {
		if pid := strings.TrimSpace(g.opts.PolarProducts[req.Plan.ID]); pid != "" {
			extras["product_id"] = pid
		}
	}
	extraStruct, err := structpb.NewStruct(extras)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("billing: build prompt extras: %w", err)
	}

	in := paymentv1.InitiatePromptRequest_builder{
		Id:     promptID,
		Route:  providerRouteKey(route),
		Amount: amount,
		Source: commonv1.ContactLink_builder{
			ProfileId: req.CandidateID,
			ContactId: phone,
			Detail:    sourceDetail,
		}.Build(),
		Recipient: commonv1.ContactLink_builder{
			ContactId: phone,
			Detail:    phone,
		}.Build(),
		Extra: extraStruct,
	}.Build()

	resp, err := g.pay.InitiatePrompt(ctx, connect.NewRequest(in))
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("billing: initiate prompt: %w", err)
	}
	data := resp.Msg.GetData()
	if id := data.GetId(); id != "" {
		promptID = id
	}
	st := mapStatus(data.GetStatus())
	redirect := redirectFromExtras(data.GetExtras())
	// Hosted checkout URL ready on first response → redirect.
	if redirect != "" && st != StatusFailed && st != StatusPaid {
		st = StatusRedirect
	}
	// STK / async prompt without a URL stays pending for the UI poller.
	if st == StatusRedirect && redirect == "" {
		st = StatusPending
	}
	return CheckoutResult{
		Status:         st,
		Route:          route,
		PromptID:       promptID,
		SubscriptionID: data.GetExternalId(),
		RedirectURL:    redirect,
	}, nil
}

// awaitRedirect short-polls Status until Polar materialises checkout_url
// (or we hit the attempt budget / a terminal state).
func (g *paymentGateway) awaitRedirect(ctx context.Context, route Route, res CheckoutResult) (CheckoutResult, error) {
	for i := 0; i < g.opts.RedirectPollAttempts; i++ {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(g.opts.RedirectPollInterval):
		}
		st, err := g.CheckoutStatus(ctx, res.PromptID)
		if err != nil {
			continue
		}
		if st.RedirectURL != "" {
			res.Status = StatusRedirect
			res.RedirectURL = st.RedirectURL
			if st.SubscriptionID != "" {
				res.SubscriptionID = st.SubscriptionID
			}
			return res, nil
		}
		if st.Status == StatusPaid || st.Status == StatusFailed {
			res.Status = st.Status
			res.SubscriptionID = st.SubscriptionID
			res.Error = st.Error
			return res, nil
		}
	}
	// Still pending — UI PendingCheckoutPoller takes over.
	res.Route = route
	return res, nil
}

func (g *paymentGateway) CheckoutStatus(ctx context.Context, promptID string) (StatusResult, error) {
	// service-payment Status looks up by (entity_id, entity_type). Prompt
	// rows are always entity_type=prompt (see publishPromptStatus).
	extras, err := structpb.NewStruct(map[string]any{"entity_type": "prompt"})
	if err != nil {
		return StatusResult{}, fmt.Errorf("billing: status extras: %w", err)
	}
	in := commonv1.StatusRequest_builder{
		Id:     promptID,
		Extras: extras,
	}.Build()
	resp, err := g.pay.Status(ctx, connect.NewRequest(in))
	if err != nil {
		return StatusResult{}, fmt.Errorf("billing: payment status: %w", err)
	}
	data := resp.Msg
	st := mapStatus(data.GetStatus())
	redirect := redirectFromExtras(data.GetExtras())
	// Once a hosted URL exists the UI should navigate — surface as pending
	// with redirect_url so the poller can open it if the first hop missed it.
	if st == StatusRedirect {
		st = StatusPending
	}
	return StatusResult{
		Status:         st,
		SubscriptionID: data.GetExternalId(),
		RedirectURL:    redirect,
		Error:          extraString(data.GetExtras(), "error"),
	}, nil
}

// providerRouteKey maps UI/product Route constants onto the keys registered
// in service-payment INITIATE_PROMPT_ROUTE_URIS (mpesa, polar, mtn, airtel).
func providerRouteKey(r Route) string {
	switch r {
	case RouteMpesa:
		return "mpesa"
	case RouteAirtel:
		return "airtel"
	case RouteMTN:
		return "mtn"
	case RoutePolar:
		return "polar"
	default:
		return strings.ToLower(strings.TrimSpace(string(r)))
	}
}

// mapStatus folds the provider STATUS enum onto our UI lifecycle.
//
//	SUCCESSFUL            → paid
//	FAILED                → failed
//	QUEUED/IN_PROCESS/... → pending
func mapStatus(s commonv1.STATUS) Status {
	switch s {
	case commonv1.STATUS_SUCCESSFUL:
		return StatusPaid
	case commonv1.STATUS_FAILED:
		return StatusFailed
	default:
		return StatusPending
	}
}

// redirectFromExtras accepts both Polar (checkout_url) and generic
// (redirect_url) provider keys.
func redirectFromExtras(s *structpb.Struct) string {
	if u := extraString(s, "checkout_url"); u != "" {
		return u
	}
	return extraString(s, "redirect_url")
}

// extraString reads a string field from a structpb.Struct, returning ""
// when absent or not a string.
func extraString(s *structpb.Struct, key string) string {
	if s == nil {
		return ""
	}
	v, ok := s.GetFields()[key]
	if !ok {
		return ""
	}
	return v.GetStringValue()
}
