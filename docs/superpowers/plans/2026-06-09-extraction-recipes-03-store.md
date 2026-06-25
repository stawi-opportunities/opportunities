# Extraction Recipes — Plan 3: Store, Migrations & Handler Wiring

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Persist recipes (a `sources.extraction_recipe` column + a `source_recipes` history table with atomic swap/rollback), provide a production `Fetcher` over the crawler's `httpx.Client` and a `connectors.CrawlIterator` shim over the Executor, and wire the Executor into `crawl_request_handler` so a source with a recipe crawls deterministically.

**Architecture:** `pkg/repository.RecipeRepository` owns the column + history table and (un)marshals `recipe.Recipe` (keeping `domain` free of a `pkg/recipe` import — `domain.Source.ExtractionRecipe` is a raw JSON `string`). A small adapter package exposes the Executor as a real `connectors.CrawlIterator`. The handler picks the recipe path when `src.ExtractionRecipe` is a non-empty recipe.

**Tech Stack:** Go 1.26, gorm, Frame datastore, testcontainers (`tests/integration/testhelpers`), `pkg/recipe` (Plans 1-2).

**Spec:** design doc §5E (Store), §5F (Integration). **Depends on:** Plans 1-2.

**Key constraints (from infra exploration):**
- Repositories: `type XRepo struct { db func(ctx, readOnly bool) *gorm.DB }`; `Update(ctx, id, map[string]any)`; jsonb via `serializer:json` or raw string. `domain.Source.Config` is a `string gorm:"type:jsonb;default:'{}'"` precedent.
- Migrations: SQL files in `db/migrations/NNNN_*.sql`; tests apply via `testhelpers.PostgresContainer(t, ctx, "../../db/migrations")` then wrap with gorm. `domain.Source` + `domain.RawRef` are also AutoMigrated at boot.
- `connectors.CrawlIterator` is `Next(ctx) bool` + `Items()/RawPayload()/HTTPStatus()/Err()/Cursor() json.RawMessage/Content() *content.Extracted`.
- `httpx.Client.Get(ctx, url, headers map[string]string) ([]byte, int, error)`.
- Connector resolution: `crawl_request_handler.go:222` `conn, ok := h.deps.Registry.Get(src.Type)` then `iter = conn.Crawl(ctx, *src)` (line ~290); `CrawlRequestDeps` is the injection struct.

---

## File Structure
| File | Responsibility |
|------|----------------|
| `db/migrations/0018_source_recipes.sql` | `sources.extraction_recipe` column + `source_recipes` history table + indexes |
| `pkg/domain/models.go` (modify) | add `ExtractionRecipe string` jsonb field to `Source` |
| `pkg/repository/recipe.go` | `RecipeRepository`: `Active`, `Activate` (atomic swap), `Rollback`, `History` |
| `pkg/repository/recipe_test.go` | testcontainer tests |
| `pkg/recipe/httpfetcher.go` | `HTTPFetcher` adapter (`recipe.Fetcher` over an httpx-like GET) |
| `pkg/connectors/recipeconn/iterator.go` | `Connector`-compatible `CrawlIterator` shim over the Executor |
| `apps/crawler/service/crawl_request_handler.go` (modify) | resolve the recipe Executor when `src.ExtractionRecipe` is set |

---

## Task 1: Migration + domain field + `RecipeRepository`

**Files:** Create `db/migrations/0018_source_recipes.sql`, `pkg/repository/recipe.go`, `pkg/repository/recipe_test.go`; modify `pkg/domain/models.go`.

- [ ] **Step 1: Write the migration** `db/migrations/0018_source_recipes.sql`

```sql
-- Active recipe lives inline on the source (queryable: "which sources have a recipe?").
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS extraction_recipe JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Full version history for diff + rollback.
CREATE TABLE IF NOT EXISTS source_recipes (
    id                VARCHAR(20)      PRIMARY KEY,
    source_id         VARCHAR(20)      NOT NULL,
    version           INTEGER          NOT NULL,
    recipe            JSONB            NOT NULL,
    status            VARCHAR(16)      NOT NULL DEFAULT 'active',  -- active | superseded | rejected
    pass_rate         DOUBLE PRECISION NOT NULL DEFAULT 0,
    model             VARCHAR(128)     NOT NULL DEFAULT '',
    validation_report JSONB,
    created_at        TIMESTAMPTZ      NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_source_recipes_source ON source_recipes (source_id, version DESC);
-- At most one active recipe per source.
CREATE UNIQUE INDEX IF NOT EXISTS idx_source_recipes_active ON source_recipes (source_id) WHERE status = 'active';
```

