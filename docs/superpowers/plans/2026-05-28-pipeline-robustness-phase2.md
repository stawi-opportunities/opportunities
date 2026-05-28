# Pipeline Robustness — Phase 2: Split Enricher Worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple the slow, GPU-bound LLM extraction from the fast, network-bound crawl. The crawler becomes a pure fetcher (HTML → R2 + `raw_payloads` row); a new enricher handler in `apps/worker` claims rows from the Postgres queue via `FOR UPDATE SKIP LOCKED`, runs LLM extraction at its own pace, and emits enriched variants downstream.

**Architecture:** The `raw_payloads` table (made into a hypertable in Phase 1) becomes a Postgres queue. The crawler writes rows with `status='pending'`; a new `EnricherHandler` runs as a periodic worker (tick every 5 s) that claims a batch of pending rows, downloads the HTML blob from R2, runs `pkg/extraction.Extractor`, and emits `opportunities.variants.ingested.v1`. `current_stage` advances `pending` → `enriching` → `enriched` (or `failed` after N retries). The crawler stops calling `enrichStubs` inline.

**Tech Stack:** Go, GORM, Postgres 16 + TimescaleDB, R2, NATS, Frame v1.97+, `SELECT ... FOR UPDATE SKIP LOCKED` (PG12+).

**Depends on:** Phase 1 (raw_payloads ledger + hypertable + status column).

---

## File Structure

**Created:**
- `pkg/repository/raw_payload_queue.go` — Postgres queue ops: `Claim(batchSize, claimTTL)`, `MarkEnriched(id, variantID)`, `MarkFailed(id, err, retryAfter)`.
- `pkg/repository/raw_payload_queue_test.go` — exercise concurrent claims, retry, backoff.
- `apps/worker/service/enricher.go` — handler that calls Claim → fetch → extract → emit → mark.
- `apps/worker/service/enricher_test.go` — unit tests.
- `apps/worker/service/enricher_integration_test.go` — end-to-end with real Postgres + fake R2 + fake extractor.

**Modified:**
- `apps/worker/cmd/main.go` — wire EnricherHandler, schedule its periodic tick.
- `apps/crawler/service/crawl_request_handler.go` — REMOVE inline `enrichStubs` call; emit variants only for stubs the connector already populated (the existing skip-when-title-empty path).
- `apps/crawler/service/crawl_request_handler.go` — for URL-only stubs, **do not emit `variants.ingested.v1`**. The enricher will emit it once enrichment completes. The crawler's job becomes purely "write raw_payloads, optionally emit variant.ingested for already-complete records."
- `pkg/events/v1/types.go` — add `RawPayloadID` field to `VariantIngestedV1` for traceability.
- `pkg/archive/archive.go` — confirm `GetRaw(hash)` is available (it already is, per Phase 1 review).

---

## Critical design decisions baked into the plan

1. **The enricher is a tick handler, not an event consumer.** Polling Postgres at 5 s is far cheaper than wiring a NATS topic for "row is pending" — the row itself is the signal. This also means no NATS redelivery semantics to argue with; the claim TTL handles crash recovery.

2. **Claim TTL = 5 minutes.** A worker that crashes mid-enrich leaves `status='enriching'` with `next_retry_at = claimed_at + 5m`. The next claimant sweeps it back to pending and increments `attempt_count`. Pick 5m because the longest LLM call we tolerate is ~2m (extraction timeout) and we want headroom for transient cluster issues.

3. **Max attempts = 3.** After three failures, status flips to `failed` and the operator gets a queryable row with `last_error`. No silent loss.

4. **Connector "complete" records bypass the queue.** If the connector returns a fully-populated `ExternalOpportunity` (title, description, all required fields), the crawler emits `variants.ingested.v1` directly AND writes a `raw_payloads` row with `status='skipped'` (audit completeness). The enricher worker IGNORES `status='skipped'` rows.

5. **The enricher emits the SAME event the crawler used to emit** — `opportunities.variants.ingested.v1`. Downstream stages (normalize, validate, dedup, canonical, publish) require no changes.

---

## Tasks

### Task 1: Migration — add variant_id back-link

The enricher needs to know which variant_id it's about to emit so the ledger stays consistent. We add an optional `claimed_variant_id` column to `raw_payloads` so the enricher can pre-allocate the variant_id before emitting (avoids race where the variant_id changes on redelivery).

**Files:**
- Create: `db/migrations/0020_raw_payloads_claimed_variant.sql`

- [ ] **Step 1: Write the migration**

```sql
-- 0020: claim semantics for raw_payloads enricher queue.
--
-- claimed_at + claimed_by: ops visibility into who's holding a row,
--   when. After (now() - claimed_at) > 5 min, the row is reclaimable
--   regardless of status.
-- claimed_variant_id: the variant_id this row will produce on success.
--   Pre-allocated at claim time so a redelivery doesn't spawn a new
--   variant for the same logical fetch.

BEGIN;

ALTER TABLE raw_payloads
    ADD COLUMN IF NOT EXISTS claimed_at         TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS claimed_by         TEXT        NULL,
    ADD COLUMN IF NOT EXISTS claimed_variant_id VARCHAR(20) NULL;

-- Reclaim index: scan rows that are claimed but stale.
CREATE INDEX IF NOT EXISTS idx_raw_payloads_stale_claims
    ON raw_payloads (claimed_at)
    WHERE status = 'enriching';

COMMIT;
```

- [ ] **Step 2: Apply, verify**

```bash
cd /home/j/code/stawi.opportunities && go test ./pkg/frametest/... -run TestMigrate -v 2>&1 | tail -5
```

- [ ] **Step 3: Commit**

```bash
git add db/migrations/0020_raw_payloads_claimed_variant.sql
git commit -m "feat(ledger): add claim columns to raw_payloads for enricher queue"
```

---

### Task 2: Repository — RawPayloadQueue with FOR UPDATE SKIP LOCKED

**Files:**
- Create: `pkg/repository/raw_payload_queue.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/repository/raw_payload_queue_test.go`:

```go
package repository_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

func TestRawPayloadQueue_ClaimReturnsBatchOfPendingRows(t *testing.T) {
	ctx := context.Background()
	pool := frametest.PostgresPool(t)
	crawl := repository.NewCrawlRepository(pool)
	queue := repository.NewRawPayloadQueue(pool)

	// Seed: a crawl job + 5 pending raw_payloads.
	job := &domain.CrawlJob{
		SourceID:       "src-q-001",
		ScheduledAt:    time.Now().UTC(),
		Status:         domain.CrawlScheduled,
		IdempotencyKey: "src-q-001:claim-test",
	}
	if err := crawl.Create(ctx, job); err != nil {
		t.Fatalf("Create job: %v", err)
	}
	for i := 0; i < 5; i++ {
		rp := &domain.RawPayload{
			CrawlJobID:  job.ID,
			SourceID:    "src-q-001",
			SourceURL:   "https://example.com",
			StorageURI:  "raw/test.html.gz",
			ContentHash: "h",
			FetchedAt:   time.Now().UTC(),
			HTTPStatus:  200,
			Status:      domain.RawPayloadStatusPending,
		}
		if err := crawl.SaveRawPayload(ctx, rp); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := queue.Claim(ctx, "enricher-test-1", 3, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Claim returned %d rows; want 3", len(got))
	}
	for _, r := range got {
		if r.Status != domain.RawPayloadStatusEnriching {
			t.Fatalf("row.Status = %q; want enriching", r.Status)
		}
		if r.ClaimedVariantID == "" {
			t.Fatalf("ClaimedVariantID empty; want populated for downstream emit")
		}
	}
}

func TestRawPayloadQueue_ConcurrentClaimsDoNotOverlap(t *testing.T) {
	ctx := context.Background()
	pool := frametest.PostgresPool(t)
	crawl := repository.NewCrawlRepository(pool)
	queue := repository.NewRawPayloadQueue(pool)

	job := &domain.CrawlJob{
		SourceID:       "src-q-002",
		ScheduledAt:    time.Now().UTC(),
		Status:         domain.CrawlScheduled,
		IdempotencyKey: "src-q-002:concurrent",
	}
	_ = crawl.Create(ctx, job)
	for i := 0; i < 10; i++ {
		_ = crawl.SaveRawPayload(ctx, &domain.RawPayload{
			CrawlJobID:  job.ID,
			SourceID:    "src-q-002",
			SourceURL:   "https://example.com",
			ContentHash: "h",
			FetchedAt:   time.Now().UTC(),
			HTTPStatus:  200,
			Status:      domain.RawPayloadStatusPending,
		})
	}

	var (
		mu       sync.Mutex
		allIDs   = map[string]struct{}{}
		dup      bool
	)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			rows, err := queue.Claim(ctx, "worker", 5, 5*time.Minute)
			if err != nil {
				t.Errorf("worker %d Claim: %v", workerID, err)
				return
			}
			mu.Lock()
			for _, r := range rows {
				if _, seen := allIDs[r.ID]; seen {
					dup = true
				}
				allIDs[r.ID] = struct{}{}
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if dup {
		t.Fatalf("two workers claimed the same row; FOR UPDATE SKIP LOCKED broken")
	}
	if len(allIDs) != 10 {
		t.Fatalf("total claimed = %d; want 10", len(allIDs))
	}
}

func TestRawPayloadQueue_MarkEnriched_ClearsClaim(t *testing.T) {
	ctx := context.Background()
	pool := frametest.PostgresPool(t)
	queue := repository.NewRawPayloadQueue(pool)
	id := seedSinglePendingRow(t, pool)
	got, _ := queue.Claim(ctx, "w", 1, 5*time.Minute)
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("Claim returned unexpected: %+v", got)
	}
	if err := queue.MarkEnriched(ctx, id, got[0].ClaimedAt); err != nil {
		t.Fatalf("MarkEnriched: %v", err)
	}
	row, _ := queue.GetByID(ctx, id)
	if row.Status != domain.RawPayloadStatusEnriched {
		t.Fatalf("Status = %q; want enriched", row.Status)
	}
}

func TestRawPayloadQueue_MarkFailed_IncrementsAttempt_RetriesAfterBackoff(t *testing.T) {
	ctx := context.Background()
	pool := frametest.PostgresPool(t)
	queue := repository.NewRawPayloadQueue(pool)
	id := seedSinglePendingRow(t, pool)

	got, _ := queue.Claim(ctx, "w", 1, 5*time.Minute)
	if err := queue.MarkFailed(ctx, id, got[0].ClaimedAt, "extractor: timeout", 30*time.Second, 3); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	row, _ := queue.GetByID(ctx, id)
	if row.Status != domain.RawPayloadStatusPending {
		t.Fatalf("Status = %q; want pending (retry allowed)", row.Status)
	}
	if row.AttemptCount != 1 {
		t.Fatalf("AttemptCount = %d; want 1", row.AttemptCount)
	}
	if row.NextRetryAt == nil || time.Until(*row.NextRetryAt) < 25*time.Second {
		t.Fatalf("NextRetryAt = %v; want ~30s in the future", row.NextRetryAt)
	}
}

func TestRawPayloadQueue_MarkFailed_ExhaustionMovesToFailed(t *testing.T) {
	ctx := context.Background()
	pool := frametest.PostgresPool(t)
	queue := repository.NewRawPayloadQueue(pool)
	id := seedSinglePendingRow(t, pool)

	// 3 failures (max attempts = 3 in this test).
	for i := 0; i < 3; i++ {
		got, _ := queue.Claim(ctx, "w", 1, 5*time.Minute)
		if len(got) == 0 {
			// Backoff already exceeded; reset next_retry_at by waiting.
			_ = pool(ctx, false).Exec("UPDATE raw_payloads SET next_retry_at = now() - INTERVAL '1 second' WHERE id = ?", id)
			continue
		}
		_ = queue.MarkFailed(ctx, id, got[0].ClaimedAt, "boom", 0, 3)
		// Reset retry clock so next claim happens immediately.
		_ = pool(ctx, false).Exec("UPDATE raw_payloads SET next_retry_at = now() - INTERVAL '1 second' WHERE id = ?", id).Error
	}
	row, _ := queue.GetByID(ctx, id)
	if row.Status != domain.RawPayloadStatusFailed {
		t.Fatalf("Status = %q; want failed after 3 attempts", row.Status)
	}
}

func seedSinglePendingRow(t *testing.T, pool any /* match the type used by frametest */) string {
	t.Helper()
	// Implementation matches the seed pattern in the other tests above.
	// Returns the inserted raw_payload ID.
	// ...
	panic("implement using the frametest helper pattern from your test scaffold")
}
```

