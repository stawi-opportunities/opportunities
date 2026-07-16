package billing

import (
	"context"
	"fmt"
	"net/url"
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
	// https://opportunities.stawi.org). Builds success_url after pay.
	PublicSiteURL string
	// RedirectPollAttempts short-polls Status after InitiatePrompt for
	// checkout_url. 0 uses default (~3s total).
	RedirectPollAttempts int
	// RedirectPollInterval is the sleep between short-polls. 0 uses default.
	RedirectPollInterval time.Duration
}

// paymentGateway implements the Flutterwave-only checkout path:
//
//  1. InitiatePrompt(route=flutterwave, id=chk_…)
//  2. Short-poll Status until extras.checkout_url is set
//  3. Return StatusRedirect + RedirectURL for the SPA to navigate
//  4. Flutterwave returns the browser to success_url
//     (…/dashboard/?billing=success&prompt_id=chk_…)
//  5. Webhook / status poll / reconciler activate the subscription
type paymentGateway struct {
	pay  PaymentClient
	opts GatewayOptions
}

// NewPaymentGateway builds a Gateway backed by a PaymentServiceClient.
func NewPaymentGateway(pay PaymentClient, opts GatewayOptions) Gateway {
	if opts.RedirectPollAttempts <= 0 {
		opts.RedirectPollAttempts = 15
	}
	if opts.RedirectPollInterval <= 0 {
		opts.RedirectPollInterval = 200 * time.Millisecond
	}
	return &paymentGateway{pay: pay, opts: opts}
}

func (g *paymentGateway) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	res, err := g.initiateFlutterwave(ctx, req)
	if err != nil {
		return CheckoutResult{}, err
	}
	// Step 2: materialise checkout_url in-process so the SPA gets one
	// redirect hop (no intermediate dashboard pending for the happy path).
	if res.Status == StatusPending && res.RedirectURL == "" {
		return g.awaitRedirect(ctx, res)
	}
	return res, nil
}

func (g *paymentGateway) initiateFlutterwave(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	// Correlation id — also embedded in success_url so the return landing
	// can activate without relying on localStorage alone.
	promptID := "chk_" + xid.New().String()
	amount := &money.Money{
		CurrencyCode: req.Plan.Currency,
		Units:        int64(req.Plan.USDCents / 100),
		Nanos:        int32((req.Plan.USDCents % 100) * 10_000_000),
	}

	phone := strings.TrimSpace(req.Phone)
	email := strings.TrimSpace(req.Email)
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
	if success := g.successURL(promptID); success != "" {
		// Flutterwave reads success_url / redirect_url for the browser return.
		extras["success_url"] = success
		extras["redirect_url"] = success
	}
	extraStruct, err := structpb.NewStruct(extras)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("billing: build prompt extras: %w", err)
	}

	in := paymentv1.InitiatePromptRequest_builder{
		Id:     promptID,
		Route:  "flutterwave",
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
	// Prefer our chk_ id (success_url is keyed on it). Only adopt provider
	// id when they rewrite it and it is non-empty.
	if id := data.GetId(); id != "" && id != promptID {
		// Provider assigned a different id — keep both worlds working:
		// ledger uses provider id if that is what Status/webhook use.
		promptID = id
	}
	st := mapStatus(data.GetStatus())
	redirect := redirectFromExtras(data.GetExtras())
	errMsg := extraString(data.GetExtras(), "error")
	if redirect != "" && st != StatusFailed && st != StatusPaid {
		st = StatusRedirect
	}
	if st == StatusRedirect && redirect == "" {
		st = StatusPending
	}
	return CheckoutResult{
		Status:         st,
		Route:          RouteFlutterwave,
		PromptID:       promptID,
		SubscriptionID: data.GetExternalId(),
		RedirectURL:    redirect,
		Error:          errMsg,
	}, nil
}

// successURL is the fixed return target after Flutterwave payment.
// prompt_id is included so the dashboard can activate even if localStorage
// was cleared mid-checkout.
func (g *paymentGateway) successURL(promptID string) string {
	site := strings.TrimRight(strings.TrimSpace(g.opts.PublicSiteURL), "/")
	if site == "" {
		return ""
	}
	u, err := url.Parse(site + "/dashboard/")
	if err != nil {
		return site + "/dashboard/?billing=success&prompt_id=" + url.QueryEscape(promptID)
	}
	q := u.Query()
	q.Set("billing", "success")
	if promptID != "" {
		q.Set("prompt_id", promptID)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// awaitRedirect short-polls Status until Flutterwave materialises
// checkout_url (or we hit the attempt budget / a terminal state).
func (g *paymentGateway) awaitRedirect(ctx context.Context, res CheckoutResult) (CheckoutResult, error) {
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
	// Still pending — rare; SPA PendingCheckoutPoller recovers.
	res.Route = RouteFlutterwave
	return res, nil
}

func (g *paymentGateway) CheckoutStatus(ctx context.Context, promptID string) (StatusResult, error) {
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

func redirectFromExtras(s *structpb.Struct) string {
	if u := extraString(s, "checkout_url"); u != "" {
		return u
	}
	// Do not treat success_url/redirect_url extras as the hosted pay page —
	// those are our return targets, not Flutterwave's charge URL.
	return ""
}

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
