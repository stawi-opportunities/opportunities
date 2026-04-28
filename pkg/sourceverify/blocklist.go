// Package sourceverify implements source-level fitness verification.
//
// A "source-level" check tests whether a source is suitable for crawling
// at all: its base URL parses, the host is not on the platform blocklist,
// every declared kind is in the registry, the URL is reachable, robots.txt
// allows our user-agent, and the connector + extractor produce at least
// one record that passes the per-record contract (pkg/opportunity.Verify).
//
// Per-record verification (does THIS extracted opportunity satisfy its
// kind contract?) is implemented in pkg/opportunity. The two are
// complementary and both must pass for a source to graduate from the
// pending → verified → active lifecycle.
package sourceverify

import (
	"net/url"
	"strings"
)

// BlockedHosts is the canonical list of hostnames the platform refuses to
// register as a crawl source — first-party stawi domains (so we don't
// recursively crawl ourselves), giant social/search sites that the
// crawler is not designed to scrape politely, and developer-host
// catch-alls (pages.dev, github.com).
//
// Mirrors apps/crawler/service/source_discovered_handler.go's local map
// before this package existed; centralising it here means the verifier
// and the discovery handler share one source of truth.
var BlockedHosts = map[string]bool{
	"google.com":              true,
	"facebook.com":            true,
	"twitter.com":             true,
	"instagram.com":           true,
	"youtube.com":             true,
	"wikipedia.org":           true,
	"linkedin.com":            true,
	"github.com":              true,
	"apple.com":               true,
	"play.google.com":         true,
	"apps.apple.com":          true,
	"stawi.org":               true,
	"opportunities.stawi.org": true,
	"jobs.stawi.org":          true, // CNAME alias of opportunities.stawi.org
	"pages.dev":               true,
}

// IsBlockedHost returns true if host (or any subdomain of a blocked
// suffix) is on the blocklist. host should already be lowercase and
// stripped of any "www." prefix; the helper is conservative and
// re-applies the lower/trim to be safe.
func IsBlockedHost(host string) bool {
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	if BlockedHosts[host] {
		return true
	}
	for blocked := range BlockedHosts {
		if strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}
	return false
}

// IsBlockedURL parses the URL and runs IsBlockedHost on the host. Returns
// true if the URL fails to parse — an unparseable URL is never safe to
// register as a source.
func IsBlockedURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return true
	}
	return IsBlockedHost(u.Hostname())
}
