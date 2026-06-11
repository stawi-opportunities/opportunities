CREATE INDEX IF NOT EXISTS application_notes_app_idx
    ON application_notes (application_id, created_at DESC)
    WHERE deleted_at IS NULL;
