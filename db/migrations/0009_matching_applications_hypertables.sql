-- 0009: Hypertables for matching + applications event/audit streams.
--
-- All four are append-only by design. Partition column is included in
-- every PK because TimescaleDB requires the partition column in every
-- unique constraint on a hypertable. event_id / run_id are xid-style
-- so the pair is unique in practice.
--
-- Retention + compression policies are added inline so the policies
-- are part of the schema (re-running this file is idempotent because
-- add_retention_policy + add_compression_policy are themselves
-- idempotent with `if_not_exists => TRUE`).

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- candidate_match_events: every match generated, viewed, dismissed.
CREATE TABLE IF NOT EXISTS candidate_match_events (
    event_id       TEXT        NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    candidate_id   TEXT        NOT NULL,
    opportunity_id TEXT        NOT NULL,
    canonical_id   TEXT        NOT NULL,
    kind           TEXT        NOT NULL,                -- generated|viewed|dismissed|overflow|rules_change_rematch
    path           TEXT        NOT NULL,                -- fanout|gap|candidate_change
    score          DOUBLE PRECISION,
    rerank_score   DOUBLE PRECISION,
    reranker_used  BOOLEAN     NOT NULL DEFAULT FALSE,
    data           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (event_id, occurred_at)
);
SELECT create_hypertable('candidate_match_events', 'occurred_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '7 days');
CREATE INDEX IF NOT EXISTS candidate_match_events_candidate_time_idx
    ON candidate_match_events (candidate_id, occurred_at DESC);
SELECT add_retention_policy('candidate_match_events', INTERVAL '365 days',
                            if_not_exists => TRUE);
ALTER TABLE candidate_match_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'candidate_id'
);
SELECT add_compression_policy('candidate_match_events', INTERVAL '7 days',
                              if_not_exists => TRUE);

-- application_events: canonical audit log for the applications domain.
CREATE TABLE IF NOT EXISTS application_events (
    event_id       TEXT        NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    application_id TEXT        NOT NULL,
    candidate_id   TEXT        NOT NULL,
    kind           TEXT        NOT NULL,                -- created|state_changed|submission_attempted|submission_succeeded|submission_failed|recruiter_replied|note_added|note_edited|note_deleted|attachment_added|attachment_deleted|reminder_set|reminder_done
    from_status    TEXT,
    to_status      TEXT,
    actor          TEXT        NOT NULL,                -- extension|user|system|admin
    data           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (event_id, occurred_at)
);
SELECT create_hypertable('application_events', 'occurred_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '30 days');
CREATE INDEX IF NOT EXISTS application_events_app_time_idx
    ON application_events (application_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS application_events_candidate_time_idx
    ON application_events (candidate_id, occurred_at DESC);
ALTER TABLE application_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'candidate_id'
);
SELECT add_compression_policy('application_events', INTERVAL '14 days',
                              if_not_exists => TRUE);
-- Note: no retention policy on application_events — kept indefinitely
-- (compliance + user-facing history requirement).

-- engagement_events: beacon traffic (view, click, dismiss, apply).
CREATE TABLE IF NOT EXISTS engagement_events (
    event_id       TEXT        NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    candidate_id   TEXT,                                -- nullable for anonymous beacons
    opportunity_id TEXT        NOT NULL,
    kind           TEXT        NOT NULL,                -- view|click|dismiss|apply
    source         TEXT        NOT NULL,                -- extension|web|email
    data           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (event_id, occurred_at)
);
SELECT create_hypertable('engagement_events', 'occurred_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '7 days');
CREATE INDEX IF NOT EXISTS engagement_events_opp_time_idx
    ON engagement_events (opportunity_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS engagement_events_candidate_time_idx
    ON engagement_events (candidate_id, occurred_at DESC)
    WHERE candidate_id IS NOT NULL;
SELECT add_retention_policy('engagement_events', INTERVAL '180 days',
                            if_not_exists => TRUE);
ALTER TABLE engagement_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'opportunity_id'
);
SELECT add_compression_policy('engagement_events', INTERVAL '7 days',
                              if_not_exists => TRUE);

-- match_run_events: per-run telemetry (op only).
CREATE TABLE IF NOT EXISTS match_run_events (
    run_id            TEXT        NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ,
    path              TEXT        NOT NULL,             -- fanout|gap|candidate_change
    triggered_by      TEXT        NOT NULL,             -- canonical_upserted|extension_poll|rules_changed|cv_changed|admin
    candidate_id      TEXT,
    canonical_id      TEXT,
    candidates_scanned INTEGER    NOT NULL DEFAULT 0,
    matches_written   INTEGER     NOT NULL DEFAULT 0,
    status            TEXT        NOT NULL,             -- ok|timeout|skipped|error|bad_embedding
    reranker_status   TEXT,                              -- used|skipped|down
    latency_ms        INTEGER,
    data              JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (run_id, started_at)
);
SELECT create_hypertable('match_run_events', 'started_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '7 days');
CREATE INDEX IF NOT EXISTS match_run_events_path_status_idx
    ON match_run_events (path, status, started_at DESC);
SELECT add_retention_policy('match_run_events', INTERVAL '90 days',
                            if_not_exists => TRUE);
ALTER TABLE match_run_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'path'
);
SELECT add_compression_policy('match_run_events', INTERVAL '7 days',
                              if_not_exists => TRUE);
