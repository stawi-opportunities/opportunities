//go:build integration

package repository_test

import (
	"context"
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
	testhelpers.ApplyGreenfieldSchema(t, ctx, sqlDB)
	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	return g, ctx
}

func dbFn(g *gorm.DB) func(context.Context, bool) *gorm.DB {
	return func(_ context.Context, _ bool) *gorm.DB { return g }
}

func seedSource(t *testing.T, g *gorm.DB, ctx context.Context, id string) {
	t.Helper()
	s := &domain.Source{Type: "schema_org", BaseURL: "https://x.io/" + id, Country: "KE"}
	s.ID = id
	s.Kinds = []string{"job"}
	require.NoError(t, g.WithContext(ctx).Create(s).Error)
}

func sampleRecipe(v int) *recipe.Recipe {
	jl := func(p string) recipe.FieldExtractor {
		return recipe.FieldExtractor{From: []string{"json_ld"}, JSONPath: p}
	}
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
	assert.Equal(t, 2, got.Version)

	hist, err := repo.History(ctx, "src_b")
	require.NoError(t, err)
	require.Len(t, hist, 2)
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
	require.NoError(t, repo.Activate(ctx, "src_c", sampleRecipe(0), 0.8, "m1", nil))
	require.NoError(t, repo.Activate(ctx, "src_c", sampleRecipe(0), 0.9, "m2", nil))

	require.NoError(t, repo.Rollback(ctx, "src_c", 1))
	got, err := repo.Active(ctx, "src_c")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 1, got.Version)
}
