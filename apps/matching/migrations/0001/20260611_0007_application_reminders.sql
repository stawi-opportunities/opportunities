CREATE TABLE IF NOT EXISTS application_reminders (
    reminder_id      TEXT        PRIMARY KEY,
    application_id   TEXT        NOT NULL REFERENCES applications(application_id) ON DELETE CASCADE,
    due_at           TIMESTAMPTZ NOT NULL,
    status           TEXT        NOT NULL,
    note             TEXT,
    completed_at     TIMESTAMPTZ,
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
