-- db/migrations/0009_candidate_sessions.sql
-- Encrypted-at-rest store of authenticated source sessions captured by the
-- browser extension. Each row holds the cookies + headers + storage snapshot
-- needed to replay an application submission as the candidate, without ever
-- holding the secret material in plaintext on disk.
--
-- Crypto model (see pkg/authsession): payload_enc is AES-256-GCM ciphertext
-- of the JSON payload under a per-row 32-byte DEK; dek_wrapped is the DEK
-- wrapped (also AES-256-GCM) under the master key identified by key_id.
-- Rotating masters re-wraps DEKs only — no touch to payload_enc.
--
-- Idempotency: the partial unique index on (candidate_id, source_type)
-- WHERE deleted_at IS NULL AND revoked_at IS NULL means a fresh capture
-- from the extension is an upsert in practice — the matching service
-- soft-deletes any prior row before inserting, so the index always
-- holds one live session per (candidate, source).
--
-- GORM AutoMigrate handles the table when the matching service boots
-- with DO_DATABASE_MIGRATE=true. This file documents the exact DDL for
-- manual apply or diagnostic queries.

CREATE TABLE IF NOT EXISTS candidate_sessions (
    id              VARCHAR(20)  PRIMARY KEY,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,

    candidate_id    VARCHAR(20)  NOT NULL,
    source_type     VARCHAR(40)  NOT NULL,

    payload_enc     BYTEA        NOT NULL,
    payload_nonce   BYTEA        NOT NULL,
    dek_wrapped     BYTEA        NOT NULL,
    dek_nonce       BYTEA        NOT NULL,
    key_id          VARCHAR(64)  NOT NULL,

    captured_at     TIMESTAMPTZ  NOT NULL,
    expires_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    user_agent      VARCHAR(255) NOT NULL DEFAULT '',
    capture_origin  VARCHAR(40)  NOT NULL DEFAULT 'extension'
);

-- One live session per (candidate, source). Soft-deleted and revoked rows
-- are excluded so a re-capture after a revoke does not conflict.
CREATE UNIQUE INDEX IF NOT EXISTS idx_candidate_sessions_active
    ON candidate_sessions (candidate_id, source_type)
    WHERE deleted_at IS NULL AND revoked_at IS NULL;

-- Supports a "stale sessions" sweep that re-prompts users to reconnect.
CREATE INDEX IF NOT EXISTS idx_candidate_sessions_expiry
    ON candidate_sessions (expires_at)
    WHERE deleted_at IS NULL AND revoked_at IS NULL;

-- Candidate-scoped list (Connected Accounts UI).
CREATE INDEX IF NOT EXISTS idx_candidate_sessions_candidate
    ON candidate_sessions (candidate_id)
    WHERE deleted_at IS NULL;
