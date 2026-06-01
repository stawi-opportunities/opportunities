# Plan D2 — URL Frontier (Priority Queue per-URL + Politeness)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the current per-source iterator model (one connector grabs a source, runs through its pages serially) with a per-URL priority queue. Hot URLs (just discovered, high-scoring source) jump the queue; stale URLs drift to the back; per-host politeness windows prevent us from hammering any one domain regardless of how many sources point at it.

**Architecture:** A new `url_frontier` Postgres table is the canonical priority queue. Connectors no longer drive the crawl loop themselves — they discover URLs and *enqueue* them with a score. A new `frontier_worker` (one process, horizontally scalable via NATS `crawl.url.requests.v1`) dequeues URLs in priority order, respects per-host politeness windows (`host_state` table tracks `next_eligible_at` per host), fetches, and forwards to the existing extractor. Connector iterators stay for the *discovery* phase (sitemaps, jsonfeeds, listings) but their output is now enqueue-only.

**Tech Stack:** Go, Postgres (priority queue + `SELECT ... FOR UPDATE SKIP LOCKED`), NATS JetStream, frame v1.97+.

**Depends on:** Plans A, B1, B2, D1, D3 (all shipped). D2 reuses the spec-connector iterator output but routes it through the new enqueue path.

---

## Why this matters

Today's per-source model has three problems the architecture doc called out:

1. **One source = one crawl burst.** A noisy ATS (e.g. greenhouse) emits 500 URLs in 30 seconds, then the connector exits. Meanwhile, a single high-value posting from a sitemap source sits unprocessed for the full source-level interval.
2. **No per-host politeness.** Two sources both pointing at `boards.greenhouse.io` produce double the request rate against one host. Greenhouse rate-limits us → we get partial failures and retry storms.
3. **No work-stealing across pods.** A single crawler pod owns the iteration for a source's full duration. If that pod restarts mid-iteration, only D1's checkpoint saves us; the other crawler pod can't help finish faster.

A URL-level priority queue fixes all three:
- Fairness emerges from per-URL scoring, not per-source intervals.
- Per-host politeness lives in `host_state.next_eligible_at` and is shared across all connectors targeting that host.
- Multiple frontier workers race on `SELECT ... FOR UPDATE SKIP LOCKED` — natural work-stealing.

---

## File Structure

**Created:**
- `apps/crawler/migrations/0001/20260529_0080_url_frontier.sql` — main queue table.
- `apps/crawler/migrations/0001/20260529_0081_url_frontier_priority_idx.sql` — composite priority index.
- `apps/crawler/migrations/0001/20260529_0082_host_state.sql` — per-host politeness state.
- `apps/crawler/migrations/0001/20260529_0083_url_frontier_dedup_idx.sql` — unique index on canonical_url.
- `pkg/frontier/frontier.go` — `Frontier` interface (`Enqueue`, `Dequeue`, `Complete`, `Fail`).
- `pkg/frontier/postgres.go` — Postgres implementation.
- `pkg/frontier/postgres_test.go` — table-driven tests against testcontainers.
- `pkg/frontier/politeness.go` — `HostStateRepository` + `NextEligibleAt(host)` helpers.
- `pkg/events/v1/frontier.go` — `URLEnqueuedV1` event + topic constants.
- `apps/frontier-worker/cmd/main.go` — new app: dequeues + fetches + emits to existing extractor pipeline.
- `apps/frontier-worker/Dockerfile`.
- `apps/api/cmd/frontier_admin.go` — `GET /admin/frontier`, `GET /admin/frontier/{url_id}`, `POST /admin/frontier/{url_id}/requeue`, `DELETE /admin/frontier/{url_id}`.

