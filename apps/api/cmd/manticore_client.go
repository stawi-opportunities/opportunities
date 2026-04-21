package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"stawi.jobs/pkg/searchindex"
)

type job struct {
	CanonicalID    string     `json:"canonical_id"`
	Slug           string     `json:"slug"`
	Title          string     `json:"title"`
	Company        string     `json:"company"`
	Description    string     `json:"description,omitempty"`
	LocationText   string     `json:"location_text,omitempty"`
	Country        string     `json:"country,omitempty"`
	Language       string     `json:"language,omitempty"`
	RemoteType     string     `json:"remote_type,omitempty"`
	EmploymentType string     `json:"employment_type,omitempty"`
	Seniority      string     `json:"seniority,omitempty"`
	Category       string     `json:"category,omitempty"`
	Industry       string     `json:"industry,omitempty"`
	SalaryMin      *int       `json:"salary_min,omitempty"`
	SalaryMax      *int       `json:"salary_max,omitempty"`
	Currency       string     `json:"currency,omitempty"`
	QualityScore   float64    `json:"quality_score,omitempty"`
	IsFeatured     bool       `json:"is_featured,omitempty"`
	PostedAt       *time.Time `json:"posted_at,omitempty"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Status         string     `json:"status,omitempty"`
}

type jobsManticore struct {
	c *searchindex.Client
}

func newJobsManticore(c *searchindex.Client) *jobsManticore {
	return &jobsManticore{c: c}
}

func (j *jobsManticore) GetByID(ctx context.Context, id string) (*job, error) {
	q := map[string]any{
		"index": "idx_jobs_rt",
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []map[string]any{
					{"equals": map[string]any{"status": "active"}},
					{"bool": map[string]any{"should": []map[string]any{
						{"equals": map[string]any{"canonical_id": id}},
						{"equals": map[string]any{"slug": id}},
					}}},
				},
			},
		},
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

func (j *jobsManticore) Count(ctx context.Context, filter []map[string]any) (int, error) {
	f := []map[string]any{{"equals": map[string]any{"status": "active"}}}
	f = append(f, filter...)
	q := map[string]any{
		"index": "idx_jobs_rt",
		"query": map[string]any{"bool": map[string]any{"filter": f}},
		"limit": 0,
	}
	_, total, err := j.search(ctx, q)
	return total, err
}

func (j *jobsManticore) Top(ctx context.Context, minScore float64, limit int) ([]job, error) {
	f := []map[string]any{{"equals": map[string]any{"status": "active"}}}
	if minScore > 0 {
		f = append(f, map[string]any{"range": map[string]any{
			"quality_score": map[string]any{"gte": minScore},
		}})
	}
	q := map[string]any{
		"index": "idx_jobs_rt",
		"query": map[string]any{"bool": map[string]any{"filter": f}},
		"sort":  []any{map[string]any{"quality_score": "desc"}, map[string]any{"posted_at": "desc"}},
		"limit": limit,
	}
	hits, _, err := j.search(ctx, q)
	return hits, err
}

func (j *jobsManticore) Latest(ctx context.Context, limit int) ([]job, error) {
	q := map[string]any{
		"index": "idx_jobs_rt",
		"query": map[string]any{"bool": map[string]any{"filter": []map[string]any{
			{"equals": map[string]any{"status": "active"}},
		}}},
		"sort":  []any{map[string]any{"posted_at": "desc"}},
		"limit": limit,
	}
	hits, _, err := j.search(ctx, q)
	return hits, err
}

func (j *jobsManticore) Facets(ctx context.Context) (map[string]map[string]int, error) {
	q := map[string]any{
		"index": "idx_jobs_rt",
		"query": map[string]any{"bool": map[string]any{"filter": []map[string]any{
			{"equals": map[string]any{"status": "active"}},
		}}},
		"limit": 0,
		"aggs": map[string]any{
			"category":        map[string]any{"terms": map[string]any{"field": "category", "size": 64}},
			"country":         map[string]any{"terms": map[string]any{"field": "country", "size": 200}},
			"remote_type":     map[string]any{"terms": map[string]any{"field": "remote_type", "size": 8}},
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

func (j *jobsManticore) search(ctx context.Context, q map[string]any) ([]job, int, error) {
	raw, err := j.c.Search(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	var parsed struct {
		Hits struct {
			Total int `json:"total"`
			Hits  []struct {
				Source job `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, 0, fmt.Errorf("manticore decode: %w", err)
	}
	out := make([]job, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		out = append(out, h.Source)
	}
	return out, parsed.Hits.Total, nil
}
