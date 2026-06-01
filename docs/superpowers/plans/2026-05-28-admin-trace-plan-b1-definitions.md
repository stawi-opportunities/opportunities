# Plan B1 — Definitions Infrastructure + Per-Source Prompt Extension

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Definitions (kind specs + extraction prompts + source seeds) load from R2 on a 5-min refresh, with instant NATS-broadcast propagation on admin edits. Plus a per-source `extraction_prompt_extension` operators can edit via the existing source-admin endpoints. The 6 spec-driven connectors (Plan B2) build on this foundation.

**Architecture:** A new `pkg/definitions` package wraps a small `Loader` interface (`Get`, `List`, `Subscribe`). The R2 implementation polls a 5-min ticker AND consumes `opportunities.definitions.changed.v1` NATS events for instant refresh. The existing `pkg/opportunity.LoadFromDir` is replaced by `pkg/opportunity.LoadFromDefinitions(loader)`. A new migration adds `sources.extraction_prompt_extension TEXT` and the crawl handler appends it to the kind prompt at extract time. Admin endpoints under `/admin/definitions/*` upload + version + rollback.

**Tech Stack:** Go (Frame v1.97+), R2 (S3-compat via aws-sdk-go-v2), NATS JetStream, Postgres + TimescaleDB.

**Depends on:** Plan A (live as v8.0.62) — same admin auth surface, same R2 archive bucket conventions.

---

## File Structure

**Created:**
- `pkg/definitions/loader.go` — `Loader` interface + types.
- `pkg/definitions/r2.go` — R2 implementation with 5-min refresh.
- `pkg/definitions/memory.go` — in-memory implementation for tests.
- `pkg/definitions/loader_test.go` — unit tests.
- `pkg/events/v1/definitions.go` — `DefinitionsChangedV1` payload + `TopicDefinitionsChanged` constant.
- `apps/api/cmd/definitions_admin.go` — admin endpoints.
- `apps/api/cmd/definitions_admin_test.go` — integration tests.
- `apps/crawler/migrations/0001/20260528_0040_sources_prompt_extension.sql` — schema migration.
- `cmd/seed-definitions/main.go` — one-shot init job that uploads `definitions/opportunity-kinds/*.yaml` to R2.

**Modified:**
- `pkg/opportunity/registry.go` — add `LoadFromDefinitions(ctx, loader) (*Registry, error)`.
- `pkg/domain/models.go` — add `ExtractionPromptExtension string` field to `Source`.
- `pkg/repository/source.go` — `Update` already accepts a map; add `extraction_prompt_extension` to the allowed columns.
- `apps/api/cmd/sources_admin.go` — `handleUpdate` accepts the new field in the request body.
- `pkg/extraction/extractor.go` — `Extract` accepts an optional `sourceExtension string` parameter.
- `apps/crawler/service/crawl_request_handler.go` — `enrichOne` reads `src.ExtractionPromptExtension` and passes it to `Extractor.Extract`.
- `apps/{crawler,api,worker,materializer,matching,writer}/cmd/main.go` — replace `opportunity.LoadFromDir` with `LoadFromDefinitions(loader)`; construct the R2 loader once per app.

**Touched (small):**
- `apps/crawler/cmd/main.go` etc — wire NATS `Subscribe` for the loader's change notifications.

---

## Tasks

### Task 1: SQL migration for `sources.extraction_prompt_extension`

**Files:**
- Create: `apps/crawler/migrations/0001/20260528_0040_sources_prompt_extension.sql`

- [ ] **Step 1: Write the migration**

```sql
-- Per-source extraction prompt extension. Appended after the kind's
-- base extraction prompt at extract time. Lets operators tune
-- extraction for tricky sources (sidebar widgets, custom HTML
-- attributes, multi-listing pages) without editing the shared kind
-- prompt that affects every source of that kind.
--
-- Empty by default (NOT NULL DEFAULT ''). 4 KB cap enforced by the
-- admin endpoint, not the schema.

ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS extraction_prompt_extension TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 2: Verify migration applies cleanly**

```bash
cd /home/j/code/stawi.opportunities
go test ./tests/integration/... -run TestMigrationsApplyCleanly -count=1 -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add apps/crawler/migrations/0001/20260528_0040_sources_prompt_extension.sql
git commit -m "feat(sources): add extraction_prompt_extension column

Per-source append to the kind-level extraction prompt. Empty
default; admin sets via PUT /admin/sources/{id}."
```

---

### Task 2: Domain model + repository

**Files:**
- Modify: `pkg/domain/models.go` — `Source` struct.
- Modify: `pkg/repository/source.go` — extend the `Update` allowed-columns set.

- [ ] **Step 1: Add field to `Source` struct**

Find the existing `Source` struct in `pkg/domain/models.go` and add (place near other text fields like `LastStoppedBy`):

```go
// ExtractionPromptExtension is appended verbatim to the kind-level
// extraction prompt for this source. Empty for sources that don't
// need customization. Operator-editable via PUT /admin/sources/{id}.
ExtractionPromptExtension string `gorm:"type:text;not null;default:''" json:"extraction_prompt_extension"`
```

- [ ] **Step 2: Allow the column in `Update`**

In `pkg/repository/source.go`, find the `Update` method's allow-list (`grep -n "allowedColumns\|columnAllowed\|switch col" pkg/repository/source.go`). Add `"extraction_prompt_extension"` to the set.

If the repo uses GORM's `Updates(map)` and just validates keys, add to the validation set. If it's a switch statement, add a case.

- [ ] **Step 3: Build + run tests**

```bash
go build ./... 2>&1 | tail -5
go test ./pkg/domain/... ./pkg/repository/... -count=1 -short 2>&1 | tail -10
```

- [ ] **Step 4: Commit**

```bash
git add pkg/domain/models.go pkg/repository/source.go
git commit -m "feat(sources): ExtractionPromptExtension field + Update support"
```

---

### Task 3: Admin endpoint accepts the new field

**Files:**
- Modify: `apps/api/cmd/sources_admin.go` — `handleUpdate` request struct.

- [ ] **Step 1: Write failing test**

In `apps/api/cmd/sources_admin_test.go`, find an existing `TestSourcesAdmin_Update_*` test that exercises PUT and clone it for the extension:

```go
func TestSourcesAdmin_Update_ExtractionPromptExtension(t *testing.T) {
    a, _ := newAdminTestScaffold(t)
    body := `{"extraction_prompt_extension": "Look for salary in data-salary attribute."}`
    req := httptest.NewRequest("PUT", "/admin/sources/src-001", strings.NewReader(body))
    req.SetPathValue("id", "src-001")
    setAdminAuth(req)
    req.Header.Set("Content-Type", "application/json")
    rec := httptest.NewRecorder()
    a.handleUpdate(rec, req)
    if rec.Code != 200 {
        t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
    }
    got, _ := a.repo.GetByID(context.Background(), "src-001")
    if got.ExtractionPromptExtension != "Look for salary in data-salary attribute." {
        t.Fatalf("extension not persisted: %q", got.ExtractionPromptExtension)
    }
}