(The test helper `seedSinglePendingRow` should use the same pattern as the inline seeding earlier in the file — extract for DRY.)

- [ ] **Step 2: Run, verify fail**

```bash
go test ./pkg/repository/ -run TestRawPayloadQueue -v 2>&1 | tail -20
```
Expected: FAIL (`NewRawPayloadQueue` undefined).

- [ ] **Step 3: Implement the queue**

Create `pkg/repository/raw_payload_queue.go`:

```go
// Package repository: RawPayloadQueue implements the Postgres queue that
// the enricher worker drains. The queue lives on the raw_payloads
// hypertable; rows enter with status='pending' and graduate through
// 'enriching' → ('enriched'|'pending again'|'failed') driven by the
// worker.
//
// Claim semantics use SELECT ... FOR UPDATE SKIP LOCKED — the same
// pattern Trustage's CronScheduler uses (apps/default/service/repository/
// schedule.go:322). It scales to dozens of concurrent enrichers without
// row-level contention.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/xid"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// QueueRow is the projection RawPayloadQueue returns. Distinct from
// domain.RawPayload because the queue cares about claim metadata, not
// the full schema.
type QueueRow struct {
	ID               string
	FetchedAt        time.Time
	CrawlJobID       string
	SourceID         string
	SourceURL        string
	StorageURI       string
	ContentHash      string
	HTTPStatus       int
	Status           domain.RawPayloadStatus
	AttemptCount    int
	ClaimedAt        time.Time
	ClaimedBy        string
	ClaimedVariantID string
}

// RawPayloadQueue wraps Postgres operations against raw_payloads
// when used as a worker queue.
type RawPayloadQueue struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewRawPayloadQueue(db func(ctx context.Context, readOnly bool) *gorm.DB) *RawPayloadQueue {
	return &RawPayloadQueue{db: db}
}

// Claim picks up to batchSize pending rows (or stale-claim rows whose
// claim TTL has expired) and atomically marks them 'enriching' with
// claimed_at = now(), claimed_by = workerID, claimed_variant_id =
// freshly-generated xid.
//
// Returns rows ready for processing. An empty slice means the queue
// is drained — caller sleeps and tries again.
func (q *RawPayloadQueue) Claim(
	ctx context.Context,
	workerID string,
	batchSize int,
	claimTTL time.Duration,
) ([]QueueRow, error) {
	if batchSize <= 0 {
		return nil, nil
	}

	// Two-step claim in a single CTE:
	//   1. Pick up to batchSize candidates: status='pending' AND
	//      (next_retry_at IS NULL OR next_retry_at <= now()), ordered
	//      by fetched_at ASC (FIFO). FOR UPDATE SKIP LOCKED.
	//   2. Plus stale-claimed rows: status='enriching' AND claimed_at <
	//      now() - claimTTL. Same FOR UPDATE SKIP LOCKED.
	//   3. UPDATE the union with new claim state.
	//   4. RETURNING the rows for the caller.
	//
	// claimed_variant_id is generated in app code (xid) and bulk-passed
	// because Postgres has no xid function. We do this by SELECT-ing the
	// candidate IDs first, generating xids per row, then UPDATE-ing.
	//
	// This is two round-trips but conceptually atomic per-row because
	// FOR UPDATE SKIP LOCKED holds the lock across the transaction.

	var rows []QueueRow
	err := q.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		// 1. Select & lock candidates.
		var candidates []struct {
			ID        string
			FetchedAt time.Time
		}
		err := tx.Raw(`
            SELECT id, fetched_at FROM raw_payloads
            WHERE (
                (status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= now()))
                OR
                (status = 'enriching' AND claimed_at < now() - (? || ' seconds')::interval)
            )
            ORDER BY fetched_at ASC
            LIMIT ?
            FOR UPDATE SKIP LOCKED
        `, int(claimTTL.Seconds()), batchSize).Scan(&candidates).Error
		if err != nil {
			return fmt.Errorf("select candidates: %w", err)
		}
		if len(candidates) == 0 {
			return nil
		}

		// 2. Generate variant_ids and update each row.
		// (A bulk UPDATE … FROM (VALUES …) is faster but harder to read
		// when N <= ~50 — typical batch size. Plain UPDATEs per row are
		// fine at our scale; the FOR UPDATE lock above already serialised
		// any contention.)
		now := time.Now().UTC()
		for _, c := range candidates {
			variantID := xid.New().String()
			err := tx.Exec(`
                UPDATE raw_payloads
                SET status              = 'enriching',
                    claimed_at          = ?,
                    claimed_by          = ?,
                    claimed_variant_id  = COALESCE(claimed_variant_id, ?)
                WHERE id = ? AND fetched_at = ?
            `, now, workerID, variantID, c.ID, c.FetchedAt).Error
			if err != nil {
				return fmt.Errorf("update %s: %w", c.ID, err)
			}
		}

		// 3. Return the freshly-claimed rows.
		ids := make([]string, 0, len(candidates))
		for _, c := range candidates {
			ids = append(ids, c.ID)
		}
		err = tx.Raw(`
            SELECT id, fetched_at, crawl_job_id, source_id, source_url,
                   storage_uri, content_hash, http_status, status,
                   attempt_count, claimed_at, claimed_by, claimed_variant_id
            FROM raw_payloads
            WHERE id IN ?
              AND status = 'enriching'
              AND claimed_by = ?
        `, ids, workerID).Scan(&rows).Error
		return err
	})
	return rows, err
}

// MarkEnriched flips a claimed row to status='enriched' iff the
// claimed_at matches (proves the worker still holds the claim).
// Returns ErrClaimLost if the row has been reclaimed by another worker.
func (q *RawPayloadQueue) MarkEnriched(ctx context.Context, id string, claimedAt time.Time) error {
	tx := q.db(ctx, false).Exec(`
        UPDATE raw_payloads
        SET status = 'enriched', updated_at = now()
        WHERE id = ? AND claimed_at = ? AND status = 'enriching'
    `, id, claimedAt)
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return ErrClaimLost
	}
	return nil
}

// MarkFailed flips status back to 'pending' with a backoff delay,
// OR to 'failed' if attempt_count + 1 >= maxAttempts.
func (q *RawPayloadQueue) MarkFailed(
	ctx context.Context,
	id string,
	claimedAt time.Time,
	errMsg string,
	retryAfter time.Duration,
	maxAttempts int,
) error {
	// Single statement: decide terminal-vs-retry based on attempt_count.
	tx := q.db(ctx, false).Exec(`
        UPDATE raw_payloads
        SET attempt_count = attempt_count + 1,
            last_error    = ?,
            status        = CASE
                              WHEN attempt_count + 1 >= ? THEN 'failed'
                              ELSE 'pending'
                            END,
            next_retry_at = CASE
                              WHEN attempt_count + 1 >= ? THEN NULL
                              ELSE now() + (? || ' seconds')::interval
                            END,
            claimed_at         = NULL,
            claimed_by         = NULL,
            updated_at         = now()
        WHERE id = ? AND claimed_at = ? AND status = 'enriching'
    `,
		errMsg, maxAttempts, maxAttempts, int(retryAfter.Seconds()),
		id, claimedAt,
	)
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return ErrClaimLost
	}
	return nil
}

// GetByID is for tests and admin endpoints; not on the hot path.
func (q *RawPayloadQueue) GetByID(ctx context.Context, id string) (*domain.RawPayload, error) {
	var row domain.RawPayload
	err := q.db(ctx, true).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

// ErrClaimLost is returned when the worker tries to advance a row it
// no longer holds (reclaimed by another worker after TTL expiry).
// Workers should treat this as "the other worker handled it, drop the
// in-flight result" — NOT as a transient error.
var ErrClaimLost = errors.New("raw_payloads: claim lost (reclaimed by another worker)")
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./pkg/repository/ -run TestRawPayloadQueue -v -count=1 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/repository/raw_payload_queue.go pkg/repository/raw_payload_queue_test.go db/migrations/0020_raw_payloads_claimed_variant.sql
git commit -m "feat(queue): raw_payloads queue with FOR UPDATE SKIP LOCKED claim

Drop-in pattern matching Trustage's CronScheduler. Concurrent
enrichers safely claim distinct rows; stale claims (worker
crashed mid-enrich) are reclaimed after 5min TTL. Failed claims
backoff and retire to 'failed' after N attempts."
```

