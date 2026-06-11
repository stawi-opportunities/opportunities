CREATE INDEX IF NOT EXISTS application_reminders_app_idx
    ON application_reminders (application_id, due_at)
    WHERE deleted_at IS NULL;
