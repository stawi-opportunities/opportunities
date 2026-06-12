# Resumable, bounded-slice crawling — design

**Date:** 2026-06-12
**Status:** approved (brainstorming) → implementation
**Author:** crawler team

## Problem

A crawl runs as **one NATS message**: the handler loops `for iter.Next(ctx)` and
drains every page of a source in a single invocation
(`apps/crawler/service/crawl_request_handler.go`). Two consequences make large
sources unsafe:

1. **Recipe sources never checkpoint.** `recipeconn.ConnectorIterator.Cursor()`
   returns `nil` and it implements neither `CheckpointableIterator` nor a resume
   seed. `recipe.PageState{url,page,cursor}` lives only in memory. A crash,
   redeploy, or `ack_wait` (300s) overrun restarts from page 0 / tenant 0. After
   the connector→recipe migration this is *every* high-volume source, including
   Greenhouse's 1117-tenant aggregate (≈220–560s of sequential GETs — already at
   the edge of `ack_wait`).
2. **Single-message drain can't express millions of jobs.** One handler
   invocation must finish within `ack_wait`; a board with millions of records
   cannot. Raising `ack_wait` to hours just makes one un-acked message fragile —
   a single pod restart loses the whole pass because nothing is persisted on the
   recipe path.

Legacy connectors (workday, smartrecruiters, sitemapcrawler, universal) have
per-page checkpoint resume via `crawl_checkpoints`, but it is still
single-message drain bounded by `ack_wait`.

## Requirements (locked during brainstorming)

- **Uniform:** one resumable, bounded path for **every** source — no per-source
  "large mode" flag, no bimodal code. A tiny board simply finishes in one slice.
- **Continuation:** after a bounded slice, the handler **self-re-enqueues**, and
  the continuation emit is **admitted through the existing backpressure gate**
  (`admit.Admit(ctx, TopicCrawlRequests, 1)`) — fast when healthy, paced when
  downstream is saturated.
- **Slice bound:** a slice ends after **K pages OR T seconds, whichever first**
  (both configurable). T stays safely under `ack_wait`; K bounds chatty tiny
  pages.
- **Completeness:** drive one full pass across slices; rely on existing
  idempotent keys (`HardKey` + `content_hash`) so slice-boundary overlap never
  double-counts. Jobs added mid-pass are caught next scheduled pass. An active
  continuation chain is **never discarded as stale** (unlike today's 6h rule for
  abandoned checkpoints).

## Approach: a `crawl_runs` state machine (Approach 2)

A first-class **crawl run** is the durable source of truth for resumption. It
subsumes `crawl_checkpoints` (one source of truth, not two; the legacy connector
path moves onto it too).

### Section 1 — Data model & migrations

`domain.CrawlRun` embeds `domain.BaseModel` (xid PK, `created_at/updated_at/
deleted_at`, `BeforeCreate`; **not** Frame's `data.BaseModel` — this project has
no tenant/partition concept, matching every other crawler model):

| Column | Purpose |
|---|---|
| `id` (xid PK), `source_id` (indexed) | identity |
| `status` `running\|paused\|completed\|failed` | lifecycle |
| `cursor` (jsonb, opaque) | serialized resume state (recipe `PageState` or connector `CheckpointState.Cursor`) |
| `page_idx`, `last_url` | progress (greenhouse: `page_idx` = tenant index) |
| `slice_count` | slices/messages consumed by this run |
| `jobs_found/emitted/rejected` | running totals across slices |
| `owner`, `lease_expires_at` | single-flight lease + crash recovery |
| `attempt` | continuation/retry counter |
| `scheduled_at`, `started_at`, `last_progress_at`, `completed_at` | scheduling tie-in + observability |
| `error_code`, `error_message` | failure detail |

**Schema lands via the two-part split the `golang-patterns` skill mandates — no
`FinalizeSchema`/Go `db.Exec` DDL anywhere:**

1. **Table + plain indexes → AutoMigrate.** Add `&domain.CrawlRun{}` to the
   `dbPool.Migrate(ctx, path, …)` list in `pkg/repository/migrate.go`
   (`pool.Migrate` AutoMigrates models before applying SQL files — same path
   `SourceRecipe` uses). `crawl_runs` is a plain mutable OLTP table, not a
   hypertable.
2. **Partial unique index → SQL migration file** (`apps/crawler/migrations/0001/
   <ts>_crawl_runs_active_uniq.sql`), one idempotent statement, applied by the
   migrator as the app role:
   ```sql
   CREATE UNIQUE INDEX IF NOT EXISTS idx_crawl_runs_active
       ON crawl_runs (source_id) WHERE status IN ('running','paused');
   ```
   This is the **hard single-flight** guarantee: at most one in-progress run per
   source, enforced by the DB.
