-- Remove TimescaleDB from the crawl database.
-- On Neon (Apache-2 build) the create_hypertable calls and policies always
-- soft-failed, so crawl_jobs and job_ingest_events were never hypertables in
-- prod; the extension was dropped there on 2026-08-31. This file converts the
-- tables where community TimescaleDB did register them (e.g. old test
-- containers), then drops the extension. Clean no-op on databases that never
-- had timescaledb.

DO $mig$
DECLARE
  tbl text;
  idx record;
  is_hyper boolean;
BEGIN
    IF to_regprocedure('append_only_guard()') IS NULL THEN
        EXECUTE $fn$
            CREATE FUNCTION append_only_guard() RETURNS trigger AS $body$
            BEGIN
                RAISE EXCEPTION '% is append-only: % is not allowed', TG_TABLE_NAME, TG_OP;
            END;
            $body$ LANGUAGE plpgsql
        $fn$;
    END IF;

    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
      FOREACH tbl IN ARRAY ARRAY['crawl_jobs', 'job_ingest_events'] LOOP
        EXECUTE format(
          'SELECT EXISTS (SELECT 1 FROM timescaledb_information.hypertables
              WHERE hypertable_schema = ''public'' AND hypertable_name = %L)',
          tbl) INTO is_hyper;
        IF NOT is_hyper THEN
          CONTINUE;
        END IF;

        -- Convert hypertable -> plain table, preserving rows and indexes.
        EXECUTE format('CREATE TABLE %I (LIKE %I INCLUDING ALL)', tbl || '_plain', tbl);
        EXECUTE format('INSERT INTO %I SELECT * FROM %I', tbl || '_plain', tbl);
        EXECUTE format('DROP TABLE %I CASCADE', tbl);
        EXECUTE format('ALTER TABLE %I RENAME TO %I', tbl || '_plain', tbl);

        FOR idx IN
          SELECT indexname FROM pg_indexes
          WHERE schemaname = 'public'
            AND tablename = tbl
            AND indexname LIKE '%\_plain%'
        LOOP
          BEGIN
            EXECUTE format('ALTER INDEX %I RENAME TO %I',
                           idx.indexname, replace(idx.indexname, '_plain', ''));
          EXCEPTION WHEN OTHERS THEN
            RAISE NOTICE 'index rename % skipped: %', idx.indexname, SQLERRM;
          END;
        END LOOP;
      END LOOP;

      -- job_ingest_events is the append-only ledger; triggers are not carried
      -- over by LIKE, so recreate them (no-op reassert when never a hypertable).
      EXECUTE 'DROP TRIGGER IF EXISTS job_ingest_events_append_only ON job_ingest_events';
      EXECUTE 'CREATE TRIGGER job_ingest_events_append_only
                 BEFORE UPDATE OR DELETE ON job_ingest_events
                 FOR EACH ROW EXECUTE FUNCTION append_only_guard()';
      EXECUTE 'DROP TRIGGER IF EXISTS job_ingest_events_no_truncate ON job_ingest_events';
      EXECUTE 'CREATE TRIGGER job_ingest_events_no_truncate
                 BEFORE TRUNCATE ON job_ingest_events
                 FOR EACH STATEMENT EXECUTE FUNCTION append_only_guard()';

      -- crawl_signals reads crawl_jobs/job_ingest_events, so the DROP TABLE
      -- CASCADE above removes it when those were hypertables; recreate it
      -- with the same definition as the 0140 migration.
      IF to_regclass('crawl_signals') IS NULL THEN
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
      END IF;
    END IF;

    BEGIN
      EXECUTE 'DROP EXTENSION IF EXISTS timescaledb';
    EXCEPTION WHEN OTHERS THEN
      RAISE NOTICE 'drop extension timescaledb skipped: %', SQLERRM;
    END;
END
$mig$;
