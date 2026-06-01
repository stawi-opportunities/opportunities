// Package sitemap implements the declarative sitemap connector type
// for pkg/connectors/spec. It walks a sitemap.xml (optionally
// recursing through <sitemapindex> entries), filters URLs by
// include/exclude patterns, and either emits URL-only stubs or fetches
// each candidate page and runs a detail-fallback impl (defaulting to
// schemaorgjsonld) over the body.
package sitemap

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec/schemaorgjsonld"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// maxSitemapDepth caps recursion into <sitemapindex> chains to avoid
// runaway crawls on misconfigured publishers.
const maxSitemapDepth = 3

func init() { spec.Register(spec.TypeSitemap, &impl{}) }

type impl struct{}

// XML shapes — kept private to this package; the sitemap protocol is
// stable enough that we don't share with pkg/connectors/sitemapcrawler.
type sitemapIndex struct {
	XMLName  xml.Name      `xml:"sitemapindex"`
	Sitemaps []sitemapLoc  `xml:"sitemap"`
}

type sitemapLoc struct {
	Loc string `xml:"loc"`
}

type urlSet struct {
	XMLName xml.Name `xml:"urlset"`
	URLs    []urlLoc `xml:"url"`
}

type urlLoc struct {
	Loc string `xml:"loc"`
}

// Crawl returns a single-shot iterator that walks the sitemap, filters
// candidate URLs, and (optionally) fetches each one to extract a
// JobPosting via the configured detail-fallback impl.
func (impl) Crawl(ctx context.Context, src domain.Source, client *httpx.Client, s *spec.ConnectorSpec) connectors.CrawlIterator {
	opts := s.Sitemap
	if opts == nil {
		opts = &spec.SitemapOptions{}
	}

	includes, err := compilePatterns(opts.IncludePatterns)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, nil, 0, fmt.Errorf("sitemap include_patterns: %w", err))
	}
	excludes, err := compilePatterns(opts.ExcludePatterns)
	if err != nil {
		return connectors.NewSinglePageIterator(nil, nil, 0, fmt.Errorf("sitemap exclude_patterns: %w", err))
	}

	urls, rawBody, status, fetchErr := walkSitemap(ctx, client, s.ListURL, s.Headers, opts.FollowIndex, 0)
	if fetchErr != nil {
		return connectors.NewSinglePageIterator(nil, rawBody, status, fetchErr)
	}

	filtered := make([]string, 0, len(urls))
	for _, u := range urls {
		if !matches(u, includes) {
			continue
		}
		if len(excludes) > 0 && matchesAny(u, excludes) {
			continue
		}
		filtered = append(filtered, u)
	}

	items := make([]domain.ExternalOpportunity, 0, len(filtered))
	// Frontier-enabled sources short-circuit the detail fetch
	// entirely: the connector's job in D2 is URL discovery, and
	// the per-URL fetch + extract happens in apps/frontier-worker
	// under per-host politeness. The legacy path (FrontierEnabled
	// = false) keeps the existing detail-fetch behaviour intact so
	// sources that haven't opted in see zero change in output.
	if opts.DetailFetch && !src.FrontierEnabled {
		fallback := opts.DetailFallbackType
		if fallback == "" {
			fallback = string(spec.TypeSchemaOrgJSONLD)
		}
		for _, u := range filtered {
			items = append(items, fetchDetail(ctx, client, src, u, fallback, s.Headers)...)
		}
	} else {
		for _, u := range filtered {
			items = append(items, domain.ExternalOpportunity{
				Source:    src.Type,
				SourceURL: u,
				ApplyURL:  u,
			})
		}
	}

	return &iter{items: items, rawBody: rawBody, httpStatus: status}
}

// fetchDetail GETs a single URL and runs the named fallback impl over
// the body. Currently only schemaorgjsonld is wired up — anything else
// emits a URL-only stub so callers can extend later without breaking
// existing specs.
func fetchDetail(ctx context.Context, client *httpx.Client, src domain.Source, url, fallback string, headers map[string]string) []domain.ExternalOpportunity {
	body, status, err := client.Get(ctx, url, headers)
	if err != nil || status != 200 {
		return []domain.ExternalOpportunity{{
			Source:    src.Type,
			SourceURL: url,
			ApplyURL:  url,
		}}
	}

	switch fallback {
	case string(spec.TypeSchemaOrgJSONLD):
		postings := schemaorgjsonld.ExtractJobPostings(body)
		if len(postings) == 0 {
			return []domain.ExternalOpportunity{{
				Source:    src.Type,
				SourceURL: url,
				ApplyURL:  url,
			}}
		}
		out := make([]domain.ExternalOpportunity, 0, len(postings))
		for _, raw := range postings {
			opp, mapErr := schemaorgjsonld.MapJobPosting(raw)
			if mapErr != nil || opp == nil {
				continue
			}
			opp.Source = src.Type
			opp.SourceURL = url
			if opp.ApplyURL == "" {
				opp.ApplyURL = url
			}
			out = append(out, *opp)
		}
		return out
	default:
		// Unknown fallback — emit a URL-only stub.
		return []domain.ExternalOpportunity{{
			Source:    src.Type,
			SourceURL: url,
			ApplyURL:  url,
		}}
	}
}

