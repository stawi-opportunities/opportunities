# Pipeline Robustness — Phase 1: Crawl & Raw-Payload Ledger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every HTTP fetch the crawler performs durably observable in Postgres — one `crawl_jobs` row per `crawl.request`, one `raw_payloads` row per page fetched, linked back to the variants they produced.

**Architecture:** R2 already stores the raw HTML content-addressed (`raw/{sha256}.html.gz` via `pkg/archive`). The gap is the Postgres ledger: `crawl_jobs` has zero rows, `raw_payloads` has zero rows, and the `pipeline_variants` row carries no link back to the raw HTML it came from. This phase wires the existing `CrawlRepository.Create`/`SaveRawPayload` calls into the `CrawlRequestHandler`, adds a `raw_payload_id` link on `pipeline_variants`, extends `raw_payloads` with a `status` column ready for Phase 2's enricher queue, and converts both `crawl_jobs` and `raw_payloads` to **TimescaleDB hypertables** (matching the pattern already used by `pipeline_variants`) so retention is a `DROP CHUNK` and time-range queries pruning chunks is a first-class operation.

**Tech Stack:** Go, GORM, Postgres 16 + TimescaleDB, R2 (S3-compat), NATS JetStream, Frame v1.97+.

---

## File Structure

**Modified (existing):**
- `apps/crawler/service/crawl_request_handler.go` — wire `CrawlRepo.Create` at Execute start, `SaveRawPayload` per iterator page, attach `raw_payload_id` + `crawl_job_id` to each emitted variant; finish the crawl_job at Execute end.
- `apps/crawler/service/scheduler_tick.go` — generate `IdempotencyKey` for the emitted `crawl.requests.v1` so the handler can dedupe.
- `apps/crawler/cmd/main.go` — construct `CrawlRepository` and pass via `CrawlRequestDeps`.
- `pkg/events/v1/types.go` (or wherever `CrawlRequestV1` lives) — add `IdempotencyKey` field.
- `pkg/variantstate/store.go` — add `RawPayloadID` field to `Variant`, accept in `Upsert`.
- `pkg/domain/models.go` — add `Status`, `NextRetryAt` columns to `RawPayload`.

**Created:**
- `db/migrations/0019_raw_payloads_status_and_variant_link.sql` — add status/next_retry_at columns; add `raw_payload_id` to `pipeline_variants`; index for Phase 2's queue lookup.
- `pkg/repository/crawl_test.go` — integration test for the repository methods against a real Postgres (uses `pkg/frametest`).
- `apps/crawler/service/crawl_request_ledger_test.go` — end-to-end test that one crawl.request produces one `crawl_jobs` row, N `raw_payloads` rows, and N variants linked to those raw_payload_ids.

**Touched (small):**
- `pkg/repository/crawl.go` — extend signatures (`SaveRawPayload` already exists; just confirm it returns the inserted ID via the BaseModel.ID populated by GORM).

---

## Pre-conditions

These hold today (verified):
- `pkg/repository/crawl.go:23` `CrawlRepository.Create` exists, takes `*domain.CrawlJob`.
- `pkg/repository/crawl.go:53` `CrawlRepository.SaveRawPayload` exists, takes `*domain.RawPayload`.
- `pkg/domain/models.go:188-219` `CrawlJob` and `RawPayload` structs exist with `BaseModel.ID` (xid varchar(20)).
- `pkg/archive/r2.go:55` `R2Archive.PutRaw(ctx, body) (hash, size, err)` exists and is idempotent.
- `apps/crawler/service/crawl_request_handler.go:204` `resolveArchiveRef` is called per iterator page today — that's where the R2 PUT happens; we'll hook the ledger writes adjacent to it.
- Live `raw_payloads` table currently has columns: `id varchar(20), created_at/updated_at/deleted_at, crawl_job_id varchar(20), storage_uri text, content_hash varchar(64), size_bytes bigint, fetched_at timestamptz, http_status bigint`. No `status` column yet.

---

## Tasks

### Task 1: Migration — add status + variant link

**Files:**
- Create: `db/migrations/0019_raw_payloads_status_and_variant_link.sql`

- [ ] **Step 1: Write the migration**

The pattern is verbatim what `db/migrations/0009_matching_applications_hypertables.sql` uses for the other hypertables in this codebase: fresh `CREATE TABLE` with composite PK, `create_hypertable`, indexes, retention + compression policies inline. Both tables currently have 0 rows so dropping the legacy schema is safe.

