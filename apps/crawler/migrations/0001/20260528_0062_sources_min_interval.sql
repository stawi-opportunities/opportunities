-- Adaptive recrawl floor in minutes. The freshness scheduler never
-- schedules a source sooner than min_interval_minutes regardless of
-- score; prevents thundering-herd from a just-completed crawl.
-- Default 15m matches the legacy crawl_interval_sec=900 floor.
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS min_interval_minutes INTEGER NOT NULL DEFAULT 15;
