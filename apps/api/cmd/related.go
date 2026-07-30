package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

// relatedHandler serves GET /api/opportunities/{id}/related — similar listings
// for the detail page (same kind + shared title tokens / country / entity).
//
// Query:
//
//	limit — default 8, max 24
func relatedHandler(jm JobsBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		if id == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}
		limit := parseLimit(req.URL.Query().Get("limit"), 8, 24)

		src, err := jm.GetBySlug(req.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if src == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		// Prefer full-text search on distinctive title tokens; fall back to
		// kind+country filter when the title is too short.
		q := relatedQueryFromTitle(src.Title, src.IssuingEntity)
		filter := []map[string]any{}
		if k := strings.TrimSpace(src.Kind); k != "" {
			filter = append(filter, map[string]any{"term": map[string]any{"kind": k}})
		}
		if c := strings.TrimSpace(src.Country); c != "" {
			filter = append(filter, map[string]any{"term": map[string]any{"country": c}})
		}

		// Over-fetch then drop self + de-dupe.
		fetchLimit := limit * 3
		if fetchLimit < 24 {
			fetchLimit = 24
		}
		var hits []job
		if q != "" {
			rows, _, _, sErr := jm.Search(req.Context(), q, filter, "posted_at", fetchLimit, nil)
			if sErr != nil {
				// Soft-fail to filtered list without text query.
				rows, sErr = jm.SearchFiltered(req.Context(), filter, fetchLimit, "posted_at")
				if sErr != nil {
					http.Error(w, sErr.Error(), http.StatusBadGateway)
					return
				}
			}
			hits = rows
		} else {
			rows, sErr := jm.SearchFiltered(req.Context(), filter, fetchLimit, "posted_at")
			if sErr != nil {
				http.Error(w, sErr.Error(), http.StatusBadGateway)
				return
			}
			hits = rows
		}

		out := make([]job, 0, limit)
		seen := map[string]struct{}{src.Slug: {}, src.CanonicalID: {}}
		// Prefer same issuing entity first.
		entity := strings.ToLower(strings.TrimSpace(src.IssuingEntity))
		pick := func(preferEntity bool) {
			for _, h := range hits {
				if len(out) >= limit {
					return
				}
				key := h.Slug
				if key == "" {
					key = h.CanonicalID
				}
				if _, ok := seen[key]; ok {
					continue
				}
				if preferEntity {
					if entity == "" || !strings.EqualFold(strings.TrimSpace(h.IssuingEntity), entity) {
						continue
					}
				} else if entity != "" && strings.EqualFold(strings.TrimSpace(h.IssuingEntity), entity) {
					continue // already considered
				}
				seen[key] = struct{}{}
				if h.CanonicalID != "" {
					seen[h.CanonicalID] = struct{}{}
				}
				out = append(out, h)
			}
		}
		pick(true)
		pick(false)

		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"source_slug": src.Slug,
			"count":       len(out),
			"results":     toSearchResults(out, nil),
		})
	}
}

// relatedQueryFromTitle builds a short search query from role-like tokens.
func relatedQueryFromTitle(title, entity string) string {
	stop := map[string]struct{}{
		"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "of": {}, "to": {},
		"for": {}, "in": {}, "on": {}, "at": {}, "with": {}, "remote": {},
		"full": {}, "time": {}, "part": {}, "contract": {}, "junior": {},
		"senior": {}, "mid": {}, "level": {}, "ii": {}, "iii": {}, "iv": {},
	}
	var tokens []string
	for _, raw := range strings.FieldsFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		t := strings.ToLower(strings.TrimSpace(raw))
		if len(t) < 3 {
			continue
		}
		if _, bad := stop[t]; bad {
			continue
		}
		tokens = append(tokens, t)
		if len(tokens) >= 5 {
			break
		}
	}
	if len(tokens) == 0 && entity != "" {
		return entity
	}
	return strings.Join(tokens, " ")
}