- [ ] **Step 2: Add the field to `domain.Source`** in `pkg/domain/models.go`

Find the `Config` field (`Config string \`gorm:"type:jsonb;default:'{}'" json:"config"\``) and add immediately after it:

```go
	// ExtractionRecipe holds the source's active AI-generated extraction
	// recipe as raw JSON ('{}' when none). Stored as a string so the domain
	// package stays free of a pkg/recipe import (recipe imports domain).
	// pkg/repository.RecipeRepository (un)marshals it to recipe.Recipe.
	ExtractionRecipe string `gorm:"type:jsonb;not null;default:'{}'" json:"extraction_recipe"`
```

- [ ] **Step 3: Write the failing test** `pkg/repository/recipe_test.go`

```go
package repository_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupRecipeDB(t *testing.T) (*gorm.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, sqlDB))
	testhelpers.ApplyMigrationsDir(t, ctx, sqlDB, "../../db/migrations")
	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	return g, ctx
}

func dbFn(g *gorm.DB) func(context.Context, bool) *gorm.DB {
	return func(_ context.Context, _ bool) *gorm.DB { return g }
}

func seedSource(t *testing.T, g *gorm.DB, ctx context.Context, id string) {
	t.Helper()
	s := &domain.Source{Type: "brightermonday", BaseURL: "https://x.io/" + id, Country: "KE"}
	s.ID = id
	s.Kinds = []string{"job"}
	require.NoError(t, g.WithContext(ctx).Create(s).Error)
}

func sampleRecipe(v int) *recipe.Recipe {
	jl := func(p string) recipe.FieldExtractor { return recipe.FieldExtractor{From: []string{"json_ld"}, JSONPath: p} }
	return &recipe.Recipe{
		Version: v, Acquisition: "structured_data", Kind: recipe.KindRule{Mode: "source_default"},
		List:   recipe.ListRule{Mode: "selector", ItemSelector: ".c", Link: recipe.FieldExtractor{From: []string{"selector"}, Selector: "a", Attr: "href"}, Pagination: recipe.Pagination{Mode: "none"}},
		Detail: recipe.DetailRule{RecordSource: "json_ld", Title: jl("$.t"), Description: jl("$.d"), IssuingEntity: jl("$.c"), ApplyURL: jl("$.u"), AnchorCountry: jl("$.k")},
	}
}

func TestRecipeRepo_ActivateThenActiveReturnsRecipe(t *testing.T) {
	g, ctx := setupRecipeDB(t)
	seedSource(t, g, ctx, "src_a")
	repo := repository.NewRecipeRepository(dbFn(g))

	// No recipe yet.
	got, err := repo.Active(ctx, "src_a")
	require.NoError(t, err)
	assert.Nil(t, got)

	require.NoError(t, repo.Activate(ctx, "src_a", sampleRecipe(1), 0.9, "model-x", nil))

	got, err = repo.Active(ctx, "src_a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 1, got.Version)
	assert.Equal(t, "structured_data", got.Acquisition)
}

func TestRecipeRepo_ActivateSupersedesAndBumpsVersion(t *testing.T) {
	g, ctx := setupRecipeDB(t)
	seedSource(t, g, ctx, "src_b")
	repo := repository.NewRecipeRepository(dbFn(g))

	require.NoError(t, repo.Activate(ctx, "src_b", sampleRecipe(0), 0.8, "m1", nil))
	require.NoError(t, repo.Activate(ctx, "src_b", sampleRecipe(0), 0.95, "m2", nil))

	got, err := repo.Active(ctx, "src_b")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 2, got.Version) // auto-incremented

	hist, err := repo.History(ctx, "src_b")
	require.NoError(t, err)
	require.Len(t, hist, 2)
	// exactly one active row
	var active int
	for _, h := range hist {
		if h.Status == "active" {
			active++
		}
	}
	assert.Equal(t, 1, active)
}

func TestRecipeRepo_Rollback(t *testing.T) {
	g, ctx := setupRecipeDB(t)
	seedSource(t, g, ctx, "src_c")
	repo := repository.NewRecipeRepository(dbFn(g))
	require.NoError(t, repo.Activate(ctx, "src_c", sampleRecipe(0), 0.8, "m1", nil)) // v1
	require.NoError(t, repo.Activate(ctx, "src_c", sampleRecipe(0), 0.9, "m2", nil)) // v2

	require.NoError(t, repo.Rollback(ctx, "src_c", 1))
	got, err := repo.Active(ctx, "src_c")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 1, got.Version)
}

var _ = sql.LevelDefault // keep database/sql import if unused elsewhere
```

