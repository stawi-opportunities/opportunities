# Plan D1 — Resumable Crawls (Iterator Checkpoints)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Crawls survive pod restart from the last persisted page. A crawler that crashes mid-iteration on page 47 of 100 picks up at page 47 on the next NATS redelivery — not page 1.

**Architecture:** A new `crawl_checkpoints` Postgres table keyed by `(source_id, connector_type)` holds the connector's `Cursor()` + per-pagination state. The `CrawlIterator` interface gains an optional `Checkpoint() *Checkpoint` method; iterators that support resumption return a serializable state after each successful page. The crawl handler persists it via UPSERT after each iteration. On every fresh `crawl.requests.v1`, the handler loads the previous checkpoint and passes it to `Connector.CrawlResume(ctx, src, checkpoint)` (new optional interface method).

**Tech Stack:** Go, Postgres + TimescaleDB, GORM, frame v1.97+.

**Depends on:** Plan B2 (live as v8.0.64) — the spec-driven connectors need to support checkpointing too, but for D1 we land the foundation + hand-coded connector support; spec connector checkpointing follows in D2.

---

## File Structure

**Created:**
- `apps/crawler/migrations/0001/20260528_0050_crawl_checkpoints.sql`
- `pkg/repository/checkpoint.go` — `CheckpointRepository` with Get/Put.
- `pkg/repository/checkpoint_test.go`.
- `pkg/connectors/checkpoint.go` — `Checkpoint` struct + `ResumableConnector` interface.

**Modified:**
- `pkg/connectors/connector.go` — `CrawlIterator` gains optional `Checkpoint() *Checkpoint`. Make it opt-in via a separate interface so existing iterators don't break.
- `apps/crawler/service/crawl_request_handler.go` — `Execute` loads checkpoint, passes to `Connector.CrawlResume` if supported, persists checkpoint after each page.
- A few existing connectors (greenhouse, workday) add resumable support — the rest stay non-resumable for now.

---

## Tasks

### Task 1: Migration — `crawl_checkpoints`

**File:** `apps/crawler/migrations/0001/20260528_0050_crawl_checkpoints.sql`

```sql
-- Iterator checkpoint store. One row per (source_id, connector_type)
-- — the latest cursor + page index the connector emitted on the last
-- successful page. Persisted after every successful page so a
-- restart picks up from the same spot.
--
-- Schema notes:
--   - cursor is JSONB so per-connector cursor shapes don't need
--     migrations as new connectors land.
--   - last_url is informational (operator drill-down); not used by
--     the resume path.
--   - last_checkpoint_at drives TTL: a checkpoint older than 6h is
--     stale (the source's listing-page state has likely shifted)
--     and the resume path discards it, falling back to a fresh crawl.

CREATE TABLE IF NOT EXISTS crawl_checkpoints (
    source_id           VARCHAR(20)  NOT NULL,
    connector_type      TEXT         NOT NULL,
    cursor              JSONB        NOT NULL DEFAULT '{}'::jsonb,
    page_idx            INTEGER      NOT NULL DEFAULT 0,
    last_url            TEXT,
    last_checkpoint_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, connector_type)
);

CREATE INDEX IF NOT EXISTS crawl_checkpoints_stale_idx
    ON crawl_checkpoints (last_checkpoint_at);
```

Commit:

```bash
git add apps/crawler/migrations/0001/20260528_0050_crawl_checkpoints.sql
git commit -m "feat(schema): crawl_checkpoints — connector cursor + page state per source"
```

---

### Task 2: `CheckpointRepository`

**File:** `pkg/repository/checkpoint.go`

