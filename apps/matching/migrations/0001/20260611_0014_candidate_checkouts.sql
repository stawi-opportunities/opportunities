CREATE TABLE IF NOT EXISTS candidate_checkouts (
    prompt_id       TEXT        NOT NULL,
    candidate_id    TEXT        NOT NULL,
    plan_id         TEXT        NOT NULL,
    route           TEXT        NOT NULL DEFAULT '',
    status          TEXT        NOT NULL DEFAULT 'pending',
    subscription_id TEXT        NOT NULL DEFAULT '',
    amount_cents    BIGINT      NOT NULL DEFAULT 0,
    currency        TEXT        NOT NULL DEFAULT '',
    country         TEXT        NOT NULL DEFAULT '',
    redirect_url    TEXT        NOT NULL DEFAULT '',
    error           TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (prompt_id)
);
