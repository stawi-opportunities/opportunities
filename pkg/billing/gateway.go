package billing

import "context"

// Status is the checkout lifecycle the UI understands.
type Status string

const (
	// StatusRedirect — hosted Flutterwave URL ready; SPA navigates to RedirectURL.
	StatusRedirect Status = "redirect"
	// StatusPending — URL not ready yet, or payment in flight. SPA polls status.
	StatusPending Status = "pending"
	// StatusPaid — provider confirmed success (activate subscription).
	StatusPaid Status = "paid"
	// StatusFailed — provider reported failure / cancel.
	StatusFailed Status = "failed"
)

// Route is always Flutterwave for opportunities.
type Route string

const RouteFlutterwave Route = "FLUTTERWAVE"

// CheckoutRequest is the input to start a Flutterwave payment.
type CheckoutRequest struct {
	CandidateID string
	Plan        Plan
	// Country is ledger/analytics only (not used for routing).
	Country string
	Email   string
	Phone   string
}

// CheckoutResult is the outcome of starting a payment.
type CheckoutResult struct {
	Status         Status
	Route          Route
	PromptID       string
	SubscriptionID string
	// RedirectURL is the Flutterwave hosted pay page (StatusRedirect).
	RedirectURL string
	Error       string
}

// StatusResult is the outcome of polling a payment.
type StatusResult struct {
	Status         Status
	RedirectURL    string
	SubscriptionID string
	Error          string
}

// Gateway abstracts the payment provider.
type Gateway interface {
	CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error)
	CheckoutStatus(ctx context.Context, promptID string) (StatusResult, error)
}
