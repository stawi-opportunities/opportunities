//go:build integration

package frontier_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// setupFrontierDB boots Postgres and AutoMigrates the queue +
// politeness tables. The frontier store is standalone — no FKs
// — so we don't need the wider migration set.
func setupFrontierDB(t *testing.T) (frontier.PoolFn, context.Context) {
	t.Helper()
	ctx := context.Background()
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, g.AutoMigrate(frontier.Schema()...))
	// canonical_url_hash unique index is the dedup safeguard the
	// SQL migration ships. AutoMigrate doesn't replicate the
	// index from tags here, so the test fixture creates it
	// explicitly to mirror production.
	require.NoError(t, g.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS url_frontier_canonical_hash_uniq
		    ON url_frontier (canonical_url_hash)`).Error)
	return func(_ context.Context, _ bool) *gorm.DB { return g }, ctx
}

func mustEnqueue(t *testing.T, f frontier.Frontier, ctx context.Context, urls ...frontier.URL) []frontier.EnqueueResult {
	t.Helper()
	res, err := f.Enqueue(ctx, urls)
	require.NoError(t, err)
	return res
}

func TestEnqueue_DedupsOnCanonicalHash(t *testing.T) {
	pool, ctx := setupFrontierDB(t)
	f := frontier.NewPostgresFrontier(pool)

	u := frontier.URL{CanonicalURL: "https://example.com/a", SourceID: "src1", Priority: 0.5}
	res1 := mustEnqueue(t, f, ctx, u)
	require.Len(t, res1, 1)
	require.True(t, res1[0].Inserted, "first enqueue should insert")

	u.Priority = 0.9 // bump priority on re-enqueue
	res2 := mustEnqueue(t, f, ctx, u)
	require.Len(t, res2, 1)
	require.False(t, res2[0].Inserted, "dup should not insert")
	require.Equal(t, res1[0].URLID, res2[0].URLID, "dup should keep the same url_id")
}

func TestDequeue_HighestPriorityFirst(t *testing.T) {
	pool, ctx := setupFrontierDB(t)
	f := frontier.NewPostgresFrontier(pool)

	// Use distinct hosts so per-host politeness doesn't shadow ordering.
	mustEnqueue(t, f, ctx,
		frontier.URL{CanonicalURL: "https://a.example.com/x", SourceID: "s1", Priority: 0.2},
		frontier.URL{CanonicalURL: "https://b.example.com/y", SourceID: "s1", Priority: 0.9},
		frontier.URL{CanonicalURL: "https://c.example.com/z", SourceID: "s1", Priority: 0.5},
	)

	out, err := f.Dequeue(ctx, 3, "worker-1")
	require.NoError(t, err)
	require.Len(t, out, 3)
	require.Equal(t, "https://b.example.com/y", out[0].CanonicalURL)
	require.Equal(t, "https://c.example.com/z", out[1].CanonicalURL)
	require.Equal(t, "https://a.example.com/x", out[2].CanonicalURL)
}

func TestDequeue_RespectsPoliteness(t *testing.T) {
	pool, ctx := setupFrontierDB(t)
	f := frontier.NewPostgresFrontier(pool)

	// Two URLs on the same host. Default window is 1 minute, so
	// after the first claim the second should be skipped.
	mustEnqueue(t, f, ctx,
		frontier.URL{CanonicalURL: "https://same.example.com/p1", SourceID: "s1", Priority: 0.9},
		frontier.URL{CanonicalURL: "https://same.example.com/p2", SourceID: "s1", Priority: 0.8},
	)

	first, err := f.Dequeue(ctx, 5, "w-a")
	require.NoError(t, err)
	require.Len(t, first, 1, "concurrency_max=1 + window > 0 should claim only one")
	require.Equal(t, "https://same.example.com/p1", first[0].CanonicalURL)

	// Even with a different worker, the host's next_eligible_at
	// is now in the future. Second Dequeue returns nothing until
	// the window elapses or the host's concurrency frees.
	second, err := f.Dequeue(ctx, 5, "w-b")
	require.NoError(t, err)
	require.Len(t, second, 0, "host politeness window should block the second URL")

	// Complete the first; that frees concurrency_now but the
	// next_eligible_at is still in the future, so the second
	// claim is still blocked.
	require.NoError(t, f.Complete(ctx, first[0].URLID))
	third, err := f.Dequeue(ctx, 5, "w-b")
	require.NoError(t, err)
	require.Len(t, third, 0, "host politeness window still in effect after complete")
}

func TestDequeue_SkipsInFlight(t *testing.T) {
	pool, ctx := setupFrontierDB(t)
	f := frontier.NewPostgresFrontier(pool)

	mustEnqueue(t, f, ctx,
		frontier.URL{CanonicalURL: "https://a.example.com/x", SourceID: "s1", Priority: 0.7},
		frontier.URL{CanonicalURL: "https://b.example.com/y", SourceID: "s1", Priority: 0.5},
	)
	first, err := f.Dequeue(ctx, 1, "w1")
	require.NoError(t, err)
	require.Len(t, first, 1)

	// Second dequeue must not re-claim the in_flight row.
	second, err := f.Dequeue(ctx, 2, "w2")
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.NotEqual(t, first[0].URLID, second[0].URLID)
}

func TestFail_BackoffThenFailedTerminal(t *testing.T) {
	pool, ctx := setupFrontierDB(t)
	f := frontier.NewPostgresFrontier(pool)
	// Shrink backoff so the test stays snappy. The retry math is
	// (2^(attempts-1)) * BackoffBase; we just want a non-MaxBackoff
	// value to verify it sets next_attempt_at in the future.
	f.BackoffBase = 100 * time.Millisecond
	f.MaxBackoff = 5 * time.Second

	mustEnqueue(t, f, ctx,
		frontier.URL{CanonicalURL: "https://flaky.example.com/x", SourceID: "s1", Priority: 0.5},
	)

	// Claim → fail with retries available.
	pick, err := f.Dequeue(ctx, 1, "w1")
	require.NoError(t, err)
	require.Len(t, pick, 1)
	require.NoError(t, f.Fail(ctx, pick[0].URLID, errors.New("boom"), 3))

	// After Fail with retries, state should be pending again and
	// next_attempt_at should be in the future.
	var got struct {
		State         string     `gorm:"column:state"`
		Attempts      int        `gorm:"column:attempts"`
		NextAttemptAt *time.Time `gorm:"column:next_attempt_at"`
	}
	require.NoError(t, pool(ctx, true).
		Table("url_frontier").
		Where("url_id = ?", pick[0].URLID).
		Take(&got).Error)
	require.Equal(t, "pending", got.State)
	require.Equal(t, 1, got.Attempts)
	require.NotNil(t, got.NextAttemptAt)
	require.True(t, got.NextAttemptAt.After(time.Now().UTC()), "next_attempt_at should be in the future")

	// Drive the row to attempts == max. We need to fast-forward
	// next_attempt_at past now so Dequeue can claim it again
	// (otherwise the politeness gate would also delay us).
	require.NoError(t, pool(ctx, false).
		Exec(`UPDATE url_frontier SET next_attempt_at = NULL WHERE url_id = ?`, pick[0].URLID).Error)
	// Reset host_state so politeness doesn't block re-dequeue.
	require.NoError(t, pool(ctx, false).
		Exec(`UPDATE host_state SET next_eligible_at = NULL, concurrency_now = 0`).Error)

	pick2, err := f.Dequeue(ctx, 1, "w1")
	require.NoError(t, err)
	require.Len(t, pick2, 1)
	require.NoError(t, f.Fail(ctx, pick2[0].URLID, errors.New("boom2"), 2))

	require.NoError(t, pool(ctx, true).
		Table("url_frontier").
		Where("url_id = ?", pick[0].URLID).
		Take(&got).Error)
	require.Equal(t, "failed", got.State, "attempts == max should land in failed terminal state")
}

func TestComplete_FreesConcurrencyAndStampsDone(t *testing.T) {
	pool, ctx := setupFrontierDB(t)
	f := frontier.NewPostgresFrontier(pool)

	mustEnqueue(t, f, ctx,
		frontier.URL{CanonicalURL: "https://ok.example.com/x", SourceID: "s1", Priority: 0.5},
	)
	pick, err := f.Dequeue(ctx, 1, "w1")
	require.NoError(t, err)
	require.Len(t, pick, 1)
	require.NoError(t, f.Complete(ctx, pick[0].URLID))

	var state string
	require.NoError(t, pool(ctx, true).Raw(
		`SELECT state FROM url_frontier WHERE url_id = ?`, pick[0].URLID,
	).Scan(&state).Error)
	require.Equal(t, "done", state)

	var concurrency int
	require.NoError(t, pool(ctx, true).Raw(
		`SELECT concurrency_now FROM host_state WHERE host = ?`, pick[0].Host,
	).Scan(&concurrency).Error)
	require.Equal(t, 0, concurrency, "complete should decrement concurrency_now")
}

// TestDequeue_NoDoubleClaimUnderRace is the critical correctness
// gate: spawn N workers racing against M URLs (on distinct hosts
// so politeness doesn't constrain throughput), insist exactly M
// rows ever transition to in_flight, and that no two workers ever
// see the same url_id.
func TestDequeue_NoDoubleClaimUnderRace(t *testing.T) {
	pool, ctx := setupFrontierDB(t)
	f := frontier.NewPostgresFrontier(pool)

	const numURLs = 80
	const numWorkers = 8

	// Each URL on its own host (politeness gate wide open).
	urls := make([]frontier.URL, 0, numURLs)
	for i := 0; i < numURLs; i++ {
		urls = append(urls, frontier.URL{
			CanonicalURL: fmt.Sprintf("https://host-%d.example.com/p", i),
			SourceID:     "s-race",
			Priority:     0.5,
		})
	}
	mustEnqueue(t, f, ctx, urls...)

	var (
		mu      sync.Mutex
		claimed = map[string]int{}
		totalOk uint64
		wg      sync.WaitGroup
	)

	wg.Add(numWorkers)
	for w := 0; w < numWorkers; w++ {
		go func(workerID string) {
			defer wg.Done()
			for {
				batch, err := f.Dequeue(ctx, 4, workerID)
				if err != nil {
					t.Errorf("worker %s dequeue: %v", workerID, err)
					return
				}
				if len(batch) == 0 {
					return
				}
				mu.Lock()
				for _, u := range batch {
					claimed[u.URLID]++
				}
				mu.Unlock()
				for _, u := range batch {
					if err := f.Complete(ctx, u.URLID); err != nil {
						t.Errorf("complete %s: %v", u.URLID, err)
					}
				}
				atomic.AddUint64(&totalOk, uint64(len(batch)))
			}
		}(fmt.Sprintf("w-%d", w))
	}
	wg.Wait()

	// Every URL was claimed exactly once.
	require.Equal(t, numURLs, len(claimed), "all URLs should be claimed")
	for id, n := range claimed {
		require.Equal(t, 1, n, "URL %s claimed %d times — double-claim regression", id, n)
	}

	// All rows should be in state 'done' — no orphan in_flight.
	var inFlight int64
	require.NoError(t, pool(ctx, true).Raw(
		`SELECT count(*) FROM url_frontier WHERE state = 'in_flight'`,
	).Scan(&inFlight).Error)
	require.Equal(t, int64(0), inFlight, "no rows should remain in_flight after all workers drain")

	var done int64
	require.NoError(t, pool(ctx, true).Raw(
		`SELECT count(*) FROM url_frontier WHERE state = 'done'`,
	).Scan(&done).Error)
	require.Equal(t, int64(numURLs), done, "every URL should be done")
}

func TestHostOf(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://Example.com/x", "example.com"},
		{"https://example.com:8080/x", "example.com"},
		{"http://EXAMPLE.com", "example.com"},
		{"", ""},
		{"not a url", ""},
		{"http://", ""},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, frontier.HostOf(tc.in), tc.in)
	}
}
