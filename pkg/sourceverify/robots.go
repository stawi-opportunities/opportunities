package sourceverify

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RobotsHTTPChecker fetches /robots.txt over HTTP and answers Allowed
// against a tiny subset of the robots.txt grammar — enough for the
// User-agent / Disallow / Allow directives that 99% of job boards
// expose. Crawl-delay, Sitemap, and other extensions are ignored.
//
// Designed to be permissive on errors: a missing or malformed robots.txt
// is treated as "allow all" so an operator-friendly source is not
// rejected because of a transient network blip.
type RobotsHTTPChecker struct {
	client  HTTPDoer
	timeout time.Duration
}

// NewRobotsHTTPChecker returns a checker that fetches robots.txt with
// the given client. timeout defaults to 5s.
func NewRobotsHTTPChecker(client HTTPDoer, timeout time.Duration) *RobotsHTTPChecker {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &RobotsHTTPChecker{client: client, timeout: timeout}
}

// Allowed implements RobotsChecker.
func (r *RobotsHTTPChecker) Allowed(ctx context.Context, rawURL, userAgent string) (bool, error) {
	if r.client == nil {
		return true, nil
	}
	target, err := url.Parse(rawURL)
	if err != nil || target.Host == "" {
		return false, fmt.Errorf("invalid url: %q", rawURL)
	}
	robotsURL := target.Scheme + "://" + target.Host + "/robots.txt"

	fetchCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return true, nil
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		// Network error: be permissive.
		return true, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode >= 500 {
		return true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return true, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return true, nil
	}
	rules := parseRobots(string(body), userAgent)
	return rules.allowed(target.Path), nil
}

// robotRules holds the disallow/allow patterns that apply to a single
// User-agent group. Patterns are stored in their raw form; matching is
// done by simple longest-prefix.
type robotRules struct {
	disallow []string
	allow    []string
}

func (r robotRules) allowed(path string) bool {
	if path == "" {
		path = "/"
	}
	// Longest match wins (Allow vs Disallow). RFC-style robots.txt does
	// not actually mandate this, but it matches Google's published
	// behaviour and is the de-facto standard.
	bestLen := -1
	bestAllow := true
	for _, p := range r.disallow {
		if matched, n := robotsMatch(p, path); matched && n > bestLen {
			bestLen = n
			bestAllow = false
		}
	}
	for _, p := range r.allow {
		if matched, n := robotsMatch(p, path); matched && n >= bestLen {
			bestLen = n
			bestAllow = true
		}
	}
	return bestAllow
}

func robotsMatch(pattern, path string) (bool, int) {
	if pattern == "" {
		// Empty Disallow == allow everything.
		return false, 0
	}
	// Simple prefix match. Wildcard support (*) is intentionally omitted
	// — adding it pulls in path-glob complexity and is rarely needed for
	// job boards.
	if strings.HasPrefix(path, pattern) {
		return true, len(pattern)
	}
	return false, 0
}

// parseRobots returns the robotRules that apply to userAgent. The
// matching algorithm follows Google's: pick the longest User-agent token
// that is a case-insensitive substring of userAgent; fall back to '*' if
// nothing matches.
func parseRobots(body, userAgent string) robotRules {
	type group struct {
		agents []string
		rules  robotRules
	}

	var groups []group
	var current *group

	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:colon]))
		val := strings.TrimSpace(line[colon+1:])

		switch key {
		case "user-agent":
			if current == nil || len(current.rules.disallow) > 0 || len(current.rules.allow) > 0 {
				groups = append(groups, group{})
				current = &groups[len(groups)-1]
			}
			current.agents = append(current.agents, strings.ToLower(val))
		case "disallow":
			if current == nil {
				continue
			}
			current.rules.disallow = append(current.rules.disallow, val)
		case "allow":
			if current == nil {
				continue
			}
			current.rules.allow = append(current.rules.allow, val)
		}
	}

	uaLower := strings.ToLower(userAgent)
	var best *group
	bestLen := -1
	for i := range groups {
		for _, a := range groups[i].agents {
			if a == "*" {
				if best == nil {
					best = &groups[i]
				}
				continue
			}
			if strings.Contains(uaLower, a) && len(a) > bestLen {
				best = &groups[i]
				bestLen = len(a)
			}
		}
	}
	if best == nil {
		return robotRules{}
	}
	return best.rules
}
