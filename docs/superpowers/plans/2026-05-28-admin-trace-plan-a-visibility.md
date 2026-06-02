# Admin Trace — Plan A: Visibility + Rejection Drill-Down

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Operators can answer "where did this go?" for any source, variant, or canonical, plus drill into rejections by reading the originating HTML.

**Architecture:** Four read-only Postgres trace endpoints on the API service (joining `crawl_jobs` × `raw_payloads` × `pipeline_variants` × `opportunities`), one R2 body-fetch endpoint, a tightened `requireAdmin` that enforces the `admin` role on JWT claims, and a Vite/React `/admin` sub-app scaffold mounted by the existing Hugo site. The event schema (`VariantRejectedV1`) grows two fields so rejections link back to the raw HTML.

**Tech Stack:** Go (Frame v1.97+, GORM), Postgres + TimescaleDB, pkg/archive (R2), Vite + React + `@stawi/auth-runtime`, Hugo.

**Depends on:** v8.0.60 (live) — `pipeline_variants.raw_payload_id` / `crawl_job_id` columns exist, `opportunities.employment_type/seniority/geo_scope` columns exist.

---

## File Structure

**Created (Go):**
- `apps/api/cmd/trace_admin.go` — handlers + repo glue for the four trace endpoints.
- `apps/api/cmd/trace_admin_test.go` — integration tests against ephemeral Postgres.
- `apps/api/cmd/raw_payload_admin.go` — `/admin/raw_payloads/{id}/body` handler.
- `pkg/repository/trace.go` — `TraceRepository` with the join queries.
- `pkg/repository/trace_test.go` — integration tests.

**Created (UI):**
- `ui/admin/index.html` — Hugo page shell.
- `ui/admin/vite.config.ts`
- `ui/admin/tsconfig.json`
- `ui/admin/package.json` (or extend root `package.json` workspaces).
- `ui/admin/src/main.tsx` — mounts the app after role check.
- `ui/admin/src/api/admin-client.ts` — fetch wrappers using `@stawi/auth-runtime`.
- `ui/admin/src/pages/SourceTrace.tsx` — one page; renders the source trace JSON.
- `ui/admin/src/components/AppGate.tsx` — role check + redirect.
- `ui/admin/src/styles/admin.css` — minimal CSS.

**Modified (Go):**
- `apps/api/cmd/sources_admin.go` lines 645-655 (`requireAdmin`) — add role check.
- `apps/api/cmd/main.go` — wire the new trace routes + raw_payload route.
- `pkg/events/v1/pipeline.go` (the `VariantRejectedV1` struct around line 71) — add two optional string fields.
- `apps/crawler/service/crawl_request_handler.go` lines 565-606 (`publishRejected`) — populate the new fields.
- `apps/writer/service/arrow_schemas.go` — declare the two new optional STRING columns on the `variants_rejected` schema.
- `apps/writer/service/arrow_build.go` (`BuildVariantRejectedRecord`) — emit the new columns.

