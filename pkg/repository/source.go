package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stawi.jobs/pkg/domain"
)

// SourceRepository wraps GORM operations for the Source entity.
type SourceRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewSourceRepository creates a new SourceRepository.
func NewSourceRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *SourceRepository {
	return &SourceRepository{db: db}
}

// Upsert inserts or updates a source on conflict of (source_type, base_url).
func (r *SourceRepository) Upsert(ctx context.Context, s *domain.Source) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "type"},
				{Name: "base_url"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"name", "country", "status", "priority",
				"crawl_interval_sec", "health_score", "config",
				"last_seen_at", "next_crawl_at", "updated_at",
			}),
		}).
		Create(s).Error
}

// GetByID returns a source by its primary key.
func (r *SourceRepository) GetByID(ctx context.Context, id int64) (*domain.Source, error) {
	var s domain.Source
	err := r.db(ctx, true).First(&s, id).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ListDue returns active sources whose next_crawl_at is due, ordered by
// next_crawl_at ASC NULLS FIRST, limited to limit rows.
func (r *SourceRepository) ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.Source, error) {
	var sources []*domain.Source
	err := r.db(ctx, true).
		Where("status = ? AND next_crawl_at <= ?", domain.SourceActive, now).
		Order("next_crawl_at ASC NULLS FIRST").
		Limit(limit).
		Find(&sources).Error
	return sources, err
}

// ListAll returns every non-deleted source.
func (r *SourceRepository) ListAll(ctx context.Context) ([]*domain.Source, error) {
	var sources []*domain.Source
	err := r.db(ctx, true).Find(&sources).Error
	return sources, err
}

// UpdateNextCrawl updates scheduling fields after a crawl run.
func (r *SourceRepository) UpdateNextCrawl(ctx context.Context, id int64, nextCrawlAt time.Time, lastSeenAt time.Time, healthScore float64) error {
	return r.db(ctx, false).
		Model(&domain.Source{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"next_crawl_at": nextCrawlAt,
			"last_seen_at":  lastSeenAt,
			"health_score":  healthScore,
		}).Error
}

// UpdateCrawlCursor stores an opaque cursor string used by paginated crawlers.
func (r *SourceRepository) UpdateCrawlCursor(ctx context.Context, id int64, cursor string) error {
	return r.db(ctx, false).
		Model(&domain.Source{}).
		Where("id = ?", id).
		Update("config", cursor).Error
}

// MarkBlocked sets a source status to blocked and resets its health score to 0.
func (r *SourceRepository) MarkBlocked(ctx context.Context, id int64) error {
	return r.db(ctx, false).
		Model(&domain.Source{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       domain.SourceBlocked,
			"health_score": 0.0,
		}).Error
}

// Count returns the number of active (non-deleted) sources.
func (r *SourceRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db(ctx, true).
		Model(&domain.Source{}).
		Where("status = ?", domain.SourceActive).
		Count(&count).Error
	return count, err
}
