# Phase 4 — Crawler Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewire `apps/crawler` so it feeds the new event-sourced pipeline instead of Postgres. After this plan lands, a Trustage `scheduler.tick` fires an admin endpoint, the crawler admits sources through a backpressure gate, emits `crawl.requests.v1` events, processes them by fetching + archiving + AI-extracting, and publishes every variant as `jobs.variants.ingested.v1` — which `apps/worker` (Phase 3) picks up to run the downstream pipeline. No new job data lands in Postgres; the only Postgres writes from the crawler are source-cursor/health updates driven by self-consumed `crawl.page.completed.v1` events.

**Architecture:** Two internal Frame subscriptions inside one `apps/crawler` binary:

1. **`crawl-request` sub** — consumes `crawl.requests.v1`, resolves connector, fetches page, archives raw bytes to R2 via `pkg/archive`, AI-extracts via `pkg/extraction.Extractor.Extract`, emits one `jobs.variants.ingested.v1` per extracted job, optionally samples `DiscoverSites` and emits `sources.discovered.v1`, emits one `crawl.page.completed.v1` summary per processed source.
2. **`page-completed` sub** — self-consumes `crawl.page.completed.v1`, updates `sources.next_crawl_at`, `health_score`, `consecutive_failures`, `last_seen_at`, and the quality-window counters. Also handles `sources.discovered.v1` (upserting new `generic-html` sources).

The scheduler moves from `/admin/crawl/dispatch-due` to `/admin/scheduler/tick`. The new endpoint uses an extended `backpressure.Gate.Admit(topic, want)` method (minimal wrapper on the existing hysteresis gate — full drain-time policy is Phase 6). Admitted sources get `next_crawl_at` stamped forward immediately so double-admission is impossible across pods; deferred sources stay on their current `next_crawl_at` and re-enter the pool on the next tick.

Legacy handlers (`pkg/pipeline/handlers/*`) and their `variant.raw.stored`/`source.urls.discovered`/`source.quality.review` topics remain in the repo untouched through this plan so Phase 5 (candidates) and Phase 6 (cutover) can delete them in one shot. After Plan 4 the crawler binary **no longer registers** the legacy handlers at startup — the worker binary from Phase 3 is the sole consumer of the v1 variant topic.

**Tech stack:**
- Go 1.26, Frame (`github.com/pitabwire/frame`), `pitabwire/util` logging
- Existing `pkg/extraction.Extractor` (Extract, DiscoverLinks, DiscoverSites)
- Existing `pkg/connectors` registry + `pkg/archive` R2 archive + `pkg/bloom` filter + `pkg/normalize`
- Existing `pkg/backpressure.Gate` — extended with `Admit(ctx, topic, want)`
- `pkg/events/v1/` (Phase 1) — extended with three crawl/source payload types
- `apps/writer` (Phase 1) — encoder switch extended for two new collections

**What's in this plan:**
- Three new event payload types: `CrawlRequestV1`, `CrawlPageCompletedV1`, `SourceDiscoveredV1` (in `pkg/events/v1/crawl.go`)
- Writer `extractHint` + `uploadBatch` extensions to persist the two durable crawl/source topics (crawl.requests is control-plane and **not** persisted)
- Partition-key routing: secondary label for `sources_discovered` collection
- `backpressure.Gate.Admit` method + unit tests
- `apps/crawler/service/crawl_request_handler.go` — the new `crawl.requests.v1` consumer (fetch + archive + extract + emit)
- `apps/crawler/service/page_completed_handler.go` — self-consumer that advances source cursors/health
- `apps/crawler/service/source_discovered_handler.go` — self-consumer that upserts new sources
- `apps/crawler/service/scheduler_tick.go` — `/admin/scheduler/tick` HTTP handler factory
- `apps/crawler/cmd/main.go` refactor — drop legacy handler registration + legacy `crawlDependencies`, wire the three new handlers, register `/admin/scheduler/tick`
- `definitions/trustage/scheduler-tick.json` — new Trustage trigger replacing `source-crawl-sweep.json`
- Integration test: POST `/admin/scheduler/tick` → assert variant events fire via an injected fake connector

**What's NOT in this plan (deferred):**
- Per-page fan-out (listing → detail URL fan-out via `crawl.requests.v1`). The connector interface is iterator-based today; splitting into a listing/detail pair is a deeper refactor. Plan 4 keeps the per-source iterator but emits every extracted variant as `jobs.variants.ingested.v1`. The per-page model is a Phase 6 follow-up.
- Full `Admit` policy with per-topic drain-time ceilings + HPA-ceiling awareness. Plan 4 ships a minimal wrapper that returns `want` when the existing hysteresis gate is open and `0` when paused. Phase 6 extends this.
- Deleting the legacy `pkg/pipeline/handlers/*` files, the `variant.raw.stored`/`source.urls.discovered`/`source.quality.review` topic constants, or the Postgres `job_variants` / `job_clusters` / `canonical_jobs` tables. Those deletions land together in Phase 6 cutover.
- Trustage-fired `sources.quality_window_reset` (weekly) and `sources.health_decay` (hourly) admin endpoints listed in spec §4.2. Both are small maintenance endpoints that mutate `sources.quality_*` / `sources.health_score`; they are orthogonal to the v1-event refactor and live better in Phase 6's ops bundle alongside retention + compaction.
- Candidates service refactor (Phase 5).
- Ops documentation, runbooks, rollback procedures (Phase 6).

---

## File structure

**Create:**

| File | Responsibility |
|---|---|
| `pkg/events/v1/crawl.go` | `CrawlRequestV1`, `CrawlPageCompletedV1`, `SourceDiscoveredV1` payload structs |
| `apps/crawler/service/crawl_request_handler.go` | Frame handler for `crawl.requests.v1` — fetch, archive, extract, emit variants |
| `apps/crawler/service/page_completed_handler.go` | Frame handler for `crawl.page.completed.v1` (self-consumed) — source cursor/health |
| `apps/crawler/service/source_discovered_handler.go` | Frame handler for `sources.discovered.v1` (self-consumed) — new-source upsert |
| `apps/crawler/service/scheduler_tick.go` | `/admin/scheduler/tick` HTTP handler factory |
| `apps/crawler/service/crawl_request_handler_test.go` | Unit tests for the crawl-request handler with a fake connector + fake archive + fake extractor |
| `apps/crawler/service/scheduler_tick_test.go` | Unit tests for `/admin/scheduler/tick` (admit, defer, emit) |
| `apps/crawler/service/e2e_test.go` | End-to-end: seed one source, POST tick, assert variant events + page-completed event fire |
| `definitions/trustage/scheduler-tick.json` | Trustage trigger firing `/admin/scheduler/tick` every 30 s |

**Modify:**

| File | Change |
|---|---|
| `pkg/events/v1/envelope_test.go` | Three new round-trip tests (one per payload type) |
| `pkg/events/v1/partitions.go` | Add `sources_discovered` label mapping + sources_discovered secondary routing |
| `apps/writer/service/handler.go` | Extend `extractHint` with `TopicCrawlPageCompleted` + `TopicSourcesDiscovered` + `TopicCrawlRequests` cases |
| `apps/writer/service/service.go` | Extend `uploadBatch` to encode `TopicCrawlPageCompleted` + `TopicSourcesDiscovered` (crawl.requests stays non-persisted) |
| `pkg/backpressure/gate.go` | Add `Admit(ctx, topic, want) (granted, wait)` method |
| `pkg/backpressure/gate_test.go` | Table-driven tests covering Admit's paused/open branches |
| `apps/crawler/cmd/main.go` | Drop legacy `pipelineHandlers` registration + legacy `crawlDependencies` + `/admin/crawl/dispatch` + `/admin/crawl/dispatch-due`. Wire three new Frame handlers and register `/admin/scheduler/tick`. |

**Delete:**

| File | Why |
|---|---|
| `definitions/trustage/source-crawl-sweep.json` | Replaced by `scheduler-tick.json` |

---

## Task 1: Add three new event payload types

**Files:**
- Create: `pkg/events/v1/crawl.go`
- Modify: `pkg/events/v1/envelope_test.go`

- [ ] **Step 1: Write failing round-trip tests**

Append to `pkg/events/v1/envelope_test.go`:

```go
func TestCrawlRequestRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCrawlRequests, CrawlRequestV1{
		RequestID: "req_1",
		SourceID:  "src_greenhouse_acme",
		URL:       "",
		Cursor:    "",
		Mode:      "auto",
		Attempt:   1,
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[CrawlRequestV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.SourceID != "src_greenhouse_acme" || back.Payload.Mode != "auto" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCrawlPageCompletedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCrawlPageCompleted, CrawlPageCompletedV1{
		RequestID:    "req_1",
		SourceID:     "src_1",
		URL:          "https://example.com/jobs",
		HTTPStatus:   200,
		JobsFound:    12,
		JobsEmitted:  11,
		JobsRejected: 1,
		Cursor:       "page=2",
		ErrorCode:    "",
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[CrawlPageCompletedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.SourceID != "src_1" || back.Payload.JobsFound != 12 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestSourceDiscoveredRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicSourcesDiscovered, SourceDiscoveredV1{
		DiscoveredURL: "https://another-board.example/careers",
		Name:          "Another Board",
		Country:       "KE",
		Type:          "generic-html",
		SourceID:      "src_1",
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[SourceDiscoveredV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.DiscoveredURL != "https://another-board.example/careers" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}
```

- [ ] **Step 2: Run to verify build failure**

```bash
cd /home/j/code/stawi.jobs
go test ./pkg/events/v1/...
```

Expected: `undefined: CrawlRequestV1, CrawlPageCompletedV1, SourceDiscoveredV1`.

- [ ] **Step 3: Implement the payload types**

Create `pkg/events/v1/crawl.go`:

