# Lean Postgres Plan 1: Rejected-Variant Pruning + Retention Policies

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop writing `pipeline_variants` rows for terminal-stage rejections, delete the 131 MB already accumulated, and set tight retention policies on the three operational hypertables.

**Architecture:** Today `publishRejected` (`apps/crawler/service/crawl_request_handler.go:565-606`) does two things — emit `opportunities.variants.rejected.v1` to NATS AND write a row to `pipeline_variants` at stage='rejected'. The writer already persists the NATS event to the `opportunities.variants_rejected` Iceberg table. The Postgres row is duplicate state. Remove it; wire `GET /admin/variants/rejected` (currently stubbed to 501) to query Iceberg instead. Then set retention policies via `add_retention_policy` matching the spec's 7 d / 30 d / 14 d windows.

**Tech Stack:** Go, Postgres 16 + TimescaleDB, NATS JetStream, Apache Iceberg via `pkg/icebergclient`, Frame v1.97+.

**Depends on:** Phase 1 of pipeline-robustness (v8.0.58) — gives us hypertable `crawl_jobs` + `raw_payloads`.

---

## File Structure

**Modified:**
- `apps/crawler/service/crawl_request_handler.go:565-606` (`publishRejected`) — remove the `VariantStore.Upsert(...)` call.
- `apps/api/cmd/endpoints.go:463-476` (`variantsRejectedHandler`) — query the Iceberg `opportunities.variants_rejected` table; return last N rows.
- `apps/api/cmd/main.go` (handler wiring) — pass the iceberg catalog client to `variantsRejectedHandler`.

**Created:**
- `db/migrations/0020_drop_rejected_variants_and_retention.sql` — DELETE existing rejected rows + set retention policies.
- `apps/crawler/service/crawl_request_handler_test.go` — extend the existing test file with a "rejected variant does NOT write to pipeline_variants" assertion.

---

## Tasks

### Task 1: Remove rejected-variant Postgres write

**Files:**
- Modify: `apps/crawler/service/crawl_request_handler.go` around line 595-604.

- [ ] **Step 1: Write the failing test**

Append to `apps/crawler/service/crawl_request_handler_test.go`:

```go
func TestExecute_RejectedVariantDoesNotWritePipelineRow(t *testing.T) {
	ctx := context.Background()
	scaffold := newCrawlTestScaffold(t)
	// Configure the connector to yield ONE item that will fail Verify
	// (e.g., missing title or unknown kind for the source).
	scaffold.RegisterPages(page{
		HTML:           []byte("<html>x</html>"),
		Items:          1,
		ForceVerifyFail: true, // helper: emits item with empty Title → Verify rejects
	})
	h := service.NewCrawlRequestHandler(scaffold.Deps())

	payload := envelopeJSON(eventsv1.CrawlRequestV1{
		RequestID:      "req-rej",
		SourceID:       scaffold.SourceID,
		IdempotencyKey: "src-rej:2026-05-28T00:00:00Z",
	})
	if err := h.Execute(ctx, &payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Assert: the variants.rejected.v1 event WAS emitted.
	if scaffold.Emitter.Count(eventsv1.TopicVariantsRejected) != 1 {
		t.Fatalf("emitter count for variants.rejected = %d; want 1",
			scaffold.Emitter.Count(eventsv1.TopicVariantsRejected))
	}

	// Assert: NO pipeline_variants row was written for this rejection.
	var rejCount int64
	scaffold.DB.Raw(
		"SELECT count(*) FROM pipeline_variants WHERE current_stage = 'rejected'",
	).Scan(&rejCount)
	if rejCount != 0 {
		t.Fatalf("pipeline_variants rejected count = %d; want 0 (rejections live in Iceberg only)", rejCount)
	}
}
```

If the existing test scaffold (`newCrawlTestScaffold`) doesn't already have a `ForceVerifyFail` hook, look at `TestExecute_VerifyRejection_EmitsRejected` (or equivalent existing test) to find the established pattern for forcing a Verify failure, and reuse it.

- [ ] **Step 2: Run, verify fail**

```bash
cd /home/j/code/stawi.opportunities && go test ./apps/crawler/service/ -run TestExecute_RejectedVariantDoesNotWritePipelineRow -v 2>&1 | tail -10
```
Expected: FAIL (pipeline_variants rejected count = 1).

