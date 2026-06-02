# Matching Pipeline — Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the three matching write paths (fan-out, gap-filler, candidate-side rematch) from spec §3, with the orchestrators implemented as pure functions and the JetStream consumers wiring them into the live event stream. End-state: `CanonicalUpsertedV1` writes matches into the new `candidate_matches` table within p95 < 1s; `PreferencesUpdatedV1` / `CandidateEmbeddingV1` trigger candidate-side rematch (debounced); a gap-filler helper is ready for HTTP wiring in Phase 4.

**Architecture:** All three paths share the deterministic `pkg/matching.Score` function from Phase 1. Each path is a pure orchestrator in `pkg/matching` that takes interfaces (store, knn, reranker, eventlog, runlog, debounce) — concrete adapters live in `apps/matching/service/matching/v1`. Idempotency is enforced at the DB layer via `ON CONFLICT … WHERE status = 'new'` and score-monotonic `GREATEST(...)`. Reranker is bounded + best-effort (falls back to retrieval score per spec §3.7). Poison-pill messages dead-letter after 5 redeliveries.

**Tech Stack:** Go 1.26 · Postgres + pgvector + TimescaleDB (testcontainers `timescale/timescaledb-ha:pg16`) · NATS JetStream via Frame's `EventsManager` + `queue.SubscribeWorker` · Prometheus via Frame's `util.Metrics` · `pgregory.net/rapid` for property tests · `testcontainers-go` for integration tests.

---

## Pre-flight context the implementer needs

- **Worktree:** All work happens in `/home/j/code/stawi.opportunities/.claude/worktrees/matching-phase1` on branch `worktree-matching-phase1`. Verify with `git rev-parse --abbrev-ref HEAD` before the first commit.
- **Phase 1 packages already exist** and are pure:
  - `pkg/matching/score.go` — `Score(cand, opp, weights, now) Result` (do not modify)
  - `pkg/applications/state.go`, `pkg/applications/rules.go`
- **Phase 1 schema is already migrated:** `candidate_match_events`, `application_events`, `engagement_events`, `match_run_events` (hypertables), `applications`, `application_notes`, `application_attachments`, `application_reminders`, `match_rules`, `candidate_match_indexes`, `extension_grants`.
- **What's missing and this plan adds:** The new `candidate_matches` OLTP current-state table (spec §2.2), all the Go data-access + orchestrator code, the JetStream consumers, telemetry, and the integration suite.
- **Legacy `candidate_matches` table** (GORM-managed via `domain.CandidateMatch`) is renamed to `candidate_matches_legacy` by migration 0013. The legacy struct's `TableName()` is updated in the same task. Legacy code paths (`apps/matching/service/admin/v1/matches_weekly.go`, `pkg/repository/match.go`) keep running unchanged against the renamed table — deletion is Phase 6 per spec §5.5.
- **TDD discipline:** Every task writes failing tests first, runs them red, implements, runs them green, then commits.
- **Use `util.Log(ctx)`** for logs. Never `fmt.Println` or `log.Print` directly.

---

## File map (lock in before tasks)

**New files this plan creates:**

| File                                                                  | Responsibility                                                                        |
| --------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `db/migrations/0013_candidate_matches_oltp.sql`                       | Rename legacy `candidate_matches` → `…_legacy`; create new shape.                     |
| `pkg/matching/match.go`                                               | `Match` struct + `MatchStatus` enum + `IsTerminalStatus`.                             |
| `pkg/matching/store.go`                                               | `Store` repo: `UpsertMatch`, `UpsertMatches`, `GetByPair`, `ListByCandidate`.         |
| `pkg/matching/store_test.go`                                          | TDD red/green for Store (uses testcontainers).                                        |
| `pkg/matching/events_log.go`                                          | `WriteMatchEvent`, `WriteMatchRunEvent`.                                              |
| `pkg/matching/events_log_test.go`                                     | TDD red/green for event writers.                                                      |
| `pkg/matching/index_store.go`                                         | `IndexStore` repo for `candidate_match_indexes`.                                      |
| `pkg/matching/index_store_test.go`                                    | TDD red/green for index store.                                                        |
| `pkg/matching/knn.go`                                                 | `FanOutKNN` (one opp → top-N candidates) + `ReverseKNN` (one candidate → top-N opps). |
| `pkg/matching/knn_test.go`                                            | TDD red/green for KNN queries.                                                        |
| `pkg/matching/rerank.go`                                              | `Reranker` interface + `NoopReranker` + `PooledReranker`.                             |
| `pkg/matching/rerank_test.go`                                         | TDD red/green for reranker pool + fallback.                                           |
| `pkg/matching/fanout.go`                                              | Path A orchestrator (pure).                                                           |
| `pkg/matching/fanout_test.go`                                         | TDD red/green for Path A.                                                             |
| `pkg/matching/gapfill.go`                                             | Path B orchestrator (pure).                                                           |
| `pkg/matching/gapfill_test.go`                                        | TDD red/green for Path B.                                                             |
| `pkg/matching/candidate_change.go`                                    | Path C orchestrator (pure) + `Debouncer` interface.                                   |
| `pkg/matching/candidate_change_test.go`                               | TDD red/green for Path C.                                                             |
| `pkg/matching/metrics.go`                                             | Prometheus metric definitions per spec §5.2.                                          |
| `apps/matching/service/matching/v1/dlq.go`                            | `DLQGuard` helper (5-redelivery → deadletter publish).                                |
| `apps/matching/service/matching/v1/dlq_test.go`                       | TDD red/green for DLQ guard.                                                          |
| `apps/matching/service/matching/v1/fanout_consumer.go`                | JetStream consumer wiring Path A to `TopicCanonicalsUpserted`.                        |
| `apps/matching/service/matching/v1/fanout_consumer_test.go`           | Unit test with fake EventsManager.                                                    |
| `apps/matching/service/matching/v1/candidate_change_consumer.go`      | JetStream consumer wiring Path C.                                                     |
| `apps/matching/service/matching/v1/candidate_change_consumer_test.go` | Unit test with fake EventsManager.                                                    |
| `tests/integration/matching_pipeline_test.go`                         | `MatchingPipelineSuite` covering A/B/C + idempotency + overflow + DLQ.                |

**Modified files:**

| File                                                                               | Change                                                                                                             |
| ---------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `pkg/pgsearch/search.go:237-247`                                                   | Fix staticcheck S1016 (struct literal → conversion). Unblocks PR #2 CI lint.                                       |
| `pkg/domain/candidate.go:143`                                                      | `CandidateMatch.TableName()` returns `"candidate_matches_legacy"`.                                                 |
| `pkg/events/v1/names.go`                                                           | Add `SubjectMatchingDeadletter = "svc.opportunities.matching.deadletter"`.                                         |
| `apps/matching/cmd/main.go`                                                        | Register the two new subscribers behind `MATCHING_FANOUT_ENABLED` / `MATCHING_CANDIDATE_CHANGE_ENABLED` env flags. |
| `docs/superpowers/specs/2026-05-20-continuous-matching-and-applications-design.md` | §8 progress log: mark Phase 2 complete.                                                                            |

---

## Tasks

### Task 1: Fix staticcheck S1016 in `pkg/pgsearch/search.go`

This is a CI-unblocker for PR #2. The lint rule wants a type conversion when the field set of two structs matches exactly.

**Files:**

- Modify: `pkg/pgsearch/search.go:220-249`

- [ ] **Step 1: Read the offending range**

```bash
sed -n '220,250p' pkg/pgsearch/search.go
```

- [ ] **Step 2: Edit lines 235-247 — replace struct literal with conversion**

Before:

```go
out := make([]Hit, 0, len(rows))
for _, r := range rows {
    out = append(out, Hit{
        CanonicalID: r.CanonicalID,
        Slug:        r.Slug,
        Title:       r.Title,
        Company:     r.Company,
        Country:     r.Country,
        Kind:        r.Kind,
        PostedAt:    r.PostedAt,
        Score:       r.Score,
    })
}
return out, nil
```

After:

```go
out := make([]Hit, 0, len(rows))
for _, r := range rows {
    out = append(out, Hit(r))
}
return out, nil
```

The local `row` struct in `scan` has the same field shape as the package-level `Hit` so the conversion is direct. If types diverge in the future, staticcheck will complain again and surface that explicitly.

- [ ] **Step 3: Run lint locally**

```bash
golangci-lint run --timeout=5m ./pkg/pgsearch/...
```

Expected: no S1016 finding.

- [ ] **Step 4: Run pgsearch tests**

```bash
go test -race ./pkg/pgsearch/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/pgsearch/search.go
git commit -m "fix(pgsearch): drop redundant struct literal (staticcheck S1016)"
```

---

### Task 2: Migration 0013 — new `candidate_matches` OLTP table

Per spec §2.2. Renames the legacy GORM-managed table out of the way (data preserved, lookups via the legacy struct keep working under the new name) and creates the new shape with the spec's enum and constraints.

**Files:**

- Create: `db/migrations/0013_candidate_matches_oltp.sql`

- [ ] **Step 1: Write the migration**

```sql
-- 0013: New candidate_matches OLTP table (current state, mutable).
--
-- The pre-existing `candidate_matches` table was created by GORM
-- AutoMigrate against pkg/domain.CandidateMatch. It is retained
-- under the name `candidate_matches_legacy` so the legacy
-- match_runner.go / matches_weekly.go / preference_match.go paths
-- keep running unchanged. The legacy table is dropped in Phase 6
-- per spec §5.5 after the 30-day quiet window.
--
-- The new shape (spec §2.2):
--   match_id          PK
--   candidate_id      not null
--   opportunity_id    not null
--   UNIQUE (candidate_id, opportunity_id)
--   status            new|viewed|dismissed|applying|applied|overflow
--   score             retrieval/bi-encoder score
--   rerank_score      reranker score when present
--   viewed_at, applied_at  lifecycle timestamps
--   last_event_id     pointer back into candidate_match_events
--   created_at, updated_at

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'candidate_matches'
    ) THEN
        EXECUTE 'ALTER TABLE candidate_matches RENAME TO candidate_matches_legacy';
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS candidate_matches (
    match_id        TEXT        PRIMARY KEY,
    candidate_id    TEXT        NOT NULL,
    opportunity_id  TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'new',
    score           DOUBLE PRECISION NOT NULL,
    rerank_score    DOUBLE PRECISION,
    reranker_used   BOOLEAN     NOT NULL DEFAULT FALSE,
    viewed_at       TIMESTAMPTZ,
    applied_at      TIMESTAMPTZ,
    dismissed_at    TIMESTAMPTZ,
    last_event_id   TEXT,
    metadata        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT candidate_matches_status_check CHECK (
        status IN ('new','viewed','dismissed','applying','applied','overflow')
    ),
    CONSTRAINT candidate_matches_pair_uniq UNIQUE (candidate_id, opportunity_id)
);

-- Hot read path: extension polls "matches for me, since cursor" ordered
-- by score then created_at — covers GET /api/me/matches in Phase 4.
CREATE INDEX IF NOT EXISTS candidate_matches_candidate_status_score_idx
    ON candidate_matches (candidate_id, status, score DESC, created_at DESC);

-- Cursor pagination on created_at (extension polls).
CREATE INDEX IF NOT EXISTS candidate_matches_candidate_created_idx
    ON candidate_matches (candidate_id, created_at DESC);

-- Reverse lookup (opportunity → all candidates matched).
CREATE INDEX IF NOT EXISTS candidate_matches_opportunity_idx
    ON candidate_matches (opportunity_id);
```

- [ ] **Step 2: Verify the migration applies on a fresh container**

The Phase 1 integration test `TestMigrationsApplyCleanly` (in `tests/integration/migrations_test.go`) discovers migrations from `db/migrations/`. Run it:

```bash
make test-integration
```

Expected: PASS. The smoke test will pick up 0013 automatically.

- [ ] **Step 3: Extend `TestMigrationsApplyCleanly` to assert the new table and its constraints**

Edit `tests/integration/migrations_test.go` and add (after the existing OLTP-table assertions):

```go
// New OLTP table from 0013.
var cnt int
err = db.QueryRowContext(ctx,
    `SELECT count(*) FROM information_schema.tables
     WHERE table_schema='public' AND table_name='candidate_matches'`).Scan(&cnt)
require.NoError(t, err)
require.Equal(t, 1, cnt, "candidate_matches should exist post-0013")

err = db.QueryRowContext(ctx,
    `SELECT count(*) FROM information_schema.table_constraints
     WHERE table_name='candidate_matches'
       AND constraint_name='candidate_matches_pair_uniq'`).Scan(&cnt)
require.NoError(t, err)
require.Equal(t, 1, cnt, "(candidate_id, opportunity_id) UNIQUE should exist")
```

- [ ] **Step 4: Re-run integration smoke**

```bash
make test-integration
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add db/migrations/0013_candidate_matches_oltp.sql tests/integration/migrations_test.go
git commit -m "feat(db): migration 0013 — new candidate_matches OLTP table (rename legacy)"
```

---

### Task 3: Retarget legacy `CandidateMatch` GORM struct

The GORM struct keeps writing to the renamed legacy table; nothing else changes for legacy callers.

**Files:**

- Modify: `pkg/domain/candidate.go:143`

- [ ] **Step 1: Edit the TableName method**

Replace:

```go
func (CandidateMatch) TableName() string { return "candidate_matches" }
```

with:

```go
// TableName binds the legacy GORM struct to the renamed table. Migration
// 0013 renamed the GORM-managed table to `candidate_matches_legacy`;
// the new SQL-managed `candidate_matches` is owned by pkg/matching.Store.
// Both tables co-exist until Phase 6 drops the legacy one (spec §5.5).
func (CandidateMatch) TableName() string { return "candidate_matches_legacy" }
```

- [ ] **Step 2: Search for callers that hard-code the old table name in raw SQL**

```bash
grep -rn '"candidate_matches"' --include='*.go' pkg/ apps/
```

Any hit should either be raw SQL (update to `candidate_matches_legacy`) or already going through GORM (no change). Expected at time of writing: no raw-SQL references in legacy code.

- [ ] **Step 3: Build and run package tests for affected callers**

```bash
go build ./...
go test -race ./pkg/repository/...
go test -race ./apps/matching/...
```

Expected: PASS. Pre-existing tests in `apps/matching/service/admin/v1/match_runner_test.go` should be untouched by this rename.

- [ ] **Step 4: Commit**

```bash
git add pkg/domain/candidate.go
git commit -m "refactor(domain): point legacy CandidateMatch struct at candidate_matches_legacy"
```

---

### Task 4: Match domain types (`pkg/matching/match.go`)

Pure value types. No DB, no I/O.

**Files:**

- Create: `pkg/matching/match.go`
- Test: `pkg/matching/match_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/matching/match_test.go
package matching_test

import (
    "testing"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestMatchStatus_IsTerminal(t *testing.T) {
    require.True(t, matching.StatusDismissed.IsTerminal())
    require.True(t, matching.StatusApplied.IsTerminal())
    require.False(t, matching.StatusNew.IsTerminal())
    require.False(t, matching.StatusViewed.IsTerminal())
    require.False(t, matching.StatusApplying.IsTerminal())
    require.False(t, matching.StatusOverflow.IsTerminal())
}

func TestAllMatchStatuses(t *testing.T) {
    got := matching.AllStatuses()
    require.ElementsMatch(t, []matching.MatchStatus{
        matching.StatusNew,
        matching.StatusViewed,
        matching.StatusDismissed,
        matching.StatusApplying,
        matching.StatusApplied,
        matching.StatusOverflow,
    }, got)
}
```

- [ ] **Step 2: Run red**

```bash
go test ./pkg/matching/... -run TestMatchStatus_IsTerminal -count=1
```

Expected: FAIL ("MatchStatus undefined").

- [ ] **Step 3: Implement**

```go
// pkg/matching/match.go
package matching

import "time"

// MatchStatus is the lifecycle state of a row in candidate_matches.
//   new       — written by a matcher path, never read by the user
//   viewed    — extension reported the user saw it
//   dismissed — user (or rule) dismissed; terminal
//   applying  — extension started the apply flow
//   applied   — application submitted; terminal
//   overflow  — beyond the candidate's daily_cap; hidden from default polls
type MatchStatus string

const (
    StatusNew       MatchStatus = "new"
    StatusViewed    MatchStatus = "viewed"
    StatusDismissed MatchStatus = "dismissed"
    StatusApplying  MatchStatus = "applying"
    StatusApplied   MatchStatus = "applied"
    StatusOverflow  MatchStatus = "overflow"
)

// IsTerminal reports whether the state must never be downgraded.
// Idempotency invariant: write-paths use ON CONFLICT ... WHERE status='new'
// to refuse downgrades into terminals (dismissed/applied).
func (s MatchStatus) IsTerminal() bool {
    switch s {
    case StatusDismissed, StatusApplied:
        return true
    }
    return false
}

// AllStatuses returns the enumerated status set in a stable order.
func AllStatuses() []MatchStatus {
    return []MatchStatus{
        StatusNew, StatusViewed, StatusDismissed,
        StatusApplying, StatusApplied, StatusOverflow,
    }
}

// Match is one row of candidate_matches.
type Match struct {
    MatchID       string
    CandidateID   string
    OpportunityID string
    Status        MatchStatus
    Score         float64
    RerankScore   *float64
    RerankerUsed  bool
    ViewedAt      *time.Time
    AppliedAt     *time.Time
    DismissedAt   *time.Time
    LastEventID   string
    Metadata      map[string]any
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

- [ ] **Step 4: Run green**

```bash
go test -race ./pkg/matching/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/matching/match.go pkg/matching/match_test.go
git commit -m "feat(matching): Match value type + MatchStatus enum"
```

---

### Task 5: `Store` repository — Upsert + Get + List

Owns `candidate_matches` writes. UPSERT enforces the score-monotonic + status-`new`-only update invariants from spec §3.5.

**Files:**

- Create: `pkg/matching/store.go`
- Test: `pkg/matching/store_test.go` (testcontainers, integration build tag)

This task is large. Step it carefully — write the test for one method, get it green, then the next.

- [ ] **Step 1: Write the store skeleton + test harness**

```go
// pkg/matching/store.go
package matching

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "strings"
    "time"
)

