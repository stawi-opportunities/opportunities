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

// OneTimeFulfiller runs post-payment work for non-subscription products
// (e.g. ATS report email). Must be idempotent for a given promptID.
type OneTimeFulfiller interface {
	FulfillOneTime(ctx context.Context, candidateID, productID, promptID string) error
}

// Activator resolves a checkout into a candidate subscription. It is the
// single idempotent path that both the webhook and the reconciler call:
//
//  1. mark the checkout row terminal (paid|failed)
//  2. on paid subscription plan, flip candidate_profiles.subscription free→paid
//  3. on paid one-time product, call OneTimeFulfiller (no subscription flip)
//
// Calling Activate twice for the same paid checkout is safe: step 1 is an
// idempotent UPDATE and step 2 no-ops once the candidate already reflects
// the subscription.
type Activator struct {
	store   CheckoutStore
	subs    SubscriptionActivator
	oneTime OneTimeFulfiller
}

// NewActivator builds an Activator.
func NewActivator(store CheckoutStore, subs SubscriptionActivator) *Activator {
	return &Activator{store: store, subs: subs}
}

// WithOneTimeFulfiller registers fulfillment for one-time products (ats_report).
func (a *Activator) WithOneTimeFulfiller(f OneTimeFulfiller) *Activator {
	if a != nil {
		a.oneTime = f
	}
	return a
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

	// One-time products (ATS report) never flip subscription.
	if IsOneTimeProduct(PlanID(row.PlanID)) {
		log.WithField("candidate_id", row.CandidateID).
			WithField("product_id", row.PlanID).
			Info("billing: one-time product paid")
		if a.oneTime != nil {
			if ferr := a.oneTime.FulfillOneTime(ctx, row.CandidateID, row.PlanID, promptID); ferr != nil {
				log.WithError(ferr).Error("billing: one-time fulfill failed")
				return fmt.Errorf("billing: one-time fulfill: %w", ferr)
			}
		}
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