3. **Retire `crawl_checkpoints` → SQL migration file**, one statement:
   `DROP TABLE IF EXISTS crawl_checkpoints;` (after code is switched to
   `crawl_runs`). In-flight checkpoints at cutover are lost → affected sources
   start one fresh pass.

Rules honoured: one statement per file (frame v1.98+ runs each as a single
prepared statement); `IF NOT EXISTS`/`IF EXISTS` idempotency; migrator-applied as
the app role. A `CrawlRunRepository` (interface + struct) wraps all access; lease
claims read the **primary** (`DB(ctx, false)`), never the replica.

### Section 2 — State machine, slice loop & continuation

States: `running` → `completed` (clean full pass) | `failed` (terminal) |
`paused` (backpressure / operator hold).

```
  scheduler tick ──► INSERT run (status=running)        [unique index = single-flight]
       │                  │ on conflict with an active run → no-op (drop delivery)
       ▼                  ▼
   ┌───────────────►  running  ───────────────────────────────┐
   │  CLAIM lease (owner, lease_expires_at = now+lease_ttl)    │
   │  seed iterator from cursor                                │ iterator done → completed
   │  run ONE slice: until K pages OR T seconds                │ terminal err   → failed
   │    per page: emit variants (today's path),                │
   │      persist cursor+page_idx, renew lease, last_progress  │
   │  more pages remain →                                      │
   │    persist cursor, slice_count++, RELEASE lease,          │
   │    admit-gated emit continuation crawl.request  ──────────┘
   └──────────────────────  continuation (RunID set) ──────────┘
```

- **Start vs resume:** a `crawl.requests` event with `RunID` set → load & resume
  that run; absent → fresh scheduled start (try-insert run row).
- **Single-flight at start:** the insert hits `idx_crawl_runs_active`; a unique
  violation means a run is already active for the source → drop the delivery
  (don't start a second pass).
- **Claim:** conditional `UPDATE … SET owner, lease_expires_at WHERE id=? AND
  (owner IS NULL OR lease_expires_at < now())` RETURNING; zero rows updated →
  another consumer owns it → drop. This prevents two consumers processing the
  same continuation concurrently.
- **Bounded slice:** loop pages until `pagesThisSlice >= K` or
  `elapsed >= T`. Per page: persist cursor + renew lease (keeps
  `lease_expires_at` ahead of `ack_wait` so the watchdog won't steal an
  actively-progressing run).
- **Yield:** iterator `done` → `Complete` (status=completed, completed_at,
  release lease, emit final page-completed). More pages → persist cursor, release
  lease, `admit.Admit` then emit continuation `crawl.requests{RunID,IsContinuation}`.
  If `admit` denies (backpressure) → leave run `running` with cursor persisted,
  owner released; the **watchdog** re-drives it later (work is never dropped).
- **Continuation event fields:** add `RunID string` and `IsContinuation bool`
  (both `omitempty`) to `CrawlRequestV1` — backward compatible.

### Section 3 — Reliability & self-healing

The **run row is the source of truth; NATS messages are nudges.** Every failure
mode recovers from the durable run:

- **Pod crash / redeploy mid-slice:** the lease expires (`lease_expires_at`
  passes — it is renewed every page, so a live slice keeps it fresh; a dead owner
  lets it lapse). The watchdog finds `status='running' AND lease_expires_at <
  now()` and re-emits an admit-gated continuation (`RunID` set), re-stamping
  `lease_expires_at = now()+lease_ttl` first so it doesn't double-emit before the
  continuation is picked up. Cursor is durable from the last persisted page →
  resume with at most one slice of reprocessing (idempotent dedup absorbs it).
- **Lost continuation message** (NATS drop, admit denial): same watchdog sweep
  re-drives it. The system converges without operator action.
- **Stuck run:** `running` with `attempt` over a max and no progress past a hard
  ceiling → mark `failed` (`error_code=stuck`), emit telemetry/alert, stop
  looping. Operator endpoint can reset/clear a run.
- **Staleness:** the 6h rule applies only to **abandoned** runs (no active lease,
  no progress, not a live chain). An actively-progressing chain is never
  discarded mid-pass.
- **Idempotency:** unchanged from today — `HardKey` + `content_hash` + the
  inbox/outbox dedup mean slice-boundary overlap and crash-replay never
  double-count.

Watchdog lives in the existing `apps/crawler/service/crawl_watchdog.go`, extended
with a `crawl_runs` sweep on its existing tick; admit-gated re-emit reuses the
scheduler's emit path.

### Section 4 — Recipe/connector unification, config, error handling, observability, testing

**Unification.** Today checkpoint logic is gated on `conn != nil` (legacy only).
The new path keys everything on the **run** (by `source_id`), not connector type.
Both the recipe `ConnectorIterator` and legacy connector iterators drive the same
run row. To make the recipe path participate:

- `recipe.PageState` gains a stable JSON codec — an exported
  `(Executor) StateCodec` or package funcs `MarshalState(PageState) ([]byte,…)` /
  `UnmarshalState([]byte) (PageState,…)`. PageState fields stay unexported; the
  codec round-trips `{url,page,cursor}` through an exported DTO.
- `recipeconn.ConnectorIterator` implements `connectors.CheckpointableIterator`
  (`Checkpoint()` serializes current state; `page_idx` = state.page) and accepts
  an initial state in its constructor (resume seed).
- The handler resolves the run's `cursor` → seeds the iterator (recipe or
  connector) uniformly; per-page it calls `CrawlRunRepository.Progress`.

