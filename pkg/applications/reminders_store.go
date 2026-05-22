package applications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ReminderStatus is the lifecycle state of a reminder.
type ReminderStatus string

const (
	ReminderPending   ReminderStatus = "pending"
	ReminderDone      ReminderStatus = "done"
	ReminderCancelled ReminderStatus = "cancelled"
)

// Reminder is one row of the application_reminders table.
type Reminder struct {
	ReminderID    string
	ApplicationID string
	DueAt         time.Time
	Status        ReminderStatus
	Note          string
	CompletedAt   *time.Time
	DeletedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// RemindersStore owns reads/writes against application_reminders.
type RemindersStore struct{ db *sql.DB }

// NewRemindersStore wraps the given handle.
func NewRemindersStore(db *sql.DB) *RemindersStore { return &RemindersStore{db: db} }

// Create inserts a new reminder and returns the persisted row.
// Status defaults to ReminderPending when empty.
func (s *RemindersStore) Create(ctx context.Context, r Reminder) (*Reminder, error) {
	if r.Status == "" {
		r.Status = ReminderPending
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO application_reminders (reminder_id, application_id, due_at, status, note)
		 VALUES ($1, $2, $3, $4, NULLIF($5,''))`,
		r.ReminderID, r.ApplicationID, r.DueAt, string(r.Status), r.Note)
	if err != nil {
		return nil, fmt.Errorf("applications: create reminder: %w", err)
	}
	return s.Get(ctx, r.ReminderID)
}

// Get returns the reminder by ID regardless of deleted_at.
func (s *RemindersStore) Get(ctx context.Context, reminderID string) (*Reminder, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT reminder_id, application_id, due_at, status, COALESCE(note,''),
		         completed_at, deleted_at, created_at, updated_at
		   FROM application_reminders WHERE reminder_id = $1`, reminderID)
	var (
		r           Reminder
		status      string
		completedAt sql.NullTime
		deletedAt   sql.NullTime
	)
	if err := row.Scan(&r.ReminderID, &r.ApplicationID, &r.DueAt, &status, &r.Note,
		&completedAt, &deletedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("applications: get reminder: %w", err)
	}
	r.Status = ReminderStatus(status)
	if completedAt.Valid {
		t := completedAt.Time
		r.CompletedAt = &t
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		r.DeletedAt = &t
	}
	return &r, nil
}

// ReminderPatch carries the fields Update will write. nil means "leave unchanged."
type ReminderPatch struct {
	DueAt  *time.Time
	Status *ReminderStatus
	Note   *string
}

// Update applies a partial patch to a non-deleted reminder.
// Transitioning to ReminderDone automatically sets completed_at.
func (s *RemindersStore) Update(ctx context.Context, reminderID string, p ReminderPatch) (*Reminder, error) {
	cur, err := s.Get(ctx, reminderID)
	if err != nil {
		return nil, err
	}
	if cur.DeletedAt != nil {
		return nil, ErrNotFound
	}
	dueAt := cur.DueAt
	if p.DueAt != nil {
		dueAt = *p.DueAt
	}
	status := cur.Status
	if p.Status != nil {
		status = *p.Status
	}
	note := cur.Note
	if p.Note != nil {
		note = *p.Note
	}
	var completedAt any
	if status == ReminderDone && cur.Status != ReminderDone {
		completedAt = time.Now()
	} else if cur.CompletedAt != nil {
		completedAt = *cur.CompletedAt
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE application_reminders
		    SET due_at = $1, status = $2, note = NULLIF($3,''),
		        completed_at = $4, updated_at = now()
		  WHERE reminder_id = $5`,
		dueAt, string(status), note, completedAt, reminderID)
	if err != nil {
		return nil, fmt.Errorf("applications: update reminder: %w", err)
	}
	return s.Get(ctx, reminderID)
}

// SoftDelete marks the reminder deleted without removing the row.
func (s *RemindersStore) SoftDelete(ctx context.Context, reminderID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE application_reminders SET deleted_at = now()
		  WHERE reminder_id = $1 AND deleted_at IS NULL`, reminderID)
	if err != nil {
		return fmt.Errorf("applications: soft-delete reminder: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListActiveByApplication returns non-deleted reminders for an application,
// ordered by due_at ASC.
func (s *RemindersStore) ListActiveByApplication(ctx context.Context, applicationID string) ([]Reminder, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT reminder_id, application_id, due_at, status, COALESCE(note,''),
		         completed_at, deleted_at, created_at, updated_at
		   FROM application_reminders
		  WHERE application_id = $1 AND deleted_at IS NULL
		  ORDER BY due_at ASC, reminder_id ASC`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("applications: list reminders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Reminder, 0)
	for rows.Next() {
		var (
			r           Reminder
			status      string
			completedAt sql.NullTime
			deletedAt   sql.NullTime
		)
		if err := rows.Scan(&r.ReminderID, &r.ApplicationID, &r.DueAt, &status, &r.Note,
			&completedAt, &deletedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("applications: scan reminder: %w", err)
		}
		r.Status = ReminderStatus(status)
		if completedAt.Valid {
			t := completedAt.Time
			r.CompletedAt = &t
		}
		if deletedAt.Valid {
			t := deletedAt.Time
			r.DeletedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
