CREATE TABLE IF NOT EXISTS application_notes (
    note_id          TEXT        PRIMARY KEY,
    application_id   TEXT        NOT NULL REFERENCES applications(application_id) ON DELETE CASCADE,
    body             TEXT        NOT NULL,
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
