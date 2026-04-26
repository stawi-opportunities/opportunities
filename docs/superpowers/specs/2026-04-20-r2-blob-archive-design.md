# R2 Blob Archive with Co-located Cluster Bundles

**Status:** Design approved, plan pending.
**Date:** 2026-04-20.

## Goal

Move every large blob out of Postgres (raw HTTP bodies, variant HTML/markdown,
canonical metadata snapshots) into a private R2 bucket organised by
`cluster_id`. Keep Postgres focused on metadata, FTS, and pointers. Retention
is tied to the canonical job's lifecycle — we keep blobs as long as the job is
live, only delete when the canonical flips to `status='deleted'`.

This enables a secondary and equally important goal: **decouple crawling from
AI processing**. With raw bodies in R2 addressed by content hash, the crawler
can race ahead independently; extraction workers stall cleanly when the AI
backend is down and resume when it recovers, without any pressure on the
crawler or the database.

## Motivation

Postgres is genuinely bad at large-blob storage: TOAST overhead, vacuum pain,
3× replication cost on primary + 2 hot standbys, backup/restore windows that
scale linearly with blob volume. At millions of jobs, the variant HTML alone
would push the working set off SSD into disk cache churn.

R2 at $0.015/GB/month beats primary-storage Postgres ~20× on cost and scales
horizontally with zero operator involvement. Content-hashed raw bodies give us
free deduplication across re-crawls of unchanged pages.

The pipeline-decoupling property is arguably bigger than the cost savings.
Today, if the LLM extractor stalls, the crawler's writes still land in
Postgres as large `bytea` columns that bloat the working set and pressure the
primary. With raw bodies in R2, the crawler's only database write is a tiny
metadata row; a multi-hour extraction outage results in queue-depth growth
(cheap) rather than storage growth (expensive and operationally dangerous).

## Architecture

### Before

```
Crawler → Postgres
  • raw_payloads.body (bytea)
  • job_variants.raw_html / clean_html / markdown (text)
  • canonical_jobs.description (text) — search-indexed
  • canonical_jobs.embedding (text)
```

### After

```
Crawler → R2 (bucket: opportunities-archive, private) + Postgres (metadata only)

R2 layout:
  raw/{sha256}.html.gz                          ← immutable, shared across clusters
  clusters/{cluster_id}/canonical.json          ← current canonical metadata snapshot
  clusters/{cluster_id}/manifest.json           ← mutable index: variants + hashes
  clusters/{cluster_id}/variants/{variant_id}.json
                                                ← { source_url, clean_html, markdown, raw_sha256 }

Postgres:
  • raw_payloads: metadata only (hash, uri, status, size, fetched_at)
  • job_variants: metadata only (ids, titles, timestamps, raw_content_hash)
  • canonical_jobs: metadata + description (kept for FTS) + embedding
```

### One place for all data about one job

The entire story of a canonical job — its current metadata, every variant it
was built from, every raw HTML blob that fed those variants — lives under
`clusters/{cluster_id}/`. For quality checks or debugging:

```
aws s3 sync s3://opportunities-archive/clusters/{cluster_id}/ ./debug/
```

pulls everything in one command. Raw HTML is fetched by hash from `raw/` as
needed; the manifest tells you which hashes to pull.

## Data formats

### R2 object: `raw/{sha256}.html.gz`

Raw HTTP response body, gzip-compressed. Content-addressable — the key IS the
sha256 of the decompressed body. Immutable. Shared: two canonicals that ingest
the same source URL from the same source will share the same raw object.

Metadata headers (for observability):
- `x-amz-meta-http-status`: original upstream status
- `x-amz-meta-fetched-at`: ISO 8601 timestamp of fetch
- `x-amz-meta-source-id`: xid of source that fetched it (first observer)
- `content-encoding`: `gzip`

### R2 object: `clusters/{cluster_id}/canonical.json`

Current canonical metadata. Rewritten on every canonical upsert. Mirrors the
DB row but is authoritative R2-side. Format:

