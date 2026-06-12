-- Retire crawl_checkpoints: the resumable-crawl work (crawl_runs) subsumes it as
-- the single source of truth for resume state, keyed by source_id with a
-- per-source lease. Every source — recipe and legacy connector alike — now
-- resumes from its crawl_runs row. In-flight checkpoints at cutover are dropped;
-- affected sources simply start one fresh pass.
DROP TABLE IF EXISTS crawl_checkpoints;
