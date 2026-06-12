package service

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// LastCrawlReader reports the most recent crawl_jobs row (satisfied by
// *repository.CrawlRepository).
type LastCrawlReader interface {
	LastCrawlAt(ctx context.Context) (time.Time, error)
}

// CrawlStallThreshold is how long with zero crawl_jobs activity counts
// as a pipeline stall. Sources crawl at most every MinCrawlIntervalHours
// with jittered phases, so a healthy fleet of N sources produces crawls
// roughly every (MinCrawlIntervalHours*60/N) minutes; several hours of
// total silence means the dispatch chain is broken, not quiet.
const CrawlStallThreshold = 3 * time.Hour

// ResumableRunStore is the crawl_runs slice the run-watchdog needs
// (satisfied by *repository.CrawlRunRepository).
type ResumableRunStore interface {
	FindResumable(ctx context.Context, limit int) ([]*domain.CrawlRun, error)
	TouchLease(ctx context.Context, id string, leaseTTL time.Duration) error
	Fail(ctx context.Context, id, code, message string, dFound, dEmitted, dRejected int) error
}

// CrawlRunWatchdogHandler returns POST /admin/crawl/runs/sweep: the
// self-healing backbone of resumable crawling. Fired by a Trustage cron, it
// finds runs whose lease has lapsed — a crashed/redeployed owner, a lost or
// backpressure-deferred continuation — and re-drives each by re-emitting a
// (backpressure-gated) continuation request. The durable crawl_runs row is the
// source of truth; NATS messages are only nudges, so even a dropped
// continuation eventually converges. Runs past the stuck-attempt ceiling are
// failed (freeing the source's single-flight slot) rather than re-driven
// forever.
func CrawlRunWatchdogHandler(
	svc *frame.Service, runs ResumableRunStore, admit Admitter, batch, stuckMaxAttempts, leaseTTLSec int,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		if batch <= 0 {
			batch = 50
		}
		leaseTTL := time.Duration(leaseTTLSec) * time.Second
		if leaseTTL <= 0 {
			leaseTTL = 5 * time.Minute
		}

		resumable, err := runs.FindResumable(ctx, batch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		evtMgr := svc.EventsManager()
		if evtMgr == nil {
			http.Error(w, `{"error":"events manager unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		var redriven, failed, deferred int
		for _, run := range resumable {
			// Stuck backstop: a run that keeps lapsing without completing is
			// failed so it stops holding the source's single-flight slot.
			if stuckMaxAttempts > 0 && run.Attempt >= stuckMaxAttempts {
				if e := runs.Fail(ctx, run.ID, "stuck", "exceeded max attempts without completing", 0, 0, 0); e != nil {
					log.WithError(e).WithField("run_id", run.ID).Warn("crawl-run-watchdog: fail stuck run failed")
				} else {
					failed++
				}
				continue
			}
			// Pace re-drives through the same backpressure gate as fresh ticks.
			if admit != nil {
				if granted, _ := admit.Admit(ctx, eventsv1.TopicCrawlRequests, 1); granted < 1 {
					deferred++
					continue
				}
			}
			// Debounce before emitting so the next sweep doesn't double-drive
			// this run before its continuation is consumed.
			if e := runs.TouchLease(ctx, run.ID, leaseTTL); e != nil {
				log.WithError(e).WithField("run_id", run.ID).Warn("crawl-run-watchdog: touch lease failed")
				continue
			}
			env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
				RequestID:   xid.New().String(),
				SourceID:    run.SourceID,
				ScheduledAt: run.ScheduledAt,
				Mode:        "auto",
				Attempt:     run.Attempt + 1,
				RunID:       run.ID,
			})
			if e := evtMgr.Emit(ctx, eventsv1.TopicCrawlRequests, env); e != nil {
				log.WithError(e).WithField("run_id", run.ID).Warn("crawl-run-watchdog: emit failed")
				continue
			}
			redriven++
		}

		if redriven > 0 || failed > 0 {
			log.WithField("resumable", len(resumable)).
				WithField("redriven", redriven).
				WithField("failed", failed).
				WithField("deferred", deferred).
				Info("crawl-run-watchdog: swept lapsed crawl runs")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"resumable": len(resumable),
			"redriven":  redriven,
			"failed":    failed,
			"deferred":  deferred,
		})
	}
}

// CrawlWatchdogHandler returns GET|POST /admin/crawl/watchdog: a
// liveness probe for the crawl pipeline as a whole. It reports stalled
// when no crawl_jobs row has been opened within CrawlStallThreshold AND
// at least one active source is past due (so a genuinely idle fleet —
// nothing due — is not a stall). Fired by a static Trustage cron; a
// stalled verdict logs at ERROR so log-based alerting picks it up even
// with no metrics backend.
//
// Both the 2026-06-03 and 2026-06-11 outages ran silently for hours to
// days precisely because no component owned this question.
func CrawlWatchdogHandler(crawls LastCrawlReader, sources CrawlOverdueLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		now := time.Now().UTC()

		last, err := crawls.LastCrawlAt(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		due, err := sources.ListDue(ctx, now, 1)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		sinceLast := time.Duration(0)
		if !last.IsZero() {
			sinceLast = now.Sub(last)
		}
		stalled := len(due) > 0 && (last.IsZero() || sinceLast > CrawlStallThreshold)

		if stalled {
			log.WithField("last_crawl_at", last).
				WithField("since_last", sinceLast.String()).
				Error("crawl-watchdog: STALLED — sources are due but nothing has crawled; check Trustage schedules, the backpressure gate (/admin/crawl/status), and worker drain")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":            !stalled,
			"stalled":       stalled,
			"last_crawl_at": last,
			"since_last":    sinceLast.String(),
			"sources_due":   len(due) > 0,
		})
	}
}