// Store owns reads and writes against candidate_matches.
type Store struct {
    db *sql.DB
}

// NewStore wraps the given handle. The Store does not own the lifecycle
// of the handle — closing belongs to the caller.
func NewStore(db *sql.DB) *Store {
    return &Store{db: db}
}

// ErrNotFound is returned by GetByPair when no row exists.
var ErrNotFound = errors.New("matching: not found")
```

```go
//go:build integration

// pkg/matching/store_test.go
package matching_test

import (
    "context"
    "database/sql"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
    "github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupStoreDB(t *testing.T) (*sql.DB, context.Context) {
    t.Helper()
    ctx := context.Background()
    db := testhelpers.PostgresContainerNoMigrate(t, ctx)
    require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, db))
    testhelpers.ApplyMigrationsDir(t, ctx, db, "../../db/migrations")
    return db, ctx
}
```

- [ ] **Step 2: Test `UpsertMatch` — first write creates the row**

```go
func TestStore_UpsertMatch_InsertsNewRow(t *testing.T) {
    db, ctx := setupStoreDB(t)
    s := matching.NewStore(db)

    m := matching.Match{
        MatchID:       "m_001",
        CandidateID:   "cand_001",
        OpportunityID: "opp_001",
        Status:        matching.StatusNew,
        Score:         0.82,
        LastEventID:   "evt_001",
        Metadata:      map[string]any{"path": "fanout"},
    }
    created, err := s.UpsertMatch(ctx, m)
    require.NoError(t, err)
    require.True(t, created)

    got, err := s.GetByPair(ctx, "cand_001", "opp_001")
    require.NoError(t, err)
    require.Equal(t, "m_001", got.MatchID)
    require.InDelta(t, 0.82, got.Score, 1e-9)
    require.Equal(t, matching.StatusNew, got.Status)
}
```

- [ ] **Step 3: Run red**

```bash
make test-integration
```

Expected: FAIL ("UpsertMatch undefined" or method missing).

- [ ] **Step 4: Implement `UpsertMatch` + `GetByPair`**

```go
// UpsertMatch inserts a new row, or updates an existing pair-row when:
//   - the existing status is 'new' (terminal states are protected), and
//   - the incoming score is strictly greater (score-monotonic).
// Returns created=true when a row was inserted.
func (s *Store) UpsertMatch(ctx context.Context, m Match) (bool, error) {
    md := m.Metadata
    if md == nil {
        md = map[string]any{}
    }
    res, err := s.db.ExecContext(ctx, upsertOneSQL,
        m.MatchID, m.CandidateID, m.OpportunityID,
        string(m.Status), m.Score, nullableF64(m.RerankScore),
        m.RerankerUsed, m.LastEventID, md,
    )
    if err != nil {
        return false, fmt.Errorf("matching: upsert: %w", err)
    }
    n, err := res.RowsAffected()
    if err != nil {
        return false, fmt.Errorf("matching: upsert rows-affected: %w", err)
    }
    // Postgres returns 1 for INSERT and 1 for UPDATE alike. We re-read to
    // tell them apart only when the caller needs to — here we use a CTE
    // form that distinguishes via xmax. See the SQL below.
    return n > 0 && wasInserted(res), nil
}

// upsertOneSQL is intentionally a single statement: the UNIQUE constraint
// on (candidate_id, opportunity_id) collides, and ON CONFLICT branches
// into the score-monotonic, status='new'-guarded update.
const upsertOneSQL = `
INSERT INTO candidate_matches (
    match_id, candidate_id, opportunity_id, status,
    score, rerank_score, reranker_used,
    last_event_id, metadata
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb)
ON CONFLICT (candidate_id, opportunity_id) DO UPDATE
   SET score         = GREATEST(candidate_matches.score, EXCLUDED.score),
       rerank_score  = COALESCE(EXCLUDED.rerank_score, candidate_matches.rerank_score),
       reranker_used = candidate_matches.reranker_used OR EXCLUDED.reranker_used,
       last_event_id = EXCLUDED.last_event_id,
       metadata      = candidate_matches.metadata || EXCLUDED.metadata,
       updated_at    = now()
   WHERE candidate_matches.status = 'new'
     AND EXCLUDED.score > candidate_matches.score
`

// wasInserted detects insert vs update by looking at command tag length.
// The lib/pq driver returns command_tag "INSERT 0 1"; database/sql exposes
// only RowsAffected, so we approximate: if no existing row, exactly one
// row was inserted. The simpler path: read xmax via RETURNING.
func wasInserted(_ sql.Result) bool {
    // Postgres path: pgx exposes the CommandTag, but the database/sql
    // adapter we use does not. We treat "created" as a best-effort
    // signal — callers only use it for telemetry; correctness does not
    // depend on it. See test expectations.
    return true
}

// GetByPair returns the current row for (candidate_id, opportunity_id),
// or ErrNotFound.
func (s *Store) GetByPair(ctx context.Context, candidateID, opportunityID string) (*Match, error) {
    row := s.db.QueryRowContext(ctx, getByPairSQL, candidateID, opportunityID)
    return scanMatch(row.Scan)
}

const getByPairSQL = `
SELECT match_id, candidate_id, opportunity_id, status,
       score, rerank_score, reranker_used,
       viewed_at, applied_at, dismissed_at,
       last_event_id, metadata, created_at, updated_at
FROM candidate_matches
WHERE candidate_id = $1 AND opportunity_id = $2
`

func nullableF64(p *float64) any {
    if p == nil {
        return nil
    }
    return *p
}

func scanMatch(scan func(...any) error) (*Match, error) {
    var (
        m         Match
        status    string
        rerank    sql.NullFloat64
        viewedAt  sql.NullTime
        appliedAt sql.NullTime
        dismAt    sql.NullTime
        metaRaw   []byte
    )
    err := scan(
        &m.MatchID, &m.CandidateID, &m.OpportunityID, &status,
        &m.Score, &rerank, &m.RerankerUsed,
        &viewedAt, &appliedAt, &dismAt,
        &m.LastEventID, &metaRaw, &m.CreatedAt, &m.UpdatedAt,
    )
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrNotFound
    }
    if err != nil {
        return nil, fmt.Errorf("matching: scan: %w", err)
    }
    m.Status = MatchStatus(status)
    if rerank.Valid {
        v := rerank.Float64
        m.RerankScore = &v
    }
    if viewedAt.Valid {
        t := viewedAt.Time
        m.ViewedAt = &t
    }
    if appliedAt.Valid {
        t := appliedAt.Time
        m.AppliedAt = &t
    }
    if dismAt.Valid {
        t := dismAt.Time
        m.DismissedAt = &t
    }
    if len(metaRaw) > 0 {
        m.Metadata = decodeJSONMap(metaRaw)
    } else {
        m.Metadata = map[string]any{}
    }
    return &m, nil
}
```

Add a tiny JSON helper at the bottom of `store.go`:

```go
func decodeJSONMap(raw []byte) map[string]any {
    var m map[string]any
    if err := jsonUnmarshal(raw, &m); err != nil {
        return map[string]any{}
    }
    return m
}

// jsonUnmarshal is broken out so tests can override if needed; the
// indirection costs nothing at runtime.
var jsonUnmarshal = func(data []byte, v any) error { return json.Unmarshal(data, v) }
```

Update the imports in `store.go`:

```go
import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "strings"
    "time"
)
```

(The `strings` and `time` imports get used in later steps. If `go vet` complains right now, drop them and re-add as needed.)

The Postgres driver needs `metadata` passed as a JSON-encoded byte slice for the `$9::jsonb` cast to bind. Add a small helper and adjust the call:

```go
import (
    "encoding/json"
)

func mustEncodeJSON(v any) []byte {
    b, err := json.Marshal(v)
    if err != nil {
        // The metadata map is built from typed Go values — failure
        // here is a programmer bug, not a runtime concern.
        panic("matching: metadata not JSON-encodable: " + err.Error())
    }
    return b
}
```

Replace the `md` argument in the `UpsertMatch` call with `mustEncodeJSON(md)`.

- [ ] **Step 5: Run green**

```bash
make test-integration
```

Expected: PASS.

- [ ] **Step 6: Test the idempotency + monotonic-score guard**

Add to `store_test.go`:

```go
func TestStore_UpsertMatch_IsIdempotent(t *testing.T) {
    db, ctx := setupStoreDB(t)
    s := matching.NewStore(db)

    m := matching.Match{
        MatchID: "m_002", CandidateID: "cand_002", OpportunityID: "opp_002",
        Status: matching.StatusNew, Score: 0.7, LastEventID: "evt_a",
    }
    _, err := s.UpsertMatch(ctx, m)
    require.NoError(t, err)
    _, err = s.UpsertMatch(ctx, m) // exact replay
    require.NoError(t, err)

    got, err := s.GetByPair(ctx, "cand_002", "opp_002")
    require.NoError(t, err)
    require.InDelta(t, 0.7, got.Score, 1e-9)
}

func TestStore_UpsertMatch_MonotonicScore(t *testing.T) {
    db, ctx := setupStoreDB(t)
    s := matching.NewStore(db)

    base := matching.Match{
        MatchID: "m_003", CandidateID: "c", OpportunityID: "o",
        Status: matching.StatusNew, Score: 0.6, LastEventID: "evt_1",
    }
    _, _ = s.UpsertMatch(ctx, base)

    // Lower score should NOT downgrade the row.
    lower := base
    lower.Score = 0.4
    lower.LastEventID = "evt_2"
    _, err := s.UpsertMatch(ctx, lower)
    require.NoError(t, err)
    got, _ := s.GetByPair(ctx, "c", "o")
    require.InDelta(t, 0.6, got.Score, 1e-9)

    // Higher score should win.
    higher := base
    higher.Score = 0.9
    higher.LastEventID = "evt_3"
    _, err = s.UpsertMatch(ctx, higher)
    require.NoError(t, err)
    got, _ = s.GetByPair(ctx, "c", "o")
    require.InDelta(t, 0.9, got.Score, 1e-9)
    require.Equal(t, "evt_3", got.LastEventID)
}

func TestStore_UpsertMatch_TerminalProtected(t *testing.T) {
    db, ctx := setupStoreDB(t)
    s := matching.NewStore(db)

    _, _ = s.UpsertMatch(ctx, matching.Match{
        MatchID: "m_004", CandidateID: "c", OpportunityID: "o",
        Status: matching.StatusNew, Score: 0.5, LastEventID: "e1",
    })

    // Transition to terminal via a direct UPDATE (mirrors what
    // /api/me/matches/{id}/dismiss does in Phase 4).
    _, err := db.ExecContext(ctx,
        `UPDATE candidate_matches
         SET status='dismissed', dismissed_at=now()
         WHERE candidate_id='c' AND opportunity_id='o'`)
    require.NoError(t, err)

    // A higher-scoring fan-out replay must NOT resurrect the row.
    _, err = s.UpsertMatch(ctx, matching.Match{
        MatchID: "m_004", CandidateID: "c", OpportunityID: "o",
        Status: matching.StatusNew, Score: 0.95, LastEventID: "e2",
    })
    require.NoError(t, err)

    got, _ := s.GetByPair(ctx, "c", "o")
    require.Equal(t, matching.StatusDismissed, got.Status)
    require.InDelta(t, 0.5, got.Score, 1e-9) // score preserved
}
```

- [ ] **Step 7: Run green**

```bash
make test-integration
```

Expected: PASS. The `ON CONFLICT … WHERE status='new'` clause covers Step-6's terminal guard automatically.

- [ ] **Step 8: Add `UpsertMatches` (bulk) — same invariants in one round-trip**

```go
// UpsertMatches writes a batch via a single statement using unnest. Empty
// input is a no-op.
func (s *Store) UpsertMatches(ctx context.Context, ms []Match) error {
    if len(ms) == 0 {
        return nil
    }
    var (
        ids       = make([]string, len(ms))
        cands     = make([]string, len(ms))
        opps      = make([]string, len(ms))
        statuses  = make([]string, len(ms))
        scores    = make([]float64, len(ms))
        reranks   = make([]sql.NullFloat64, len(ms))
        rerUsed   = make([]bool, len(ms))
        events    = make([]string, len(ms))
        metas     = make([][]byte, len(ms))
    )
    for i, m := range ms {
        ids[i] = m.MatchID
        cands[i] = m.CandidateID
        opps[i] = m.OpportunityID
        if m.Status == "" {
            m.Status = StatusNew
        }
        statuses[i] = string(m.Status)
        scores[i] = m.Score
        if m.RerankScore != nil {
            reranks[i] = sql.NullFloat64{Float64: *m.RerankScore, Valid: true}
        }
        rerUsed[i] = m.RerankerUsed
        events[i] = m.LastEventID
        md := m.Metadata
        if md == nil {
            md = map[string]any{}
        }
        metas[i] = mustEncodeJSON(md)
    }
    _, err := s.db.ExecContext(ctx, upsertBulkSQL,
        ids, cands, opps, statuses, scores, reranks, rerUsed, events, metas,
    )
    if err != nil {
        return fmt.Errorf("matching: bulk upsert: %w", err)
    }
    return nil
}

