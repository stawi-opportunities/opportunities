-- 0020: candidate checkout / subscription-intent ledger.
--
-- One row per checkout attempt. The POST /billing/checkout handler
-- inserts a 'pending' row keyed by prompt_id (the payment provider's
-- transaction/prompt reference) before returning to the UI. The status
-- poller, the provider webhook, and the reconciler all converge on this
-- row to drive the candidate_profiles.subscription free→paid flip.
--
-- prompt_id is the natural key the UI polls on (GET
-- /billing/checkout/status?prompt_id=...). It is unique so a webhook and
-- the reconciler can't race two activations for the same payment.
--
-- status mirrors ui/app/src/api/billing.ts CheckoutStatus:
--   redirect | pending | paid | failed
-- We never persist 'redirect' (that's a transient response state for the
-- card/Polar flow); the stored lifecycle is pending → paid|failed.

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

-- Status poller + "latest checkout for candidate" reads.
CREATE INDEX IF NOT EXISTS idx_candidate_checkouts_candidate_created
    ON candidate_checkouts (candidate_id, created_at DESC);

-- Reconciler sweep: "pending checkouts, oldest first".
CREATE INDEX IF NOT EXISTS idx_candidate_checkouts_status
    ON candidate_checkouts (status, created_at)
    WHERE status = 'pending';
