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
	// checkout_url. 0 uses default (~10s total).
	RedirectPollAttempts int
	// RedirectPollInterval is the sleep between short-polls. 0 uses default.
	RedirectPollInterval time.Duration
}

// paymentGateway implements the Flutterwave-only checkout path:
//
//  1. InitiatePrompt(route=flutterwave, id=chk_…)
//  2. Short-poll Status until a real pay URL (checkout_url) appears
//  3. Return StatusRedirect + RedirectURL so the SPA navigates there
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
		// Flutterwave processes prompts asynchronously on a queue worker.
		// ~10s covers typical queue + provider latency.
		opts.RedirectPollAttempts = 40
	}
	if opts.RedirectPollInterval <= 0 {
		opts.RedirectPollInterval = 250 * time.Millisecond
	}
	return &paymentGateway{pay: pay, opts: opts}
}

func (g *paymentGateway) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	res, err := g.initiateFlutterwave(ctx, req)
	if err != nil {
		return CheckoutResult{}, err
	}
	// Already have a pay URL or a terminal state on the first hop.
	if res.RedirectURL != "" || res.Status == StatusPaid || res.Status == StatusFailed {
		return res, nil
	}
	// Materialise checkout_url in-process so the SPA gets one redirect hop.
	res, err = g.awaitRedirect(ctx, res)
	if err != nil {
		return res, err
	}
	// Without a pay URL the SPA cannot redirect. Surface a clear failure
	// instead of stranding the user on a pending dashboard poller.
	if res.RedirectURL == "" && res.Status != StatusPaid && res.Status != StatusFailed {
		res.Status = StatusFailed
		if res.Error == "" {
			res.Error = "payment page was not ready in time; please try again"
		}
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
	// Prefer email as source detail so empty phone does not dominate.
	sourceDetail := email
	if sourceDetail == "" {
		sourceDetail = phone
	}

	success := g.successURL(promptID)
	extras := map[string]any{
		"plan_id": string(req.Plan.ID),
		// Hosted multipayment page (Flutterwave Standard). Must be "card"
		// or "hosted" — "bank_transfer" is rejected by v4 orchestrator
		// (Invalid value 'bank_transfer' for PaymentMethodIn).
		"payment_method_type": "hosted",
	}
	if email != "" {
		extras["customer_email"] = email
		extras["email"] = email
	}
	if success != "" {
		// Browser return after pay (NOT the pay page itself).
		extras["success_url"] = success
		extras["redirect_url"] = success
	}
	extraStruct, err := structpb.NewStruct(extras)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("billing: build prompt extras: %w", err)
	}

	// Avoid empty recipient account number — service-payment always
	// resolves an Account row for InitiatePrompt.
	accountRef := "stawi-" + req.CandidateID
	if len(accountRef) > 40 {
		accountRef = accountRef[:40]
	}

	in := paymentv1.InitiatePromptRequest_builder{
		Id:    promptID,
		Route: "flutterwave",
		Amount: amount,
		Source: commonv1.ContactLink_builder{
			ProfileId: req.CandidateID,
			// Leave ContactId empty unless we have a real phone — a non-empty
			// ContactId makes Flutterwave prefer MoMo corridors over hosted card.
			ContactId: phone,
			Detail:    sourceDetail,
		}.Build(),
		Recipient: commonv1.ContactLink_builder{
			ContactId: phone,
			Detail:    sourceDetail,
		}.Build(),
		RecipientAccount: paymentv1.Account_builder{
			AccountNumber: accountRef,
			Name:          "stawi-opportunities",
			CountryCode:   "US",
		}.Build(),
		Extra: extraStruct,
	}.Build()

	resp, err := g.pay.InitiatePrompt(ctx, connect.NewRequest(in))
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("billing: initiate prompt: %w", err)
	}
	data := resp.Msg.GetData()
	// Status.ToAPI returns EntityID (= prompt id). Keep our chk_ unless the
	// provider rewrote it to something else.
	if id := data.GetId(); id != "" {
		promptID = id
	}
	st := mapStatus(data.GetStatus())
	redirect := payURLFromExtras(data.GetExtras(), success)
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

// awaitRedirect short-polls Status until Flutterwave materialises a pay URL
// (or we hit the attempt budget / a terminal state).
func (g *paymentGateway) awaitRedirect(ctx context.Context, res CheckoutResult) (CheckoutResult, error) {
	success := g.successURL(res.PromptID)
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
		// Prefer live status extras; fall back to any URL already on the result.
		payURL := st.RedirectURL
		if payURL == "" {
			payURL = res.RedirectURL
		}
		// Filter out our own success URL if it leaked into status extras.
		if payURL != "" && !isReturnURL(payURL, success) {
			res.Status = StatusRedirect
			res.RedirectURL = payURL
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
		if st.Error != "" {
			res.Error = st.Error
		}
	}
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
	success := g.successURL(promptID)
	redirect := payURLFromExtras(data.GetExtras(), success)
	// Keep status as pending while a hosted URL exists so pollers that only
	// look at status keep going — the RedirectURL field is what triggers
	// browser navigation.
	if st == StatusRedirect {
		st = StatusPending
	}
	return StatusResult{
		Status:         st,
		SubscriptionID: data.GetExternalId(),
		RedirectURL:    redirect,
		Error:          firstNonEmpty(extraString(data.GetExtras(), "error"), extraString(data.GetExtras(), "payment_instruction")),
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

// payURLFromExtras extracts the Flutterwave hosted pay page URL.
// Never returns our own success/return URL (those live in the same extras
// keys on some provider echoes).
func payURLFromExtras(s *structpb.Struct, successURL string) string {
	for _, key := range []string{"checkout_url", "payment_link", "link", "hosted_link"} {
		if u := extraString(s, key); u != "" && !isReturnURL(u, successURL) {
			return u
		}
	}
	// Some providers put the pay page under redirect_url — only accept it
	// when it is not our return landing.
	if u := extraString(s, "redirect_url"); u != "" && !isReturnURL(u, successURL) {
		return u
	}
	return ""
}

func isReturnURL(u, successURL string) bool {
	if u == "" {
		return false
	}
	if successURL != "" && (u == successURL || strings.HasPrefix(u, successURL)) {
		return true
	}
	// Our SPA return markers.
	return strings.Contains(u, "billing=success") ||
		strings.Contains(u, "/dashboard/?billing=") ||
		strings.Contains(u, "/dashboard?billing=")
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
