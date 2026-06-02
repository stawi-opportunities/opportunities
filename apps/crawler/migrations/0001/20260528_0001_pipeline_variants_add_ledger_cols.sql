-- pipeline-robustness-phase1: forward-link pipeline_variants to the
-- crawler audit ledger. Single ALTER with two ADD COLUMN clauses is
-- one PostgreSQL statement (one prepared exec).
--
-- Both columns nullable: the pre-ledger backlog has neither value,
-- and tests construct variants directly.

ALTER TABLE pipeline_variants
    ADD COLUMN IF NOT EXISTS raw_payload_id VARCHAR(20),
    ADD COLUMN IF NOT EXISTS crawl_job_id   VARCHAR(20);
