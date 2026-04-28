-- db/migrations/0007_opportunity_flags.sql
-- User-facing scam-flagging surface.
--
-- Logged-in users can flag a canonical opportunity as suspicious. Three or
-- more distinct unresolved scam flags from distinct users on the same slug
-- triggers auto-action (OpportunityAutoFlaggedV1 event → materializer drops
-- the row from search by zeroing quality_score and pushing deadline to now).
--
-- The unique index on (opportunity_slug, submitted_by) makes the
-- "same user can only flag the same slug once" rule a hard database
-- constraint — duplicate POSTs return 409 from the handler before
-- they hit a transaction conflict.
--
-- GORM AutoMigrate handles all of this when the migration job runs. This
-- file documents the exact DDL for environments that bypass AutoMigrate
-- (operators applying schema changes manually, or running diagnostic
-- queries before a deploy).

CREATE TABLE IF NOT EXISTS opportunity_flags (
    id                 VARCHAR(20)  PRIMARY KEY,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at         TIMESTAMPTZ,
    opportunity_slug   VARCHAR(255) NOT NULL,
    opportunity_kind   VARCHAR(40)  NOT NULL,
    submitted_by       VARCHAR(64)  NOT NULL,
    reason             VARCHAR(20)  NOT NULL,
    description        TEXT,
    resolved_at        TIMESTAMPTZ,
    resolved_by        VARCHAR(64)  NOT NULL DEFAULT '',
    resolution_action  VARCHAR(20)  NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_opportunity_flags_slug         ON opportunity_flags (opportunity_slug);
CREATE INDEX IF NOT EXISTS idx_opportunity_flags_submitted_by ON opportunity_flags (submitted_by);
CREATE INDEX IF NOT EXISTS idx_opportunity_flags_reason       ON opportunity_flags (reason);
CREATE INDEX IF NOT EXISTS idx_opportunity_flags_deleted_at   ON opportunity_flags (deleted_at);

-- Single-flag-per-user rule. Soft-deleted rows are ignored so a deleted
-- flag history doesn't lock the user out of re-flagging if needed.
CREATE UNIQUE INDEX IF NOT EXISTS idx_flag_slug_user
    ON opportunity_flags (opportunity_slug, submitted_by)
    WHERE deleted_at IS NULL;
