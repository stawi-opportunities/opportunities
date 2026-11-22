package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrNonPublicAddress is returned when a crawl request would connect to a
// loopback, private, link-local (cloud metadata), CGNAT or otherwise
// non-public address.
var ErrNonPublicAddress = errors.New("httpx: refusing to connect to non-public address")

const maxPublicRedirects = 10

var (
	cgnatPrefix    = netip.MustParsePrefix("100.64.0.0/10")
	thisNetPrefix  = netip.MustParsePrefix("0.0.0.0/8")
	benchmarkRange = netip.MustParsePrefix("198.18.0.0/15")
)

// isPublicAddr reports whether addr is safe for the crawler to connect to.
func isPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	switch {
	case !addr.IsValid(),
		addr.IsLoopback(),
		addr.IsPrivate(),
		addr.IsLinkLocalUnicast(),
		addr.IsLinkLocalMulticast(),
		addr.IsInterfaceLocalMulticast(),
		addr.IsMulticast(),
		addr.IsUnspecified(),
		cgnatPrefix.Contains(addr),
		thisNetPrefix.Contains(addr),
		benchmarkRange.Contains(addr):
		return false
	}
	return true
}

// publicOnlyControl runs after DNS resolution, on the exact address being
// dialled, so DNS rebinding and redirects to internal hosts are covered.
func publicOnlyControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNonPublicAddress, address)
	}
	if !isPublicAddr(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrNonPublicAddress, ap.Addr())
	}
	return nil
}

// NewPublicClient returns an *http.Client for fetching third-party URLs
// (job boards, sitemaps, detail pages). Crawl targets come from scraped or
// source-supplied data, so the client only connects to public addresses,
// ignores proxy environment variables, follows at most 10 redirects and
// only over http/https.
func NewPublicClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   publicOnlyControl,
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxPublicRedirects {
				return fmt.Errorf("httpx: stopped after %d redirects", maxPublicRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("httpx: refusing redirect to scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
}
