# Resumable Crawling + URL Frontier + Adaptive Recrawl

**Date:** 2026-05-28
**Status:** Approved direction (extends `2026-05-28-pluggable-crawler-and-admin-trace.md`)

## Problem

The system today crawls sources at a fixed per-source interval (`sources.crawl_interval_sec`, default 21600 = 6 hours) and treats each `crawl.request` as a one-shot transaction. If the crawler pod restarts mid-iterator, the connector starts from page 1 again — partial progress is lost. If a job is newly posted, the crawler doesn't notice for up to 6 hours. If a job expires, nobody removes it from search.

The user's research crystallised the gap: this is fine for a small fixed-cadence crawler. It's NOT fine for a "job intelligence ingestion engine" — the actual product is freshness, completeness, and cross-source deduplication, and those need:

1. **Resumable crawls** — checkpoint after every iterator page so a restart picks up exactly where the previous run left off.
2. **URL frontier** — per-URL priority queue, not just per-source fixed cadence. Each detail page has its own `next_crawl_at` based on freshness + validThrough + change frequency.
3. **Adaptive recrawl scoring** — `priority = source_reliability + recent_job_boost + near_expiry_boost + changed_recently_boost − unchanged_penalty − rate_limit_penalty`.
4. **Canonical jobs vs source postings** — one job = one canonical, observed at N sources. Today `opportunities` IS the canonical and `pipeline_variants` carries the per-source provenance, but the dedup keys + the lifecycle (job goes inactive when ALL postings 404) need explicit modelling.
5. **JSON-LD JobPosting** as the primary structured-data target — already covered by the new `schemaorgjsonld` connector in Plan B; this spec formalises the field mapping into the data model.
6. **Three-tier source classification** for crawl cadence.

This spec is the architecture; concrete plans land separately.

## Solution

Five orthogonal capabilities, each independently shippable:

| Cap | What | Net effect |
|---|---|---|
| **D1. Iterator checkpoints** | After every successful iterator page, persist `(source_id, cursor, page_idx)` into a `crawl_checkpoints` row keyed by source_id. On crawl.request, look up + resume from the checkpoint. | Restarts don't waste work; rate-limited sources can be paused and resumed without re-fetching |
| **D2. URL frontier** | New `source_urls` hypertable holding per-URL state: `(source_id, url, url_type, last_crawled_at, next_crawl_at, etag, last_modified, content_hash, priority, status_code)`. Crawler claims URLs via SKIP LOCKED instead of "crawl this source's listing now". | Per-URL freshness; ETag + content-hash short-circuit avoids re-extraction; priority queue scales naturally |
| **D3. Adaptive recrawl scoring** | A `pkg/freshness` package that computes `next_crawl_at` based on job age, `validThrough`, change frequency, source reliability, user-alert relevance. Applied at URL frontier admit time + every crawl outcome. | Newly-posted jobs crawled every 10-30 min; stable jobs crawled less; expiry-soon jobs crawled more |
| **D4. Canonical/posting split** | Promote the existing `opportunities` table from "single canonical row" to "canonical + posting_count". Move per-source provenance from `pipeline_variants` (operational) to a durable `job_postings` table (one row per `(canonical_id, source_id, posting_url)`). Each posting has its own freshness/status. A canonical is "inactive" when ALL its postings are inactive. | Explicit cross-source dedup; remove a job when it disappears from all sources; "this job appears on N boards" is one query |
| **D5. JSON-LD-first extraction** | The crawler's per-page extraction path prefers JSON-LD `JobPosting` blocks over LLM extraction. Falls back to the existing LLM path only when no JSON-LD is present. Field mapping is canonical (`datePosted` → `posted_at`, `validThrough` → `expires_at`, `hiringOrganization.name` → `issuing_entity`). | Faster (no LLM call for JSON-LD pages), more deterministic, matches Google's source-of-truth |

Together these turn the existing crawler from "fixed-interval polite fetcher" into "URL-frontier priority-driven freshness engine" without rewriting the connector layer (the existing 13 Go connectors + the 6 spec-driven connectors from Plan B continue to operate; they just consume URLs from the frontier and emit checkpoints).

## Architecture

