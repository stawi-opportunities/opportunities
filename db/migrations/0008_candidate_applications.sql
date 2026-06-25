-- db/migrations/0008_candidate_applications.sql
-- Persistent record of every application submitted on behalf of a candidate.
-- Covers both auto-apply (method='auto') and future manual-submission tracking
-- (method='manual'). The table is write-once for auto-apply: a submitted
-- application is never mutated except when the ATS sends a response webhook.
--
-- Idempotency: the unique partial index on (candidate_id, canonical_job_id)
-- WHERE deleted_at IS NULL means a duplicate auto-apply intent for the same
-- candidate × job returns a constraint violation that the handler maps to a
-- skip rather than a double-submission.
--
-- GORM AutoMigrate handles the table when the autoapply service boots with
-- DO_DATABASE_MIGRATE=true. This file documents the exact DDL for manual
-- apply or diagnostic queries.

CREATE TABLE IF NOT EXISTS candidate_applications (
    id                VARCHAR(20)  PRIMARY KEY,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMPTZ,
    candidate_id      VARCHAR(20)  NOT NULL,
    match_id          VARCHAR(20),
    canonical_job_id  VARCHAR(20)  NOT NULL,
    method            VARCHAR(20)  NOT NULL DEFAULT 'auto',
    status            VARCHAR(20)  NOT NULL DEFAULT 'pending',
    apply_url         TEXT         NOT NULL DEFAULT '',
    cover_letter      TEXT         NOT NULL DEFAULT '',
    submitted_at      TIMESTAMPTZ,
    response_at       TIMESTAMPTZ,
    response_type     VARCHAR(20)  NOT NULL DEFAULT ''
);

-- Fast candidate-scoped queries (dashboard, daily-limit check).
CREATE INDEX IF NOT EXISTS idx_candidate_applications_candidate
    ON candidate_applications (candidate_id);

-- Lookup by originating match row.
CREATE INDEX IF NOT EXISTS idx_candidate_applications_match
    ON candidate_applications (match_id)
    WHERE match_id IS NOT NULL;

-- Partial unique: one active application per candidate × job.
-- Soft-deleted rows are excluded so a previously-deleted application
-- does not block a re-apply.
CREATE UNIQUE INDEX IF NOT EXISTS idx_candidate_applications_unique
    ON candidate_applications (candidate_id, canonical_job_id)
    WHERE deleted_at IS NULL;

-- Supports daily-limit count: submitted_at >= today AND status != 'failed'.
CREATE INDEX IF NOT EXISTS idx_candidate_applications_daily
    ON candidate_applications (candidate_id, submitted_at DESC)
    WHERE submitted_at IS NOT NULL;
