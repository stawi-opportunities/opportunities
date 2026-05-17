package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

// hashID mirrors materializer/service/indexer.go::hashID. Both packages
// must produce identical uint64 keys for the same canonical_id string
// because writes go through the materializer's helper and reads happen
// here — drift would mean lookups by slug return (nil, nil) even when
// the row exists.
func hashID(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// job mirrors the polymorphic idx_opportunities_rt schema in
// pkg/searchindex/schema.go. The JSON tags match the Manticore column
// names exactly so /search responses unmarshal into this struct via
// `_source` without translation. Optional pointer fields keep zero-
// valued columns out of the JSON the API hands back to consumers.
//
// Legacy callers (Hugo snapshot publisher, dashboard v1) referenced
// `status`/`quality_score`/`slug`/`canonical_id` which never landed in
// the polymorphic schema; those callers are migrated to use `id`
// (numeric primary key) and a deadline-driven "active" predicate.
type job struct {
	// Numeric primary key — hashID(opportunity_id) on write. Stable
	// across replays; the only field clients can use to dereference
	// a single opportunity.
	ID uint64 `json:"id"`
	// Polymorphic discriminator: job, scholarship, tender, deal, funding.
	Kind string `json:"kind,omitempty"`
	// Universal text — written by buildDocFromCanonical.
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	IssuingEntity string `json:"issuing_entity,omitempty"`
	// Categories is a Manticore multi-value attribute (mva64); decoded
	// here as an array of int64 IDs that the API can resolve to display
	// names against the kind registry.
	Categories []int64 `json:"categories,omitempty"`
	// Universal location.
	Country  string  `json:"country,omitempty"`
	Region   string  `json:"region,omitempty"`
	City     string  `json:"city,omitempty"`
	Lat      float64 `json:"lat,omitempty"`
	Lon      float64 `json:"lon,omitempty"`
	Remote   bool    `json:"remote,omitempty"`
	GeoScope string  `json:"geo_scope,omitempty"`
	// Universal time. Manticore stores as unix seconds; we expose
	// pointer-to-time so zero (no value) round-trips as null JSON.
	PostedAt *time.Time `json:"posted_at,omitempty"`
	Deadline *time.Time `json:"deadline,omitempty"`
	// Universal monetary.
	AmountMin float64 `json:"amount_min,omitempty"`
	AmountMax float64 `json:"amount_max,omitempty"`
	Currency  string  `json:"currency,omitempty"`
	// Per-kind sparse facet columns. Populated only when the source kind
	// declares them; absent columns stay zero-valued.
	EmploymentType    string  `json:"employment_type,omitempty"`
	Seniority         string  `json:"seniority,omitempty"`
	FieldOfStudy      string  `json:"field_of_study,omitempty"`
	DegreeLevel       string  `json:"degree_level,omitempty"`
	ProcurementDomain string  `json:"procurement_domain,omitempty"`
	FundingFocus      string  `json:"funding_focus,omitempty"`
	DiscountPercent   float64 `json:"discount_percent,omitempty"`
	// SourceID is the hashID(source_id) provenance pointer.
	SourceID uint64 `json:"source_id,omitempty"`
}

type jobsManticore struct {
	c *searchindex.Client
}

func newJobsManticore(c *searchindex.Client) *jobsManticore {
	return &jobsManticore{c: c}
}

// activeFilter returns the predicate that keeps "live" opportunities in
// every query. The polymorphic schema has no `status` column — the
// design is that expired rows get DELETEd (CanonicalExpiredHandler
// patches `deadline` to the expiry instant and rows fall out of this
// range). A row whose deadline is in the future OR exactly 0 (i.e. no
// declared deadline, treated as evergreen) is considered active.
func activeFilter() []map[string]any {
	now := time.Now().Unix()
	// deadline > now OR deadline == 0 → bool/should expression. The
	// "should" clause acts as OR within a bool query; we wrap the
	// entire active-predicate in another bool/filter to compose with
	// caller-supplied filters at the top level without ambiguity.
	return []map[string]any{
		{"bool": map[string]any{
			"should": []map[string]any{
				{"range": map[string]any{"deadline": map[string]any{"gt": now}}},
				{"equals": map[string]any{"deadline": 0}},
			},
		}},
	}
}

// GetByID fetches a single opportunity by its public string identifier
// (slug or canonical_id — the polymorphic schema collapses both into
// the same numeric primary key). The lookup hashes the supplied string
// the same way materializer.hashID hashes the canonical_id on write,
// so passing whichever identifier the URL routing surfaces yields the
// right row. The legacy "match status=active AND (canonical_id=X OR
// slug=X)" path is replaced because neither column exists in the
// polymorphic idx_opportunities_rt; rows are kept active by deletion
// of expired ones (see activeFilter).
//
// Returns (nil, nil) for not-found.
func (j *jobsManticore) GetByID(ctx context.Context, id string) (*job, error) {
	return j.GetByNumericID(ctx, hashID(id))
}

// GetByNumericID looks up by the raw uint64 Manticore primary key.
// Callers that already hold the hashed id (e.g. internal pipeline
// services) use this to skip a hash round-trip.
func (j *jobsManticore) GetByNumericID(ctx context.Context, id uint64) (*job, error) {
	f := append(activeFilter(),
		map[string]any{"equals": map[string]any{"id": id}})
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": f}},
		"limit": 1,
	}
	hits, _, err := j.search(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}
	return &hits[0], nil
}

