-- Product DB capability SQL for matching.
-- Neon Timescale is Apache-2: no retention/compression policies.
-- Enterprise features are attempted and skipped on feature_not_supported.

DO $mig$
DECLARE
  ent_ok boolean := true;
BEGIN
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS timescaledb';
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS vector';

    EXECUTE 'ALTER TABLE candidate_match_indexes
               ADD COLUMN IF NOT EXISTS embedding vector(1024)';
    -- Only force NOT NULL when table is empty or all rows have embeddings.
    BEGIN
      EXECUTE 'ALTER TABLE candidate_match_indexes ALTER COLUMN embedding SET NOT NULL';
    EXCEPTION WHEN others THEN
      RAISE NOTICE 'embedding NOT NULL skipped: %', SQLERRM;
    END;
    EXECUTE 'CREATE INDEX IF NOT EXISTS candidate_match_indexes_embedding_hnsw
               ON candidate_match_indexes USING hnsw (embedding vector_cosine_ops) WHERE enabled=true';

    BEGIN
      PERFORM create_hypertable('candidate_match_events', 'occurred_at',
                                if_not_exists => TRUE,
                                migrate_data => TRUE,
                                chunk_time_interval => INTERVAL '7 days');
    EXCEPTION WHEN others THEN
      RAISE NOTICE 'hypertable candidate_match_events: %', SQLERRM;
    END;
    EXECUTE 'CREATE INDEX IF NOT EXISTS candidate_match_events_candidate_time_idx
               ON candidate_match_events (candidate_id, occurred_at DESC)';

    BEGIN
      PERFORM create_hypertable('application_events', 'occurred_at',
                                if_not_exists => TRUE,
                                migrate_data => TRUE,
                                chunk_time_interval => INTERVAL '30 days');
    EXCEPTION WHEN others THEN
      RAISE NOTICE 'hypertable application_events: %', SQLERRM;
    END;
    EXECUTE 'CREATE INDEX IF NOT EXISTS application_events_app_time_idx
               ON application_events (application_id, occurred_at DESC)';
    EXECUTE 'CREATE INDEX IF NOT EXISTS application_events_candidate_time_idx
               ON application_events (candidate_id, occurred_at DESC)';

    BEGIN
      PERFORM create_hypertable('engagement_events', 'occurred_at',
                                if_not_exists => TRUE,
                                migrate_data => TRUE,
                                chunk_time_interval => INTERVAL '7 days');
    EXCEPTION WHEN others THEN
      RAISE NOTICE 'hypertable engagement_events: %', SQLERRM;
    END;
    EXECUTE 'CREATE INDEX IF NOT EXISTS engagement_events_opp_time_idx
               ON engagement_events (opportunity_id, occurred_at DESC)';
    EXECUTE 'CREATE INDEX IF NOT EXISTS engagement_events_candidate_time_idx
               ON engagement_events (candidate_id, occurred_at DESC)
               WHERE candidate_id IS NOT NULL';

    BEGIN
      PERFORM create_hypertable('match_run_events', 'started_at',
                                if_not_exists => TRUE,
                                migrate_data => TRUE,
                                chunk_time_interval => INTERVAL '7 days');
    EXCEPTION WHEN others THEN
      RAISE NOTICE 'hypertable match_run_events: %', SQLERRM;
    END;

    -- Community/enterprise Timescale policies — skip on Neon Apache.
    BEGIN
      PERFORM add_retention_policy('candidate_match_events', INTERVAL '365 days', if_not_exists => TRUE);
      PERFORM add_retention_policy('engagement_events', INTERVAL '180 days', if_not_exists => TRUE);
      PERFORM add_retention_policy('match_run_events', INTERVAL '90 days', if_not_exists => TRUE);
    EXCEPTION WHEN feature_not_supported OR undefined_function OR others THEN
      ent_ok := false;
      RAISE NOTICE 'timescale retention policies skipped (Apache/Neon): %', SQLERRM;
    END;

    IF ent_ok THEN
      BEGIN
        EXECUTE 'ALTER TABLE candidate_match_events SET (
                   timescaledb.compress,
                   timescaledb.compress_segmentby = ''candidate_id'')';
        PERFORM add_compression_policy('candidate_match_events', INTERVAL '7 days', if_not_exists => TRUE);
        EXECUTE 'ALTER TABLE application_events SET (
                   timescaledb.compress,
                   timescaledb.compress_segmentby = ''candidate_id'')';
        PERFORM add_compression_policy('application_events', INTERVAL '14 days', if_not_exists => TRUE);
        EXECUTE 'ALTER TABLE engagement_events SET (
                   timescaledb.compress,
                   timescaledb.compress_segmentby = ''opportunity_id'')';
        PERFORM add_compression_policy('engagement_events', INTERVAL '7 days', if_not_exists => TRUE);
        EXECUTE 'ALTER TABLE match_run_events SET (
                   timescaledb.compress,
                   timescaledb.compress_segmentby = ''path'')';
        PERFORM add_compression_policy('match_run_events', INTERVAL '7 days', if_not_exists => TRUE);
      EXCEPTION WHEN feature_not_supported OR undefined_function OR others THEN
        RAISE NOTICE 'timescale compression policies skipped: %', SQLERRM;
      END;
    END IF;

    IF to_regprocedure('append_only_guard()') IS NULL THEN
        EXECUTE $fn$
            CREATE FUNCTION append_only_guard() RETURNS trigger AS $body$
            BEGIN
                RAISE EXCEPTION '% is append-only: % is not allowed', TG_TABLE_NAME, TG_OP;
            END;
            $body$ LANGUAGE plpgsql
        $fn$;
    END IF;

    EXECUTE 'DROP TRIGGER IF EXISTS candidate_match_events_append_only ON candidate_match_events';
    EXECUTE 'CREATE TRIGGER candidate_match_events_append_only
               BEFORE UPDATE OR DELETE ON candidate_match_events
               FOR EACH ROW EXECUTE FUNCTION append_only_guard()';
    EXECUTE 'DROP TRIGGER IF EXISTS candidate_match_events_no_truncate ON candidate_match_events';
    EXECUTE 'CREATE TRIGGER candidate_match_events_no_truncate
               BEFORE TRUNCATE ON candidate_match_events
               FOR EACH STATEMENT EXECUTE FUNCTION append_only_guard()';

    EXECUTE 'DROP TRIGGER IF EXISTS application_events_append_only ON application_events';
    EXECUTE 'CREATE TRIGGER application_events_append_only
               BEFORE UPDATE OR DELETE ON application_events
               FOR EACH ROW EXECUTE FUNCTION append_only_guard()';
    EXECUTE 'DROP TRIGGER IF EXISTS application_events_no_truncate ON application_events';
    EXECUTE 'CREATE TRIGGER application_events_no_truncate
               BEFORE TRUNCATE ON application_events
               FOR EACH STATEMENT EXECUTE FUNCTION append_only_guard()';

    EXECUTE 'DROP TRIGGER IF EXISTS engagement_events_append_only ON engagement_events';
    EXECUTE 'CREATE TRIGGER engagement_events_append_only
               BEFORE UPDATE OR DELETE ON engagement_events
               FOR EACH ROW EXECUTE FUNCTION append_only_guard()';
    EXECUTE 'DROP TRIGGER IF EXISTS engagement_events_no_truncate ON engagement_events';
    EXECUTE 'CREATE TRIGGER engagement_events_no_truncate
               BEFORE TRUNCATE ON engagement_events
               FOR EACH STATEMENT EXECUTE FUNCTION append_only_guard()';

    EXECUTE 'DROP TRIGGER IF EXISTS match_run_events_append_only ON match_run_events';
    EXECUTE 'CREATE TRIGGER match_run_events_append_only
               BEFORE UPDATE OR DELETE ON match_run_events
               FOR EACH ROW EXECUTE FUNCTION append_only_guard()';
    EXECUTE 'DROP TRIGGER IF EXISTS match_run_events_no_truncate ON match_run_events';
    EXECUTE 'CREATE TRIGGER match_run_events_no_truncate
               BEFORE TRUNCATE ON match_run_events
               FOR EACH STATEMENT EXECUTE FUNCTION append_only_guard()';

    -- Continuous aggregates: optional (may require community features).
    BEGIN
      EXECUTE 'CREATE MATERIALIZED VIEW IF NOT EXISTS candidate_match_events_daily
                 WITH (timescaledb.continuous) AS
               SELECT time_bucket(INTERVAL ''1 day'', occurred_at) AS day,
                      candidate_id,
                      count(*) FILTER (WHERE kind = ''generated'') AS matches_generated,
                      count(*) FILTER (WHERE kind = ''dismissed'') AS matches_dismissed,
                      count(*) FILTER (WHERE kind = ''overflow'') AS matches_overflowed
                 FROM candidate_match_events
                GROUP BY day, candidate_id
                 WITH NO DATA';
      PERFORM add_continuous_aggregate_policy('candidate_match_events_daily',
          start_offset => INTERVAL '7 days',
          end_offset => INTERVAL '1 minute',
          schedule_interval => INTERVAL '5 minutes',
          if_not_exists => TRUE);
    EXCEPTION WHEN others THEN
      RAISE NOTICE 'continuous aggregate candidate_match_events_daily skipped: %', SQLERRM;
    END;

    BEGIN
      EXECUTE 'CREATE MATERIALIZED VIEW IF NOT EXISTS engagement_events_hourly
                 WITH (timescaledb.continuous) AS
               SELECT time_bucket(INTERVAL ''1 hour'', occurred_at) AS hour,
                      candidate_id,
                      opportunity_id,
                      kind,
                      count(*) AS event_count
                 FROM engagement_events
                GROUP BY hour, candidate_id, opportunity_id, kind
                 WITH NO DATA';
      PERFORM add_continuous_aggregate_policy('engagement_events_hourly',
          start_offset => INTERVAL '2 days',
          end_offset => INTERVAL '1 minute',
          schedule_interval => INTERVAL '5 minutes',
          if_not_exists => TRUE);
    EXCEPTION WHEN others THEN
      RAISE NOTICE 'continuous aggregate engagement_events_hourly skipped: %', SQLERRM;
    END;
END
$mig$;
