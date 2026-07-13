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
		mkSource("a", "schema_org", "{}"),
		mkSource("b", "schema_org", `{"acquisition":"x"}`),
		mkSource("c", "workday", "{}"),
		mkSource("d", "generic_html", ""),
	}
	targets := map[domain.SourceType]bool{"schema_org": true, "generic_html": true}

	var queued []string
	emit := func(_ context.Context, sourceID string) error {
		queued = append(queued, sourceID)
		return nil
	}

	n, err := BackfillRecipes(context.Background(), sources, targets, 0, emit)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.ElementsMatch(t, []string{"a", "d"}, queued)
}

func TestBackfillRecipes_EmitErrorStopsAndReports(t *testing.T) {
	sources := []domain.Source{mkSource("a", "schema_org", "{}")}
	targets := map[domain.SourceType]bool{"schema_org": true}
	emit := func(_ context.Context, _ string) error { return assert.AnError }
	n, err := BackfillRecipes(context.Background(), sources, targets, 0, emit)
	require.Error(t, err)
	assert.Equal(t, 0, n)
}

func TestBackfillRecipes_MaxQueueCapsEligibleNotScanned(t *testing.T) {
	// Cap applies to QUEUED sources (post-filter): with maxQueue=2 and an
	// ineligible source first, the two eligible ones still both queue.
	sources := []domain.Source{
		mkSource("x", "workday", "{}"), // not a target — skipped, doesn't consume the cap
		mkSource("a", "schema_org", "{}"),
		mkSource("b", "generic_html", "{}"),
		mkSource("c", "schema_org", "{}"), // over the cap — not queued
	}
	targets := map[domain.SourceType]bool{"schema_org": true, "generic_html": true}
	var queued []string
	emit := func(_ context.Context, id string) error { queued = append(queued, id); return nil }
	n, err := BackfillRecipes(context.Background(), sources, targets, 2, emit)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, []string{"a", "b"}, queued)
}
