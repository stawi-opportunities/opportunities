CREATE INDEX IF NOT EXISTS idx_candidate_checkouts_candidate_created
    ON candidate_checkouts (candidate_id, created_at DESC);
