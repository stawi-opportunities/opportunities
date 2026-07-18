package matching

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// CandidateIndex is the denormalized row stored in candidate_match_indexes.
// The fan-out worker reads this to decide which opportunities to send to a
// given candidate.
type CandidateIndex struct {
	CandidateID    string
	Embedding      []float32
	MinScore       float64
	DailyCap       int
	WeeklyCap      int
	Kinds          []string
	Countries      []string
	SalaryFloorUSD *int
	RemoteOnly     bool
	// RerankText is short persona text for Path A cross-encoder stage-2.
	// Non-empty also marks the row as persona-owned for dual-writer protection.
	RerankText string
	Enabled    bool
	UpdatedAt  time.Time
}

// IndexStore persists and retrieves CandidateIndex rows.
type IndexStore struct {
	db *sql.DB
}

// NewIndexStore constructs an IndexStore backed by db.
func NewIndexStore(db *sql.DB) *IndexStore { return &IndexStore{db: db} }

// CandidatePlanID returns the plan_id on candidate_profiles for entitlement
// mapping. Empty string when the row is missing (caller uses defaults).
func (s *IndexStore) CandidatePlanID(ctx context.Context, candidateID string) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	var plan string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(plan_id, '') FROM candidate_profiles WHERE id = $1`,
		candidateID,
	).Scan(&plan)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return plan, nil
}

const upsertIndexSQL = `
INSERT INTO candidate_match_indexes
    (candidate_id, embedding, min_score, daily_cap, weekly_cap,
     kinds, countries, salary_floor_usd, remote_only, rerank_text, enabled, updated_at)
VALUES ($1, $2::vector, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
ON CONFLICT (candidate_id) DO UPDATE
   SET embedding        = EXCLUDED.embedding,
       min_score        = EXCLUDED.min_score,
       daily_cap        = EXCLUDED.daily_cap,
       weekly_cap       = EXCLUDED.weekly_cap,
       kinds            = EXCLUDED.kinds,
       countries        = EXCLUDED.countries,
       salary_floor_usd = EXCLUDED.salary_floor_usd,
       remote_only      = EXCLUDED.remote_only,
       rerank_text      = CASE
                            WHEN EXCLUDED.rerank_text <> '' THEN EXCLUDED.rerank_text
                            ELSE candidate_match_indexes.rerank_text
                          END,
       enabled          = EXCLUDED.enabled,
       updated_at       = now()
`

// Upsert inserts or fully replaces the CandidateIndex for ci.CandidateID.
func (s *IndexStore) Upsert(ctx context.Context, ci CandidateIndex) error {
	var floor any
	if ci.SalaryFloorUSD != nil {
		floor = *ci.SalaryFloorUSD
	}
	_, err := s.db.ExecContext(ctx, upsertIndexSQL,
		ci.CandidateID, vectorLiteral(ci.Embedding),
		ci.MinScore, ci.DailyCap, ci.WeeklyCap,
		pq.Array(ci.Kinds), pq.Array(ci.Countries),
		floor, ci.RemoteOnly, ci.RerankText, ci.Enabled,
	)
	if err != nil {
		return fmt.Errorf("matching: index upsert: %w", err)
	}
	return nil
}

const getIndexSQL = `
SELECT candidate_id, embedding::text, min_score, daily_cap, weekly_cap,
       kinds, countries, salary_floor_usd, remote_only,
       COALESCE(rerank_text, ''), enabled, updated_at
FROM candidate_match_indexes
WHERE candidate_id = $1
`

// IndexFilters are preference filters applied without replacing the embedding.
type IndexFilters struct {
	Countries      []string
	Kinds          []string
	SalaryFloorUSD *int
	RemoteOnly     bool
}

// UpsertFilters updates countries/kinds/salary filters when an index row
// already exists (embedding is NOT NULL so we cannot create a filter-only row).
func (s *IndexStore) UpsertFilters(ctx context.Context, candidateID string, f IndexFilters) error {
	if s == nil || s.db == nil || candidateID == "" {
		return nil
	}
	var floor any
	if f.SalaryFloorUSD != nil {
		floor = *f.SalaryFloorUSD
	}
	kinds := f.Kinds
	if len(kinds) == 0 {
		kinds = []string{"job"}
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE candidate_match_indexes SET
    kinds = $2,
    countries = $3,
    salary_floor_usd = COALESCE($4, salary_floor_usd),
    remote_only = $5,
    updated_at = now()
WHERE candidate_id = $1
`, candidateID, pq.Array(kinds), pq.Array(f.Countries), floor, f.RemoteOnly)
	if err != nil {
		return fmt.Errorf("matching: index filter upsert: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns the CandidateIndex for candidateID, or ErrNotFound if absent.
func (s *IndexStore) Get(ctx context.Context, candidateID string) (*CandidateIndex, error) {
	row := s.db.QueryRowContext(ctx, getIndexSQL, candidateID)
	var (
		ci        CandidateIndex
		embText   string
		kinds     pq.StringArray
		countries pq.StringArray
		floor     sql.NullInt64
	)
	err := row.Scan(
		&ci.CandidateID, &embText, &ci.MinScore, &ci.DailyCap, &ci.WeeklyCap,
		&kinds, &countries, &floor, &ci.RemoteOnly, &ci.RerankText, &ci.Enabled, &ci.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("matching: index get: %w", err)
	}
	ci.Embedding = parseVectorLiteral(embText)
	ci.Kinds = []string(kinds)
	ci.Countries = []string(countries)
	if floor.Valid {
		v := int(floor.Int64)
		ci.SalaryFloorUSD = &v
	}
	return &ci, nil
}

// vectorLiteral renders []float32 as pgvector's textual form "[a,b,c]".
func vectorLiteral(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// parseVectorLiteral converts pgvector textual form "[a,b,c]" to []float32.
// Parsing errors are silent — bad input yields nil, matching the package's
// soft-fail pattern.
func parseVectorLiteral(s string) []float32 {
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]float32, len(parts))
	for i, p := range parts {
		f, _ := strconv.ParseFloat(strings.TrimSpace(p), 32)
		out[i] = float32(f)
	}
	return out
}

// ParseVectorLiteral exports the textual-vector parser for callers that read
// pgvector columns directly. Errors are silent — bad input yields a nil slice.
func ParseVectorLiteral(s string) []float32 { return parseVectorLiteral(s) }
