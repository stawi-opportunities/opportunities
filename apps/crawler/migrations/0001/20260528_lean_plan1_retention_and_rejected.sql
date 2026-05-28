-- lean-postgres-plan1: DELETE the rejected-variant backlog and tighten
-- TimescaleDB retention windows on the operational hypertables.
--
-- publishRejected used to write a terminal-stage row to
-- pipeline_variants for every Verify-failure; the durable record lives
-- in the Iceberg opportunities.variants_rejected table, so the
-- Postgres row was duplicate state dominating the hypertable.
--
-- Retention windows match docs/superpowers/specs/2026-05-28-lean-postgres-design.md:
--   pipeline_variants  7 d  — in-flight ledger; in-progress crawls + recent failures only
--   crawl_jobs        30 d  — source-health investigations (only applied if hypertable)
--   raw_payloads      14 d  — strictly inside R2's 30 d lifecycle (only applied if hypertable)
--
-- crawl_jobs and raw_payloads are currently plain Postgres tables
-- (GORM-managed) — the hypertable promotion is deferred. The retention
-- statements for those tables are guarded by a check against
-- timescaledb_information.hypertables; they no-op until the promotion
-- migration runs, and pick up automatically afterwards.
--
-- All three operations are idempotent (DELETE always re-runnable;
-- remove_retention_policy + add_retention_policy use if_exists /
-- if_not_exists guards).

-- ---------- Clear the rejected backlog ----------

DELETE FROM pipeline_variants WHERE current_stage = 'rejected';

-- ---------- pipeline_variants retention (7 d) ----------

SELECT remove_retention_policy('pipeline_variants', if_exists => TRUE);
SELECT add_retention_policy('pipeline_variants', INTERVAL '7 days',
                            if_not_exists => TRUE);

-- ---------- crawl_jobs retention (30 d) — only if hypertable ----------

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM timescaledb_information.hypertables
    WHERE hypertable_name = 'crawl_jobs'
  ) THEN
    PERFORM remove_retention_policy('crawl_jobs', if_exists => TRUE);
    PERFORM add_retention_policy('crawl_jobs', INTERVAL '30 days',
                                 if_not_exists => TRUE);
  END IF;
END $$;

-- ---------- raw_payloads retention (14 d) — only if hypertable ----------

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM timescaledb_information.hypertables
    WHERE hypertable_name = 'raw_payloads'
  ) THEN
    PERFORM remove_retention_policy('raw_payloads', if_exists => TRUE);
    PERFORM add_retention_policy('raw_payloads', INTERVAL '14 days',
                                 if_not_exists => TRUE);
  END IF;
END $$;
