package main

import (
	"context"
	"log"
	"time"

	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/repository"
)

// runEvery invokes fn immediately, then every d until ctx is cancelled.
// Non-fatal panics/errors inside fn are the caller's responsibility.
func runEvery(ctx context.Context, d time.Duration, fn func(context.Context)) {
	fn(ctx)
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

// runExpire flips any 'active' row past its expires_at to status='expired'.
// Called every 15 minutes.
func runExpire(ctx context.Context, retentionRepo *repository.RetentionRepository) {
	n, err := retentionRepo.ExpireDue(ctx)
	if err != nil {
		log.Printf("retention.expire: error: %v", err)
		return
	}
	if n > 0 {
		log.Printf("retention.expire: flipped %d rows to expired", n)
	}
}

// runMVRefresh refreshes mv_job_facets concurrently. Called every 5 minutes.
func runMVRefresh(ctx context.Context, facetRepo *repository.FacetRepository) {
	if err := facetRepo.Refresh(ctx); err != nil {
		log.Printf("retention.mv_refresh: error: %v", err)
	}
}

// runRetention physically deletes R2 snapshots for rows whose expired grace
// window has passed, then flips status to 'deleted'. Called nightly.
func runRetention(
	ctx context.Context,
	retentionRepo *repository.RetentionRepository,
	publisher *publish.R2Publisher,
	purger *publish.CachePurger,
	graceDays int,
) {
	if publisher == nil {
		return
	}
	const batchSize = 1000
	total := 0
	for {
		rows, err := retentionRepo.SelectDeletable(ctx, graceDays, batchSize)
		if err != nil {
			log.Printf("retention.stage2: select: %v", err)
			return
		}
		if len(rows) == 0 {
			break
		}
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			if r.Slug == "" {
				ids = append(ids, r.ID)
				continue
			}
			key := "jobs/" + r.Slug + ".json"
			if err := publisher.Delete(ctx, key); err != nil {
				log.Printf("retention.stage2: r2 delete %s: %v", key, err)
				continue
			}
			_ = purger.PurgeURL(ctx, publish.PublicURL(key))
			ids = append(ids, r.ID)
		}
		if err := retentionRepo.MarkDeleted(ctx, ids); err != nil {
			log.Printf("retention.stage2: mark deleted: %v", err)
			return
		}
		total += len(ids)
	}
	if total > 0 {
		log.Printf("retention.stage2: deleted %d snapshots", total)
	}
}
