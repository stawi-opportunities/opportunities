-- pg-backed idempotency for apply/save/billing mutations.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    candidate_id     TEXT        NOT NULL,
    key              TEXT        NOT NULL,
    route_group      TEXT        NOT NULL,
    status_code      INTEGER     NOT NULL,
    response_body    JSONB       NOT NULL,
    response_headers JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (candidate_id, key, route_group)
);