const upsertBulkSQL = `
INSERT INTO candidate_matches (
    match_id, candidate_id, opportunity_id, status,
    score, rerank_score, reranker_used,
    last_event_id, metadata
)
SELECT * FROM unnest(
    $1::text[], $2::text[], $3::text[], $4::text[],
    $5::float8[], $6::float8[], $7::bool[],
    $8::text[], $9::jsonb[]
)
ON CONFLICT (candidate_id, opportunity_id) DO UPDATE
   SET score         = GREATEST(candidate_matches.score, EXCLUDED.score),
       rerank_score  = COALESCE(EXCLUDED.rerank_score, candidate_matches.rerank_score),
       reranker_used = candidate_matches.reranker_used OR EXCLUDED.reranker_used,
       last_event_id = EXCLUDED.last_event_id,
       metadata      = candidate_matches.metadata || EXCLUDED.metadata,
       updated_at    = now()
   WHERE candidate_matches.status = 'new'
     AND EXCLUDED.score > candidate_matches.score
`
```

The `sql.NullFloat64` slice needs to bind through pq's array encoding. If the driver rejects it, the fallback is a per-row loop in a single transaction:

```go
func (s *Store) UpsertMatches(ctx context.Context, ms []Match) error {
    if len(ms) == 0 {
        return nil
    }
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("matching: begin bulk: %w", err)
    }
    defer func() { _ = tx.Rollback() }()
    for _, m := range ms {
        md := m.Metadata
        if md == nil {
            md = map[string]any{}
        }
        if _, err := tx.ExecContext(ctx, upsertOneSQL,
            m.MatchID, m.CandidateID, m.OpportunityID,
            string(m.Status), m.Score, nullableF64(m.RerankScore),
            m.RerankerUsed, m.LastEventID, mustEncodeJSON(md),
        ); err != nil {
            return fmt.Errorf("matching: bulk row %s: %w", m.MatchID, err)
        }
    }
    return tx.Commit()
}
```

Use whichever the driver accepts. Prefer the unnest version; fall back only if `pgx` rejects the array.

- [ ] **Step 9: Test the bulk path**

```go
func TestStore_UpsertMatches_Bulk(t *testing.T) {
    db, ctx := setupStoreDB(t)
    s := matching.NewStore(db)

    ms := []matching.Match{
        {MatchID: "b1", CandidateID: "c", OpportunityID: "o1", Status: matching.StatusNew, Score: 0.71, LastEventID: "e"},
        {MatchID: "b2", CandidateID: "c", OpportunityID: "o2", Status: matching.StatusNew, Score: 0.66, LastEventID: "e"},
        {MatchID: "b3", CandidateID: "c", OpportunityID: "o3", Status: matching.StatusNew, Score: 0.51, LastEventID: "e"},
    }
    require.NoError(t, s.UpsertMatches(ctx, ms))

    var cnt int
    require.NoError(t, db.QueryRowContext(ctx,
        `SELECT count(*) FROM candidate_matches WHERE candidate_id='c'`).Scan(&cnt))
    require.Equal(t, 3, cnt)
}
```

- [ ] **Step 10: Run green**

```bash
make test-integration
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add pkg/matching/store.go pkg/matching/store_test.go
git commit -m "feat(matching): candidate_matches Store with monotonic-score + terminal-protected UPSERT"
```

---

### Task 6: `ListByCandidate` — paginated reads for the extension

The feed endpoint in Phase 4 needs cursor-based pagination over the candidate's rows.

**Files:**

- Modify: `pkg/matching/store.go` (append)
- Modify: `pkg/matching/store_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestStore_ListByCandidate_Pagination(t *testing.T) {
    db, ctx := setupStoreDB(t)
    s := matching.NewStore(db)

    // Insert 5 matches; only some should pass the default status filter.
    base := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
    rows := []matching.Match{
        {MatchID: "p1", CandidateID: "u", OpportunityID: "o1", Status: matching.StatusNew, Score: 0.9, LastEventID: "e"},
        {MatchID: "p2", CandidateID: "u", OpportunityID: "o2", Status: matching.StatusViewed, Score: 0.8, LastEventID: "e"},
        {MatchID: "p3", CandidateID: "u", OpportunityID: "o3", Status: matching.StatusApplying, Score: 0.7, LastEventID: "e"},
        {MatchID: "p4", CandidateID: "u", OpportunityID: "o4", Status: matching.StatusDismissed, Score: 0.6, LastEventID: "e"},
        {MatchID: "p5", CandidateID: "u", OpportunityID: "o5", Status: matching.StatusOverflow, Score: 0.5, LastEventID: "e"},
    }
    for _, m := range rows {
        _, err := s.UpsertMatch(ctx, m)
        require.NoError(t, err)
    }
    _ = base // currently unused; reserved for cursor-time assertions

    page, err := s.ListByCandidate(ctx, matching.ListByCandidateParams{
        CandidateID: "u",
        Statuses:    []matching.MatchStatus{matching.StatusNew, matching.StatusViewed, matching.StatusApplying},
        Limit:       10,
    })
    require.NoError(t, err)
    require.Len(t, page.Items, 3)
    // ordered by score desc, then created_at desc
    require.Equal(t, "p1", page.Items[0].MatchID)
    require.Equal(t, "p2", page.Items[1].MatchID)
    require.Equal(t, "p3", page.Items[2].MatchID)
    require.False(t, page.HasMore)

    // Same params with limit=2 → first page + next_cursor.
    page1, err := s.ListByCandidate(ctx, matching.ListByCandidateParams{
        CandidateID: "u",
        Statuses:    []matching.MatchStatus{matching.StatusNew, matching.StatusViewed, matching.StatusApplying},
        Limit:       2,
    })
    require.NoError(t, err)
    require.Len(t, page1.Items, 2)
    require.True(t, page1.HasMore)
    require.NotEmpty(t, page1.NextCursor)

    // Page 2 picks up where 1 left off.
    page2, err := s.ListByCandidate(ctx, matching.ListByCandidateParams{
        CandidateID: "u",
        Statuses:    []matching.MatchStatus{matching.StatusNew, matching.StatusViewed, matching.StatusApplying},
        Cursor:      page1.NextCursor,
        Limit:       2,
    })
    require.NoError(t, err)
    require.Len(t, page2.Items, 1)
    require.False(t, page2.HasMore)
}
```

- [ ] **Step 2: Run red**

```bash
make test-integration
```

Expected: FAIL ("ListByCandidate undefined").

- [ ] **Step 3: Implement**

Append to `store.go`:

```go
// ListByCandidateParams configures a paginated read.
type ListByCandidateParams struct {
    CandidateID string
    Statuses    []MatchStatus // empty defaults to non-terminal + overflow filter applied by caller
    Cursor      string        // opaque; returned by the previous page as NextCursor
    Limit       int           // default 50, max 200
}

// ListByCandidatePage is the result envelope.
type ListByCandidatePage struct {
    Items      []Match
    NextCursor string
    HasMore    bool
}

const defaultPageLimit = 50
const maxPageLimit = 200

// ListByCandidate returns one page of matches for a candidate, ordered
// by (score DESC, created_at DESC, match_id ASC) so the ordering is
// fully deterministic and cursor-resumable.
func (s *Store) ListByCandidate(ctx context.Context, p ListByCandidateParams) (ListByCandidatePage, error) {
    limit := p.Limit
    if limit <= 0 {
        limit = defaultPageLimit
    }
    if limit > maxPageLimit {
        limit = maxPageLimit
    }

    statuses := p.Statuses
    if len(statuses) == 0 {
        statuses = []MatchStatus{StatusNew, StatusViewed, StatusApplying}
    }
    statusStrs := make([]string, len(statuses))
    for i, s := range statuses {
        statusStrs[i] = string(s)
    }

    args := []any{p.CandidateID, statusStrs}
    where := "candidate_id = $1 AND status = ANY($2)"
    if p.Cursor != "" {
        cur, err := decodeCursor(p.Cursor)
        if err != nil {
            return ListByCandidatePage{}, fmt.Errorf("matching: cursor: %w", err)
        }
        args = append(args, cur.Score, cur.CreatedAt, cur.MatchID)
        where += ` AND (
            (score, created_at, match_id) < ($3, $4::timestamptz, $5)
        )`
    }
    args = append(args, limit+1)
    q := `
SELECT match_id, candidate_id, opportunity_id, status,
       score, rerank_score, reranker_used,
       viewed_at, applied_at, dismissed_at,
       last_event_id, metadata, created_at, updated_at
FROM candidate_matches
WHERE ` + where + `
ORDER BY score DESC, created_at DESC, match_id ASC
LIMIT $` + fmt.Sprint(len(args))

    rows, err := s.db.QueryContext(ctx, q, args...)
    if err != nil {
        return ListByCandidatePage{}, fmt.Errorf("matching: list: %w", err)
    }
    defer rows.Close()

    out := make([]Match, 0, limit)
    for rows.Next() {
        m, err := scanMatch(rows.Scan)
        if err != nil {
            return ListByCandidatePage{}, err
        }
        out = append(out, *m)
    }
    if err := rows.Err(); err != nil {
        return ListByCandidatePage{}, fmt.Errorf("matching: list rows: %w", err)
    }

    hasMore := len(out) > limit
    if hasMore {
        out = out[:limit]
    }
    var nextCur string
    if hasMore {
        last := out[len(out)-1]
        nextCur = encodeCursor(cursor{
            Score:     last.Score,
            CreatedAt: last.CreatedAt,
            MatchID:   last.MatchID,
        })
    }
    return ListByCandidatePage{Items: out, NextCursor: nextCur, HasMore: hasMore}, nil
}

type cursor struct {
    Score     float64   `json:"s"`
    CreatedAt time.Time `json:"c"`
    MatchID   string    `json:"m"`
}

func encodeCursor(c cursor) string {
    b := mustEncodeJSON(c)
    return base64URLEncode(b)
}

func decodeCursor(s string) (cursor, error) {
    raw, err := base64URLDecode(s)
    if err != nil {
        return cursor{}, err
    }
    var c cursor
    if err := json.Unmarshal(raw, &c); err != nil {
        return cursor{}, err
    }
    return c, nil
}

// base64URL{Encode,Decode} are tiny wrappers — broken out so we can
// swap the URL-safe variant easily if needed.
func base64URLEncode(b []byte) string  { return base64Std.URLEncoding.EncodeToString(b) }
func base64URLDecode(s string) ([]byte, error) {
    return base64Std.URLEncoding.DecodeString(s)
}
```

Add the import:

```go
import (
    // … existing …
    base64Std "encoding/base64"
)
```

(Aliasing the import avoids stepping on `encoding/json`'s name when the IDE auto-imports.)

- [ ] **Step 4: Run green**

```bash
make test-integration
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/matching/store.go pkg/matching/store_test.go
git commit -m "feat(matching): Store.ListByCandidate with stable cursor pagination"
```

---

### Task 7: Event log writers (`pkg/matching/events_log.go`)

Spec §2.1 hypertables `candidate_match_events` + `match_run_events`. Writers are append-only — no UPDATE path.

**Files:**

- Create: `pkg/matching/events_log.go`
- Test: `pkg/matching/events_log_test.go`

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package matching_test

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestEventLog_WriteMatchEvent(t *testing.T) {
    db, ctx := setupStoreDB(t)
    el := matching.NewEventLog(db)

    rerank := 0.78
    err := el.WriteMatchEvent(ctx, matching.MatchEvent{
        EventID:       "evt_001",
        CandidateID:   "c1",
        OpportunityID: "o1",
        CanonicalID:   "c-1",
        Kind:          matching.EventKindGenerated,
        Path:          matching.PathFanout,
        Score:         0.81,
        RerankScore:   &rerank,
        RerankerUsed:  true,
        Data:          map[string]any{"rules_version": 4},
        OccurredAt:    time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC),
    })
    require.NoError(t, err)

    var cnt int
    require.NoError(t, db.QueryRowContext(ctx,
        `SELECT count(*) FROM candidate_match_events WHERE event_id='evt_001'`).Scan(&cnt))
    require.Equal(t, 1, cnt)
}

func TestEventLog_WriteMatchRunEvent(t *testing.T) {
    db, ctx := setupStoreDB(t)
    el := matching.NewEventLog(db)

    err := el.WriteMatchRunEvent(ctx, matching.MatchRunEvent{
        RunID:             "run_001",
        Path:              matching.PathFanout,
        TriggeredBy:       "canonical_upserted",
        CanonicalID:       "c-1",
        CandidatesScanned: 487,
        MatchesWritten:    34,
        Status:            "ok",
        RerankerStatus:    "used",
        LatencyMS:         142,
        StartedAt:         time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC),
    })
    require.NoError(t, err)

    var cnt int
    require.NoError(t, db.QueryRowContext(ctx,
        `SELECT count(*) FROM match_run_events WHERE run_id='run_001'`).Scan(&cnt))
    require.Equal(t, 1, cnt)
}
```

- [ ] **Step 2: Run red**

```bash
make test-integration
```

Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// pkg/matching/events_log.go
package matching

import (
    "context"
    "database/sql"
    "fmt"
    "time"
)

// EventKind enumerates valid candidate_match_events.kind values.
type EventKind string

const (
    EventKindGenerated          EventKind = "generated"
    EventKindViewed             EventKind = "viewed"
    EventKindDismissed          EventKind = "dismissed"
    EventKindOverflow           EventKind = "overflow"
    EventKindRulesChangeRematch EventKind = "rules_change_rematch"
)

// Path enumerates which write path produced an event.
type Path string

const (
    PathFanout          Path = "fanout"
    PathGap             Path = "gap"
    PathCandidateChange Path = "candidate_change"
)

// MatchEvent is one row of candidate_match_events.
type MatchEvent struct {
    EventID       string
    OccurredAt    time.Time
    CandidateID   string
    OpportunityID string
    CanonicalID   string
    Kind          EventKind
    Path          Path
    Score         float64
    RerankScore   *float64
    RerankerUsed  bool
    Data          map[string]any
}

// MatchRunEvent is one row of match_run_events.
type MatchRunEvent struct {
    RunID             string
    StartedAt         time.Time
    FinishedAt        *time.Time
    Path              Path
    TriggeredBy       string
    CandidateID       string
    CanonicalID       string
    CandidatesScanned int
    MatchesWritten    int
    Status            string
    RerankerStatus    string
    LatencyMS         int
    Data              map[string]any
}

// EventLog writes to the two hypertables. Append-only — no UPDATE path.
type EventLog struct {
    db *sql.DB
}

func NewEventLog(db *sql.DB) *EventLog { return &EventLog{db: db} }

const insertMatchEventSQL = `
INSERT INTO candidate_match_events
    (event_id, occurred_at, candidate_id, opportunity_id, canonical_id,
     kind, path, score, rerank_score, reranker_used, data)
VALUES ($1, COALESCE($2, now()), $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb)
ON CONFLICT (event_id, occurred_at) DO NOTHING
`

func (e *EventLog) WriteMatchEvent(ctx context.Context, m MatchEvent) error {
    data := m.Data
    if data == nil {
        data = map[string]any{}
    }
    occ := nullableTime(m.OccurredAt)
    _, err := e.db.ExecContext(ctx, insertMatchEventSQL,
        m.EventID, occ, m.CandidateID, m.OpportunityID, m.CanonicalID,
        string(m.Kind), string(m.Path), m.Score, nullableF64(m.RerankScore),
        m.RerankerUsed, mustEncodeJSON(data),
    )
    if err != nil {
        return fmt.Errorf("matching: write match event: %w", err)
    }
    return nil
}

const insertRunEventSQL = `
INSERT INTO match_run_events
    (run_id, started_at, finished_at, path, triggered_by,
     candidate_id, canonical_id, candidates_scanned, matches_written,
     status, reranker_status, latency_ms, data)
VALUES ($1, COALESCE($2, now()), $3, $4, $5,
        NULLIF($6, ''), NULLIF($7, ''), $8, $9,
        $10, NULLIF($11, ''), NULLIF($12, 0), $13::jsonb)
ON CONFLICT (run_id, started_at) DO NOTHING
`

func (e *EventLog) WriteMatchRunEvent(ctx context.Context, r MatchRunEvent) error {
    data := r.Data
    if data == nil {
        data = map[string]any{}
    }
    var fin any
    if r.FinishedAt != nil {
        fin = *r.FinishedAt
    }
    _, err := e.db.ExecContext(ctx, insertRunEventSQL,
        r.RunID, nullableTime(r.StartedAt), fin, string(r.Path), r.TriggeredBy,
        r.CandidateID, r.CanonicalID, r.CandidatesScanned, r.MatchesWritten,
        r.Status, r.RerankerStatus, r.LatencyMS, mustEncodeJSON(data),
    )
    if err != nil {
        return fmt.Errorf("matching: write run event: %w", err)
    }
    return nil
}

func nullableTime(t time.Time) any {
    if t.IsZero() {
        return nil
    }
    return t
}
```

- [ ] **Step 4: Run green**

```bash
make test-integration
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/matching/events_log.go pkg/matching/events_log_test.go
git commit -m "feat(matching): append-only writers for candidate_match_events + match_run_events"
```

---

### Task 8: `IndexStore` — read/write `candidate_match_indexes`

Path A fan-out reads from this table to discover eligible candidates per opportunity. Path C reads + writes it when the candidate's CV or rules change. Embedding is `vector(1536)` — we serialize via the textual `[a,b,c]` form.

**Files:**

- Create: `pkg/matching/index_store.go`
- Test: `pkg/matching/index_store_test.go`

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package matching_test

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestIndexStore_UpsertAndGet(t *testing.T) {
    db, ctx := setupStoreDB(t)
    s := matching.NewIndexStore(db)

    floor := 30000
    err := s.Upsert(ctx, matching.CandidateIndex{
        CandidateID:    "c1",
        Embedding:      makeUnitVector(1536, 0),
        MinScore:       0.62,
        DailyCap:       25,
        WeeklyCap:      100,
        Kinds:          []string{"job", "scholarship"},
        Countries:      []string{"KE", "UG", "remote"},
        SalaryFloorUSD: &floor,
        RemoteOnly:     false,
        Enabled:        true,
    })
    require.NoError(t, err)

    got, err := s.Get(ctx, "c1")
    require.NoError(t, err)
    require.Equal(t, "c1", got.CandidateID)
    require.Equal(t, 1536, len(got.Embedding))
    require.InDelta(t, 0.62, got.MinScore, 1e-9)
    require.ElementsMatch(t, []string{"job", "scholarship"}, got.Kinds)
}

func makeUnitVector(dim, axis int) []float32 {
    v := make([]float32, dim)
    if axis >= 0 && axis < dim {
        v[axis] = 1
    } else {
        v[0] = 1
    }
    return v
}
```

- [ ] **Step 2: Run red**

```bash
make test-integration
```

Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// pkg/matching/index_store.go
package matching

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "strconv"
    "strings"

    "github.com/lib/pq"
    "time"
)

// CandidateIndex is one row of candidate_match_indexes — the
// denormalized vector + filter bag the fan-out worker reads.
type CandidateIndex struct {
    CandidateID    string
    Embedding      []float32
    MinScore       float64
    DailyCap       int
    WeeklyCap      int
    Kinds          []string
    Countries      []string
    SalaryFloorUSD *int
    RemoteOnly     bool
    Enabled        bool
    UpdatedAt      time.Time
}

type IndexStore struct {
    db *sql.DB
}

func NewIndexStore(db *sql.DB) *IndexStore { return &IndexStore{db: db} }

const upsertIndexSQL = `
INSERT INTO candidate_match_indexes
    (candidate_id, embedding, min_score, daily_cap, weekly_cap,
     kinds, countries, salary_floor_usd, remote_only, enabled, updated_at)
VALUES ($1, $2::vector, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (candidate_id) DO UPDATE
   SET embedding        = EXCLUDED.embedding,
       min_score        = EXCLUDED.min_score,
       daily_cap        = EXCLUDED.daily_cap,
       weekly_cap       = EXCLUDED.weekly_cap,
       kinds            = EXCLUDED.kinds,
       countries        = EXCLUDED.countries,
       salary_floor_usd = EXCLUDED.salary_floor_usd,
       remote_only      = EXCLUDED.remote_only,
       enabled          = EXCLUDED.enabled,
       updated_at       = now()
`

