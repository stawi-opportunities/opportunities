-- Product Neon: BM25 via lakebase_text (not pg_search).
-- Requires Lakebase Search enabled on the Neon project and:
--   CREATE EXTENSION IF NOT EXISTS lakebase_text;
-- Applied by matching migrate/setup (product DB owner).

-- Generated tsvector for title + body + issuer + location facets.
ALTER TABLE opportunities
  ADD COLUMN IF NOT EXISTS search_tsv tsvector
  GENERATED ALWAYS AS (
    to_tsvector(
      'english',
      coalesce(title, '') || ' ' ||
      coalesce(description, '') || ' ' ||
      coalesce(issuing_entity, '') || ' ' ||
      coalesce(city, '') || ' ' ||
      coalesce(region, '') || ' ' ||
      coalesce(country, '') || ' ' ||
      coalesce(employment_type, '') || ' ' ||
      coalesce(seniority, '') || ' ' ||
      coalesce(slug, '')
    )
  ) STORED;

-- Build after data load when possible; IF NOT EXISTS for greenfield/re-run.
-- default_limit matches typical API page sizes; override via GUC per query.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relname = 'opportunities_search_bm25' AND n.nspname = 'public'
  ) THEN
    CREATE INDEX opportunities_search_bm25
      ON opportunities USING lakebase_bm25 (search_tsv)
      WITH (default_limit = 50);
  END IF;
EXCEPTION
  WHEN undefined_object THEN
    -- lakebase_text not installed yet; extension bootstrap may run later.
    RAISE NOTICE 'lakebase_text unavailable; skip opportunities_search_bm25 (enable Lakebase Search + CREATE EXTENSION)';
  WHEN OTHERS THEN
    RAISE NOTICE 'opportunities_search_bm25 create skipped: %', SQLERRM;
END $$;
