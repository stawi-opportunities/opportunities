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
	"github.com/go-chi/chi/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
)

type apiConfig struct {
	ServerPort  string `env:"SERVER_PORT" envDefault:":8082"`
	DatabaseURL string `env:"DATABASE_URL" envDefault:""`
	OllamaURL   string `env:"OLLAMA_URL" envDefault:""`
	OllamaModel string `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`
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

	if err := db.AutoMigrate(
		&domain.Source{},
		&domain.CanonicalJob{},
		&domain.JobVariant{},
	); err != nil {
		log.Fatalf("auto-migrate: %v", err)
	}

	dbFn := func(_ context.Context, _ bool) *gorm.DB { return db }
	sourceRepo := repository.NewSourceRepository(dbFn)
	jobRepo := repository.NewJobRepository(dbFn)

	// Optional AI extractor for semantic search query embeddings.
	var extractor *extraction.Extractor
	if cfg.OllamaURL != "" {
		extractor = extraction.NewExtractor(cfg.OllamaURL, cfg.OllamaModel)
		log.Printf("Semantic search enabled: url=%s model=%s", cfg.OllamaURL, cfg.OllamaModel)
	}

	r := chi.NewRouter()

	// GET /healthz — returns counts.
	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
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
	r.Get("/search", func(w http.ResponseWriter, req *http.Request) {
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
	r.Get("/sources", func(w http.ResponseWriter, req *http.Request) {
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

	// GET /search/semantic?q=<text>&limit=20
	// Hybrid search: tsvector candidates re-ranked by embedding cosine similarity.
	r.Get("/search/semantic", func(w http.ResponseWriter, req *http.Request) {
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

	srv := &http.Server{
		Addr:    cfg.ServerPort,
		Handler: r,
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
