package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
)

// CrawlRepository wraps GORM operations for CrawlJob and RawPayload entities.
type CrawlRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewCrawlRepository creates a new CrawlRepository.
func NewCrawlRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *CrawlRepository {
	return &CrawlRepository{db: db}
}

// Create inserts a new crawl job record.
func (r *CrawlRepository) Create(ctx context.Context, job *domain.CrawlJob) error {
	return r.db(ctx, false).Create(job).Error
}

// Start marks a crawl job as running and records its start time.
func (r *CrawlRepository) Start(ctx context.Context, id string) error {
	now := time.Now().UTC()
	return r.db(ctx, false).
		Model(&domain.CrawlJob{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     domain.CrawlRunning,
			"started_at": now,
		}).Error
}

// Finish updates the terminal state of a crawl job.
func (r *CrawlRepository) Finish(ctx context.Context, id string, status domain.CrawlJobStatus, errorCode string) error {
	now := time.Now().UTC()
	return r.db(ctx, false).
		Model(&domain.CrawlJob{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      status,
			"finished_at": now,
			"error_code":  errorCode,
		}).Error
}

// SaveRawPayload inserts a raw HTTP payload record associated with a crawl job.
func (r *CrawlRepository) SaveRawPayload(ctx context.Context, payload *domain.RawPayload) error {
	return r.db(ctx, false).Create(payload).Error
}
