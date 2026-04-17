-- 0002_hugo_millions.sql
--
-- Schema changes for the Hugo-millions rebuild:
--   * Add retention/publish fields to canonical_jobs (status, expires_at,
--     r2_version, published_at, category).
--   * Drop the old is_active boolean; Status supersedes it.
--   * Convert search_vector to a GENERATED STORED column so upserts no longer
--     need a raw-SQL UPDATE after insert.
--   * Partial indexes keyed on status='active' for the hot read path.
--   * Materialized view mv_job_facets for live facet counts.

BEGIN;

-- 1. New columns (additive). Defaults make the ALTER cheap on an existing table.
ALTER TABLE canonical_jobs ADD COLUMN IF NOT EXISTS status        TEXT        NOT NULL DEFAULT 'active';
ALTER TABLE canonical_jobs ADD COLUMN IF NOT EXISTS expires_at    TIMESTAMPTZ;
ALTER TABLE canonical_jobs ADD COLUMN IF NOT EXISTS r2_version    INTEGER     NOT NULL DEFAULT 0;
ALTER TABLE canonical_jobs ADD COLUMN IF NOT EXISTS published_at  TIMESTAMPTZ;
ALTER TABLE canonical_jobs ADD COLUMN IF NOT EXISTS category      TEXT;

-- 2. Backfill status from the pre-existing is_active bool, then drop it.
--    (IF EXISTS guards make this safe to re-run.)
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name='canonical_jobs' AND column_name='is_active') THEN
        UPDATE canonical_jobs SET status = 'active'   WHERE is_active IS TRUE  AND status = 'active';
        UPDATE canonical_jobs SET status = 'inactive' WHERE is_active IS FALSE;
    END IF;
END $$;

DROP INDEX IF EXISTS idx_canonical_jobs_is_active;
ALTER TABLE canonical_jobs DROP COLUMN IF EXISTS is_active;

-- 3. Seed expires_at for already-ingested rows: posted_at (or first_seen_at) + 120 days.
UPDATE canonical_jobs
SET expires_at = COALESCE(posted_at, first_seen_at) + INTERVAL '120 days'
WHERE expires_at IS NULL;

-- 4. Convert search_vector to a GENERATED STORED column. This requires dropping
--    the existing column + its GIN index. Partial replacement indexes are
--    created below, keyed on status='active' rather than the whole table.
DROP INDEX IF EXISTS idx_canonical_search;
ALTER TABLE canonical_jobs DROP COLUMN IF EXISTS search_vector;

ALTER TABLE canonical_jobs ADD COLUMN search_vector tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(title,       '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(company,     '')), 'B') ||
        setweight(to_tsvector('simple', coalesce(skills,      '')), 'C') ||
        setweight(to_tsvector('simple', coalesce(description, '')), 'D')
    ) STORED;

-- 5. Backfill category using a rough keyword heuristic that matches
--    domain.DeriveCategory so the Go code and DB stay in sync for existing rows.
UPDATE canonical_jobs SET category = CASE
    WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
        ~ '(developer|engineer|programmer|software|backend|frontend|full-?stack)'
        THEN 'programming'
    WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
        ~ '(designer|ux|ui|graphic|visual)'
        THEN 'design'
    WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
        ~ '(support|customer success|customer service|help desk)'
        THEN 'customer-support'
    WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
        ~ '(marketing|growth|seo|content|social media)'
        THEN 'marketing'
    WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
        ~ '(sales|account executive|business development|revenue)'
        THEN 'sales'
    WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
        ~ '(devops|sre|infrastructure|platform|reliability)'
        THEN 'devops'
    WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
        ~ '(product manager|product owner|product lead)'
        THEN 'product'
    WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
        ~ '(data scientist|data engineer|analyst|machine learning|ai\y)'
        THEN 'data'
    WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
        ~ '(manager|director|vp|head of|chief|lead)'
        THEN 'management'
    ELSE 'other'
END
WHERE category IS NULL;

-- 6. Partial indexes on the hot path.
CREATE INDEX IF NOT EXISTS canonical_jobs_fts
    ON canonical_jobs USING gin(search_vector)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS canonical_jobs_recent
    ON canonical_jobs (posted_at DESC NULLS LAST, id DESC)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS canonical_jobs_category
    ON canonical_jobs (category, posted_at DESC NULLS LAST, id DESC)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS canonical_jobs_remote
    ON canonical_jobs (remote_type, posted_at DESC NULLS LAST, id DESC)
    WHERE status = 'active';

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS canonical_jobs_skills_trgm
    ON canonical_jobs USING gin(skills gin_trgm_ops)
    WHERE status = 'active';

-- 7. Facet materialized view (global counts over active jobs).
DROP MATERIALIZED VIEW IF EXISTS mv_job_facets;
CREATE MATERIALIZED VIEW mv_job_facets AS
    SELECT 'category'::text        AS dim, coalesce(category,        '') AS key, count(*)::bigint AS n
        FROM canonical_jobs WHERE status='active' GROUP BY 1,2
    UNION ALL
    SELECT 'remote_type',     coalesce(remote_type,     ''), count(*)::bigint
        FROM canonical_jobs WHERE status='active' GROUP BY 1,2
    UNION ALL
    SELECT 'employment_type', coalesce(employment_type, ''), count(*)::bigint
        FROM canonical_jobs WHERE status='active' GROUP BY 1,2
    UNION ALL
    SELECT 'seniority',       coalesce(seniority,       ''), count(*)::bigint
        FROM canonical_jobs WHERE status='active' GROUP BY 1,2
    UNION ALL
    SELECT 'country',         coalesce(country,         ''), count(*)::bigint
        FROM canonical_jobs WHERE status='active' GROUP BY 1,2;

CREATE UNIQUE INDEX mv_job_facets_pk ON mv_job_facets(dim, key);

COMMIT;
