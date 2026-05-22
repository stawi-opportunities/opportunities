package applications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Application is one row of the applications OLTP table.
type Application struct {
	ApplicationID string
	CandidateID   string
	OpportunityID string
	MatchID       string
	Status        Status
	CurrentStage  string
	Metadata      map[string]any
	SubmittedAt   *time.Time
	LastEventID   string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ErrNotFound mirrors pkg/matching's sentinel.
var ErrNotFound = errors.New("applications: not found")

// ErrAlreadyExists is returned by Create when the (candidate, opp) pair
// already has an application row.
var ErrAlreadyExists = errors.New("applications: already exists for pair")

// Store owns reads/writes against `applications`.
type Store struct{ db *sql.DB }

// NewStore wraps the given handle. The Store does not own the lifecycle
// of the handle — closing belongs to the caller.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const createApplicationSQL = `
INSERT INTO applications (
    application_id, candidate_id, opportunity_id, match_id,
    status, current_stage, metadata, last_event_id
) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
ON CONFLICT (candidate_id, opportunity_id) DO NOTHING
RETURNING application_id
`

// Create inserts a new application row. Returns ErrAlreadyExists when a
// row already exists for the (candidate_id, opportunity_id) pair.
func (s *Store) Create(ctx context.Context, a Application) (*Application, error) {
	if a.Status == "" {
		a.Status = StatusNew
	}
	md := a.Metadata
	if md == nil {
		md = map[string]any{}
	}
	var returnedID string
	err := s.db.QueryRowContext(ctx, createApplicationSQL,
		a.ApplicationID, a.CandidateID, a.OpportunityID, a.MatchID,
		string(a.Status), a.CurrentStage, mustEncodeJSON(md), a.LastEventID,
	).Scan(&returnedID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAlreadyExists
	}
	if err != nil {
		return nil, fmt.Errorf("applications: create: %w", err)
	}
	return s.GetByID(ctx, returnedID)
}

const getByIDSQL = `
SELECT application_id, candidate_id, opportunity_id, match_id,
       status, COALESCE(current_stage,''), metadata,
       submitted_at, COALESCE(last_event_id,''), created_at, updated_at
FROM applications
WHERE application_id = $1
`

const getByPairSQL = `
SELECT application_id, candidate_id, opportunity_id, match_id,
       status, COALESCE(current_stage,''), metadata,
       submitted_at, COALESCE(last_event_id,''), created_at, updated_at
FROM applications
WHERE candidate_id = $1 AND opportunity_id = $2
`

// GetByID returns the application with the given ID, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id string) (*Application, error) {
	return s.scanOne(ctx, getByIDSQL, id)
}

// GetByPair returns the application for a (candidateID, opportunityID) pair,
// or ErrNotFound.
func (s *Store) GetByPair(ctx context.Context, candidateID, opportunityID string) (*Application, error) {
	return s.scanOne(ctx, getByPairSQL, candidateID, opportunityID)
}

func (s *Store) scanOne(ctx context.Context, q string, args ...any) (*Application, error) {
	row := s.db.QueryRowContext(ctx, q, args...)
	var (
		a            Application
		status       string
		submittedAt  sql.NullTime
		metadataBlob []byte
	)
	err := row.Scan(
		&a.ApplicationID, &a.CandidateID, &a.OpportunityID, &a.MatchID,
		&status, &a.CurrentStage, &metadataBlob,
		&submittedAt, &a.LastEventID, &a.CreatedAt, &a.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("applications: scan: %w", err)
	}
	a.Status = Status(status)
	if submittedAt.Valid {
		t := submittedAt.Time
		a.SubmittedAt = &t
	}
	if len(metadataBlob) > 0 {
		_ = json.Unmarshal(metadataBlob, &a.Metadata)
	}
	if a.Metadata == nil {
		a.Metadata = map[string]any{}
	}
	return &a, nil
}

// ListByCandidateParams configures a paginated read.
type ListByCandidateParams struct {
	CandidateID string
	Statuses    []Status
	Cursor      string // opaque; returned by the previous page as NextCursor
	Limit       int    // default 50, max 200
}

// ListByCandidatePage is the result envelope.
type ListByCandidatePage struct {
	Items      []Application
	NextCursor string
	HasMore    bool
}

const defaultPageLimit = 50
const maxPageLimit = 200

// ListByCandidate returns one page of applications for a candidate, optionally
// filtered by status, ordered by (created_at DESC, application_id ASC) so the
// ordering is fully deterministic and cursor-resumable.
func (s *Store) ListByCandidate(ctx context.Context, p ListByCandidateParams) (ListByCandidatePage, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}

	statusStrs := make([]string, 0, len(p.Statuses))
	for _, st := range p.Statuses {
		statusStrs = append(statusStrs, string(st))
	}
	args := []any{p.CandidateID, statusStrs}
	where := "candidate_id = $1 AND (cardinality($2::text[]) = 0 OR status = ANY($2))"
	if p.Cursor != "" {
		cur, err := decodeCursor(p.Cursor)
		if err != nil {
			return ListByCandidatePage{}, fmt.Errorf("applications: cursor: %w", err)
		}
		args = append(args, cur.CreatedAt, cur.ApplicationID)
		where += " AND (created_at, application_id) < ($3::timestamptz, $4)"
	}
	args = append(args, limit+1)
	q := `
SELECT application_id, candidate_id, opportunity_id, match_id,
       status, COALESCE(current_stage,''), metadata,
       submitted_at, COALESCE(last_event_id,''), created_at, updated_at
FROM applications
WHERE ` + where + `
ORDER BY created_at DESC, application_id ASC
LIMIT $` + fmt.Sprint(len(args))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return ListByCandidatePage{}, fmt.Errorf("applications: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Application, 0, limit)
	for rows.Next() {
		var (
			a            Application
			status       string
			submittedAt  sql.NullTime
			metadataBlob []byte
		)
		if err := rows.Scan(
			&a.ApplicationID, &a.CandidateID, &a.OpportunityID, &a.MatchID,
			&status, &a.CurrentStage, &metadataBlob,
			&submittedAt, &a.LastEventID, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return ListByCandidatePage{}, fmt.Errorf("applications: list scan: %w", err)
		}
		a.Status = Status(status)
		if submittedAt.Valid {
			t := submittedAt.Time
			a.SubmittedAt = &t
		}
		if len(metadataBlob) > 0 {
			_ = json.Unmarshal(metadataBlob, &a.Metadata)
		}
		if a.Metadata == nil {
			a.Metadata = map[string]any{}
		}
		out = append(out, a)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	page := ListByCandidatePage{Items: out, HasMore: hasMore}
	if hasMore {
		last := out[len(out)-1]
		page.NextCursor = encodeCursor(pageCursor{CreatedAt: last.CreatedAt, ApplicationID: last.ApplicationID})
	}
	return page, nil
}

// Patch carries the fields ApplyTransition will write. Zero values are ignored.
type Patch struct {
	Status       *Status
	CurrentStage *string
	Metadata     map[string]any
	LastEventID  string
}

// ApplyTransition performs a state-machine-validated UPDATE.
//   - If newStatus is the same as the current row's status, returns the
//     current row (no-op on status, other fields still applied).
//   - Otherwise validates from→newStatus via ValidateTransition and
//     updates status + current_stage + metadata + last_event_id +
//     submitted_at (when transitioning into StatusSubmitted) in a single
//     statement.
//
// Returns ErrNotFound if the row is gone; returns *InvalidTransitionError
// when the transition is illegal.
func (s *Store) ApplyTransition(ctx context.Context, applicationID string, p Patch) (*Application, error) {
	cur, err := s.GetByID(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	newStatus := cur.Status
	if p.Status != nil {
		newStatus = *p.Status
		if newStatus != cur.Status {
			if err := ValidateTransition(cur.Status, newStatus); err != nil {
				return nil, err
			}
		}
	}
	currentStage := cur.CurrentStage
	if p.CurrentStage != nil {
		currentStage = *p.CurrentStage
	}
	// merge metadata (existing || incoming) — incoming wins on key collision
	md := cur.Metadata
	if md == nil {
		md = map[string]any{}
	}
	for k, v := range p.Metadata {
		md[k] = v
	}
	var submittedAt any
	if newStatus == StatusSubmitted && cur.Status != StatusSubmitted {
		now := time.Now()
		submittedAt = now
	} else if cur.SubmittedAt != nil {
		submittedAt = *cur.SubmittedAt
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE applications
   SET status        = $1,
       current_stage = NULLIF($2, ''),
       metadata      = $3::jsonb,
       last_event_id = $4,
       submitted_at  = $5,
       updated_at    = now()
 WHERE application_id = $6
`,
		string(newStatus), currentStage, mustEncodeJSON(md),
		p.LastEventID, submittedAt, applicationID,
	)
	if err != nil {
		return nil, fmt.Errorf("applications: apply transition: %w", err)
	}
	return s.GetByID(ctx, applicationID)
}

// ---- cursor helpers ----

type pageCursor struct {
	CreatedAt     time.Time `json:"c"`
	ApplicationID string    `json:"a"`
}

func encodeCursor(c pageCursor) string {
	b, _ := json.Marshal(c)
	return base64URLEncode(b)
}

func decodeCursor(s string) (pageCursor, error) {
	raw, err := base64URLDecode(s)
	if err != nil {
		return pageCursor{}, err
	}
	var c pageCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return pageCursor{}, err
	}
	if c.ApplicationID == "" || c.CreatedAt.IsZero() {
		return pageCursor{}, fmt.Errorf("missing fields")
	}
	return c, nil
}