**Modified (deployment.manifests):**
- `namespaces/product-opportunities/api/opportunities-api.yaml` — no env change required (Frame's existing security stack already extracts Roles); just confirm the admin role is present in the Hydra client's allowed scopes.
- Hugo's main `static/` build picks up `ui/admin/dist/` via the existing build glob — no manifest change needed unless the routing requires it (see Task 11).

---

## Tasks

### Task 1: Tighten `requireAdmin` to enforce the `admin` role

**Files:**
- Modify: `apps/api/cmd/sources_admin.go:645-655`
- Modify: existing tests if any assert on the old token-only behaviour (`grep -rn "requireAdmin\|TestRequireAdmin" apps/api/cmd/`).

- [ ] **Step 1: Write the failing test**

Append to `apps/api/cmd/sources_admin_test.go` (or wherever the existing requireAdmin tests live; if none, create `apps/api/cmd/admin_auth_test.go`):

```go
package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pitabwire/frame/security"
)

func TestRequireAdmin_NoBearer_Returns401(t *testing.T) {
	called := false
	h := requireAdmin(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/admin/test", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if called {
		t.Fatal("handler invoked despite missing token")
	}
}

func TestRequireAdmin_BearerButNoAdminRole_Returns403(t *testing.T) {
	called := false
	h := requireAdmin(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/admin/test", nil)
	req.Header.Set("Authorization", "Bearer fake-token")
	claims := &security.AuthenticationClaims{}
	claims.Roles = []string{"candidate"} // any non-admin role
	ctx := claims.ClaimsToContext(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", rec.Code)
	}
	if called {
		t.Fatal("handler invoked despite missing admin role")
	}
}

func TestRequireAdmin_BearerAndAdminRole_PassesThrough(t *testing.T) {
	called := false
	h := requireAdmin(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest("GET", "/admin/test", nil)
	req.Header.Set("Authorization", "Bearer fake-token")
	claims := &security.AuthenticationClaims{}
	claims.Roles = []string{"admin", "candidate"}
	ctx := claims.ClaimsToContext(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if !called {
		t.Fatal("handler not invoked despite admin role")
	}
}
```

- [ ] **Step 2: Run to verify two fail**

```bash
cd /home/j/code/stawi.opportunities
go test ./apps/api/cmd/ -run TestRequireAdmin -v 2>&1 | tail -15
```

Expected: `TestRequireAdmin_BearerButNoAdminRole_Returns403` FAILS (current `requireAdmin` returns 200 because it only checks Bearer presence). `TestRequireAdmin_BearerAndAdminRole_PassesThrough` PASSES (admin role wasn't checked, so anything works). `TestRequireAdmin_NoBearer_Returns401` PASSES (token check is correct).

- [ ] **Step 3: Update `requireAdmin`**

Replace the function at `apps/api/cmd/sources_admin.go:645-655` with:

```go
func requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing Bearer token")
			return
		}
		claims := security.ClaimsFromContext(r.Context())
		if claims == nil {
			// Bearer present but unauthenticated context — Frame's security
			// middleware hasn't populated claims (token rejected upstream).
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid Bearer token")
			return
		}
		if !containsRole(claims.GetRoles(), "admin") {
			writeError(w, http.StatusForbidden, "forbidden", "admin role required")
			return
		}
		h(w, r)
	}
}

func containsRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}
```

Add `"github.com/pitabwire/frame/security"` to the imports.

- [ ] **Step 4: Run, verify all three pass**

```bash
go test ./apps/api/cmd/ -run TestRequireAdmin -v 2>&1 | tail -15
```

Expected: all three PASS.

- [ ] **Step 5: Run full api/cmd test suite (catch regressions in source-admin tests that assumed no role check)**

```bash
go test ./apps/api/cmd/... -count=1 2>&1 | tail -10
```

If any existing source-admin test that hits a `requireAdmin`-protected endpoint now fails with 403, update that test to set `claims.Roles = []string{"admin"}` on the request context the way the new tests do.

- [ ] **Step 6: Commit**

```bash
git add apps/api/cmd/sources_admin.go apps/api/cmd/admin_auth_test.go apps/api/cmd/sources_admin_test.go
git commit -m "feat(admin): requireAdmin enforces 'admin' role on JWT claims

Previously only checked for Bearer-token presence — any
authenticated user could hit admin endpoints. Now reads
security.ClaimsFromContext, checks for the 'admin' role, and
returns 403 if missing.

The JS auth-runtime exposes getRoles() which parses the same
JWT shape (roles[] or realm_access.roles[]) so the React app
self-gates symmetrically."
```

---

### Task 2: TraceRepository — three Postgres join queries

**Files:**
- Create: `pkg/repository/trace.go`
- Create: `pkg/repository/trace_test.go`

This task adds the data layer; subsequent tasks add the HTTP handlers.

- [ ] **Step 1: Write the failing test**

Create `pkg/repository/trace_test.go`:

```go
//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func TestTraceRepository_SourceSummary(t *testing.T) {
	ctx := context.Background()
	pool := testhelpers.PostgresPool(t)

	// Seed: 1 source, 2 crawl_jobs, 3 raw_payloads, 5 pipeline_variants.
	crawl := repository.NewCrawlRepository(pool)
	job1 := &domain.CrawlJob{
		SourceID:       "src-trace-1",
		ScheduledAt:    time.Now().Add(-1 * time.Hour),
		Status:         domain.CrawlSucceeded,
		Attempt:        1,
		IdempotencyKey: "src-trace-1:t1",
		JobsFound:      3,
		JobsStored:     3,
	}
	finishedAt := time.Now().Add(-59 * time.Minute)
	job1.FinishedAt = &finishedAt
	_ = crawl.Create(ctx, job1)

	repo := repository.NewTraceRepository(pool)
	got, err := repo.SourceSummary(ctx, "src-trace-1", 24*time.Hour)
	if err != nil {
		t.Fatalf("SourceSummary: %v", err)
	}
	if got.CrawlJobs != 1 {
		t.Fatalf("CrawlJobs = %d; want 1", got.CrawlJobs)
	}
}

func TestTraceRepository_RecentCrawls_NewestFirst(t *testing.T) {
	ctx := context.Background()
	pool := testhelpers.PostgresPool(t)
	crawl := repository.NewCrawlRepository(pool)
	for i := 0; i < 3; i++ {
		_ = crawl.Create(ctx, &domain.CrawlJob{
			SourceID:       "src-trace-2",
			ScheduledAt:    time.Now().Add(time.Duration(-i) * time.Hour),
			Status:         domain.CrawlSucceeded,
			IdempotencyKey: "src-trace-2:t" + string(rune('0'+i)),
		})
	}
	repo := repository.NewTraceRepository(pool)
	rows, err := repo.RecentCrawls(ctx, "src-trace-2", 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("RecentCrawls: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len = %d; want 3", len(rows))
	}
	if !rows[0].ScheduledAt.After(rows[1].ScheduledAt) {
		t.Fatalf("rows not in DESC order: %v", rows)
	}
}

func TestTraceRepository_VariantTimeline_LinksRawPayload(t *testing.T) {
	ctx := context.Background()
	pool := testhelpers.PostgresPool(t)
	// This requires a variantstate.Variant row + raw_payloads row already
	// existing — the existing integration scaffold from Phase 1 seeds them.
	scaffold := testhelpers.SeedSimpleVariant(t, pool, "src-trace-3", "var-trace-3")

	repo := repository.NewTraceRepository(pool)
	tl, err := repo.VariantTimeline(ctx, "var-trace-3")
	if err != nil {
		t.Fatalf("VariantTimeline: %v", err)
	}
	if tl.RawPayload == nil || tl.RawPayload.ID != scaffold.RawPayloadID {
		t.Fatalf("RawPayload link broken: got %+v want %s", tl.RawPayload, scaffold.RawPayloadID)
	}
	if tl.CurrentStage == "" {
		t.Fatal("CurrentStage empty")
	}
}

func TestTraceRepository_OpportunityVariants(t *testing.T) {
	ctx := context.Background()
	pool := testhelpers.PostgresPool(t)
	// Seed two variants joined to the same canonical_id.
	testhelpers.SeedTwoVariantsOneCanonical(t, pool, "opp-trace-1")

	repo := repository.NewTraceRepository(pool)
	got, err := repo.OpportunityVariants(ctx, "opp-trace-1")
	if err != nil {
		t.Fatalf("OpportunityVariants: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("variants len = %d; want 2", len(got))
	}
}
```

Note on test helpers: `testhelpers.SeedSimpleVariant` and `SeedTwoVariantsOneCanonical` don't exist yet. Either add them inline in the test file (duplicate, OK for first plan), OR extend the existing helper. Recommendation: add them inline in `trace_test.go` as local helpers so this plan stays self-contained.

- [ ] **Step 2: Run, verify fail**

```bash
go test -tags=integration ./pkg/repository/ -run TestTraceRepository -v 2>&1 | tail -10
```

Expected: build failure (`NewTraceRepository` undefined).

- [ ] **Step 3: Implement `pkg/repository/trace.go`**

```go
// Package repository: TraceRepository walks the crawl-to-publish chain
// (crawl_jobs -> raw_payloads -> pipeline_variants -> opportunities) for
// the admin /admin/trace/* endpoints.
//
// Read-only. Soft-fails on Postgres miss (returns nil + nil).
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// SourceSummary aggregates trace metrics for a single source over a
// time window. CrawlJobs/Variants counts come from Postgres only —
// any historic data beyond pipeline_variants' 7d retention is missing
// (Plan C wires the Iceberg fallback).
type SourceSummary struct {
	Window            time.Duration            `json:"-"`
	CrawlJobs         int64                    `json:"crawl_jobs"`
	CrawlJobsFailed   int64                    `json:"crawl_jobs_failed"`
	RawPayloads       int64                    `json:"raw_payloads"`
	VariantsEmitted   int64                    `json:"variants_emitted"`
	VariantsPublished int64                    `json:"variants_published"`
	VariantsRejected  int64                    `json:"variants_rejected"`
	RejectionReasons  map[string]int64         `json:"rejection_reasons"` // empty until L5 wires Iceberg
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

type SourceTrace struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type RawPayloadTrace struct {
	ID          string    `json:"id"`
	SourceURL   string    `json:"source_url,omitempty"`
	StorageURI  string    `json:"storage_uri,omitempty"`
	ContentHash string    `json:"content_hash,omitempty"`
	SizeBytes   int64     `json:"size_bytes"`
	FetchedAt   time.Time `json:"fetched_at"`
	HTTPStatus  int       `json:"http_status"`
	BodyURL     string    `json:"body_url,omitempty"` // populated by the handler, not the repo
}

type StageTransition struct {
	Stage      string    `json:"stage"`
	At         time.Time `json:"at"`
	DurationMs int64     `json:"duration_ms,omitempty"`
}

// OpportunityVariant is one entry in /admin/trace/opportunities/{slug}.variants.
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

func NewTraceRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *TraceRepository {
	return &TraceRepository{db: db}
}

// SourceSummary returns the aggregate metrics for a source over a window.
func (r *TraceRepository) SourceSummary(ctx context.Context, sourceID string, window time.Duration) (*SourceSummary, error) {
	since := time.Now().Add(-window)
	var s SourceSummary
	s.Window = window
	s.RejectionReasons = map[string]int64{}
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

// RecentCrawls returns up to `limit` most-recent crawl_jobs rows for a
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
	// Single composite query: variant + its source + crawl_job + raw_payload + canonical slug.
	var row struct {
		VariantID      string
		ExternalID     *string
		HardKey        string
		SourceID       string
		SourceType     string
		CrawlJobID     *string
		ScheduledAt    *time.Time
		StartedAt      *time.Time
		FinishedAt     *time.Time
		CrawlStatus    *string
		JobsFound      *int
		JobsStored     *int
		RawPayloadID   *string
		RPSourceURL    *string
		RPStorageURI   *string
		RPContentHash  *string
		RPSizeBytes    *int64
		RPFetchedAt    *time.Time
		RPHTTPStatus   *int
		CurrentStage   string
		StageAt        time.Time
		IngestedAt     time.Time
		CanonicalID    *string
		Slug           *string
		LastError      *string
	}
	err := r.db(ctx, true).Raw(`
        SELECT pv.variant_id, pv.hard_key, NULL::text AS external_id, pv.source_id,
               COALESCE(s.source_type::text, '') AS source_type,
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
     LEFT JOIN sources s         ON s.id  = pv.source_id
     LEFT JOIN crawl_jobs cj     ON cj.id = pv.crawl_job_id
     LEFT JOIN raw_payloads rp   ON rp.id = pv.raw_payload_id
     LEFT JOIN opportunities o   ON o.canonical_id = pv.canonical_id
         WHERE pv.variant_id = ?
         ORDER BY pv.ingested_at DESC
         LIMIT 1
    `, variantID).Row().Scan(
		&row.VariantID, &row.HardKey, &row.ExternalID, &row.SourceID, &row.SourceType,
		&row.CrawlJobID, &row.ScheduledAt, &row.StartedAt, &row.FinishedAt, &row.CrawlStatus, &row.JobsFound, &row.JobsStored,
		&row.RawPayloadID, &row.RPSourceURL, &row.RPStorageURI, &row.RPContentHash, &row.RPSizeBytes, &row.RPFetchedAt, &row.RPHTTPStatus,
		&row.CurrentStage, &row.StageAt, &row.IngestedAt, &row.CanonicalID, &row.Slug, &row.LastError,
	)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
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
	if row.ExternalID != nil {
		out.ExternalID = *row.ExternalID
	}
	if row.CrawlJobID != nil {
		out.CrawlJob = &CrawlSummary{
			CrawlJobID:  *row.CrawlJobID,
			Status:      domain.CrawlJobStatus(strValue(row.CrawlStatus)),
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
	rows := []OpportunityVariant{}
	err := r.db(ctx, true).Raw(`
        SELECT pv.variant_id,
               pv.source_id      AS source_id,
               COALESCE(s.source_type::text,'') AS source_type,
               pv.ingested_at, pv.stage_at AS joined_at
          FROM pipeline_variants pv
          JOIN opportunities o ON o.canonical_id = pv.canonical_id
     LEFT JOIN sources s        ON s.id = pv.source_id
         WHERE o.slug = ?
         ORDER BY pv.stage_at ASC
    `, slug).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("OpportunityVariants: %w", err)
	}
	return rows, nil
}

func strValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test -tags=integration ./pkg/repository/ -run TestTraceRepository -v 2>&1 | tail -15
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/repository/trace.go pkg/repository/trace_test.go
git commit -m "feat(repository): TraceRepository — source/variant/opportunity joins"
```

---

### Task 3: HTTP handler — `GET /admin/trace/sources/{id}`

**Files:**
- Create: `apps/api/cmd/trace_admin.go`
- Modify: `apps/api/cmd/main.go` (wire route)

- [ ] **Step 1: Write the failing test**

Append to `apps/api/cmd/trace_admin_test.go`:

```go
//go:build integration

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pitabwire/frame/security"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func adminRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer test")
	claims := &security.AuthenticationClaims{}
	claims.Roles = []string{"admin"}
	return req.WithContext(claims.ClaimsToContext(req.Context()))
}

func TestTraceAdmin_SourceTrace_ReturnsSummary(t *testing.T) {
	pool := testhelpers.PostgresPool(t)
	// Seed via the existing scaffold helpers (or inline).
	testhelpers.SeedSourceWithCrawls(t, pool, "src-trace-A", 3)

	h := traceAdminHandler{trace: repository.NewTraceRepository(pool)}
	req := adminRequest("GET", "/admin/trace/sources/src-trace-A?since=24h")
	req.SetPathValue("id", "src-trace-A")
	rec := httptest.NewRecorder()
	h.SourceTrace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body)
	}
	var resp struct {
		Source  map[string]any `json:"source"`
		Summary struct {
			CrawlJobs int64 `json:"crawl_jobs"`
		} `json:"summary"`
		RecentCrawls []map[string]any `json:"recent_crawls"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Summary.CrawlJobs != 3 {
		t.Fatalf("summary.crawl_jobs = %d; want 3", resp.Summary.CrawlJobs)
	}
	if len(resp.RecentCrawls) != 3 {
		t.Fatalf("recent_crawls len = %d; want 3", len(resp.RecentCrawls))
	}
}

func TestTraceAdmin_SourceTrace_NotFound(t *testing.T) {
	pool := testhelpers.PostgresPool(t)
	h := traceAdminHandler{trace: repository.NewTraceRepository(pool)}
	req := adminRequest("GET", "/admin/trace/sources/nope?since=24h")
	req.SetPathValue("id", "nope")
	rec := httptest.NewRecorder()
	h.SourceTrace(rec, req)
	// Source row not in sources table -> 404 (the handler also returns
	// an empty summary when the source-table read misses; we choose
	// 404 to match the rest of admin convention).
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run, verify fail**

```bash
go test -tags=integration ./apps/api/cmd/ -run TestTraceAdmin_SourceTrace -v 2>&1 | tail -10
```

Expected: build failure (`traceAdminHandler` undefined).

- [ ] **Step 3: Implement `apps/api/cmd/trace_admin.go`**

```go
package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// traceAdminHandler bundles the dependencies for /admin/trace/* routes.
type traceAdminHandler struct {
	trace   *repository.TraceRepository
	sources sourceLookup // narrow read-only interface
}

// sourceLookup is the small read slice the trace handler needs. The
// production implementation is *repository.SourceRepository.
type sourceLookup interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
}

// registerTraceAdmin wires every /admin/trace/* route. Called from main.go.
func registerTraceAdmin(mux *http.ServeMux, trace *repository.TraceRepository, sources sourceLookup) {
	h := traceAdminHandler{trace: trace, sources: sources}
	mux.HandleFunc("GET /admin/trace/sources/{id}", requireAdmin(h.SourceTrace))
	mux.HandleFunc("GET /admin/trace/variants/{id}", requireAdmin(h.VariantTrace))
	mux.HandleFunc("GET /admin/trace/opportunities/{slug}", requireAdmin(h.OpportunityTrace))
}

// SourceTrace handles GET /admin/trace/sources/{id}?since=24h&limit=50.
func (h traceAdminHandler) SourceTrace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "source id required")
		return
	}
	window := parseWindow(r.URL.Query().Get("since"), 24*time.Hour)
	limit := parseLimit(r.URL.Query().Get("limit"), 50)

	src, err := h.sources.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "source not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}

	summary, err := h.trace.SourceSummary(ctx, id, window)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	recents, err := h.trace.RecentCrawls(ctx, id, window, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"source": map[string]any{
			"id":            src.ID,
			"type":          src.Type,
			"base_url":      src.BaseURL,
			"country":       src.Country,
			"status":        src.Status,
			"health_score":  src.HealthScore,
			"next_crawl_at": src.NextCrawlAt,
			"last_seen_at":  src.LastSeenAt,
		},
		"summary":       summary,
		"recent_crawls": recents,
	})
}

// parseWindow accepts "24h", "7d", "1h" etc. Returns default on parse failure.
func parseWindow(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	if d > 30*24*time.Hour {
		return 30 * 24 * time.Hour
	}
	return d
}

func parseLimit(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 200 {
		return 200
	}
	return n
}
```

Add the `context`, `domain` imports as needed. The `sourceLookup` interface lets the test inject a fake without dragging the full source repo.

- [ ] **Step 4: Wire in main.go**

In `apps/api/cmd/main.go`, find where `registerSourcesAdmin(...)` is called and add adjacent:

```go
traceRepo := repository.NewTraceRepository(dbFn)
registerTraceAdmin(mux, traceRepo, sourceRepo)
```

(`sourceRepo` is the existing `*repository.SourceRepository` already constructed in main.go for the sources admin.)

- [ ] **Step 5: Run, verify pass**

```bash
go build ./... 2>&1 | tail -5
go test -tags=integration ./apps/api/cmd/ -run TestTraceAdmin_SourceTrace -v 2>&1 | tail -10
```

Expected: build clean, tests PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/api/cmd/trace_admin.go apps/api/cmd/trace_admin_test.go apps/api/cmd/main.go
git commit -m "feat(admin): GET /admin/trace/sources/{id} — source summary + recent crawls"
```

---

### Task 4: HTTP handler — `GET /admin/trace/variants/{id}` + `GET /admin/trace/opportunities/{slug}`

**Files:**
- Modify: `apps/api/cmd/trace_admin.go` (add two handler methods)
- Modify: `apps/api/cmd/trace_admin_test.go`

- [ ] **Step 1: Write failing tests**

Append:

```go
func TestTraceAdmin_VariantTrace_ReturnsTimeline(t *testing.T) {
	pool := testhelpers.PostgresPool(t)
	seed := testhelpers.SeedSimpleVariant(t, pool, "src-trace-B", "var-trace-B")

	h := traceAdminHandler{trace: repository.NewTraceRepository(pool), sources: testhelpers.NewFakeSourceRepo(map[string]*domain.Source{
		"src-trace-B": {ID: "src-trace-B", Type: "greenhouse"},
	})}
	req := adminRequest("GET", "/admin/trace/variants/var-trace-B")
	req.SetPathValue("id", "var-trace-B")
	rec := httptest.NewRecorder()
	h.VariantTrace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	var resp struct {
		VariantID    string         `json:"variant_id"`
		RawPayload   map[string]any `json:"raw_payload"`
		CurrentStage string         `json:"current_stage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.VariantID != "var-trace-B" {
		t.Fatalf("variant_id = %q", resp.VariantID)
	}
	if resp.RawPayload == nil || resp.RawPayload["id"] != seed.RawPayloadID {
		t.Fatalf("raw_payload missing or wrong id: %v", resp.RawPayload)
	}
	if !strings.HasPrefix(resp.RawPayload["body_url"].(string), "/admin/raw_payloads/") {
		t.Fatalf("body_url not populated: %v", resp.RawPayload["body_url"])
	}
}

func TestTraceAdmin_OpportunityTrace(t *testing.T) {
	pool := testhelpers.PostgresPool(t)
	testhelpers.SeedTwoVariantsOneCanonical(t, pool, "opp-trace-X")
	// opp must exist in opportunities table for the slug lookup; seed helper does this.

	h := traceAdminHandler{trace: repository.NewTraceRepository(pool)}
	req := adminRequest("GET", "/admin/trace/opportunities/opp-trace-X-slug")
	req.SetPathValue("slug", "opp-trace-X-slug")
	rec := httptest.NewRecorder()
	h.OpportunityTrace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	var resp struct {
		VariantCount int                       `json:"variant_count"`
		Variants     []map[string]any          `json:"variants"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.VariantCount != 2 {
		t.Fatalf("variant_count = %d; want 2", resp.VariantCount)
	}
}
```

- [ ] **Step 2: Run, verify fail**

```bash
go test -tags=integration ./apps/api/cmd/ -run "TestTraceAdmin_(Variant|Opportunity)" -v 2>&1 | tail -10
```

Expected: build failure (`VariantTrace`, `OpportunityTrace` methods undefined).

- [ ] **Step 3: Implement**

Append to `apps/api/cmd/trace_admin.go`:

```go
// VariantTrace handles GET /admin/trace/variants/{id}.
func (h traceAdminHandler) VariantTrace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "variant id required")
		return
	}
	tl, err := h.trace.VariantTimeline(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if tl == nil {
		writeError(w, http.StatusNotFound, "not_found", "variant not in pipeline_variants (retention may have elapsed)")
		return
	}
	// Populate body_url on the raw_payload sub-object so the UI can link.
	if tl.RawPayload != nil && tl.RawPayload.ID != "" {
		tl.RawPayload.BodyURL = "/admin/raw_payloads/" + tl.RawPayload.ID + "/body"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tl)
}

// OpportunityTrace handles GET /admin/trace/opportunities/{slug}.
func (h traceAdminHandler) OpportunityTrace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "slug required")
		return
	}
	variants, err := h.trace.OpportunityVariants(ctx, slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"slug":          slug,
		"variant_count": len(variants),
		"variants":      variants,
	})
}
```

- [ ] **Step 4: Run, verify pass**

```bash
go test -tags=integration ./apps/api/cmd/ -run "TestTraceAdmin_(Variant|Opportunity)" -v 2>&1 | tail -10
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api/cmd/trace_admin.go apps/api/cmd/trace_admin_test.go
git commit -m "feat(admin): variant + opportunity trace endpoints"
```

---

### Task 5: Add `raw_payload_id` + `crawl_job_id` to `VariantRejectedV1`

**Files:**
- Modify: `pkg/events/v1/pipeline.go` (around line 71, the VariantRejectedV1 struct).
- Modify: `apps/crawler/service/crawl_request_handler.go` (publishRejected, around line 565).

- [ ] **Step 1: Add fields to the event payload**

In `pkg/events/v1/pipeline.go`, change `VariantRejectedV1`:

```go
type VariantRejectedV1 struct {
	VariantID    string    `json:"variant_id"     parquet:"variant_id"`
	SourceID     string    `json:"source_id"      parquet:"source_id"`
	Kind         string    `json:"kind"           parquet:"kind"`
	Title        string    `json:"title,omitempty" parquet:"title,optional"`
	Reasons      []string  `json:"reasons"        parquet:"reasons,list"`
	// NEW — present on events emitted after 2026-05-28; NULL/empty on
	// older rows in the Iceberg sink (backwards-compatible).
	RawPayloadID string    `json:"raw_payload_id,omitempty" parquet:"raw_payload_id,optional"`
	CrawlJobID   string    `json:"crawl_job_id,omitempty"   parquet:"crawl_job_id,optional"`
	RejectedAt   time.Time `json:"rejected_at"    parquet:"rejected_at"`
}
```

(Field order matters for parquet compatibility — keep RejectedAt last as it was.)

- [ ] **Step 2: Populate in publishRejected**

Currently `publishRejected` builds the `rej` struct from `opp.Title`/`source_id`/`kind`/`reasons`. The crawler-side has `crawlJob.ID` + `pageRawPayloadID` in scope. Modify the signature OR pull them from the closure:

`publishRejected` is currently a method on `*CrawlRequestHandler` called from inside the iterator loop — `crawlJob` and `pageRawPayloadID` ARE in scope at the call site (`apps/crawler/service/crawl_request_handler.go` around line 328). The cleanest fix: pass them in as parameters.

Change the signature:

```go
func (h *CrawlRequestHandler) publishRejected(
    ctx context.Context, sourceID, kind string,
    opp domain.ExternalOpportunity, res opportunity.VerifyResult,
    crawlJobID, rawPayloadID string,
) error {
    // ...
    rej := eventsv1.VariantRejectedV1{
        // existing fields ...
        RawPayloadID: rawPayloadID,
        CrawlJobID:   crawlJobID,
    }
    // ...
}
```

Update the call site (around line 328):

```go
if rerr := h.publishRejected(ctx, src.ID, kind, extJob, res, crawlJob.ID, pageRawPayloadID); rerr != nil {
    log.WithError(rerr).Warn("crawl.request: publishRejected failed")
}
```

- [ ] **Step 3: Build + tests**

```bash
go build ./... 2>&1 | tail -5
go test ./apps/crawler/service/... -count=1 2>&1 | tail -10
```

Expected: clean build, tests pass. Existing tests that constructed `publishRejected`-equivalent fakes need updating if they assert on the call signature.

- [ ] **Step 4: Commit**

```bash
git add pkg/events/v1/pipeline.go apps/crawler/service/crawl_request_handler.go
git commit -m "feat(events): VariantRejectedV1 carries raw_payload_id + crawl_job_id

