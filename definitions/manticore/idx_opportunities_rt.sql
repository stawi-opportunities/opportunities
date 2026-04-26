-- idx_opportunities_rt — primary job search RT index for Manticore Search.
-- Extracted from pkg/searchindex/schema.go (idxJobsRTDDL constant).
--
-- Apply via:
--   kubectl -n opportunities exec -i svc/manticore -- /usr/bin/mysql -h127.0.0.1 -P9306 < definitions/manticore/idx_opportunities_rt.sql
-- Or use scripts/bootstrap/create-manticore-schema.sh which wraps the above.
--
-- This is idempotent if Manticore returns "table already exists" — the bootstrap
-- script (and pkg/searchindex.Apply) swallows that error.
--
-- Embedding dimension is pinned to 1536 (OpenAI text-embedding-3-small).
-- Changing knn_dims is a full rebuild (DROP + re-create + re-materialize).

CREATE TABLE IF NOT EXISTS idx_opportunities_rt (
    canonical_id    string attribute,
    slug            string attribute,
    title           text indexed,
    company         text indexed,
    description     text indexed stored,
    location_text   text indexed,
    category        string attribute,
    country         string attribute,
    language        string attribute,
    remote_type     string attribute,
    employment_type string attribute,
    seniority       string attribute,
    salary_min      uint,
    salary_max      uint,
    currency        string attribute,
    quality_score   float,
    is_featured     bool,
    posted_at       timestamp,
    last_seen_at    timestamp,
    expires_at      timestamp,
    status          string attribute,
    embedding       float_vector knn_type='hnsw' knn_dims='1536' hnsw_similarity='COSINE',
    embedding_model string attribute
);
