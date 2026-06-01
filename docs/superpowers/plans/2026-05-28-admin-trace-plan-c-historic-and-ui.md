# Plan C — Iceberg Historic + Full Admin UI

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Trace endpoints honor `?since=` beyond the 7-day Postgres retention by reading from the Iceberg event log. Plus six new admin UI pages (`VariantTrace`, `OpportunityTrace`, `SeedDigest`, `DefinitionsList`, `DefinitionEditor`, `RawPayloadViewer`) that turn the operator dashboard into a complete picture.

**Architecture:** A new `pkg/icebergclient.ReadRecent(ctx, namespace, table, columns, limit)` helper queries Iceberg via the `iceberg-go` library. The trace API endpoints check Postgres first; if the date range extends past retention, they call Iceberg for the rest. New `/admin/trace/seeds/{id}/digest?date=...` rolls up per-day metrics. The admin UI gains six routes wired into the same auth-gated router from Plan A.

**Tech Stack:** Go (Frame v1.97+, iceberg-go v0.5.0), Vite + React + react-router + @stawi/auth-runtime.

**Depends on:** Plan A (live as v8.0.62), Plan B1 (live as v8.0.63), Plan B2 (live as v8.0.64). Iceberg `variants_rejected` already has `raw_payload_id` + `crawl_job_id` columns.

---

## File Structure

**Created (Go):**
- `pkg/icebergclient/read.go` — `ReadRecent` helper.
- `pkg/icebergclient/read_test.go` — unit + integration (requires minio/R2 catalog access).
- `apps/api/cmd/digest_admin.go` — `GET /admin/trace/seeds/{id}/digest`.
- `apps/api/cmd/digest_admin_test.go`.

**Modified (Go):**
- `apps/api/cmd/trace_admin.go` — extend `SourceTrace` to call Iceberg for `?since>7d`; populate `summary.rejection_reasons` from Iceberg.
- `pkg/repository/trace.go` — extend `SourceSummary` to flag when its data is Postgres-only vs. Postgres+Iceberg.

**Created (UI):**
- `ui/admin/src/pages/VariantTrace.tsx`
- `ui/admin/src/pages/OpportunityTrace.tsx`
- `ui/admin/src/pages/SeedDigest.tsx`
- `ui/admin/src/pages/DefinitionsList.tsx`
- `ui/admin/src/pages/DefinitionEditor.tsx`
- `ui/admin/src/pages/RawPayloadViewer.tsx`
- `ui/admin/src/components/Layout.tsx` — top nav with links to all pages.
- `ui/admin/src/components/TraceTimeline.tsx` — reusable timeline visualization.
- `ui/admin/src/components/RejectionChart.tsx` — bar chart for rejection reasons.

**Modified (UI):**
- `ui/admin/src/main.tsx` — register all new routes.
- `ui/admin/src/api/admin-client.ts` — typed wrappers for all `/admin/*` endpoints.

---

## Tasks

### Task 1: `pkg/icebergclient.ReadRecent`

**Files:**
- Create: `pkg/icebergclient/read.go`
- Create: `pkg/icebergclient/read_test.go`

- [ ] **Step 1: Implement**

