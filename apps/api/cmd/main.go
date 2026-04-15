package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	env "github.com/caarlos0/env/v11"
	"github.com/go-chi/chi/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
)

type apiConfig struct {
	ServerPort  string `env:"SERVER_PORT" envDefault:":8082"`
	DatabaseURL string `env:"DATABASE_URL" envDefault:""`
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
