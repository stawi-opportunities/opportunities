// Package service contains the Frame Queue consumer for the autoapply service.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// SubmitterRouter is the narrow seam between the handler and the
// concrete autoapply.Registry. Tests substitute a fake.
type SubmitterRouter interface {
	Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error)
}

// ApplicationRepo is the slice of repository.ApplicationRepository the
// handler needs.
type ApplicationRepo interface {
	ExistsForCandidate(ctx context.Context, candidateID, canonicalJobID string) (bool, error)
	Create(ctx context.Context, app *domain.CandidateApplication) error
	UpdateStatus(ctx context.Context, id, status, method, externalRef string) error
}

// MatchRepo is the slice of repository.MatchRepository the handler
// needs.
type MatchRepo interface {
	MarkApplied(ctx context.Context, id string) error
}

// Config bundles the runtime knobs that affect handler behaviour.
type Config struct {
	Enabled            bool
	DryRun             bool
	DailyLimitBackstop int
	ScoreMinBackstop   float64
}

// AutoApplyHandler is the Frame Queue consumer for SubjectAutoApplySubmit.
// It downloads the CV, routes through the submitter registry, and
// persists the result.
type AutoApplyHandler struct {
	svc       *frame.Service
	router    SubmitterRouter
	appRepo   ApplicationRepo
	matchRepo MatchRepo
	cv        CVFetcher
	cfg       Config
}

// HandlerDeps bundles the constructor arguments. Use named fields so
// later additions don't break call sites.
type HandlerDeps struct {
	Svc       *frame.Service
	Router    SubmitterRouter
	AppRepo   ApplicationRepo
	MatchRepo MatchRepo
	CV        CVFetcher
	Config    Config
}

// NewAutoApplyHandler wires the handler.
func NewAutoApplyHandler(d HandlerDeps) *AutoApplyHandler {
	return &AutoApplyHandler{
		svc:       d.Svc,
		router:    d.Router,
		appRepo:   d.AppRepo,
		matchRepo: d.MatchRepo,
		cv:        d.CV,
		cfg:       d.Config,
	}
}

// Handle implements the Frame queue.SubscribeWorker contract.
//
// Returns a non-nil error to signal *transient infra* failures
// (DB outage, NATS outage, browser launch failure) so the queue
// redelivers. Idempotency is enforced via a unique partial index on
// (candidate_id, canonical_job_id) plus a "pending" row inserted
// before the submitter runs — so a redelivery during a transient
// failure is safe.
//
// Terminal outcomes (skip, captcha, unsupported, form failure, unsafe
// CV URL, candidate already applied) return nil so the message is
// acknowledged and not re-queued.
func (h *AutoApplyHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("autoapply: empty payload")
	}

	var env eventsv1.Envelope[eventsv1.AutoApplyIntentV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		// Malformed payload — ack so the bad message doesn't loop
		// forever, but log loudly.
		util.Log(ctx).WithError(err).Error("autoapply: decode intent (acked, bad payload)")
		return nil
	}
	intent := env.Payload

	log := util.Log(ctx).
		WithField("event_id", env.EventID).
		WithField("candidate_id", intent.CandidateID).
		WithField("canonical_job_id", intent.CanonicalJobID).
		WithField("match_id", intent.MatchID).
		WithField("apply_url_host", urlHost(intent.ApplyURL)).
		WithField("score", intent.Score)

	if !h.cfg.Enabled {
		log.Info("autoapply: disabled by config; acking intent")
		return nil
	}
	if intent.CandidateID == "" || intent.CanonicalJobID == "" {
		log.Warn("autoapply: missing required fields (acked)")
		return nil
	}
	if h.cfg.ScoreMinBackstop > 0 && intent.Score < h.cfg.ScoreMinBackstop {
		log.Info("autoapply: score below backstop; acking")
		return nil
	}

	started := time.Now()

	// Fast-path idempotency check before any I/O.
	exists, err := h.appRepo.ExistsForCandidate(ctx, intent.CandidateID, intent.CanonicalJobID)
	if err != nil {
		telemetry.RecordAutoApplyTransientError("exists_check")
		return fmt.Errorf("autoapply: exists check: %w", err)
	}
	if exists {
		log.Info("autoapply: already applied; skipping")
		return nil
	}

	// Reserve the slot with a pending row before any submission. This
	// closes the TOCTOU race: two concurrent intents for the same
	// candidate × job both call Create, the unique partial index lets
	// exactly one through, and the loser is treated as "already in
	// flight" without ever calling the ATS.
	pending := &domain.CandidateApplication{
		CandidateID:    intent.CandidateID,
		CanonicalJobID: intent.CanonicalJobID,
		ApplyURL:       intent.ApplyURL,
		Method:         "auto",
		Status:         domain.AppStatusPending,
	}
	if intent.MatchID != "" {
		pending.MatchID = &intent.MatchID
	}
	if err := h.appRepo.Create(ctx, pending); err != nil {
		if isUniqueViolation(err) {
			log.Info("autoapply: race-dup pending row; another worker is in flight")
			return nil
		}
		telemetry.RecordAutoApplyTransientError("persist")
		return fmt.Errorf("autoapply: insert pending: %w", err)
	}

	// Download CV bytes if a URL is provided. ErrCVUnsafeURL is terminal
	// (candidate-supplied bad URL); everything else is a soft failure
	// — submitters cope with nil CVBytes for ATSes that allow
	// resume-on-file or skipping the upload.
	var (
		cvBytes    []byte
		cvFilename string
	)
	if h.cv != nil {
		cvBytes, cvFilename, err = h.cv.Fetch(ctx, intent.CVUrl)
		if err != nil {
			if errors.Is(err, ErrCVUnsafeURL) {
				log.WithError(err).Warn("autoapply: unsafe CV URL; failing application")
				h.finalise(ctx, log, pending, intent, autoapply.SubmitResult{
					Method:     "skipped",
					SkipReason: "unsafe_cv_url",
				}, domain.AppStatusFailed, started)
				return nil
			}
			log.WithError(err).Warn("autoapply: CV download failed; proceeding without CV")
		}
	}

	req := autoapply.SubmitRequest{
		SourceType:   domain.SourceType(intent.SourceType),
		ApplyURL:     intent.ApplyURL,
		CandidateID:  intent.CandidateID,
		FullName:     intent.FullName,
		Email:        intent.Email,
		Phone:        intent.Phone,
		Location:     intent.Location,
		CurrentTitle: intent.CurrentTitle,
		Skills:       intent.Skills,
		CoverLetter:  intent.CoverLetter,
		CVBytes:      cvBytes,
		CVFilename:   cvFilename,
	}

	// Dry-run short-circuits the submitter so a developer can drive
	// the full pipeline (queue → DB → analytics emit) without sending
	// any real browser traffic. Persisted as "skipped/dry_run".
	if h.cfg.DryRun {
		log.Info("autoapply: dry-run; skipping submitter")
		h.finalise(ctx, log, pending, intent,
			autoapply.SubmitResult{Method: "skipped", SkipReason: "dry_run"},
			domain.AppStatusSkipped, started)
		return nil
	}

	result, submitErr := h.router.Submit(ctx, req)
	if submitErr != nil {
		// Treat browser/network failures as transient: redelivery is
		// safe (the pending row blocks duplicate submits via the unique
		// index, and the submitter checks page state before retrying).
		log.WithError(submitErr).Warn("autoapply: transient submit error; will redeliver")
		telemetry.RecordAutoApplyTransientError("submit")
		// Drop the pending row to free the slot for the next attempt.
		_ = h.appRepo.UpdateStatus(ctx, pending.ID, domain.AppStatusFailed, "error", "")
		telemetry.RecordAutoApplySubmitDuration(time.Since(started).Seconds(), "failed", "error")
		return submitErr
	}

	status := domain.AppStatusSubmitted
	if result.Method == "skipped" {
		status = domain.AppStatusSkipped
	}
	h.finalise(ctx, log, pending, intent, result, status, started)
	return nil
}

