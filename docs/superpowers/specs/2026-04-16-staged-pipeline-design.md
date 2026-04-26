# Staged Pipeline — Design Spec

**Date**: 2026-04-16
**Goal**: Rebuild the crawl-to-ready pipeline as a staged, event-driven system where every job passes through crawl → dedup → normalize → validate → canonical stages. Each stage is a Frame event backed by NATS JetStream. No data is lost. AI takes as long as it needs. Full OTel instrumentation throughout.

---

## 1. Problem

The current system does everything in one pass: crawl → dedupe → store. This causes:
- Incomplete data (0% seniority, 0% skills, 0% industry across 4,000+ jobs)
- AI enrichment either blocks crawling or gets skipped
- Failed enrichment means data is permanently incomplete
- No way to retry failed stages independently
- No visibility into where jobs are in the pipeline

## 2. Pipeline Stages

```
Stage 0: CRAWL    → fast, no AI, bloom filter + DB dedup
Stage 1: DEDUP    → per-source uniqueness, advisory locks
Stage 2: NORMALIZE → AI extraction, takes its time, discovers new sources
Stage 3: VALIDATE  → AI review, independent from extractor
Stage 4: CANONICAL → cluster, embed, score, ready for matching
```

### Stage 0: CRAWL

Connector fetches data. For each page:

**Content extraction pipeline (before storing):**
1. Fetch raw HTML
2. Normalize encoding / decompress (handle gzip, charset detection)
3. Extract main content using go-trafilatura or go-domdistiller (strips nav, ads, footers, sidebars)
4. Sanitize remaining junk (script remnants, tracking pixels, etc.)
5. Convert extracted HTML to Markdown using html-to-markdown
6. Store all three forms on the variant:
   - `raw_html` — original HTTP response body (for replay without recrawling)
   - `clean_html` — extracted main content HTML (for future reprocessing with better extractors)
   - `markdown` — clean markdown (what AI and humans see, what Stage 2 normalizes from)

**Dedup check:**
7. Compute hard_key from extracted content
8. Check **Valkey bloom filter**: `GETBIT bloom:seen:{source_id} {crc32(hard_key) % 2^20}`. If set → skip.
9. If not in bloom → check `job_variants` table for `hard_key`. If exists → skip.
10. If truly new → store variant with `stage = 'raw'`, set bloom bit.
11. Emit: `variant.raw.stored`

Crawler moves to next job immediately. No AI, no normalization.

**For JSON API connectors:** Steps 1-5 are skipped (data is already structured). The connector output is stored directly. Markdown is generated from the description field for consistency.

### Stage 1: DEDUP

Event: `variant.raw.stored`
1. Acquire advisory lock on hard_key
2. Check `job_variants` for existing hard_key within same source
3. If duplicate → update existing variant's `scraped_at`, done
4. If new → set `stage = 'deduped'`
5. Emit: `variant.deduped`

Pure code, no AI.

### Stage 2: NORMALIZE

Event: `variant.deduped`
1. Load variant's **markdown** content (clean, focused, no HTML noise)
2. Call Ollama with markdown + title + company — extract ALL intelligence fields:
   - seniority, skills (required + nice-to-have), tools_frameworks
   - industry, department, education, experience
   - salary estimate, currency, remote_type, employment_type
   - benefits, contacts, urgency, company_size, role_scope
   - geo_restrictions, timezone_req, application_type, ats_platform
   - **discovered_urls** — any external job board URLs found in the content
3. No timeout pressure — AI takes as long as it needs
4. Store extracted fields, `stage = 'normalized'`
5. Emit: `variant.normalized`
6. If discovered_urls present → emit: `source.urls.discovered`

If Ollama is down → event waits in NATS queue indefinitely, resumes when available.

### Stage 3: VALIDATE

