-- Product Neon: catalog serving indexes (embedding ANN + facets).
-- Soft-fail when extensions missing so migrate stays green on local PG.

DO $mig$
BEGIN
  BEGIN
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS vector';
  EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'vector extension skipped: %', SQLERRM;
  END;

  BEGIN
    EXECUTE 'ALTER TABLE opportunities ADD COLUMN IF NOT EXISTS embedding vector(1024)';
  EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'opportunities.embedding skipped: %', SQLERRM;
  END;

  BEGIN
    EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_embedding_hnsw
               ON opportunities USING hnsw (embedding vector_cosine_ops)
               WHERE embedding IS NOT NULL AND hidden=false AND status=''active''';
  EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'opportunities_embedding_hnsw skipped: %', SQLERRM;
  END;

  EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_active_recent_idx
             ON opportunities(last_seen_at DESC) WHERE hidden=false AND status=''active''';
  EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_kind_country_idx
             ON opportunities(kind,country,last_seen_at DESC) WHERE hidden=false AND status=''active''';
  EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_employment_type_idx
             ON opportunities(employment_type) WHERE hidden=false AND status=''active'' AND employment_type IS NOT NULL';
  EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_seniority_idx
             ON opportunities(seniority) WHERE hidden=false AND status=''active'' AND seniority IS NOT NULL';
  EXECUTE 'CREATE INDEX IF NOT EXISTS opportunities_geo_scope_idx
             ON opportunities(geo_scope) WHERE hidden=false AND status=''active'' AND geo_scope IS NOT NULL';
  EXECUTE 'CREATE INDEX IF NOT EXISTS opportunity_sources_source_idx
             ON opportunity_sources (source_id, last_seen_at DESC)';

  -- Drop retired ParadeDB index if present.
  EXECUTE 'DROP INDEX IF EXISTS opportunities_bm25';
END
$mig$;
