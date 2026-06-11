// recipe-verify dry-runs an extraction recipe against the LIVE site using
// the exact production code paths and reports whether it produces the
// required outcome:
//
//  1. structural Validate() — enums, selectors, required-field extractors
//  2. ValidateRecipe() — the activation quality gate: discover real detail
//     pages, fetch them, extract every field, run opportunity.Verify
//  3. Executor.Page() — one real list-page execution, proving the list
//     rule + pagination produce items end-to-end (the part the activation
//     gate does not exercise)
//
// Usage:
//
//	go run ./cmd/recipe-verify -recipe recipe.json -base-url https://www.example.com \
//	    [-kinds job] [-samples 3] [-kinds-dir definitions/opportunity-kinds]
//
// Exit code 0 = recipe verified (gate pass rate met AND page 1 yields items).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

func main() {
	recipePath := flag.String("recipe", "", "path to recipe JSON (or '-' for stdin)")
	baseURL := flag.String("base-url", "", "source base URL (listing page)")
	kinds := flag.String("kinds", "job", "comma-separated source kinds")
	sampleN := flag.Int("samples", 3, "detail pages to validate against")
	kindsDir := flag.String("kinds-dir", "definitions/opportunity-kinds", "opportunity kind definitions dir")
	passThreshold := flag.Float64("threshold", 0.8, "required validation pass rate")
	flag.Parse()

	if *recipePath == "" || *baseURL == "" {
		fmt.Fprintln(os.Stderr, "usage: recipe-verify -recipe recipe.json -base-url https://…")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	raw, err := readInput(*recipePath)
	fatal(err, "read recipe")

	var rec recipe.Recipe
	fatal(json.Unmarshal(raw, &rec), "parse recipe JSON")
	rec.Normalize()

	fmt.Printf("── 1. structural validation ─────────────────────────\n")
	if verr := rec.Validate(); verr != nil {
		fmt.Printf("FAIL: %v\n", verr)
		os.Exit(1)
	}
	fmt.Printf("OK   acquisition=%s list.mode=%s pagination=%s\n",
		rec.Acquisition, rec.List.Mode, rec.List.Pagination.Mode)

	reg, err := opportunity.LoadFromDir(*kindsDir)
	fatal(err, "load kind registry")

	src := domain.Source{
		BaseURL: *baseURL,
		Kinds:   strings.Split(*kinds, ","),
		Status:  domain.SourceActive,
	}

	client := httpx.NewClient(20*time.Second, "opportunities-recipe-verify/1.0")
	fetcher := recipe.NewHTTPFetcher(client)

	fmt.Printf("\n── 2. activation gate (detail extraction vs live pages) ──\n")
	sampleURLs, err := recipe.DiscoverDetailURLs(ctx, fetcher, *baseURL, *sampleN)
	if err != nil || len(sampleURLs) == 0 {
		fmt.Printf("WARN: detail-link discovery failed (%v); validating against base URL only\n", err)
		sampleURLs = []string{*baseURL}
	}
	var samples []recipe.SamplePage
	for _, u := range sampleURLs {
		body, status, ferr := fetcher.Get(ctx, u)
		if ferr != nil || status != 200 {
			fmt.Printf("     sample %s: fetch failed (status=%d err=%v)\n", u, status, ferr)
			continue
		}
		samples = append(samples, recipe.SamplePage{URL: u, HTML: string(body)})
	}
	if len(samples) == 0 {
		fmt.Println("FAIL: no fetchable sample pages — cannot verify extraction")
		os.Exit(1)
	}
	rep := recipe.ValidateRecipe(&rec, src, samples, reg)
	for _, s := range rep.PerSample {
		if s.OK {
			fmt.Printf("PASS %s\n", s.URL)
		} else {
			fmt.Printf("FAIL %s — missing %v\n", s.URL, s.Missing)
		}
	}
	fmt.Printf("pass rate %.2f (threshold %.2f, stored gate result at activation: see source_recipes.pass_rate)\n",
		rep.PassRate, *passThreshold)

	fmt.Printf("\n── 3. executor end-to-end (list rule + pagination, page 1) ──\n")
	ex := recipe.NewExecutor(&rec, fetcher)
	items, _, status, _, done, perr := ex.Page(ctx, src, recipe.PageState{})
	if perr != nil {
		fmt.Printf("FAIL: executor page 1: %v (status=%d)\n", perr, status)
		os.Exit(1)
	}
	fmt.Printf("OK   page 1: %d items, status=%d, more_pages=%v\n", len(items), status, !done)
	for i, it := range items {
		if i >= 3 {
			break
		}
		fmt.Printf("     • %q @ %q → %s\n", trunc(it.Title, 50), trunc(it.IssuingEntity, 30), trunc(it.ApplyURL, 70))
	}

	if rep.PassRate < *passThreshold || len(items) == 0 {
		fmt.Println("\nVERDICT: NOT VERIFIED")
		os.Exit(1)
	}
	fmt.Println("\nVERDICT: VERIFIED — recipe extracts required fields from live pages and the list rule yields items")
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		var b []byte
		buf := make([]byte, 64*1024)
		for {
			n, err := os.Stdin.Read(buf)
			b = append(b, buf[:n]...)
			if err != nil {
				return b, nil
			}
		}
	}
	return os.ReadFile(path)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func fatal(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "recipe-verify: %s: %v\n", what, err)
		os.Exit(1)
	}
}
