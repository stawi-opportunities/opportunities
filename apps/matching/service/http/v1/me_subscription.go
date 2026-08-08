package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// MeSubscription is the JSON shape the candidate dashboard expects from
// GET /me/subscription. Field names match
// ui/app/src/api/candidates.ts:fetchMeSubscription so the frontend can
// drop the synchronous fallback once this ships.
type MeSubscription struct {
	Plan              *string  `json:"plan"`
	Status            string   `json:"status"`
	RenewsAt          *string  `json:"renews_at,omitempty"`
	CancelAtPeriodEnd bool     `json:"cancel_at_period_end,omitempty"`
	Agent             *MeAgent `json:"agent,omitempty"`
	QueuedMatches     int      `json:"queued_matches"`
	DeliveredThisWeek int      `json:"delivered_this_week"`
}

// MeAgent is the human-recruiter card surfaced to "managed"-plan
// candidates. Empty for self-serve tiers.
type MeAgent struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// CandidateProfileReader loads a candidate for the logged-in profile.
// GetByID is kept for tests; production readers implement
// CandidateByProfileID (profile_id = JWT sub).
type CandidateProfileReader interface {
	GetByID(ctx context.Context, id string) (*domain.CandidateProfile, error)
}

// CandidateByProfileID resolves a candidate by platform profile_id
// (JWT sub). Preferred over GetByID for live traffic.
type CandidateByProfileID interface {
	ResolveByProfileID(ctx context.Context, profileID string) (*domain.CandidateProfile, error)
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
//	JWT sub = platform profile_id
//	→ 200 { plan, status, queued_matches, delivered_this_week, ... }
//
// Wrap with httpmw.CandidateAuth before mounting. The wrapper puts the
// JWT subject (profile_id) into the request context.
func SubscriptionHandler(deps SubscriptionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		// JWT sub is the platform profile_id — the only login identity.
		profileID := httpmw.CandidateFromContext(ctx)

		cand, err := loadCandidateByProfileID(ctx, deps.Candidates, profileID)
		if err != nil {
			log.WithError(err).WithField("profile_id", profileID).
				Error("me/subscription: candidate lookup failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway,
				"candidate_lookup_failed", "could not load candidate profile")
			return
		}

		resp := MeSubscription{
			Plan:              planValue(cand),
			Status:            statusFromCandidate(cand),
			CancelAtPeriodEnd: cand != nil && cand.CancelAtPeriodEnd,
		}
		if cand != nil && cand.CurrentPeriodEnd != nil {
			s := cand.CurrentPeriodEnd.UTC().Format(time.RFC3339)
			resp.RenewsAt = &s
		}
		if deps.Matches != nil {
			// Match FKs use candidate row id (often equal to profile_id).
			matchKey := profileID
			if cand != nil && strings.TrimSpace(cand.ID) != "" {
				matchKey = cand.ID
			}
			queued, delivered, sumErr := deps.Matches.SubscriptionSummary(ctx, matchKey)
			if sumErr != nil {
				// Degrade to zeroes rather than 5xx. The dashboard
				// already renders a graceful state when counts are 0
				// and a wedged metrics query shouldn't take the whole
				// subscription panel down.
				log.WithError(sumErr).WithField("profile_id", profileID).
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

// loadCandidateByProfileID loads the job-seeker for a platform profile_id
// (JWT sub). Prefer ResolveByProfileID; tests may only implement GetByID.
func loadCandidateByProfileID(ctx context.Context, r CandidateProfileReader, profileID string) (*domain.CandidateProfile, error) {
	if r == nil {
		return nil, nil
	}
	if byProfile, ok := r.(CandidateByProfileID); ok {
		return byProfile.ResolveByProfileID(ctx, profileID)
	}
	// Fakes that key rows by profile_id for unit tests.
	return r.GetByID(ctx, profileID)
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
// also encodes the trial vs. paid distinction) into the dashboard enum.
//
// Entitled (product access): paid, trial, past_due (dunning grace), and
// legacy rows that stored "active" instead of "paid". Soft-cancel keeps
// subscription=paid until period end, so cancel_at_period_end still maps
// to "active" here.
func statusFromCandidate(c *domain.CandidateProfile) string {
	if c == nil {
		return "none"
	}
	switch strings.ToLower(strings.TrimSpace(string(c.Subscription))) {
	case string(domain.SubscriptionPaid), string(domain.SubscriptionTrial), "active":
		return "active"
	case string(domain.SubscriptionPastDue):
		// Still entitled during dunning — dashboard shows update-payment UX.
		return "past_due"
	case string(domain.SubscriptionCancelled), "canceled":
		// Hard-cancelled (period already ended). Soft-cancel keeps tier=paid.
		return "cancelled"
	default:
		// Recovery: non-empty subscription_id after a webhook race still
		// means billing activated them — never send them back to paywall.
		if strings.TrimSpace(c.SubscriptionID) != "" {
			return "active"
		}
		return "none"
	}
}