```go
package eventsv1

// CrawlRequestV1 is the control-plane event that tells a single
// crawler replica "please fetch this source (or URL within it) now."
// Emitted by the scheduler-tick admin endpoint (one per admitted
// source) and optionally chained by the crawl-request handler itself
// when a listing page exposes pagination.
//
// Wire format only — this event is not persisted in the Parquet log.
// apps/writer skips it in the encoder switch.
//
// Mode values:
//   - "auto":    use source.type's connector iterator (current default)
//   - "listing": fetch URL and run DiscoverLinks to fan out detail URLs
//   - "detail":  fetch URL, run Extract, emit one VariantIngestedV1
//
// Only "auto" is wired in Phase 4; "listing" and "detail" are reserved
// for a future per-page fan-out refactor that will land post-cutover.
type CrawlRequestV1 struct {
	// RequestID is a fresh xid per admission. Carried through
	// downstream events (page-completed) so audit logs can trace a
	// single crawl attempt end-to-end.
	RequestID string `json:"request_id" parquet:"request_id"`

	SourceID string `json:"source_id" parquet:"source_id"`

	// URL is optional. Empty means "start at source.base_url". Non-
	// empty is used by the not-yet-wired detail fan-out path.
	URL string `json:"url,omitempty" parquet:"url,optional"`

	// Cursor is the opaque pagination token emitted by the connector
	// on a prior page. Empty on first request for a source.
	Cursor string `json:"cursor,omitempty" parquet:"cursor,optional"`

	// Mode defaults to "auto". See the type docstring for values.
	Mode string `json:"mode,omitempty" parquet:"mode,optional"`

	// Attempt is 1 for a fresh admission; retries bump it. Logged,
	// not acted on — retry policy lives in Frame's redelivery config.
	Attempt int `json:"attempt,omitempty" parquet:"attempt,optional"`
}

// CrawlPageCompletedV1 is emitted by the crawl-request handler after
// it finishes processing one request. Self-consumed by the crawler's
// page-completed handler to update the Postgres sources row (cursor,
// next_crawl_at, health_score, quality counters). Persisted in the
// Parquet log for audit + replay.
type CrawlPageCompletedV1 struct {
	RequestID string `json:"request_id" parquet:"request_id"`
	SourceID  string `json:"source_id"  parquet:"source_id"`

	URL        string `json:"url,omitempty"         parquet:"url,optional"`
	HTTPStatus int    `json:"http_status,omitempty" parquet:"http_status,optional"`

	// JobsFound    — raw job count returned by the iterator
	// JobsEmitted  — count of jobs that passed quality gate and were emitted
	// JobsRejected — count of jobs that failed the deterministic quality check
	JobsFound    int `json:"jobs_found"     parquet:"jobs_found"`
	JobsEmitted  int `json:"jobs_emitted"   parquet:"jobs_emitted"`
	JobsRejected int `json:"jobs_rejected"  parquet:"jobs_rejected"`

	// Cursor is the connector's last-emitted pagination token. Empty
	// means "this source has no more pages to crawl this cycle."
	Cursor string `json:"cursor,omitempty" parquet:"cursor,optional"`

	// ErrorCode is populated when the crawl failed entirely (reachability
	// probe failed, connector raised an error, or the listing returned
	// a 5xx). Empty on success. ErrorMessage carries the human-readable
	// detail for audit.
	ErrorCode    string `json:"error_code,omitempty"    parquet:"error_code,optional"`
	ErrorMessage string `json:"error_message,omitempty" parquet:"error_message,optional"`
}

// SourceDiscoveredV1 is emitted by the crawl-request handler when a
// sampled DiscoverSites call finds a link to another job board on the
// current page. Self-consumed by the source-discovered handler to
// upsert the target URL as a `generic-html` source. Persisted.
//
// SourceID is the *origin* source — the crawl that discovered the new
// link. Kept so the source_expand audit trail shows provenance.
type SourceDiscoveredV1 struct {
	DiscoveredURL string `json:"discovered_url" parquet:"discovered_url"`
	Name          string `json:"name,omitempty"    parquet:"name,optional"`
	Country       string `json:"country,omitempty" parquet:"country,optional"`
	Type          string `json:"type,omitempty"    parquet:"type,optional"`

	SourceID string `json:"source_id" parquet:"source_id"`
}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./pkg/events/v1/...
```

Expected: PASS (three new tests + all prior tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/events/v1/crawl.go pkg/events/v1/envelope_test.go
git commit -m "feat(events): add crawl request/completed + source discovered payload types"
```

---

## Task 2: Extend partition routing for `sources_discovered`

**Files:**
- Modify: `pkg/events/v1/partitions.go`
- Modify: `pkg/events/v1/envelope_test.go`

- [ ] **Step 1: Write failing test**

Append to `pkg/events/v1/envelope_test.go`:

```go
func TestPartitionKeySourcesDiscoveredUsesSourceHint(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	pk := PartitionKey(TopicSourcesDiscovered, now, "src_origin_1")
	if pk.Secondary != "src_origin_1" {
		t.Fatalf("Secondary=%q, want src_origin_1", pk.Secondary)
	}
}

func TestPartitionObjectPathSourcesDiscoveredLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "src_origin_1"}
	got := pk.ObjectPath("sources_discovered", "xyz789")
	want := "sources_discovered/dt=2026-04-21/src=src_origin_1/xyz789.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run — expect failure**

```bash
go test ./pkg/events/v1/... -run 'TestPartitionKeySourcesDiscovered|TestPartitionObjectPathSourcesDiscoveredLabel'
```

Expected: first test FAILs with `Secondary="_all"` (default bucket); second test FAILs with path label `p=` (default label).

- [ ] **Step 3: Update `partitionSecondary`**

Edit `pkg/events/v1/partitions.go`. In `partitionSecondary`, extend the first `case` so it covers `TopicSourcesDiscovered` too (the hint is the *origin* source_id, same shape as variants/crawl_page_completed):

```go
func partitionSecondary(eventType, hint string) string {
	switch eventType {
	case TopicVariantsIngested, TopicCrawlPageCompleted, TopicSourcesDiscovered:
		// per-source files — lots of small sources, keep them grouped
		return strings.ToLower(hint)
	case TopicCanonicalsUpserted, TopicCanonicalsExpired,
		TopicEmbeddings, TopicPublished:
		// cluster_id prefix (2 hex chars = 256 buckets per day)
		return firstN(strings.ToLower(hint), 2)
	case TopicTranslations:
		// hint is the target language code
		return strings.ToLower(hint)
	default:
		// everything else: single bucket per day
		return "_all"
	}
}
```

- [ ] **Step 4: Update `partitionSecondaryLabel`**

In the same file, extend `partitionSecondaryLabel` so `sources_discovered` gets the `src=` prefix (matches variants + crawl_page_completed):

```go
func partitionSecondaryLabel(collection string) string {
	switch collection {
	case "variants", "crawl_page_completed", "sources_discovered":
		return "src"
	case "canonicals", "canonicals_expired", "embeddings", "published":
		return "cc"
	case "translations":
		return "lang"
	default:
		return "p"
	}
}
```

- [ ] **Step 5: Run — expect PASS**

```bash
go test ./pkg/events/v1/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/events/v1/partitions.go pkg/events/v1/envelope_test.go
git commit -m "feat(events): route sources_discovered by source_id like variants"
```

---

## Task 3: Writer encoder + hint extraction for the two new persisted topics

**Files:**
- Modify: `apps/writer/service/handler.go`
- Modify: `apps/writer/service/service.go`

`TopicCrawlRequests` stays non-persisted: it is control-plane (tick → per-source dispatch), has no long-term audit value, and the scheduler enumerates its emissions in the Postgres `sources` row via `next_crawl_at` already. `apps/writer/service/buffer.go` → `collectionForTopic` does not list `TopicCrawlRequests`, which correctly maps it to `"_unknown"` and the buffer drops it — confirmed by reading the current file.

- [ ] **Step 1: Extend `extractHint`**

Edit `apps/writer/service/handler.go`. The current `extractHint` handles `TopicVariantsIngested`, `TopicCanonicalsUpserted`, `TopicCanonicalsExpired`, `TopicEmbeddings`, `TopicTranslations`. Add cases for the two new persisted topics. `TopicCrawlRequests` is left out — the writer never persists it.

```go
func extractHint(raw json.RawMessage, topic string) string {
	switch topic {
	case eventsv1.TopicVariantsIngested,
		eventsv1.TopicCrawlPageCompleted,
		eventsv1.TopicSourcesDiscovered:
		return decodeField(raw, "payload.source_id")
	case eventsv1.TopicCanonicalsUpserted, eventsv1.TopicCanonicalsExpired:
		return decodeField(raw, "payload.cluster_id")
	case eventsv1.TopicEmbeddings:
		return decodeField(raw, "payload.canonical_id")
	case eventsv1.TopicTranslations:
		return decodeField(raw, "payload.lang")
	default:
		return ""
	}
}
```

- [ ] **Step 2: Extend `uploadBatch` in `apps/writer/service/service.go`**

Add two new cases to the existing switch so `uploadBatch` knows how to encode the new Parquet rows. Add them adjacent to the `TopicPublished` case:

```go
	case eventsv1.TopicPublished:
		body, err = encodeBatch[eventsv1.PublishedV1](b.Events)
	case eventsv1.TopicCrawlPageCompleted:
		body, err = encodeBatch[eventsv1.CrawlPageCompletedV1](b.Events)
	case eventsv1.TopicSourcesDiscovered:
		body, err = encodeBatch[eventsv1.SourceDiscoveredV1](b.Events)
	default:
```

- [ ] **Step 3: Build + run writer tests**

```bash
go build ./apps/writer/...
go test ./apps/writer/... -count=1 -timeout 5m
```

Expected: no compile errors, existing tests pass. (The integration test in `apps/writer/service/service_test.go` does not exercise the new topics yet — that's covered by the e2e test in Task 11.)

- [ ] **Step 4: Commit**

```bash
git add apps/writer/service/handler.go apps/writer/service/service.go
git commit -m "feat(writer): encode crawl_page_completed + sources_discovered Parquet"
```

---

## Task 4: Add `Gate.Admit` for topic-aware admission

**Files:**
- Modify: `pkg/backpressure/gate.go`
- Modify: `pkg/backpressure/gate_test.go`

- [ ] **Step 1: Write failing tests**

Append to `pkg/backpressure/gate_test.go`:

```go
func TestAdmitOpenGrantsFullWant(t *testing.T) {
	// Construct a gate with a stub monitor that reports a small pending
	// count (well below LowWater). Admit should grant the full amount
	// requested.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"streams":[{"config":{"name":"svc"},"state":{"consumers":[{"name":"c","num_pending":10}]}}]}`))
	}))
	defer srv.Close()

	g := New(Config{
		MonitorURL:   srv.URL,
		StreamName:   "svc",
		ConsumerName: "c",
		HighWater:    100,
		LowWater:     50,
		CacheTTL:     0,
	}, srv.Client())

	granted, wait := g.Admit(context.Background(), "crawl.requests.v1", 20)
	if granted != 20 {
		t.Fatalf("granted=%d, want 20", granted)
	}
	if wait != 0 {
		t.Fatalf("wait=%v, want 0", wait)
	}
}

func TestAdmitPausedGrantsZero(t *testing.T) {
	// Stub monitor reports pending above HighWater; gate flips saturated.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"streams":[{"config":{"name":"svc"},"state":{"consumers":[{"name":"c","num_pending":200}]}}]}`))
	}))
	defer srv.Close()

	g := New(Config{
		MonitorURL:   srv.URL,
		StreamName:   "svc",
		ConsumerName: "c",
		HighWater:    100,
		LowWater:     50,
		CacheTTL:     0,
	}, srv.Client())

	granted, wait := g.Admit(context.Background(), "crawl.requests.v1", 20)
	if granted != 0 {
		t.Fatalf("granted=%d, want 0", granted)
	}
	if wait <= 0 {
		t.Fatalf("wait=%v, want positive hint", wait)
	}
}