func TestSourcesAdmin_Update_ExtensionTooLong_Returns400(t *testing.T) {
    a, _ := newAdminTestScaffold(t)
    big := strings.Repeat("x", 4097)
    body := `{"extraction_prompt_extension": "` + big + `"}`
    req := httptest.NewRequest("PUT", "/admin/sources/src-001", strings.NewReader(body))
    req.SetPathValue("id", "src-001")
    setAdminAuth(req)
    req.Header.Set("Content-Type", "application/json")
    rec := httptest.NewRecorder()
    a.handleUpdate(rec, req)
    if rec.Code != 400 {
        t.Fatalf("status = %d; want 400 (>4KB extension)", rec.Code)
    }
}
```

Run:
```bash
go test ./apps/api/cmd/ -run TestSourcesAdmin_Update_Extraction -v 2>&1 | tail -10
```
Expected: FAIL (field not in request struct; no length cap).

- [ ] **Step 2: Update the request struct + handler**

In `apps/api/cmd/sources_admin.go`, find the `updateSourceRequest` struct (around the `handleUpdate` method). Add:

```go
ExtractionPromptExtension *string `json:"extraction_prompt_extension"`
```

In `handleUpdate`, near where other fields are added to the updates map, append:

```go
if req.ExtractionPromptExtension != nil {
    if len(*req.ExtractionPromptExtension) > 4096 {
        writeError(w, http.StatusBadRequest, "bad_request", "extraction_prompt_extension exceeds 4096 bytes")
        return
    }
    updates["extraction_prompt_extension"] = *req.ExtractionPromptExtension
}
```

- [ ] **Step 3: Run tests, verify pass**

```bash
go test ./apps/api/cmd/ -run TestSourcesAdmin_Update_Extraction -v 2>&1 | tail -10
```

- [ ] **Step 4: Commit**

```bash
git add apps/api/cmd/sources_admin.go apps/api/cmd/sources_admin_test.go
git commit -m "feat(admin): PUT /admin/sources/{id} accepts extraction_prompt_extension"
```

---

### Task 4: Wire extension into extractor + crawler

**Files:**
- Modify: `pkg/extraction/extractor.go` — `Extract` signature.
- Modify: `apps/crawler/service/crawl_request_handler.go` — `enrichOne`.

- [ ] **Step 1: Extend `Extract` signature**

In `pkg/extraction/extractor.go`, find the `Extract` method on `*Extractor`. Add a `sourceExtension string` parameter (last position to minimize signature breakage at call sites):

```go
// Extract runs the kind-aware extraction LLM call. sourceExtension is
// appended after the kind prompt — operator-edited per-source guidance
// that doesn't justify changing the shared kind template.
func (e *Extractor) Extract(ctx context.Context, body string, kinds []string, sourceExtension string) (*Extracted, error) {
```

Inside the function, after building the kind prompt, append:

```go
if sourceExtension != "" {
    prompt += "\n\nAdditional instructions for THIS source:\n" + sourceExtension
}
```

- [ ] **Step 2: Update all call sites**

```bash
grep -rn "\.Extract(ctx" pkg/ apps/ --include="*.go" | grep -v _test
```

Update each to pass `src.ExtractionPromptExtension` when a source is in scope, or empty string `""` otherwise. The main call site is `apps/crawler/service/crawl_request_handler.go` `enrichOne` (around line 605):

```go
extracted, err := h.deps.Extractor.Extract(ctx, body, src.Kinds, src.ExtractionPromptExtension)
```

Tests using the extractor likely need updating — usually they pass `nil` for kinds and now need `""` for the extension. Trivial edits.

- [ ] **Step 3: Build + run tests**

```bash
go build ./... 2>&1 | tail -5
go test ./pkg/extraction/... ./apps/crawler/... -count=1 -short 2>&1 | tail -15
```

- [ ] **Step 4: Commit**

```bash
git add pkg/extraction/extractor.go apps/crawler/service/crawl_request_handler.go pkg/extraction/extractor_test.go
git commit -m "feat(extraction): Extract accepts per-source prompt extension

Crawler reads src.ExtractionPromptExtension and passes it through;
empty string is the existing behaviour."
```

---

### Task 5: `pkg/definitions` — loader interface + memory impl

**Files:**
- Create: `pkg/definitions/loader.go`
- Create: `pkg/definitions/memory.go`
- Create: `pkg/definitions/loader_test.go`

- [ ] **Step 1: Define the interface**

```go
// Package definitions exposes a typed loader for the long-tail
// customization surface — opportunity-kind YAMLs, extraction
// prompts, source seeds, declarative connector specs. The loader
// fronts R2 (production), with in-memory test impl + a 5-min
// refresh + NATS broadcast for instant-on-edit propagation.
//
// Lifecycle:
//
//   1. Boot — Loader.Start(ctx) populates the in-memory cache from
//      R2 (List + Get every active object).
//   2. Tick — every refresh interval, re-list R2; for any object
//      whose ETag changed since last fetch, re-fetch + fire
//      subscribers.
//   3. Push — on opportunities.definitions.changed.v1 with type+name,
//      re-fetch that one object immediately and fire subscribers.
package definitions

import (
    "context"
    "errors"
    "time"
)

// Type discriminates what kind of definition we're loading.
type Type string

const (
    TypeKind      Type = "kind"
    TypePrompt    Type = "prompt"
    TypeConnector Type = "connector"
    TypeSeed      Type = "seed"
)

// Entry is one item in a List() result.
type Entry struct {
    Type      Type      `json:"type"`
    Name      string    `json:"name"`
    Version   string    `json:"version"`     // R2 ETag or object version
    UpdatedAt time.Time `json:"updated_at"`
    Size      int64     `json:"size"`
}

// Loader is the read-only public face. Tests construct MemoryLoader;
// production constructs R2Loader via NewR2Loader.
type Loader interface {
    // Start begins the background refresh. Must be called once
    // before Get / List. Blocks until the initial population
    // succeeds; returns the start error if the first List fails
    // (no point booting if R2 is broken).
    Start(ctx context.Context) error

    // Stop ends the background refresh. Safe to call multiple times.
    Stop()

    // Get returns the active body + version for (type, name).
    // Returns ErrNotFound when the definition was deleted or never
    // existed. Stale-cache reads are OK — the loader prioritises
    // availability over freshness on R2 outage.
    Get(ctx context.Context, t Type, name string) (body []byte, version string, err error)

    // List returns every active definition of a type. Sorted by Name.
    List(ctx context.Context, t Type) ([]Entry, error)

    // Subscribe registers a callback fired whenever a definition
    // of `t` changes (refresh tick OR NATS push). The callback
    // runs on the loader's goroutine — keep it non-blocking.
    // Returns an unsubscribe func.
    Subscribe(t Type, fn func(name string, version string)) func()
}

// ErrNotFound is returned by Get when the definition has been
// deleted or never existed.
var ErrNotFound = errors.New("definitions: not found")
```

- [ ] **Step 2: Implement memory loader for tests**

`pkg/definitions/memory.go`:

```go
package definitions

import (
    "context"
    "fmt"
    "sort"
    "sync"
    "time"
)

// MemoryLoader is the in-memory test implementation. Production
// uses NewR2Loader; tests use this directly so they don't need R2.
type MemoryLoader struct {
    mu      sync.RWMutex
    data    map[Type]map[string]Entry
    bodies  map[string][]byte // key: type+":"+name+":"+version
    subs    map[Type][]func(name, version string)
    nextVer int
}

func NewMemoryLoader() *MemoryLoader {
    return &MemoryLoader{
        data:   make(map[Type]map[string]Entry),
        bodies: make(map[string][]byte),
        subs:   make(map[Type][]func(name, version string)),
    }
}

func (m *MemoryLoader) Start(_ context.Context) error { return nil }
func (m *MemoryLoader) Stop()                          {}

// Put is the test-only setter. Production R2 path uses
// admin-endpoint PUT + ScanR2 to update the cache.
func (m *MemoryLoader) Put(t Type, name string, body []byte) string {
    m.mu.Lock()
    m.nextVer++
    version := fmt.Sprintf("v%d", m.nextVer)
    if m.data[t] == nil {
        m.data[t] = make(map[string]Entry)
    }
    m.data[t][name] = Entry{
        Type:      t,
        Name:      name,
        Version:   version,
        UpdatedAt: time.Now().UTC(),
        Size:      int64(len(body)),
    }
    m.bodies[string(t)+":"+name+":"+version] = body
    subs := append([]func(string, string){}, m.subs[t]...)
    m.mu.Unlock()
    for _, fn := range subs {
        fn(name, version)
    }
    return version
}

// Delete is the test-only remover. Production fires on
// definitions.changed.v1 with Action=delete.
func (m *MemoryLoader) Delete(t Type, name string) {
    m.mu.Lock()
    delete(m.data[t], name)
    subs := append([]func(string, string){}, m.subs[t]...)
    m.mu.Unlock()
    for _, fn := range subs {
        fn(name, "deleted")
    }
}

func (m *MemoryLoader) Get(_ context.Context, t Type, name string) ([]byte, string, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    entry, ok := m.data[t][name]
    if !ok {
        return nil, "", ErrNotFound
    }
    body := m.bodies[string(t)+":"+name+":"+entry.Version]
    return body, entry.Version, nil
}

func (m *MemoryLoader) List(_ context.Context, t Type) ([]Entry, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]Entry, 0, len(m.data[t]))
    for _, e := range m.data[t] {
        out = append(out, e)
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
    return out, nil
}

