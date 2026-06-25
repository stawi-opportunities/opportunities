package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/pitabwire/util"
)

// SubscriptionActivator flips a candidate's subscription to paid. Satisfied
// in production by an adapter over repository.CandidateRepository's
// ActivateSubscription. The bool reports whether the candidate row actually
// changed, so the activator can log idempotent re-fires distinctly.
type SubscriptionActivator interface {
	ActivateSubscription(ctx context.Context, candidateID, subscriptionID, planID string) (changed bool, err error)
}

// CheckoutStore is the persistence surface the activator + reconciler need.
// *Store satisfies it.
type CheckoutStore interface {
	GetByPromptID(ctx context.Context, promptID string) (Checkout, error)
	UpdateStatus(ctx context.Context, promptID string, status Status, subscriptionID, errMsg string) (Checkout, error)
	ListPending(ctx context.Context, limit int) ([]Checkout, error)
}

// Activator resolves a checkout into a candidate subscription. It is the
// single idempotent path that both the webhook and the reconciler call:
//
//  1. mark the checkout row terminal (paid|failed)
//  2. on paid, flip candidate_profiles.subscription free→paid + set
//     subscription_id / plan_id (idempotent at the repo layer)
//
// Calling Activate twice for the same paid checkout is safe: step 1 is an
// idempotent UPDATE and step 2 no-ops once the candidate already reflects
// the subscription.
type Activator struct {
	store CheckoutStore
	subs  SubscriptionActivator
}

// NewActivator builds an Activator.
func NewActivator(store CheckoutStore, subs SubscriptionActivator) *Activator {
	return &Activator{store: store, subs: subs}
}

// Activate applies a resolved payment status to the checkout identified by
// promptID. subscriptionID may be empty (falls back to the stored value).
// Only StatusPaid triggers the candidate flip; StatusFailed just records
// the terminal state. Non-terminal statuses are a no-op.
func (a *Activator) Activate(ctx context.Context, promptID string, status Status, subscriptionID, errMsg string) error {
	log := util.Log(ctx).WithField("prompt_id", promptID)

	switch status {
	case StatusPaid, StatusFailed:
		// terminal — proceed
	default:
		return nil // pending/redirect carry no activation
	}

	row, err := a.store.UpdateStatus(ctx, promptID, status, subscriptionID, errMsg)
	if errors.Is(err, ErrNotFound) {
		// A webhook for a checkout we never recorded (or a different
		// service's prompt). Not our row — ignore rather than error so a
		// shared webhook endpoint stays tolerant.
		log.Warn("billing: activate: no checkout row for prompt; ignoring")
		return nil
	}
	if err != nil {
		return fmt.Errorf("billing: activate update status: %w", err)
	}

	if status != StatusPaid {
		log.WithField("status", string(status)).Info("billing: checkout resolved non-paid")
		return nil
	}

	subID := subscriptionID
	if subID == "" {
		subID = row.SubscriptionID
	}
	changed, err := a.subs.ActivateSubscription(ctx, row.CandidateID, subID, row.PlanID)
	if err != nil {
		return fmt.Errorf("billing: activate subscription: %w", err)
	}
	log.WithField("candidate_id", row.CandidateID).
		WithField("plan_id", row.PlanID).
		WithField("changed", changed).
		Info("billing: subscription activated")
	return nil
}
