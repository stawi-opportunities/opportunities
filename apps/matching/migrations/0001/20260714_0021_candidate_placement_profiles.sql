-- Dynamic placement profile: combined qualifications + preferences summary
-- used for agent guidance and vector matching (query-side embedding).

CREATE TABLE IF NOT EXISTS candidate_placement_profiles (
    candidate_id          text PRIMARY KEY,
    version               int  NOT NULL DEFAULT 1,
    summary_text          text NOT NULL DEFAULT '',
    qualifications_text   text NOT NULL DEFAULT '',
    preferences_text      text NOT NULL DEFAULT '',
    missing               text[] NOT NULL DEFAULT '{}',
    ready                 boolean NOT NULL DEFAULT false,
    updated_at            timestamptz NOT NULL DEFAULT now()
);