```json
{
  "id": "cr7qs3q8j1hci9fn3sag",
  "cluster_id": "cr7qs3q8j1hci9fn3sag",
  "slug": "senior-go-developer-at-acme-corp-a3f7b2",
  "title": "Senior Go Developer",
  "company": "Acme Corp",
  "description": "We are hiring…",
  "country": "NG",
  "language": "en",
  "remote_type": "remote",
  "employment_type": "full_time",
  "salary_min": 80000,
  "salary_max": 120000,
  "currency": "USD",
  "apply_url": "https://acme.com/careers/senior-go",
  "quality_score": 85,
  "posted_at": "2026-04-15T10:00:00Z",
  "first_seen_at": "2026-04-15T11:00:00Z",
  "last_seen_at": "2026-04-20T09:15:00Z",
  "status": "active",
  "category": "programming",
  "r2_version": 7
}
```

### R2 object: `clusters/{cluster_id}/variants/{variant_id}.json`

One file per variant, written when the variant finishes its normalize stage.
Rewritten when normalized fields change.

```json
{
  "id": "cr7qr2q8j1hci9fn3sbg",
  "cluster_id": "cr7qs3q8j1hci9fn3sag",
  "source_id": "cr0001k8j1hci9fn3000",
  "source_url": "https://acme.com/careers/1234",
  "apply_url": "https://acme.com/apply/1234",
  "raw_sha256": "a3f7b2c4…",
  "clean_html": "<article>…</article>",
  "markdown": "# Senior Go Developer\n\n…",
  "extracted_fields": {
    "title": "Senior Go Developer",
    "company": "Acme Corp",
    "skills": ["Go", "PostgreSQL"]
  },
  "scraped_at": "2026-04-15T11:00:00Z",
  "stage": "normalized"
}
```

### R2 object: `clusters/{cluster_id}/manifest.json`

Mutable index. Rewritten on every canonical or variant write. Tells you what's
in the cluster directory without enumerating S3 (which is paginated and slow
for debugging).

```json
{
  "cluster_id": "cr7qs3q8j1hci9fn3sag",
  "canonical_id": "cr7qs3q8j1hci9fn3sag",
  "slug": "senior-go-developer-at-acme-corp-a3f7b2",
  "variants": [
    {
      "variant_id": "cr7qr2q8j1hci9fn3sbg",
      "source_id": "cr0001k8j1hci9fn3000",
      "raw_sha256": "a3f7b2c4…",
      "scraped_at": "2026-04-15T11:00:00Z"
    },
    {
      "variant_id": "cr7qr5q8j1hci9fn3tcg",
      "source_id": "cr0002k8j1hci9fn3001",
      "raw_sha256": "d9e2a8f1…",
      "scraped_at": "2026-04-15T13:30:00Z"
    }
  ],
  "updated_at": "2026-04-20T09:15:00Z"
}
```

## Database schema changes

### `raw_payloads`

Drop:
- `body bytea`

Keep:
- `content_hash varchar(64)` — join key into R2 `raw/{hash}.html.gz`
- `storage_uri text` — full R2 URL (redundant with hash; kept as cache/convenience)
- `http_status int`
- `fetched_at timestamptz`
- `crawl_job_id varchar(20)`

Add:
- `size_bytes bigint` — decompressed body size, useful for quota/cost tracking

### `job_variants`

Drop:
- `raw_html text`
- `clean_html text`
- `markdown text`

Add:
- `raw_content_hash varchar(64)` — points at `raw/{hash}.html.gz`

Keep all other columns (ids, titles, extracted fields, stage, timestamps).

### `canonical_jobs`

`description` stays in DB because it feeds the `search_vector` GENERATED
column. It's bounded (a few KB per row) and compresses well with Postgres's
own TOAST for the long tail. `embedding` stays too — small, queried every
match cycle, no win to offloading.

Add two columns to support purge bookkeeping:

- `deleted_at timestamptz` — stamped when status flips to 'deleted'.
- `r2_purged_at timestamptz` — stamped by the purge sweeper after R2 teardown.

### New table: `raw_refs`

Reference count for deduplication so we can safely GC `raw/{hash}` objects.

```sql
CREATE TABLE raw_refs (
  id                varchar(20) PRIMARY KEY,
  content_hash      varchar(64) NOT NULL,
  cluster_id        varchar(20) NOT NULL,
  variant_id        varchar(20) NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE(content_hash, variant_id)
);

CREATE INDEX idx_raw_refs_hash ON raw_refs(content_hash);
CREATE INDEX idx_raw_refs_cluster ON raw_refs(cluster_id);
```

