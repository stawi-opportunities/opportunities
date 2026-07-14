package placement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Store persists placement profile summaries.
type Store interface {
	EnsureSchema(ctx context.Context) error
	Upsert(ctx context.Context, doc Document) (version int, err error)
	Get(ctx context.Context, candidateID string) (*Document, error)
}

// SQLStore is Postgres-backed placement profile storage.
type SQLStore struct {
	DB *sql.DB
}

// EnsureSchema creates candidate_placement_profiles if missing.
func (s *SQLStore) EnsureSchema(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("placement: db is nil")
	}
	const ddl = `
CREATE TABLE IF NOT EXISTS candidate_placement_profiles (
    candidate_id          text PRIMARY KEY,
    version               int  NOT NULL DEFAULT 1,
    summary_text          text NOT NULL DEFAULT '',
    qualifications_text   text NOT NULL DEFAULT '',
    preferences_text      text NOT NULL DEFAULT '',
    missing               text[] NOT NULL DEFAULT '{}',
    ready                 boolean NOT NULL DEFAULT false,
    updated_at            timestamptz NOT NULL DEFAULT now()
);
`
	_, err := s.DB.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("placement: ensure schema: %w", err)
	}
	return nil
}

// Upsert writes the placement document and increments version when summary changes.
func (s *SQLStore) Upsert(ctx context.Context, doc Document) (int, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("placement: db is nil")
	}
	if doc.CandidateID == "" {
		return 0, fmt.Errorf("placement: candidate_id required")
	}
	var prevSummary string
	var prevVer int
	err := s.DB.QueryRowContext(ctx,
		`SELECT summary_text, version FROM candidate_placement_profiles WHERE candidate_id = $1`,
		doc.CandidateID,
	).Scan(&prevSummary, &prevVer)
	version := 1
	switch {
	case errors.Is(err, sql.ErrNoRows):
		version = 1
	case err != nil:
		return 0, fmt.Errorf("placement: load prior: %w", err)
	default:
		if prevSummary == doc.SummaryText {
			version = prevVer
		} else {
			version = prevVer + 1
		}
	}

	const q = `
INSERT INTO candidate_placement_profiles (
    candidate_id, version, summary_text, qualifications_text, preferences_text,
    missing, ready, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,now())
ON CONFLICT (candidate_id) DO UPDATE SET
    version             = EXCLUDED.version,
    summary_text        = EXCLUDED.summary_text,
    qualifications_text = EXCLUDED.qualifications_text,
    preferences_text    = EXCLUDED.preferences_text,
    missing             = EXCLUDED.missing,
    ready               = EXCLUDED.ready,
    updated_at          = now()
`
	_, err = s.DB.ExecContext(ctx, q,
		doc.CandidateID, version, doc.SummaryText, doc.QualificationsText, doc.PreferencesText,
		pq.Array(doc.Missing), doc.Ready,
	)
	if err != nil {
		return 0, fmt.Errorf("placement: upsert: %w", err)
	}
	return version, nil
}

// Get returns the current placement profile or nil.
func (s *SQLStore) Get(ctx context.Context, candidateID string) (*Document, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("placement: db is nil")
	}
	const q = `
SELECT candidate_id, version, summary_text, qualifications_text, preferences_text,
       missing, ready, updated_at
FROM candidate_placement_profiles
WHERE candidate_id = $1
`
	var d Document
	var missing pq.StringArray
	var updated time.Time
	err := s.DB.QueryRowContext(ctx, q, candidateID).Scan(
		&d.CandidateID, &d.Version, &d.SummaryText, &d.QualificationsText, &d.PreferencesText,
		&missing, &d.Ready, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("placement: get: %w", err)
	}
	d.Missing = []string(missing)
	return &d, nil
}

// parse helper kept for tests
func parsePGTextArray(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return nil
	}
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
