# Lean Postgres Plan 2: Hot-Field Promotion

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lift the three filter-hot attribute fields (`employment_type`, `seniority`, `geo_scope`) out of the `opportunities.attributes` JSONB to top-level columns with partial indexes. Eliminates the TOAST-out + JSON-path-extract cost on every filter query without changing what the API returns to clients (the full `attributes` blob keeps echoing back unchanged).

**Architecture:** The canonical merge in `apps/worker/service/canonical.go:252-315` (`snapshotToOpportunity`) constructs the row that `UpsertOpportunity` writes. It already populates the simple top-level fields; we add the three new columns at the same site. The SQL UPSERT in `pkg/variantstate/store.go:399-450` extends the INSERT column list. The reader in `apps/api/cmd/jobs_backend.go:367-381` replaces `attributes->>'X'` with bare `X` in the filter projection and facet aggregation. The `attributes` JSONB stays — clients still see it on read.

**Tech Stack:** Go, GORM, Postgres 16 + TimescaleDB.

**Depends on:** Plan 1 (v8.0.59) for the retention story to be in place, but functionally independent — could ship before Plan 1 if needed.

---

## File Structure

**Created:**
- `db/migrations/0021_opportunities_hot_fields.sql` — add three nullable TEXT columns, three partial B-tree indexes, backfill UPDATE from existing JSONB.

**Modified:**
- `pkg/variantstate/store.go` (lines 329-358, `Opportunity` struct; lines 399-450, `UpsertOpportunity` SQL) — add three fields, extend the INSERT + ON CONFLICT clauses.
- `apps/worker/service/canonical.go` (lines 252-315, `snapshotToOpportunity`) — pull the three fields out of `Attributes` and set them on the returned struct.
- `apps/api/cmd/jobs_backend.go` (lines 367-381, the `facetColumns` and `filterColumns` slices) — replace `attributes->>'X'` with `X`.

---

## Tasks

### Task 1: Migration — add columns, indexes, backfill

**Files:**
- Create: `db/migrations/0021_opportunities_hot_fields.sql`

- [ ] **Step 1: Write the migration**

```sql
-- 0021: promote three hot filter fields from opportunities.attributes
-- to top-level columns. Eliminates the TOAST-out + JSON-path-extract
-- on every filtered list query.
--
-- Per the spec at docs/superpowers/specs/2026-05-28-lean-postgres-design.md:
-- the JSONB blob itself stays — the API still returns full attributes
-- on read, so display fields like categories[], application_url,
-- and kind-specific extensions are preserved.
--
-- The three columns are TEXT NULL because:
--   - employment_type: free-form within a known set (full-time, part-time, contract, ...).
--   - seniority: free-form within a known set (junior, mid, senior, lead, ...).
--   - geo_scope: free-form within a known set (global, regional, national, local).
-- A typed enum would force a schema migration every time the source
-- vocabulary expands; the partial B-tree indexes are equally fast on
-- TEXT for low-cardinality values.
--
-- Indexes are partial on (hidden = false AND status = 'active') so
-- they only cover the rows the search/list queries actually filter on
-- — the same predicate the existing opportunities_kind_country_last_seen_idx
-- (migration 0012) uses.

BEGIN;

ALTER TABLE opportunities
    ADD COLUMN IF NOT EXISTS employment_type TEXT,
    ADD COLUMN IF NOT EXISTS seniority       TEXT,
    ADD COLUMN IF NOT EXISTS geo_scope       TEXT;

-- Backfill from existing JSONB. Safe at any scale because it's a
-- single seq scan with a projection (the JSON ops are CPU-cheap on
-- TOASTed rows).
UPDATE opportunities
   SET employment_type = NULLIF(attributes->>'employment_type', ''),
       seniority       = NULLIF(attributes->>'seniority', ''),
       geo_scope       = NULLIF(attributes->>'geo_scope', '')
 WHERE employment_type IS NULL
    OR seniority IS NULL
    OR geo_scope IS NULL;

-- Partial indexes scoped to the search predicate.
CREATE INDEX IF NOT EXISTS opportunities_employment_type_idx
    ON opportunities (employment_type)
    WHERE hidden = false AND status = 'active' AND employment_type IS NOT NULL;

CREATE INDEX IF NOT EXISTS opportunities_seniority_idx
    ON opportunities (seniority)
    WHERE hidden = false AND status = 'active' AND seniority IS NOT NULL;

CREATE INDEX IF NOT EXISTS opportunities_geo_scope_idx
    ON opportunities (geo_scope)
    WHERE hidden = false AND status = 'active' AND geo_scope IS NOT NULL;

COMMIT;
```