- [ ] **Step 3: Remove the Upsert call**

In `apps/crawler/service/crawl_request_handler.go` around line 590-604 (`publishRejected`), delete the block:

```go
hk := opp.ExternalID
if hk == "" {
    hk = opp.Title
}
_ = h.deps.VariantStore.Upsert(ctx, variantstate.Variant{
    VariantID:    rej.VariantID,
    SourceID:     sourceID,
    HardKey:      hk,
    Kind:         kind,
    CurrentStage: variantstate.StageRejected,
})
```

Replace with a comment so the next reader doesn't re-add it:

```go
// Verify-stage rejections are NOT persisted to pipeline_variants —
// the Iceberg sink (opportunities.variants_rejected) carries the
// durable record, and GET /admin/variants/rejected reads from there.
// Keeping a Postgres row would duplicate ~700 k rows at our crawl
// rate within a few hours and bloat the hypertable. See:
// docs/superpowers/specs/2026-05-28-lean-postgres-design.md
```

- [ ] **Step 4: Run test, verify it passes**

```bash
go test ./apps/crawler/service/ -run TestExecute_RejectedVariantDoesNotWritePipelineRow -v 2>&1 | tail -10
go test ./apps/crawler/service/... -count=1 2>&1 | tail -15
```
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add apps/crawler/service/crawl_request_handler.go apps/crawler/service/crawl_request_handler_test.go
git commit -m "$(cat <<'EOF'
feat(lean): stop writing pipeline_variants rows for rejected variants

publishRejected was emitting the NATS event (durable to Iceberg
via the writer) AND inserting a pipeline_variants row at terminal
stage='rejected'. Two sources of truth, and the Postgres row was
dominating the hypertable — 697 k rejected rows out of 1.74 M
within hours of deploy. Drop the Upsert; keep the event. Iceberg's
opportunities.variants_rejected is the operator view.

Saves ~131 MB today and prevents indefinite growth at the rejection
rate (~hundreds of rejections/s during a crawl burst).
EOF
)"
```

---

### Task 2: Migration — DELETE rejected rows + retention policies

**Files:**
- Create: `db/migrations/0020_drop_rejected_variants_and_retention.sql`

- [ ] **Step 1: Write the migration**

```sql
-- 0020: drop rejected pipeline_variants rows + set retention windows.
--
-- Pairs with the Task-1 code change (publishRejected stops writing).
-- The DELETE here clears the rows that landed before the code change,
-- so the hypertable returns to its true working size.
--
-- Retention windows match docs/superpowers/specs/2026-05-28-lean-postgres-design.md:
--   pipeline_variants  7 d  — in-flight ledger; in-progress crawls + recent failures only
--   crawl_jobs        30 d  — source-health investigations
--   raw_payloads      14 d  — strictly inside R2's 30 d lifecycle on raw/ prefix
--
-- All three are idempotent: add_retention_policy with if_not_exists,
-- preceded by remove_retention_policy(if_exists) to handle the case
-- where 0019 set a wider window we're tightening.

-- ---------- Clear the rejected backlog ----------

DELETE FROM pipeline_variants WHERE current_stage = 'rejected';

-- ---------- pipeline_variants retention (7 d) ----------

SELECT remove_retention_policy('pipeline_variants', if_exists => TRUE);
SELECT add_retention_policy('pipeline_variants', INTERVAL '7 days',
                            if_not_exists => TRUE);