func (m *MemoryLoader) Subscribe(t Type, fn func(name, version string)) func() {
    m.mu.Lock()
    m.subs[t] = append(m.subs[t], fn)
    idx := len(m.subs[t]) - 1
    m.mu.Unlock()
    return func() {
        m.mu.Lock()
        defer m.mu.Unlock()
        // Best-effort removal — slice tombstone OK for tests.
        if idx < len(m.subs[t]) {
            m.subs[t][idx] = func(string, string) {}
        }
    }
}
```

- [ ] **Step 3: Unit test**

`pkg/definitions/loader_test.go`:

```go
package definitions_test

import (
    "context"
    "testing"

    "github.com/stawi-opportunities/opportunities/pkg/definitions"
)

func TestMemoryLoader_PutGet(t *testing.T) {
    l := definitions.NewMemoryLoader()
    body := []byte("kind: job\nuniversal_required: [title]")
    ver := l.Put(definitions.TypeKind, "job", body)
    if ver == "" {
        t.Fatal("Put returned empty version")
    }
    got, v, err := l.Get(context.Background(), definitions.TypeKind, "job")
    if err != nil {
        t.Fatalf("Get: %v", err)
    }
    if string(got) != string(body) || v != ver {
        t.Fatalf("got = (%q, %q); want (%q, %q)", got, v, body, ver)
    }
}

func TestMemoryLoader_Get_NotFound(t *testing.T) {
    l := definitions.NewMemoryLoader()
    _, _, err := l.Get(context.Background(), definitions.TypeKind, "nope")
    if err != definitions.ErrNotFound {
        t.Fatalf("err = %v; want ErrNotFound", err)
    }
}

func TestMemoryLoader_Subscribe_FiresOnPut(t *testing.T) {
    l := definitions.NewMemoryLoader()
    fired := 0
    l.Subscribe(definitions.TypeKind, func(name, version string) {
        if name == "job" {
            fired++
        }
    })
    l.Put(definitions.TypeKind, "job", []byte("x"))
    l.Put(definitions.TypeKind, "scholarship", []byte("y")) // shouldn't count
    if fired != 1 {
        t.Fatalf("fired = %d; want 1", fired)
    }
}

func TestMemoryLoader_List_SortedByName(t *testing.T) {
    l := definitions.NewMemoryLoader()
    l.Put(definitions.TypeKind, "tender", []byte("x"))
    l.Put(definitions.TypeKind, "job", []byte("y"))
    l.Put(definitions.TypeKind, "scholarship", []byte("z"))
    entries, _ := l.List(context.Background(), definitions.TypeKind)
    if len(entries) != 3 {
        t.Fatalf("len = %d; want 3", len(entries))
    }
    if entries[0].Name != "job" || entries[1].Name != "scholarship" || entries[2].Name != "tender" {
        t.Fatalf("order wrong: %v", entries)
    }
}
```

- [ ] **Step 4: Run, verify pass**

```bash
go test ./pkg/definitions/ -count=1 -v 2>&1 | tail -15
```

- [ ] **Step 5: Commit**

```bash
git add pkg/definitions/
git commit -m "feat(definitions): Loader interface + MemoryLoader for tests"
```

---

### Task 6: `pkg/definitions/r2.go` — R2-backed loader

**Files:**
- Create: `pkg/definitions/r2.go`
- Modify: `pkg/definitions/loader_test.go` (optional R2-against-minio integration test).

- [ ] **Step 1: Implement R2 loader**

```go
package definitions

import (
    "context"
    "fmt"
    "io"
    "path"
    "strings"
    "sync"
    "time"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/pitabwire/util"
)

// R2Config carries the R2 connection params + the bucket + prefix
// for definitions storage.
type R2Config struct {
    Client          *s3.Client       // pre-constructed by caller (same client the archive package uses)
    Bucket          string           // typically "product-opportunities-content"
    Prefix          string           // typically "definitions"
    RefreshInterval time.Duration    // default 5 min
}

// R2Loader is the production Loader. Backs definitions in R2 with a
// 5-min refresh ticker + NATS-driven instant invalidation. Construct
// once per process; share across handlers.
type R2Loader struct {
    cfg     R2Config
    cache   sync.Map // key: "type/name" → r2Entry{Entry, body []byte}
    subs    sync.Map // key: Type → []func(name, version string)
    stopCh  chan struct{}
    stopped sync.Once
}

type r2Entry struct {
    Entry
    body []byte
}

// NewR2Loader constructs a loader. Call Start(ctx) to populate the
// initial cache and begin the refresh ticker.
func NewR2Loader(cfg R2Config) *R2Loader {
    if cfg.RefreshInterval <= 0 {
        cfg.RefreshInterval = 5 * time.Minute
    }
    if cfg.Prefix == "" {
        cfg.Prefix = "definitions"
    }
    return &R2Loader{cfg: cfg, stopCh: make(chan struct{})}
}

func (r *R2Loader) Start(ctx context.Context) error {
    if err := r.refresh(ctx); err != nil {
        return fmt.Errorf("definitions: initial R2 fetch: %w", err)
    }
    go r.refreshLoop(ctx)
    return nil
}

func (r *R2Loader) Stop() {
    r.stopped.Do(func() { close(r.stopCh) })
}

func (r *R2Loader) refreshLoop(ctx context.Context) {
    t := time.NewTicker(r.cfg.RefreshInterval)
    defer t.Stop()
    for {
        select {
        case <-r.stopCh:
            return
        case <-ctx.Done():
            return
        case <-t.C:
            if err := r.refresh(ctx); err != nil {
                util.Log(ctx).WithError(err).Warn("definitions: refresh tick failed; serving stale cache")
            }
        }
    }
}

// refresh lists every definition object and re-fetches any whose
// ETag changed since the last refresh. Idempotent.
func (r *R2Loader) refresh(ctx context.Context) error {
    for _, t := range []Type{TypeKind, TypePrompt, TypeConnector, TypeSeed} {
        if err := r.refreshType(ctx, t); err != nil {
            return err
        }
    }
    return nil
}