// Count returns the number of active opportunities matching the
// caller-supplied filter. Filter clauses must reference schema columns
// (see pkg/searchindex/schema.go); use of `status` or `quality_score`
// will return Manticore parse_exception.
func (j *jobsManticore) Count(ctx context.Context, filter []map[string]any) (int, error) {
	f := append(activeFilter(), filter...)
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": f}},
		"limit": 0,
	}
	_, total, err := j.search(ctx, q)
	return total, err
}

// Top returns up-to-limit active opportunities ordered by recency. The
// polymorphic schema has no `quality_score` proxy; recency is the
// closest stand-in. minScore is accepted but ignored to preserve the
// existing handler signatures — TODO is to surface a real ranking
// signal when one exists in the schema.
func (j *jobsManticore) Top(ctx context.Context, _ float64, limit int) ([]job, error) {
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": activeFilter()}},
		"sort":  []any{map[string]any{"posted_at": "desc"}},
		"limit": limit,
	}
	hits, _, err := j.search(ctx, q)
	return hits, err
}

// Latest returns up-to-limit active opportunities by posted_at desc.
func (j *jobsManticore) Latest(ctx context.Context, limit int) ([]job, error) {
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": activeFilter()}},
		"sort":  []any{map[string]any{"posted_at": "desc"}},
		"limit": limit,
	}
	hits, _, err := j.search(ctx, q)
	return hits, err
}

