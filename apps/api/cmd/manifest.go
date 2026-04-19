package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/locale"
	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/repository"
)

// ── Feed-manifest generator ────────────────────────────────────────
//
// Publishes a per-country pre-baked feed manifest to R2 so the
// frontend's first paint doesn't depend on the live search API.
//
//   /feeds/<cc>.json       — per-country tiered feed (16 shards)
//   /feeds/default.json    — generic global feed for users outside
//                            the published shard set
//   /feeds/index.json      — list of shards + generated_at so the
//                            client can tell how stale the data is
//
// The manifests carry CDN Cache-Control: public, max-age=60,
// s-maxage=300 (set by UploadPublicSnapshot). The rebuild cadence
// (3h via Trustage) is unrelated to the cache TTL — manifests stay
// in the edge cache for 5 minutes at most, so a new rebuild is
// visible to users within that window without explicit purge.
//
// Each shard's jobs list is capped — baking 20 jobs/tier × 4 tiers
// keeps the file ~40 KB post-gzip. Beyond that the user scrolls
// into live /api/feed/tier territory anyway.

// shardMeta mirrors an entry in ui/data/locale_shards.yaml. We
// duplicate the list here rather than reading the YAML — the cluster
// pod doesn't have the Hugo site bundled, and shard set changes are
// infrequent.  Adding a market is three lines in this file plus the
// Hugo YAML + an _index.md stub.
type shardMeta struct {
	Country   string
	Languages []string
}

var shardList = []shardMeta{
	{Country: "KE", Languages: []string{"en", "sw"}},
	{Country: "UG", Languages: []string{"en", "sw"}},
	{Country: "TZ", Languages: []string{"en", "sw"}},
	{Country: "RW", Languages: []string{"en", "fr"}},
	{Country: "ET", Languages: []string{"en", "am"}},
	{Country: "NG", Languages: []string{"en"}},
	{Country: "GH", Languages: []string{"en"}},
	{Country: "ZA", Languages: []string{"en"}},
	{Country: "EG", Languages: []string{"ar", "en"}},
	{Country: "MA", Languages: []string{"ar", "fr", "en"}},
	{Country: "US", Languages: []string{"en"}},
	{Country: "GB", Languages: []string{"en"}},
	{Country: "DE", Languages: []string{"de", "en"}},
	{Country: "IN", Languages: []string{"en"}},
	{Country: "PH", Languages: []string{"en"}},
	{Country: "BR", Languages: []string{"pt", "en"}},
}

// manifestTier mirrors the frontend's FeedTier. Aliased to the live
// /api/feed response type so the two stay wire-compatible by
// construction — changing one updates both.
type manifestTier = tieredResponseTier

type manifestContext struct {
	Country   string        `json:"country"`
	Languages []string      `json:"languages"`
	Region    locale.Region `json:"region"`
}

// feedManifest is the JSON shape the frontend downloads from R2.
// Intentionally mirror-compatible with the /api/feed response so the
// Cascade component can consume both paths without branching.
type feedManifest struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Context     manifestContext `json:"context"`
	Tiers       []manifestTier  `json:"tiers"`
}

// manifestIndex lists the shards we've published + their last-
// generation timestamp so the client can show a "data is N minutes
// old" indicator and so the Trustage health check can verify the
// cron actually ran.
type manifestIndex struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Shards      []indexShardRef `json:"shards"`
}

type indexShardRef struct {
	Country   string    `json:"country"`
	Languages []string  `json:"languages"`
	Key       string    `json:"key"` // e.g. "feeds/ke.json"
	UpdatedAt time.Time `json:"updated_at"`
}

