package billing

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
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
// legacy direct-prompt path uses. Declaring it locally lets tests substitute a
// fake without the full Connect client surface.
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
	// https://opportunities.stawi.org). Builds return_url after pay.
	PublicSiteURL string

	// CheckoutServiceURI is the Connect base for CheckoutService
	// (e.g. https://api.stawi.org or http://service-payment-checkout:80).
	// When set, CreateCheckout opens pay.stawi.org hosted checkout (embedded card)
	// instead of a Flutterwave multipay redirect.
	CheckoutServiceURI string

	// CheckoutPublicBaseURL is optional override when the session response
	// lacks pageUrl (normally checkout returns PublicBaseURL + /c/{ref}).
	CheckoutPublicBaseURL string

	// PreferCard restricts hosted methods to card (embedded) when true.
	// Default true for opportunities subscriptions.
	PreferCard *bool

	// RedirectPollAttempts short-polls Status after InitiatePrompt for
	// checkout_url (legacy path only). 0 uses default (~10s total).
	RedirectPollAttempts int
	// RedirectPollInterval is the sleep between short-polls. 0 uses default.
	RedirectPollInterval time.Duration
}

// paymentGateway implements collection via:
//
//  1. Preferred: CheckoutService.CreateCheckoutSession → pay.stawi.org/c/{ref}
//     (embedded Flutterwave v4 card on our domain; provider-switchable route)
//  2. Fallback: InitiatePrompt(route=flutterwave) + short-poll for hosted URL
//
// On success the browser returns to PublicSiteURL/dashboard/?billing=success&session=…
// and Activate still reconciles via prompt/session status.
type paymentGateway struct {
	pay      PaymentClient
	checkout CheckoutSessionClient
	opts     GatewayOptions
}

// NewPaymentGateway builds a Gateway. When opts.CheckoutServiceURI is set,
// sessions are created on the hosted checkout page (Stripe Link style).
func NewPaymentGateway(pay PaymentClient, opts GatewayOptions) Gateway {
	if opts.RedirectPollAttempts <= 0 {
		opts.RedirectPollAttempts = 40
	}
	if opts.RedirectPollInterval <= 0 {
		opts.RedirectPollInterval = 250 * time.Millisecond
	}
	var co CheckoutSessionClient
	if strings.TrimSpace(opts.CheckoutServiceURI) != "" {
		co = NewHTTPCheckoutClient(opts.CheckoutServiceURI)
	}
	return &paymentGateway{pay: pay, checkout: co, opts: opts}
}

// NewPaymentGatewayWithCheckout injects a custom checkout client (tests).
func NewPaymentGatewayWithCheckout(pay PaymentClient, checkout CheckoutSessionClient, opts GatewayOptions) Gateway {
	if opts.RedirectPollAttempts <= 0 {
		opts.RedirectPollAttempts = 40
	}
	if opts.RedirectPollInterval <= 0 {
		opts.RedirectPollInterval = 250 * time.Millisecond
	}
	return &paymentGateway{pay: pay, checkout: checkout, opts: opts}
}

func (g *paymentGateway) preferCard() bool {
	if g.opts.PreferCard == nil {
		return true
	}
	return *g.opts.PreferCard
}

func (g *paymentGateway) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	// Card-first products must use hosted pay.* (embedded AES-GCM card → v4 OAuth).
	// Direct InitiatePrompt without encrypted card fields cannot charge with
	// OAuth-only credentials and must not fall back to FLWSECK multipay.
	if g.checkout != nil {
		return g.createHostedCheckout(ctx, req)
	}
	if g.preferCard() {
		return CheckoutResult{
			Status: StatusFailed,
			Route:  RouteFlutterwave,
			Error: "checkout is not configured: set CHECKOUT_SERVICE_URI so payers " +
				"open pay.stawi.org (embedded card). OAuth-only Flutterwave cannot " +
				"open Standard multipay without FLWSECK_*",
		}, nil
	}
	// Non-card (e.g. MoMo-only) may still use direct prompts.
	return g.createLegacyPromptCheckout(ctx, req)
}

