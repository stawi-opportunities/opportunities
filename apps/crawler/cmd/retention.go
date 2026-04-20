package main

import (
	"context"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/repository"
)

// Retention helpers are invoked from the crawler's admin HTTP handlers
// on cadences controlled by Trustage workflow schedules
// (definitions/trustage/retention-*.json), not from an in-process
// ticker. Each function is idempotent so a retry policy at the
// workflow level can't corrupt state.

// runExpire flips any 'active' row past its expires_at to status='expired'.
// Called every 15 minutes.
func runExpire(ctx context.Context, retentionRepo *repository.RetentionRepository) {
	n, err := retentionRepo.ExpireDue(ctx)
	if err != nil {
		util.Log(ctx).WithError(err).Error("retention: expire sweep failed")
		return
	}
	if n > 0 {
		util.Log(ctx).WithField("rows_expired", n).Info("retention: expire sweep complete")
	}
}

// runMVRefresh refreshes mv_job_facets concurrently. Called every 5 minutes.
func runMVRefresh(ctx context.Context, facetRepo *repository.FacetRepository) {
	if err := facetRepo.Refresh(ctx); err != nil {
		util.Log(ctx).WithError(err).Error("retention: mv_job_facets refresh failed")
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
	log := util.Log(ctx)
	const batchSize = 1000
	total := 0
	for {
		rows, err := retentionRepo.SelectDeletable(ctx, graceDays, batchSize)
		if err != nil {
			log.WithError(err).Error("retention.stage2: select failed")
			return
		}
		if len(rows) == 0 {
			break
		}
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			if r.Slug == "" {
				ids = append(ids, r.ID)
				continue
			}
			key := "jobs/" + r.Slug + ".json"
			if err := publisher.Delete(ctx, key); err != nil {
				log.WithError(err).WithField("r2_key", key).
					Warn("retention.stage2: r2 delete failed")
				continue
			}
			_ = purger.PurgeURL(ctx, publish.PublicURL(key))
			ids = append(ids, r.ID)
		}
		if err := retentionRepo.MarkDeleted(ctx, ids); err != nil {
			log.WithError(err).Error("retention.stage2: mark deleted failed")
			return
		}
		total += len(ids)
	}
	if total > 0 {
		log.WithField("snapshots_deleted", total).Info("retention.stage2: complete")
	}
}
