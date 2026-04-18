package handlers

import (
	"context"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"stawi.jobs/pkg/analytics"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/services"
)

var livenessTracer = otel.Tracer("stawi.jobs.liveness")

// livenessThrottle is the minimum time between probes per job. Each
// probe is a HEAD against the third-party apply URL, so a popular viral
// job could otherwise drive thousands of HEADs per hour at the employer.
// One week means a dead link is detected on the first view in the
// following week's window; lag is the cost of being a good citizen.
const livenessThrottle = 7 * 24 * time.Hour

// consecutiveFailuresToExpire controls how many back-to-back probe
// failures before we flip the canonical job to `expired`. A single 500
// from a flaky employer site shouldn't take the listing down; three in
// a row strongly suggests the URL isn't recoverable.
const consecutiveFailuresToExpire = 3

// LivenessProbe runs throttled reachability checks against third-party
// apply URLs. It's called inline from the jobs API view endpoint — no
// event bus / no dedicated service — because the work is small, async
// is already implicit (HTTP handler goroutine), and fan-out is already
// bounded by the throttle window.
type LivenessProbe struct {
	jobRepo        *repository.JobRepository
	httpClient     *httpx.Client
	redirectClient *services.RedirectClient
	analytics      *analytics.Client
}

// NewLivenessProbe builds a LivenessProbe. httpClient is required;
// redirectClient and analytics may be nil (dev / degraded modes).
func NewLivenessProbe(
	jobRepo *repository.JobRepository,
	httpClient *httpx.Client,
	redirectClient *services.RedirectClient,
	analyticsClient *analytics.Client,
) *LivenessProbe {
	return &LivenessProbe{
		jobRepo:        jobRepo,
		httpClient:     httpClient,
		redirectClient: redirectClient,
		analytics:      analyticsClient,
	}
}

// ProbeOnView is the entry point the API's /jobs/{slug}/view handler
// calls. Resolves the canonical, enforces the throttle, runs the probe,
// persists the outcome, and — on terminal or exhausted failures —
// expires both the redirect link and the canonical job. Safe to call
// from a fire-and-forget goroutine; never returns an error (everything
// is logged) so callers don't have to invent a response-path decision.
func (p *LivenessProbe) ProbeOnView(ctx context.Context, slug string) {
	ctx, span := livenessTracer.Start(ctx, "liveness.probe_on_view")
	defer span.End()
	span.SetAttributes(attribute.String("slug", slug))

	job, err := p.jobRepo.GetCanonicalBySlug(ctx, slug)
	if err != nil {
		log.Printf("liveness: load canonical slug=%q (non-fatal): %v", slug, err)
		return
	}
	if job == nil {
		return
	}

	// Throttle gate. Bounds probe cost to ~1 HEAD per active job per
	// throttle window, no matter how viral the view traffic is.
	if job.LastCheckedAt != nil && time.Since(*job.LastCheckedAt) < livenessThrottle {
		return
	}

	// Skip anything we can't meaningfully probe.
	if job.ApplyURL == "" || job.Status != "active" {
		_ = p.jobRepo.RecordAccessibilityCheck(ctx, job.ID, 0, job.ConsecutiveApplyFailures)
		return
	}

	status, verr := p.httpClient.Verify(ctx, job.ApplyURL)
	span.SetAttributes(attribute.Int("probe_status", status))

	reachable := verr == nil && isReachableStatus(status)

	var nextFailures int
	if reachable {
		nextFailures = 0
	} else {
		nextFailures = job.ConsecutiveApplyFailures + 1
	}
	if err := p.jobRepo.RecordAccessibilityCheck(ctx, job.ID, status, nextFailures); err != nil {
		log.Printf("liveness: record check for canonical %d (non-fatal): %v", job.ID, err)
	}

	// 404 / 410 are terminal immediately — no point retrying a page
	// the server explicitly says is gone. Everything else expires only
	// after consecutiveFailuresToExpire strikes.
	terminal := status == 404 || status == 410
	exhausted := nextFailures >= consecutiveFailuresToExpire
	if !reachable && (terminal || exhausted) {
		p.expireJob(ctx, job, status, verr)
	}

	p.emitAnalytics(ctx, job, status, verr, reachable, nextFailures)
}

func (p *LivenessProbe) expireJob(ctx context.Context, job *domain.CanonicalJob, status int, verr error) {
	if err := p.jobRepo.UpdateCanonicalFields(ctx, job.ID, map[string]any{"status": "expired"}); err != nil {
		log.Printf("liveness: mark expired for canonical %d (non-fatal): %v", job.ID, err)
	}

	if p.redirectClient != nil && job.RedirectLinkID != "" {
		if err := p.redirectClient.ExpireLink(ctx, job.RedirectLinkID); err != nil {
			log.Printf("liveness: expire redirect link %s (non-fatal): %v", job.RedirectLinkID, err)
		}
	}

	log.Printf("liveness: canonical %d expired — apply_url status=%d err=%v", job.ID, status, verr)
}

func (p *LivenessProbe) emitAnalytics(ctx context.Context, job *domain.CanonicalJob, status int, verr error, reachable bool, consecutiveFailures int) {
	if p.analytics == nil {
		return
	}
	errStr := ""
	if verr != nil {
		errStr = verr.Error()
	}
	p.analytics.Send(ctx, "stawi_jobs_events", map[string]any{
		"event":                "liveness_probe",
		"canonical_job_id":     job.ID,
		"slug":                 job.Slug,
		"company":              job.Company,
		"country":              job.Country,
		"apply_url":            job.ApplyURL,
		"probe_status":         status,
		"probe_error":          errStr,
		"reachable":            reachable,
		"consecutive_failures": consecutiveFailures,
	})
}

// isReachableStatus classifies an HTTP status as "destination is up".
// 2xx/3xx are obvious successes. 401/403 mean the host answered but
// requires auth — fine for our purposes (the apply page exists).
// 405 (method not allowed) similarly proves the host responds, just
// not to HEAD.
func isReachableStatus(status int) bool {
	if status >= 200 && status < 400 {
		return true
	}
	return status == 401 || status == 403 || status == 405
}