Event: `variant.normalized`
1. Call Ollama with independent reviewer prompt
2. AI returns: `{valid: bool, confidence: 0.0-1.0, issues: [...], recommendation: "accept"/"reject"/"flag"}`
3. If valid (confidence >= 0.7) → `stage = 'validated'`, emit: `variant.validated`
4. If invalid → `stage = 'flagged'`, store validation_notes, keep for analysis
5. Increment source quality counters in sliding window
6. If failure rate > 50% in window → emit: `source.quality.review`

If Ollama is down → event waits in queue, resumes when available.

### Stage 4: CANONICAL

Event: `variant.validated`
1. Acquire advisory lock on hard_key
2. Find or create cluster (reuse existing if hard_key match exists)
3. Build canonical job from validated variant
4. Generate embedding via Ollama
5. Compute quality score
6. Update tsvector search index
7. `stage = 'ready'`
8. Emit: `job.ready`

---

## 3. Source Lifecycle

### Seed → Discovery → Growth

1. **Seed files** loaded on startup → initial sources in `sources` table
2. **AI discovery** during Stage 2 (normalize): AI extracts `discovered_urls` from job content
3. **Source expansion handler** receives `source.urls.discovered`:
   - Follows redirect chains (HTTP HEAD, max 10 hops) to resolve final destination
   - Filters out non-job sites (social media, generic sites, tracking redirects)
   - Deduplicates against existing sources
   - Upserts valid new sources with `status = 'active'`, `type = 'generic_html'`
4. **Source health management**: active → degraded → paused → disabled (circuit breaker)
5. **AI source review**: when validation failure rate > 50% in sliding window

### Source Quality Sliding Window

```
quality_window_start   TIMESTAMPTZ
quality_window_days    INT DEFAULT 1   -- starts at 1 day
quality_validated      INT DEFAULT 0
quality_flagged        INT DEFAULT 0
```

Window progression: 1 day → 2 days → 4 days → 7 days → 14 days (cap).

After passing AI review: double window, reset counters.

Trigger: `quality_flagged / (quality_validated + quality_flagged) > 0.5`

### Source Quality AI Review

Event: `source.quality.review`
1. Load last N validated + flagged variants from the source
2. Call Ollama: assess source quality from samples
3. AI returns: `continue` / `reduce_frequency` / `pause` / `disable` + reason
4. Apply recommendation automatically

---

## 4. Bloom Filter

Shared Valkey bitmap for fast duplicate detection at crawl time.

```
Key:    bloom:seen:{source_id}
Check:  GETBIT bloom:seen:{source_id} {crc32(hard_key) % 2^20}
Set:    SETBIT bloom:seen:{source_id} {crc32(hard_key) % 2^20} 1
Size:   ~128KB per source (1M bits)
FPR:    <1% at 50K items per source
TTL:    None (persists until source disabled)
```

Fallback when Valkey unavailable: skip bloom, go straight to DB check.

Flow:
1. Bloom says "seen" → skip immediately (fast path, 95%+ of re-crawls)
2. Bloom says "not seen" → check DB for hard_key (accurate confirmation)
3. DB says exists → skip (bloom false negative, rare)
4. DB says new → store variant, set bloom bit

---

## 5. Data Model Changes

### New Fields on job_variants

```sql
stage              VARCHAR(20) NOT NULL DEFAULT 'raw'
raw_html           TEXT        -- original HTTP response (for replay)
clean_html         TEXT        -- main content extracted (for reprocessing)
markdown           TEXT        -- clean markdown (AI input, human readable)
validation_score   REAL
validation_notes   TEXT
discovered_urls    TEXT
```

### Storage Strategy

| Field | Purpose | When populated |
|---|---|---|
| `raw_html` | Replay without recrawling | Stage 0, always |
| `clean_html` | Reprocess with better extractors | Stage 0, HTML sources only |
| `markdown` | AI normalization input, human readable | Stage 0, always |
| `description` | Legacy field, kept for backward compat | Stage 0 (connector output) |

For JSON API connectors: `raw_html` stores the JSON response, `clean_html` is empty, `markdown` is generated from the description field.

### Index

