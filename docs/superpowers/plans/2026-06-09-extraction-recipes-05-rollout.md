# Extraction Recipes — Plan 5: Rollout

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`).

**Goal:** Roll the recipe engine out safely — a global `RECIPE_ENABLED` cutover gate so recipes are only used when switched on, and a backfill that queues recipe generation for the existing per-page-LLM ("universal") sources. Plus the documented shadow→cutover→retire sequence.

**Architecture:** The handler already routes a source with a recipe through the Executor (Plan 3); this plan gates that on `RECIPE_ENABLED` and adds `BackfillRecipes`, a pure function that emits `recipe.generate.v1` for recipe-less sources of the targeted types.

**Tech Stack:** Go 1.26, `pkg/recipe`, `pkg/events/v1`, `apps/crawler`.
**Spec:** design §9 (Rollout). **Depends on:** Plans 1-4.

---

## File Structure
| File | Responsibility |
|------|----------------|
| `apps/crawler/service/crawl_request_handler.go` (modify) | gate recipe path on `RecipeEnabled` |
| `apps/crawler/cmd/main.go` (modify) | pass `RecipeEnabled` to deps |
| `apps/crawler/service/recipe_backfill.go` | `BackfillRecipes` — emit generate events for recipe-less universal sources |
| `apps/crawler/service/recipe_backfill_test.go` | tests |

---

## Task 1: `RECIPE_ENABLED` cutover gate

**Files:** modify `crawl_request_handler.go`, `main.go`.

- [ ] **Step 1:** In `crawl_request_handler.go`, add to `CrawlRequestDeps`:

```go
	// RecipeEnabled gates the recipe path globally. When false, sources keep
	// crawling via their registered connector even if they have a recipe — the
	// rollout kill-switch (RECIPE_ENABLED).
	RecipeEnabled bool
