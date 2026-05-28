//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// TestDigest_PostgresPath_RecentDate exercises the within-retention
// path: the requested date is today, so Postgres is the data source
// and crawl_jobs/variants_emitted should be > 0 after seeding.
func TestDigest_PostgresPath_RecentDate(t *testing.T) {
	pool := setupTraceAdminDB(t)
	testhelpers.SeedSourceWithCrawls(t, pool, "src-dig-1", 3)

	h := digestAdminHandler{trace: repository.NewTraceRepository(pool)}
	today := time.Now().UTC().Format("2006-01-02")

	req := adminTraceReq("GET", "/admin/trace/seeds/src-dig-1/digest?date="+today)
	req.SetPathValue("id", "src-dig-1")
	rec := httptest.NewRecorder()
	h.Digest(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "postgres", resp["data_source"])
	require.Equal(t, "src-dig-1", resp["source_id"])
	require.Equal(t, today, resp["date"])

	// Seeded 3 crawls — count comes back as float64 through json.Unmarshal.
	require.GreaterOrEqual(t, resp["crawl_jobs"].(float64), float64(1),
		"crawl_jobs should reflect the seeded crawls")
}

// TestDigest_IcebergPath_NoCatalogReturns503 confirms that an old date
// with no Iceberg catalog wired returns 503 — operator UI surfaces a
// clear "configure ICEBERG_CATALOG_URI" message instead of an empty body.
func TestDigest_IcebergPath_OldDate_NoCatalogReturns503(t *testing.T) {
	pool := setupTraceAdminDB(t)
	h := digestAdminHandler{trace: repository.NewTraceRepository(pool), iceberg: nil}
	// Date well outside the 7d retention window.
	oldDate := time.Now().UTC().Add(-30 * 24 * time.Hour).Format("2006-01-02")

	req := adminTraceReq("GET", "/admin/trace/seeds/x/digest?date="+oldDate)
	req.SetPathValue("id", "x")
	rec := httptest.NewRecorder()
	h.Digest(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, "body=%s", rec.Body.String())
}

// TestDigest_BadDate_Returns400 — operator typo guard.
func TestDigest_BadDate_Returns400(t *testing.T) {
	pool := setupTraceAdminDB(t)
	h := digestAdminHandler{trace: repository.NewTraceRepository(pool)}
	req := adminTraceReq("GET", "/admin/trace/seeds/x/digest?date=not-a-date")
	req.SetPathValue("id", "x")
	rec := httptest.NewRecorder()
	h.Digest(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestDigest_MissingID_Returns400 — path-value guard.
func TestDigest_MissingID_Returns400(t *testing.T) {
	pool := setupTraceAdminDB(t)
	h := digestAdminHandler{trace: repository.NewTraceRepository(pool)}
	req := adminTraceReq("GET", "/admin/trace/seeds//digest")
	// PathValue("id") returns "" — handler should reject.
	rec := httptest.NewRecorder()
	h.Digest(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
