package service

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/pitabwire/util"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// retentionSweeper is repository.SweepCrawlRetention (a seam for tests).
var retentionSweeper = repository.SweepCrawlRetention

// retentionFirstDelayMax caps the randomised delay before the first sweep.
const retentionFirstDelayMax = 10 * time.Minute

// retentionFirstDelay picks the first sweep's delay: random in
// [1m, min(10m, interval)) — a seam for tests.
var retentionFirstDelay = func(interval time.Duration) time.Duration {
	upper := retentionFirstDelayMax
	if interval < upper {
		upper = interval
	}
	if upper <= time.Minute {
		return upper
	}
	return time.Minute + time.Duration(rand.Int64N(int64(upper-time.Minute)))
}

// RunRetentionLoop runs the crawl-plane retention sweep until ctx is
// cancelled: first after a random delay of up to retentionFirstDelayMax
// (so pods restarted more often than hourly still sweep, without every
// replica hitting the database at once), then every interval. Errors are
// logged, never returned (a returned error would stop the whole crawler).
func RunRetentionLoop(ctx context.Context, db func(context.Context, bool) *gorm.DB, p repository.RetentionPolicy, interval time.Duration) error {
	first := retentionFirstDelay(interval)
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			timer.Reset(interval)
		}
		started := time.Now()
		res, err := retentionSweeper(ctx, db, p)
		log := util.Log(ctx).WithField("duration", time.Since(started).String())
		for table, n := range res {
			log = log.WithField(table, n)
		}
		if err != nil && ctx.Err() == nil {
			log.WithError(err).Error("crawl retention: sweep failed")
			continue
		}
		log.Info("crawl retention: sweep complete")
	}
}