---

### Task 3: Add RawPayloadID to VariantIngestedV1

**Files:**
- Modify: `pkg/events/v1/types.go` (the `VariantIngestedV1` struct).

- [ ] **Step 1: Locate**

```bash
grep -n "type VariantIngestedV1 struct" pkg/events/v1/*.go
```

- [ ] **Step 2: Add field**

Add `RawPayloadID string \`json:"raw_payload_id,omitempty"\`` to the struct (next to `Stage` is a good spot).

- [ ] **Step 3: Build**

```bash
go build ./... 2>&1 | tail -5
```

- [ ] **Step 4: Commit**

```bash
git add pkg/events/v1/types.go
git commit -m "feat(events): add RawPayloadID to VariantIngestedV1 for trace-back"
```

---

### Task 4: EnricherHandler — the worker that drains the queue

**Files:**
- Create: `apps/worker/service/enricher.go`
- Create: `apps/worker/service/enricher_test.go`

- [ ] **Step 1: Write failing test**

```go
// apps/worker/service/enricher_test.go
package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/apps/worker/service"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

func TestEnricher_Tick_ClaimsAndProcesses(t *testing.T) {
	ctx := context.Background()
	pool := frametest.PostgresPool(t)
	// 1. Seed 3 pending rows + a fake R2 archive with their HTML.
	// 2. Construct EnricherHandler with a fake Extractor that returns
	//    {Title:"X"} for every input.
	// 3. Call h.Tick(ctx).
	// 4. Assert: 3 variants.ingested.v1 events emitted, 3 rows transitioned to enriched.

	scaffold := newEnricherScaffold(t, pool)
	scaffold.SeedPending(3)
	scaffold.Extractor.NextOutput = &extraction.Result{Title: "X", Description: "desc"}

	h := service.NewEnricherHandler(service.EnricherDeps{
		Queue:       scaffold.Queue,
		Archive:     scaffold.Archive,
		Extractor:   scaffold.Extractor,
		Emitter:     scaffold.Emitter,
		WorkerID:    "enricher-test",
		BatchSize:   10,
		ClaimTTL:    5 * time.Minute,
		MaxAttempts: 3,
		RetryDelay:  30 * time.Second,
	})

	processed, err := h.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if processed != 3 {
		t.Fatalf("processed = %d; want 3", processed)
	}
	if len(scaffold.Emitter.Emitted) != 3 {
		t.Fatalf("emitted = %d; want 3", len(scaffold.Emitter.Emitted))
	}
	// All 3 rows should be enriched.
	for _, id := range scaffold.SeededIDs {
		row, _ := scaffold.Queue.GetByID(ctx, id)
		if row.Status != domain.RawPayloadStatusEnriched {
			t.Fatalf("row %s status = %q; want enriched", id, row.Status)
		}
	}
}

func TestEnricher_Tick_FetchFails_Retries(t *testing.T) {
	ctx := context.Background()
	scaffold := newEnricherScaffold(t, frametest.PostgresPool(t))
	scaffold.SeedPending(1)
	scaffold.Archive.GetErr = errors.New("R2: 503")

	h := service.NewEnricherHandler(scaffold.Deps())
	_, _ = h.Tick(ctx)

	row, _ := scaffold.Queue.GetByID(ctx, scaffold.SeededIDs[0])
	if row.Status != domain.RawPayloadStatusPending {
		t.Fatalf("status = %q; want pending (retry queued)", row.Status)
	}
	if row.AttemptCount != 1 {
		t.Fatalf("AttemptCount = %d; want 1", row.AttemptCount)
	}
}

func TestEnricher_Tick_ExtractFails_Retries(t *testing.T) {
	ctx := context.Background()
	scaffold := newEnricherScaffold(t, frametest.PostgresPool(t))
	scaffold.SeedPending(1)
	scaffold.Extractor.NextErr = errors.New("LLM: timeout")

	h := service.NewEnricherHandler(scaffold.Deps())
	_, _ = h.Tick(ctx)

	row, _ := scaffold.Queue.GetByID(ctx, scaffold.SeededIDs[0])
	if row.Status != domain.RawPayloadStatusPending {
		t.Fatalf("status = %q; want pending after extractor failure", row.Status)
	}
}

func TestEnricher_Tick_NoPendingRows_NoOp(t *testing.T) {
	ctx := context.Background()
	scaffold := newEnricherScaffold(t, frametest.PostgresPool(t))
	h := service.NewEnricherHandler(scaffold.Deps())
	processed, err := h.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed = %d; want 0", processed)
	}
}
```

