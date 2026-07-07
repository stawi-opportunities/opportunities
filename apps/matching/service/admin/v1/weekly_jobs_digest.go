package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// UnpaidCandidate is the projection of a CandidateProfile that the
// weekly jobs digest cares about. Kept narrow so test fakes don't
// have to construct full domain rows.
type UnpaidCandidate struct {
	ID      string
	Country string
	Locale  string
	// Kinds is the set of opportunity-kind IDs the candidate opted
	// into (e.g. ["job", "scholarship"]). When empty, the handler
	// falls back to []string{"job"} so the digest still has
	// something to show.
	Kinds []string
}

// UnpaidCandidateLister enumerates the candidates targeted by the
// weekly digest. Production impl wraps CandidateRepository.ListUnpaidWithProfile.
type UnpaidCandidateLister interface {
	ListUnpaid(ctx context.Context) ([]UnpaidCandidate, error)
}

// NewJobsLister returns the freshest jobs (across the past N days)
// matching a candidate's filters. Production reads PostgreSQL.
type NewJobsLister interface {
	ListNewJobs(ctx context.Context, since time.Time, country string, kinds []string, limit int) ([]eventsv1.DigestJob, error)
}

// WeeklyStatsLister computes the global headline analytics block
// (total + top-3 countries / kinds) for the past N days. Computed
// once per digest run and embedded into every envelope; per-country
// narrowing happens client-side in the email template.
type WeeklyStatsLister interface {
	GlobalStats(ctx context.Context, since time.Time) (eventsv1.DigestStats, error)
}

// WeeklyJobsDigestDeps bundles collaborators.
type WeeklyJobsDigestDeps struct {
	Svc      *frame.Service
	Lister   UnpaidCandidateLister
	Jobs     NewJobsLister
	Stats    WeeklyStatsLister
	PlansURL string // e.g. "https://jobs.stawi.org/pricing/"
	// Window is the look-back for "new jobs this week". Defaults to
	// 7 * 24h.
	Window time.Duration
	// JobLimit caps the per-candidate job list. Defaults to 10.
	JobLimit int
}

type weeklyJobsDigestResponse struct {
	OK       bool `json:"ok"`
	Emitted  int  `json:"emitted"`
	Skipped  int  `json:"skipped"`
	Failed   int  `json:"failed"`
	Audience int  `json:"audience"`
}

