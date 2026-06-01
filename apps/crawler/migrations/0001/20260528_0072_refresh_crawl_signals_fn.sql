-- Wrapper so the scheduler can call REFRESH idempotently. The
-- CONCURRENTLY guarantees a stale-but-readable view during refresh —
-- the scheduler's LoadSignals can run in parallel without locking.
-- Single CREATE OR REPLACE statement, safe under Frame's prepared-
-- statement migration runner.
CREATE OR REPLACE FUNCTION refresh_crawl_signals() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    REFRESH MATERIALIZED VIEW CONCURRENTLY crawl_signals;
END;
$$;
