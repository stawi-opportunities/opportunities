CREATE INDEX IF NOT EXISTS idx_candidate_checkouts_status
    ON candidate_checkouts (status, created_at)
    WHERE status = 'pending';
