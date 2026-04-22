-- db/migrations/0003_cutover_drop_legacy.sql
-- Phase 6 greenfield cutover. Drops the Postgres tables that Phases
-- 1-5 replaced with the R2-Parquet event log + Manticore derived view.
-- Idempotent via IF EXISTS so re-runs are safe.
--
-- Tables backed by pkg/repository/job.go, pkg/repository/facets.go,
-- pkg/repository/retention.go, pkg/repository/rejected.go, and
-- pkg/repository/rerank_cache.go have all been deleted from the Go
-- codebase. The corresponding tables below are therefore safe to drop.

BEGIN;

-- Dependent rows first.
DROP TABLE IF EXISTS job_cluster_members CASCADE;
DROP TABLE IF EXISTS job_clusters         CASCADE;
DROP TABLE IF EXISTS job_variants         CASCADE;

-- Materialized view drops before the underlying table.
DROP MATERIALIZED VIEW IF EXISTS mv_job_facets;
DROP TABLE IF EXISTS canonical_jobs CASCADE;

DROP TABLE IF EXISTS crawl_page_states  CASCADE;
DROP TABLE IF EXISTS rerank_cache       CASCADE;
DROP TABLE IF EXISTS rejected_jobs      CASCADE;
DROP TABLE IF EXISTS saved_jobs         CASCADE;

-- Candidate schema trim.
--
-- The CandidateProfile Go struct (pkg/domain/candidate.go) was already
-- updated to match the live Phase 5 schema. The columns listed below are
-- legacy names from older struct revisions that may exist on a long-lived
-- staging or production DB but are absent from the current struct. The
-- IF EXISTS guard makes every ALTER a safe no-op if a column was already
-- dropped or never existed.
--
-- Fields kept (Phase 5 active): id, profile_id, status, subscription,
-- auto_apply, cv_url, cv_raw_text, current_title, seniority,
-- years_experience, skills, strong_skills, working_skills,
-- tools_frameworks, certifications, preferred_roles, industries,
-- education, preferred_locations, preferred_countries, remote_preference,
-- salary_min, salary_max, currency, target_job_title, experience_level,
-- job_search_status, preferred_regions, preferred_timezones,
-- us_work_auth, needs_sponsorship, wants_ats_report, subscription_id,
-- plan_id, languages, bio, work_history, comm_email, comm_whatsapp,
-- comm_telegram, comm_sms, embedding, matches_sent, last_matched_at,
-- last_contacted_at, cv_score, cv_report_json, cv_scored_at,
-- cv_scored_version, created_at, updated_at.
ALTER TABLE candidate_profiles
    DROP COLUMN IF EXISTS cv_raw,
    DROP COLUMN IF EXISTS cv_extracted,
    DROP COLUMN IF EXISTS cv_embedding,
    DROP COLUMN IF EXISTS cv_version,
    DROP COLUMN IF EXISTS preferences,
    DROP COLUMN IF EXISTS target_role,
    DROP COLUMN IF EXISTS excluded_companies,
    DROP COLUMN IF EXISTS score_components,
    DROP COLUMN IF EXISTS priority_fixes;

COMMIT;