```

- [ ] **Step 2:** In `Execute`, change the recipe-path guard so it only fires when enabled. Find:

```go
	if h.deps.RecipeRepo != nil {
		rec, rErr := h.deps.RecipeRepo.Active(ctx, src.ID)
```

and change the condition to:

```go
	if h.deps.RecipeEnabled && h.deps.RecipeRepo != nil {
		rec, rErr := h.deps.RecipeRepo.Active(ctx, src.ID)
```

- [ ] **Step 3:** In `main.go`, add `RecipeEnabled: cfg.RecipeEnabled,` to the `service.CrawlRequestDeps{...}` literal.

- [ ] **Step 4: Extract a testable predicate** so the gate has a unit test. Add to `crawl_request_handler.go`:

```go
// useRecipePath reports whether a crawl should use the deterministic recipe
// Executor: only when the engine is globally enabled and the source has one.
func useRecipePath(enabled bool, rec *recipe.Recipe) bool {
	return enabled && rec != nil
}
```

and use it in the guard: `if h.deps.RecipeEnabled && h.deps.RecipeRepo != nil { rec, rErr := ...; if rErr == nil && useRecipePath(h.deps.RecipeEnabled, rec) { ... } ... }` (the outer `RecipeEnabled` check short-circuits the DB lookup when disabled; `useRecipePath` keeps the decision in one tested place).

- [ ] **Step 5: Test** `crawl_request_handler.go` predicate — create/add to a handler test file:

```go
func TestUseRecipePath(t *testing.T) {
	r := &recipe.Recipe{Acquisition: "api"}
	assert.False(t, useRecipePath(false, r))   // disabled -> connector
	assert.False(t, useRecipePath(true, nil))  // no recipe -> connector
	assert.True(t, useRecipePath(true, r))     // enabled + recipe -> executor
}
```

Add imports `"github.com/stawi-opportunities/opportunities/pkg/recipe"` and testify to the test file (or create `apps/crawler/service/recipe_gate_test.go`).

- [ ] **Step 6:** `go build ./... && go test ./apps/crawler/... && go vet ./...` — green. Commit:

```bash
git add apps/crawler/service/crawl_request_handler.go apps/crawler/cmd/main.go apps/crawler/service/recipe_gate_test.go
git commit -m "feat(crawler): gate the recipe path behind RECIPE_ENABLED (rollout kill-switch)"
```

---

## Task 2: `BackfillRecipes`

**Files:** create `recipe_backfill.go`, `recipe_backfill_test.go`.

- [ ] **Step 1: Failing test** `apps/crawler/service/recipe_backfill_test.go`

```go
package service

import (
	"context"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkSource(id string, typ domain.SourceType, recipe string) domain.Source {
	s := domain.Source{Type: typ, ExtractionRecipe: recipe}
	s.ID = id
	return s
}

func TestBackfillRecipes_QueuesOnlyRecipelessTargetedSources(t *testing.T) {
	sources := []domain.Source{
		mkSource("a", "brightermonday", "{}"),                 // targeted, no recipe -> queue
		mkSource("b", "brightermonday", `{"acquisition":"x"}`), // targeted, HAS recipe -> skip
		mkSource("c", "greenhouse", "{}"),                      // not targeted -> skip
		mkSource("d", "jobberman", ""),                         // targeted, empty recipe -> queue
	}
	targets := map[domain.SourceType]bool{"brightermonday": true, "jobberman": true}

	var queued []string
	emit := func(_ context.Context, sourceID string) error {
		queued = append(queued, sourceID)
		return nil
	}

	n, err := BackfillRecipes(context.Background(), sources, targets, emit)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.ElementsMatch(t, []string{"a", "d"}, queued)
}

func TestBackfillRecipes_EmitErrorStopsAndReports(t *testing.T) {
	sources := []domain.Source{mkSource("a", "brightermonday", "{}")}
	targets := map[domain.SourceType]bool{"brightermonday": true}
	emit := func(_ context.Context, _ string) error { return assert.AnError }
	n, err := BackfillRecipes(context.Background(), sources, targets, emit)
	require.Error(t, err)
	assert.Equal(t, 0, n)
}
```

- [ ] **Step 2:** Run, expect FAIL (undefined BackfillRecipes).

- [ ] **Step 3: Implement** `apps/crawler/service/recipe_backfill.go`

```go
package service

import (
	"context"
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// hasRecipe reports whether a source already carries a non-empty recipe.
func hasRecipe(s domain.Source) bool {
	return s.ExtractionRecipe != "" && s.ExtractionRecipe != "{}"
}

// BackfillRecipes queues recipe generation (via emit) for every source whose
// type is in `targets` and which lacks a recipe. It is the one-off that moves
// existing per-page-LLM ("universal") sources onto generated recipes. Returns
// the count queued; stops and returns the error if emit fails.
func BackfillRecipes(ctx context.Context, sources []domain.Source, targets map[domain.SourceType]bool, emit func(ctx context.Context, sourceID string) error) (int, error) {
	queued := 0
	for _, s := range sources {
		if !targets[s.Type] || hasRecipe(s) {
			continue
		}
		if err := emit(ctx, s.ID); err != nil {
			return queued, fmt.Errorf("backfill: emit %s: %w", s.ID, err)
		}
		queued++
	}
	return queued, nil
}

// UniversalRecipeTargets is the set of source types that currently use the
// per-page-LLM universal connector — the backfill's default targets.
var UniversalRecipeTargets = map[domain.SourceType]bool{
	domain.SourceBrighterMonday:       true,
	domain.SourceJobberman:            true,
	domain.SourceMyJobMag:             true,
	domain.SourceNjorku:               true,
	domain.SourceCareers24:            true,
	domain.SourcePNet:                 true,
	domain.SourceSchemaOrg:            true,
	domain.SourceHostedBoards:         true,
	domain.SourceGenericHTML:          true,
	domain.SourceSmartRecruitersPage:  true,
}
```

(If any `domain.Source*` constant name differs, match the real names in `pkg/domain/models.go`. Drop any that don't exist.)

- [ ] **Step 4:** Run `go test ./apps/crawler/service/ -run TestBackfillRecipes -v` — PASS. Then `go test ./apps/crawler/... && go vet ./... && gofmt -l apps/crawler`.

- [ ] **Step 5:** Commit:

```bash
git add apps/crawler/service/recipe_backfill.go apps/crawler/service/recipe_backfill_test.go
git commit -m "feat(crawler): BackfillRecipes queues recipe generation for recipe-less universal sources"
```

- [ ] **Step 6: Operational note (no code).** The backfill is invoked from an admin endpoint or one-off job that (1) lists sources via `sourceRepo`, (2) calls `BackfillRecipes(ctx, sources, service.UniversalRecipeTargets, emit)` where `emit` publishes `recipe.generate.v1` via `svc.EventsManager().Emit`. Wiring an `/admin/recipes/backfill` endpoint follows the existing `/admin/*` handler pattern in `cmd/main.go`; this plan leaves the trigger to operator tooling and ships the pure, tested function.

---

## Rollout sequence (operational runbook — design §9)
1. **Phase 0:** ship Plans 1-5 with `RECIPE_ENABLED=false` (dormant; migrations are backward-compatible).
2. **Phase 1 — shadow (optional follow-up):** for a few pilot sources, generate a recipe and run the Executor in parallel with the legacy path, logging output diffs but emitting from legacy. (Not in this plan — a `RECIPE_SHADOW` branch in the handler that runs both and compares counts; deferred.)
3. **Phase 2 — cutover:** set `RECIPE_ENABLED=true`. Sources with an active recipe now crawl via the Executor; watch `llm_calls_total` drop and reject_rate/health hold.
4. **Phase 3 — backfill:** invoke `BackfillRecipes` for `UniversalRecipeTargets` to generate recipes for all existing universal sources.
5. **Phase 4 — retire:** once coverage is high and stable, remove the universal AI connectors from `BuildRegistry` (keep the Generator + operator backstop). Tracked as a follow-up PR.

---

## Self-Review
- §9 cutover gate (`RECIPE_ENABLED`) → Task 1 (handler + tested predicate). ✓
- §9 backfill → Task 2 (pure, tested `BackfillRecipes` + default targets). ✓
- §9 shadow + retire → documented as operational follow-ups (shadow is a deferred handler branch; retire is a registry edit) — intentionally not code in this plan. ✓
- Placeholder scan: none. Task 2 Step 3 notes to match real `domain.Source*` constant names.
- Type consistency: `BackfillRecipes(ctx, []domain.Source, map[domain.SourceType]bool, emit) (int, error)`; `useRecipePath(bool, *recipe.Recipe) bool`. `CrawlRequestDeps.RecipeEnabled bool` consumed in the handler guard and set from `cfg.RecipeEnabled`.
