-- PostgreSQL is the system of record for job ingestion and serving.
-- job_ingest_queue is deliberately mutable: it is a leased work queue.
-- job_ingest_events is deliberately append-only. Operational crawl records
-- are also time-partitioned, while mutable queue and serving state stay on
-- ordinary PostgreSQL tables.
DO $mig$
BEGIN
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS timescaledb';
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS vector';
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS pg_trgm';
    -- ParadeDB full-text search (BM25). No-op if the extension is already
    -- installed cluster-wide; fails soft on images that don't ship it.
    BEGIN
        EXECUTE 'CREATE EXTENSION IF NOT EXISTS pg_search';
    EXCEPTION WHEN OTHERS THEN
        RAISE NOTICE 'pg_search extension unavailable: %', SQLERRM;
    END;

    EXECUTE 'CREATE UNIQUE INDEX IF NOT EXISTS idx_source_recipes_active
               ON source_recipes (source_id) WHERE status = ''active''';
    EXECUTE 'CREATE UNIQUE INDEX IF NOT EXISTS idx_crawl_runs_active
               ON crawl_runs (source_id) WHERE status IN (''running'', ''paused'')';

    PERFORM create_hypertable('crawl_jobs','scheduled_at',if_not_exists=>true,migrate_data=>true,chunk_time_interval=>interval '1 day');
    EXECUTE 'CREATE UNIQUE INDEX IF NOT EXISTS crawl_jobs_idempotency_idx ON crawl_jobs(idempotency_key,scheduled_at)';
    EXECUTE 'CREATE INDEX IF NOT EXISTS crawl_jobs_source_time_idx ON crawl_jobs(source_id,scheduled_at DESC)';
    EXECUTE 'ALTER TABLE crawl_jobs SET (timescaledb.compress,
               timescaledb.compress_segmentby = ''source_id'')';
    PERFORM add_compression_policy('crawl_jobs', interval '14 days', if_not_exists=>true);
    PERFORM add_retention_policy('crawl_jobs',interval '90 days',if_not_exists=>true);

    EXECUTE 'CREATE INDEX IF NOT EXISTS job_ingest_queue_claim_idx
               ON job_ingest_queue (available_at, created_at)
               WHERE status IN (''pending'',''retry'')';
    EXECUTE 'CREATE INDEX IF NOT EXISTS job_ingest_queue_lease_idx
               ON job_ingest_queue (lease_expires_at)
               WHERE status = ''processing''';

    EXECUTE 'ALTER TABLE opportunities ADD COLUMN IF NOT EXISTS embedding vector(1024)';
    -- ANN index for candidate→job reverse KNN / search. Partial: only rows
    -- with embeddings and currently served participate (keeps build smaller).
    EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_embedding_hnsw
               ON opportunities USING hnsw (embedding vector_cosine_ops)
               WHERE embedding IS NOT NULL AND hidden=false AND status=''active''';
    -- ParadeDB pg_search BM25 for GET /api/search?q=… (apps/api jobsPostgres.Search).
    -- Requires CREATE EXTENSION pg_search (shipped on the CNPG image).
    EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_bm25 ON opportunities
               USING bm25 (canonical_id, title, description, issuing_entity, slug, city, region, country, employment_type, seniority)
               WITH (key_field=''canonical_id'')';
    EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_active_recent_idx ON opportunities(last_seen_at DESC) WHERE hidden=false AND status=''active''';
    EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_kind_country_idx ON opportunities(kind,country,last_seen_at DESC) WHERE hidden=false AND status=''active''';
    EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_employment_type_idx ON opportunities(employment_type) WHERE hidden=false AND status=''active'' AND employment_type IS NOT NULL';
    EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_seniority_idx ON opportunities(seniority) WHERE hidden=false AND status=''active'' AND seniority IS NOT NULL';
    EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_geo_scope_idx ON opportunities(geo_scope) WHERE hidden=false AND status=''active'' AND geo_scope IS NOT NULL';
    EXECUTE 'CREATE INDEX IF NOT EXISTS opportunity_sources_source_idx
               ON opportunity_sources (source_id, last_seen_at DESC)';
    PERFORM create_hypertable('job_ingest_events', 'occurred_at',
                              if_not_exists => TRUE,
                              migrate_data => TRUE,
                              chunk_time_interval => INTERVAL '1 day');
    EXECUTE 'CREATE INDEX IF NOT EXISTS job_ingest_events_ingest_time_idx
               ON job_ingest_events (ingest_id, occurred_at DESC)';
    EXECUTE 'ALTER TABLE job_ingest_events SET (timescaledb.compress,
               timescaledb.compress_segmentby = ''source_id,event_type'')';
    PERFORM add_compression_policy('job_ingest_events', INTERVAL '7 days', if_not_exists => TRUE);
    PERFORM add_retention_policy('job_ingest_events', INTERVAL '90 days', if_not_exists => TRUE);

    IF to_regprocedure('append_only_guard()') IS NULL THEN
        EXECUTE $fn$
            CREATE FUNCTION append_only_guard() RETURNS trigger AS $body$
            BEGIN
                RAISE EXCEPTION '% is append-only: % is not allowed', TG_TABLE_NAME, TG_OP;
            END;
            $body$ LANGUAGE plpgsql
        $fn$;
    END IF;
    EXECUTE 'DROP TRIGGER IF EXISTS job_ingest_events_append_only ON job_ingest_events';
    EXECUTE 'CREATE TRIGGER job_ingest_events_append_only
               BEFORE UPDATE OR DELETE ON job_ingest_events
               FOR EACH ROW EXECUTE FUNCTION append_only_guard()';
    EXECUTE 'DROP TRIGGER IF EXISTS job_ingest_events_no_truncate ON job_ingest_events';
    EXECUTE 'CREATE TRIGGER job_ingest_events_no_truncate
               BEFORE TRUNCATE ON job_ingest_events
               FOR EACH STATEMENT EXECUTE FUNCTION append_only_guard()';
    EXECUTE $proc$
        CREATE OR REPLACE PROCEDURE job_ingest_queue_cleanup(job_id integer, config jsonb)
        LANGUAGE plpgsql AS $body$
        BEGIN
            DELETE FROM job_ingest_queue
            WHERE (status='processed' AND processed_at < now()-interval '30 days')
               OR (status IN ('dead','rejected') AND updated_at < now()-interval '90 days');
        END
        $body$
    $proc$;
    IF NOT EXISTS (SELECT 1 FROM timescaledb_information.jobs WHERE proc_name='job_ingest_queue_cleanup') THEN
        PERFORM add_job('job_ingest_queue_cleanup', interval '1 hour');
    END IF;

    EXECUTE 'DROP MATERIALIZED VIEW IF EXISTS crawl_signals CASCADE';
    EXECUTE $sql$
        CREATE MATERIALIZED VIEW crawl_signals AS
        WITH crawls AS (
            SELECT source_id, count(*) FILTER (WHERE started_at >= now()-interval '7 days') AS crawls_7d
            FROM crawl_jobs GROUP BY source_id
        ), queued AS (
            SELECT source_id,
                   count(*) FILTER (WHERE created_at >= now()-interval '7 days') AS variants_7d,
                   count(*) FILTER (WHERE created_at >= now()-interval '7 days' AND status='processed') AS accepted_7d,
                   max(created_at) FILTER (WHERE created_at >= now()-interval '7 days') AS last_new_variant_at
            FROM job_ingest_queue GROUP BY source_id
        ), rejected AS (
            SELECT source_id, count(*) AS rejected_7d FROM job_ingest_events
            WHERE event_type='rejected' AND occurred_at >= now()-interval '7 days' GROUP BY source_id
        )
        SELECT s.id AS source_id, COALESCE(c.crawls_7d,0) AS crawls_7d,
               COALESCE(q.variants_7d,0) AS variants_7d, COALESCE(q.accepted_7d,0) AS accepted_7d,
               COALESCE(r.rejected_7d,0) AS rejected_7d, q.last_new_variant_at
        FROM sources s LEFT JOIN crawls c ON c.source_id=s.id
        LEFT JOIN queued q ON q.source_id=s.id LEFT JOIN rejected r ON r.source_id=s.id
    $sql$;
    EXECUTE 'CREATE UNIQUE INDEX IF NOT EXISTS crawl_signals_source_id_idx ON crawl_signals(source_id)';
    EXECUTE $fn$
        CREATE OR REPLACE FUNCTION refresh_crawl_signals() RETURNS void LANGUAGE sql AS $body$
            REFRESH MATERIALIZED VIEW CONCURRENTLY crawl_signals
        $body$
    $fn$;
END
$mig$;
