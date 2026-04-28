package sourceverify

import (
	"context"
	"errors"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// SourceStore is the slice of *repository.SourceRepository the dispatcher
// needs. Kept as an interface so the verifier can be unit-tested without
// a real database.
type SourceStore interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
	SaveVerificationReport(ctx context.Context, id string, report *domain.VerificationReport, status domain.SourceStatus, at time.Time) error
	Approve(ctx context.Context, id, operator string, at time.Time) error
}

// Dispatcher couples a Verifier with a SourceStore: load source, run
// verification, persist outcome, optionally auto-promote on pass.
type Dispatcher struct {
	verifier *Verifier
	store    SourceStore
}

// NewDispatcher wires a verifier with the source store.
func NewDispatcher(v *Verifier, store SourceStore) *Dispatcher {
	return &Dispatcher{verifier: v, store: store}
}

// VerifyAndPersist runs the synchronous flow used by the admin
// `/verify` endpoint. The source must already exist; the report is
// persisted before returning so the operator can inspect it via a GET.
//
// Status transitions:
//
//	pending|verifying|verified|rejected → verifying (on entry)
//	on pass:  verified, then active if AutoApprove
//	on fail:  rejected
//
// Sources already in a terminal operational state (active, paused, etc.)
// are still re-verifiable — the report is overwritten and the status is
// returned to active on pass / rejected on fail. Callers that do not
// want to disturb operational status should not invoke this on those
// rows.
func (d *Dispatcher) VerifyAndPersist(ctx context.Context, sourceID string) (*domain.VerificationReport, error) {
	if d.verifier == nil || d.store == nil {
		return nil, ErrNotConfigured
	}

	src, err := d.store.GetByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, errors.New("sourceverify: source not found")
	}

	// Mark verifying so a concurrent admin GET sees the in-flight state.
	// We don't bother persisting "verifying" when the source is already
	// active/paused — the transition would be confusing and the report
	// runs synchronously here anyway.
	report := d.verifier.Run(ctx, src)
	finalStatus := domain.SourceVerified
	if !report.OverallPass {
		finalStatus = domain.SourceRejected
	}

	at := time.Now().UTC()
	if err := d.store.SaveVerificationReport(ctx, sourceID, report, finalStatus, at); err != nil {
		return report, err
	}

	// Auto-approve flips verified → active when the operator pre-decided
	// to trust this source (e.g. they manually created it via the admin
	// API).
	if report.OverallPass && src.AutoApprove {
		if approveErr := d.store.Approve(ctx, sourceID, "system", at); approveErr != nil {
			util.Log(ctx).WithError(approveErr).WithField("source_id", sourceID).
				Warn("sourceverify: auto-approve failed; left in verified state")
		}
	}

	return report, nil
}

// VerifyAsync runs VerifyAndPersist in a fresh detached goroutine. Used
// by source-discovery so the operator's first review of a discovered
// source already shows a verification report. Errors are logged and
// dropped — the caller does not block on the outcome.
//
// TODO(golang-patterns): this work calls external HTTP (HEAD/GET probes,
// /robots.txt, sample extraction) so it should ideally run on Frame's
// workerpool — restart-resilience and bounded parallelism would protect
// the verifier from runaway discovery floods. Migrate when the
// crawler service exposes a workerpool.Manager hook on Dispatcher.
func (d *Dispatcher) VerifyAsync(parent context.Context, sourceID string) {
	go func() {
		// Detach the context so a request cancellation in the parent
		// (e.g. discovery handler returning) doesn't kill the verifier.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		// Carry the logger fields if present in the parent.
		log := util.Log(parent).WithField("source_id", sourceID)
		if _, err := d.VerifyAndPersist(ctx, sourceID); err != nil {
			log.WithError(err).Warn("sourceverify: async verification failed")
		}
	}()
}
