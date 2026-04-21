// apps/api/cmd/endpoints_v2.go
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func v2SearchHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		q := strings.TrimSpace(qs.Get("q"))
		filter := []map[string]any{{"equals": map[string]any{"status": "active"}}}
		if v := strings.ToUpper(strings.TrimSpace(qs.Get("country"))); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"country": v}})
		}
		if v := strings.TrimSpace(qs.Get("remote_type")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"remote_type": v}})
		}
		if v := strings.TrimSpace(qs.Get("employment_type")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"employment_type": v}})
		}
		if v := strings.TrimSpace(qs.Get("category")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"category": v}})
		}
		if v := strings.TrimSpace(qs.Get("seniority")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"seniority": v}})
		}
		if v := qs.Get("salary_min"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter = append(filter, map[string]any{"range": map[string]any{"salary_min": map[string]any{"gte": n}}})
			}
		}
		if v := qs.Get("salary_max"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter = append(filter, map[string]any{"range": map[string]any{"salary_max": map[string]any{"lte": n}}})
			}
		}

		limit := parseLimit(qs.Get("limit"), 20, 50)

		boolQ := map[string]any{"filter": filter}
		if q != "" {
			boolQ["must"] = []map[string]any{{"match": map[string]any{"*": q}}}
		}

		sort := qs.Get("sort")
		var sortSpec []any
		switch sort {
		case "recent":
			sortSpec = []any{map[string]any{"posted_at": "desc"}}
		case "quality":
			sortSpec = []any{map[string]any{"quality_score": "desc"}, map[string]any{"posted_at": "desc"}}
		default:
			if q == "" {
				sortSpec = []any{map[string]any{"posted_at": "desc"}}
			}
		}

		query := map[string]any{
			"index": "idx_jobs_rt",
			"query": map[string]any{"bool": boolQ},
			"limit": limit,
			"aggs": map[string]any{
				"category":        map[string]any{"terms": map[string]any{"field": "category", "size": 32}},
				"country":         map[string]any{"terms": map[string]any{"field": "country", "size": 200}},
				"remote_type":     map[string]any{"terms": map[string]any{"field": "remote_type", "size": 8}},
				"employment_type": map[string]any{"terms": map[string]any{"field": "employment_type", "size": 16}},
				"seniority":       map[string]any{"terms": map[string]any{"field": "seniority", "size": 16}},
			},
		}
		if sortSpec != nil {
			query["sort"] = sortSpec
		}

		raw, err := jm.c.Search(ctx, query)
		if err != nil {
			http.Error(w, `{"error":"search failed: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		var parsed struct {
			Hits struct {
				Total int `json:"total"`
				Hits  []struct {
					Source job `json:"_source"`
				} `json:"hits"`
			} `json:"hits"`
			Aggregations map[string]struct {
				Buckets []struct {
					Key      string `json:"key"`
					DocCount int    `json:"doc_count"`
				} `json:"buckets"`
			} `json:"aggregations"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			http.Error(w, `{"error":"decode failed"}`, http.StatusInternalServerError)
			return
		}
		hits := make([]job, 0, len(parsed.Hits.Hits))
		for _, h := range parsed.Hits.Hits {
			hits = append(hits, h.Source)
		}
		facets := map[string]map[string]int{}
		for name, agg := range parsed.Aggregations {
			m := map[string]int{}
			for _, b := range agg.Buckets {
				if b.Key != "" {
					m[b.Key] = b.DocCount
				}
			}
			facets[name] = m
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":   q,
			"results": hits,
			"facets":  facets,
			"total":   parsed.Hits.Total,
			"sort":    sort,
		})
	}
}

func v2JobByIDHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		if id == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}
		j, err := jm.GetByID(req.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if j == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(j)
	}
}

func v2TopHandler(jm *jobsManticore) http.HandlerFunc {
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
			"results":   rows,
		})
	}
}

func v2LatestHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 100)
		rows, err := jm.Latest(req.Context(), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
	}
}

func v2CategoriesHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		facets, err := jm.Facets(req.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		type bucket struct {
			Slug  string `json:"slug"`
			Count int    `json:"count"`
		}
		out := make([]bucket, 0, len(facets["category"]))
		for k, n := range facets["category"] {
			out = append(out, bucket{Slug: k, Count: n})
		}
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"categories": out})
	}
}

func v2StatsHandler(jm *jobsManticore) http.HandlerFunc {
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
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_jobs":      totalJobs,
			"total_companies": 0,
			"countries":       countries,
		})
	}
}

func v2FeedHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		country := strings.ToUpper(qs.Get("country"))
		if country == "" {
			country = strings.ToUpper(req.Header.Get("CF-IPCountry"))
		}
		perTier := parseLimit(qs.Get("per_tier"), 10, 30)

		type tier struct {
			Name    string `json:"name"`
			Country string `json:"country,omitempty"`
			Results []job  `json:"results"`
		}

		resp := struct {
			Country string `json:"country"`
			Tiers   []tier `json:"tiers"`
		}{Country: country}

		if country != "" {
			localFilter := []map[string]any{
				{"equals": map[string]any{"status": "active"}},
				{"equals": map[string]any{"country": country}},
			}
			local, err := jm.searchFiltered(ctx, localFilter, perTier, "posted_at")
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			resp.Tiers = append(resp.Tiers, tier{Name: "local", Country: country, Results: local})
		}

		global, err := jm.Latest(ctx, perTier)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp.Tiers = append(resp.Tiers, tier{Name: "global", Results: global})

		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func v2FeedTierHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()
		tierName := qs.Get("tier")
		limit := parseLimit(qs.Get("limit"), 20, 50)

		filter := []map[string]any{{"equals": map[string]any{"status": "active"}}}
		if tierName == "local" {
			country := strings.ToUpper(qs.Get("country"))
			if country == "" {
				http.Error(w, `{"error":"country required for local tier"}`, http.StatusBadRequest)
				return
			}
			filter = append(filter, map[string]any{"equals": map[string]any{"country": country}})
		}

		rows, err := jm.searchFiltered(ctx, filter, limit, "posted_at")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tier":    tierName,
			"results": rows,
		})
	}
}
