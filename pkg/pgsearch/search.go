package pgsearch

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Hit is one row returned by KNNWithFilters / ListNewJobs. The match
// layer's SearchHit type is structurally identical and converts via a
// type assertion in the adapter at the matching layer.
type Hit struct {
	CanonicalID string
	Slug        string
	Title       string
	Company     string
	Country     string
	Kind        string
	PostedAt    time.Time
	Score       float64
}

// StatBucket is one row of the GlobalStats facet aggregation.
type StatBucket struct {
	Code  string
	Count int
}

// Stats is the headline aggregation returned by GlobalStats —
// total + top-N facet buckets.
type Stats struct {
	TotalNewThisWeek int
	TopCountries     []StatBucket
	TopKinds         []StatBucket
}

// DB is the read-side database accessor — same shape every other
// repository in the codebase consumes (frame.DatastoreManager().GetPool
// .DB).
type DB func(ctx context.Context, readOnly bool) *gorm.DB

// Search renders pgvector KNN + filter queries against the
// `opportunities` table.
//
// Construction is parameterless — there is no Manticore URL to dial.
// All queries go through the read-only Postgres pool the rest of the
// matching service already holds.
type Search struct {
	db DB
}

// New wires the Search adapter against a read-only DB factory.
func New(db DB) *Search { return &Search{db: db} }

// KNNRequest is the input to KNNWithFilters.
//
//   - Vector: candidate embedding (pgvector cosine distance).
//   - Limit:  rows to return (default 200).
//   - Filter: pgsearch.Filter built by per-kind matchers.
//
// The status=active + hidden=false + deadline-in-future predicates
// are always applied — they're the "what would a user see" baseline
// that callers should not have to repeat.
type KNNRequest struct {
	Vector []float32
	Limit  int
	Filter Filter
}

// KNNWithFilters runs the KNN-with-filters query: top-K opportunities
// by cosine similarity to Vector, restricted to rows matching Filter
// plus the "active + visible + not yet expired" baseline.
//
// The query shape is:
//
//	SELECT canonical_id, slug, title, COALESCE(issuing_entity,''), …,
//	       1 - (embedding <=> $1::vector) AS score
//	FROM opportunities
//	WHERE status = 'active' AND hidden = false
//	  AND (deadline IS NULL OR deadline > now())
//	  AND <filter SQL>
//	ORDER BY embedding <=> $1::vector
//	LIMIT $N
//
// The cosine-distance operator `<=>` is pgvector; smaller is more
// similar, so we ORDER BY distance and convert to a similarity score
// in [0, 1] via `1 - distance` for the response.
func (s *Search) KNNWithFilters(ctx context.Context, req KNNRequest) ([]Hit, error) {
	if len(req.Vector) == 0 {
		return nil, fmt.Errorf("pgsearch: empty vector")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}

	vec := vectorLiteral(req.Vector)
	args := []any{vec}
	whereSQL, filterArgs := req.Filter.Build(len(args) + 1)
	args = append(args, filterArgs...)
	limitParam := fmt.Sprintf("$%d", len(args)+1)
	args = append(args, limit)

	where := `status = 'active'
	          AND hidden = false
	          AND (deadline IS NULL OR deadline > now())`
	if whereSQL != "" {
		where += " AND " + whereSQL
	}

	q := `SELECT canonical_id,
	             slug,
	             title,
	             COALESCE(issuing_entity, '') AS company,
	             COALESCE(country, '')        AS country,
	             kind,
	             COALESCE(posted_at, first_seen_at) AS posted_at,
	             1 - (embedding <=> $1::vector)     AS score
	      FROM opportunities
	      WHERE ` + where + `
	      ORDER BY embedding <=> $1::vector
	      LIMIT ` + limitParam

	return s.scan(ctx, q, args)
}