// WeeklyJobsDigestHandler returns the HTTP handler Trustage hits on
// the Monday morning cron. Algorithm:
//
//  1. List all unpaid candidates with completed onboarding.
//  2. Compute the global stats block once (cheap aggregate).
//  3. For each candidate, query top-N new jobs filtered by their
//     country + opted-in kinds, build the digest payload, emit one
//     `candidates.weekly_jobs_digest.v1` envelope.
//
// Per-candidate failures (jobs query, emit) are logged and counted
// but do not abort the sweep. The notification service is the sole
// consumer and is responsible for rate-limiting / unsubscribe logic.
func WeeklyJobsDigestHandler(deps WeeklyJobsDigestDeps) http.HandlerFunc {
	window := deps.Window
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	jobLimit := deps.JobLimit
	if jobLimit <= 0 {
		jobLimit = 10
	}
	plansURL := deps.PlansURL
	if plansURL == "" {
		plansURL = "https://jobs.stawi.org/pricing/"
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		since := time.Now().UTC().Add(-window)

		candidates, err := deps.Lister.ListUnpaid(ctx)
		if err != nil {
			log.WithError(err).Error("weekly-jobs-digest: ListUnpaid failed")
			http.Error(w, `{"error":"list unpaid failed"}`, http.StatusInternalServerError)
			return
		}

		// Compute global stats up front. On failure we still emit
		// digests with an empty stats block — the email template
		// renders the jobs block without the analytics section
		// rather than rendering nothing.
		stats, statsErr := deps.Stats.GlobalStats(ctx, since)
		if statsErr != nil {
			log.WithError(statsErr).Warn("weekly-jobs-digest: GlobalStats failed; emitting without analytics")
			stats = eventsv1.DigestStats{}
		}

		resp := weeklyJobsDigestResponse{OK: true, Audience: len(candidates)}
		for _, c := range candidates {
			kinds := c.Kinds
			if len(kinds) == 0 {
				kinds = []string{"job"}
			}
			jobs, jobErr := deps.Jobs.ListNewJobs(ctx, since, c.Country, kinds, jobLimit)
			if jobErr != nil {
				log.WithError(jobErr).WithField("candidate_id", c.ID).Warn("weekly-jobs-digest: ListNewJobs failed")
				resp.Failed++
				continue
			}
			if len(jobs) == 0 {
				// No new jobs to surface — skip rather than send an
				// empty digest the user will read as spam.
				resp.Skipped++
				continue
			}
			payload := eventsv1.WeeklyJobsDigestV1{
				CandidateID: c.ID,
				Country:     c.Country,
				Locale:      c.Locale,
				Jobs:        jobs,
				Stats:       stats,
				PlansURL:    plansURL,
			}
			env := eventsv1.NewEnvelope(eventsv1.TopicCandidateWeeklyJobsDigest, payload)
			if err := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateWeeklyJobsDigest, env); err != nil {
				log.WithError(err).WithField("candidate_id", c.ID).Warn("weekly-jobs-digest: emit failed")
				resp.Failed++
				continue
			}
			resp.Emitted++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ── Production adapters ─────────────────────────────────────────────

// UnpaidProfileRepo is the subset of repository.CandidateRepository
// the unpaid lister needs.
type UnpaidProfileRepo interface {
	ListUnpaidWithProfile(ctx context.Context, limit int) ([]*domain.CandidateProfile, error)
}

// RepoUnpaidCandidateLister adapts the repository into UnpaidCandidateLister.
// Falls back to "job" when the candidate has no recorded opt-in kinds
// (we don't yet store an explicit opt-in set; we infer from the
// presence of TargetJobTitle, which onboarding mandates for the job
// flow).
type RepoUnpaidCandidateLister struct {
	repo  UnpaidProfileRepo
	limit int
}

// NewRepoUnpaidCandidateLister wires the adapter. `limit` caps the
// per-sweep audience size so a runaway query can't OOM the matching
// pod.
func NewRepoUnpaidCandidateLister(repo UnpaidProfileRepo, limit int) *RepoUnpaidCandidateLister {
	if limit <= 0 {
		limit = 5000
	}
	return &RepoUnpaidCandidateLister{repo: repo, limit: limit}
}

func (l *RepoUnpaidCandidateLister) ListUnpaid(ctx context.Context) ([]UnpaidCandidate, error) {
	rows, err := l.repo.ListUnpaidWithProfile(ctx, l.limit)
	if err != nil {
		return nil, err
	}
	out := make([]UnpaidCandidate, 0, len(rows))
	for _, r := range rows {
		country := primaryCountry(r)
		out = append(out, UnpaidCandidate{
			ID:      r.ID,
			Country: country,
			Locale:  deriveLocale(r),
			Kinds:   inferKinds(r),
		})
	}
	return out, nil
}

// primaryCountry picks the first comma-separated entry from
// PreferredCountries; the candidate explicitly chose this during
// onboarding. Empty means "global" — the jobs lister applies no
// country filter.
func primaryCountry(r *domain.CandidateProfile) string {
	if r.PreferredCountries == "" {
		return ""
	}
	parts := strings.SplitN(r.PreferredCountries, ",", 2)
	return strings.TrimSpace(parts[0])
}

// deriveLocale falls back to "en" when the profile carries no
// language signal. service_notification uses this to pick the
// template variant.
func deriveLocale(r *domain.CandidateProfile) string {
	if r.Languages == "" {
		return "en"
	}
	parts := strings.SplitN(r.Languages, ",", 2)
	loc := strings.TrimSpace(parts[0])
	if loc == "" {
		return "en"
	}
	return loc
}

// inferKinds derives the candidate's opted-in opportunity kinds from
// existing profile signals. We don't yet store an explicit opt-in
// set on CandidateProfile, so this is a heuristic:
//
//   - If TargetJobTitle is set → "job"
//   - Always include "scholarship" + "tender" + "deal" + "funding"
//     until preferences gain per-kind opt-ins.
//
// Returns nil to signal "use the handler's default fallback" rather
// than an empty slice so the handler can distinguish "no opinion"
// from "explicitly empty".
func inferKinds(r *domain.CandidateProfile) []string {
	kinds := []string{}
	if r.TargetJobTitle != "" {
		kinds = append(kinds, "job")
	}
	// TODO: read explicit per-kind opt-ins once they exist on the
	// candidate row. Until then the digest spans every kind the
	// platform indexes — broader than the matching pipeline but
	// fine for a non-personalised re-engagement email.
	kinds = append(kinds, "scholarship", "tender", "deal", "funding")
	if len(kinds) == 0 {
		return nil
	}
	return kinds
}
