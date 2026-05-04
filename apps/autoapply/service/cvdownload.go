package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// ErrCVUnsafeURL signals a CV URL that failed the SSRF allowlist
// (non-https scheme, private/loopback host, etc.). Callers persist a
// failed application and ack the message — there is no point retrying
// a URL the candidate gave us.
var ErrCVUnsafeURL = errors.New("cvdownload: unsafe URL")

// CVFetcher downloads candidate CV bytes from a signed URL with SSRF
// guards and a body-size cap. The implementation is the small concrete
// httpCVFetcher below; the interface lets tests inject a fake without a
// live HTTP server.
type CVFetcher interface {
	Fetch(ctx context.Context, rawURL string) (data []byte, filename string, err error)
}

// NewHTTPCVFetcher constructs a fetcher that uses a stdlib http.Client
// with a per-request timeout and the given byte cap.
func NewHTTPCVFetcher(timeout time.Duration, maxBytes int64) CVFetcher {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	return &httpCVFetcher{
		client:   &http.Client{Timeout: timeout},
		maxBytes: maxBytes,
	}
}

type httpCVFetcher struct {
	client   *http.Client
	maxBytes int64
}

// Fetch validates rawURL, downloads the body up to the cap, and derives
// a safe filename. Returns ErrCVUnsafeURL when the URL fails policy;
// other errors are transient.
func (f *httpCVFetcher) Fetch(ctx context.Context, rawURL string) ([]byte, string, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, "", nil // not an error — no CV is fine
	}
	if err := validateCVURL(rawURL); err != nil {
		telemetry.RecordAutoApplyCVDownloadFailure("blocked_url")
		return nil, "", fmt.Errorf("%w: %v", ErrCVUnsafeURL, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		telemetry.RecordAutoApplyCVDownloadFailure("bad_request")
		return nil, "", fmt.Errorf("cv: build request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		telemetry.RecordAutoApplyCVDownloadFailure("network")
		return nil, "", fmt.Errorf("cv: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		telemetry.RecordAutoApplyCVDownloadFailure("http_status")
		return nil, "", fmt.Errorf("cv: http %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, f.maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		telemetry.RecordAutoApplyCVDownloadFailure("network")
		return nil, "", fmt.Errorf("cv: read: %w", err)
	}
	if int64(len(data)) > f.maxBytes {
		telemetry.RecordAutoApplyCVDownloadFailure("too_large")
		return nil, "", fmt.Errorf("cv: body exceeds %d bytes", f.maxBytes)
	}

	return data, deriveFilename(resp, rawURL), nil
}

// validateCVURL enforces an https-only allowlist and rejects URLs that
// resolve to private / loopback / link-local hosts (SSRF defence).
func validateCVURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("scheme must be https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("missing host")
	}
	// Empty-by-default allowlist of well-known cloud-storage hosts is
	// future work; for now we just deny anything that resolves only to
	// non-public addresses.
	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns: %w", err)
	}
	for _, ip := range addrs {
		if isPublicIP(ip) {
			return nil
		}
	}
	return fmt.Errorf("host %q resolves only to non-public addresses", host)
}

// isPublicIP returns true for routable, non-private, non-loopback,
// non-link-local addresses. We accept the URL as long as at least one
// returned address is public; refusing entirely on a single private
// reply would break dual-stack hosts in some clouds.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// Reject the AWS / GCE metadata address explicitly even if a future
	// stdlib classifies it differently.
	if ip.Equal(net.IPv4(169, 254, 169, 254)) {
		return false
	}
	return true
}

// deriveFilename pulls a safe filename from Content-Disposition or, as
// a fallback, the URL path. The result is sanitised so it can be used
// as both an attachment name and a temp-file pattern.
func deriveFilename(resp *http.Response, rawURL string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mimeParse(cd); err == nil {
			if name := params["filename"]; name != "" {
				return sanitise(name)
			}
		}
	}
	if u, err := url.Parse(rawURL); err == nil {
		base := path.Base(u.Path)
		if base != "" && base != "/" && base != "." && strings.Contains(base, ".") {
			return sanitise(base)
		}
	}
	return "resume.pdf"
}

// mimeParse is a thin wrapper around mime.ParseMediaType so the call
// site is one line. Indirected for unit testability.
func mimeParse(v string) (string, map[string]string, error) {
	return mimeParseImpl(v)
}

// sanitise reduces name to [A-Za-z0-9._-] and clamps to 64 chars. Both
// POSIX and Windows-style separators are stripped before basename so a
// crafted Content-Disposition can't write outside the temp dir.
func sanitise(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" || name == "" {
		return "resume.pdf"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "resume.pdf"
	}
	return b.String()
}