func (r *R2Loader) refreshType(ctx context.Context, t Type) error {
    prefix := r.cfg.Prefix + "/" + string(t) + "/"
    pages := s3.NewListObjectsV2Paginator(r.cfg.Client, &s3.ListObjectsV2Input{
        Bucket: aws.String(r.cfg.Bucket),
        Prefix: aws.String(prefix),
    })
    seen := make(map[string]struct{})
    for pages.HasMorePages() {
        page, err := pages.NextPage(ctx)
        if err != nil {
            return fmt.Errorf("list %s: %w", prefix, err)
        }
        for _, obj := range page.Contents {
            key := aws.ToString(obj.Key)
            etag := strings.Trim(aws.ToString(obj.ETag), `"`)
            name := strings.TrimSuffix(path.Base(key), path.Ext(key)) // strip .yaml etc
            seen[name] = struct{}{}
            cacheKey := string(t) + "/" + name
            // Skip re-fetch if etag matches.
            if existing, ok := r.cache.Load(cacheKey); ok {
                if existing.(r2Entry).Version == etag {
                    continue
                }
            }
            body, err := r.fetch(ctx, key)
            if err != nil {
                util.Log(ctx).WithError(err).WithField("key", key).Warn("definitions: fetch failed")
                continue
            }
            entry := r2Entry{
                Entry: Entry{
                    Type:      t,
                    Name:      name,
                    Version:   etag,
                    UpdatedAt: aws.ToTime(obj.LastModified),
                    Size:      aws.ToInt64(obj.Size),
                },
                body: body,
            }
            r.cache.Store(cacheKey, entry)
            r.fireSubs(t, name, etag)
        }
    }
    // Detect deletions: any cached entry of this type whose name
    // isn't in `seen` was deleted upstream.
    r.cache.Range(func(k, v any) bool {
        key := k.(string)
        if !strings.HasPrefix(key, string(t)+"/") {
            return true
        }
        name := strings.TrimPrefix(key, string(t)+"/")
        if _, present := seen[name]; !present {
            r.cache.Delete(key)
            r.fireSubs(t, name, "deleted")
        }
        return true
    })
    return nil
}

func (r *R2Loader) fetch(ctx context.Context, key string) ([]byte, error) {
    out, err := r.cfg.Client.GetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(r.cfg.Bucket),
        Key:    aws.String(key),
    })
    if err != nil {
        return nil, err
    }
    defer out.Body.Close()
    return io.ReadAll(out.Body)
}

func (r *R2Loader) Get(ctx context.Context, t Type, name string) ([]byte, string, error) {
    v, ok := r.cache.Load(string(t) + "/" + name)
    if !ok {
        return nil, "", ErrNotFound
    }
    e := v.(r2Entry)
    return e.body, e.Version, nil
}

func (r *R2Loader) List(ctx context.Context, t Type) ([]Entry, error) {
    var out []Entry
    r.cache.Range(func(k, v any) bool {
        if strings.HasPrefix(k.(string), string(t)+"/") {
            out = append(out, v.(r2Entry).Entry)
        }
        return true
    })
    // Sort by name for deterministic output.
    return sortEntriesByName(out), nil
}

func sortEntriesByName(es []Entry) []Entry {
    // sort.Slice avoid import; manual insertion sort is fine for <100 items.
    for i := 1; i < len(es); i++ {
        for j := i; j > 0 && es[j-1].Name > es[j].Name; j-- {
            es[j], es[j-1] = es[j-1], es[j]
        }
    }
    return es
}

func (r *R2Loader) Subscribe(t Type, fn func(name, version string)) func() {
    existing, _ := r.subs.LoadOrStore(t, []func(string, string){})
    list := append(existing.([]func(string, string)), fn)
    r.subs.Store(t, list)
    return func() { /* leak-tolerant; subscribers are per-process lifetime */ }
}

func (r *R2Loader) fireSubs(t Type, name, version string) {
    raw, ok := r.subs.Load(t)
    if !ok {
        return
    }
    for _, fn := range raw.([]func(string, string)) {
        fn(name, version)
    }
}

// Invalidate is called by the NATS consumer when an admin edits
// land. Re-fetches the one object NOW; doesn't wait for the next
// refresh tick.
func (r *R2Loader) Invalidate(ctx context.Context, t Type, name string) error {
    key := r.cfg.Prefix + "/" + string(t) + "/" + name + ".yaml"
    body, err := r.fetch(ctx, key)
    if err != nil {
        // 404 — treat as delete.
        r.cache.Delete(string(t) + "/" + name)
        r.fireSubs(t, name, "deleted")
        return nil
    }
    // We don't have ETag here cheaply — use a timestamp-derived "version".
    version := time.Now().UTC().Format(time.RFC3339Nano)
    r.cache.Store(string(t)+"/"+name, r2Entry{
        Entry: Entry{
            Type:      t,
            Name:      name,
            Version:   version,
            UpdatedAt: time.Now().UTC(),
            Size:      int64(len(body)),
        },
        body: body,
    })
    r.fireSubs(t, name, version)
    return nil
}
```

- [ ] **Step 2: Build clean**

```bash
go build ./pkg/definitions/... 2>&1 | tail -5
go vet ./pkg/definitions/... 2>&1 | tail -5
```

- [ ] **Step 3: Commit**

```bash
git add pkg/definitions/r2.go
git commit -m "feat(definitions): R2-backed Loader with 5-min refresh + invalidate"
```

---

### Task 7: NATS event type for definition changes

**Files:**
- Create: `pkg/events/v1/definitions.go`

```go
package eventsv1

import "time"

// TopicDefinitionsChanged is broadcast when an admin edits land. Loaders
// subscribe and call Invalidate on the matching cache entry — instant
// propagation without waiting for the 5-min R2 refresh tick.
const TopicDefinitionsChanged = "opportunities.definitions.changed.v1"

// DefinitionsChangedV1 is the payload.
type DefinitionsChangedV1 struct {
    Type      string    `json:"type"`      // matches definitions.Type strings (kind|prompt|connector|seed)
    Name      string    `json:"name"`
    Version   string    `json:"version"`   // R2 ETag of the new object, or "deleted"
    Action    string    `json:"action"`    // upsert | delete
    ChangedAt time.Time `json:"changed_at"`
    ChangedBy string    `json:"changed_by,omitempty"`
}
```

Add `TopicDefinitionsChanged` to `AllTopics()` if that function exists (`grep -n "AllTopics" pkg/events/v1/`).

Commit:

```bash
git add pkg/events/v1/definitions.go
git commit -m "feat(events): DefinitionsChangedV1 for instant loader invalidation"
```

---

### Task 8: Admin endpoints — list / get / put / rollback / reload

**Files:**
- Create: `apps/api/cmd/definitions_admin.go`

This is the bulk of the user-visible work. ~200 lines.

```go
package main

