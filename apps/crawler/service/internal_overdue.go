package service

import (
	"context"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"
)

// RunInternalOverdueLoop periodically dispatches overdue sources without
// depending on Trustage HTTP callbacks. Trustage remains the preferred
// driver for healthy per-source schedules; this loop is the resilience
// path when schedules never activate or Trustage cannot call the admin API.
//
// interval <= 0 disables the loop. The first tick runs after a short boot
// delay so migration/seed/reconcile finish first.
func RunInternalOverdueLoop(
	ctx context.Context,
	svc *frame.Service,
	lister CrawlOverdueLister,
	bumper NextCrawlBumper,
	admit Admitter,
	batch int,
	interval time.Duration,
) {
	if interval <= 0 {
		util.Log(ctx).Info("internal-overdue: disabled")
		return
	}
	if batch <= 0 {
		batch = 25
	}
	log := util.Log(ctx)
	log.WithField("interval", interval.String()).WithField("batch", batch).
		Info("internal-overdue: loop started")

	// Fire once after boot (2m) so newly seeded sources (next_crawl_at≈now)
	// are dispatched without waiting OverdueSlack or a full interval.
	boot := time.NewTimer(2 * time.Minute)
	ticker := time.NewTicker(interval)
	defer boot.Stop()
	defer ticker.Stop()

	runOnce := func(slack time.Duration) {
		tctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		res, err := DispatchOverdue(tctx, svc, lister, bumper, admit, batch, slack)
		if err != nil {
			log.WithError(err).Warn("internal-overdue: sweep failed")
			return
		}
		log.WithField("dispatched", res.Dispatched).WithField("due", res.Due).
			WithField("throttled", res.Throttled).WithField("slack", slack.String()).
			Info("internal-overdue: sweep complete")
	}

	for {
		select {
		case <-ctx.Done():
			log.Info("internal-overdue: stopped")
			return
		case <-boot.C:
			// Zero slack on first boot tick so seeds with next_crawl_at=now are eligible.
			runOnce(0)
		case <-ticker.C:
			// Steady-state: same slack as the Trustage overdue cron.
			runOnce(OverdueSlack)
		}
	}
}
