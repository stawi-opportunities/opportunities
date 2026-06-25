-- OLTP applications table (raw-SQL serving table; the matching serving code
-- uses `applications`, while domain.CandidateApplication maps to
-- candidate_applications via AutoMigrate). One application per
-- (candidate_id, opportunity_id).
CREATE TABLE IF NOT EXISTS applications (
    application_id   TEXT        PRIMARY KEY,
    candidate_id     TEXT        NOT NULL,
    opportunity_id   TEXT        NOT NULL,
    match_id         TEXT        NOT NULL,
    status           TEXT        NOT NULL,
    current_stage    TEXT,
    metadata         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    submitted_at     TIMESTAMPTZ,
    last_event_id    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (candidate_id, opportunity_id)
);