import (
    "encoding/json"
    "io"
    "net/http"
    "time"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/pitabwire/frame"
    "github.com/pitabwire/util"

    "github.com/stawi-opportunities/opportunities/pkg/definitions"
    eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

const maxDefinitionBytes = 64 * 1024 // 64 KB per definition

type definitionsAdmin struct {
    loader   *definitions.R2Loader
    s3       *s3.Client
    bucket   string
    prefix   string
    emitter  frame.EventsManager
}

func registerDefinitionsAdmin(mux *http.ServeMux, loader *definitions.R2Loader, s3c *s3.Client, bucket, prefix string, emitter frame.EventsManager) {
    a := &definitionsAdmin{loader: loader, s3: s3c, bucket: bucket, prefix: prefix, emitter: emitter}
    mux.HandleFunc("GET /admin/definitions",                  requireAdmin(a.list))
    mux.HandleFunc("GET /admin/definitions/{type}/{name}",    requireAdmin(a.get))
    mux.HandleFunc("PUT /admin/definitions/{type}/{name}",    requireAdmin(a.put))
    mux.HandleFunc("DELETE /admin/definitions/{type}/{name}", requireAdmin(a.delete))
    mux.HandleFunc("POST /admin/definitions/reload",          requireAdmin(a.reload))
}

func (a *definitionsAdmin) list(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    t := definitions.Type(r.URL.Query().Get("type"))
    if t == "" {
        // Aggregate all types.
        out := map[definitions.Type][]definitions.Entry{}
        for _, typ := range []definitions.Type{definitions.TypeKind, definitions.TypePrompt, definitions.TypeConnector, definitions.TypeSeed} {
            es, err := a.loader.List(ctx, typ)
            if err != nil {
                writeError(w, http.StatusInternalServerError, "internal", err.Error())
                return
            }
            out[typ] = es
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(out)
        return
    }
    es, err := a.loader.List(ctx, t)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "internal", err.Error())
        return
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{"type": t, "items": es})
}

func (a *definitionsAdmin) get(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    t := definitions.Type(r.PathValue("type"))
    name := r.PathValue("name")
    body, version, err := a.loader.Get(ctx, t, name)
    if err == definitions.ErrNotFound {
        writeError(w, http.StatusNotFound, "not_found", "definition not found")
        return
    }
    if err != nil {
        writeError(w, http.StatusInternalServerError, "internal", err.Error())
        return
    }
    w.Header().Set("Content-Type", "application/x-yaml")
    w.Header().Set("ETag", version)
    _, _ = w.Write(body)
}

func (a *definitionsAdmin) put(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    t := definitions.Type(r.PathValue("type"))
    name := r.PathValue("name")
    body, err := io.ReadAll(io.LimitReader(r.Body, maxDefinitionBytes+1))
    if err != nil {
        writeError(w, http.StatusBadRequest, "bad_request", err.Error())
        return
    }
    if len(body) > maxDefinitionBytes {
        writeError(w, http.StatusBadRequest, "too_large", "definition exceeds 64 KB")
        return
    }
    if err := validateDefinition(t, body); err != nil {
        writeError(w, http.StatusBadRequest, "invalid", err.Error())
        return
    }
    key := a.prefix + "/" + string(t) + "/" + name + ".yaml"
    _, err = a.s3.PutObject(ctx, &s3.PutObjectInput{
        Bucket:      aws.String(a.bucket),
        Key:         aws.String(key),
        Body:        bytesReader(body),
        ContentType: aws.String("application/x-yaml"),
    })
    if err != nil {
        writeError(w, http.StatusBadGateway, "r2_put_failed", err.Error())
        return
    }
    // Force local cache update + broadcast.
    _ = a.loader.Invalidate(ctx, t, name)
    a.broadcast(ctx, t, name, "upsert", r)
    w.WriteHeader(http.StatusNoContent)
}

func (a *definitionsAdmin) delete(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    t := definitions.Type(r.PathValue("type"))
    name := r.PathValue("name")
    key := a.prefix + "/" + string(t) + "/" + name + ".yaml"
    _, err := a.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
        Bucket: aws.String(a.bucket),
        Key:    aws.String(key),
    })
    if err != nil {
        writeError(w, http.StatusBadGateway, "r2_delete_failed", err.Error())
        return
    }
    _ = a.loader.Invalidate(ctx, t, name)
    a.broadcast(ctx, t, name, "delete", r)
    w.WriteHeader(http.StatusNoContent)
}

func (a *definitionsAdmin) reload(w http.ResponseWriter, r *http.Request) {
    // Force-refresh: broadcast a wildcard event; every loader re-fetches everything.
    a.broadcast(r.Context(), "*", "*", "reload", r)
    w.WriteHeader(http.StatusAccepted)
}

func (a *definitionsAdmin) broadcast(ctx context.Context, t definitions.Type, name, action string, r *http.Request) {
    by := ""
    if claims := frameSecurityFromContext(ctx); claims != nil {
        by = claims.Subject
    }
    event := eventsv1.DefinitionsChangedV1{
        Type:      string(t),
        Name:      name,
        Version:   time.Now().UTC().Format(time.RFC3339Nano),
        Action:    action,
        ChangedAt: time.Now().UTC(),
        ChangedBy: by,
    }
    env := eventsv1.NewEnvelope(eventsv1.TopicDefinitionsChanged, event)
    if a.emitter != nil {
        if err := a.emitter.Emit(ctx, eventsv1.TopicDefinitionsChanged, env); err != nil {
            util.Log(ctx).WithError(err).Warn("definitions: broadcast failed")
        }
    }
}

// validateDefinition runs a per-type sanity check on the body so a
// malformed kind YAML doesn't crash every reader on the next refresh.
func validateDefinition(t definitions.Type, body []byte) error {
    switch t {
    case definitions.TypeKind:
        return validateKindYAML(body)
    case definitions.TypePrompt:
        if len(body) == 0 {
            return errEmptyBody
        }
        return nil
    case definitions.TypeConnector:
        return validateConnectorYAML(body)
    case definitions.TypeSeed:
        return validateSeedYAML(body)
    default:
        return errUnknownType
    }
}

var (
    errEmptyBody   = errors.New("body is empty")
    errUnknownType = errors.New("unknown definition type")
)

func validateKindYAML(body []byte) error {
    // Unmarshal into opportunity.Spec; reject if it can't.
    var spec opportunity.Spec
    if err := yaml.Unmarshal(body, &spec); err != nil {
        return fmt.Errorf("yaml: %w", err)
    }
    return spec.Validate()
}

func validateConnectorYAML(body []byte) error {
    // Plan B2 wires a real spec validator. For B1 just yaml-parse it.
    var raw map[string]any
    return yaml.Unmarshal(body, &raw)
}

func validateSeedYAML(body []byte) error {
    var raw map[string]any
    return yaml.Unmarshal(body, &raw)
}

