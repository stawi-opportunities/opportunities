package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// CandidatesOnboardStore is what the handler needs from the
// repository: one atomic operation that updates the candidate row
// with the submitted profile values AND clears the onboarding_draft
// in the same transaction. Implemented in production by a thin
// adapter over CandidateRepository.Transaction.
type CandidatesOnboardStore interface {
	OnboardAtomically(ctx context.Context, candidateID string, mutate func(*domain.CandidateProfile)) error
}

// OnboardMatchTrigger fires the candidate-change / preference-update
// path so a freshly-onboarded candidate gets gap-fill matches even
// before (or without) a CV upload. Production impl emits a
// PreferencesUpdatedV1 event onto the preferences-updated topic, which
// the PreferenceMatchHandler consumes. Best-effort: errors are logged,
// never surfaced to the onboard response.
type OnboardMatchTrigger interface {
	TriggerInitialMatch(ctx context.Context, candidateID string, kinds []string) error
}

// CandidatesOnboardDeps bundles the inputs the handler needs.
type CandidatesOnboardDeps struct {
	Store CandidatesOnboardStore
	// Match optionally fires the initial match pass after a successful
	// onboard. nil disables the trigger (the wizard still completes).
	Match OnboardMatchTrigger
}

// onboardPayload mirrors ui/app/src/api/candidates.ts:OnboardingPayload.
type onboardPayload struct {
	TargetJobTitle     string   `json:"target_job_title"`
	ExperienceLevel    string   `json:"experience_level"`
	JobSearchStatus    string   `json:"job_search_status"`
	SalaryRange        string   `json:"salary_range,omitempty"`
	WantsATSReport     bool     `json:"wants_ats_report"`
	PreferredRegions   []string `json:"preferred_regions"`
	PreferredTimezones []string `json:"preferred_timezones"`
	PreferredLanguages []string `json:"preferred_languages"`
	JobTypes           []string `json:"job_types"`
	Country            string   `json:"country"`
	Plan               string   `json:"plan"`
	AgreeTerms         bool     `json:"agree_terms"`
	SalaryMin          *float32 `json:"salary_min,omitempty"`
	SalaryMax          *float32 `json:"salary_max,omitempty"`
	USWorkAuth         *bool    `json:"us_work_auth,omitempty"`
	NeedsSponsorship   *bool    `json:"needs_sponsorship,omitempty"`
	Currency           string   `json:"currency,omitempty"`
}

func validPlan(plan string) bool {
	switch plan {
	case "starter", "pro", "managed":
		return true
	}
	return false
}

// CandidatesOnboardHandler serves POST /candidates/onboard — the
// wizard's final submit. Promotes the in-progress draft into the
// canonical candidate-profile columns AND clears the onboarding_draft
// in one transaction. The candidate's subscription status stays at
// "free" — service-payment flips it to "active" via webhook when the
// checkout completes.
func CandidatesOnboardHandler(deps CandidatesOnboardDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"body_read_failed", "could not read request body")
			return
		}
		var in onboardPayload
		if err := json.Unmarshal(body, &in); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"invalid_json", "request body is not valid JSON")
			return
		}
		// Minimal validation — most schema enforcement lives in the
		// wizard's client-side react-hook-form Step1/2/3 schemas.
		// Server-side we guard the cross-step invariants: agree_terms
		// must be true, plan must be a known tier. target_job_title is
		// OPTIONAL — the wizard never collects a job title; matching keys
		// off the CV embedding + preferred roles/geo/salary, so an empty
		// title must be accepted.
		if !validPlan(in.Plan) {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"invalid_plan", "plan must be one of: starter, pro, managed")
			return
		}
		if !in.AgreeTerms {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"terms_not_accepted", "agree_terms must be true to onboard")
			return
		}

		err = deps.Store.OnboardAtomically(ctx, candidateID, func(c *domain.CandidateProfile) {
			c.TargetJobTitle = in.TargetJobTitle
			c.ExperienceLevel = in.ExperienceLevel
			c.JobSearchStatus = in.JobSearchStatus
			c.WantsATSReport = in.WantsATSReport
			c.PreferredRegions = joinJSONArray(in.PreferredRegions)
			c.PreferredTimezones = joinJSONArray(in.PreferredTimezones)
			c.Languages = joinJSONArray(in.PreferredLanguages)
			c.PreferredRoles = joinJSONArray(in.JobTypes)
			c.PreferredCountries = in.Country
			// Prefer the plan the seeker just chose; keep an existing paid plan
			// if a concurrent checkout already activated them.
			if c.Subscription != domain.SubscriptionPaid || c.PlanID == "" {
				c.PlanID = in.Plan
			}
			c.Status = domain.CandidateActive
			// Never downgrade a paid subscription if checkout finished first.
			if c.Subscription != domain.SubscriptionPaid {
				c.Subscription = domain.SubscriptionFree
			}
			if in.SalaryMin != nil {
				c.SalaryMin = *in.SalaryMin
			}
			if in.SalaryMax != nil {
				c.SalaryMax = *in.SalaryMax
			}
			if in.USWorkAuth != nil {
				c.USWorkAuth = in.USWorkAuth
			}
			if in.NeedsSponsorship != nil {
				c.NeedsSponsorship = in.NeedsSponsorship
			}
			if in.Currency != "" {
				c.Currency = in.Currency
			}
		})
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).
				Error("candidates/onboard: transaction failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway,
				"onboard_failed", "could not save onboarding data")
			return
		}

		// Best-effort: kick off an initial match pass so a candidate who
		// set preferences gets gap-fill matches even before/without a CV.
		// Non-fatal — a failure here must not fail the onboard response.
		if deps.Match != nil {
			if err := deps.Match.TriggerInitialMatch(ctx, candidateID, in.JobTypes); err != nil {
				log.WithError(err).WithField("candidate_id", candidateID).
					Warn("candidates/onboard: initial match trigger failed (non-fatal)")
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":         candidateID,
			"profile_id": candidateID,
		})
	}
}

// joinJSONArray returns a JSON-encoded string of the slice for
// storing into a TEXT column. The domain model stores these as
// opaque text today; the matcher service parses them when needed.
// Empty/nil slice produces "[]".
func joinJSONArray(vs []string) string {
	if len(vs) == 0 {
		return "[]"
	}
	b, err := json.Marshal(vs)
	if err != nil {
		return "[]"
	}
	return string(b)
}
