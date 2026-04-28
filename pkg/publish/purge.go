package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// CachePurger issues Cloudflare cache purges for specific URLs. A zero-value
// configuration (empty zone or token) makes PurgeURL a silent no-op, which is
// what local development wants.
type CachePurger struct {
	zoneID  string
	token   string
	baseURL string
	client  HTTPClient
}

// NewCachePurger constructs a purger. Pass empty strings for a no-op.
// baseURL == "" means use Cloudflare's production API root.
// httpClient may be nil; production callers should pass
// svc.HTTPClientManager().Client(ctx) so OTEL trace propagation applies.
func NewCachePurger(zoneID, token, baseURL string, httpClient HTTPClient) *CachePurger {
	if baseURL == "" {
		baseURL = "https://api.cloudflare.com/client/v4"
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &CachePurger{
		zoneID:  zoneID,
		token:   token,
		baseURL: baseURL,
		client:  httpClient,
	}
}

// PurgeURL invalidates the edge cache entry for a single URL.
// If the purger is unconfigured, returns nil without making a request.
func (p *CachePurger) PurgeURL(ctx context.Context, url string) error {
	if p == nil || p.zoneID == "" || p.token == "" {
		return nil
	}
	body, _ := json.Marshal(map[string][]string{"files": {url}})
	u := fmt.Sprintf("%s/zones/%s/purge_cache", p.baseURL, p.zoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cf purge %s: status %d", url, resp.StatusCode)
	}
	return nil
}