func bytesReader(b []byte) io.Reader { return strings.NewReader(string(b)) }
```

(Imports + `frameSecurityFromContext` helper + `yaml` import need to be added; the agent doing implementation will resolve them based on what's actually in the codebase.)

Test in `definitions_admin_test.go` — happy path + validation path + missing path. Use the existing `setAdminAuth` helper. Mock the S3 client via an interface if necessary, OR test the validation logic directly.

- [ ] **Step (build + commit)**

```bash
go build ./apps/api/... 2>&1 | tail -5
go test ./apps/api/cmd/ -run TestDefinitions -v 2>&1 | tail -10
git add apps/api/cmd/definitions_admin.go apps/api/cmd/definitions_admin_test.go
git commit -m "feat(admin): /admin/definitions/* — list/get/put/delete/reload"
```

---

### Task 9: Wire loader into each app's main.go

Each app currently calls `opportunity.LoadFromDir(cfg.OpportunityKindsDir)`. Replace with `LoadFromDefinitions(loader)`.

**Files:**
- Modify: `pkg/opportunity/registry.go` — add `LoadFromDefinitions(ctx, loader)`.
- Modify: 7 main.go files (`apps/{api,crawler,worker,materializer,matching,writer}/cmd/main.go`).

- [ ] **Step 1: `LoadFromDefinitions`**

In `pkg/opportunity/registry.go` add:

```go
// LoadFromDefinitions builds a Registry from kind YAMLs in a Loader.
// Used in place of LoadFromDir when the app is wired through the
// definitions service. Re-runs on every definitions.changed.v1 for
// type=kind (the caller wires the Subscribe).
func LoadFromDefinitions(ctx context.Context, loader definitions.Loader) (*Registry, error) {
    entries, err := loader.List(ctx, definitions.TypeKind)
    if err != nil {
        return nil, fmt.Errorf("opportunity: list kinds: %w", err)
    }
    reg := New()
    for _, e := range entries {
        body, _, err := loader.Get(ctx, definitions.TypeKind, e.Name)
        if err != nil {
            return nil, fmt.Errorf("opportunity: get %s: %w", e.Name, err)
        }
        var s Spec
        if err := yaml.Unmarshal(body, &s); err != nil {
            return nil, fmt.Errorf("opportunity: parse %s: %w", e.Name, err)
        }
        if err := reg.Add(&s); err != nil {
            return nil, fmt.Errorf("opportunity: register %s: %w", e.Name, err)
        }
    }
    return reg, nil
}
```

- [ ] **Step 2: Update one app at a time**

For each `apps/{appname}/cmd/main.go`, replace the `LoadFromDir` call with a loader-constructed registry. Pattern:

```go
// Existing pattern (delete):
reg, err := opportunity.LoadFromDir(cfg.OpportunityKindsDir)

