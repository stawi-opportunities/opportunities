-- 0008: flip sources.status default from 'active' to 'pending'.
--
-- Background: every code path that INSERTs into `sources` already sets
-- status explicitly (seed loader → active; admin POST /sources →
-- pending; source-discovered handler → pending). A row landing in the
-- table without an explicit status would have inherited the original
-- 0001_init default of 'active' — i.e. been crawled immediately
-- without going through the verify-then-approve gate.
--
-- We close that footgun here: any future INSERT path that forgets to
-- set status now lands in 'pending' and waits for an operator. The
-- live application keeps setting status explicitly, so this change is
-- purely defensive.
--
-- Existing rows are unaffected — only the column DEFAULT changes.

ALTER TABLE sources
    ALTER COLUMN status SET DEFAULT 'pending';