- [ ] **Step 4: Run, expect FAIL**

Run: `go test ./pkg/repository/ -run TestRecipeRepo -v`
Expected: FAIL — `undefined: repository.NewRecipeRepository`.

- [ ] **Step 5: Implement** `pkg/repository/recipe.go`

```go
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/xid"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"gorm.io/gorm"
)

// SourceRecipe is one row of the source_recipes history table.
type SourceRecipe struct {
	ID               string          `gorm:"column:id;primaryKey"`
	SourceID         string          `gorm:"column:source_id"`
	Version          int             `gorm:"column:version"`
	Recipe           json.RawMessage `gorm:"column:recipe;type:jsonb"`
	Status           string          `gorm:"column:status"`
	PassRate         float64         `gorm:"column:pass_rate"`
	Model            string          `gorm:"column:model"`
	ValidationReport json.RawMessage `gorm:"column:validation_report;type:jsonb"`
	CreatedAt        time.Time       `gorm:"column:created_at"`
}

func (SourceRecipe) TableName() string { return "source_recipes" }

// RecipeRepository persists per-source extraction recipes: the active recipe
// inline on the source plus full version history with atomic swap and rollback.
type RecipeRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewRecipeRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *RecipeRepository {
	return &RecipeRepository{db: db}
}

// Active returns the source's active recipe, or nil when it has none. An empty
// JSON object ('{}', i.e. Acquisition unset) counts as "none".
func (r *RecipeRepository) Active(ctx context.Context, sourceID string) (*recipe.Recipe, error) {
	var src domain.Source
	if err := r.db(ctx, true).Select("extraction_recipe").Where("id = ?", sourceID).First(&src).Error; err != nil {
		return nil, err
	}
	return decodeRecipe(src.ExtractionRecipe)
}

// decodeRecipe parses a recipe JSON string, returning nil for an empty/unset one.
func decodeRecipe(s string) (*recipe.Recipe, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var rec recipe.Recipe
	if err := json.Unmarshal([]byte(s), &rec); err != nil {
		return nil, fmt.Errorf("decode recipe: %w", err)
	}
	if rec.Acquisition == "" {
		return nil, nil
	}
	return &rec, nil
}

// Activate atomically installs rec as the source's active recipe: it inserts a
// new history row (version = prior max + 1) as 'active', marks any prior active
// row 'superseded', and updates sources.extraction_recipe — all in one tx. The
// recipe's Version field is set to the assigned version.
func (r *RecipeRepository) Activate(ctx context.Context, sourceID string, rec *recipe.Recipe, passRate float64, model string, validationReport any) error {
	recJSON, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal recipe: %w", err)
	}
	var reportJSON json.RawMessage
	if validationReport != nil {
		if reportJSON, err = json.Marshal(validationReport); err != nil {
			return fmt.Errorf("marshal validation report: %w", err)
		}
	}
	return r.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		var maxV *int
		if err := tx.Model(&SourceRecipe{}).Where("source_id = ?", sourceID).
			Select("MAX(version)").Scan(&maxV).Error; err != nil {
			return err
		}
		next := 1
		if maxV != nil {
			next = *maxV + 1
		}
		rec.Version = next
		// Re-marshal so the stored JSON carries the assigned version.
		if recJSON, err = json.Marshal(rec); err != nil {
			return err
		}
		if err := tx.Model(&SourceRecipe{}).
			Where("source_id = ? AND status = ?", sourceID, "active").
			Update("status", "superseded").Error; err != nil {
			return err
		}
		row := &SourceRecipe{
			ID: xid.New().String(), SourceID: sourceID, Version: next,
			Recipe: recJSON, Status: "active", PassRate: passRate, Model: model,
			ValidationReport: reportJSON, CreatedAt: time.Now().UTC(),
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		return tx.Model(&domain.Source{}).Where("id = ?", sourceID).
			Update("extraction_recipe", json.RawMessage(recJSON)).Error
	})
}

// Rollback re-activates a prior version: marks the current active row
// superseded, the target version active, and copies its recipe back inline.
func (r *RecipeRepository) Rollback(ctx context.Context, sourceID string, version int) error {
	return r.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		var target SourceRecipe
		if err := tx.Where("source_id = ? AND version = ?", sourceID, version).First(&target).Error; err != nil {
			return fmt.Errorf("rollback target v%d: %w", version, err)
		}
		if err := tx.Model(&SourceRecipe{}).Where("source_id = ? AND status = ?", sourceID, "active").
			Update("status", "superseded").Error; err != nil {
			return err
		}
		if err := tx.Model(&SourceRecipe{}).Where("id = ?", target.ID).
			Update("status", "active").Error; err != nil {
			return err
		}
		return tx.Model(&domain.Source{}).Where("id = ?", sourceID).
			Update("extraction_recipe", target.Recipe).Error
	})
}

// History returns all recipe versions for a source, newest first.
func (r *RecipeRepository) History(ctx context.Context, sourceID string) ([]SourceRecipe, error) {
	var rows []SourceRecipe
	err := r.db(ctx, true).Where("source_id = ?", sourceID).Order("version DESC").Find(&rows).Error
	return rows, err
}
```

