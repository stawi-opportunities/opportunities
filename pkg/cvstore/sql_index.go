package cvstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SQLIndexStore is the Postgres-backed local CV document index.
type SQLIndexStore struct {
	DB *sql.DB
}

// EnsureSchema creates the candidate_cv_documents table if missing.
// Safe to call on every boot (IF NOT EXISTS).
func (s *SQLIndexStore) EnsureSchema(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("cvstore: db is nil")
	}
	const ddl = `
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
`
	_, err := s.DB.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("cvstore: ensure schema: %w", err)
	}
	return nil
}

// UpsertDocument increments version when content_hash changes, otherwise
// refreshes metadata while keeping version stable for identical bytes.
func (s *SQLIndexStore) UpsertDocument(ctx context.Context, doc Document) (int, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("cvstore: db is nil")
	}
	if doc.CandidateID == "" {
		return 0, fmt.Errorf("cvstore: candidate_id required")
	}
	var prevHash string
	var prevVer int
	err := s.DB.QueryRowContext(ctx,
		`SELECT content_hash, version FROM candidate_cv_documents WHERE candidate_id = $1`,
		doc.CandidateID,
	).Scan(&prevHash, &prevVer)
	version := 1
	switch {
	case errors.Is(err, sql.ErrNoRows):
		version = 1
	case err != nil:
		return 0, fmt.Errorf("cvstore: load prior: %w", err)
	default:
		if prevHash != "" && prevHash == doc.ContentHash {
			version = prevVer
		} else {
			version = prevVer + 1
		}
	}
	if version < 1 {
		version = 1
	}

	const q = `
INSERT INTO candidate_cv_documents (
    candidate_id, version, file_id, content_uri, content_hash,
    filename, content_type, size_bytes, extracted_text, text_length,
    storage, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
ON CONFLICT (candidate_id) DO UPDATE SET
    version        = EXCLUDED.version,
    file_id        = EXCLUDED.file_id,
    content_uri    = EXCLUDED.content_uri,
    content_hash   = EXCLUDED.content_hash,
    filename       = EXCLUDED.filename,
    content_type   = EXCLUDED.content_type,
    size_bytes     = EXCLUDED.size_bytes,
    extracted_text = EXCLUDED.extracted_text,
    text_length    = EXCLUDED.text_length,
    storage        = EXCLUDED.storage,
    updated_at     = now()
`
	_, err = s.DB.ExecContext(ctx, q,
		doc.CandidateID, version, doc.FileID, doc.ContentURI, doc.ContentHash,
		doc.Filename, doc.ContentType, doc.SizeBytes, doc.ExtractedText, doc.TextLength,
		doc.Storage,
	)
	if err != nil {
		return 0, fmt.Errorf("cvstore: upsert document: %w", err)
	}
	return version, nil
}

// GetCurrent returns the latest document, or nil when none exists.
func (s *SQLIndexStore) GetCurrent(ctx context.Context, candidateID string) (*Document, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("cvstore: db is nil")
	}
	const q = `
SELECT candidate_id, version, file_id, content_uri, content_hash,
       filename, content_type, size_bytes, extracted_text, text_length,
       storage, updated_at
FROM candidate_cv_documents
WHERE candidate_id = $1
`
	var d Document
	var updated time.Time
	err := s.DB.QueryRowContext(ctx, q, candidateID).Scan(
		&d.CandidateID, &d.Version, &d.FileID, &d.ContentURI, &d.ContentHash,
		&d.Filename, &d.ContentType, &d.SizeBytes, &d.ExtractedText, &d.TextLength,
		&d.Storage, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cvstore: get current: %w", err)
	}
	d.UpdatedAt = updated
	return &d, nil
}
