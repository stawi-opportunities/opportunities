# Crawl System Robustness — Design Spec

**Date**: 2026-04-16
**Goal**: Make the crawling system robust, self-healing, and correct. Fewer jobs done well beats many jobs done badly.

---

## 1. Context

The system has 35 sources but only 7 produce jobs. 28 sources produce nothing or flood rejected_jobs (72K rejects from Himalayas alone). Health scores never degrade — broken sources keep getting re-crawled at full frequency. The dedupe pipeline has a bug creating 45K canonical entries from 3.8K variants. Connectors break on minor API format changes.

### Current State

| Category | Count |
|---|---|
| Sources producing jobs | 7 |
| Sources producing nothing | 21 |
| Sources flooding rejects | 7 |
| Canonical jobs (inflated) | 45,032 |
| Actual job variants | 3,855 |
| Total rejects | ~74,000 |

---

## 2. Circuit Breaker & Source Health

### Two distinct failure modes

**Connection failures** (DNS, timeout, 5xx, blocked) trigger the circuit breaker:

```
active → degraded (3 consecutive failures)
  → paused (2 more failures while degraded)
  → disabled (failed recovery attempt after 7 days)
```

Recovery: a successful crawl at any degraded/paused state returns the source to active.

**High reject rates** (>80% of jobs rejected in a crawl cycle) are handled separately:

- Log warning: "connector quality issue: {source} rejected {N}% of jobs"
- Keep crawling at normal interval
- Mark source with `needs_tuning = true` flag
- Do NOT count as failure for circuit breaker

Rationale: a site being down is different from our code being wrong. Punishing a source because our connector is broken penalizes the wrong thing. When the connector is fixed, the source immediately benefits.

### Crawl interval by state

| State | Interval |
|---|---|
| active | Source's configured interval |
| degraded | 6x configured interval |
| paused | Try once after 24hr, then once after 7 days |
| disabled | Never. Re-enable via admin API only. |

### Health score computation

```
Successful crawl (reject rate < 80%):
  health_score = min(1.0, health_score + 0.1)
  consecutive_failures = 0

Failed crawl OR reject rate >= 80%:
  health_score = max(0.0, health_score - 0.2)
  consecutive_failures += 1
```

### New fields on Source

- `consecutive_failures INT DEFAULT 0`
- `needs_tuning BOOLEAN DEFAULT false`
- Status values: `active`, `degraded`, `paused`, `disabled`

---

## 3. Quality Gate

### Current (too strict)

All 5 fields required: title, company, location, description, apply_url. Rejects good jobs missing company or location.

### Revised

| Field | Rule | On missing |
|---|---|---|
| title | Required, > 3 chars | Reject |
| description | Required, > 50 chars | Reject |
| apply_url | If empty, fall back to source page URL | Never reject |
| company | Optional | Accept, store empty |
| location | Optional | Accept, store empty |

Only title and description cause rejection. Apply URL always has a fallback (the page URL the job was crawled from). Company and location are best-effort — AI fills them when possible, but missing values don't block storage.

---

## 4. Connector Architecture

### Two categories

**JSON API connectors** (direct parsing, no AI):
- Greenhouse, Lever, Workday, SmartRecruiters, RemoteOK, Arbeitnow, Jobicy, TheMuse, Himalayas, FindWork
- Parse structured JSON responses directly — fast, reliable
- Make parsers **tolerant of field type changes**: handle string-or-array, int-or-string gracefully instead of crashing on unexpected types

**Universal AI connector** (everything else):
- All HTML board sources: BrighterMonday, Jobberman, MyJobMag, Njorku, Careers24, PNet, Schema.org, and any new source
- AI discovers links on listing pages, AI extracts fields from detail pages
- Resilient: if AI link discovery returns nothing, fall back to simple href pattern matching for common URL patterns (`/jobs/`, `/listings/`, `/vacancies/`, `/careers/`, `/job/`, `/position/`)

### AI timeouts — let it finish

- Extraction timeout: **10 minutes** (let Ollama complete on CPU)
- Embedding timeout: **5 minutes**
- Connection refused: fail immediately (Ollama is down, not slow)
- No rushing. Slow and correct beats fast and broken.

### Specific connector bugs to fix

| Connector | Bug | Fix |
|---|---|---|
| Himalayas | `jobType` is array, struct expects string | Handle both types |
| Jobicy | Same `jobType` array issue | Handle both types |
| Lever | 0 variants from all boards | Debug API response format |
| Greenhouse (5 boards) | 0 variants (deliveroo, doordash, andela, shopify, canva) | Verify API paths |
| Workday | 0 variants | Debug API path |
| FindWork | 0 variants | Debug response format |

### JSON parser resilience pattern

Instead of strict Go structs that break on type changes:

```go
// Bad: breaks when API changes field type
type Job struct {
    JobType string `json:"jobType"`
}

// Good: handles string, array, or missing
type Job struct {
    JobType flexString `json:"jobType"`
}
// flexString unmarshals string, []string, or null into a single string
```

---

## 5. Dedupe Fix + Data Rebuild

### The Bug

`UpsertAndCluster` creates a new cluster for every variant regardless of hard_key matches. Result: 45K canonicals from 3.8K variants.

### Fix

Correct logic:
1. Upsert variant
2. Look up existing variant by hard_key
3. If match found with existing cluster → add new variant to that cluster, update canonical with latest data
4. If no match → create new cluster + new canonical

### Data Rebuild

After fixing dedupe:
1. Truncate `canonical_jobs`, `job_clusters`, `job_cluster_members`
2. Iterate all `job_variants` ordered by `scraped_at ASC`
3. Run each through fixed `UpsertAndCluster`
4. Produces correct canonical count (should be ≤ variant count)

Available via admin endpoint: `POST /admin/rebuild-canonicals`

---

## 6. Observability

### Enhanced /healthz

```json
{
  "status": "ok",
  "sources": {
    "total": 35,
    "active": 20,
    "degraded": 5,
    "paused": 7,
    "disabled": 3
  },
  "jobs": {
    "canonical": 3200,
    "variants": 3855
  },
  "last_crawl_cycle": {
    "sources_processed": 12,
    "jobs_accepted": 340,
    "jobs_rejected": 15,
    "duration_sec": 45
  },
  "connector_quality": {
    "greenhouse": {"accept_rate": 1.0, "sources": 10},
    "arbeitnow": {"accept_rate": 1.0, "sources": 1},
    "myjobmag": {"accept_rate": 0.0, "needs_tuning": true, "sources": 4}
  }
}
```

### Admin Endpoints

```
POST /admin/sources/:id/pause       ← manually pause a source
POST /admin/sources/:id/enable      ← re-enable a disabled source
POST /admin/rebuild-canonicals      ← trigger one-time data rebuild
GET  /admin/sources/health          ← detailed per-source health report
```

---

## 7. Deferred

- Headless browser (go-rod) for JS-rendered pages — keep the code but don't activate until Chrome is in the container image
- NATS as event transport — currently using in-memory Frame events
- Soft/semantic deduplication — hard-key only for now