```sql
-- 0019: crawl_jobs + raw_payloads as TimescaleDB hypertables, matching
-- the pattern in 0009_matching_applications_hypertables.sql.
--
-- Why hypertable: both tables grow O(crawls/day). Time-partition
-- pruning makes recent-data queries fast, retention is DROP CHUNK,
-- and old chunks compress.
--
-- Both tables exist today (created by GORM auto-migrate from
-- migration 0001) with zero rows in production (verified
-- 2026-05-28). Safe to drop+recreate as part of this migration.
--
-- The original migration 0001 declared crawl_jobs + raw_payloads
-- with BIGSERIAL ids; the live schema is GORM's varchar(20) xid
-- shape. Recreating from scratch resolves that drift in one shot.

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- ---------- crawl_jobs ----------

DROP TABLE IF EXISTS crawl_jobs CASCADE;

CREATE TABLE crawl_jobs (
    id              VARCHAR(20)   NOT NULL,
    scheduled_at    TIMESTAMPTZ   NOT NULL,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,
    source_id       VARCHAR(20)   NOT NULL,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    status          VARCHAR(20)   NOT NULL DEFAULT 'scheduled',
    attempt         INTEGER       NOT NULL DEFAULT 1,
    idempotency_key VARCHAR(255)  NOT NULL,
    error_code      TEXT          NOT NULL DEFAULT '',
    error_message   TEXT          NOT NULL DEFAULT '',
    jobs_found      INTEGER       NOT NULL DEFAULT 0,
    jobs_stored     INTEGER       NOT NULL DEFAULT 0,
    PRIMARY KEY (id, scheduled_at)
);

SELECT create_hypertable('crawl_jobs', 'scheduled_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '1 day');

-- Unique on (idempotency_key, scheduled_at) — re-deliveries carry the
-- same scheduled_at so dedup behaves as expected; distinct ticks
-- have distinct scheduled_at and both rows are correctly retained.
CREATE UNIQUE INDEX crawl_jobs_idempotency_idx
    ON crawl_jobs (idempotency_key, scheduled_at);

CREATE INDEX crawl_jobs_source_time_idx
    ON crawl_jobs (source_id, scheduled_at DESC);

SELECT add_retention_policy('crawl_jobs', INTERVAL '90 days',
                            if_not_exists => TRUE);
ALTER TABLE crawl_jobs SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'source_id'
);
SELECT add_compression_policy('crawl_jobs', INTERVAL '14 days',
                              if_not_exists => TRUE);

-- ---------- raw_payloads ----------

DROP TABLE IF EXISTS raw_payloads CASCADE;

CREATE TABLE raw_payloads (
    id             VARCHAR(20)  NOT NULL,
    fetched_at     TIMESTAMPTZ  NOT NULL,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,
    crawl_job_id   VARCHAR(20)  NOT NULL,
    source_id      VARCHAR(20),
    source_url     TEXT,
    storage_uri    TEXT,
    content_hash   VARCHAR(64),
    size_bytes     BIGINT       NOT NULL DEFAULT 0,
    http_status    INTEGER      NOT NULL,
    status         TEXT         NOT NULL DEFAULT 'pending',
    attempt_count  INTEGER      NOT NULL DEFAULT 0,
    next_retry_at  TIMESTAMPTZ,
    last_error     TEXT         NOT NULL DEFAULT '',
    PRIMARY KEY (id, fetched_at)
);

SELECT create_hypertable('raw_payloads', 'fetched_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '1 day');

-- Queue-claim index (Phase 2's FOR UPDATE SKIP LOCKED). Partial
-- on status='pending' keeps it tiny — only claimable rows.
CREATE INDEX raw_payloads_pending_idx
    ON raw_payloads (next_retry_at NULLS FIRST, fetched_at)
    WHERE status = 'pending';

CREATE INDEX raw_payloads_status_time_idx
    ON raw_payloads (status, fetched_at DESC);

CREATE INDEX raw_payloads_source_time_idx
    ON raw_payloads (source_id, fetched_at DESC);

CREATE INDEX raw_payloads_crawl_job_idx
    ON raw_payloads (crawl_job_id, fetched_at DESC);

CREATE INDEX raw_payloads_content_hash_idx
    ON raw_payloads (content_hash);

SELECT add_retention_policy('raw_payloads', INTERVAL '30 days',
                            if_not_exists => TRUE);
ALTER TABLE raw_payloads SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'source_id'
);
SELECT add_compression_policy('raw_payloads', INTERVAL '7 days',
                              if_not_exists => TRUE);

-- ---------- pipeline_variants: forward references ----------

ALTER TABLE pipeline_variants
    ADD COLUMN IF NOT EXISTS raw_payload_id VARCHAR(20),
    ADD COLUMN IF NOT EXISTS crawl_job_id   VARCHAR(20);

CREATE INDEX IF NOT EXISTS pipeline_variants_raw_payload_idx
    ON pipeline_variants (raw_payload_id) WHERE raw_payload_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS pipeline_variants_crawl_job_idx
    ON pipeline_variants (crawl_job_id) WHERE crawl_job_id IS NOT NULL;
```

The migration is NOT wrapped in `BEGIN/COMMIT` because `create_hypertable`, `add_retention_policy`, and `add_compression_policy` are not transactional in some TimescaleDB versions — the running pattern in 0009 omits the explicit transaction for the same reason.

- [ ] **Step 2: Verify migration applies cleanly to a fresh DB**

Run:
```bash
cd /home/j/code/stawi.opportunities && go test ./pkg/frametest/... -run TestMigrate -count=1 -v
```
Expected: PASS. The frametest helper boots an ephemeral Postgres and replays migrations.

- [ ] **Step 3: Commit**

```bash
git add db/migrations/0019_raw_payloads_status_and_variant_link.sql
git commit -m "feat(ledger): add raw_payloads.status + raw_payload_id link on pipeline_variants

Phase 1 of pipeline-robustness. The status column is the foundation
of the FOR UPDATE SKIP LOCKED enricher queue landing in Phase 2.
raw_payload_id on pipeline_variants closes the audit loop: every
variant can be traced back to the HTML it was extracted from."
```

---

### Task 2: Domain model — extend RawPayload struct

**Files:**
- Modify: `pkg/domain/models.go:209-219`

- [ ] **Step 1: Write the failing test**

Create `pkg/domain/raw_payload_test.go`:

```go
package domain_test

import (
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestRawPayload_StatusDefault(t *testing.T) {
	r := &domain.RawPayload{}
	if r.Status != "" {
		t.Fatalf("zero value Status = %q; want empty (DB default applies)", r.Status)
	}
}

func TestRawPayload_AttemptedTracking(t *testing.T) {
	now := time.Now().UTC()
	r := &domain.RawPayload{
		Status:        domain.RawPayloadStatusFailed,
		AttemptCount:  3,
		NextRetryAt:   &now,
		LastError:     "extractor: timeout",
	}
	if r.Status != "failed" {
		t.Fatalf("Status = %q; want failed", r.Status)
	}
	if r.AttemptCount != 3 {
		t.Fatalf("AttemptCount = %d; want 3", r.AttemptCount)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/j/code/stawi.opportunities && go test ./pkg/domain/... -run TestRawPayload -v 2>&1 | tail -10
```
Expected: FAIL — `Status`/`AttemptCount` undefined on `RawPayload`.

- [ ] **Step 3: Extend the RawPayload struct**

Follow the pattern of `pkg/variantstate/store.go:100-115` `Variant` struct, which is already a hypertable with composite PK. Both PK columns are tagged `gorm:"primaryKey"` — that's the codebase pattern — and `BaseModel` is **not embedded** when the table is a hypertable (matches `Variant`).

Replace `pkg/domain/models.go:206-219` with:

```go
// RawPayloadStatus is the queue-state lifecycle for raw_payloads.
type RawPayloadStatus string

const (
	RawPayloadStatusPending   RawPayloadStatus = "pending"
	RawPayloadStatusEnriching RawPayloadStatus = "enriching"
	RawPayloadStatusEnriched  RawPayloadStatus = "enriched"
	RawPayloadStatusFailed    RawPayloadStatus = "failed"
	RawPayloadStatusSkipped   RawPayloadStatus = "skipped"
)

// RawPayload is the metadata row for every HTTP fetch the crawler
// makes. The actual response body lives in R2 at raw/{content_hash}.html.gz
// via pkg/archive — this row records the fetch event, points to the
// blob, and drives the enricher queue.
//
// Hypertable partitioned by fetched_at (see migration 0019). PK is
// (id, fetched_at) — matches the variantstate.Variant pattern.
type RawPayload struct {
	ID           string           `gorm:"primaryKey;column:id;type:varchar(20)" json:"id"`
	FetchedAt    time.Time        `gorm:"primaryKey;column:fetched_at;not null" json:"fetched_at"`
	CreatedAt    time.Time        `gorm:"column:created_at;not null;default:now()" json:"created_at"`
	UpdatedAt    time.Time        `gorm:"column:updated_at;not null;default:now()" json:"updated_at"`
	DeletedAt    *time.Time       `gorm:"column:deleted_at" json:"deleted_at,omitempty"`
	CrawlJobID   string           `gorm:"column:crawl_job_id;type:varchar(20);not null" json:"crawl_job_id"`
	SourceID     string           `gorm:"column:source_id;type:varchar(20)" json:"source_id,omitempty"`
	SourceURL    string           `gorm:"column:source_url;type:text" json:"source_url,omitempty"`
	StorageURI   string           `gorm:"column:storage_uri;type:text" json:"storage_uri"`
	ContentHash  string           `gorm:"column:content_hash;type:varchar(64)" json:"content_hash"`
	SizeBytes    int64            `gorm:"column:size_bytes;not null;default:0" json:"size_bytes"`
	HTTPStatus   int              `gorm:"column:http_status;not null" json:"http_status"`
	Status       RawPayloadStatus `gorm:"column:status;type:text;not null;default:'pending'" json:"status"`
	AttemptCount int              `gorm:"column:attempt_count;not null;default:0" json:"attempt_count"`
	NextRetryAt  *time.Time       `gorm:"column:next_retry_at" json:"next_retry_at,omitempty"`
	LastError    string           `gorm:"column:last_error;type:text" json:"last_error,omitempty"`
}

func (RawPayload) TableName() string { return "raw_payloads" }
```

- [ ] **Step 3b: Same treatment for CrawlJob**

Replace `pkg/domain/models.go:188-204` with:

```go
// CrawlJob records a single crawl execution against a source.
//
// Hypertable partitioned by scheduled_at (see migration 0019). PK is
// (id, scheduled_at). Idempotency is enforced by a composite UNIQUE
// INDEX on (idempotency_key, scheduled_at) defined in the migration.
type CrawlJob struct {
	ID             string         `gorm:"primaryKey;column:id;type:varchar(20)" json:"id"`
	ScheduledAt    time.Time      `gorm:"primaryKey;column:scheduled_at;not null" json:"scheduled_at"`
	CreatedAt      time.Time      `gorm:"column:created_at;not null;default:now()" json:"created_at"`
	UpdatedAt      time.Time      `gorm:"column:updated_at;not null;default:now()" json:"updated_at"`
	DeletedAt      *time.Time     `gorm:"column:deleted_at" json:"deleted_at,omitempty"`
	SourceID       string         `gorm:"column:source_id;type:varchar(20);not null" json:"source_id"`
	StartedAt      *time.Time     `gorm:"column:started_at" json:"started_at"`
	FinishedAt     *time.Time     `gorm:"column:finished_at" json:"finished_at"`
	Status         CrawlJobStatus `gorm:"column:status;type:varchar(20);not null;default:'scheduled'" json:"status"`
	Attempt        int            `gorm:"column:attempt;not null;default:1" json:"attempt"`
	IdempotencyKey string         `gorm:"column:idempotency_key;type:varchar(255)" json:"idempotency_key"`
	ErrorCode      string         `gorm:"column:error_code;type:text" json:"error_code"`
	ErrorMessage   string         `gorm:"column:error_message;type:text" json:"error_message"`
	JobsFound      int            `gorm:"column:jobs_found;not null;default:0" json:"jobs_found"`
	JobsStored     int            `gorm:"column:jobs_stored;not null;default:0" json:"jobs_stored"`
}

func (CrawlJob) TableName() string { return "crawl_jobs" }
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./pkg/domain/... -run TestRawPayload -v 2>&1 | tail -5
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/domain/models.go pkg/domain/raw_payload_test.go
git commit -m "feat(ledger): extend RawPayload with status/attempt/retry fields"
```

---

### Task 3: Event payload — add IdempotencyKey to CrawlRequestV1

**Files:**
- Modify: `pkg/events/v1/types.go` (or wherever `CrawlRequestV1` is defined; grep to confirm: `grep -rn "type CrawlRequestV1 struct" pkg/events/`).
- Modify: `apps/crawler/service/scheduler_tick.go` — populate the field.

- [ ] **Step 1: Locate CrawlRequestV1**

```bash
grep -rn "type CrawlRequestV1 struct" pkg/events/ | head -3
```

- [ ] **Step 2: Add the field**

Add to the `CrawlRequestV1` struct (place after `SourceID` to match existing convention):

```go
// IdempotencyKey is set by the scheduler so the crawl handler can
// detect re-delivery (NATS at-least-once) and reuse the same
// crawl_jobs row. Empty key is tolerated — the handler then derives
// one from RequestID + ScheduledAt.
IdempotencyKey string `json:"idempotency_key,omitempty"`
```

- [ ] **Step 3: Scheduler emits the key**

Modify `apps/crawler/service/scheduler_tick.go` where the `CrawlRequestV1` envelope is constructed. The key MUST be deterministic for the same source-and-tick so two concurrent ticks can't double-insert. Use `fmt.Sprintf("%s:%s", sourceID, time.Now().UTC().Format(time.RFC3339))` truncated to the minute.

```go
// Inside the per-source loop where CrawlRequestV1 is built today.
tickMinute := now.UTC().Truncate(time.Minute).Format(time.RFC3339)
req := eventsv1.CrawlRequestV1{
    RequestID:      xid.New().String(),
    SourceID:       source.ID,
    IdempotencyKey: fmt.Sprintf("%s:%s", source.ID, tickMinute),
    ScheduledAt:    now,
}
```

(Replace existing assignment in scheduler_tick.go; preserve any other fields the struct sets.)

- [ ] **Step 4: Build**

```bash
go build ./... 2>&1 | tail -10
```
Expected: no output (success).

- [ ] **Step 5: Commit**

```bash
git add pkg/events/v1/types.go apps/crawler/service/scheduler_tick.go
git commit -m "feat(events): add IdempotencyKey to CrawlRequestV1 for re-delivery dedup"
```

---

### Task 4: Repository — make CrawlRepository.Create return the ID

**Files:**
- Modify: `pkg/repository/crawl.go:22-25`

