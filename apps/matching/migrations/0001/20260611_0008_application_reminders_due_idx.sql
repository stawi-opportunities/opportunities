CREATE INDEX IF NOT EXISTS application_reminders_due_idx
    ON application_reminders (due_at)
    WHERE status = 'pending' AND deleted_at IS NULL;
