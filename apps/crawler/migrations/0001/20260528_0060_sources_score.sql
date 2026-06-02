-- Adaptive recrawl: per-source freshness score in [0.0, 1.0].
-- 1.0 = crawl at min_interval; 0.0 = crawl at max_interval. Computed
-- every scheduler tick from the crawl_signals materialized view.
-- Default 0.5 (neutral) so first-crawl sources land midway.
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS score DOUBLE PRECISION NOT NULL DEFAULT 0.5;