func TestAdmitNoMonitorAlwaysOpen(t *testing.T) {
	// A gate with no monitor URL should always return the full amount
	// (fail-open matches the existing Check() behaviour).
	g := New(Config{HighWater: 100, LowWater: 50}, nil)
	granted, _ := g.Admit(context.Background(), "anything.v1", 5)
	if granted != 5 {
		t.Fatalf("granted=%d, want 5", granted)
	}
}
```

Imports to add at top of the test file:

```go
import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)
```

(Skip any that already exist.)

- [ ] **Step 2: Run — expect failure**

```bash
go test ./pkg/backpressure/... -run TestAdmit
```

Expected: build error — `g.Admit undefined`.

- [ ] **Step 3: Implement `Admit`**

Append to `pkg/backpressure/gate.go`:

```go
// Admit asks to emit `want` events on `topic`. For Phase 4 this is a
// minimal wrapper over the existing hysteresis gate:
//
//   - open (pending below HighWater / under hysteresis) → grant full want
//   - paused (pending above HighWater)                  → grant 0
//   - transport error reading the monitor               → grant full want (fail-open)
//
// The returned wait hint is a coarse "try again in ~30 s" when paused,
// and zero otherwise. Phase 6 replaces this with the full drain-time
// policy described in §8.3 of the design (per-topic thresholds, HPA-
// ceiling awareness). The method signature is chosen to match that
// future policy so the scheduler-tick endpoint doesn't need to change.
//
// `topic` is currently unused but accepted so callers don't have to
// change when per-topic policies land. Passing the real topic today
// keeps log lines correct.
func (g *Gate) Admit(ctx context.Context, topic string, want int) (int, time.Duration) {
	if want <= 0 {
		return 0, 0
	}
	if g == nil || g.monitorURL == "" {
		return want, 0
	}
	state, err := g.Check(ctx)
	if err != nil {
		// Fail-open matches Check()'s own contract (the existing code
		// already treats a monitor error as "assume open").
		return want, 0
	}
	if state.Paused {
		return 0, 30 * time.Second
	}
	return want, 0
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./pkg/backpressure/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/backpressure/gate.go pkg/backpressure/gate_test.go
git commit -m "feat(backpressure): add Admit(topic, want) wrapping hysteresis gate"
```

---

## Task 5: `/admin/scheduler/tick` handler

**Files:**
- Create: `apps/crawler/service/scheduler_tick.go`
- Create: `apps/crawler/service/scheduler_tick_test.go`

- [ ] **Step 1: Write the failing test**

Create `apps/crawler/service/scheduler_tick_test.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// admitterFunc is a minimal Admitter used for unit tests so we don't
// stand up a NATS monitor stub just to drive Admit.
type admitterFunc func(ctx context.Context, topic string, want int) (int, time.Duration)

func (f admitterFunc) Admit(ctx context.Context, topic string, want int) (int, time.Duration) {
	return f(ctx, topic, want)
}

// collector subscribes to a topic and records every envelope it sees.
type tickCollector struct {
	topic string
	got   []eventsv1.Envelope[eventsv1.CrawlRequestV1]
}

func (c *tickCollector) Name() string     { return c.topic }
func (c *tickCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *tickCollector) Validate(_ context.Context, _ any) error { return nil }
func (c *tickCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CrawlRequestV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.got = append(c.got, env)
	return nil
}

func TestSchedulerTickEmitsOneRequestPerAdmittedSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("scheduler-tick-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &tickCollector{topic: eventsv1.TopicCrawlRequests}
	if err := svc.EventsManager().Add(col); err != nil {
		t.Fatalf("add collector: %v", err)
	}

	// Run Frame so the in-memory subscriber becomes ready before
	// the scheduler emits. Same pattern as apps/writer/service_test.go.
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	// Seed two due sources in the fake repo (defined below).
	repo := newFakeSourceRepo(
		&domain.Source{BaseModel: domain.BaseModel{ID: "s1"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
		&domain.Source{BaseModel: domain.BaseModel{ID: "s2"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
	)

	admit := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) {
		return want, 0 // grant everything
	})

	handler := SchedulerTickHandler(svc, repo, admit)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/tick", nil)
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp schedulerTickResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Considered != 2 || resp.Admitted != 2 || resp.Dispatched != 2 {
		t.Fatalf("unexpected counts: %+v", resp)
	}

	// Both sources should have next_crawl_at pushed forward.
	for _, id := range []string{"s1", "s2"} {
		src, _ := repo.GetByID(ctx, id)
		if src == nil || !src.NextCrawlAt.After(time.Now().UTC()) {
			t.Fatalf("source %s next_crawl_at not advanced: %+v", id, src)
		}
	}

	// Give the in-memory pub/sub a moment to deliver.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(col.got) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(col.got) != 2 {
		t.Fatalf("emitted envelopes=%d, want 2", len(col.got))
	}
	for _, env := range col.got {
		if env.Payload.SourceID == "" || env.Payload.RequestID == "" {
			t.Fatalf("bad envelope: %+v", env)
		}
	}
}

func TestSchedulerTickRespectsAdmitDecisions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("scheduler-tick-deferred"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	if err := svc.EventsManager().Add(&tickCollector{topic: eventsv1.TopicCrawlRequests}); err != nil {
		t.Fatalf("add: %v", err)
	}

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	repo := newFakeSourceRepo(
		&domain.Source{BaseModel: domain.BaseModel{ID: "s1"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
		&domain.Source{BaseModel: domain.BaseModel{ID: "s2"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
		&domain.Source{BaseModel: domain.BaseModel{ID: "s3"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
	)

	admit := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) {
		return 1, 0 // only one of the three gets admitted this tick
	})

	handler := SchedulerTickHandler(svc, repo, admit)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/tick", nil)
	handler(rec, req)

	var resp schedulerTickResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Considered != 3 || resp.Admitted != 1 || resp.Dispatched != 1 {
		t.Fatalf("unexpected counts: %+v", resp)
	}

	// Only one source should have next_crawl_at advanced.
	advanced := 0
	for _, id := range []string{"s1", "s2", "s3"} {
		src, _ := repo.GetByID(ctx, id)
		if src != nil && src.NextCrawlAt.After(time.Now().UTC()) {
			advanced++
		}
	}
	if advanced != 1 {
		t.Fatalf("advanced=%d, want 1", advanced)
	}
}
```

The `SchedulerTickHandler` depends on narrow interfaces (`SourceLister`, `Admitter`) declared in `scheduler_tick.go`, so the test can use an inline fake instead of touching the real repository. Append the fake after the tests above, in the same file:

```go
// fakeSourceRepo implements SourceLister.
type fakeSourceRepo struct {
	mu   sync.Mutex
	rows map[string]*domain.Source
}

func newFakeSourceRepo(src ...*domain.Source) *fakeSourceRepo {
	r := &fakeSourceRepo{rows: make(map[string]*domain.Source, len(src))}
	for _, s := range src {
		r.rows[s.ID] = s
	}
	return r
}

func (r *fakeSourceRepo) ListDue(_ context.Context, _ time.Time, limit int) ([]*domain.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.Source, 0, len(r.rows))
	for _, s := range r.rows {
		out = append(out, s)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (r *fakeSourceRepo) UpdateNextCrawl(_ context.Context, id string, next, verified time.Time, health float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.NextCrawlAt = next
		s.LastVerifiedAt = &verified
		s.HealthScore = health
	}
	return nil
}

func (r *fakeSourceRepo) GetByID(_ context.Context, id string) (*domain.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.rows[id]
	if !ok {
		return nil, nil
	}
	// Return a copy so tests can compare before/after state.
	cp := *s
	return &cp, nil
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/crawler/service/... -run 'TestSchedulerTick'
```

Expected: `undefined: SchedulerTickHandler, schedulerTickResponse`.

- [ ] **Step 3: Implement the handler**

Create `apps/crawler/service/scheduler_tick.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// SourceLister is the narrow slice of *repository.SourceRepository the
// scheduler-tick handler needs. Kept here (rather than importing the
// full repo type) so unit tests can swap in a fake without touching
// Postgres.
type SourceLister interface {
	ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.Source, error)
	UpdateNextCrawl(ctx context.Context, id string, next, verified time.Time, health float64) error
}

// Admitter is the slice of *backpressure.Gate the handler needs.
type Admitter interface {
	Admit(ctx context.Context, topic string, want int) (int, time.Duration)
}

// schedulerTickResponse is the JSON body returned to Trustage. Kept
// exported-enough-for-tests via the lowercase struct + json tags.
type schedulerTickResponse struct {
	OK         bool `json:"ok"`
	Considered int  `json:"considered"`
	Admitted   int  `json:"admitted"`
	Dispatched int  `json:"dispatched"`

	// WaitHintSec is set when the gate refused and wants the caller
	// to back off. Trustage logs it; it does not re-drive retries.
	WaitHintSec int `json:"wait_hint_sec,omitempty"`
}

// SchedulerTickHandler returns an HTTP handler that implements the
// scheduler.tick contract from §6.1 of the design:
//
//  1. Enumerate due sources (up to limit) via SourceLister.ListDue.
//  2. Ask the Admitter how many of those N the pipeline can accept.
//  3. For the admitted K: emit one crawl.requests.v1 per source and
//     stamp next_crawl_at forward so a concurrent tick on another pod
//     can't double-admit.
//  4. Leave the deferred (N-K) sources untouched — the next tick
//     retries them.
//
// The handler is idempotent over the same wallclock window: if the
// Trustage-configured cadence is 30 s and the UpdateNextCrawl adds the
// source's CrawlIntervalSec (typically 60–7200 s), a second tick 30 s
// later will see no rows in ListDue for the same set of sources.
func SchedulerTickHandler(svc *frame.Service, lister SourceLister, admit Admitter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		log := util.Log(ctx)
		now := time.Now().UTC()

		sources, err := lister.ListDue(ctx, now, 500)
		if err != nil {
			log.WithError(err).Error("scheduler/tick: ListDue failed")
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		granted, wait := admit.Admit(ctx, eventsv1.TopicCrawlRequests, len(sources))

		resp := schedulerTickResponse{
			OK:         true,
			Considered: len(sources),
			Admitted:   granted,
		}
		if wait > 0 {
			resp.WaitHintSec = int(wait / time.Second)
		}

		evtMgr := svc.EventsManager()
		if evtMgr == nil {
			http.Error(w, `{"error":"events manager unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		for i := 0; i < granted && i < len(sources); i++ {
			src := sources[i]
			env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
				RequestID: xid.New().String(),
				SourceID:  src.ID,
				Mode:      "auto",
				Attempt:   1,
			})
			if emitErr := evtMgr.Emit(ctx, eventsv1.TopicCrawlRequests, env); emitErr != nil {
				log.WithError(emitErr).WithField("source_id", src.ID).Warn("scheduler/tick: emit failed")
				continue
			}

			// Stamp next_crawl_at forward so a concurrent pod's tick
			// doesn't re-admit this source. Keep health + verified
			// values unchanged.
			next := now.Add(time.Duration(src.CrawlIntervalSec) * time.Second)
			if stampErr := lister.UpdateNextCrawl(ctx, src.ID, next, now, src.HealthScore); stampErr != nil {
				log.WithError(stampErr).WithField("source_id", src.ID).Warn("scheduler/tick: stamp next_crawl_at failed")
			}
			resp.Dispatched++
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/crawler/service/... -run 'TestSchedulerTick' -count=1
```

Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add apps/crawler/service/scheduler_tick.go apps/crawler/service/scheduler_tick_test.go
git commit -m "feat(crawler): /admin/scheduler/tick emits crawl.requests.v1 under gate"
```

---

## Task 6: Crawl-request handler (fetch + archive + extract + emit)

**Files:**
- Create: `apps/crawler/service/crawl_request_handler.go`
- Create: `apps/crawler/service/crawl_request_handler_test.go`

This handler is the heart of the refactor. It replaces the `crawlDependencies.processSource` body in the old `apps/crawler/cmd/main.go`. The shape: accept a `CrawlRequestV1` envelope, load the source, run the connector, for each returned `ExternalJob` emit a `VariantIngestedV1` (**no Postgres write for the variant**), optionally sample DiscoverSites and emit `SourceDiscoveredV1`, emit one `CrawlPageCompletedV1` summary at the end.

- [ ] **Step 1: Write the failing test**

Create `apps/crawler/service/crawl_request_handler_test.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	"stawi.jobs/pkg/archive"
	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/content"
	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// --- fakes ---

type fakeConnector struct {
	jobs []domain.ExternalJob
	raw  []byte
}

func (f *fakeConnector) Type() domain.SourceType { return domain.SourceGenericHTML }
func (f *fakeConnector) Crawl(_ context.Context, _ domain.Source) connectors.CrawlIterator {
	return connectors.NewSinglePageIterator(f.jobs, f.raw, 200, nil)
}

type fakeArchive struct {
	mu     sync.Mutex
	bodies map[string][]byte
}

func newFakeArchive() *fakeArchive { return &fakeArchive{bodies: map[string][]byte{}} }

func (a *fakeArchive) PutRaw(_ context.Context, body []byte) (string, int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	hash := "h" + time.Now().UTC().Format("150405.000000000")
	a.bodies[hash] = body
	return hash, int64(len(body)), nil
}
func (a *fakeArchive) GetRaw(_ context.Context, hash string) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, ok := a.bodies[hash]
	if !ok {
		return nil, archive.ErrNotFound
	}
	return b, nil
}
func (a *fakeArchive) HasRaw(_ context.Context, hash string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.bodies[hash]
	return ok, nil
}
func (a *fakeArchive) PutCanonical(context.Context, string, archive.CanonicalSnapshot) error { return nil }
func (a *fakeArchive) GetCanonical(context.Context, string) (archive.CanonicalSnapshot, error) {
	return archive.CanonicalSnapshot{}, archive.ErrNotFound
}
func (a *fakeArchive) PutVariant(context.Context, string, string, archive.VariantBlob) error { return nil }
func (a *fakeArchive) GetVariant(context.Context, string, string) (archive.VariantBlob, error) {
	return archive.VariantBlob{}, archive.ErrNotFound
}
func (a *fakeArchive) PutManifest(context.Context, string, archive.Manifest) error { return nil }
func (a *fakeArchive) GetManifest(context.Context, string) (archive.Manifest, error) {
	return archive.Manifest{}, archive.ErrNotFound
}
func (a *fakeArchive) DeleteCluster(context.Context, string) error { return nil }
func (a *fakeArchive) DeleteRaw(context.Context, string) error     { return nil }

// fakeSourceGetter satisfies the handler's GetByID dependency.
type fakeSourceGetter struct {
	rows map[string]*domain.Source
}

func (g *fakeSourceGetter) GetByID(_ context.Context, id string) (*domain.Source, error) {
	s, ok := g.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

// collector captures envelopes for topic assertions.
type envCollector[P any] struct {
	topic string
	mu    sync.Mutex
	got   []eventsv1.Envelope[P]
}

func (c *envCollector[P]) Name() string { return c.topic }
func (c *envCollector[P]) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *envCollector[P]) Validate(context.Context, any) error { return nil }
func (c *envCollector[P]) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[P]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *envCollector[P]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

// --- test ---

func TestCrawlRequestHandlerEmitsVariantAndPageCompleted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-req-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	variantCol := &envCollector[eventsv1.VariantIngestedV1]{topic: eventsv1.TopicVariantsIngested}
	pageCol := &envCollector[eventsv1.CrawlPageCompletedV1]{topic: eventsv1.TopicCrawlPageCompleted}
	for _, c := range []frame.EventI{variantCol, pageCol} {
		if err := svc.EventsManager().Add(c); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	// Start Frame so subscriptions are live before Execute runs.
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	reg := connectors.NewRegistry()
	reg.Register(&fakeConnector{
		jobs: []domain.ExternalJob{{
			ExternalID: "ext-1",
			Title:      "Backend Engineer",
			Company:    "Acme",
			ApplyURL:   "https://acme.example/jobs/ext-1",
		}},
		raw: []byte("<html>job body</html>"),
	})

	srcs := &fakeSourceGetter{
		rows: map[string]*domain.Source{
			"s1": {
				BaseModel: domain.BaseModel{ID: "s1"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://acme.example/jobs",
				Status:    domain.SourceActive,
				Country:   "KE",
				Language:  "en",
			},
		},
	}

	h := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc:            svc,
		Sources:        srcs,
		Registry:       reg,
		Archive:        newFakeArchive(),
		Extractor:      nil, // nil extractor → no AI enrichment, deterministic path
		DiscoverSample: 0,   // disable sampling in this test
	})

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
		RequestID: "req-1",
		SourceID:  "s1",
		Mode:      "auto",
		Attempt:   1,
	})
	raw, _ := json.Marshal(env)
	if err := h.Execute(ctx, (*json.RawMessage)(&raw)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if variantCol.Len() == 1 && pageCol.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if variantCol.Len() != 1 {
		t.Fatalf("variant events=%d, want 1", variantCol.Len())
	}
	if pageCol.Len() != 1 {
		t.Fatalf("page-completed events=%d, want 1", pageCol.Len())
	}

	v := variantCol.got[0].Payload
	if v.VariantID == "" || v.HardKey == "" {
		t.Fatalf("variant missing ids: %+v", v)
	}
	if v.SourceID != "s1" || v.Title != "Backend Engineer" {
		t.Fatalf("variant content lost: %+v", v)
	}
	if v.RawArchiveRef == "" {
		t.Fatalf("variant missing raw_archive_ref")
	}

	pc := pageCol.got[0].Payload
	if pc.SourceID != "s1" || pc.RequestID != "req-1" {
		t.Fatalf("page-completed ids wrong: %+v", pc)
	}
	if pc.JobsFound != 1 || pc.JobsEmitted != 1 || pc.JobsRejected != 0 {
		t.Fatalf("page-completed counts wrong: %+v", pc)
	}
}

func TestCrawlRequestHandlerUnknownSourceEmitsErrorCompleted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-req-unknown"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	pageCol := &envCollector[eventsv1.CrawlPageCompletedV1]{topic: eventsv1.TopicCrawlPageCompleted}
	_ = svc.EventsManager().Add(pageCol)

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	h := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc:      svc,
		Sources:  &fakeSourceGetter{rows: map[string]*domain.Source{}},
		Registry: connectors.NewRegistry(),
		Archive:  newFakeArchive(),
	})

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
		RequestID: "req-unknown",
		SourceID:  "missing",
		Mode:      "auto",
	})
	raw, _ := json.Marshal(env)
	if err := h.Execute(ctx, (*json.RawMessage)(&raw)); err != nil && !errors.Is(err, errNoSource) {
		t.Fatalf("unexpected Execute err: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pageCol.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pageCol.Len() != 1 {
		t.Fatalf("expected one page-completed event, got %d", pageCol.Len())
	}
	if got := pageCol.got[0].Payload.ErrorCode; got != "source_not_found" {
		t.Fatalf("error_code=%q, want source_not_found", got)
	}

	_ = content.Extracted{} // keep content import alive for fakeConnector file
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/crawler/service/... -run TestCrawlRequestHandler
```

Expected: `undefined: NewCrawlRequestHandler, CrawlRequestDeps, errNoSource`.

- [ ] **Step 3: Implement the handler**

Create `apps/crawler/service/crawl_request_handler.go`:

```go
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"stawi.jobs/pkg/archive"
	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/normalize"
	"stawi.jobs/pkg/quality"
)

// SourceGetter is the narrow repository slice the crawl-request handler
// needs. Satisfied by *repository.SourceRepository in production.
type SourceGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
}

// CrawlRequestDeps bundles the handler's collaborators so construction
// stays one-shot and tests can inject fakes without ceremony.
type CrawlRequestDeps struct {
	Svc       *frame.Service
	Sources   SourceGetter
	Registry  *connectors.Registry
	Archive   archive.Archive
	Extractor *extraction.Extractor // nil → skip AI enrichment
	// DiscoverSample is the probability [0..1] that a successful
	// listing page is additionally run through DiscoverSites. 0.0 =
	// disabled (unit-test default). In production, set ~0.05 so roughly
	// one in twenty crawls attempts site discovery.
	DiscoverSample float64
	// Rand is optional; nil uses a package-local deterministic source
	// seeded in init(). Tests can inject a fixed seed to make sampling
	// predictable.
	Rand *rand.Rand
}

// CrawlRequestHandler consumes jobs.crawl.requests.v1, runs the
// connector iterator for the source, archives raw HTML, optionally
// extracts job fields via AI, and emits:
//
//   - one jobs.variants.ingested.v1 per accepted job
//   - optionally one sources.discovered.v1 per DiscoverSites hit
//   - exactly one crawl.page.completed.v1 summary per request
type CrawlRequestHandler struct {
	deps CrawlRequestDeps
}

// NewCrawlRequestHandler wires the handler. Deps are captured by value
// because nothing in the struct is mutable between calls.
func NewCrawlRequestHandler(deps CrawlRequestDeps) *CrawlRequestHandler {
	if deps.Rand == nil {
		deps.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &CrawlRequestHandler{deps: deps}
}

// errNoSource is returned when the referenced source is missing.
// Non-retryable — Execute still emits a page-completed summary with
// error_code="source_not_found" so the event log has an audit row.
var errNoSource = errors.New("crawl.request: source not found")

// Name implements frame.EventI.
func (h *CrawlRequestHandler) Name() string { return eventsv1.TopicCrawlRequests }

// PayloadType returns a pointer to json.RawMessage so Frame skips
// payload-specific deserialization; Execute does the typed decode.
func (h *CrawlRequestHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate is a cheap shape check — an empty payload is a bug and
// should dead-letter.
func (h *CrawlRequestHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("crawl.request: empty payload")
	}
	return nil
}

// Execute processes one crawl request. Error returns trigger Frame
// redelivery. Non-retryable conditions (unknown source, unknown
// connector type) return nil after emitting a page-completed event
// with error_code set — they are data-plane outcomes, not transport
// failures.
func (h *CrawlRequestHandler) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("crawl.request: wrong payload type")
	}
	var env eventsv1.Envelope[eventsv1.CrawlRequestV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("crawl.request: decode envelope: %w", err)
	}
	req := env.Payload

	log := util.Log(ctx).
		WithField("request_id", req.RequestID).
		WithField("source_id", req.SourceID)

	evtMgr := h.deps.Svc.EventsManager()
	if evtMgr == nil {
		return errors.New("crawl.request: events manager unavailable")
	}

	src, err := h.deps.Sources.GetByID(ctx, req.SourceID)
	if err != nil {
		// Transient; let Frame redeliver.
		return fmt.Errorf("crawl.request: GetByID: %w", err)
	}
	if src == nil {
		h.emitCompleted(ctx, eventsv1.CrawlPageCompletedV1{
			RequestID: req.RequestID,
			SourceID:  req.SourceID,
			ErrorCode: "source_not_found",
		})
		log.Warn("crawl.request: source not found")
		return nil
	}
	if src.Status != domain.SourceActive && src.Status != domain.SourceDegraded {
		h.emitCompleted(ctx, eventsv1.CrawlPageCompletedV1{
			RequestID: req.RequestID,
			SourceID:  req.SourceID,
			URL:       src.BaseURL,
			ErrorCode: "source_not_eligible",
		})
		return nil
	}

	conn, ok := h.deps.Registry.Get(src.Type)
	if !ok {
		h.emitCompleted(ctx, eventsv1.CrawlPageCompletedV1{
			RequestID: req.RequestID,
			SourceID:  req.SourceID,
			URL:       src.BaseURL,
			ErrorCode: "connector_not_registered",
		})
		log.WithField("source_type", src.Type).Warn("crawl.request: no connector")
		return nil
	}

	iter := conn.Crawl(ctx, *src)

	var (
		jobsFound    int
		jobsEmitted  int
		jobsRejected int
		lastCursor   string
		iterErr      error
		status       = http.StatusOK
	)

	for iter.Next(ctx) {
		status = iter.HTTPStatus()
		for _, extJob := range iter.Jobs() {
			jobsFound++

			// Convert to a VariantIngested payload via the existing
			// normalize helper. This keeps hard-key derivation, stage
			// defaulting, and ExtendedFields-to-JobFields mapping in
			// one place.
			variant := normalize.ExternalToVariant(
				extJob, src.ID, src.Country, string(src.Type), src.Language,
				time.Now().UTC(),
			)

			// Fallback apply-URL chain (same rules as the legacy path).
			quality.EnsureApplyURL(&extJob, extJob.SourceURL)
			if extJob.ApplyURL == "" {
				quality.EnsureApplyURL(&extJob, src.BaseURL)
			}
			if extJob.ApplyURL != "" {
				variant.ApplyURL = extJob.ApplyURL
			}

			// Deterministic quality gate — same check the legacy
			// crawler ran. Rejected jobs are counted and dropped; no
			// dead-letter topic at this layer (downstream validate
			// stage is the place for AI-driven flags).
			if qErr := quality.Check(extJob); qErr != nil {
				jobsRejected++
				continue
			}

			// Archive raw bytes. PutRaw is content-addressed — a
			// repeat crawl of an unchanged page re-uses the stored
			// blob. rawArchiveRef is the R2 key, carried through to
			// the event log for downstream replay.
			var rawArchiveRef string
			if pageContent := iter.Content(); pageContent != nil && len(pageContent.RawHTML) > 0 {
				body := []byte(pageContent.RawHTML)
				hash := sha256Hex(body)
				if has, hasErr := h.deps.Archive.HasRaw(ctx, hash); hasErr == nil && !has {
					if putHash, _, putErr := h.deps.Archive.PutRaw(ctx, body); putErr == nil {
						rawArchiveRef = archive.RawKey(putHash)
					}
				} else {
					rawArchiveRef = archive.RawKey(hash)
				}
			}

			// Build the event payload from the domain variant. Fields
			// the pipeline does not need at ingest (extended JobFields,
			// intelligence signals) land in later events — spec §5.2
			// keeps variant-ingested narrow.
			now := time.Now().UTC()
			payload := eventsv1.VariantIngestedV1{
				VariantID:      xid.New().String(),
				SourceID:       src.ID,
				ExternalID:     variant.ExternalJobID,
				HardKey:        variant.HardKey,
				Stage:          string(domain.StageRaw),
				Title:          variant.Title,
				Company:        variant.Company,
				LocationText:   variant.LocationText,
				Country:        variant.Country,
				Language:       variant.Language,
				RemoteType:     variant.RemoteType,
				EmploymentType: variant.EmploymentType,
				SalaryMin:      variant.SalaryMin,
				SalaryMax:      variant.SalaryMax,
				Currency:       variant.Currency,
				Description:    variant.Description,
				ApplyURL:       variant.ApplyURL,
				ScrapedAt:      now,
				ContentHash:    variant.RawContentHash,
				RawArchiveRef:  rawArchiveRef,
			}
			if variant.PostedAt != nil {
				payload.PostedAt = *variant.PostedAt
			}
			if h.deps.Extractor != nil {
				// Model version is only populated when AI enrichment ran.
				// In Phase 4 the enrichment decision lives in the worker's
				// normalize/validate stages; the crawler only stamps the
				// extractor model when Extract was invoked (§6.1 of spec).
				// For now leave empty and add enrichment in the next task
				// series if/when the per-page model lands.
			}

			if emitErr := evtMgr.Emit(ctx, eventsv1.TopicVariantsIngested,
				eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, payload),
			); emitErr != nil {
				log.WithError(emitErr).Warn("crawl.request: emit variant failed")
				continue
			}
			jobsEmitted++
		}

		// Capture the connector's cursor on every successful page; the
		// value at loop end is the source's pagination state.
		if cur := iter.Cursor(); cur != nil {
			lastCursor = string(cur)
		}

		// Opportunistic DiscoverSites — sampled so we don't triple the
		// AI bill. Operates on the last page's raw HTML (iter.Content
		// is already parsed; use RawPayload for the unprocessed bytes).
		if h.deps.Extractor != nil && h.deps.DiscoverSample > 0 &&
			h.deps.Rand.Float64() < h.deps.DiscoverSample {
			h.sampleDiscoverSites(ctx, src, iter.RawPayload())
		}
	}

	if e := iter.Err(); e != nil {
		iterErr = e
		log.WithError(e).Warn("crawl.request: iterator failed")
	}

	completed := eventsv1.CrawlPageCompletedV1{
		RequestID:    req.RequestID,
		SourceID:     src.ID,
		URL:          src.BaseURL,
		HTTPStatus:   status,
		JobsFound:    jobsFound,
		JobsEmitted:  jobsEmitted,
		JobsRejected: jobsRejected,
		Cursor:       lastCursor,
	}
	if iterErr != nil {
		completed.ErrorCode = "iterator_failed"
		completed.ErrorMessage = iterErr.Error()
	}
	h.emitCompleted(ctx, completed)

	log.WithField("found", jobsFound).
		WithField("emitted", jobsEmitted).
		WithField("rejected", jobsRejected).
		Info("crawl.request: done")

	return nil
}

