package recipe

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PaesslerAG/jsonpath"
	"github.com/PuerkitoBio/goquery"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// Executor runs a recipe deterministically — no LLM. It is constructed per
// source with that source's active recipe and a Fetcher for HTTP.
type Executor struct {
	recipe  *Recipe
	fetcher Fetcher

	// sitemap list-mode caches the enumerated job URLs so the sitemap
	// (and its index) is fetched once per crawl, not once per page.
	sitemapURLs   []string
	sitemapLoaded bool
}

// NewExecutor builds an Executor for a recipe + fetcher.
func NewExecutor(r *Recipe, f Fetcher) *Executor {
	return &Executor{recipe: r, fetcher: f}
}

// apiPage fetches one api-mode page from pageURL, parses the records under
// List.ItemsPath, and builds an opportunity per record. It returns the page's
// items, the raw body, the HTTP status, the root JSON (for cursor pagination),
// and any error.
func (e *Executor) apiPage(ctx context.Context, src domain.Source, pageURL, tenant string) (items []domain.ExternalOpportunity, raw []byte, status int, root any, err error) {
	raw, status, err = e.fetcher.Get(ctx, pageURL)
	if err != nil {
		return nil, raw, status, nil, err
	}
	if status < 200 || status >= 300 {
		return nil, raw, status, nil, fmt.Errorf("api page %s returned status %d", pageURL, status)
	}
	if err = json.Unmarshal(raw, &root); err != nil {
		return nil, raw, status, nil, fmt.Errorf("api page %s: invalid JSON: %w", pageURL, err)
	}
	recsVal, err := jsonpath.Get(e.recipe.List.ItemsPath, root)
	if err != nil {
		return nil, raw, status, root, fmt.Errorf("api page %s: items_path %q: %w", pageURL, e.recipe.List.ItemsPath, err)
	}
	recs, ok := recsVal.([]any)
	if !ok {
		return nil, raw, status, root, nil
	}
	for _, rv := range recs {
		rec, ok := rv.(map[string]any)
		if !ok {
			continue
		}
		pc, perr := NewPageContext(pageURL, "", rec)
		if perr != nil {
			return nil, raw, status, root, perr
		}
		pc.Tenant = tenant // current board token, for {"from":["tenant"]}
		opp, berr := buildOpportunity(pc, src, e.recipe)
		if berr != nil {
			return nil, raw, status, root, berr
		}
		items = append(items, opp)
	}
	return items, raw, status, root, nil
}

// htmlPage fetches one listing page, enumerates detail URLs via the recipe's
// ItemSelector + Link, fetches each same-host detail page, and builds an
// opportunity from it. Cross-host detail links are skipped (SSRF guard).
// It returns the items, the listing's raw body, its HTTP status, the listing
// PageContext (for next-link pagination), and any error.
func (e *Executor) htmlPage(ctx context.Context, src domain.Source, listURL string) (items []domain.ExternalOpportunity, raw []byte, status int, listPC *PageContext, err error) {
	raw, status, err = e.fetcher.Get(ctx, listURL)
	if err != nil {
		return nil, raw, status, nil, err
	}
	if status < 200 || status >= 300 {
		return nil, raw, status, nil, fmt.Errorf("listing %s returned status %d", listURL, status)
	}
	listPC, err = NewPageContext(listURL, string(raw), nil)
	if err != nil {
		return nil, raw, status, nil, err
	}

	detailURLs := e.collectDetailURLs(listPC, listURL)
	skipped := 0
	for _, du := range detailURLs {
		if !sameHost(src.BaseURL, du) {
			continue
		}
		body, st, ferr := e.fetcher.Get(ctx, du)
		if ferr != nil {
			skipped++
			telemetry.RecordCrawlSilentLoss("detail_fetch_skip")
			continue // skip a detail page we can't fetch; keep crawling
		}
		if st < 200 || st >= 300 {
			skipped++
			telemetry.RecordCrawlSilentLoss("detail_fetch_skip")
			continue
		}
		pc, perr := NewPageContext(du, string(body), nil)
		if perr != nil {
			return items, raw, status, listPC, perr
		}
		opp, berr := buildOpportunity(pc, src, e.recipe)
		if berr != nil {
			return items, raw, status, listPC, berr
		}
		items = append(items, opp)
	}
	if skipped > 0 {
		// A listing whose detail pages are unreachable yields fewer jobs
		// while reporting success — make the degradation visible.
		util.Log(ctx).WithField("listing", listURL).
			WithField("skipped", skipped).
			WithField("fetched", len(items)).
			Warn("recipe: detail pages skipped (fetch error or non-2xx)")
	}
	return items, raw, status, listPC, nil
}