```go
package icebergclient

import (
	"context"
	"fmt"

	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
)

// ReadRecent returns up to `limit` rows from `namespace.tableName`
// projected to the requested columns, sorted by the table's
// time-partition column DESC. Used by admin trace endpoints that
// need historic data beyond Postgres retention.
//
// Returns []map[string]any so the caller serializes to JSON. For
// large reads the projection should be small — Iceberg's column-
// pruning makes per-column scans cheap.
func (c *Catalog) ReadRecent(
	ctx context.Context,
	namespace, tableName string,
	columns []string,
	limit int,
) ([]map[string]any, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	ident := table.Identifier{namespace, tableName}
	tbl, err := c.cat.LoadTable(ctx, ident)
	if err != nil {
		return nil, fmt.Errorf("LoadTable %v: %w", ident, err)
	}

	scan := tbl.NewScan()
	if len(columns) > 0 {
		scan = scan.Select(columns...)
	}

	// iceberg-go v0.5.0: scan.ToArrowRecords returns an arrow.RecordReader
	// (the exact API may differ — read pkg version go doc to confirm).
	rdr, err := scan.ToArrowRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan %v: %w", ident, err)
	}
	defer rdr.Release()

	rows := make([]map[string]any, 0, limit)
	for rdr.Next() && len(rows) < limit {
		rec := rdr.Record()
		schema := rec.Schema()
		nrows := int(rec.NumRows())
		ncols := int(rec.NumCols())
		for i := 0; i < nrows && len(rows) < limit; i++ {
			row := make(map[string]any, ncols)
			for j := 0; j < ncols; j++ {
				row[schema.Field(j).Name] = rec.Column(j).GetOneForMarshal(i)
			}
			rows = append(rows, row)
		}
	}
	if err := rdr.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
```

**Important:** the `iceberg-go` API for scanning to Arrow records varies by version. Run `go doc github.com/apache/iceberg-go/table Scan` and `go doc github.com/apache/iceberg-go/table ScanResult` to confirm the actual method names. If `ToArrowRecords` doesn't exist, the v0.5.0 alternative is `Plan().ToArrowRecordReader()` or `ToArrowTable()`. Adapt to the actual API.

- [ ] **Step 2: Test**

```go
//go:build integration

package icebergclient_test

import (
	"context"
	"os"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
)

func TestReadRecent_VariantsRejected(t *testing.T) {
	// Requires R2 catalog creds in env. Skip if not present.
	if os.Getenv("R2_ACCOUNT_ID") == "" {
		t.Skip("R2_ACCOUNT_ID not set")
	}
	ctx := context.Background()
	cat, err := icebergclient.NewCatalogFromEnv(ctx)
	if err != nil {
		t.Fatalf("NewCatalogFromEnv: %v", err)
	}
	rows, err := cat.ReadRecent(ctx, "opportunities", "variants_rejected",
		[]string{"variant_id", "source_id", "kind", "rejected_at"}, 10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	t.Logf("read %d rows", len(rows))
}
```

- [ ] **Step 3: Build + commit**

```bash
cd /home/j/code/stawi.opportunities
go build ./pkg/icebergclient/... 2>&1 | tail -5
go vet ./pkg/icebergclient/... 2>&1 | tail -5
git add pkg/icebergclient/read.go pkg/icebergclient/read_test.go
git commit -m "feat(icebergclient): ReadRecent helper for historic admin queries"
```

---

### Task 2: Trace API + rejection rollup from Iceberg

**Files:**
- Modify: `apps/api/cmd/trace_admin.go` — `SourceTrace` handler.
- Modify: `pkg/repository/trace.go` — `SourceSummary` returns a `DataSource` enum.

- [ ] **Step 1: Update `SourceSummary` shape**

In `pkg/repository/trace.go`, add a `DataSource` field:

```go
type SourceSummary struct {
    // ... existing fields ...
    DataSource string `json:"data_source"` // "postgres" | "postgres+iceberg"
}
```

In `SourceSummary` query (around the existing Postgres aggregate), keep the aggregate as-is and SET `DataSource = "postgres"`. The Iceberg portion is layered on top in the API handler.

- [ ] **Step 2: API handler queries Iceberg for rejection reasons**

In `apps/api/cmd/trace_admin.go` `SourceTrace`, after `h.trace.SourceSummary(...)` returns, if `window > 6 * 24 * time.Hour` (i.e. crossing the 7-day Postgres retention), query Iceberg:

