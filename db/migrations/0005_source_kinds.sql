-- db/migrations/0005_source_kinds.sql
-- Phase 4.1 of the Opportunity Generification: every source must declare
-- which opportunity kinds it emits, plus optional per-kind attribute
-- tightening. Existing rows default to ['job'] / '{}' so the migration
-- is fully backward compatible — the crawler keeps producing job records
-- even before any per-source overrides are configured.
--
-- GORM AutoMigrate adds these columns automatically when the crawler's
-- migration job runs (DO_DATABASE_MIGRATE=true). This file documents the
-- exact DDL for environments that bypass AutoMigrate (e.g. operators
-- applying schema changes manually).

ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS kinds TEXT[] NOT NULL DEFAULT ARRAY['job']::TEXT[],
    ADD COLUMN IF NOT EXISTS required_attributes_by_kind JSONB NOT NULL DEFAULT '{}'::JSONB;
