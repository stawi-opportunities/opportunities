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

// LinkState mirrors redirect.v1.LinkState. Only the transitions we use
// from stawi-jobs are declared; add more as needed. Values match the
// proto enum so they serialize correctly over Connect/JSON.
type LinkState int

const (
	LinkStateUnspecified LinkState = 0
	LinkStateActive      LinkState = 1
	LinkStatePaused      LinkState = 2
	LinkStateExpired     LinkState = 3
	LinkStateDeleted     LinkState = 4
)

// ExpireLink flips a link's state to EXPIRED so the redirect service
// stops forwarding on /r/{slug}. Historical click data stays intact.
// Used by the publish handler on unpublish and by the liveness handler
// when the destination URL fails probing.
func (c *RedirectClient) ExpireLink(ctx context.Context, linkID string) error {
	return c.updateLinkState(ctx, linkID, LinkStateExpired)
}

func (c *RedirectClient) updateLinkState(ctx context.Context, linkID string, state LinkState) error {
	if strings.TrimSpace(linkID) == "" {
		return fmt.Errorf("redirect: linkID is required")
	}
	reqBody := map[string]any{
		"id":    linkID,
		"state": int(state),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("redirect: marshal update: %w", err)
	}

	url := c.baseURL + "/redirect.v1.RedirectService/UpdateLink"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("redirect: create update request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("redirect: do update: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("redirect: update status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
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
