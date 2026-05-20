//go:build integration

package integration_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// migrationsDir resolves db/migrations relative to this test file.
// (Caller of go test can be in any cwd.)
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..", "db", "migrations")
}

func TestMigrationsApplyCleanly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Boot container without migrations.
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)

	// Create minimal opportunities stub so migration 0012 (which creates
	// an index on it) doesn't fail.
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, db))

	// Apply all migrations.
	testhelpers.ApplyMigrationsDir(t, ctx, db, migrationsDir(t))

	// Hypertables registered with Timescale.
	for _, name := range []string{
		"candidate_match_events",
		"application_events",
		"engagement_events",
		"match_run_events",
	} {
		var count int
		err := db.QueryRowContext(ctx, `
			SELECT count(*)
			FROM timescaledb_information.hypertables
			WHERE hypertable_name = $1
		`, name).Scan(&count)
		require.NoError(t, err, "query hypertables for %s", name)
		require.Equal(t, 1, count, "%s should be a hypertable", name)
	}

	// Retention policy on candidate_match_events.
	var retentionDays int
	err := db.QueryRowContext(ctx, `
		SELECT EXTRACT(DAY FROM (config->>'drop_after')::INTERVAL)::INT
		FROM timescaledb_information.jobs
		WHERE proc_name = 'policy_retention'
		  AND hypertable_name = 'candidate_match_events'
	`).Scan(&retentionDays)
	require.NoError(t, err)
	require.Equal(t, 365, retentionDays)

	// Compression policy on application_events.
	var compressDays int
	err = db.QueryRowContext(ctx, `
		SELECT EXTRACT(DAY FROM (config->>'compress_after')::INTERVAL)::INT
		FROM timescaledb_information.jobs
		WHERE proc_name = 'policy_compression'
		  AND hypertable_name = 'application_events'
	`).Scan(&compressDays)
	require.NoError(t, err)
	require.Equal(t, 14, compressDays)

	// OLTP tables present.
	for _, name := range []string{
		"applications", "application_notes",
		"application_attachments", "application_reminders",
		"match_rules", "candidate_match_indexes", "extension_grants",
	} {
		var exists bool
		err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_name = $1
			)
		`, name).Scan(&exists)
		require.NoError(t, err, "check %s exists", name)
		require.True(t, exists, "%s should exist", name)
	}

	// pgvector extension present.
	var hasVector bool
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')`,
	).Scan(&hasVector))
	require.True(t, hasVector, "pgvector should be enabled")

	// HNSW index on candidate_match_indexes.
	var hnswExists bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE indexname = 'candidate_match_indexes_embedding_hnsw'
		)
	`).Scan(&hnswExists))
	require.True(t, hnswExists, "HNSW index should exist")

	// Partial opportunities index created by migration 0012.
	var idxExists bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE indexname = 'opportunities_active_posted_idx'
		)
	`).Scan(&idxExists))
	require.True(t, idxExists, "opportunities_active_posted_idx should exist")
}
