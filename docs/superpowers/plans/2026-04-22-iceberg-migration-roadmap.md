# Iceberg + R2-direct Architecture Reference

Date: 2026-04-22
Status: Current — reflects production design
Owner: Peter Bwire

---

## 1. System overview

Stawi Jobs uses a hybrid storage model:

- **Append-only event log (Iceberg on R2 + JDBC catalog):** 11 tables across `jobs` and `candidates` namespaces. Writers commit Parquet files to R2 and register each file via an Iceberg `AppendFiles` transaction. The JDBC catalog lives on the existing Postgres instance.

- **Canonical job body (R2-direct, no Iceberg):** Each canonical job is stored as a plain JSON file at `s3://opportunities-content/jobs/<slug>.json`. Translations are at `jobs/<slug>/<lang>.json`. These paths are the authoritative source for job detail pages and for KV rebuild — there is no Iceberg `jobs.canonicals` table.

- **Materializer (Frame subscriber):** The materializer subscribes to four NATS JetStream topics via Frame's handler registration. It does not scan Iceberg snapshots, does not hold a watermark, and does not participate in leader election. Consumer lag is observable via the NATS JetStream native metric `nats_jetstream_consumer_num_pending`.

- **Valkey KV cache:** `cluster:<id>` keys hold `kv.ClusterSnapshot` JSON for the hot-path dedup and serve the search API. These keys are rebuilt from the R2 canonical files by the worker's `/_admin/kv/rebuild` endpoint.

- **Search index (Manticore):** The materializer writes to `idx_opportunities_rt` (replace + update). The materializer's Frame subscriber handlers are the only write path to Manticore.

**Tables NOT in Iceberg:**

| Data | Storage |
|---|---|
| Canonical job body | `s3://opportunities-content/jobs/<slug>.json` |
| Job translations | `s3://opportunities-content/jobs/<slug>/<lang>.json` |
| Canonical expiry events | Frame event only — no persistent table |

---

## 2. Architecture diagram

```
        Frame pub/sub topics (NATS JetStream)
                │
          ┌─────┴──────────────────────────┐
          │                                │
          ▼                                ▼
   ┌───────────────────────────┐   ┌──────────────────────────────┐
   │ apps/writer               │   │ apps/materializer            │
   │  Parquet → R2             │   │  Frame subscriber (4 topics) │
   │  AppendFiles → catalog    │   │  → Manticore idx_opportunities_rt     │
   └─────┬──────────────┬──────┘   └──────────────────────────────┘
         │              │
         │              ▼
         │   ┌──────────────────────────┐
         │   │ JDBC catalog (Postgres)  │
         │   │   iceberg_namespaces     │
         │   │   iceberg_tables         │
         │   │   iceberg_history        │
         │   │   iceberg_manifests      │
         │   └──────────────────────────┘
         ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ R2 (Cloudflare)                                              │
   │                                                              │
   │  opportunities-content/                                         │
   │    jobs/<slug>.json          ← canonical body (authoritative)│
   │    jobs/<slug>/<lang>.json   ← translation body              │
   │                                                              │
   │  opportunities-log/                                             │
   │    <collection>/dt=…/        ← Parquet (Iceberg files)       │
   └───────────────────────┬──────────────────────────────────────┘
                           │
                    ┌──────┴──────┐
                    │             │
                    ▼             ▼
             ┌───────────┐  ┌────────────────────────┐
             │ nightly   │  │ apps/worker             │
             │ CronJob   │  │  /_admin/kv/rebuild     │
             │ pyiceberg │  │  lists jobs/*.json      │
             │ rewrite + │  │  → Valkey cluster:*     │
             │ expire    │  └────────────────────────┘
             └───────────┘
```

**Catalog** = JDBC-on-Postgres, schema added via `db/migrations/0004_iceberg_catalog.sql`. No new service, no new HA story.

**Writer** = adds one `iceberg-go` call after each successful R2 Put. If the commit fails, the writer nacks the Frame message and retries (same semantics as an R2 upload failure today). Topics `canonicals.upserted`, `canonicals.expired`, and `translations` are ACKed without Iceberg persistence — their data lives in R2 JSON files.

**Materializer** = four Frame handler registrations (`CanonicalUpserted`, `CanonicalExpired`, `Translation`, `Embedding`). No snapshot scan. No watermark. Consumer lag is `nats_jetstream_consumer_num_pending{consumer=~"materializer.*"}`.

**KV rebuild** = `/_admin/kv/rebuild` pages through `opportunities-content/jobs/*.json` (single-slash `.json` files only, skipping per-language translations), decodes `CanonicalUpsertedV1`, and writes `cluster:<id>` keys via Lua CAS. Concurrent 16-goroutine worker pool.

**Maintenance** = a single Python pod, fires once nightly, runs ~5 min:

```python
# apps/iceberg-ops/main.py — 11 tables (canonicals/translations/expired are R2-direct)
for tbl in [
    "jobs.variants", "jobs.embeddings", "jobs.published",
    "jobs.crawl_page_completed", "jobs.sources_discovered",
    "candidates.cv_uploaded", "candidates.cv_extracted",
    "candidates.cv_improved", "candidates.preferences",
    "candidates.embeddings", "candidates.matches_ready",
]:
    t = cat.load_table(tbl)
    t.rewrite_data_files(target_file_size_bytes=128 << 20)
    t.rewrite_manifests()
    t.expire_snapshots(older_than_days=14)
```

---

## 2.1 Memory-adaptive operation

All hot paths are O(batch) in memory, regardless of data scale, via
`pkg/memconfig`:

- **KV rebuild** (`apps/worker/service/kv_rebuild.go`): bounded map with
  Lua CAS flush. Budget = 30% of pod memory. Peak = budget / 2. At 10B
  canonicals / 32 buckets, a 4 GiB worker pod processes ~5 M cluster IDs
  per bounded-map cycle before flushing to Valkey.

- **StaleReader** (`pkg/candidatestore/stale_reader.go`): bounded map +
  heap retract. Budget = 20% of pod memory. Non-stale candidates discovered
  after an earlier stale flush are retracted from the result heap.

- **Writer buffer** (`apps/writer/service/buffer.go`): global cap across
  all open partition buffers (30% of pod memory). Oldest partition is
  force-flushed when total exceeds cap.

- **Compaction parallelism** (`apps/writer/service/compact.go`): adaptive.
  `maxConcurrent = (50% of pod memory) / 1.5 GiB`, minimum 1. A 1 GiB
  pod runs single-threaded; a 12 GiB pod runs up to 4 threads.

- **Materializer batch** (`apps/materializer/service/service.go`): adaptive.
  `batchSize = (10% of pod memory / 2) / 2 KiB`, capped [100, 5000].

The 30-second refresh cycle means pods can be vertically scaled in-place
and the system adapts without a restart.

---

## 4. Table design (11 Iceberg tables)

Canonical job body, translations, and expiry events are NOT in Iceberg — see §1.

### 4.1 Jobs namespace (5 tables)

| Table | Partition | Sort order after compaction |
|---|---|---|
| `jobs.variants` | `days(occurred_at), bucket(16, source_id)` | `posted_at` |
| `jobs.embeddings` | `bucket(16, canonical_id)` | — |
| `jobs.published` | `days(occurred_at)` | `published_at` |
| `jobs.crawl_page_completed` | `days(occurred_at), bucket(16, source_id)` | `source_id` |
| `jobs.sources_discovered` | `days(occurred_at)` | — |

### 4.2 Candidates namespace (6 tables)

| Table | Partition | Sort order |
|---|---|---|
| `candidates.cv_uploaded` | `days(occurred_at), bucket(16, candidate_id)` | `(candidate_id, cv_version)` |
| `candidates.cv_extracted` | `days(occurred_at), bucket(16, candidate_id)` | `(candidate_id, cv_version)` |
| `candidates.cv_improved` | `days(occurred_at), bucket(16, candidate_id)` | `(candidate_id, cv_version)` |
| `candidates.preferences` | `bucket(16, candidate_id)` | `candidate_id` |
| `candidates.embeddings` | `bucket(16, candidate_id)` | `candidate_id` |
| `candidates.matches_ready` | `days(occurred_at), bucket(16, candidate_id)` | `(candidate_id, match_batch_id)` |

### 4.3 Z-ordering

Applied during nightly compaction to the sort-key columns above. Pays off most on `candidates.cv_extracted` (candidate lookup). Skip for `embeddings` — vector columns don't sort meaningfully.

---

## 5. Tooling stack

| Piece | Pick | Notes |
|---|---|---|
| Go writer library | `github.com/apache/iceberg-go` | Pin to a version with write support. If current version's writes are shaky at spike time, fall back to Python sidecar |
| Python maintenance | `pyiceberg >= 0.8` | Runs as a single `CronJob`, 512Mi memory |
| Catalog backend | JDBC on existing `opportunities` Postgres | Four catalog tables via migration `0004_iceberg_catalog.sql` |
| Ad-hoc query | DuckDB with `iceberg` extension | Zero-infra analytics |
| Reader library (materializer, candidatestore, backfill) | `iceberg-go` | Same version as writer |

---

## 6. The single-phase plan

### 6.0 Prerequisites (day 0)

Applies before any code lands:
- Phase 6's Postgres cutover migration (`0003_cutover_drop_legacy.sql`) can either merge cleanly with the new iceberg migration or ship first. Pick one order; doesn't matter which.
- R2 bucket `opportunities-log` exists (same bucket Phase 6 targets).
- Vault path for `r2-log-credentials-opportunities` seeded.