func (g *paymentGateway) createHostedCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	sessionID := "chk_" + xid.New().String()
	returnURL := g.successURL(sessionID)
	amount := formatPlanAmount(req.Plan)

	methods := []string{"card"}
	if !g.preferCard() {
		methods = nil // all methods for currency
	}

	meta := map[string]string{
		"source":       "opportunities",
		"plan_id":      string(req.Plan.ID),
		"candidate_id": req.CandidateID,
		"prompt_id":    sessionID,
	}
	if req.Email != "" {
		meta["email"] = req.Email
	}
	if req.Country != "" {
		meta["country"] = req.Country
	}

	created, err := g.checkout.CreateSession(ctx, CreateHostedSessionRequest{
		Name:        "Stawi " + string(req.Plan.ID),
		Description: "Subscription — " + string(req.Plan.ID),
		Amount:      amount,
		Currency:    req.Plan.Currency,
		OrderRef:    sessionID,
		ReturnURL:   returnURL,
		ProfileID:   req.CandidateID,
		Email:       req.Email,
		Phone:       req.Phone,
		Methods:     methods,
		Metadata:    meta,
	})
	if err != nil {
		// Never fall back to naked card InitiatePrompt under OAuth — that path
		// only works with FLWSECK multipay or encrypted card extras.
		return CheckoutResult{}, fmt.Errorf("billing: create checkout session: %w", err)
	}

	pageURL := created.PageURL
	if pageURL == "" && g.opts.CheckoutPublicBaseURL != "" && created.Ref != "" {
		pageURL = strings.TrimRight(g.opts.CheckoutPublicBaseURL, "/") + "/c/" + created.Ref
	}
	if pageURL == "" {
		return CheckoutResult{
			Status:   StatusFailed,
			Route:    RouteFlutterwave,
			PromptID: sessionID,
			Error:    "checkout page url missing",
		}, nil
	}

	return CheckoutResult{
		Status:      StatusRedirect,
		Route:       RouteFlutterwave,
		PromptID:    sessionID,
		RedirectURL: pageURL,
	}, nil
}

func (g *paymentGateway) createLegacyPromptCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	res, err := g.initiateFlutterwave(ctx, req)
	if err != nil {
		return CheckoutResult{}, err
	}
	if res.RedirectURL != "" || res.Status == StatusPaid || res.Status == StatusFailed {
		return res, nil
	}
	res, err = g.awaitRedirect(ctx, res)
	if err != nil {
		return res, err
	}
	if res.RedirectURL == "" && res.Status != StatusPaid && res.Status != StatusFailed {
		res.Status = StatusFailed
		if res.Error == "" {
			res.Error = "payment page was not ready in time; please try again"
		}
	}
	return res, nil
}

func (g *paymentGateway) initiateFlutterwave(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	promptID := "chk_" + xid.New().String()
	amount := &money.Money{
		CurrencyCode: req.Plan.Currency,
		Units:        int64(req.Plan.USDCents / 100),
		Nanos:        int32((req.Plan.USDCents % 100) * 10_000_000),
	}

	phone := strings.TrimSpace(req.Phone)
	email := strings.TrimSpace(req.Email)
	sourceDetail := email
	if sourceDetail == "" {
		sourceDetail = phone
	}

	success := g.successURL(promptID)
	extras := map[string]any{
		"plan_id":             string(req.Plan.ID),
		"payment_method_type": "card",
	}
	if email != "" {
		extras["customer_email"] = email
		extras["email"] = email
	}
	if success != "" {
		extras["success_url"] = success
		extras["redirect_url"] = success
	}
	extraStruct, err := structpb.NewStruct(extras)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("billing: build prompt extras: %w", err)
	}

	accountRef := "stawi-" + req.CandidateID
	if len(accountRef) > 40 {
		accountRef = accountRef[:40]
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

// successURL is the fixed return target after payment (hosted checkout or FW).
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
	q.Set("prompt_id", promptID)
	q.Set("session", promptID)
	u.RawQuery = q.Encode()
	return u.String()
}