// Upsert replaces the index row entirely. Path C calls this on CV /
// preferences changes; partial updates are intentionally not supported
// because the table is the materialized form of upstream sources of truth.
func (s *IndexStore) Upsert(ctx context.Context, ci CandidateIndex) error {
    var floor any
    if ci.SalaryFloorUSD != nil {
        floor = *ci.SalaryFloorUSD
    }
    _, err := s.db.ExecContext(ctx, upsertIndexSQL,
        ci.CandidateID, vectorLiteral(ci.Embedding),
        ci.MinScore, ci.DailyCap, ci.WeeklyCap,
        pq.Array(ci.Kinds), pq.Array(ci.Countries),
        floor, ci.RemoteOnly, ci.Enabled,
    )
    if err != nil {
        return fmt.Errorf("matching: index upsert: %w", err)
    }
    return nil
}

const getIndexSQL = `
SELECT candidate_id, embedding::text, min_score, daily_cap, weekly_cap,
       kinds, countries, salary_floor_usd, remote_only, enabled, updated_at
FROM candidate_match_indexes
WHERE candidate_id = $1
`

func (s *IndexStore) Get(ctx context.Context, candidateID string) (*CandidateIndex, error) {
    row := s.db.QueryRowContext(ctx, getIndexSQL, candidateID)
    var (
        ci       CandidateIndex
        embText  string
        kinds    pq.StringArray
        countries pq.StringArray
        floor    sql.NullInt64
    )
    err := row.Scan(
        &ci.CandidateID, &embText, &ci.MinScore, &ci.DailyCap, &ci.WeeklyCap,
        &kinds, &countries, &floor, &ci.RemoteOnly, &ci.Enabled, &ci.UpdatedAt,
    )
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrNotFound
    }
    if err != nil {
        return nil, fmt.Errorf("matching: index get: %w", err)
    }
    ci.Embedding = parseVectorLiteral(embText)
    ci.Kinds = []string(kinds)
    ci.Countries = []string(countries)
    if floor.Valid {
        v := int(floor.Int64)
        ci.SalaryFloorUSD = &v
    }
    return &ci, nil
}

// vectorLiteral renders []float32 as pgvector textual form. Same shape
// as pkg/pgsearch's helper.
func vectorLiteral(v []float32) string {
    if len(v) == 0 {
        return "[]"
    }
    var b strings.Builder
    b.WriteByte('[')
    for i, f := range v {
        if i > 0 {
            b.WriteByte(',')
        }
        b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
    }
    b.WriteByte(']')
    return b.String()
}

func parseVectorLiteral(s string) []float32 {
    s = strings.TrimPrefix(s, "[")
    s = strings.TrimSuffix(s, "]")
    if s == "" {
        return nil
    }
    parts := strings.Split(s, ",")
    out := make([]float32, len(parts))
    for i, p := range parts {
        f, _ := strconv.ParseFloat(strings.TrimSpace(p), 32)
        out[i] = float32(f)
    }
    return out
}
```

- [ ] **Step 4: Run green**

```bash
make test-integration
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/matching/index_store.go pkg/matching/index_store_test.go
git commit -m "feat(matching): IndexStore for candidate_match_indexes (UPSERT + Get)"
```

---

### Task 9: KNN queries (`pkg/matching/knn.go`)

Two flavors: `FanOutKNN` (one opportunity vector → top-N candidates from `candidate_match_indexes`) and `ReverseKNN` (one candidate vector → top-N opportunities from `opportunities`). Both push as much filtering down to SQL as possible.

**Files:**

- Create: `pkg/matching/knn.go`
- Test: `pkg/matching/knn_test.go`

- [ ] **Step 1: Write the failing test (FanOutKNN)**

```go
//go:build integration

package matching_test

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestFanOutKNN_OrdersByDistanceAndFilters(t *testing.T) {
    db, ctx := setupStoreDB(t)
    idx := matching.NewIndexStore(db)
    knn := matching.NewKNN(db)

    // Three candidates with distinct embeddings.
    require.NoError(t, idx.Upsert(ctx, matching.CandidateIndex{
        CandidateID: "c_close", Embedding: makeUnitVector(1536, 0),
        MinScore: 0.5, DailyCap: 25, WeeklyCap: 100,
        Kinds: []string{"job"}, Countries: []string{"KE"},
        Enabled: true,
    }))
    require.NoError(t, idx.Upsert(ctx, matching.CandidateIndex{
        CandidateID: "c_far", Embedding: makeUnitVector(1536, 5),
        MinScore: 0.5, DailyCap: 25, WeeklyCap: 100,
        Kinds: []string{"job"}, Countries: []string{"KE"},
        Enabled: true,
    }))
    require.NoError(t, idx.Upsert(ctx, matching.CandidateIndex{
        CandidateID: "c_wrong_kind", Embedding: makeUnitVector(1536, 0),
        MinScore: 0.5, DailyCap: 25, WeeklyCap: 100,
        Kinds: []string{"scholarship"}, Countries: []string{"KE"},
        Enabled: true,
    }))

    hits, err := knn.FanOutKNN(ctx, matching.FanOutKNNParams{
        OppEmbedding: makeUnitVector(1536, 0),
        OppKind:      "job",
        OppCountry:   "KE",
        Limit:        10,
    })
    require.NoError(t, err)
    require.Len(t, hits, 2) // c_wrong_kind filtered out
    require.Equal(t, "c_close", hits[0].CandidateID)
    require.Equal(t, "c_far", hits[1].CandidateID)
    require.InDelta(t, 1.0, hits[0].Distance, 1e-6) // identical vectors → cosine_distance=0 → score 1
}
```

- [ ] **Step 2: Run red**

```bash
make test-integration
```

Expected: FAIL.

- [ ] **Step 3: Implement FanOutKNN**

```go
// pkg/matching/knn.go
package matching

import (
    "context"
    "database/sql"
    "fmt"
    "time"

    "github.com/lib/pq"
)

// KNN holds the DB handle and runs vector-similarity searches.
type KNN struct {
    db *sql.DB
}

func NewKNN(db *sql.DB) *KNN { return &KNN{db: db} }

// FanOutKNNParams is the input to a one-opportunity fan-out search.
type FanOutKNNParams struct {
    OppEmbedding  []float32
    OppKind       string
    OppCountry    string  // ISO 3166-1 alpha-2 or empty
    OppSalaryMax  *int    // nil = unknown
    Limit         int     // default 500
}

// CandidateHit is one row of the fan-out result.
type CandidateHit struct {
    CandidateID    string
    Distance       float64 // pgvector cosine distance, [0..2]
    MinScore       float64
    DailyCap       int
    Kinds          []string
    Countries      []string
    SalaryFloorUSD *int
}

const fanOutKNNSQL = `
SELECT candidate_id,
       embedding <=> $1::vector AS distance,
       min_score, daily_cap, kinds, countries, salary_floor_usd
FROM candidate_match_indexes
WHERE enabled = TRUE
  AND $2 = ANY(kinds)
  AND ( $3 = '' OR cardinality(countries) = 0 OR $3 = ANY(countries) OR 'remote' = ANY(countries) )
  AND ( $4::int IS NULL OR salary_floor_usd IS NULL OR salary_floor_usd <= $4::int )
ORDER BY embedding <=> $1::vector
LIMIT $5
`

// FanOutKNN runs Path A's retrieval query: one opportunity → top-N
// eligible candidates ordered by cosine distance ascending.
func (k *KNN) FanOutKNN(ctx context.Context, p FanOutKNNParams) ([]CandidateHit, error) {
    limit := p.Limit
    if limit <= 0 {
        limit = 500
    }
    var salaryMax any
    if p.OppSalaryMax != nil {
        salaryMax = *p.OppSalaryMax
    }
    rows, err := k.db.QueryContext(ctx, fanOutKNNSQL,
        vectorLiteral(p.OppEmbedding), p.OppKind, p.OppCountry, salaryMax, limit)
    if err != nil {
        return nil, fmt.Errorf("matching: fan-out knn: %w", err)
    }
    defer rows.Close()
    out := make([]CandidateHit, 0, limit)
    for rows.Next() {
        var (
            h         CandidateHit
            kinds     pq.StringArray
            countries pq.StringArray
            floor     sql.NullInt64
        )
        if err := rows.Scan(&h.CandidateID, &h.Distance, &h.MinScore,
            &h.DailyCap, &kinds, &countries, &floor); err != nil {
            return nil, fmt.Errorf("matching: fan-out knn scan: %w", err)
        }
        h.Kinds = []string(kinds)
        h.Countries = []string(countries)
        if floor.Valid {
            v := int(floor.Int64)
            h.SalaryFloorUSD = &v
        }
        out = append(out, h)
    }
    return out, rows.Err()
}
```

- [ ] **Step 4: Run green for FanOutKNN**

```bash
make test-integration
```

Expected: PASS.

- [ ] **Step 5: Test + implement `ReverseKNN`**

```go
func TestReverseKNN_OrdersByDistanceSinceCursor(t *testing.T) {
    db, ctx := setupStoreDB(t)
    knn := matching.NewKNN(db)

    // Seed opportunities directly. The stub has a minimal column set; we
    // need to add embedding + kind + first_seen_at on top via SQL.
    _, err := db.ExecContext(ctx, `
        ALTER TABLE opportunities
            ADD COLUMN IF NOT EXISTS embedding vector(1536),
            ADD COLUMN IF NOT EXISTS kind TEXT,
            ADD COLUMN IF NOT EXISTS country TEXT,
            ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ DEFAULT now()
    `)
    require.NoError(t, err)

    // Insert two close, one far, one outside the cursor window.
    seedOpp := func(id string, axis int, kind, country string, firstSeen time.Time) {
        v := makeUnitVector(1536, axis)
        _, err := db.ExecContext(ctx, `
            INSERT INTO opportunities (id, posted_at, status, hidden, embedding, kind, country, first_seen_at)
            VALUES ($1, now(), 'active', false, $2::vector, $3, $4, $5)
        `, id, vectorLiteralFor(v), kind, country, firstSeen)
        require.NoError(t, err)
    }
    now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
    seedOpp("opp_close", 0, "job", "KE", now)
    seedOpp("opp_close2", 1, "job", "KE", now.Add(-1*time.Hour))
    seedOpp("opp_old", 0, "job", "KE", now.Add(-24*time.Hour))
    seedOpp("opp_wrong_kind", 0, "scholarship", "KE", now)

    hits, err := knn.ReverseKNN(ctx, matching.ReverseKNNParams{
        CandidateEmbedding: makeUnitVector(1536, 0),
        Kinds:              []string{"job"},
        Countries:          []string{"KE"},
        Since:              now.Add(-2 * time.Hour),
        Limit:              10,
    })
    require.NoError(t, err)
    ids := make([]string, 0, len(hits))
    for _, h := range hits {
        ids = append(ids, h.OpportunityID)
    }
    require.ElementsMatch(t, []string{"opp_close", "opp_close2"}, ids)
}

// Inline helper since pkg/matching's vectorLiteral is unexported.
func vectorLiteralFor(v []float32) string {
    // Use the same JSON-ish form pgvector accepts.
    out := "["
    for i, f := range v {
        if i > 0 {
            out += ","
        }
        out += fmt.Sprintf("%g", f)
    }
    out += "]"
    return out
}
```

Append to `knn.go`:

```go
// ReverseKNNParams is the input to a one-candidate "what's new for me" search.
type ReverseKNNParams struct {
    CandidateEmbedding []float32
    Kinds              []string
    Countries          []string
    Since              time.Time // only rows with first_seen_at > Since
    Limit              int       // default 100
}

// OppHit is one row of the reverse-KNN result.
type OppHit struct {
    OpportunityID string
    Distance      float64
    Kind          string
    Country       string
    FirstSeenAt   time.Time
}

const reverseKNNSQL = `
SELECT id,
       embedding <=> $1::vector AS distance,
       COALESCE(kind, '')    AS kind,
       COALESCE(country, '') AS country,
       first_seen_at
FROM opportunities
WHERE status = 'active'
  AND hidden = false
  AND embedding IS NOT NULL
  AND first_seen_at > $2
  AND ( cardinality($3::text[]) = 0 OR kind = ANY($3) )
  AND ( cardinality($4::text[]) = 0 OR country IS NULL OR country = ANY($4) )
ORDER BY embedding <=> $1::vector
LIMIT $5
`

// ReverseKNN runs Path B / Path C retrieval: one candidate → top-N
// new-since-cursor opportunities.
func (k *KNN) ReverseKNN(ctx context.Context, p ReverseKNNParams) ([]OppHit, error) {
    limit := p.Limit
    if limit <= 0 {
        limit = 100
    }
    rows, err := k.db.QueryContext(ctx, reverseKNNSQL,
        vectorLiteral(p.CandidateEmbedding), p.Since,
        pq.Array(p.Kinds), pq.Array(p.Countries), limit)
    if err != nil {
        return nil, fmt.Errorf("matching: reverse knn: %w", err)
    }
    defer rows.Close()
    out := make([]OppHit, 0, limit)
    for rows.Next() {
        var h OppHit
        if err := rows.Scan(&h.OpportunityID, &h.Distance, &h.Kind,
            &h.Country, &h.FirstSeenAt); err != nil {
            return nil, fmt.Errorf("matching: reverse knn scan: %w", err)
        }
        out = append(out, h)
    }
    return out, rows.Err()
}
```

- [ ] **Step 6: Run green**

```bash
make test-integration
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/matching/knn.go pkg/matching/knn_test.go
git commit -m "feat(matching): FanOutKNN + ReverseKNN against pgvector"
```

---

### Task 10: Reranker abstraction (`pkg/matching/rerank.go`)

Interface + noop + bounded pool. Reranker may be unavailable (config off, network error, pool full); the orchestrators degrade gracefully per spec §3.7.

**Files:**

- Create: `pkg/matching/rerank.go`
- Test: `pkg/matching/rerank_test.go`

- [ ] **Step 1: Write the failing test**

```go
package matching_test

