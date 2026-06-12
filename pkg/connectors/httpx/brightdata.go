package httpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// brightDataAPI is Bright Data's Web Unlocker request endpoint. Like scrape.do
// it fetches the target itself (solving anti-bot / Akamai / Cloudflare with a
// managed residential pool) and returns the origin's body, so we hand it the
// target URL rather than tunnelling.
const brightDataAPI = "https://api.brightdata.com/request"

// BrightDataScheme is the UNBLOCKER_PROXY_URL scheme that selects the Bright
// Data Web Unlocker *API* backend (vs a plain forward proxy). Format:
//
//	brightdata-api://<api_token>@<zone>[?country=xx]
//
// Reusing UNBLOCKER_PROXY_URL means no new secret/env wiring — the existing
// ExternalSecret mapping carries it.
const BrightDataScheme = "brightdata-api"

// BrightDataDoer is an unblocker backend that routes a request through Bright
// Data's Web Unlocker API: POST https://api.brightdata.com/request with the
// target URL + zone. It implements HTTPDoer, so FallbackDoer uses it exactly
// like the scrape.do backend — confined to requests the direct client couldn't
// fetch. Stronger than scrape.do on Akamai/enterprise anti-bot.
type BrightDataDoer struct {
	apiKey  string
	zone    string
	country string // optional egress country, e.g. "ae"
	http    HTTPDoer
}

// NewBrightDataDoer builds the Bright Data backend. httpDoer calls the API
// (owns the timeout); nil uses a default.
func NewBrightDataDoer(apiKey, zone, country string, httpDoer HTTPDoer, timeout time.Duration) *BrightDataDoer {
	if httpDoer == nil {
		httpDoer = &http.Client{Timeout: timeout}
	}
	return &BrightDataDoer{apiKey: apiKey, zone: zone, country: country, http: httpDoer}
}

// parseBrightDataURL parses brightdata-api://<token>@<zone>[?country=xx]. The
// token is the userinfo and the zone is the host so neither needs escaping.
func parseBrightDataURL(raw string) (apiKey, zone, country string, err error) {
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", "", "", fmt.Errorf("brightdata: parse url: %w", perr)
	}
	if u.User == nil || u.User.Username() == "" {
		return "", "", "", fmt.Errorf("brightdata: missing api token in %q (want brightdata-api://<token>@<zone>)", raw)
	}
	zone = u.Host
	if zone == "" {
		return "", "", "", fmt.Errorf("brightdata: missing zone in %q", raw)
	}
	return u.User.Username(), zone, u.Query().Get("country"), nil
}

// Do issues POST https://api.brightdata.com/request with {zone,url,method,
// format:"raw"[,country]}. Bright Data passes the origin's body through (and,
// with format=raw, the origin status), so the returned *http.Response slots
// into FallbackDoer. The caller's emulated headers don't apply — Bright Data
// supplies its own browser identity.
func (d *BrightDataDoer) Do(req *http.Request) (*http.Response, error) {
	payload := map[string]any{
		"zone":   d.zone,
		"url":    req.URL.String(),
		"method": req.Method,
		"format": "raw",
	}
	if d.country != "" {
		payload["country"] = d.country
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("brightdata: marshal payload: %w", err)
	}
	out, err := http.NewRequestWithContext(req.Context(), http.MethodPost, brightDataAPI, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("brightdata: build request: %w", err)
	}
	out.Header.Set("Authorization", "Bearer "+d.apiKey)
	out.Header.Set("Content-Type", "application/json")
	return d.http.Do(out)
}

// isBrightDataURL reports whether a proxy URL selects the Bright Data API backend.
func isBrightDataURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), BrightDataScheme+"://")
}
