package billing

import (
	"context"
	"fmt"

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

// paymentGateway is the production Gateway. It maps our gateway-neutral
// CheckoutRequest onto the antinvestor payment service's two rails:
//
//   - card / Polar          → CreatePaymentLink (hosted redirect)
//   - M-PESA / Airtel / MTN → InitiatePrompt    (STK push, then poll)
//
// and maps the provider STATE/STATUS back onto our Status enum.
type paymentGateway struct {
	pay PaymentClient
}

// NewPaymentGateway builds a Gateway backed by a PaymentServiceClient.
func NewPaymentGateway(pay PaymentClient) Gateway {
	return &paymentGateway{pay: pay}
}

func (g *paymentGateway) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error) {
	route := RouteForCountry(req.Country, req.RouteHint)
	// Our own correlation id. We thread it into the provider request's
	// external/reference id so the later Status poll resolves it and the
	// webhook/reconciler can match it back to our checkout row.
	promptID := "chk_" + xid.New().String()
	amount := &money.Money{CurrencyCode: req.Plan.Currency, Units: int64(req.Plan.USDCents / 100), Nanos: int32((req.Plan.USDCents % 100) * 10_000_000)}

	if isMobileMoney(route) {
		return g.initiatePrompt(ctx, req, route, promptID, amount)
	}
	return g.createPaymentLink(ctx, req, route, promptID, amount)
}

func (g *paymentGateway) createPaymentLink(ctx context.Context, req CheckoutRequest, route Route, promptID string, amount *money.Money) (CheckoutResult, error) {
	link := paymentv1.PaymentLink_builder{
		Name:         req.Plan.Name + " subscription",
		Description:  req.Plan.Description,
		ExternalRef:  promptID,
		AmountOption: "FIXED",
		Amount:       amount,
		Currency:     req.Plan.Currency,
		SaleType:     "SUBSCRIPTION",
	}.Build()
	customer := paymentv1.Customer_builder{
		Source: commonv1.ContactLink_builder{
			ProfileId: req.CandidateID,
			Detail:    req.Email,
		}.Build(),
		CountryCode:         req.Country,
		CustomerExternalRef: req.CandidateID,
	}.Build()
	in := paymentv1.CreatePaymentLinkRequest_builder{
		Customers:   []*paymentv1.Customer{customer},
		PaymentLink: link,
	}.Build()

	resp, err := g.pay.CreatePaymentLink(ctx, connect.NewRequest(in))
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("billing: create payment link: %w", err)
	}
	data := resp.Msg.GetData()
	// Prefer the provider's own id when it returns one; fall back to ours.
	if id := data.GetId(); id != "" {
		promptID = id
	}
	redirect := extraString(data.GetExtras(), "redirect_url")
	st := mapStatus(data.GetStatus())
	// A freshly-created link with no terminal status is a redirect.
	if st == StatusPending && redirect != "" {
		st = StatusRedirect
	}
	return CheckoutResult{
		Status:         st,
		Route:          route,
		PromptID:       promptID,
		SubscriptionID: data.GetExternalId(),
		RedirectURL:    redirect,
	}, nil
}

func (g *paymentGateway) initiatePrompt(ctx context.Context, req CheckoutRequest, route Route, promptID string, amount *money.Money) (CheckoutResult, error) {
	in := paymentv1.InitiatePromptRequest_builder{
		Id:     promptID,
		Route:  string(route),
		Amount: amount,
		Source: commonv1.ContactLink_builder{
			ProfileId: req.CandidateID,
			Detail:    req.Phone,
		}.Build(),
		Recipient: commonv1.ContactLink_builder{
			Detail: req.Phone,
		}.Build(),
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
	// STK push is never instantly "redirect"; the UI polls until terminal.
	if st == StatusRedirect {
		st = StatusPending
	}
	return CheckoutResult{
		Status:         st,
		Route:          route,
		PromptID:       promptID,
		SubscriptionID: data.GetExternalId(),
	}, nil
}

func (g *paymentGateway) CheckoutStatus(ctx context.Context, promptID string) (StatusResult, error) {
	in := commonv1.StatusRequest_builder{Id: promptID}.Build()
	resp, err := g.pay.Status(ctx, connect.NewRequest(in))
	if err != nil {
		return StatusResult{}, fmt.Errorf("billing: payment status: %w", err)
	}
	data := resp.Msg
	st := mapStatus(data.GetStatus())
	// Status polling only resolves to a terminal or pending state — a
	// "redirect" makes no sense once a payment is in flight.
	if st == StatusRedirect {
		st = StatusPending
	}
	return StatusResult{
		Status:         st,
		SubscriptionID: data.GetExternalId(),
		RedirectURL:    extraString(data.GetExtras(), "redirect_url"),
	}, nil
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
