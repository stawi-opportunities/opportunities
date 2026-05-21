package applications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Attachment is one row of the application_attachments table.
type Attachment struct {
	AttachmentID  string
	ApplicationID string
	R2Key         string
	ContentType   string
	Bytes         int64
	Filename      string
	DeletedAt     *time.Time
	CreatedAt     time.Time
}

// AttachmentsStore owns reads/writes against application_attachments.
type AttachmentsStore struct{ db *sql.DB }

// NewAttachmentsStore wraps the given handle.
func NewAttachmentsStore(db *sql.DB) *AttachmentsStore { return &AttachmentsStore{db: db} }

// Create inserts a new attachment metadata row and returns the persisted row.
func (s *AttachmentsStore) Create(ctx context.Context, a Attachment) (*Attachment, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO application_attachments
		   (attachment_id, application_id, r2_key, content_type, bytes, filename)
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6,''))`,
		a.AttachmentID, a.ApplicationID, a.R2Key, a.ContentType, a.Bytes, a.Filename)
	if err != nil {
		return nil, fmt.Errorf("applications: create attachment: %w", err)
	}
	return s.Get(ctx, a.AttachmentID)
}

// Get returns the attachment by ID regardless of deleted_at.
func (s *AttachmentsStore) Get(ctx context.Context, attachmentID string) (*Attachment, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT attachment_id, application_id, r2_key, content_type, bytes,
		         COALESCE(filename,''), deleted_at, created_at
		   FROM application_attachments WHERE attachment_id = $1`, attachmentID)
	var (
		a   Attachment
		del sql.NullTime
	)
	if err := row.Scan(&a.AttachmentID, &a.ApplicationID, &a.R2Key, &a.ContentType,
		&a.Bytes, &a.Filename, &del, &a.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("applications: get attachment: %w", err)
	}
	if del.Valid {
		t := del.Time
		a.DeletedAt = &t
	}
	return &a, nil
}

// SoftDelete marks the attachment deleted without removing the row.
func (s *AttachmentsStore) SoftDelete(ctx context.Context, attachmentID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE application_attachments SET deleted_at = now()
		  WHERE attachment_id = $1 AND deleted_at IS NULL`, attachmentID)
	if err != nil {
		return fmt.Errorf("applications: soft-delete attachment: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByApplication returns non-deleted attachments for an application,
// newest first.
func (s *AttachmentsStore) ListByApplication(ctx context.Context, applicationID string) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT attachment_id, application_id, r2_key, content_type, bytes,
		         COALESCE(filename,''), deleted_at, created_at
		   FROM application_attachments
		  WHERE application_id = $1 AND deleted_at IS NULL
		  ORDER BY created_at DESC, attachment_id ASC`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("applications: list attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Attachment, 0)
	for rows.Next() {
		var (
			a   Attachment
			del sql.NullTime
		)
		if err := rows.Scan(&a.AttachmentID, &a.ApplicationID, &a.R2Key, &a.ContentType,
			&a.Bytes, &a.Filename, &del, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("applications: scan attachment: %w", err)
		}
		if del.Valid {
			t := del.Time
			a.DeletedAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
