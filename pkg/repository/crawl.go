package repository

import (
	"context"
	"errors"
	"time"

	"github.com/rs/xid"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// CrawlRepository wraps GORM operations for CrawlJob and RawPayload entities.
// Both tables are TimescaleDB hypertables (see migration 0019); the structs
// don't embed frame's BaseModel because the composite PK conflicts with the
// `primary_key` tag on ID. As a result, this repo populates xid + the partition
// time column explicitly when the caller hasn't.
type CrawlRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewCrawlRepository creates a new CrawlRepository.
func NewCrawlRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *CrawlRepository {
	return &CrawlRepository{db: db}
}

// Create inserts a new crawl job record. Populates ID + ScheduledAt if either
// is zero — frame's BaseModel.BeforeCreate hook is absent on the hypertable
// model, so the repo carries that responsibility.
func (r *CrawlRepository) Create(ctx context.Context, job *domain.CrawlJob) error {
	if job.ID == "" {
		job.ID = xid.New().String()
	}
	if job.ScheduledAt.IsZero() {
		job.ScheduledAt = time.Now().UTC()
	}
	return r.db(ctx, false).Create(job).Error
}

// GetByIdempotencyKey returns the crawl job matching the unique key, or
// (nil, nil) if none exists. The unique index covers (idempotency_key,
// scheduled_at); the lookup is by idempotency_key alone because the
// scheduler stamps the same key for any redelivery of the same tick.
func (r *CrawlRepository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.CrawlJob, error) {
	var job domain.CrawlJob
	err := r.db(ctx, true).
		Where("idempotency_key = ?", key).
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
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

// Finish records terminal state + per-run counts. Idempotent on subsequent
// calls (overwrites are intentional — a redelivery would replay with the
// same counts).
func (r *CrawlRepository) Finish(
	ctx context.Context,
	id string,
	status domain.CrawlJobStatus,
	jobsFound, jobsStored int,
	errorCode, errorMessage string,
) error {
	now := time.Now().UTC()
	return r.db(ctx, false).
		Model(&domain.CrawlJob{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":        status,
			"finished_at":   now,
			"jobs_found":    jobsFound,
			"jobs_stored":   jobsStored,
			"error_code":    errorCode,
			"error_message": errorMessage,
		}).Error
}

// SaveRawPayload inserts a raw HTTP payload record associated with a crawl job.
// Populates ID + FetchedAt if either is zero.
func (r *CrawlRepository) SaveRawPayload(ctx context.Context, payload *domain.RawPayload) error {
	if payload.ID == "" {
		payload.ID = xid.New().String()
	}
	if payload.FetchedAt.IsZero() {
		payload.FetchedAt = time.Now().UTC()
	}
	return r.db(ctx, false).Create(payload).Error
}

// GetRawPayload returns the raw_payloads row matching id, or
// (nil, nil) when not found (expired by retention or never written).
// Used by /admin/raw_payloads/{id}/body.
func (r *CrawlRepository) GetRawPayload(ctx context.Context, id string) (*domain.RawPayload, error) {
	var rp domain.RawPayload
	err := r.db(ctx, true).Where("id = ?", id).First(&rp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rp, nil
}

// CrawlJobSummary is the per-row projection /admin/crawl_jobs returns.
// Decoupled from domain.CrawlJob because the API shape carries a
// computed `raw_payloads` count joined in via a correlated subquery.
type CrawlJobSummary struct {
	ID             string                `json:"id"`
	IdempotencyKey string                `json:"idempotency_key"`
	ScheduledAt    time.Time             `json:"scheduled_at"`
	StartedAt      *time.Time            `json:"started_at,omitempty"`
	FinishedAt     *time.Time            `json:"finished_at,omitempty"`
	Status         domain.CrawlJobStatus `json:"status"`
	JobsFound      int                   `json:"jobs_found"`
	JobsStored     int                   `json:"jobs_stored"`
	RawPayloads    int                   `json:"raw_payloads"`
	ErrorCode      string                `json:"error_code,omitempty"`
}

// ListBySource returns the most-recent N crawl jobs for a source,
// each with a count of raw_payloads it produced. Used by
// GET /admin/crawl_jobs?source_id=...
func (r *CrawlRepository) ListBySource(ctx context.Context, sourceID string, limit int) ([]CrawlJobSummary, error) {
	rows := []CrawlJobSummary{}
	err := r.db(ctx, true).Raw(`
        SELECT cj.id, cj.idempotency_key, cj.scheduled_at, cj.started_at, cj.finished_at,
               cj.status, cj.jobs_found, cj.jobs_stored, cj.error_code,
               (SELECT count(*) FROM raw_payloads rp WHERE rp.crawl_job_id = cj.id) AS raw_payloads
          FROM crawl_jobs cj
         WHERE cj.source_id = ?
         ORDER BY cj.scheduled_at DESC
         LIMIT ?
    `, sourceID, limit).Scan(&rows).Error
	return rows, err
}

// ListRawPayloadsBySource returns up to `limit` recently-fetched
// raw_payloads for a source. Used by the reparse admin endpoint
// to enqueue a batch.
func (r *CrawlRepository) ListRawPayloadsBySource(ctx context.Context, sourceID string, since time.Duration, limit int) ([]domain.RawPayload, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	cutoff := time.Now().Add(-since)
	var rows []domain.RawPayload
	err := r.db(ctx, true).
		Where("source_id = ? AND fetched_at >= ?", sourceID, cutoff).
		Order("fetched_at DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// IncrementReparseCount bumps reparse_count + sets last_reparsed_at.
// Called by the crawler's reparse handler after a re-extraction completes.
func (r *CrawlRepository) IncrementReparseCount(ctx context.Context, id string) error {
	return r.db(ctx, false).
		Table("raw_payloads").
		Where("id = ?", id).
		Updates(map[string]any{
			"reparse_count":    gorm.Expr("reparse_count + 1"),
			"last_reparsed_at": time.Now().UTC(),
		}).Error
}