```sql
CREATE INDEX idx_job_variants_stage ON job_variants(stage);
```

### Source Quality Fields

```sql
-- On sources table:
quality_window_start  TIMESTAMPTZ
quality_window_days   INT DEFAULT 1
quality_validated     INT DEFAULT 0
quality_flagged       INT DEFAULT 0
```

---

## 6. Event Configuration

### NATS JetStream Streams

```
Stream: svc_opportunities_pipeline
Subjects:
  - svc.opportunities.pipeline.variant.raw.stored
  - svc.opportunities.pipeline.variant.deduped
  - svc.opportunities.pipeline.variant.normalized
  - svc.opportunities.pipeline.variant.validated
  - svc.opportunities.pipeline.source.urls.discovered
  - svc.opportunities.pipeline.source.quality.review
  - svc.opportunities.pipeline.job.ready
Retention: workqueue
Storage: file
MaxAge: 720h (30 days)
```

### Event Retry Policy

| Event | Retry | On failure |
|---|---|---|
| `variant.raw.stored` | 3x then dead-letter | Code-only, DB failure is fatal |
| `variant.deduped` | Infinite, exponential backoff | Waits for Ollama |
| `variant.normalized` | Infinite, exponential backoff | Waits for Ollama |
| `variant.validated` | 3x then dead-letter | Code-only (cluster + embed) |
| `source.urls.discovered` | Infinite, exponential backoff | HTTP may fail |
| `source.quality.review` | Infinite, exponential backoff | Waits for Ollama |

AI stages never give up. Events wait in the NATS queue until Ollama is available.

### In-Memory Fallback (Development)

When NATS is not configured, Frame falls back to `mem://` queue. Events are processed in-process but lost on restart. Acceptable for local development only.

---

## 7. Instrumentation (OpenTelemetry)

### Traces

One trace per job, propagated through NATS message headers across stages:

```
crawl (root span)
  → dedup (child span)
    → normalize (child span, may take minutes)
      → source_discovery (child span, if URLs found)
      → validate (child span)
        → canonical (child span)
          → embedding (child span)
```

### Metrics

```
pipeline.stage.transitions    counter    {from, to, source_type}
pipeline.stage.duration       histogram  {stage, source_type}
pipeline.queue.depth          gauge      {stage}
pipeline.bloom.hits           counter    {source_id}
pipeline.bloom.misses         counter    {source_id}
source.health.score           gauge      {source_id, source_type}
source.validation.rate        gauge      {source_id}
source.field.coverage         gauge      {source_id, field}
source.discovery.new          counter    {}
```

### Health Endpoint

```json
{
  "pipeline": {
    "raw": 45,
    "deduped": 12,
    "normalizing": 3,
    "validated": 5,
    "ready": 4200,
    "flagged": 23
  },
  "queue_depth": {
    "variant.deduped": 12,
    "variant.normalized": 8,
    "variant.validated": 5
  },
  "ollama": {
    "status": "up",
    "model": "gemma3:1b"
  },
  "sources": {
    "active": 25,
    "degraded": 3,
    "paused": 5,
    "disabled": 2
  }
}
```

---

## 8. Migration Path

### From Current → Staged Pipeline

1. Add `stage` column to `job_variants` (default 'raw')
2. Set all existing variants to `stage = 'ready'` (they've already been processed, keep them)
3. Deploy new event handlers
4. New jobs entering the system go through the full pipeline
5. Optionally: re-emit existing variants through normalize → validate to enrich them

### Existing Data

4,147 existing variants keep `stage = 'ready'` and their current data. They won't have AI-enriched intelligence fields until re-processed. A backfill can re-emit them through Stage 2+ if desired.

---

## 9. Deferred

- NATS JetStream integration (use Frame's in-memory queue until NATS is wired)
- Grafana dashboards for pipeline metrics
- Dead-letter queue monitoring and alerting
- Per-source crawl rate limiting via Valkey
- Headless browser (go-rod) for JS-rendered pages
