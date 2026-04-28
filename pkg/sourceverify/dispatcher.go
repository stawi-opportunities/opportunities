package sourceverify

import (
	"context"
	"errors"
	"time"

	"github.com/pitabwire/frame/workerpool"
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

// asyncRunner abstracts how VerifyAsync schedules background work.
// The production wiring uses Frame's workerpool (NewDispatcher); the
// test/CLI fallback uses a goroutine (NewDispatcherWithGoroutine).
type asyncRunner func(parent context.Context, run func(ctx context.Context))

// Dispatcher couples a Verifier with a SourceStore: load source, run
// verification, persist outcome, optionally auto-promote on pass.
type Dispatcher struct {
	verifier *Verifier
	store    SourceStore
	runAsync asyncRunner
}

// NewDispatcher wires a verifier with the source store using Frame's
// workerpool for async verification (per the golang-patterns rule:
// no raw goroutines for critical work). The pool is shared with the
// rest of the host service so verification is bounded by the same
// concurrency budget that governs other async tasks.
//
// Pass workerpool.Manager from svc.WorkManager() at construction.
// A nil workMan falls back to a goroutine-based runner so a partially
// initialised host (e.g. in tests / CLI tools that don't boot a full
// Frame service) keeps working — but production callers should always
// supply a real manager.
func NewDispatcher(v *Verifier, store SourceStore, workMan workerpool.Manager) *Dispatcher {
	d := &Dispatcher{verifier: v, store: store}
	if workMan != nil {
		d.runAsync = workerpoolRunner(workMan)
	} else {
		d.runAsync = goroutineRunner()
	}
	return d
}

// NewDispatcherWithGoroutine constructs a Dispatcher whose async path
// runs work on a fresh detached goroutine. Intended for tests + CLI
// tools that don't have a Frame workerpool. Prefer NewDispatcher in
// production code.
func NewDispatcherWithGoroutine(v *Verifier, store SourceStore) *Dispatcher {
	return &Dispatcher{verifier: v, store: store, runAsync: goroutineRunner()}
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

// VerifyAsync schedules VerifyAndPersist on Frame's workerpool (or a
// fallback goroutine when constructed via NewDispatcherWithGoroutine).
// Used by source-discovery so the operator's first review of a
// discovered source already shows a verification report. Errors are
// logged and dropped — the caller does not block on the outcome.
//
// Restart-resilience: the workerpool is process-local, so a restart
// during verification loses the in-flight job. Source-discovery is
// idempotent (re-discovery upserts the same row) and the operator
// can manually trigger via POST /admin/sources/{id}/verify if the
// async run never completes; making this fully durable would require
// promoting verification to a Frame Queue subject, which is a bigger
// refactor and unnecessary for the pre-review UX.
func (d *Dispatcher) VerifyAsync(parent context.Context, sourceID string) {
	d.runAsync(parent, func(ctx context.Context) {
		log := util.Log(parent).WithField("source_id", sourceID)
		if _, err := d.VerifyAndPersist(ctx, sourceID); err != nil {
			log.WithError(err).Warn("sourceverify: async verification failed")
		}
	})
}

// workerpoolRunner builds an asyncRunner that submits work to Frame's
// workerpool. Each VerifyAsync invocation creates a single Job whose
// body runs the supplied function with a detached + timeout-bounded
// context.
func workerpoolRunner(m workerpool.Manager) asyncRunner {
	return func(parent context.Context, run func(ctx context.Context)) {
		job := workerpool.NewJob(func(_ context.Context, _ workerpool.JobResultPipe[struct{}]) error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			run(ctx)
			return nil
		})
		if err := workerpool.SubmitJob(parent, m, job); err != nil {
			util.Log(parent).WithError(err).
				Warn("sourceverify: failed to submit async job to workerpool; falling back to inline goroutine")
			goroutineRunner()(parent, run)
		}
	}
}

// goroutineRunner is the fallback async runner for tests + CLI use.
// It detaches the context so a parent cancellation (e.g. an HTTP
// handler returning) doesn't kill the verifier.
func goroutineRunner() asyncRunner {
	return func(_ context.Context, run func(ctx context.Context)) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			run(ctx)
		}()
	}
}
