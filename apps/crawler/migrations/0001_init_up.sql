-- Migration: 0001_init_up.sql
-- Description: Initial schema for crawl framework v2

-- 1. sources
CREATE TABLE IF NOT EXISTS sources (
    id                  BIGSERIAL PRIMARY KEY,
    source_type         TEXT NOT NULL,
    base_url            TEXT NOT NULL,
    country             TEXT,
    region              TEXT,
    status              TEXT DEFAULT 'active',
    priority            TEXT DEFAULT 'normal',
    crawl_interval_sec  INT DEFAULT 21600,
    health_score        DOUBLE PRECISION DEFAULT 1.0,
    crawl_cursor        JSONB DEFAULT '{}',
    last_seen_at        TIMESTAMPTZ,
    next_crawl_at       TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT now(),
    updated_at          TIMESTAMPTZ DEFAULT now(),
    UNIQUE (source_type, base_url)
);

-- 2. crawl_jobs
CREATE TABLE IF NOT EXISTS crawl_jobs (
    id                  BIGSERIAL PRIMARY KEY,
    source_id           BIGINT REFERENCES sources(id),
    scheduled_at        TIMESTAMPTZ,
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    status              TEXT DEFAULT 'scheduled',
    attempt             INT DEFAULT 1,
    idempotency_key     TEXT UNIQUE,
    error_code          TEXT,
    created_at          TIMESTAMPTZ DEFAULT now(),
    updated_at          TIMESTAMPTZ DEFAULT now()
);

-- 3. raw_payloads
CREATE TABLE IF NOT EXISTS raw_payloads (
    id              BIGSERIAL PRIMARY KEY,
    crawl_job_id    BIGINT REFERENCES crawl_jobs(id),
    storage_uri     TEXT,
    content_hash    TEXT,
    fetched_at      TIMESTAMPTZ,
    http_status     INT,
    body            BYTEA,
    created_at      TIMESTAMPTZ DEFAULT now()
);

-- 4. job_variants
CREATE TABLE IF NOT EXISTS job_variants (
    id                  BIGSERIAL PRIMARY KEY,
    external_job_id     TEXT,
    source_id           BIGINT REFERENCES sources(id),
    source_url          TEXT,
    apply_url           TEXT,
    title               TEXT,
    company             TEXT,
    location_text       TEXT,
    country             TEXT,
    region              TEXT,
    language            TEXT,
    source_board        TEXT,
    remote_type         TEXT,
    employment_type     TEXT,
    salary_min          DOUBLE PRECISION DEFAULT 0,
    salary_max          DOUBLE PRECISION DEFAULT 0,
    currency            TEXT,
    description_text    TEXT,
    posted_at           TIMESTAMPTZ,
    scraped_at          TIMESTAMPTZ NOT NULL,
    content_hash        TEXT,
    hard_key            TEXT,
    created_at          TIMESTAMPTZ DEFAULT now(),
    updated_at          TIMESTAMPTZ DEFAULT now(),
    UNIQUE (source_id, external_job_id)
);

CREATE INDEX IF NOT EXISTS idx_job_variants_hard_key   ON job_variants (hard_key);
CREATE INDEX IF NOT EXISTS idx_job_variants_scraped_at ON job_variants (scraped_at);

-- 5. job_clusters
CREATE TABLE IF NOT EXISTS job_clusters (
    id                      BIGSERIAL PRIMARY KEY,
    canonical_variant_id    BIGINT REFERENCES job_variants(id),
    confidence              DOUBLE PRECISION DEFAULT 1.0,
    created_at              TIMESTAMPTZ DEFAULT now(),
    updated_at              TIMESTAMPTZ DEFAULT now()
);

-- 6. job_cluster_members
CREATE TABLE IF NOT EXISTS job_cluster_members (
    cluster_id  BIGINT REFERENCES job_clusters(id),
    variant_id  BIGINT REFERENCES job_variants(id),
    match_type  TEXT,
    score       DOUBLE PRECISION DEFAULT 1.0,
    updated_at  TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (cluster_id, variant_id)
);

-- 7. canonical_jobs
CREATE TABLE IF NOT EXISTS canonical_jobs (
    id                  BIGSERIAL PRIMARY KEY,
    cluster_id          BIGINT UNIQUE REFERENCES job_clusters(id),
    title               TEXT,
    company             TEXT,
    description_text    TEXT,
    location_text       TEXT,
    country             TEXT,
    remote_type         TEXT,
    employment_type     TEXT,
    salary_min          DOUBLE PRECISION DEFAULT 0,
    salary_max          DOUBLE PRECISION DEFAULT 0,
    currency            TEXT,
    apply_url           TEXT,
    posted_at           TIMESTAMPTZ,
    first_seen_at       TIMESTAMPTZ,
    last_seen_at        TIMESTAMPTZ,
    is_active           BOOLEAN DEFAULT true,
    created_at          TIMESTAMPTZ DEFAULT now(),
    updated_at          TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_canonical_jobs_last_seen_at ON canonical_jobs (last_seen_at DESC);

-- 8. crawl_page_state
CREATE TABLE IF NOT EXISTS crawl_page_state (
    source_id       BIGINT REFERENCES sources(id),
    page_key        TEXT,
    content_hash    TEXT NOT NULL,
    last_seen_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (source_id, page_key)
);

-- 9. rejected_jobs
CREATE TABLE IF NOT EXISTS rejected_jobs (
    id              BIGSERIAL PRIMARY KEY,
    source_id       BIGINT REFERENCES sources(id),
    source_type     TEXT NOT NULL,
    external_job_id TEXT,
    reason          TEXT NOT NULL,
    raw_data        JSONB,
    rejected_at     TIMESTAMPTZ DEFAULT now()
);
