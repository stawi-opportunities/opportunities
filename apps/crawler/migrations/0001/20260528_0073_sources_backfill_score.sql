-- One-shot backfill so existing sources have a sane starting score +
-- next_crawl_at after the D3 columns land. Two-pronged:
--   - score defaults to 0.5 (neutral) — the scheduler will overwrite
--     on the next tick once crawl_signals is populated.
--   - next_crawl_at falls back to (last_seen_at OR now()) + the legacy
--     crawl_interval_sec so the scheduler doesn't queue every existing
--     source at the same moment after the migration applies.
--
-- Only touches rows where the columns are NULL / unset; idempotent.
UPDATE sources
SET    score = 0.5,
       next_crawl_at = COALESCE(last_seen_at, now())
                       + make_interval(secs => GREATEST(crawl_interval_sec, 60))
WHERE  next_crawl_at IS NULL
   OR  next_crawl_at = 'epoch'::timestamptz;
