// apps/api/cmd/endpoints_v2.go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pitabwire/util"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/counters"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/publish"
)

// (universalFacets was removed when the search response moved to the
// fixed SPA-shaped facet families in shapeFacetsForSPA — the SPA's
// Facets type already enumerates the wanted families.)

func searchHandler(jm JobsBackend, reg *opportunity.Registry, ct *counters.Counters) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		q := strings.TrimSpace(qs.Get("q"))
		// Active-row filtering is enforced by the PostgreSQL backend;
		// caller-supplied filters compose on top of it.
		filter := append([]map[string]any{}, activeFilter()...)
		if v := strings.ToUpper(strings.TrimSpace(qs.Get("country"))); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"country": v}})
		}
		// `remote_type` → `geo_scope` (schema column) for callers that
		// still use the old query-string name. New callers should send
		// `geo_scope=...` directly.
		if v := strings.TrimSpace(qs.Get("geo_scope")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"geo_scope": v}})
		} else if v := strings.TrimSpace(qs.Get("remote_type")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"geo_scope": v}})
		}
		if v := strings.TrimSpace(qs.Get("employment_type")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"employment_type": v}})
		}
		// Category labels live under attributes.categories (string array).
		// Match the public slug case-insensitively via the equals path.
		if v := strings.TrimSpace(qs.Get("category")); v != "" {
			filter = append(filter, map[string]any{
				"equals": map[string]any{"categories": v},
			})
		}
		if v := strings.TrimSpace(qs.Get("seniority")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"seniority": v}})
		}
		if v := strings.TrimSpace(qs.Get("kind")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"kind": v}})
		}
		// `salary_min`/`salary_max` → `amount_min`/`amount_max` (universal
		// monetary columns in the polymorphic schema).
		if v := qs.Get("salary_min"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter = append(filter, map[string]any{"range": map[string]any{"amount_min": map[string]any{"gte": n}}})
			}
		}
		if v := qs.Get("salary_max"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter = append(filter, map[string]any{"range": map[string]any{"amount_max": map[string]any{"lte": n}}})
			}
		}

		limit := parseLimit(qs.Get("limit"), 20, 50)

		boolQ := map[string]any{"filter": filter}
		if q != "" {
			boolQ["must"] = []map[string]any{{"match": map[string]any{"*": q}}}
		}

		// Default sort is newest-first so browse and filtered lists surface
		// fresh roles. Explicit "relevance" is only meaningful with a query.
		sort := strings.TrimSpace(qs.Get("sort"))
		if sort == "" {
			if q != "" {
				sort = "relevance"
			} else {
				sort = "recent"
			}
		}

		// Facet families requested per page; PostgreSQL resolves them with
		// GROUP BY queries.
		aggs := map[string]any{
			"kind":            map[string]any{"terms": map[string]any{"field": "kind", "size": 16}},
			"categories":      map[string]any{"terms": map[string]any{"field": "categories", "size": 32}},
			"country":         map[string]any{"terms": map[string]any{"field": "country", "size": 200}},
			"geo_scope":       map[string]any{"terms": map[string]any{"field": "geo_scope", "size": 8}},
			"employment_type": map[string]any{"terms": map[string]any{"field": "employment_type", "size": 16}},
			"seniority":       map[string]any{"terms": map[string]any{"field": "seniority", "size": 16}},
			"field_of_study":  map[string]any{"terms": map[string]any{"field": "field_of_study", "size": 32}},
			"degree_level":    map[string]any{"terms": map[string]any{"field": "degree_level", "size": 8}},
		}
		_ = boolQ // composed into the backend Search via filter slice below
		hits, total, rawFacets, err := jm.Search(ctx, q, filter, sort, limit, aggs)
		if err != nil {
			http.Error(w, `{"error":"search failed: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}

		decorated := embedCounters(ctx, ct, hits, registryCategoryLabel(reg))
		facets := shapeFacetsForSPA(rawFacets)
		hasMore := len(hits) < total
		cursorNext := ""
		if hasMore {
			cursorNext = strconv.Itoa(limit)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":       q,
			"results":     decorated,
			"facets":      facets,
			"total":       total,
			"sort":        sort,
			"has_more":    hasMore,
			"cursor_next": cursorNext,
		})
	}
}

// searchResultWithStats is the v2-search result row in the wire shape
// the SPA expects, plus the 24h view + apply counters. Embedded
// in-line so the SPA component reads the counters as top-level fields.
type searchResultWithStats struct {
	searchResult
	Views24h   int64 `json:"views_24h"`
	Applies24h int64 `json:"applies_24h"`
}

// embedCounters fetches view+apply counts in one Valkey MGET and
// attaches them to every hit. Returns the converted-to-wire-shape
// hits unchanged when the counters client is nil. `categoryLabel`
// resolves the first categories[] id to a display string; pass a nil
// resolver to leave Category empty.
func embedCounters(ctx context.Context, ct *counters.Counters, hits []job, categoryLabel func(int64) string) []searchResultWithStats {
	out := make([]searchResultWithStats, len(hits))
	for i, h := range hits {
		out[i] = searchResultWithStats{searchResult: toSearchResult(h, categoryLabel)}
	}
	if ct == nil || len(hits) == 0 {
		return out
	}
	slugs := make([]string, 0, len(hits))
	for _, h := range hits {
		slugs = append(slugs, h.Slug)
	}
	stats, err := ct.GetStatsBatch(ctx, slugs)
	if err != nil {
		util.Log(ctx).WithError(err).Debug("search: counters batch failed")
		return out
	}
	for i, h := range hits {
		s := stats[h.Slug]
		out[i].Views24h = s.Views24h
		out[i].Applies24h = s.Applies24h
	}
	return out
}

// registryCategoryLabel returns a label-resolver function suitable for
// passing to toSearchResult / toSearchResults. nil registry yields a
// nil resolver so callers' wire output has Category=="" for every row,
// which is the right fallback when the registry can't be loaded.
func registryCategoryLabel(reg *opportunity.Registry) func(int64) string {
	if reg == nil {
		return nil
	}
	labels := reg.CategoryLabels()
	return func(id int64) string {
		return labels[id]
	}
}

func jobByIDHandler(jm JobsBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		if id == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}
		j, err := jm.GetBySlug(req.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if j == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		attrs := j.Attributes
		if attrs != nil {
			// Copy so we never mutate the cached map; strip paywalled keys.
			cp := make(map[string]any, len(attrs))
			for k, v := range attrs {
				if k == "how_to_apply" {
					continue
				}
				cp[k] = v
			}
			attrs = cp
		}
		// Ensure description is sanitized HTML for consistent client rendering
		// (new rows stored as HTML; legacy markdown/plain converted at read).
		descHTML := publish.DescriptionHTML(j.Description)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": 1,
			"id":             j.CanonicalID, "slug": j.Slug, "kind": j.Kind,
			"title": j.Title, "description": descHTML,
			// Public flag only — how_to_apply body is on GET /me/opportunities/{id}/apply.
			"has_how_to_apply": j.HasHowToApply,
			"issuing_entity":   j.IssuingEntity, "apply_url": j.ApplyURL,
			"posted_at": j.PostedAt, "deadline": j.Deadline,
			"anchor_location": map[string]any{"country": j.Country, "region": j.Region, "city": j.City},
			"remote":          j.Remote, "geo_scope": j.GeoScope,
			"amount_min": j.AmountMin, "amount_max": j.AmountMax, "currency": j.Currency,
			"attributes": attrs,
		})
	}
}

func topHandler(jm JobsBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 200)
		minScore := 60.0
		if v := req.URL.Query().Get("min_score"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				minScore = f
			}
		}
		rows, err := jm.Top(req.Context(), minScore, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"min_score": minScore,
			"count":     len(rows),
			"results":   toSearchResults(rows, nil),
		})
	}
}

func latestHandler(jm JobsBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 100)
		rows, err := jm.Latest(req.Context(), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": toSearchResults(rows, nil)})
	}
}

func categoriesHandler(jm JobsBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		facets, err := jm.Facets(req.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		// SPA FacetEntry expects {key, count}. Prefer attributes-derived
		// "categories"; fall back to empty list when none are stored yet.
		raw := facets["categories"]
		out := make([]facetEntry, 0, len(raw))
		for k, n := range raw {
			out = append(out, facetEntry{Key: k, Count: n})
		}
		// Stable order by count desc.
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if out[j].Count > out[i].Count || (out[j].Count == out[i].Count && out[j].Key < out[i].Key) {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"categories": out})
	}
}

// categoryJobsHandler serves GET /api/categories/{slug}/jobs — jobs filtered
// by attributes.categories containing the slug (case-insensitive).
func categoryJobsHandler(jm JobsBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		slug := strings.TrimSpace(req.PathValue("slug"))
		if slug == "" {
			http.Error(w, `{"error":"missing category"}`, http.StatusBadRequest)
			return
		}
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 100)
		// Fetch one extra to signal has_more. Cursor is last_seen_at ISO for simplicity.
		filter := []map[string]any{
			{"equals": map[string]any{"categories": slug}},
		}
		if cur := strings.TrimSpace(req.URL.Query().Get("cursor")); cur != "" {
			if t, err := time.Parse(time.RFC3339, cur); err == nil {
				filter = append(filter, map[string]any{
					"range": map[string]any{"last_seen_at": map[string]any{"lte": t.UTC().Format(time.RFC3339)}},
				})
			}
		}
		// Newest postings first (posted_at), not crawl last_seen.
		rows, err := jm.SearchFiltered(req.Context(), filter, limit+1, "posted_at")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}
		cursorNext := ""
		if hasMore && len(rows) > 0 {
			// Approximate next page: last row's posted/last_seen isn't on job;
			// use posted_at when present else empty (client stops).
			last := rows[len(rows)-1]
			if last.PostedAt != nil {
				cursorNext = last.PostedAt.UTC().Format(time.RFC3339)
			}
		}
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"category":    slug,
			"results":     toSearchResults(rows, nil),
			"has_more":    hasMore,
			"cursor_next": cursorNext,
		})
	}
}

func jobStatsHandler(jm JobsBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		totalJobs, err := jm.Count(ctx, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		facets, err := jm.Facets(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		countries := len(facets["country"])
		// Company count: best-effort via the Postgres backend; a fresh
		// environment without the companies table just reports 0.
		totalCompanies := 0
		if cc, ok := jm.(interface {
			CountCompanies(context.Context) (int, error)
		}); ok {
			if n, cerr := cc.CountCompanies(ctx); cerr == nil {
				totalCompanies = n
			}
		}
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_jobs":      totalJobs,
			"total_companies": totalCompanies,
			"countries":       countries,
		})
	}
}

// feedTier matches the FeedTier type the SPA imports from
// ui/app/src/types/search.ts. Field names use snake_case to match the
// SPA's JSON parsing; cursor + has_more drive client-side pagination
// for the "Load more" affordance.
type feedTier struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Jobs      []searchResult `json:"jobs"`
	Cursor    string         `json:"cursor"`
	HasMore   bool           `json:"has_more"`
	Country   string         `json:"country,omitempty"`
	Countries []string       `json:"countries,omitempty"`
	Language  string         `json:"language,omitempty"`
}

// feedContext matches FeedContext on the SPA side. The geo-IP-derived
// country drives "local" tier filtering; the SPA reads context.country
// directly so this MUST be populated (or set to "") — `undefined` here
// is the exact crash the SPA exhibited before this rewrite.
type feedContext struct {
	Country   string   `json:"country"`
	Languages []string `json:"languages"`
	Region    string   `json:"region"`
}

// feedResponse matches FeedResponse on the SPA side. Facets is required
// (the SPA sidebar reads it); empty slices are fine for the no-result
// case. Sort echoes back the input so the SPA can show the active sort.
type feedResponse struct {
	Context feedContext             `json:"context"`
	Tiers   []feedTier              `json:"tiers"`
	Facets  map[string][]facetEntry `json:"facets"`
	Sort    string                  `json:"sort"`
}

// feedHandler returns the home-page tiered feed in the shape the
// front-end SPA expects (see ui/app/src/types/search.ts FeedResponse).
// Previous response (root-level `country`, tiers with `name`/`results`)
// caused the SPA to crash with "Cannot read properties of undefined
// (reading 'country')" because it dereferenced `context.country` and
// expected `tiers[].id`/`jobs` not `name`/`results`. This rewrite
// produces the exact wire shape the SPA imports.
func feedHandler(jm JobsBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		country := strings.ToUpper(qs.Get("country"))
		if country == "" {
			country = strings.ToUpper(req.Header.Get("CF-IPCountry"))
		}
		perTier := parseLimit(qs.Get("per_tier"), 20, 30)
		sort := qs.Get("sort")
		if sort == "" {
			sort = "recent"
		}

		resp := feedResponse{
			Context: feedContext{
				Country:   country,
				Languages: []string{},
				Region:    "",
			},
			Tiers:  []feedTier{},
			Facets: map[string][]facetEntry{},
			Sort:   sort,
		}

		if country != "" {
			localFilter := append(activeFilter(),
				map[string]any{"equals": map[string]any{"country": country}})
			local, err := jm.SearchFiltered(ctx, localFilter, perTier+1, "posted_at")
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			hasMore := len(local) > perTier
			if hasMore {
				local = local[:perTier]
			}
			resp.Tiers = append(resp.Tiers, feedTier{
				ID:      "local",
				Label:   "Near you",
				Jobs:    toSearchResults(local, nil),
				Country: country,
				HasMore: hasMore,
			})
		}

		global, err := jm.Latest(ctx, perTier+1)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hasMoreGlobal := len(global) > perTier
		if hasMoreGlobal {
			global = global[:perTier]
		}
		resp.Tiers = append(resp.Tiers, feedTier{
			ID:      "global",
			Label:   "Global",
			Jobs:    toSearchResults(global, nil),
			HasMore: hasMoreGlobal,
		})

		// Best-effort facets — failure here should not blank out the
		// feed since results are the more important payload.
		if facets, ferr := jm.Facets(ctx); ferr == nil {
			resp.Facets = shapeFacetsForSPA(facets)
		}

		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// feedTierHandler returns one page of a specific tier (used by the
// SPA's "Load more" button). Response shape mirrors TierPageResponse
// in ui/app/src/types/search.ts.
func feedTierHandler(jm JobsBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()
		tierName := qs.Get("tier")
		if tierName == "" {
			tierName = qs.Get("id")
		}
		limit := parseLimit(qs.Get("limit"), 20, 50)

		filter := append([]map[string]any{}, activeFilter()...)
		respCountry := ""
		if tierName == "local" {
			country := strings.ToUpper(qs.Get("country"))
			if country == "" {
				http.Error(w, `{"error":"country required for local tier"}`, http.StatusBadRequest)
				return
			}
			filter = append(filter, map[string]any{"equals": map[string]any{"country": country}})
			respCountry = country
		}

		rows, err := jm.SearchFiltered(ctx, filter, limit+1, "posted_at")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(feedTier{
			ID:      tierName,
			Label:   tierName,
			Jobs:    toSearchResults(rows, nil),
			Country: respCountry,
			HasMore: hasMore,
		})
	}
}

func variantsRejectedHandler(db func(context.Context, bool) *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit := parseLimit(req.URL.Query().Get("limit"), 100, 500)
		var rows []struct {
			VariantID  string          `json:"variant_id"`
			SourceID   string          `json:"source_id"`
			OccurredAt time.Time       `json:"occurred_at"`
			Details    json.RawMessage `json:"details"`
		}
		if err := db(req.Context(), true).WithContext(req.Context()).Raw(`SELECT variant_id,source_id,occurred_at,details
			FROM job_ingest_events WHERE event_type='rejected' ORDER BY occurred_at DESC LIMIT ?`, limit).Scan(&rows).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"rejections": rows, "count": len(rows)})
	}
}
