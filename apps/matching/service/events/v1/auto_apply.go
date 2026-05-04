package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// CandidateRepo is the narrow slice of repository.CandidateRepository
// the trigger handler needs.
type CandidateRepo interface {
	GetByID(ctx context.Context, id string) (*domain.CandidateProfile, error)
}

// AppRepo is the narrow slice of repository.ApplicationRepository the
// trigger handler needs.
type AppRepo interface {
	ExistsForCandidate(ctx context.Context, candidateID, canonicalJobID string) (bool, error)
	CountTodayForCandidate(ctx context.Context, candidateID string) (int, error)
}

// MatchLookup is the narrow slice of repository.MatchRepository the
// trigger handler needs to populate MatchID on each intent so the
// downstream autoapply service can mark the match as applied.
type MatchLookup interface {
	GetIDByCandidateJob(ctx context.Context, candidateID, canonicalJobID string) (string, error)
}

// CVStore loads the extracted CV fields for a candidate. The matching
// service already has a candidatestore.Reader; this is the minimal slice
// needed for packing the intent event.
type CVStore interface {
	// LatestExtracted returns name, email, phone for the candidate.
	// Returns zero-value strings when the candidate has no extracted CV.
	LatestExtracted(ctx context.Context, candidateID string) (name, email, phone string, err error)
}

// AutoApplyTriggerDeps bundles collaborators for the handler.
type AutoApplyTriggerDeps struct {
	Svc           *frame.Service
	CandidateRepo CandidateRepo
	AppRepo       AppRepo
	MatchLookup   MatchLookup // optional; without it MatchID is left empty
	CVStore       CVStore     // optional; intent is still published without CV fields
	ScoreMin      float64     // default 0.75
	DailyLimit    int         // default 5
	QueueURL      string      // SubjectAutoApplySubmit publisher URL
	Enabled       bool

	// PublishFn overrides Svc.QueueManager().Publish in tests. When nil,
	// the real Frame QueueManager is used.
	PublishFn func(ctx context.Context, subject string, data []byte) error
}

// AutoApplyTriggerHandler subscribes to TopicCandidateMatchesReady and
// publishes AutoApplyIntentV1 messages for each qualifying match. All
// eligibility checks run here so the autoapply service is purely a
// submitter and recorder.
type AutoApplyTriggerHandler struct {
	deps AutoApplyTriggerDeps
}

// NewAutoApplyTriggerHandler wires the handler.
func NewAutoApplyTriggerHandler(deps AutoApplyTriggerDeps) *AutoApplyTriggerHandler {
	if deps.ScoreMin <= 0 {
		deps.ScoreMin = 0.75
	}
	if deps.DailyLimit <= 0 {
		deps.DailyLimit = 5
	}
	return &AutoApplyTriggerHandler{deps: deps}
}

// Name is the topic this handler subscribes to.
func (h *AutoApplyTriggerHandler) Name() string { return eventsv1.TopicCandidateMatchesReady }

// PayloadType returns the handler's payload type for Frame's JSON-decoded
// event manager.
func (h *AutoApplyTriggerHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ensures the payload is non-empty before Execute runs.
func (h *AutoApplyTriggerHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("auto-apply trigger: empty payload")
	}
	return nil
}