**Modified:**
- `pkg/connectors/spec/htmllisting/htmllisting.go`, `pkg/connectors/spec/jsonfeed/jsonfeed.go`, `pkg/connectors/spec/sitemap/sitemap.go`, etc. — each connector's iterator output funnels into `frontier.Enqueue(urls)` instead of (or *in addition to*) returning extracted opportunities directly. Spec connectors that already extract directly from listing pages stay backward-compatible; the new behaviour is opt-in via a `frontier_enabled` flag on `Source` (Task 9).
- `apps/crawler/service/crawl_request_handler.go` — when `frontier_enabled=true`, the handler runs the iterator in *discovery mode* (URLs only), then enqueues; non-frontier sources stay on the current direct-extract path.
- `pkg/repository/source.go` — adds `FrontierEnabled bool` field.
- `apps/api/cmd/sources_admin.go` — operator can toggle `frontier_enabled` per source.

---

## Schema design

### `url_frontier`

```
PK              url_id           VARCHAR(20)         (KSUID; not the URL hash — the URL hash lives separately for dedup)
                canonical_url    TEXT NOT NULL       (post-normalization: lowercased host, trailing slash stripped, query-key sorted, tracking params removed)
                canonical_url_hash CHAR(64)          (sha256 hex; the dedup key)
                host             TEXT NOT NULL       (extracted from canonical_url; the politeness window key)
                source_id        VARCHAR(20)         (FK; the source that discovered the URL — used for score + ownership)
                priority         DOUBLE PRECISION    (computed: source.score * 0.7 + url_signals * 0.3; high = pop first)
                state            TEXT NOT NULL       ('pending', 'in_flight', 'done', 'failed', 'parked')
                attempts         INT NOT NULL DEFAULT 0
                last_error       TEXT
                enqueued_at      TIMESTAMPTZ NOT NULL DEFAULT now()
                claimed_at       TIMESTAMPTZ        (when a worker SELECT...FOR UPDATE'd this row)
                claimed_by       TEXT                (frontier-worker pod name; observability + stuck-row cleanup)
                completed_at     TIMESTAMPTZ
                next_attempt_at  TIMESTAMPTZ        (for backoff after Fail; null = eligible now)
                metadata         JSONB              (connector-defined: title hint, source rank, etc.)
```

### `host_state`

```
PK              host               TEXT
                window_minutes     INT NOT NULL DEFAULT 1      (host-specific politeness window)
                last_request_at    TIMESTAMPTZ
                next_eligible_at   TIMESTAMPTZ                  (computed: last_request_at + window_minutes; queried by Dequeue)
                ok_count_24h       INT NOT NULL DEFAULT 0       (rolling counters; used by D3 to bump source health)
                err_count_24h      INT NOT NULL DEFAULT 0
                concurrency_max    INT NOT NULL DEFAULT 1       (operator override: how many parallel in-flight for this host)
                concurrency_now    INT NOT NULL DEFAULT 0       (atomic counter; UPDATE ... SET concurrency_now = concurrency_now + 1 in Dequeue txn)
```

### Indexes

- `url_frontier`: composite `(state, priority DESC, next_attempt_at)` for the Dequeue hot path.
- `url_frontier`: unique `canonical_url_hash` — dedup safeguard at the index level.
- `url_frontier`: btree `host` for host-level rollup queries (admin).
- `host_state`: PK already covers the only lookup pattern.

---

## Tasks

### Task 1 — Migrations

