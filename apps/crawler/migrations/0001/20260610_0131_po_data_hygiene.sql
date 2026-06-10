-- 20260610_0131: product-opportunities data hygiene.
--
-- Aligns the live DB with the deliberate design in 0130 (pipeline_variants is
-- MUTABLE, retention-bounded, NOT compressed) and closes the remaining
-- data-waste gaps found in the 2026-06 storage/efficiency audit:
--
--   1. Remove the orphan compression policy still attached to pipeline_variants.
--      0130 states it gets NO compression, but an earlier policy lingered:
--      compress_after (7d) == retention drop_after (7d), so it could never
--      compress before the chunk dropped — pure churn (74/106 "failed" runs).
--      No compressed chunks exist, so removal is safe.
--   2. Tighten chunk_time_interval 7d -> 1d so retention drops old data daily
--      (max footprint ~7 days instead of ~2x7d chunks). Affects new chunks only;
--      matches crawl_jobs / raw_payloads from 0130.
--   3. Drop indexes with ~zero scans over 15 days (crawl_job, raw_payload,
--      ingested_at — the last redundant with the PK's 2nd column). Cuts write
--      amplification and index bloat on a hot-UPDATE table.
--   4. Pin aggressive autovacuum on the churned hypertable; relax the tiny,
--      over-vacuumed sources table (was vacuuming a 101-row table ~every 2.5m).
--   5. crawl_inbox TTL safety net. The inbox is drained by the pump and self-
--      limits to <1 day in normal operation; this only fires if the pump stalls,
--      capping the backlog. Abandoned rows are re-crawled on the next cycle.
--      No pg_cron in this cluster, so it runs as a TimescaleDB background job.
--
-- SHAPE NOTE: frame v1.98+ runs each migration file as ONE prepared statement,
-- which cannot contain multiple SQL commands — everything lives in a single DO
-- block with nested EXECUTEs (same pattern as 0130).

DO $mig$
DECLARE
    idx TEXT;
BEGIN
    -- 1. pipeline_variants: remove orphan compression (design: no compression)
    IF to_regclass('pipeline_variants') IS NOT NULL THEN
        PERFORM remove_compression_policy('pipeline_variants', if_exists => TRUE);
        BEGIN
            EXECUTE 'ALTER TABLE pipeline_variants SET (timescaledb.compress = false)';
        EXCEPTION WHEN others THEN
            NULL; -- compression flag was never set; nothing to clear
        END;

        -- 2. tighter bounding for new chunks (7d -> 1d)
        PERFORM set_chunk_time_interval('pipeline_variants', INTERVAL '1 day');

        -- 3. drop ~unused indexes
        FOREACH idx IN ARRAY ARRAY['pipeline_variants_crawl_job_idx',
                                   'pipeline_variants_raw_payload_idx',
                                   'pipeline_variants_ingested_at_idx'] LOOP
            EXECUTE format('DROP INDEX IF EXISTS %I', idx);
        END LOOP;

        -- 4a. autovacuum: keep the churn-heavy hypertable tidy
        EXECUTE 'ALTER TABLE pipeline_variants SET (
                    autovacuum_vacuum_scale_factor  = 0.0,
                    autovacuum_vacuum_threshold      = 50000,
                    autovacuum_analyze_scale_factor = 0.0,
                    autovacuum_analyze_threshold     = 50000,
                    autovacuum_vacuum_cost_limit     = 2000)';
    END IF;

    -- 4b. stop over-vacuuming the tiny sources table
    IF to_regclass('sources') IS NOT NULL THEN
        EXECUTE 'ALTER TABLE sources SET (
                    autovacuum_vacuum_scale_factor  = 0.2,
                    autovacuum_vacuum_threshold      = 500,
                    autovacuum_analyze_scale_factor = 0.1)';
    END IF;

    -- 5. crawl_inbox TTL safety net (runs as a TimescaleDB background job)
    IF to_regclass('crawl_inbox') IS NOT NULL THEN
        EXECUTE $proc$
            CREATE OR REPLACE PROCEDURE crawl_inbox_ttl(job_id INT, config JSONB)
            LANGUAGE plpgsql AS $body$
            BEGIN
                DELETE FROM crawl_inbox WHERE created_at < now() - INTERVAL '3 days';
            END;
            $body$
        $proc$;
        IF NOT EXISTS (
            SELECT 1 FROM timescaledb_information.jobs WHERE proc_name = 'crawl_inbox_ttl'
        ) THEN
            PERFORM add_job('crawl_inbox_ttl', INTERVAL '1 hour');
        END IF;
    END IF;
END
$mig$;
