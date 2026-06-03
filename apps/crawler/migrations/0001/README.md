-- Reference SQL for the crawler-owned schema (NOT applied directly).
--
-- The actual application of SQL changes happens via the timestamped
-- *.sql files in this directory through frame's pool.Migrate; this
-- file documents the canonical post-migration shape of every table
-- the crawler owns end-to-end so a reader can see the whole picture
-- without replaying the deltas in order.
--
-- Mirrors the trustage migrations/0001/migration.sql convention.
--
-- Two GORM-managed tables (sources, raw_refs) are NOT described here
-- because GORM AutoMigrate is the source of truth for those — see
-- repository.Migrate in pkg/repository/migrate.go.

-- ===========================================================================
-- Extensions (loaded via shared_preload_libraries on the CNPG cluster;
-- these statements just expose the SQL surface).
-- ===========================================================================
CREATE EXTENSION IF NOT EXISTS timescaledb;
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS vectorscale CASCADE;
CREATE EXTENSION IF NOT EXISTS pg_search;

-- ===========================================================================
-- pipeline_variants — work ledger (TimescaleDB hypertable).
--
-- One row per crawler-emitted variant. Each chain stage UPDATEs
-- current_stage atomically (CAS pattern). hard_key indexed for
-- dedup lookups.
--
-- Hypertable partitioned by ingested_at; chunks compressed at 7d,
-- dropped at 7d retention (lean-postgres-plan1 tightened from 90d).
--
-- The composite (variant_id, ingested_at) PK is structural —
-- TimescaleDB requires the partition column in every unique
-- constraint. variant_id is xid-generated so the tuple is unique in
-- practice.
--
-- raw_payload_id + crawl_job_id forward-link a variant to the audit
-- ledger (pipeline-robustness-phase1).
-- ===========================================================================
CREATE TABLE pipeline_variants (
  variant_id        TEXT NOT NULL,
  source_id         TEXT NOT NULL,
  hard_key          TEXT NOT NULL,
  kind              TEXT NOT NULL,
  current_stage     TEXT NOT NULL DEFAULT 'ingested',
  canonical_id      TEXT,
  slug              TEXT,
  raw_content_hash  TEXT,
  raw_payload_id    VARCHAR(20),
  crawl_job_id      VARCHAR(20),
  ingested_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  stage_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  attempts          JSONB NOT NULL DEFAULT '{}'::jsonb,
  last_error        TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (variant_id, ingested_at)
);

SELECT create_hypertable(
  'pipeline_variants',
  by_range('ingested_at', INTERVAL '7 days')
);

ALTER TABLE pipeline_variants SET (
  timescaledb.compress,
  timescaledb.compress_segmentby = 'source_id,kind,current_stage',
  timescaledb.compress_orderby = 'ingested_at DESC, variant_id'
);
SELECT add_compression_policy('pipeline_variants', INTERVAL '7 days');
SELECT add_retention_policy('pipeline_variants', INTERVAL '7 days');

CREATE INDEX pipeline_variants_in_flight_idx
  ON pipeline_variants (current_stage, stage_at)
  WHERE current_stage NOT IN ('manticore', 'published', 'rejected');

CREATE INDEX pipeline_variants_source_stage_idx
  ON pipeline_variants (source_id, stage_at);

CREATE INDEX pipeline_variants_hard_key_canonical_idx
  ON pipeline_variants (hard_key)
  WHERE canonical_id IS NOT NULL;

CREATE INDEX pipeline_variants_variant_id_idx
  ON pipeline_variants (variant_id);

CREATE INDEX pipeline_variants_raw_payload_idx
  ON pipeline_variants (raw_payload_id) WHERE raw_payload_id IS NOT NULL;

CREATE INDEX pipeline_variants_crawl_job_idx
  ON pipeline_variants (crawl_job_id) WHERE crawl_job_id IS NOT NULL;