(The scaffold helper `newEnricherScaffold` builds the fakes. Pattern matches existing worker test files.)

- [ ] **Step 2: Run, verify fail**

```bash
go test ./apps/worker/service/ -run TestEnricher -v 2>&1 | tail -20
```
Expected: FAIL (`NewEnricherHandler` undefined).

- [ ] **Step 3: Implement**

```go
// apps/worker/service/enricher.go
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// EnricherDeps bundles the handler's collaborators.
type EnricherDeps struct {
	Queue       *repository.RawPayloadQueue
	Archive     archive.Archive
	Extractor   *extraction.Extractor
	Emitter     frame.EventsManager // svc.EventsManager() in prod
	WorkerID    string              // pod name + uuid; for ops triage.
	BatchSize   int                 // claim batch size. ~16 = good default.
	ClaimTTL    time.Duration       // stale-claim reclaim threshold. 5m.
	MaxAttempts int                 // 3 → move to 'failed' on 3rd error.
	RetryDelay  time.Duration       // backoff before retry. 30s.
}

// EnricherHandler drains the raw_payloads queue.
type EnricherHandler struct {
	deps EnricherDeps
}

func NewEnricherHandler(deps EnricherDeps) *EnricherHandler {
	if deps.BatchSize <= 0 {
		deps.BatchSize = 16
	}
	if deps.ClaimTTL <= 0 {
		deps.ClaimTTL = 5 * time.Minute
	}
	if deps.MaxAttempts <= 0 {
		deps.MaxAttempts = 3
	}
	if deps.RetryDelay <= 0 {
		deps.RetryDelay = 30 * time.Second
	}
	return &EnricherHandler{deps: deps}
}

// Tick claims a batch, enriches each row, emits a variant.ingested
// event per success, and marks rows enriched/pending-again/failed.
// Returns the number of rows it processed (including failures).
//
// Designed to be called periodically by the worker app's scheduler
// (e.g. every 5 seconds). Returns nil on transient errors — the next
// tick will retry. A non-nil error is reserved for unrecoverable
// configuration issues.
func (h *EnricherHandler) Tick(ctx context.Context) (int, error) {
	log := util.Log(ctx).WithField("worker_id", h.deps.WorkerID)

	rows, err := h.deps.Queue.Claim(ctx, h.deps.WorkerID, h.deps.BatchSize, h.deps.ClaimTTL)
	if err != nil {
		log.WithError(err).Warn("enricher: claim failed")
		return 0, nil
	}
	if len(rows) == 0 {
		return 0, nil
	}

	processed := 0
	for _, row := range rows {
		processed++
		if err := h.processOne(ctx, row); err != nil {
			log.WithError(err).
				WithField("raw_payload_id", row.ID).
				Warn("enricher: row failed; will retry or mark failed")
		}
	}
	return processed, nil
}

func (h *EnricherHandler) processOne(ctx context.Context, row repository.QueueRow) error {
	log := util.Log(ctx).WithField("raw_payload_id", row.ID)

	// 1. Fetch the HTML from R2 via the content_hash.
	body, err := h.deps.Archive.GetRaw(ctx, row.ContentHash)
	if err != nil {
		_ = h.deps.Queue.MarkFailed(ctx, row.ID, row.ClaimedAt,
			fmt.Sprintf("archive.GetRaw: %v", err),
			h.deps.RetryDelay, h.deps.MaxAttempts)
		return err
	}

	// 2. Strip HTML to extraction-friendly text (existing pattern).
	body = stripToExtractable(body)

	// 3. Run LLM extraction.
	extracted, err := h.deps.Extractor.Extract(ctx, string(body), nil /* src.Kinds — see Step 4 */)
	if err != nil {
		_ = h.deps.Queue.MarkFailed(ctx, row.ID, row.ClaimedAt,
			fmt.Sprintf("extractor: %v", err),
			h.deps.RetryDelay, h.deps.MaxAttempts)
		return err
	}
	if extracted == nil || strings.TrimSpace(extracted.Title) == "" {
		// Extractor returned empty result — treat as failure with a
		// distinct error so ops can diagnose "extractor running but
		// not producing".
		_ = h.deps.Queue.MarkFailed(ctx, row.ID, row.ClaimedAt,
			"extractor: empty result", h.deps.RetryDelay, h.deps.MaxAttempts)
		return errors.New("empty extraction")
	}

	// 4. Build VariantIngestedV1 and emit.
	event := eventsv1.VariantIngestedV1{
		VariantID:     row.ClaimedVariantID,
		SourceID:      row.SourceID,
		Kind:          extracted.Kind,
		Title:         extracted.Title,
		IssuingEntity: extracted.IssuingEntity,
		// ...populate remaining fields from `extracted` — match the
		// payload built by the existing crawler at lines 321-337 of
		// apps/crawler/service/crawl_request_handler.go.
		ScrapedAt:     time.Now().UTC(),
		RawPayloadID:  row.ID,
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, event)
	if emitErr := h.deps.Emitter.Emit(ctx, eventsv1.TopicVariantsIngested, env); emitErr != nil {
		_ = h.deps.Queue.MarkFailed(ctx, row.ID, row.ClaimedAt,
			fmt.Sprintf("emit: %v", emitErr),
			h.deps.RetryDelay, h.deps.MaxAttempts)
		return emitErr
	}

	// 5. Mark enriched.
	if err := h.deps.Queue.MarkEnriched(ctx, row.ID, row.ClaimedAt); err != nil {
		if errors.Is(err, repository.ErrClaimLost) {
			log.Warn("enricher: claim expired before MarkEnriched; another worker took over")
			return nil
		}
		return err
	}
	return nil
}

func stripToExtractable(body []byte) []byte {
	// Use pkg/content.ExtractFromHTML's Markdown projection if present
	// — matches the existing extractor input pipeline in the crawler.
	// On any error, return the raw body and let the extractor handle it.
	if ext, _ := contentExtract(body); ext != nil && ext.Markdown != "" {
		return []byte(ext.Markdown)
	}
	return body
}
```