- [ ] **Step 6: Run, expect PASS**

Run: `go test ./pkg/repository/ -run TestRecipeRepo -v` (needs Docker for testcontainers)
Expected: PASS (3 tests). If `xid` import path differs, match the one `pkg/domain/basemodel.go` uses (search `xid` there).

- [ ] **Step 7: Commit**

```bash
git add db/migrations/0018_source_recipes.sql pkg/domain/models.go pkg/repository/recipe.go pkg/repository/recipe_test.go
git commit -m "feat(repository): RecipeRepository + source_recipes table + sources.extraction_recipe column"
```

---

## Task 2: Production `Fetcher` + `CrawlIterator` shim

**Files:** Create `pkg/recipe/httpfetcher.go`, `pkg/connectors/recipeconn/iterator.go`, and tests.

- [ ] **Step 1: Write failing tests** `pkg/recipe/httpfetcher_test.go`

```go
package recipe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubGetter struct{ c *http.Client }

func (s stubGetter) Get(ctx context.Context, url string, _ map[string]string) ([]byte, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := s.c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var b []byte
	tmp := make([]byte, 1024)
	for {
		n, rerr := resp.Body.Read(tmp)
		b = append(b, tmp[:n]...)
		if rerr != nil {
			break
		}
	}
	return b, resp.StatusCode, nil
}

func TestHTTPFetcher_AdaptsHeaderedGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	f := NewHTTPFetcher(stubGetter{c: srv.Client()})
	body, status, err := f.Get(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, 200, status)
	assert.Equal(t, "hello", string(body))
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./pkg/recipe/ -run TestHTTPFetcher` → undefined `NewHTTPFetcher`.

- [ ] **Step 3: Implement** `pkg/recipe/httpfetcher.go`

```go
package recipe

import "context"

// HeaderedGetter is the subset of the crawler's httpx.Client the recipe
// Fetcher needs. *httpx.Client satisfies it (Get(ctx, url, headers)).
type HeaderedGetter interface {
	Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error)
}

// HTTPFetcher adapts a HeaderedGetter to the recipe.Fetcher interface (no
// headers). This is the production Fetcher; tests use httptest directly.
type HTTPFetcher struct{ g HeaderedGetter }

func NewHTTPFetcher(g HeaderedGetter) *HTTPFetcher { return &HTTPFetcher{g: g} }

func (f *HTTPFetcher) Get(ctx context.Context, url string) ([]byte, int, error) {
	return f.g.Get(ctx, url, nil)
}
```

- [ ] **Step 4: Write failing test** `pkg/connectors/recipeconn/iterator_test.go`

