// Package repository: TraceRepository walks the crawl-to-publish chain
// (crawl_jobs -> raw_payloads -> pipeline_variants -> opportunities) for
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
// time window. The base aggregate is Postgres-only; the api handler
// layers Iceberg historic rejections on top when the requested window
// exceeds the pipeline_variants 7d retention. The DataSource flag tells
// the operator UI which path was used so it can surface a "historic
// data included" badge.
type SourceSummary struct {
	Window            time.Duration    `json:"-"`
	CrawlJobs         int64            `json:"crawl_jobs"`
	CrawlJobsFailed   int64            `json:"crawl_jobs_failed"`
	RawPayloads       int64            `json:"raw_payloads"`
	VariantsEmitted   int64            `json:"variants_emitted"`
	VariantsPublished int64            `json:"variants_published"`
	VariantsRejected  int64            `json:"variants_rejected"`
	RejectionReasons  map[string]int64 `json:"rejection_reasons"`
	// DataSource is "postgres" by default; the api handler upgrades
	// it to "postgres+iceberg" after a successful historic read.
	DataSource string `json:"data_source"`
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
	RawPayloads int64                 `json:"raw_payloads"`
	ErrorCode   string                `json:"error_code,omitempty"`
}

// VariantTimeline carries the full join for a single variant.
type VariantTimeline struct {
	VariantID       string            `json:"variant_id"`
	ExternalID      string            `json:"external_id,omitempty"`
	HardKey         string            `json:"hard_key,omitempty"`
	Source          SourceTrace       `json:"source"`
	CrawlJob        *CrawlSummary     `json:"crawl_job,omitempty"`
	RawPayload      *RawPayloadTrace  `json:"raw_payload,omitempty"`
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

// RawPayloadTrace is the raw_payloads projection embedded in
// VariantTimeline. body_url is populated by the handler (not the repo)
// so the UI can link directly to /admin/raw_payloads/{id}/body.
type RawPayloadTrace struct {
	ID          string    `json:"id"`
	SourceURL   string    `json:"source_url,omitempty"`
	StorageURI  string    `json:"storage_uri,omitempty"`
	ContentHash string    `json:"content_hash,omitempty"`
	SizeBytes   int64     `json:"size_bytes"`
	FetchedAt   time.Time `json:"fetched_at"`
	HTTPStatus  int       `json:"http_status"`
	BodyURL     string    `json:"body_url,omitempty"`
}

// StageTransition is one row in VariantTimeline.stages. The repo
// currently emits a single row (current_stage); a follow-up plan can
// hydrate the full history from the Iceberg variants.* sinks.
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
// All counts are Postgres-only — VariantsRejected is 0 until Plan C
// wires the Iceberg fallback for the variants_rejected sink.
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
		  (SELECT count(*) FROM raw_payloads rp
		     JOIN crawl_jobs cj ON cj.id = rp.crawl_job_id
		    WHERE cj.source_id = ? AND rp.fetched_at >= ?) AS raw_payloads,
		  (SELECT count(*) FROM pipeline_variants WHERE source_id = ? AND ingested_at >= ?) AS variants_emitted,
		  (SELECT count(*) FROM pipeline_variants WHERE source_id = ? AND ingested_at >= ? AND current_stage = 'published') AS variants_published,
		  0::bigint AS variants_rejected
	`,
		sourceID, since,
		sourceID, since,
		sourceID, since,
		sourceID, since,
		sourceID, since,
	).Row().Scan(&s.CrawlJobs, &s.CrawlJobsFailed, &s.RawPayloads, &s.VariantsEmitted, &s.VariantsPublished, &s.VariantsRejected)
	if err != nil {
		return nil, fmt.Errorf("SourceSummary: %w", err)
	}
	return &s, nil
}

// RecentCrawls returns up to limit most-recent crawl_jobs rows for a
// source within a time window, each annotated with its raw_payloads
// count.
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
		       COALESCE(cj.error_code, '') AS error_code,
		       (SELECT count(*) FROM raw_payloads rp WHERE rp.crawl_job_id = cj.id) AS raw_payloads
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
// Returns (nil, nil) when the variant isn't in pipeline_variants
// (retention expired or never written).
func (r *TraceRepository) VariantTimeline(ctx context.Context, variantID string) (*VariantTimeline, error) {
	var row struct {
		VariantID     string
		HardKey       string
		SourceID      string
		SourceType    string
		CrawlJobID    *string
		ScheduledAt   *time.Time
		StartedAt     *time.Time
		FinishedAt    *time.Time
		CrawlStatus   *string
		JobsFound     *int
		JobsStored    *int
		RawPayloadID  *string
		RPSourceURL   *string
		RPStorageURI  *string
		RPContentHash *string
		RPSizeBytes   *int64
		RPFetchedAt   *time.Time
		RPHTTPStatus  *int
		CurrentStage  string
		StageAt       time.Time
		IngestedAt    time.Time
		CanonicalID   *string
		Slug          *string
		LastError     *string
	}
	err := r.db(ctx, true).Raw(`
		SELECT pv.variant_id, pv.hard_key, pv.source_id,
		       COALESCE(s.type::text, '') AS source_type,
		       pv.crawl_job_id,
		       cj.scheduled_at, cj.started_at, cj.finished_at,
		       cj.status AS crawl_status, cj.jobs_found, cj.jobs_stored,
		       pv.raw_payload_id,
		       rp.source_url AS rp_source_url, rp.storage_uri AS rp_storage_uri,
		       rp.content_hash AS rp_content_hash, rp.size_bytes AS rp_size_bytes,
		       rp.fetched_at AS rp_fetched_at, rp.http_status AS rp_http_status,
		       pv.current_stage, pv.stage_at, pv.ingested_at,
		       pv.canonical_id, o.slug, pv.last_error
		  FROM pipeline_variants pv
	 LEFT JOIN sources s        ON s.id  = pv.source_id
	 LEFT JOIN crawl_jobs cj    ON cj.id = pv.crawl_job_id
	 LEFT JOIN raw_payloads rp  ON rp.id = pv.raw_payload_id
	 LEFT JOIN opportunities o  ON o.canonical_id = pv.canonical_id
		 WHERE pv.variant_id = ?
		 ORDER BY pv.ingested_at DESC
		 LIMIT 1
	`, variantID).Row().Scan(
		&row.VariantID, &row.HardKey, &row.SourceID, &row.SourceType,
		&row.CrawlJobID, &row.ScheduledAt, &row.StartedAt, &row.FinishedAt, &row.CrawlStatus, &row.JobsFound, &row.JobsStored,
		&row.RawPayloadID, &row.RPSourceURL, &row.RPStorageURI, &row.RPContentHash, &row.RPSizeBytes, &row.RPFetchedAt, &row.RPHTTPStatus,
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
	if row.RawPayloadID != nil {
		out.RawPayload = &RawPayloadTrace{
			ID:          *row.RawPayloadID,
			SourceURL:   strValue(row.RPSourceURL),
			StorageURI:  strValue(row.RPStorageURI),
			ContentHash: strValue(row.RPContentHash),
		}
		if row.RPSizeBytes != nil {
			out.RawPayload.SizeBytes = *row.RPSizeBytes
		}
		if row.RPFetchedAt != nil {
			out.RawPayload.FetchedAt = *row.RPFetchedAt
		}
		if row.RPHTTPStatus != nil {
			out.RawPayload.HTTPStatus = *row.RPHTTPStatus
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

// OpportunityVariants returns every pipeline_variants row joined to a
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
		SELECT pv.variant_id,
		       pv.source_id      AS source_id,
		       COALESCE(s.type::text,'') AS source_type,
		       pv.ingested_at, pv.stage_at AS joined_at
		  FROM pipeline_variants pv
		  JOIN opportunities o ON o.canonical_id = pv.canonical_id
	 LEFT JOIN sources s        ON s.id = pv.source_id
		 WHERE o.slug = ?
		 ORDER BY pv.stage_at ASC
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
// by TraceRepository.DayDigest for the Postgres path; the api handler
// fills the same shape from Iceberg when the date is older than the
// pipeline_variants 7d retention. RejectionReasons is left empty by the
// Postgres path because variants_rejected isn't persisted to Postgres —
// the Iceberg branch fills it.
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
		  (SELECT count(*) FROM pipeline_variants WHERE source_id = ? AND ingested_at >= ? AND ingested_at < ?) AS variants_emitted,
		  (SELECT count(*) FROM pipeline_variants WHERE source_id = ? AND ingested_at >= ? AND ingested_at < ? AND current_stage = 'published') AS variants_published,
		  0::bigint AS variants_rejected
	`, sourceID, start, end,
		sourceID, start, end,
		sourceID, start, end,
	).Row().Scan(&s.CrawlJobs, &s.VariantsEmitted, &s.VariantsPublished, &s.VariantsRejected)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &s, nil
		}
		return nil, fmt.Errorf("DayDigest: %w", err)
	}
	return &s, nil
}