The `src.Kinds` parameter passed to `Extractor.Extract` (currently set to `nil` above) needs the source's declared kinds. Wire it by storing `src.Kinds` in the `raw_payloads` row at Phase 1 write time, OR by looking up the source via `SourceGetter.GetByID(row.SourceID)`. The lookup is one Postgres roundtrip per row — acceptable at our scale.

- [ ] **Step 4: Wire Source lookup**

Add a `Sources SourceGetter` field to `EnricherDeps`. In `processOne`, before calling `h.deps.Extractor.Extract`:

```go
src, err := h.deps.Sources.GetByID(ctx, row.SourceID)
if err != nil || src == nil {
    _ = h.deps.Queue.MarkFailed(ctx, row.ID, row.ClaimedAt,
        fmt.Sprintf("source lookup: %v", err),
        h.deps.RetryDelay, h.deps.MaxAttempts)
    return err
}
// Now pass src.Kinds:
extracted, err := h.deps.Extractor.Extract(ctx, string(body), src.Kinds)
```

`SourceGetter` is the same interface already defined in `apps/crawler/service/crawl_request_handler.go:36-38` — copy or move to `pkg/domain` for shared use.

- [ ] **Step 5: Run tests, verify pass**

```bash
go test ./apps/worker/service/ -run TestEnricher -v 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/worker/service/enricher.go apps/worker/service/enricher_test.go
git commit -m "feat(worker): EnricherHandler drains raw_payloads queue via Tick

Periodic worker claims pending rows (FOR UPDATE SKIP LOCKED),
downloads HTML from R2 via archive.GetRaw, runs the LLM extractor,
emits variants.ingested.v1, marks rows enriched. Retries up to 3
times with 30s backoff before flipping to 'failed' for human review."
```

---

### Task 5: Wire EnricherHandler into apps/worker/cmd/main.go

**Files:**
- Modify: `apps/worker/cmd/main.go`

- [ ] **Step 1: Construct + schedule the tick**

Find where existing handlers are registered in `apps/worker/cmd/main.go`. After their wiring, add:

```go
// Enricher: drains raw_payloads queue with a periodic tick.
enricherQueue := repository.NewRawPayloadQueue(svc.DatastoreManager().Pool().DB)
sourceRepo := repository.NewSourceRepository(svc.DatastoreManager().Pool().DB)
enricher := service.NewEnricherHandler(service.EnricherDeps{
    Queue:       enricherQueue,
    Archive:     arch, // same Archive instance the crawler uses
    Extractor:   extractor,
    Emitter:     svc.EventsManager(),
    Sources:     sourceRepo,
    WorkerID:    os.Getenv("HOSTNAME"),
    BatchSize:   cfg.EnricherBatchSize,    // default 16
    ClaimTTL:    cfg.EnricherClaimTTL,     // default 5m
    MaxAttempts: cfg.EnricherMaxAttempts,  // default 3
    RetryDelay:  cfg.EnricherRetryDelay,   // default 30s
})

// Schedule the tick. Use the standard frame periodic-job pattern.
go func() {
    ticker := time.NewTicker(cfg.EnricherTickInterval) // default 5s
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if n, err := enricher.Tick(ctx); err != nil {
                util.Log(ctx).WithError(err).Warn("enricher tick failed")
            } else if n > 0 {
                util.Log(ctx).WithField("processed", n).Debug("enricher tick")
            }
        }
    }
}()
```

- [ ] **Step 2: Add config fields to apps/worker/config/config.go**

```go
EnricherBatchSize    int           `env:"ENRICHER_BATCH_SIZE" envDefault:"16"`
EnricherClaimTTL     time.Duration `env:"ENRICHER_CLAIM_TTL" envDefault:"5m"`
EnricherMaxAttempts  int           `env:"ENRICHER_MAX_ATTEMPTS" envDefault:"3"`
EnricherRetryDelay   time.Duration `env:"ENRICHER_RETRY_DELAY" envDefault:"30s"`
EnricherTickInterval time.Duration `env:"ENRICHER_TICK_INTERVAL" envDefault:"5s"`
```

- [ ] **Step 3: Build**