```go
package recipeconn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectorIterator_SatisfiesCrawlIterator(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"jobs":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"jobs":[{"title":"A","description":"A description comfortably over the fifty character minimum gate now.","company":"X","apply":"https://x.io/a","country":"KE"}]}`))
	}))
	defer srv.Close()

	rec := func(p string) recipe.FieldExtractor { return recipe.FieldExtractor{From: []string{"record"}, JSONPath: p} }
	r := &recipe.Recipe{Acquisition: "api", Kind: recipe.KindRule{Mode: "source_default"},
		List:   recipe.ListRule{Mode: "api", Endpoint: "/api/jobs", ItemsPath: "$.jobs", Pagination: recipe.Pagination{Mode: "page_param", Param: "page", MaxPages: 5}},
		Detail: recipe.DetailRule{RecordSource: "record", Title: rec("$.title"), Description: rec("$.description"), IssuingEntity: rec("$.company"), ApplyURL: rec("$.apply"), AnchorCountry: rec("$.country")}}
	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "NG"}
	src.ID = "s1"
	src.Kinds = []string{"job"}

	exec := recipe.NewExecutor(r, httptestFetcher{c: srv.Client()})

	// Compile-time: the shim is a connectors.CrawlIterator.
	var it connectors.CrawlIterator = NewConnectorIterator(exec, src)

	var all []domain.ExternalOpportunity
	for it.Next(context.Background()) {
		all = append(all, it.Items()...)
	}
	require.NoError(t, it.Err())
	require.Len(t, all, 1)
	assert.Equal(t, "A", all[0].Title)
	assert.Nil(t, it.Content())
}
```

- [ ] **Step 5: Run, expect FAIL** — undefined `NewConnectorIterator`.

- [ ] **Step 6: Implement** `pkg/connectors/recipeconn/iterator.go`

```go
package recipeconn

import (
	"context"
	"encoding/json"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

// ConnectorIterator binds an Executor to a single source and exposes it as a
// connectors.CrawlIterator so the crawl pipeline can drive it with no changes.
type ConnectorIterator struct {
	exec *recipe.Executor
	src  domain.Source

	state recipe.PageState
	done  bool

	items  []domain.ExternalOpportunity
	raw    []byte
	status int
	err    error
}

var _ connectors.CrawlIterator = (*ConnectorIterator)(nil)

// NewConnectorIterator binds exec to src.
func NewConnectorIterator(exec *recipe.Executor, src domain.Source) *ConnectorIterator {
	return &ConnectorIterator{exec: exec, src: src}
}

func (it *ConnectorIterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}
	items, raw, status, next, done, err := it.exec.Page(ctx, it.src, it.state)
	it.items, it.raw, it.status = items, raw, status
	if err != nil {
		it.err = err
		return false
	}
	if done {
		it.done = true
	} else {
		it.state = next
	}
	return true
}

func (it *ConnectorIterator) Items() []domain.ExternalOpportunity { return it.items }
func (it *ConnectorIterator) RawPayload() []byte                  { return it.raw }
func (it *ConnectorIterator) HTTPStatus() int                     { return it.status }
func (it *ConnectorIterator) Err() error                          { return it.err }
func (it *ConnectorIterator) Cursor() json.RawMessage             { return nil }
func (it *ConnectorIterator) Content() *content.Extracted         { return nil }
```

- [ ] **Step 7: Run, expect PASS** — `go test ./pkg/recipe/ ./pkg/connectors/recipeconn/ -run 'TestHTTPFetcher|TestConnectorIterator' -v`. Then `go test -race ./pkg/recipe/... ./pkg/connectors/recipeconn/... && go vet ./...`.

- [ ] **Step 8: Commit**

```bash
git add pkg/recipe/httpfetcher.go pkg/recipe/httpfetcher_test.go pkg/connectors/recipeconn/iterator.go pkg/connectors/recipeconn/iterator_test.go
git commit -m "feat(recipe): production HTTPFetcher + connectors.CrawlIterator shim (ConnectorIterator)"
```

---

## Task 3: Wire the Executor into `crawl_request_handler`

**Files:** Modify `apps/crawler/service/crawl_request_handler.go`, `apps/crawler/cmd/main.go`.

- [ ] **Step 1: Add deps** — in `crawl_request_handler.go`, add to `CrawlRequestDeps`:

```go
	// RecipeRepo, when set, supplies per-source extraction recipes. A source
	// with an active recipe crawls via the deterministic recipe Executor
	// instead of its registered connector.
	RecipeRepo *repository.RecipeRepository