- [ ] **Step 2: Verify migration applies + backfill works**

```bash
cd /home/j/code/stawi.opportunities && go test ./tests/integration/... -run TestMigrationsApplyCleanly -count=1 -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add db/migrations/0021_opportunities_hot_fields.sql
git commit -m "feat(lean): promote employment_type/seniority/geo_scope to columns

Three filter-hot fields move from opportunities.attributes JSONB to
top-level columns + partial B-tree indexes scoped to the search
predicate (hidden=false AND status='active' AND X IS NOT NULL).

Backfill from existing JSONB runs inline. attributes blob stays so
clients still see the full payload on read. No quality regression —
this is a pure perf change."
```

---

### Task 2: Extend variantstate.Opportunity struct + UpsertOpportunity SQL

**Files:**
- Modify: `pkg/variantstate/store.go` (Opportunity struct around lines 329-358; UpsertOpportunity around lines 399-450).

- [ ] **Step 1: Add the three fields to the struct**

In `pkg/variantstate/store.go`, find `type Opportunity struct {` and add three pointer fields after `Currency`:

```go
EmploymentType *string `gorm:"column:employment_type"`
Seniority      *string `gorm:"column:seniority"`
GeoScope       *string `gorm:"column:geo_scope"`
```

Pointer because all three are nullable. Empty string from the snapshot becomes `nil` via `ptrIfNonEmpty` (already defined elsewhere in this file).

- [ ] **Step 2: Extend UpsertOpportunity SQL**

In the same file, find the `UpsertOpportunity` method (around line 377). The SQL has an INSERT column list, a VALUES list, and an ON CONFLICT DO UPDATE clause. Add three columns to all three sections.

INSERT column list — add `employment_type, seniority, geo_scope` between `currency, amount_min, amount_max` and `status, first_seen_at, last_seen_at`:

```sql
INSERT INTO opportunities (
    canonical_id, slug, kind, source_id,
    title, description, issuing_entity,
    country, region, city,
    remote, apply_url, posted_at, deadline,
    currency, amount_min, amount_max,
    employment_type, seniority, geo_scope,
    status, first_seen_at, last_seen_at,
    attributes, quality_score, hidden, hidden_reason
) VALUES (
    ?, ?, ?, ?,
    ?, ?, ?,
    ?, ?, ?,
    ?, ?, ?, ?,
    ?, ?, ?,
    ?, ?, ?,
    ?, ?, ?,
    COALESCE(?::jsonb, '{}'::jsonb), ?, ?, ?
)
```

ON CONFLICT clause — preserve the same COALESCE/NULLIF pattern used for `country`, `currency` etc. (incoming non-empty wins, otherwise prior value sticks). Add after the `currency = …` line:

```sql
employment_type = COALESCE(NULLIF(EXCLUDED.employment_type,''), opportunities.employment_type),
seniority       = COALESCE(NULLIF(EXCLUDED.seniority,''),       opportunities.seniority),
geo_scope       = COALESCE(NULLIF(EXCLUDED.geo_scope,''),       opportunities.geo_scope),
```

Args slice — find the `s.db(ctx, false).Exec(...)` call where the args are listed. Add `nullStr(o.EmploymentType), nullStr(o.Seniority), nullStr(o.GeoScope)` between the existing `nullStr(o.Currency), o.AmountMin, o.AmountMax,` line and `o.Status, o.FirstSeenAt, o.LastSeenAt,`.

- [ ] **Step 3: Run existing tests; expect them to still pass**

