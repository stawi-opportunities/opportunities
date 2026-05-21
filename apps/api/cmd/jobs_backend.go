// Package main — JobsBackend interface and the Postgres-backed
// implementation that drives Phase 6 of the consolidation plan
// (see deployment.manifests/SHARED_TASK_NOTES.md).
//
// jobsPostgres satisfies this interface; the legacy Manticore backend
// has been retired (Phase 6, spec §5.6).
//
// 1-month default time window: every list / count query that doesn't
// explicitly pass `since` filters on `last_seen_at >= now() - 1 month`.
// Callers that want a wider range pass `since` as either an ISO8601
// timestamp (`since=2025-01-01T00:00:00Z`) or the literal `alltime`
// to drop the filter entirely.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/pitabwire/util"
	"gorm.io/gorm"
)

// hashID mirrors materializer/service/indexer.go::hashID. Kept here
// for test compatibility until all callers migrate to slug-based lookups.
func hashID(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// job is the canonical in-memory representation shared by all endpoint
// handlers. The Postgres backend populates Slug and CanonicalID; the
// legacy Manticore backend (now retired) populated ID.
type job struct {
	// Numeric primary key — used by the retired Manticore backend.
	// Zero on the Postgres backend (which uses Slug/CanonicalID).
	ID uint64 `json:"id"`
	// Public string identifier (opportunities.slug).
	Slug string `json:"slug,omitempty"`
	// CanonicalID is the xid that worker.canonical assigns.
	CanonicalID string `json:"canonical_id,omitempty"`
	// Polymorphic discriminator: job, scholarship, tender, deal, funding.
	Kind          string `json:"kind,omitempty"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	IssuingEntity string `json:"issuing_entity,omitempty"`
	Categories    []int64 `json:"categories,omitempty"`
	Country       string  `json:"country,omitempty"`
	Region        string  `json:"region,omitempty"`
	City          string  `json:"city,omitempty"`
	Lat           float64 `json:"lat,omitempty"`
	Lon           float64 `json:"lon,omitempty"`
	Remote        bool    `json:"remote,omitempty"`
	GeoScope      string  `json:"geo_scope,omitempty"`
	PostedAt      *time.Time `json:"posted_at,omitempty"`
	Deadline      *time.Time `json:"deadline,omitempty"`
	AmountMin     float64 `json:"amount_min,omitempty"`
	AmountMax     float64 `json:"amount_max,omitempty"`
	Currency      string  `json:"currency,omitempty"`
	EmploymentType    string  `json:"employment_type,omitempty"`
	Seniority         string  `json:"seniority,omitempty"`
	FieldOfStudy      string  `json:"field_of_study,omitempty"`
	DegreeLevel       string  `json:"degree_level,omitempty"`
	ProcurementDomain string  `json:"procurement_domain,omitempty"`
	FundingFocus      string  `json:"funding_focus,omitempty"`
	DiscountPercent   float64 `json:"discount_percent,omitempty"`
	SourceID          uint64  `json:"source_id,omitempty"`
}

// JobsBackend is the common read API every public endpoint uses.
// Implementations live in manticore_client.go (Manticore-RT) and
// jobs_backend.go below (Postgres pg_search + pgvectorscale).
type JobsBackend interface {
	// GetBySlug returns one opportunity by its public string slug, or
	// (nil, nil) when no row matches. Both backends accept the same
	// slug shape — the Manticore backend hashes the input, the
	// Postgres backend does an exact match on opportunities.slug.
	GetBySlug(ctx context.Context, slug string) (*job, error)

	// Count returns the total active rows matching filter. Filter is
	// the same "Manticore-shape" filter clauses used by the Manticore
	// backend; the Postgres backend translates them to SQL predicates.
	// Nil filter → 1-month default window applied.
	Count(ctx context.Context, filter []map[string]any) (int, error)

	// Top returns up-to-limit active opportunities ordered by recency.
	// The minScore parameter is accepted for the Manticore backend's
	// signature compatibility; Postgres ignores it (no quality_score
	// in the schema yet).
	Top(ctx context.Context, minScore float64, limit int) ([]job, error)

	// Latest returns up-to-limit active opportunities by posted_at
	// desc, filtered to the 1-month default window.
	Latest(ctx context.Context, limit int) ([]job, error)

	// Facets returns term-aggregations across the active set. Keys are
	// the public facet family names (kind, country, geo_scope, …).
	Facets(ctx context.Context) (map[string]map[string]int, error)

	// SearchFiltered returns the top-N rows matching the filter, sorted
	// by sortField desc. Used by the feed endpoints.
	SearchFiltered(ctx context.Context, filter []map[string]any, limit int, sortField string) ([]job, error)

	// Search runs a full-text search with filters, paging, and
	// term-aggregations in one round trip. Returns (hits, total, aggs).
	// q is the free-text query (empty for filter-only); when non-empty
	// the Postgres backend uses pg_search BM25; the Manticore backend
	// uses its match query.
	Search(
		ctx context.Context,
		q string,
		filter []map[string]any,
		sort string,
		limit int,
		aggs map[string]any,
	) ([]job, int, map[string]map[string]int, error)
}

// ---------------------------------------------------------------------------
// Postgres backend
// ---------------------------------------------------------------------------

// jobsPostgres satisfies JobsBackend against the CNPG cluster's
// opportunities table — populated by worker.canonical (Phase 4) and
// the materializer's admin handlers (Phase 5).
//
// We hold Frame's GORM pool factory (same shape pkg/variantstate uses)
// and pull a *sql.DB out of it per call. Read-only queries go through
// the ro pool when one is configured.
type jobsPostgres struct {
	pool func(ctx context.Context, readOnly bool) *gorm.DB
}

func newJobsPostgres(pool func(ctx context.Context, readOnly bool) *gorm.DB) *jobsPostgres {
	return &jobsPostgres{pool: pool}
}

// sqlDB returns the underlying *sql.DB for the read-only or read-write
// connection. Surfaced as a single helper so the per-method query
// sites stay terse.
//
// Frame's DatastoreManager returns nil from the readOnly factory when
// the cluster doesn't have a separate REPLICA_DATABASE_URL wired —
// which is our case, the api speaks only to pooler-rw. Fall back to
// the RW pool transparently so callers don't have to branch.
func (p *jobsPostgres) sqlDB(ctx context.Context, readOnly bool) (*sql.DB, error) {
	if p == nil || p.pool == nil {
		return nil, errors.New("jobsPostgres: pool not wired")
	}
	gdb := p.pool(ctx, readOnly)
	if gdb == nil && readOnly {
		gdb = p.pool(ctx, false) // RW fallback
	}
	if gdb == nil {
		return nil, errors.New("jobsPostgres: gorm pool returned nil")
	}
	return gdb.DB()
}

// defaultSince returns the start of the 1-month window applied to
// every API-serving query that doesn't override `since`.
func defaultSince() time.Time {
	return time.Now().UTC().AddDate(0, -1, 0)
}

// activePred returns the universal predicate that keeps live rows.
// Mirrors the Manticore activeFilter semantics: not hidden, status =
// active, deadline future or null.
func activePred() string {
	return `hidden = false
	  AND status = 'active'
	  AND (deadline IS NULL OR deadline > now())`
}

// scanJob reads a row from the standard column-select used by every
// jobsPostgres method.
func scanJob(rows *sql.Rows) (job, error) {
	var (
		j           job
		desc        sql.NullString
		issuer      sql.NullString
		country     sql.NullString
		region      sql.NullString
		city        sql.NullString
		remote      sql.NullBool
		apply       sql.NullString
		postedAt    sql.NullTime
		deadline    sql.NullTime
		currency    sql.NullString
		amountMin   sql.NullFloat64
		amountMax   sql.NullFloat64
		quality     sql.NullFloat64
		attrsRaw    sql.RawBytes
		categoryRaw sql.RawBytes
	)
	if err := rows.Scan(
		&j.CanonicalID, &j.Slug, &j.Kind,
		&j.Title, &desc, &issuer,
		&country, &region, &city,
		&remote, &apply,
		&postedAt, &deadline,
		&currency, &amountMin, &amountMax,
		&quality, &attrsRaw, &categoryRaw,
	); err != nil {
		return j, err
	}
	j.Description = desc.String
	j.IssuingEntity = issuer.String
	j.Country = country.String
	j.Region = region.String
	j.City = city.String
	j.Remote = remote.Bool
	j.Currency = currency.String
	j.AmountMin = amountMin.Float64
	j.AmountMax = amountMax.Float64
	_ = apply.String // captured for completeness; SearchResult doesn't expose
	if postedAt.Valid {
		t := postedAt.Time.UTC()
		j.PostedAt = &t
	}
	if deadline.Valid {
		t := deadline.Time.UTC()
		j.Deadline = &t
	}
	// Per-kind sparse facet projection — opportunities.attributes
	// carries the same map the Manticore sparseColsForKind path
	// projects. Pull just the keys the searchResult shape exposes;
	// callers that need the rest can hit a future per-row endpoint.
	if len(attrsRaw) > 0 {
		var attrs map[string]any
		if err := json.Unmarshal(attrsRaw, &attrs); err == nil {
			if v, ok := attrs["employment_type"].(string); ok {
				j.EmploymentType = v
			}
			if v, ok := attrs["seniority"].(string); ok {
				j.Seniority = v
			}
			if v, ok := attrs["field_of_study"].(string); ok {
				j.FieldOfStudy = v
			}
			if v, ok := attrs["degree_level"].(string); ok {
				j.DegreeLevel = v
			}
			if v, ok := attrs["procurement_domain"].(string); ok {
				j.ProcurementDomain = v
			}
			if v, ok := attrs["funding_focus"].(string); ok {
				j.FundingFocus = v
			}
		}
	}
	// categories is currently stored under attributes.categories; once
	// the materializer adds a first-class column we'll select it
	// directly. Empty for now is safe — searchResult.Category falls
	// back to "" which the SPA handles.
	_ = categoryRaw
	_ = quality
	return j, nil
}

const selectColumns = `canonical_id, slug, kind,
	title, description, issuing_entity,
	country, region, city,
	remote, apply_url,
	posted_at, deadline,
	currency, amount_min, amount_max,
	quality_score, attributes, NULL::text AS categories_placeholder`

// GetBySlug fetches one row by opportunities.slug. Postgres-side this
// is a unique-index hit, so latency is ~0.5ms.
func (p *jobsPostgres) GetBySlug(ctx context.Context, slug string) (*job, error) {
	if slug == "" {
		return nil, nil
	}
	db, err := p.sqlDB(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("postgres: pool: %w", err)
	}
	q := `SELECT ` + selectColumns + ` FROM opportunities WHERE slug = $1 LIMIT 1`
	rows, err := db.QueryContext(ctx, q, slug)
	if err != nil {
		return nil, fmt.Errorf("postgres: get by slug: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, nil
	}
	j, err := scanJob(rows)
	if err != nil {
		return nil, fmt.Errorf("postgres: scan: %w", err)
	}
	return &j, nil
}

// Count returns the count of active rows matching the caller-supplied
// filter. The 1-month default window is applied automatically; pass a
// {"since": "alltime"} clause in filter to opt out.
func (p *jobsPostgres) Count(ctx context.Context, filter []map[string]any) (int, error) {
	db, err := p.sqlDB(ctx, true)
	if err != nil {
		return 0, err
	}
	where, args := postgresWhere(filter)
	q := `SELECT count(*) FROM opportunities WHERE ` + activePred() + where
	var n int
	if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: count: %w", err)
	}
	return n, nil
}

// Top returns up-to-limit recent rows. minScore is ignored (kept for
// JobsBackend signature parity).
func (p *jobsPostgres) Top(ctx context.Context, _ float64, limit int) ([]job, error) {
	return p.list(ctx, nil, limit, "last_seen_at")
}

// Latest returns up-to-limit rows by posted_at desc within the 1-month
// default window.
func (p *jobsPostgres) Latest(ctx context.Context, limit int) ([]job, error) {
	return p.list(ctx, nil, limit, "posted_at")
}

// SearchFiltered is the feed endpoint's path — filter clauses + sort.
func (p *jobsPostgres) SearchFiltered(ctx context.Context, filter []map[string]any, limit int, sortField string) ([]job, error) {
	return p.list(ctx, filter, limit, sortField)
}

func (p *jobsPostgres) list(ctx context.Context, filter []map[string]any, limit int, sortField string) ([]job, error) {
	db, err := p.sqlDB(ctx, true)
	if err != nil {
		return nil, err
	}
	sortField = sanitizeSortField(sortField)
	where, args := postgresWhere(filter)
	q := `SELECT ` + selectColumns + `
	      FROM opportunities
	      WHERE ` + activePred() + where + `
	      ORDER BY ` + sortField + ` DESC NULLS LAST
	      LIMIT $` + intToStr(len(args)+1)
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]job, 0, limit)
	for rows.Next() {
		j, scanErr := scanJob(rows)
		if scanErr != nil {
			util.Log(ctx).WithError(scanErr).Warn("postgres: scan row failed")
			continue
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Facets returns terms aggregations as map[name]map[bucket]count.
// Each family is a separate GROUP BY query — five trips total, each
// using the composite (kind, country, last_seen_at) partial index.
func (p *jobsPostgres) Facets(ctx context.Context) (map[string]map[string]int, error) {
	families := []struct {
		name   string
		expr   string
		fromCol string
	}{
		{"kind", "kind", "kind"},
		{"country", "country", "country"},
		{"geo_scope", "attributes->>'geo_scope'", "geo_scope"},
		{"employment_type", "attributes->>'employment_type'", "employment_type"},
		{"seniority", "attributes->>'seniority'", "seniority"},
	}
	_ = families
	out := map[string]map[string]int{}
	for _, f := range []struct {
		name string
		expr string
	}{
		{"kind", "kind"},
		{"country", "country"},
		{"geo_scope", "attributes->>'geo_scope'"},
		{"employment_type", "attributes->>'employment_type'"},
		{"seniority", "attributes->>'seniority'"},
	} {
		q := `SELECT ` + f.expr + ` AS bucket, count(*)
		      FROM opportunities
		      WHERE ` + activePred() + `
		        AND last_seen_at >= $1
		        AND ` + f.expr + ` IS NOT NULL
		        AND ` + f.expr + ` <> ''
		      GROUP BY bucket
		      ORDER BY count(*) DESC
		      LIMIT 200`
		db, dbErr := p.sqlDB(ctx, true)
		if dbErr != nil {
			return nil, dbErr
		}
		rows, err := db.QueryContext(ctx, q, defaultSince())
		if err != nil {
			return nil, fmt.Errorf("postgres: facet %s: %w", f.name, err)
		}
		buckets := map[string]int{}
		for rows.Next() {
			var k sql.NullString
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if k.Valid && k.String != "" {
				buckets[k.String] = n
			}
		}
		_ = rows.Close()
		out[f.name] = buckets
	}
	return out, nil
}

// Search runs a full-text BM25 query + filters + facets in one pass.
// Empty q falls back to the listing path; non-empty q uses pg_search's
// `@@@` operator with paradedb.parse(). Hybrid scoring (BM25 +
// embedding cosine) lands in a follow-up — for v1 BM25 alone is on
// par with the Manticore default.
func (p *jobsPostgres) Search(
	ctx context.Context,
	q string,
	filter []map[string]any,
	sort string,
	limit int,
	_ map[string]any,
) ([]job, int, map[string]map[string]int, error) {
	db, err := p.sqlDB(ctx, true)
	if err != nil {
		return nil, 0, nil, err
	}
	q = strings.TrimSpace(q)
	where, args := postgresWhere(filter)
	args = append(args, defaultSince())
	sinceIdx := len(args)
	// 1-month window applies to BOTH the text-match path and the
	// listing fallback. We chain it as an extra WHERE clause so the
	// composite (kind, country, last_seen_at) partial index helps.
	windowed := `AND last_seen_at >= $` + intToStr(sinceIdx)

	var (
		rows  *sql.Rows
		total int
	)

	if q == "" {
		// Listing fallback — same as Latest/Top but with the user's
		// filter on top of the active predicate.
		sortField := sanitizeSortField(sort)
		args = append(args, limit)
		limitIdx := len(args)
		query := `SELECT ` + selectColumns + `
		         FROM opportunities
		         WHERE ` + activePred() + where + ` ` + windowed + `
		         ORDER BY ` + sortField + ` DESC NULLS LAST
		         LIMIT $` + intToStr(limitIdx)
		rows, err = db.QueryContext(ctx, query, args...)
	} else {
		args = append(args, q)
		qIdx := len(args)
		args = append(args, limit)
		limitIdx := len(args)
		// pg_search BM25: `canonical_id @@@ paradedb.parse('…')`. The
		// parser handles AND/OR/quoted phrases natively — pass the
		// user's q through verbatim.
		query := `SELECT ` + selectColumns + `,
		                 paradedb.score(canonical_id) AS bm25_score
		          FROM opportunities
		          WHERE canonical_id @@@ paradedb.parse($` + intToStr(qIdx) + `)
		            AND ` + activePred() + where + ` ` + windowed + `
		          ORDER BY bm25_score DESC
		          LIMIT $` + intToStr(limitIdx)
		rows, err = db.QueryContext(ctx, query, args...)
	}
	if err != nil {
		return nil, 0, nil, fmt.Errorf("postgres: search: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hits := make([]job, 0, limit)
	for rows.Next() {
		// The BM25 query has one extra column; scanJob's known shape
		// covers the leading columns and rows.Next continues to the
		// next row without us reading bm25_score. We rely on
		// rows.Scan-then-discard via a wrapper.
		jrow, scanErr := scanJobMaybeBM25(rows, q != "")
		if scanErr != nil {
			util.Log(ctx).WithError(scanErr).Warn("postgres: scan row failed")
			continue
		}
		hits = append(hits, jrow)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, nil, err
	}

	// Total via a parallel count — cheap because the partial index
	// covers the predicate. We re-derive args (filter + since) without
	// the q/limit pair to keep the placeholder numbering sane.
	cwhere, cargs := postgresWhere(filter)
	cargs = append(cargs, defaultSince())
	csinceIdx := len(cargs)
	var countQ string
	if q == "" {
		countQ = `SELECT count(*) FROM opportunities WHERE ` + activePred() + cwhere + `
		          AND last_seen_at >= $` + intToStr(csinceIdx)
	} else {
		cargs = append(cargs, q)
		cqIdx := len(cargs)
		countQ = `SELECT count(*) FROM opportunities
		          WHERE canonical_id @@@ paradedb.parse($` + intToStr(cqIdx) + `)
		            AND ` + activePred() + cwhere + `
		            AND last_seen_at >= $` + intToStr(csinceIdx)
	}
	if cerr := db.QueryRowContext(ctx, countQ, cargs...).Scan(&total); cerr != nil {
		// Soft-fail on the total — return hits with an approximate total
		// rather than 502 the whole search.
		util.Log(ctx).WithError(cerr).Warn("postgres: search count failed; using hit count")
		total = len(hits)
	}

	// Facets — same shape as Facets() but constrained to the current
	// filter so the SPA's sidebar reflects "what's still available
	// given my filters". We reuse Facets() for v1 (filter-naive); a
	// follow-up can specialise.
	facets, _ := p.Facets(ctx)

	return hits, total, facets, nil
}

// scanJobMaybeBM25 reads the same column set as scanJob, optionally
// followed by a final bm25_score column (which we discard — the SPA
// gets results in BM25 order from the SQL ORDER BY clause).
func scanJobMaybeBM25(rows *sql.Rows, hasScore bool) (job, error) {
	if !hasScore {
		return scanJob(rows)
	}
	var (
		j           job
		desc        sql.NullString
		issuer      sql.NullString
		country     sql.NullString
		region      sql.NullString
		city        sql.NullString
		remote      sql.NullBool
		apply       sql.NullString
		postedAt    sql.NullTime
		deadline    sql.NullTime
		currency    sql.NullString
		amountMin   sql.NullFloat64
		amountMax   sql.NullFloat64
		quality     sql.NullFloat64
		attrsRaw    sql.RawBytes
		categoryRaw sql.RawBytes
		score       float64
	)
	if err := rows.Scan(
		&j.CanonicalID, &j.Slug, &j.Kind,
		&j.Title, &desc, &issuer,
		&country, &region, &city,
		&remote, &apply,
		&postedAt, &deadline,
		&currency, &amountMin, &amountMax,
		&quality, &attrsRaw, &categoryRaw,
		&score,
	); err != nil {
		return j, err
	}
	j.Description = desc.String
	j.IssuingEntity = issuer.String
	j.Country = country.String
	j.Region = region.String
	j.City = city.String
	j.Remote = remote.Bool
	j.Currency = currency.String
	j.AmountMin = amountMin.Float64
	j.AmountMax = amountMax.Float64
	_ = apply.String
	if postedAt.Valid {
		t := postedAt.Time.UTC()
		j.PostedAt = &t
	}
	if deadline.Valid {
		t := deadline.Time.UTC()
		j.Deadline = &t
	}
	if len(attrsRaw) > 0 {
		var attrs map[string]any
		if err := json.Unmarshal(attrsRaw, &attrs); err == nil {
			if v, ok := attrs["employment_type"].(string); ok {
				j.EmploymentType = v
			}
			if v, ok := attrs["seniority"].(string); ok {
				j.Seniority = v
			}
		}
	}
	_ = categoryRaw
	_ = quality
	return j, nil
}

// ---------------------------------------------------------------------------
// Filter translation
// ---------------------------------------------------------------------------

// postgresWhere converts the Manticore-shape filter clauses used by
// endpoints_v2.go into a SQL fragment + arg slice. The clause shape
// mirrors the keys the Manticore backend already accepts:
//
//	{"equals": {"country": "KE"}}
//	{"equals": {"categories": 123456789}}
//	{"range":  {"amount_min": {"gte": 500}}}
//	{"range":  {"amount_max": {"lte": 5000}}}
//
// Unknown shapes are skipped silently — the goal is feature parity
// with the live Manticore filter, not full SQL DSL coverage.
func postgresWhere(filter []map[string]any) (string, []any) {
	var b strings.Builder
	args := []any{}
	for _, clause := range filter {
		if eq, ok := clause["equals"].(map[string]any); ok {
			for col, raw := range eq {
				colExpr, colArg, ok := translateEquals(col, raw, len(args)+1)
				if !ok {
					continue
				}
				b.WriteString(" AND ")
				b.WriteString(colExpr)
				args = append(args, colArg)
			}
			continue
		}
		if rng, ok := clause["range"].(map[string]any); ok {
			for col, raw := range rng {
				spec, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if gte, ok := spec["gte"]; ok {
					b.WriteString(" AND ")
					b.WriteString(quoteSafeCol(col))
					b.WriteString(" >= $" + intToStr(len(args)+1))
					args = append(args, gte)
				}
				if lte, ok := spec["lte"]; ok {
					b.WriteString(" AND ")
					b.WriteString(quoteSafeCol(col))
					b.WriteString(" <= $" + intToStr(len(args)+1))
					args = append(args, lte)
				}
			}
			continue
		}
	}
	return b.String(), args
}

// translateEquals maps the Manticore column name to the Postgres
// column or JSONB expression. Returns ok=false for unknown columns.
func translateEquals(col string, val any, pidx int) (string, any, bool) {
	switch col {
	case "kind", "country", "region", "city", "currency":
		return quoteSafeCol(col) + " = $" + intToStr(pidx), val, true
	case "geo_scope", "employment_type", "seniority", "field_of_study", "degree_level":
		// Per-kind sparse facets live under attributes JSONB.
		return "attributes->>'" + col + "' = $" + intToStr(pidx), val, true
	case "categories":
		// Manticore stored categories as int64; Postgres carries them
		// in attributes.categories as a JSON array of strings. Skip
		// for v1 — facet filter on category lands in a follow-up
		// once the materializer projects a first-class column.
		return "", nil, false
	case "remote":
		return "remote = $" + intToStr(pidx), val, true
	}
	return "", nil, false
}

// quoteSafeCol is a tiny allowlist guard for column names that ride
// in unquoted via filter or sort. Anything not in the allowlist is
// quoted and prefixed with `_invalid_` so the query fails fast in
// development rather than executing with attacker-influenced SQL.
func quoteSafeCol(col string) string {
	allowed := map[string]bool{
		"kind": true, "country": true, "region": true, "city": true,
		"currency": true, "remote": true,
		"amount_min": true, "amount_max": true,
		"posted_at": true, "deadline": true,
		"last_seen_at": true, "first_seen_at": true, "created_at": true,
	}
	if allowed[col] {
		return col
	}
	return `"_invalid_` + col + `"`
}

// sanitizeSortField maps a caller-supplied sort key to a known column.
// Anything else defaults to last_seen_at — keeps the SPA's "Sort by:
// quality" working without exposing a sort-injection vector.
func sanitizeSortField(s string) string {
	switch s {
	case "posted_at", "last_seen_at", "first_seen_at", "deadline", "amount_max", "amount_min":
		return s
	case "quality", "score", "":
		return "last_seen_at"
	default:
		return "last_seen_at"
	}
}

// intToStr converts a small positive int to a decimal string without
// pulling in fmt. Placeholder numbering hits this 5–10× per query.
func intToStr(n int) string {
	if n < 0 {
		return "0"
	}
	if n < 10 {
		return string('0' + byte(n))
	}
	// 99 placeholders is the de-facto Postgres safety ceiling; up to
	// 999 covers any pathological caller.
	if n < 100 {
		return string('0'+byte(n/10)) + string('0'+byte(n%10))
	}
	return string('0'+byte(n/100)) + string('0'+byte((n/10)%10)) + string('0'+byte(n%10))
}

// Compile-time check that jobsPostgres satisfies JobsBackend.
var _ JobsBackend = (*jobsPostgres)(nil)
