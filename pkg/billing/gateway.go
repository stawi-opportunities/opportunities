package billing

import "context"

// Status mirrors ui/app/src/api/billing.ts CheckoutStatus. It is the
// normalised lifecycle the UI understands, derived from the payment
// provider's lower-level state.
type Status string

const (
	// StatusRedirect — a hosted-checkout / payment link was created; the
	// UI must navigate the browser to RedirectURL (card / Polar route).
	StatusRedirect Status = "redirect"
	// StatusPending — payment initiated, awaiting confirmation. Mobile
	// money (STK push) lands here; the UI polls until paid|failed.
	StatusPending Status = "pending"
	// StatusPaid — provider confirmed the payment succeeded.
	StatusPaid Status = "paid"
	// StatusFailed — provider reported the payment failed / was cancelled.
	StatusFailed Status = "failed"
)

// Route identifies the payment rail chosen for a checkout. Mirrors
// ui/app/src/api/billing.ts BillingRoute.
type Route string

const (
	RoutePolar  Route = "POLAR"
	RouteMpesa  Route = "M-PESA"
	RouteAirtel Route = "AIRTEL"
	RouteMTN    Route = "MTN"
)

// CheckoutRequest is the gateway-neutral input to start a payment.
type CheckoutRequest struct {
	CandidateID string
	Plan        Plan
	// Country is the resolved CF-IPCountry (or a hint). Drives route
	// selection: African mobile-money countries → STK push, else card.
	Country string
	Email   string
	Phone   string
	// RouteHint optionally forces a route ("POLAR"/"M-PESA"/...). Empty
	// lets the gateway pick from Country.
	RouteHint string
}

// CheckoutResult is the gateway-neutral outcome of starting a payment.
type CheckoutResult struct {
	Status Status
	Route  Route
	// PromptID is the provider transaction/prompt reference the status
	// poller + webhook + reconciler key on. Always set on success.
	PromptID string
	// SubscriptionID is the provider/billing subscription identifier when
	// the gateway creates one up front; may be empty until activation.
	SubscriptionID string
	// RedirectURL is set when Status == StatusRedirect.
	RedirectURL string
	Error       string
}

// StatusResult is the gateway-neutral result of polling a payment.
type StatusResult struct {
	Status         Status
	RedirectURL    string
	SubscriptionID string
	Error          string
}

// Gateway abstracts the payment provider. The production implementation
// (NewPaymentGateway) wraps the antinvestor PaymentServiceClient; tests
// and unconfigured deployments use fakes / NopGateway.
type Gateway interface {
	// CreateCheckout starts a payment for the given plan and returns the
	// initial lifecycle state (redirect|pending|paid|failed).
	CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error)
	// CheckoutStatus polls the provider for the current state of a
	// previously-created checkout, identified by promptID.
	CheckoutStatus(ctx context.Context, promptID string) (StatusResult, error)
}
