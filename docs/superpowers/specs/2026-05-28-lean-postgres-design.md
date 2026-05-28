# Lean Postgres + R2 Split — Design

**Date:** 2026-05-28
**Status:** Approved (user confirmed direction 2026-05-28; description externalization explicitly REJECTED to preserve matching quality)

## Problem

The opportunities Postgres cluster is on a trajectory to ~100 GB of hot data at 1 M opportunities + 100 k candidates. An audit on 2026-05-28 found the bulk of that growth is avoidable:

- `pipeline_variants` already holds 1.74 M rows (793 MB in a single chunk) just hours after the latest deploy, with 697 k of those rows at terminal stage `rejected` — duplicating data the Iceberg event log already carries.
- No retention policies are firing yet on `pipeline_variants`, `crawl_jobs`, or `raw_payloads`. Operational state, by design, doesn't need permanent history.
- `candidate_profiles.cv_raw_text` is the largest single per-row payload that will land in Postgres at scale (10–50 KB × 100 k candidates = 1–5 GB) for data the matcher uses only to re-extract structured fields.
- Hot filter fields (`employment_type`, `seniority`, `geo_scope`) live inside the `attributes` JSONB. Every WHERE clause is a TOAST-out + JSON-path extract; cheap at 369 rows, painful at 1 M.

What is NOT a problem:

- `opportunities.description` stays in Postgres. Matching quality (BM25 keyword recall + re-embedding when models change) depends on having the full text local. Externalizing it would weaken search.
- DiskANN vector index is already in use (`opportunities_embedding_diskann_idx`) — best-in-class for lean memory. No change.
- ParadeDB BM25 index — already in use for full-text. No change.

## Solution

Four orthogonal changes, each independently shippable:

1. **Rejected-variant pruning.** Stop writing `pipeline_variants` rows for `stage='rejected'`. The Iceberg `variants.rejected.v1` event already records the rejection durably; an admin view reads it from there.
2. **Retention policies.** `pipeline_variants` 7 days, `raw_payloads` 14 days (matches R2 lifecycle), `crawl_jobs` 30 days. Operational-state tables, not history.
3. **Hot-field promotion.** Move `employment_type`, `seniority`, `geo_scope` from `attributes` JSONB to top-level columns on `opportunities` with a B-tree index. Backfill from existing rows. The remaining attributes stay in JSONB for display.
4. **Candidate CV externalization.** `candidate_profiles.cv_raw_text` → R2 at `candidates/{candidate_id}/cv-raw.txt.gz`. The row stores `cv_storage_uri` + the extracted structured fields the matcher actually queries (skills, target_role, salary_min/max, currency, experience_level). Same treatment for `bio` and `work_history` JSONB blobs.

Each change ships as a separate plan; no plan depends on another.

## Architecture

### Change 1 — Rejected variants

`apps/crawler/service/crawl_request_handler.go` `publishRejected` (current line ~497) does two things:

```go
_ = h.deps.VariantStore.Upsert(ctx, variantstate.Variant{
    VariantID:    rej.VariantID,
    ...
    CurrentStage: variantstate.StageRejected,
})
env := eventsv1.NewEnvelope(eventsv1.TopicVariantsRejected, rej)
return h.deps.Svc.EventsManager().Emit(ctx, ...)
```

Remove the `Upsert` call. Keep the event emission — `apps/writer` already persists `variants.rejected.v1` to Iceberg (`cluster-chronicle` bucket), and `apps/api/cmd/main.go:185` already exposes `GET /admin/variants/rejected` (currently stubbed to 501). Wire that endpoint to query the Iceberg table.

Migration cleanup: `DELETE FROM pipeline_variants WHERE current_stage = 'rejected'` to free the 131 MB already accumulated. Safe to run because no downstream consumer queries this stage from Postgres (verified by grep).

### Change 2 — Retention policies

Single SQL migration (`db/migrations/0020_operational_retention.sql`):

```sql
SELECT add_retention_policy('pipeline_variants', INTERVAL '7 days', if_not_exists => TRUE);
-- crawl_jobs + raw_payloads already have 90 d / 30 d from 0019; tighten:
SELECT remove_retention_policy('crawl_jobs', if_exists => TRUE);
SELECT add_retention_policy('crawl_jobs', INTERVAL '30 days', if_not_exists => TRUE);
SELECT remove_retention_policy('raw_payloads', if_exists => TRUE);
SELECT add_retention_policy('raw_payloads', INTERVAL '14 days', if_not_exists => TRUE);
```

