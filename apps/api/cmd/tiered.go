package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"stawi.jobs/pkg/locale"
	"stawi.jobs/pkg/repository"
)

// ── Tiered search ──────────────────────────────────────────────────
//
// GET /api/feed returns jobs cascaded into locality tiers:
//
//  1. preferred — the caller supplied `boost_countries` + `boost_languages`
//     (authenticated users pass their CandidateProfile here).
//  2. local     — inferred home country (CF-IPCountry or `boost_country`).
//  3. regional  — the surrounding labour-market region.
//  4. global    — everywhere else, recent first.
//
// The endpoint runs up to four independent SearchCanonical calls in
// parallel against the existing full-text-indexed table, each one
// narrowed by the tier's country/language predicates. Empty tiers
// (no results) are omitted so the UI never renders an empty section.
//
// Response shape:
//
//	{
//	  "context":  {"country": "KE", "languages": ["en","sw"], "region": "east_africa"},
//	  "tiers": [
//	    {"id":"preferred", "label":"Your preferred", "jobs":[…], "cursor":"…", "has_more":true},
//	    {"id":"local",     "label":"Jobs in Kenya",  "jobs":[…], "cursor":"…", "has_more":true},
//	    {"id":"regional",  "label":"East Africa",    "jobs":[…], "cursor":"…", "has_more":true},
//	    {"id":"global",    "label":"Worldwide",      "jobs":[…], "cursor":"…", "has_more":false}
//	  ],
//	  "facets": { … }
//	}
//
// The caller may request a single tier's next page with
// GET /api/feed/tier?tier=local&cursor=…&country=KE&… — we don't
// re-run the whole cascade for pagination.

// tieredResponseTier is one section in the cascade response.
type tieredResponseTier struct {
	ID       string                      `json:"id"`
	Label    string                      `json:"label"`
	Jobs     []*repository.SearchResult  `json:"jobs"`
	Cursor   string                      `json:"cursor"`
	HasMore  bool                        `json:"has_more"`
	// Country / Countries / Language surface the exact filter used
	// so the frontend's "Load more in this section" keeps the right
	// scope without re-deriving it.
	Country   string   `json:"country,omitempty"`
	Countries []string `json:"countries,omitempty"`
	Language  string   `json:"language,omitempty"`
}

// tieredFeedContext is emitted as `context` in the response so the
// client can tell which inferred signals we used for ranking. Useful
// for UI chrome ("Showing jobs near Kenya · change") and for debugging
// "why is this the top tier".
type tieredFeedContext struct {
	Country   string        `json:"country"`
	Languages []string      `json:"languages"`
	Region    locale.Region `json:"region"`
}

// inferContextFromRequest resolves the effective country and languages
// for a request. Precedence (first non-empty wins):
//
//	country:   ?country=  > CF-IPCountry header > X-Country-Code > ""
//	languages: ?lang=csv  > Accept-Language header parsed > []
//
// Empty results simply produce a less-ranked cascade — the endpoint
// still works, it just degrades to a generic global feed.
func inferContextFromRequest(req *http.Request) (string, []string) {
	country := strings.ToUpper(strings.TrimSpace(req.URL.Query().Get("country")))
	if country == "" {
		country = strings.ToUpper(strings.TrimSpace(req.Header.Get("CF-IPCountry")))
	}
	if country == "" {
		country = strings.ToUpper(strings.TrimSpace(req.Header.Get("X-Country-Code")))
	}

	var langs []string
	if raw := strings.TrimSpace(req.URL.Query().Get("lang")); raw != "" {
		for _, l := range strings.Split(raw, ",") {
			if b := locale.BaseTag(l); b != "" {
				langs = append(langs, b)
			}
		}
	}
	if len(langs) == 0 {
		langs = locale.ParseAcceptLanguage(req.Header.Get("Accept-Language"))
	}
	return country, langs
}

// parseBoostList splits a comma-separated ISO-3166/BCP-47 list into an
// uppercased-country or lowercased-base-tag slice. Empty-safe.
func parseBoostList(raw string, kind string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		switch kind {
		case "country":
			if u := strings.ToUpper(strings.TrimSpace(p)); u != "" {
				out = append(out, u)
			}
		case "language":
			if b := locale.BaseTag(p); b != "" {
				out = append(out, b)
			}
		}
	}
	return out
}

