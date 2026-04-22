# Iceberg Migration Roadmap

Date: 2026-04-22
Status: Proposal — awaits reading + redirect
Owner: Peter Bwire
Target start: after Phase 6 stabilises (≥2 weeks green in prod)
Estimated end: +4 phases, ~6 months part-time

---

## 1. Executive summary

Phase 6 shipped a direct-to-R2 Parquet event log with custom Go compactor, custom materializer watermarks, and custom `*_current/` snapshot rebuilding. All of this is table-stakes work for a data lake — none of it is differentiation. Apache Iceberg replaces the lot with a catalog, `MERGE INTO` SQL, and scheduled `rewrite_data_files()` maintenance.

This roadmap cuts over both the jobs pipeline (crawler + worker + materializer) and the candidate CV lifecycle to Iceberg over four phases. Each phase leaves the system in a green, deployable state. Phase 6 code is deleted incrementally, not all at once.

**North star after migration:**
- Writers land Parquet files to R2 and commit snapshots to a JDBC catalog hosted on the existing Postgres. One additional commit per flush. No compactor pod, no watermark table, no bucket-prefix logic.
- Maintenance is one `pyiceberg` CronJob per table, run nightly. Replaces `apps/writer/service/compact*.go` entirely.
- Materializer reads snapshot diffs (`scan.useRef(prevSnapshot).planFiles()`) — no R2 polling, no dedup logic, no partition-prefix tracking.
- External consumers (firehose, agent-economy products) read the same tables via pyiceberg/DuckDB/Trino. Zero custom SDK.
- `candidates_cv_current/`, `canonicals_current/`, etc. stop existing — those are queries against the latest snapshot.

**What does NOT change:**
- R2 is still the storage. Iceberg is a metadata+catalog layer on top of the same Parquet files you already write.
- Manticore is still the search index. Materializer still upserts into it.
- Valkey still holds dedup + rerank cache.
- Postgres still holds sources, candidate identity, and now the Iceberg catalog tables.
- Frame pub/sub still transports events.

---

## 2. Current state (Phase 6 outcome)

Writer path (apps/writer/service):
- Receives every `*.v1` event from Frame pub/sub.
- Buffers per `(topic, partition-secondary)` in-memory.
- Flushes to R2 at `{10k events | 64 MB | 30 s}` → `<collection>/dt=YYYY-MM-DD/<secondary>/<xid>.parquet`.
- Ack after S3 ETag.

Compactor path (apps/writer/service/compact*.go):
- Hourly: merge small files, dedup by `event_id`, earliest `occurred_at` wins.
- Daily: rebuild `<collection>_current/` from the last snapshot per business key.

Materializer path (apps/materializer):
- Polls R2 every 15 s.
- Tracks watermark per partition in Valkey/Postgres.
- Reads new Parquet files, upserts to Manticore via `/replace` + `/update`.

Reader path (pkg/candidatestore, apps/api/cmd/backfill_parquet.go):
- Lists `candidates_cv_current/cnd=<prefix>/` → filters → returns latest per candidate.
- Walks `canonicals_current/cc=<prefix>/` → emits Hugo snapshots.

Current Postgres residue: `sources`, `candidate_profiles` (trimmed), `crawl_jobs`, `raw_payloads`, `subscriptions`, `entitlements`.

---

## 3. Why Iceberg — problems it solves

