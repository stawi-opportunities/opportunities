package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLister struct{ srcs []*domain.Source }

func (f fakeLister) ListByStatuses(_ context.Context, _ []domain.SourceStatus, _ int) ([]*domain.Source, error) {
	return f.srcs, nil
}

func bfSource(id string, typ domain.SourceType, recipe string, tuning bool) *domain.Source {
	s := &domain.Source{Type: typ, ExtractionRecipe: recipe, NeedsTuning: tuning}
	if tuning {
		now := time.Now().UTC()
		s.NeedsTuningAt = &now // freshly flagged — inside the cooldown
	}
	s.ID = id
	s.Status = domain.SourceActive
	return s
}

func TestRecipeBackfillHandler_EnqueuesOnlyEligible(t *testing.T) {
	stale := time.Now().UTC().Add(-NeedsTuningTTL - time.Hour)
	expired := bfSource("f", "generic_html", "{}", true)
	expired.NeedsTuningAt = &stale // flag older than the TTL — re-admitted
	nilStamp := bfSource("g", "generic_html", "{}", true)
	nilStamp.NeedsTuningAt = nil // flagged before the column existed — re-admitted

	lister := fakeLister{srcs: []*domain.Source{
		bfSource("a", "schema_org", "{}", false),                  // eligible -> queue
		bfSource("b", "schema_org", `{"acquisition":"x"}`, false), // has recipe -> skip
		bfSource("c", "greenhouse", "{}", false),                      // not a target type -> skip
		bfSource("d", "generic_html", "{}", true),                        // fresh needs_tuning -> skip
		bfSource("e", "generic_html", "", false),                         // eligible -> queue
		expired,                                                       // expired needs_tuning -> queue
		nilStamp,                                                      // nil NeedsTuningAt stamp -> queue
	}}
	var queued []string
	emit := func(_ context.Context, id string) error { queued = append(queued, id); return nil }

	h := RecipeBackfillHandler(RecipeBackfillDeps{
		Sources: lister, Enabled: true, Targets: RecipeBackfillTargets, Emit: emit, Limit: 100,
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/recipes/backfill", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.ElementsMatch(t, []string{"a", "e", "f", "g"}, queued)
	assert.Contains(t, rec.Body.String(), `"queued":4`)
}

func TestRecipeBackfillHandler_DisabledNoOps(t *testing.T) {
	var queued []string
	emit := func(_ context.Context, id string) error { queued = append(queued, id); return nil }
	h := RecipeBackfillHandler(RecipeBackfillDeps{
		Sources: fakeLister{srcs: []*domain.Source{bfSource("a", "schema_org", "{}", false)}},
		Enabled: false, Targets: RecipeBackfillTargets, Emit: emit,
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/recipes/backfill", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, queued)
	assert.Contains(t, rec.Body.String(), "disabled")
}