// Execute processes a MatchesReadyV1 event. For each match that passes
// the eligibility + score + idempotency checks, it publishes an
// AutoApplyIntentV1 onto the durable auto-apply queue.
func (h *AutoApplyTriggerHandler) Execute(ctx context.Context, payload any) error {
	if !h.deps.Enabled {
		return nil
	}

	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.MatchesReadyV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("auto-apply trigger: decode: %w", err)
	}
	in := env.Payload
	if in.CandidateID == "" {
		return errors.New("auto-apply trigger: candidate_id required")
	}

	log := util.Log(ctx).WithField("candidate_id", in.CandidateID)

	// Load candidate profile from Postgres.
	profile, err := h.deps.CandidateRepo.GetByID(ctx, in.CandidateID)
	if err != nil {
		log.WithError(err).Warn("auto-apply trigger: load candidate failed")
		return nil
	}
	if profile == nil {
		log.Debug("auto-apply trigger: candidate not found")
		return nil
	}

	// Eligibility gate.
	if !profile.AutoApply ||
		profile.Status != domain.CandidateActive ||
		profile.Subscription != domain.SubscriptionPaid {
		return nil
	}

	// Daily limit check.
	today, err := h.deps.AppRepo.CountTodayForCandidate(ctx, in.CandidateID)
	if err != nil {
		log.WithError(err).Warn("auto-apply trigger: daily count failed")
		return nil
	}
	if today >= h.deps.DailyLimit {
		log.WithField("today_count", today).Debug("auto-apply trigger: daily limit reached")
		telemetry.RecordAutoApplyLimitReached()
		return nil
	}
	remaining := h.deps.DailyLimit - today

	// Load CV fields for form fill (best-effort).
	var cvName, cvEmail, cvPhone string
	if h.deps.CVStore != nil {
		cvName, cvEmail, cvPhone, _ = h.deps.CVStore.LatestExtracted(ctx, in.CandidateID)
	}
	if cvName == "" {
		cvName = profile.CurrentTitle // fallback: at least something in "name" field
	}

	published := 0
	for _, match := range in.Matches {
		if published >= remaining {
			break
		}
		if match.Score < h.deps.ScoreMin {
			continue
		}
		if strings.TrimSpace(match.ApplyURL) == "" {
			continue
		}

		// Idempotency check.
		exists, err := h.deps.AppRepo.ExistsForCandidate(ctx, in.CandidateID, match.CanonicalID)
		if err != nil {
			log.WithError(err).Warn("auto-apply trigger: exists check failed")
			continue
		}
		if exists {
			continue
		}

		// Populate MatchID so the autoapply consumer can flip the
		// CandidateMatch row to status='applied' on success. Best
		// effort — a missing row leaves the field empty and downstream
		// MarkApplied is a no-op.
		var matchID string
		if h.deps.MatchLookup != nil {
			id, err := h.deps.MatchLookup.GetIDByCandidateJob(ctx, in.CandidateID, match.CanonicalID)
			if err != nil {
				log.WithError(err).Debug("auto-apply trigger: match-id lookup failed")
			}
			matchID = id
		}

		intent := eventsv1.AutoApplyIntentV1{
			CandidateID:    in.CandidateID,
			MatchID:        matchID,
			CanonicalJobID: match.CanonicalID,
			ApplyURL:       match.ApplyURL,
			SourceType:     deriveSourceType(match.ApplyURL),
			Score:          match.Score,
			CVUrl:          profile.CVUrl,
			FullName:       cvName,
			Email:          cvEmail,
			Phone:          cvPhone,
			Location:       profile.PreferredLocations,
			CurrentTitle:   profile.CurrentTitle,
			Skills:         profile.Skills,
		}

		intentEnv := eventsv1.NewEnvelope(eventsv1.SubjectAutoApplySubmit, intent)
		intentBytes, err := json.Marshal(intentEnv)
		if err != nil {
			log.WithError(err).Warn("auto-apply trigger: marshal intent failed")
			continue
		}

		pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var pubErr error
		if h.deps.PublishFn != nil {
			pubErr = h.deps.PublishFn(pubCtx, eventsv1.SubjectAutoApplySubmit, intentBytes)
		} else {
			pubErr = h.deps.Svc.QueueManager().Publish(pubCtx, eventsv1.SubjectAutoApplySubmit, intentBytes)
		}
		cancel()
		if pubErr != nil {
			log.WithError(pubErr).Warn("auto-apply trigger: publish intent failed")
			continue
		}

		published++
		log.WithField("canonical_job_id", match.CanonicalID).
			WithField("score", match.Score).
			Info("auto-apply trigger: intent published")
	}

	return nil
}

// deriveSourceType returns the ATS source type from an apply URL using
// the same URL patterns as the submitters' CanHandle methods. Returns ""
// for unknown ATS — the registry will fall through to the LLM or email tier.
func deriveSourceType(applyURL string) string {
	lower := strings.ToLower(applyURL)
	switch {
	case strings.Contains(lower, "boards.greenhouse.io") ||
		strings.Contains(lower, "gh_jid=") ||
		strings.Contains(lower, "greenhouse.io/jobs"):
		return string(domain.SourceGreenhouse)
	case strings.Contains(lower, "jobs.lever.co") ||
		strings.Contains(lower, "lever.co"):
		return string(domain.SourceLever)
	case strings.Contains(lower, "myworkdayjobs.com") ||
		strings.Contains(lower, "wd3.myworkday.com") ||
		strings.Contains(lower, "wd5.myworkday.com"):
		return string(domain.SourceWorkday)
	case strings.Contains(lower, "smartrecruiters.com"):
		return string(domain.SourceSmartRecruitersPage)
	default:
		return ""
	}
}