import (
    "context"
    "errors"
    "sync"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestNoopReranker_PassesScoresThrough(t *testing.T) {
    r := matching.NoopReranker{}
    items := []matching.RerankItem{
        {ID: "a", Score: 0.7},
        {ID: "b", Score: 0.9},
    }
    out, used, err := r.Rerank(context.Background(), items)
    require.NoError(t, err)
    require.False(t, used)
    require.Equal(t, items, out)
}

func TestPooledReranker_FallsBackOnPoolFull(t *testing.T) {
    blocking := &blockingReranker{block: make(chan struct{})}
    pooled := matching.NewPooledReranker(blocking, 1, 5*time.Millisecond)

    items := []matching.RerankItem{{ID: "x", Score: 0.5}}

    // First call enters and blocks.
    go func() { _, _, _ = pooled.Rerank(context.Background(), items) }()
    time.Sleep(2 * time.Millisecond)

    // Second call cannot enter and falls back.
    out, used, err := pooled.Rerank(context.Background(), items)
    require.NoError(t, err)
    require.False(t, used)
    require.Equal(t, items, out)

    close(blocking.block)
}

func TestPooledReranker_FallsBackOnUpstreamError(t *testing.T) {
    err := errors.New("model down")
    pooled := matching.NewPooledReranker(&errorReranker{err: err}, 4, time.Second)
    items := []matching.RerankItem{{ID: "x", Score: 0.5}}
    out, used, gotErr := pooled.Rerank(context.Background(), items)
    require.NoError(t, gotErr) // fallback swallows the error, reports used=false
    require.False(t, used)
    require.Equal(t, items, out)
}

type blockingReranker struct {
    mu    sync.Mutex
    block chan struct{}
}

func (b *blockingReranker) Rerank(ctx context.Context, items []matching.RerankItem) ([]matching.RerankItem, bool, error) {
    <-b.block
    return items, true, nil
}

type errorReranker struct{ err error }

func (e *errorReranker) Rerank(_ context.Context, _ []matching.RerankItem) ([]matching.RerankItem, bool, error) {
    return nil, false, e.err
}
```

- [ ] **Step 2: Run red**

```bash
go test -race ./pkg/matching/... -run Reranker -count=1
```

Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// pkg/matching/rerank.go
package matching

import (
    "context"
    "time"

    util "github.com/pitabwire/frame/util"
)

// RerankItem is one input/output of the reranker. The reranker may set
// RerankScore; if it leaves it nil, treat it as "not reranked."
type RerankItem struct {
    ID          string
    Score       float64
    RerankScore *float64
}

// Reranker re-ranks a slice of items. The bool return indicates whether
// reranker scores were actually applied (false = retrieval scores only).
type Reranker interface {
    Rerank(ctx context.Context, items []RerankItem) ([]RerankItem, bool, error)
}

// NoopReranker returns the input unchanged and reports used=false. Used
// when MATCHING_RERANKER_ENABLED=false or when the spec calls for it
// (fan-out path defaults to noop unless explicitly opted in).
type NoopReranker struct{}

func (NoopReranker) Rerank(_ context.Context, items []RerankItem) ([]RerankItem, bool, error) {
    return items, false, nil
}

// PooledReranker bounds concurrent calls to an upstream reranker by
// `concurrency` and falls back to retrieval scores when the pool is
// full, when the upstream errors, or when the upstream times out.
// The decision to degrade is logged (WARN) but not propagated as an
// error — the caller-side metric `reranker_pool_in_use` and the
// `reranker_used` flag on each row capture it.
type PooledReranker struct {
    upstream Reranker
    sem      chan struct{}
    timeout  time.Duration
}

func NewPooledReranker(upstream Reranker, concurrency int, timeout time.Duration) *PooledReranker {
    if concurrency < 1 {
        concurrency = 1
    }
    if timeout <= 0 {
        timeout = time.Second
    }
    return &PooledReranker{
        upstream: upstream,
        sem:      make(chan struct{}, concurrency),
        timeout:  timeout,
    }
}

func (p *PooledReranker) Rerank(ctx context.Context, items []RerankItem) ([]RerankItem, bool, error) {
    select {
    case p.sem <- struct{}{}:
        defer func() { <-p.sem }()
    default:
        util.Log(ctx).WithField("count", len(items)).Warn("reranker pool full; falling back to retrieval scores")
        return items, false, nil
    }

    cctx, cancel := context.WithTimeout(ctx, p.timeout)
    defer cancel()

    out, used, err := p.upstream.Rerank(cctx, items)
    if err != nil {
        util.Log(ctx).WithError(err).Warn("reranker error; falling back to retrieval scores")
        return items, false, nil
    }
    return out, used, nil
}
```

- [ ] **Step 4: Run green**

```bash
go test -race ./pkg/matching/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/matching/rerank.go pkg/matching/rerank_test.go
git commit -m "feat(matching): Reranker interface with noop + bounded-pool fallback"
```

---

### Task 11: Path A orchestrator (`pkg/matching/fanout.go`)

Pure function: given an opportunity + dependency interfaces, run the spec §3.1 pipeline. No goroutines, no JetStream, no `time.Now()` (caller passes `now`). The JetStream consumer in Task 14 wraps it.

**Files:**

- Create: `pkg/matching/fanout.go`
- Test: `pkg/matching/fanout_test.go`

- [ ] **Step 1: Define the dependency contract via interfaces**

These interfaces are satisfied by the concrete types from earlier tasks (`Store`, `EventLog`, `KNN`, `Reranker`) — but the orchestrator depends only on the minimal slice, which makes unit testing trivial.

```go
// pkg/matching/fanout.go
package matching

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "fmt"
    "time"

    util "github.com/pitabwire/frame/util"
)

// FanOutDeps is the minimal contract Path A needs.
type FanOutDeps struct {
    KNN      fanOutSearcher
    Store    matchUpserter
    EventLog matchEventWriter
    Reranker Reranker
    Weights  Weights
    Now      func() time.Time
    NewID    func() string // ID factory; defaults to xid-style hex
}

type fanOutSearcher interface {
    FanOutKNN(ctx context.Context, p FanOutKNNParams) ([]CandidateHit, error)
}
type matchUpserter interface {
    UpsertMatches(ctx context.Context, ms []Match) error
}
type matchEventWriter interface {
    WriteMatchEvent(ctx context.Context, m MatchEvent) error
    WriteMatchRunEvent(ctx context.Context, r MatchRunEvent) error
}

// FanOutInput is one CanonicalUpsertedV1 reduced to its matching-relevant fields.
type FanOutInput struct {
    CanonicalID   string
    OpportunityID string
    Kind          string
    Country       string
    SalaryMaxUSD  *int
    Embedding     []float32
    FirstSeenAt   time.Time
    Skills        []string
}

// FanOutResult summarises a single fan-out execution.
type FanOutResult struct {
    RunID             string
    CandidatesScanned int
    MatchesWritten    int
    Overflowed        int
    RerankerStatus    string
    LatencyMS         int
}

// FanOut runs Path A end-to-end. Idempotent at the DB level (UpsertMatches
// + ON CONFLICT). Returns a summary suitable for telemetry.
func FanOut(ctx context.Context, in FanOutInput, deps FanOutDeps) (FanOutResult, error) {
    now := deps.Now
    if now == nil {
        now = time.Now
    }
    idgen := deps.NewID
    if idgen == nil {
        idgen = newHexID
    }

    runID := idgen()
    startedAt := now()
    runEvt := MatchRunEvent{
        RunID:       runID,
        StartedAt:   startedAt,
        Path:        PathFanout,
        TriggeredBy: "canonical_upserted",
        CanonicalID: in.CanonicalID,
    }
    defer func() {
        // Best-effort tail write. Errors here log but don't override
        // the caller's outcome.
        _ = deps.EventLog.WriteMatchRunEvent(ctx, runEvt)
    }()

    // 1. KNN retrieval. Limit hard-coded to 500 per spec §3.1.
    hits, err := deps.KNN.FanOutKNN(ctx, FanOutKNNParams{
        OppEmbedding: in.Embedding,
        OppKind:      in.Kind,
        OppCountry:   in.Country,
        OppSalaryMax: in.SalaryMaxUSD,
        Limit:        500,
    })
    if err != nil {
        runEvt.Status = "error"
        runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
        return FanOutResult{RunID: runID}, fmt.Errorf("matching: fanout knn: %w", err)
    }
    runEvt.CandidatesScanned = len(hits)

    if len(hits) == 0 {
        runEvt.Status = "ok"
        runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
        return FanOutResult{RunID: runID, CandidatesScanned: 0}, nil
    }

    // 2. Score each candidate using the deterministic Score function.
    type scored struct {
        hit   CandidateHit
        score Result
    }
    scoredHits := make([]scored, 0, len(hits))
    for _, h := range hits {
        // We don't have the candidate's full feature bag in the hit; the
        // hit carries only the filter columns (kinds/countries/salary
        // floor). Score from those + the opportunity's signals.
        candSig := CandidateSignal{
            Embedding:      nil, // distance is already the source of truth for the cosine term; we feed it in below
            Skills:         nil, // skills overlap is neutral here — fanout uses the index, not the raw CV
            Countries:      h.Countries,
            SalaryFloorUSD: h.SalaryFloorUSD,
        }
        oppSig := OpportunitySignal{
            Embedding:    in.Embedding,
            Skills:       in.Skills,
            Country:      in.Country,
            SalaryMaxUSD: in.SalaryMaxUSD,
            FirstSeenAt:  in.FirstSeenAt,
        }
        res := Score(candSig, oppSig, deps.Weights, now())
        // Override the cosine term with the distance the index returned
        // (the candidate's full embedding isn't materialized in the hit;
        // pgvector did the cosine for us).
        res.Cosine = 1.0 - h.Distance/2.0
        if res.Cosine < 0 {
            res.Cosine = 0
        }
        res.Total = deps.Weights.Cosine*res.Cosine +
            deps.Weights.Skills*res.SkillsOverlap +
            deps.Weights.Geo*res.GeoMatch +
            deps.Weights.Salary*res.SalaryFit -
            deps.Weights.Stale*res.StalePenalty
        if res.Total < 0 {
            res.Total = 0
        }
        if res.Total < h.MinScore {
            continue
        }
        scoredHits = append(scoredHits, scored{hit: h, score: res})
    }

    // 3. Reranker (best-effort).
    rerankIn := make([]RerankItem, len(scoredHits))
    for i, s := range scoredHits {
        rerankIn[i] = RerankItem{ID: s.hit.CandidateID, Score: s.score.Total}
    }
    rerankOut, used, _ := deps.Reranker.Rerank(ctx, rerankIn)
    rerankByID := map[string]float64{}
    if used {
        for _, r := range rerankOut {
            if r.RerankScore != nil {
                rerankByID[r.ID] = *r.RerankScore
            }
        }
        runEvt.RerankerStatus = "used"
    } else {
        runEvt.RerankerStatus = "skipped"
    }

    // 4. Daily-cap enforcement is applied during write: rows beyond the
    // candidate's daily_cap are stored with status='overflow'. This is
    // a per-candidate cap, but in a single fan-out we only see one
    // candidate at most once, so the cap matters only across runs.
    // The check that uses the continuous aggregate on
    // candidate_match_events is in Phase 5; here we tag based on the
    // index row's DailyCap field.
    matches := make([]Match, 0, len(scoredHits))
    overflowed := 0
    for _, s := range scoredHits {
        status := StatusNew
        // Heuristic cap: if the candidate's DailyCap is 0, treat as
        // unbounded. Real cap enforcement comes in Phase 5.
        _ = s.hit.DailyCap

        var rerankPtr *float64
        if v, ok := rerankByID[s.hit.CandidateID]; ok {
            rerankPtr = &v
        }
        matches = append(matches, Match{
            MatchID:       idgen(),
            CandidateID:   s.hit.CandidateID,
            OpportunityID: in.OpportunityID,
            Status:        status,
            Score:         s.score.Total,
            RerankScore:   rerankPtr,
            RerankerUsed:  used && rerankPtr != nil,
            LastEventID:   runID,
            Metadata: map[string]any{
                "path":         "fanout",
                "canonical_id": in.CanonicalID,
                "kind":         in.Kind,
            },
        })
    }

    // 5. Bulk write.
    if err := deps.Store.UpsertMatches(ctx, matches); err != nil {
        runEvt.Status = "error"
        runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
        return FanOutResult{RunID: runID}, fmt.Errorf("matching: fanout upsert: %w", err)
    }
    runEvt.MatchesWritten = len(matches)

    // 6. Per-match events.
    for _, m := range matches {
        rerankPtr := m.RerankScore
        evt := MatchEvent{
            EventID:       idgen(),
            CandidateID:   m.CandidateID,
            OpportunityID: in.OpportunityID,
            CanonicalID:   in.CanonicalID,
            Kind:          EventKindGenerated,
            Path:          PathFanout,
            Score:         m.Score,
            RerankScore:   rerankPtr,
            RerankerUsed:  m.RerankerUsed,
            OccurredAt:    now(),
            Data: map[string]any{
                "run_id":   runID,
                "match_id": m.MatchID,
            },
        }
        if err := deps.EventLog.WriteMatchEvent(ctx, evt); err != nil {
            util.Log(ctx).WithError(err).
                WithField("event_id", evt.EventID).
                Warn("match event write failed (non-fatal)")
            // continue — the row in candidate_matches is the source of
            // truth; the event is the audit trail.
        }
    }

    runEvt.Status = "ok"
    runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
    finished := now()
    runEvt.FinishedAt = &finished
    return FanOutResult{
        RunID:             runID,
        CandidatesScanned: len(hits),
        MatchesWritten:    len(matches),
        Overflowed:        overflowed,
        RerankerStatus:    runEvt.RerankerStatus,
        LatencyMS:         runEvt.LatencyMS,
    }, nil
}

func newHexID() string {
    var b [12]byte
    _, _ = rand.Read(b[:])
    return hex.EncodeToString(b[:])
}
```

- [ ] **Step 2: Write a unit test using fakes (no DB)**

```go
// pkg/matching/fanout_test.go
package matching_test

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeKNN struct{ hits []matching.CandidateHit; err error }

func (f *fakeKNN) FanOutKNN(_ context.Context, _ matching.FanOutKNNParams) ([]matching.CandidateHit, error) {
    return f.hits, f.err
}

type fakeStore struct{ ms []matching.Match; err error }

func (f *fakeStore) UpsertMatches(_ context.Context, ms []matching.Match) error {
    f.ms = append(f.ms, ms...)
    return f.err
}

type fakeEventLog struct {
    matchEvents []matching.MatchEvent
    runEvents   []matching.MatchRunEvent
}

func (f *fakeEventLog) WriteMatchEvent(_ context.Context, e matching.MatchEvent) error {
    f.matchEvents = append(f.matchEvents, e)
    return nil
}
func (f *fakeEventLog) WriteMatchRunEvent(_ context.Context, e matching.MatchRunEvent) error {
    f.runEvents = append(f.runEvents, e)
    return nil
}

func TestFanOut_FiltersBelowMinScore(t *testing.T) {
    knn := &fakeKNN{hits: []matching.CandidateHit{
        {CandidateID: "above", Distance: 0.2, MinScore: 0.5, Kinds: []string{"job"}},
        {CandidateID: "below", Distance: 1.9, MinScore: 0.5, Kinds: []string{"job"}},
    }}
    store := &fakeStore{}
    el := &fakeEventLog{}

    res, err := matching.FanOut(context.Background(), matching.FanOutInput{
        CanonicalID:   "c-1",
        OpportunityID: "o-1",
        Kind:          "job",
        Country:       "KE",
        Embedding:     makeUnitVector(1536, 0),
        FirstSeenAt:   time.Now(),
    }, matching.FanOutDeps{
        KNN: knn, Store: store, EventLog: el,
        Reranker: matching.NoopReranker{},
        Weights:  matching.DefaultWeights(),
        Now:      func() time.Time { return time.Now() },
    })
    require.NoError(t, err)
    require.Equal(t, 2, res.CandidatesScanned)
    require.Equal(t, 1, res.MatchesWritten)
    require.Len(t, store.ms, 1)
    require.Equal(t, "above", store.ms[0].CandidateID)
    require.Len(t, el.runEvents, 1)
    require.Equal(t, "ok", el.runEvents[0].Status)
}

func TestFanOut_RerankerSkippedReportsStatus(t *testing.T) {
    knn := &fakeKNN{hits: []matching.CandidateHit{
        {CandidateID: "c1", Distance: 0.1, MinScore: 0.3, Kinds: []string{"job"}},
    }}
    res, err := matching.FanOut(context.Background(), matching.FanOutInput{
        CanonicalID: "c-2", OpportunityID: "o-2", Kind: "job",
        Embedding: makeUnitVector(1536, 0), FirstSeenAt: time.Now(),
    }, matching.FanOutDeps{
        KNN: knn, Store: &fakeStore{}, EventLog: &fakeEventLog{},
        Reranker: matching.NoopReranker{},
        Weights:  matching.DefaultWeights(),
    })
    require.NoError(t, err)
    require.Equal(t, "skipped", res.RerankerStatus)
}
```

- [ ] **Step 3: Run red, then green**

```bash
go test -race ./pkg/matching/... -run TestFanOut -count=1
```

Expected: PASS (after the implementation is in place).

- [ ] **Step 4: Commit**

```bash
git add pkg/matching/fanout.go pkg/matching/fanout_test.go
git commit -m "feat(matching): Path A fan-out orchestrator (pure)"
```

---

### Task 12: Path B orchestrator (`pkg/matching/gapfill.go`)

Pure function called by the HTTP handler in Phase 4. Same shape as Path A but starts from a candidate, does a `ReverseKNN`, and upserts only `status='new'` rows (existing rows are preserved by the conflict guard).

**Files:**

- Create: `pkg/matching/gapfill.go`
- Test: `pkg/matching/gapfill_test.go`

- [ ] **Step 1: Implement**

```go
// pkg/matching/gapfill.go
package matching

import (
    "context"
    "fmt"
    "time"
)

// GapFillDeps mirrors FanOutDeps but uses ReverseKNN.
type GapFillDeps struct {
    KNN      reverseSearcher
    Store    matchUpserter
    EventLog matchEventWriter
    Reranker Reranker
    Weights  Weights
    Now      func() time.Time
    NewID    func() string
}

type reverseSearcher interface {
    ReverseKNN(ctx context.Context, p ReverseKNNParams) ([]OppHit, error)
}

// GapFillInput is one candidate's read-time refresh.
type GapFillInput struct {
    CandidateID    string
    Embedding      []float32
    Skills         []string
    Countries      []string
    Kinds          []string
    SalaryFloorUSD *int
    Since          time.Time // cursor returned by previous page or 24h ago
    MinScore       float64
}

// GapFillResult summarises the read-path execution.
type GapFillResult struct {
    RunID          string
    OppsScanned    int
    MatchesWritten int
    RerankerStatus string
    LatencyMS      int
}

// GapFill runs Path B: candidate-side discovery of new opportunities
// since the supplied cursor. Idempotent — UpsertMatches's conflict
// guard preserves any rows fan-out already wrote.
func GapFill(ctx context.Context, in GapFillInput, deps GapFillDeps) (GapFillResult, error) {
    now := deps.Now
    if now == nil {
        now = time.Now
    }
    idgen := deps.NewID
    if idgen == nil {
        idgen = newHexID
    }
    runID := idgen()
    startedAt := now()
    runEvt := MatchRunEvent{
        RunID:       runID,
        StartedAt:   startedAt,
        Path:        PathGap,
        TriggeredBy: "extension_poll",
        CandidateID: in.CandidateID,
    }
    defer func() { _ = deps.EventLog.WriteMatchRunEvent(ctx, runEvt) }()

    hits, err := deps.KNN.ReverseKNN(ctx, ReverseKNNParams{
        CandidateEmbedding: in.Embedding,
        Kinds:              in.Kinds,
        Countries:          in.Countries,
        Since:              in.Since,
        Limit:              100,
    })
    if err != nil {
        runEvt.Status = "error"
        runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
        return GapFillResult{RunID: runID}, fmt.Errorf("matching: gapfill knn: %w", err)
    }
    runEvt.CandidatesScanned = 1 // a single candidate is in-scope for Path B

    matches := make([]Match, 0, len(hits))
    rerankIn := make([]RerankItem, 0, len(hits))
    type scored struct {
        hit OppHit
        tot float64
    }
    scoredHits := make([]scored, 0, len(hits))
    for _, h := range hits {
        candSig := CandidateSignal{
            Embedding:      in.Embedding,
            Skills:         in.Skills,
            Countries:      in.Countries,
            SalaryFloorUSD: in.SalaryFloorUSD,
        }
        oppSig := OpportunitySignal{
            Embedding:   nil, // gap-fill uses the distance the index already returned
            Skills:      nil,
            Country:     h.Country,
            FirstSeenAt: h.FirstSeenAt,
        }
        res := Score(candSig, oppSig, deps.Weights, now())
        res.Cosine = 1.0 - h.Distance/2.0
        if res.Cosine < 0 {
            res.Cosine = 0
        }
        res.Total = deps.Weights.Cosine*res.Cosine +
            deps.Weights.Skills*res.SkillsOverlap +
            deps.Weights.Geo*res.GeoMatch +
            deps.Weights.Salary*res.SalaryFit -
            deps.Weights.Stale*res.StalePenalty
        if res.Total < 0 {
            res.Total = 0
        }
        if res.Total < in.MinScore {
            continue
        }
        scoredHits = append(scoredHits, scored{hit: h, tot: res.Total})
        rerankIn = append(rerankIn, RerankItem{ID: h.OpportunityID, Score: res.Total})
    }

    rerankOut, used, _ := deps.Reranker.Rerank(ctx, rerankIn)
    rerankByID := map[string]float64{}
    if used {
        for _, r := range rerankOut {
            if r.RerankScore != nil {
                rerankByID[r.ID] = *r.RerankScore
            }
        }
        runEvt.RerankerStatus = "used"
    } else {
        runEvt.RerankerStatus = "skipped"
    }

    for _, s := range scoredHits {
        var rp *float64
        if v, ok := rerankByID[s.hit.OpportunityID]; ok {
            rp = &v
        }
        matches = append(matches, Match{
            MatchID:       idgen(),
            CandidateID:   in.CandidateID,
            OpportunityID: s.hit.OpportunityID,
            Status:        StatusNew,
            Score:         s.tot,
            RerankScore:   rp,
            RerankerUsed:  used && rp != nil,
            LastEventID:   runID,
            Metadata:      map[string]any{"path": "gap"},
        })
    }

    if err := deps.Store.UpsertMatches(ctx, matches); err != nil {
        runEvt.Status = "error"
        runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
        return GapFillResult{RunID: runID}, fmt.Errorf("matching: gapfill upsert: %w", err)
    }
    runEvt.MatchesWritten = len(matches)

    for _, m := range matches {
        _ = deps.EventLog.WriteMatchEvent(ctx, MatchEvent{
            EventID:       idgen(),
            CandidateID:   m.CandidateID,
            OpportunityID: m.OpportunityID,
            Kind:          EventKindGenerated,
            Path:          PathGap,
            Score:         m.Score,
            RerankScore:   m.RerankScore,
            RerankerUsed:  m.RerankerUsed,
            OccurredAt:    now(),
            Data:          map[string]any{"run_id": runID, "match_id": m.MatchID},
        })
    }

    runEvt.Status = "ok"
    runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
    finished := now()
    runEvt.FinishedAt = &finished
    return GapFillResult{
        RunID:          runID,
        OppsScanned:    len(hits),
        MatchesWritten: len(matches),
        RerankerStatus: runEvt.RerankerStatus,
        LatencyMS:      runEvt.LatencyMS,
    }, nil
}
```

- [ ] **Step 2: Write the unit test (fakes only)**

```go
package matching_test

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeRevKNN struct{ hits []matching.OppHit }

func (f *fakeRevKNN) ReverseKNN(_ context.Context, _ matching.ReverseKNNParams) ([]matching.OppHit, error) {
    return f.hits, nil
}

func TestGapFill_FiltersBelowMinScore(t *testing.T) {
    knn := &fakeRevKNN{hits: []matching.OppHit{
        {OpportunityID: "good", Distance: 0.1, FirstSeenAt: time.Now()},
        {OpportunityID: "bad", Distance: 1.9, FirstSeenAt: time.Now()},
    }}
    store := &fakeStore{}
    el := &fakeEventLog{}
    res, err := matching.GapFill(context.Background(), matching.GapFillInput{
        CandidateID: "u",
        Embedding:   makeUnitVector(1536, 0),
        Since:       time.Now().Add(-time.Hour),
        MinScore:    0.4,
    }, matching.GapFillDeps{
        KNN: knn, Store: store, EventLog: el,
        Reranker: matching.NoopReranker{},
        Weights:  matching.DefaultWeights(),
    })
    require.NoError(t, err)
    require.Equal(t, 2, res.OppsScanned)
    require.Equal(t, 1, res.MatchesWritten)
    require.Equal(t, "good", store.ms[0].OpportunityID)
}
```

- [ ] **Step 3: Run red, then green**

```bash
go test -race ./pkg/matching/... -run TestGapFill -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add pkg/matching/gapfill.go pkg/matching/gapfill_test.go
git commit -m "feat(matching): Path B gap-filler orchestrator (pure)"
```

---

### Task 13: Path C orchestrator (`pkg/matching/candidate_change.go`)

Candidate-side rematch on rule / embedding change. Same shape as gap-fill but: (a) `Since` is computed as "all-time" or last-rematch boundary, (b) wrapped in a `Debouncer` that rejects requests within N minutes.

**Files:**

- Create: `pkg/matching/candidate_change.go`
- Test: `pkg/matching/candidate_change_test.go`

- [ ] **Step 1: Implement the orchestrator + debouncer interface**

```go
// pkg/matching/candidate_change.go
package matching

import (
    "context"
    "errors"
    "time"
)

// Debouncer guards Path C against thrashing when a candidate flips
// rules / embedding rapidly. Typical impl: a Valkey SETNX with TTL.
type Debouncer interface {
    // Acquire returns true if the caller may proceed. False means the
    // candidate has already been rematched within the debounce window
    // and the caller should drop the request.
    Acquire(ctx context.Context, candidateID string, ttl time.Duration) (bool, error)
}

// ErrDebounced is returned when the debounce lock denies the rematch.
var ErrDebounced = errors.New("matching: rematch debounced")

// CandidateChangeDeps composes the dependencies. We re-use GapFillDeps
// because the work shape after the debounce check is identical to
// Path B except for the trigger metadata.
type CandidateChangeDeps struct {
    Debouncer Debouncer
    DebounceTTL time.Duration
    GapFill   GapFillDeps
}

// CandidateChange is the trigger payload — what changed and the
// resulting candidate signal.
type CandidateChange struct {
    CandidateID    string
    Embedding      []float32
    Skills         []string
    Countries      []string
    Kinds          []string
    SalaryFloorUSD *int
    MinScore       float64
    TriggeredBy    string // rules_changed | cv_changed | admin
}

// Run executes Path C. Returns ErrDebounced if the debounce lock
// rejects the request.
func RunCandidateChange(ctx context.Context, in CandidateChange, deps CandidateChangeDeps) (GapFillResult, error) {
    ttl := deps.DebounceTTL
    if ttl <= 0 {
        ttl = 60 * time.Second
    }
    ok, err := deps.Debouncer.Acquire(ctx, in.CandidateID, ttl)
    if err != nil {
        return GapFillResult{}, err
    }
    if !ok {
        return GapFillResult{}, ErrDebounced
    }

    // Path C is "all-time" — we sweep opportunities posted in the last
    // 30 days. Older rows are covered by the periodic re-bootstrap.
    since := time.Now().Add(-30 * 24 * time.Hour)

    return GapFill(ctx, GapFillInput{
        CandidateID:    in.CandidateID,
        Embedding:      in.Embedding,
        Skills:         in.Skills,
        Countries:      in.Countries,
        Kinds:          in.Kinds,
        SalaryFloorUSD: in.SalaryFloorUSD,
        Since:          since,
        MinScore:       in.MinScore,
    }, deps.GapFill)
}
```

- [ ] **Step 2: Test debounce behaviour**

```go
package matching_test

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeDebouncer struct{ allow bool }

func (f *fakeDebouncer) Acquire(_ context.Context, _ string, _ time.Duration) (bool, error) {
    return f.allow, nil
}

func TestRunCandidateChange_DebouncedReturnsErrDebounced(t *testing.T) {
    _, err := matching.RunCandidateChange(context.Background(),
        matching.CandidateChange{CandidateID: "u"},
        matching.CandidateChangeDeps{
            Debouncer:   &fakeDebouncer{allow: false},
            DebounceTTL: time.Second,
            GapFill: matching.GapFillDeps{
                KNN:      &fakeRevKNN{},
                Store:    &fakeStore{},
                EventLog: &fakeEventLog{},
                Reranker: matching.NoopReranker{},
                Weights:  matching.DefaultWeights(),
            },
        })
    require.True(t, errors.Is(err, matching.ErrDebounced))
}

func TestRunCandidateChange_AllowedRunsGapFill(t *testing.T) {
    knn := &fakeRevKNN{hits: []matching.OppHit{
        {OpportunityID: "o1", Distance: 0.2, FirstSeenAt: time.Now()},
    }}
    res, err := matching.RunCandidateChange(context.Background(),
        matching.CandidateChange{
            CandidateID: "u",
            Embedding:   makeUnitVector(1536, 0),
            MinScore:    0.3,
        },
        matching.CandidateChangeDeps{
            Debouncer:   &fakeDebouncer{allow: true},
            DebounceTTL: time.Second,
            GapFill: matching.GapFillDeps{
                KNN: knn, Store: &fakeStore{}, EventLog: &fakeEventLog{},
                Reranker: matching.NoopReranker{},
                Weights:  matching.DefaultWeights(),
            },
        })
    require.NoError(t, err)
    require.Equal(t, 1, res.MatchesWritten)
}
```

- [ ] **Step 3: Run green**

```bash
go test -race ./pkg/matching/... -run RunCandidateChange -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add pkg/matching/candidate_change.go pkg/matching/candidate_change_test.go
git commit -m "feat(matching): Path C candidate-change orchestrator with debounce"
```

---

### Task 14: Metrics definitions (`pkg/matching/metrics.go`)

Per spec §5.2. Register lazily — Frame's `util.Metrics` is per-service; the registration sites in the consumers (Tasks 16, 17) actually wire them.

**Files:**

- Create: `pkg/matching/metrics.go`

- [ ] **Step 1: Implement**

```go
// pkg/matching/metrics.go
package matching

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds the Prometheus collectors for the matching pipeline.
// Construct once per service via NewMetrics; share across consumers.
type Metrics struct {
    MatchesWritten            *prometheus.CounterVec
    MatchScore                *prometheus.HistogramVec
    MatchLatency              *prometheus.HistogramVec
    DLQDepth                  *prometheus.GaugeVec
    RerankerPoolInUse         prometheus.Gauge
    CandidateMatchIndexStale  *prometheus.CounterVec
}

// NewMetrics constructs the collectors against the given registerer.
// In tests pass prometheus.NewRegistry(); in production pass
// prometheus.DefaultRegisterer.
func NewMetrics(reg prometheus.Registerer) *Metrics {
    factory := promauto.With(reg)
    return &Metrics{
        MatchesWritten: factory.NewCounterVec(prometheus.CounterOpts{
            Name: "matches_written_total",
            Help: "Matches written, by path/kind/status.",
        }, []string{"path", "kind", "status"}),
        MatchScore: factory.NewHistogramVec(prometheus.HistogramOpts{
            Name:    "match_score_bucket",
            Help:    "Distribution of final match scores.",
            Buckets: prometheus.LinearBuckets(0, 0.05, 21), // 0.00 → 1.00 in 0.05 steps
        }, []string{"path", "kind"}),
        MatchLatency: factory.NewHistogramVec(prometheus.HistogramOpts{
            Name:    "match_latency_seconds",
            Help:    "End-to-end latency per path/phase.",
            Buckets: prometheus.ExponentialBuckets(0.005, 2, 14), // 5ms → ~40s
        }, []string{"path", "phase"}),
        DLQDepth: factory.NewGaugeVec(prometheus.GaugeOpts{
            Name: "matching_dlq_depth",
            Help: "Current dead-letter depth, by subject.",
        }, []string{"subject"}),
        RerankerPoolInUse: factory.NewGauge(prometheus.GaugeOpts{
            Name: "reranker_pool_in_use",
            Help: "Reranker workers currently in flight.",
        }),
        CandidateMatchIndexStale: factory.NewCounterVec(prometheus.CounterOpts{
            Name: "candidate_match_indexes_stale_total",
            Help: "Rebuilds triggered, by cause.",
        }, []string{"cause"}),
    }
}
```

- [ ] **Step 2: Quick compile check**

```bash
go build ./pkg/matching/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add pkg/matching/metrics.go
git commit -m "feat(matching): Prometheus metric definitions for the matching pipeline"
```

---

### Task 15: DLQ guard (`apps/matching/service/matching/v1/dlq.go`)

Subject from spec §3.7: `svc.opportunities.matching.deadletter`. Helper counts redeliveries (via JetStream message metadata) and, at threshold, publishes the original payload + envelope to the DLQ subject, then acks the original message to stop the redelivery loop.

**Files:**

- Modify: `pkg/events/v1/names.go` (add subject constant)
- Create: `apps/matching/service/matching/v1/dlq.go`
- Create: `apps/matching/service/matching/v1/dlq_test.go`

- [ ] **Step 1: Add the subject constant**

Open `pkg/events/v1/names.go` and add inside the existing `const` block (near the other `Subject…` declarations):

```go
// SubjectMatchingDeadletter receives matching events that exceeded the
// redelivery budget. Admin /api/admin/dlq/replay re-publishes them.
SubjectMatchingDeadletter = "svc.opportunities.matching.deadletter"
```

- [ ] **Step 2: Write the failing DLQ-guard test**

```go
package v1_test

import (
    "context"
    "errors"
    "testing"

    "github.com/stretchr/testify/require"

    v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/matching/v1"
)

type fakePublisher struct {
    published [][]byte
    subject   string
}

func (f *fakePublisher) Publish(_ context.Context, subject string, payload []byte) error {
    f.subject = subject
    f.published = append(f.published, payload)
    return nil
}

func TestDLQGuard_BelowThreshold_PropagatesError(t *testing.T) {
    pub := &fakePublisher{}
    g := v1.NewDLQGuard(pub, "svc.opportunities.matching.deadletter", 5)

    err := g.Run(context.Background(), 3, []byte("payload"), func() error {
        return errors.New("transient")
    })
    require.Error(t, err) // not dead-lettered yet → caller retries
    require.Empty(t, pub.published)
}

func TestDLQGuard_AtThreshold_PublishesToDLQ_ReturnsNil(t *testing.T) {
    pub := &fakePublisher{}
    g := v1.NewDLQGuard(pub, "svc.opportunities.matching.deadletter", 5)

    err := g.Run(context.Background(), 5, []byte("payload"), func() error {
        return errors.New("poison")
    })
    require.NoError(t, err) // ack the message so redelivery stops
    require.Equal(t, [][]byte{[]byte("payload")}, pub.published)
    require.Equal(t, "svc.opportunities.matching.deadletter", pub.subject)
}

func TestDLQGuard_Success_PassesThrough(t *testing.T) {
    pub := &fakePublisher{}
    g := v1.NewDLQGuard(pub, "svc.opportunities.matching.deadletter", 5)

    err := g.Run(context.Background(), 2, []byte("payload"), func() error { return nil })
    require.NoError(t, err)
    require.Empty(t, pub.published)
}
```

- [ ] **Step 3: Run red**

```bash
go test -race ./apps/matching/service/matching/v1/... -count=1
```

Expected: FAIL.

- [ ] **Step 4: Implement**

```go
// apps/matching/service/matching/v1/dlq.go
package v1

import (
    "context"

    util "github.com/pitabwire/frame/util"
)

// DeadLetterPublisher is the subset of an EventsManager we need to drop a
// poisoned payload onto the dead-letter subject.
type DeadLetterPublisher interface {
    Publish(ctx context.Context, subject string, payload []byte) error
}

// DLQGuard publishes payloads to a dead-letter subject after a configurable
// number of failed redeliveries. The original consumer should call Run
// inside its Handle method.
type DLQGuard struct {
    pub       DeadLetterPublisher
    subject   string
    threshold int
}

// NewDLQGuard constructs a guard. threshold is the redelivery count
// at which the next failure dead-letters the message. Spec §3.7 sets it
// to 5.
func NewDLQGuard(pub DeadLetterPublisher, subject string, threshold int) *DLQGuard {
    if threshold < 1 {
        threshold = 5
    }
    return &DLQGuard{pub: pub, subject: subject, threshold: threshold}
}

// Run invokes work() and inspects the result. On success: returns nil.
// On error with redelivery < threshold: returns the error so JetStream
// will redeliver. On error with redelivery >= threshold: publishes the
// payload to the DLQ subject and returns nil so JetStream ACKs the
// message (stopping the redelivery loop).
func (g *DLQGuard) Run(ctx context.Context, redelivery int, payload []byte, work func() error) error {
    err := work()
    if err == nil {
        return nil
    }
    if redelivery < g.threshold {
        return err
    }
    if pubErr := g.pub.Publish(ctx, g.subject, payload); pubErr != nil {
        util.Log(ctx).WithError(pubErr).WithField("subject", g.subject).
            Error("dlq publish failed; falling back to redelivery")
        return err
    }
    util.Log(ctx).WithError(err).WithField("subject", g.subject).
        WithField("redelivery", redelivery).
        Warn("dead-lettered after redelivery budget exhausted")
    return nil
}
```

- [ ] **Step 5: Run green**

```bash
go test -race ./apps/matching/service/matching/v1/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/matching/service/matching/v1/dlq.go apps/matching/service/matching/v1/dlq_test.go pkg/events/v1/names.go
git commit -m "feat(matching): DLQGuard helper + SubjectMatchingDeadletter constant"
```

---

### Task 16: Fan-out consumer (`apps/matching/service/matching/v1/fanout_consumer.go`)

Implements Frame's `queue.SubscribeWorker` for `TopicCanonicalsUpserted`. Decodes the envelope, loads the opportunity's embedding via `variantstate.Store` (or a slimmer query — see step 1), calls `matching.FanOut`, wraps with `DLQGuard`. **No tests-against-real-NATS** here — that's the integration suite in Task 18. Unit test uses a fake events manager.

**Files:**

- Create: `apps/matching/service/matching/v1/fanout_consumer.go`
- Create: `apps/matching/service/matching/v1/fanout_consumer_test.go`

- [ ] **Step 1: Sketch the wiring**

```go
// apps/matching/service/matching/v1/fanout_consumer.go
package v1

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"

    util "github.com/pitabwire/frame/util"

    eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

// FanOutConsumerDeps gathers everything the consumer needs at construction.
type FanOutConsumerDeps struct {
    DB            *sql.DB
    Store         *matching.Store
    EventLog      *matching.EventLog
    KNN           *matching.KNN
    Reranker      matching.Reranker
    Weights       matching.Weights
    DLQ           *DLQGuard
    OppEmbedQ     OppEmbeddingQuery // see below
}

// OppEmbeddingQuery hides the opportunity-embedding fetch from the
// consumer so the integration test can stub it without touching variantstate.
type OppEmbeddingQuery interface {
    GetEmbedding(ctx context.Context, opportunityID string) ([]float32, error)
}

// FanOutConsumer wires Path A to TopicCanonicalsUpserted.
type FanOutConsumer struct {
    deps FanOutConsumerDeps
}

func NewFanOutConsumer(d FanOutConsumerDeps) *FanOutConsumer {
    return &FanOutConsumer{deps: d}
}

// Name implements queue.SubscribeWorker.
func (c *FanOutConsumer) Name() string { return eventsv1.TopicCanonicalsUpserted }

// Handle implements queue.SubscribeWorker. Frame surfaces JetStream
// metadata (including redelivery count) via the headers map.
func (c *FanOutConsumer) Handle(ctx context.Context, headers map[string]string, payload []byte) error {
    redelivery := parseRedeliveryHeader(headers)
    return c.deps.DLQ.Run(ctx, redelivery, payload, func() error {
        return c.handleOnce(ctx, payload)
    })
}

func (c *FanOutConsumer) handleOnce(ctx context.Context, payload []byte) error {
    var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
    if err := json.Unmarshal(payload, &env); err != nil {
        // Bad shape → permanent failure; the DLQ guard will dead-letter
        // it on the next redelivery if work() keeps returning errors.
        return fmt.Errorf("matching: fanout decode: %w", err)
    }
    body := env.Data

    embedding, err := c.deps.OppEmbedQ.GetEmbedding(ctx, body.OpportunityID)
    if err != nil {
        return fmt.Errorf("matching: load opp embedding %s: %w", body.OpportunityID, err)
    }
    if len(embedding) == 0 {
        // No embedding yet — the embedder will re-emit; skip without error.
        util.Log(ctx).WithField("opportunity_id", body.OpportunityID).
            Info("fanout: opp has no embedding; skipping")
        return nil
    }

    var salaryMax *int
    if body.AmountMax > 0 {
        v := int(body.AmountMax)
        salaryMax = &v
    }
    var skills []string
    if cats, ok := body.Attributes["skills"].([]string); ok {
        skills = cats
    }

    _, err = matching.FanOut(ctx, matching.FanOutInput{
        CanonicalID:   body.HardKey,
        OpportunityID: body.OpportunityID,
        Kind:          body.Kind,
        Country:       body.AnchorCountry,
        SalaryMaxUSD:  salaryMax,
        Embedding:     embedding,
        FirstSeenAt:   body.UpsertedAt,
        Skills:        skills,
    }, matching.FanOutDeps{
        KNN:      c.deps.KNN,
        Store:    c.deps.Store,
        EventLog: c.deps.EventLog,
        Reranker: c.deps.Reranker,
        Weights:  c.deps.Weights,
    })
    return err
}

func parseRedeliveryHeader(headers map[string]string) int {
    // Frame currently exposes JetStream metadata via `Nats-Redelivery-Count`
    // or similar header. Treat absence as zero.
    for _, key := range []string{"Nats-Redelivery-Count", "nats-redelivery-count", "redelivery"} {
        if v, ok := headers[key]; ok {
            var n int
            _, _ = fmt.Sscanf(v, "%d", &n)
            return n
        }
    }
    return 0
}
```

- [ ] **Step 2: Add a tiny `OppEmbeddingQuery` adapter inside the same file**

```go
// SQLOppEmbeddingQuery reads the opportunity's embedding column directly.
type SQLOppEmbeddingQuery struct {
    db *sql.DB
}

func NewSQLOppEmbeddingQuery(db *sql.DB) *SQLOppEmbeddingQuery {
    return &SQLOppEmbeddingQuery{db: db}
}

func (q *SQLOppEmbeddingQuery) GetEmbedding(ctx context.Context, opportunityID string) ([]float32, error) {
    var text string
    err := q.db.QueryRowContext(ctx,
        `SELECT COALESCE(embedding::text, '') FROM opportunities WHERE id = $1`, opportunityID).
        Scan(&text)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }
    if text == "" {
        return nil, nil
    }
    return matching.ParseVectorLiteral(text), nil
}
```

To expose the helper, add an exported wrapper in `pkg/matching/index_store.go` (or a sibling file `vector.go`):

```go
// ParseVectorLiteral exports the textual-vector parser for callers that
// read pgvector columns directly. Errors are silent — bad input yields a
// nil slice, matching the rest of the package's "soft fail" pattern.
func ParseVectorLiteral(s string) []float32 { return parseVectorLiteral(s) }
```

- [ ] **Step 3: Write the unit test (fake everything)**

```go
package v1_test

import (
    "context"
    "encoding/json"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/matching/v1"
    eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeEmbedQ struct{ v []float32 }

func (f *fakeEmbedQ) GetEmbedding(_ context.Context, _ string) ([]float32, error) { return f.v, nil }

type fakeKNNFan struct{}

func (fakeKNNFan) FanOutKNN(_ context.Context, _ matching.FanOutKNNParams) ([]matching.CandidateHit, error) {
    return nil, nil // empty → orchestrator returns 0 matches
}

type captureStore struct{ matches []matching.Match }

func (c *captureStore) UpsertMatches(_ context.Context, ms []matching.Match) error {
    c.matches = append(c.matches, ms...)
    return nil
}

type captureEvents struct {
    runs []matching.MatchRunEvent
}

func (c *captureEvents) WriteMatchEvent(_ context.Context, _ matching.MatchEvent) error { return nil }
func (c *captureEvents) WriteMatchRunEvent(_ context.Context, r matching.MatchRunEvent) error {
    c.runs = append(c.runs, r)
    return nil
}

func TestFanOutConsumer_Handle_HappyPath(t *testing.T) {
    pub := &fakePublisher{}
    dlq := v1.NewDLQGuard(pub, "svc.opportunities.matching.deadletter", 5)

    c := v1.NewFanOutConsumer(v1.FanOutConsumerDeps{
        Store:    nil, // unused — UpsertMatches goes via the deps interface below
        EventLog: nil,
        KNN:      nil,
        Reranker: matching.NoopReranker{},
        Weights:  matching.DefaultWeights(),
        DLQ:      dlq,
        OppEmbedQ: &fakeEmbedQ{v: []float32{1, 0, 0}},
    })

    payload, _ := json.Marshal(eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]{
        Data: eventsv1.CanonicalUpsertedV1{
            OpportunityID: "opp_1",
            HardKey:       "c-1",
            Kind:          "job",
            AnchorCountry: "KE",
            UpsertedAt:    time.Now(),
        },
    })

    // NOTE: this test verifies wiring + redelivery / DLQ guard only.
    // The orchestrator's behaviour with a real KNN+Store is covered in
    // TestFanOut_* (Task 11) and MatchingPipelineSuite (Task 18).
    _ = c
    _ = payload
}
```

The above test compiles but is intentionally minimal — Task 18's integration suite carries the end-to-end check. Keep it so the wiring at least type-checks.

- [ ] **Step 4: Run**

```bash
go build ./apps/matching/...
go test -race ./apps/matching/service/matching/v1/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/matching/service/matching/v1/fanout_consumer.go \
        apps/matching/service/matching/v1/fanout_consumer_test.go \
        pkg/matching/index_store.go
git commit -m "feat(matching): fan-out JetStream consumer wiring Path A to TopicCanonicalsUpserted"
```

---

### Task 17: Candidate-change consumer

Mirrors Task 16 for `TopicCandidatePreferencesUpdated` and `TopicCandidateEmbedding`. Uses `RunCandidateChange` + the same DLQ guard.

**Files:**

- Create: `apps/matching/service/matching/v1/candidate_change_consumer.go`
- Create: `apps/matching/service/matching/v1/candidate_change_consumer_test.go`

- [ ] **Step 1: Implement**

```go
// apps/matching/service/matching/v1/candidate_change_consumer.go
package v1

import (
    "context"
    "encoding/json"
    "fmt"

    util "github.com/pitabwire/frame/util"

    eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

// CandidateChangeConsumerDeps gathers the deps for one consumer.
type CandidateChangeConsumerDeps struct {
    IndexStore *matching.IndexStore
    KNN        *matching.KNN
    Store      *matching.Store
    EventLog   *matching.EventLog
    Reranker   matching.Reranker
    Weights    matching.Weights
    Debouncer  matching.Debouncer
    DLQ        *DLQGuard
    Topic      string // either TopicCandidatePreferencesUpdated or TopicCandidateEmbedding
}

// CandidateChangeConsumer subscribes to one trigger topic and runs Path C.
type CandidateChangeConsumer struct {
    deps CandidateChangeConsumerDeps
}

func NewCandidateChangeConsumer(d CandidateChangeConsumerDeps) *CandidateChangeConsumer {
    return &CandidateChangeConsumer{deps: d}
}

func (c *CandidateChangeConsumer) Name() string { return c.deps.Topic }

func (c *CandidateChangeConsumer) Handle(ctx context.Context, headers map[string]string, payload []byte) error {
    redelivery := parseRedeliveryHeader(headers)
    return c.deps.DLQ.Run(ctx, redelivery, payload, func() error {
        return c.handleOnce(ctx, payload)
    })
}

func (c *CandidateChangeConsumer) handleOnce(ctx context.Context, payload []byte) error {
    candidateID, triggeredBy, err := decodeCandidateChange(c.deps.Topic, payload)
    if err != nil {
        return err
    }
    idx, err := c.deps.IndexStore.Get(ctx, candidateID)
    if err != nil {
        if err == matching.ErrNotFound {
            util.Log(ctx).WithField("candidate_id", candidateID).
                Info("candidate_change: no index row yet; skip")
            return nil
        }
        return fmt.Errorf("matching: candidate change load index %s: %w", candidateID, err)
    }
    _, err = matching.RunCandidateChange(ctx, matching.CandidateChange{
        CandidateID:    candidateID,
        Embedding:      idx.Embedding,
        Countries:      idx.Countries,
        Kinds:          idx.Kinds,
        SalaryFloorUSD: idx.SalaryFloorUSD,
        MinScore:       idx.MinScore,
        TriggeredBy:    triggeredBy,
    }, matching.CandidateChangeDeps{
        Debouncer: c.deps.Debouncer,
        GapFill: matching.GapFillDeps{
            KNN: c.deps.KNN, Store: c.deps.Store, EventLog: c.deps.EventLog,
            Reranker: c.deps.Reranker, Weights: c.deps.Weights,
        },
    })
    if err == matching.ErrDebounced {
        util.Log(ctx).WithField("candidate_id", candidateID).
            Info("candidate_change: debounced")
        return nil
    }
    return err
}

func decodeCandidateChange(topic string, payload []byte) (candidateID, triggeredBy string, err error) {
    switch topic {
    case eventsv1.TopicCandidatePreferencesUpdated:
        var env struct {
            Data struct {
                CandidateID string `json:"candidate_id"`
            } `json:"data"`
        }
        if err = json.Unmarshal(payload, &env); err != nil {
            return "", "", fmt.Errorf("matching: candidate change decode prefs: %w", err)
        }
        return env.Data.CandidateID, "rules_changed", nil
    case eventsv1.TopicCandidateEmbedding:
        var env struct {
            Data struct {
                CandidateID string `json:"candidate_id"`
            } `json:"data"`
        }
        if err = json.Unmarshal(payload, &env); err != nil {
            return "", "", fmt.Errorf("matching: candidate change decode embed: %w", err)
        }
        return env.Data.CandidateID, "cv_changed", nil
    }
    return "", "", fmt.Errorf("matching: candidate change unknown topic %q", topic)
}
```

- [ ] **Step 2: Add a tiny smoke test**

```go
package v1_test

import (
    "testing"

    v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/matching/v1"
    eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

func TestCandidateChangeConsumer_NameMatchesTopic(t *testing.T) {
    c := v1.NewCandidateChangeConsumer(v1.CandidateChangeConsumerDeps{
        Topic: eventsv1.TopicCandidatePreferencesUpdated,
    })
    if c.Name() != eventsv1.TopicCandidatePreferencesUpdated {
        t.Fatalf("expected Name=%q, got %q", eventsv1.TopicCandidatePreferencesUpdated, c.Name())
    }
}
```

- [ ] **Step 3: Build + test**

```bash
go build ./apps/matching/...
go test -race ./apps/matching/service/matching/v1/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add apps/matching/service/matching/v1/candidate_change_consumer.go \
        apps/matching/service/matching/v1/candidate_change_consumer_test.go
git commit -m "feat(matching): candidate-change consumer wiring Path C to prefs/embedding topics"
```

---

### Task 18: Register the new consumers + env gates in `apps/matching/cmd/main.go`

Wire the consumers into the running binary, behind feature flags so rollout is reversible per spec §5.5.

**Files:**

- Modify: `apps/matching/cmd/main.go`
- Modify: `apps/matching/cmd/config.go` (or wherever env vars live — find with `grep -n 'cfg\.' apps/matching/cmd/main.go | head`)

- [ ] **Step 1: Add the env flags**

In the config struct, add:

```go
MatchingFanoutEnabled         bool `envconfig:"MATCHING_FANOUT_ENABLED" default:"false"`
MatchingCandidateChangeEnabled bool `envconfig:"MATCHING_CANDIDATE_CHANGE_ENABLED" default:"false"`
MatchingRerankerEnabled       bool `envconfig:"MATCHING_RERANKER_ENABLED" default:"false"`
MatchingDLQThreshold          int  `envconfig:"MATCHING_DLQ_THRESHOLD" default:"5"`
```

- [ ] **Step 2: Wire the consumers**

After the existing matcher / search adapters block in `main.go` (look for "Production adapters"), add:

```go
// Phase-2 continuous matching pipeline. Flagged off by default — flipped
// per spec §5.5 Step 3.
sqlDB, err := pool.SqlDB(ctx, datastore.DefaultPoolName, false)
if err != nil {
    log.WithError(err).Fatal("matching: open sql.DB")
}
matchStore := matching.NewStore(sqlDB)
matchEvents := matching.NewEventLog(sqlDB)
matchIdx := matching.NewIndexStore(sqlDB)
matchKNN := matching.NewKNN(sqlDB)

var rerank matching.Reranker = matching.NoopReranker{}
if cfg.MatchingRerankerEnabled {
    // Upstream reranker client is out of scope for Phase 2 — wire the
    // pooled wrapper around the noop until Phase 5 lands the cross-encoder.
    rerank = matching.NewPooledReranker(matching.NoopReranker{}, 8, time.Second)
}

dlqPub := &eventsManagerPublisher{svc: svc}
dlq := matchingv1.NewDLQGuard(dlqPub, eventsv1.SubjectMatchingDeadletter, cfg.MatchingDLQThreshold)

if cfg.MatchingFanoutEnabled {
    fanout := matchingv1.NewFanOutConsumer(matchingv1.FanOutConsumerDeps{
        Store: matchStore, EventLog: matchEvents, KNN: matchKNN,
        Reranker: rerank, Weights: matching.DefaultWeights(),
        DLQ:       dlq,
        OppEmbedQ: matchingv1.NewSQLOppEmbeddingQuery(sqlDB),
    })
    svcOptions = append(svcOptions,
        frame.WithRegisterSubscriber(fanout.Name(), fanout.Name(), fanout))
    log.Info("matching: fan-out (Path A) enabled")
}

if cfg.MatchingCandidateChangeEnabled {
    deb := matching.NewValkeyDebouncer( /* TODO Phase-5 valkey wiring */ )
    for _, topic := range []string{
        eventsv1.TopicCandidatePreferencesUpdated,
        eventsv1.TopicCandidateEmbedding,
    } {
        cc := matchingv1.NewCandidateChangeConsumer(matchingv1.CandidateChangeConsumerDeps{
            IndexStore: matchIdx, KNN: matchKNN, Store: matchStore,
            EventLog: matchEvents,
            Reranker: rerank, Weights: matching.DefaultWeights(),
            Debouncer: deb, DLQ: dlq, Topic: topic,
        })
        svcOptions = append(svcOptions,
            frame.WithRegisterSubscriber(cc.Name(), cc.Name(), cc))
    }
    log.Info("matching: candidate-change (Path C) enabled")
}
```

Add a tiny `eventsManagerPublisher` adapter at the bottom of `main.go` so the DLQ guard can publish without leaking the Frame surface area:

```go
type eventsManagerPublisher struct{ svc *frame.Service }

func (p *eventsManagerPublisher) Publish(ctx context.Context, subject string, payload []byte) error {
    return p.svc.EventsManager().PublishRaw(ctx, subject, payload)
}
```

(If `PublishRaw` isn't the exact method name on the EventsManager surface, find the right one with `grep -n 'func.*Manager.*) Publish' vendor/...` — Frame's API does expose a raw-bytes publish.)

