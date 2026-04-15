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
	"stawi.jobs/pkg/repository"
)

type schedulerConfig struct {
	ServerPort  string `env:"SERVER_PORT" envDefault:":8081"`
	DatabaseURL string `env:"DATABASE_URL" envDefault:""`
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
