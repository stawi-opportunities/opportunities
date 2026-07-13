// Package repository: TraceRepository walks the PostgreSQL ingestion chain
// (crawl_jobs -> job_ingest_queue -> opportunities) for
// the admin /admin/trace/* endpoints.
//
// Read-only. The repo soft-fails on Postgres misses (returns nil + nil)
// so the handler can emit 404 without inspecting GORM error types.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// SourceSummary aggregates trace metrics for a single source over a
// time window.
type SourceSummary struct {
	Window            time.Duration    `json:"-"`
	CrawlJobs         int64            `json:"crawl_jobs"`
	CrawlJobsFailed   int64            `json:"crawl_jobs_failed"`
	VariantsEmitted   int64            `json:"variants_emitted"`
	VariantsPublished int64            `json:"variants_published"`
	VariantsRejected  int64            `json:"variants_rejected"`
	RejectionReasons  map[string]int64 `json:"rejection_reasons"`
	DataSource        string           `json:"data_source"`
}

// CrawlSummary is one row in the SourceTrace.recent_crawls list.
type CrawlSummary struct {
	CrawlJobID  string                `json:"crawl_job_id"`
	ScheduledAt time.Time             `json:"scheduled_at"`
	StartedAt   *time.Time            `json:"started_at,omitempty"`
	FinishedAt  *time.Time            `json:"finished_at,omitempty"`
	DurationMs  int64                 `json:"duration_ms"`
	Status      domain.CrawlJobStatus `json:"status"`
	JobsFound   int                   `json:"jobs_found"`
	JobsStored  int                   `json:"jobs_stored"`
	ErrorCode   string                `json:"error_code,omitempty"`
}

// VariantTimeline carries the full join for a single variant.
type VariantTimeline struct {
	VariantID       string            `json:"variant_id"`
	ExternalID      string            `json:"external_id,omitempty"`
	HardKey         string            `json:"hard_key,omitempty"`
	Source          SourceTrace       `json:"source"`
	CrawlJob        *CrawlSummary     `json:"crawl_job,omitempty"`
	Stages          []StageTransition `json:"stages"`
	CurrentStage    string            `json:"current_stage"`
	OpportunitySlug string            `json:"opportunity_slug,omitempty"`
	LastError       string            `json:"last_error,omitempty"`
}

// SourceTrace is the minimal source projection embedded in
// VariantTimeline / OpportunityVariant rows.
type SourceTrace struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// StageTransition is one row in VariantTimeline.stages. The repo
// currently emits the latest queue state.
type StageTransition struct {
	Stage      string    `json:"stage"`
	At         time.Time `json:"at"`
	DurationMs int64     `json:"duration_ms,omitempty"`
}

// OpportunityVariant is one entry in
// /admin/trace/opportunities/{slug}.variants.
type OpportunityVariant struct {
	VariantID  string      `json:"variant_id"`
	Source     SourceTrace `json:"source"`
	IngestedAt time.Time   `json:"ingested_at"`
	JoinedAt   time.Time   `json:"joined_at"` // canonical_id-set time; approx via stage_at
}

// TraceRepository wraps the read queries that walk the audit chain.
type TraceRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewTraceRepository wires the repo. The db factory is the same
// function shape every other repository in this package uses
// (frame.DatastoreManager().Pool…DB).
func NewTraceRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *TraceRepository {
	return &TraceRepository{db: db}
}