**Config** (`apps/crawler/config`): `CRAWL_SLICE_MAX_PAGES` (K, default 50),
`CRAWL_SLICE_MAX_SECONDS` (T, default 120 — under the 300s `ack_wait`),
`CRAWL_RUN_LEASE_TTL_SECONDS` (default 300 — renewed every page so a slow page
never expires a live slice; a dead owner's lease lapses within one TTL and the
watchdog reclaims), `CRAWL_RUN_STUCK_MAX_ATTEMPTS` (default 20, → `failed`),
`CRAWL_RUN_STALE_HOURS` (default 6, abandoned-run discard for a fresh scheduled
start). Lease TTL ≥ slice budget so the watchdog never races an in-flight slice.

**Error handling:** transient slice fetch error → persist cursor, `attempt++`,
release lease, let the watchdog retry (or immediate admit-gated re-emit);
exceed max attempts → `failed`. Graceful 429 mid-pagination (already implemented)
folds in naturally: it ends the slice cleanly → continuation paces via admit.
Terminal error (invalid recipe, source gone) → `failed`, alert, no loop. Postgres
unavailable for a run write → fail the slice and retry (don't silently drain
un-checkpointed).

**Observability:** `crawl_runs` *is* the run dashboard — active runs, progress,
slice count, throughput, per-source resume position. Metrics:
`crawl_runs_started`, `crawl_slices_executed`, `crawl_runs_completed`,
`crawl_runs_failed`, `crawl_run_duration`, `crawl_pages_per_slice`,
`crawl_runs_resumed_by_watchdog`. `crawl_jobs` stays the per-slice audit ledger.

**Testing:**

- *Repository* (`crawl_run_test.go`): start → single-flight rejection on a second
  active insert; claim lease (conditional update, contention loses); progress
  updates cursor/page/lease; complete/fail transitions; `FindResumable` returns
  lease-expired/stalled runs and skips actively-leased ones.
- *Recipe codec* (`executor`/`recipeconn` tests): `MarshalState`/`UnmarshalState`
  round-trip; resume seeds the correct page/tenant; greenhouse-style multi-tenant
  resumes mid-list.
- *Handler* (`crawl_request_handler_test.go`): a fake N-page source drains across
  multiple slices; assert every item emitted once (dedup), `slice_count`
  increments, continuation emitted with `RunID`, run `completed` at the end;
  slice bound honoured for both K (page count) and T (fake clock); start with an
  active run is a no-op.
- *Watchdog* (`crawl_watchdog_test.go`): a `running` run with expired lease is
  re-emitted; an actively-leased run is left alone; a stuck run is `failed`.
- *Race:* `go test -race` over the handler + repository.

## File plan

- **New:** `pkg/domain/crawl_run.go` (model + `CrawlRunStatus`),
  `pkg/repository/crawl_run.go` (+ interface, + `_test.go`),
  `apps/crawler/migrations/0001/<ts>_crawl_runs_active_uniq.sql`,
  `apps/crawler/migrations/0001/<ts>_drop_crawl_checkpoints.sql`.
- **Modify:** `pkg/repository/migrate.go` (add model to AutoMigrate list),
  `pkg/events/v1/crawl.go` (`RunID`, `IsContinuation`),
  `pkg/recipe/executor.go` (state codec), `pkg/connectors/recipeconn/iterator.go`
  (Checkpointable + resume seed), `apps/crawler/service/crawl_request_handler.go`
  (run lifecycle + bounded slice + continuation + unify recipe/connector),
  `apps/crawler/service/crawl_watchdog.go` (resumable-run sweep),
  `apps/crawler/config/*` (slice/lease config), `apps/crawler/service/setup.go`
  + `apps/crawler/cmd/main.go` (wire `CrawlRunRepository`).
- **Retire:** `pkg/repository/checkpoint.go` usage (replaced by `crawl_run.go`);
  drop `crawl_checkpoints` table.

## Non-goals (this spec)

- Frontier-first default (`FrontierEnabled=true` everywhere) — complementary
  per-detail-scale track, separate spec.
- Strict single-pass snapshot consistency — impossible for live boards; rejected.
- Cross-source global rate coordination beyond the existing admit gate.
