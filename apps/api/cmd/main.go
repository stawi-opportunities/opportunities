package main

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"

	env "github.com/caarlos0/env/v11"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/repository"
)

type apiConfig struct {
	ServerPort        string  `env:"SERVER_PORT" envDefault:":8082"`
	DatabaseURL       string  `env:"DATABASE_URL" envDefault:""`
	OllamaURL         string  `env:"OLLAMA_URL" envDefault:""`
	OllamaModel       string  `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`
	DoDatabaseMigrate bool    `env:"DO_DATABASE_MIGRATE" envDefault:"false"`
	R2AccountID       string  `env:"R2_ACCOUNT_ID" envDefault:""`
	R2AccessKeyID     string  `env:"R2_ACCESS_KEY_ID" envDefault:""`
	R2SecretAccessKey string  `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Bucket          string  `env:"R2_BUCKET" envDefault:"stawi-jobs-content"`
	R2DeployHookURL   string  `env:"R2_DEPLOY_HOOK_URL" envDefault:""`
	PublishMinQuality float64 `env:"PUBLISH_MIN_QUALITY" envDefault:"50"`
}

func main() {
	var cfg apiConfig
	if err := env.Parse(&cfg); err != nil {
		log.Fatalf("parse config: %v", err)
	}

	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	if cfg.DoDatabaseMigrate {
		if err := db.AutoMigrate(
			&domain.Source{},
			&domain.CanonicalJob{},
			&domain.JobVariant{},
		); err != nil {
			log.Fatalf("auto-migrate: %v", err)
		}
		log.Println("migration complete")
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

	// Optional AI extractor for semantic search query embeddings.
	var extractor *extraction.Extractor
	if cfg.OllamaURL != "" {
		extractor = extraction.NewExtractor(cfg.OllamaURL, cfg.OllamaModel)
		log.Printf("Semantic search enabled: url=%s model=%s", cfg.OllamaURL, cfg.OllamaModel)
	}

	// Optional R2 publisher for backfill endpoint.
	var r2Publisher *publish.R2Publisher
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" {
		r2Publisher = publish.NewR2Publisher(
			cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey,
			cfg.R2Bucket, cfg.R2DeployHookURL,
		)
		log.Printf("R2 publisher enabled: bucket=%s", cfg.R2Bucket)
	}

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

	// GET /search?q=&limit=20
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		q := req.URL.Query().Get("q")
		limitStr := req.URL.Query().Get("limit")
		limit := 20
		if limitStr != "" {
			if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 200 {
				limit = v
			}
		}

		jobs, err := jobRepo.SearchCanonical(ctx, q, limit, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"query":   q,
			"count":   len(jobs),
			"results": jobs,
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
			Where("is_active = true").
			Select("COUNT(DISTINCT company)").
			Scan(&totalCompanies)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"total_jobs":      totalJobs,
			"total_companies": totalCompanies,
			"active_sources":  totalSources,
		})
	})

	// GET /search/semantic?q=<text>&limit=20
	// Hybrid search: tsvector candidates re-ranked by embedding cosine similarity.
	mux.HandleFunc("GET /search/semantic", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		q := req.URL.Query().Get("q")
		if q == "" {
			http.Error(w, `{"error":"query parameter q is required"}`, http.StatusBadRequest)
			return
		}

		limitStr := req.URL.Query().Get("limit")
		limit := 20
		if limitStr != "" {
			if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 200 {
				limit = v
			}
		}

		if extractor == nil {
			http.Error(w, `{"error":"semantic search not available (OLLAMA_URL not configured)"}`, http.StatusServiceUnavailable)
			return
		}

		// Step 1: Get tsvector candidates (fetch more than limit for re-ranking).
		candidateLimit := limit * 5
		if candidateLimit < 100 {
			candidateLimit = 100
		}
		candidates, err := jobRepo.SearchCanonical(ctx, q, candidateLimit, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Step 2: Embed the query.
		queryEmbedding, err := extractor.Embed(ctx, q)
		if err != nil {
			// Fall back to tsvector results if embedding fails.
			if len(candidates) > limit {
				candidates = candidates[:limit]
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"query":    q,
				"count":    len(candidates),
				"results":  candidates,
				"semantic": false,
			})
			return
		}

		// Step 3: Re-rank candidates by cosine similarity.
		type scored struct {
			job   *domain.CanonicalJob
			score float64
		}
		var scored_jobs []scored
		for _, job := range candidates {
			if job.Embedding == "" {
				continue
			}
			var jobEmb []float32
			if err := json.Unmarshal([]byte(job.Embedding), &jobEmb); err != nil {
				continue
			}
			sim := cosineSimilarity(queryEmbedding, jobEmb)
			scored_jobs = append(scored_jobs, scored{job: job, score: sim})
		}

		sort.Slice(scored_jobs, func(i, j int) bool {
			return scored_jobs[i].score > scored_jobs[j].score
		})

		if len(scored_jobs) > limit {
			scored_jobs = scored_jobs[:limit]
		}

		results := make([]*domain.CanonicalJob, len(scored_jobs))
		for i, s := range scored_jobs {
			results[i] = s.job
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"query":    q,
			"count":    len(results),
			"results":  results,
			"semantic": true,
		})
	})

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
			Where("is_active = true AND quality_score >= ?", minQuality).
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
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
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
					log.Printf("backfill: marshal %d: %v", job.ID, merr)
					skipped++
					continue
				}
				key := "jobs/" + job.Slug + ".json"
				if err := r2Publisher.UploadPublicSnapshot(ctx, key, body); err != nil {
					log.Printf("backfill: failed to upload %s: %v", key, err)
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
				log.Printf("backfill: deploy hook failed: %v", err)
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
		log.Printf("API server listening on %s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down API...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	log.Println("API stopped")
}

// cosineSimilarity computes the cosine similarity between two float32 vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dotProduct / denom
}