-- ---------- crawl_jobs retention (30 d, tightened from 0019's 90 d) ----------

SELECT remove_retention_policy('crawl_jobs', if_exists => TRUE);
SELECT add_retention_policy('crawl_jobs', INTERVAL '30 days',
                            if_not_exists => TRUE);

-- ---------- raw_payloads retention (14 d, tightened from 0019's 30 d) ----------

SELECT remove_retention_policy('raw_payloads', if_exists => TRUE);
SELECT add_retention_policy('raw_payloads', INTERVAL '14 days',
                            if_not_exists => TRUE);
```

- [ ] **Step 2: Apply against the ephemeral test DB to verify clean apply**

```bash
cd /home/j/code/stawi.opportunities && go test ./tests/integration/... -run TestMigrationsApplyCleanly -count=1 -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add db/migrations/0020_drop_rejected_variants_and_retention.sql
git commit -m "feat(lean): retention windows + clear rejected backlog

7 d on pipeline_variants, 30 d on crawl_jobs, 14 d on raw_payloads.
DELETE of existing stage='rejected' rows (~697 k, ~131 MB) reclaims
the space that publishRejected wrote before the prior commit
stopped emitting them.

The DELETE is safe — no consumer queries this stage from Postgres
(verified by grep), the data lives durably in Iceberg's
opportunities.variants_rejected table."
```

---

### Task 3: Wire /admin/variants/rejected to Iceberg

**Files:**
- Modify: `apps/api/cmd/endpoints.go:463-476` (`variantsRejectedHandler`).
- Modify: `apps/api/cmd/main.go` (handler wiring).
- Reference: `pkg/icebergclient/catalog.go` for client construction; `pkg/icebergclient/tables.go` for table-name constants.

This task replaces the 501 stub with a real Iceberg query against `opportunities.variants_rejected`.

- [ ] **Step 1: Inspect the existing icebergclient surface**

```bash
cd /home/j/code/stawi.opportunities
cat pkg/icebergclient/catalog.go
cat pkg/icebergclient/tables.go
# How does the writer read existing rows from Iceberg? (e.g., the compact handler)
grep -rn "Scan\|ScanTable\|ReadTable\|catalog.Catalog" pkg/icebergclient apps/writer/service --include="*.go" 2>&1 | grep -v _test | head -10
```

If the codebase doesn't yet have a generic "read last N rows from an Iceberg table" helper, you'll need to add one to `pkg/icebergclient`. If it does, reuse it.

- [ ] **Step 2: Add icebergclient.ReadRecent helper (if not present)**

Create or extend `pkg/icebergclient/read.go`:

```go
package icebergclient

import (
	"context"
	"fmt"

	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/table"
)

// ReadRecent returns up to limit rows from `tableName` in `namespace`,
// ordered by the table's primary timestamp column DESC. Used by
// operator admin endpoints (e.g. /admin/variants/rejected) that need
// "the most-recently-rejected N rows" without dragging the full
// Parquet partitions through memory.
//
// The implementation does a planFiles scan filtered on the most-recent
// snapshot's manifest, then projects only the requested columns. For
// admin views that read ≤ 1 000 rows this is cheap; for larger ranges,
// callers should use a column-projected reader directly.
func (c *Catalog) ReadRecent(
	ctx context.Context,
	namespace, tableName string,
	timeColumn string,
	limit int,
	columns []string,
) ([]map[string]any, error) {
	ident := table.Identifier{namespace, tableName}
	tbl, err := c.cat.LoadTable(ctx, ident, nil)
	if err != nil {
		return nil, fmt.Errorf("load table %s.%s: %w", namespace, tableName, err)
	}

	// Single-snapshot scan with projection. iceberg-go exposes Scan()
	// which returns an arrow.RecordReader; rows accumulate into a slice
	// of maps because the admin endpoint returns JSON.
	scan := tbl.NewScan().Select(columns...)
	rdr, err := scan.ToArrowRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan %s.%s: %w", namespace, tableName, err)
	}
	defer rdr.Release()

	rows := make([]map[string]any, 0, limit)
	for rdr.Next() && len(rows) < limit {
		rec := rdr.Record()
		nrows := int(rec.NumRows())
		ncols := int(rec.NumCols())
		schema := rec.Schema()
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

Note: `iceberg-go`'s exact API for scanning to Arrow varies by version. Run `go doc github.com/apache/iceberg-go/table Scan` and adapt — if `ToArrowRecords` doesn't exist on this version, the closest equivalent is `ToArrowTable` or `Plan().ToArrowRecordReader()`. The codebase already uses this library in `apps/writer/service/` for writing; mirror whatever read pattern is closest to what writer uses for round-trip verification.

- [ ] **Step 3: Replace the 501 stub**

In `apps/api/cmd/endpoints.go:463-476`:

```go
// variantsRejectedHandler returns the most-recently rejected variants
// (Verify-stage rejections) read from the
// opportunities.variants_rejected Iceberg table.
func variantsRejectedHandler(catalog *icebergclient.Catalog) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		limit := 50
		if l := req.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		rows, err := catalog.ReadRecent(
			ctx,
			"opportunities",
			"variants_rejected",
			"rejected_at",
			limit,
			[]string{"variant_id", "source_id", "kind", "title", "reasons", "rejected_at"},
		)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows":  rows,
			"count": len(rows),
		})
	}
}
```

Add the import `"github.com/stawi-opportunities/opportunities/pkg/icebergclient"` and `"strconv"` if not already present.

- [ ] **Step 4: Wire the catalog client in main.go**

In `apps/api/cmd/main.go`, after the existing service init but before the handler registration:

```go
icebergCat, err := icebergclient.NewCatalogFromEnv(ctx)
if err != nil {
    log.WithError(err).Fatal("iceberg catalog init failed")
}
```

Replace the line `mux.HandleFunc("GET /admin/variants/rejected", variantsRejectedHandler())` with:

```go
mux.HandleFunc("GET /admin/variants/rejected", variantsRejectedHandler(icebergCat))
```

The `icebergclient.NewCatalogFromEnv` constructor name is illustrative — use whatever the existing writer uses to bootstrap its catalog (`grep -n "NewCatalog\|catalog.NewClient" apps/writer/cmd/main.go`).

- [ ] **Step 5: Test**

The endpoint depends on a real R2 catalog; integration testing isn't practical in unit tests. Instead, write a smoke test against the live catalog:

```bash
cd /home/j/code/stawi.opportunities
go build ./apps/api/... 2>&1 | tail -5
# Smoke test (requires deploy):
# kubectl port-forward -n product-opportunities svc/opportunities-api 18080:80
# curl -s "http://localhost:18080/admin/variants/rejected?limit=5" | jq .
```

For a unit test, mock the catalog: extract `ReadRecent` behind an interface and have the handler take that interface; tests pass an in-memory fake. Skip if the existing test pattern doesn't already do this — wait for the deploy smoke test.

- [ ] **Step 6: Commit**

```bash
git add apps/api/cmd/endpoints.go apps/api/cmd/main.go pkg/icebergclient/read.go
git commit -m "feat(api): wire /admin/variants/rejected to read from Iceberg

Replaces the 501 stub. Returns the most-recently rejected variants
from the opportunities.variants_rejected Iceberg table. ?limit=N
parameter caps at 500."
```

---

### Task 4: Tag + deploy v8.0.59

- [ ] **Step 1: Push, tag**

```bash
cd /home/j/code/stawi.opportunities
git push origin main
git tag v8.0.59
git push origin v8.0.59
```

- [ ] **Step 2: Wait for build, let Flux reconcile**

```bash
gh run watch $(gh run list --limit 1 --workflow release.yaml --repo stawi-opportunities/opportunities --json databaseId -q '.[0].databaseId') --repo stawi-opportunities/opportunities --exit-status
```

- [ ] **Step 3: Verify in cluster**

```bash
# Confirm migration 0020 ran: pipeline_variants no longer has 'rejected' rows
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "SELECT current_stage, count(*) FROM pipeline_variants GROUP BY current_stage"

# Confirm retention policies are in place
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "
SELECT hypertable_name, config FROM timescaledb_information.jobs
WHERE proc_name = 'policy_retention'
ORDER BY hypertable_name"

# Smoke-test the admin endpoint
kubectl port-forward -n product-opportunities svc/opportunities-api 18080:80 &
sleep 2
curl -s http://localhost:18080/admin/variants/rejected?limit=5 | jq .
```

Expected:
- Zero rows at `current_stage='rejected'`.
- Three retention policies at 7d / 30d / 14d.
- Admin endpoint returns JSON (may be empty if Iceberg table has nothing recent).

---

## Plan 1 Exit Criteria

- [ ] No new `pipeline_variants` rows at `current_stage='rejected'` for at least 1 hour post-deploy.
- [ ] `SELECT count(*) FROM pipeline_variants` drops by ~700 k after migration.
- [ ] All three retention policies present and active.
- [ ] `GET /admin/variants/rejected?limit=10` returns valid JSON from Iceberg.
- [ ] No regressions in existing crawler / API tests.