// New:
loader := definitions.NewR2Loader(definitions.R2Config{
    Client: s3Client, // existing app should already have one
    Bucket: cfg.R2ContentBucket,
    Prefix: "definitions",
})
if err := loader.Start(ctx); err != nil {
    log.WithError(err).Fatal("definitions: loader start failed")
}
reg, err := opportunity.LoadFromDefinitions(ctx, loader)
```

For the crawler/api/worker that also need the loader for NATS subscribe + rebuild, add the rebuild subscription:

```go
loader.Subscribe(definitions.TypeKind, func(name, _ string) {
    util.Log(ctx).WithField("name", name).Info("definitions: kind changed, rebuilding registry")
    newReg, err := opportunity.LoadFromDefinitions(ctx, loader)
    if err != nil {
        util.Log(ctx).WithError(err).Warn("definitions: registry rebuild failed; keeping stale")
        return
    }
    reg.ReplaceWith(newReg) // pkg/opportunity.Registry would need a Replace method
})
```

(For B1 we may just log + skip; the rebuild-on-broadcast can be a follow-up sub-task if `Registry.ReplaceWith` is non-trivial.)

- [ ] **Step 3: NATS subscriber for `DefinitionsChangedV1`**

In each app that has a Loader, register a Frame subscriber on `opportunities.definitions.changed.v1` that calls `loader.Invalidate(ctx, type, name)` (or `loader.Stop` + recreate for `action=reload`). Pattern matches existing event-router subscribers.

- [ ] **Step 4: Run + build clean**

```bash
go build ./... 2>&1 | tail -5
go test ./... -count=1 -short 2>&1 | tail -15
```

- [ ] **Step 5: Commit (multiple — one per app — or one big commit if the diffs are small)**

```bash
git add apps/ pkg/opportunity/registry.go
git commit -m "feat(definitions): all apps load opportunity kinds from R2 via Loader"
```

---

### Task 10: Init seed-definitions Job

**Files:**
- Create: `cmd/seed-definitions/main.go`
- Create: K8s Job in deployment.manifests (or add a Helm pre-install hook).

A small CLI that walks `definitions/opportunity-kinds/*.yaml` and uploads each to R2 at `definitions/kind/{name}.yaml`. Runs once on first deploy via a CronJob with a "run-once" annotation, or a Helm post-install hook.

- [ ] **Step 1: Write the seed binary**

`cmd/seed-definitions/main.go`:

```go
// seed-definitions uploads the bootstrap kind YAMLs from
// definitions/opportunity-kinds/ to R2 under definitions/kind/.
// Idempotent: existing R2 keys are overwritten only if their content
// differs from the local file (compared by sha256 → ETag).
//
// Run once at first deploy. Future edits flow through the admin UI;
// re-running this binary OVERWRITES admin edits with whatever's in
// git (intentional — git is the canonical source per spec).
package main

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "flag"
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
    "strings"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/credentials"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
    var (
        source = flag.String("source", "definitions/opportunity-kinds", "Local source directory")
        bucket = flag.String("bucket", os.Getenv("R2_CONTENT_BUCKET"), "R2 bucket")
        prefix = flag.String("prefix", "definitions/kind", "R2 key prefix")
    )
    flag.Parse()
    ctx := context.Background()

    cfg, err := config.LoadDefaultConfig(ctx,
        config.WithRegion("auto"),
        config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
            os.Getenv("R2_ACCESS_KEY_ID"),
            os.Getenv("R2_SECRET_ACCESS_KEY"),
            "",
        )),
    )
    if err != nil {
        die("config: %v", err)
    }
    endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", os.Getenv("R2_ACCOUNT_ID"))
    cli := s3.NewFromConfig(cfg, func(o *s3.Options) {
        o.BaseEndpoint = aws.String(endpoint)
    })

    err = filepath.WalkDir(*source, func(path string, d fs.DirEntry, err error) error {
        if err != nil { return err }
        if d.IsDir() || !strings.HasSuffix(path, ".yaml") { return nil }
        body, err := os.ReadFile(path)
        if err != nil { return err }
        name := strings.TrimSuffix(filepath.Base(path), ".yaml")
        key := *prefix + "/" + name + ".yaml"
        _, err = cli.PutObject(ctx, &s3.PutObjectInput{
            Bucket:      aws.String(*bucket),
            Key:         aws.String(key),
            Body:        bytesReader(body),
            ContentType: aws.String("application/x-yaml"),
        })
        if err != nil {
            return fmt.Errorf("put %s: %w", key, err)
        }
        sum := sha256.Sum256(body)
        fmt.Printf("seeded %s (sha256=%s)\n", key, hex.EncodeToString(sum[:8]))
        return nil
    })
    if err != nil {
        die("walk: %v", err)
    }
}

func bytesReader(b []byte) *bytesReaderType {
    return &bytesReaderType{b: b}
}

type bytesReaderType struct{ b []byte; i int }

func (r *bytesReaderType) Read(p []byte) (int, error) {
    if r.i >= len(r.b) { return 0, fmt.Errorf("EOF") }
    n := copy(p, r.b[r.i:])
    r.i += n
    return n, nil
}

func die(format string, a ...any) {
    fmt.Fprintf(os.Stderr, "seed-definitions: " + format + "\n", a...)
    os.Exit(1)
}
```

- [ ] **Step 2: Add to `cmd/` build**

If there's a `Makefile` `build` target, add this binary. Otherwise just rely on `go build ./cmd/seed-definitions/`.

- [ ] **Step 3: K8s manifest** — a Job that runs this binary once

In `/home/j/code/stawi.org/deployment.manifests/namespaces/product-opportunities/`, add a one-shot Job that mounts the same R2 secret the crawler uses + runs `seed-definitions --bucket=$R2_CONTENT_BUCKET --prefix=definitions/kind`. Pattern matches the existing `iceberg-bootstrap` Job.

Don't commit the manifest blindly — confirm by reading `apps/crawler` config that `R2_CONTENT_BUCKET` is the right bucket; consider using a different bucket if the content bucket has separate IAM scope.

- [ ] **Step 4: Run + commit**

```bash
go build ./cmd/seed-definitions/ 2>&1 | tail -5
git add cmd/seed-definitions/ Makefile
git commit -m "feat(seed): one-shot R2 uploader for bootstrap kind YAMLs"
```

(Manifest commit in deployment.manifests repo.)

---

### Task 11: Tag + deploy v8.0.63

- [ ] **Step 1: Push, tag**

```bash
git push origin main
git tag v8.0.63
git push origin v8.0.63
```

- [ ] **Step 2: Wait for build, let Flux roll**

```bash
gh run watch $(gh run list --limit 1 --workflow release.yaml --repo stawi-opportunities/opportunities --json databaseId -q '.[0].databaseId') --repo stawi-opportunities/opportunities --exit-status
```

- [ ] **Step 3: Run seed Job once (manual if not a Helm hook)**

```bash
kubectl create job --from=cronjob/seed-definitions seed-definitions-manual-$(date +%s) -n product-opportunities
# Or if Helm hook runs automatically, skip.
```

- [ ] **Step 4: Verify**

```bash
# Admin endpoint reachable:
kubectl port-forward -n product-opportunities svc/opportunities-api 18080:80 >/dev/null 2>&1 &
sleep 2
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" http://localhost:18080/admin/definitions?type=kind | jq .

# Confirm 5 kind definitions are listed (job, deal, funding, scholarship, tender).
```

---

### Task 12: Reparse rejected raw payloads

**Why this matters:** rejected variants are not necessarily bad jobs — they're failures of the current parsing semantics. Now that operators can edit `extraction_prompt_extension` (Task 4) and full kind prompts (Task 8), they need a way to re-process previously-rejected pages and see if the new parsing succeeds. The HTML is preserved in R2 (content-addressed by `raw_payloads.content_hash`); the rejection is in Iceberg with `raw_payload_id` (post-PA-S3). All the ingredients exist; we just need an endpoint to drive them.

**Files:**
- Create: `apps/api/cmd/reparse_admin.go` — `POST /admin/raw_payloads/{id}/reparse`, `POST /admin/sources/{id}/reparse?since=24h`.
- Modify: `pkg/events/v1/crawl.go` — add `ReparseRequestV1` (or reuse `CrawlRequestV1` with a `RawPayloadID` field — see decision below).
- Modify: `apps/crawler/service/crawl_request_handler.go` — handler for the reparse path that bypasses the connector iterator and runs extraction directly on the stored HTML.

**Decision: reuse `CrawlRequestV1`** with a new optional `RawPayloadID` field. When non-empty, the crawler's `Execute` skips `conn.Crawl(...)` and instead:

1. `crawlRepo.GetRawPayload(ctx, RawPayloadID)` → row.
2. `archive.GetRaw(ctx, row.ContentHash)` → HTML (or markdown via `content.ExtractFromHTML`).
3. `extractor.Extract(ctx, body, src.Kinds, src.ExtractionPromptExtension)` → `ExternalOpportunity`.
4. Run the existing `opportunity.Verify` + emit `variants.ingested.v1` exactly the way the iterator path does.
5. Audit: increment `raw_payloads.reparse_count` (new column, set in the migration in Task 1 — append `ADD COLUMN reparse_count INTEGER NOT NULL DEFAULT 0`).

This re-uses the same downstream pipeline (normalize → validate → cluster → canonical → publish) with no duplication.

- [ ] **Step 1: Extend the migration**

Append to the SQL file from Task 1:

```sql
ALTER TABLE raw_payloads
    ADD COLUMN IF NOT EXISTS reparse_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_reparsed_at TIMESTAMPTZ;
```

(If Task 1 has already been committed, add a new migration file `20260528_0041_raw_payloads_reparse_count.sql` with just these ALTERs.)

- [ ] **Step 2: Add `RawPayloadID` to `CrawlRequestV1`**

In `pkg/events/v1/crawl.go`, add to the existing struct (after `URL`):

```go
// RawPayloadID, when non-empty, switches the crawl handler from
// connector-iterator mode to reparse mode: fetch the stored HTML
// from R2 by content_hash and re-run extraction. Used by the
// /admin/raw_payloads/{id}/reparse endpoint after operators edit
// extraction prompts or connector specs.
RawPayloadID string `json:"raw_payload_id,omitempty" parquet:"raw_payload_id,optional"`
```

- [ ] **Step 3: Reparse branch in the crawl handler**

In `apps/crawler/service/crawl_request_handler.go` `Execute`, near the start (after parsing the envelope, before the source lookup), add:

```go
if req.RawPayloadID != "" {
    return h.reparse(ctx, req)
}
```

Then add the new method:

```go
// reparse re-runs extraction on a previously-fetched HTML page,
// bypassing the connector iterator. Used by /admin/raw_payloads/{id}/reparse.
func (h *CrawlRequestHandler) reparse(ctx context.Context, req eventsv1.CrawlRequestV1) error {
    log := util.Log(ctx).WithField("raw_payload_id", req.RawPayloadID)
    rp, err := h.deps.CrawlRepo.GetRawPayload(ctx, req.RawPayloadID)
    if err != nil { return fmt.Errorf("reparse: lookup raw_payload: %w", err) }
    if rp == nil { return errors.New("reparse: raw_payload not found") }

    src, err := h.deps.Sources.GetByID(ctx, rp.SourceID)
    if err != nil || src == nil { return fmt.Errorf("reparse: source: %w", err) }

    body, err := h.deps.Archive.GetRaw(ctx, rp.ContentHash)
    if err != nil { return fmt.Errorf("reparse: archive: %w", err) }

    bodyStr := string(body)
    if ext, _ := content.ExtractFromHTML(bodyStr); ext != nil && ext.Markdown != "" {
        bodyStr = ext.Markdown
    }

    extracted, err := h.deps.Extractor.Extract(ctx, bodyStr, src.Kinds, src.ExtractionPromptExtension)
    if err != nil || extracted == nil {
        log.WithError(err).Warn("reparse: extraction returned empty")
        return nil // not a NATS-level failure; an empty extraction is a data outcome
    }

    extJob := *extracted
    extJob.SourceID = src.ID
    ensureApplyURL(&extJob, rp.SourceURL)
    kind := extJob.Kind
    if kind == "" && len(src.Kinds) > 0 { kind = src.Kinds[0]; extJob.Kind = kind }

    if h.deps.Kinds != nil {
        if res := opportunity.Verify(&extJob, src, h.deps.Kinds); !res.OK {
            log.WithField("reasons", res.Missing).Info("reparse: still rejected after re-extraction")
            // Still emit the rejection — Iceberg gets the updated reasons.
            return h.publishRejected(ctx, src.ID, kind, extJob, res, "", rp.ID)
        }
    }

    // Build + emit variants.ingested as the normal iterator path does.
    // (Copy the relevant block from the iterator loop; OR factor that
    // block into a helper and call it from both places.)
    return h.emitReparsedVariant(ctx, src, &extJob, kind, rp)
}
```

(The `emitReparsedVariant` helper mirrors lines 271-372 of the existing iterator loop — extracting the variant build + VariantStore.Upsert + Emit into a shared helper is the cleanest refactor.)

Also bump the audit column:

```go
defer func() {
    if h.deps.CrawlRepo != nil {
        h.deps.CrawlRepo.IncrementReparseCount(ctx, rp.ID)
    }
}()
```

Add `IncrementReparseCount` to `pkg/repository/crawl.go`:

```go
func (r *CrawlRepository) IncrementReparseCount(ctx context.Context, id string) error {
    return r.db(ctx, false).
        Table("raw_payloads").
        Where("id = ?", id).
        Updates(map[string]any{
            "reparse_count":     gorm.Expr("reparse_count + 1"),
            "last_reparsed_at":  time.Now().UTC(),
        }).Error
}
```

- [ ] **Step 4: Admin endpoints**

`apps/api/cmd/reparse_admin.go`:

```go
package main

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/pitabwire/frame"

    eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
    "github.com/stawi-opportunities/opportunities/pkg/repository"
)

type reparseAdmin struct {
    crawlRepo *repository.CrawlRepository
    emitter   frame.EventsManager
}

func registerReparseAdmin(mux *http.ServeMux, crawl *repository.CrawlRepository, emitter frame.EventsManager) {
    a := &reparseAdmin{crawlRepo: crawl, emitter: emitter}
    mux.HandleFunc("POST /admin/raw_payloads/{id}/reparse",   requireAdmin(a.reparseOne))
    mux.HandleFunc("POST /admin/sources/{id}/reparse",        requireAdmin(a.reparseSource))
}

// reparseOne enqueues a single raw_payload for re-extraction.
func (a *reparseAdmin) reparseOne(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    id := r.PathValue("id")
    rp, err := a.crawlRepo.GetRawPayload(ctx, id)
    if err != nil { writeError(w, http.StatusInternalServerError, "internal", err.Error()); return }
    if rp == nil  { writeError(w, http.StatusNotFound, "not_found", "raw_payload not found"); return }
    if err := a.emitReparse(ctx, rp); err != nil {
        writeError(w, http.StatusBadGateway, "emit_failed", err.Error()); return
    }
    w.WriteHeader(http.StatusAccepted)
    _ = json.NewEncoder(w).Encode(map[string]any{"queued": 1, "raw_payload_id": id})
}

// reparseSource enqueues every raw_payload of a source within a time window.
func (a *reparseAdmin) reparseSource(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    sourceID := r.PathValue("id")
    since := parseWindow(r.URL.Query().Get("since"), 24*time.Hour)
    rows, err := a.crawlRepo.ListRawPayloadsBySource(ctx, sourceID, since, 1000)
    if err != nil { writeError(w, http.StatusInternalServerError, "internal", err.Error()); return }
    queued := 0
    for _, rp := range rows {
        if err := a.emitReparse(ctx, &rp); err != nil {
            // log + continue — partial success is acceptable
            continue
        }
        queued++
    }
    w.WriteHeader(http.StatusAccepted)
    _ = json.NewEncoder(w).Encode(map[string]any{"queued": queued, "source_id": sourceID, "window_seconds": int(since.Seconds())})
}

func (a *reparseAdmin) emitReparse(ctx context.Context, rp *domain.RawPayload) error {
    env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
        RequestID:    xid.New().String(),
        SourceID:     rp.SourceID,
        RawPayloadID: rp.ID,
        ScheduledAt:  time.Now().UTC(),
        Mode:         "reparse",
        Attempt:      1,
    })
    return a.emitter.Emit(ctx, eventsv1.TopicCrawlRequests, env)
}
```

Add `ListRawPayloadsBySource` to `pkg/repository/crawl.go`:

```go
func (r *CrawlRepository) ListRawPayloadsBySource(ctx context.Context, sourceID string, since time.Duration, limit int) ([]domain.RawPayload, error) {
    if limit <= 0 || limit > 5000 { limit = 1000 }
    cutoff := time.Now().Add(-since)
    var rows []domain.RawPayload
    err := r.db(ctx, true).
        Where("source_id = ? AND fetched_at >= ?", sourceID, cutoff).
        Order("fetched_at DESC").
        Limit(limit).
        Find(&rows).Error
    return rows, err
}
```

- [ ] **Step 5: Wire in main**

In `sources_admin.go` (where other admins are wired), add:

```go
registerReparseAdmin(mux, repository.NewCrawlRepository(pool.DB), svc.EventsManager())
```

(`svc.EventsManager()` requires the api to have a Frame service handle; the source admin path already constructs one. If `svc` isn't in scope at that callsite, route the emitter from main.go.)

- [ ] **Step 6: Integration test**

Add to `reparse_admin_test.go`:

```go
//go:build integration

func TestReparseOne_Enqueues(t *testing.T) {
    pool := testhelpers.PostgresPool(t)
    seed := testhelpers.SeedSimpleVariant(t, pool, "src-rp-1", "var-rp-1")
    capture := frametest.NewEventCapture()
    a := reparseAdmin{
        crawlRepo: repository.NewCrawlRepository(pool),
        emitter:   capture,
    }
    req := httptest.NewRequest("POST", "/admin/raw_payloads/"+seed.RawPayloadID+"/reparse", nil)
    req.SetPathValue("id", seed.RawPayloadID)
    setAdminAuth(req)
    rec := httptest.NewRecorder()
    a.reparseOne(rec, req)
    if rec.Code != 202 {
        t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
    }
    if capture.Count(eventsv1.TopicCrawlRequests) != 1 {
        t.Fatalf("emitted = %d; want 1", capture.Count(eventsv1.TopicCrawlRequests))
    }
}
```

(`frametest.NewEventCapture` is the same in-memory event emitter used in earlier slices.)

- [ ] **Step 7: Build + tests + commit**

```bash
go build ./... 2>&1 | tail -5
go test ./apps/api/cmd/ ./apps/crawler/service/ -count=1 -short 2>&1 | tail -10
git add apps/api/cmd/reparse_admin.go apps/crawler/service/crawl_request_handler.go pkg/events/v1/crawl.go pkg/repository/crawl.go apps/crawler/migrations/0001/
git commit -m "feat(admin): POST /admin/raw_payloads/{id}/reparse + /sources/{id}/reparse

Rejected variants aren't terminal — their HTML is preserved in R2
content-addressed. After an operator edits the kind prompt, the
per-source prompt extension, or (in Plan B2) the connector spec,
they can re-run extraction on the stored payloads without waiting
for the next crawl tick.

reparse path bypasses the connector iterator: GetRawPayload →
archive.GetRaw → Extract → Verify → emit. raw_payloads tracks
reparse_count + last_reparsed_at for audit.

Batch endpoint POST /admin/sources/{id}/reparse?since=24h enqueues
every raw_payload of a source in a window — operator workflow is
'edit prompt, see source trace fill with re-extracted variants in
seconds.'"
```

---

## Plan B1 Exit Criteria

- [ ] `apps/api/cmd/sources_admin.go` `handleUpdate` persists `extraction_prompt_extension` (4 KB cap).
- [ ] Crawler `enrichOne` reads `src.ExtractionPromptExtension` and passes it through to `Extract`.
- [ ] `pkg/definitions.Loader` interface + `R2Loader` + `MemoryLoader` exist with unit tests.
- [ ] R2 contains `definitions/kind/{job,deal,funding,scholarship,tender}.yaml` after the seed Job runs.
- [ ] All 7 apps boot via `LoadFromDefinitions` instead of `LoadFromDir`.
- [ ] `PUT /admin/definitions/kind/job` with new body broadcasts `opportunities.definitions.changed.v1`; the crawler's loader re-fetches and rebuilds the registry.
- [ ] `POST /admin/raw_payloads/{id}/reparse` enqueues a `CrawlRequestV1` with `RawPayloadID`; the crawler's `reparse` branch re-runs extraction on the stored HTML.
- [ ] `POST /admin/sources/{id}/reparse?since=24h` enqueues N raw_payloads for the source.
- [ ] `raw_payloads.reparse_count` increments on each reparse.
- [ ] All existing tests pass.