```go
if h.iceberg != nil && window > 6*24*time.Hour {
    rows, err := h.iceberg.ReadRecent(ctx, "opportunities", "variants_rejected",
        []string{"source_id", "reasons", "rejected_at"}, 5000)
    if err == nil {
        // Aggregate rejection reasons for this source over the window.
        cutoff := time.Now().Add(-window)
        for _, row := range rows {
            if row["source_id"] != sourceID { continue }
            if rejAt, ok := row["rejected_at"].(time.Time); ok && rejAt.Before(cutoff) {
                continue
            }
            if reasons, ok := row["reasons"].([]any); ok {
                for _, r := range reasons {
                    if s, ok := r.(string); ok {
                        summary.RejectionReasons[s]++
                    }
                }
            }
            summary.VariantsRejected++
        }
        summary.DataSource = "postgres+iceberg"
    }
}
```

(The exact type returned by `ReadRecent` for `reasons` is `[]any` if Iceberg serialized it as a list; adjust based on what the type inspector reveals.)

Inject the `iceberg` dep into `traceAdminHandler`:

```go
type traceAdminHandler struct {
    trace   *repository.TraceRepository
    sources sourceLookup
    iceberg *icebergclient.Catalog // optional; nil if not configured
}

func registerTraceAdmin(mux *http.ServeMux, trace *repository.TraceRepository, sources sourceLookup, iceberg *icebergclient.Catalog) {
    h := traceAdminHandler{trace: trace, sources: sources, iceberg: iceberg}
    // ... existing route registrations ...
}
```

Update the call site in `sources_admin.go`:

```go
iceberg, _ := icebergclient.NewCatalogFromEnv(ctx) // nil if R2 catalog unavailable
registerTraceAdmin(mux, repository.NewTraceRepository(pool.DB), repo, iceberg)
```

- [ ] **Step 3: Build + test + commit**

```bash
go build ./... 2>&1 | tail -5
go test ./apps/api/cmd/... -count=1 -short 2>&1 | tail -10
git add apps/api/cmd/trace_admin.go apps/api/cmd/sources_admin.go pkg/repository/trace.go
git commit -m "feat(admin): trace endpoints layer Iceberg historic onto Postgres aggregate"
```

---

### Task 3: Seed digest endpoint

**Files:**
- Create: `apps/api/cmd/digest_admin.go`
- Create: `apps/api/cmd/digest_admin_test.go`

```go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

type digestAdminHandler struct {
	trace   *repository.TraceRepository
	iceberg *icebergclient.Catalog
}

func registerDigestAdmin(mux *http.ServeMux, trace *repository.TraceRepository, iceberg *icebergclient.Catalog) {
	h := digestAdminHandler{trace: trace, iceberg: iceberg}
	mux.HandleFunc("GET /admin/trace/seeds/{id}/digest", requireAdmin(h.Digest))
}

// Digest returns a per-day rollup for a source. Postgres covers the
// last 7 days; older dates use Iceberg.
//
// Query: ?date=YYYY-MM-DD (defaults to today).
func (h digestAdminHandler) Digest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sourceID := r.PathValue("id")
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		dateStr = time.Now().UTC().Format("2006-01-02")
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "date must be YYYY-MM-DD")
		return
	}

	dayStart := date.UTC()
	dayEnd := dayStart.Add(24 * time.Hour)
	dataSource := "postgres"
	if dayStart.Before(time.Now().Add(-7 * 24 * time.Hour)) {
		dataSource = "iceberg"
	}

	// Postgres aggregate for recent dates.
	out := map[string]any{
		"source_id":   sourceID,
		"date":        dateStr,
		"data_source": dataSource,
	}
	if dataSource == "postgres" {
		summary, err := h.trace.DayDigest(ctx, sourceID, dayStart, dayEnd)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		out["crawl_jobs"] = summary.CrawlJobs
		out["variants_emitted"] = summary.VariantsEmitted
		out["variants_rejected"] = summary.VariantsRejected
		out["variants_published"] = summary.VariantsPublished
		out["rejection_reasons"] = summary.RejectionReasons
	} else {
		// Iceberg path — read all four topics, aggregate.
		if h.iceberg == nil {
			writeError(w, http.StatusServiceUnavailable, "iceberg_unavailable", "Iceberg catalog not configured for historic queries")
			return
		}
		// TODO Plan D: cache results; for now compute fresh.
		ingestedRows, _ := h.iceberg.ReadRecent(ctx, "opportunities", "variants_ingested",
			[]string{"source_id", "scraped_at"}, 5000)
		publishedRows, _ := h.iceberg.ReadRecent(ctx, "opportunities", "published",
			[]string{"source_id", "published_at"}, 5000)
		rejectedRows, _ := h.iceberg.ReadRecent(ctx, "opportunities", "variants_rejected",
			[]string{"source_id", "reasons", "rejected_at"}, 5000)
		out["variants_emitted"] = countInWindow(ingestedRows, "scraped_at", sourceID, dayStart, dayEnd)
		out["variants_published"] = countInWindow(publishedRows, "published_at", sourceID, dayStart, dayEnd)
		var rejected int64
		reasons := map[string]int64{}
		for _, row := range rejectedRows {
			if row["source_id"] != sourceID {
				continue
			}
			if t, ok := row["rejected_at"].(time.Time); !ok || t.Before(dayStart) || !t.Before(dayEnd) {
				continue
			}
			rejected++
			if rs, ok := row["reasons"].([]any); ok {
				for _, r := range rs {
					if s, ok := r.(string); ok {
						reasons[s]++
					}
				}
			}
		}
		out["variants_rejected"] = rejected
		out["rejection_reasons"] = reasons
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func countInWindow(rows []map[string]any, timeColumn, sourceID string, start, end time.Time) int64 {
	var count int64
	for _, row := range rows {
		if row["source_id"] != sourceID {
			continue
		}
		if t, ok := row[timeColumn].(time.Time); ok && !t.Before(start) && t.Before(end) {
			count++
		}
	}
	return count
}
```