### 6.1 One-week spike (week 0)

1. Stand up JDBC catalog against the dev Postgres.
2. Create the `jobs` + `candidates` namespaces and ONE table (`jobs.variants`) via a pyiceberg one-off.
3. Write a handful of rows via `iceberg-go` from a throwaway Go script; verify the catalog registers the files.
4. Run `rewrite_data_files` via pyiceberg; verify the compaction does what it claims.
5. Read from Go: `NewScan().PlanFiles()`.

Outcome = go/no-go one-pager. If `iceberg-go` writes are stable enough: proceed as Go-native. If not: the writer publishes a shadow event to a Python sidecar that commits on its behalf (extra pod but keeps the critical path boring).

### 6.2 Catalog + tables (week 1)

**Create:**
- `db/migrations/0004_iceberg_catalog.sql` — the 4 Iceberg catalog tables
- `definitions/iceberg/create_namespaces.py` — creates `jobs` and `candidates` namespaces
- `definitions/iceberg/create_tables.py` — creates all 11 tables listed in §4 with their partition specs, sort orders, and schemas derived from `pkg/events/v1/*.go`

**Deploy:**
- Apply `0004_iceberg_catalog.sql` via the existing migration runner
- Run `create_namespaces.py` + `create_tables.py` once (one-shot Job)

**Exit:** every table in the catalog, empty, ready to accept commits.

### 6.3 Writer with catalog commits (week 2)

**Modify `apps/writer/service/`:**
- Remove `compact.go`, `compact_admin.go`, and their tests. Trust git — they're preserved in `main` history.
- Add `iceberg_commit.go` with one helper:

```go
func (s *Service) commitToCatalog(ctx context.Context, table string, parquetKey string, etag string, rowCount int) error {
    t, err := s.catalog.LoadTable(ctx, catalog.Namespaces(table))
    if err != nil { return err }
    return t.NewAppend().
        AppendFile(iceberg.DataFile{
            Path: "s3://" + s.bucket + "/" + parquetKey,
            FileFormat: iceberg.FileFormatParquet,
            RecordCount: int64(rowCount),
        }).
        Commit(ctx)
}
```

- Extend `uploadBatch` in `service.go` to call `commitToCatalog` after the S3 Put succeeds. Remove any reference to `<collection>/dt=<dt>/` hand-built paths — Iceberg controls paths now.
- Delete the two Trustage triggers `compact-hourly.json` and `compact-daily.json`. They are never enabled.

**Modify `apps/writer/cmd/main.go`:**
- Wire a catalog client at startup. JDBC URL from env.
- Drop the compactor construction + mux routes for `/_admin/compact/*`.

**Build + test:**
- Unit tests for `commitToCatalog` with a catalog stub.
- Integration test (behind `//go:build integration`): real JDBC catalog + MinIO, write a batch, scan it back.

### 6.4 Maintenance CronJob (week 3)

**Create:**
- `apps/iceberg-ops/` — new tiny Python app with one main script
- `apps/iceberg-ops/Dockerfile` — `python:3.12-slim` + `pyiceberg[sql]` + `psycopg2`
- `apps/iceberg-ops/main.py` — the nightly rewrite + expire for the 11 append-only Iceberg tables
- `manifests/namespaces/opportunities/iceberg-ops/cronjob.yaml` — one CronJob at `0 2 * * *`
- `manifests/namespaces/opportunities/iceberg-ops/kustomization.yaml`
- Add `iceberg-ops/` to the top-level `opportunities/kustomization.yaml`

**Deploy:**
- Build the image in the existing release workflow (add `apps/iceberg-ops` to the matrix)
- Apply the manifest; first run happens at 02:00 UTC next day

### 6.5 Materializer (already done — Frame subscriber)

The materializer is already a Frame subscriber. No Iceberg scan, no watermark, no leader election. See §1 and `apps/materializer/service/service.go`.

Consumer lag monitoring: `nats_jetstream_consumer_num_pending{consumer=~"materializer.*"}` — see `definitions/openobserve/alerts/critical-materializer-lag-high.json`.

### 6.6 Reader refactor (week 4)

**Modify:**
- `pkg/candidatestore/reader.go` — scan `candidates.embeddings` + `candidates.preferences` via iceberg-go filters (latest-per-candidate using `ORDER BY occurred_at DESC LIMIT 1`)
- `pkg/candidatestore/stale_reader.go` — scan `candidates.cv_extracted` filtered by `occurred_at < cutoff`
- `apps/api/cmd/backfill_parquet.go` — scan `jobs.variants` or `jobs.published` for Hugo backfill; canonical detail comes from R2 JSON files directly

Method signatures stay the same. Callers don't change.

### 6.7 Cut v6.0.0 (week 4)

