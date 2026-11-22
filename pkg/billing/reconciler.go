package billing

import (
	"context"
	"time"

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
	// StuckActivations: paid checkouts that exhausted activation retries.
	StuckActivations int64 `json:"stuck_activations"`
	// SupersededLast24h: paid checkouts not applied because a later purchase
	// was already active — money taken, nothing granted; review for refund.
	SupersededLast24h int64 `json:"superseded_last_24h"`
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

	// Paid checkouts whose activation failed (DB blip after the ledger
	// write) would otherwise leave a paying user on free forever.
	unactivated, err := r.store.ListPaidUnactivated(ctx, r.batch)
	if err != nil {
		res.Errors++
		log.WithError(err).Warn("billing: reconcile: list paid-unactivated failed")
	}
	for _, c := range unactivated {
		res.Examined++
		if actErr := r.activator.Activate(ctx, c.PromptID, StatusPaid, c.SubscriptionID, ""); actErr != nil {
			res.Errors++
			log.WithError(actErr).WithField("prompt_id", c.PromptID).
				Warn("billing: reconcile: activation retry failed")
			continue
		}
		res.Activated++
	}

	stuck, err := r.store.CountStuckActivations(ctx)
	if err != nil {
		res.Errors++
		log.WithError(err).Warn("billing: reconcile: count stuck activations failed")
	} else if stuck > 0 {
		res.StuckActivations = stuck
		log.WithField("stuck_activations", stuck).
			Error("billing: paid checkouts exhausted activation retries — users paid without entitlement; investigate activation_error")
	}

	superseded, err := r.store.CountSupersededSince(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		res.Errors++
		log.WithError(err).Warn("billing: reconcile: count superseded failed")
	} else if superseded > 0 {
		res.SupersededLast24h = superseded
		log.WithField("superseded_last_24h", superseded).
			Error("billing: paid checkouts superseded by a later purchase — review for refund (activation_outcome='superseded')")
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