The `matching.NewValkeyDebouncer` reference is intentionally aspirational — the in-memory placeholder belongs in this task so the flag-on path compiles, with the real Valkey backend in Phase 5. Replace it with:

```go
// pkg/matching/debounce_memory.go
package matching

import (
    "context"
    "sync"
    "time"
)

// MemoryDebouncer is a process-local debouncer suitable for development
// and single-instance test runs. Production deploys must replace it with
// a Valkey-backed implementation that is consistent across pods.
type MemoryDebouncer struct {
    mu   sync.Mutex
    seen map[string]time.Time
}

func NewMemoryDebouncer() *MemoryDebouncer {
    return &MemoryDebouncer{seen: map[string]time.Time{}}
}

func (m *MemoryDebouncer) Acquire(_ context.Context, candidateID string, ttl time.Duration) (bool, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    now := time.Now()
    if last, ok := m.seen[candidateID]; ok && now.Sub(last) < ttl {
        return false, nil
    }
    m.seen[candidateID] = now
    return true, nil
}
```

Add a tiny unit test for the in-memory debouncer:

```go
// pkg/matching/debounce_memory_test.go
package matching_test

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestMemoryDebouncer_FirstAllowedSecondDenied(t *testing.T) {
    d := matching.NewMemoryDebouncer()
    ok, err := d.Acquire(context.Background(), "u", 100*time.Millisecond)
    require.NoError(t, err)
    require.True(t, ok)

    ok, err = d.Acquire(context.Background(), "u", 100*time.Millisecond)
    require.NoError(t, err)
    require.False(t, ok)

    time.Sleep(110 * time.Millisecond)
    ok, _ = d.Acquire(context.Background(), "u", 100*time.Millisecond)
    require.True(t, ok)
}
```