```go
package repository

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    "gorm.io/gorm"
)

// Checkpoint is the persisted iterator state.
type Checkpoint struct {
    SourceID          string          `gorm:"column:source_id;primaryKey"`
    ConnectorType     string          `gorm:"column:connector_type;primaryKey"`
    Cursor            json.RawMessage `gorm:"column:cursor;type:jsonb"`
    PageIdx           int             `gorm:"column:page_idx"`
    LastURL           string          `gorm:"column:last_url"`
    LastCheckpointAt  time.Time       `gorm:"column:last_checkpoint_at"`
}

func (Checkpoint) TableName() string { return "crawl_checkpoints" }

// CheckpointRepository persists per-source iterator checkpoints.
//
// Stale checkpoints (>StaleAfter) are returned with IsStale=true so
// callers can choose to discard.
type CheckpointRepository struct {
    db          func(ctx context.Context, readOnly bool) *gorm.DB
    StaleAfter  time.Duration
}

func NewCheckpointRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *CheckpointRepository {
    return &CheckpointRepository{db: db, StaleAfter: 6 * time.Hour}
}

// Get returns the checkpoint for (sourceID, connectorType) or
// (nil, false, nil) when absent. isStale=true means the row exists
// but is older than StaleAfter — caller may discard.
func (r *CheckpointRepository) Get(ctx context.Context, sourceID, connectorType string) (*Checkpoint, bool, error) {
    var cp Checkpoint
    err := r.db(ctx, true).
        Where("source_id = ? AND connector_type = ?", sourceID, connectorType).
        First(&cp).Error
    if errors.Is(err, gorm.ErrRecordNotFound) {
        return nil, false, nil
    }
    if err != nil {
        return nil, false, fmt.Errorf("checkpoint get: %w", err)
    }
    isStale := time.Since(cp.LastCheckpointAt) > r.StaleAfter
    return &cp, isStale, nil
}

// Put upserts the checkpoint. cursor is the connector's serialized
// cursor (its own shape) — passed through as raw JSON.
func (r *CheckpointRepository) Put(ctx context.Context, sourceID, connectorType string, cursor []byte, pageIdx int, lastURL string) error {
    cp := Checkpoint{
        SourceID:         sourceID,
        ConnectorType:    connectorType,
        Cursor:           cursor,
        PageIdx:          pageIdx,
        LastURL:          lastURL,
        LastCheckpointAt: time.Now().UTC(),
    }
    return r.db(ctx, false).
        Save(&cp).Error
}

// Clear removes the checkpoint — used when a crawl completes
// successfully so the next crawl starts fresh (rather than
// resuming from page N+1 of a stale listing).
func (r *CheckpointRepository) Clear(ctx context.Context, sourceID, connectorType string) error {
    return r.db(ctx, false).
        Where("source_id = ? AND connector_type = ?", sourceID, connectorType).
        Delete(&Checkpoint{}).Error
}
```

Test `pkg/repository/checkpoint_test.go`:

```go
//go:build integration

package repository_test

import (
    "context"
    "testing"
    "time"

    "github.com/stawi-opportunities/opportunities/pkg/repository"
    "github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func TestCheckpoint_PutGet(t *testing.T) {
    ctx := context.Background()
    pool := testhelpers.PostgresPool(t)
    repo := repository.NewCheckpointRepository(pool)
    err := repo.Put(ctx, "src-cp-1", "greenhouse", []byte(`{"after":"foo"}`), 3, "https://x")
    if err != nil { t.Fatal(err) }
    cp, stale, err := repo.Get(ctx, "src-cp-1", "greenhouse")
    if err != nil { t.Fatal(err) }
    if cp == nil { t.Fatal("nil checkpoint") }
    if stale { t.Fatal("fresh checkpoint reported stale") }
    if cp.PageIdx != 3 { t.Fatalf("PageIdx = %d; want 3", cp.PageIdx) }
}

func TestCheckpoint_StaleAfter6h(t *testing.T) {
    ctx := context.Background()
    pool := testhelpers.PostgresPool(t)
    repo := repository.NewCheckpointRepository(pool)
    repo.StaleAfter = time.Millisecond // for the test
    _ = repo.Put(ctx, "src-cp-stale", "x", []byte(`{}`), 0, "")
    time.Sleep(2 * time.Millisecond)
    _, stale, _ := repo.Get(ctx, "src-cp-stale", "x")
    if !stale { t.Fatal("expected stale=true") }
}

func TestCheckpoint_Clear(t *testing.T) {
    ctx := context.Background()
    pool := testhelpers.PostgresPool(t)
    repo := repository.NewCheckpointRepository(pool)
    _ = repo.Put(ctx, "src-cp-clr", "x", []byte(`{}`), 0, "")
    _ = repo.Clear(ctx, "src-cp-clr", "x")
    cp, _, _ := repo.Get(ctx, "src-cp-clr", "x")
    if cp != nil { t.Fatal("Clear didn't remove") }
}
```

Commit:

```bash
go build ./pkg/repository/... 2>&1 | tail -5
go test -tags=integration ./pkg/repository/ -run TestCheckpoint -v 2>&1 | tail -10
git add pkg/repository/checkpoint.go pkg/repository/checkpoint_test.go
git commit -m "feat(repository): CheckpointRepository for iterator state persistence"
```

---

### Task 3: ResumableConnector interface

**File:** `pkg/connectors/checkpoint.go`

