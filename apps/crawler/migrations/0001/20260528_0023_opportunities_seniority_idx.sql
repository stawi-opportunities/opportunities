CREATE INDEX IF NOT EXISTS opportunities_seniority_idx
    ON opportunities (seniority)
    WHERE hidden = false AND status = 'active' AND seniority IS NOT NULL;
