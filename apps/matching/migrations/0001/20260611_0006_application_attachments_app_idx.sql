CREATE INDEX IF NOT EXISTS application_attachments_app_idx
    ON application_attachments (application_id, created_at DESC)
    WHERE deleted_at IS NULL;