// collectDetailURLs returns the job-detail URLs on a listing page. The
// reusable path (preferred) is list.link_pattern: harvest every anchor
// whose resolved href contains the pattern. The legacy path evaluates
// the Link extractor inside each ItemSelector match.
func (e *Executor) collectDetailURLs(listPC *PageContext, listURL string) []string {
	if listPC.HTML == nil {
		return nil
	}

	// link_pattern: any anchor under the job URL path. No per-site CSS —
	// the URL contract ("/listings/", "/job/", …) is the stable signal.
	if pat := e.recipe.List.LinkPattern; pat != "" {
		var urls []string
		seen := map[string]bool{}
		listPC.HTML.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			abs, err := resolveURL(listURL, href)
			if err != nil {
				return
			}
			u := abs.String()
			if strings.Contains(u, pat) && !seen[u] {
				seen[u] = true
				urls = append(urls, u)
			}
		})
		return urls
	}

	if e.recipe.List.ItemSelector == "" {
		return nil
	}
	var urls []string
	listPC.HTML.Find(e.recipe.List.ItemSelector).Each(func(_ int, item *goquery.Selection) {
		outer, oerr := goquery.OuterHtml(item)
		if oerr != nil {
			return
		}
		itemPC, perr := NewPageContext(listURL, outer, nil)
		if perr != nil {
			return
		}
		// Resolve every link against the listing URL so a relative href
		// ("/job/123") becomes absolute regardless of whether the recipe
		// author remembered the absolute_url transform — otherwise the
		// downstream same-host check drops it and the board silently
		// yields zero detail URLs.
		if link, _ := Evaluate(e.recipe.List.Link, itemPC); link != "" {
			if abs, err := resolveURL(listURL, link); err == nil {
				urls = append(urls, abs.String())
			}
		}
	})
	return urls
}

// sameHost reports whether target shares base's host AND uses an http(s)
// scheme (SSRF guard). A target that fails to parse, has a non-http scheme, or
// a base without a host, is treated as not-same-host.
func sameHost(base, target string) bool {
	b, err := url.Parse(base)
	if err != nil || b.Host == "" {
		return false
	}
	t, err := url.Parse(target)
	if err != nil {
		return false
	}
	if t.Scheme != "http" && t.Scheme != "https" {
		return false
	}
	return strings.EqualFold(b.Host, t.Host)
}

// PageState carries pagination position between Page calls. The zero value
// means "start at the beginning".
type PageState struct {
	url    string
	page   int
	cursor string
}

// Page fetches and extracts ONE page (one api response, or one listing page
// plus its detail pages) and computes the next PageState. done is true when
// there are no more pages. This is the unit the CrawlIterator adapter drives.
func (e *Executor) Page(ctx context.Context, src domain.Source, st PageState) (items []domain.ExternalOpportunity, raw []byte, status int, next PageState, done bool, err error) {
	switch {
	case e.recipe.Acquisition == "api":
		return e.apiPaged(ctx, src, st)
	case e.recipe.List.Mode == "sitemap":
		return e.sitemapPaged(ctx, src, st)
	default:
		return e.htmlPaged(ctx, src, st)
	}
}

// sitemapBatch is how many detail pages one sitemap page fetches+extracts.
const sitemapBatch = 25

