package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeMirror struct {
	paid     int
	cancel   int
	pastDue  int
	lastEnt  string
	lastSub  string
	lastPlan string
}

func (f *fakeMirror) ApplyPaid(_ context.Context, entityID, subscriptionID, planID string, _ *time.Time) error {
	f.paid++
	f.lastEnt = entityID
	f.lastSub = subscriptionID
	f.lastPlan = planID
	return nil
}

func (f *fakeMirror) ApplyCancelled(_ context.Context, entityID string, _ bool, _ *time.Time) error {
	f.cancel++
	f.lastEnt = entityID
	return nil
}

func (f *fakeMirror) ApplyPastDue(_ context.Context, entityID, subscriptionID string) error {
	f.pastDue++
	f.lastEnt = entityID
	f.lastSub = subscriptionID
	return nil
}

func TestLifecycleConsumer_Handle(t *testing.T) {
	t.Parallel()
	m := &fakeMirror{}
	c := &billing.LifecycleConsumer{Mirror: m}
	ctx := context.Background()

	require.NoError(t, c.Handle(ctx, billing.LifecycleEvent{
		Event: billing.LifecycleActivated, ExternalEntityID: "cand_1",
		SubscriptionID: "sub_1", PlanID: "pro",
	}))
	assert.Equal(t, 1, m.paid)
	assert.Equal(t, "cand_1", m.lastEnt)

	require.NoError(t, c.Handle(ctx, billing.LifecycleEvent{
		Event: billing.LifecyclePastDue, ExternalEntityID: "cand_1", SubscriptionID: "sub_1",
	}))
	assert.Equal(t, 1, m.pastDue)

	require.NoError(t, c.Handle(ctx, billing.LifecycleEvent{
		Event: billing.LifecycleCancelled, ExternalEntityID: "cand_1", State: "ACTIVE",
	}))
	assert.Equal(t, 1, m.cancel)

	// No external entity → no-op
	require.NoError(t, c.Handle(ctx, billing.LifecycleEvent{Event: billing.LifecycleBilled}))
	assert.Equal(t, 1, m.paid)
}

func TestEntitlementStatus(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "paid", billing.EntitlementStatus(billing.LifecycleBilled))
	assert.Equal(t, "past_due", billing.EntitlementStatus(billing.LifecyclePastDue))
	assert.Equal(t, "cancelled", billing.EntitlementStatus(billing.LifecycleCancelled))
	assert.Equal(t, "", billing.EntitlementStatus("unknown"))
}

func TestValidateStartSubscriptionInput(t *testing.T) {
	t.Parallel()
	err := billing.ValidateStartSubscriptionInput(billing.StartSubscriptionRequest{})
	require.Error(t, err)
	err = billing.ValidateStartSubscriptionInput(billing.StartSubscriptionRequest{
		ProfileID: "p", PlanID: "pro", CatalogVersionID: "cv", Currency: "USD",
	})
	require.NoError(t, err)
}
