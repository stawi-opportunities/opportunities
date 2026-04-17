package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// RedirectLink represents a redirect link from the redirect service.
type RedirectLink struct {
	ID             string `json:"id,omitempty"`
	DestinationURL string `json:"destinationUrl,omitempty"`
	Slug           string `json:"slug,omitempty"`
	AffiliateID    string `json:"affiliateId,omitempty"`
	Campaign       string `json:"campaign,omitempty"`
	Source         string `json:"source,omitempty"`
	Medium         string `json:"medium,omitempty"`
}

// RedirectClient is a lightweight client for the redirect.v1.RedirectService.
// It speaks Connect protocol (JSON) over HTTP without needing generated protobuf code.
type RedirectClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewRedirectClient creates a new redirect client for the given base URL.
func NewRedirectClient(baseURL string) *RedirectClient {
	return &RedirectClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{},
	}
}

// CreateLink creates a tracked redirect link via the redirect service.
func (c *RedirectClient) CreateLink(ctx context.Context, link *RedirectLink) (*RedirectLink, error) {
	reqBody := map[string]any{
		"data": map[string]any{
			"destinationUrl": link.DestinationURL,
			"affiliateId":    link.AffiliateID,
			"campaign":       link.Campaign,
			"source":         link.Source,
			"medium":         link.Medium,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("redirect: marshal request: %w", err)
	}

	url := c.baseURL + "/redirect.v1.RedirectService/CreateLink"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("redirect: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("redirect: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("redirect: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("redirect: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data *RedirectLink `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("redirect: unmarshal response: %w", err)
	}

	if result.Data == nil {
		return nil, fmt.Errorf("redirect: empty response data")
	}

	return result.Data, nil
}
