package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// MeSubscription is the JSON shape the candidate dashboard expects from
// GET /me/subscription. Field names match
// ui/app/src/api/candidates.ts:fetchMeSubscription so the frontend can
// drop the synchronous fallback once this ships.
type MeSubscription struct {
	Plan              *string         `json:"plan"`
	Status            string          `json:"status"`
	RenewsAt          *string         `json:"renews_at,omitempty"`
	Agent             *MeAgent        `json:"agent,omitempty"`
	QueuedMatches     int             `json:"queued_matches"`
	DeliveredThisWeek int             `json:"delivered_this_week"`
}

// MeAgent is the human-recruiter card surfaced to "managed"-plan
// candidates. Empty for self-serve tiers.
type MeAgent struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// CandidateProfileReader is the subset of repository.CandidateRepository
// the subscription handler needs. Defined here as an interface so the
// handler is testable against an in-memory fake without dragging GORM
// into the test binary.
type CandidateProfileReader interface {
	GetByID(ctx context.Context, id string) (*domain.CandidateProfile, error)
}

// MatchSummarizer returns the dashboard's queued/delivered counters for
// one candidate. Implemented by *matching.Store; the interface lets
// tests substitute a deterministic stub.
type MatchSummarizer interface {
	SubscriptionSummary(ctx context.Context, candidateID string) (queued, deliveredThisWeek int, err error)
}

// SubscriptionDeps bundles the inputs SubscriptionHandler needs. A
// nil Matches summarizer is allowed — the route still serves the
// candidate's plan / status with both counters returned as zero. This
// preserves the dashboard contract on deployments that haven't enabled
// the Phase-4 matching stack yet.
type SubscriptionDeps struct {
	Candidates CandidateProfileReader
	Matches    MatchSummarizer
}

// SubscriptionHandler returns the JSON dashboard payload for the
// currently-authenticated candidate.
//
//	GET /me/subscription
//	X-Candidate-ID: <candidate primary-key>
//	→ 200 { plan, status, queued_matches, delivered_this_week, ... }
//
// Wrap with httpmw.CandidateAuth before mounting. The wrapper is
// what populates the candidate identity into the request context.
func SubscriptionHandler(deps SubscriptionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		cand, err := deps.Candidates.GetByID(ctx, candidateID)
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).
				Error("me/subscription: candidate lookup failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway,
				"candidate_lookup_failed", "could not load candidate profile")
			return
		}

		resp := MeSubscription{
			Plan:   planValue(cand),
			Status: statusFromCandidate(cand),
		}
		if deps.Matches != nil {
			queued, delivered, sumErr := deps.Matches.SubscriptionSummary(ctx, candidateID)
			if sumErr != nil {
				// Degrade to zeroes rather than 5xx. The dashboard
				// already renders a graceful state when counts are 0
				// and a wedged metrics query shouldn't take the whole
				// subscription panel down.
				log.WithError(sumErr).WithField("candidate_id", candidateID).
					Warn("me/subscription: summary query failed; returning zero counts")
			} else {
				resp.QueuedMatches = queued
				resp.DeliveredThisWeek = delivered
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// planValue maps the persisted PlanID to the dashboard's nullable
// `plan` field. An empty string on the candidate row means "no plan
// chosen yet" and the dashboard renders the "Choose a plan" CTA;
// returning *string lets the JSON encoder emit `null` for that case.
func planValue(c *domain.CandidateProfile) *string {
	if c == nil {
		return nil
	}
	p := strings.TrimSpace(c.PlanID)
	if p == "" {
		return nil
	}
	return &p
}

// statusFromCandidate flattens the candidate's SubscriptionTier (which
// also encodes the trial vs. paid distinction) into the four-state
// enum the dashboard understands. `past_due` is intentionally
// unreachable today — there's no source-of-truth field on the
// candidate row for it; surface it once service-payment publishes a
// past-due event we can persist.
func statusFromCandidate(c *domain.CandidateProfile) string {
	if c == nil {
		return "none"
	}
	switch c.Subscription {
	case domain.SubscriptionPaid, domain.SubscriptionTrial:
		return "active"
	case domain.SubscriptionCancelled:
		return "cancelled"
	default: // SubscriptionFree, unknown
		return "none"
	}
}