```
                                            ┌─────────────────┐
                                            │ Source Registry │
                                            │ (existing)      │
                                            └────────┬────────┘
                                                     │
                                  ┌──────────────────┼──────────────────┐
                                  │                  │                  │
                       Discovery workers      URL frontier       Adaptive recrawl
                       ───────────────────    ─────────────      ─────────────────
                       Google CSE queries     source_urls        pkg/freshness
                       Sitemap discovery      (per-URL state)    score(url) →
                       ATS subdomain probe                       next_crawl_at
                                  │                  │                  │
                                  └────────┬─────────┴────────┬─────────┘
                                           ▼                  ▼
                                  ┌───────────────────────────────────┐
                                  │ Crawl workers (existing crawler)  │
                                  │ - Claim URLs (FOR UPDATE SKIP     │
                                  │   LOCKED) from source_urls        │
                                  │ - Read iterator checkpoint        │
                                  │ - Honour ETag / Last-Modified     │
                                  │ - Persist checkpoint per page     │
                                  │ - Update source_urls.next_crawl_at│
                                  │   from freshness score on outcome │
                                  └────────────────────┬──────────────┘
                                                       ▼
                                  ┌───────────────────────────────────┐
                                  │ Extraction (existing)             │
                                  │ ─ JSON-LD JobPosting FIRST        │
                                  │ ─ Connector-specific second       │
                                  │ ─ LLM extractor last (fallback)   │
                                  └────────────────────┬──────────────┘
                                                       ▼
                                  ┌───────────────────────────────────┐
                                  │ Pipeline (existing)               │
                                  │ ingested → normalized → validated │
                                  │  → clustered → canonical →        │
                                  │  published                        │
                                  └────────────────────┬──────────────┘
                                                       ▼
                                ┌─────────────────────────────────────┐
                                │ Postgres                            │
                                │  jobs       (canonical, NEW)        │
                                │  job_postings  (per-source, NEW)    │
                                │  opportunities (rename → jobs)      │
                                │  source_urls   (URL frontier, NEW)  │
                                │  crawl_checkpoints (NEW)            │
                                └─────────────────────────────────────┘
```

Existing tables that stay (some renamed):

- `sources` — unchanged. The registry.
- `crawl_jobs`, `raw_payloads` — unchanged. Audit ledger.
- `pipeline_variants` — unchanged. Operational pipeline state.
- `opportunities` — **renamed to `jobs`** (cleaner naming now that we model "posting" explicitly). Migration is a view-aliased rename.

## Decisions Locked

These are NOT open for re-litigation in plan-writing:

1. **JSON-LD JobPosting is the primary extraction path.** Connector-specific extraction is second; LLM is last.
2. **One canonical = one job_id in the `jobs` table.** Each job has 1..N `job_postings` rows. Each posting has its own freshness state.
3. **Recrawl is per-URL, not per-source.** Sources still have `crawl_interval_sec` as a discovery hint (how often to re-walk the listing pages), but individual detail-page recrawl decisions live in `source_urls.next_crawl_at`.
4. **Adaptive scoring is a pure function in `pkg/freshness`.** Inputs: row state. No external calls. Output: a duration to add to `last_crawled_at`.
5. **Checkpoints survive pod restart.** Stored in Postgres, not memory.
6. **Inactive detection** — a posting flips to `inactive` when:
   - The page returns 404/410, OR
   - JSON-LD `validThrough` is in the past, OR
   - The connector indicates "no longer accepting", OR
   - The canonical URL redirects to a generic jobs page (a redirect that ISN'T the original detail URL).
   A canonical job flips to `inactive` only when ALL its postings are inactive.

## Components

### D1. Iterator Checkpoints

**New table:**

```sql
CREATE TABLE crawl_checkpoints (
    source_id        VARCHAR(20)  NOT NULL,
    connector_type   TEXT         NOT NULL,
    cursor           JSONB        NOT NULL DEFAULT '{}'::jsonb,
    page_idx         INTEGER      NOT NULL DEFAULT 0,
    last_url         TEXT,
    last_checkpoint_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, connector_type)
);
```

The connector iterator surfaces `Checkpoint() *Checkpoint` after each page. The crawl handler persists it via `crawl_checkpoints` UPSERT. On crawl.request start, the handler reads the checkpoint and passes it to `connector.CrawlResume(ctx, src, checkpoint)`.

Existing connectors (the 13 Go ones + 6 spec-driven from Plan B) implement `CrawlResume` — most can do this trivially by accepting the cursor in their constructor. Sitemap + JSON-LD connectors checkpoint after every URL processed.

### D2. URL Frontier

**New hypertable:**

```sql
CREATE TABLE source_urls (
    id              VARCHAR(20)  NOT NULL,
    discovered_at   TIMESTAMPTZ  NOT NULL,
    source_id       VARCHAR(20)  NOT NULL,
    url             TEXT         NOT NULL,
    url_type        TEXT         NOT NULL,  -- 'listing' | 'detail' | 'sitemap' | 'feed'
    priority        INTEGER      NOT NULL DEFAULT 0,
    last_crawled_at TIMESTAMPTZ,
    next_crawl_at   TIMESTAMPTZ  NOT NULL,
    etag            TEXT,
    last_modified   TIMESTAMPTZ,
    content_hash    VARCHAR(64),
    last_status     INTEGER,
    consecutive_404 INTEGER      NOT NULL DEFAULT 0,
    canonical_id    VARCHAR(20),  -- back-link to jobs row when known
    PRIMARY KEY (id, discovered_at)
);

SELECT create_hypertable('source_urls', 'discovered_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '7 days');

CREATE INDEX source_urls_due_idx
    ON source_urls (next_crawl_at, priority DESC)
    WHERE last_status IS NULL OR last_status < 400 OR consecutive_404 < 3;

CREATE UNIQUE INDEX source_urls_unique_url
    ON source_urls (source_id, url);

-- Retention: 90 d on URLs not re-seen.
SELECT add_retention_policy('source_urls', INTERVAL '90 days', if_not_exists => TRUE);
```

A new scheduler tick (replacing the current `/admin/scheduler/tick`) claims due URLs via `FOR UPDATE SKIP LOCKED LIMIT N`, dispatches them as `crawl.requests.v1` events with the URL pre-resolved, and the crawler fetches that single URL directly.

### D3. Adaptive Recrawl Scoring

New package `pkg/freshness`:

```go
// Score returns the recommended interval-to-next-crawl for a URL,
// based on its state. Pure function — no I/O. Inputs come from the
// source_urls row + the latest known JobPosting JSON-LD (if any).
//
// Range: 5 min (highly-changing detail page near validThrough) to
//        7 days (stable URL never-changes).
//
// Formula (illustrative):
//
//   base = source_reliability_to_interval(source.health_score, source.tier)
//   if validThrough within 6h:    base *= 0.1
//   if posted_at within 24h:      base *= 0.3
//   if content_hash unchanged on last N crawls: base *= 1.5
//   if status_code == 404 on last 3 crawls:     return 30 days (decay slowly)
//   if url_type == listing:        base *= 0.7  (listings move faster than details)
//
// Caller stores: now + Score(...) → source_urls.next_crawl_at.
func Score(state URLState) time.Duration
```

Pure-function design means it's trivially testable: table-driven tests over `(input, expected interval)` pairs.

### D4. Canonical/Posting Split

**Migration plan:**

1. Rename `opportunities` → `jobs` (via view-alias for transitional reads).
2. Create `job_postings`:

```sql
CREATE TABLE job_postings (
    id              VARCHAR(20) NOT NULL,
    discovered_at   TIMESTAMPTZ NOT NULL,
    canonical_id    VARCHAR(20) NOT NULL,    -- → jobs.canonical_id
    source_id       VARCHAR(20) NOT NULL,
    url             TEXT        NOT NULL,
    apply_url       TEXT,
    raw_title       TEXT,
    raw_company     TEXT,
    raw_location    TEXT,
    date_posted     TIMESTAMPTZ,
    valid_through   TIMESTAMPTZ,
    posting_status  TEXT NOT NULL DEFAULT 'active', -- active | inactive | expired
    extracted_jsonld JSONB,                -- the raw JobPosting JSON-LD
    content_hash    VARCHAR(64),
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, discovered_at)
);

SELECT create_hypertable('job_postings', 'discovered_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '30 days');

CREATE INDEX job_postings_canonical_idx ON job_postings (canonical_id);
CREATE INDEX job_postings_active_idx
    ON job_postings (last_seen_at DESC)
    WHERE posting_status = 'active';
```

The existing canonical merge in `apps/worker/service/canonical.go` writes BOTH a `jobs` row AND a `job_postings` row. The dedup key (`hard_key`) determines the canonical_id; multiple postings sharing a hard_key all link to the same canonical_id.

When all postings of a canonical flip to `inactive`, a periodic job (existing retention.expire endpoint we already wired in Plan 1) flips the canonical's `status` to `expired`. No separate logic needed.

### D5. JSON-LD-First Extraction

In `apps/crawler/service/crawl_request_handler.go`, change the extraction priority:

```go
// 1. Try JSON-LD JobPosting — fast, deterministic, no LLM cost.
if extracted := extractJSONLD(body); extracted != nil {
    mergeStubFields(opp, extracted)
    return  // success path; skip the LLM
}

// 2. Connector-specific extraction (existing).
if connectorSpecific := conn.ExtractFromContent(body); connectorSpecific != nil {
    mergeStubFields(opp, connectorSpecific)
    return
}

// 3. LLM extractor (existing fallback).
if extracted, err := h.deps.Extractor.Extract(ctx, body, src.Kinds); err == nil && extracted != nil {
    mergeStubFields(opp, extracted)
}
```

The `pkg/connectors/spec.schemaorgjsonld` connector (planned in Plan B) becomes a thin wrapper over this same extraction step — the logic lives in `pkg/extraction/jsonld` so the crawl handler and the spec connector share it.

## Phasing (Plans D1-D5)

Each cap is its own plan, written when prior caps ship cleanly:

| Plan | Caps | Estimate | Ships as |
|---|---|---|---|
| D1 | Iterator checkpoints | 3-4 days | Drops mid-crawl resume cost from "full restart" to "from the last persisted page" |
| D2 | URL frontier table + per-URL claim | 5-7 days | Crawl scheduler claims URLs not sources; per-URL freshness state |
| D3 | `pkg/freshness` scoring + integration | 3-4 days | Adaptive `next_crawl_at`, replacing fixed `crawl_interval_sec` |
| D4 | `job_postings` split + canonical/posting model | 6-8 days | Cross-source dedup explicit; canonical inactive when all postings inactive |
| D5 | JSON-LD-first extraction | 3-4 days | LLM cost drops sharply on JSON-LD-rich sites |

Total: ~3-4 weeks. D1 + D3 are the cheapest and highest-value first wins.

## Relationship to A/B/C Plans

| Today's Plan | Relationship |
|---|---|
| **Plan A** (admin trace) | UNCHANGED. Trace endpoints already JOIN through pipeline_variants → opportunities; once jobs/job_postings exist (D4), the trace endpoint adds a "postings of this canonical" view that pulls from job_postings instead of pipeline_variants for the durable history. |
| **Plan B** (definitions + connectors) | Mostly unchanged. The 6 spec-driven connectors (`htmllisting`, `jsonfeed`, `rssfeed`, `sitemap`, `schemaorgjsonld`, `xmlfeed`) all need to support `CrawlResume(checkpoint)` to participate in D1. The `schemaorgjsonld` connector's extraction logic is shared with D5. |
| **Plan C** (Iceberg historic + UI) | Largely unchanged. The admin UI gains a "postings" view on the canonical detail page after D4 lands. |

The new D plans run in parallel to A/B/C — they don't block each other. A natural order: ship A (visibility) and the smaller half of B (definitions service) FIRST so operators can see what's happening before we change the crawl model.

## Out of Scope (For This Spec)

- **Google Custom Search discovery** — useful per the research doc but the API quotas + costs need separate analysis. Add as a "Plan D6" if and when we want it.
- **Bright Data / SerpApi paid SERP** — supplemental discovery; doesn't change the core architecture.
- **Search-engine choice** — ParadeDB BM25 + DiskANN already in use; no switch to Manticore/OpenSearch/Meilisearch needed.
- **Employer-intelligence / hire-funnel features** — product surface, not crawl architecture.
- **Application tracking** — already a separate service (`apps/applications`).
- **JobPosting → embedding bridging changes** — existing canonical embedding flow handles it.

## Open Questions

None blocking — every decision either fits cleanly into the existing schema or has a locked answer above.