A row exists for every (raw_sha256, variant) pairing. When a cluster is
purged, we delete its `raw_refs` rows, then GC any `content_hash` whose row
count drops to zero.

## R2 client & configuration

New package: `pkg/archive/` wraps an R2 client with a single-purpose API.

```go
type Archive interface {
    // Raw operations (content-addressed, deduped).
    PutRaw(ctx context.Context, body []byte) (hash string, size int64, err error)
    GetRaw(ctx context.Context, hash string) ([]byte, error)
    HasRaw(ctx context.Context, hash string) (bool, error)

    // Cluster bundle operations.
    PutCanonical(ctx context.Context, clusterID string, snap CanonicalSnapshot) error
    PutVariant(ctx context.Context, clusterID, variantID string, v VariantBlob) error
    PutManifest(ctx context.Context, clusterID string, m Manifest) error

    GetCanonical(ctx context.Context, clusterID string) (CanonicalSnapshot, error)
    GetVariant(ctx context.Context, clusterID, variantID string) (VariantBlob, error)
    GetManifest(ctx context.Context, clusterID string) (Manifest, error)

    // Cluster teardown (for canonical.status='deleted').
    DeleteCluster(ctx context.Context, clusterID string) error
}
```

Existing `pkg/publish/R2Publisher` already wraps R2 for public snapshots; this
is a parallel client for the private archive bucket. They can share the
underlying `R2Config` struct but MUST use separate bucket names and separate
access credentials (least privilege — archive writer never touches the public
bucket and vice versa).

## Write paths

### Crawler (new flow)

```
1. fetch(url) → body + status
2. hash = sha256(body)
3. if !archive.HasRaw(hash):
     archive.PutRaw(body)       ← deduped write; skipped for unchanged pages
4. rawPayloadRepo.Create(&RawPayload{
     CrawlJobID: job.ID,
     ContentHash: hash,
     StorageURI: "raw/" + hash + ".html.gz",
     HTTPStatus: status,
     SizeBytes: len(body),
     FetchedAt: now,
   })
5. emit variant.raw.stored event { variant_id, source_id, content_hash }
```

Crawler never writes `body` to Postgres. The event carries the content hash
so the downstream extractor can pull from R2.

### Normalize handler (variant.deduped → variant.normalized)

```
1. Load variant by ID from Postgres.
2. raw, _ := archive.GetRaw(ctx, variant.RawContentHash)
3. extract HTML / markdown / fields from raw.
4. archive.PutVariant(ctx, variant.ClusterID, variant.ID, VariantBlob{
     CleanHTML: clean,
     Markdown: md,
     ExtractedFields: fields,
     RawSha256: variant.RawContentHash,
     ...
   })
5. jobRepo.UpdateVariantFields(ctx, variant.ID, map[string]any{
     "stage": "normalized",
     // no more raw_html / clean_html / markdown columns
   })
6. archive.PutManifest(ctx, variant.ClusterID, …)  ← rewrite manifest
```

### Canonical handler (variant.validated → job.ready)

```
1. Upsert canonical in Postgres (existing path, unchanged).
2. archive.PutCanonical(ctx, canonical.ClusterID, snapshot(canonical))
3. archive.PutManifest(ctx, canonical.ClusterID, …)
4. rawRefsRepo.Upsert(rawRef{hash: variant.RawContentHash, cluster_id, variant_id})
5. emit job.ready { canonical_job_id }
```

### Publish handler (unchanged)

The existing publish flow writes `jobs/{slug}.json` to the **public** bucket
and is untouched. Archive bucket and public bucket are separate — public is
the user-visible snapshot, archive is the internal audit/reprocessing store.

## Read paths

### Reprocessing (e.g. re-run extractor on all variants)

```
1. jobRepo.ListByStage(ctx, "deduped", batchSize)
2. for each variant: raw = archive.GetRaw(variant.RawContentHash)
3. re-extract, update variant JSON on R2 + fields in DB.
```

Reprocessing is R2-GET-heavy but GETs are cheap and cacheable in Valkey for
the working set.

### Quality debugging

Support engineer hits a confusing canonical job. They grab the cluster_id
from the admin UI, then:

```
aws s3 sync s3://opportunities-archive/clusters/cr7qs3q8j1hci9fn3sag/ ./debug/
cat ./debug/canonical.json          # current state
cat ./debug/manifest.json            # what variants exist
cat ./debug/variants/{id}.json       # how each variant looked pre-canonical
aws s3 cp s3://opportunities-archive/raw/{hash}.html.gz -
                                     # pull the raw HTML for any variant
```

All on their laptop, no DB access needed.

## Decoupling & backpressure

### Before

- Crawler writes large `bytea` to Postgres as part of its main loop.
- If extractor is down: crawler continues, raw bytes pile up in `raw_payloads`,
  primary disk pressure rises, vacuum stalls, entire DB degrades.
- Backpressure gate limits new crawl dispatches by checking queue depth, but
  once dispatched a crawl still writes full body to DB.

### After

- Crawler's DB write is a tiny metadata row (few hundred bytes). Body goes to
  R2 (effectively infinite).
- If extractor is down: NATS queue grows. R2 keeps receiving new blobs
  without pressure on any Postgres replica.
- Backpressure gate now watches **processing queue depth** (pending NATS
  messages), not DB state. Gate trips → crawler pauses → R2 stops receiving
  new writes. Existing queue drains naturally. Resume when pending drops
  below low-water.

### Backpressure gate behaviour (unchanged interface, refined semantics)

```
Check(ctx) →
  state.Pending = stream_pending_msgs(EventVariantRawStored)
  state.Paused = state.Pending >= state.HighWater
  // resume below low-water
```

This is what the gate already does today; the refinement is that it now
*works correctly* because DB pressure is no longer the coupling mechanism —
NATS queue depth is the only signal we need.

## Retention & purge

### Core policy

Blobs live as long as the canonical job is live. Deletion is
**lifecycle-driven, not time-driven.**

- `canonical_jobs.status = 'active'` → all blobs retained, no action.
- `canonical_jobs.status = 'expired'` → all blobs retained (job might
  re-activate if it re-appears in a later crawl).
- `canonical_jobs.status = 'deleted'` → mark for purge.

### Cold-tier policy

R2 Infrequent Access lifecycle rule on the bucket:

- `clusters/*` objects with no writes for 30 days → move to IA storage class
  (~50% cost reduction, higher read latency but still milliseconds).
- `raw/*` objects with no writes for 30 days → same.

Rewrites reset the cold-tier clock automatically. An active canonical that
gets re-crawled stays warm. A canonical that ages out without update moves to
cold, but is still instantly readable if someone opens it.

**No time-based deletion rule.** The bucket never deletes on its own.

### Purge sweeper (status='deleted' → physical purge)

New cron job in the crawler or candidates service (unified with existing
retention sweeps). Uses two new columns on `canonical_jobs`:

- `deleted_at timestamptz` — set when status flips to 'deleted' (distinct
  from GORM's soft-delete column which we don't use on this table).
- `r2_purged_at timestamptz` — set after R2 teardown completes.

```
1. SELECT cluster_id, id FROM canonical_jobs
   WHERE status = 'deleted'
     AND deleted_at < now() - interval '7 days'
     AND r2_purged_at IS NULL
   LIMIT 100;
2. for each:
     a. SELECT content_hash FROM raw_refs WHERE cluster_id = $id
     b. DELETE FROM raw_refs WHERE cluster_id = $id
     c. for each hash:
          SELECT COUNT(*) FROM raw_refs WHERE content_hash = $hash
          if 0: archive.DeleteObject(raw/{hash}.html.gz)
     d. archive.DeleteCluster(cluster_id)  → deletes clusters/{id}/*
     e. UPDATE canonical_jobs SET r2_purged_at = now() WHERE id = $id
```

The 7-day grace window gives ops a chance to reverse a mistaken delete
before blobs are physically gone. Adjustable via env. `r2_purged_at`
prevents double-purging if the sweeper is restarted mid-batch.

## Crash safety & consistency

Two failure modes matter. Neither is catastrophic; both are handled.

**Case 1: R2 write succeeds, DB commit fails.** Orphan blob in R2 with no
DB reference. The `raw_refs` ref count for its hash never increments, so the
purge sweeper's "ref count == 0" check eventually GCs it. For cluster bundles,
a nightly reconciliation job scans `clusters/*` and deletes any directory
whose `cluster_id` isn't in `canonical_jobs`. Low priority — orphans cost
pennies until GC'd.