func (g *paymentGateway) CheckoutStatus(ctx context.Context, promptID string) (StatusResult, error) {
	// Prefer checkout session status when we have a checkout client and the id
	// looks like a session order ref we minted.
	if g.checkout != nil && promptID != "" {
		// Try as session ref first when GetSession works with orderRef mapping —
		// our CreateSession uses OrderRef=chk_*; Get uses session ref from PageURL.
		// Products poll with PromptID we returned (= sessionID / order ref).
		// GetCheckoutSession needs the ref; we store orderRef=promptID but
		// PageURL uses session.Ref. Status via payment Status still works when
		// checkout has set prompt_id on the session.
		if st, err := g.checkout.GetSession(ctx, promptID); err == nil && st.Ref != "" {
			return mapHostedStatus(st), nil
		}
	}
	if g.pay == nil {
		return StatusResult{Status: StatusPending}, nil
	}
	return g.paymentStatus(ctx, promptID)
}

func mapHostedStatus(st HostedSessionStatus) StatusResult {
	switch st.Status {
	case "completed":
		return StatusResult{Status: StatusPaid, SubscriptionID: st.PromptID}
	case "failed", "expired":
		return StatusResult{Status: StatusFailed}
	case "processing":
		return StatusResult{Status: StatusPending}
	default:
		return StatusResult{Status: StatusPending}
	}
}

func (g *paymentGateway) paymentStatus(ctx context.Context, promptID string) (StatusResult, error) {
	// service-payment Status expects entity_type=prompt for collection prompts.
	extras, _ := structpb.NewStruct(map[string]any{"entity_type": "prompt"})
	resp, err := g.pay.Status(ctx, connect.NewRequest(&commonv1.StatusRequest{
		Id:     promptID,
		Extras: extras,
	}))
	if err != nil {
		return StatusResult{}, fmt.Errorf("billing: payment status: %w", err)
	}
	msg := resp.Msg
	st := mapStatus(msg.GetStatus())
	redirect := payURLFromExtras(msg.GetExtras(), "")
	errMsg := extraString(msg.GetExtras(), "error")
	if redirect != "" && st != StatusFailed && st != StatusPaid {
		st = StatusRedirect
	}
	return StatusResult{
		Status:         st,
		RedirectURL:    redirect,
		SubscriptionID: msg.GetExternalId(),
		Error:          errMsg,
	}, nil
}

func (g *paymentGateway) awaitRedirect(ctx context.Context, res CheckoutResult) (CheckoutResult, error) {
	for i := 0; i < g.opts.RedirectPollAttempts; i++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		st, err := g.paymentStatus(ctx, res.PromptID)
		if err != nil {
			// Soft-fail polls; keep waiting.
			time.Sleep(g.opts.RedirectPollInterval)
			continue
		}
		if st.RedirectURL != "" {
			res.Status = StatusRedirect
			res.RedirectURL = st.RedirectURL
			return res, nil
		}
		if st.Status == StatusPaid || st.Status == StatusFailed {
			res.Status = st.Status
			res.Error = st.Error
			res.SubscriptionID = st.SubscriptionID
			return res, nil
		}
		time.Sleep(g.opts.RedirectPollInterval)
	}
	return res, nil
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

func payURLFromExtras(extras *structpb.Struct, successURL string) string {
	if extras == nil {
		return ""
	}
	for _, k := range []string{"checkout_url", "auth_redirect_url", "link", "payment_link"} {
		if u := extraString(extras, k); u != "" && !isOurReturnURL(u, successURL) {
			return u
		}
	}
	return ""
}

func extraString(extras *structpb.Struct, key string) string {
	if extras == nil {
		return ""
	}
	f, ok := extras.GetFields()[key]
	if !ok || f == nil {
		return ""
	}
	return f.GetStringValue()
}

func isOurReturnURL(u, success string) bool {
	if success != "" && strings.HasPrefix(u, success) {
		return true
	}
	return strings.Contains(u, "billing=success") ||
		strings.Contains(u, "/dashboard/?billing=") ||
		strings.Contains(u, "/dashboard?billing=")
}

func formatPlanAmount(plan Plan) string {
	// USDCents → major units with 2 decimal places.
	major := plan.USDCents / 100
	minor := plan.USDCents % 100
	return strconv.FormatInt(int64(major), 10) + "." + fmt.Sprintf("%02d", minor)
}