```bash
go build ./apps/worker/... 2>&1 | tail -5
```
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add apps/worker/cmd/main.go apps/worker/config/config.go
git commit -m "feat(worker): wire EnricherHandler as periodic tick"
```

---

### Task 6: Crawler — stop enriching inline

**Files:**
- Modify: `apps/crawler/service/crawl_request_handler.go:212` (the `enrichStubs` call).
- Modify: `apps/crawler/service/crawl_request_handler.go:214-358` (the per-item loop).

- [ ] **Step 1: Write failing test**

Add to `apps/crawler/service/crawl_request_handler_test.go`:

```go
func TestExecute_StubsGoToQueue_NotInlineLLM(t *testing.T) {
	ctx := context.Background()
	scaffold := newCrawlTestScaffold(t)
	scaffold.RegisterPages(page{
		HTML: []byte("<html>stub</html>"),
		Items: 2, // both URL-only stubs
		StubItems: true,
	})
	// Critical: the extractor should NOT be called by the crawler.
	scaffold.Extractor.AssertNeverCalled(t)

	h := service.NewCrawlRequestHandler(scaffold.Deps())
	payload := envelopeJSON(eventsv1.CrawlRequestV1{
		RequestID:      "req-stubs",
		SourceID:       scaffold.SourceID,
		IdempotencyKey: "src-stubs:t",
	})
	_ = h.Execute(ctx, &payload)

	// raw_payloads should be created with status='pending'.
	var pending int64
	scaffold.DB.Raw(
		"SELECT count(*) FROM raw_payloads WHERE crawl_job_id IN (SELECT id FROM crawl_jobs WHERE idempotency_key='src-stubs:t') AND status = 'pending'",
	).Scan(&pending)
	if pending != 1 {
		t.Fatalf("pending raw_payloads = %d; want 1 (one page)", pending)
	}
	// No variants.ingested.v1 should have been emitted by the crawler
	// for stub items (the enricher will do it).
	if scaffold.Emitter.Count(eventsv1.TopicVariantsIngested) != 0 {
		t.Fatalf("emitter emitted %d variants.ingested events; want 0 for stubs",
			scaffold.Emitter.Count(eventsv1.TopicVariantsIngested))
	}
}

func TestExecute_CompleteRecordsBypassQueue(t *testing.T) {
	ctx := context.Background()
	scaffold := newCrawlTestScaffold(t)
	scaffold.RegisterPages(page{
		HTML: []byte("<html>complete</html>"),
		Items: 3, // all with Title set
		StubItems: false,
	})
	h := service.NewCrawlRequestHandler(scaffold.Deps())
	payload := envelopeJSON(eventsv1.CrawlRequestV1{
		RequestID: "req-complete", SourceID: scaffold.SourceID,
		IdempotencyKey: "src-complete:t",
	})
	_ = h.Execute(ctx, &payload)

	// raw_payloads row exists with status='skipped' (audit completeness).
	var skipped int64
	scaffold.DB.Raw(
		"SELECT count(*) FROM raw_payloads WHERE status='skipped' AND crawl_job_id IN (SELECT id FROM crawl_jobs WHERE idempotency_key='src-complete:t')",
	).Scan(&skipped)
	if skipped != 1 {
		t.Fatalf("skipped raw_payloads = %d; want 1", skipped)
	}
	// Variants emitted directly (3 complete items).
	if scaffold.Emitter.Count(eventsv1.TopicVariantsIngested) != 3 {
		t.Fatalf("emitted = %d; want 3", scaffold.Emitter.Count(eventsv1.TopicVariantsIngested))
	}
}
```

- [ ] **Step 2: Run, verify fail**

```bash
go test ./apps/crawler/service/ -run "TestExecute_StubsGoToQueue|TestExecute_CompleteRecordsBypassQueue" -v 2>&1 | tail -20
```
Expected: FAIL.

- [ ] **Step 3: Modify the crawler**

In `apps/crawler/service/crawl_request_handler.go`:

a) REMOVE the `h.enrichStubs(ctx, pageItems, src)` line at line 212.

b) Replace the inner loop so:
- If `extJob.Title == ""` AND `extJob.ApplyURL != ""` → it's a stub. Skip the variant emission entirely. The raw_payloads row (already written by Phase 1) will be claimed by the enricher worker.
- If `extJob.Title != ""` → emit the variant as before, AND mark the page's raw_payloads row as `status='skipped'`.

Concretely, between line 213 (`for i := range pageItems`) and line 220 (`ensureApplyURL`):

```go
// Skip stubs — they go to the enricher queue via the raw_payloads
// row already written above. The enricher will fetch the detail
// page, extract, and emit variants.ingested.v1 with the same
// raw_payload_id reference.
if strings.TrimSpace(extJob.Title) == "" && strings.TrimSpace(extJob.ApplyURL) != "" {
    continue
}
```

And after the loop ends (around line 376), add:

```go
// If we emitted variants for every item on this page, mark the
// raw_payloads row 'skipped' — the connector was self-sufficient,
// no enrichment needed.
if pageRawPayloadID != "" && jobsEmitted > 0 && jobsEmitted == jobsFound {
    _ = h.deps.CrawlRepo.UpdateRawPayloadStatus(ctx, pageRawPayloadID, domain.RawPayloadStatusSkipped)
}
```

- [ ] **Step 4: Add UpdateRawPayloadStatus to repository**

```go
// pkg/repository/crawl.go
func (r *CrawlRepository) UpdateRawPayloadStatus(ctx context.Context, id string, status domain.RawPayloadStatus) error {
    return r.db(ctx, false).
        Table("raw_payloads").
        Where("id = ?", id).
        Update("status", status).Error
}
```

- [ ] **Step 5: Delete `enrichStubs` and `enrichOne`**

Lines 545-660 of `apps/crawler/service/crawl_request_handler.go` become dead code. Delete them — and the `PageFetcher *httpx.Client` field on `CrawlRequestDeps` (line 54), and the `EnrichConcurrency` field (line 60).

Also delete the wiring in `apps/crawler/cmd/main.go` around line 305 that sets `PageFetcher: httpClient`.

- [ ] **Step 6: Run all tests**

```bash
go test ./apps/crawler/... -count=1 2>&1 | tail -15
go test ./apps/worker/... -count=1 2>&1 | tail -10
```
Expected: all PASS. Any test that depended on inline enrichment must be updated to reflect the new design.

- [ ] **Step 7: Commit**

```bash
git add apps/crawler/service/crawl_request_handler.go apps/crawler/cmd/main.go pkg/repository/crawl.go apps/crawler/service/crawl_request_handler_test.go
git commit -m "feat(crawler): stop inline LLM enrichment