// sampleDiscoverSites is best-effort. DiscoverSites cost is not on the
// user hot path and we never want a discovery failure to fail the
// surrounding crawl — swallow errors.
func (h *CrawlRequestHandler) sampleDiscoverSites(ctx context.Context, src *domain.Source, raw []byte) {
	if h.deps.Extractor == nil || len(raw) == 0 {
		return
	}
	sites, err := h.deps.Extractor.DiscoverSites(ctx, string(raw), src.BaseURL)
	if err != nil {
		util.Log(ctx).WithError(err).Debug("crawl.request: DiscoverSites failed (sampled)")
		return
	}
	evtMgr := h.deps.Svc.EventsManager()
	for _, site := range sites {
		env := eventsv1.NewEnvelope(eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
			DiscoveredURL: site.URL,
			Name:          site.Name,
			Country:       site.Country,
			Type:          site.Type,
			SourceID:      src.ID,
		})
		_ = evtMgr.Emit(ctx, eventsv1.TopicSourcesDiscovered, env)
	}
}

// emitCompleted publishes a CrawlPageCompletedV1 envelope. Best-
// effort — if emission fails (rare; Frame transport down) we log and
// return. Next crawl picks up the source again via scheduler tick.
func (h *CrawlRequestHandler) emitCompleted(ctx context.Context, payload eventsv1.CrawlPageCompletedV1) {
	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, payload)
	if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCrawlPageCompleted, env); err != nil {
		util.Log(ctx).WithError(err).WithField("source_id", payload.SourceID).
			Warn("crawl.request: emit page-completed failed")
	}
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/crawler/service/... -run TestCrawlRequestHandler -count=1 -timeout 2m
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/crawler/service/crawl_request_handler.go apps/crawler/service/crawl_request_handler_test.go
git commit -m "feat(crawler): crawl.requests.v1 handler — archive, extract, emit variants"
```

---

## Task 7: Page-completed self-consumer — advance source cursor + health

**Files:**
- Create: `apps/crawler/service/page_completed_handler.go`

This handler replaces the inline `sourceRepo.RecordSuccess` / `RecordFailure` / `UpdateNextCrawl` calls that used to run inside `processSource`. It runs in the same binary as the producer; Frame delivers the event via a separate consumer group so a slow page-completed update doesn't back-pressure the crawl path itself.

- [ ] **Step 1: Write failing test**

Append to a new file `apps/crawler/service/page_completed_handler_test.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// fakeSourceHealthRepo captures calls so tests can assert which
// "record" method ran.
type fakeSourceHealthRepo struct {
	success  []string
	failure  []string
	tuning   map[string]bool
	nextSeen map[string]time.Time
}

