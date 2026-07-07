//go:build integration

package jobqueue_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func TestQueueCapacityLeaseAndAtomicCanonicalMerge(t *testing.T) {
	ctx := context.Background()
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(testSchema).Error)
	pool := func(context.Context, bool) *gorm.DB { return db }
	producer := jobqueue.NewProducer(pool, 1)

	enqueue := func(variant, key, external string) error {
		body, marshalErr := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventsv1.VariantIngestedV1{
			VariantID: variant, SourceID: "source-1", ExternalID: external, HardKey: "same-job", Kind: "job", Title: "Engineer", ApplyURL: "https://example.test/apply/" + external, ScrapedAt: time.Now().UTC(),
		}))
		require.NoError(t, marshalErr)
		return producer.Enqueue(ctx, jobqueue.EnqueueRequest{VariantID: variant, SourceID: "source-1", IdempotencyKey: key, Payload: body})
	}

	require.NoError(t, enqueue("variant-1", "page-1:same-job", "external-1"))
	require.NoError(t, enqueue("variant-duplicate", "page-1:same-job", "external-1"))
	require.ErrorIs(t, enqueue("variant-2", "page-2:same-job", "external-2"), jobqueue.ErrCapacity)

	consumer := jobqueue.New(pool)
	items, err := consumer.Claim(ctx, "worker-1", 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, items, 1)
	_, err = consumer.Complete(ctx, items[0], jobqueue.Canonical{CandidateID: "canonical-1", HardKey: "same-job", Kind: "job", SourceID: "source-1", ExternalID: "external-1", Title: "Engineer", ApplyURL: "https://example.test/apply/external-1", SeenAt: time.Now().UTC(), Attributes: map[string]any{}})
	require.NoError(t, err)

	require.NoError(t, enqueue("variant-2", "page-2:same-job", "external-2"))
	items, err = consumer.Claim(ctx, "worker-2", 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, items, 1)
	_, err = consumer.Complete(ctx, items[0], jobqueue.Canonical{CandidateID: "canonical-2", HardKey: "same-job", Kind: "job", SourceID: "source-1", ExternalID: "external-2", Title: "Senior Engineer", ApplyURL: "https://example.test/apply/external-2", SeenAt: time.Now().UTC(), Attributes: map[string]any{}})
	require.NoError(t, err)

	var opportunities, identities, lineage int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM opportunities`).Scan(&opportunities).Error)
	require.NoError(t, db.Raw(`SELECT count(*) FROM opportunity_identities`).Scan(&identities).Error)
	require.NoError(t, db.Raw(`SELECT count(*) FROM opportunity_sources`).Scan(&lineage).Error)
	require.EqualValues(t, 1, opportunities)
	require.EqualValues(t, 1, identities)
	require.EqualValues(t, 2, lineage)
	var applyURL string
	require.NoError(t, db.Raw(`SELECT apply_url FROM opportunities WHERE canonical_id='canonical-1'`).Scan(&applyURL).Error)
	require.Equal(t, "https://example.test/apply/external-2", applyURL)
	stats, err := consumer.Stats(ctx)
	require.NoError(t, err)
	require.Zero(t, stats.Pending)

	cutoff := time.Now().UTC().Add(time.Hour)
	require.NoError(t, consumer.ReconcileSource(ctx, "source-1", cutoff))
	var hidden bool
	require.NoError(t, db.Raw(`SELECT hidden FROM opportunities WHERE canonical_id='canonical-1'`).Scan(&hidden).Error)
	require.True(t, hidden)
}

const testSchema = `
CREATE TABLE job_ingest_queue (id varchar(20) primary key, variant_id varchar(20) unique not null, source_id varchar(20) not null, crawl_run_id varchar(20), crawl_job_id varchar(20), idempotency_key text unique not null, payload jsonb not null, status varchar(16) not null default 'pending', attempt int not null default 0, available_at timestamptz not null default now(), claimed_at timestamptz, lease_expires_at timestamptz, claimed_by text, last_error text, created_at timestamptz not null default now(), updated_at timestamptz not null default now(), processed_at timestamptz);
CREATE TABLE opportunity_identities (hard_key text primary key, canonical_id varchar(20) unique not null, created_at timestamptz not null default now());
CREATE TABLE opportunities (canonical_id varchar(20) primary key, slug text unique not null, kind text not null, source_id varchar(20), title text not null, description text, issuing_entity text, country text, region text, city text, remote bool, apply_url text not null check (btrim(apply_url) <> ''), posted_at timestamptz, deadline timestamptz, currency text, amount_min double precision, amount_max double precision, employment_type text, seniority text, geo_scope text, status text not null, first_seen_at timestamptz not null, last_seen_at timestamptz not null, attributes jsonb not null default '{}', hidden bool not null default false, hidden_reason text, updated_at timestamptz not null default now());
CREATE TABLE opportunity_sources (canonical_id varchar(20) not null references opportunities(canonical_id), source_id varchar(20) not null, external_id text not null default '', apply_url text not null check (btrim(apply_url) <> ''), content_hash varchar(64), first_seen_at timestamptz not null, last_seen_at timestamptz not null, last_crawl_run_id varchar(20), inactive_after timestamptz, active bool not null, primary key(canonical_id,source_id,external_id));
CREATE TABLE job_ingest_events (event_id varchar(20) not null, occurred_at timestamptz not null default now(), ingest_id varchar(20) not null, variant_id varchar(20) not null, source_id varchar(20) not null, event_type varchar(32) not null, attempt int not null, details jsonb not null, primary key(event_id,occurred_at));`
