-- 0014: applications-phase3 support tables.
--
-- idempotency_keys: pg-backed alternative to Valkey for the
--   Idempotency-Key middleware. Keyed by (candidate_id, key); the
--   response body is stored as JSONB so a replay returns the exact
--   bytes of the original 2xx response.
--
-- deleted_at columns: soft-delete on notes + attachments so the audit
--   log (application_events) remains the source of truth. The DELETE
--   endpoints UPDATE deleted_at instead of removing the row.

CREATE TABLE IF NOT EXISTS idempotency_keys (
    candidate_id  TEXT        NOT NULL,
    key           TEXT        NOT NULL,
    route_group   TEXT        NOT NULL,
    status_code   INTEGER     NOT NULL,
    response_body JSONB       NOT NULL,
    response_headers JSONB    NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (candidate_id, key, route_group)
);
CREATE INDEX IF NOT EXISTS idempotency_keys_expires_idx
    ON idempotency_keys (expires_at);