Closes the audit gap: rejected variants now link back to the
crawler ledger so '/admin/trace/variants/{id}' can render the
rejection AND show a button to the raw HTML."
```

---

### Task 6: Update writer Arrow schema for the new VariantRejectedV1 fields

**Files:**
- Modify: `apps/writer/service/arrow_schemas.go` — the `variants_rejected` schema declaration.
- Modify: `apps/writer/service/arrow_build.go` (`BuildVariantRejectedRecord`).
- Modify: any `arrow_build_test.go` golden file.

- [ ] **Step 1: Locate the schema**

```bash
grep -n "variants_rejected\|VariantRejected" apps/writer/service/arrow_schemas.go apps/writer/service/arrow_build.go | head -10
```

- [ ] **Step 2: Add two optional STRING columns**

In `apps/writer/service/arrow_schemas.go`, find the `variantsRejectedSchema` (or equivalently-named) array of `arrow.Field`. Append two optional strings:

```go
_opt("raw_payload_id", arrow.BinaryTypes.String),
_opt("crawl_job_id",   arrow.BinaryTypes.String),
```

(The `_opt` helper already exists in the file; mirror how existing optional strings are declared.)

- [ ] **Step 3: Populate in BuildVariantRejectedRecord**

In `apps/writer/service/arrow_build.go`, the `BuildVariantRejectedRecord` function builds a record from decoded events. Add the two field appenders. Find the existing `Append` calls for `title` / `reasons` and add adjacent (matching the schema field order):

```go
appendOptStr(b.Field(idxRawPayloadID).(*array.StringBuilder), p.RawPayloadID)
appendOptStr(b.Field(idxCrawlJobID).(*array.StringBuilder),   p.CrawlJobID)
```

The exact index integers depend on the schema; renumber the existing indices if necessary, or use named indices.

- [ ] **Step 4: Update golden tests if any**

```bash
grep -rn "variants_rejected" apps/writer/service/ | grep _test.go | head
```

If a golden test exists for the rejected-variant Arrow record, update its expected output to include the two new columns (both NULL when not set on the input event).

- [ ] **Step 5: Run writer tests**

```bash
go test ./apps/writer/... -count=1 2>&1 | tail -15
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/writer/service/arrow_schemas.go apps/writer/service/arrow_build.go apps/writer/service/arrow_build_test.go
git commit -m "feat(writer): variants_rejected sink carries raw_payload_id + crawl_job_id

