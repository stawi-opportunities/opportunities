package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stawi.jobs/pkg/domain"
)

// RawRefRepository tracks which variants reference which raw content
// hashes. Used by the purge sweeper to safely GC raw/{hash}.html.gz
// blobs: a hash is deletable once its ref count drops to zero.
type RawRefRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewRawRefRepository creates a new RawRefRepository.
func NewRawRefRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *RawRefRepository {
	return &RawRefRepository{db: db}
}

// Upsert registers (hash, cluster, variant). Idempotent on the
// (content_hash, variant_id) unique index — second call with the
// same variant is a no-op so retried handlers don't inflate counts.
func (r *RawRefRepository) Upsert(ctx context.Context, hash, clusterID, variantID string) error {
	ref := &domain.RawRef{
		ContentHash: hash,
		ClusterID:   clusterID,
		VariantID:   variantID,
	}
	return r.db(ctx, false).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(ref).Error
}

// CountByHash returns the number of variants referencing this hash.
// A return of 0 means the raw/ blob is garbage.
func (r *RawRefRepository) CountByHash(ctx context.Context, hash string) (int64, error) {
	var n int64
	err := r.db(ctx, true).
		Model(&domain.RawRef{}).
		Where("content_hash = ?", hash).
		Count(&n).Error
	return n, err
}

// DeleteByCluster removes every ref row for the given cluster and
// returns the hashes whose ref count transitioned to zero — i.e.
// orphaned hashes that the caller should now GC from R2.
//
// Executed in a single transaction so the "after" counts are
// consistent with the deletion.
func (r *RawRefRepository) DeleteByCluster(ctx context.Context, clusterID string) ([]string, error) {
	var orphans []string
	err := r.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		// Collect the hashes we're about to drop refs for.
		var hashes []string
		if err := tx.Model(&domain.RawRef{}).
			Where("cluster_id = ?", clusterID).
			Distinct("content_hash").
			Pluck("content_hash", &hashes).Error; err != nil {
			return err
		}
		// Delete this cluster's rows.
		if err := tx.Where("cluster_id = ?", clusterID).
			Delete(&domain.RawRef{}).Error; err != nil {
			return err
		}
		// For each hash, check if any other variant still refs it.
		for _, h := range hashes {
			var n int64
			if err := tx.Model(&domain.RawRef{}).
				Where("content_hash = ?", h).
				Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				orphans = append(orphans, h)
			}
		}
		return nil
	})
	return orphans, err
}