URL-only stubs now flow to the raw_payloads queue and are picked up
by the enricher worker. Connector-complete records emit variants
directly and their raw_payloads row is marked 'skipped' for audit.
Removes 100+ lines of enrich plumbing — that logic now lives in
apps/worker/service/enricher.go where it can scale independently."
```

---

### Task 7: End-to-end integration test

Round-trip: crawl → raw_payloads pending → enricher tick → variants.ingested emitted.

**Files:**
- Create: `apps/worker/service/enricher_integration_test.go`

- [ ] **Step 1: Write test**

```go
func TestEnricher_EndToEnd_PendingToEmitted(t *testing.T) {
	ctx := context.Background()
	pool := frametest.PostgresPool(t)

	// 1. Seed via the crawler path (or directly via SaveRawPayload).
	crawlRepo := repository.NewCrawlRepository(pool)
	job := &domain.CrawlJob{
		SourceID: "src-e2e", ScheduledAt: time.Now().UTC(),
		Status: domain.CrawlScheduled, IdempotencyKey: "src-e2e:t",
	}
	_ = crawlRepo.Create(ctx, job)
	rp := &domain.RawPayload{
		CrawlJobID:  job.ID,
		SourceID:    "src-e2e",
		SourceURL:   "https://example.com/job/123",
		StorageURI:  "raw/abc.html.gz",
		ContentHash: "abc",
		FetchedAt:   time.Now().UTC(),
		HTTPStatus:  200,
		Status:      domain.RawPayloadStatusPending,
	}
	_ = crawlRepo.SaveRawPayload(ctx, rp)

	// 2. Construct the enricher with a fake extractor.
	fakeArchive := &archive.FakeArchive{}
	fakeArchive.Put("abc", []byte("<html>job listing</html>"))
	fakeExtractor := &extraction.FakeExtractor{
		Result: &extraction.Result{Title: "Software Engineer", Kind: "job"},
	}
	emitter := &frametest.EventCapture{}
	h := service.NewEnricherHandler(service.EnricherDeps{
		Queue:     repository.NewRawPayloadQueue(pool),
		Archive:   fakeArchive,
		Extractor: fakeExtractor,
		Emitter:   emitter,
		Sources:   &fakeSourceRepo{kinds: []string{"job"}},
		WorkerID:  "test",
	})

	// 3. Tick.
	processed, err := h.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d; want 1", processed)
	}

	// 4. Assert event emitted with the right raw_payload_id link.
	if len(emitter.Emitted) != 1 {
		t.Fatalf("emitted = %d; want 1", len(emitter.Emitted))
	}
	event := emitter.Emitted[0].Payload.(eventsv1.VariantIngestedV1)
	if event.RawPayloadID != rp.ID {
		t.Fatalf("event.RawPayloadID = %q; want %q", event.RawPayloadID, rp.ID)
	}

	// 5. Row is enriched.
	row, _ := repository.NewRawPayloadQueue(pool).GetByID(ctx, rp.ID)
	if row.Status != domain.RawPayloadStatusEnriched {
		t.Fatalf("row.Status = %q; want enriched", row.Status)
	}
}
```

- [ ] **Step 2: Run, verify pass**

```bash
go test ./apps/worker/service/ -run TestEnricher_EndToEnd -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add apps/worker/service/enricher_integration_test.go
git commit -m "test(enricher): end-to-end pending→emitted round-trip"
```

---

### Task 8: Operator visibility — GET /admin/raw_payloads/backlog

**Files:**
- Modify: `apps/api/cmd/main.go` or whichever app owns admin endpoints.
- Create: `pkg/repository/raw_payload_admin.go`

- [ ] **Step 1: Implement backlog query**

```go
// pkg/repository/raw_payload_admin.go
func (q *RawPayloadQueue) BacklogSummary(ctx context.Context) ([]BacklogRow, error) {
    var rows []BacklogRow
    err := q.db(ctx, true).Raw(`
        SELECT status,
               count(*) AS rows,
               max(now() - fetched_at) AS oldest_age,
               max(attempt_count) AS max_attempts
        FROM raw_payloads
        WHERE status IN ('pending', 'enriching', 'failed')
        GROUP BY status
        ORDER BY status
    `).Scan(&rows).Error
    return rows, err
}

type BacklogRow struct {
    Status      string        `json:"status"`
    Rows        int64         `json:"rows"`
    OldestAge   time.Duration `json:"oldest_age_seconds"`
    MaxAttempts int           `json:"max_attempts"`
}
```

- [ ] **Step 2: Wire endpoint in apps/api/cmd/main.go**

```go
mux.HandleFunc("GET /admin/raw_payloads/backlog", requireAdmin(rawPayloadBacklogHandler))
```

- [ ] **Step 3: Build + commit**

```bash
go build ./... 2>&1 | tail -5
git add pkg/repository/raw_payload_admin.go apps/api/cmd/main.go
git commit -m "feat(admin): GET /admin/raw_payloads/backlog for enricher queue depth"
```

---

### Task 9: Tag and deploy

- [ ] **Step 1: Push, tag, deploy**

```bash
git push origin main
git tag v8.0.59
git push origin v8.0.59
```

- [ ] **Step 2: Verify in cluster**

After Flux rolls the worker:

```bash
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "SELECT status, count(*) FROM raw_payloads GROUP BY status"
```

Expected to see `pending` count dropping and `enriched` count growing.

Watch enricher logs:

```bash
kubectl logs -n product-opportunities -l app.kubernetes.io/name=opportunities-worker --tail=100 | grep enricher
```

---

## Phase 2 Exit Criteria

- [ ] `raw_payloads.status='pending'` rows drain at >= 1 row/sec on average (LLM-bound).
- [ ] Crawler throughput is no longer bottlenecked on LLM — `crawl.requests.v1` consumer lag stays near zero.
- [ ] Enricher worker can be scaled (more replicas → more concurrent claims).
- [ ] When the LLM service is down, `raw_payloads.status` accumulates as `pending` (visible via `/admin/raw_payloads/backlog`), and the crawler keeps running.
- [ ] All previous tests pass.
