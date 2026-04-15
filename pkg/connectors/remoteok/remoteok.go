// Package remoteok implements a connector for the RemoteOK JSON API.
package remoteok

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/domain"
)

const apiURL = "https://remoteok.com/api"

// Connector fetches jobs from the RemoteOK public JSON API.
type Connector struct {
	client *httpx.Client
}

// New creates a RemoteOK Connector with sensible defaults.
func New() *Connector {
	return &Connector{
		client: httpx.NewClient(30*time.Second, "stawi.jobs/crawler"),
	}
}

// Type returns the SourceType for RemoteOK.
func (c *Connector) Type() domain.SourceType { return domain.SourceRemoteOK }

// Crawl fetches the single-page RemoteOK API and returns a CrawlIterator.
func (c *Connector) Crawl(ctx context.Context, _ domain.Source) connectors.CrawlIterator {
	raw, status, err := c.client.Get(ctx, apiURL, map[string]string{
		"Accept":     "application/json",
		"User-Agent": "stawi.jobs/1.0 (job aggregator; +https://stawi.jobs)",
	})
	if err != nil {
		return connectors.NewSinglePageIterator(nil, raw, status, err)
	}
	if status != 200 {
		return connectors.NewSinglePageIterator(nil, raw, status,
			fmt.Errorf("remoteok: unexpected status %d", status))
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return connectors.NewSinglePageIterator(nil, raw, status,
			fmt.Errorf("remoteok: unmarshal array: %w", err))
	}

	type remoteOKJob struct {
		Slug        string `json:"slug"`
		Position    string `json:"position"`
		Company     string `json:"company"`
		Location    string `json:"location"`
		Description string `json:"description"`
		ApplyURL    string `json:"apply_url"`
		URL         string `json:"url"`
	}

	var jobs []domain.ExternalJob
	for _, item := range items {
		var j remoteOKJob
		if err := json.Unmarshal(item, &j); err != nil {
			continue
		}
		// Skip metadata entries (no position field).
		if j.Position == "" {
			continue
		}
		applyURL := j.ApplyURL
		if applyURL == "" {
			applyURL = j.URL
		}
		jobs = append(jobs, domain.ExternalJob{
			ExternalID:  j.Slug,
			Title:       j.Position,
			Company:     j.Company,
			LocationText: j.Location,
			Description: j.Description,
			ApplyURL:    applyURL,
			RemoteType:  "remote",
			Currency:    "USD",
		})
	}

	return connectors.NewSinglePageIterator(jobs, raw, status, nil)
}
