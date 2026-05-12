package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

// ManticoreJobsLister implements NewJobsLister against the same
// idx_opportunities_rt index the public search hits. We keep the
// index name + status filter aligned with apps/api/cmd/manticore_client.go
// so the "new jobs this week" list contains exactly what a browsing
// user would see — no schism between the digest and the search UI.
type ManticoreJobsLister struct {
	c     *searchindex.Client
	index string
}

// NewManticoreJobsLister wires the lister against the candidates
// service's existing Manticore client.
func NewManticoreJobsLister(c *searchindex.Client) *ManticoreJobsLister {
	return &ManticoreJobsLister{c: c, index: "idx_opportunities_rt"}
}

// ListNewJobs returns up to `limit` jobs whose `posted_at >= since`,
// filtered by `country` and `kinds`. Empty `country` means no country
// filter; empty `kinds` means no kind filter (defaults applied by
// the caller).
func (l *ManticoreJobsLister) ListNewJobs(ctx context.Context, since time.Time, country string, kinds []string, limit int) ([]eventsv1.DigestJob, error) {
	if limit <= 0 {
		limit = 10
	}
	filter := []map[string]any{
		{"equals": map[string]any{"status": "active"}},
		{"range": map[string]any{"posted_at": map[string]any{"gte": since.Unix()}}},
	}
	if country != "" {
		filter = append(filter, map[string]any{"equals": map[string]any{"country": country}})
	}
	if len(kinds) > 0 {
		shoulds := make([]map[string]any, 0, len(kinds))
		for _, k := range kinds {
			shoulds = append(shoulds, map[string]any{"equals": map[string]any{"kind": k}})
		}
		filter = append(filter, map[string]any{"bool": map[string]any{"should": shoulds}})
	}
	q := map[string]any{
		"index": l.index,
		"query": map[string]any{"bool": map[string]any{"filter": filter}},
		"sort":  []any{map[string]any{"posted_at": "desc"}},
		"limit": limit,
	}
	raw, err := l.c.Search(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("manticore jobs lister: %w", err)
	}
	var parsed struct {
		Hits struct {
			Hits []struct {
				Source struct {
					CanonicalID string `json:"canonical_id"`
					Title       string `json:"title"`
					Company     string `json:"company"`
					Country     string `json:"country"`
					Kind        string `json:"kind"`
					Slug        string `json:"slug"`
					// posted_at is a unix timestamp (int) in the
					// materialiser's schema. Manticore JSON
					// sometimes returns it as a number, sometimes
					// as a string — accept both.
					PostedAt json.RawMessage `json:"posted_at"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("manticore jobs lister decode: %w", err)
	}
	out := make([]eventsv1.DigestJob, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		out = append(out, eventsv1.DigestJob{
			CanonicalID: h.Source.CanonicalID,
			Title:       h.Source.Title,
			Company:     h.Source.Company,
			Country:     h.Source.Country,
			Kind:        h.Source.Kind,
			Slug:        h.Source.Slug,
			PostedAt:    parseEpochMaybeString(h.Source.PostedAt),
		})
	}
	return out, nil
}

func parseEpochMaybeString(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	// Try int first.
	var asInt int64
	if err := json.Unmarshal(raw, &asInt); err == nil && asInt > 0 {
		return time.Unix(asInt, 0).UTC()
	}
	// Then ISO-8601 string.
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil && asString != "" {
		if t, err := time.Parse(time.RFC3339, asString); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ManticoreWeeklyStatsLister implements WeeklyStatsLister against
// Manticore's `aggs` facet feature. We compute three numbers per run:
//
//  1. total_new_this_week — count of `status=active AND posted_at >= since`.
//  2. top_countries — top 3 buckets of `country` agg.
//  3. top_kinds — top 3 buckets of `kind` agg.
//
// All in one /search round trip (limit=0; we only care about the
// aggregation block).
type ManticoreWeeklyStatsLister struct {
	c     *searchindex.Client
	index string
}

// NewManticoreWeeklyStatsLister wires the lister.
func NewManticoreWeeklyStatsLister(c *searchindex.Client) *ManticoreWeeklyStatsLister {
	return &ManticoreWeeklyStatsLister{c: c, index: "idx_opportunities_rt"}
}

// GlobalStats fetches the three headline numbers for the past N days.
func (l *ManticoreWeeklyStatsLister) GlobalStats(ctx context.Context, since time.Time) (eventsv1.DigestStats, error) {
	q := map[string]any{
		"index": l.index,
		"query": map[string]any{"bool": map[string]any{"filter": []map[string]any{
			{"equals": map[string]any{"status": "active"}},
			{"range": map[string]any{"posted_at": map[string]any{"gte": since.Unix()}}},
		}}},
		"limit": 0,
		"aggs": map[string]any{
			"country": map[string]any{"terms": map[string]any{"field": "country", "size": 32}},
			"kind":    map[string]any{"terms": map[string]any{"field": "kind", "size": 8}},
		},
	}
	raw, err := l.c.Search(ctx, q)
	if err != nil {
		return eventsv1.DigestStats{}, fmt.Errorf("manticore weekly stats: %w", err)
	}
	var parsed struct {
		Hits struct {
			Total int `json:"total"`
		} `json:"hits"`
		Aggregations map[string]struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int    `json:"doc_count"`
			} `json:"buckets"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return eventsv1.DigestStats{}, fmt.Errorf("manticore weekly stats decode: %w", err)
	}
	return eventsv1.DigestStats{
		TotalNewThisWeek: parsed.Hits.Total,
		TopCountries:     top3(parsed.Aggregations["country"].Buckets),
		TopKinds:         top3(parsed.Aggregations["kind"].Buckets),
	}, nil
}

func top3(buckets []struct {
	Key      string `json:"key"`
	DocCount int    `json:"doc_count"`
}) []eventsv1.DigestStatBucket {
	if len(buckets) == 0 {
		return nil
	}
	// Manticore returns buckets descending by doc_count already, but
	// sort defensively in case a future Manticore version changes
	// that.
	cp := make([]eventsv1.DigestStatBucket, 0, len(buckets))
	for _, b := range buckets {
		if b.Key == "" {
			continue
		}
		cp = append(cp, eventsv1.DigestStatBucket{Code: b.Key, Count: b.DocCount})
	}
	sort.Slice(cp, func(i, j int) bool { return cp[i].Count > cp[j].Count })
	if len(cp) > 3 {
		cp = cp[:3]
	}
	return cp
}
