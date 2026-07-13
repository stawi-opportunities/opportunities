# Crawl pipeline

One durable processing boundary: **PostgreSQL**. Crawl never writes `opportunities` directly.

## Flow

```text
Trustage per-source schedule
  → POST /admin/sources/{id}/crawl  (backpressure admitter)
  → crawl.requests.v1
  → apps/crawler  (resumable crawl_runs slice)
       structured connector | recipe | schema.org
  → pkg/crawlaccept  (prepare → verify → normalize → pack)
  → job_ingest_queue
  → apps/worker  (claim, merge, ack)
  → opportunities + opportunity_sources
  → apps/api search / detail
```

Optional branch: source with `frontier_enabled` enqueues detail URLs; **apps/frontier-worker** fetches each URL and extracts schema.org JobPosting JSON-LD only (no AI stubs).

## What counts as a storable job

`pkg/crawlaccept` + `definitions/opportunity-kinds/job.yaml`:

| Field | Rule |
|-------|------|
| title | non-empty |
| description | ≥ 50 characters |
| issuing_entity | non-empty |
| apply_url | non-empty (never invent source base URL) |

Rejections are written to the append-only ingest events ledger; they do not enter the queue.

## Structured extract paths

| Path | Package / service |
|------|-------------------|
| JSON APIs | engine `api` + recipe (`pkg/recipe` / stock recipes) |
| ATS | engines `workday`, `smartrecruiters_api` |
| Sitemap + JobPosting JSON-LD | `pkg/connectors/sitemapcrawler` |
| HTML JSON-LD | engine `schema_org` (`pkg/connectors/structured`) |
| HTML list+detail | engine `generic_html` + recipe |
| Recipes | `pkg/recipe` + `recipeconn` when an active recipe exists |
| Spec YAML | R2 `definitions/connector/*` |

There is **no** universal AI connector and **no** crawl-time LLM enrichment of URL stubs.

## Backpressure

- `INGEST_MAX_PENDING` — hard outstanding queue ceiling (advisory-locked)
- `INGEST_MAX_OLDEST_AGE` — stop admitting when the oldest unfinished item is too old
- Frontier dequeue respects the same limits

When admission closes, a crawl run **yields** after the current page; the run watchdog resumes later. Ingest keys are idempotent.

## Processing (worker)

Workers claim with `FOR UPDATE SKIP LOCKED` and a bounded lease. One transaction:

1. Resolve `opportunity_identities.hard_key`
2. Upsert `opportunities`
3. Upsert `opportunity_sources` lineage
4. Refresh visibility (`hidden` if no active lineage)
5. Ack queue row + append event

Failures use bounded exponential retry, then `dead`.

## Full-crawl reconciliation

After the iterator exhausts, lineage not seen since the run started becomes inactive. An opportunity is hidden only when it has no active source.

## TimescaleDB

`job_ingest_events` is append-only (daily chunks, compression, retention). Mutable queue and serving tables are ordinary PostgreSQL.

Raw HTTP bodies are parsed in memory and discarded. Only queue payloads, canonical rows, lineage, and operational events persist.
