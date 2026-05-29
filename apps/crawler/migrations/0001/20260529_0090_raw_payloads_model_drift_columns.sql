-- Schema drift fix: domain.RawPayload (pkg/domain/models.go) carries
-- source_id, source_url, status, attempt_count, next_retry_at, and
-- last_error, but the raw_payloads table was never migrated to add
-- them. Every GORM full-struct INSERT therefore failed with
-- "column source_id of relation raw_payloads does not exist"
-- (SQLSTATE 42703) — silently, because both the crawler and the
-- frontier-worker treat raw_payload persistence as best-effort. Net
-- effect: raw_payloads had ZERO rows and /admin/raw_payloads/{id}/body
-- never had anything to serve.
--
-- Multiple ADD COLUMN clauses in a single ALTER is one statement, so
-- this is safe under Frame's prepared-statement migration runner (the
-- SQLSTATE 42601 multi-statement trap only applies to semicolon-
-- separated statements in one file). Mirrors the 0041 reparse-audit
-- migration's shape.
ALTER TABLE raw_payloads
    ADD COLUMN IF NOT EXISTS source_id     VARCHAR(20),
    ADD COLUMN IF NOT EXISTS source_url    TEXT,
    ADD COLUMN IF NOT EXISTS status        TEXT        NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS attempt_count INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error    TEXT;
