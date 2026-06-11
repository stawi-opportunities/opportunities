CREATE INDEX IF NOT EXISTS idx_candidate_saved_jobs_candidate_created
    ON candidate_saved_jobs (candidate_id, created_at DESC);
