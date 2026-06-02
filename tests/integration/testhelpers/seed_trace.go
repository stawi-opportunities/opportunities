//go:build integration

// Package testhelpers — seed helpers for the admin /admin/trace/* tests.
//
// These helpers materialise crawl_jobs + raw_payloads + pipeline_variants
// + opportunities rows so TraceRepository's join queries have something
// to walk. They assume the caller has already:
//
//  1. Booted a Postgres container via PostgresContainerNoMigrate.
//  2. Created the stub tables via EnsureOpportunitiesStub.
//  3. Applied db/migrations (which promotes crawl_jobs + raw_payloads
//     to TimescaleDB hypertables and adds the two pipeline_variants
//     forward-link columns).
//  4. AutoMigrate'd variantstate.Variant and domain.Source so the
//     full column set exists.
//
// The shape of the helpers mirrors the pattern in
// apps/crawler/service/crawl_request_ledger_test.go.
package testhelpers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// PoolFn is the GORM pool factory shape the repository layer uses.
type PoolFn func(ctx context.Context, readOnly bool) *gorm.DB

// SimpleVariantSeed records the IDs SeedSimpleVariant materialises so
// the test can assert join correctness without re-querying.
type SimpleVariantSeed struct {
	SourceID     string
	CrawlJobID   string
	RawPayloadID string
	VariantID    string
}

// EnsureSourceRow inserts a sources row with the minimum NOT NULL
// columns the live schema enforces (matching the GORM Source struct in
// pkg/domain/models.go). Idempotent on id.
func EnsureSourceRow(t *testing.T, pool PoolFn, sourceID string) {
	t.Helper()
	ctx := context.Background()
	db := pool(ctx, false)
	// The Source struct uses (type, base_url) as a unique index — use a
	// unique base_url per id so multiple seeds in the same test don't
	// conflict. The minimal NOT NULL set: type, base_url, country,
	// language, status, priority, crawl_interval_sec, health_score,
	// consecutive_failures, needs_tuning, config, next_crawl_at, kinds,
	// required_attributes_by_kind, quality_window_days, quality_validated,
	// quality_flagged, last_verify_status, auto_approve.
	err := db.Exec(`
		INSERT INTO sources (
			id, type, name, base_url, country, language, status, priority,
			crawl_interval_sec, health_score, consecutive_failures, needs_tuning,
			config, next_crawl_at, kinds, required_attributes_by_kind,
			quality_window_days, quality_validated, quality_flagged,
			last_verify_status, auto_approve, created_at, updated_at
		) VALUES (
			?, 'greenhouse', 'seed', 'https://example.com/'||?, 'US', 'en', 'active', 1,
			3600, 1.0, 0, false,
			'{}', now(), ARRAY['job']::text[], '{}'::jsonb,
			1, 0, 0,
			0, true, now(), now()
		)
		ON CONFLICT (id) DO NOTHING`, sourceID, sourceID).Error
	require.NoError(t, err, "EnsureSourceRow %s", sourceID)
}

// SeedSourceWithCrawls inserts a sources row + n crawl_jobs rows. The
// scheduled_at values are staggered backward from now (newest first),
// one hour apart, all with status='succeeded'.
func SeedSourceWithCrawls(t *testing.T, pool PoolFn, sourceID string, n int) {
	t.Helper()
	ctx := context.Background()
	EnsureSourceRow(t, pool, sourceID)
	crawl := repository.NewCrawlRepository(pool)
	for i := 0; i < n; i++ {
		err := crawl.Create(ctx, &domain.CrawlJob{
			SourceID:       sourceID,
			ScheduledAt:    time.Now().Add(-time.Duration(i) * time.Hour),
			Status:         domain.CrawlSucceeded,
			Attempt:        1,
			IdempotencyKey: fmt.Sprintf("%s:t%d", sourceID, i),
		})
		require.NoError(t, err, "seed crawl_jobs[%d] for %s", i, sourceID)
	}
}

