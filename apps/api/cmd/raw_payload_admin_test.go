//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// setupRawPayloadAdminDB boots Postgres + migrations + AutoMigrates the
// GORM-owned domain shapes so the SeedSimpleVariant helper can populate
// crawl_jobs, raw_payloads, and pipeline_variants. Mirrors
// setupTraceAdminDB exactly because the two surfaces share the same
// underlying tables.
func setupRawPayloadAdminDB(t *testing.T) testhelpers.PoolFn {
	t.Helper()
	ctx := context.Background()
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, sqlDB))
	testhelpers.ApplyMigrationsDir(t, ctx, sqlDB, "../../../db/migrations")

	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, g.Exec(`DROP TABLE IF EXISTS sources CASCADE`).Error)
	require.NoError(t, g.Exec(`DROP TABLE IF EXISTS opportunities CASCADE`).Error)
	require.NoError(t, g.AutoMigrate(
		&domain.Source{},
		&variantstate.Opportunity{},
		&variantstate.Variant{},
	))
	return func(_ context.Context, _ bool) *gorm.DB { return g }
}

func TestRawPayloadBody_StreamsHTML(t *testing.T) {
	pool := setupRawPayloadAdminDB(t)
	repo := repository.NewCrawlRepository(pool)

	// Seed a sources row + crawl_job + raw_payload using the same helper
	// the trace tests use — it materialises the join chain consistently.
	seed := testhelpers.SeedSimpleVariant(t, pool, "src-rpa", "var-rpa")

	// Look up the raw_payload that the helper just wrote so we have its id.
	rp, err := repo.GetRawPayload(context.Background(), seed.RawPayloadID)
	require.NoError(t, err)
	require.NotNil(t, rp, "seed raw_payload exists")

	// Stash an HTML blob in the fake archive keyed by the seed's content_hash.
	htmlBody := []byte("<html><body><h1>seeded</h1></body></html>")
	arch := archive.NewFakeArchive()
	_, _, err = arch.PutRaw(context.Background(), htmlBody)
	require.NoError(t, err)
	// PutRaw key is sha256(body), not the seed's content_hash. Update the
	// seeded row to point at the real hash so the handler resolves the blob.
	hash, _, err := arch.PutRaw(context.Background(), htmlBody)
	require.NoError(t, err)
	require.NoError(t, pool(context.Background(), false).
		Exec(`UPDATE raw_payloads SET content_hash = ? WHERE id = ?`, hash, rp.ID).Error)

	h := rawPayloadAdminHandler{repo: repo, arch: arch}

	req := httptest.NewRequest("GET", "/admin/raw_payloads/"+rp.ID+"/body", nil)
	req.SetPathValue("id", rp.ID)
	rec := httptest.NewRecorder()
	h.Body(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body)
	require.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Equal(t, rp.SourceURL, rec.Header().Get("X-Source-URL"))
	require.Equal(t, hash, rec.Header().Get("X-Content-Hash"))
	require.Equal(t, htmlBody, rec.Body.Bytes())
}

func TestRawPayloadBody_NotFound(t *testing.T) {
	pool := setupRawPayloadAdminDB(t)
	h := rawPayloadAdminHandler{
		repo: repository.NewCrawlRepository(pool),
		arch: archive.NewFakeArchive(),
	}

	req := httptest.NewRequest("GET", "/admin/raw_payloads/nope/body", nil)
	req.SetPathValue("id", "nope")
	rec := httptest.NewRecorder()
	h.Body(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body)
}

func TestRawPayloadBody_MissingContentHash(t *testing.T) {
	pool := setupRawPayloadAdminDB(t)
	repo := repository.NewCrawlRepository(pool)
	ctx := context.Background()

	testhelpers.EnsureSourceRow(t, pool, "src-rpb")
	job := &domain.CrawlJob{
		SourceID:       "src-rpb",
		ScheduledAt:    time.Now(),
		Status:         domain.CrawlSucceeded,
		Attempt:        1,
		IdempotencyKey: "src-rpb:nohash",
	}
	require.NoError(t, repo.Create(ctx, job))
	rp := &domain.RawPayload{
		CrawlJobID: job.ID,
		SourceID:   "src-rpb",
		SourceURL:  "https://example.com/no-hash",
		StorageURI: "raw/none.html.gz",
		// ContentHash deliberately empty — pre-archive row.
		SizeBytes:  0,
		FetchedAt:  time.Now(),
		HTTPStatus: 200,
		Status:     domain.RawPayloadStatusPending,
	}
	require.NoError(t, repo.SaveRawPayload(ctx, rp))

	h := rawPayloadAdminHandler{repo: repo, arch: archive.NewFakeArchive()}
	req := httptest.NewRequest("GET", "/admin/raw_payloads/"+rp.ID+"/body", nil)
	req.SetPathValue("id", rp.ID)
	rec := httptest.NewRecorder()
	h.Body(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body)
}

func TestRawPayloadBody_ArchiveMiss(t *testing.T) {
	pool := setupRawPayloadAdminDB(t)
	repo := repository.NewCrawlRepository(pool)

	// Seed a row whose content_hash points at a blob the archive doesn't have.
	seed := testhelpers.SeedSimpleVariant(t, pool, "src-rpc", "var-rpc")

	h := rawPayloadAdminHandler{repo: repo, arch: archive.NewFakeArchive()}
	req := httptest.NewRequest("GET", "/admin/raw_payloads/"+seed.RawPayloadID+"/body", nil)
	req.SetPathValue("id", seed.RawPayloadID)
	rec := httptest.NewRecorder()
	h.Body(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body)
}

func TestRawPayloadBody_RequireAdmin(t *testing.T) {
	// Mux-level test confirming the route is gated by requireAdmin —
	// unauthenticated requests must 401 before the handler runs.
	mux := http.NewServeMux()
	registerRawPayloadAdmin(mux,
		repository.NewCrawlRepository(setupRawPayloadAdminDB(t)),
		archive.NewFakeArchive())

	req := httptest.NewRequest("GET", "/admin/raw_payloads/anything/body", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code, "body=%s", rec.Body)
}
