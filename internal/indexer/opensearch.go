package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	"stawi.jobs/internal/config"
	"stawi.jobs/internal/domain"
)

const canonicalIndex = "jobs_canonical_v1"

type OpenSearchIndexer struct {
	client *opensearch.Client
	log    *slog.Logger
}

func NewOpenSearchIndexer(cfg config.Config, log *slog.Logger) (Indexer, error) {
	if cfg.OpenSearchURL == "" {
		return nil, fmt.Errorf("empty opensearch url")
	}
	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{cfg.OpenSearchURL}})
	if err != nil {
		return nil, fmt.Errorf("create opensearch client: %w", err)
	}
	idx := &OpenSearchIndexer{client: client, log: log}
	if err := idx.ensureIndex(context.Background()); err != nil {
		return nil, err
	}
	return idx, nil
}

func (o *OpenSearchIndexer) ensureIndex(ctx context.Context) error {
	res, err := o.client.Indices.Exists([]string{canonicalIndex}, o.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("check index exists: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		return nil
	}
	mapping := map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"cluster_id":      map[string]any{"type": "long"},
				"title":           map[string]any{"type": "text"},
				"company":         map[string]any{"type": "text"},
				"description":     map[string]any{"type": "text"},
				"location_text":   map[string]any{"type": "keyword"},
				"country":         map[string]any{"type": "keyword"},
				"remote_type":     map[string]any{"type": "keyword"},
				"employment_type": map[string]any{"type": "keyword"},
				"salary_min":      map[string]any{"type": "double"},
				"salary_max":      map[string]any{"type": "double"},
				"currency":        map[string]any{"type": "keyword"},
				"apply_url":       map[string]any{"type": "keyword"},
			},
		},
	}
	body, _ := json.Marshal(mapping)
	create, err := o.client.Indices.Create(canonicalIndex, o.client.Indices.Create.WithBody(bytes.NewReader(body)), o.client.Indices.Create.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	defer create.Body.Close()
	if create.IsError() {
		return fmt.Errorf("create index status: %s", create.Status())
	}
	return nil
}

func (o *OpenSearchIndexer) IndexCanonicalJob(ctx context.Context, job domain.CanonicalJob) error {
	doc := map[string]any{
		"cluster_id":      job.ClusterID,
		"title":           job.Title,
		"company":         job.Company,
		"description":     job.Description,
		"location_text":   job.LocationText,
		"country":         job.Country,
		"remote_type":     job.RemoteType,
		"employment_type": job.EmploymentType,
		"salary_min":      job.SalaryMin,
		"salary_max":      job.SalaryMax,
		"currency":        job.Currency,
		"apply_url":       job.ApplyURL,
	}
	body, _ := json.Marshal(doc)
	req := opensearchapi.IndexRequest{
		Index:      canonicalIndex,
		DocumentID: fmt.Sprintf("%d", job.ClusterID),
		Body:       bytes.NewReader(body),
		Refresh:    "false",
	}
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("index document: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("index status: %s", res.Status())
	}
	return nil
}

func (o *OpenSearchIndexer) Search(ctx context.Context, query string, limit int) ([]domain.CanonicalJob, error) {
	if limit <= 0 {
		limit = 20
	}
	payload := map[string]any{
		"size": limit,
		"query": map[string]any{
			"multi_match": map[string]any{
				"query":  query,
				"fields": []string{"title^2", "company^2", "description"},
			},
		},
	}
	if strings.TrimSpace(query) == "" {
		payload["query"] = map[string]any{"match_all": map[string]any{}}
	}
	body, _ := json.Marshal(payload)
	req := opensearchapi.SearchRequest{Index: []string{canonicalIndex}, Body: bytes.NewReader(body)}
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return nil, fmt.Errorf("search documents: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("search status: %s", res.Status())
	}
	var raw struct {
		Hits struct {
			Hits []struct {
				Source struct {
					ClusterID      int64   `json:"cluster_id"`
					Title          string  `json:"title"`
					Company        string  `json:"company"`
					Description    string  `json:"description"`
					LocationText   string  `json:"location_text"`
					Country        string  `json:"country"`
					RemoteType     string  `json:"remote_type"`
					EmploymentType string  `json:"employment_type"`
					SalaryMin      float64 `json:"salary_min"`
					SalaryMax      float64 `json:"salary_max"`
					Currency       string  `json:"currency"`
					ApplyURL       string  `json:"apply_url"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode search result: %w", err)
	}
	jobs := make([]domain.CanonicalJob, 0, len(raw.Hits.Hits))
	for _, hit := range raw.Hits.Hits {
		jobs = append(jobs, domain.CanonicalJob{
			ClusterID:      hit.Source.ClusterID,
			Title:          hit.Source.Title,
			Company:        hit.Source.Company,
			Description:    hit.Source.Description,
			LocationText:   hit.Source.LocationText,
			Country:        hit.Source.Country,
			RemoteType:     hit.Source.RemoteType,
			EmploymentType: hit.Source.EmploymentType,
			SalaryMin:      hit.Source.SalaryMin,
			SalaryMax:      hit.Source.SalaryMax,
			Currency:       hit.Source.Currency,
			ApplyURL:       hit.Source.ApplyURL,
		})
	}
	return jobs, nil
}

func New(cfg config.Config, log *slog.Logger) Indexer {
	idx, err := NewOpenSearchIndexer(cfg, log)
	if err != nil {
		log.Warn("opensearch unavailable, using memory indexer", "error", err.Error())
		return NewMemoryIndexer()
	}
	return idx
}
