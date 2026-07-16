package billing

import (
	"context"
	"fmt"
	"strings"
)

// CollectionClient is the product-facing surface for service-billing
// CollectionService (StartSubscription / ConfirmPayment / CancelSubscription).
// Recurring rebills are owned by billing's RenewalSweeper — products only
// start, confirm, cancel, and mirror lifecycle events into entitlement cache.
type CollectionClient interface {
	StartSubscription(ctx context.Context, in StartSubscriptionRequest) (StartSubscriptionResult, error)
	ConfirmPayment(ctx context.Context, sessionRef string) (ConfirmPaymentResult, error)
	CancelSubscription(ctx context.Context, subscriptionID string) (CancelSubscriptionResult, error)
}

// StartSubscriptionRequest mirrors CollectionService.StartSubscription.
type StartSubscriptionRequest struct {
	ProfileID          string
	PlanID             string
	CatalogVersionID   string
	Currency           string
	ReturnURL          string
	PayerDisplayName   string
	Methods            []string
	ExternalEntityID   string
	ExternalEntityType string
	IntegrationRouteID string
	Metadata           map[string]string
}

// StartSubscriptionResult is the redirect / already-complete outcome.
type StartSubscriptionResult struct {
	PageURL         string
	SessionRef      string
	InvoiceID       string
	SubscriptionID  string
	AlreadyComplete bool
}

// ConfirmPaymentResult settles a hosted session.
type ConfirmPaymentResult struct {
	InvoiceID         string
	InvoiceState      string
	SubscriptionID    string
	SubscriptionState string
	Paid              bool
}

// CancelSubscriptionResult is soft-cancel for ACTIVE (end of period) or hard for PENDING.
type CancelSubscriptionResult struct {
	SubscriptionID    string
	SubscriptionState string
	VoidedInvoiceID   string
}

// NopCollectionClient degrades gracefully when billing Collection is not wired.
type NopCollectionClient struct{}

func (NopCollectionClient) StartSubscription(context.Context, StartSubscriptionRequest) (StartSubscriptionResult, error) {
	return StartSubscriptionResult{}, fmt.Errorf("%w: collection client not configured", ErrGatewayUnavailable)
}

func (NopCollectionClient) ConfirmPayment(context.Context, string) (ConfirmPaymentResult, error) {
	return ConfirmPaymentResult{}, fmt.Errorf("%w: collection client not configured", ErrGatewayUnavailable)
}

func (NopCollectionClient) CancelSubscription(context.Context, string) (CancelSubscriptionResult, error) {
	return CancelSubscriptionResult{}, fmt.Errorf("%w: collection client not configured", ErrGatewayUnavailable)
}

// ValidateStartSubscriptionInput checks required fields before RPC.
func ValidateStartSubscriptionInput(in StartSubscriptionRequest) error {
	if strings.TrimSpace(in.ProfileID) == "" {
		return fmt.Errorf("profile_id required")
	}
	if strings.TrimSpace(in.PlanID) == "" {
		return fmt.Errorf("plan_id required")
	}
	if strings.TrimSpace(in.CatalogVersionID) == "" {
		return fmt.Errorf("catalog_version_id required")
	}
	cur := strings.ToUpper(strings.TrimSpace(in.Currency))
	if len(cur) != 3 {
		return fmt.Errorf("currency must be ISO-4217 (3 letters)")
	}
	return nil
}

// LifecycleEventName values published by service-billing to product queues.
const (
	LifecycleCreated       = "subscription.created"
	LifecycleActivated     = "subscription.activated"
	LifecycleCancelled     = "subscription.cancelled"
	LifecycleBilled        = "subscription.billed"
	LifecyclePaymentFailed = "subscription.payment_failed"
	LifecyclePastDue       = "subscription.past_due"
)

// LifecycleEvent is the payload product workers consume for entitlement cache.
type LifecycleEvent struct {
	Event              string `json:"event"`
	SubscriptionID     string `json:"subscription_id"`
	ProfileID          string `json:"profile_id"`
	PlanID             string `json:"plan_id"`
	ExternalEntityID   string `json:"external_entity_id,omitempty"`
	ExternalEntityType string `json:"external_entity_type,omitempty"`
	InvoiceID          string `json:"invoice_id,omitempty"`
	State              string `json:"state,omitempty"`
}

// EntitlementStatus maps lifecycle events to local subscription cache values.
func EntitlementStatus(event string) string {
	switch event {
	case LifecycleActivated, LifecycleBilled:
		return "paid"
	case LifecycleCancelled:
		return "cancelled"
	case LifecyclePaymentFailed, LifecyclePastDue:
		return "past_due"
	default:
		return ""
	}
}