func newFakeHealthRepo() *fakeSourceHealthRepo {
	return &fakeSourceHealthRepo{
		tuning:   map[string]bool{},
		nextSeen: map[string]time.Time{},
	}
}

func (r *fakeSourceHealthRepo) GetByID(_ context.Context, id string) (*domain.Source, error) {
	return &domain.Source{BaseModel: domain.BaseModel{ID: id}, CrawlIntervalSec: 60, HealthScore: 1.0}, nil
}
func (r *fakeSourceHealthRepo) RecordSuccess(_ context.Context, id string, _ float64) error {
	r.success = append(r.success, id); return nil
}
func (r *fakeSourceHealthRepo) RecordFailure(_ context.Context, id string, _ float64, _ int) error {
	r.failure = append(r.failure, id); return nil
}
func (r *fakeSourceHealthRepo) FlagNeedsTuning(_ context.Context, id string, flag bool) error {
	r.tuning[id] = flag; return nil
}
func (r *fakeSourceHealthRepo) UpdateNextCrawl(_ context.Context, id string, next, _ time.Time, _ float64) error {
	r.nextSeen[id] = next; return nil
}

func TestPageCompletedSuccessRecordsSuccess(t *testing.T) {
	repo := newFakeHealthRepo()
	h := NewPageCompletedHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		SourceID: "s1", JobsFound: 10, JobsEmitted: 9, JobsRejected: 1,
	})
	raw, _ := json.Marshal(env)
	if err := h.Execute(context.Background(), (*json.RawMessage)(&raw)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(repo.success) != 1 || repo.success[0] != "s1" {
		t.Fatalf("success not recorded: %+v", repo.success)
	}
	if len(repo.failure) != 0 {
		t.Fatalf("failure unexpectedly recorded: %+v", repo.failure)
	}
	if _, ok := repo.nextSeen["s1"]; !ok {
		t.Fatalf("next_crawl_at not set")
	}
}

