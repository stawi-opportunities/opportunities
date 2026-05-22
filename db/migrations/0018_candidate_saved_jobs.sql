-- 0018: candidate-starred opportunities.
-- Composite PK so the same candidate can star the same opportunity
-- only once. Read pattern is "give me all stars for candidate X",
-- so a single index keyed by candidate_id with created_at DESC for
-- the dashboard's reverse-chronological default ordering is enough.

CREATE TABLE IF NOT EXISTS candidate_saved_jobs (
    candidate_id   TEXT        NOT NULL,
    opportunity_id TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (candidate_id, opportunity_id)
);

CREATE INDEX IF NOT EXISTS idx_candidate_saved_jobs_candidate_created
    ON candidate_saved_jobs (candidate_id, created_at DESC);
