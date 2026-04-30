package v1

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

// ManticoreSearch adapts *searchindex.Client to the SearchIndex
// interface required by MatchHandler.
type ManticoreSearch struct {
	client *searchindex.Client
	index  string
}

// NewManticoreSearch opens a Manticore client at the given URL and
// returns an adapter bound to `index` (typically "idx_opportunities_rt").
func NewManticoreSearch(url, index string) (*ManticoreSearch, error) {
	c, err := searchindex.Open(searchindex.Config{URL: url})
	if err != nil {
		return nil, fmt.Errorf("search: open: %w", err)
	}
	return &ManticoreSearch{client: c, index: index}, nil
}

// KNNWithFilters builds a Manticore JSON query combining a KNN clause
// on the `embedding` attribute with hard filters on remote_type /
// salary_min / country. Returns the decoded hits.
//
// Manticore's KNN query shape (documented at
// https://manual.manticoresearch.com/Searching/KNN ):
//
//	{
//	  "index": "idx_opportunities_rt",
//	  "knn": { "field": "embedding", "query_vector": [...], "k": 200 },
//	  "query": { "bool": { "must": [ ...filters... ] } },
//	  "_source": ["canonical_id","slug","title","company"]
//	}
func (m *ManticoreSearch) KNNWithFilters(ctx context.Context, req SearchRequest) ([]SearchHit, error) {
	if len(req.Vector) == 0 {
		return nil, fmt.Errorf("search: empty vector")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}

	filters := []map[string]any{
		{"equals": map[string]any{"status": "active"}},
	}
	if req.RemotePreference != "" {
		filters = append(filters, map[string]any{"equals": map[string]any{"remote_type": req.RemotePreference}})
	}
	if req.SalaryMinFloor > 0 {
		filters = append(filters, map[string]any{"range": map[string]any{
			"salary_min": map[string]any{"gte": req.SalaryMinFloor},
		}})
	}
	if len(req.PreferredLocations) > 0 {
		filters = append(filters, map[string]any{"in": map[string]any{"country": req.PreferredLocations}})
	}

	query := map[string]any{
		"index": m.index,
		"knn": map[string]any{
			"field":        "embedding",
			"query_vector": req.Vector,
			"k":            limit,
		},
		"query":   map[string]any{"bool": map[string]any{"must": filters}},
		"_source": []string{"canonical_id", "slug", "title", "company", "apply_url"},
		"limit":   limit,
	}

	raw, err := m.client.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	var out struct {
		Hits struct {
			Hits []struct {
				ID     string  `json:"_id"`
				Score  float64 `json:"_score"`
				Source struct {
					CanonicalID string `json:"canonical_id"`
					Slug        string `json:"slug"`
					Title       string `json:"title"`
					Company     string `json:"company"`
					ApplyURL    string `json:"apply_url"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("search: decode: %w", err)
	}

	hits := make([]SearchHit, 0, len(out.Hits.Hits))
	for _, h := range out.Hits.Hits {
		hits = append(hits, SearchHit{
			CanonicalID: h.Source.CanonicalID,
			Slug:        h.Source.Slug,
			Title:       h.Source.Title,
			Company:     h.Source.Company,
			ApplyURL:    h.Source.ApplyURL,
			Score:       h.Score,
		})
	}
	return hits, nil
}
