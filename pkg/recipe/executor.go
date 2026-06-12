package recipe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/PaesslerAG/jsonpath"
	"github.com/PuerkitoBio/goquery"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Executor runs a recipe deterministically — no LLM. It is constructed per
// source with that source's active recipe and a Fetcher for HTTP.
type Executor struct {
	recipe  *Recipe
	fetcher Fetcher
}

// NewExecutor builds an Executor for a recipe + fetcher.
func NewExecutor(r *Recipe, f Fetcher) *Executor {
	return &Executor{recipe: r, fetcher: f}
}

// apiPage fetches one api-mode page from pageURL, parses the records under
// List.ItemsPath, and builds an opportunity per record. It returns the page's
// items, the raw body, the HTTP status, the root JSON (for cursor pagination),
// and any error.
func (e *Executor) apiPage(ctx context.Context, src domain.Source, pageURL string) (items []domain.ExternalOpportunity, raw []byte, status int, root any, err error) {
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
	for _, du := range detailURLs {
		if !sameHost(src.BaseURL, du) {
			continue
		}
		body, st, ferr := e.fetcher.Get(ctx, du)
		if ferr != nil {
			continue // skip a detail page we can't fetch; keep crawling
		}
		if st < 200 || st >= 300 {
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
	return items, raw, status, listPC, nil
}

// collectDetailURLs evaluates the recipe's Link extractor inside each
// ItemSelector match, returning resolved detail URLs.
func (e *Executor) collectDetailURLs(listPC *PageContext, listURL string) []string {
	if listPC.HTML == nil || e.recipe.List.ItemSelector == "" {
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
	switch e.recipe.Acquisition {
	case "api":
		return e.apiPaged(ctx, src, st)
	default:
		return e.htmlPaged(ctx, src, st)
	}
}

// ListProbe executes ONLY the recipe's list rule against the live source
// and reports how many items/detail links it finds — no detail-page
// fetches, no field extraction. This is the activation gate's defense
// against hallucinated list selectors: detail validation alone can score
// 1.00 while item_selector matches nothing (both first production
// recipes shipped that way and would have zero-yielded their boards).
func (e *Executor) ListProbe(ctx context.Context, src domain.Source) (int, error) {
	if e.recipe.Acquisition == "api" {
		pageURL, err := e.apiURL(src, 1, "")
		if err != nil {
			return 0, err
		}
		items, _, _, _, err := e.apiPage(ctx, src, pageURL)
		if err != nil {
			return 0, err
		}
		return len(items), nil
	}

	listURL, err := e.htmlListURL(src, 1)
	if err != nil {
		return 0, err
	}
	raw, status, err := e.fetcher.Get(ctx, listURL)
	if err != nil {
		return 0, err
	}
	if status < 200 || status >= 300 {
		return 0, fmt.Errorf("listing %s returned status %d", listURL, status)
	}
	pc, err := NewPageContext(listURL, string(raw), nil)
	if err != nil {
		return 0, err
	}
	urls := e.collectDetailURLs(pc, listURL)
	n := 0
	for _, u := range urls {
		if sameHost(src.BaseURL, u) {
			n++
		}
	}
	return n, nil
}

func (e *Executor) apiPaged(ctx context.Context, src domain.Source, st PageState) ([]domain.ExternalOpportunity, []byte, int, PageState, bool, error) {
	pg := st.page
	if pg == 0 {
		pg = 1
	}
	pageURL, err := e.apiURL(src, pg, st.cursor)
	if err != nil {
		return nil, nil, 0, PageState{}, true, err
	}
	items, raw, status, root, err := e.apiPage(ctx, src, pageURL)
	if err != nil {
		return nil, raw, status, PageState{}, true, err
	}

	p := e.recipe.List.Pagination
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
