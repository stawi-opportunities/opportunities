// recipe-gen synthesizes and verifies an extraction recipe for ONE live
// source, using the exact production Generator — including the list-rule
// gate — but driven locally so a recipe can be produced and proven in
// minutes instead of one prod backfill tick per 15 minutes.
//
// All inputs are live public pages; the only credential is the
// OpenAI-compatible inference key (INFERENCE_API_KEY / INFERENCE_BASE_URL /
// INFERENCE_MODEL env, same contract as the crawler).
//
// Usage:
//
//	INFERENCE_API_KEY=… go run ./cmd/recipe-gen \
//	    -base-url https://www.jobberman.com -type jobberman -country NG \
//	    [-kinds job] [-samples 3] [-out recipe.json]
//
// Exit 0 only when the Generator's full gate chain (structural validation,
// ≥0.8 detail pass rate on live sample pages, list rule yielding items on
// the live listing) accepts the recipe.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

func main() {
	baseURL := flag.String("base-url", "", "source listing URL")
	srcType := flag.String("type", "generic_html", "source type")
	kinds := flag.String("kinds", "job", "comma-separated kinds")
	country := flag.String("country", "", "anchor country code")
	language := flag.String("language", "en", "source language")
	sampleN := flag.Int("samples", 3, "detail pages to learn/validate from")
	attempts := flag.Int("attempts", 3, "LLM repair attempts")
	kindsDir := flag.String("kinds-dir", "definitions/opportunity-kinds", "kind definitions dir")
	out := flag.String("out", "", "write the accepted recipe JSON here")
	listing := flag.String("listing", "auto", "listing path relative to base-url ('auto' probes common paths, '' uses the base)")
	flag.Parse()

	if *baseURL == "" {
		fmt.Fprintln(os.Stderr, "usage: recipe-gen -base-url https://… [-type t] [-country CC]")
		os.Exit(2)
	}
	infBase := os.Getenv("INFERENCE_BASE_URL")
	infKey := os.Getenv("INFERENCE_API_KEY")
	infModel := os.Getenv("INFERENCE_MODEL")
	if infBase == "" || infKey == "" || infModel == "" {
		fmt.Fprintln(os.Stderr, "recipe-gen: INFERENCE_BASE_URL / INFERENCE_API_KEY / INFERENCE_MODEL must be set")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	reg, err := opportunity.LoadFromDir(*kindsDir)
	fatal(err, "load kind registry")

	extractor := extraction.New(extraction.Config{
		BaseURL:    infBase,
		APIKey:     infKey,
		Model:      infModel,
		Registry:   reg,
		HTTPClient: &http.Client{Timeout: 3 * time.Minute},
	})

	client := httpx.NewClient(20*time.Second, "opportunities-recipe-gen/1.0")
	fetcher := recipe.NewHTTPFetcher(client)

	src := domain.Source{
		Type:     domain.SourceType(*srcType),
		BaseURL:  *baseURL,
		Country:  *country,
		Language: *language,
		Status:   domain.SourceActive,
		Kinds:    strings.Split(*kinds, ","),
	}

	// Find the real jobs LISTING: the source base_url is often a homepage
	// whose "job-ish" links are advice articles, not postings. Probe the
	// base plus common listing paths and keep the one yielding the most
	// detail links. -listing overrides the probe.
	listingRef := *listing
	if listingRef == "auto" {
		listingRef = ""
		best := 0
		for _, cand := range []string{"", "/jobs", "/vacancies", "/jobs/search", "/job-vacancies", "/en/jobs", "/search/jobs"} {
			u, uerr := url.JoinPath(*baseURL, cand)
			if uerr != nil {
				continue
			}
			found, _ := recipe.DiscoverDetailURLs(ctx, fetcher, u, 10)
			if len(found) > best {
				best, listingRef = len(found), cand
			}
		}
		fmt.Printf("listing: base%s (%d detail links)\n", listingRef, best)
	}
	listingURL, err := url.JoinPath(*baseURL, listingRef)
	fatal(err, "resolve listing URL")

	sampleURLs, derr := recipe.DiscoverDetailURLs(ctx, fetcher, listingURL, *sampleN)
	if derr != nil || len(sampleURLs) == 0 {
		fmt.Fprintf(os.Stderr, "recipe-gen: no detail pages discoverable on %s (%v) — recipe cannot be learned\n", listingURL, derr)
		os.Exit(1)
	}
	fmt.Printf("samples: %s\n", strings.Join(sampleURLs, "\n         "))

	gen := recipe.NewGenerator(extractor, fetcher, reg, *attempts)
	rec, samples, gerr := gen.GenerateFrom(ctx, src, listingRef, sampleURLs)
	if gerr != nil {
		fmt.Fprintf(os.Stderr, "recipe-gen: NOT ACCEPTED: %v\n", gerr)
		os.Exit(1)
	}

	rep := recipe.ValidateRecipe(rec, src, samples, reg)
	n, lerr := recipe.NewExecutor(rec, fetcher).ListProbe(ctx, src)
	fatal(lerr, "list probe")

	fmt.Printf("ACCEPTED acquisition=%s list.mode=%s pagination=%s pass_rate=%.2f list_items=%d\n",
		rec.Acquisition, rec.List.Mode, rec.List.Pagination.Mode, rep.PassRate, n)

	encoded, err := json.MarshalIndent(rec, "", " ")
	fatal(err, "encode recipe")
	if *out != "" {
		fatal(os.WriteFile(*out, encoded, 0o644), "write recipe")
		// Companion report for activation bookkeeping (pass_rate, samples).
		repJSON, _ := json.Marshal(rep)
		_ = os.WriteFile(strings.TrimSuffix(*out, ".json")+".report.json", repJSON, 0o644)
	} else {
		fmt.Println(string(encoded))
	}
}

func fatal(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "recipe-gen: %s: %v\n", what, err)
		os.Exit(1)
	}
}
