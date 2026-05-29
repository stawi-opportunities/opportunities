-- Adaptive recrawl ceiling in minutes. The freshness scheduler never
-- defers a source past max_interval_minutes — every source still gets
-- crawled at least once per ceiling regardless of how cold it scores.
-- Default 7 days for parity with the legacy "weekly drift" cadence.
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS max_interval_minutes INTEGER NOT NULL DEFAULT 10080;
