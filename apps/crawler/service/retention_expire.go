package service

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// RetentionSweeper is the variantstate.Store slice the retention sweep
// needs. Narrow so tests can fake it.
type RetentionSweeper interface {
	HideStale(ctx context.Context, minAge time.Duration) (int64, error)
	RestoreReseen(ctx context.Context) (int64, error)
}

// RetentionMinAge is the floor on how long a job must go unseen before
// the sweep hides it, regardless of how short its source's crawl
// interval is. Generous on purpose: hiding late costs a stale listing
// for a few days; hiding early costs a live job.
const RetentionMinAge = 7 * 24 * time.Hour

// RetentionExpireHandler returns POST /admin/retention/expire — the
// stale-job sweep driven by the opportunities.retention.expire Trustage
// cron. Restore runs before hide so a job that reappeared comes back in
// the same tick it would otherwise be re-evaluated.
//
// Before this handler existed, nothing reconciled "jobs seen this crawl"
// against the serving table: a posting removed from its source stayed
// visible until its deadline passed or an operator stopped the whole
// source.
func RetentionExpireHandler(sweeper RetentionSweeper) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)

		restored, err := sweeper.RestoreReseen(ctx)
		if err != nil {
			log.WithError(err).Error("retention: restore re-seen failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hidden, err := sweeper.HideStale(ctx, RetentionMinAge)
		if err != nil {
			log.WithError(err).Error("retention: hide stale failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		telemetry.RecordRetentionExpired(hidden)
		if hidden > 0 || restored > 0 {
			log.WithField("hidden", hidden).WithField("restored", restored).
				Info("retention: sweep complete")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "hidden": hidden, "restored": restored})
	}
}
