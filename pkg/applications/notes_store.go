package applications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Note is one row of the application_notes table.
type Note struct {
	NoteID        string
	ApplicationID string
	Body          string
	DeletedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NotesStore owns reads/writes against application_notes.
type NotesStore struct{ db *sql.DB }

// NewNotesStore wraps the given handle.
func NewNotesStore(db *sql.DB) *NotesStore { return &NotesStore{db: db} }

// Create inserts a new note and returns the persisted row.
func (s *NotesStore) Create(ctx context.Context, n Note) (*Note, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO application_notes (note_id, application_id, body) VALUES ($1,$2,$3)`,
		n.NoteID, n.ApplicationID, n.Body)
	if err != nil {
		return nil, fmt.Errorf("applications: create note: %w", err)
	}
	return s.Get(ctx, n.NoteID)
}

// Get returns the note by ID regardless of deleted_at.
func (s *NotesStore) Get(ctx context.Context, noteID string) (*Note, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT note_id, application_id, body, deleted_at, created_at, updated_at
		   FROM application_notes WHERE note_id = $1`, noteID)
	var (
		n   Note
		del sql.NullTime
	)
	if err := row.Scan(&n.NoteID, &n.ApplicationID, &n.Body, &del, &n.CreatedAt, &n.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("applications: get note: %w", err)
	}
	if del.Valid {
		t := del.Time
		n.DeletedAt = &t
	}
	return &n, nil
}

// Update overwrites the body of a non-deleted note.
func (s *NotesStore) Update(ctx context.Context, noteID, body string) (*Note, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE application_notes SET body = $1, updated_at = now()
		  WHERE note_id = $2 AND deleted_at IS NULL`, body, noteID)
	if err != nil {
		return nil, fmt.Errorf("applications: update note: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, noteID)
}

// SoftDelete marks the note deleted without removing the row.
func (s *NotesStore) SoftDelete(ctx context.Context, noteID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE application_notes SET deleted_at = now()
		  WHERE note_id = $1 AND deleted_at IS NULL`, noteID)
	if err != nil {
		return fmt.Errorf("applications: soft-delete note: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByApplication returns non-deleted notes for an application, newest first.
// limit <= 0 defaults to 200.
func (s *NotesStore) ListByApplication(ctx context.Context, applicationID string, limit int) ([]Note, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT note_id, application_id, body, deleted_at, created_at, updated_at
		   FROM application_notes
		  WHERE application_id = $1 AND deleted_at IS NULL
		  ORDER BY created_at DESC, note_id ASC
		  LIMIT $2`, applicationID, limit)
	if err != nil {
		return nil, fmt.Errorf("applications: list notes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Note, 0)
	for rows.Next() {
		var (
			n   Note
			del sql.NullTime
		)
		if err := rows.Scan(&n.NoteID, &n.ApplicationID, &n.Body, &del, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("applications: scan note: %w", err)
		}
		if del.Valid {
			t := del.Time
			n.DeletedAt = &t
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
