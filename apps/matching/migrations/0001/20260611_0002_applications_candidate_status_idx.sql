CREATE INDEX IF NOT EXISTS applications_candidate_status_idx
    ON applications (candidate_id, status, created_at DESC);