R2 already has 30-day lifecycle on the `raw/` prefix; 14-day Postgres retention keeps the ledger window strictly inside that.

`pipeline_variants` retention to 7 d means the in-flight ledger stays 7 days. Variants that successfully publish remain queryable via `opportunities`; failed ones beyond 7 d are visible via the Iceberg `variants.flagged.v1` topic.

### Change 3 — Hot-field promotion

Schema:

```sql
ALTER TABLE opportunities
    ADD COLUMN employment_type TEXT,
    ADD COLUMN seniority       TEXT,
    ADD COLUMN geo_scope       TEXT;

CREATE INDEX opportunities_employment_type_idx
    ON opportunities (employment_type)
    WHERE hidden = false AND status = 'active';

CREATE INDEX opportunities_seniority_idx
    ON opportunities (seniority)
    WHERE hidden = false AND status = 'active';

CREATE INDEX opportunities_geo_scope_idx
    ON opportunities (geo_scope)
    WHERE hidden = false AND status = 'active';
```

Writer (canonical merge in `apps/worker/service/canonical.go`): when upserting into `opportunities`, pull these three fields out of the merged Attributes map and set them as columns. The JSONB blob keeps everything else (for display).

Reader (`apps/api/cmd/jobs_backend.go` lines 367-381): replace `attributes->>'employment_type'` with `employment_type`, etc., in both the filter projection and the facet aggregation. Keep the JSONB read for display (the API still returns the full `attributes` blob to the client at line 264).

Backfill via a one-shot UPDATE in the migration:

```sql
UPDATE opportunities
   SET employment_type = attributes->>'employment_type',
       seniority       = attributes->>'seniority',
       geo_scope       = attributes->>'geo_scope';
```

Safe at current scale (369 rows). Cheap at growth (single seq scan + projection; bounded by total opportunity count).

### Change 4 — Candidate CV externalization

`candidate_profiles.cv_raw_text` (the extracted CV text), `bio`, and `work_history` JSONB move to R2 at `candidates/{candidate_id}/cv-raw.txt.gz`, `candidates/{candidate_id}/bio.txt.gz`, `candidates/{candidate_id}/work-history.json.gz`.

Row stores only:
- `cv_storage_uri TEXT` — pointer to the blob.
- `cv_content_hash VARCHAR(64)` — sha256 for change detection.
- Structured extracted fields the matcher uses: `skills text[]`, `strong_skills text[]`, `working_skills text[]`, `target_role`, `seniority`, `years_experience`, `salary_min`, `salary_max`, `currency`, `experience_level`, `preferred_countries text[]`, `preferred_regions text[]`.

`skills text[]` with GIN index makes "candidates with skill X" fast:

```sql
CREATE INDEX candidate_profiles_skills_gin_idx
    ON candidate_profiles USING gin (skills);
```

Migration: add new columns, backfill from existing strings (split on commas — current `skills TEXT` is a comma-separated list), upload existing CV text to R2 via a one-shot Go script, then drop the legacy columns.

Matching service (`apps/matching/service/...`): if it currently reads `cv_raw_text` (verify via grep), replace with R2 fetch through `pkg/archive`. If matching uses only the extracted fields (likely — embedding is the actual matching signal), no change needed.

## Components

### Files touched per change

| Change | Files |
|---|---|
| 1. Rejected pruning | `apps/crawler/service/crawl_request_handler.go` (remove Upsert), `apps/api/cmd/main.go` (wire the existing /admin/variants/rejected endpoint to Iceberg), migration `0020_drop_rejected_variants.sql` (DELETE FROM pipeline_variants WHERE current_stage='rejected') |
| 2. Retention | `db/migrations/0021_operational_retention.sql` |
| 3. Hot-field promotion | `db/migrations/0022_opportunities_hot_fields.sql`, `pkg/variantstate/store.go` (Opportunity struct + UpsertOpportunity SQL), `apps/worker/service/canonical.go` (pull fields out before upsert), `apps/api/cmd/jobs_backend.go` (read columns instead of JSON paths) |
| 4. CV externalization | `db/migrations/0023_candidate_lean.sql`, `pkg/repository/candidate.go`, `apps/matching/...` (if it reads cv_raw_text), one-shot Go migration script `cmd/migrate-cv-to-r2/main.go` |

### Decisions locked