// sitemapPaged crawls a board whose sitemap enumerates every job URL —
// the most complete + efficient method when available (no listing
// pagination, no JS). It loads all job URLs from list.endpoint (a
// sitemap or sitemap-index URL; auto-discovered from robots.txt when
// empty), filtered by list.link_pattern, then extracts them in batches
// across pages, capped by list.pagination.max_pages.
func (e *Executor) sitemapPaged(ctx context.Context, src domain.Source, st PageState) ([]domain.ExternalOpportunity, []byte, int, PageState, bool, error) {
	if !e.sitemapLoaded {
		urls, err := e.loadSitemapURLs(ctx, src)
		if err != nil {
			return nil, nil, 0, PageState{}, true, err
		}
		e.sitemapURLs = urls
		e.sitemapLoaded = true
	}
	start := st.page * sitemapBatch
	if start >= len(e.sitemapURLs) {
		return nil, nil, 200, PageState{}, true, nil
	}
	end := min(start+sitemapBatch, len(e.sitemapURLs))

	var items []domain.ExternalOpportunity
	for _, u := range e.sitemapURLs[start:end] {
		body, status, ferr := e.fetcher.Get(ctx, u)
		if ferr != nil || status < 200 || status >= 300 {
			continue
		}
		pc, perr := NewPageContext(u, string(body), nil)
		if perr != nil {
			continue
		}
		if opp, berr := buildOpportunity(pc, src, e.recipe); berr == nil {
			items = append(items, opp)
		}
	}
	nextPage := st.page + 1
	maxPages := e.recipe.List.Pagination.MaxPages
	done := end >= len(e.sitemapURLs) || (maxPages > 0 && nextPage >= maxPages)
	return items, nil, 200, PageState{page: nextPage}, done, nil
}

// loadSitemapURLs fetches the sitemap (or sitemap index, recursing one
// level into sub-sitemaps) and returns the same-host job-detail URLs
// matching list.link_pattern. list.endpoint is the sitemap URL; when
// empty it is auto-discovered from robots.txt, then /sitemap.xml.
func (e *Executor) loadSitemapURLs(ctx context.Context, src domain.Source) ([]string, error) {
	roots := e.sitemapRoots(ctx, src)
	pat := e.recipe.List.LinkPattern
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		if u == "" || seen[u] || !sameHost(src.BaseURL, u) {
			return
		}
		if pat != "" && !strings.Contains(u, pat) {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	// One level of sitemap-index recursion: a <loc> that is itself a
	// sitemap (.xml/.xml.gz, or contains "sitemap") is fetched and its
	// <loc>s collected; everything else is a candidate job URL.
	for _, root := range roots {
		locs := e.fetchLocs(ctx, root)
		for _, loc := range locs {
			if isSitemapURL(loc) {
				for _, sub := range e.fetchLocs(ctx, loc) {
					add(sub)
				}
				continue
			}
			add(loc)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("sitemap: no job URLs matching %q under %v", pat, roots)
	}
	return out, nil
}

func (e *Executor) sitemapRoots(ctx context.Context, src domain.Source) []string {
	if e.recipe.List.Endpoint != "" {
		if u, err := resolveURL(src.BaseURL, e.recipe.List.Endpoint); err == nil {
			return []string{u.String()}
		}
	}
	// Discover from robots.txt.
	var roots []string
	if u, err := resolveURL(src.BaseURL, "/robots.txt"); err == nil {
		if body, status, ferr := e.fetcher.Get(ctx, u.String()); ferr == nil && status == 200 {
			for _, line := range strings.Split(string(body), "\n") {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "sitemap:") {
					roots = append(roots, strings.TrimSpace(line[strings.Index(line, ":")+1:]))
				}
			}
		}
	}
	if len(roots) == 0 {
		if u, err := resolveURL(src.BaseURL, "/sitemap.xml"); err == nil {
			roots = []string{u.String()}
		}
	}
	return roots
}

// fetchLocs fetches a sitemap (gunzipping .gz) and returns its <loc> values.
func (e *Executor) fetchLocs(ctx context.Context, sitemapURL string) []string {
	body, status, err := e.fetcher.Get(ctx, sitemapURL)
	if err != nil || status != 200 || len(body) == 0 {
		return nil
	}
	if strings.HasSuffix(sitemapURL, ".gz") {
		if zr, zerr := gzip.NewReader(bytes.NewReader(body)); zerr == nil {
			if un, rerr := io.ReadAll(io.LimitReader(zr, 64*1024*1024)); rerr == nil {
				body = un
			}
			_ = zr.Close()
		}
	}
	var out []string
	for _, m := range locRe.FindAllStringSubmatch(string(body), -1) {
		out = append(out, strings.TrimSpace(html.UnescapeString(m[1])))
	}
	return out
}

var locRe = regexp.MustCompile(`(?s)<loc>\s*(.*?)\s*</loc>`)

func isSitemapURL(u string) bool {
	l := strings.ToLower(u)
	return strings.HasSuffix(l, ".xml") || strings.HasSuffix(l, ".xml.gz") || strings.Contains(l, "sitemap")
}

// ListProbe executes ONLY the recipe's list rule against the live source
// and reports how many items/detail links it finds — no detail-page
// fetches, no field extraction. This is the activation gate's defense
// against hallucinated list selectors: detail validation alone can score
// 1.00 while item_selector matches nothing (both first production
// recipes shipped that way and would have zero-yielded their boards).
func (e *Executor) ListProbe(ctx context.Context, src domain.Source) (int, error) {
	if e.recipe.Acquisition == "api" {
		// Probe the first tenant for a multi-tenant source, else the single endpoint.
		pageURL, tenant := "", ""
		if t := e.recipe.List.Tenants; len(t) > 0 {
			tenant = t[0]
			pageURL = strings.ReplaceAll(e.recipe.List.Endpoint, "{tenant}", tenant)
			if u, uerr := resolveURL(src.BaseURL, pageURL); uerr == nil {
				pageURL = u.String()
			}
		} else {
			var err error
			if pageURL, err = e.apiURL(src, 1, ""); err != nil {
				return 0, err
			}
		}
		items, _, _, _, err := e.apiPage(ctx, src, pageURL, tenant)
		if err != nil {
			return 0, err
		}
		return len(items), nil
	}
	urls, err := e.ListDetailURLs(ctx, src)
	return len(urls), err
}

// resetSitemapCache lets a verifier re-run ListDetailURLs and a crawl on
// the same Executor without the first call's cache masking the second.
func (e *Executor) resetSitemapCache() { e.sitemapLoaded, e.sitemapURLs = false, nil }

// ListDetailURLs runs the recipe's list rule against the live listing
// (HTML mode) and returns the same-host detail URLs it finds — the real
// job pages, derived from the recipe itself. Verification and sample
// selection use this instead of generic link discovery, so the pages
// validated are exactly the ones the recipe will crawl (not advice or
// category pages a pattern matcher might surface).
func (e *Executor) ListDetailURLs(ctx context.Context, src domain.Source) ([]string, error) {
	if e.recipe.List.Mode == "sitemap" {
		return e.loadSitemapURLs(ctx, src)
	}
	listURL, err := e.htmlListURL(src, 1)
	if err != nil {
		return nil, err
	}
	raw, status, err := e.fetcher.Get(ctx, listURL)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("listing %s returned status %d", listURL, status)
	}
	pc, err := NewPageContext(listURL, string(raw), nil)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, u := range e.collectDetailURLs(pc, listURL) {
		if sameHost(src.BaseURL, u) {
			out = append(out, u)
		}
	}
	return out, nil
}

