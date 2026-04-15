CREATE TABLE IF NOT EXISTS sources (
    id BIGSERIAL PRIMARY KEY,
    source_type TEXT NOT NULL,
    base_url TEXT NOT NULL,
    country TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    crawl_interval_sec INTEGER NOT NULL DEFAULT 21600,
    health_score DOUBLE PRECISION NOT NULL DEFAULT 1,
    last_seen_at TIMESTAMPTZ,
    next_crawl_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(source_type, base_url)
);

CREATE TABLE IF NOT EXISTS crawl_jobs (
    id BIGSERIAL PRIMARY KEY,
    source_id BIGINT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    scheduled_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    status TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 1,
    idempotency_key TEXT NOT NULL UNIQUE,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS raw_payloads (
    id BIGSERIAL PRIMARY KEY,
    crawl_job_id BIGINT,
    storage_uri TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL,
    http_status INTEGER NOT NULL,
    body BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS job_variants (
    id BIGSERIAL PRIMARY KEY,
    external_job_id TEXT NOT NULL,
    source_id BIGINT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    source_url TEXT NOT NULL,
    apply_url TEXT,
    title TEXT NOT NULL,
    company TEXT NOT NULL,
    location_text TEXT,
    country TEXT,
    remote_type TEXT,
    employment_type TEXT,
    salary_min DOUBLE PRECISION,
    salary_max DOUBLE PRECISION,
    currency TEXT,
    description_text TEXT,
    posted_at TIMESTAMPTZ,
    scraped_at TIMESTAMPTZ NOT NULL,
    content_hash TEXT NOT NULL,
    hard_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(source_id, external_job_id)
);

CREATE INDEX IF NOT EXISTS idx_job_variants_hard_key ON job_variants(hard_key);
CREATE INDEX IF NOT EXISTS idx_job_variants_scraped_at ON job_variants(scraped_at);

CREATE TABLE IF NOT EXISTS job_clusters (
    id BIGSERIAL PRIMARY KEY,
    canonical_variant_id BIGINT REFERENCES job_variants(id) ON DELETE SET NULL,
    confidence DOUBLE PRECISION NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS job_cluster_members (
    cluster_id BIGINT NOT NULL REFERENCES job_clusters(id) ON DELETE CASCADE,
    variant_id BIGINT NOT NULL REFERENCES job_variants(id) ON DELETE CASCADE,
    match_type TEXT NOT NULL,
    score DOUBLE PRECISION NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(cluster_id, variant_id)
);

CREATE TABLE IF NOT EXISTS canonical_jobs (
    id BIGSERIAL PRIMARY KEY,
    cluster_id BIGINT NOT NULL UNIQUE REFERENCES job_clusters(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    company TEXT NOT NULL,
    description_text TEXT,
    location_text TEXT,
    country TEXT,
    remote_type TEXT,
    employment_type TEXT,
    salary_min DOUBLE PRECISION,
    salary_max DOUBLE PRECISION,
    currency TEXT,
    apply_url TEXT,
    posted_at TIMESTAMPTZ,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_canonical_jobs_search_tsv ON canonical_jobs USING GIN (to_tsvector('simple', coalesce(title,'') || ' ' || coalesce(company,'') || ' ' || coalesce(description_text,'')));
CREATE INDEX IF NOT EXISTS idx_canonical_jobs_last_seen ON canonical_jobs(last_seen_at DESC);
