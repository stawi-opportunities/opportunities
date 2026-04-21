package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"stawi.jobs/pkg/searchindex"
)

// searchV2Hit is the JSON shape returned by /api/v2/search.
type searchV2Hit struct {
	CanonicalID string `json:"canonical_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Company     string `json:"company"`
	Country     string `json:"country"`
	RemoteType  string `json:"remote_type"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
}

type searchV2Response struct {
	Hits  []searchV2Hit `json:"hits"`
	Total int           `json:"total"`
}

// manticoreSearchResponse mirrors the /search JSON response shape.
type manticoreSearchResponse struct {
	Hits struct {
		Total int `json:"total"`
		Hits  []struct {
			ID     int64           `json:"_id"`
			Score  float64         `json:"_score"`
			Source json.RawMessage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// searchV2Handler returns an HTTP handler that queries Manticore's
// idx_jobs_rt via the /search JSON endpoint. Params: q, country,
// remote_type, category, limit. Always filters status='active'.
// Phase 2 — no vectors, no rerank, no tiered cascade.
func searchV2Handler(client *searchindex.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		q := strings.TrimSpace(req.URL.Query().Get("q"))
		country := strings.ToUpper(strings.TrimSpace(req.URL.Query().Get("country")))
		remote := strings.TrimSpace(req.URL.Query().Get("remote_type"))
		category := strings.TrimSpace(req.URL.Query().Get("category"))

		limit := 20
		if s := req.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 50 {
			limit = 50
		}

		filter := []map[string]any{{"equals": map[string]any{"status": "active"}}}
		if country != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"country": country}})
		}
		if remote != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"remote_type": remote}})
		}
		if category != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"category": category}})
		}

		boolQ := map[string]any{"filter": filter}
		if q != "" {
			boolQ["must"] = []map[string]any{{"match": map[string]any{"*": q}}}
		} else {
			boolQ["must"] = []map[string]any{{"match_all": map[string]any{}}}
		}

		query := map[string]any{
			"index": "idx_jobs_rt",
			"query": map[string]any{"bool": boolQ},
			"sort":  []any{map[string]any{"posted_at": "desc"}},
			"limit": limit,
		}

		raw, err := client.Search(ctx, query)
		if err != nil {
			http.Error(w, "search failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var parsed manticoreSearchResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			http.Error(w, "search decode failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		resp := searchV2Response{Total: parsed.Hits.Total}
		for _, h := range parsed.Hits.Hits {
			var src searchV2Hit
			if err := json.Unmarshal(h.Source, &src); err != nil {
				continue
			}
			resp.Hits = append(resp.Hits, src)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
