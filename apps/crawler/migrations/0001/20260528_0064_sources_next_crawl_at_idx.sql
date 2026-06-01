-- Partial index on next_crawl_at for the scheduler ListDue hot path.
-- WHERE clause excludes NULL rows so the index stays compact on
-- newly-onboarded sources that haven't been scored yet.
CREATE INDEX IF NOT EXISTS sources_next_crawl_at_idx
    ON sources (next_crawl_at)
    WHERE next_crawl_at IS NOT NULL;
