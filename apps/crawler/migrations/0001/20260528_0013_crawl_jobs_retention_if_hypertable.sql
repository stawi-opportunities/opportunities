-- lean-plan1: 30 d retention on crawl_jobs IF (and only if) the
-- table has been promoted to a TimescaleDB hypertable. The
-- promotion is deferred to a follow-up — this guard makes the
-- retention policy land automatically as soon as that runs.
--
-- DO block is ONE PostgreSQL statement (single Exec).

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM timescaledb_information.hypertables
    WHERE hypertable_name = 'crawl_jobs'
  ) THEN
    PERFORM remove_retention_policy('crawl_jobs', if_exists => TRUE);
    PERFORM add_retention_policy('crawl_jobs', INTERVAL '30 days', if_not_exists => TRUE);
  END IF;
END $$;
