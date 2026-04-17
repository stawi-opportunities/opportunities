package repository

import (
	"fmt"

	"gorm.io/gorm"
)

// FinalizeSchema handles the Postgres-specific schema tweaks that GORM's
// AutoMigrate cannot express: dropping legacy columns, converting
// search_vector to a GENERATED STORED column, partial indexes keyed on
// status='active', pg_trgm extension + trigram index, and mv_job_facets.
//
// Call this once immediately after db.AutoMigrate(). Every step is idempotent
// and safe to re-run on every boot.
func FinalizeSchema(db *gorm.DB) error {
	steps := []struct {
		name string
		sql  string
	}{
		// 1. Backfill legacy data before dropping is_active.
		{"backfill status from is_active", `
			DO $$
			BEGIN
				IF EXISTS (SELECT 1 FROM information_schema.columns
				           WHERE table_name='canonical_jobs' AND column_name='is_active') THEN
					UPDATE canonical_jobs SET status='inactive'
					 WHERE is_active IS FALSE AND status='active';
				END IF;
			END $$`},

		{"backfill expires_at (posted_at or first_seen_at + 120d)", `
			UPDATE canonical_jobs
			   SET expires_at = COALESCE(posted_at, first_seen_at) + INTERVAL '120 days'
			 WHERE expires_at IS NULL`},

		{"backfill category via heuristic", `
			UPDATE canonical_jobs SET category = CASE
				WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
					~ '(developer|engineer|programmer|software|backend|frontend|full-?stack)' THEN 'programming'
				WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
					~ '(designer|ux|ui|graphic|visual)' THEN 'design'
				WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
					~ '(support|customer success|customer service|help desk)' THEN 'customer-support'
				WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
					~ '(marketing|growth|seo|content|social media)' THEN 'marketing'
				WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
					~ '(sales|account executive|business development|revenue)' THEN 'sales'
				WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
					~ '(devops|sre|infrastructure|platform|reliability)' THEN 'devops'
				WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
					~ '(product manager|product owner|product lead)' THEN 'product'
				WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
					~ '(data scientist|data engineer|analyst|machine learning|ai\y)' THEN 'data'
				WHEN lower(coalesce(roles,'') || ' ' || coalesce(industry,''))
					~ '(manager|director|vp|head of|chief|lead)' THEN 'management'
				ELSE 'other'
			END
			WHERE category IS NULL OR category=''`},

		// 2. Drop the legacy boolean.
		{"drop legacy is_active index", `DROP INDEX IF EXISTS idx_canonical_jobs_is_active`},
		{"drop legacy is_active column",
			`ALTER TABLE canonical_jobs DROP COLUMN IF EXISTS is_active`},

		// 3. Convert search_vector to GENERATED STORED. We can only do this by
		//    dropping and re-adding the column. Guarded by a column-type check
		//    so re-runs are no-ops.
		{"convert search_vector to generated", `
			DO $$
			DECLARE
				is_generated text;
			BEGIN
				SELECT is_generated INTO is_generated
				  FROM information_schema.columns
				 WHERE table_name='canonical_jobs' AND column_name='search_vector';
				IF is_generated IS DISTINCT FROM 'ALWAYS' THEN
					EXECUTE 'DROP INDEX IF EXISTS idx_canonical_search';
					EXECUTE 'ALTER TABLE canonical_jobs DROP COLUMN IF EXISTS search_vector';
					EXECUTE $gen$
						ALTER TABLE canonical_jobs ADD COLUMN search_vector tsvector
						GENERATED ALWAYS AS (
							setweight(to_tsvector('simple', coalesce(title,       '')), 'A') ||
							setweight(to_tsvector('simple', coalesce(company,     '')), 'B') ||
							setweight(to_tsvector('simple', coalesce(skills,      '')), 'C') ||
							setweight(to_tsvector('simple', coalesce(description, '')), 'D')
						) STORED
					$gen$;
				END IF;
			END $$`},

		// 4. Partial indexes on the hot path (status='active'). GORM can't emit
		//    the WHERE predicate via struct tags.
		{"idx canonical_jobs_fts (partial GIN on search_vector)", `
			CREATE INDEX IF NOT EXISTS canonical_jobs_fts
			  ON canonical_jobs USING gin(search_vector)
			  WHERE status = 'active'`},

		{"idx canonical_jobs_recent (partial, recency)", `
			CREATE INDEX IF NOT EXISTS canonical_jobs_recent
			  ON canonical_jobs (posted_at DESC NULLS LAST, id DESC)
			  WHERE status = 'active'`},

		{"idx canonical_jobs_category (partial, browse)", `
			CREATE INDEX IF NOT EXISTS canonical_jobs_category
			  ON canonical_jobs (category, posted_at DESC NULLS LAST, id DESC)
			  WHERE status = 'active'`},

		{"idx canonical_jobs_remote (partial, browse)", `
			CREATE INDEX IF NOT EXISTS canonical_jobs_remote
			  ON canonical_jobs (remote_type, posted_at DESC NULLS LAST, id DESC)
			  WHERE status = 'active'`},

		// 5. pg_trgm for skills autocomplete (future).
		{"extension pg_trgm", `CREATE EXTENSION IF NOT EXISTS pg_trgm`},

		{"idx canonical_jobs_skills_trgm (fuzzy)", `
			CREATE INDEX IF NOT EXISTS canonical_jobs_skills_trgm
			  ON canonical_jobs USING gin(skills gin_trgm_ops)
			  WHERE status = 'active'`},

		// 6. Facet materialized view. Recomputed every 5 min by the scheduler.
		{"materialized view mv_job_facets", `
			CREATE MATERIALIZED VIEW IF NOT EXISTS mv_job_facets AS
				SELECT 'category'::text AS dim, coalesce(category, '') AS key, count(*)::bigint AS n
				  FROM canonical_jobs WHERE status='active' GROUP BY 1,2
				UNION ALL
				SELECT 'remote_type', coalesce(remote_type, ''), count(*)::bigint
				  FROM canonical_jobs WHERE status='active' GROUP BY 1,2
				UNION ALL
				SELECT 'employment_type', coalesce(employment_type, ''), count(*)::bigint
				  FROM canonical_jobs WHERE status='active' GROUP BY 1,2
				UNION ALL
				SELECT 'seniority', coalesce(seniority, ''), count(*)::bigint
				  FROM canonical_jobs WHERE status='active' GROUP BY 1,2
				UNION ALL
				SELECT 'country', coalesce(country, ''), count(*)::bigint
				  FROM canonical_jobs WHERE status='active' GROUP BY 1,2`},

		{"mv_job_facets unique index", `
			CREATE UNIQUE INDEX IF NOT EXISTS mv_job_facets_pk ON mv_job_facets(dim, key)`},
	}

	for _, s := range steps {
		if err := db.Exec(s.sql).Error; err != nil {
			return fmt.Errorf("finalize schema step %q: %w", s.name, err)
		}
	}
	return nil
}
