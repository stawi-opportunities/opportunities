-- 20260610_0130: database-level append-only enforcement + TimescaleDB
-- promotion for the crawler's time-series ledgers.
--
-- Append-only contract, enforced by trigger (not convention):
--   candidate_match_events, application_events, engagement_events,
--   match_run_events — pure event ledgers; no code path mutates them and
--   none may. UPDATE/DELETE/TRUNCATE raise. TimescaleDB retention still
--   works: drop_chunks removes whole chunks and does not fire row triggers.
--
-- Deliberately MUTABLE (NOT append-only, by design — do not add triggers):
--   pipeline_variants  — stage ledger, CAS UPDATEs (AdvanceStage et al).
--   crawl_jobs         — status transitions scheduled→running→succeeded.
--   raw_payloads       — enricher queue-state column.
-- Their UPDATEs land on fresh rows only, so the 14-day compression window
-- below never collides with writes. pipeline_variants gets NO compression
-- policy (its backlog rows can be updated arbitrarily late).
--
-- Also promotes crawl_jobs + raw_payloads to hypertables IN PLACE
-- (supersedes the orphaned db/migrations/0019, which assumed empty tables
-- and drop-recreated; prod has live rows now).
--
-- SHAPE NOTE: frame v1.98+ executes each migration file as ONE prepared
-- statement, which cannot contain multiple SQL commands — hence everything
-- below lives in a single DO block with nested EXECUTEs.

DO $mig$
DECLARE
    t TEXT;
BEGIN
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS timescaledb';

    -- ---------- append-only guard ----------
    EXECUTE $fn$
        CREATE OR REPLACE FUNCTION append_only_guard() RETURNS trigger AS $body$
        BEGIN
            RAISE EXCEPTION '% is append-only: % is not allowed', TG_TABLE_NAME, TG_OP
                USING HINT = 'event ledgers are immutable; retention runs via drop_chunks';
        END;
        $body$ LANGUAGE plpgsql
    $fn$;

    FOREACH t IN ARRAY ARRAY['candidate_match_events','application_events',
                             'engagement_events','match_run_events'] LOOP
        IF to_regclass(t) IS NULL THEN
            CONTINUE; -- table owned by another service's migrations; skip if absent
        END IF;
        EXECUTE format('DROP TRIGGER IF EXISTS %I_append_only ON %I', t, t);
        EXECUTE format(
            'CREATE TRIGGER %I_append_only BEFORE UPDATE OR DELETE ON %I
               FOR EACH ROW EXECUTE FUNCTION append_only_guard()', t, t);
        EXECUTE format('DROP TRIGGER IF EXISTS %I_no_truncate ON %I', t, t);
        EXECUTE format(
            'CREATE TRIGGER %I_no_truncate BEFORE TRUNCATE ON %I
               FOR EACH STATEMENT EXECUTE FUNCTION append_only_guard()', t, t);
    END LOOP;

    -- ---------- crawl_jobs: in-place hypertable promotion ----------
    IF to_regclass('crawl_jobs') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM timescaledb_information.hypertables
        WHERE hypertable_name = 'crawl_jobs'
    ) THEN
        -- Hypertables need the partition column in every unique constraint.
        -- GORM AutoMigrate declared the composite PK (id, scheduled_at); rebuild
        -- defensively in case an environment carries the legacy (id) PK.
        IF NOT EXISTS (
            SELECT 1 FROM pg_constraint c
            WHERE c.conrelid = 'crawl_jobs'::regclass AND c.contype = 'p'
              AND (SELECT array_agg(a.attname ORDER BY a.attname)
                   FROM unnest(c.conkey) k JOIN pg_attribute a
                     ON a.attrelid = c.conrelid AND a.attnum = k)
                  = ARRAY['id','scheduled_at']::name[]
        ) THEN
            EXECUTE 'ALTER TABLE crawl_jobs DROP CONSTRAINT IF EXISTS crawl_jobs_pkey';
            EXECUTE 'ALTER TABLE crawl_jobs ADD PRIMARY KEY (id, scheduled_at)';
        END IF;
        -- Plain unique indexes that lack the partition column block promotion.
        EXECUTE 'DROP INDEX IF EXISTS idx_crawl_jobs_idempotency_key';
        EXECUTE 'DROP INDEX IF EXISTS crawl_jobs_idempotency_idx';
        PERFORM create_hypertable('crawl_jobs', 'scheduled_at',
                                  if_not_exists => TRUE,
                                  migrate_data => TRUE,
                                  chunk_time_interval => INTERVAL '1 day');
        EXECUTE 'CREATE UNIQUE INDEX IF NOT EXISTS crawl_jobs_idempotency_idx
                   ON crawl_jobs (idempotency_key, scheduled_at)';
        EXECUTE 'CREATE INDEX IF NOT EXISTS crawl_jobs_source_time_idx
                   ON crawl_jobs (source_id, scheduled_at DESC)';
        PERFORM add_retention_policy('crawl_jobs', INTERVAL '90 days', if_not_exists => TRUE);
        EXECUTE 'ALTER TABLE crawl_jobs SET (timescaledb.compress,
                                             timescaledb.compress_segmentby = ''source_id'')';
        PERFORM add_compression_policy('crawl_jobs', INTERVAL '14 days', if_not_exists => TRUE);
    END IF;

    -- ---------- raw_payloads: in-place hypertable promotion ----------
    IF to_regclass('raw_payloads') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM timescaledb_information.hypertables
        WHERE hypertable_name = 'raw_payloads'
    ) THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_constraint c
            WHERE c.conrelid = 'raw_payloads'::regclass AND c.contype = 'p'
              AND (SELECT array_agg(a.attname ORDER BY a.attname)
                   FROM unnest(c.conkey) k JOIN pg_attribute a
                     ON a.attrelid = c.conrelid AND a.attnum = k)
                  = ARRAY['fetched_at','id']::name[]
        ) THEN
            EXECUTE 'ALTER TABLE raw_payloads DROP CONSTRAINT IF EXISTS raw_payloads_pkey';
            EXECUTE 'ALTER TABLE raw_payloads ADD PRIMARY KEY (id, fetched_at)';
        END IF;
        PERFORM create_hypertable('raw_payloads', 'fetched_at',
                                  if_not_exists => TRUE,
                                  migrate_data => TRUE,
                                  chunk_time_interval => INTERVAL '1 day');
        EXECUTE 'CREATE INDEX IF NOT EXISTS raw_payloads_pending_idx
                   ON raw_payloads (next_retry_at NULLS FIRST, fetched_at)
                   WHERE status = ''pending''';
        EXECUTE 'CREATE INDEX IF NOT EXISTS raw_payloads_status_time_idx
                   ON raw_payloads (status, fetched_at DESC)';
        EXECUTE 'CREATE INDEX IF NOT EXISTS raw_payloads_source_time_idx
                   ON raw_payloads (source_id, fetched_at DESC)';
        EXECUTE 'CREATE INDEX IF NOT EXISTS raw_payloads_crawl_job_idx
                   ON raw_payloads (crawl_job_id, fetched_at DESC)';
        PERFORM add_retention_policy('raw_payloads', INTERVAL '90 days', if_not_exists => TRUE);
        EXECUTE 'ALTER TABLE raw_payloads SET (timescaledb.compress,
                                               timescaledb.compress_segmentby = ''source_id'')';
        PERFORM add_compression_policy('raw_payloads', INTERVAL '14 days', if_not_exists => TRUE);
    END IF;
END
$mig$;
