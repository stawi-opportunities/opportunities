CREATE INDEX IF NOT EXISTS idempotency_keys_expires_idx
    ON idempotency_keys (expires_at);