func TestPageCompletedIteratorErrorRecordsFailure(t *testing.T) {
	repo := newFakeHealthRepo()
	h := NewPageCompletedHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		SourceID: "s2", ErrorCode: "iterator_failed", ErrorMessage: "timeout",
	})
	raw, _ := json.Marshal(env)
	if err := h.Execute(context.Background(), (*json.RawMessage)(&raw)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(repo.success) != 0 {
		t.Fatalf("success unexpectedly recorded: %+v", repo.success)
	}
	if len(repo.failure) != 1 {
		t.Fatalf("failure not recorded: %+v", repo.failure)
	}
}

func TestPageCompletedHighRejectRateFlagsTuning(t *testing.T) {
	repo := newFakeHealthRepo()
	h := NewPageCompletedHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		SourceID: "s3", JobsFound: 10, JobsEmitted: 1, JobsRejected: 9, // 90% rejects
	})
	raw, _ := json.Marshal(env)
	_ = h.Execute(context.Background(), (*json.RawMessage)(&raw))

	if !repo.tuning["s3"] {
		t.Fatalf("needs_tuning not flagged")
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/crawler/service/... -run TestPageCompleted
```

Expected: `undefined: NewPageCompletedHandler`.

- [ ] **Step 3: Implement the handler**

Create `apps/crawler/service/page_completed_handler.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// SourceHealthRepo is the slice of SourceRepository used to reconcile
// a crawl's outcome back onto the Postgres sources row. Narrow so
// tests can fake it without ceremony.
type SourceHealthRepo interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
	RecordSuccess(ctx context.Context, id string, newHealth float64) error
	RecordFailure(ctx context.Context, id string, newHealth float64, consecutive int) error
	FlagNeedsTuning(ctx context.Context, id string, flag bool) error
	UpdateNextCrawl(ctx context.Context, id string, next, verified time.Time, health float64) error
}

// PageCompletedHandler consumes crawl.page.completed.v1 and updates
// the sources row's health_score / consecutive_failures / needs_tuning
// / next_crawl_at fields. The logic mirrors the legacy inline updates
// in apps/crawler/cmd/main.go's processSource, split out so the crawl
// hot path is pure emit + the reconciliation runs on its own consumer
// group (its slowness never back-pressures fetches).
type PageCompletedHandler struct {
	repo SourceHealthRepo
}

// NewPageCompletedHandler wires the handler.
func NewPageCompletedHandler(repo SourceHealthRepo) *PageCompletedHandler {
	return &PageCompletedHandler{repo: repo}
}

// Name implements frame.EventI.
func (h *PageCompletedHandler) Name() string { return eventsv1.TopicCrawlPageCompleted }

// PayloadType returns a raw message — typed decode happens inside Execute.
func (h *PageCompletedHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ensures the payload is non-empty.
func (h *PageCompletedHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("page-completed: empty payload")
	}
	return nil
}

// Execute applies success/failure bookkeeping and stamps next_crawl_at.
func (h *PageCompletedHandler) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("page-completed: wrong payload type")
	}
	var env eventsv1.Envelope[eventsv1.CrawlPageCompletedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("page-completed: decode: %w", err)
	}
	p := env.Payload

	log := util.Log(ctx).WithField("source_id", p.SourceID).WithField("request_id", p.RequestID)

	src, err := h.repo.GetByID(ctx, p.SourceID)
	if err != nil {
		return fmt.Errorf("page-completed: GetByID: %w", err)
	}
	if src == nil {
		log.Warn("page-completed: source vanished; dropping")
		return nil
	}

	now := time.Now().UTC()
	next := now.Add(time.Duration(src.CrawlIntervalSec) * time.Second)

	rejectRate := 0.0
	if p.JobsFound > 0 {
		rejectRate = float64(p.JobsRejected) / float64(p.JobsFound)
	}

	switch {
	case p.ErrorCode != "":
		// Connection/iterator failure — circuit-breaker style decay.
		newHealth := src.HealthScore - 0.2
		if newHealth < 0 {
			newHealth = 0
		}
		if err := h.repo.RecordFailure(ctx, src.ID, newHealth, src.ConsecutiveFailures+1); err != nil {
			log.WithError(err).Warn("page-completed: RecordFailure failed")
		}
		if err := h.repo.UpdateNextCrawl(ctx, src.ID, next, now, newHealth); err != nil {
			log.WithError(err).Warn("page-completed: UpdateNextCrawl failed (err path)")
		}

	case rejectRate > 0.8 && p.JobsFound > 0:
		// High reject rate — flag for review but keep crawling.
		if err := h.repo.FlagNeedsTuning(ctx, src.ID, true); err != nil {
			log.WithError(err).Warn("page-completed: FlagNeedsTuning failed")
		}
		if err := h.repo.UpdateNextCrawl(ctx, src.ID, next, now, src.HealthScore); err != nil {
			log.WithError(err).Warn("page-completed: UpdateNextCrawl failed (tuning path)")
		}

	default:
		newHealth := src.HealthScore + 0.1
		if newHealth > 1.0 {
			newHealth = 1.0
		}
		if err := h.repo.RecordSuccess(ctx, src.ID, newHealth); err != nil {
			log.WithError(err).Warn("page-completed: RecordSuccess failed")
		}
		if src.NeedsTuning && rejectRate < 0.5 {
			_ = h.repo.FlagNeedsTuning(ctx, src.ID, false)
		}
		if err := h.repo.UpdateNextCrawl(ctx, src.ID, next, now, newHealth); err != nil {
			log.WithError(err).Warn("page-completed: UpdateNextCrawl failed (success path)")
		}
	}

	return nil
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/crawler/service/... -run TestPageCompleted -count=1
```

Expected: PASS for all three cases.

- [ ] **Step 5: Commit**

```bash
git add apps/crawler/service/page_completed_handler.go apps/crawler/service/page_completed_handler_test.go
git commit -m "feat(crawler): page-completed self-consumer advances source cursor + health"
```

---

## Task 8: Source-discovered self-consumer — upsert new sources

**Files:**
- Create: `apps/crawler/service/source_discovered_handler.go`
- Create: `apps/crawler/service/source_discovered_handler_test.go`

This replaces the legacy `pkg/pipeline/handlers/source_expand.go` handler. The domain filters (blocked domains, self-domain avoidance, HEAD redirect following) are preserved — this just adapts the payload shape from `SourceURLsPayload` to the v1 `SourceDiscoveredV1` envelope and drops the batching (each discovered URL is its own event now).

- [ ] **Step 1: Write failing test**

Create `apps/crawler/service/source_discovered_handler_test.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"testing"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

type fakeSourceUpserter struct {
	upserts []string
	rows    map[string]*domain.Source
}

func newFakeUpserter() *fakeSourceUpserter {
	return &fakeSourceUpserter{rows: map[string]*domain.Source{}}
}

func (r *fakeSourceUpserter) GetByID(_ context.Context, id string) (*domain.Source, error) {
	s, ok := r.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}
func (r *fakeSourceUpserter) Upsert(_ context.Context, s *domain.Source) error {
	r.upserts = append(r.upserts, s.BaseURL)
	r.rows[s.BaseURL] = s
	return nil
}

func TestSourceDiscoveredUpsertsNewURL(t *testing.T) {
	repo := newFakeUpserter()
	repo.rows["s-origin"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s-origin"},
		BaseURL:   "https://example.com",
	}
	h := NewSourceDiscoveredHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
		DiscoveredURL: "https://anotherboard.example/careers",
		SourceID:      "s-origin",
		Country:       "KE",
	})
	raw, _ := json.Marshal(env)
	if err := h.Execute(context.Background(), (*json.RawMessage)(&raw)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(repo.upserts) != 1 {
		t.Fatalf("upserts=%v, want one", repo.upserts)
	}
}

func TestSourceDiscoveredSkipsSameDomain(t *testing.T) {
	repo := newFakeUpserter()
	repo.rows["s-origin"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s-origin"},
		BaseURL:   "https://example.com",
	}
	h := NewSourceDiscoveredHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
		DiscoveredURL: "https://example.com/another-page",
		SourceID:      "s-origin",
	})
	raw, _ := json.Marshal(env)
	_ = h.Execute(context.Background(), (*json.RawMessage)(&raw))

	if len(repo.upserts) != 0 {
		t.Fatalf("same-domain URL must be skipped, got upserts=%v", repo.upserts)
	}
}

func TestSourceDiscoveredSkipsBlocklistedDomain(t *testing.T) {
	repo := newFakeUpserter()
	repo.rows["s-origin"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s-origin"},
		BaseURL:   "https://example.com",
	}
	h := NewSourceDiscoveredHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicSourcesDiscovered, eventsv1.SourceDiscoveredV1{
		DiscoveredURL: "https://linkedin.com/jobs/apply/foo",
		SourceID:      "s-origin",
	})
	raw, _ := json.Marshal(env)
	_ = h.Execute(context.Background(), (*json.RawMessage)(&raw))

	if len(repo.upserts) != 0 {
		t.Fatalf("blocked domain must be skipped, got upserts=%v", repo.upserts)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/crawler/service/... -run TestSourceDiscovered
```

Expected: `undefined: NewSourceDiscoveredHandler`.

- [ ] **Step 3: Implement the handler**

Create `apps/crawler/service/source_discovered_handler.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// SourceUpserter is the narrow slice of SourceRepository used by the
// source-discovered handler.
type SourceUpserter interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
	Upsert(ctx context.Context, src *domain.Source) error
}

