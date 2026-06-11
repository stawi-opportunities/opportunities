CREATE TABLE IF NOT EXISTS candidate_saved_jobs (
    candidate_id   TEXT        NOT NULL,
    opportunity_id TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (candidate_id, opportunity_id)
);