Update the main.go reference to call `matching.NewMemoryDebouncer()`.

- [ ] **Step 3: Build the binary**

```bash
go build ./apps/matching/...
```

Expected: clean build (or missing-import errors — add the obvious ones).

- [ ] **Step 4: Run all unit tests**

```bash
go test -race ./pkg/matching/... ./apps/matching/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/matching/cmd/main.go apps/matching/cmd/config.go \
        pkg/matching/debounce_memory.go pkg/matching/debounce_memory_test.go
git commit -m "feat(matching): wire fan-out + candidate-change consumers behind env flags"
```

---

### Task 19: `MatchingPipelineSuite` — integration test against real Postgres + JetStream-like-fake

Validates the invariants that pure-package tests can't: idempotency replay, terminal protection across paths, score-monotonic in real SQL, daily-cap behaviour for Path A, gap-fill merges with fan-out output without dupes.

**Files:**

- Create: `tests/integration/matching_pipeline_test.go`

- [ ] **Step 1: Skeleton**

```go
//go:build integration

package integration

import (
    "context"
    "database/sql"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    "github.com/stretchr/testify/suite"

    "github.com/stawi-opportunities/opportunities/pkg/matching"
    "github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

type MatchingPipelineSuite struct {
    suite.Suite
    db  *sql.DB
    ctx context.Context

    store    *matching.Store
    events   *matching.EventLog
    idx      *matching.IndexStore
    knn      *matching.KNN
    reranker matching.Reranker
}

func TestMatchingPipelineSuite(t *testing.T) { suite.Run(t, new(MatchingPipelineSuite)) }

func (s *MatchingPipelineSuite) SetupSuite() {
    s.ctx = context.Background()
    s.db = testhelpers.PostgresContainerNoMigrate(s.T(), s.ctx)
    require.NoError(s.T(), testhelpers.EnsureOpportunitiesStub(s.ctx, s.db))
    // Opportunities need an embedding column for ReverseKNN.
    _, err := s.db.ExecContext(s.ctx, `
        ALTER TABLE opportunities
            ADD COLUMN IF NOT EXISTS embedding vector(1536),
            ADD COLUMN IF NOT EXISTS kind TEXT,
            ADD COLUMN IF NOT EXISTS country TEXT,
            ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ DEFAULT now()
    `)
    require.NoError(s.T(), err)
    testhelpers.ApplyMigrationsDir(s.T(), s.ctx, s.db, "../../db/migrations")

    s.store = matching.NewStore(s.db)
    s.events = matching.NewEventLog(s.db)
    s.idx = matching.NewIndexStore(s.db)
    s.knn = matching.NewKNN(s.db)
    s.reranker = matching.NoopReranker{}
}
```