// SourceSummary returns aggregate metrics for a source over a window.
// All counts are served from PostgreSQL.
func (r *TraceRepository) SourceSummary(ctx context.Context, sourceID string, window time.Duration) (*SourceSummary, error) {
	since := time.Now().Add(-window)
	s := SourceSummary{
		Window:           window,
		RejectionReasons: map[string]int64{},
		DataSource:       "postgres",
	}
	err := r.db(ctx, true).Raw(`
		SELECT
		  (SELECT count(*) FROM crawl_jobs WHERE source_id = ? AND scheduled_at >= ?) AS crawl_jobs,
		  (SELECT count(*) FROM crawl_jobs WHERE source_id = ? AND scheduled_at >= ? AND status = 'failed') AS crawl_jobs_failed,
		  (SELECT count(*) FROM job_ingest_queue WHERE source_id = ? AND created_at >= ?) AS variants_emitted,
		  (SELECT count(*) FROM job_ingest_queue WHERE source_id = ? AND created_at >= ? AND status = 'processed') AS variants_published,
		  (SELECT count(*) FROM job_ingest_events WHERE source_id = ? AND occurred_at >= ? AND event_type = 'rejected') AS variants_rejected
	`,
		sourceID, since,
		sourceID, since,
		sourceID, since,
		sourceID, since,
		sourceID, since,
	).Row().Scan(&s.CrawlJobs, &s.CrawlJobsFailed, &s.VariantsEmitted, &s.VariantsPublished, &s.VariantsRejected)
	if err != nil {
		return nil, fmt.Errorf("SourceSummary: %w", err)
	}
	reasons, rerr := r.rejectionReasons(ctx, sourceID, since, time.Time{})
	if rerr != nil {
		return nil, rerr
	}
	s.RejectionReasons = reasons
	return &s, nil
}

// rejectionReasons aggregates low-cardinality reject codes from
// job_ingest_events.details. VariantRejectedV1 stores reasons[] (array);
// older rows may use a single "reason" string. Both are counted.
// until zero means open-ended (now + 1h safety margin).
func (r *TraceRepository) rejectionReasons(ctx context.Context, sourceID string, since, until time.Time) (map[string]int64, error) {
	out := map[string]int64{}
	if until.IsZero() {
		until = time.Now().UTC().Add(time.Hour)
	}
	q := `
		SELECT COALESCE(NULLIF(reason, ''), 'unknown') AS reason, count(*)::bigint
		  FROM (
		    SELECT jsonb_array_elements_text(
		             CASE WHEN jsonb_typeof(details->'reasons') = 'array'
		                  THEN details->'reasons'
		                  ELSE '[]'::jsonb END
		           ) AS reason
		      FROM job_ingest_events
		     WHERE event_type = 'rejected'
		       AND source_id = ?
		       AND occurred_at >= ? AND occurred_at < ?
		    UNION ALL
		    SELECT details->>'reason' AS reason
		      FROM job_ingest_events
		     WHERE event_type = 'rejected'
		       AND source_id = ?
		       AND occurred_at >= ? AND occurred_at < ?
		       AND COALESCE(details->>'reason', '') <> ''
		       AND (details->'reasons' IS NULL OR jsonb_typeof(details->'reasons') <> 'array'
		            OR jsonb_array_length(details->'reasons') = 0)
		  ) r
		 GROUP BY 1
		 ORDER BY 2 DESC
		 LIMIT 50`
	rows, err := r.db(ctx, true).Raw(q,
		sourceID, since, until,
		sourceID, since, until,
	).Rows()
	if err != nil {
		return out, fmt.Errorf("rejectionReasons: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var reason string
		var n int64
		if err := rows.Scan(&reason, &n); err != nil {
			return out, fmt.Errorf("rejectionReasons scan: %w", err)
		}
		out[reason] = n
	}
	return out, rows.Err()
}

// RecentCrawls returns up to limit most-recent crawl_jobs rows for a
// source within a time window.
func (r *TraceRepository) RecentCrawls(ctx context.Context, sourceID string, window time.Duration, limit int) ([]CrawlSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	since := time.Now().Add(-window)
	rows := []CrawlSummary{}
	err := r.db(ctx, true).Raw(`
		SELECT cj.id AS crawl_job_id,
		       cj.scheduled_at, cj.started_at, cj.finished_at,
		       COALESCE(EXTRACT(EPOCH FROM (cj.finished_at - cj.started_at))*1000, 0)::bigint AS duration_ms,
		       cj.status, cj.jobs_found, cj.jobs_stored,
		       COALESCE(cj.error_code, '') AS error_code
		  FROM crawl_jobs cj
		 WHERE cj.source_id = ? AND cj.scheduled_at >= ?
		 ORDER BY cj.scheduled_at DESC
		 LIMIT ?
	`, sourceID, since, limit).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("RecentCrawls: %w", err)
	}
	return rows, nil
}