// finalise updates the pending row with the terminal status, marks the
// upstream match (when applicable), records metrics, and emits the
// downstream ApplicationSubmittedV1 event. Failures inside finalise
// are logged but not propagated — the pending row already records
// the truth and the queue should not redeliver after this point.
func (h *AutoApplyHandler) finalise(
	ctx context.Context,
	log *util.LogEntry,
	pending *domain.CandidateApplication,
	intent eventsv1.AutoApplyIntentV1,
	result autoapply.SubmitResult,
	status string,
	started time.Time,
) {
	if err := h.appRepo.UpdateStatus(ctx, pending.ID, status, result.Method, result.ExternalRef); err != nil {
		log.WithError(err).Warn("autoapply: update status failed")
	}

	if status == domain.AppStatusSubmitted && intent.MatchID != "" && h.matchRepo != nil {
		if err := h.matchRepo.MarkApplied(ctx, intent.MatchID); err != nil {
			log.WithError(err).Warn("autoapply: mark match applied failed")
		}
	}

	telemetry.RecordAutoApplyAttempt(status, result.Method)
	telemetry.RecordAutoApplySubmitDuration(time.Since(started).Seconds(), status, result.Method)

	log.WithField("method", result.Method).
		WithField("status", status).
		WithField("skip_reason", result.SkipReason).
		WithField("duration_ms", time.Since(started).Milliseconds()).
		Info("autoapply: attempt complete")

	h.emitApplicationSubmitted(ctx, intent, result, status, pending.ID)
}

func (h *AutoApplyHandler) emitApplicationSubmitted(
	ctx context.Context,
	intent eventsv1.AutoApplyIntentV1,
	result autoapply.SubmitResult,
	status, applicationID string,
) {
	if h.svc == nil || h.svc.EventsManager() == nil {
		return
	}
	appEnv := eventsv1.NewEnvelope(eventsv1.TopicApplicationSubmitted, eventsv1.ApplicationSubmittedV1{
		CandidateID:    intent.CandidateID,
		ApplicationID:  applicationID,
		MatchID:        intent.MatchID,
		CanonicalJobID: intent.CanonicalJobID,
		Method:         result.Method,
		Status:         status,
		SkipReason:     result.SkipReason,
		ExternalRef:    result.ExternalRef,
		SubmittedAt:    time.Now().UTC(),
	})
	// Detach from the parent so a queue-handler ctx cancel after we've
	// already persisted doesn't drop the analytics emit — but inherit
	// the parent's logger / trace via util.NewContext-style hand-off
	// is not in scope here; we just use a fresh context with timeout.
	emitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := h.svc.EventsManager().Emit(emitCtx, eventsv1.TopicApplicationSubmitted, appEnv); err != nil {
		util.Log(ctx).WithError(err).Warn("autoapply: emit ApplicationSubmittedV1 failed")
	}
}

// isUniqueViolation reports whether err is the postgres "duplicate key"
// (SQLSTATE 23505) thrown by the partial unique index on
// (candidate_id, canonical_job_id). Other backends would need their
// own discriminator.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	// Fallback string match for drivers that don't surface a typed
	// error — defensive only.
	return strings.Contains(strings.ToLower(err.Error()), "duplicate key value")
}

// urlHost returns the host portion of a URL for log context, falling
// back to a short tag when parsing fails.
func urlHost(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "unparsed"
	}
	return u.Host
}