// blockedDiscoveredDomains mirrors the blocklist in the legacy
// pkg/pipeline/handlers/source_expand.go. Kept local so this file can
// be tested without importing the legacy handler, and so the legacy
// file can be deleted in Phase 6 without touching Plan 4 code.
var blockedDiscoveredDomains = map[string]bool{
	"google.com":      true,
	"facebook.com":    true,
	"twitter.com":     true,
	"instagram.com":   true,
	"youtube.com":     true,
	"wikipedia.org":   true,
	"linkedin.com":    true,
	"github.com":      true,
	"apple.com":       true,
	"play.google.com": true,
	"apps.apple.com":  true,
	"stawi.org":       true,
	"stawi.jobs":      true,
	"pages.dev":       true,
}

// SourceDiscoveredHandler consumes sources.discovered.v1 and upserts
// the target URL as a `generic-html` source. Same blocklist + self-
// domain filter as the legacy SourceExpansionHandler; payload shape
// changed from SourceURLsPayload (batched) to one event per URL.
type SourceDiscoveredHandler struct {
	repo SourceUpserter
}

// NewSourceDiscoveredHandler wires the handler.
func NewSourceDiscoveredHandler(repo SourceUpserter) *SourceDiscoveredHandler {
	return &SourceDiscoveredHandler{repo: repo}
}

// Name implements frame.EventI.
func (h *SourceDiscoveredHandler) Name() string { return eventsv1.TopicSourcesDiscovered }

// PayloadType yields a raw message for typed decode inside Execute.
func (h *SourceDiscoveredHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ensures we have a parseable envelope.
func (h *SourceDiscoveredHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("source-discovered: empty payload")
	}
	return nil
}

// Execute upserts the discovered URL as a new source, unless the URL
// resolves to the same domain as the origin source or is on the
// block list.
func (h *SourceDiscoveredHandler) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("source-discovered: wrong payload type")
	}
	var env eventsv1.Envelope[eventsv1.SourceDiscoveredV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("source-discovered: decode: %w", err)
	}
	p := env.Payload

	log := util.Log(ctx).WithField("origin_id", p.SourceID).WithField("url", p.DiscoveredURL)

	origin, err := h.repo.GetByID(ctx, p.SourceID)
	if err != nil {
		return fmt.Errorf("source-discovered: GetByID: %w", err)
	}
	if origin == nil {
		log.Warn("source-discovered: origin source missing; dropping")
		return nil
	}

	target, err := url.Parse(p.DiscoveredURL)
	if err != nil || target.Host == "" {
		log.Debug("source-discovered: unparseable URL")
		return nil
	}
	host := strings.ToLower(strings.TrimPrefix(target.Hostname(), "www."))

	originHost := ""
	if ou, perr := url.Parse(origin.BaseURL); perr == nil {
		originHost = strings.ToLower(strings.TrimPrefix(ou.Hostname(), "www."))
	}
	if host == originHost {
		return nil
	}

	if isBlockedHost(host) {
		return nil
	}

	baseURL := fmt.Sprintf("%s://%s", target.Scheme, target.Host)
	newSrc := &domain.Source{
		Type:             domain.SourceGenericHTML,
		BaseURL:          baseURL,
		Country:          p.Country,
		Status:           domain.SourceActive,
		Priority:         domain.PriorityNormal,
		CrawlIntervalSec: 7200,
		HealthScore:      1.0,
		Config:           "{}",
	}
	if err := h.repo.Upsert(ctx, newSrc); err != nil {
		log.WithError(err).Warn("source-discovered: upsert failed")
		return nil
	}
	log.Info("source-discovered: upserted new source")
	return nil
}

func isBlockedHost(host string) bool {
	if blockedDiscoveredDomains[host] {
		return true
	}
	for blocked := range blockedDiscoveredDomains {
		if strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/crawler/service/... -run TestSourceDiscovered -count=1
```

Expected: PASS for all three cases.

- [ ] **Step 5: Commit**

```bash
git add apps/crawler/service/source_discovered_handler.go apps/crawler/service/source_discovered_handler_test.go
git commit -m "feat(crawler): sources.discovered.v1 self-consumer — upsert new sources"
```

---

## Task 9: Refactor `apps/crawler/cmd/main.go`

**Files:**
- Modify: `apps/crawler/cmd/main.go`

The entrypoint currently registers **seven** legacy handlers (dedup, normalize, validate, canonical, source-expansion, source-quality, publish, translate) plus the legacy `crawlDependencies` that subscribes to `handlers.EventCrawlRequest` and writes variants to Postgres. After this task the entrypoint registers only the three new v1 handlers and exposes the new `/admin/scheduler/tick` endpoint. The legacy admin endpoints `/admin/crawl/dispatch` and `/admin/crawl/dispatch-due` go away; `/admin/crawl/status` stays because dashboards read it.

- [ ] **Step 1: Drop legacy handler wiring**

Remove the block in `apps/crawler/cmd/main.go` that builds `pipelineHandlers` and calls `svc.Init(ctx, pipelineHandlers...)`. Concretely: delete the entire block from `// Register pipeline stage handlers.` through the second `svc.Init(ctx, pipelineHandlers...)` call (roughly lines 241–268 in the current file — verify with `git blame` before editing).

Also delete the `stuck-variant recovery` AddPreStartMethod block (roughly lines 271–287). That block emits legacy `variant.raw.stored` events for stuck variants; with the new pipeline there are no intermediate Postgres stages to recover from.

- [ ] **Step 2: Drop the legacy `crawlDependencies` type and its registration**

Delete:

- The `_ = retentionRepo` / `_ = facetRepo` comment block preceding `crawlDeps`.
- The construction `crawlDeps := &crawlDependencies{…}` and `svc.Init(ctx, frame.WithRegisterEvents(crawlDeps))`.
- The `crawlDependencies` struct definition and all of its methods (`Name`, `PayloadType`, `Validate`, `Execute`, `processSource`).
- The `sha256Hex` helper (it now lives in `apps/crawler/service/crawl_request_handler.go`).
- The `stageToEventName` helper.

After deletion, `main.go` should end (before the health-checker struct) at the admin-mux registrations.

- [ ] **Step 3: Drop the legacy admin endpoints**

Delete the handler registrations for:
- `POST /admin/crawl/dispatch`
- `POST /admin/crawl/dispatch-due`

Keep:
- `GET  /admin/sources/due` (used by dashboards + Plan 4's new tick reads the repo directly so this endpoint is purely operator-facing)
- `GET  /admin/crawl/status`
- `POST /admin/sources/pause|enable`
- `GET  /admin/sources/health`
- `POST /admin/rebuild-canonicals` (operator emergency tool, untouched by Plan 4)
- `POST /admin/retention/*`
- `POST /admin/r2/purge|reconcile`

- [ ] **Step 4: Wire the three new Frame handlers + the scheduler-tick endpoint**

Insert the new wiring at the point the legacy handlers used to be initialized. Add to imports:

```go
crawlersvc "stawi.jobs/apps/crawler/service"
```

Then replace the deleted block with:

```go
// ── Phase 4 event handlers ──────────────────────────────────────
//
// Three internal subscriptions. Frame gives each its own consumer
// group so a slow reconciliation never back-pressures the fetch path.
//
//   crawl.requests.v1      → CrawlRequestHandler (fetch + archive + extract + emit)
//   crawl.page.completed.v1 → PageCompletedHandler (self-consumed; cursor + health)
//   sources.discovered.v1   → SourceDiscoveredHandler (self-consumed; upsert)
crawlReqH := crawlersvc.NewCrawlRequestHandler(crawlersvc.CrawlRequestDeps{
	Svc:            svc,
	Sources:        sourceRepo,
	Registry:       registry,
	Archive:        arch,
	Extractor:      extractor,
	DiscoverSample: 0.05, // roughly 1-in-20 pages get DiscoverSites
})
pageDoneH := crawlersvc.NewPageCompletedHandler(sourceRepo)
srcDiscH := crawlersvc.NewSourceDiscoveredHandler(sourceRepo)

svc.Init(ctx, frame.WithRegisterEvents(crawlReqH, pageDoneH, srcDiscH))
```

The passed `sourceRepo` already satisfies `SourceGetter`, `SourceHealthRepo`, and `SourceUpserter` because those interfaces were chosen to match its method set (confirm by grepping `pkg/repository/source_repository.go` for `GetByID`, `RecordSuccess`, `RecordFailure`, `FlagNeedsTuning`, `UpdateNextCrawl`, `Upsert` — the interfaces exist precisely to name the existing methods without dragging the full repo interface into test files).

- [ ] **Step 5: Register `/admin/scheduler/tick`**

Adjacent to the other `adminMux.HandleFunc` calls, add:

```go
// Trustage fires this every 30 s; see definitions/trustage/scheduler-tick.json.
adminMux.HandleFunc("POST /admin/scheduler/tick",
	crawlersvc.SchedulerTickHandler(svc, sourceRepo, bpGate))
```

The existing `bpGate` already implements the `Admitter` interface after Task 4 adds `Admit`.

- [ ] **Step 6: Drop imports that no longer resolve**

After the deletions the following imports in `main.go` are unused:

- `"crypto/sha256"`, `"encoding/hex"` — used only by the removed `sha256Hex` helper.
- `"stawi.jobs/pkg/pipeline/handlers"` — used only by the removed pipeline handlers and `crawlDependencies`.
- `"stawi.jobs/pkg/normalize"` — used only inside the removed `processSource`.
- `"stawi.jobs/pkg/quality"` — used only inside the removed `processSource`.
- `"stawi.jobs/pkg/dedupe"` — **still used** by `rebuildCanonicalsHandler`; keep.
- `"stawi.jobs/pkg/bloom"` — **still used** by the bloom filter initialization; keep.
- `"stawi.jobs/pkg/services"` — used by `services.NewRedirectClient`. The client was consumed only by the legacy publish handler; after its deletion, delete the client construction and the `RedirectPublicBaseURL` / `RedirectServiceURI` wiring too.
- `"stawi.jobs/pkg/publish"` — still used by `r2Publisher` construction, the CachePurger, and the retention endpoints (`purgeR2Archive`, `runRetention`); keep.
- `"stawi.jobs/pkg/translate"` — only used by the legacy translate handler; delete the `translator` construction and the `TRANSLATE_*` config reads.
- `"github.com/pitabwire/frame/events"` — used only by the deleted `frame.WithRegisterEvents(eventHandlers...)` path via `[]events.EventI`; the new handlers implement the `frame.EventI` alias so `frame.WithRegisterEvents` typing works without the subpackage. Keep or drop based on compile errors.

Run `goimports -w apps/crawler/cmd/main.go` to let the toolchain clean up.

- [ ] **Step 7: Build + run crawler tests**

```bash
go build ./apps/crawler/...
go test ./apps/crawler/... -count=1 -timeout 5m
```

Expected: successful build and the handler unit tests from Tasks 5–8 all pass. If you see `unused variable: translator` or similar, finish the trim — no legacy handler path should remain.

- [ ] **Step 8: Run the full build across the module**

```bash
go build ./...
go test ./pkg/... -count=1 -timeout 5m
```

Expected: everything green. Legacy files (`pkg/pipeline/handlers/*.go`) are still present and compilable — Phase 6 deletes them. Their tests still pass because nothing in Plan 4 touched those files.

- [ ] **Step 9: Commit**

```bash
git add apps/crawler/cmd/main.go
git commit -m "refactor(crawler): wire v1 event handlers; drop legacy pipeline dispatch"
```

---

## Task 10: New Trustage trigger `scheduler-tick.json`

**Files:**
- Create: `definitions/trustage/scheduler-tick.json`
- Delete: `definitions/trustage/source-crawl-sweep.json`

- [ ] **Step 1: Create the new trigger definition**

Create `definitions/trustage/scheduler-tick.json`:

```json
{
  "version": "1.0",
  "name": "stawi-jobs.scheduler.tick",
  "description": "Every 30 s: ask the stawi-jobs crawler to admit due sources through the backpressure gate and emit one crawl.requests.v1 per admitted source. Replaces source-crawl-sweep.json now that the crawler consumes v1 topics.",
  "schedule": {
    "cron": "30s",
    "active": true
  },
  "input": {},
  "config": {},
  "timeout": "2m",
  "on_error": {
    "action": "abort"
  },
  "steps": [
    {
      "id": "scheduler_tick",
      "type": "call",
      "name": "Invoke scheduler tick",
      "timeout": "90s",
      "retry": {
        "max_attempts": 3,
        "backoff_strategy": "exponential",
        "initial_backoff": "10s"
      },
      "call": {
        "action": "http.request",
        "input": {
          "url": "http://stawi-jobs-crawler.stawi-jobs.svc/admin/scheduler/tick",
          "method": "POST",
          "headers": { "Content-Type": "application/json" },
          "body": {}
        },
        "output_var": "tick_result"
      }
    }
  ]
}
```

- [ ] **Step 2: Delete the old trigger**

```bash
rm definitions/trustage/source-crawl-sweep.json
```

- [ ] **Step 3: Verify the json is parseable**

```bash
python3 -m json.tool < definitions/trustage/scheduler-tick.json > /dev/null
```

Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add definitions/trustage/scheduler-tick.json definitions/trustage/source-crawl-sweep.json
git commit -m "feat(trustage): scheduler-tick.json replaces source-crawl-sweep.json"
```

(`git add` on the deleted file records the removal; `git status` will show the file as `deleted:` in the staged set.)

---

## Task 11: End-to-end crawler test

**Files:**
- Create: `apps/crawler/service/e2e_test.go`

- [ ] **Step 1: Write the test**

Create `apps/crawler/service/e2e_test.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// always-grant admitter reused from Task 5's test file.
// (The admitterFunc type is declared in scheduler_tick_test.go — both
// test files share the `service` package so no re-declaration needed.)

// composite fake satisfying every interface the crawler wires in main.go.
type fakeCrawlerRepo struct {
	mu   sync.Mutex
	rows map[string]*domain.Source
}

func newFakeCrawlerRepo(src ...*domain.Source) *fakeCrawlerRepo {
	r := &fakeCrawlerRepo{rows: make(map[string]*domain.Source, len(src))}
	for _, s := range src {
		r.rows[s.ID] = s
	}
	return r
}
func (r *fakeCrawlerRepo) ListDue(_ context.Context, _ time.Time, limit int) ([]*domain.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.Source, 0, len(r.rows))
	for _, s := range r.rows {
		cp := *s
		out = append(out, &cp)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
func (r *fakeCrawlerRepo) GetByID(_ context.Context, id string) (*domain.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}
func (r *fakeCrawlerRepo) UpdateNextCrawl(_ context.Context, id string, next, verified time.Time, health float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.NextCrawlAt = next
		s.LastVerifiedAt = &verified
		s.HealthScore = health
	}
	return nil
}
func (r *fakeCrawlerRepo) RecordSuccess(_ context.Context, id string, h float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.HealthScore = h
		s.ConsecutiveFailures = 0
	}
	return nil
}
func (r *fakeCrawlerRepo) RecordFailure(_ context.Context, id string, h float64, cf int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.HealthScore = h
		s.ConsecutiveFailures = cf
	}
	return nil
}
func (r *fakeCrawlerRepo) FlagNeedsTuning(_ context.Context, id string, flag bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.NeedsTuning = flag
	}
	return nil
}
func (r *fakeCrawlerRepo) Upsert(_ context.Context, src *domain.Source) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := string(src.Type) + "|" + src.BaseURL
	r.rows[key] = src
	return nil
}