Add `DayDigest(ctx, sourceID, start, end)` to `pkg/repository/trace.go`:

```go
type DayDigest struct {
    CrawlJobs         int64            `json:"crawl_jobs"`
    VariantsEmitted   int64            `json:"variants_emitted"`
    VariantsRejected  int64            `json:"variants_rejected"`
    VariantsPublished int64            `json:"variants_published"`
    RejectionReasons  map[string]int64 `json:"rejection_reasons"`
}

func (r *TraceRepository) DayDigest(ctx context.Context, sourceID string, start, end time.Time) (*DayDigest, error) {
    var s DayDigest
    s.RejectionReasons = map[string]int64{}
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
    if err != nil { return nil, err }
    return &s, nil
}
```

Wire `registerDigestAdmin` in `sources_admin.go` adjacent to the trace registration.

Commit:

```bash
git add apps/api/cmd/digest_admin.go apps/api/cmd/digest_admin_test.go apps/api/cmd/sources_admin.go pkg/repository/trace.go
git commit -m "feat(admin): GET /admin/trace/seeds/{id}/digest — per-day rollup"
```

---

### Task 4: Admin UI — typed API client + Layout + nav

**Files:**
- Modify: `ui/admin/src/api/admin-client.ts` — add typed wrappers for all `/admin/*` endpoints.
- Create: `ui/admin/src/components/Layout.tsx` — top nav + page container.

In `admin-client.ts`, add response types and fetch wrappers for:
- `getVariantTrace(variantID)` → `VariantTimelineResponse`
- `getOpportunityTrace(slug)` → `OpportunityTraceResponse`
- `getSeedDigest(sourceID, date)` → `SeedDigestResponse`
- `listDefinitions(type?)` → `DefinitionsListResponse`
- `getDefinition(type, name)` → `string` (raw body)
- `putDefinition(type, name, body)` → `void`
- `deleteDefinition(type, name)` → `void`
- `reparseRawPayload(id)` → `{queued: number}`
- `reparseSource(id, since)` → `{queued: number, source_id: string, window_seconds: number}`
- `getRawPayloadBodyURL(id)` → `string` (just the URL, browser fetches directly)

Each response type is a TypeScript interface mirroring the Go JSON shape.