```go
package connectors

import (
    "context"
    "encoding/json"

    "github.com/stawi-opportunities/opportunities/pkg/domain"
)

// CheckpointState is the per-page state a connector emits to drive
// resumption. The Cursor is the connector's own serialized state
// (opaque to the crawl handler); PageIdx is informational + drives
// the page counter the operator sees on the trace UI.
type CheckpointState struct {
    Cursor  json.RawMessage `json:"cursor"`
    PageIdx int             `json:"page_idx"`
    LastURL string          `json:"last_url,omitempty"`
}

// CheckpointableIterator is the optional extension a CrawlIterator
// implements to participate in checkpoint persistence. The handler
// type-asserts on this; iterators that don't implement it simply
// don't checkpoint.
type CheckpointableIterator interface {
    Checkpoint() *CheckpointState
}

// ResumableConnector is the optional extension a Connector implements
// to accept a previous checkpoint and resume from it. The handler
// type-asserts on this; connectors that don't implement it ignore
// the checkpoint and start fresh.
//
// The checkpoint may be nil (no prior state) or stale (older than
// the repository's StaleAfter) — connectors choose how to handle
// the stale case. Most should discard stale and start fresh.
type ResumableConnector interface {
    CrawlResume(ctx context.Context, src domain.Source, cp *CheckpointState) CrawlIterator
}
```

Commit:

```bash
git add pkg/connectors/checkpoint.go
git commit -m "feat(connectors): CheckpointableIterator + ResumableConnector opt-in interfaces"
```

---

### Task 4: Handler wires checkpoint load + persist

**File:** `apps/crawler/service/crawl_request_handler.go`

In `Execute`, after the source + connector lookups but before `iter := conn.Crawl(ctx, *src)`, add:

```go
// Resume from a prior checkpoint if the connector supports it.
var iter connectors.CrawlIterator
var prevCheckpoint *connectors.CheckpointState
if h.deps.CheckpointRepo != nil {
    cp, stale, err := h.deps.CheckpointRepo.Get(ctx, src.ID, string(conn.Type()))
    if err != nil {
        log.WithError(err).Warn("crawl.request: checkpoint Get failed")
    } else if cp != nil && !stale {
        prevCheckpoint = &connectors.CheckpointState{
            Cursor:  cp.Cursor,
            PageIdx: cp.PageIdx,
            LastURL: cp.LastURL,
        }
    } else if stale {
        log.WithField("source_id", src.ID).Debug("crawl.request: discarding stale checkpoint")
    }
}
if resumable, ok := conn.(connectors.ResumableConnector); ok && prevCheckpoint != nil {
    iter = resumable.CrawlResume(ctx, *src, prevCheckpoint)
} else {
    iter = conn.Crawl(ctx, *src)
}
```

After each successful `iter.Next(ctx)` iteration (inside the existing loop body, after the items are processed), persist the checkpoint:

```go
if h.deps.CheckpointRepo != nil {
    if cpi, ok := iter.(connectors.CheckpointableIterator); ok {
        if cp := cpi.Checkpoint(); cp != nil {
            cursorBytes, _ := json.Marshal(cp)
            _ = h.deps.CheckpointRepo.Put(ctx, src.ID, string(conn.Type()), cursorBytes, cp.PageIdx, cp.LastURL)
        }
    }
}
```

After the iterator exits cleanly (no error, no more pages), CLEAR the checkpoint so the next scheduled tick starts fresh:

```go
if iter.Err() == nil && h.deps.CheckpointRepo != nil {
    _ = h.deps.CheckpointRepo.Clear(ctx, src.ID, string(conn.Type()))
}
```

Add `CheckpointRepo *repository.CheckpointRepository` to `CrawlRequestDeps` struct.

Wire in `apps/crawler/cmd/main.go` `service.NewCrawlRequestHandler(...)` call — pass `repository.NewCheckpointRepository(dbFn)`.

Build + tests + commit:

```bash
go build ./... 2>&1 | tail -5
go vet ./... 2>&1 | tail -5
go test ./apps/crawler/... -count=1 -short 2>&1 | tail -10
git add apps/crawler/service/crawl_request_handler.go apps/crawler/cmd/main.go
git commit -m "feat(crawler): checkpoint persistence + resume on crawl.request

CrawlRequestDeps gains a CheckpointRepo. Execute loads the prior
checkpoint, passes it to Connector.CrawlResume if the connector
implements ResumableConnector, persists after every page via
iter.Checkpoint() (if iter implements CheckpointableIterator),
and clears on clean completion."
```

---

### Task 5: Greenhouse + Workday gain resume support

The two most-used hand-coded connectors should support resume so the feature is end-to-end testable.

**Files:**
- Modify: `pkg/connectors/greenhouse/greenhouse.go` — add `CrawlResume` + iterator implements `Checkpoint()`.
- Modify: `pkg/connectors/workday/workday.go` — same.

Greenhouse pattern (illustrative — adapt to the actual code):