func (e *Executor) apiPaged(ctx context.Context, src domain.Source, st PageState) ([]domain.ExternalOpportunity, []byte, int, PageState, bool, error) {
	pg := st.page
	if pg == 0 {
		pg = 1
	}

	// Multi-tenant: one page == one tenant. Crawl every board token in
	// turn, skipping dead ones (a 404 board doesn't fail the whole
	// platform source), until the tenant list is exhausted.
	if tenants := e.recipe.List.Tenants; len(tenants) > 0 {
		if pg > len(tenants) {
			return nil, nil, 200, PageState{}, true, nil
		}
		tenant := tenants[pg-1]
		pageURL := strings.ReplaceAll(e.recipe.List.Endpoint, "{tenant}", tenant)
		if u, uerr := resolveURL(src.BaseURL, pageURL); uerr == nil {
			pageURL = u.String()
		}
		items, raw, status, _, perr := e.apiPage(ctx, src, pageURL, tenant)
		done := pg >= len(tenants)
		if perr != nil {
			util.Log(ctx).WithError(perr).WithField("tenant", tenant).
				Warn("recipe.api: tenant failed; skipping")
			return nil, raw, status, PageState{page: pg + 1}, done, nil
		}
		return items, raw, status, PageState{page: pg + 1}, done, nil
	}

	p := e.recipe.List.Pagination
	// Politeness pacing between pages of a deep paginated API.
	if pg > 1 && p.DelayMs > 0 {
		select {
		case <-ctx.Done():
			return nil, nil, 0, PageState{}, true, ctx.Err()
		case <-time.After(time.Duration(p.DelayMs) * time.Millisecond):
		}
	}

	pageURL, err := e.apiURL(src, pg, st.cursor)
	if err != nil {
		return nil, nil, 0, PageState{}, true, err
	}
	items, raw, status, root, err := e.apiPage(ctx, src, pageURL, "")
	if err != nil {
		// A 429 deep into pagination means "enough for now" — end cleanly
		// (we already have the earlier pages). Only page 1 is a real fail.
		if pg > 1 && status == 429 {
			return nil, raw, status, PageState{}, true, nil
		}
		return nil, raw, status, PageState{}, true, err
	}

	maxPages := p.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}
	switch p.Mode {
	case "page_param":
		if len(items) == 0 || pg >= maxPages {
			return items, raw, status, PageState{}, true, nil
		}
		return items, raw, status, PageState{page: pg + 1}, false, nil
	case "cursor":
		cur := ""
		if !p.Cursor.empty() && root != nil {
			cur = jsonPathScalar(p.Cursor.JSONPath, root)
		}
		// Stop on: no cursor, no progress (cursor didn't advance), empty page, or cap.
		if cur == "" || cur == st.cursor || len(items) == 0 || pg >= maxPages {
			return items, raw, status, PageState{}, true, nil
		}
		return items, raw, status, PageState{page: pg + 1, cursor: cur}, false, nil
	default:
		return items, raw, status, PageState{}, true, nil
	}
}

