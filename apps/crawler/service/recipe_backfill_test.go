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
		mkSource("a", "brightermonday", "{}"),
		mkSource("b", "brightermonday", `{"acquisition":"x"}`),
		mkSource("c", "greenhouse", "{}"),
		mkSource("d", "jobberman", ""),
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