Iceberg schema evolution adds two optional STRING columns. Old
rows have NULL — backwards-compatible per Iceberg spec."
```

---

### Task 7: `/admin/raw_payloads/{id}/body` — fetch gzipped HTML from R2

**Files:**
- Create: `apps/api/cmd/raw_payload_admin.go`
- Modify: `apps/api/cmd/main.go` (wire route)
- Modify: `pkg/repository/crawl.go` — add `GetRawPayload(ctx, id) (*domain.RawPayload, error)` if not present.

- [ ] **Step 1: Add `GetRawPayload` to CrawlRepository**

```bash
grep -n "GetRawPayload\|raw_payloads.*Where" pkg/repository/crawl.go
```

If missing, append to `pkg/repository/crawl.go`:

```go
// GetRawPayload returns the row matching id, or (nil, nil) if absent.
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
```

- [ ] **Step 2: Implement the handler**

Create `apps/api/cmd/raw_payload_admin.go`:

```go
package cmd

import (
	"net/http"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

type rawPayloadAdminHandler struct {
	repo *repository.CrawlRepository
	arch archive.Archive
}

func registerRawPayloadAdmin(mux *http.ServeMux, repo *repository.CrawlRepository, arch archive.Archive) {
	h := rawPayloadAdminHandler{repo: repo, arch: arch}
	mux.HandleFunc("GET /admin/raw_payloads/{id}/body", requireAdmin(h.Body))
}

// Body returns the gzipped HTML stored in R2 for a raw_payloads row.
// Streamed straight to the client with Content-Encoding: gzip; the
// browser inflates and displays.
func (h rawPayloadAdminHandler) Body(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "id required")
		return
	}
	rp, err := h.repo.GetRawPayload(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if rp == nil {
		writeError(w, http.StatusNotFound, "not_found", "raw_payload not found")
		return
	}
	if rp.ContentHash == "" {
		writeError(w, http.StatusNotFound, "not_found", "raw_payload has no content_hash")
		return
	}
	body, err := h.arch.GetRaw(ctx, rp.ContentHash)
	if err != nil {
		writeError(w, http.StatusBadGateway, "archive_get_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Source-URL", rp.SourceURL)
	_, _ = w.Write(body)
}
```

- [ ] **Step 3: Wire in main.go**

In `apps/api/cmd/main.go`, after the existing trace registration:

```go
arch := archive.NewR2Archive(archive.R2Config{
    AccountID:       cfg.R2AccountID,
    AccessKeyID:     cfg.R2AccessKeyID,
    SecretAccessKey: cfg.R2SecretAccessKey,
    Endpoint:        cfg.R2Endpoint,
    Bucket:          cfg.R2ContentBucket, // same bucket the crawler writes to
})
crawlRepo := repository.NewCrawlRepository(dbFn)
registerRawPayloadAdmin(mux, crawlRepo, arch)
```

(The api's config may not yet have R2 fields — confirm via `grep R2 apps/api/config/config.go`; if absent, add the same fields the crawler config has. They share the same secret.)

- [ ] **Step 4: Build + smoke test**

```bash
go build ./apps/api/... 2>&1 | tail -5
```

Smoke test against production after deploy (not in this plan).

- [ ] **Step 5: Commit**

```bash
git add apps/api/cmd/raw_payload_admin.go apps/api/cmd/main.go pkg/repository/crawl.go apps/api/config/config.go
git commit -m "feat(admin): GET /admin/raw_payloads/{id}/body returns gzipped HTML from R2"
```

---

### Task 8: Admin sub-app scaffold — Vite + React at `ui/admin/`

**Files:**
- Create: `ui/admin/package.json`
- Create: `ui/admin/vite.config.ts`
- Create: `ui/admin/tsconfig.json`
- Create: `ui/admin/index.html`
- Create: `ui/admin/src/main.tsx`
- Create: `ui/admin/src/components/AppGate.tsx`
- Create: `ui/admin/src/api/admin-client.ts`
- Create: `ui/admin/src/pages/SourceTrace.tsx`
- Create: `ui/admin/src/styles/admin.css`

This task scaffolds the sub-app; one page renders the source trace JSON as a starting point. Task 9 polishes the UI.

- [ ] **Step 1: Mirror the existing `ui/app/` build config**

Look at how the existing candidate-facing app's Vite is configured:

```bash
grep -l "base:\|build:\|outDir:" ui/app/vite.config.* | head -3
cat ui/app/vite.config.ts 2>&1 | head -40
```

Mirror the same shape in `ui/admin/vite.config.ts`:

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ command }) => ({
  base: command === "build" ? "/admin/" : "/",
  plugins: [react()],
  build: {
    outDir: "../../static/admin-app",
    emptyOutDir: true,
    rollupOptions: {
      input: "src/main.tsx",
      output: {
        entryFileNames: "assets/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]",
        chunkFileNames: "assets/[name]-[hash].js",
      },
    },
  },
}));
```

- [ ] **Step 2: `ui/admin/package.json`**

```json
{
  "name": "@stawi-opportunities/admin",
  "private": true,
  "version": "0.0.1",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "typecheck": "tsc --noEmit"
  },
  "dependencies": {
    "@stawi/auth-runtime": "workspace:*",
    "react": "^18.3.0",
    "react-dom": "^18.3.0",
    "react-router-dom": "^7.1.0"
  },
  "devDependencies": {
    "@types/react": "^18.3.0",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.0",
    "typescript": "^5.5.0",
    "vite": "^5.4.0"
  }
}
```

(Match versions to what `ui/app/package.json` actually has — `grep "react\":\|vite\":" ui/app/package.json`.)

- [ ] **Step 3: `ui/admin/tsconfig.json`**

Copy from `ui/app/tsconfig.json` adjusting paths. (`cp ui/app/tsconfig.json ui/admin/tsconfig.json` and tweak.)

- [ ] **Step 4: `ui/admin/index.html`** — the Hugo-included shell

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Stawi Admin</title>
    <link rel="stylesheet" href="/admin-app/assets/admin.css" />
  </head>
  <body>
    <div id="admin-root"></div>
    <script type="module" src="/admin-app/assets/main.js"></script>
  </body>
</html>
```

- [ ] **Step 5: `ui/admin/src/api/admin-client.ts`** — fetch wrapper

```ts
import { createAuthRuntime, type AuthRuntime } from "@stawi/auth-runtime";

let runtime: AuthRuntime | null = null;

function authRuntime(): AuthRuntime {
  if (runtime) return runtime;
  runtime = createAuthRuntime({
    // mirror the candidate dashboard's config (read from /env.js or similar);
    // for the smoke test we read window.__STAWI_AUTH_CONFIG__ injected by Hugo.
    ...(window as any).__STAWI_AUTH_CONFIG__,
  });
  return runtime;
}

export async function getRoles(): Promise<string[]> {
  return authRuntime().getRoles();
}

export async function fetchAdminJSON<T = unknown>(path: string): Promise<T> {
  return authRuntime().fetch<T>(path);
}

export type SourceTraceResponse = {
  source: {
    id: string;
    type: string;
    base_url: string;
    country: string;
    status: string;
    health_score: number;
    next_crawl_at: string | null;
    last_seen_at: string | null;
  };
  summary: {
    crawl_jobs: number;
    crawl_jobs_failed: number;
    raw_payloads: number;
    variants_emitted: number;
    variants_published: number;
    variants_rejected: number;
    rejection_reasons: Record<string, number>;
  };
  recent_crawls: Array<{
    crawl_job_id: string;
    scheduled_at: string;
    started_at: string | null;
    finished_at: string | null;
    duration_ms: number;
    status: string;
    jobs_found: number;
    jobs_stored: number;
    raw_payloads: number;
    error_code?: string;
  }>;
};
```

- [ ] **Step 6: `ui/admin/src/components/AppGate.tsx`** — role-based mount

```tsx
import { useEffect, useState, type ReactNode } from "react";
import { getRoles } from "../api/admin-client";

export function AppGate({ children }: { children: ReactNode }) {
  const [state, setState] = useState<"checking" | "ok" | "denied">("checking");
  useEffect(() => {
    getRoles().then((roles) => {
      setState(roles.includes("admin") ? "ok" : "denied");
    }).catch(() => setState("denied"));
  }, []);
  if (state === "checking") return <p>Loading…</p>;
  if (state === "denied") return <p>Not found.</p>;  // 404-shaped, not 403, per spec
  return <>{children}</>;
}
```

- [ ] **Step 7: `ui/admin/src/pages/SourceTrace.tsx`** — minimal first page

```tsx
import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { fetchAdminJSON, type SourceTraceResponse } from "../api/admin-client";

export function SourceTrace() {
  const { id } = useParams<{ id: string }>();
  const [data, setData] = useState<SourceTraceResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    fetchAdminJSON<SourceTraceResponse>(`/admin/trace/sources/${id}?since=24h`)
      .then(setData)
      .catch((e) => setErr(String(e)));
  }, [id]);

  if (err) return <pre style={{ color: "red" }}>{err}</pre>;
  if (!data) return <p>Loading…</p>;

  return (
    <div>
      <h1>{data.source.id} <small>({data.source.type})</small></h1>
      <p>Status: {data.source.status} • Health: {data.source.health_score.toFixed(2)}</p>

      <h2>24h summary</h2>
      <ul>
        <li>Crawl jobs: {data.summary.crawl_jobs} ({data.summary.crawl_jobs_failed} failed)</li>
        <li>Variants emitted: {data.summary.variants_emitted}</li>
        <li>Variants published: {data.summary.variants_published}</li>
        <li>Variants rejected: {data.summary.variants_rejected}</li>
      </ul>

      <h2>Recent crawls</h2>
      <table>
        <thead><tr><th>Time</th><th>Status</th><th>Found</th><th>Stored</th><th>Duration (ms)</th><th>Error</th></tr></thead>
        <tbody>
          {data.recent_crawls.map((c) => (
            <tr key={c.crawl_job_id}>
              <td>{new Date(c.scheduled_at).toLocaleString()}</td>
              <td>{c.status}</td>
              <td>{c.jobs_found}</td>
              <td>{c.jobs_stored}</td>
              <td>{c.duration_ms}</td>
              <td>{c.error_code ?? ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
```

- [ ] **Step 8: `ui/admin/src/main.tsx`** — root with router

```tsx
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { AppGate } from "./components/AppGate";
import { SourceTrace } from "./pages/SourceTrace";
import "./styles/admin.css";

const root = createRoot(document.getElementById("admin-root")!);
root.render(
  <StrictMode>
    <BrowserRouter basename="/admin">
      <AppGate>
        <Routes>
          <Route path="/" element={<p>Pick a source.</p>} />
          <Route path="/sources/:id" element={<SourceTrace />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AppGate>
    </BrowserRouter>
  </StrictMode>
);
```

- [ ] **Step 9: `ui/admin/src/styles/admin.css`** — minimal styles

```css
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 0; padding: 1rem 2rem; }
table { border-collapse: collapse; width: 100%; margin-top: 0.5rem; }
th, td { border: 1px solid #ddd; padding: 0.4rem 0.6rem; text-align: left; }
th { background: #f3f3f3; }
```

- [ ] **Step 10: Build + verify**

```bash
cd /home/j/code/stawi.opportunities/ui/admin
pnpm install 2>&1 | tail -5
pnpm typecheck 2>&1 | tail -5
pnpm build 2>&1 | tail -5
```

Expected: clean build, files emitted to `static/admin-app/`.

- [ ] **Step 11: Hugo wiring**

The Hugo config in `ui/hugo.toml` already serves `/static/*` at the root. The admin app at `static/admin-app/` will be served at `/admin-app/`. Create a Hugo content page at `ui/content/admin/_index.md` (or wherever the Hugo content lives) that uses the admin shell:

```yaml
---
title: Admin
layout: admin
---
```

And the matching layout at `ui/layouts/admin/single.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <title>Admin — Stawi</title>
    <link rel="stylesheet" href="{{ "/admin-app/assets/admin.css" | relURL }}" />
    <script>
      window.__STAWI_AUTH_CONFIG__ = {
        idpBaseUrl: "{{ .Site.Params.idpBaseUrl }}",
        clientId:   "{{ .Site.Params.clientId }}",
        // ...mirror what ui/app's layout sets
      };
    </script>
  </head>
  <body>
    <div id="admin-root"></div>
    <script type="module" src="{{ "/admin-app/assets/main.js" | relURL }}"></script>
  </body>
</html>
```

Match the auth-config shape the candidate dashboard uses (`grep -A 5 __STAWI_AUTH_CONFIG__ ui/layouts/`).

- [ ] **Step 12: Commit**

```bash
git add ui/admin/ ui/layouts/admin/ ui/content/admin/
git commit -m "feat(admin-ui): Vite + React /admin sub-app scaffold with SourceTrace page"
```

---

### Task 9: Tag + deploy v8.0.61

- [ ] **Step 1: Push + tag**

```bash
cd /home/j/code/stawi.opportunities
git push origin main
git tag v8.0.61
git push origin v8.0.61
```

- [ ] **Step 2: Wait for build**

```bash
gh run watch $(gh run list --limit 1 --workflow release.yaml --repo stawi-opportunities/opportunities --json databaseId -q '.[0].databaseId') --repo stawi-opportunities/opportunities --exit-status
```

- [ ] **Step 3: Verify in cluster**

After Flux rolls the api + crawler pods:

```bash
# 1. Confirm the new endpoints are reachable (requires an admin JWT).
kubectl port-forward -n product-opportunities svc/opportunities-api 18080:80 &
sleep 2

# Without admin role → 403
curl -s -o /dev/null -w "%{http_code}\n" \
  -H "Authorization: Bearer $NON_ADMIN_TOKEN" \
  http://localhost:18080/admin/trace/sources/$(kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -tAc "SELECT id FROM sources LIMIT 1")
# Expected: 403

# With admin role
curl -s \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:18080/admin/trace/sources/$(SOURCE_ID)?since=1h | jq .summary

# 2. Verify a rejection-event-emitted-since-v8.0.61 carries the new IDs.
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "
  SELECT variant_id, current_stage FROM pipeline_variants
   WHERE current_stage='ingested' ORDER BY ingested_at DESC LIMIT 1"
# Take the variant_id, look it up:
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" http://localhost:18080/admin/trace/variants/$VID | jq .raw_payload

# 3. UI smoke: open /admin/sources/{id} in a browser.
```

---

## Plan A Exit Criteria

- [ ] Without admin role: `GET /admin/trace/*` returns 403.
- [ ] With admin role + valid source: `GET /admin/trace/sources/{id}?since=24h` returns JSON with non-empty summary + recent_crawls.
- [ ] `GET /admin/trace/variants/{id}` returns the variant timeline + raw_payload link with `body_url`.
- [ ] `GET /admin/trace/opportunities/{slug}` returns the variant list joined to the canonical.
- [ ] `GET /admin/raw_payloads/{id}/body` returns the gzipped HTML the crawler fetched.
- [ ] `VariantRejectedV1` events emitted post-deploy carry `raw_payload_id` + `crawl_job_id`.
- [ ] `/admin/sources/{id}` loads in a browser, role-checks via auth-runtime, and renders the JSON via the React page.
- [ ] No existing tests regress.