- **Description stays in Postgres** (user-confirmed 2026-05-28; matching quality requires it).
- **DiskANN unchanged** — already optimal for lean Postgres.
- **BM25 index unchanged** — `title + description + issuing_entity + country + kind` stays.
- **Retention windows**: 7 d variants, 30 d crawl_jobs, 14 d raw_payloads.
- **R2 paths**: `candidates/{candidate_id}/cv-raw.txt.gz`, `candidates/{candidate_id}/bio.txt.gz`, `candidates/{candidate_id}/work-history.json.gz`. Same `r2-account-credentials-opportunities` token (already cluster-wide).
- **Hot fields to promote**: `employment_type`, `seniority`, `geo_scope`. The categories array and remaining kind-specific facets stay in JSONB until the API actually needs them as filter columns.

## Data Flow

Unchanged from today except:

- `publishRejected` no longer writes to Postgres; only emits to NATS.
- `canonical.go` extracts three hot fields from Attributes before `UpsertOpportunity`.
- Candidate CV upload (in applications/onboarding flow) writes the raw text to R2 first, then stores `cv_storage_uri` + extracted fields on the row.
- Old `pipeline_variants` chunks beyond 7 d auto-drop via the TimescaleDB retention background worker (every ~10 min default).

## Error Handling

- **Rejected pruning**: if NATS is down, the event isn't emitted; the variant is silently rejected without an audit trail. This is acceptable because the crawl is then retried by NATS redelivery of the upstream `crawl.requests.v1`. Operator should see this as a Prometheus alert on `nats_jetstream_publish_errors`.
- **Retention**: TimescaleDB's `add_retention_policy` is idempotent. If a chunk has open references (it won't — pipeline_variants is append-only after the crawler row write), the policy logs and skips.
- **Hot-field promotion**: backfill is a single UPDATE in a transaction. If it fails mid-flight, rollback leaves the table unchanged.
- **CV externalization**: upload-to-R2 happens before the DB UPDATE. If R2 is down, the candidate's profile save is rejected — same failure semantics as the existing CV-upload flow. Existing CVs are migrated in a one-shot Go script that's idempotent on `cv_storage_uri IS NOT NULL`.

## Testing

- **Change 1**: integration test in `apps/crawler/service/` confirms that a rejected variant emits the NATS event AND no `pipeline_variants` row is written.
- **Change 2**: assert via `timescaledb_information.policies` that the three retention windows match. No data-level test needed.
- **Change 3**: integration test on `UpsertOpportunity` confirms the three columns persist from the Attributes map. Reader test on `/api/search?employment_type=full-time` confirms the WHERE clause uses the column not the JSON path (EXPLAIN ANALYZE assertion).
- **Change 4**: end-to-end test uploads a CV, verifies the R2 blob exists at the expected key, the row has `cv_storage_uri` set, and the matcher can still run against the candidate (matching uses the structured fields, not the raw text).

## Migration Path

1. Plan 1 (rejected pruning + retention) ships as v8.0.59. Single migration + small code change. Drops 131 MB of rejected rows immediately; caps `pipeline_variants` growth.
2. Plan 2 (hot-field promotion) ships as v8.0.60. Migration + writer change + reader change. Backwards-compatible — old rows have NULL in the new columns until the backfill UPDATE runs (which happens in the same migration).
3. Plan 3 (CV externalization) ships as v8.0.61. Has a one-shot migration script that uploads existing CV text before the DB columns are dropped. Run the script with `--dry-run` first against production to confirm row counts.

## Projected Steady-State Footprint (1 M opportunities + 100 k candidates)

| Layer | Pre-change | Post-change |
|---|---|---|
| `opportunities` heap | ~6 GB | ~6 GB (description retained) |
| `opportunities_bm25_idx` | ~12 GB | ~12 GB (description retained) |
| `opportunities_embedding_diskann_idx` | ~5 GB | unchanged |
| `pipeline_variants` (no retention, no pruning) | ~80 GB / 6 mo | <2 GB (7 d window, no rejected, no attempts) |
| `candidate_profiles` | ~2 GB | ~100 MB (CV + bio + work_history in R2) |
| **Total hot footprint** | **~100 GB** | **~25 GB** |

The win is concentrated in `pipeline_variants` (no retention + rejected dup) and `candidate_profiles` (CV blob). Opportunities heap stays the same intentionally — that's the price of strong matching.

## Out of Scope

- Description externalization (rejected: matching quality).
- Embedding model swap or vector dimension change.
- Search architecture changes (BM25 / DiskANN both stay).
- Tenancy column cleanup (`tenant_id`, `partition_id`, `access_id` are framework-level; not worth the risk to remove).
- ParadeDB upgrade.
- Frontend or API-shape changes.
