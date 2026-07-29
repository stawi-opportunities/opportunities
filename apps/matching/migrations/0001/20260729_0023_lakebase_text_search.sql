-- Product Neon: BM25 via lakebase_text (not pg_search).
-- Single DO block for simple-protocol compatibility.
-- Requires Lakebase Search enabled on the Neon project for lakebase_bm25.

DO $mig$
BEGIN
  BEGIN
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS lakebase_text';
  EXCEPTION WHEN others THEN
    RAISE NOTICE 'lakebase_text extension skipped: %', SQLERRM;
  END;

  BEGIN
    EXECUTE $q$
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
        ) STORED
    $q$;
  EXCEPTION WHEN others THEN
    RAISE NOTICE 'search_tsv column skipped: %', SQLERRM;
  END;

  BEGIN
    IF NOT EXISTS (
      SELECT 1 FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE c.relname = 'opportunities_search_bm25' AND n.nspname = 'public'
    ) THEN
      EXECUTE $q$
        CREATE INDEX opportunities_search_bm25
          ON opportunities USING lakebase_bm25 (search_tsv)
          WITH (default_limit = 50)
      $q$;
    END IF;
  EXCEPTION WHEN others THEN
    RAISE NOTICE 'opportunities_search_bm25 skipped: %', SQLERRM;
  END;
END
$mig$;
