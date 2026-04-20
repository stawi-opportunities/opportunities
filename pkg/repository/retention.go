package repository

import (
	"context"

	"gorm.io/gorm"
)

// RetentionRepository encapsulates the two-stage retention lifecycle:
//   stage 1: flip 'active' → 'expired' once expires_at passes
//   stage 2: physically delete R2 snapshots + flip 'expired' → 'deleted'
//            after a grace window
type RetentionRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewRetentionRepository constructs a RetentionRepository.
func NewRetentionRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *RetentionRepository {
	return &RetentionRepository{db: db}
}

// DeletableRow is the minimal shape required by the stage-2 worker.
type DeletableRow struct {
	ID   string
	Slug string
}

// ExpireDue flips status to 'expired' for any 'active' row past its TTL.
// Returns number of rows affected.
func (r *RetentionRepository) ExpireDue(ctx context.Context) (int64, error) {
	res := r.db(ctx, false).
		Exec(`UPDATE canonical_jobs
		      SET status='expired', published_at=NULL
		      WHERE status='active' AND expires_at IS NOT NULL AND expires_at < now()`)
	return res.RowsAffected, res.Error
}

// SelectDeletable returns up to batchLimit slugs whose grace window has passed.
func (r *RetentionRepository) SelectDeletable(ctx context.Context, graceDays, batchLimit int) ([]DeletableRow, error) {
	var rows []DeletableRow
	err := r.db(ctx, true).
		Table("canonical_jobs").
		Select("id, slug").
		Where("status='expired' AND expires_at < now() - (? || ' days')::interval", graceDays).
		Limit(batchLimit).
		Scan(&rows).Error
	return rows, err
}

// MarkDeleted transitions the given ids to status='deleted'.
// Stamps deleted_status_at so the R2 purge sweeper can locate rows
// whose grace window has elapsed.
func (r *RetentionRepository) MarkDeleted(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db(ctx, false).
		Exec("UPDATE canonical_jobs SET status='deleted', deleted_status_at=now(), published_at=NULL WHERE id = ANY(?)", ids).
		Error
}
