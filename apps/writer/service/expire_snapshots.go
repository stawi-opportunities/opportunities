package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
	"github.com/pitabwire/util"
)

// AppendOnlyTables is the authoritative list of writer-persisted tables
// subject to snapshot retention. Kept here (not in a pkg/ registry) so
// reading this file shows the entire maintenance scope at a glance.
var AppendOnlyTables = [][]string{
	{"jobs", "variants"},
	{"jobs", "canonicals"},
	{"jobs", "canonicals_expired"},
	{"jobs", "embeddings"},
	{"jobs", "translations"},
	{"jobs", "published"},
	{"jobs", "crawl_page_completed"},
	{"jobs", "sources_discovered"},
	{"candidates", "cv_uploaded"},
	{"candidates", "cv_extracted"},
	{"candidates", "cv_improved"},
	{"candidates", "preferences"},
	{"candidates", "embeddings"},
	{"candidates", "matches_ready"},
}

// ExpireSnapshotsConfig tunes retention.
type ExpireSnapshotsConfig struct {
	OlderThan          time.Duration // snapshots older than this are candidates for expiry
	MinSnapshotsToKeep int           // floor: never expire below this count, even if older
	PerTableTimeout    time.Duration // bound each table's expiry at this
	Parallelism        int           // fan-out across tables; default 4
}

// ExpireSnapshotsResult summarises one run.
type ExpireSnapshotsResult struct {
	Tables       int
	FailedTables []string
	ExpiredTotal int // sum of expired snapshots across all tables
}

// ExpireSnapshots iterates AppendOnlyTables and calls
// Transaction.ExpireSnapshots on each via a bounded goroutine pool.
// Per-table failure does NOT abort the run — failing tables are
// collected and reported for operator follow-up.
func ExpireSnapshots(ctx context.Context, cat catalog.Catalog, cfg ExpireSnapshotsConfig) (ExpireSnapshotsResult, error) {
	if cfg.OlderThan <= 0 {
		cfg.OlderThan = 14 * 24 * time.Hour
	}
	if cfg.MinSnapshotsToKeep <= 0 {
		cfg.MinSnapshotsToKeep = 100
	}
	if cfg.PerTableTimeout <= 0 {
		cfg.PerTableTimeout = 5 * time.Minute
	}
	if cfg.Parallelism <= 0 {
		cfg.Parallelism = 4
	}

	type result struct {
		ident   []string
		err     error
		expired int
	}
	jobs := make(chan []string, len(AppendOnlyTables))
	results := make(chan result, len(AppendOnlyTables))

	var wg sync.WaitGroup
	for i := 0; i < cfg.Parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ident := range jobs {
				perTableCtx, cancel := context.WithTimeout(ctx, cfg.PerTableTimeout)
				expired, err := expireOne(perTableCtx, cat, ident, cfg.OlderThan, cfg.MinSnapshotsToKeep)
				cancel()
				results <- result{ident: ident, err: err, expired: expired}
			}
		}()
	}
	for _, ident := range AppendOnlyTables {
		jobs <- ident
	}
	close(jobs)
	wg.Wait()
	close(results)

	var out ExpireSnapshotsResult
	for r := range results {
		out.Tables++
		if r.err != nil {
			out.FailedTables = append(out.FailedTables, fmt.Sprintf("%v: %v", r.ident, r.err))
			util.Log(ctx).WithError(r.err).WithField("ident", r.ident).
				Warn("expire_snapshots: per-table failure")
			continue
		}
		out.ExpiredTotal += r.expired
		util.Log(ctx).
			WithField("ident", r.ident).
			WithField("expired", r.expired).
			Info("expire_snapshots: table ok")
	}
	return out, nil
}

// expireOne loads the table and commits a single ExpireSnapshots transaction.
// Returns the number of snapshots removed (before − after), or an error.
//
// The iceberg-go v0.5.0 API:
//   - table.WithOlderThan(d time.Duration) — mark snapshots older than d as
//     candidates; maps to maxSnapshotAgeMs internally.
//   - table.WithRetainLast(n int) — floor: keep at least n snapshots even if
//     older than OlderThan; maps to minSnapshotsToKeep internally.
//
// Both options are mandatory: ExpireSnapshots returns an error if either
// minSnapshotsToKeep or maxSnapshotAgeMs is nil on the transaction metadata.
func expireOne(ctx context.Context, cat catalog.Catalog, ident []string, olderThan time.Duration, minKeep int) (int, error) {
	tbl, err := cat.LoadTable(ctx, ident)
	if err != nil {
		return 0, fmt.Errorf("load: %w", err)
	}
	before := len(tbl.Metadata().Snapshots())

	txn := tbl.NewTransaction()
	err = txn.ExpireSnapshots(
		table.WithOlderThan(olderThan),
		table.WithRetainLast(minKeep),
	)
	if err != nil {
		return 0, fmt.Errorf("expire: %w", err)
	}
	newTbl, err := txn.Commit(ctx)
	if err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	after := len(newTbl.Metadata().Snapshots())
	if before <= after {
		return 0, nil
	}
	return before - after, nil
}
