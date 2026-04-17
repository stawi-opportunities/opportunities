package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	env "github.com/caarlos0/env/v11"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/repository"
)

type schedulerConfig struct {
	ServerPort  string `env:"SERVER_PORT" envDefault:":8081"`
	DatabaseURL string `env:"DATABASE_URL" envDefault:""`

	// R2 + Cloudflare — optional; if missing, retention stage 2 is skipped.
	R2AccountID        string `env:"R2_ACCOUNT_ID" envDefault:""`
	R2AccessKeyID      string `env:"R2_ACCESS_KEY_ID" envDefault:""`
	R2SecretAccessKey  string `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Bucket           string `env:"R2_BUCKET" envDefault:"stawi-jobs-content"`
	ContentOrigin      string `env:"CONTENT_ORIGIN" envDefault:"https://jobs-repo.stawi.org"`
	CloudflareZoneID   string `env:"CLOUDFLARE_ZONE_ID" envDefault:""`
	CloudflareAPIToken string `env:"CLOUDFLARE_API_TOKEN" envDefault:""`

	RetentionGraceDays int `env:"RETENTION_GRACE_DAYS" envDefault:"7"`
}

func main() {
	var cfg schedulerConfig
	if err := env.Parse(&cfg); err != nil {
		log.Fatalf("parse config: %v", err)
	}

	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	if err := db.AutoMigrate(&domain.Source{}); err != nil {
		log.Fatalf("auto-migrate: %v", err)
	}

	dbFn := func(_ context.Context, _ bool) *gorm.DB { return db }
	sourceRepo := repository.NewSourceRepository(dbFn)
	facetRepo := repository.NewFacetRepository(dbFn)
	retentionRepo := repository.NewRetentionRepository(dbFn)

	var r2Publisher *publish.R2Publisher
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" {
		r2Publisher = publish.NewR2Publisher(
			cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey,
			cfg.R2Bucket, "",
		)
		publish.SetContentOrigin(cfg.ContentOrigin)
	}
	purger := publish.NewCachePurger(cfg.CloudflareZoneID, cfg.CloudflareAPIToken, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Background scheduling monitor.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now().UTC()
				due, err := sourceRepo.ListDue(ctx, now, 1000)
				if err != nil {
					log.Printf("ERROR: list due: %v", err)
					continue
				}
				log.Printf("scheduler tick: %d sources due", len(due))
			}
		}
	}()

	// Retention: expire stage 1 every 15 min.
	go runEvery(ctx, 15*time.Minute, func(ctx context.Context) {
		runExpire(ctx, retentionRepo)
	})

	// mv_job_facets refresh every 5 min.
	go runEvery(ctx, 5*time.Minute, func(ctx context.Context) {
		runMVRefresh(ctx, facetRepo)
	})

	// Retention stage 2 daily.
	go runEvery(ctx, 24*time.Hour, func(ctx context.Context) {
		runRetention(ctx, retentionRepo, r2Publisher, purger, cfg.RetentionGraceDays)
	})

	// Health endpoint.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	srv := &http.Server{
		Addr:    cfg.ServerPort,
		Handler: mux,
	}

	go func() {
		log.Printf("scheduler HTTP listening on %s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down scheduler...")

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	log.Println("scheduler stopped")
}