One statement per file (Frame's tx.Exec is prepared-statement; SQLSTATE 42601 on multi-stmt). Five files:

- `20260529_0080_url_frontier_table.sql`
- `20260529_0081_url_frontier_priority_idx.sql`
- `20260529_0082_url_frontier_dedup_uniq.sql`
- `20260529_0083_host_state_table.sql`
- `20260529_0084_sources_frontier_enabled.sql` (`ALTER TABLE sources ADD COLUMN IF NOT EXISTS frontier_enabled BOOLEAN NOT NULL DEFAULT FALSE`)

Each file's body matches the schema sections above. Commit per migration (5 commits).

---

### Task 2 — `pkg/frontier` interface

**File:** `pkg/frontier/frontier.go`

```go
package frontier

import (
    "context"
    "time"
)

// URL is the unit of work the frontier shuttles between discovery
// connectors and the fetch worker.
type URL struct {
    URLID            string            // KSUID; assigned on enqueue
    CanonicalURL     string
    Host             string
    SourceID         string
    Priority         float64
    State            State
    Attempts         int
    EnqueuedAt       time.Time
    ClaimedAt        *time.Time
    NextAttemptAt    *time.Time
    Metadata         map[string]any
}

type State string

const (
    StatePending  State = "pending"
    StateInFlight State = "in_flight"
    StateDone     State = "done"
    StateFailed   State = "failed"
    StateParked   State = "parked"
)

// Frontier is the interface frontier-worker + connectors talk to.
type Frontier interface {
    // Enqueue adds URLs (or returns ErrDuplicate per-row via a
    // []EnqueueResult; never errors as a whole unless DB is down).
    // Duplicates by canonical_url_hash are deduped against the
    // existing row's priority — the higher of old/new wins, and
    // the row is left in its current state (so an in-flight URL
    // isn't reset).
    Enqueue(ctx context.Context, urls []URL) ([]EnqueueResult, error)

    // Dequeue claims up to `n` URLs respecting per-host politeness
    // (host's next_eligible_at <= now) and concurrency caps.
    // Atomic: SELECT ... FOR UPDATE SKIP LOCKED ... UPDATE state =
    // 'in_flight' in one transaction.
    Dequeue(ctx context.Context, n int, workerID string) ([]URL, error)

    // Complete marks the URL done + bumps host_state.ok_count_24h
    // + advances next_request_at.
    Complete(ctx context.Context, urlID string) error

    // Fail marks the URL failed (or schedules a retry with backoff
    // if attempts < maxAttempts). Backoff: exponential capped at 1h.
    Fail(ctx context.Context, urlID string, err error, maxAttempts int) error
}

type EnqueueResult struct {
    URLID     string
    Inserted  bool   // false = duplicate; the existing row may have been updated to a higher priority
}
```

**File:** `pkg/frontier/postgres.go`

GORM-backed implementation. Use raw SQL with `FOR UPDATE SKIP LOCKED` for the Dequeue hot path — GORM's builder won't compose this cleanly. The Dequeue join:

```sql
SELECT f.url_id, f.canonical_url, f.host, f.source_id, f.priority, f.attempts, f.metadata
FROM   url_frontier f
JOIN   host_state h ON h.host = f.host
WHERE  f.state = 'pending'
  AND  (f.next_attempt_at IS NULL OR f.next_attempt_at <= now())
  AND  h.next_eligible_at <= now()
  AND  h.concurrency_now < h.concurrency_max
ORDER BY f.priority DESC, f.enqueued_at ASC
LIMIT  $1
FOR UPDATE OF f SKIP LOCKED;
```

Then in the same transaction:
- `UPDATE url_frontier SET state='in_flight', claimed_at=now(), claimed_by=$worker, attempts=attempts+1 WHERE url_id IN (...)`
- `UPDATE host_state SET concurrency_now = concurrency_now + 1, last_request_at=now(), next_eligible_at = now() + (window_minutes || ' minutes')::interval WHERE host IN (...)`

This atomicity is what gives us:
1. No double-claim across workers.
2. Per-host concurrency cap respected.
3. Politeness window enforced at the queue level — discovery connectors don't need to think about it.

Tests at `pkg/frontier/postgres_test.go` cover:
- Enqueue dedups on canonical_url_hash.
- Dequeue returns highest-priority first.
- Dequeue skips rows still in their host's politeness window.
- Dequeue skips rows already in_flight (race with another worker).
- Fail with attempts < max schedules a retry with backoff.
- Fail with attempts == max moves to 'failed' state.
- Concurrent dequeue from N workers never double-claims.

---

### Task 3 — Events: `URLEnqueuedV1`

**File:** `pkg/events/v1/frontier.go`

```go
const TopicURLEnqueued = "crawl.url.enqueued.v1"

type URLEnqueuedV1 struct {
    URLID         string    `json:"url_id"`
    CanonicalURL  string    `json:"canonical_url"`
    Host          string    `json:"host"`
    SourceID      string    `json:"source_id"`
    Priority      float64   `json:"priority"`
    DiscoveredAt  time.Time `json:"discovered_at"`
}
```

The event is emitted by `Frontier.Enqueue` after the DB write succeeds. The frontier-worker subscribes to a JetStream consumer on this topic and pulls from the DB — the event is the wake-up signal, not the work payload (the canonical URL list lives in `url_frontier`).

This split lets the worker scale by NATS consumer + the queue stays the source of truth.

---

### Task 4 — `apps/frontier-worker`

Standalone app. Skeleton:

```go
func main() {
    ctx := frame.NewContext(...)
    svc := frame.NewServiceWithContext(ctx, "frontier-worker", ...)
    frontier := frontier.NewPostgresFrontier(svc.DB)
    fetcher := newFetcher(svc.HTTPClientManager())
    extractor := loadExtractor(ctx, ...)

    // One-shot Dequeue loop per worker; the loop blocks on a NATS
    // consumer signal + a 1s heartbeat so it can scale up under
    // discovery bursts and idle cheaply when the queue is empty.
    consumer := svc.Subscribe(eventsv1.TopicURLEnqueued, func(ctx context.Context, _ events.EventI) error {
        for {
            urls, err := frontier.Dequeue(ctx, 5, podName())
            if err != nil { log.WithError(err).Warn("dequeue"); return err }
            if len(urls) == 0 { return nil } // back to idle
            for _, u := range urls {
                runOne(ctx, u, fetcher, extractor, frontier)
            }
        }
    })
    // ... heartbeat ticker that also calls Dequeue every 1s as a fallback ...
}
```

`runOne` is roughly:

```go
func runOne(ctx, u, fetcher, extractor, frontier) {
    body, err := fetcher.Get(ctx, u.CanonicalURL)
    if err != nil {
        _ = frontier.Fail(ctx, u.URLID, err, 5) // 5 retries
        return
    }
    payload, _ := saveRaw(ctx, body) // archive → R2
    items := extractor.Extract(ctx, body, kindsFor(u.SourceID), promptExtFor(u.SourceID))
    // emit variants as usual (existing pipeline)
    emitVariants(ctx, items, u, payload)
    _ = frontier.Complete(ctx, u.URLID)
}
```

Dockerfile + deployment manifest follow the existing crawler/worker pattern — same chainguard/static base, same env-driven config. Initial replicas = 2.

---

### Task 5 — Connector iterator → enqueue plumbing

`pkg/connectors/spec/sitemap/sitemap.go` already extracts URLs as its primary job (sitemaps ARE URL lists). Change it to:

- If `source.FrontierEnabled` → call `frontier.Enqueue` for each URL it finds + skip the per-URL detail fetch entirely (the frontier-worker handles fetch).
- Else → keep the existing behaviour (detail_fetch fallback to schemaorgjsonld).

For `jsonfeed.go` / `rssfeed.go` / `htmllisting.go`: when `FrontierEnabled`, the iterator's first pass enqueues the URLs in the listing page; the detail-page fetch becomes the frontier-worker's job.

This is the cleanest split: the connector knows how to *discover URLs from a listing*; the worker knows how to *fetch a single URL and extract*.

Compatibility flag: `FrontierEnabled` defaults to false → no behaviour change for existing sources. Operators turn it on via `PATCH /admin/sources/{id}` after they've watched the frontier behave well.

---

### Task 6 — `apps/crawler/service/crawl_request_handler.go` branch

When `source.FrontierEnabled == true`:
- Run the iterator in *URL-discovery* mode (sitemap returns URLs; jsonfeed returns URLs from `entry.url`; listing returns URLs from anchors; etc.).
- Each iterator page hands its URLs to `frontier.Enqueue(...)`.
- Emit a `crawl.completed` event when the iterator drains (existing pattern).

When false: keep the existing extract-in-line flow.

---

### Task 7 — Admin operator surface

`GET /admin/frontier` — list URLs by state + host + priority bucket. Filters: `state=pending|in_flight|failed`, `host=...`, `source_id=...`. Pagination: cursor by priority+enqueued_at.

`GET /admin/frontier/{url_id}` — single-URL detail: raw row + recent attempts + linked raw_payloads.

`POST /admin/frontier/{url_id}/requeue` — reset state back to pending, clear `next_attempt_at`, increment attempts only if state was 'failed'.

`DELETE /admin/frontier/{url_id}` — hard delete; operator override when a URL is stuck and shouldn't retry.

`PATCH /admin/sources/{id}` gains `frontier_enabled` as a settable field.

UI: new SourceList column showing `frontier_enabled` + a row-level toggle action.

---

### Task 8 — Backfill the politeness table from existing hosts

```sql
INSERT INTO host_state (host)
SELECT DISTINCT regexp_replace(base_url, '^https?://([^/]+).*', '\1') AS host
FROM   sources
WHERE  base_url IS NOT NULL
ON CONFLICT (host) DO NOTHING;
```

As `apps/crawler/migrations/0001/20260529_0085_host_state_backfill.sql`.

---

### Task 9 — Tag + deploy

```bash
git push origin main
git tag v8.0.70
git push origin v8.0.70
```

Verify:

```bash
DBPOD=product-opportunities-db-1

# Tables exist:
kubectl exec -n product-opportunities $DBPOD -c postgres -- psql -U postgres -d opportunities -c "\d url_frontier"
kubectl exec -n product-opportunities $DBPOD -c postgres -- psql -U postgres -d opportunities -c "\d host_state"

# Backfill populated host_state:
kubectl exec -n product-opportunities $DBPOD -c postgres -- psql -U postgres -d opportunities -c "SELECT count(*) FROM host_state;"

# Toggle frontier on for ONE test source first:
kubectl exec -n product-opportunities $DBPOD -c postgres -- psql -U postgres -d opportunities -c \
  "UPDATE sources SET frontier_enabled = true WHERE id = 'd8a8akcgpioc739hsh80'"

# Trigger a crawl; URLs should land in url_frontier:
curl -X POST https://opportunities-api/admin/sources/d8a8akcgpioc739hsh80/crawl

# Frontier worker logs should show dequeue + politeness:
kubectl logs -n product-opportunities -l app.kubernetes.io/name=opportunities-frontier-worker --tail=50
```

---

## Plan D2 Exit Criteria

- [ ] All 5 migrations apply clean (one statement per file).
- [ ] `pkg/frontier` tests pass against testcontainers (`go test ./pkg/frontier/ -v -count=1`).
- [ ] `apps/frontier-worker` builds + runs locally against a minimal Postgres.
- [ ] A test source with `frontier_enabled=true` enqueues URLs on next crawl; the frontier-worker dequeues + fetches them.
- [ ] Per-host politeness: two sources sharing a host see their URLs interleave at the host's window cadence, not back-to-back.
- [ ] Two frontier-worker pods running in parallel never double-claim a URL (race test: 1000 URLs, 2 workers, `count(*) WHERE state='done'` == 1000, no orphaned `in_flight`).
- [ ] Operator can requeue a failed URL via `POST /admin/frontier/{url_id}/requeue`.
- [ ] Toggling `frontier_enabled=false` on a source reverts it to the legacy direct-extract path with no rebuilds.

---

## Scope explicitly NOT in D2

- Per-URL adaptive recrawl (re-enqueueing the same URL on a schedule). That's a follow-up; D2 lands the queue + the politeness mechanism.
- Robots.txt parsing. Politeness windows are operator-set per host today; robots.txt fetching is a follow-up.
- Cross-region frontier (one queue per region). Single Postgres queue is sufficient for current scale.
- Canonical/posting split (jobs vs job_postings tables). That's a separate plan even though it intersects with this one — the architecture doc treats them as orthogonal concerns.
