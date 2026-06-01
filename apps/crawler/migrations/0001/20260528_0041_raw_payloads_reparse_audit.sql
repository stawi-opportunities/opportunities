-- Reparse-audit columns. Operators can re-run extraction on a
-- previously-fetched HTML page via POST /admin/raw_payloads/{id}/reparse;
-- these columns record how many times that has happened and when last.
-- Used by the source-trace UI to surface "this source has had N
-- reparse operations in the last 24h".

ALTER TABLE raw_payloads
    ADD COLUMN IF NOT EXISTS reparse_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_reparsed_at TIMESTAMPTZ;