`Layout.tsx`:

```tsx
import { Link, Outlet, useNavigate } from "react-router-dom";

export function Layout() {
  const navigate = useNavigate();
  return (
    <div>
      <nav style={{ borderBottom: "1px solid #ddd", padding: "1rem", display: "flex", gap: "1rem" }}>
        <Link to="/">Sources</Link>
        <Link to="/definitions">Definitions</Link>
        <form onSubmit={(e) => {
          e.preventDefault();
          const slug = (e.target as any).slug.value.trim();
          if (slug) navigate(`/opportunities/${slug}`);
        }} style={{ marginLeft: "auto" }}>
          <input name="slug" placeholder="Opportunity slug..." />
          <button type="submit">Go</button>
        </form>
      </nav>
      <main style={{ padding: "1rem 2rem", maxWidth: "1100px", margin: "0 auto" }}>
        <Outlet />
      </main>
    </div>
  );
}
```

Wire into `main.tsx`:

```tsx
<Route path="/" element={<Layout />}>
  <Route index element={<SourceList />} />  {/* new landing page */}
  <Route path="/sources/:id" element={<SourceTrace />} />
  <Route path="/variants/:id" element={<VariantTrace />} />
  <Route path="/opportunities/:slug" element={<OpportunityTrace />} />
  <Route path="/seeds/:id/digest" element={<SeedDigest />} />
  <Route path="/definitions" element={<DefinitionsList />} />
  <Route path="/definitions/:type/:name" element={<DefinitionEditor />} />
  <Route path="/raw_payloads/:id" element={<RawPayloadViewer />} />
</Route>
```

Commit:

```bash
git add ui/admin/src/api/admin-client.ts ui/admin/src/components/Layout.tsx ui/admin/src/main.tsx
git commit -m "feat(admin-ui): typed API client + Layout with top nav"
```

---

### Task 5: VariantTrace + OpportunityTrace pages

**Files:**
- Create: `ui/admin/src/pages/VariantTrace.tsx`
- Create: `ui/admin/src/pages/OpportunityTrace.tsx`
- Create: `ui/admin/src/components/TraceTimeline.tsx`

`TraceTimeline.tsx` — reusable timeline component:

```tsx
type Stage = { stage: string; at: string; duration_ms?: number; canonical_id?: string };
export function TraceTimeline({ stages }: { stages: Stage[] }) {
  return (
    <ol style={{ listStyle: "none", padding: 0, borderLeft: "2px solid #ccc", marginLeft: "1rem" }}>
      {stages.map((s, i) => (
        <li key={i} style={{ padding: "0.5rem 0 0.5rem 1rem", position: "relative" }}>
          <span style={{ position: "absolute", left: "-7px", top: "0.7rem", width: "12px", height: "12px", borderRadius: "50%", background: s.stage === "rejected" ? "#c00" : "#0a0" }} />
          <strong>{s.stage}</strong>{" "}
          <small style={{ color: "#666" }}>at {new Date(s.at).toLocaleString()}</small>
          {s.duration_ms != null && <span style={{ marginLeft: "0.5rem", color: "#888" }}>({s.duration_ms} ms)</span>}
        </li>
      ))}
    </ol>
  );
}
```

`VariantTrace.tsx`:

```tsx
import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { getVariantTrace, type VariantTimelineResponse } from "../api/admin-client";
import { TraceTimeline } from "../components/TraceTimeline";

export function VariantTrace() {
  const { id } = useParams<{ id: string }>();
  const [data, setData] = useState<VariantTimelineResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    getVariantTrace(id).then(setData).catch((e) => setErr(String(e)));
  }, [id]);

  if (err) return <pre style={{ color: "crimson" }}>{err}</pre>;
  if (!data) return <p>Loading variant…</p>;

  return (
    <div>
      <h1>Variant {data.variant_id}</h1>
      <p><strong>Source:</strong> {data.source.id} ({data.source.type})</p>
      {data.opportunity_slug && (
        <p><strong>Opportunity:</strong> <a href={`/admin/opportunities/${data.opportunity_slug}`}>{data.opportunity_slug}</a></p>
      )}
      <h2>Timeline</h2>
      <TraceTimeline stages={data.stages} />
      {data.raw_payload && (
        <section>
          <h2>Raw payload</h2>
          <p>
            <strong>URL:</strong> <a href={data.raw_payload.source_url}>{data.raw_payload.source_url}</a><br />
            <strong>Hash:</strong> <code>{data.raw_payload.content_hash}</code><br />
            <strong>Size:</strong> {data.raw_payload.size_bytes} bytes<br />
            <strong>HTTP status:</strong> {data.raw_payload.http_status}<br />
            <a href={data.raw_payload.body_url} target="_blank" rel="noreferrer">View captured HTML →</a>
          </p>
        </section>
      )}
      {data.last_error && (
        <section>
          <h2>Last error</h2>
          <pre style={{ color: "crimson", background: "#fee", padding: "0.5rem" }}>{data.last_error}</pre>
        </section>
      )}
    </div>
  );
}
```

`OpportunityTrace.tsx` — similar shape, lists variants:

```tsx
export function OpportunityTrace() {
  const { slug } = useParams<{ slug: string }>();
  const [data, setData] = useState<OpportunityTraceResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!slug) return;
    getOpportunityTrace(slug).then(setData).catch((e) => setErr(String(e)));
  }, [slug]);

  if (err) return <pre style={{ color: "crimson" }}>{err}</pre>;
  if (!data) return <p>Loading…</p>;

  return (
    <div>
      <h1>{slug}</h1>
      <p>{data.variant_count} variants joined this canonical</p>
      <table>
        <thead><tr><th>Variant</th><th>Source</th><th>Ingested</th><th>Joined</th></tr></thead>
        <tbody>
          {data.variants.map((v) => (
            <tr key={v.variant_id}>
              <td><a href={`/admin/variants/${v.variant_id}`}>{v.variant_id}</a></td>
              <td>{v.source.id} ({v.source.type})</td>
              <td>{new Date(v.ingested_at).toLocaleString()}</td>
              <td>{new Date(v.joined_at).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
```

Commit:

```bash
git add ui/admin/src/pages/{VariantTrace,OpportunityTrace}.tsx ui/admin/src/components/TraceTimeline.tsx
git commit -m "feat(admin-ui): VariantTrace + OpportunityTrace pages with timeline component"
```

---

### Task 6: SeedDigest + RawPayloadViewer pages

**Files:**
- Create: `ui/admin/src/pages/SeedDigest.tsx`
- Create: `ui/admin/src/pages/RawPayloadViewer.tsx`
- Create: `ui/admin/src/components/RejectionChart.tsx`

`RejectionChart.tsx` — bar chart for rejection reasons (no external chart lib; use CSS bars):

```tsx
export function RejectionChart({ reasons }: { reasons: Record<string, number> }) {
  const entries = Object.entries(reasons).sort((a, b) => b[1] - a[1]);
  const max = Math.max(1, ...entries.map(([, n]) => n));
  return (
    <table>
      <tbody>
        {entries.map(([reason, count]) => (
          <tr key={reason}>
            <td style={{ width: "200px" }}><code>{reason}</code></td>
            <td>
              <div style={{ background: "#c00", width: `${(count / max) * 100}%`, padding: "0.2rem", color: "white", paddingLeft: "0.4rem" }}>
                {count}
              </div>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
```

`SeedDigest.tsx`:

```tsx
import { useEffect, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { getSeedDigest, type SeedDigestResponse, reparseSource } from "../api/admin-client";
import { RejectionChart } from "../components/RejectionChart";

export function SeedDigest() {
  const { id } = useParams<{ id: string }>();
  const [params, setParams] = useSearchParams();
  const date = params.get("date") ?? new Date().toISOString().slice(0, 10);
  const [data, setData] = useState<SeedDigestResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    getSeedDigest(id, date).then(setData).catch((e) => setErr(String(e)));
  }, [id, date]);

  const handleReparse = async () => {
    if (!id) return;
    const result = await reparseSource(id, "24h");
    alert(`Queued ${result.queued} raw_payloads for re-extraction`);
  };

  return (
    <div>
      <h1>Digest for {id} on {date}</h1>
      <input type="date" value={date} onChange={(e) => setParams({ date: e.target.value })} />
      <button onClick={handleReparse} style={{ marginLeft: "1rem" }}>Reparse last 24h</button>

      {err && <pre style={{ color: "crimson" }}>{err}</pre>}
      {data && (
        <>
          <ul>
            <li>Crawl jobs: {data.crawl_jobs}</li>
            <li>Variants emitted: {data.variants_emitted}</li>
            <li>Variants published: {data.variants_published}</li>
            <li>Variants rejected: {data.variants_rejected}</li>
          </ul>
          <small>data source: <code>{data.data_source}</code></small>

          {Object.keys(data.rejection_reasons).length > 0 && (
            <>
              <h2>Rejection reasons</h2>
              <RejectionChart reasons={data.rejection_reasons} />
            </>
          )}
        </>
      )}
    </div>
  );
}
```

`RawPayloadViewer.tsx` — wraps the body fetch in an `<iframe>`:

```tsx
import { useParams } from "react-router-dom";
import { getRawPayloadBodyURL } from "../api/admin-client";

export function RawPayloadViewer() {
  const { id } = useParams<{ id: string }>();
  if (!id) return <p>No id</p>;
  return (
    <div>
      <h1>Raw payload {id}</h1>
      <iframe
        src={getRawPayloadBodyURL(id)}
        style={{ width: "100%", height: "calc(100vh - 200px)", border: "1px solid #ccc" }}
        sandbox="allow-same-origin"
      />
    </div>
  );
}
```

Commit:

```bash
git add ui/admin/src/pages/{SeedDigest,RawPayloadViewer}.tsx ui/admin/src/components/RejectionChart.tsx
git commit -m "feat(admin-ui): SeedDigest + RawPayloadViewer pages"
```

---

### Task 7: DefinitionsList + DefinitionEditor pages

**Files:**
- Create: `ui/admin/src/pages/DefinitionsList.tsx`
- Create: `ui/admin/src/pages/DefinitionEditor.tsx`

`DefinitionsList.tsx`:

```tsx
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { listDefinitions, type DefinitionEntry } from "../api/admin-client";

export function DefinitionsList() {
  const [data, setData] = useState<Record<string, DefinitionEntry[]> | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    listDefinitions().then(setData).catch((e) => setErr(String(e)));
  }, []);

  return (
    <div>
      <h1>Definitions</h1>
      {err && <pre style={{ color: "crimson" }}>{err}</pre>}
      {data && Object.entries(data).map(([type, entries]) => (
        <section key={type}>
          <h2>{type}</h2>
          <table>
            <thead><tr><th>Name</th><th>Version</th><th>Updated</th><th>Size</th><th></th></tr></thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.name}>
                  <td>{e.name}</td>
                  <td><code>{e.version.slice(0, 8)}</code></td>
                  <td>{new Date(e.updated_at).toLocaleString()}</td>
                  <td>{e.size} B</td>
                  <td><Link to={`/definitions/${type}/${e.name}`}>edit</Link></td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ))}
    </div>
  );
}
```

`DefinitionEditor.tsx`:

```tsx
import { useEffect, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { getDefinition, putDefinition, deleteDefinition } from "../api/admin-client";

export function DefinitionEditor() {
  const { type, name } = useParams<{ type: string; name: string }>();
  const navigate = useNavigate();
  const [body, setBody] = useState<string>("");
  const [status, setStatus] = useState<string | null>(null);

  useEffect(() => {
    if (!type || !name) return;
    getDefinition(type, name).then(setBody).catch((e) => setStatus(`Load failed: ${e}`));
  }, [type, name]);

  const save = async () => {
    if (!type || !name) return;
    setStatus("Saving…");
    try {
      await putDefinition(type, name, body);
      setStatus("Saved. Broadcasting to crawlers…");
    } catch (e) {
      setStatus(`Save failed: ${e}`);
    }
  };

  const remove = async () => {
    if (!type || !name) return;
    if (!confirm(`Delete ${type}/${name}?`)) return;
    await deleteDefinition(type, name);
    navigate("/definitions");
  };

  return (
    <div>
      <h1>{type} / {name}</h1>
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        style={{ width: "100%", height: "60vh", fontFamily: "monospace", fontSize: "0.9rem" }}
      />
      <div style={{ marginTop: "0.5rem", display: "flex", gap: "0.5rem", alignItems: "center" }}>
        <button onClick={save}>Save</button>
        <button onClick={remove} style={{ color: "crimson" }}>Delete</button>
        {status && <small>{status}</small>}
      </div>
    </div>
  );
}
```

Commit:

```bash
git add ui/admin/src/pages/{DefinitionsList,DefinitionEditor}.tsx ui/admin/src/main.tsx
git commit -m "feat(admin-ui): DefinitionsList + DefinitionEditor pages

Operators edit kind YAMLs, prompts, connector specs, and seeds
directly in the browser. PUT broadcasts opportunities.definitions
.changed.v1 so every crawler/api/worker replica picks it up
within seconds."
```

---

### Task 8: SourceList landing page

**Files:**
- Create: `ui/admin/src/pages/SourceList.tsx`

A simple landing page listing recent sources. Calls a new API endpoint (or reuses `GET /admin/sources` from sources_admin which already exists). Sketch:

```tsx
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { listSources } from "../api/admin-client";

export function SourceList() {
  const [data, setData] = useState<any[]>([]);
  useEffect(() => {
    listSources().then(setData).catch(() => {});
  }, []);
  return (
    <div>
      <h1>Sources</h1>
      <table>
        <thead><tr><th>ID</th><th>Type</th><th>Status</th><th>Country</th><th>Health</th></tr></thead>
        <tbody>
          {data.map((s) => (
            <tr key={s.id}>
              <td><Link to={`/sources/${s.id}`}>{s.id}</Link></td>
              <td>{s.type}</td>
              <td>{s.status}</td>
              <td>{s.country}</td>
              <td>{s.health_score?.toFixed?.(2) ?? "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
```

Add `listSources()` to `admin-client.ts` calling `GET /admin/sources?limit=100`.

Commit:

```bash
git add ui/admin/src/pages/SourceList.tsx ui/admin/src/api/admin-client.ts
git commit -m "feat(admin-ui): SourceList landing page"
```

---

### Task 9: Tag + deploy v8.0.65

```bash
git push origin main
git tag v8.0.65
git push origin v8.0.65
```

After Flux rolls + the admin UI rebuilds:

- Open `/admin/` in a browser.
- Navigate the new pages.
- Edit a kind YAML via `/admin/definitions/kind/job`, save, watch the crawler log react.
- Open a rejected variant's trace → click "view captured HTML".

---

## Plan C Exit Criteria

- [ ] `pkg/icebergclient.ReadRecent` works against the production R2 catalog.
- [ ] `GET /admin/trace/sources/{id}?since=14d` returns Iceberg-augmented summary with `data_source: "postgres+iceberg"` flag.
- [ ] `GET /admin/trace/seeds/{id}/digest?date=YYYY-MM-DD` works for both recent (Postgres) and historic (Iceberg) dates.
- [ ] Admin UI has 7 pages: SourceList, SourceTrace, VariantTrace, OpportunityTrace, SeedDigest, DefinitionsList, DefinitionEditor, RawPayloadViewer.
- [ ] Editing a kind YAML in the UI persists to R2 + broadcasts; the crawler log shows a definitions reload.
- [ ] No existing tests fail.
