package billing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/util"
)

// EntitlementMirror applies billing lifecycle events to the product entitlement
// cache (candidate_profiles). Implementations live in repository adapters.
type EntitlementMirror interface {
	// ApplyPaid sets subscription=paid, subscription_id, plan_id, period end.
	ApplyPaid(ctx context.Context, entityID, subscriptionID, planID string, periodEnd *time.Time) error
	// ApplyCancelled sets subscription=cancelled (or cancel_at_period_end).
	ApplyCancelled(ctx context.Context, entityID string, atPeriodEnd bool, periodEnd *time.Time) error
	// ApplyPastDue sets subscription=past_due for dunning UX.
	ApplyPastDue(ctx context.Context, entityID, subscriptionID string) error
}

// LifecycleConsumer maps billing lifecycle queue messages onto EntitlementMirror.
// Idempotent: re-delivery with the same event is safe.
type LifecycleConsumer struct {
	Mirror EntitlementMirror
}

// Handle processes one lifecycle event. Unknown events are ignored.
func (c *LifecycleConsumer) Handle(ctx context.Context, evt LifecycleEvent) error {
	if c == nil || c.Mirror == nil {
		return fmt.Errorf("lifecycle consumer not configured")
	}
	entityID := strings.TrimSpace(evt.ExternalEntityID)
	if entityID == "" {
		// Without external entity we cannot map to a product row.
		util.Log(ctx).WithField("event", evt.Event).
			WithField("subscription_id", evt.SubscriptionID).
			Debug("billing lifecycle: no external_entity_id; skip")
		return nil
	}

	switch evt.Event {
	case LifecycleActivated, LifecycleBilled:
		return c.Mirror.ApplyPaid(ctx, entityID, evt.SubscriptionID, evt.PlanID, nil)
	case LifecycleCancelled:
		// Soft-cancel while still ACTIVE is signalled as cancelled event with state ACTIVE.
		atPeriodEnd := strings.EqualFold(evt.State, "ACTIVE") || strings.EqualFold(evt.State, "active")
		return c.Mirror.ApplyCancelled(ctx, entityID, atPeriodEnd, nil)
	case LifecyclePaymentFailed, LifecyclePastDue:
		return c.Mirror.ApplyPastDue(ctx, entityID, evt.SubscriptionID)
	default:
		return nil
	}
}