func (e *Executor) htmlPaged(ctx context.Context, src domain.Source, st PageState) ([]domain.ExternalOpportunity, []byte, int, PageState, bool, error) {
	pg := st.page
	if pg == 0 {
		pg = 1
	}
	listURL := st.url
	if listURL == "" {
		var err error
		if listURL, err = e.htmlListURL(src, pg); err != nil {
			return nil, nil, 0, PageState{}, true, err
		}
	}
	items, raw, status, listPC, err := e.htmlPage(ctx, src, listURL)
	if err != nil {
		return nil, raw, status, PageState{}, true, err
	}

	p := e.recipe.List.Pagination
	maxPages := p.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}
	switch p.Mode {
	case "next_link":
		nextURL := ""
		if listPC != nil && !p.Next.empty() {
			nextURL, _ = Evaluate(p.Next, listPC)
		}
		if nextURL == "" || pg >= maxPages || !sameHost(src.BaseURL, nextURL) {
			return items, raw, status, PageState{}, true, nil
		}
		return items, raw, status, PageState{url: nextURL, page: pg + 1}, false, nil
	case "page_param":
		if len(items) == 0 || pg >= maxPages {
			return items, raw, status, PageState{}, true, nil
		}
		nextURL, err := e.htmlListURL(src, pg+1)
		if err != nil {
			return items, raw, status, PageState{}, true, nil
		}
		return items, raw, status, PageState{url: nextURL, page: pg + 1}, false, nil
	default:
		return items, raw, status, PageState{}, true, nil
	}
}

// apiURL builds the endpoint URL for an api page, applying static params plus
// the page_param/cursor for pagination.
func (e *Executor) apiURL(src domain.Source, page int, cursor string) (string, error) {
	u, err := resolveURL(src.BaseURL, e.recipe.List.Endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range e.recipe.List.Params {
		q.Set(k, v)
	}
	p := e.recipe.List.Pagination
	if p.Mode == "page_param" && p.Param != "" {
		q.Set(p.Param, strconv.Itoa(page))
	}
	if p.Mode == "cursor" && p.Param != "" && cursor != "" {
		q.Set(p.Param, cursor)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// htmlListURL builds the listing URL for an html page. For page_param it sets
// the page query param on the base URL; otherwise it uses the base URL as-is.
func (e *Executor) htmlListURL(src domain.Source, page int) (string, error) {
	u, err := resolveURL(src.BaseURL, e.recipe.List.Endpoint)
	if err != nil {
		return "", err
	}
	p := e.recipe.List.Pagination
	if p.Mode == "page_param" && p.Param != "" {
		q := u.Query()
		q.Set(p.Param, strconv.Itoa(page))
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// resolveURL joins a possibly-relative ref against base. An empty ref yields base.
func resolveURL(base, ref string) (*url.URL, error) {
	b, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		return b, nil
	}
	r, err := url.Parse(ref)
	if err != nil {
		return nil, err
	}
	return b.ResolveReference(r), nil
}
