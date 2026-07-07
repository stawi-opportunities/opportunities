// Package candidatestore reads candidate matching state from PostgreSQL.
package candidatestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

var ErrNotFound = errors.New("candidatestore: candidate state not found")
var ErrEmbeddingNotFound = fmt.Errorf("%w: no embedding row", ErrNotFound)

type Reader struct{ db *sql.DB }

func NewReader(db *sql.DB) *Reader { return &Reader{db: db} }

func SavePreferences(ctx context.Context, db *sql.DB, p eventsv1.PreferencesUpdatedV1) error {
	b, err := json.Marshal(p.OptIns)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO candidate_preferences (candidate_id,opt_ins,updated_at)
		VALUES ($1,$2::jsonb,$3) ON CONFLICT (candidate_id) DO UPDATE SET
		opt_ins=EXCLUDED.opt_ins, updated_at=GREATEST(EXCLUDED.updated_at,candidate_preferences.updated_at)`,
		p.CandidateID, string(b), p.UpdatedAt)
	return err
}

func (r *Reader) LatestEmbedding(ctx context.Context, candidateID string) (eventsv1.CandidateEmbeddingV1, error) {
	var vector, model string
	var updated time.Time
	err := r.db.QueryRowContext(ctx, `SELECT embedding::text, 'candidate-match-index', updated_at
		FROM candidate_match_indexes WHERE candidate_id=$1 AND enabled=true`, candidateID).Scan(&vector, &model, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return eventsv1.CandidateEmbeddingV1{}, ErrEmbeddingNotFound
	}
	if err != nil {
		return eventsv1.CandidateEmbeddingV1{}, fmt.Errorf("candidatestore: embedding: %w", err)
	}
	return eventsv1.CandidateEmbeddingV1{CandidateID: candidateID, Vector: matching.ParseVectorLiteral(vector), ModelVersion: model, OccurredAt: updated}, nil
}

func (r *Reader) LatestPreferences(ctx context.Context, candidateID string) (eventsv1.PreferencesUpdatedV1, error) {
	var raw []byte
	var updated time.Time
	err := r.db.QueryRowContext(ctx, `SELECT opt_ins, updated_at FROM candidate_preferences WHERE candidate_id=$1`, candidateID).Scan(&raw, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return eventsv1.PreferencesUpdatedV1{}, ErrNotFound
	}
	if err != nil {
		return eventsv1.PreferencesUpdatedV1{}, fmt.Errorf("candidatestore: preferences: %w", err)
	}
	var optIns map[string]json.RawMessage
	if err := json.Unmarshal(raw, &optIns); err != nil {
		return eventsv1.PreferencesUpdatedV1{}, fmt.Errorf("candidatestore: preferences JSON: %w", err)
	}
	return eventsv1.PreferencesUpdatedV1{CandidateID: candidateID, OptIns: optIns, UpdatedAt: updated, OccurredAt: updated}, nil
}
