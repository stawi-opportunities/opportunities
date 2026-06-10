package service

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/pitabwire/util"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// SourceLister lists sources by status. *repository.SourceRepository satisfies it.
type SourceLister interface {
	ListByStatuses(ctx context.Context, statuses []domain.SourceStatus, limit int) ([]*domain.Source, error)
}

// RecipeBackfillDeps wires the cron-driven recipe-backfill endpoint.
type RecipeBackfillDeps struct {
	Sources SourceLister
	Enabled bool
	Targets map[domain.SourceType]bool
	Emit    func(ctx context.Context, sourceID string) error
	Limit   int
}

// RecipeBackfillHandler is POSTed by the trustage cron
// (definitions/trustage/sources-recipe-backfill.json). It enqueues recipe
// generation (recipe.generate.v1) for active/degraded sources of a recipe-
// eligible type that lack a recipe and are not flagged for operator tuning.
//
// It is idempotent (sources that already have a recipe are skipped) and a no-op
// while recipe generation is disabled, so the cron is always safe to fire. The
// handler only enumerates and enqueues — generation runs on the
// recipe.generate.v1 consumers, so throughput scales by adding crawler replicas.
func RecipeBackfillHandler(deps RecipeBackfillDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		w.Header().Set("Content-Type", "application/json")

		if !deps.Enabled {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "skipped": "recipe generation disabled"})
			return
		}

		// List broadly; deps.Limit caps how many get QUEUED (in BackfillRecipes),
		// not how many are listed — a small list limit would return arbitrary
		// (often recipe-ineligible) sources and queue nothing forever.
		srcs, err := deps.Sources.ListByStatuses(ctx, []domain.SourceStatus{domain.SourceActive, domain.SourceDegraded}, 1000)
		if err != nil {
			util.Log(ctx).WithError(err).Error("recipe-backfill: list sources failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Skip sources flagged for operator tuning (a prior failed generation, or a
		// struggling source) so the cron doesn't re-enqueue them every tick — that
		// would re-fire inference indefinitely. An operator clearing needs_tuning
		// re-admits them on the next tick.
		eligible := make([]domain.Source, 0, len(srcs))
		for _, s := range srcs {
			if s == nil || s.NeedsTuning {
				continue
			}
			eligible = append(eligible, *s)
		}

		queued, err := BackfillRecipes(ctx, eligible, deps.Targets, deps.Limit, deps.Emit)
		if err != nil {
			util.Log(ctx).WithError(err).WithField("queued", queued).Error("recipe-backfill: emit failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		util.Log(ctx).WithField("queued", queued).WithField("scanned", len(eligible)).
			Info("recipe-backfill: enqueued recipe generation")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "queued": queued, "scanned": len(eligible)})
	}
}
