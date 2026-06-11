CREATE TABLE IF NOT EXISTS application_attachments (
    attachment_id    TEXT        PRIMARY KEY,
    application_id   TEXT        NOT NULL REFERENCES applications(application_id) ON DELETE CASCADE,
    r2_key           TEXT        NOT NULL,
    content_type     TEXT        NOT NULL,
    bytes            BIGINT      NOT NULL,
    filename         TEXT,
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
