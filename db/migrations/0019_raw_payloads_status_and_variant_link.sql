-- NOTE: this file is applied only by the INTEGRATION TEST env (db/migrations
-- is not the deployed path). Production promotion happens in-place via
-- apps/crawler/migrations/0001/20260610_0130_timescale_append_only.sql.
-- 0019: crawl_jobs + raw_payloads as TimescaleDB hypertables, matching
-- the pattern in 0009_matching_applications_hypertables.sql.
--
-- Why hypertable: both tables grow O(crawls/day). Time-partition
-- pruning makes recent-data queries fast, retention is DROP CHUNK,
-- and old chunks compress.
--
-- Both tables exist today (created by GORM auto-migrate from
-- migration 0001) with zero rows in production (verified
-- 2026-05-28). Safe to drop+recreate as part of this migration.
--
-- The original migration 0001 declared crawl_jobs + raw_payloads
-- with BIGSERIAL ids; the live schema is GORM's varchar(20) xid
-- shape. Recreating from scratch resolves that drift in one shot.

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- ---------- crawl_jobs ----------

DROP TABLE IF EXISTS crawl_jobs CASCADE;

CREATE TABLE crawl_jobs (
    id              VARCHAR(20)   NOT NULL,
    scheduled_at    TIMESTAMPTZ   NOT NULL,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,
    source_id       VARCHAR(20)   NOT NULL,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    status          VARCHAR(20)   NOT NULL DEFAULT 'scheduled',
    attempt         INTEGER       NOT NULL DEFAULT 1,
    idempotency_key VARCHAR(255)  NOT NULL,
    error_code      TEXT          NOT NULL DEFAULT '',
    error_message   TEXT          NOT NULL DEFAULT '',
    jobs_found      INTEGER       NOT NULL DEFAULT 0,
    jobs_stored     INTEGER       NOT NULL DEFAULT 0,
    PRIMARY KEY (id, scheduled_at)
);

SELECT create_hypertable('crawl_jobs', 'scheduled_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '1 day');

CREATE UNIQUE INDEX crawl_jobs_idempotency_idx
    ON crawl_jobs (idempotency_key, scheduled_at);

CREATE INDEX crawl_jobs_source_time_idx
    ON crawl_jobs (source_id, scheduled_at DESC);

SELECT add_retention_policy('crawl_jobs', INTERVAL '90 days',
                            if_not_exists => TRUE);
ALTER TABLE crawl_jobs SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'source_id'
);
SELECT add_compression_policy('crawl_jobs', INTERVAL '14 days',
                              if_not_exists => TRUE);

-- ---------- raw_payloads ----------

DROP TABLE IF EXISTS raw_payloads CASCADE;

CREATE TABLE raw_payloads (
    id             VARCHAR(20)  NOT NULL,
    fetched_at     TIMESTAMPTZ  NOT NULL,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,
    crawl_job_id   VARCHAR(20)  NOT NULL,
    source_id      VARCHAR(20),
    source_url     TEXT,
    storage_uri    TEXT,
    content_hash   VARCHAR(64),
    size_bytes     BIGINT       NOT NULL DEFAULT 0,
    http_status    INTEGER      NOT NULL,
    status         TEXT         NOT NULL DEFAULT 'pending',
    attempt_count  INTEGER      NOT NULL DEFAULT 0,
    next_retry_at  TIMESTAMPTZ,
    last_error     TEXT         NOT NULL DEFAULT '',
    PRIMARY KEY (id, fetched_at)
);

SELECT create_hypertable('raw_payloads', 'fetched_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '1 day');

CREATE INDEX raw_payloads_pending_idx
    ON raw_payloads (next_retry_at NULLS FIRST, fetched_at)
    WHERE status = 'pending';

CREATE INDEX raw_payloads_status_time_idx
    ON raw_payloads (status, fetched_at DESC);

CREATE INDEX raw_payloads_source_time_idx
    ON raw_payloads (source_id, fetched_at DESC);

CREATE INDEX raw_payloads_crawl_job_idx
    ON raw_payloads (crawl_job_id, fetched_at DESC);

CREATE INDEX raw_payloads_content_hash_idx
    ON raw_payloads (content_hash);

SELECT add_retention_policy('raw_payloads', INTERVAL '30 days',
                            if_not_exists => TRUE);
ALTER TABLE raw_payloads SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'source_id'
);
SELECT add_compression_policy('raw_payloads', INTERVAL '7 days',
                              if_not_exists => TRUE);

-- ---------- pipeline_variants: forward references ----------

ALTER TABLE pipeline_variants
    ADD COLUMN IF NOT EXISTS raw_payload_id VARCHAR(20),
    ADD COLUMN IF NOT EXISTS crawl_job_id   VARCHAR(20);

CREATE INDEX IF NOT EXISTS pipeline_variants_raw_payload_idx
    ON pipeline_variants (raw_payload_id) WHERE raw_payload_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS pipeline_variants_crawl_job_idx
    ON pipeline_variants (crawl_job_id) WHERE crawl_job_id IS NOT NULL;