| Phase 6 pain | Iceberg replacement |
|---|---|
| 500 LOC custom compactor with per-collection dispatch + xid file naming | `CALL system.rewrite_data_files('<table>', options => map('target-file-size-bytes', '134217728'))` |
| `*_current/` rebuild is a whole file-listing pass + hashmap fold + bucket layout | `MERGE INTO target USING source ON key WHEN MATCHED UPDATE … WHEN NOT MATCHED INSERT …` |
| Envelope widening (Phase 0 / Task 0) required touching 17 structs + encoder switch | `ALTER TABLE jobs.variants ADD COLUMN event_id string` — writers that don't know about the column just write NULL |
| Materializer watermark table is a per-partition KV, concurrency-managed manually | Snapshot IDs are monotonic commit timestamps; `scan.useRef(snapshotId).planFiles()` is a client-library one-liner |
| R2 small-file problem solved by custom hourly compactor | Maintenance is a ~5-min nightly pyiceberg CronJob |
| External consumers need custom "read R2 bucket prefix, dedup by event_id, parse Parquet" code | `pyiceberg.catalog.load_catalog().load_table('jobs.variants').scan().to_duckdb('vr')` |
| Time-travel debugging requires grepping R2 paths | `SELECT … FROM jobs.canonicals FOR VERSION AS OF <snapshot_id>` |
| Schema drift between writer and reader goes undetected until a Parquet read panic | Iceberg catalog validates schema on every commit |

**What Iceberg does NOT solve:**
- Throughput bottleneck — throughput is Manticore-bound for search, not storage-bound.
- Realtime freshness — still limited by writer flush interval + materializer cadence.
- Cost — same Parquet bytes on R2.

---

## 4. Target architecture

### 4.1 Topology

```
        Frame pub/sub topics
                │
                ▼
   ┌───────────────────────────┐
   │ apps/writer               │
   │  - Parquet file → R2      │
   │  - AppendFiles → catalog  │  (one commit per flush, ~every 30 s)
   └─────┬──────────────┬──────┘
         │              │
         │              ▼
         │   ┌──────────────────────────┐
         │   │ JDBC catalog (Postgres)  │
         │   │  iceberg_namespace       │
         │   │  iceberg_tables          │
         │   │  iceberg_history         │
         │   │  iceberg_manifests       │
         │   └───────┬──────────┬───────┘
         ▼           │          │
   ┌───────────────┐ │          │
   │ R2: Parquet   │ │          │
   │  same layout  │ │          │
   │  as today     │ │          │
   └───────────────┘ │          │
                     ▼          ▼
              ┌──────────────┐  ┌──────────────────────┐
              │ maintenance  │  │ readers:             │
              │  CronJob     │  │  - apps/materializer │
              │  pyiceberg   │  │  - pkg/candidatestore│
              │  rewrite +   │  │  - apps/api backfill │
              │  expire      │  │  - external firehose │
              └──────────────┘  └──────────────────────┘
```

### 4.2 Catalog — JDBC on existing Postgres

The JDBC catalog is literally four tables in the existing `stawi_jobs` database. No new pod, no new HA story.

```
iceberg_namespaces
iceberg_tables
iceberg_table_history
iceberg_manifest_lists
```

Creation: one-time `pyiceberg catalog --create jdbc://…` on deploy. Migrations tracked via the existing `db/migrations/` directory.

Why JDBC over REST catalogs:
- No additional service to run, scale, or back up.
- Commits are Postgres transactions. ACID for free.
- The existing `stawi_jobs` DB is already HA'd via the cluster's Postgres operator.
- Go client + pyiceberg both speak JDBC catalog natively.
- REST catalog (Polaris, Nessie) is valuable when many independent teams need their own catalog slice; not needed here.

If a future scale-out requires a shared catalog across multiple products (agent economy), swap JDBC → REST in one config change. The table format on R2 doesn't move.

### 4.3 Writer

The writer continues to:
1. Buffer events in memory by `(topic, partition-secondary)`.
2. Flush Parquet file to R2 at thresholds.
3. Ack the Frame pub/sub message only after success.

One thing changes:
4. After the S3 ETag returns, issue an `AppendFiles` commit to the catalog. This records the new Parquet file in the table's current snapshot.

`AppendFiles` is lightweight — one Postgres `INSERT INTO iceberg_manifest_lists` per commit. Latency overhead: single-digit ms.

If the commit fails (catalog unreachable, retryable error), the writer treats it the same as an R2 upload failure: nack, keep buffered events, redeliver.

### 4.4 Compaction = scheduled maintenance

No compactor pod. Replaced by one CronJob per table (or per-namespace sweep):

