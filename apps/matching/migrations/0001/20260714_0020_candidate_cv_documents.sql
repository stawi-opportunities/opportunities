-- Local CV document index: extracted text + files-service / archive pointers.
-- Binary content lives in the platform files service (preferred) or R2 archive;
-- this table is the matching-local source of truth for chat + re-embed.

CREATE TABLE IF NOT EXISTS candidate_cv_documents (
    candidate_id    text PRIMARY KEY,
    version         int  NOT NULL DEFAULT 1,
    file_id         text NOT NULL DEFAULT '',
    content_uri     text NOT NULL DEFAULT '',
    content_hash    text NOT NULL DEFAULT '',
    filename        text NOT NULL DEFAULT '',
    content_type    text NOT NULL DEFAULT '',
    size_bytes      bigint NOT NULL DEFAULT 0,
    extracted_text  text NOT NULL DEFAULT '',
    text_length     int  NOT NULL DEFAULT 0,
    storage         text NOT NULL DEFAULT 'archive',
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS candidate_cv_documents_hash_idx
    ON candidate_cv_documents (content_hash)
    WHERE content_hash <> '';