// ListNewJobs returns up to `limit` opportunities posted on/after
// `since`, optionally filtered by country + kinds. Used by the weekly
// jobs digest.
func (s *Search) ListNewJobs(ctx context.Context, since time.Time, country string, kinds []string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	args := []any{since}
	wheres := []string{
		"status = 'active'",
		"hidden = false",
		"COALESCE(posted_at, first_seen_at) >= $1",
	}
	n := 2
	if country != "" {
		wheres = append(wheres, fmt.Sprintf("country = $%d", n))
		args = append(args, country)
		n++
	}
	if len(kinds) > 0 {
		wheres = append(wheres, fmt.Sprintf("kind = ANY($%d)", n))
		args = append(args, kinds)
		n++
	}
	args = append(args, limit)
	q := `SELECT canonical_id,
	             slug,
	             title,
	             COALESCE(issuing_entity, '') AS company,
	             COALESCE(country, '')        AS country,
	             kind,
	             COALESCE(posted_at, first_seen_at) AS posted_at,
	             0::float8                          AS score
	      FROM opportunities
	      WHERE ` + strings.Join(wheres, " AND ") + `
	      ORDER BY COALESCE(posted_at, first_seen_at) DESC
	      LIMIT $` + strconv.Itoa(n)
	return s.scan(ctx, q, args)
}

// GlobalStats returns the headline numbers for the past-since window:
// total active rows + top-3 country and kind buckets.
func (s *Search) GlobalStats(ctx context.Context, since time.Time) (Stats, error) {
	db := s.db(ctx, true)

	var total int64
	if err := db.Raw(`SELECT COUNT(*) FROM opportunities
	                  WHERE status = 'active' AND hidden = false
	                    AND COALESCE(posted_at, first_seen_at) >= ?`, since).
		Scan(&total).Error; err != nil {
		return Stats{}, fmt.Errorf("pgsearch: total: %w", err)
	}

	countries, err := s.bucket(ctx, "country", since, 3)
	if err != nil {
		return Stats{}, err
	}
	kinds, err := s.bucket(ctx, "kind", since, 3)
	if err != nil {
		return Stats{}, err
	}
	return Stats{TotalNewThisWeek: int(total), TopCountries: countries, TopKinds: kinds}, nil
}

func (s *Search) bucket(ctx context.Context, col string, since time.Time, top int) ([]StatBucket, error) {
	// `col` is a hard-coded ident from a fixed-set call site (country/kind);
	// it never sees user input, so interpolating it is safe.
	q := fmt.Sprintf(`SELECT %[1]s AS code, COUNT(*) AS cnt
	                  FROM opportunities
	                  WHERE status = 'active' AND hidden = false
	                    AND %[1]s IS NOT NULL AND %[1]s <> ''
	                    AND COALESCE(posted_at, first_seen_at) >= ?
	                  GROUP BY %[1]s
	                  ORDER BY cnt DESC
	                  LIMIT ?`, col)
	var rows []struct {
		Code string
		Cnt  int
	}
	if err := s.db(ctx, true).Raw(q, since, top).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("pgsearch: bucket %s: %w", col, err)
	}
	out := make([]StatBucket, 0, len(rows))
	for _, r := range rows {
		out = append(out, StatBucket{Code: r.Code, Count: r.Cnt})
	}
	return out, nil
}

func (s *Search) scan(ctx context.Context, q string, args []any) ([]Hit, error) {
	type row struct {
		CanonicalID string
		Slug        string
		Title       string
		Company     string
		Country     string
		Kind        string
		PostedAt    time.Time
		Score       float64
	}
	var rows []row
	if err := s.db(ctx, true).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("pgsearch: scan: %w", err)
	}
	out := make([]Hit, 0, len(rows))
	for _, r := range rows {
		out = append(out, Hit(r))
	}
	return out, nil
}

// vectorLiteral renders []float32 as pgvector's textual form
// `[1.1,2.2,...]`. Mirrors variantstate's helper of the same shape;
// duplicated here so pgsearch has no dependency cycle on variantstate.
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
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
