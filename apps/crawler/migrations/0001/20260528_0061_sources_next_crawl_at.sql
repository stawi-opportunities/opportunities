-- Adaptive recrawl: per-source next_crawl_at already exists on the
-- sources table; this is a defensive no-op so an older deployment that
-- somehow dropped the column gets it back. Idempotent: IF NOT EXISTS.
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS next_crawl_at TIMESTAMPTZ;