// feedRebuildHandler runs the full manifest generation. Synchronous —
// with 16 shards at a ~200ms query each, the whole run fits in 5-8s
// end-to-end, which is fine for a Trustage cron.  Returns a summary
// JSON on success so ops can see what was written without tailing
// logs.
//
// Authentication: the caller is expected to sit behind the gateway's
// admin-auth layer (X-Admin-Token header), same contract as
// /admin/backfill.
func feedRebuildHandler(jobRepo *repository.JobRepository, r2 *publish.R2Publisher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r2 == nil {
			http.Error(w, `{"error":"r2 not configured"}`, http.StatusServiceUnavailable)
			return
		}

		ctx := r.Context()
		log := util.Log(ctx)
		start := time.Now()

		// Hard deadline — a Trustage retry is cheaper than a stuck
		// request wedging the scheduler.
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		summary := rebuildAllManifests(ctx, jobRepo, r2)
		summary.Duration = time.Since(start).Round(time.Millisecond).String()

		log.WithField("duration", summary.Duration).
			WithField("shards_ok", summary.ShardsOK).
			WithField("shards_failed", summary.ShardsFailed).
			Info("feeds: rebuild complete")

		status := http.StatusOK
		if summary.ShardsFailed > 0 {
			// Partial failure — 207 Multi-Status so Trustage can
			// distinguish "nothing happened" from "some shards shipped".
			status = http.StatusMultiStatus
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(summary)
	}
}

// rebuildSummary is what we return to the caller and log for ops.
type rebuildSummary struct {
	Duration     string              `json:"duration"`
	ShardsOK     int                 `json:"shards_ok"`
	ShardsFailed int                 `json:"shards_failed"`
	Errors       map[string]string   `json:"errors,omitempty"`
	Written      []indexShardRef     `json:"written"`
}

// rebuildAllManifests drives the full generation — pure function
// exposed separately so tests can exercise it without an HTTP server.
func rebuildAllManifests(
	ctx context.Context,
	jobRepo *repository.JobRepository,
	r2 *publish.R2Publisher,
) rebuildSummary {
	log := util.Log(ctx)
	generatedAt := time.Now().UTC()

	// Fan out the 16 shard generators in parallel with a small cap
	// so we don't saturate the DB connection pool. 4 at a time is
	// a safe default on the api service's typical 16-conn pool.
	const parallel = 4
	sem := make(chan struct{}, parallel)
	var mu sync.Mutex
	var wg sync.WaitGroup

	summary := rebuildSummary{
		Errors:  map[string]string{},
		Written: make([]indexShardRef, 0, len(shardList)+1),
	}

	buildAndUpload := func(country string, langs []string, key string) {
		defer wg.Done()
		sem <- struct{}{}
		defer func() { <-sem }()

		manifest, err := buildShardManifest(ctx, jobRepo, country, langs)
		if err != nil {
			mu.Lock()
			summary.ShardsFailed++
			summary.Errors[country] = err.Error()
			mu.Unlock()
			log.WithError(err).WithField("country", country).
				Warn("feeds: build manifest failed")
			return
		}
		manifest.GeneratedAt = generatedAt
		body, err := json.Marshal(manifest)
		if err != nil {
			mu.Lock()
			summary.ShardsFailed++
			summary.Errors[country] = "marshal: " + err.Error()
			mu.Unlock()
			return
		}
		if err := r2.UploadPublicSnapshot(ctx, key, body); err != nil {
			mu.Lock()
			summary.ShardsFailed++
			summary.Errors[country] = "upload: " + err.Error()
			mu.Unlock()
			log.WithError(err).WithField("country", country).
				Error("feeds: upload failed")
			return
		}
		mu.Lock()
		summary.ShardsOK++
		summary.Written = append(summary.Written, indexShardRef{
			Country:   country,
			Languages: langs,
			Key:       key,
			UpdatedAt: generatedAt,
		})
		mu.Unlock()
	}

	for _, s := range shardList {
		wg.Add(1)
		country, langs := s.Country, s.Languages
		key := fmt.Sprintf("feeds/%s.json", lowercaseASCII(country))
		go buildAndUpload(country, langs, key)
	}

	// Default (unknown-country) manifest — a generic global feed the
	// frontend uses when CF-IPCountry is empty or outside our shard
	// set. Covers the long tail so first paint always has real data.
	wg.Add(1)
	go func() {
		defer wg.Done()
		sem <- struct{}{}
		defer func() { <-sem }()
		manifest, err := buildDefaultManifest(ctx, jobRepo)
		if err != nil {
			mu.Lock()
			summary.ShardsFailed++
			summary.Errors["default"] = err.Error()
			mu.Unlock()
			return
		}
		manifest.GeneratedAt = generatedAt
		body, err := json.Marshal(manifest)
		if err == nil {
			if err := r2.UploadPublicSnapshot(ctx, "feeds/default.json", body); err == nil {
				mu.Lock()
				summary.ShardsOK++
				summary.Written = append(summary.Written, indexShardRef{
					Country:   "",
					Languages: nil,
					Key:       "feeds/default.json",
					UpdatedAt: generatedAt,
				})
				mu.Unlock()
			}
		}
	}()

	wg.Wait()

	// Index file last — captures the successful writes only. A client
	// that fetches the index first can trust any shard listed.
	idx := manifestIndex{
		GeneratedAt: generatedAt,
		Shards:      summary.Written,
	}
	if body, err := json.Marshal(idx); err == nil {
		_ = r2.UploadPublicSnapshot(ctx, "feeds/index.json", body)
	}

	return summary
}