// feedHandler backs GET /api/feed. Wraps the concurrent per-tier
// fetches + dedup logic; callers pass the shared repos.
func feedHandler(jobRepo *repository.JobRepository, facetRepo *repository.FacetRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		// Common filters apply to every tier.
		q := strings.TrimSpace(qs.Get("q"))
		category := qs.Get("category")
		remote := qs.Get("remote_type")
		employment := qs.Get("employment_type")
		seniority := qs.Get("seniority")
		tierLimit := parseLimit(qs.Get("tier_limit"), 25, 100)
		sort := qs.Get("sort")

		// Inferred signals: CF-IPCountry + Accept-Language.
		inferredCountry, inferredLangs := inferContextFromRequest(req)

		// Explicit boosts (logged-in user's preferences).
		boostCountries := parseBoostList(qs.Get("boost_countries"), "country")
		boostLangs := parseBoostList(qs.Get("boost_languages"), "language")

		// The "preferred" tier fires only when the caller supplied an
		// explicit boost. Inference alone does NOT drive it — that
		// would steal the "local" tier's spot and misname the section.
		hasPreferred := len(boostCountries) > 0 || len(boostLangs) > 0

		region := locale.RegionOf(inferredCountry)
		regionCountries := locale.CountriesIn(region)

		// Track what each tier has already pulled so subsequent tiers
		// can exclude those countries / languages and we don't show
		// the same job twice.
		seenCountries := make([]string, 0, 32)
		seenLanguages := make([]string, 0, 4)

		baseReq := repository.SearchRequest{
			Query:          q,
			Category:       category,
			RemoteType:     remote,
			EmploymentType: employment,
			Seniority:      seniority,
			Sort:           sort,
			Limit:          tierLimit,
		}

		var specs []tierSpec

		// 1. Preferred (when supplied).
		if hasPreferred {
			r := baseReq
			r.Countries = boostCountries
			// Language: only apply if exactly one preferred lang; a
			// multi-lang preferred tier would shrink to jobs tagged
			// with ALL, which isn't how preferences work. If the user
			// has multiple, the tier acts on country only and the
			// language boost applies in cheaper tiers below.
			if len(boostLangs) == 1 {
				r.Language = boostLangs[0]
				seenLanguages = append(seenLanguages, boostLangs[0])
			}
			specs = append(specs, tierSpec{
				id:        "preferred",
				label:     "Your preferred",
				req:       r,
				countries: boostCountries,
				language:  preferredTierLanguage(boostLangs),
			})
			seenCountries = append(seenCountries, boostCountries...)
		}

		// 2. Local (inferred country). If it overlaps the preferred
		//    set, collapse rather than duplicate.
		if inferredCountry != "" && !containsIgnoreCase(boostCountries, inferredCountry) {
			r := baseReq
			r.Country = inferredCountry
			// Prefer the user's first language as a soft filter on
			// the local tier — keeps a Nairobi English-browser away
			// from a Francophone Kenyan job at #1. Skipped if no
			// language inferred.
			if lang := firstOrEmpty(inferredLangs); lang != "" {
				r.Language = lang
			}
			specs = append(specs, tierSpec{
				id:       "local",
				label:    "Jobs in " + countryDisplayName(inferredCountry),
				req:      r,
				country:  inferredCountry,
				language: firstOrEmpty(inferredLangs),
			})
			seenCountries = append(seenCountries, inferredCountry)
		}

		// 3. Regional (labour-market cluster minus what we already pulled).
		if region != locale.RegionUnknown {
			nearby := filterOut(regionCountries, seenCountries)
			if len(nearby) > 0 {
				r := baseReq
				r.Countries = nearby
				specs = append(specs, tierSpec{
					id:        "regional",
					label:     locale.RegionLabel(region),
					req:       r,
					countries: nearby,
				})
				seenCountries = append(seenCountries, nearby...)
			}
		}

		// 4. Global — everything else. ExcludeCountries blocks the
		//    preferred/local/regional set so no job appears twice.
		globalReq := baseReq
		globalReq.ExcludeCountries = seenCountries
		_ = seenLanguages // reserved for future language-based exclusion
		specs = append(specs, tierSpec{
			id:    "global",
			label: "Worldwide",
			req:   globalReq,
		})

		// Fan out — each tier is an independent query; Postgres
		// handles the parallelism cheaply (all predicates hit the
		// same indexed columns).
		tiers := runTiersParallel(ctx, jobRepo, specs)

		// Drop empty tiers so the UI never renders a header over
		// nothing. Preferred/local/regional/global are all optional —
		// we might only have "global" in a tiny market.
		kept := tiers[:0]
		for _, t := range tiers {
			if len(t.Jobs) > 0 {
				kept = append(kept, t)
			}
		}

		facets, _ := facetRepo.Read(ctx)
		w.Header().Set("Content-Type", "application/json")
		// Short CDN cache: the same (country, lang, query) combo is
		// served to many users per minute, and a 30s window is well
		// below our crawl cadence so freshness stays acceptable.
		if q == "" {
			w.Header().Set("Cache-Control", "public, max-age=30")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"context": tieredFeedContext{
				Country:   inferredCountry,
				Languages: inferredLangs,
				Region:    region,
			},
			"tiers":  kept,
			"facets": facets,
			"sort":   effectiveSort(sort, q),
		})
	}
}

