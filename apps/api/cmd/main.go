package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	env "github.com/caarlos0/env/v11"
	"github.com/pitabwire/util"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"stawi.jobs/pkg/analytics"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/pipeline/handlers"
	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/repository"
)

// hashIP returns an opaque, one-way hash of the client IP from the
// X-Forwarded-For chain (or RemoteAddr fallback). Used for best-effort
// anon dedup in analytics without storing raw addresses. A daily-
// rotating salt would be stricter, but for MVP a stable hash is enough
// to distinguish sessions within a single query window.
func hashIP(req *http.Request) string {
	ip := req.Header.Get("CF-Connecting-IP")
	if ip == "" {
		ip = req.Header.Get("X-Forwarded-For")
		if i := strings.IndexByte(ip, ','); i >= 0 {
			ip = ip[:i]
		}
		ip = strings.TrimSpace(ip)
	}
	if ip == "" {
		ip = req.RemoteAddr
	}
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:12]) // 12 bytes = 24 hex chars — enough entropy, compact
}

// profileIDFromJWT extracts the `sub` claim from a Bearer token without
// verifying the signature. Verification happens at the gateway layer;
// here we only need the id for attribution. Returns "" for anon requests.
func profileIDFromJWT(req *http.Request) string {
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	token := auth[len("Bearer "):]
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Sub
}

// --- helpers shared by the new /api/* handlers -------------------------------

func parseLimit(s string, def, max int) int {
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		if v > max {
			return max
		}
		return v
	}
	return def
}

func effectiveSort(s, q string) string {
	if s != "" {
		return s
	}
	if q == "" {
		return "recent"
	}
	return "relevance"
}

func encodeCursor(t time.Time, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%d", t.UnixNano(), id)))
}

func decodeCursor(s string) (time.Time, int64, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, false
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, false
	}
	ns, err1 := strconv.ParseInt(parts[0], 10, 64)
	id, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return time.Time{}, 0, false
	}
	return time.Unix(0, ns).UTC(), id, true
}

