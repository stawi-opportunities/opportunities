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

// classifyATS recognises hosted-ATS job-board URLs and returns the engine
// type plus the canonical BaseURL including the company segment. ok=false
// means "not a known ATS" and the caller should fall back to the generic
// HTML flow (whose BaseURL is scheme://host only).
//
// Lever/Greenhouse company boards use generic_html + recipe (no dedicated
// engine). SmartRecruiters uses the smartrecruiters_api engine.
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
		return withCompany(domain.SourceGenericHTML)
	case "boards.greenhouse.io", "job-boards.greenhouse.io", "job-boards.eu.greenhouse.io":
		return withCompany(domain.SourceGenericHTML)
	case "careers.smartrecruiters.com", "jobs.smartrecruiters.com":
		return withCompany(domain.SourceSmartRecruitersAPI)
	}
	return "", "", false
}
