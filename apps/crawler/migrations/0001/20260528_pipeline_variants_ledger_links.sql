-- pipeline-robustness-phase1: forward-link pipeline_variants to the
-- crawler audit ledger (crawl_jobs + raw_payloads).
--
-- These two nullable columns let consumers trace a variant back to
-- the HTML page (raw_payloads) and crawl execution (crawl_jobs)
-- it originated from. Nullable because (a) the pre-ledger backlog
-- has neither value, and (b) tests construct variants directly.
--
-- The crawl_jobs + raw_payloads hypertable promotion is intentionally
-- deferred to a follow-up — promoting those plain tables to
-- TimescaleDB hypertables requires a destructive DROP TABLE +
-- CREATE TABLE (composite PK requirement), so it's handled in a
-- separate change window.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS + CREATE INDEX IF NOT EXISTS.

ALTER TABLE pipeline_variants
    ADD COLUMN IF NOT EXISTS raw_payload_id VARCHAR(20),
    ADD COLUMN IF NOT EXISTS crawl_job_id   VARCHAR(20);

CREATE INDEX IF NOT EXISTS pipeline_variants_raw_payload_idx
    ON pipeline_variants (raw_payload_id) WHERE raw_payload_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS pipeline_variants_crawl_job_idx
    ON pipeline_variants (crawl_job_id) WHERE crawl_job_id IS NOT NULL;
