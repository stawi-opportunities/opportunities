//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupCrawlRunDB(t *testing.T) (*repository.CrawlRunRepository, *gorm.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	// Mirror production: AutoMigrate builds crawl_runs from the model, then the
	// SQL migration file adds the partial unique index (one active run/source).
	require.NoError(t, g.AutoMigrate(&domain.CrawlRun{}))
	require.NoError(t, g.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_crawl_runs_active `+
		`ON crawl_runs (source_id) WHERE status IN ('running','paused')`).Error)
	repo := repository.NewCrawlRunRepository(func(_ context.Context, _ bool) *gorm.DB { return g })
	return repo, g, ctx
}

// ageLease forces a run's lease into the past to simulate a crashed owner.
func ageLease(t *testing.T, g *gorm.DB, id string) {
	t.Helper()
	require.NoError(t, g.Exec(
		`UPDATE crawl_runs SET lease_expires_at = now() - interval '1 hour' WHERE id = ?`, id).Error)
}

func TestCrawlRun_StartRun_SingleFlight(t *testing.T) {
	repo, _, ctx := setupCrawlRunDB(t)
	ttl := 5 * time.Minute

	run, started, err := repo.StartRun(ctx, "src-1", time.Now().UTC(), "owner-A", ttl)
	require.NoError(t, err)
	require.True(t, started)
	require.NotNil(t, run)
	assert.Equal(t, domain.CrawlRunRunning, run.Status)
	assert.Equal(t, "owner-A", run.Owner)

	// A second tick while the run is active is a no-op; it returns the existing
	// run and started=false so the caller drops the duplicate.
	again, started2, err := repo.StartRun(ctx, "src-1", time.Now().UTC(), "owner-B", ttl)
	require.NoError(t, err)
	assert.False(t, started2)
	require.NotNil(t, again)
	assert.Equal(t, run.ID, again.ID)

	// A different source is unaffected.
	other, started3, err := repo.StartRun(ctx, "src-2", time.Now().UTC(), "owner-A", ttl)
	require.NoError(t, err)
	assert.True(t, started3)
	assert.NotEqual(t, run.ID, other.ID)
}

func TestCrawlRun_Claim_Contention(t *testing.T) {
	repo, g, ctx := setupCrawlRunDB(t)
	ttl := 5 * time.Minute

	run, _, err := repo.StartRun(ctx, "src-1", time.Now().UTC(), "owner-A", ttl)
	require.NoError(t, err)

	// owner-B cannot claim while owner-A holds a live lease.
	_, ok, err := repo.Claim(ctx, run.ID, "owner-B", ttl)
	require.NoError(t, err)
	assert.False(t, ok)

	// The lease owner may re-claim (renew).
	_, ok, err = repo.Claim(ctx, run.ID, "owner-A", ttl)
	require.NoError(t, err)
	assert.True(t, ok)

	// After the lease lapses, owner-B wins.
	ageLease(t, g, run.ID)
	claimed, ok, err := repo.Claim(ctx, run.ID, "owner-B", ttl)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "owner-B", claimed.Owner)
}

func TestCrawlRun_Lifecycle(t *testing.T) {
	repo, _, ctx := setupCrawlRunDB(t)
	ttl := 5 * time.Minute

	run, _, err := repo.StartRun(ctx, "src-1", time.Now().UTC(), "owner-A", ttl)
	require.NoError(t, err)

	// Per-page progress persists the cursor and renews the lease.
	cur := json.RawMessage(`{"page":3}`)
	require.NoError(t, repo.Progress(ctx, run.ID, cur, 3, "https://x.io/p3", ttl))
	got, err := repo.GetByID(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, got.PageIdx)
	assert.JSONEq(t, `{"page":3}`, string(got.Cursor))

	// Yield ends a slice with pages remaining: counts the slice, accrues tallies,
	// releases the lease but keeps the run active (so a second tick still drops).
	require.NoError(t, repo.Yield(ctx, run.ID, json.RawMessage(`{"page":4}`), 4, "", ttl, 10, 8, 2))
	got, err = repo.GetByID(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.CrawlRunRunning, got.Status)
	assert.Equal(t, "", got.Owner)
	assert.Equal(t, 1, got.SliceCount)
	assert.Equal(t, 10, got.JobsFound)
	assert.Equal(t, 8, got.JobsEmitted)
	assert.Equal(t, 2, got.JobsRejected)

	_, started, err := repo.StartRun(ctx, "src-1", time.Now().UTC(), "owner-A", ttl)
	require.NoError(t, err)
	assert.False(t, started, "run still active after Yield")

	// Complete frees the source for a fresh pass.
	require.NoError(t, repo.Complete(ctx, run.ID, json.RawMessage(`{"page":5}`), 5, "", 5, 5, 0))
	got, err = repo.GetByID(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.CrawlRunCompleted, got.Status)
	require.NotNil(t, got.CompletedAt)
	assert.Equal(t, 2, got.SliceCount)
	assert.Equal(t, 15, got.JobsFound)

	_, started, err = repo.StartRun(ctx, "src-1", time.Now().UTC(), "owner-A", ttl)
	require.NoError(t, err)
	assert.True(t, started, "completed run frees the source")
}

func TestCrawlRun_Fail_FreesSource(t *testing.T) {
	repo, _, ctx := setupCrawlRunDB(t)
	ttl := 5 * time.Minute

	run, _, err := repo.StartRun(ctx, "src-1", time.Now().UTC(), "owner-A", ttl)
	require.NoError(t, err)
	require.NoError(t, repo.Fail(ctx, run.ID, "stuck", "exceeded attempts", 0, 0, 0))

	got, err := repo.GetByID(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.CrawlRunFailed, got.Status)
	assert.Equal(t, "stuck", got.ErrorCode)

	_, started, err := repo.StartRun(ctx, "src-1", time.Now().UTC(), "owner-A", ttl)
	require.NoError(t, err)
	assert.True(t, started, "failed run frees the source")
}

func TestCrawlRun_FindResumable(t *testing.T) {
	repo, g, ctx := setupCrawlRunDB(t)
	ttl := 5 * time.Minute

	// Actively-leased run — must NOT be resumable.
	live, _, err := repo.StartRun(ctx, "src-live", time.Now().UTC(), "owner-A", ttl)
	require.NoError(t, err)

	// Crashed run — lease in the past — must be resumable.
	dead, _, err := repo.StartRun(ctx, "src-dead", time.Now().UTC(), "owner-B", ttl)
	require.NoError(t, err)
	ageLease(t, g, dead.ID)

	resumable, err := repo.FindResumable(ctx, 100)
	require.NoError(t, err)
	ids := map[string]bool{}
	for _, r := range resumable {
		ids[r.ID] = true
	}
	assert.True(t, ids[dead.ID], "lease-expired run is resumable")
	assert.False(t, ids[live.ID], "actively-leased run is not resumable")

	// TouchLease (watchdog debounce) pushes the lease forward, so the dead run
	// is no longer resumable on the next sweep until the new lease lapses.
	require.NoError(t, repo.TouchLease(ctx, dead.ID, ttl))
	after, err := repo.FindResumable(ctx, 100)
	require.NoError(t, err)
	for _, r := range after {
		assert.NotEqual(t, dead.ID, r.ID, "touched run is debounced out of the resumable set")
	}
}

func TestCrawlRun_ListRuns(t *testing.T) {
	repo, _, ctx := setupCrawlRunDB(t)
	ttl := 5 * time.Minute
	_, _, err := repo.StartRun(ctx, "src-a", time.Now().UTC(), "o", ttl)
	require.NoError(t, err)
	_, _, err = repo.StartRun(ctx, "src-b", time.Now().UTC(), "o", ttl)
	require.NoError(t, err)

	all, err := repo.ListRuns(ctx, "", 100)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	justA, err := repo.ListRuns(ctx, "src-a", 100)
	require.NoError(t, err)
	require.Len(t, justA, 1)
	assert.Equal(t, "src-a", justA[0].SourceID)
}