// tierSpec captures everything needed to run one tier of the cascade.
type tierSpec struct {
	id, label string
	req       repository.SearchRequest
	// country/countries/language are echoed back on the response so
	// the frontend's "load more" call targets the same filter set.
	country   string
	countries []string
	language  string
}

// tierFetchResult holds one goroutine's output.
type tierFetchResult struct {
	index int
	rows  []*repository.SearchResult
	err   error
}

// runTiersParallel runs each tier spec concurrently and stitches the
// results back in the caller's order. A tier error is non-fatal —
// we log and emit an empty jobs list for that tier so the whole
// feed still renders.
func runTiersParallel(
	ctx context.Context,
	jobRepo *repository.JobRepository,
	specs []tierSpec,
) []tieredResponseTier {
	// Hard deadline: the whole feed mustn't exceed 3s. Tail-latency
	// budget per tier is tight to keep first-paint snappy.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	results := make([]tierFetchResult, len(specs))
	done := make(chan tierFetchResult, len(specs))

	for i, spec := range specs {
		go func(idx int, r repository.SearchRequest) {
			rows, err := jobRepo.SearchCanonical(ctx, r)
			done <- tierFetchResult{index: idx, rows: rows, err: err}
		}(i, spec.req)
	}
	for range specs {
		r := <-done
		results[r.index] = r
	}

	out := make([]tieredResponseTier, len(specs))
	for i, spec := range specs {
		rows := results[i].rows
		hasMore := len(rows) > spec.req.Limit
		if hasMore {
			rows = rows[:spec.req.Limit]
		}
		var cursor string
		if hasMore && spec.req.Query == "" && len(rows) > 0 {
			// Scan backwards to find a row with a non-nil PostedAt.
			// Keyset pagination needs (posted_at, id) to advance; a
			// tail row with NULL posted_at would emit an empty
			// cursor and the client would refetch the same page in
			// an infinite loop.
			for j := len(rows) - 1; j >= 0; j-- {
				if rows[j].PostedAt != nil {
					cursor = encodeCursor(*rows[j].PostedAt, rows[j].ID)
					break
				}
			}
			// If EVERY row in the tail has NULL posted_at, we can't
			// honestly paginate past this batch — flip has_more off
			// so the client stops asking.
			if cursor == "" {
				hasMore = false
			}
		}
		out[i] = tieredResponseTier{
			ID:        spec.id,
			Label:     spec.label,
			Jobs:      rows,
			Cursor:    cursor,
			HasMore:   hasMore,
			Country:   spec.country,
			Countries: spec.countries,
			Language:  spec.language,
		}
	}
	return out
}