```yaml
# manifests/namespaces/stawi-jobs/iceberg-maintenance/cronjob.yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: iceberg-nightly-maintenance
spec:
  schedule: "0 2 * * *"  # 02:00 UTC
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: pyiceberg
              image: ghcr.io/antinvestor/stawi-jobs-iceberg-ops:v1.0.0
              command: ["python", "-m", "stawi_iceberg_ops.nightly"]
              resources:
                requests: {cpu: "200m", memory: "512Mi"}
                limits:   {cpu: "1000m", memory: "2Gi"}
```

Inside the Python script:

```python
from pyiceberg.catalog import load_catalog
cat = load_catalog("stawi", type="sql", uri="postgresql://…")
for table in cat.list_tables("jobs") + cat.list_tables("candidates"):
    t = cat.load_table(table)
    t.rewrite_data_files(target_file_size_bytes=128 * 1024 * 1024,
                         sort_order=DEFAULT_SORT_FOR[table.name])
    t.rewrite_manifests()
    t.expire_snapshots(older_than_days=14)
```

Runs in ~5 min. Replaces ~700 LOC of Phase 6 Go compactor code.

### 4.5 Materializer

Becomes thinner — Iceberg's snapshot model does the bookkeeping:

```go
cat := iceberg.LoadCatalog(ctx, jdbcURL)
tbl, _ := cat.LoadTable(ctx, []string{"jobs", "canonicals"})

prev := readWatermarkSnapshotID(ctx)  // from Valkey or Postgres
current := tbl.CurrentSnapshot().SnapshotID()

newFiles, _ := tbl.NewScan().
    UseRef(current).
    FromSnapshotExclusive(prev).
    PlanFiles(ctx)

for _, f := range newFiles {
    rows, _ := f.Read(ctx)
    for _, r := range rows {
        manticoreClient.Replace(ctx, "idx_jobs_rt", r.ID, r.Doc)
    }
}

writeWatermarkSnapshotID(ctx, current)
```

No partition-prefix tracking. No small-file proliferation handling. No dedup — the writer's Iceberg commits are idempotent at the snapshot level.

### 4.6 Readers — the big simplification

Phase 6's `pkg/candidatestore/reader.go` and `stale_reader.go` each walk R2 prefix listings, read Parquet, fold by business key. After migration:

```go
// pkg/candidatestore/reader.go — post-migration
func (r *Reader) LatestByCandidate(ctx context.Context, candidateID string) (*CVState, error) {
    scan := r.tbl.NewScan().
        Filter(iceberg.Equal("candidate_id", candidateID)).
        Select("cv_extract_state")
    rows, _ := scan.ToArrow(ctx)
    return rows.Latest("occurred_at"), nil
}
```

`MERGE INTO` handles the "latest wins" semantics; the reader is just a filter + limit.

---

## 5. Table design

### 5.1 Jobs namespace