func TestCrawlerE2ETickToVariantEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawler-e2e"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	// Collectors on the two topics the crawler emits.
	variantCol := &envCollector[eventsv1.VariantIngestedV1]{topic: eventsv1.TopicVariantsIngested}
	pageCol := &envCollector[eventsv1.CrawlPageCompletedV1]{topic: eventsv1.TopicCrawlPageCompleted}
	for _, c := range []frame.EventI{variantCol, pageCol} {
		if err := svc.EventsManager().Add(c); err != nil {
			t.Fatalf("add collector: %v", err)
		}
	}

	// Fake source repo + connector.
	repo := newFakeCrawlerRepo(&domain.Source{
		BaseModel:        domain.BaseModel{ID: "src_e2e"},
		Type:             domain.SourceGenericHTML,
		BaseURL:          "https://acme.example/jobs",
		Status:           domain.SourceActive,
		Country:          "KE",
		Language:         "en",
		CrawlIntervalSec: 60,
		HealthScore:      1.0,
	})

	reg := connectors.NewRegistry()
	reg.Register(&fakeConnector{
		jobs: []domain.ExternalJob{
			{ExternalID: "ext-a", Title: "Backend Engineer", Company: "Acme",
				ApplyURL: "https://acme.example/jobs/ext-a"},
			{ExternalID: "ext-b", Title: "Data Scientist", Company: "Acme",
				ApplyURL: "https://acme.example/jobs/ext-b"},
		},
		raw: []byte("<html>page</html>"),
	})

	// Wire the three production handlers + the tick endpoint.
	reqH := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc: svc, Sources: repo, Registry: reg, Archive: newFakeArchive(),
	})
	pageH := NewPageCompletedHandler(repo)
	discH := NewSourceDiscoveredHandler(repo)
	if err := svc.EventsManager().Add(reqH); err != nil {
		t.Fatalf("add reqH: %v", err)
	}
	if err := svc.EventsManager().Add(pageH); err != nil {
		t.Fatalf("add pageH: %v", err)
	}
	if err := svc.EventsManager().Add(discH); err != nil {
		t.Fatalf("add discH: %v", err)
	}

	// Start Frame so every subscription is live before the tick emits.
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(250 * time.Millisecond)

	// Hit the tick endpoint; the admitter grants everything.
	admit := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) { return want, 0 })
	handler := SchedulerTickHandler(svc, repo, admit)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/tick", nil)
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tick status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp schedulerTickResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Dispatched != 1 {
		t.Fatalf("dispatched=%d, want 1", resp.Dispatched)
	}

	// Wait for the pipeline: crawl.requests.v1 → reqH → (2 variants + 1 page-completed).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if variantCol.Len() == 2 && pageCol.Len() == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if variantCol.Len() != 2 {
		t.Fatalf("variant events=%d, want 2", variantCol.Len())
	}
	if pageCol.Len() != 1 {
		t.Fatalf("page-completed events=%d, want 1", pageCol.Len())
	}

	// Source's health should have been bumped up by the page-completed
	// handler (success path).
	src, _ := repo.GetByID(ctx, "src_e2e")
	if src == nil || src.HealthScore <= 1.0 && src.HealthScore != 1.0 {
		t.Fatalf("unexpected health: %+v", src)
	}
}
```

- [ ] **Step 2: Run**

```bash
go test ./apps/crawler/service/... -run TestCrawlerE2ETickToVariantEvents -count=1 -timeout 3m
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add apps/crawler/service/e2e_test.go
git commit -m "test(crawler): end-to-end tick → crawl.requests → variants + page-completed"
```

---

## Task 12: Full build + sanity sweep

**Files:**
- None (verification only)

- [ ] **Step 1: Build the whole module**

```bash
cd /home/j/code/stawi.jobs
go build ./...
```

Expected: exit 0, no output.

- [ ] **Step 2: Run the full test suite**

```bash
go test ./... -count=1 -timeout 15m
```

Expected: PASS. Integration tests from Phases 1–3 (Parquet writer, materializer, worker pipeline) remain green because Plan 4 did not touch any of their contracts.

- [ ] **Step 3: Confirm legacy handler package still compiles and tests pass**

```bash
go test ./pkg/pipeline/... -count=1 -timeout 2m
```

Expected: PASS. The legacy `pkg/pipeline/handlers/*` files stay intact — they are no longer wired anywhere but still compile under the module. Phase 6 cutover deletes them.

- [ ] **Step 4: Verify the binaries still assemble**

```bash
rm -rf bin
make build
ls -la bin/crawler bin/writer bin/materializer bin/worker bin/api
```

Expected: all five binaries present.

- [ ] **Step 5: Commit nothing**

This task produces no diffs; it's a verification gate before closing the phase.

---

## Plan completion verification

After every task above is done:

```bash
go build ./...
go test ./... -count=1 -timeout 15m
git log --oneline main..HEAD
```

Expected:
- Every test passes (new handler unit tests + end-to-end crawler test + pre-existing Phase 1–3 tests + legacy `pkg/pipeline/handlers` tests).
- 11 commits on this branch, one per task (Task 12 is verification-only).
- `bin/crawler` is a working binary that, once deployed:
  1. receives a Trustage-fired `POST /admin/scheduler/tick`,
  2. enumerates due sources through the backpressure gate,
  3. emits `crawl.requests.v1` per admitted source and stamps `next_crawl_at` forward,
  4. consumes its own `crawl.requests.v1`, runs the connector iterator, archives raw bytes to R2, and emits `jobs.variants.ingested.v1` (picked up by `apps/worker` from Phase 3) + `crawl.page.completed.v1` + sampled `sources.discovered.v1`,
  5. self-consumes `crawl.page.completed.v1` to update source health + cursor,
  6. self-consumes `sources.discovered.v1` to upsert new `generic-html` sources.

No new job data is written to Postgres from the crawler. The legacy `pkg/pipeline/handlers/*` files remain in the repo but are unreferenced by any deployment; Phase 6 cutover removes them together with the `canonical_jobs` / `job_variants` / `job_clusters` tables.

Phase 5 now begins the candidates-service refactor, wiring CV lifecycle events + a Manticore-backed match endpoint with no further changes to the crawler.
