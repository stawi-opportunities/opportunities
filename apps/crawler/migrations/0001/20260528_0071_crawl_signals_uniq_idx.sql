-- Unique index required for REFRESH MATERIALIZED VIEW CONCURRENTLY.
-- Without this the refresh wrapper falls back to an exclusive lock
-- and the scheduler tick blocks LoadSignals reads for the duration.
CREATE UNIQUE INDEX IF NOT EXISTS crawl_signals_source_id_uniq
    ON crawl_signals (source_id);
