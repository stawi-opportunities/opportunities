package matching

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

// RulesRow is one row of match_rules. The Document is the canonical
// JSON body validated by pkg/applications/rules.go.
type RulesRow struct {
	CandidateID string
	Document    applications.Rules
	Version     int
	Enabled     bool
	Autoapply   bool
	UpdatedAt   time.Time
}

// RulesStore reads and writes per-candidate autonomy rules from the
// match_rules table.
type RulesStore struct{ db *sql.DB }

// NewRulesStore wraps the given database handle.
func NewRulesStore(db *sql.DB) *RulesStore { return &RulesStore{db: db} }

// Get returns the candidate's rules. Returns ErrNotFound when no row
// exists for the candidate.
func (s *RulesStore) Get(ctx context.Context, candidateID string) (*RulesRow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT candidate_id, document, version, enabled, autoapply, updated_at
		   FROM match_rules WHERE candidate_id = $1`, candidateID)
	var (
		rr   RulesRow
		body []byte
	)
	if err := row.Scan(&rr.CandidateID, &body, &rr.Version, &rr.Enabled, &rr.Autoapply, &rr.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("matching: rules get: %w", err)
	}
	doc, err := applications.ParseRules(body)
	if err != nil {
		return nil, fmt.Errorf("matching: rules parse: %w", err)
	}
	rr.Document = doc
	return &rr, nil
}

// Upsert replaces the rules document. The denormalized boolean columns
// (enabled, autoapply) are kept in sync so fan-out filters stay fast.
// version increments on every successful write.
func (s *RulesStore) Upsert(ctx context.Context, candidateID string, doc applications.Rules) (*RulesRow, error) {
	body, err := applications.MarshalRules(doc)
	if err != nil {
		return nil, fmt.Errorf("matching: rules marshal: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO match_rules (candidate_id, document, version, enabled, autoapply, updated_at)
VALUES ($1, $2::jsonb, 1, $3, $4, now())
ON CONFLICT (candidate_id) DO UPDATE
   SET document   = EXCLUDED.document,
       version    = match_rules.version + 1,
       enabled    = EXCLUDED.enabled,
       autoapply  = EXCLUDED.autoapply,
       updated_at = now()
`, candidateID, body, doc.Enabled, doc.Autoapply.Enabled)
	if err != nil {
		return nil, fmt.Errorf("matching: rules upsert: %w", err)
	}
	return s.Get(ctx, candidateID)
}
