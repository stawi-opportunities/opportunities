-- 0012: composite partial index supporting the fan-out worker's
-- "active visible opportunities posted recently" filter (spec §2.3).
-- Partial index keeps it tiny by excluding hidden + non-active rows
-- (the vast majority over time).

CREATE INDEX IF NOT EXISTS opportunities_active_posted_idx
    ON opportunities (posted_at DESC)
    WHERE status = 'active' AND hidden = FALSE;
