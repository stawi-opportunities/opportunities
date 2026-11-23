//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func TestGreenfieldSchemaAppliesCleanly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyGreenfieldSchema(t, ctx, db)

	// Event tables are plain append-only Postgres tables with guard triggers.
	for _, name := range []string{
		"candidate_match_events",
		"application_events",
		"engagement_events",
		"match_run_events",
	} {
		var exists bool
		err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)
		`, name).Scan(&exists)
		require.NoError(t, err, "check %s exists", name)
		require.True(t, exists, "%s should exist", name)

		var triggers int
		err = db.QueryRowContext(ctx, `
			SELECT count(*)
			FROM pg_trigger
			WHERE tgrelid = $1::regclass
			  AND tgname IN ($1 || '_append_only', $1 || '_no_truncate')
		`, name).Scan(&triggers)
		require.NoError(t, err, "query triggers for %s", name)
		require.Equal(t, 2, triggers, "%s should have append-only + no-truncate triggers", name)
	}

	// TimescaleDB is gone.
	var hasTimescale bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`,
	).Scan(&hasTimescale)
	require.NoError(t, err)
	require.False(t, hasTimescale, "timescaledb extension should not be installed")

	// OLTP tables present.
	for _, name := range []string{
		"applications", "application_notes",
		"application_attachments", "application_reminders",
		"match_rules", "candidate_match_indexes",
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

	// Partial opportunities index created by the crawler capability migration.
	var idxExists bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE indexname = 'opportunities_active_recent_idx'
		)
	`).Scan(&idxExists))
	require.True(t, idxExists, "opportunities_active_recent_idx should exist")

	// Matching OLTP table created by GORM.
	var cnt int
	err = db.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.tables
     WHERE table_schema='public' AND table_name='candidate_matches'`).Scan(&cnt)
	require.NoError(t, err)
	require.Equal(t, 1, cnt, "candidate_matches should exist post-0013")

	var matchPairIndex bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE tablename='candidate_matches'
			  AND indexname='candidate_matches_pair_uniq'
		)`).Scan(&matchPairIndex))
	require.True(t, matchPairIndex, "(candidate_id, opportunity_id) UNIQUE should exist")

	// The old continuous aggregates are gone (removed with TimescaleDB).
	err = db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_class
     WHERE relname IN ('candidate_match_events_daily', 'engagement_events_hourly')`).Scan(&cnt)
	require.NoError(t, err)
	require.Equal(t, 0, cnt, "continuous aggregates should not exist post-0032")
}

// 0028 must not destroy CV contact data on a database where it is still
// pending (production's migrate Job did not run for months): the legacy
// columns are archived, inserts that no longer mention them keep working,
// and re-running is a no-op.
func TestLegacyContactColumnsAreArchivedNotDropped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyGreenfieldSchema(t, ctx, db)

	// Recreate the pre-0028 production shape with data in it (the greenfield
	// replay above already ran 0028, so remove its archive columns first).
	_, err := db.ExecContext(ctx, `
		ALTER TABLE candidate_profiles DROP COLUMN IF EXISTS legacy_phone_unmigrated;
		ALTER TABLE candidate_profiles DROP COLUMN IF EXISTS legacy_emails_unmigrated;
		ALTER TABLE candidate_profiles ADD COLUMN phone text NOT NULL DEFAULT '';
		ALTER TABLE candidate_profiles ADD COLUMN IF NOT EXISTS emails text NOT NULL DEFAULT '';
		INSERT INTO candidate_profiles (id, phone, emails) VALUES ('cand-1', '+254700000001', 'a@x.test,b@x.test');`)
	require.NoError(t, err)

	path := filepath.Join("..", "..", "apps", "matching", "migrations", "0001", "20260808_0028_drop_legacy_contact_columns.sql")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	for i := 0; i < 2; i++ { // idempotent
		_, err = db.ExecContext(ctx, string(body))
		require.NoError(t, err, "run %d", i+1)
	}

	var phone, emails string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT legacy_phone_unmigrated, legacy_emails_unmigrated FROM candidate_profiles WHERE id='cand-1'`).Scan(&phone, &emails))
	require.Equal(t, "+254700000001", phone)
	require.Equal(t, "a@x.test,b@x.test", emails)

	var stillThere int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name='candidate_profiles' AND column_name IN ('phone','emails')`).Scan(&stillThere))
	require.Zero(t, stillThere, "application-facing columns are gone")

	// Current code inserts without the legacy columns.
	_, err = db.ExecContext(ctx, `INSERT INTO candidate_profiles (id) VALUES ('cand-2')`)
	require.NoError(t, err)
}