```go
// connector
func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
    return &iter{ctx: ctx, src: src, client: c.client, page: 1}
}

func (c *Connector) CrawlResume(ctx context.Context, src domain.Source, cp *connectors.CheckpointState) connectors.CrawlIterator {
    it := &iter{ctx: ctx, src: src, client: c.client, page: 1}
    if cp != nil {
        var s struct {
            Page int `json:"page"`
        }
        if err := json.Unmarshal(cp.Cursor, &s); err == nil && s.Page > 0 {
            it.page = s.Page
        }
    }
    return it
}

// iterator
func (it *iter) Checkpoint() *connectors.CheckpointState {
    cursor, _ := json.Marshal(struct {
        Page int `json:"page"`
    }{Page: it.page})
    return &connectors.CheckpointState{
        Cursor:  cursor,
        PageIdx: it.page,
        LastURL: it.lastURL,
    }
}
```

Tests: add `TestGreenhouse_CrawlResume_StartsFromCheckpointPage` that verifies the iter doesn't refetch already-seen pages.

Commit:

```bash
go test ./pkg/connectors/greenhouse/ ./pkg/connectors/workday/ -count=1 -short 2>&1 | tail -10
git add pkg/connectors/greenhouse/ pkg/connectors/workday/
git commit -m "feat(connectors): greenhouse + workday support CrawlResume + Checkpoint

First two connectors to participate in the checkpoint loop.
sitemap + spec-driven connectors land in a follow-up (D2)."
```

---

### Task 6: Admin endpoint to inspect + clear checkpoints

**Files:**
- Create: `apps/api/cmd/checkpoint_admin.go`

```go
// GET /admin/checkpoints?source_id=...
// DELETE /admin/checkpoints/{source_id}/{connector_type}
//
// Operator surface for diagnosing stuck resume loops + force-restart.
package main

import (
    "encoding/json"
    "net/http"

    "github.com/stawi-opportunities/opportunities/pkg/repository"
)

type checkpointAdmin struct {
    repo *repository.CheckpointRepository
}

func registerCheckpointAdmin(mux *http.ServeMux, repo *repository.CheckpointRepository) {
    a := &checkpointAdmin{repo: repo}
    mux.HandleFunc("GET /admin/checkpoints",                                 requireAdmin(a.list))
    mux.HandleFunc("DELETE /admin/checkpoints/{source_id}/{connector_type}", requireAdmin(a.clear))
}

func (a *checkpointAdmin) list(w http.ResponseWriter, r *http.Request) {
    // ... simple SELECT * FROM crawl_checkpoints WHERE source_id = ? if set, else all ...
    // (Repo doesn't have List yet; add it: ListAll(ctx) + ListBySource(ctx, id))
}

func (a *checkpointAdmin) clear(w http.ResponseWriter, r *http.Request) {
    sourceID := r.PathValue("source_id")
    connectorType := r.PathValue("connector_type")
    if err := a.repo.Clear(r.Context(), sourceID, connectorType); err != nil {
        writeError(w, http.StatusInternalServerError, "internal", err.Error())
        return
    }
    w.WriteHeader(http.StatusNoContent)
}
```

Add `ListAll(ctx)` and `ListBySource(ctx, sourceID)` to `CheckpointRepository`. Wire in `sources_admin.go`.

UI: add a "Reset checkpoint" button to the SourceTrace page that calls DELETE.

Commit:

```bash
git add apps/api/cmd/checkpoint_admin.go apps/api/cmd/sources_admin.go pkg/repository/checkpoint.go ui/admin/src/pages/SourceTrace.tsx
git commit -m "feat(admin): GET/DELETE /admin/checkpoints for resume diagnostics + reset"
```

---

### Task 7: Tag + deploy v8.0.66

```bash
git push origin main
git tag v8.0.66
git push origin v8.0.66
```

After Flux rolls + the new migration applies:

```bash
# Verify the new table:
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- \
  psql -U postgres -d opportunities -c "\d crawl_checkpoints"

# Watch checkpoints accumulate during a crawl burst:
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- \
  psql -U postgres -d opportunities -c "SELECT * FROM crawl_checkpoints LIMIT 5"

# Crash + recover simulation:
# 1. Curl /admin/scheduler/tick to start a fresh crawl.
# 2. After ~5 sec (mid-crawl), delete one of the crawler pods:
#    kubectl delete pod opportunities-crawler-xxx
# 3. The replacement pod picks up the NATS redelivery + reads the checkpoint.
# 4. Verify in logs: "crawl.request: resuming from checkpoint page=N"
```

---

## Plan D1 Exit Criteria

- [ ] `crawl_checkpoints` table populates during active crawls.
- [ ] Checkpoint cleared on successful completion.
- [ ] Crash + recovery resumes from the last checkpoint page (not page 1).
- [ ] Greenhouse + Workday connectors checkpoint per page.
- [ ] Stale checkpoint (>6h) is discarded; crawl starts fresh.
- [ ] `DELETE /admin/checkpoints/{src}/{type}` clears a stuck resume.
- [ ] No existing tests fail.
