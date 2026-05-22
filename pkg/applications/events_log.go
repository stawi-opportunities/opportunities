package applications

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// EventKind enumerates the events the audit log accepts.
type EventKind string

const (
	EventKindCreated             EventKind = "created"
	EventKindStateChanged        EventKind = "state_changed"
	EventKindSubmissionAttempted EventKind = "submission_attempted"
	EventKindSubmissionSucceeded EventKind = "submission_succeeded"
	EventKindSubmissionFailed    EventKind = "submission_failed"
	EventKindRecruiterReplied    EventKind = "recruiter_replied"
	EventKindNoteAdded           EventKind = "note_added"
	EventKindNoteEdited          EventKind = "note_edited"
	EventKindNoteDeleted         EventKind = "note_deleted"
	EventKindAttachmentAdded     EventKind = "attachment_added"
	EventKindAttachmentDeleted   EventKind = "attachment_deleted"
	EventKindReminderSet         EventKind = "reminder_set"
	EventKindReminderDone        EventKind = "reminder_done"
)

// Actor enumerates who/what produced an event.
type Actor string

const (
	ActorUser      Actor = "user"
	ActorExtension Actor = "extension"
	ActorSystem    Actor = "system"
	ActorAdmin     Actor = "admin"
)

// Event is one row of application_events.
type Event struct {
	EventID       string
	OccurredAt    time.Time
	ApplicationID string
	CandidateID   string
	Kind          EventKind
	FromStatus    Status
	ToStatus      Status
	Actor         Actor
	Data          map[string]any
}

// EventLog is an append-only writer for the application_events hypertable.
type EventLog struct{ db *sql.DB }

// NewEventLog wraps the given handle.
func NewEventLog(db *sql.DB) *EventLog { return &EventLog{db: db} }

const insertEventSQL = `
INSERT INTO application_events
    (event_id, occurred_at, application_id, candidate_id,
     kind, from_status, to_status, actor, data)
VALUES ($1, COALESCE($2, now()), $3, $4, $5, NULLIF($6,''), NULLIF($7,''), $8, $9::jsonb)
ON CONFLICT (event_id, occurred_at) DO NOTHING
`

// Write appends a single event to application_events. Duplicate
// (event_id, occurred_at) pairs are silently ignored (idempotent).
func (e *EventLog) Write(ctx context.Context, evt Event) error {
	data := evt.Data
	if data == nil {
		data = map[string]any{}
	}
	var occurred any
	if !evt.OccurredAt.IsZero() {
		occurred = evt.OccurredAt
	}
	_, err := e.db.ExecContext(ctx, insertEventSQL,
		evt.EventID, occurred, evt.ApplicationID, evt.CandidateID,
		string(evt.Kind),
		string(evt.FromStatus), string(evt.ToStatus),
		string(evt.Actor),
		mustEncodeJSON(data),
	)
	if err != nil {
		return fmt.Errorf("applications: write event: %w", err)
	}
	return nil
}

// ListByApplication returns events for an application ordered most-recent first.
func (e *EventLog) ListByApplication(ctx context.Context, applicationID string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 200
	}
	const q = `
SELECT event_id, occurred_at, application_id, candidate_id,
       kind, COALESCE(from_status,''), COALESCE(to_status,''), actor, data
FROM application_events
WHERE application_id = $1
ORDER BY occurred_at DESC, event_id ASC
LIMIT $2
`
	rows, err := e.db.QueryContext(ctx, q, applicationID, limit)
	if err != nil {
		return nil, fmt.Errorf("applications: list events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Event, 0, 16)
	for rows.Next() {
		var (
			evt     Event
			kind    string
			from    string
			to      string
			actor   string
			dataRaw []byte
		)
		if err := rows.Scan(&evt.EventID, &evt.OccurredAt, &evt.ApplicationID,
			&evt.CandidateID, &kind, &from, &to, &actor, &dataRaw); err != nil {
			return nil, fmt.Errorf("applications: list events scan: %w", err)
		}
		evt.Kind = EventKind(kind)
		evt.FromStatus = Status(from)
		evt.ToStatus = Status(to)
		evt.Actor = Actor(actor)
		if len(dataRaw) > 0 {
			_ = json.Unmarshal(dataRaw, &evt.Data)
		}
		if evt.Data == nil {
			evt.Data = map[string]any{}
		}
		out = append(out, evt)
	}
	return out, rows.Err()
}