// walkSitemap fetches a sitemap URL and returns every <loc> entry it
// can reach. When followIndex is true, <sitemapindex> bodies are
// recursed into (up to maxSitemapDepth levels). rawBody + status are
// the response of the first fetch — useful for surfacing the top-level
// HTTP context to callers.
func walkSitemap(ctx context.Context, client *httpx.Client, url string, headers map[string]string, followIndex bool, depth int) ([]string, []byte, int, error) {
	if depth > maxSitemapDepth {
		return nil, nil, 0, nil
	}

	body, status, err := client.Get(ctx, url, headers)
	if err != nil {
		return nil, body, status, fmt.Errorf("sitemap fetch %s: %w", url, err)
	}
	if status != 200 {
		return nil, body, status, fmt.Errorf("sitemap %s returned status %d", url, status)
	}

	// Try sitemapindex first.
	var idx sitemapIndex
	if err := xml.Unmarshal(body, &idx); err == nil && len(idx.Sitemaps) > 0 {
		if !followIndex {
			// Caller opted out — treat the index as zero URLs.
			return nil, body, status, nil
		}
		var all []string
		for _, sm := range idx.Sitemaps {
			if sm.Loc == "" {
				continue
			}
			sub, _, _, subErr := walkSitemap(ctx, client, sm.Loc, headers, followIndex, depth+1)
			if subErr != nil {
				// Sub-sitemap fetch failures shouldn't kill the whole crawl;
				// log via the error chain only if the entire walk yields nothing.
				continue
			}
			all = append(all, sub...)
		}
		return all, body, status, nil
	}

	// Fall back to urlset.
	var us urlSet
	if err := xml.Unmarshal(body, &us); err != nil {
		return nil, body, status, fmt.Errorf("sitemap unmarshal %s: %w", url, err)
	}
	urls := make([]string, 0, len(us.URLs))
	for _, u := range us.URLs {
		if u.Loc != "" {
			urls = append(urls, u.Loc)
		}
	}
	return urls, body, status, nil
}

// compilePatterns turns YAML include/exclude entries into a slice of
// matchers. A pattern prefixed with "re:" is treated as a regular
// expression; otherwise it is a case-sensitive substring match.
func compilePatterns(raw []string) ([]matcher, error) {
	out := make([]matcher, 0, len(raw))
	for _, p := range raw {
		if strings.HasPrefix(p, "re:") {
			re, err := regexp.Compile(p[3:])
			if err != nil {
				return nil, err
			}
			out = append(out, matcher{re: re})
		} else {
			out = append(out, matcher{sub: p})
		}
	}
	return out, nil
}

// matcher is a compiled include/exclude pattern. Substring matches are
// the common case; the regex variant is reserved for "re:" prefixed
// patterns.
type matcher struct {
	sub string
	re  *regexp.Regexp
}

// matches reports whether u satisfies at least one of the matchers.
// An empty matcher list is treated as "everything matches".
func matches(u string, list []matcher) bool {
	if len(list) == 0 {
		return true
	}
	return matchesAny(u, list)
}

// matchesAny is the unconditional any-match form. Used directly by
// exclude rules (where empty list means "exclude nothing").
func matchesAny(u string, list []matcher) bool {
	for _, m := range list {
		if m.re != nil {
			if m.re.MatchString(u) {
				return true
			}
		} else if strings.Contains(u, m.sub) {
			return true
		}
	}
	return false
}

// iter is a single-page iterator. Sitemap crawls don't paginate in the
// classical sense — the whole walk happens during Crawl().
type iter struct {
	items      []domain.ExternalOpportunity
	rawBody    []byte
	httpStatus int
	consumed   bool
}

// Next yields the full sitemap batch on the first call and false
// thereafter.
func (it *iter) Next(_ context.Context) bool {
	if it.consumed {
		return false
	}
	it.consumed = true
	return true
}

// Items returns every (post-filter, post-detail-fetch) opportunity.
func (it *iter) Items() []domain.ExternalOpportunity { return it.items }

// RawPayload returns the raw body of the top-level sitemap fetch.
func (it *iter) RawPayload() []byte { return it.rawBody }

// HTTPStatus returns the status code of the top-level sitemap fetch.
func (it *iter) HTTPStatus() int { return it.httpStatus }

// Err returns nil — fetch errors are surfaced via a
// SinglePageIterator constructed at Crawl time.
func (it *iter) Err() error { return nil }

// Cursor returns nil — sitemap crawls have no resumable cursor.
func (it *iter) Cursor() json.RawMessage { return nil }

// Content returns nil — per-item content is either absent (URL-only
// stubs) or populated directly by the detail-fallback impl.
func (it *iter) Content() *content.Extracted { return nil }