// tierPageHandler backs GET /api/feed/tier — cursor-paginated fetch
// for a single tier after the frontend has shown the initial cascade.
// Keeps the URL-state explicit (country=…, countries=…, language=…)
// so the client doesn't need to reconstruct the tier spec itself.
func tierPageHandler(jobRepo *repository.JobRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		limit := parseLimit(qs.Get("limit"), 25, 100)
		var cursorTS *time.Time
		var cursorID string
		if c := qs.Get("cursor"); c != "" {
			if ts, id, ok := decodeCursor(c); ok {
				cursorTS, cursorID = &ts, id
			}
		}
		var excludeCountries []string
		if ex := qs.Get("exclude_countries"); ex != "" {
			excludeCountries = parseBoostList(ex, "country")
		}

		searchReq := repository.SearchRequest{
			Query:          strings.TrimSpace(qs.Get("q")),
			Category:       qs.Get("category"),
			RemoteType:     qs.Get("remote_type"),
			EmploymentType: qs.Get("employment_type"),
			Seniority:      qs.Get("seniority"),
			Country:        strings.ToUpper(strings.TrimSpace(qs.Get("country"))),
			Countries:      parseBoostList(qs.Get("countries"), "country"),
			ExcludeCountries: excludeCountries,
			Language:       locale.BaseTag(qs.Get("language")),
			Sort:           qs.Get("sort"),
			Limit:          limit,
			CursorPostedAt: cursorTS,
			CursorID:       cursorID,
		}
		// Text-search uses offset pagination.
		if v, err := strconv.Atoi(qs.Get("offset")); err == nil && v > 0 {
			searchReq.Offset = v
		}

		rows, err := jobRepo.SearchCanonical(ctx, searchReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}
		var nextCursor string
		if hasMore && searchReq.Query == "" && len(rows) > 0 {
			// Same NULL-posted_at fallback as the initial feed: scan
			// backwards, and if no row has a non-nil timestamp we
			// can't paginate past this batch.
			for j := len(rows) - 1; j >= 0; j-- {
				if rows[j].PostedAt != nil {
					nextCursor = encodeCursor(*rows[j].PostedAt, rows[j].ID)
					break
				}
			}
			if nextCursor == "" {
				hasMore = false
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=30")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jobs":        rows,
			"cursor_next": nextCursor,
			"has_more":    hasMore,
		})
	}
}

// ── helpers ───────────────────────────────────────────────────────

func containsIgnoreCase(xs []string, s string) bool {
	up := strings.ToUpper(strings.TrimSpace(s))
	for _, x := range xs {
		if strings.ToUpper(strings.TrimSpace(x)) == up {
			return true
		}
	}
	return false
}

func filterOut(src, exclude []string) []string {
	if len(exclude) == 0 {
		return src
	}
	excl := make(map[string]struct{}, len(exclude))
	for _, e := range exclude {
		excl[strings.ToUpper(strings.TrimSpace(e))] = struct{}{}
	}
	out := make([]string, 0, len(src))
	for _, s := range src {
		if _, drop := excl[strings.ToUpper(strings.TrimSpace(s))]; drop {
			continue
		}
		out = append(out, s)
	}
	return out
}

func firstOrEmpty(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[0]
}

func preferredTierLanguage(langs []string) string {
	if len(langs) == 1 {
		return langs[0]
	}
	return ""
}

// countryDisplayName returns a human label for a two-letter country
// code.  This list is deliberately short — just the top source markets
// plus a generic fallback. Anything unmapped uses the code itself,
// which is still useful ("Jobs in ZW").
func countryDisplayName(cc string) string {
	switch strings.ToUpper(cc) {
	case "KE":
		return "Kenya"
	case "UG":
		return "Uganda"
	case "TZ":
		return "Tanzania"
	case "RW":
		return "Rwanda"
	case "ET":
		return "Ethiopia"
	case "NG":
		return "Nigeria"
	case "GH":
		return "Ghana"
	case "ZA":
		return "South Africa"
	case "EG":
		return "Egypt"
	case "MA":
		return "Morocco"
	case "US":
		return "the United States"
	case "GB":
		return "the United Kingdom"
	case "CA":
		return "Canada"
	case "DE":
		return "Germany"
	case "IN":
		return "India"
	case "AU":
		return "Australia"
	case "PH":
		return "the Philippines"
	case "BR":
		return "Brazil"
	default:
		if cc == "" {
			return "your region"
		}
		return cc
	}
}
