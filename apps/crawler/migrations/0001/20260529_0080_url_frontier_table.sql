-- URL Frontier — per-URL priority queue that replaces the
-- per-source iterator model with URL-level work scheduling.
--
-- One row per discovered URL. Connectors enqueue here in
-- discovery mode; the frontier-worker dequeues by priority
-- under per-host politeness constraints and runs the fetch +
-- extract pipeline.
--
-- url_id: KSUID; the primary key. The dedup key is
--   canonical_url_hash (sha256 over the normalized URL) — kept
--   separate from the PK so the same URL can be re-enqueued
--   with a refreshed priority without losing its history.
-- state: 'pending' | 'in_flight' | 'done' | 'failed' | 'parked'.
-- priority: source.score * 0.7 + url_signals * 0.3 at enqueue
--   time; the Dequeue hot path orders DESC then by enqueued_at
--   ASC for stable FIFO among equal-priority rows.
-- metadata: connector-defined hints (title preview, source
--   rank, kind, etc.) — JSONB so new connectors don't require
--   schema migrations.
-- next_attempt_at: NULL when eligible immediately; set by Fail
--   after a backoff schedule (capped at 1h).
-- claimed_at / claimed_by: stamped during Dequeue's atomic
--   UPDATE so stuck-row cleanup + observability know which pod
--   owns an in_flight URL.

CREATE TABLE IF NOT EXISTS url_frontier (
    url_id              VARCHAR(20)      PRIMARY KEY,
    canonical_url       TEXT             NOT NULL,
    canonical_url_hash  CHAR(64)         NOT NULL,
    host                TEXT             NOT NULL,
    source_id           VARCHAR(20)      NOT NULL,
    priority            DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    state               TEXT             NOT NULL DEFAULT 'pending',
    attempts            INTEGER          NOT NULL DEFAULT 0,
    last_error          TEXT,
    enqueued_at         TIMESTAMPTZ      NOT NULL DEFAULT now(),
    claimed_at          TIMESTAMPTZ,
    claimed_by          TEXT,
    completed_at        TIMESTAMPTZ,
    next_attempt_at     TIMESTAMPTZ,
    metadata            JSONB            NOT NULL DEFAULT '{}'::jsonb
);
