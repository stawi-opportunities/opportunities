package matching

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Store owns reads and writes against candidate_matches.
type Store struct {
	db *sql.DB
}

// NewStore wraps the given handle. The Store does not own the lifecycle
// of the handle — closing belongs to the caller.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// ErrNotFound is returned by GetByPair when no row exists.
var ErrNotFound = errors.New("matching: not found")

// upsertOneSQL is intentionally a single statement: the UNIQUE constraint
// on (candidate_id, opportunity_id) collides, and ON CONFLICT branches
// into the score-monotonic, status='new'-guarded update.
//
// RETURNING (xmax = 0) AS inserted distinguishes a fresh INSERT (xmax=0)
// from an UPDATE (xmax holds the old row's transaction ID). When the
// conflict fires but the WHERE guard prevents the update, no row is
// returned — the caller treats sql.ErrNoRows as inserted=false.
const upsertOneSQL = `
INSERT INTO candidate_matches (
    match_id, candidate_id, opportunity_id, status,
    score, rerank_score, reranker_used,
    last_event_id, metadata
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb)
ON CONFLICT (candidate_id, opportunity_id) DO UPDATE
   SET score         = GREATEST(candidate_matches.score, EXCLUDED.score),
       rerank_score  = COALESCE(EXCLUDED.rerank_score, candidate_matches.rerank_score),
       reranker_used = candidate_matches.reranker_used OR EXCLUDED.reranker_used,
       last_event_id = EXCLUDED.last_event_id,
       metadata      = candidate_matches.metadata || EXCLUDED.metadata,
       updated_at    = now()
   WHERE candidate_matches.status = 'new'
     AND EXCLUDED.score > candidate_matches.score
RETURNING (xmax = 0) AS inserted
`

// UpsertMatch inserts a new row, or updates an existing pair-row when:
//   - the existing status is 'new' (terminal states are protected), and
//   - the incoming score is strictly greater (score-monotonic).
//
// Returns inserted=true when a brand-new row was created, false when an
// existing row was updated or when the conflict guard prevented any change.
// This is a best-effort telemetry signal; correctness does not depend on it.
func (s *Store) UpsertMatch(ctx context.Context, m Match) (bool, error) {
	if m.Status == "" {
		m.Status = StatusNew
	}
	md := m.Metadata
	if md == nil {
		md = map[string]any{}
	}
	var inserted bool
	err := s.db.QueryRowContext(ctx, upsertOneSQL,
		m.MatchID, m.CandidateID, m.OpportunityID,
		string(m.Status), m.Score, nullableF64(m.RerankScore),
		m.RerankerUsed, m.LastEventID, mustEncodeJSON(md),
	).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		// Conflict fired but WHERE guard blocked the update — row unchanged.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("matching: upsert: %w", err)
	}
	return inserted, nil
}

const getByPairSQL = `
SELECT match_id, candidate_id, opportunity_id, status,
       score, rerank_score, reranker_used,
       viewed_at, applied_at, dismissed_at,
       last_event_id, metadata, created_at, updated_at
FROM candidate_matches
WHERE candidate_id = $1 AND opportunity_id = $2
`

// GetByPair returns the current row for (candidate_id, opportunity_id),
// or ErrNotFound.
func (s *Store) GetByPair(ctx context.Context, candidateID, opportunityID string) (*Match, error) {
	row := s.db.QueryRowContext(ctx, getByPairSQL, candidateID, opportunityID)
	return scanMatch(row.Scan)
}

