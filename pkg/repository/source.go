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
				"name", "country", "language", "status", "priority",
				"crawl_interval_sec", "health_score", "config",
				"last_seen_at", "next_crawl_at", "updated_at",
			}),
		}).
		Create(s).Error
}

// GetByID returns a source by its primary key.
func (r *SourceRepository) GetByID(ctx context.Context, id string) (*domain.Source, error) {
	var s domain.Source
	err := r.db(ctx, true).First(&s, id).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ListDue returns active and degraded sources whose next_crawl_at is due,
// ordered by next_crawl_at ASC NULLS FIRST, limited to limit rows.
// Paused and disabled sources are excluded.
func (r *SourceRepository) ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.Source, error) {
	var sources []*domain.Source
	err := r.db(ctx, true).
		Where("status IN ? AND next_crawl_at <= ?", []domain.SourceStatus{domain.SourceActive, domain.SourceDegraded}, now).
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
func (r *SourceRepository) UpdateNextCrawl(ctx context.Context, id string, nextCrawlAt time.Time, lastSeenAt time.Time, healthScore float64) error {
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
func (r *SourceRepository) UpdateCrawlCursor(ctx context.Context, id string, cursor string) error {
	return r.db(ctx, false).
		Model(&domain.Source{}).
		Where("id = ?", id).
		Update("config", cursor).Error
}

// MarkBlocked sets a source status to blocked and resets its health score to 0.
func (r *SourceRepository) MarkBlocked(ctx context.Context, id string) error {
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

// RecordSuccess resets failure count, bumps health score, returns source to active.
func (r *SourceRepository) RecordSuccess(ctx context.Context, id string, healthScore float64) error {
	return r.db(ctx, false).Model(&domain.Source{}).Where("id = ?", id).
		Updates(map[string]any{
			"health_score":         healthScore,
			"consecutive_failures": 0,
			"status":               domain.SourceActive,
			"last_seen_at":         time.Now(),
		}).Error
}

// RecordFailure increments failures, drops health, transitions status:
// active (3 failures) → degraded (5 failures) → paused
func (r *SourceRepository) RecordFailure(ctx context.Context, id string, healthScore float64, consecutiveFailures int) error {
	updates := map[string]any{
		"health_score":         healthScore,
		"consecutive_failures": consecutiveFailures,
	}
	if consecutiveFailures >= 5 {
		updates["status"] = domain.SourcePaused
	} else if consecutiveFailures >= 3 {
		updates["status"] = domain.SourceDegraded
	}
	return r.db(ctx, false).Model(&domain.Source{}).Where("id = ?", id).Updates(updates).Error
}

// FlagNeedsTuning marks a source as having connector quality issues.
func (r *SourceRepository) FlagNeedsTuning(ctx context.Context, id string, needsTuning bool) error {
	return r.db(ctx, false).Model(&domain.Source{}).Where("id = ?", id).Update("needs_tuning", needsTuning).Error
}

// PauseSource manually pauses a source.
func (r *SourceRepository) PauseSource(ctx context.Context, id string) error {
	return r.db(ctx, false).Model(&domain.Source{}).Where("id = ?", id).Update("status", domain.SourcePaused).Error
}

// EnableSource re-enables a paused or disabled source.
func (r *SourceRepository) EnableSource(ctx context.Context, id string) error {
	return r.db(ctx, false).Model(&domain.Source{}).Where("id = ?", id).
		Updates(map[string]any{"status": domain.SourceActive, "consecutive_failures": 0}).Error
}

// ListHealthReport returns all sources with health info ordered by worst first.
func (r *SourceRepository) ListHealthReport(ctx context.Context) ([]domain.Source, error) {
	var sources []domain.Source
	err := r.db(ctx, true).Order("health_score ASC, consecutive_failures DESC").Find(&sources).Error
	return sources, err
}

// CountByStatus returns the number of sources with the given status.
func (r *SourceRepository) CountByStatus(ctx context.Context, status domain.SourceStatus) (int64, error) {
	var count int64
	err := r.db(ctx, true).Model(&domain.Source{}).Where("status = ?", status).Count(&count).Error
	return count, err
}

// IncrementQualityValidated increments the validated count in the quality window.
func (r *SourceRepository) IncrementQualityValidated(ctx context.Context, id string) error {
	return r.db(ctx, false).Model(&domain.Source{}).
		Where("id = ?", id).
		UpdateColumn("quality_validated", gorm.Expr("quality_validated + 1")).Error
}

// IncrementQualityFlagged increments the flagged count in the quality window.
func (r *SourceRepository) IncrementQualityFlagged(ctx context.Context, id string) error {
	return r.db(ctx, false).Model(&domain.Source{}).
		Where("id = ?", id).
		UpdateColumn("quality_flagged", gorm.Expr("quality_flagged + 1")).Error
}

// GetQualityRate returns the failure rate for a source's quality window.
func (r *SourceRepository) GetQualityRate(ctx context.Context, id string) (float64, int, error) {
	var src domain.Source
	err := r.db(ctx, true).Select("quality_validated, quality_flagged").Where("id = ?", id).First(&src).Error
	if err != nil {
		return 0, 0, err
	}
	total := src.QualityValidated + src.QualityFlagged
	if total == 0 {
		return 0, 0, nil
	}
	return float64(src.QualityFlagged) / float64(total), total, nil
}

// ReduceCrawlFrequency multiplies crawl_interval_sec by 3 (reduces crawl rate),
// capped at 604800 seconds (7 days) to prevent unbounded growth.
func (r *SourceRepository) ReduceCrawlFrequency(ctx context.Context, id string) error {
	return r.db(ctx, false).Model(&domain.Source{}).
		Where("id = ?", id).
		UpdateColumn("crawl_interval_sec", gorm.Expr("LEAST(crawl_interval_sec * 3, 604800)")).Error
}

// DisableSource sets a source's status to disabled.
func (r *SourceRepository) DisableSource(ctx context.Context, id string) error {
	return r.db(ctx, false).Model(&domain.Source{}).
		Where("id = ?", id).
		Update("status", domain.SourceDisabled).Error
}

// RecordVerifyResult stores the outcome of a pre-crawl reachability probe.
// On failure, callers should also push NextCrawlAt out via UpdateNextCrawl
// (kept separate so the caller can choose the backoff curve).
func (r *SourceRepository) RecordVerifyResult(ctx context.Context, id string, status int, at time.Time) error {
	return r.db(ctx, false).Model(&domain.Source{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"last_verified_at":   at,
			"last_verify_status": status,
		}).Error
}

// ResetQualityWindow resets counters and doubles the window (cap 14 days).
func (r *SourceRepository) ResetQualityWindow(ctx context.Context, id string) error {
	now := time.Now()
	return r.db(ctx, false).Model(&domain.Source{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"quality_window_start": now,
			"quality_window_days":  gorm.Expr("LEAST(quality_window_days * 2, 14)"),
			"quality_validated":    0,
			"quality_flagged":      0,
		}).Error
}
