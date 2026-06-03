-- 0011: match_rules (per-candidate autonomy doc), candidate_match_indexes
-- (denormalized vector + filter bag the fan-out worker reads), and
-- extension_grants (per-install JWT claim tracking).

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS match_rules (
    candidate_id     TEXT        PRIMARY KEY,
    document         JSONB       NOT NULL,                -- validated by pkg/applications/rules
    version          INTEGER     NOT NULL DEFAULT 1,
    enabled          BOOLEAN     NOT NULL DEFAULT TRUE,   -- denormalized for cheap filter
    autoapply        BOOLEAN     NOT NULL DEFAULT FALSE,  -- denormalized
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS candidate_match_indexes (
    candidate_id     TEXT        PRIMARY KEY,
    embedding        vector(1024) NOT NULL,
    min_score        DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    daily_cap        INTEGER     NOT NULL DEFAULT 25,
    weekly_cap       INTEGER     NOT NULL DEFAULT 100,
    kinds            TEXT[]      NOT NULL DEFAULT ARRAY['job']::TEXT[],
    countries        TEXT[]      NOT NULL DEFAULT '{}'::TEXT[],
    salary_floor_usd INTEGER,
    remote_only      BOOLEAN     NOT NULL DEFAULT FALSE,
    enabled          BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- HNSW index for the fan-out KNN. Cosine distance to match pgsearch.
CREATE INDEX IF NOT EXISTS candidate_match_indexes_embedding_hnsw
    ON candidate_match_indexes
    USING hnsw (embedding vector_cosine_ops)
    WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS candidate_match_indexes_kinds_gin
    ON candidate_match_indexes USING GIN (kinds)
    WHERE enabled = TRUE;

CREATE TABLE IF NOT EXISTS extension_grants (
    grant_id             TEXT        PRIMARY KEY,
    candidate_id         TEXT        NOT NULL,
    extension_install_id TEXT        NOT NULL UNIQUE,
    user_agent           TEXT,
    issued_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at           TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS extension_grants_candidate_idx
    ON extension_grants (candidate_id, issued_at DESC);