| Iceberg table | Replaces | Partition spec | Sort key (z-order after compaction) |
|---|---|---|---|
| `jobs.variants` | `variants/` | `days(occurred_at), bucket(16, source_id)` | `posted_at` |
| `jobs.canonicals` | `canonicals/` + `canonicals_current/` via MERGE | `days(occurred_at), bucket(16, cluster_id)` | `(quality_score DESC, posted_at DESC)` |
| `jobs.canonicals_expired` | `canonicals_expired/` | `days(occurred_at)` | `expired_at` |
| `jobs.embeddings` | `embeddings/` + `embeddings_current/` via MERGE | `bucket(16, canonical_id)` | — (vectors don't sort) |
| `jobs.translations` | `translations/` + `translations_current/` via MERGE | `days(occurred_at), bucket(8, lang)` | `canonical_id` |
| `jobs.published` | `published/` | `days(occurred_at)` | `published_at` |
| `jobs.crawl_page_completed` | `crawl_page_completed/` | `days(occurred_at), bucket(16, source_id)` | `source_id` |
| `jobs.sources_discovered` | `sources_discovered/` | `days(occurred_at)` | — |

### 5.2 Candidates namespace

| Iceberg table | Replaces | Partition spec | Sort key |
|---|---|---|---|
| `candidates.cv_uploaded` | `candidates_cv/` (upload events) | `days(occurred_at), bucket(16, candidate_id)` | `(candidate_id, cv_version)` |
| `candidates.cv_extracted` | `candidates_cv/` (extract events) + `candidates_cv_current/` via MERGE | `days(occurred_at), bucket(16, candidate_id)` | `(candidate_id, cv_version)` |
| `candidates.cv_improved` | `candidates_improvements/` | `days(occurred_at), bucket(16, candidate_id)` | `(candidate_id, cv_version)` |
| `candidates.preferences` | `candidates_preferences/` + `candidates_preferences_current/` | `bucket(16, candidate_id)` | `candidate_id` |
| `candidates.embeddings` | `candidates_embeddings/` + `candidates_embeddings_current/` | `bucket(16, candidate_id)` | `candidate_id` |
| `candidates.matches_ready` | `candidates_matches_ready/` | `days(occurred_at), bucket(16, candidate_id)` | `(candidate_id, match_batch_id)` |

### 5.3 Why partition + bucket vs just partition

- `days(occurred_at)` prunes 99% of files for "latest N days" queries (most consumer queries).
- `bucket(16, key)` gives evenly-sized files within a day without requiring hash prefixes in directory names.
- Together they replace the Phase 6 `cc=<prefix>/cnd=<prefix>` directory scheme with a deterministic transformation.

Z-ordering applies during nightly compaction — the sort key column is re-sorted across file boundaries so range-scans on `posted_at` or `quality_score` only read the files in that range. For the match endpoint's "give me all canonicals with `quality_score >= 60` sorted by `posted_at`" — this is an order-of-magnitude fewer file reads.

### 5.4 `*_current/` is now a query, not a table

Example: "latest CV per candidate."

Before (Phase 6):
```
List R2 under candidates_cv_current/cnd=<prefix>/
Read every Parquet file
Filter to candidate_id
Return
```

After:
```sql
-- Stored in pyiceberg nightly MERGE, run as part of maintenance:
MERGE INTO candidates.cv_current AS t
USING (
    SELECT candidate_id, max_by(cv_version, occurred_at) AS cv_version, …
    FROM candidates.cv_extracted
    GROUP BY candidate_id
) AS s
ON t.candidate_id = s.candidate_id
WHEN MATCHED THEN UPDATE SET …
WHEN NOT MATCHED THEN INSERT …;
```

`candidates.cv_current` is still an Iceberg table — but it's REGENERATED atomically nightly by the maintenance job. Writers never touch it; only the maintenance job does. Readers query it normally.

For "fresher than nightly" reads, the reader runs the SAME query on the append-only `candidates.cv_extracted` table with `ORDER BY occurred_at DESC LIMIT 1`. Iceberg manifest pruning + sort-order metadata makes this fast.

---

## 6. Tooling stack

| Piece | Pick | Rationale |
|---|---|---|
| Catalog library | `github.com/apache/iceberg-go` for Go services; `pyiceberg` for maintenance | Go for hot path, Python for scheduled batch |
| Catalog backend | JDBC on existing Postgres | No new pods |
| Writer client | `iceberg-go` v0.6+ (writes stable by end of 2026) | Keep writer in Go |
| Maintenance client | `pyiceberg >= 0.8` with `sqlalchemy` driver | Mature rewrite/expire APIs |
| Ad-hoc query | DuckDB with `iceberg` extension | Zero-infra analytics |
| External firehose query | Trino-on-demand or pyiceberg | When agent economy products need it |
| Schema migrations | `db/migrations/*.sql` for catalog tables; iceberg table creation via pyiceberg scripts in `definitions/iceberg/` | Same pattern as existing |

**If `iceberg-go` write support isn't production-ready at migration time:** dual-write via a tiny Python sidecar that subscribes to the same Frame pub/sub topic and commits to Iceberg while the Go writer also writes direct Parquet. This is the safety-valve pattern — never block the primary write path on the experimental dependency.

---

## 7. Migration roadmap

Each phase ships independently and leaves green CI, runnable code, and deployable manifests.

### Phase 7 — Foundation + first table (weeks 1–3)

**Scope:**
- Install pyiceberg + iceberg-go in the repo; pick exact versions.
- Add JDBC catalog tables via `db/migrations/0004_iceberg_catalog.sql`.
- Create namespace `jobs` + table `jobs.canonicals` via a one-off pyiceberg script.
- Writer learns to commit: for the `TopicCanonicalsUpserted` topic only, after the Parquet upload, `cat.LoadTable("jobs.canonicals").AppendFiles(...)` the just-uploaded file.
- Dual-write proof: Parquet goes to the existing R2 path AND is registered in Iceberg. Run both paths in parallel for 1 week.
- Verify: `pyiceberg table scan jobs.canonicals --limit 10` returns data. `select * from jobs.canonicals` via DuckDB returns data. The existing compactor still works against the direct-Parquet path.

**Exit criteria:**
- Writer emits Iceberg commits for `canonicals` with 100% parity vs direct-Parquet over 1000+ events.
- Iceberg nightly CronJob runs `rewrite_data_files` on `jobs.canonicals` successfully against MinIO in CI.
- No regression in Phase 6 materializer / reader paths.

**Deferred to later phases:**
- Other jobs tables (variants, embeddings, translations, etc.).
- Candidates tables.
- Materializer reading from Iceberg.
- Decommissioning the compactor.

### Phase 8 — Jobs path cutover (weeks 4–9)

**Scope:**
- Create the remaining seven jobs tables (variants, canonicals_expired, embeddings, translations, published, crawl_page_completed, sources_discovered).
- Extend the writer to commit to each on flush — one `AppendFiles` per Parquet file.
- Refactor `apps/materializer` to read from Iceberg: `LoadTable → NewScan → planFiles`. Keep the Manticore upsert unchanged. Dual-read for 2 weeks (materializer reads from BOTH direct-Parquet and Iceberg, verifies Manticore row parity).
- Move apps/api's `/admin/backfill` (Hugo publish) to read from `jobs.canonicals` via iceberg-go. Delete `apps/api/cmd/backfill_parquet.go`; replace with `backfill_iceberg.go`.
- Nightly maintenance now runs against all eight jobs tables.

**Exit criteria:**
- Materializer reading exclusively from Iceberg for ≥2 weeks with zero parity deltas.
- Nightly maintenance green for ≥2 weeks.
- Manticore row count + checksum matches the previous direct-Parquet run.
- The Phase 6 compactor pod is IDLE (receives zero work) for ≥1 week.

**Decommissioning in this phase:**
- Stop writing direct-Parquet for jobs collections. Writer writes ONLY to Iceberg.
- Delete `apps/writer/service/compact*.go`.
- Delete the Trustage triggers `compact-hourly.json` and `compact-daily.json` (replaced by the single nightly CronJob).
- Delete `canonicals_current/`, `embeddings_current/`, `translations_current/` from R2 — they're gone-for-good; the Iceberg `_current` tables replace them.

### Phase 9 — Candidates path cutover (weeks 10–14)

**Scope:**
- Create the six candidates tables.
- Writer commits candidates events to Iceberg (was already writing Parquet; now ALSO commits).
- Refactor `pkg/candidatestore/reader.go` and `stale_reader.go` to use iceberg-go.
- Refactor the match endpoint's "latest embedding + preferences per candidate" to `candidates.embeddings` + `candidates.preferences` Iceberg tables.
- Rewrite `apps/candidates/service/admin/v1/stale_lister.go` against `candidates.cv_extracted` Iceberg table.

**Exit criteria:**
- Match endpoint returns identical top-K for 100+ candidates run against both paths.
- Stale-nudge admin returns the same candidate set.
- All candidates tests still pass (these mostly use fakes; small impact).

**Decommissioning:**
- Delete `candidates_cv_current/`, `candidates_embeddings_current/`, `candidates_preferences_current/` from R2.
- Delete Phase 6 `pkg/candidatestore/reader.go` + `stale_reader.go`; replace with Iceberg-backed versions (same interface).

### Phase 10 — Deprecate Phase 6 infrastructure (weeks 15–16)

**Scope:**
- Delete the `compact-hourly.json` + `compact-daily.json` + `retention-reconcile.json` + related Trustage triggers (already inactive).
- Delete `apps/writer/service/compact.go`, `compact_admin.go`, `compact_test.go`, `compact_admin_test.go`.
- Delete the materializer watermark table (`materializer_watermarks`) and the `pkg/repository/materializer_watermark.go` adapter — Iceberg snapshots are the watermark.
- Remove unused env vars and config flags.
- Update docs/ops runbooks to reflect Iceberg.

**Exit criteria:**
- Phase 6 compactor no longer builds into any image.
- All removed code has zero remaining callers.
- Docs + runbooks describe the Iceberg path.

### Phase 11 — External reader surface (optional, when needed)

**Scope:**
- Expose a pyiceberg-backed firehose: Trustage-triggered Python Job that exports new commits to external consumers. Implements the design spec §Agent-economy firehose product.
- Publish a public-facing DuckDB view for analytics consumers.
- Document the external-consumer contract (table names, schemas, SLA).

**Only build when there's a real external consumer ready to onboard.**

---

## 8. Risk + rollback per phase

| Phase | Biggest risk | Mitigation | Rollback path |
|---|---|---|---|
| 7 | iceberg-go write support immaturity | Dual-write direct-Parquet + Iceberg for full phase; never block on the new dep | Disable Iceberg commits via env flag |
| 8 | Materializer parity gap (missing rows in Manticore) | Dual-read for 2 weeks + checksum verification | Revert materializer to direct-Parquet scan; keep writer dual-writing |
| 9 | Candidate match endpoint regression | Shadow-read: both paths execute, compare top-K, serve from old until parity ≥99.5% | Disable Iceberg reader via env flag per candidate service |
| 10 | Deleting compactor breaks something invisible | Keep the code in git for 30 days; Phase 10 is the last point of true decommissioning | `git revert` the deletion commit |
| 11 | External consumer discovers schema drift | Publish a fixed v1 schema contract; version future changes | Iceberg schema evolution supports additive changes without reader disruption |

---

## 9. Cost model

### Steady-state Phase 10 vs Phase 6

| Resource | Phase 6 | Post-Iceberg | Delta |
|---|---|---|---|
| Writer pod | 1× 500m/1.5Gi | 1× 500m/1.5Gi | 0 |
| Compactor pod (as part of writer) | hourly tick within writer | deleted | **−** |
| Maintenance CronJob | — | nightly 200m/512Mi for ~5min | +negligible (≤0.3 CPU-hr/day) |
| Materializer pod | 1× 200m/512Mi polling | 1× 200m/512Mi snapshot-diff | 0 |
| R2 storage | same Parquet files | same Parquet + ~0.1% metadata | +negligible |
| Postgres | sources + candidates | sources + candidates + iceberg_* | +few MB for the catalog tables |
| Custom Go code | ~2000 LOC in compactor/reader/watermark | ~300 LOC in iceberg-backed wrappers | **−** |
| Operational surface | 3 Trustage triggers (compact-hourly/daily + retention) | 1 CronJob | **−** |

**Net: resource usage roughly flat to slightly lower; code surface drops ~1500 LOC; operational surface drops from 3 cron-style jobs to 1.**

### What the cost model doesn't capture

- Engineering time for the migration itself: ~6 months part-time, one engineer.
- Risk of the dual-write/dual-read periods — production stability > migration speed.
- If you bring in Spark (don't, unless external analytics products demand it), the cost story changes dramatically.

---

## 10. Open questions

**10.1 iceberg-go write stability timeline.** Writes in `apache/iceberg-go` went stable in v0.6 (late 2026 per upstream). Verify against current version before committing to Go-only writes. Fallback: Python sidecar.

**10.2 Catalog HA.** JDBC-on-Postgres inherits the Postgres operator's HA story. Test a Postgres failover scenario end-to-end — confirm writer retries the commit cleanly. Document the recovery path.

**10.3 Vector embedding storage.** Iceberg + Parquet handle float32 list columns fine, but `bge-m3` is 1024-dim × 10M canonicals = ~40 GB of embeddings. Acceptable on R2; verify Iceberg manifest planning doesn't slow on wide vector columns.

**10.4 Firehose schema contract.** When Phase 11 lands, what's the external schema? Iceberg lets you `ALTER TABLE` freely, but external consumers want a stable contract. Pin a `v1` schema; breaking changes go to `v2` tables.

**10.5 Multi-region R2.** If/when multi-region lands (design §12.7), does Iceberg change anything? Iceberg supports S3-compatible multi-region via the catalog referencing full paths. No architectural change needed; operational care around R2 cross-region replication still applies.

**10.6 Migration test harness.** Phase 7-9 all have "dual-write then cut over" patterns. Build a shared Go+pyiceberg test harness in `tests/iceberg_parity/` that diffs the two data paths. Reusable across phases.

---

## 11. Decommission checklist (end of Phase 10)

- [ ] `apps/writer/service/compact.go` deleted
- [ ] `apps/writer/service/compact_admin.go` deleted
- [ ] `apps/writer/service/compact_test.go` deleted
- [ ] `apps/writer/service/compact_admin_test.go` deleted
- [ ] `definitions/trustage/compact-hourly.json` deleted
- [ ] `definitions/trustage/compact-daily.json` deleted
- [ ] `pkg/repository/materializer_watermark.go` deleted
- [ ] `db/migrations/0002_materializer_watermark.sql` marked superseded (kept for historical apply; new deploys use iceberg_watermark)
- [ ] `apps/materializer/service/service.go` snapshot-based
- [ ] `pkg/candidatestore/reader.go` iceberg-backed
- [ ] `pkg/candidatestore/stale_reader.go` iceberg-backed
- [ ] `apps/candidates/service/admin/v1/stale_lister.go` iceberg-backed
- [ ] `apps/api/cmd/backfill_parquet.go` deleted; replaced by `backfill_iceberg.go`
- [ ] All `*_current/` R2 prefixes removed
- [ ] Runbook updated: `docs/ops/runbook-iceberg-maintenance.md`
- [ ] Runbook updated: `docs/ops/cutover-runbook.md` notes Iceberg as the data plane
- [ ] Nightly maintenance CronJob green for ≥4 weeks

---

## 12. Next step

Before executing Phase 7, a one-week spike:

1. Stand up a JDBC catalog against the dev Postgres.
2. Load one day of Phase 6 Parquet files as an existing-data registration (not a copy — Iceberg registers files in-place).
3. Run a pyiceberg `rewrite_data_files` pass and verify file counts drop + sort order applies.
4. Run a materializer-style scan from Go: `iceberg-go` `NewScan().PlanFiles()`.
5. Confirm `iceberg-go` is stable enough for writes against the v0.x available at spike time. If yes: Phase 7 proceeds as-is. If no: Phase 7 uses Python sidecar for writes.

At the end of the spike, write a one-page go/no-go memo. If go: write Phase 7's task-by-task plan at `docs/superpowers/plans/<date>-phase7-iceberg-foundation.md`. If no-go: document blockers and revisit in a quarter.

---

## References

- Phase 6 plan: `docs/superpowers/plans/2026-04-22-phase6-cutover-ops.md`
- Design spec: `docs/superpowers/specs/2026-04-21-parquet-manticore-greenfield-design.md`
- Iceberg spec v2: https://iceberg.apache.org/spec/
- apache/iceberg-go: https://github.com/apache/iceberg-go
- pyiceberg: https://py.iceberg.apache.org/
