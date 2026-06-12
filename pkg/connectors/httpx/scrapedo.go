package httpx

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// scrapeDoAPI is scrape.do's request endpoint. It fetches the target
// itself (rotating residential IPs, optional headless rendering) and
// returns the origin's body + status, so we hand it the target URL
// rather than tunnelling — it is an API, not a forward proxy.
const scrapeDoAPI = "https://api.scrape.do"

// ScrapeDoDoer is an unblocker backend that routes a request through
// scrape.do's API: GET https://api.scrape.do/?token=…&url=<target>.
// It implements HTTPDoer, so FallbackDoer can use it exactly like the
// Bright Data proxy doer — confined to requests the direct client
// couldn't fetch.
type ScrapeDoDoer struct {
	token   string
	render  bool   // run a headless browser (solves JS/Cloudflare challenges)
	super   bool   // "super" residential proxy (harder IP-reputation blocks)
	geoCode string // optional egress country, e.g. "us"
	http    HTTPDoer
}

// NewScrapeDoDoer builds the scrape.do backend. http is the underlying
// client used to call the API (owns the timeout); nil uses a default.
func NewScrapeDoDoer(token string, render, super bool, geoCode string, httpDoer HTTPDoer, timeout time.Duration) *ScrapeDoDoer {
	if httpDoer == nil {
		httpDoer = &http.Client{Timeout: timeout}
	}
	return &ScrapeDoDoer{token: token, render: render, super: super, geoCode: geoCode, http: httpDoer}
}

// Do issues GET https://api.scrape.do/?token=…&url=<target>&[render,super,geoCode].
// scrape.do passes the origin's status code and body straight through,
// so the returned *http.Response slots into FallbackDoer unchanged.
// Only the target URL is forwarded — scrape.do supplies its own browser
// identity, so the caller's emulated headers don't apply here.
func (d *ScrapeDoDoer) Do(req *http.Request) (*http.Response, error) {
	q := url.Values{}
	q.Set("token", d.token)
	q.Set("url", req.URL.String())
	if d.render {
		q.Set("render", "true")
	}
	if d.super {
		q.Set("super", "true")
	}
	if d.geoCode != "" {
		q.Set("geoCode", d.geoCode)
	}

	apiURL := scrapeDoAPI + "/?" + q.Encode()
	out, err := http.NewRequestWithContext(req.Context(), http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("scrape.do: build request: %w", err)
	}
	return d.http.Do(out)
}

// UnblockerConfig selects and configures an unblocker fallback backend.
// Zero value disables it (direct-only). scrape.do takes precedence over a
// generic proxy URL when both are set.
type UnblockerConfig struct {
	ScrapeDoToken   string
	ScrapeDoRender  bool
	ScrapeDoSuper   bool
	ScrapeDoGeoCode string

	ProxyURL    string // generic forward-proxy unblocker (Bright Data, Oxylabs…)
	ProxyCACert string

	Timeout time.Duration
}

// NewUnblocker returns the configured unblocker HTTPDoer (or nil when none
// is configured), a short human description for logging, and — for the
// proxy backend — whether TLS verification was disabled. base is reused
// as the scrape.do API client; the proxy backend builds its own.
func NewUnblocker(c UnblockerConfig, base HTTPDoer) (doer HTTPDoer, desc string, insecure bool, err error) {
	switch {
	case isBrightDataURL(c.ProxyURL):
		// Bright Data Web Unlocker API — strongest backend (Akamai/enterprise
		// anti-bot); takes precedence over scrape.do when configured.
		apiKey, zone, country, perr := parseBrightDataURL(c.ProxyURL)
		if perr != nil {
			return nil, "", false, perr
		}
		desc := "brightdata-unlocker zone=" + zone
		if country != "" {
			desc += " country=" + country
		}
		return NewBrightDataDoer(apiKey, zone, country, base, c.Timeout), desc, false, nil
	case c.ScrapeDoToken != "":
		opts := ""
		if c.ScrapeDoRender {
			opts += " render"
		}
		if c.ScrapeDoSuper {
			opts += " super"
		}
		return NewScrapeDoDoer(c.ScrapeDoToken, c.ScrapeDoRender, c.ScrapeDoSuper, c.ScrapeDoGeoCode, base, c.Timeout),
			"scrape.do" + opts, false, nil
	case c.ProxyURL != "":
		d, ins, perr := NewProxyDoer(c.ProxyURL, c.ProxyCACert, c.Timeout)
		if perr != nil {
			return nil, "", false, perr
		}
		return d, "proxy-unblocker insecure=" + strconv.FormatBool(ins), ins, nil
	default:
		return nil, "", false, nil
	}
}