- Tag `v6.0.0` on the commit that removes the last compactor reference.
- Release workflow builds seven images (the existing six + `iceberg-ops`).
- Apply the deployments-repo manifest bumps.
- Cutover runbook (`docs/ops/cutover-runbook.md`) updated: "step 2.3 — nightly compact cron, not hourly compact cron." No other procedural changes; the data path is greenfield so the "drop tables + lift banner" sequence is identical.

**Decommissioning:**
- `apps/writer/service/compact.go` already deleted in §6.3.
- `pkg/repository/materializer_watermark.go` deleted in §6.5.
- Trustage triggers `compact-hourly.json` + `compact-daily.json` deleted.
- `db/migrations/0002_materializer_watermark.sql` marked superseded (left in place for historical apply order; new clusters skip it via a conditional check).

---

## 7. Risks + mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| `iceberg-go` write support flaky at release time | Writer crashloops in prod | Spike (§6.1) is the go/no-go gate. Python sidecar fallback if needed |
| Catalog is now a single point of failure for writes | Writer blocks on Postgres outage | Same HA posture as the existing Postgres instance. Add retry-with-backoff on the commit path; fail-open is NOT correct here — dropping the commit loses the file from the catalog |
| First nightly CronJob fails silently | Small-file accumulation, eventually degraded read perf | CronJob posts a success marker to Prometheus; alert if no success in 36 hours |
| Schema evolution surprise: Phase 6 payload struct added a field; Iceberg table didn't know | Writer commit fails with schema mismatch | Include the Iceberg table DDL in the same review as any payload struct change. Add a CI check: `pyiceberg table schema` matches the Go struct's parquet tags |
| pyiceberg MERGE support immature | `_current` tables don't get rebuilt | If MERGE isn't available at the pyiceberg version in use, fall back to "delete all, insert latest" inside a single transaction — atomic via Iceberg's snapshot isolation |
| R2 object lifecycle mismatch | Orphaned files after `expire_snapshots` | `expire_snapshots` retains files referenced by retained snapshots. Set retention to 14 days initially; tune down once stable |

---

## 8. Cost model

| Resource | Phase 6 | v6.0.0 | Delta |
|---|---|---|---|
| Writer pod | 1× 500m/1.5Gi | 1× 500m/1.5Gi | 0 |
| Compactor code (in writer) | hourly ticks | deleted | **−** |
| Maintenance CronJob | — | nightly 200m/512Mi for ~5 min | +negligible |
| Materializer pod | 1× 200m/512Mi | 1× 200m/512Mi | 0 |
| Trustage triggers (compact) | 2 | 0 | **−2** |
| R2 storage | Parquet | Parquet + ~0.1% Iceberg metadata | +negligible |
| Postgres size | 4 tables (sources, candidate_profiles, crawl_jobs, raw_payloads) | + 4 catalog tables | +few MB |
| Custom Go LOC | ~2000 LOC compactor/watermark/bucket-prefix | ~800 LOC Iceberg wrappers | **−~1200 LOC** |
| Python LOC | 0 | ~200 LOC in apps/iceberg-ops | +200 |

Net: infrastructure flat, operational surface down, code complexity down.

---

## 9. What the spike must prove

The week-0 spike (§6.1) is not optional. It answers:

1. **Writes:** Does `iceberg-go` commit files to a JDBC catalog reliably under concurrent writers? Target: 100 concurrent AppendFiles commits, zero metadata corruption.
2. **Maintenance:** Does `pyiceberg` rewrite_data_files produce the expected sorted layout against real R2 (with S3-compatible endpoint config)?
3. **Read:** Can iceberg-go `NewScan().PlanFiles()` efficiently return rows for the candidatestore / backfill readers?

If all four are green, proceed with §6.2 onward. If any are red, the mitigation path is known: Python sidecar for writes, simpler maintenance query shape, defer z-order to a follow-up.

---

## 10. References

- Phase 6 plan (the one this replaces): `docs/superpowers/plans/2026-04-22-phase6-cutover-ops.md`
- Design spec: `docs/superpowers/specs/2026-04-21-parquet-manticore-greenfield-design.md`
- Iceberg v2 spec: https://iceberg.apache.org/spec/
- apache/iceberg-go: https://github.com/apache/iceberg-go
- pyiceberg: https://py.iceberg.apache.org/

---

## 11. Call to action

1. Schedule the week-0 spike. One engineer, one week.
2. At spike end, produce a go/no-go memo.
3. If go: write the executable task-by-task plan as `docs/superpowers/plans/<date>-v6-iceberg-cutover.md` (like the Phase 6 plan shape). Four weeks of work across the 19 existing task slots is approximately 1-to-1 replacement.
4. If no-go: document blockers. Phase 6's direct-Parquet path is stable and can carry prod for a quarter while upstream matures.