// SeedSimpleVariant creates source + one crawl_job + one raw_payload +
// one pipeline_variants row, all linked together. Returns the IDs so
// the test can verify the join.
func SeedSimpleVariant(t *testing.T, pool PoolFn, sourceID, variantID string) SimpleVariantSeed {
	t.Helper()
	ctx := context.Background()
	EnsureSourceRow(t, pool, sourceID)

	crawl := repository.NewCrawlRepository(pool)
	job := &domain.CrawlJob{
		SourceID:       sourceID,
		ScheduledAt:    time.Now(),
		Status:         domain.CrawlSucceeded,
		Attempt:        1,
		IdempotencyKey: sourceID + ":simple",
	}
	require.NoError(t, crawl.Create(ctx, job), "seed crawl_jobs")

	rp := &domain.RawPayload{
		CrawlJobID:  job.ID,
		SourceID:    sourceID,
		SourceURL:   "https://example.com/list",
		StorageURI:  "raw/" + variantID + ".html.gz",
		ContentHash: "hash-" + variantID,
		SizeBytes:   1024,
		FetchedAt:   time.Now(),
		HTTPStatus:  200,
		Status:      domain.RawPayloadStatusPending,
	}
	require.NoError(t, crawl.SaveRawPayload(ctx, rp), "seed raw_payloads")

	db := pool(ctx, false)
	err := db.Exec(`
		INSERT INTO pipeline_variants (
			variant_id, ingested_at, source_id, hard_key, kind,
			current_stage, raw_payload_id, crawl_job_id, stage_at,
			attempts, created_at, updated_at
		) VALUES (
			?, now(), ?, ?, 'job',
			'ingested', ?, ?, now(),
			'{}'::jsonb, now(), now()
		)
		ON CONFLICT (variant_id, ingested_at) DO NOTHING`,
		variantID, sourceID, "hk-"+variantID, rp.ID, job.ID,
	).Error
	require.NoError(t, err, "seed pipeline_variants")

	return SimpleVariantSeed{
		SourceID:     sourceID,
		CrawlJobID:   job.ID,
		RawPayloadID: rp.ID,
		VariantID:    variantID,
	}
}

// SeedTwoVariantsOneCanonical inserts one opportunities row + two
// pipeline_variants rows joined to it. The slug derived from
// canonicalID by suffixing "-slug".
func SeedTwoVariantsOneCanonical(t *testing.T, pool PoolFn, canonicalID string) {
	t.Helper()
	ctx := context.Background()
	db := pool(ctx, false)

	// One sources row to hang the variants on. The id is namespaced per
	// canonical so multiple seed calls in the same test don't collide.
	sourceID := "src-" + canonicalID
	EnsureSourceRow(t, pool, sourceID)

	slug := canonicalID + "-slug"
	err := db.Exec(`
		INSERT INTO opportunities (
			canonical_id, slug, kind, title, status,
			first_seen_at, last_seen_at, attributes, hidden
		) VALUES (
			?, ?, 'job', 'Test Job', 'active',
			now(), now(), '{}'::jsonb, false
		)
		ON CONFLICT (canonical_id) DO NOTHING`,
		canonicalID, slug,
	).Error
	require.NoError(t, err, "seed opportunities")

	for i := 0; i < 2; i++ {
		vid := fmt.Sprintf("%s-v%d", canonicalID, i)
		// stage_at is staggered i*5 minutes after now() so the
		// OpportunityVariants ASC ordering is deterministic.
		stageAt := time.Now().Add(time.Duration(i*5) * time.Minute)
		err := db.Exec(`
			INSERT INTO pipeline_variants (
				variant_id, ingested_at, source_id, hard_key, kind,
				current_stage, canonical_id, slug, stage_at,
				attempts, created_at, updated_at
			) VALUES (
				?, now(), ?, ?, 'job',
				'canonical', ?, ?, ?,
				'{}'::jsonb, now(), now()
			)
			ON CONFLICT (variant_id, ingested_at) DO NOTHING`,
			vid, sourceID, "hk-"+vid, canonicalID, slug, stageAt,
		).Error
		require.NoError(t, err, "seed pipeline_variants[%d]", i)
	}
}

// FakeSourceRepo is a tiny in-memory implementation of the sourceLookup
// interface the trace handler depends on. Lifted into the package so
// both pkg/repository/trace_test.go and apps/api/cmd/trace_admin_test.go
// can share a single in-memory fake.
type FakeSourceRepo struct {
	Sources map[string]*domain.Source
}

// NewFakeSourceRepo wraps a pre-populated map.
func NewFakeSourceRepo(m map[string]*domain.Source) *FakeSourceRepo {
	if m == nil {
		m = map[string]*domain.Source{}
	}
	return &FakeSourceRepo{Sources: m}
}

// GetByID implements sourceLookup. Returns (nil, nil) on miss, matching
// the production SourceRepository convention.
func (f *FakeSourceRepo) GetByID(_ context.Context, id string) (*domain.Source, error) {
	if s, ok := f.Sources[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}