**Case 2: DB commit succeeds, R2 write fails.** DB thinks a blob exists but
R2 doesn't have it. This is the worse case because it breaks reprocessing.
Mitigation: archive writes happen *before* the DB write within each handler
(fail fast — if R2 PUT fails, abort the transaction, let NATS redeliver).
Since NATS delivery is at-least-once and our handlers are already idempotent,
retries converge to a consistent state.

The `make archive-verify` QA script (see Testing) flags any drift that
slipped past the write-order discipline.

## Concurrency

Multiple variants for the same cluster can arrive in parallel (two crawlers
fetch the same job from different sources simultaneously). Writes to
`clusters/{cluster_id}/variants/{variant_id}.json` are naturally
cluster-unique by variant_id — no conflict. Writes to
`clusters/{cluster_id}/manifest.json` and `clusters/{cluster_id}/canonical.json`
DO conflict; two handlers could race and the last-writer-wins could miss a
variant.

Mitigation: manifest writes happen inside the same handler that writes the
variant, and the handler holds a row-level lock on the canonical_job row via
`SELECT ... FOR UPDATE` during the manifest rebuild. Since the manifest is
derived from `raw_refs + canonical_jobs + job_variants` in DB, rebuild is a
simple query and the lock serialises concurrent writers.

For the canonical.json write, the existing `UpsertCanonical` already uses
`ON CONFLICT (cluster_id) DO UPDATE` which serialises at the DB. Wrapping
the R2 PUT in the same transaction keeps them consistent.

## Testing strategy

### Unit tests

- `pkg/archive/`: mock R2 client (interface-driven), test key format,
  content-hash computation, error paths (R2 timeout, not-found, conflict).
- `raw_refs` repository: ref counting math, GC candidate selection.
- Purge sweeper: idempotency, grace window handling.

### Integration tests

- Use testcontainers `minio` (S3-compatible) for end-to-end archive tests.
- Crawl → dedup flow with R2 round-trip.
- Reprocess flow: stored variant.json + raw.html.gz → re-extract → updated variant.json.

### QA script

Write a `make archive-verify` target that spot-checks 50 random canonical
jobs by sampling their cluster directory and asserting:
1. `canonical.json` exists and matches DB row
2. Every variant in `manifest.json` has a `variants/{id}.json` file
3. Every `raw_sha256` in variants has a `raw/{hash}.html.gz`
4. Every hash in `raw_refs` has a live R2 object

Run this post-deploy and nightly. Flags drift between Postgres and R2 early.

## Migration (DB-wipe context)

Because the user plans to wipe the DB and restart, no data migration is
required. The new schema is the only schema a fresh cluster sees. What needs
to happen in order:

1. Provision the new R2 bucket `opportunities-archive` with two access keys
   (reader + writer), separate credentials from the existing public
   `job-repo` bucket.
2. Provision Vault paths for the new credentials.
3. Deploy new image with archive-aware pipeline.
4. Fresh crawl populates both DB metadata and R2 blobs from the first run.

No dual-write, no backfill, no coexistence mode. The DB wipe makes this a
greenfield cutover.

## Non-goals

Explicitly NOT changing in this spec:

- The public R2 bucket (`job-repo.stawi.org`) and `jobs/{slug}.json` contract.
  Those are user-facing and untouched.
- The canonical job's public slug format or URL structure.
- The candidate-matching / reranker / embedding pipeline.
- The Hugo shell / UI — they consume the public bucket, which is unchanged.
- Search: `canonical_jobs.description` stays in DB so `search_vector` remains
  authoritative.
- Redirect service integration (`/r/{slug}` tracking).

## Open questions

None remaining. All choices locked:

- ✅ Separate private bucket `opportunities-archive`
- ✅ Content-addressed raw (`raw/{sha256}.html.gz`)
- ✅ Co-located cluster bundles (`clusters/{cluster_id}/`)
- ✅ Mutable manifest (not append-only)
- ✅ `canonical_jobs.description` stays in DB (search_vector dependency)
- ✅ Retention tied to `canonical_jobs.status`, not time
- ✅ Cold tier after 30 days of inactivity (cost-only, no behaviour change)
- ✅ No auto-delete policy
- ✅ Purge sweeper with 7-day grace window on `status='deleted'`
- ✅ `raw_refs` reference counting for dedup-safe GC
- ✅ Backpressure now gated on NATS queue depth, not DB state