type apiConfig struct {
	ServerPort        string  `env:"SERVER_PORT" envDefault:":8082"`
	DatabaseURL       string  `env:"DATABASE_URL" envDefault:""`
	InferenceBaseURL  string  `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey   string  `env:"INFERENCE_API_KEY" envDefault:""`
	InferenceModel    string  `env:"INFERENCE_MODEL" envDefault:""`
	OllamaURL         string  `env:"OLLAMA_URL" envDefault:""`
	OllamaModel       string  `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`
	EmbeddingBaseURL  string  `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey   string  `env:"EMBEDDING_API_KEY" envDefault:""`
	EmbeddingModel    string  `env:"EMBEDDING_MODEL" envDefault:""`
	RerankBaseURL     string  `env:"RERANK_BASE_URL" envDefault:""`
	RerankAPIKey      string  `env:"RERANK_API_KEY" envDefault:""`
	RerankModel       string  `env:"RERANK_MODEL" envDefault:""`
	DoDatabaseMigrate bool    `env:"DO_DATABASE_MIGRATE" envDefault:"false"`
	R2AccountID       string  `env:"R2_ACCOUNT_ID" envDefault:""`
	R2AccessKeyID     string  `env:"R2_ACCESS_KEY_ID" envDefault:""`
	R2SecretAccessKey string  `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Bucket          string  `env:"R2_BUCKET" envDefault:"stawi-jobs-content"`
	R2DeployHookURL   string  `env:"R2_DEPLOY_HOOK_URL" envDefault:""`
	PublishMinQuality float64 `env:"PUBLISH_MIN_QUALITY" envDefault:"50"`

	// Analytics (OpenObserve). Blank BaseURL disables the ingest path
	// cleanly; API keeps serving without emitting.
	AnalyticsBaseURL  string `env:"ANALYTICS_BASE_URL" envDefault:""`
	AnalyticsOrg      string `env:"ANALYTICS_ORG" envDefault:"default"`
	AnalyticsUsername string `env:"ANALYTICS_USERNAME" envDefault:""`
	AnalyticsPassword string `env:"ANALYTICS_PASSWORD" envDefault:""`
}

func main() {
	// At the very top of main(), ctx isn't established yet — but
	// util.Log(context.Background()) is still a structured logger
	// with the same output format as the rest of the service. Use it
	// so bootstrap failures show up in the same log stream (and same
	// OpenObserve view) as every other log line.
	ctx := context.Background()
	log := util.Log(ctx)

	var cfg apiConfig
	if err := env.Parse(&cfg); err != nil {
		log.WithError(err).Fatal("api: parse config failed")
	}

	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		log.WithError(err).Fatal("api: open database failed")
	}

	if cfg.DoDatabaseMigrate {
		if err := db.AutoMigrate(
			&domain.Source{},
			&domain.CanonicalJob{},
			&domain.JobVariant{},
		); err != nil {
			log.WithError(err).Fatal("api: auto-migrate failed")
		}
		// GORM AutoMigrate handles column additions from struct tags; this
		// covers the Postgres-specific bits it can't (column drops, generated
		// columns, partial indexes, pg_trgm, materialized views, data backfill).
		if err := repository.FinalizeSchema(db); err != nil {
			log.WithError(err).Fatal("api: finalize schema failed")
		}
		log.Info("api: migration complete")
		return
	}

	dbFn := func(_ context.Context, readOnly bool) *gorm.DB {
		if readOnly {
			return db.Session(&gorm.Session{}).Set("gorm:query_option", "/*readonly*/")
		}
		return db
	}
	sourceRepo := repository.NewSourceRepository(dbFn)
	jobRepo := repository.NewJobRepository(dbFn)

	// Optional AI extractor for semantic search query embeddings. Accepts
	// INFERENCE_* (CF AI Gateway, Groq, OpenAI) or legacy OLLAMA_*.
	var extractor *extraction.Extractor
	infBase, infModel, infKey := extraction.ResolveInference(
		cfg.InferenceBaseURL, cfg.InferenceModel, cfg.InferenceAPIKey,
		cfg.OllamaURL, cfg.OllamaModel,
	)
	if infBase != "" {
		embBase, embModel, embKey := extraction.ResolveEmbedding(
			cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingAPIKey,
			cfg.OllamaURL, cfg.OllamaModel,
		)
		extractor = extraction.New(extraction.Config{
			BaseURL:          infBase,
			APIKey:           infKey,
			Model:            infModel,
			EmbeddingBaseURL: embBase,
			EmbeddingAPIKey:  embKey,
			EmbeddingModel:   embModel,
            RerankBaseURL:    cfg.RerankBaseURL,
            RerankAPIKey:     cfg.RerankAPIKey,
            RerankModel:      cfg.RerankModel,
		})
		log.WithField("url", infBase).WithField("model", infModel).Info("semantic search enabled")
	}

	// Optional R2 publisher for backfill endpoint.
	var r2Publisher *publish.R2Publisher
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" {
		r2Publisher = publish.NewR2Publisher(
			cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey,
			cfg.R2Bucket, cfg.R2DeployHookURL,
		)
		log.WithField("bucket", cfg.R2Bucket).Info("R2 publisher enabled")
	}

	// Analytics ingest. /jobs/{slug}/view emits a server-attributed
	// row carrying profile_id from the JWT, for correlation with the
	// browser RUM stream. The destination-URL reachability check lives
	// in the redirect service (service-files) — it runs inline on
	// /r/{slug} and flips LinkState=EXPIRED on failure, which this
	// service then reflects into canonical_jobs.status via the
	// redirect service's link_expired signal (handled separately).
	analyticsClient := analytics.New(analytics.Config{
		BaseURL:  cfg.AnalyticsBaseURL,
		Org:      cfg.AnalyticsOrg,
		Username: cfg.AnalyticsUsername,
		Password: cfg.AnalyticsPassword,
	})

	mux := http.NewServeMux()

	// GET /healthz — returns counts.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		totalJobs, _ := jobRepo.CountCanonical(ctx)
		totalSources, _ := sourceRepo.Count(ctx)

		// Count all sources (active count is from sourceRepo.Count).
		var allSourceCount int64
		db.Model(&domain.Source{}).Count(&allSourceCount)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"total_jobs":     totalJobs,
			"total_sources":  allSourceCount,
			"active_sources": totalSources,
		})
	})

	// GET /search — legacy path, proxies to the new repository contract.
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		q := req.URL.Query().Get("q")
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 200)

		rows, err := jobRepo.SearchCanonical(ctx, repository.SearchRequest{
			Query: q,
			Limit: limit,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(rows) > limit {
			rows = rows[:limit]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"query":   q,
			"count":   len(rows),
			"results": rows,
		})
	})

	// GET /sources?limit=200
	mux.HandleFunc("GET /sources", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		sources, err := sourceRepo.ListAll(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Apply limit.
		limitStr := req.URL.Query().Get("limit")
		limit := 200
		if limitStr != "" {
			if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
				limit = v
			}
		}
		if len(sources) > limit {
			sources = sources[:limit]
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"count":   len(sources),
			"sources": sources,
		})
	})

	// GET /jobs/top?limit=20&min_score=60 — returns top jobs by quality score.
	mux.HandleFunc("GET /jobs/top", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		limitStr := req.URL.Query().Get("limit")
		limit := 20
		if limitStr != "" {
			if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 200 {
				limit = v
			}
		}
		minScoreStr := req.URL.Query().Get("min_score")
		minScore := 60.0
		if minScoreStr != "" {
			if v, err := strconv.ParseFloat(minScoreStr, 64); err == nil && v >= 0 {
				minScore = v
			}
		}

		jobs, err := jobRepo.TopByQualityScore(ctx, minScore, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"min_score": minScore,
			"count":     len(jobs),
			"results":   jobs,
		})
	})

	// GET /jobs/{id} — single job detail.
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		idStr := req.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, `{"error":"valid job id required"}`, http.StatusBadRequest)
			return
		}

		job, err := jobRepo.GetCanonicalByID(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if job == nil {
			http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(job)
	})

	// OPTIONS preflight for the view beacon. The gateway normally
	// handles CORS for /api/*, but the /jobs/{slug}/view path is called
	// from the browser straight at this service via the public host
	// without the /api prefix, so we answer the preflight ourselves.
	mux.HandleFunc("OPTIONS /jobs/{slug}/view", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(http.StatusNoContent)
	})

	// POST /jobs/{slug}/view — view-event ingest + liveness trigger.
	// The browser's OpenObserve RUM SDK already ships the rich view
	// payload direct to observe.stawi.org; this endpoint exists only
	// to (a) provide a server-side attribution point for the profile
	// JWT and (b) kick off a throttled apply-URL liveness probe.
	// Accepts an empty body; CORS-safe and idempotent.
	mux.HandleFunc("POST /jobs/{slug}/view", func(w http.ResponseWriter, req *http.Request) {
		slug := req.PathValue("slug")
		if slug == "" {
			http.Error(w, `{"error":"slug required"}`, http.StatusBadRequest)
			return
		}
		// CORS: permit any origin; the body carries no secrets and
		// the route is safe to be hit from any Stawi-owned domain.
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Opportunistic analytics ledger entry on the server side.
		// The browser RUM stream carries the client-rich event; this
		// one is the canonical server-attributed record with the
		// profile ID from the JWT when one is present.
		if analyticsClient != nil {
			evt := map[string]any{
				"event":      "server_view",
				"slug":       slug,
				"ip_hash":    hashIP(req),
				"user_agent": req.Header.Get("User-Agent"),
				"referer":    req.Header.Get("Referer"),
				"cf_country": req.Header.Get("CF-IPCountry"),
				"cf_ray":     req.Header.Get("CF-Ray"),
			}
			if profileID := profileIDFromJWT(req); profileID != "" {
				evt["profile_id"] = profileID
			}
			analyticsClient.Send(req.Context(), "stawi_jobs_views", evt)
		}

		w.WriteHeader(http.StatusNoContent)
	})

	// GET /categories — category list with job counts.
	mux.HandleFunc("GET /categories", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		counts, err := jobRepo.CountByCategory(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"categories": counts,
		})
	})

	// GET /stats — live site statistics.
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		totalJobs, _ := jobRepo.CountCanonical(ctx)
		totalSources, _ := sourceRepo.Count(ctx)

		var totalCompanies int64
		db.Model(&domain.CanonicalJob{}).
			Where("status = 'active'").
			Select("COUNT(DISTINCT company)").
			Scan(&totalCompanies)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"total_jobs":      totalJobs,
			"total_companies": totalCompanies,
			"active_sources":  totalSources,
		})
	})

	// ---- new /api/* endpoints for the Hugo shell -------------------------
	facetRepo := repository.NewFacetRepository(dbFn)

	// GET /api/search?q=&category=&remote_type=&sort=&limit=&offset=&cursor=
	mux.HandleFunc("GET /api/search", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		q := strings.TrimSpace(req.URL.Query().Get("q"))
		category := req.URL.Query().Get("category")
		remote := req.URL.Query().Get("remote_type")
		sort := req.URL.Query().Get("sort")
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 100)
		offset := 0
		if v, err := strconv.Atoi(req.URL.Query().Get("offset")); err == nil && v > 0 {
			offset = v
		}
		if offset > 1000 {
			offset = 1000
		}

		var cursorTS *time.Time
		var cursorID int64
		if c := req.URL.Query().Get("cursor"); c != "" {
			if ts, id, ok := decodeCursor(c); ok {
				cursorTS, cursorID = &ts, id
			}
		}

		rows, err := jobRepo.SearchCanonical(ctx, repository.SearchRequest{
			Query:          q,
			Category:       category,
			RemoteType:     remote,
			Sort:           sort,
			Limit:          limit,
			Offset:         offset,
			CursorPostedAt: cursorTS,
			CursorID:       cursorID,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}

		var nextCursor string
		if hasMore && q == "" && len(rows) > 0 {
			last := rows[len(rows)-1]
			if last.PostedAt != nil {
				nextCursor = encodeCursor(*last.PostedAt, last.ID)
			}
		}

		facets, _ := facetRepo.Read(ctx)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":       q,
			"results":     rows,
			"has_more":    hasMore,
			"cursor_next": nextCursor,
			"facets":      facets,
			"sort":        effectiveSort(sort, q),
		})
	})

	// GET /api/feed — tiered, location-aware discovery feed. Returns
	// a cascade of {preferred, local, regional, global} sections so
	// the UI renders locally-relevant jobs at the top without a
	// per-user personalisation round-trip.
	mux.HandleFunc("GET /api/feed", feedHandler(jobRepo, facetRepo))

	// POST /admin/feeds/rebuild — regenerates the per-country feed
	// manifests in R2 (/feeds/<cc>.json + /feeds/default.json +
	// /feeds/index.json). Anon/unfiltered users hit R2 directly for
	// first paint; logged-in / filtered / paginated browsing still
	// goes through /api/feed live. Trustage posts to this every 3h.
	mux.HandleFunc("POST /admin/feeds/rebuild", feedRebuildHandler(jobRepo, r2Publisher))

	// GET /api/feed/tier — cursor-paginated fetch for a single tier
	// after the initial cascade. Caller echoes back the filter scope
	// (country / countries / language) so we don't re-derive it.
	mux.HandleFunc("GET /api/feed/tier", tierPageHandler(jobRepo))

	// GET /api/categories
	mux.HandleFunc("GET /api/categories", func(w http.ResponseWriter, req *http.Request) {
		f, err := facetRepo.Read(req.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"categories": f.Category})
	})

	// GET /api/categories/{slug}/jobs
	mux.HandleFunc("GET /api/categories/{slug}/jobs", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		category := req.PathValue("slug")
		if category == "" {
			http.Error(w, `{"error":"slug required"}`, http.StatusBadRequest)
			return
		}
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 100)
		var cursorTS *time.Time
		var cursorID int64
		if c := req.URL.Query().Get("cursor"); c != "" {
			if ts, id, ok := decodeCursor(c); ok {
				cursorTS, cursorID = &ts, id
			}
		}
		rows, err := jobRepo.SearchCanonical(ctx, repository.SearchRequest{
			Category:       category,
			Sort:           "recent",
			Limit:          limit,
			CursorPostedAt: cursorTS,
			CursorID:       cursorID,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}
		var nextCursor string
		if hasMore && len(rows) > 0 {
			last := rows[len(rows)-1]
			if last.PostedAt != nil {
				nextCursor = encodeCursor(*last.PostedAt, last.ID)
			}
		}
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"category":    category,
			"results":     rows,
			"has_more":    hasMore,
			"cursor_next": nextCursor,
		})
	})

	// GET /api/jobs/latest
	mux.HandleFunc("GET /api/jobs/latest", func(w http.ResponseWriter, req *http.Request) {
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 100)
		rows, err := jobRepo.SearchCanonical(req.Context(), repository.SearchRequest{
			Sort:  "recent",
			Limit: limit,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(rows) > limit {
			rows = rows[:limit]
		}
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
	})

	// GET /api/stats/summary
	mux.HandleFunc("GET /api/stats/summary", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		totalJobs, _ := jobRepo.CountCanonical(ctx)
		totalSources, _ := sourceRepo.Count(ctx)
		var totalCompanies int64
		db.Model(&domain.CanonicalJob{}).
			Where("status = 'active'").
			Select("COUNT(DISTINCT company)").
			Scan(&totalCompanies)
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_jobs":      totalJobs,
			"total_companies": totalCompanies,
			"active_sources":  totalSources,
		})
	})

	// POST /admin/republish?status=active&limit=1000
	// Re-emits publish for canonical rows via the publish handler (not the queue).
	// Runs in a detached goroutine so large re-publish jobs survive CF's 15s
	// gateway timeout; response is 202 Accepted.
	mux.HandleFunc("POST /admin/republish", func(w http.ResponseWriter, req *http.Request) {
		if r2Publisher == nil {
			http.Error(w, `{"error":"R2 not configured"}`, http.StatusServiceUnavailable)
			return
		}
		status := req.URL.Query().Get("status")
		if status == "" {
			status = "active"
		}
		limit := parseLimit(req.URL.Query().Get("limit"), 1000, 1000000)

		purger := publish.NewCachePurger(
			os.Getenv("CLOUDFLARE_ZONE_ID"),
			os.Getenv("CLOUDFLARE_API_TOKEN"),
			"",
		)
		// svc=nil: the backfill endpoint doesn't emit downstream events
		// (no translator fan-out from here). The handler handles that
		// gracefully.
		// redirectClient=nil, redirectBase="": the backfill path runs
		// ad-hoc from ops; we don't mint fresh redirect links on a
		// rebuild — existing canonicals already carry their RedirectSlug
		// from their original publish, and the handler reuses it.
		handler := handlers.NewPublishHandler(jobRepo, r2Publisher, purger, nil, nil, "", cfg.PublishMinQuality)

		go func() {
			// Detached from request context so the long-running loop survives
			// client disconnects (CF gateway timeout).
			ctx := context.Background()
			var lastID int64
			total := 0
			util.Log(ctx).WithField("status", status).WithField("limit", limit).Info("republish: starting")
			for total < limit {
				var rows []*domain.CanonicalJob
				if err := db.
					Where("status = ? AND id > ?", status, lastID).
					Order("id ASC").
					Limit(500).
					Find(&rows).Error; err != nil {
					util.Log(ctx).WithError(err).Warn("republish: query failed")
					return
				}
				if len(rows) == 0 {
					break
				}
				for _, j := range rows {
					if err := handler.Execute(ctx, &handlers.JobReadyPayload{CanonicalJobID: j.ID}); err != nil {
						util.Log(ctx).WithError(err).WithField("canonical_job_id", j.ID).Warn("republish: job failed")
					}
					lastID = j.ID
					total++
					if total >= limit {
						break
					}
				}
				if total%500 == 0 {
					util.Log(ctx).WithField("total", total).WithField("last_id", lastID).Info("republish: progress")
				}
			}
			util.Log(ctx).WithField("total", total).WithField("last_id", lastID).Info("republish: done")
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     status,
			"accepted":   true,
			"limit":      limit,
			"message":    "republish started; tail the pod logs for progress",
		})
	})

	// /search/semantic is removed in the Hugo-millions rebuild. The plain
	// /api/search endpoint (added below) is the supported search path.
	// Semantic ranking can be re-introduced later via pgvector on canonical_jobs.
	_ = extractor // still used in the backfill warm path if present

	// POST /admin/backfill — publish jobs to R2 for Hugo site generation.
	// Query params: from_id, to_id, since (RFC3339 date), batch_size, min_quality, trigger_deploy
	mux.HandleFunc("POST /admin/backfill", func(w http.ResponseWriter, req *http.Request) {
		if r2Publisher == nil {
			http.Error(w, `{"error":"R2 publisher not configured"}`, http.StatusServiceUnavailable)
			return
		}

		ctx := req.Context()
		q := req.URL.Query()

		batchSize := 500
		if v := q.Get("batch_size"); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 5000 {
				batchSize = parsed
			}
		}

		minQuality := cfg.PublishMinQuality
		if v := q.Get("min_quality"); v != "" {
			if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed >= 0 {
				minQuality = parsed
			}
		}

		triggerDeploy := q.Get("trigger_deploy") != "false"

		// Build query conditions.
		query := db.Model(&domain.CanonicalJob{}).
			Where("status = 'active' AND quality_score >= ?", minQuality).
			Order("id ASC")

		if v := q.Get("from_id"); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				query = query.Where("id >= ?", id)
			}
		}
		if v := q.Get("to_id"); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				query = query.Where("id <= ?", id)
			}
		}
		if v := q.Get("since"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				query = query.Where("created_at >= ?", t)
			} else if t, err := time.Parse("2006-01-02", v); err == nil {
				query = query.Where("created_at >= ?", t)
			}
		}

		// Stream results in batches.
		var total, uploaded, skipped int
		offset := 0

		// Set response headers for streaming progress.
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)

		for {
			var jobs []*domain.CanonicalJob
			if err := query.Limit(batchSize).Offset(offset).Find(&jobs).Error; err != nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			if len(jobs) == 0 {
				break
			}

			for _, job := range jobs {
				total++

				// Ensure slug exists.
				if job.Slug == "" {
					job.Slug = domain.BuildSlug(job.Title, job.Company, job.ID)
					_ = jobRepo.UpdateCanonicalFields(ctx, job.ID, map[string]any{"slug": job.Slug})
				}

				descHTML := publish.RenderDescriptionHTML(job.Description)
				snap := domain.BuildSnapshotWithHTML(job, descHTML)
				body, merr := json.Marshal(snap)
				if merr != nil {
					util.Log(ctx).WithError(merr).WithField("canonical_job_id", job.ID).Warn("backfill: marshal failed")
					skipped++
					continue
				}
				key := "jobs/" + job.Slug + ".json"
				if err := r2Publisher.UploadPublicSnapshot(ctx, key, body); err != nil {
					util.Log(ctx).WithError(err).WithField("r2_key", key).Warn("backfill: upload failed")
					skipped++
					continue
				}
				uploaded++
			}

			// Stream progress.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"progress": true,
				"offset":   offset,
				"batch":    len(jobs),
				"uploaded": uploaded,
				"skipped":  skipped,
			})
			if flusher != nil {
				flusher.Flush()
			}

			offset += batchSize
		}

		// Trigger deploy hook if requested.
		if triggerDeploy && uploaded > 0 {
			if err := r2Publisher.TriggerDeploy(); err != nil {
				util.Log(ctx).WithError(err).Warn("backfill: deploy hook failed")
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"done":     true,
			"total":    total,
			"uploaded": uploaded,
			"skipped":  skipped,
			"deployed": triggerDeploy && uploaded > 0,
		})
	})

	srv := &http.Server{
		Addr:    cfg.ServerPort,
		Handler: mux,
	}

	go func() {
		util.Log(ctx).WithField("port", cfg.ServerPort).Info("API server listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			util.Log(ctx).WithError(err).Fatal("http server: exited with error")
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	util.Log(ctx).Info("shutting down API")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		util.Log(shutdownCtx).WithError(err).Warn("http shutdown failed")
	}

	util.Log(ctx).Info("API stopped")
}

