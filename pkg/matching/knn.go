package matching

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
)

type KNN struct {
	db *sql.DB
}

func NewKNN(db *sql.DB) *KNN { return &KNN{db: db} }

type FanOutKNNParams struct {
	OppEmbedding []float32
	OppKind      string
	OppCountry   string
	OppSalaryMax *int
	Limit        int
}

type CandidateHit struct {
	CandidateID    string
	Distance       float64
	MinScore       float64
	DailyCap       int
	WeeklyCap      int
	Kinds          []string
	Countries      []string
	SalaryFloorUSD *int
	// Text is short conversation-grounded persona (rerank_text) for stage-2.
	Text string
}

const fanOutKNNSQL = `
SELECT candidate_id,
       embedding <=> $1::vector AS distance,
       min_score, daily_cap, weekly_cap, kinds, countries, salary_floor_usd,
       COALESCE(rerank_text, '') AS text
FROM candidate_match_indexes
WHERE enabled = TRUE
  AND $2 = ANY(kinds)
  AND ( $3 = '' OR cardinality(countries) = 0 OR $3 = ANY(countries) OR 'remote' = ANY(countries) )
  AND ( $4::int IS NULL OR salary_floor_usd IS NULL OR salary_floor_usd <= $4::int )
ORDER BY embedding <=> $1::vector
LIMIT $5
`

func (k *KNN) FanOutKNN(ctx context.Context, p FanOutKNNParams) ([]CandidateHit, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 500
	}
	var salaryMax any
	if p.OppSalaryMax != nil {
		salaryMax = *p.OppSalaryMax
	}
	rows, err := k.db.QueryContext(ctx, fanOutKNNSQL,
		vectorLiteral(p.OppEmbedding), p.OppKind, p.OppCountry, salaryMax, limit)
	if err != nil {
		return nil, fmt.Errorf("matching: fan-out knn: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]CandidateHit, 0, limit)
	for rows.Next() {
		var (
			h         CandidateHit
			kinds     pq.StringArray
			countries pq.StringArray
			floor     sql.NullInt64
		)
		if err := rows.Scan(&h.CandidateID, &h.Distance, &h.MinScore,
			&h.DailyCap, &h.WeeklyCap, &kinds, &countries, &floor, &h.Text); err != nil {
			return nil, fmt.Errorf("matching: fan-out knn scan: %w", err)
		}
		h.Kinds = []string(kinds)
		h.Countries = []string(countries)
		if floor.Valid {
			v := int(floor.Int64)
			h.SalaryFloorUSD = &v
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ReverseKNNParams configures semantic (pgvector) opportunity retrieval for a
// candidate embedding. Retrieval is semantic-first: ORDER BY embedding distance
// uses the opportunities HNSW index. Hard country filters only when
// HardCountries is true; kinds hard-filter by default when Kinds is non-empty
// (product scopes job vs scholarship) unless SoftKinds is set.
type ReverseKNNParams struct {
	CandidateEmbedding []float32
	Kinds              []string
	Countries          []string
	// SoftKinds when true skips the kind SQL filter even if Kinds is set.
	// Default false: kind is hard-scoped when Kinds is non-empty.
	SoftKinds bool
	// HardCountries when true applies countries as a SQL filter (legacy).
	// Default false: countries still influence Score.GeoMatch after retrieval.
	HardCountries bool
	Since         time.Time
	// Limit is the semantic recall size (nearest neighbours). Default 250.
	Limit int
	// MaxDistance is pgvector cosine distance ceiling (0–2). ≤0 = no cap.
	// Typical semantic cutoff ~0.85–1.0 (similarity ≳ 0.5–0.575).
	MaxDistance float64
}

type OppHit struct {
	OpportunityID string
	Distance      float64
	Kind          string
	Country       string
	FirstSeenAt   time.Time
	// Text is the opportunity's title + truncated description, used as the
	// document passed to the cross-encoder reranker.
	Text string
}

// reverseKNNSQL is semantic-first: ORDER BY embedding distance uses the
// opportunities HNSW index (vector_cosine_ops). Hard filters only drop
// inactive / non-embedded rows and optional kind/country/distance caps.
const reverseKNNSQL = `
SELECT canonical_id,
       embedding <=> $1::vector AS distance,
       COALESCE(kind, '')    AS kind,
       COALESCE(country, '') AS country,
       first_seen_at,
       COALESCE(title,'') || ' ' || left(COALESCE(attributes->>'description',''), 1500) AS text
FROM opportunities
WHERE status = 'active'
  AND hidden = false
  AND embedding IS NOT NULL
  AND first_seen_at > $2
  AND ( NOT $3::bool OR cardinality($4::text[]) = 0 OR kind = ANY($4) )
  AND ( NOT $5::bool OR cardinality($6::text[]) = 0 OR country IS NULL OR country = ANY($6) )
  AND ( $7::float8 <= 0 OR (embedding <=> $1::vector) <= $7::float8 )
ORDER BY embedding <=> $1::vector
LIMIT $8
`

// DefaultReverseKNNLimit is the default semantic recall size for MatchInvoke /
// GapFill (larger than the old 100 so HNSW surfaces more true neighbours).
const DefaultReverseKNNLimit = 250

// DefaultSemanticMaxDistance is the default cosine-distance ceiling applied
// during reverse-KNN. Cosine similarity ≳ 1 − 0.90/2 = 0.55. ≤0 in
// ReverseKNNParams means "no SQL distance cap".
const DefaultSemanticMaxDistance = 0.90

func (k *KNN) ReverseKNN(ctx context.Context, p ReverseKNNParams) ([]OppHit, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultReverseKNNLimit
	}
	// Semantic-first defaults: kind scopes product surface; country is soft
	// (score-time GeoMatch) unless HardCountries is set.
	hardKinds := len(p.Kinds) > 0 && !p.SoftKinds
	hardCountries := p.HardCountries

	rows, err := k.db.QueryContext(ctx, reverseKNNSQL,
		vectorLiteral(p.CandidateEmbedding), p.Since,
		hardKinds, pq.Array(p.Kinds),
		hardCountries, pq.Array(p.Countries),
		p.MaxDistance, limit)
	if err != nil {
		return nil, fmt.Errorf("matching: reverse knn: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]OppHit, 0, limit)
	for rows.Next() {
		var h OppHit
		if err := rows.Scan(&h.OpportunityID, &h.Distance, &h.Kind,
			&h.Country, &h.FirstSeenAt, &h.Text); err != nil {
			return nil, fmt.Errorf("matching: reverse knn scan: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