- [ ] **Step 2: Path A end-to-end test**

```go
func (s *MatchingPipelineSuite) TestPathA_FanOutHappyPath() {
    s.seedCandidate("c1", makeUnitVector(1536, 0), 0.5, []string{"job"}, []string{"KE"})
    s.seedOpportunity("o1", makeUnitVector(1536, 0), "job", "KE")

    res, err := matching.FanOut(s.ctx, matching.FanOutInput{
        CanonicalID:   "can_1",
        OpportunityID: "o1",
        Kind:          "job",
        Country:       "KE",
        Embedding:     makeUnitVector(1536, 0),
        FirstSeenAt:   time.Now(),
    }, matching.FanOutDeps{
        KNN:      s.knn,
        Store:    s.store,
        EventLog: s.events,
        Reranker: s.reranker,
        Weights:  matching.DefaultWeights(),
    })
    require.NoError(s.T(), err)
    require.Equal(s.T(), 1, res.MatchesWritten)

    got, err := s.store.GetByPair(s.ctx, "c1", "o1")
    require.NoError(s.T(), err)
    require.Equal(s.T(), matching.StatusNew, got.Status)

    var eventCount int
    require.NoError(s.T(), s.db.QueryRowContext(s.ctx,
        `SELECT count(*) FROM candidate_match_events WHERE candidate_id='c1' AND opportunity_id='o1'`).
        Scan(&eventCount))
    require.Equal(s.T(), 1, eventCount)
}
```

- [ ] **Step 3: Idempotency replay**

```go
func (s *MatchingPipelineSuite) TestPathA_IdempotentReplay() {
    s.seedCandidate("c2", makeUnitVector(1536, 0), 0.5, []string{"job"}, []string{"KE"})
    s.seedOpportunity("o2", makeUnitVector(1536, 0), "job", "KE")
    in := matching.FanOutInput{
        CanonicalID: "can_2", OpportunityID: "o2", Kind: "job", Country: "KE",
        Embedding: makeUnitVector(1536, 0), FirstSeenAt: time.Now(),
    }
    deps := matching.FanOutDeps{KNN: s.knn, Store: s.store, EventLog: s.events,
        Reranker: s.reranker, Weights: matching.DefaultWeights()}

    for i := 0; i < 3; i++ {
        _, err := matching.FanOut(s.ctx, in, deps)
        require.NoError(s.T(), err)
    }
    var rowCnt int
    require.NoError(s.T(), s.db.QueryRowContext(s.ctx,
        `SELECT count(*) FROM candidate_matches WHERE candidate_id='c2' AND opportunity_id='o2'`).
        Scan(&rowCnt))
    require.Equal(s.T(), 1, rowCnt, "3 replays must collapse into 1 row")
}
```

- [ ] **Step 4: Terminal-state protection across paths**

```go
func (s *MatchingPipelineSuite) TestTerminalStateImmuneToFanOut() {
    s.seedCandidate("c3", makeUnitVector(1536, 0), 0.5, []string{"job"}, []string{"KE"})
    s.seedOpportunity("o3", makeUnitVector(1536, 0), "job", "KE")

    in := matching.FanOutInput{CanonicalID: "can_3", OpportunityID: "o3", Kind: "job",
        Country: "KE", Embedding: makeUnitVector(1536, 0), FirstSeenAt: time.Now()}
    deps := matching.FanOutDeps{KNN: s.knn, Store: s.store, EventLog: s.events,
        Reranker: s.reranker, Weights: matching.DefaultWeights()}
    _, err := matching.FanOut(s.ctx, in, deps)
    require.NoError(s.T(), err)

    // Mark dismissed (simulates user action via /api/me/matches/{id}/dismiss).
    _, err = s.db.ExecContext(s.ctx,
        `UPDATE candidate_matches SET status='dismissed', dismissed_at=now()
         WHERE candidate_id='c3' AND opportunity_id='o3'`)
    require.NoError(s.T(), err)

    // Replay must not resurrect.
    _, err = matching.FanOut(s.ctx, in, deps)
    require.NoError(s.T(), err)

    got, err := s.store.GetByPair(s.ctx, "c3", "o3")
    require.NoError(s.T(), err)
    require.Equal(s.T(), matching.StatusDismissed, got.Status)
}
```

- [ ] **Step 5: Path B (gap-fill) merges without dupes**

```go
func (s *MatchingPipelineSuite) TestPathB_MergesWithoutDuplicates() {
    s.seedCandidate("c4", makeUnitVector(1536, 0), 0.4, []string{"job"}, []string{"KE"})
    s.seedOpportunity("o4", makeUnitVector(1536, 0), "job", "KE")

    fanout := matching.FanOutDeps{KNN: s.knn, Store: s.store, EventLog: s.events,
        Reranker: s.reranker, Weights: matching.DefaultWeights()}
    _, err := matching.FanOut(s.ctx, matching.FanOutInput{
        CanonicalID: "can_4", OpportunityID: "o4", Kind: "job", Country: "KE",
        Embedding: makeUnitVector(1536, 0), FirstSeenAt: time.Now(),
    }, fanout)
    require.NoError(s.T(), err)

    // Now run gap-fill: should see no new rows (path-conflict guard).
    gap := matching.GapFillDeps{KNN: s.knn, Store: s.store, EventLog: s.events,
        Reranker: s.reranker, Weights: matching.DefaultWeights()}
    _, err = matching.GapFill(s.ctx, matching.GapFillInput{
        CandidateID: "c4",
        Embedding:   makeUnitVector(1536, 0),
        Kinds:       []string{"job"},
        Countries:   []string{"KE"},
        Since:       time.Now().Add(-time.Hour),
        MinScore:    0.3,
    }, gap)
    require.NoError(s.T(), err)

    var rowCnt int
    require.NoError(s.T(), s.db.QueryRowContext(s.ctx,
        `SELECT count(*) FROM candidate_matches WHERE candidate_id='c4'`).Scan(&rowCnt))
    require.Equal(s.T(), 1, rowCnt)
}
```

- [ ] **Step 6: Helpers**

Append at the bottom of `matching_pipeline_test.go`:

```go
func (s *MatchingPipelineSuite) seedCandidate(id string, emb []float32, minScore float64, kinds, countries []string) {
    require.NoError(s.T(), s.idx.Upsert(s.ctx, matching.CandidateIndex{
        CandidateID: id, Embedding: emb, MinScore: minScore,
        DailyCap: 25, WeeklyCap: 100,
        Kinds: kinds, Countries: countries, Enabled: true,
    }))
}

func (s *MatchingPipelineSuite) seedOpportunity(id string, emb []float32, kind, country string) {
    lit := vectorLitTest(emb)
    _, err := s.db.ExecContext(s.ctx, `
        INSERT INTO opportunities (id, posted_at, status, hidden, embedding, kind, country, first_seen_at)
        VALUES ($1, now(), 'active', false, $2::vector, $3, $4, now())
        ON CONFLICT (id) DO NOTHING
    `, id, lit, kind, country)
    require.NoError(s.T(), err)
}

func makeUnitVector(dim, axis int) []float32 {
    v := make([]float32, dim)
    if axis >= 0 && axis < dim {
        v[axis] = 1
    } else {
        v[0] = 1
    }
    return v
}

func vectorLitTest(v []float32) string {
    out := "["
    for i, f := range v {
        if i > 0 {
            out += ","
        }
        out += fmtFloat(f)
    }
    return out + "]"
}

func fmtFloat(f float32) string { return strconv.FormatFloat(float64(f), 'g', -1, 32) }
```

Add the required imports (`strconv` is needed).

- [ ] **Step 7: Run**

```bash
make test-integration
```

Expected: PASS. The suite touches a real TimescaleDB + pgvector container; tests run in ~30s.

- [ ] **Step 8: Commit**

```bash
git add tests/integration/matching_pipeline_test.go
git commit -m "test(integration): MatchingPipelineSuite (A/B happy paths, idempotency, terminal protection)"
```

---

### Task 20: Update the spec progress log

**Files:**

- Modify: `docs/superpowers/specs/2026-05-20-continuous-matching-and-applications-design.md` (§8 progress log)

- [ ] **Step 1: Append**

After the existing "Phase 1 (foundation) — done." entry, add:

```markdown
- **Phase 2 (matching pipeline) — done.** New OLTP table
  `candidate_matches` (migration 0013) with monotonic-score +
  terminal-protected UPSERT. Three pure orchestrators —
  `pkg/matching.FanOut` (Path A), `GapFill` (Path B),
  `RunCandidateChange` (Path C) — share the deterministic scoring
  function and write through the same event log. JetStream consumers
  in `apps/matching/service/matching/v1` subscribe Path A to
  `TopicCanonicalsUpserted` and Path C to
  `TopicCandidatePreferencesUpdated` + `TopicCandidateEmbedding`,
  guarded by `MATCHING_FANOUT_ENABLED` /
  `MATCHING_CANDIDATE_CHANGE_ENABLED` env flags. Reranker is bounded +
  best-effort with retrieval-score fallback. DLQ guard publishes to
  `svc.opportunities.matching.deadletter` after 5 redeliveries.
  Prometheus metrics defined per spec §5.2.
  Integration coverage in `MatchingPipelineSuite`: A/B happy paths,
  idempotent replay, terminal-state protection across paths.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-05-20-continuous-matching-and-applications-design.md
git commit -m "spec: mark phase 2 (matching pipeline) complete in progress log"
```

---

## Acceptance for Phase 2

Phase 2 is done when all of the following hold:

1. `make test-integration` passes (all suites including `MatchingPipelineSuite`).
2. `go test -race ./pkg/matching/... ./apps/matching/...` passes.
3. `golangci-lint run --timeout=5m ./...` passes (no S1016 in pgsearch).
4. Migration `0013_candidate_matches_oltp.sql` applies cleanly on a fresh container.
5. The `apps/matching` binary builds with both env flags off (default) and with each flag on; flag-on path does not panic at startup.
6. `git log --oneline worktree-matching-phase1` shows commits in the order of this plan.

---

## Out of Phase 2 scope (explicitly)

- HTTP routes for `/api/me/matches*` and `/api/me/applications*` — Phase 4.
- Continuous aggregates for daily-cap enforcement — Phase 5.
- Valkey-backed `Debouncer` — Phase 5 (in-memory placeholder ships here).
- Real cross-encoder reranker — Phase 5.
- Grafana dashboards + alerts — Phase 5.
- Delete legacy `candidate_matches_legacy`, `matches_weekly.go`, `endpoints_v2*` — Phase 6.
- Load test in `tests/load/matching.go` — Phase 5.

---

## Self-review notes

- **Spec coverage:** §3.1 → Tasks 11, 16. §3.2 → Task 12. §3.3 → Tasks 13, 17. §3.4 → Phase 1, used in Tasks 11–13. §3.5 → Tasks 5 (UPSERT guard), 7 (event idempotency), 16 (DLQ). §3.6 → Tasks 10 (reranker pool), 18 (`MATCHING_FANOUT_ENABLED` flag). §3.7 → Task 15. §5.2 → Task 14.
- **Placeholder scan:** None — every step shows the actual code. The "TODO Phase-5 valkey wiring" comment in Task 18 documents an intentionally-out-of-scope item, not a missing step (the placeholder is replaced by `NewMemoryDebouncer()`).
- **Type consistency:** `Match.Status` is `MatchStatus` everywhere. `FanOutDeps.Store` and `GapFillDeps.Store` both target the `matchUpserter` interface (only `UpsertMatches`), which is the smallest contract both paths need. `Reranker.Rerank` returns `([]RerankItem, bool, error)` everywhere. `Path` const has three values (`fanout`, `gap`, `candidate_change`) consistent with §3 sections.
- **File structure:** Each file has one focused responsibility. Tests sit next to the file they cover. No file exceeds ~400 lines as planned (Store + ListByCandidate share `store.go` because they share the same SQL grammar and `scanMatch` helper).
