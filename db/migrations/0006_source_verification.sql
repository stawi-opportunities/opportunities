-- db/migrations/0006_source_verification.sql
-- Source-level verification + approval lifecycle.
--
-- New status values are additive — existing rows in 'active', 'degraded',
-- 'paused', 'blocked', 'disabled' keep their meaning. Discovered sources
-- now land in 'pending' (instead of going straight to 'active') and walk
-- through verifying → verified → active under operator control.
--
-- New columns:
--   verification_report  jsonb  — last source-level verification outcome
--   approved_at          timestamptz  — when the source was promoted to active
--   approved_by          varchar(64)  — operator profile_id (or "system" for auto)
--   rejection_reason     text         — explanation when status='rejected'
--   auto_approve         boolean      — promote to active automatically when
--                                       verification passes (operator-trusted)
--
-- GORM AutoMigrate handles all of this when the crawler's migration job
-- runs (DO_DATABASE_MIGRATE=true). This file documents the exact DDL for
-- environments that bypass AutoMigrate (e.g. operators applying schema
-- changes manually or running diagnostic queries before a deploy).

ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS verification_report JSONB,
    ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS approved_by VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS rejection_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS auto_approve BOOLEAN NOT NULL DEFAULT FALSE;

-- No status column rewrite — existing 'active'/'degraded'/etc. rows stay
-- as-is, and the application-side status enum is additive.

-- Index on status to speed the common admin queries (?status=pending,
-- discovered queue listing).
CREATE INDEX IF NOT EXISTS idx_sources_status ON sources (status);
