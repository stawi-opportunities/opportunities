//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// setupTraceAdminDB boots Postgres, applies the project migrations, and
// AutoMigrates the GORM-owned domain shapes the trace queries assume.
// Mirrors setupTraceDB in pkg/repository/trace_test.go.
func setupTraceAdminDB(t *testing.T) testhelpers.PoolFn {
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

// adminTraceReq builds an httptest request with admin Bearer + claims
// already attached. Uses setAdminAuth from sources_admin_test.go since
// both files live in package main.
func adminTraceReq(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	setAdminAuth(req)
	return req
}

func TestTraceAdmin_SourceTrace_ReturnsSummary(t *testing.T) {
	pool := setupTraceAdminDB(t)
	testhelpers.SeedSourceWithCrawls(t, pool, "src-trace-A", 3)

	h := traceAdminHandler{
		trace: repository.NewTraceRepository(pool),
		sources: testhelpers.NewFakeSourceRepo(map[string]*domain.Source{
			"src-trace-A": {
				BaseModel: domain.BaseModel{ID: "src-trace-A"},
				Type:      domain.SourceGreenhouse,
				BaseURL:   "https://example.com/src-trace-A",
			},
		}),
	}
	req := adminTraceReq("GET", "/admin/trace/sources/src-trace-A?since=24h")
	req.SetPathValue("id", "src-trace-A")
	rec := httptest.NewRecorder()
	h.SourceTrace(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body)

	var resp struct {
		Source  map[string]any `json:"source"`
		Summary struct {
			CrawlJobs int64 `json:"crawl_jobs"`
		} `json:"summary"`
		RecentCrawls []map[string]any `json:"recent_crawls"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, int64(3), resp.Summary.CrawlJobs, "summary.crawl_jobs")
	require.Len(t, resp.RecentCrawls, 3, "recent_crawls")
	require.Equal(t, "src-trace-A", resp.Source["id"])
}

func TestTraceAdmin_SourceTrace_NotFound(t *testing.T) {
	pool := setupTraceAdminDB(t)
	h := traceAdminHandler{
		trace:   repository.NewTraceRepository(pool),
		sources: testhelpers.NewFakeSourceRepo(nil),
	}
	req := adminTraceReq("GET", "/admin/trace/sources/nope?since=24h")
	req.SetPathValue("id", "nope")
	rec := httptest.NewRecorder()
	h.SourceTrace(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body)
}

func TestTraceAdmin_SourceTrace_MissingID(t *testing.T) {
	pool := setupTraceAdminDB(t)
	h := traceAdminHandler{
		trace:   repository.NewTraceRepository(pool),
		sources: testhelpers.NewFakeSourceRepo(nil),
	}
	req := adminTraceReq("GET", "/admin/trace/sources/")
	// PathValue("id") returns "" — handler should reject.
	rec := httptest.NewRecorder()
	h.SourceTrace(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body)
}
