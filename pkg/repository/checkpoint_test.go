//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// setupCheckpointDB boots Postgres and AutoMigrates just the
// Checkpoint struct. The checkpoint store is standalone (no FKs into
// sources/crawl_jobs) so we don't need the full trace-test stub.
func setupCheckpointDB(t *testing.T) (testhelpers.PoolFn, context.Context) {
	t.Helper()
	ctx := context.Background()
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, g.AutoMigrate(&repository.Checkpoint{}))
	return func(_ context.Context, _ bool) *gorm.DB { return g }, ctx
}

func TestCheckpoint_PutGet(t *testing.T) {
	pool, ctx := setupCheckpointDB(t)
	repo := repository.NewCheckpointRepository(pool)

	require.NoError(t, repo.Put(ctx, "src-cp-1", "greenhouse", []byte(`{"after":"foo"}`), 3, "https://x"))

	cp, stale, err := repo.Get(ctx, "src-cp-1", "greenhouse")
	require.NoError(t, err)
	require.NotNil(t, cp, "fresh checkpoint not returned")
	require.False(t, stale, "fresh checkpoint reported stale")
	require.Equal(t, 3, cp.PageIdx)
	require.Equal(t, "https://x", cp.LastURL)
}

func TestCheckpoint_StaleAfter(t *testing.T) {
	pool, ctx := setupCheckpointDB(t)
	repo := repository.NewCheckpointRepository(pool)
	repo.StaleAfter = time.Millisecond

	require.NoError(t, repo.Put(ctx, "src-cp-stale", "x", []byte(`{}`), 0, ""))
	time.Sleep(5 * time.Millisecond)

	_, stale, err := repo.Get(ctx, "src-cp-stale", "x")
	require.NoError(t, err)
	require.True(t, stale, "expected stale=true after StaleAfter window")
}

func TestCheckpoint_Clear(t *testing.T) {
	pool, ctx := setupCheckpointDB(t)
	repo := repository.NewCheckpointRepository(pool)

	require.NoError(t, repo.Put(ctx, "src-cp-clr", "x", []byte(`{}`), 0, ""))
	require.NoError(t, repo.Clear(ctx, "src-cp-clr", "x"))

	cp, _, err := repo.Get(ctx, "src-cp-clr", "x")
	require.NoError(t, err)
	require.Nil(t, cp, "Clear didn't remove the row")
}

func TestCheckpoint_ListBySource(t *testing.T) {
	pool, ctx := setupCheckpointDB(t)
	repo := repository.NewCheckpointRepository(pool)

	require.NoError(t, repo.Put(ctx, "src-list-1", "greenhouse", []byte(`{}`), 1, ""))
	require.NoError(t, repo.Put(ctx, "src-list-1", "workday", []byte(`{}`), 2, ""))
	require.NoError(t, repo.Put(ctx, "src-list-2", "greenhouse", []byte(`{}`), 1, ""))

	rows, err := repo.ListBySource(ctx, "src-list-1")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "greenhouse", rows[0].ConnectorType)
	require.Equal(t, "workday", rows[1].ConnectorType)
}
