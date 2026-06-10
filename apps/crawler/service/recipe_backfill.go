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

// BackfillRecipes queues recipe generation (via emit) for sources whose type is
// in `targets` and which lack a recipe, stopping after maxQueue emits
// (maxQueue <= 0 means unlimited). The cap is applied to QUEUED sources — after
// the target/has-recipe filters — so a small cap paces generation under an
// inference rate limit without starving on ineligible sources. Returns the
// count queued; stops and returns the error if emit fails.
func BackfillRecipes(ctx context.Context, sources []domain.Source, targets map[domain.SourceType]bool, maxQueue int, emit func(ctx context.Context, sourceID string) error) (int, error) {
	queued := 0
	for _, s := range sources {
		if !targets[s.Type] || hasRecipe(s) {
			continue
		}
		if err := emit(ctx, s.ID); err != nil {
			return queued, fmt.Errorf("backfill: emit %s: %w", s.ID, err)
		}
		queued++
		if maxQueue > 0 && queued >= maxQueue {
			break
		}
	}
	return queued, nil
}

// UniversalRecipeTargets is the set of source types that currently use the
// per-page-LLM universal connector — the backfill's default targets.
var UniversalRecipeTargets = map[domain.SourceType]bool{
	domain.SourceBrighterMonday:      true,
	domain.SourceJobberman:           true,
	domain.SourceMyJobMag:            true,
	domain.SourceNjorku:              true,
	domain.SourceCareers24:           true,
	domain.SourcePNet:                true,
	domain.SourceSchemaOrg:           true,
	domain.SourceHostedBoards:        true,
	domain.SourceGenericHTML:         true,
	domain.SourceSmartRecruitersPage: true,
}
