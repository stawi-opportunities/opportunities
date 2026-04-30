// Package service contains the Frame Queue consumer for the autoapply service.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// ApplicationRepo is the narrow slice of repository.ApplicationRepository
// the handler needs.
type ApplicationRepo interface {
	ExistsForCandidate(ctx context.Context, candidateID, canonicalJobID string) (bool, error)
	Create(ctx context.Context, app *domain.CandidateApplication) error
}

// MatchRepo is the narrow slice of repository.MatchRepository the
// handler needs.
type MatchRepo interface {
	MarkApplied(ctx context.Context, id string) error
}

// AutoApplyHandler is the Frame Queue consumer for SubjectAutoApplySubmit.
// It downloads the CV, routes through the submitter registry, and persists
// the result.
type AutoApplyHandler struct {
	svc      *frame.Service
	registry *autoapply.Registry
	appRepo  ApplicationRepo
	matchRepo MatchRepo
	http     *http.Client
}

// NewAutoApplyHandler wires the handler.
func NewAutoApplyHandler(
	svc *frame.Service,
	registry *autoapply.Registry,
	appRepo ApplicationRepo,
	matchRepo MatchRepo,
) *AutoApplyHandler {
	return &AutoApplyHandler{
		svc:       svc,
		registry:  registry,
		appRepo:   appRepo,
		matchRepo: matchRepo,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

// Handle implements the Frame queue.SubscribeWorker contract.
// Returns a non-nil error only for transient failures (DB write, emit)
// that should trigger redelivery. Terminal outcomes (skip, form failure)
// return nil so the message is acknowledged and not re-queued.
func (h *AutoApplyHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("autoapply: empty payload")
	}

	var env eventsv1.Envelope[eventsv1.AutoApplyIntentV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("autoapply: decode intent: %w", err)
	}
	intent := env.Payload

	log := util.Log(ctx).
		WithField("candidate_id", intent.CandidateID).
		WithField("canonical_job_id", intent.CanonicalJobID).
		WithField("match_id", intent.MatchID).
		WithField("apply_url", intent.ApplyURL)

	// Idempotency guard — skip if an application record already exists.
	exists, err := h.appRepo.ExistsForCandidate(ctx, intent.CandidateID, intent.CanonicalJobID)
	if err != nil {
		return fmt.Errorf("autoapply: exists check: %w", err)
	}
	if exists {
		log.Debug("autoapply: already applied; skipping")
		return nil
	}

	// Download CV bytes if a URL is provided. Failures are non-fatal —
	// ATS handlers cope with nil CVBytes (some ATS platforms have a
	// separate resume-import step or allow skipping the upload).
	cvBytes, cvFilename := downloadCV(h.http, intent.CVUrl)

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

	result, submitErr := h.registry.Submit(ctx, req)
	if submitErr != nil {
		// Transient failure — record a failed application and return nil
		// (don't redeliver — the failure is already persisted).
		log.WithError(submitErr).Warn("autoapply: submission failed")
		_ = h.persistApplication(ctx, intent, domain.AppStatusFailed, "", "")
		telemetry.RecordAutoApplyAttempt("failed", "error")
		return nil
	}

	status := domain.AppStatusSubmitted
	if result.Method == "skipped" {
		status = domain.AppStatusSkipped
	}

	if err := h.persistApplication(ctx, intent, status, result.Method, result.ExternalRef); err != nil {
		return fmt.Errorf("autoapply: persist application: %w", err)
	}

	if status == domain.AppStatusSubmitted && intent.MatchID != "" {
		if err := h.matchRepo.MarkApplied(ctx, intent.MatchID); err != nil {
			log.WithError(err).Warn("autoapply: mark match applied failed")
			// Non-fatal — application is already recorded.
		}
	}

	telemetry.RecordAutoApplyAttempt(status, result.Method)
	log.WithField("method", result.Method).WithField("status", status).Info("autoapply: attempt complete")

	h.emitApplicationSubmitted(ctx, intent, result, status)
	return nil
}

func (h *AutoApplyHandler) persistApplication(
	ctx context.Context,
	intent eventsv1.AutoApplyIntentV1,
	status, method, externalRef string,
) error {
	now := time.Now().UTC()
	app := &domain.CandidateApplication{
		CandidateID:    intent.CandidateID,
		CanonicalJobID: intent.CanonicalJobID,
		ApplyURL:       intent.ApplyURL,
		Method:         method,
		Status:         status,
		SubmittedAt:    &now,
	}
	if intent.MatchID != "" {
		app.MatchID = &intent.MatchID
	}
	return h.appRepo.Create(ctx, app)
}

func (h *AutoApplyHandler) emitApplicationSubmitted(
	ctx context.Context,
	intent eventsv1.AutoApplyIntentV1,
	result autoapply.SubmitResult,
	status string,
) {
	if h.svc == nil || h.svc.EventsManager() == nil {
		return
	}
	appEnv := eventsv1.NewEnvelope(eventsv1.TopicApplicationSubmitted, eventsv1.ApplicationSubmittedV1{
		CandidateID:    intent.CandidateID,
		MatchID:        intent.MatchID,
		CanonicalJobID: intent.CanonicalJobID,
		Method:         result.Method,
		Status:         status,
		SkipReason:     result.SkipReason,
		ExternalRef:    result.ExternalRef,
		SubmittedAt:    time.Now().UTC(),
	})
	emitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.svc.EventsManager().Emit(emitCtx, eventsv1.TopicApplicationSubmitted, appEnv); err != nil {
		util.Log(ctx).WithError(err).Warn("autoapply: emit ApplicationSubmittedV1 failed")
	}
}

// downloadCV fetches CV bytes from url. Returns (nil, "") on any error
// so callers can proceed with a best-effort submission without a file.
func downloadCV(client *http.Client, url string) ([]byte, string) {
	if strings.TrimSpace(url) == "" {
		return nil, ""
	}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MiB cap
	if err != nil {
		return nil, ""
	}
	// Derive filename from Content-Disposition or URL path.
	name := "resume.pdf"
	if cd := resp.Header.Get("Content-Disposition"); strings.Contains(cd, "filename=") {
		parts := strings.Split(cd, "filename=")
		if len(parts) > 1 {
			name = strings.Trim(parts[1], `"' `)
		}
	} else if idx := strings.LastIndex(url, "/"); idx >= 0 && idx < len(url)-1 {
		candidate := url[idx+1:]
		if strings.Contains(candidate, ".") {
			name = candidate
		}
	}
	return data, name
}