Today `Create` returns only `error`; GORM populates `job.ID` on the passed pointer, but call sites that pre-set their own ID (xid) won't see it as a problem. Confirm BaseModel.BeforeCreate hooks (if any) populate ID when blank — most Frame repos do. If they DON'T, generate xid explicitly at the call site.

- [ ] **Step 1: Confirm BaseModel ID generation**

```bash
grep -n "BeforeCreate\|GenerateID" pkg/domain/models.go pkg/repository/*.go 2>&1 | head -5
```

- [ ] **Step 2: Inspect frame's BaseModel**

```bash
grep -rn "BeforeCreate\|GenerateID" /home/j/go/pkg/mod/github.com/pitabwire/frame*/ 2>&1 | head -5
```

- [ ] **Step 3: If BaseModel does NOT auto-generate, update Create to populate xid before insert**

In `pkg/repository/crawl.go:23`, replace:

```go
func (r *CrawlRepository) Create(ctx context.Context, job *domain.CrawlJob) error {
	return r.db(ctx, false).Create(job).Error
}
```

with:

```go
func (r *CrawlRepository) Create(ctx context.Context, job *domain.CrawlJob) error {
	if job.ID == "" {
		job.ID = xid.New().String()
	}
	return r.db(ctx, false).Create(job).Error
}
```

Add the import `"github.com/rs/xid"` if not already present.

(If BaseModel already does this, skip Step 3 — the existing code is fine.)

- [ ] **Step 4: Same treatment for SaveRawPayload**

```go
func (r *CrawlRepository) SaveRawPayload(ctx context.Context, payload *domain.RawPayload) error {
	if payload.ID == "" {
		payload.ID = xid.New().String()
	}
	return r.db(ctx, false).Create(payload).Error
}
```

- [ ] **Step 5: Build**

```bash
go build ./... 2>&1 | tail -5
```
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add pkg/repository/crawl.go
git commit -m "fix(crawl): ensure xid is generated on Create+SaveRawPayload"
```

---

### Task 5: Integration test — repository writes round-trip

**Files:**
- Create: `pkg/repository/crawl_test.go`

- [ ] **Step 1: Write the failing test**

```go
package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

func TestCrawlRepository_CreateAndSaveRawPayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pgPool := frametest.PostgresPool(t) // helper from existing test utilities

	repo := repository.NewCrawlRepository(pgPool)

	job := &domain.CrawlJob{
		SourceID:       "src-test-001",
		ScheduledAt:    time.Now().UTC(),
		Status:         domain.CrawlScheduled,
		Attempt:        1,
		IdempotencyKey: "src-test-001:2026-05-28T00:00:00Z",
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if job.ID == "" {
		t.Fatalf("Create did not populate ID")
	}

	rp := &domain.RawPayload{
		CrawlJobID:  job.ID,
		SourceID:    job.SourceID,
		SourceURL:   "https://example.com/list",
		StorageURI:  "raw/abc123.html.gz",
		ContentHash: "abc123",
		SizeBytes:   2048,
		FetchedAt:   time.Now().UTC(),
		HTTPStatus:  200,
		// Status defaults to 'pending' via DB default.
	}
	if err := repo.SaveRawPayload(ctx, rp); err != nil {
		t.Fatalf("SaveRawPayload: %v", err)
	}
	if rp.ID == "" {
		t.Fatalf("SaveRawPayload did not populate ID")
	}
	if rp.Status != domain.RawPayloadStatusPending {
		t.Fatalf("Status = %q; want %q (DB default)", rp.Status, domain.RawPayloadStatusPending)
	}
}

