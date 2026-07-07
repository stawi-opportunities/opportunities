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
	sql, err := os.ReadFile("../../apps/crawler/migrations/0001/20260706_0140_postgres_job_pipeline.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(sql))
	require.NoError(t, err)

	var hypertable bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM timescaledb_information.hypertables WHERE hypertable_name='job_ingest_events')`).Scan(&hypertable))
	require.True(t, hypertable)

	_, err = db.ExecContext(ctx, `INSERT INTO job_ingest_events
		(event_id,ingest_id,variant_id,source_id,event_type,attempt) VALUES ('e1','i1','v1','s1','enqueued',0)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE job_ingest_events SET event_type='changed' WHERE event_id='e1'`)
	require.Error(t, err, "append-only ledger must reject updates")

	_, err = db.ExecContext(ctx, `INSERT INTO opportunities
		(canonical_id,slug,kind,title,apply_url) VALUES ('o1','role','job','Role','')`)
	require.Error(t, err, "canonical opportunities must require apply_url")
}
