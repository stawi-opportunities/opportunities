//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimescaleAppendOnlyMigration applies the deployed
// 20260610_0130_timescale_append_only.sql against real TimescaleDB and proves:
// event ledgers reject UPDATE/DELETE at the database level, the crawler
// ledgers get promoted to hypertables in place (data intact), and the
// migration is idempotent (boot job re-runs are no-ops).
func TestTimescaleAppendOnlyMigration(t *testing.T) {
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)

	// Minimal prerequisite shapes (prod has these via earlier migrations /
	// AutoMigrate): one event ledger + the two crawler ledgers with live rows.
	for _, q := range []string{
		`CREATE EXTENSION IF NOT EXISTS timescaledb`,
		`CREATE TABLE match_run_events (
			id VARCHAR(20) NOT NULL, started_at TIMESTAMPTZ NOT NULL,
			detail TEXT, PRIMARY KEY (id, started_at))`,
		`SELECT create_hypertable('match_run_events','started_at', chunk_time_interval => INTERVAL '1 day')`,
		`INSERT INTO match_run_events VALUES ('e1', now(), 'x')`,
		`CREATE TABLE crawl_jobs (
			id VARCHAR(20) NOT NULL, scheduled_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			deleted_at TIMESTAMPTZ, source_id VARCHAR(20) NOT NULL,
			started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ,
			status VARCHAR(20) NOT NULL DEFAULT 'scheduled', attempt INTEGER NOT NULL DEFAULT 1,
			idempotency_key VARCHAR(255) NOT NULL, error_code TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '', jobs_found INTEGER NOT NULL DEFAULT 0,
			jobs_stored INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (id, scheduled_at))`,
		`INSERT INTO crawl_jobs (id, scheduled_at, source_id, idempotency_key)
			VALUES ('j1', now(), 's1', 'k1')`,
		`CREATE TABLE raw_payloads (
			id VARCHAR(20) NOT NULL, fetched_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			deleted_at TIMESTAMPTZ, crawl_job_id VARCHAR(20) NOT NULL,
			source_id VARCHAR(20), source_url TEXT, storage_uri TEXT,
			content_hash VARCHAR(64), size_bytes BIGINT NOT NULL DEFAULT 0,
			http_status INTEGER NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
			attempt_count INTEGER NOT NULL DEFAULT 0, next_retry_at TIMESTAMPTZ,
			last_error TEXT NOT NULL DEFAULT '', PRIMARY KEY (id, fetched_at))`,
		`INSERT INTO raw_payloads (id, fetched_at, crawl_job_id, source_id, source_url, http_status)
			VALUES ('p1', now(), 'j1', 's1', 'https://x.io/job/1', 200)`,
	} {
		_, err := db.ExecContext(ctx, q)
		require.NoError(t, err, q)
	}

	sqlBytes, err := os.ReadFile("../../apps/crawler/migrations/0001/20260610_0130_timescale_append_only.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(sqlBytes))
	require.NoError(t, err, "migration must apply cleanly")

	// Event ledger: append-only enforced at the DATABASE level.
	_, err = db.ExecContext(ctx, `INSERT INTO match_run_events VALUES ('e2', now(), 'y')`)
	assert.NoError(t, err, "INSERT must still work")
	_, err = db.ExecContext(ctx, `UPDATE match_run_events SET detail='hacked' WHERE id='e1'`)
	require.Error(t, err, "UPDATE must be rejected")
	assert.Contains(t, err.Error(), "append-only")
	_, err = db.ExecContext(ctx, `DELETE FROM match_run_events WHERE id='e1'`)
	require.Error(t, err, "DELETE must be rejected")
	_, err = db.ExecContext(ctx, `TRUNCATE match_run_events`)
	require.Error(t, err, "TRUNCATE must be rejected")

	// Crawler ledgers: promoted to hypertables IN PLACE, rows intact, and
	// (deliberately) still mutable — status transitions are their design.
	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM timescaledb_information.hypertables
		  WHERE hypertable_name IN ('crawl_jobs','raw_payloads')`).Scan(&n))
	assert.Equal(t, 2, n, "both ledgers must be hypertables")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM crawl_jobs`).Scan(&n))
	assert.Equal(t, 1, n, "promotion must keep existing rows")
	_, err = db.ExecContext(ctx, `UPDATE crawl_jobs SET status='succeeded' WHERE id='j1'`)
	assert.NoError(t, err, "crawl_jobs stays mutable by design")

	// Idempotency + frame v1.98 constraint: the migrator executes each file as
	// ONE prepared statement (multi-command files fail with SQLSTATE 42601).
	// Prepare-then-exec proves the file is a single command AND a no-op re-run.
	stmt, err := db.PrepareContext(ctx, string(sqlBytes))
	require.NoError(t, err, "file must be preparable as a single statement (frame v1.98 migrator)")
	_, err = stmt.ExecContext(ctx)
	assert.NoError(t, err, "second apply must be a no-op")
	_ = stmt.Close()
}
