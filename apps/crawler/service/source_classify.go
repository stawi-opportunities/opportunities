package service

import (
	"net/url"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// managedATSHosts are multi-tenant ATS platforms that a SINGLE aggregate source
// crawls in full (one `api` recipe with a tenant list — see
// docs/ops/crawl-framework.md). Discovering an individual board on one of these
// hosts must NOT mint a standalone per-company source: those have no connector
// (the connectors were migrated to recipes) and would error "connector not
// registered" forever. The aggregate already covers the whole platform, and the
// tenant list grows via cmd/ats-tenants / Common Crawl.
var managedATSHosts = map[string]bool{
	"jobs.lever.co":               true,
	"jobs.eu.lever.co":            true,
	"boards.greenhouse.io":        true,
	"job-boards.greenhouse.io":    true,
	"job-boards.eu.greenhouse.io": true,
	"jobs.ashbyhq.com":            true,
}

// isManagedATSHost reports whether host (already lowercased, www-stripped) is a
// multi-tenant ATS platform owned by an aggregate source.
func isManagedATSHost(host string) bool { return managedATSHosts[host] }

// classifyATS recognises hosted-ATS job-board URLs and returns the connector
// type that extracts them most effectively (structured API/page connectors —
// no LLM, no recipe) plus the canonical BaseURL including the company segment
// the connector needs. ok=false means "not a known ATS" and the caller should
// fall back to the generic-HTML flow (whose BaseURL is scheme://host only).
//
// This is the intake "research" step: a discovered jobs.lever.co/acme link
// must become a lever source for https://jobs.lever.co/acme — typing it
// generic_html would burn LLM/recipe effort on a board with a free JSON API.
func classifyATS(target *url.URL) (domain.SourceType, string, bool) {
	host := strings.ToLower(strings.TrimPrefix(target.Hostname(), "www."))
	segs := strings.Split(strings.Trim(target.EscapedPath(), "/"), "/")
	first := ""
	if len(segs) > 0 {
		first = segs[0]
	}

	withCompany := func(t domain.SourceType) (domain.SourceType, string, bool) {
		if first == "" {
			return "", "", false // ATS host without a company segment — nothing crawlable
		}
		return t, "https://" + host + "/" + first, true
	}

	switch host {
	case "jobs.lever.co", "jobs.eu.lever.co":
		return withCompany(domain.SourceLever)
	case "boards.greenhouse.io", "job-boards.greenhouse.io", "job-boards.eu.greenhouse.io":
		return withCompany(domain.SourceGreenhouse)
	case "careers.smartrecruiters.com", "jobs.smartrecruiters.com":
		return withCompany(domain.SourceSmartRecruitersPage)
	}
	return "", "", false
}
