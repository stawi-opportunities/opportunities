package httpx

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/pitabwire/util"
)

// defaultBlockStatuses are the responses we treat as "the origin (or its
// WAF/CDN) refused this datacenter request" and worth retrying through the
// unblocker proxy. 403 is the common Cloudflare/Akamai block; 429/451/503 cover
// rate-limit and "under attack" interstitials.
var defaultBlockStatuses = map[int]bool{
	http.StatusForbidden:                  true,
	http.StatusTooManyRequests:            true,
	http.StatusUnavailableForLegalReasons: true,
	http.StatusServiceUnavailable:         true,
}

// FallbackDoer tries the direct client first and, only when the origin blocks
// the request (a block status or a transport error), retries the same request
// through an unblocker proxy (Bright Data Web Unlocker, Oxylabs Web Unblocker,
// etc.). This confines the paid unblocker to requests that actually need it —
// hosts that serve datacenter IPs fine (most JSON APIs) never incur its cost.
//
// It implements HTTPDoer, so it slots in beneath the retry/backoff Client
// transparently.
type FallbackDoer struct {
	direct    HTTPDoer
	unblocker HTTPDoer
	block     map[int]bool
}

// NewFallbackDoer wraps direct with an unblocker fallback. A nil unblocker makes
// it a direct-only pass-through, so callers can wire it unconditionally.
func NewFallbackDoer(direct, unblocker HTTPDoer) *FallbackDoer {
	if direct == nil {
		direct = http.DefaultClient
	}
	return &FallbackDoer{direct: direct, unblocker: unblocker, block: defaultBlockStatuses}
}

// Do runs the request directly, then falls back to the unblocker on a block.
func (f *FallbackDoer) Do(req *http.Request) (*http.Response, error) {
	// Buffer the body up front so the request can be replayed through the
	// unblocker (the direct attempt consumes a streaming body).
	body, err := bufferBody(req)
	if err != nil {
		return f.direct.Do(req)
	}

	resp, derr := f.direct.Do(cloneWithBody(req, body))
	if f.unblocker == nil {
		return resp, derr
	}
	if derr == nil && resp != nil && !f.block[resp.StatusCode] {
		return resp, nil // direct succeeded
	}

	// Blocked or errored — retry via the unblocker.
	if resp != nil {
		_ = resp.Body.Close()
	}
	uResp, uErr := f.unblocker.Do(cloneWithBody(req, body))
	if uErr != nil {
		// Unblocker also failed; surface the direct outcome if it was a real
		// response, otherwise the unblocker error.
		if derr == nil && resp != nil {
			return f.direct.Do(cloneWithBody(req, body))
		}
		return uResp, uErr
	}
	util.Log(req.Context()).WithField("url", req.URL.String()).WithField("via", "unblocker").
		Debug("httpx: direct request blocked, served via unblocker")
	return uResp, nil
}

func bufferBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	b, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	return b, err
}

func cloneWithBody(req *http.Request, body []byte) *http.Request {
	c := req.Clone(req.Context())
	if body != nil {
		c.Body = io.NopCloser(bytes.NewReader(body))
		c.ContentLength = int64(len(body))
	}
	return c
}

// NewProxyDoer builds an HTTPDoer that routes every request through an unblocker
// proxy URL, e.g. Bright Data Web Unlocker:
//
//	http://brd-customer-<id>-zone-<zone>:<password>@brd.superproxy.io:33335
//
// Unblocker proxies terminate and re-sign origin TLS to inspect/solve
// challenges. Supply the provider's CA (caCertPEM) so those re-signed certs are
// verified against it — the secure path. Only when no CA is pinned does the
// transport skip verification (caller must warn); the direct client is never
// affected either way. insecure reports whether verification was disabled.
func NewProxyDoer(proxyURL, caCertPEM string, timeout time.Duration) (doer HTTPDoer, insecure bool, err error) {
	u, perr := url.Parse(proxyURL)
	if perr != nil {
		return nil, false, fmt.Errorf("parse unblocker proxy url: %w", perr)
	}
	if u.Host == "" {
		return nil, false, fmt.Errorf("unblocker proxy url has no host: %q", proxyURL)
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caCertPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caCertPEM)) {
			return nil, false, fmt.Errorf("unblocker CA cert: no valid certificate in PEM")
		}
		tlsCfg.RootCAs = pool
	} else {
		// No CA pinned: the proxy presents a cert we can't chain to a trusted
		// root, so verification has to be skipped. Pin the provider CA via
		// UNBLOCKER_CA_CERT to remove this.
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // documented fallback; pin UNBLOCKER_CA_CERT to verify
		insecure = true
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: http.ProxyURL(u), TLSClientConfig: tlsCfg},
	}, insecure, nil
}
