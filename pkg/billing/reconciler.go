package billing

import (
	"context"

	"github.com/pitabwire/util"
)

// SubscriptionFinalizer applies cancel-at-period-end when the paid period
// has elapsed. Optional — when nil, Run only reconciles payment checkouts.
type SubscriptionFinalizer interface {
	FinalizeExpiredCancellations(ctx context.Context, limit int) (int64, error)
}

// Reconciler polls the payment provider for pending checkouts and drives
// confirmed ones through the Activator. It is the safety net behind the
// webhook: webhooks drop, and a status the UI never polled to completion
// would otherwise leave a paid candidate stuck on free. Designed to be
// invoked from a Trustage cron (POST /_admin/billing/reconcile) or a
// periodic goroutine.
type Reconciler struct {
	store     CheckoutStore
	gateway   Gateway
	activator *Activator
	finalizer SubscriptionFinalizer
	batch     int
}

// NewReconciler builds a Reconciler. batch bounds how many pending
// checkouts one Run examines; <=0 defaults to 100.
func NewReconciler(store CheckoutStore, gateway Gateway, activator *Activator, batch int) *Reconciler {
	if batch <= 0 {
		batch = 100
	}
	return &Reconciler{store: store, gateway: gateway, activator: activator, batch: batch}
}

// WithFinalizer attaches cancel-at-period-end processing to each Run.
func (r *Reconciler) WithFinalizer(f SubscriptionFinalizer) *Reconciler {
	r.finalizer = f
	return r
}

// ReconcileResult summarises one Run.
type ReconcileResult struct {
	Examined      int   `json:"examined"`
	Activated     int   `json:"activated"`
	Failed        int   `json:"failed"`
	Errors        int   `json:"errors"`
	Cancellations int64 `json:"cancellations_finalized"`
}

// Run sweeps pending checkouts once. Per-checkout errors are logged and
// counted but never abort the sweep — one wedged payment must not block
// the rest. Returns the tallies for the caller (cron handler) to surface.
func (r *Reconciler) Run(ctx context.Context) (ReconcileResult, error) {
	log := util.Log(ctx)
	pending, err := r.store.ListPending(ctx, r.batch)
	if err != nil {
		return ReconcileResult{}, err
	}
	var res ReconcileResult
	res.Examined = len(pending)
	for _, c := range pending {
		st, statusErr := r.gateway.CheckoutStatus(ctx, c.PromptID)
		if statusErr != nil {
			res.Errors++
			log.WithError(statusErr).WithField("prompt_id", c.PromptID).
				Warn("billing: reconcile: status poll failed")
			continue
		}
		switch st.Status {
		case StatusPaid:
			if actErr := r.activator.Activate(ctx, c.PromptID, StatusPaid, st.SubscriptionID, ""); actErr != nil {
				res.Errors++
				log.WithError(actErr).WithField("prompt_id", c.PromptID).
					Warn("billing: reconcile: activation failed")
				continue
			}
			res.Activated++
		case StatusFailed:
			if actErr := r.activator.Activate(ctx, c.PromptID, StatusFailed, st.SubscriptionID, st.Error); actErr != nil {
				res.Errors++
				log.WithError(actErr).WithField("prompt_id", c.PromptID).
					Warn("billing: reconcile: mark-failed failed")
				continue
			}
			res.Failed++
		default:
			// still pending — leave it for the next sweep.
		}
	}

	if r.finalizer != nil {
		n, finErr := r.finalizer.FinalizeExpiredCancellations(ctx, r.batch)
		if finErr != nil {
			res.Errors++
			log.WithError(finErr).Warn("billing: reconcile: cancel finalization failed")
		} else {
			res.Cancellations = n
		}
	}

	log.WithField("examined", res.Examined).
		WithField("activated", res.Activated).
		WithField("failed", res.Failed).
		WithField("cancellations", res.Cancellations).
		WithField("errors", res.Errors).
		Info("billing: reconcile sweep complete")
	return res, nil
}
