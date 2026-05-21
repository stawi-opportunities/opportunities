// apps/api/cmd/search_helpers.go
//
// Shared helpers for endpoint handlers: wire-shape types, filter
// builders, and result converters. Factored out from the legacy
// manticore_client.go during the Phase 6 Manticore retirement.
package main

import (
	"strconv"
	"strings"
	"time"
)

// activeFilter returns an empty filter slice. The Postgres backend
// applies activePred() automatically in every query, so callers do not
// need to inject an explicit active clause — this exists for API
// compatibility with the handler code that composes filter slices.
func activeFilter() []map[string]any {
	return nil
}

// searchResult is the SPA-facing wire shape for a single opportunity.
// Field names match ui/app/src/types/search.ts SearchResult exactly.
//
//	id            ← Slug when set (Postgres backend); numeric hashID string otherwise (legacy)
//	category      ← first categories[] entry resolved via Registry; "" otherwise
//	quality_score ← 0 (no proxy in the polymorphic schema)
//	snippet       ← first 280 chars of description, on a word boundary
type searchResult struct {
	ID             string     `json:"id"`
	Slug           string     `json:"slug"`
	Title          string     `json:"title"`
	Company        string     `json:"company"`
	Description    string     `json:"description,omitempty"`
	LocationText   string     `json:"location_text"`
	Country        string     `json:"country"`
	RemoteType     string     `json:"remote_type"`
	Category       string     `json:"category"`
	EmploymentType string     `json:"employment_type,omitempty"`
	Seniority      string     `json:"seniority,omitempty"`
	SalaryMin      float64    `json:"salary_min"`
	SalaryMax      float64    `json:"salary_max"`
	Currency       string     `json:"currency"`
	PostedAt       *time.Time `json:"posted_at"`
	QualityScore   float64    `json:"quality_score"`
	Snippet        string     `json:"snippet"`
	IsFeatured     bool       `json:"is_featured"`
	Kind           string     `json:"kind,omitempty"`
}

// toSearchResult converts an internal job into the SPA-facing wire shape.
// Postgres backend: j.Slug is the canonical public identifier; j.ID is 0.
func toSearchResult(j job, categoryLabel func(int64) string) searchResult {
	// Prefer the real slug; fall back to the numeric ID string for
	// any residual Manticore-era rows that lack a slug column.
	id := j.Slug
	if id == "" {
		id = strconv.FormatUint(j.ID, 10)
	}
	out := searchResult{
		ID:             id,
		Slug:           id,
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

// toSearchResults converts a slice of jobs to wire shape.
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

// deriveRemoteType collapses the schema's (remote bool, geo_scope)
// columns into the single string the SPA expects.
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

// buildSnippet trims description to ~280 chars on a word boundary.
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

// facetEntry matches FacetEntry in ui/app/src/types/search.ts.
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
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Count > out[i].Count || (out[j].Count == out[i].Count && out[j].Key < out[i].Key) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// shapeFacetsForSPA converts raw facet aggregations into the five
// families the SPA's Facets type declares.
func shapeFacetsForSPA(raw map[string]map[string]int) map[string][]facetEntry {
	return map[string][]facetEntry{
		"category":        toFacetEntries(raw["categories"]),
		"remote_type":     toFacetEntries(raw["geo_scope"]),
		"employment_type": toFacetEntries(raw["employment_type"]),
		"seniority":       toFacetEntries(raw["seniority"]),
		"country":         toFacetEntries(raw["country"]),
	}
}
