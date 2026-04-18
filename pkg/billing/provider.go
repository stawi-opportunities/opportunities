package billing

import (
	"context"
	"errors"
	"net/http"
)

// EventStatus is the normalised payment/subscription lifecycle state
// that adapters emit — mapped 1:1 onto the existing
// billingWebhookHandler contract in apps/candidates/cmd/main.go.
type EventStatus string

const (
	StatusPaid      EventStatus = "paid"
	StatusActive    EventStatus = "active"
	StatusTrial     EventStatus = "trial"
	StatusCancelled EventStatus = "cancelled"
	StatusExpired   EventStatus = "expired"
	StatusPending   EventStatus = "pending"
	StatusFailed    EventStatus = "failed"
)

// CheckoutRequest is the provider-agnostic input to CreateCheckout.
type CheckoutRequest struct {
	// ProfileID is the authenticated user's profile id. Propagated
	// through to the webhook via metadata / merchant_reference so we
	// can flip the right candidate row to "paid".
	ProfileID string
	// Plan is the selected plan resolved from the catalog.
	Plan Plan
	// Country is the ISO-3166 alpha-2 reported by Cloudflare. Used
	// by the DusuPay adapter to hint the right mobile-money rail.
	Country string
	// Email is used by DusuPay's hosted-checkout as the contact. Not
	// all providers require it; empty is allowed.
	Email string
	// SuccessURL / CancelURL are the redirect targets for the
	// provider's hosted page. Caller supplies absolute URLs rooted on
	// the public site (jobs.stawi.org).
	SuccessURL string
	CancelURL  string
}

// CheckoutResponse is what CreateCheckout returns.
type CheckoutResponse struct {
	// RedirectURL is the provider's hosted-checkout URL. The caller
	// should 303-redirect the browser here.
	RedirectURL string
	// ProviderRef is the provider's session / order id we keep on
	// file for reconciliation. Lands on the candidate row as
	// SubscriptionID when the webhook confirms payment.
	ProviderRef string
	// Provider is the routing label ("dusupay" / "plar") emitted to
	// the client so the frontend can surface provider-specific
	// copy ("You will be taken to DusuPay to pay with M-Pesa…").
	Provider string
	// AmountCents + Currency echo back the resolved price so the
	// frontend can render a confirmation line before the redirect
	// actually happens.
	AmountCents int64
	Currency    string
}

// WebhookEvent is the normalised form every adapter's ParseWebhook
// emits. The candidates service maps Status onto the existing
// CandidateProfile.Subscription enum.
type WebhookEvent struct {
	ProfileID      string
	PlanID         PlanID
	SubscriptionID string
	Status         EventStatus
	// EventID is the provider's unique id for this webhook call.
	// Used by the handler for idempotency — we drop replays silently
	// when we've already processed this id.
	EventID string
	// Raw is the untouched payload for debug logging.
	Raw []byte
}

// Provider is the payment-agnostic contract every adapter satisfies.
// All methods take a context for cancellation; adapters use the
// context-scoped HTTP client where applicable.
type Provider interface {
	// Name identifies the provider in logs and metrics ("dusupay",
	// "plar"). The frontend also reads this to switch copy.
	Name() string
	// CreateCheckout opens a hosted-payment session and returns the
	// URL to redirect the user to. The adapter is responsible for
	// encoding {profile_id, plan_id} in the provider's metadata so
	// the webhook can round-trip them.
	CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResponse, error)
	// VerifyWebhook authenticates the inbound webhook request —
	// usually an HMAC over the raw body. Adapters MUST reject any
	// request that fails signature verification before calling
	// ParseWebhook.
	VerifyWebhook(req *http.Request, body []byte) error
	// ParseWebhook decodes a verified webhook body into the
	// normalised WebhookEvent. Returns ErrUnhandledEvent for event
	// types the adapter knows about but intentionally ignores (e.g.
	// charge.pending) so the HTTP handler can 204 without error.
	ParseWebhook(body []byte) (WebhookEvent, error)
}

// ErrUnhandledEvent signals "recognised but ignored" — e.g. a
// charge.pending webhook we ACK with a 204 but take no action on.
var ErrUnhandledEvent = errors.New("webhook event type intentionally not handled")

// ErrSignatureInvalid is the canonical error adapters return when
// webhook signature verification fails. The handler maps this to 401.
var ErrSignatureInvalid = errors.New("webhook signature invalid")
