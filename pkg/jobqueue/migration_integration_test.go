//go:build integration

package jobqueue_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func TestPostgresPipelineMigration(t *testing.T) {
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gormDB.AutoMigrate(
		&domain.Source{},
		&domain.CrawlJob{},
		&domain.CrawlRun{},
		&repository.SourceRecipe{},
		&jobqueue.QueueRecord{},
		&jobqueue.OpportunityRecord{},
		&jobqueue.OpportunityIdentityRecord{},
		&jobqueue.OpportunitySourceRecord{},
		&jobqueue.IngestEventRecord{},
	))
	for _, file := range []string{
		"../../apps/crawler/migrations/0001/20260706_0140_postgres_job_pipeline.sql",
		"../../apps/crawler/migrations/0001/20260831_0023_remove_timescaledb.sql",
	} {
		sql, err := os.ReadFile(file)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(sql))
		require.NoError(t, err, "apply %s", file)
	}

	// job_ingest_events is a plain append-only table; timescaledb is gone.
	var hasTimescale bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_extension WHERE extname='timescaledb')`).Scan(&hasTimescale))
	require.False(t, hasTimescale, "timescaledb extension should not be installed")

	var triggers int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM pg_trigger
		WHERE tgrelid = 'job_ingest_events'::regclass
		  AND tgname IN ('job_ingest_events_append_only','job_ingest_events_no_truncate')`).Scan(&triggers))
	require.Equal(t, 2, triggers, "append-only triggers should exist")

	_, err = db.ExecContext(ctx, `INSERT INTO job_ingest_events
		(event_id,ingest_id,variant_id,source_id,event_type,attempt) VALUES ('e1','i1','v1','s1','enqueued',0)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE job_ingest_events SET event_type='changed' WHERE event_id='e1'`)
	require.Error(t, err, "append-only ledger must reject updates")

	_, err = db.ExecContext(ctx, `INSERT INTO opportunities
		(canonical_id,slug,kind,title,apply_url) VALUES ('o1','role','job','Role','')`)
	require.Error(t, err, "canonical opportunities must require apply_url")
}