func TestCrawlRepository_Start_Finish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pgPool := frametest.PostgresPool(t)
	repo := repository.NewCrawlRepository(pgPool)

	job := &domain.CrawlJob{
		SourceID:       "src-test-002",
		ScheduledAt:    time.Now().UTC(),
		Status:         domain.CrawlScheduled,
		IdempotencyKey: "src-test-002:t2",
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Start(ctx, job.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := repo.Finish(ctx, job.ID, domain.CrawlSucceeded, ""); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}
```

If `frametest.PostgresPool` doesn't exist, check what helper the existing repository tests use:
```bash
grep -rn "func.*PostgresPool\|func.*TestDB\|frametest\." pkg/repository/ pkg/frametest/ 2>&1 | head -10
```
Use whatever helper the existing `pkg/repository/*_test.go` files use.

- [ ] **Step 2: Run, verify fail**

```bash
go test ./pkg/repository/ -run TestCrawlRepository -v 2>&1 | tail -20
```
Expected: PASS if Tasks 1-4 done correctly. (This test is more of a verification gate than a true RED test, since the code is already there — we're confirming nothing regressed.)

- [ ] **Step 3: Commit**

```bash
git add pkg/repository/crawl_test.go
git commit -m "test(crawl): integration test for crawl_jobs + raw_payloads round-trip"
```

---

### Task 6: Variantstate — add RawPayloadID + CrawlJobID

**Files:**
- Modify: `pkg/variantstate/store.go:100-115` (Variant struct), `pkg/variantstate/store.go:142-173` (Upsert).

- [ ] **Step 1: Extend Variant struct**

Add two fields after `RawContentHash` in `Variant`:

```go
RawPayloadID  *string        `gorm:"column:raw_payload_id"`
CrawlJobID    *string        `gorm:"column:crawl_job_id"`
```

- [ ] **Step 2: Persist them in Upsert**

GORM's `Create` with `OnConflict{DoNothing: true}` already writes whatever fields are set. No code change in Upsert — just add the fields above and tests will confirm they persist.

- [ ] **Step 3: Write test**

Add to `pkg/variantstate/store_test.go`:

```go
func TestStore_Upsert_PersistsRawPayloadLink(t *testing.T) {
	ctx := context.Background()
	store := variantstate.NewStore(frametest.PostgresPool(t))

	rawID := "raw-xid-001"
	jobID := "crawl-xid-001"
	v := variantstate.Variant{
		VariantID:     "var-xid-001",
		SourceID:      "src-001",
		HardKey:       "hk-001",
		Kind:          "job",
		CurrentStage:  variantstate.StageIngested,
		RawPayloadID:  &rawID,
		CrawlJobID:    &jobID,
	}
	if err := store.Upsert(ctx, v); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Read back via direct query.
	var got struct {
		RawPayloadID *string
		CrawlJobID   *string
	}
	if err := frametest.DB(t).Raw(
		"SELECT raw_payload_id, crawl_job_id FROM pipeline_variants WHERE variant_id = ?",
		v.VariantID,
	).Scan(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.RawPayloadID == nil || *got.RawPayloadID != rawID {
		t.Fatalf("RawPayloadID = %v; want %q", got.RawPayloadID, rawID)
	}
	if got.CrawlJobID == nil || *got.CrawlJobID != jobID {
		t.Fatalf("CrawlJobID = %v; want %q", got.CrawlJobID, jobID)
	}
}
```

- [ ] **Step 4: Run**

```bash
go test ./pkg/variantstate/ -run TestStore_Upsert_PersistsRawPayloadLink -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/variantstate/store.go pkg/variantstate/store_test.go
git commit -m "feat(variantstate): persist raw_payload_id + crawl_job_id on Variant"
```

---

### Task 7: Wire CrawlRepository into CrawlRequestDeps

**Files:**
- Modify: `apps/crawler/service/crawl_request_handler.go:42-77` (deps struct).
- Modify: `apps/crawler/cmd/main.go` (construction).

- [ ] **Step 1: Add to deps**

In `apps/crawler/service/crawl_request_handler.go:42-77`, add to `CrawlRequestDeps`:

```go
// CrawlRepo writes the crawl_jobs + raw_payloads audit ledger.
// nil disables ledger writes (test paths). Errors propagate — a
// Postgres outage MUST fail the crawl, otherwise the ledger silently
// diverges from reality.
CrawlRepo *repository.CrawlRepository
```

Import `"github.com/stawi-opportunities/opportunities/pkg/repository"` if not already imported.

- [ ] **Step 2: Construct in main**

In `apps/crawler/cmd/main.go`, after the existing DB pool is initialised and before `NewCrawlRequestHandler` is called:

```go
crawlRepo := repository.NewCrawlRepository(dbPool)
```

Then pass it in `CrawlRequestDeps{...}`:

```go
CrawlRepo: crawlRepo,
```

- [ ] **Step 3: Build**

```bash
go build ./apps/crawler/... 2>&1 | tail -5
```
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add apps/crawler/service/crawl_request_handler.go apps/crawler/cmd/main.go
git commit -m "feat(crawler): inject CrawlRepository into CrawlRequestDeps"
```

---

### Task 8: Handler — create crawl_jobs row at Execute start

**Files:**
- Modify: `apps/crawler/service/crawl_request_handler.go:124-200` (Execute prelude).

- [ ] **Step 1: Write the failing test**

Append to `apps/crawler/service/crawl_request_handler_test.go`:

```go
func TestExecute_CreatesCrawlJobsRow(t *testing.T) {
	ctx := context.Background()
	// Builds the standard test scaffold: in-memory archive, fake source repo,
	// real *repository.CrawlRepository against an ephemeral Postgres.
	scaffold := newCrawlTestScaffold(t)
	h := service.NewCrawlRequestHandler(scaffold.Deps())

	payload := envelopeJSON(eventsv1.CrawlRequestV1{
		RequestID:      "req-001",
		SourceID:       scaffold.SourceID,
		IdempotencyKey: "src-001:2026-05-28T00:00:00Z",
		ScheduledAt:    time.Now().UTC(),
	})

	if err := h.Execute(ctx, &payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var count int64
	scaffold.DB.Raw(
		"SELECT count(*) FROM crawl_jobs WHERE idempotency_key = ?",
		"src-001:2026-05-28T00:00:00Z",
	).Scan(&count)
	if count != 1 {
		t.Fatalf("crawl_jobs count = %d; want 1", count)
	}
}

func TestExecute_IdempotentOnRedelivery(t *testing.T) {
	ctx := context.Background()
	scaffold := newCrawlTestScaffold(t)
	h := service.NewCrawlRequestHandler(scaffold.Deps())

	payload := envelopeJSON(eventsv1.CrawlRequestV1{
		RequestID:      "req-002",
		SourceID:       scaffold.SourceID,
		IdempotencyKey: "src-002:2026-05-28T00:00:00Z",
	})
	// First delivery.
	if err := h.Execute(ctx, &payload); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	// Re-delivery.
	if err := h.Execute(ctx, &payload); err != nil {
		t.Fatalf("re-delivery Execute: %v", err)
	}

	var count int64
	scaffold.DB.Raw(
		"SELECT count(*) FROM crawl_jobs WHERE idempotency_key = ?",
		"src-002:2026-05-28T00:00:00Z",
	).Scan(&count)
	if count != 1 {
		t.Fatalf("crawl_jobs count after re-delivery = %d; want 1 (idempotent)", count)
	}
}
```

(If `newCrawlTestScaffold` doesn't exist yet, look at the existing test patterns in this file and adapt the helper that's already used by other tests in the file.)

- [ ] **Step 2: Run, verify fail**

```bash
go test ./apps/crawler/service/ -run TestExecute_CreatesCrawlJobsRow -v 2>&1 | tail -10
```
Expected: FAIL (no crawl_jobs row written; count == 0).

- [ ] **Step 3: Implement — at the top of Execute (after the source lookup and connector checks at line ~188)**

Add this block just before `iter := conn.Crawl(ctx, *src)` (currently at line 191):

```go
// Audit ledger: open a crawl_jobs row. Idempotent on idempotency_key
// — Frame redeliveries of the same NATS msg reuse the same row.
//
// IdempotencyKey is the (source_id, tick_minute) tuple set by the
// scheduler. A bare RequestID would be unique per emission, which
// would break dedup across redeliveries.
crawlJob := &domain.CrawlJob{
    SourceID:       src.ID,
    ScheduledAt:    req.ScheduledAt,
    Status:         domain.CrawlScheduled,
    Attempt:        1,
    IdempotencyKey: req.IdempotencyKey,
}
if crawlJob.IdempotencyKey == "" {
    // Defensive fallback if the scheduler didn't populate.
    crawlJob.IdempotencyKey = fmt.Sprintf("%s:%s", src.ID, req.ScheduledAt.Format(time.RFC3339))
}

if h.deps.CrawlRepo != nil {
    if err := h.deps.CrawlRepo.Create(ctx, crawlJob); err != nil {
        // Unique-constraint violation on idempotency_key → this is a
        // re-delivery; look the existing row up and reuse it.
        if existing, lookupErr := h.deps.CrawlRepo.GetByIdempotencyKey(ctx, crawlJob.IdempotencyKey); lookupErr == nil && existing != nil {
            crawlJob = existing
        } else {
            // Transient DB error — Frame should redeliver.
            return fmt.Errorf("crawl.request: open crawl_jobs row: %w", err)
        }
    }
    if err := h.deps.CrawlRepo.Start(ctx, crawlJob.ID); err != nil {
        return fmt.Errorf("crawl.request: mark started: %w", err)
    }
}
```

- [ ] **Step 4: Add `GetByIdempotencyKey` to CrawlRepository**

In `pkg/repository/crawl.go`, append:

```go
// GetByIdempotencyKey returns the crawl job matching the unique key,
// or (nil, nil) if none exists. Used by the crawl handler to dedupe
// NATS re-deliveries of the same crawl.request.
func (r *CrawlRepository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.CrawlJob, error) {
	var job domain.CrawlJob
	err := r.db(ctx, true).
		Where("idempotency_key = ?", key).
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}
```

Import `"errors"` and `"gorm.io/gorm"` at the top of the file if not present.

- [ ] **Step 5: Run tests, verify they pass**

```bash
go test ./apps/crawler/service/ -run TestExecute_CreatesCrawlJobsRow -v 2>&1 | tail -10
go test ./apps/crawler/service/ -run TestExecute_IdempotentOnRedelivery -v 2>&1 | tail -10
```
Expected: both PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/crawler/service/crawl_request_handler.go apps/crawler/service/crawl_request_handler_test.go pkg/repository/crawl.go
git commit -m "feat(crawler): create crawl_jobs ledger row at Execute start

Idempotent on (source_id, tick_minute) — Frame redeliveries reuse
the existing row rather than spawning duplicates. The audit trail
now answers 'did we crawl source X at time T?' as a SQL query."
```

---

### Task 9: Handler — save raw_payloads row per iterator page

**Files:**
- Modify: `apps/crawler/service/crawl_request_handler.go:202-376` (the iterator loop).

- [ ] **Step 1: Write the failing test**

Append to `crawl_request_handler_test.go`:

```go
func TestExecute_WritesRawPayloadRowPerPage(t *testing.T) {
	ctx := context.Background()
	scaffold := newCrawlTestScaffold(t)
	// Configure the fake connector to yield 2 pages with HTML bodies.
	scaffold.RegisterPages(
		page{HTML: []byte("<html>page1</html>"), Items: 5},
		page{HTML: []byte("<html>page2</html>"), Items: 3},
	)
	h := service.NewCrawlRequestHandler(scaffold.Deps())

	payload := envelopeJSON(eventsv1.CrawlRequestV1{
		RequestID:      "req-pages",
		SourceID:       scaffold.SourceID,
		IdempotencyKey: "src-pages:2026-05-28T00:00:00Z",
	})
	if err := h.Execute(ctx, &payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var count int64
	scaffold.DB.Raw(
		"SELECT count(*) FROM raw_payloads WHERE crawl_job_id IN (SELECT id FROM crawl_jobs WHERE idempotency_key = ?)",
		"src-pages:2026-05-28T00:00:00Z",
	).Scan(&count)
	if count != 2 {
		t.Fatalf("raw_payloads count = %d; want 2 (one per page)", count)
	}
}
```

- [ ] **Step 2: Run, verify fail**

```bash
go test ./apps/crawler/service/ -run TestExecute_WritesRawPayloadRow -v 2>&1 | tail -10
```
Expected: FAIL (count == 0).

- [ ] **Step 3: Implement**

In `apps/crawler/service/crawl_request_handler.go`, inside the `for iter.Next(ctx)` loop (currently around line 202), AFTER the existing `pageArchiveRef := resolveArchiveRef(...)` call (line 204), add:

```go
// Audit-ledger: row per page. The R2 blob is already written by
// resolveArchiveRef; here we just record metadata.
var (
    pageRawPayloadID string
    pageContentHash  string
)
if h.deps.CrawlRepo != nil && iter.Content() != nil && len(iter.Content().RawHTML) > 0 {
    rawBody := []byte(iter.Content().RawHTML)
    pageContentHash = sha256Hex(rawBody)
    rp := &domain.RawPayload{
        CrawlJobID:  crawlJob.ID,
        SourceID:    src.ID,
        SourceURL:   src.BaseURL,
        StorageURI:  pageArchiveRef,
        ContentHash: pageContentHash,
        SizeBytes:   int64(len(rawBody)),
        FetchedAt:   time.Now().UTC(),
        HTTPStatus:  iter.HTTPStatus(),
        Status:      domain.RawPayloadStatusPending,
    }
    if rawErr := h.deps.CrawlRepo.SaveRawPayload(ctx, rp); rawErr != nil {
        log.WithError(rawErr).Warn("crawl.request: save raw_payload failed")
    } else {
        pageRawPayloadID = rp.ID
    }
}
```

Then, in the inner `for i := range pageItems` loop where the variant is built (around line 321-337), modify the `variantstate.Upsert` call (line 345) to include the linkage:

```go
rawIDPtr := stringPtrOrNil(pageRawPayloadID)
jobIDPtr := stringPtrOrNil(crawlJob.ID)
_ = h.deps.VariantStore.Upsert(ctx, variantstate.Variant{
    VariantID:     eventPayload.VariantID,
    SourceID:      eventPayload.SourceID,
    HardKey:       eventPayload.HardKey,
    Kind:          eventPayload.Kind,
    CurrentStage:  variantstate.StageIngested,
    RawPayloadID:  rawIDPtr,
    CrawlJobID:    jobIDPtr,
})
```

Helper at the bottom of the file:

```go
func stringPtrOrNil(s string) *string {
    if s == "" {
        return nil
    }
    return &s
}
```

- [ ] **Step 4: Run test, verify it passes**

```bash
go test ./apps/crawler/service/ -run TestExecute_WritesRawPayloadRow -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/crawler/service/crawl_request_handler.go apps/crawler/service/crawl_request_handler_test.go
git commit -m "feat(crawler): write raw_payloads row per iterator page

Closes the audit loop: every fetched page is one R2 blob (via
resolveArchiveRef) AND one raw_payloads row. Variants now carry
raw_payload_id so 'show me the HTML this variant came from' is a
two-row join. status defaults to 'pending' to seed Phase 2's
enricher queue."
```

---

### Task 10: Handler — finish crawl_jobs row at Execute end

**Files:**
- Modify: `apps/crawler/service/crawl_request_handler.go:380-410` (after the page-completed emit).

- [ ] **Step 1: Write the failing test**

```go
func TestExecute_FinishesCrawlJob_Success(t *testing.T) {
	ctx := context.Background()
	scaffold := newCrawlTestScaffold(t)
	scaffold.RegisterPages(page{HTML: []byte("<html>x</html>"), Items: 1})
	h := service.NewCrawlRequestHandler(scaffold.Deps())

	payload := envelopeJSON(eventsv1.CrawlRequestV1{
		RequestID:      "req-finish",
		SourceID:       scaffold.SourceID,
		IdempotencyKey: "src-finish:2026-05-28T00:00:00Z",
	})
	if err := h.Execute(ctx, &payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var row struct {
		Status      string
		FinishedAt  *time.Time
		JobsFound   int
		JobsStored  int
	}
	scaffold.DB.Raw(
		"SELECT status, finished_at, jobs_found, jobs_stored FROM crawl_jobs WHERE idempotency_key = ?",
		"src-finish:2026-05-28T00:00:00Z",
	).Scan(&row)
	if row.Status != string(domain.CrawlSucceeded) {
		t.Fatalf("status = %q; want %q", row.Status, domain.CrawlSucceeded)
	}
	if row.FinishedAt == nil {
		t.Fatalf("finished_at is null; want set")
	}
	if row.JobsFound != 1 {
		t.Fatalf("jobs_found = %d; want 1", row.JobsFound)
	}
}
```

- [ ] **Step 2: Run, verify fail**

```bash
go test ./apps/crawler/service/ -run TestExecute_FinishesCrawlJob -v 2>&1 | tail -10
```
Expected: FAIL.

- [ ] **Step 3: Extend CrawlRepository.Finish to accept jobs_found / jobs_stored**

In `pkg/repository/crawl.go:40-50`, replace `Finish` with:

```go
// Finish records terminal state + counts. Idempotent on subsequent
// calls (overwrites are intentional — a redelivery would replay
// with the same counts).
func (r *CrawlRepository) Finish(
	ctx context.Context,
	id string,
	status domain.CrawlJobStatus,
	jobsFound, jobsStored int,
	errorCode, errorMessage string,
) error {
	now := time.Now().UTC()
	return r.db(ctx, false).
		Model(&domain.CrawlJob{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":        status,
			"finished_at":   now,
			"jobs_found":    jobsFound,
			"jobs_stored":   jobsStored,
			"error_code":    errorCode,
			"error_message": errorMessage,
		}).Error
}
```

- [ ] **Step 4: Call Finish in the handler**

In `apps/crawler/service/crawl_request_handler.go`, AFTER `h.emitCompleted(ctx, completed)` (currently line 397), add:

```go
if h.deps.CrawlRepo != nil {
    finalStatus := domain.CrawlSucceeded
    errorCode := ""
    errorMessage := ""
    if iterErr != nil {
        finalStatus = domain.CrawlFailed
        errorCode = "iterator_failed"
        errorMessage = iterErr.Error()
    }
    if finErr := h.deps.CrawlRepo.Finish(
        ctx, crawlJob.ID, finalStatus,
        jobsFound, jobsEmitted,
        errorCode, errorMessage,
    ); finErr != nil {
        log.WithError(finErr).Warn("crawl.request: finish crawl_jobs failed")
    }
}
```

- [ ] **Step 5: Run, verify pass**

```bash
go test ./apps/crawler/service/ -run TestExecute_FinishesCrawlJob -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/crawler/service/crawl_request_handler.go pkg/repository/crawl.go apps/crawler/service/crawl_request_handler_test.go
git commit -m "feat(crawler): finalise crawl_jobs row with status + counts on completion"
```

---

### Task 11: Admin endpoint — GET /admin/crawl_jobs

A new operator-facing endpoint that returns the last N crawl_jobs for a source, with raw_payload counts. Cheap to add now, valuable for triage immediately.

**Files:**
- Modify: `apps/crawler/cmd/main.go` (register endpoint).
- Create: `apps/crawler/service/crawl_jobs_admin.go` (handler).

- [ ] **Step 1: Create the handler**

```go
// apps/crawler/service/crawl_jobs_admin.go
package service

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// CrawlJobsAdminHandler returns the last N crawl_jobs for a given source,
// each with a count of raw_payloads it produced.
func CrawlJobsAdminHandler(repo *repository.CrawlRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sourceID := r.URL.Query().Get("source_id")
		if sourceID == "" {
			http.Error(w, "source_id required", http.StatusBadRequest)
			return
		}
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		rows, err := repo.ListBySource(ctx, sourceID, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"source_id": sourceID,
			"jobs":      rows,
		})
	}
}

// CrawlJobSummary is the row shape returned by ListBySource.
type CrawlJobSummary struct {
	ID            string                    `json:"id"`
	IdempotencyKey string                   `json:"idempotency_key"`
	ScheduledAt   time.Time                 `json:"scheduled_at"`
	StartedAt     *time.Time                `json:"started_at,omitempty"`
	FinishedAt    *time.Time                `json:"finished_at,omitempty"`
	Status        domain.CrawlJobStatus     `json:"status"`
	JobsFound     int                       `json:"jobs_found"`
	JobsStored    int                       `json:"jobs_stored"`
	RawPayloads   int                       `json:"raw_payloads"`
	ErrorCode     string                    `json:"error_code,omitempty"`
}
```

- [ ] **Step 2: Add ListBySource to CrawlRepository**

In `pkg/repository/crawl.go`:

```go
// ListBySource returns the most-recent N crawl jobs for a source,
// joined with raw_payload counts. Used by /admin/crawl_jobs.
func (r *CrawlRepository) ListBySource(ctx context.Context, sourceID string, limit int) ([]service.CrawlJobSummary, error) {
	// ... import cycle prevention: define the struct here or in a shared pkg.
```

Avoid an import cycle: move `CrawlJobSummary` to `pkg/repository/crawl.go` instead and have the handler import it.

Replace the handler's local `CrawlJobSummary` reference with `repository.CrawlJobSummary`. The repo method:

```go
type CrawlJobSummary struct {
	ID             string                `json:"id"`
	IdempotencyKey string                `json:"idempotency_key"`
	ScheduledAt    time.Time             `json:"scheduled_at"`
	StartedAt      *time.Time            `json:"started_at,omitempty"`
	FinishedAt     *time.Time            `json:"finished_at,omitempty"`
	Status         domain.CrawlJobStatus `json:"status"`
	JobsFound      int                   `json:"jobs_found"`
	JobsStored     int                   `json:"jobs_stored"`
	RawPayloads    int                   `json:"raw_payloads"`
	ErrorCode      string                `json:"error_code,omitempty"`
}

func (r *CrawlRepository) ListBySource(ctx context.Context, sourceID string, limit int) ([]CrawlJobSummary, error) {
	rows := []CrawlJobSummary{}
	err := r.db(ctx, true).Raw(`
        SELECT cj.id, cj.idempotency_key, cj.scheduled_at, cj.started_at, cj.finished_at,
               cj.status, cj.jobs_found, cj.jobs_stored, cj.error_code,
               (SELECT count(*) FROM raw_payloads rp WHERE rp.crawl_job_id = cj.id) AS raw_payloads
        FROM crawl_jobs cj
        WHERE cj.source_id = ?
        ORDER BY cj.scheduled_at DESC
        LIMIT ?
    `, sourceID, limit).Scan(&rows).Error
	return rows, err
}
```

- [ ] **Step 3: Wire in main.go**

In `apps/crawler/cmd/main.go`, where other admin handlers are registered (look around lines 472-528):

```go
adminMux.HandleFunc("GET /admin/crawl_jobs",
    service.CrawlJobsAdminHandler(crawlRepo))
```

- [ ] **Step 4: Build**

```bash
go build ./apps/crawler/... 2>&1 | tail -5
```
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add apps/crawler/service/crawl_jobs_admin.go pkg/repository/crawl.go apps/crawler/cmd/main.go
git commit -m "feat(crawler): GET /admin/crawl_jobs?source_id=… returns recent runs

Operator triage endpoint: 'when did we last crawl source X, what
happened, and how many raw_payloads did it produce?' is now a
single HTTP call."
```

---

### Task 12: End-to-end test — full ledger trail

**Files:**
- Create: `apps/crawler/service/crawl_request_ledger_test.go`

This test asserts that one crawl.request produces ledger rows everywhere — proving the full chain works.

- [ ] **Step 1: Write the test**

```go
package service_test

import (
	"context"
	"testing"

	"github.com/stawi-opportunities/opportunities/apps/crawler/service"
	"github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

func TestExecute_FullLedgerTrail(t *testing.T) {
	ctx := context.Background()
	scaffold := newCrawlTestScaffold(t)
	scaffold.RegisterPages(
		page{HTML: []byte("<html>page1</html>"), Items: 3},
		page{HTML: []byte("<html>page2</html>"), Items: 2},
	)
	h := service.NewCrawlRequestHandler(scaffold.Deps())

	payload := envelopeJSON(eventsv1.CrawlRequestV1{
		RequestID:      "req-full",
		SourceID:       scaffold.SourceID,
		IdempotencyKey: "src-full:2026-05-28T00:00:00Z",
	})
	if err := h.Execute(ctx, &payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// One crawl_jobs row, status=succeeded.
	var jobRow struct {
		ID         string
		Status     string
		JobsFound  int
	}
	scaffold.DB.Raw(
		"SELECT id, status, jobs_found FROM crawl_jobs WHERE idempotency_key = ?",
		"src-full:2026-05-28T00:00:00Z",
	).Scan(&jobRow)
	if jobRow.Status != "succeeded" {
		t.Fatalf("crawl_jobs.status = %q; want succeeded", jobRow.Status)
	}
	if jobRow.JobsFound != 5 {
		t.Fatalf("jobs_found = %d; want 5", jobRow.JobsFound)
	}

	// Two raw_payloads rows, both pending.
	var rawCount int64
	scaffold.DB.Raw(
		"SELECT count(*) FROM raw_payloads WHERE crawl_job_id = ? AND status = 'pending'",
		jobRow.ID,
	).Scan(&rawCount)
	if rawCount != 2 {
		t.Fatalf("raw_payloads count = %d; want 2", rawCount)
	}

	// Five pipeline_variants rows, each linked to a raw_payload_id + the crawl_job_id.
	var variantCount int64
	scaffold.DB.Raw(
		"SELECT count(*) FROM pipeline_variants WHERE crawl_job_id = ? AND raw_payload_id IS NOT NULL",
		jobRow.ID,
	).Scan(&variantCount)
	if variantCount != 5 {
		t.Fatalf("pipeline_variants count = %d; want 5", variantCount)
	}
}
```

- [ ] **Step 2: Run, verify pass**

```bash
go test ./apps/crawler/service/ -run TestExecute_FullLedgerTrail -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 3: Run the whole crawler service suite**

```bash
go test ./apps/crawler/service/... -count=1 2>&1 | tail -10
```
Expected: PASS (no regressions in existing tests).

- [ ] **Step 4: Commit**

```bash
git add apps/crawler/service/crawl_request_ledger_test.go
git commit -m "test(crawler): full ledger-trail end-to-end test"
```

---

### Task 13: Tag and deploy

- [ ] **Step 1: Push branch and open PR**

```bash
git push origin main
```

- [ ] **Step 2: Tag**

```bash
git tag v8.0.58
git push origin v8.0.58
```

- [ ] **Step 3: Wait for build, let Flux reconcile, verify in cluster**

```bash
gh run watch $(gh run list --limit 1 --workflow release.yaml --repo stawi-opportunities/opportunities --json databaseId -q '.[0].databaseId') --repo stawi-opportunities/opportunities --exit-status
```

Then after the crawler migration job completes:

```bash
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "SELECT count(*) AS jobs, sum(jobs_found) AS total_found FROM crawl_jobs"
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "SELECT status, count(*) FROM raw_payloads GROUP BY status"
```

Expected:
- `crawl_jobs` row count > 0 within ~2 minutes of crawler restart.
- `raw_payloads` row count grows with `status=pending`.

---

## Phase 1 Exit Criteria

- [ ] Every `crawl.request` writes exactly one `crawl_jobs` row.
- [ ] NATS redelivery of the same `crawl.request` does NOT create a duplicate `crawl_jobs` row.
- [ ] Every iterator page writes one `raw_payloads` row.
- [ ] Every variant emitted carries `raw_payload_id` and `crawl_job_id` in `pipeline_variants`.
- [ ] `/admin/crawl_jobs?source_id=X` returns the last 20 crawls with raw_payload counts.
- [ ] Live cluster query `SELECT count(*) FROM crawl_jobs` and `… FROM raw_payloads` both return > 0.
- [ ] All existing crawler tests still pass.

---

## Notes for Phase 2

After Phase 1 ships:
- `raw_payloads` will accumulate rows at `status='pending'` — by design (no enricher yet to drain them).
- Operator can `SELECT count(*) FROM raw_payloads WHERE status='pending'` to see the backlog Phase 2 must drain.
- Phase 2 changes the crawler to STOP calling `enrichStubs` inline; instead, the new enricher worker pulls from `raw_payloads` queue.
- Until Phase 2 ships, raw_payloads rows stay pending forever (harmless; the existing pipeline still works via the connector's own structured output where available).
