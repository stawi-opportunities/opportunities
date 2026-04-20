package main

import (
	"context"
	"time"

	"github.com/pitabwire/util"
	"gorm.io/gorm"

	"stawi.jobs/pkg/archive"
	"stawi.jobs/pkg/repository"
)

// purgeR2Archive finds canonicals stuck in status='deleted' past the
// grace window, tears down their R2 bundles, and GCs any raw/{hash}
// blobs whose ref count drops to zero. Idempotent: uses r2_purged_at
// to avoid double work on restart.
//
// Called from the retention cron loop (same cadence as runRetention).
func purgeR2Archive(
	ctx context.Context,
	db func(ctx context.Context, readOnly bool) *gorm.DB,
	arch archive.Archive,
	rawRefRepo *repository.RawRefRepository,
	graceDays int,
	batchLimit int,
) {
	log := util.Log(ctx)

	type row struct {
		ID        string
		ClusterID string
	}
	var rows []row
	cutoff := time.Now().UTC().Add(-time.Duration(graceDays) * 24 * time.Hour)
	if err := db(ctx, true).
		Table("canonical_jobs").
		Select("id, cluster_id").
		Where("status = ? AND deleted_status_at IS NOT NULL AND deleted_status_at < ? AND r2_purged_at IS NULL",
			"deleted", cutoff).
		Limit(batchLimit).
		Scan(&rows).Error; err != nil {
		log.WithError(err).Error("r2-purge: select failed")
		return
	}
	if len(rows) == 0 {
		return
	}

	purged := 0
	for _, r := range rows {
		// 1. Drop ref rows, collect orphan hashes.
		orphans, err := rawRefRepo.DeleteByCluster(ctx, r.ClusterID)
		if err != nil {
			log.WithError(err).WithField("cluster_id", r.ClusterID).
				Warn("r2-purge: delete refs failed, skipping")
			continue
		}
		// 2. Delete orphaned raw/ blobs.
		for _, h := range orphans {
			if err := arch.DeleteRaw(ctx, h); err != nil {
				log.WithError(err).WithField("content_hash", h).
					Warn("r2-purge: delete raw failed, continuing")
			}
		}
		// 3. Delete the cluster bundle.
		if err := arch.DeleteCluster(ctx, r.ClusterID); err != nil {
			log.WithError(err).WithField("cluster_id", r.ClusterID).
				Warn("r2-purge: delete cluster failed, skipping")
			continue
		}
		// 4. Stamp r2_purged_at so we don't re-process.
		if err := db(ctx, false).
			Table("canonical_jobs").
			Where("id = ?", r.ID).
			Update("r2_purged_at", time.Now().UTC()).Error; err != nil {
			log.WithError(err).WithField("canonical_job_id", r.ID).
				Warn("r2-purge: stamp purged_at failed")
			continue
		}
		purged++
	}
	if purged > 0 {
		log.WithField("canonicals_purged", purged).Info("r2-purge: sweep complete")
	}
}