// buildShardManifest runs the tiered query for one country and
// returns the manifest struct ready to marshal. Mirrors feedHandler's
// tier assembly — factored into its own function so the manifest
// writer and the live HTTP handler share a single implementation of
// the cascade logic.
func buildShardManifest(
	ctx context.Context,
	jobRepo *repository.JobRepository,
	country string,
	languages []string,
) (*feedManifest, error) {
	// Keep tier_limit modest for the manifest — bigger buffers mean
	// bigger JSON payloads on slow mobile networks and the user will
	// scroll into /api/feed/tier territory anyway.
	const tierLimit = 20

	region := locale.RegionOf(country)
	regionCountries := locale.CountriesIn(region)

	specs := []tierSpec{}
	seenCountries := []string{}

	// Local.
	localReq := repository.SearchRequest{
		Country: country,
		Sort:    "recent",
		Limit:   tierLimit,
	}
	if len(languages) > 0 {
		localReq.Language = languages[0]
	}
	specs = append(specs, tierSpec{
		id:       "local",
		label:    "Jobs in " + countryDisplayName(country),
		req:      localReq,
		country:  country,
		language: firstOrEmpty(languages),
	})
	seenCountries = append(seenCountries, country)

	// Regional.
	if region != locale.RegionUnknown {
		nearby := filterOut(regionCountries, seenCountries)
		if len(nearby) > 0 {
			specs = append(specs, tierSpec{
				id:        "regional",
				label:     locale.RegionLabel(region),
				req:       repository.SearchRequest{Countries: nearby, Sort: "recent", Limit: tierLimit},
				countries: nearby,
			})
			seenCountries = append(seenCountries, nearby...)
		}
	}

	// Global — everything except countries already covered above.
	specs = append(specs, tierSpec{
		id:    "global",
		label: "Worldwide",
		req: repository.SearchRequest{
			ExcludeCountries: seenCountries,
			Sort:             "recent",
			Limit:            tierLimit,
		},
	})

	tiers := runTiersParallel(ctx, jobRepo, specs)
	return &feedManifest{
		Context: manifestContext{
			Country:   country,
			Languages: languages,
			Region:    region,
		},
		Tiers: keepNonEmpty(tiers),
	}, nil
}

// buildDefaultManifest returns a country-agnostic manifest — just
// "Worldwide" tier with the freshest 20 jobs. Used for users from
// countries not covered by the published shard set.
func buildDefaultManifest(
	ctx context.Context,
	jobRepo *repository.JobRepository,
) (*feedManifest, error) {
	specs := []tierSpec{{
		id:    "global",
		label: "Worldwide",
		req:   repository.SearchRequest{Sort: "recent", Limit: 20},
	}}
	tiers := runTiersParallel(ctx, jobRepo, specs)
	return &feedManifest{
		Context: manifestContext{Region: locale.RegionUnknown},
		Tiers:   keepNonEmpty(tiers),
	}, nil
}

// keepNonEmpty filters out tiers with zero results so the client
// never has to render an empty section with a header over nothing.
func keepNonEmpty(tiers []tieredResponseTier) []manifestTier {
	out := make([]manifestTier, 0, len(tiers))
	for _, t := range tiers {
		if len(t.Jobs) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// lowercaseASCII is strings.ToLower restricted to ASCII — enough for
// ISO-3166 alpha-2 codes, and avoids the Unicode table lookup in the
// hot path of the upload loop.
func lowercaseASCII(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