// VariantTimeline returns the full join for a single variant_id.
// Returns (nil, nil) when the variant isn't in the ingestion queue.
func (r *TraceRepository) VariantTimeline(ctx context.Context, variantID string) (*VariantTimeline, error) {
	var row struct {
		VariantID    string
		HardKey      string
		SourceID     string
		SourceType   string
		CrawlJobID   *string
		ScheduledAt  *time.Time
		StartedAt    *time.Time
		FinishedAt   *time.Time
		CrawlStatus  *string
		JobsFound    *int
		JobsStored   *int
		CurrentStage string
		StageAt      time.Time
		IngestedAt   time.Time
		CanonicalID  *string
		Slug         *string
		LastError    *string
	}
	err := r.db(ctx, true).Raw(`
		SELECT q.variant_id, COALESCE(q.payload->'payload'->>'hard_key',''), q.source_id,
		       COALESCE(s.type::text, '') AS source_type,
		       q.crawl_job_id,
		       cj.scheduled_at, cj.started_at, cj.finished_at,
		       cj.status AS crawl_status, cj.jobs_found, cj.jobs_stored,
		       q.status, q.updated_at, q.created_at,
		       oi.canonical_id, o.slug, q.last_error
		  FROM job_ingest_queue q
	 LEFT JOIN sources s        ON s.id  = q.source_id
	 LEFT JOIN crawl_jobs cj    ON cj.id = q.crawl_job_id
	 LEFT JOIN opportunity_identities oi ON oi.hard_key = q.payload->'payload'->>'hard_key'
	 LEFT JOIN opportunities o  ON o.canonical_id = oi.canonical_id
		 WHERE q.variant_id = ?
		 ORDER BY q.created_at DESC
		 LIMIT 1
	`, variantID).Row().Scan(
		&row.VariantID, &row.HardKey, &row.SourceID, &row.SourceType,
		&row.CrawlJobID, &row.ScheduledAt, &row.StartedAt, &row.FinishedAt, &row.CrawlStatus, &row.JobsFound, &row.JobsStored,
		&row.CurrentStage, &row.StageAt, &row.IngestedAt, &row.CanonicalID, &row.Slug, &row.LastError,
	)
	if err != nil {
		// Row().Scan returns sql.ErrNoRows on miss (not gorm.ErrRecordNotFound
		// — that's only emitted by the higher-level First/Take helpers).
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("VariantTimeline: %w", err)
	}

	out := &VariantTimeline{
		VariantID:    row.VariantID,
		HardKey:      row.HardKey,
		Source:       SourceTrace{ID: row.SourceID, Type: row.SourceType},
		CurrentStage: row.CurrentStage,
		Stages:       []StageTransition{{Stage: row.CurrentStage, At: row.StageAt}},
	}
	if row.CrawlJobID != nil {
		out.CrawlJob = &CrawlSummary{
			CrawlJobID: *row.CrawlJobID,
			Status:     domain.CrawlJobStatus(strValue(row.CrawlStatus)),
		}
		if row.ScheduledAt != nil {
			out.CrawlJob.ScheduledAt = *row.ScheduledAt
		}
		out.CrawlJob.StartedAt = row.StartedAt
		out.CrawlJob.FinishedAt = row.FinishedAt
		if row.JobsFound != nil {
			out.CrawlJob.JobsFound = *row.JobsFound
		}
		if row.JobsStored != nil {
			out.CrawlJob.JobsStored = *row.JobsStored
		}
	}
	if row.Slug != nil {
		out.OpportunitySlug = *row.Slug
	}
	if row.LastError != nil {
		out.LastError = *row.LastError
	}
	return out, nil
}