-- ===========================================================================
-- opportunities — canonical merged state + search/serve table.
--
-- Not a hypertable: UPSERT-by-canonical_id needs a unique constraint
-- on canonical_id alone, which TimescaleDB can't express on a
-- partitioned table. Row count is bounded by canonical_id cardinality.
--
-- employment_type/seniority/geo_scope are promoted out of the
-- attributes JSONB for filter-hot reads (lean-postgres-plan2).
-- ===========================================================================
CREATE TABLE opportunities (
  canonical_id     TEXT PRIMARY KEY,
  slug             TEXT UNIQUE NOT NULL,
  kind             TEXT NOT NULL,
  source_id        TEXT,
  title            TEXT NOT NULL,
  description      TEXT,
  issuing_entity   TEXT,
  country          TEXT,
  region           TEXT,
  city             TEXT,
  remote           BOOLEAN,
  apply_url        TEXT,
  posted_at        TIMESTAMPTZ,
  deadline         TIMESTAMPTZ,
  currency         TEXT,
  amount_min       NUMERIC,
  amount_max       NUMERIC,
  employment_type  TEXT,
  seniority        TEXT,
  geo_scope        TEXT,
  status           TEXT NOT NULL DEFAULT 'active',
  first_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  attributes       JSONB NOT NULL DEFAULT '{}'::jsonb,
  embedding        vector(1024),  -- intfloat/multilingual-e5-large; see migration 20260602_0110
  quality_score    NUMERIC,
  hidden           BOOLEAN NOT NULL DEFAULT false,
  hidden_reason    TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX opportunities_bm25_idx ON opportunities
  USING bm25 (canonical_id, title, description, issuing_entity, country, kind)
  WITH (key_field = 'canonical_id');

CREATE INDEX opportunities_embedding_diskann_idx ON opportunities
  USING diskann (embedding vector_cosine_ops);

CREATE INDEX opportunities_kind_country_last_seen_idx
  ON opportunities (kind, country, last_seen_at DESC)
  WHERE hidden = false AND status = 'active';

CREATE INDEX opportunities_last_seen_idx
  ON opportunities (last_seen_at DESC)
  WHERE hidden = false AND status = 'active';

CREATE INDEX opportunities_deadline_idx
  ON opportunities (deadline)
  WHERE deadline IS NOT NULL AND hidden = false AND status = 'active';

CREATE INDEX opportunities_source_idx
  ON opportunities (source_id);

CREATE INDEX opportunities_created_at_brin_idx ON opportunities
  USING brin (created_at);

CREATE INDEX opportunities_employment_type_idx
  ON opportunities (employment_type)
  WHERE hidden = false AND status = 'active' AND employment_type IS NOT NULL;

CREATE INDEX opportunities_seniority_idx
  ON opportunities (seniority)
  WHERE hidden = false AND status = 'active' AND seniority IS NOT NULL;

CREATE INDEX opportunities_geo_scope_idx
  ON opportunities (geo_scope)
  WHERE hidden = false AND status = 'active' AND geo_scope IS NOT NULL;

-- ===========================================================================
-- candidate_profiles — owned by the matching service. Documented here
-- because lean-postgres-plan3 reshapes columns on this table; the
-- matching app's AutoMigrate creates the base table.
-- ===========================================================================
-- (See pkg/domain/candidate.go for the GORM struct; only the lean-plan
-- deltas — cv_storage_uri / cv_content_hash + text[] skill columns —
-- live in the timestamped *.sql files.)

-- ===========================================================================
-- updated_at maintenance trigger.
-- ===========================================================================
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

CREATE TRIGGER pipeline_variants_set_updated_at
  BEFORE UPDATE ON pipeline_variants
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER opportunities_set_updated_at
  BEFORE UPDATE ON opportunities
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ===========================================================================
-- Grants for the application role.
-- ===========================================================================
GRANT SELECT, INSERT, UPDATE, DELETE ON pipeline_variants TO opportunities;
GRANT SELECT, INSERT, UPDATE, DELETE ON opportunities      TO opportunities;