```

- [ ] **Step 2: Add a resolver helper** in `crawl_request_handler.go` (near the connector resolution):

```go
// resolveIterator returns the crawl iterator for src: the deterministic recipe
// Executor when src has an active recipe, otherwise the registered connector.
// The bool reports whether a connector/recipe was found.
func (h *CrawlRequestHandler) resolveIterator(ctx context.Context, src *domain.Source) (connectors.CrawlIterator, bool) {
	if h.deps.RecipeRepo != nil {
		if rec, err := h.deps.RecipeRepo.Active(ctx, src.ID); err == nil && rec != nil {
			fetcher := recipe.NewHTTPFetcher(h.deps.PageFetcher)
			exec := recipe.NewExecutor(rec, fetcher)
			return recipeconn.NewConnectorIterator(exec, *src), true
		}
	}
	conn, ok := h.deps.Registry.Get(src.Type)
	if !ok {
		return nil, false
	}
	return conn.Crawl(ctx, *src), true
}
```

Add imports: `"github.com/stawi-opportunities/opportunities/pkg/recipe"`, `"github.com/stawi-opportunities/opportunities/pkg/connectors/recipeconn"`, and `"github.com/stawi-opportunities/opportunities/pkg/repository"` (if not already imported).

- [ ] **Step 3: Use it.** Replace the connector resolution at `crawl_request_handler.go:222` (`conn, ok := h.deps.Registry.Get(src.Type)` + the `if !ok {...}` block) and the later `iter = conn.Crawl(ctx, *src)` so that, when `RecipeRepo` yields a recipe, the recipe iterator is used and the checkpoint-resume branch is skipped. Concretely: keep the existing `conn, ok := h.deps.Registry.Get(src.Type)` for the `connector_not_registered` telemetry path, but BEFORE it, try the recipe path:

```go
	// Recipe path: deterministic Executor when the source has an active recipe.
	if h.deps.RecipeRepo != nil {
		if rec, rErr := h.deps.RecipeRepo.Active(ctx, src.ID); rErr == nil && rec != nil {
			recipeIter := recipeconn.NewConnectorIterator(
				recipe.NewExecutor(rec, recipe.NewHTTPFetcher(h.deps.PageFetcher)), *src)
			return h.runIterator(ctx, req, src, recipeIter, "recipe")
		} else if rErr != nil {
			log.WithError(rErr).Warn("crawl.request: recipe lookup failed; falling back to connector")
		}
	}
```

placed right after `src` is loaded and validated (before the `conn, ok := h.deps.Registry.Get` line). NOTE: this assumes the page-processing loop (iterate `iter.Next` → archive → verify → emit → page-completed) is extractable into a method `runIterator(ctx, req, src, iter, connType string) error`. If that refactor is larger than a mechanical extraction, STOP and report DONE_WITH_CONCERNS describing the handler's current structure so the controller can scope it — do NOT restructure the handler speculatively.

- [ ] **Step 4: Wire `RecipeRepo` at boot** in `apps/crawler/cmd/main.go`: construct `recipeRepo := repository.NewRecipeRepository(dbFn)` alongside the other repositories, and add `RecipeRepo: recipeRepo,` to the `service.CrawlRequestDeps{...}` literal.

- [ ] **Step 5: Build + test**

Run: `go build ./... && go test ./apps/crawler/... ./pkg/recipe/... ./pkg/connectors/... 2>&1 | tail -20`
Expected: builds; existing crawler tests still pass (no recipe set on any source → unchanged behavior).

- [ ] **Step 6: Commit**

```bash
git add apps/crawler/service/crawl_request_handler.go apps/crawler/cmd/main.go
git commit -m "feat(crawler): route sources with an active recipe through the deterministic Executor"
```

---

## Self-Review
- §5E storage (column + history + atomic swap + rollback) → Task 1. ✓ Cycle avoided (`domain.Source.ExtractionRecipe` is a string; `pkg/repository` marshals).
- §5F integration (production Fetcher, CrawlIterator shim, handler resolution) → Tasks 2-3. ✓
- Placeholder scan: none. Task 3 Step 3 includes an explicit STOP-and-report guard if the handler-loop extraction is non-mechanical (honest about the one place the plan can't fully see).
- Type consistency: `RecipeRepository.Active(ctx, id) (*recipe.Recipe, error)` consumed by Task 3; `recipe.NewExecutor`/`NewHTTPFetcher`/`recipeconn.NewConnectorIterator` signatures match Plans 1-2 + Task 2. `domain.Source.ExtractionRecipe string`.
- Note: Task 1 tests need Docker (testcontainers). Tasks 2 use httptest only. If the `xid` package import differs from `pkg/domain`, match the existing one.
