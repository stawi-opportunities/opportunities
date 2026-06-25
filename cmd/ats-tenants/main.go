// ats-tenants discovers every live board token ("tenant") for a
// multi-tenant ATS platform (Greenhouse, Lever) so a single multi-tenant
// source can crawl them all — no manual hunting.
//
// Neither platform exposes a global list, so we harvest candidate tokens
// from the free Common Crawl URL index (the whole web, indexed) and
// validate each against the board API, keeping only live ones. The
// output is a JSON array ready to paste into a recipe's list.tenants.
//
// Usage:
//
//	go run ./cmd/ats-tenants -platform greenhouse [-pages 5] [-concurrency 32]
//	go run ./cmd/ats-tenants -platform lever      > lever-tenants.json
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

func main() {
	plat := flag.String("platform", "", "greenhouse | lever")
	pages := flag.Int("pages", 5, "Common Crawl index pages to scan (each ~15k urls)")
	conc := flag.Int("concurrency", 32, "parallel validation requests")
	flag.Parse()

	var (
		host    string
		tokenRe *regexp.Regexp
		valURL  func(string) string
	)
	switch *plat {
	case "greenhouse":
		host = "boards.greenhouse.io"
		tokenRe = regexp.MustCompile(`boards\.greenhouse\.io/([A-Za-z0-9_-]+)`)
		valURL = func(t string) string {
			return "https://boards-api.greenhouse.io/v1/boards/" + t + "/jobs"
		}
	case "lever":
		host = "jobs.lever.co"
		tokenRe = regexp.MustCompile(`jobs\.lever\.co/([A-Za-z0-9_-]+)`)
		valURL = func(t string) string {
			return "https://api.lever.co/v0/postings/" + t + "?mode=json&limit=1"
		}
	case "ashby":
		host = "jobs.ashbyhq.com"
		tokenRe = regexp.MustCompile(`jobs\.ashbyhq\.com/([A-Za-z0-9_.-]+)`)
		valURL = func(t string) string {
			return "https://api.ashbyhq.com/posting-api/job-board/" + t
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: ats-tenants -platform greenhouse|lever")
		os.Exit(2)
	}

	ctx := context.Background()
	candidates := harvestCommonCrawl(ctx, host, tokenRe, *pages)
	fmt.Fprintf(os.Stderr, "harvested %d candidate tenants from Common Crawl\n", len(candidates))

	live := validate(ctx, candidates, valURL, *plat, *conc)
	sort.Strings(live)
	fmt.Fprintf(os.Stderr, "%d live tenants (of %d candidates)\n", len(live), len(candidates))

	out, _ := json.MarshalIndent(live, "", "  ")
	fmt.Println(string(out))
}

// harvestCommonCrawl queries the latest CC URL index for the host and
// returns the distinct tenant tokens, scanning up to `pages` index pages.
func harvestCommonCrawl(ctx context.Context, host string, tokenRe *regexp.Regexp, pages int) []string {
	client := &http.Client{Timeout: 120 * time.Second}

	// Latest available index.
	idxAPI := "https://index.commoncrawl.org/CC-MAIN-2026-21-index"
	if info := ccLatest(ctx, client); info != "" {
		idxAPI = info
	}

	tokens := map[string]struct{}{}
	skip := map[string]bool{"embed": true, "v1": true, "favicon.ico": true}
	for p := range pages {
		url := fmt.Sprintf("%s?url=%s/*&output=json&fl=url&page=%d", idxAPI, host, p)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err != nil {
			break
		}
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
		got := 0
		for sc.Scan() {
			var row struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(sc.Bytes(), &row) != nil {
				continue
			}
			if m := tokenRe.FindStringSubmatch(row.URL); m != nil {
				t := strings.ToLower(m[1])
				if !skip[t] && len(t) > 1 {
					tokens[t] = struct{}{}
				}
			}
			got++
		}
		_ = sc.Err()
		_ = resp.Body.Close()
		if got == 0 {
			break
		}
	}
	out := make([]string, 0, len(tokens))
	for t := range tokens {
		out = append(out, t)
	}
	return out
}

func ccLatest(ctx context.Context, client *http.Client) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://index.commoncrawl.org/collinfo.json", nil)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var cols []struct {
		API string `json:"cdx-api"`
	}
	if json.NewDecoder(resp.Body).Decode(&cols) != nil || len(cols) == 0 {
		return ""
	}
	return cols[0].API
}

// validate keeps the candidate tokens whose board API responds with live
// postings, concurrently.
func validate(ctx context.Context, candidates []string, valURL func(string) string, plat string, conc int) []string {
	client := &http.Client{Timeout: 15 * time.Second}
	sem := make(chan struct{}, conc)
	var (
		mu   sync.Mutex
		live []string
		wg   sync.WaitGroup
	)
	for _, t := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(tok string) {
			defer wg.Done()
			defer func() { <-sem }()
			if isLive(ctx, client, valURL(tok), plat) {
				mu.Lock()
				live = append(live, tok)
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()
	return live
}

func isLive(ctx context.Context, client *http.Client, url, plat string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return false
	}
	// Lever 200s with an empty array, Ashby 200s with {"jobs":[]}, for a
	// board with no live postings — skip those. Greenhouse 200 is enough.
	switch plat {
	case "lever":
		var arr []json.RawMessage
		if json.NewDecoder(resp.Body).Decode(&arr) != nil || len(arr) == 0 {
			return false
		}
	case "ashby":
		var b struct {
			Jobs []json.RawMessage `json:"jobs"`
		}
		if json.NewDecoder(resp.Body).Decode(&b) != nil || len(b.Jobs) == 0 {
			return false
		}
	}
	return true
}