```bash
cd /home/j/code/stawi.opportunities
go build ./... 2>&1 | tail -5
go test ./pkg/variantstate/... -count=1 2>&1 | tail -10
```
Expected: clean build, no test changes needed (the existing tests don't assert on these specific columns).

- [ ] **Step 4: Commit**

```bash
git add pkg/variantstate/store.go
git commit -m "feat(lean): variantstate.UpsertOpportunity writes three hot-field columns"
```

---

### Task 3: Update canonical merge to populate the hot fields

**Files:**
- Modify: `apps/worker/service/canonical.go` (`snapshotToOpportunity`, lines 252-315).

- [ ] **Step 1: Write the failing test**

Append to `apps/worker/service/canonical_test.go` (or whichever file has the existing snapshotToOpportunity tests):

```go
func TestSnapshotToOpportunity_PromotesHotFields(t *testing.T) {
	snap := kv.ClusterSnapshot{
		CanonicalID:  "opp-001",
		Slug:         "test-slug",
		Kind:         "job",
		Title:        "Senior Engineer",
		Attributes: map[string]any{
			"employment_type": "full-time",
			"seniority":       "senior",
			"geo_scope":       "global",
			"other_field":     "kept-in-attrs",
		},
		FirstSeenAt: time.Now().UTC(),
		LastSeenAt:  time.Now().UTC(),
	}
	opp := snapshotToOpportunity(snap, "hk-001")

	if opp.EmploymentType == nil || *opp.EmploymentType != "full-time" {
		t.Fatalf("EmploymentType = %v; want full-time", opp.EmploymentType)
	}
	if opp.Seniority == nil || *opp.Seniority != "senior" {
		t.Fatalf("Seniority = %v; want senior", opp.Seniority)
	}
	if opp.GeoScope == nil || *opp.GeoScope != "global" {
		t.Fatalf("GeoScope = %v; want global", opp.GeoScope)
	}
	// attributes blob still has the original keys — the API echoes them.
	if opp.Attributes["other_field"] != "kept-in-attrs" {
		t.Fatalf("attributes lost other_field: %v", opp.Attributes)
	}
}

func TestSnapshotToOpportunity_EmptyHotFieldsAreNil(t *testing.T) {
	snap := kv.ClusterSnapshot{
		CanonicalID: "opp-002",
		Slug:        "test",
		Kind:        "job",
		Title:       "Engineer",
		Attributes:  map[string]any{},
		FirstSeenAt: time.Now().UTC(),
		LastSeenAt:  time.Now().UTC(),
	}
	opp := snapshotToOpportunity(snap, "")

	if opp.EmploymentType != nil {
		t.Fatalf("EmploymentType = %v; want nil (no Attributes value)", opp.EmploymentType)
	}
	if opp.Seniority != nil {
		t.Fatalf("Seniority = %v; want nil", opp.Seniority)
	}
	if opp.GeoScope != nil {
		t.Fatalf("GeoScope = %v; want nil", opp.GeoScope)
	}
}
```

- [ ] **Step 2: Run, verify fail**

```bash
go test ./apps/worker/service/ -run TestSnapshotToOpportunity_Promotes -v 2>&1 | tail -10
```
Expected: FAIL — `opp.EmploymentType` doesn't exist or is nil.

- [ ] **Step 3: Update snapshotToOpportunity**

In `apps/worker/service/canonical.go` around lines 252-315, before the `return variantstate.Opportunity{...}` statement, add an extraction step. The attrs map is already built earlier in the function. Add right before the `return`:

```go
// Hot-field promotion: lift filter-hot attribute keys to top-level
// pointer fields. attrs keeps the original values so the API echoes
// the full blob on read.
extractAttrString := func(key string) *string {
    if v, ok := attrs[key].(string); ok && v != "" {
        return &v
    }
    return nil
}
employmentType := extractAttrString("employment_type")
seniority      := extractAttrString("seniority")
geoScope       := extractAttrString("geo_scope")
```

Then in the `return variantstate.Opportunity{...}` literal, add the three fields (alphabetical placement is fine; the struct doesn't care):

```go
EmploymentType: employmentType,
Seniority:      seniority,
GeoScope:       geoScope,
```

- [ ] **Step 4: Run, verify pass**

```bash
go test ./apps/worker/service/ -run TestSnapshotToOpportunity -v 2>&1 | tail -10
go test ./apps/worker/service/... -count=1 2>&1 | tail -15
```
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add apps/worker/service/canonical.go apps/worker/service/canonical_test.go
git commit -m "feat(lean): canonical merge extracts employment_type/seniority/geo_scope

snapshotToOpportunity lifts these three keys from the merged Attributes
map onto top-level pointer fields. The attrs blob retains its
original values so the API response shape is unchanged for clients."
```

---

### Task 4: Update API reads to use columns

**Files:**
- Modify: `apps/api/cmd/jobs_backend.go` lines 367-381 (the `filterColumns` + `facetColumns` slices); also line 667 (the kind-specific facet switch).

- [ ] **Step 1: Inspect the existing structure**

Read `apps/api/cmd/jobs_backend.go:360-400` to see exactly how `filterColumns` / `facetColumns` are defined. They're slices of struct literals with `{name, sqlExpr, alias}` shape (see the lines: `{"geo_scope", "attributes->>'geo_scope'", "geo_scope"}`).

- [ ] **Step 2: Replace the JSON-path expressions with bare column names**

In `apps/api/cmd/jobs_backend.go` around line 367-369 (the `filterColumns` / faceting slice — the exact name varies):

```go
// Old:
{"geo_scope",       "attributes->>'geo_scope'",       "geo_scope"},
{"employment_type", "attributes->>'employment_type'", "employment_type"},
{"seniority",       "attributes->>'seniority'",       "seniority"},
```

Becomes:

```go
{"geo_scope",       "geo_scope",       "geo_scope"},
{"employment_type", "employment_type", "employment_type"},
{"seniority",       "seniority",       "seniority"},
```

Same replacement at lines 379-381 (the second slice that's the WHERE-clause version, structurally similar).

If line 667 also references these three keys in a switch statement (`case "geo_scope", "employment_type", "seniority", "field_of_study", "degree_level":`), look at the surrounding logic. The switch likely chooses between "bare column" and "JSONB path" — three of the cases now want the bare-column branch. Adapt by either:
- Splitting the case so `"geo_scope", "employment_type", "seniority"` use the bare-column branch, or
- Keeping a single case and letting the existing logic produce `attributes->>'X'` — slightly wasteful but functionally correct (PG still uses the bare column index because the planner sees the equivalent expression).

Prefer the split (more explicit, no relying on planner magic).

- [ ] **Step 3: Verify the SELECT still projects everything**

The SELECT statement in `jobs_backend.go:264` reads `quality_score, attributes, NULL::text AS categories_placeholder`. This is unchanged — `attributes` continues to come back as the full JSONB blob so clients see employment_type/seniority/geo_scope echoed inside.

If the SELECT also explicitly projects `employment_type`, `seniority`, `geo_scope` AS aliases of the JSON-path expressions (look around line 264), those projections should likewise switch to bare column reads.

- [ ] **Step 4: Run tests**

```bash
go test ./apps/api/cmd/... -count=1 2>&1 | tail -10
go build ./apps/api/... 2>&1 | tail -5
```
Expected: PASS.

If any tests query the filter behaviour with `?employment_type=full-time` and rely on a specific SQL shape, those tests pass either way because both expressions return the same value — but inspect the test expectations to be sure no test pins on the literal `attributes->>` string.

- [ ] **Step 5: Commit**

```bash
git add apps/api/cmd/jobs_backend.go
git commit -m "feat(lean): API reads hot fields from columns, not JSON paths

employment_type/seniority/geo_scope filters and facets now use the
new top-level columns + partial B-tree indexes from migration 0021.
attributes JSONB is still returned on read so the client payload
shape is identical."
```

---

### Task 5: Tag + deploy v8.0.60

- [ ] **Step 1: Push, tag**

```bash
cd /home/j/code/stawi.opportunities
git push origin main
git tag v8.0.60
git push origin v8.0.60
```

- [ ] **Step 2: Wait for build + flux**

```bash
gh run watch $(gh run list --limit 1 --workflow release.yaml --repo stawi-opportunities/opportunities --json databaseId -q '.[0].databaseId') --repo stawi-opportunities/opportunities --exit-status
```

- [ ] **Step 3: Verify in cluster**

```bash
# Columns + indexes present
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "\d opportunities" | grep -E "employment_type|seniority|geo_scope"

# Backfill ran
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "
SELECT
  count(*) FILTER (WHERE employment_type IS NOT NULL) AS et_filled,
  count(*) FILTER (WHERE seniority IS NOT NULL)       AS sen_filled,
  count(*) FILTER (WHERE geo_scope IS NOT NULL)       AS gs_filled,
  count(*) AS total
FROM opportunities"

# EXPLAIN ANALYZE the filter path to confirm the index is used
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "
EXPLAIN (ANALYZE, BUFFERS)
SELECT canonical_id FROM opportunities
WHERE employment_type = 'full-time' AND hidden = false AND status = 'active'
LIMIT 50"
```

Expected:
- Three columns added.
- Backfill non-zero for at least one column (depends on what the current 369 rows have in attributes).
- EXPLAIN shows `Index Scan using opportunities_employment_type_idx` (or `Bitmap Index Scan` for OR'd predicates).

---

## Plan 2 Exit Criteria

- [ ] Three new columns + three partial B-tree indexes present.
- [ ] `EXPLAIN ANALYZE` on a filter query shows the new index is used.
- [ ] Backfill UPDATE ran (rows with non-null `employment_type`/`seniority`/`geo_scope` > 0 if the attributes blob had those keys).
- [ ] API response shape unchanged from client's perspective — `attributes` blob still echoes back.
- [ ] No regressions in API or worker tests.
