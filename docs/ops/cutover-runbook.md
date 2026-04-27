# Phase 5 → 6 Cutover Runbook (historical)

> **HISTORICAL DOCUMENT** — This runbook describes the one-time Phase 5→6 migration
> that dropped the legacy Postgres read path. That migration is complete.
> For new deploys or DR recovery, use `docs/ops/first-deploy-runbook.md` instead.

**Owner:** Platform / Peter Bwire
**Audience:** Historical reference only
**Completed:** 2026-04-17 (commit fa1562b)

## 0. Prerequisites

- [ ] Phase 6 plan committed to main and CI is green.
- [ ] Managed infra:
  - [ ] Manticore cluster at $MANTICORE_URL
  - [ ] Valkey at $VALKEY_URL
  - [ ] R2 bucket cluster-chronicle with writer credentials
  - [ ] TEI chat / embed / rerank endpoints answer /health
- [ ] Iceberg catalog migration applied: `psql "$DATABASE_URL" -f db/migrations/0004_iceberg_catalog.sql`
- [ ] Iceberg namespaces + tables bootstrapped:
  - `python3 definitions/iceberg/create_namespaces.py`
  - `python3 definitions/iceberg/create_tables.py`
- [ ] Vault secret seeded at `antinvestor/opportunities/common/iceberg-catalog` (ICEBERG_CATALOG_URI, warehouse, R2 credentials).
- [ ] Trustage workflows deployed: scheduler-tick, writer-compact, writer-expire-snapshots, sources-quality-window-reset, sources-health-decay, candidates-matches-weekly-digest, candidates-cv-stale-nudge.
- [ ] Backpressure gate env set: BACKPRESSURE_MAX_DRAIN=15m, BACKPRESSURE_HARD_CEILING=45m.
- [ ] idx_opportunities_rt schema provisioned (Phase 2 migration). Verify: curl -s $MANTICORE_URL/sql?mode=raw -d "query=SHOW TABLES"
- [ ] All six app images built + pushed at the Phase 6 SHA.

## 1. Staging smoke (T-1 day)

- [ ] Deploy Phase 6 images to staging.
- [ ] Seed 3 live sources into sources.
- [ ] Fire scheduler-tick manually: kubectl -n stage exec deploy/trustage -- trustage run opportunities.scheduler.tick
- [ ] Wait 60s: assert a variant event landed in R2 under variants/dt=<today>/.
- [ ] Wait 60s: assert a canonical landed in canonicals/dt=<today>/.
- [ ] GET /api/v2/search?q=engineer returns non-empty hits.
- [ ] GET /api/v2/jobs/<slug> returns 200 on a seeded hit.
- [ ] Any step fails → STOP, investigate, do not proceed.

## 2. Prod cutover

### 2.1 Pre-flight (15 min)

- [ ] Post #stawi-ops: "Starting greenfield cutover in 15 min".
- [ ] Take manual backup: pg_dump --schema-only --no-owner $DATABASE_URL > /tmp/pre-cutover.sql

### 2.2 Deploy the Phase 6 apps (30 min)

- [ ] kubectl apply Phase 6 image tags for all six apps.
- [ ] Each pod reaches Ready.
- [ ] Watch writer logs: expect "parquet flushed" within 2 min.
- [ ] /healthz on apps/api returns ok.

### 2.3 Trigger initial scheduler + compact cycle

- [ ] curl -XPOST $CRAWLER_URL/admin/scheduler/tick
- [ ] Wait 5 min. Run manual compact:
  - curl -XPOST $WRITER_URL/_admin/compact
- [ ] Run expire-snapshots if needed:
  - curl -XPOST $WRITER_URL/_admin/expire-snapshots
- [ ] Apply the new Trustage triggers:
  - trustage deploy definitions/trustage/writer-compact.json
  - trustage deploy definitions/trustage/writer-expire-snapshots.json

### 2.4 Fill checkpoint (1–3 hours)

Wait for idx_opportunities_rt row count to cross launch threshold (default 50k):
- [ ] Poll: curl -s $API_URL/healthz | jq .total_jobs
- [ ] Tail writer flushes: kubectl logs -f -l app=writer | grep "parquet flushed"
- [ ] Tail materializer upserts: kubectl logs -f -l app=materializer | grep "manticore upsert"
- Every 30 minutes: post progress to #stawi-ops.

### 2.5 Flip the site to v2 endpoints

The api binary mounts /api/v2/* and legacy /api/* shim paths route to v2. No additional flip needed — the Phase 6 deploy in Step 2.2 already made them live.
- [ ] Exercise: search, category page, detail page, country filter, remote filter — all return data.

### 2.6 Drop legacy Postgres tables (POINT OF NO RETURN)

- [ ] psql "$DATABASE_URL" -f db/migrations/0003_cutover_drop_legacy.sql
- [ ] Re-run the migration to verify idempotency.

### 2.7 Close out

- [ ] Post #stawi-ops: "Cutover complete. Monitoring for 24h."
- [ ] Schedule a 24h post-mortem.

## 3. Rollback (before 2.6 only)

1. kubectl rollout undo deployment/<app> -n prod on each of the six apps.
2. Verify legacy /api/search returns data.
3. Post-mortem.

After 2.6: restore /tmp/pre-cutover.sql and revert to SHA 46a71bb.

## 4. Post-cut verification (24h)

- [ ] tests/k6/smoke_post_cut.js passes against prod.
- [ ] Manticore query p95 <100ms.
- [ ] Writer ack-lag p95 <30s.
- [ ] Materializer poll-lag p95 <30s.
- [ ] No unresolved alerts.

## 5. Resource scaling

All opportunities services adapt their in-memory working set to the pod's
actual cgroup memory limit at runtime via `pkg/memconfig`. There are no
hardcoded memory ceilings in the application code.

**Operator rules:**
- `requests.memory` = minimum for reliable startup. Do not lower.
- `limits.memory` = advisory ceiling. The system shrinks batch sizes
  automatically when the limit is reduced — it never OOMs.
- To speed up a service: raise `limits.memory`. Batch sizes and
  parallelism grow automatically within 30 s (next cgroup re-read).
- To constrain a service: lower `limits.memory`. The service works
  more slowly but never crashes.

**What each subsystem uses:**

| Service     | Subsystem        | Budget | Notes                              |
|-------------|------------------|--------|------------------------------------|
| writer      | writer-buffer    | 30%    | global cap across all partitions   |
| writer      | compact          | 50%    | parallelism = budget / 1.5 GiB     |
| worker      | kv-rebuild       | 30%    | bounded map + Lua CAS flush        |
| candidates  | stale-reader     | 20%    | bounded map + heap retract         |
| materializer| materializer-bulk| 10%    | Manticore NDJSON batch size        |

See `manifests/namespaces/opportunities/common/RESOURCE_SCALING.md` for
the full per-service breakdown.
