package recipe

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// detailPathHint matches path segments that job boards use for detail pages.
var detailPathHint = regexp.MustCompile(`(?i)/(job|jobs|vacanc|position|listing|career|opening|ad)s?[/-]`)

// idishTail matches slugs/ids detail URLs end with (boards/123, x-job-k7mqn5…).
var idishTail = regexp.MustCompile(`(?i)([0-9]{3,}|[a-z0-9]{5,}-[a-z0-9-]{4,})/?$`)

// DiscoverDetailURLs fetches a listing page and harvests up to n same-host
// links that look like opportunity DETAIL pages. Recipe validation dry-runs
// detail extraction per sample, so a listing page alone can never pass — this
// turns a bare BaseURL into validatable samples for sources with no recorded
// crawl history yet.
func DiscoverDetailURLs(ctx context.Context, f Fetcher, listingURL string, n int) ([]string, error) {
	if n <= 0 {
		n = 2
	}
	body, status, err := f.Get(ctx, listingURL)
	if err != nil || status != 200 {
		return nil, fmt.Errorf("discover samples: fetch %s: status=%d err=%v", listingURL, status, err)
	}
	base, err := url.Parse(listingURL)
	if err != nil {
		return nil, fmt.Errorf("discover samples: parse %s: %w", listingURL, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("discover samples: parse html: %w", err)
	}

	seen := map[string]bool{}
	var out []string
	doc.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, _ := a.Attr("href")
		u, perr := base.Parse(strings.TrimSpace(href))
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return true
		}
		if !strings.EqualFold(strings.TrimPrefix(u.Hostname(), "www."), strings.TrimPrefix(base.Hostname(), "www.")) {
			return true // same site only
		}
		u.Fragment, u.RawQuery = "", ""
		s := u.String()
		if seen[s] || s == listingURL {
			return true
		}
		if !detailPathHint.MatchString(u.Path) || !idishTail.MatchString(u.Path) {
			return true // listing/category links, nav, etc.
		}
		seen[s] = true
		out = append(out, s)
		return len(out) < n
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("discover samples: no detail-like links on %s", listingURL)
	}
	return out, nil
}