// Facets returns terms aggregations across the active set. Field names
// must match schema columns; `category` (singular) is rewritten to
// `categories` (multi) and the legacy `remote_type` is replaced by
// `geo_scope` since the schema only carries those two.
func (j *jobsManticore) Facets(ctx context.Context) (map[string]map[string]int, error) {
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": activeFilter()}},
		"limit": 0,
		"aggs": map[string]any{
			"kind":            map[string]any{"terms": map[string]any{"field": "kind", "size": 16}},
			"categories":      map[string]any{"terms": map[string]any{"field": "categories", "size": 64}},
			"country":         map[string]any{"terms": map[string]any{"field": "country", "size": 200}},
			"geo_scope":       map[string]any{"terms": map[string]any{"field": "geo_scope", "size": 8}},
			"employment_type": map[string]any{"terms": map[string]any{"field": "employment_type", "size": 16}},
			"seniority":       map[string]any{"terms": map[string]any{"field": "seniority", "size": 16}},
		},
	}
	raw, err := j.c.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Aggregations map[string]struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int    `json:"doc_count"`
			} `json:"buckets"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}
	out := map[string]map[string]int{}
	for name, agg := range parsed.Aggregations {
		out[name] = map[string]int{}
		for _, b := range agg.Buckets {
			if b.Key == "" {
				continue
			}
			out[name][b.Key] = b.DocCount
		}
	}
	return out, nil
}

func (j *jobsManticore) searchFiltered(ctx context.Context, filter []map[string]any, limit int, sortField string) ([]job, error) {
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": filter}},
		"sort":  []any{map[string]any{sortField: "desc"}},
		"limit": limit,
	}
	hits, _, err := j.search(ctx, q)
	return hits, err
}

func (j *jobsManticore) search(ctx context.Context, q map[string]any) ([]job, int, error) {
	raw, err := j.c.Search(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	var parsed struct {
		Hits struct {
			Total int `json:"total"`
			Hits  []struct {
				ID     uint64         `json:"_id"`
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, 0, fmt.Errorf("manticore decode: %w", err)
	}
	out := make([]job, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		j, err := decodeHit(h.ID, h.Source)
		if err != nil {
			// One bad row should not break the page; log via the error
			// path and continue. Callers can detect partial success via
			// the returned slice length vs Total.
			continue
		}
		out = append(out, j)
	}
	return out, parsed.Hits.Total, nil
}

// decodeHit converts a /search hit (numeric _id + _source map) into a
// typed job. Timestamps come back as unix seconds (int64 or float64);
// we normalise both to *time.Time so JSON encoders emit ISO strings.
func decodeHit(id uint64, src map[string]any) (job, error) {
	j := job{ID: id}
	if v, ok := src["kind"].(string); ok {
		j.Kind = v
	}
	if v, ok := src["title"].(string); ok {
		j.Title = v
	}
	if v, ok := src["description"].(string); ok {
		j.Description = v
	}
	if v, ok := src["issuing_entity"].(string); ok {
		j.IssuingEntity = v
	}
	if v, ok := src["country"].(string); ok {
		j.Country = v
	}
	if v, ok := src["region"].(string); ok {
		j.Region = v
	}
	if v, ok := src["city"].(string); ok {
		j.City = v
	}
	if v, ok := src["geo_scope"].(string); ok {
		j.GeoScope = v
	}
	if v, ok := src["currency"].(string); ok {
		j.Currency = v
	}
	if v, ok := src["employment_type"].(string); ok {
		j.EmploymentType = v
	}
	if v, ok := src["seniority"].(string); ok {
		j.Seniority = v
	}
	if v, ok := src["field_of_study"].(string); ok {
		j.FieldOfStudy = v
	}
	if v, ok := src["degree_level"].(string); ok {
		j.DegreeLevel = v
	}
	if v, ok := src["procurement_domain"].(string); ok {
		j.ProcurementDomain = v
	}
	if v, ok := src["funding_focus"].(string); ok {
		j.FundingFocus = v
	}
	j.Remote, _ = src["remote"].(bool)
	j.Lat = asFloat(src["lat"])
	j.Lon = asFloat(src["lon"])
	j.AmountMin = asFloat(src["amount_min"])
	j.AmountMax = asFloat(src["amount_max"])
	j.DiscountPercent = asFloat(src["discount_percent"])
	j.SourceID = asUint64(src["source_id"])
	j.PostedAt = unixSecondsToTime(src["posted_at"])
	j.Deadline = unixSecondsToTime(src["deadline"])
	if arr, ok := src["categories"].([]any); ok {
		for _, v := range arr {
			if n := asInt64(v); n != 0 {
				j.Categories = append(j.Categories, n)
			}
		}
	}
	return j, nil
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	}
	return 0
}

func asInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	}
	return 0
}

func asUint64(v any) uint64 {
	switch t := v.(type) {
	case uint64:
		return t
	case int64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case float64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case json.Number:
		n, _ := t.Int64()
		if n < 0 {
			return 0
		}
		return uint64(n)
	}
	return 0
}

// unixSecondsToTime turns a numeric unix-seconds value (Manticore
// timestamp column) into *time.Time. Zero/missing → nil so JSON
// callers see null rather than "1970-01-01".
func unixSecondsToTime(v any) *time.Time {
	s := asInt64(v)
	if s <= 0 {
		return nil
	}
	t := time.Unix(s, 0).UTC()
	return &t
}

// searchResult is the wire shape every public list endpoint emits.
// Matches ui/app/src/types/search.ts SearchResult exactly so the SPA
// can render results without a translation layer.
//
// The polymorphic idx_opportunities_rt schema does not carry slug,
// company, location_text, remote_type, category (singular), or
// quality_score — those derived fields are computed here from the
// columns the schema does carry:
//
//	slug          ← strconv.FormatUint(id, 10) (numeric id as string)
//	company       ← issuing_entity
//	location_text ← "city, region, country" (compact, skips empties)
//	remote_type   ← derived from `remote` bool + `geo_scope`
//	category      ← first categories[] entry resolved via Registry; "" otherwise
//	quality_score ← 0 (no proxy in the polymorphic schema)
//	snippet       ← first 280 chars of description, on a word boundary
type searchResult struct {
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	Title         string  `json:"title"`
	Company       string  `json:"company"`
	Description   string  `json:"description,omitempty"`
	LocationText  string  `json:"location_text"`
	Country       string  `json:"country"`
	RemoteType    string  `json:"remote_type"`
	Category      string  `json:"category"`
	EmploymentType string `json:"employment_type,omitempty"`
	Seniority      string `json:"seniority,omitempty"`
	SalaryMin     float64 `json:"salary_min"`
	SalaryMax     float64 `json:"salary_max"`
	Currency      string  `json:"currency"`
	PostedAt      *time.Time `json:"posted_at"`
	QualityScore  float64 `json:"quality_score"`
	Snippet       string  `json:"snippet"`
	IsFeatured    bool    `json:"is_featured"`
	Kind          string  `json:"kind,omitempty"`
}

// toSearchResult converts an internal job (typed mirror of the
// Manticore document) into the SPA-facing wire shape.
func toSearchResult(j job, categoryLabel func(int64) string) searchResult {
	idStr := strconv.FormatUint(j.ID, 10)
	out := searchResult{
		ID:             idStr,
		Slug:           idStr,
		Title:          j.Title,
		Company:        j.IssuingEntity,
		Description:    j.Description,
		LocationText:   buildLocationText(j),
		Country:        j.Country,
		RemoteType:     deriveRemoteType(j),
		EmploymentType: j.EmploymentType,
		Seniority:      j.Seniority,
		SalaryMin:      j.AmountMin,
		SalaryMax:      j.AmountMax,
		Currency:       j.Currency,
		PostedAt:       j.PostedAt,
		Snippet:        buildSnippet(j.Description),
		Kind:           j.Kind,
	}
	if len(j.Categories) > 0 && categoryLabel != nil {
		out.Category = categoryLabel(j.Categories[0])
	}
	return out
}

// toSearchResults converts a slice of jobs to wire shape with a single
// closure over the registry resolver.
func toSearchResults(jobs []job, categoryLabel func(int64) string) []searchResult {
	out := make([]searchResult, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toSearchResult(j, categoryLabel))
	}
	return out
}

func buildLocationText(j job) string {
	parts := make([]string, 0, 3)
	if j.City != "" {
		parts = append(parts, j.City)
	}
	if j.Region != "" {
		parts = append(parts, j.Region)
	}
	if j.Country != "" {
		parts = append(parts, j.Country)
	}
	return strings.Join(parts, ", ")
}

// deriveRemoteType collapses the schema's separate (remote bool, geo_scope)
// columns into the single string the SPA expects: "remote" | "hybrid" |
// "on_site" | "" (unknown).
func deriveRemoteType(j job) string {
	if j.Remote {
		return "remote"
	}
	switch j.GeoScope {
	case "hybrid":
		return "hybrid"
	case "remote":
		return "remote"
	case "local", "national", "regional", "onsite", "on_site":
		return "on_site"
	}
	return ""
}

// buildSnippet trims description to ~280 chars on a word boundary,
// suitable for the search-result card. Empty description ⇒ empty.
func buildSnippet(desc string) string {
	const max = 280
	if len(desc) <= max {
		return desc
	}
	cut := desc[:max]
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// facetEntry / facetEntries match Facets / FacetEntry in
// ui/app/src/types/search.ts. The internal Manticore aggregation comes
// back as map[string]int; the SPA expects []{key, count} sorted by
// count desc for stable rendering.
type facetEntry struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// toFacetEntries flattens a {key:count} map into a count-desc slice.
func toFacetEntries(m map[string]int) []facetEntry {
	out := make([]facetEntry, 0, len(m))
	for k, c := range m {
		out = append(out, facetEntry{Key: k, Count: c})
	}
	// Bubble the largest counts first; secondary sort by key for determinism.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Count > out[i].Count || (out[j].Count == out[i].Count && out[j].Key < out[i].Key) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// shapeFacetsForSPA converts the Manticore aggregation shape used by
// jobsManticore.Facets into the SPA's required category/remote_type/
// employment_type/seniority/country families. Missing families are
// emitted as empty slices so the SPA can iterate without nil checks.
func shapeFacetsForSPA(raw map[string]map[string]int) map[string][]facetEntry {
	out := map[string][]facetEntry{
		"category":        toFacetEntries(raw["categories"]),
		"remote_type":     toFacetEntries(raw["geo_scope"]),
		"employment_type": toFacetEntries(raw["employment_type"]),
		"seniority":       toFacetEntries(raw["seniority"]),
		"country":         toFacetEntries(raw["country"]),
	}
	return out
}
