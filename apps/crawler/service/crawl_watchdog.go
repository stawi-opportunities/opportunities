package service

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pitabwire/util"
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
