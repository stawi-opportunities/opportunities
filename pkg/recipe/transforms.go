package recipe

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// transformCtx carries side data some transforms need (e.g. the page URL for
// resolving relative links). Most transforms ignore it.
type transformCtx struct {
	BaseURL string
}

type transformFn func(string, transformCtx) (string, error)

var wsRe = regexp.MustCompile(`\s+`)
var moneyStripRe = regexp.MustCompile(`[^0-9.\-]`)

// dateLayouts are tried in order by parse_date. Output is always RFC3339 (UTC).
var dateLayouts = []string{
	time.RFC3339, "2006-01-02T15:04:05", "2006-01-02",
	"02/01/2006", "01/02/2006", "January 2, 2006", "Jan 2, 2006", "2 January 2006",
}

var transformRegistry = map[string]transformFn{
	"trim":  func(s string, _ transformCtx) (string, error) { return strings.TrimSpace(s), nil },
	"lower": func(s string, _ transformCtx) (string, error) { return strings.ToLower(s), nil },
	"collapse_ws": func(s string, _ transformCtx) (string, error) {
		return strings.TrimSpace(wsRe.ReplaceAllString(s, " ")), nil
	},
	"html_to_text": func(s string, _ transformCtx) (string, error) {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(s))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(wsRe.ReplaceAllString(doc.Text(), " ")), nil
	},
	"absolute_url": func(s string, tc transformCtx) (string, error) {
		if s == "" || tc.BaseURL == "" {
			return s, nil
		}
		base, err := url.Parse(tc.BaseURL)
		if err != nil {
			return s, nil
		}
		ref, err := url.Parse(strings.TrimSpace(s))
		if err != nil {
			return s, nil
		}
		return base.ResolveReference(ref).String(), nil
	},
	"parse_money": func(s string, _ transformCtx) (string, error) {
		return moneyStripRe.ReplaceAllString(s, ""), nil
	},
	"parse_date": func(s string, _ transformCtx) (string, error) {
		s = strings.TrimSpace(s)
		for _, layout := range dateLayouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC().Format(time.RFC3339), nil
			}
		}
		return "", fmt.Errorf("parse_date: unrecognized date %q", s)
	},
}

func transformExists(name string) bool {
	_, ok := transformRegistry[name]
	return ok
}

// applyTransforms pipes value through the named transforms in order.
func applyTransforms(value string, names []string, tc transformCtx) (string, error) {
	for _, name := range names {
		fn, ok := transformRegistry[name]
		if !ok {
			return "", fmt.Errorf("unknown transform %q", name)
		}
		out, err := fn(value, tc)
		if err != nil {
			return "", fmt.Errorf("transform %q: %w", name, err)
		}
		value = out
	}
	return value, nil
}