func nullableF64(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func scanMatch(scan func(...any) error) (*Match, error) {
	var (
		m         Match
		status    string
		rerank    sql.NullFloat64
		viewedAt  sql.NullTime
		appliedAt sql.NullTime
		dismAt    sql.NullTime
		metaRaw   []byte
	)
	err := scan(
		&m.MatchID, &m.CandidateID, &m.OpportunityID, &status,
		&m.Score, &rerank, &m.RerankerUsed,
		&viewedAt, &appliedAt, &dismAt,
		&m.LastEventID, &metaRaw, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("matching: scan: %w", err)
	}
	m.Status = MatchStatus(status)
	if rerank.Valid {
		v := rerank.Float64
		m.RerankScore = &v
	}
	if viewedAt.Valid {
		t := viewedAt.Time
		m.ViewedAt = &t
	}
	if appliedAt.Valid {
		t := appliedAt.Time
		m.AppliedAt = &t
	}
	if dismAt.Valid {
		t := dismAt.Time
		m.DismissedAt = &t
	}
	if len(metaRaw) > 0 {
		m.Metadata = decodeJSONMap(metaRaw)
	} else {
		m.Metadata = map[string]any{}
	}
	return &m, nil
}

// UpsertMatches writes a batch via a single transaction using per-row
// upserts. Empty input is a no-op.
//
// Note: The unnest-based bulk SQL was attempted first but the pgx/stdlib
// driver does not support passing []sql.NullFloat64 as a Postgres float8[]
// array parameter, so we fall back to the tx-loop implementation which
// reuses the well-tested upsertOneSQL and enforces the same invariants.
func (s *Store) UpsertMatches(ctx context.Context, ms []Match) error {
	if len(ms) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("matching: begin bulk: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, m := range ms {
		md := m.Metadata
		if md == nil {
			md = map[string]any{}
		}
		if m.Status == "" {
			m.Status = StatusNew
		}
		if _, err := tx.ExecContext(ctx, upsertOneSQL,
			m.MatchID, m.CandidateID, m.OpportunityID,
			string(m.Status), m.Score, nullableF64(m.RerankScore),
			m.RerankerUsed, m.LastEventID, mustEncodeJSON(md),
		); err != nil {
			return fmt.Errorf("matching: bulk row %s: %w", m.MatchID, err)
		}
	}
	return tx.Commit()
}

// ListByCandidateParams configures a paginated read.
type ListByCandidateParams struct {
	CandidateID string
	Statuses    []MatchStatus // empty defaults to [new, viewed, applying]
	Cursor      string        // opaque; returned by the previous page as NextCursor
	Limit       int           // default 50, max 200
}

// ListByCandidatePage is the result envelope.
type ListByCandidatePage struct {
	Items      []Match
	NextCursor string
	HasMore    bool
}

const defaultPageLimit = 50
const maxPageLimit = 200

// ListByCandidate returns one page of matches for a candidate, ordered
// by (score DESC, created_at DESC, match_id ASC) so the ordering is
// fully deterministic and cursor-resumable.
func (s *Store) ListByCandidate(ctx context.Context, p ListByCandidateParams) (ListByCandidatePage, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}

	statuses := p.Statuses
	if len(statuses) == 0 {
		statuses = []MatchStatus{StatusNew, StatusViewed, StatusApplying}
	}
	statusStrs := make([]string, len(statuses))
	for i, st := range statuses {
		statusStrs[i] = string(st)
	}

	args := []any{p.CandidateID, statusStrs}
	where := "candidate_id = $1 AND status = ANY($2)"
	if p.Cursor != "" {
		cur, err := decodeCursor(p.Cursor)
		if err != nil {
			return ListByCandidatePage{}, fmt.Errorf("matching: cursor: %w", err)
		}
		args = append(args, cur.Score, cur.CreatedAt, cur.MatchID)
		where += ` AND (
            (score, created_at, match_id) < ($3, $4::timestamptz, $5)
        )`
	}
	args = append(args, limit+1)
	q := `
SELECT match_id, candidate_id, opportunity_id, status,
       score, rerank_score, reranker_used,
       viewed_at, applied_at, dismissed_at,
       last_event_id, metadata, created_at, updated_at
FROM candidate_matches
WHERE ` + where + `
ORDER BY score DESC, created_at DESC, match_id ASC
LIMIT $` + fmt.Sprint(len(args))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return ListByCandidatePage{}, fmt.Errorf("matching: list: %w", err)
	}
	defer rows.Close()

	out := make([]Match, 0, limit)
	for rows.Next() {
		m, err := scanMatch(rows.Scan)
		if err != nil {
			return ListByCandidatePage{}, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return ListByCandidatePage{}, fmt.Errorf("matching: list rows: %w", err)
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	var nextCur string
	if hasMore {
		last := out[len(out)-1]
		nextCur = encodeCursor(pageCursor{
			Score:     last.Score,
			CreatedAt: last.CreatedAt,
			MatchID:   last.MatchID,
		})
	}
	return ListByCandidatePage{Items: out, NextCursor: nextCur, HasMore: hasMore}, nil
}

type pageCursor struct {
	Score     float64   `json:"s"`
	CreatedAt time.Time `json:"c"`
	MatchID   string    `json:"m"`
}

func encodeCursor(c pageCursor) string {
	b := mustEncodeJSON(c)
	return base64.URLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (pageCursor, error) {
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return pageCursor{}, err
	}
	var c pageCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return pageCursor{}, err
	}
	if c.MatchID == "" || c.CreatedAt.IsZero() {
		return pageCursor{}, fmt.Errorf("matching: cursor: missing required fields")
	}
	return c, nil
}

// mustEncodeJSON marshals v to JSON bytes. Panics on failure — the metadata
// map is built from typed Go values so failure here is a programmer error,
// not a runtime concern.
func mustEncodeJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("matching: metadata not JSON-encodable: " + err.Error())
	}
	return b
}

func decodeJSONMap(raw []byte) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}