// OpportunityVariants returns every processed ingestion row joined to a
// canonical (by slug). Used for the canonical-lineage view.
func (r *TraceRepository) OpportunityVariants(ctx context.Context, slug string) ([]OpportunityVariant, error) {
	type row struct {
		VariantID  string    `gorm:"column:variant_id"`
		SourceID   string    `gorm:"column:source_id"`
		SourceType string    `gorm:"column:source_type"`
		IngestedAt time.Time `gorm:"column:ingested_at"`
		JoinedAt   time.Time `gorm:"column:joined_at"`
	}
	raw := []row{}
	err := r.db(ctx, true).Raw(`
		SELECT q.variant_id,
		       q.source_id      AS source_id,
		       COALESCE(s.type::text,'') AS source_type,
		       q.created_at AS ingested_at, q.processed_at AS joined_at
		  FROM job_ingest_queue q
		  JOIN opportunity_identities oi ON oi.hard_key = q.payload->'payload'->>'hard_key'
		  JOIN opportunities o ON o.canonical_id = oi.canonical_id
	 LEFT JOIN sources s        ON s.id = q.source_id
		 WHERE o.slug = ?
		 ORDER BY q.processed_at ASC
	`, slug).Scan(&raw).Error
	if err != nil {
		return nil, fmt.Errorf("OpportunityVariants: %w", err)
	}
	out := make([]OpportunityVariant, 0, len(raw))
	for _, r := range raw {
		out = append(out, OpportunityVariant{
			VariantID:  r.VariantID,
			Source:     SourceTrace{ID: r.SourceID, Type: r.SourceType},
			IngestedAt: r.IngestedAt,
			JoinedAt:   r.JoinedAt,
		})
	}
	return out, nil
}

func strValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// DayDigest is the per-source rollup for a single calendar day. Populated
// by TraceRepository.DayDigest.
type DayDigest struct {
	CrawlJobs         int64            `json:"crawl_jobs"`
	VariantsEmitted   int64            `json:"variants_emitted"`
	VariantsRejected  int64            `json:"variants_rejected"`
	VariantsPublished int64            `json:"variants_published"`
	RejectionReasons  map[string]int64 `json:"rejection_reasons"`
}

// DayDigest returns the crawl/emit/publish counts for a source between
// [start, end). Used by the admin /admin/trace/seeds/{id}/digest
// endpoint for the recent-date (Postgres) path.
func (r *TraceRepository) DayDigest(ctx context.Context, sourceID string, start, end time.Time) (*DayDigest, error) {
	s := DayDigest{RejectionReasons: map[string]int64{}}
	err := r.db(ctx, true).Raw(`
		SELECT
		  (SELECT count(*) FROM crawl_jobs WHERE source_id = ? AND scheduled_at >= ? AND scheduled_at < ?) AS crawl_jobs,
		  (SELECT count(*) FROM job_ingest_queue WHERE source_id = ? AND created_at >= ? AND created_at < ?) AS variants_emitted,
		  (SELECT count(*) FROM job_ingest_queue WHERE source_id = ? AND created_at >= ? AND created_at < ? AND status = 'processed') AS variants_published,
		  (SELECT count(*) FROM job_ingest_events WHERE source_id = ? AND occurred_at >= ? AND occurred_at < ? AND event_type = 'rejected') AS variants_rejected
	`, sourceID, start, end,
		sourceID, start, end,
		sourceID, start, end,
		sourceID, start, end,
	).Row().Scan(&s.CrawlJobs, &s.VariantsEmitted, &s.VariantsPublished, &s.VariantsRejected)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &s, nil
		}
		return nil, fmt.Errorf("DayDigest: %w", err)
	}
	reasons, rerr := r.rejectionReasons(ctx, sourceID, start, end)
	if rerr != nil {
		return nil, rerr
	}
	s.RejectionReasons = reasons
	return &s, nil
}
