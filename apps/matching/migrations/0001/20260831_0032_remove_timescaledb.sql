-- Remove TimescaleDB from the matching database.
-- On Neon (Apache-2 build) the compression/retention/CAGG policies always
-- soft-failed, so the hypertables behaved as plain tables with extra catalog
-- baggage. Prod was converted with this same pattern on 2026-08-31; this
-- file repeats it, guarded, for any other environment and is a clean no-op
-- on databases that never had timescaledb.

DO $mig$
DECLARE
  tbl text;
  idx record;
  is_hyper boolean;
BEGIN
    -- Continuous aggregates only ever existed where community TimescaleDB
    -- ran (e.g. old test containers); creation always soft-failed on Neon.
    -- Drop them first: they depend on the hypertables converted below.
    BEGIN
      EXECUTE 'DROP MATERIALIZED VIEW IF EXISTS candidate_match_events_daily CASCADE';
      EXECUTE 'DROP MATERIALIZED VIEW IF EXISTS engagement_events_hourly CASCADE';
    EXCEPTION WHEN OTHERS THEN
      RAISE NOTICE 'continuous aggregate drop skipped: %', SQLERRM;
    END;

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
      FOREACH tbl IN ARRAY ARRAY[
        'candidate_match_events',
        'match_run_events',
        'application_events',
        'engagement_events'
      ] LOOP
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
        EXECUTE format('DROP TABLE %I', tbl);
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

        -- Triggers are not carried over by LIKE; recreate the append-only guards.
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', tbl || '_append_only', tbl);
        EXECUTE format('CREATE TRIGGER %I
                          BEFORE UPDATE OR DELETE ON %I
                          FOR EACH ROW EXECUTE FUNCTION append_only_guard()',
                       tbl || '_append_only', tbl);
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', tbl || '_no_truncate', tbl);
        EXECUTE format('CREATE TRIGGER %I
                          BEFORE TRUNCATE ON %I
                          FOR EACH STATEMENT EXECUTE FUNCTION append_only_guard()',
                       tbl || '_no_truncate', tbl);
      END LOOP;
    END IF;

    BEGIN
      EXECUTE 'DROP EXTENSION IF EXISTS timescaledb';
    EXCEPTION WHEN OTHERS THEN
      RAISE NOTICE 'drop extension timescaledb skipped: %', SQLERRM;
    END;
END
$mig$;
