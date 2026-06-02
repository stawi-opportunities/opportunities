//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// setupTraceDB boots Postgres, applies the project's SQL migrations,
// and AutoMigrates the structs the SQL migrations don't own (Source +
// pipeline_variants). Returns the pool factory the repository layer
// expects.
func setupTraceDB(t *testing.T) (testhelpers.PoolFn, context.Context) {
	t.Helper()
	ctx := context.Background()
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, sqlDB))
	testhelpers.ApplyMigrationsDir(t, ctx, sqlDB, "../../db/migrations")

	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	// Migration 0001 declared `sources` with BIGSERIAL id + a
	// `source_type` column; the GORM-owned production schema (managed
	// by AutoMigrate in the matching service) uses varchar(20) id + a
	// `type` column. EnsureOpportunitiesStub plants a minimal
	// `opportunities` table with a single TEXT `id` PK; production
	// uses `canonical_id` + many domain columns. Drop both legacy
	// stubs so AutoMigrate can recreate them with the modern column
	// set the trace queries expect.
	require.NoError(t, g.Exec(`DROP TABLE IF EXISTS sources CASCADE`).Error)
	require.NoError(t, g.Exec(`DROP TABLE IF EXISTS opportunities CASCADE`).Error)

	// Materialise the production column sets: sources (Source struct),
	// opportunities (variantstate.Opportunity), and pipeline_variants's
	// full column set on top of the stub (variant_id, ingested_at).
	require.NoError(t, g.AutoMigrate(
		&domain.Source{},
		&variantstate.Opportunity{},
		&variantstate.Variant{},
	))

	return func(_ context.Context, _ bool) *gorm.DB { return g }, ctx
}

func TestTraceRepository_SourceSummary(t *testing.T) {
	pool, ctx := setupTraceDB(t)

	crawl := repository.NewCrawlRepository(pool)
	testhelpers.EnsureSourceRow(t, pool, "src-trace-1")
	finishedAt := time.Now().Add(-59 * time.Minute)
	job1 := &domain.CrawlJob{
		SourceID:       "src-trace-1",
		ScheduledAt:    time.Now().Add(-1 * time.Hour),
		Status:         domain.CrawlSucceeded,
		Attempt:        1,
		IdempotencyKey: "src-trace-1:t1",
		JobsFound:      3,
		JobsStored:     3,
		FinishedAt:     &finishedAt,
	}
	require.NoError(t, crawl.Create(ctx, job1))

	repo := repository.NewTraceRepository(pool)
	got, err := repo.SourceSummary(ctx, "src-trace-1", 24*time.Hour)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.CrawlJobs, "CrawlJobs")
	require.Equal(t, int64(0), got.CrawlJobsFailed, "CrawlJobsFailed (no failed jobs seeded)")
}

func TestTraceRepository_RecentCrawls_NewestFirst(t *testing.T) {
	pool, _ := setupTraceDB(t)
	testhelpers.SeedSourceWithCrawls(t, pool, "src-trace-2", 3)

	repo := repository.NewTraceRepository(pool)
	rows, err := repo.RecentCrawls(context.Background(), "src-trace-2", 24*time.Hour, 10)
	require.NoError(t, err)
	require.Len(t, rows, 3, "RecentCrawls len")
	require.True(t, rows[0].ScheduledAt.After(rows[1].ScheduledAt),
		"rows not in DESC order: row0=%v row1=%v", rows[0].ScheduledAt, rows[1].ScheduledAt)
}

func TestTraceRepository_VariantTimeline_LinksRawPayload(t *testing.T) {
	pool, _ := setupTraceDB(t)
	seed := testhelpers.SeedSimpleVariant(t, pool, "src-trace-3", "var-trace-3")

	repo := repository.NewTraceRepository(pool)
	tl, err := repo.VariantTimeline(context.Background(), "var-trace-3")
	require.NoError(t, err)
	require.NotNil(t, tl, "VariantTimeline must hit the seeded row")
	require.NotNil(t, tl.RawPayload, "RawPayload join must populate")
	require.Equal(t, seed.RawPayloadID, tl.RawPayload.ID, "RawPayload.ID join")
	require.NotEmpty(t, tl.CurrentStage, "CurrentStage")
	require.Equal(t, "src-trace-3", tl.Source.ID)
}

func TestTraceRepository_VariantTimeline_NotFound(t *testing.T) {
	pool, _ := setupTraceDB(t)
	repo := repository.NewTraceRepository(pool)
	tl, err := repo.VariantTimeline(context.Background(), "does-not-exist")
	require.NoError(t, err, "miss is not an error")
	require.Nil(t, tl, "miss returns nil")
}

func TestTraceRepository_OpportunityVariants(t *testing.T) {
	pool, _ := setupTraceDB(t)
	testhelpers.SeedTwoVariantsOneCanonical(t, pool, "opp-trace-1")

	repo := repository.NewTraceRepository(pool)
	got, err := repo.OpportunityVariants(context.Background(), "opp-trace-1-slug")
	require.NoError(t, err)
	require.Len(t, got, 2, "two variants for the canonical")
}
