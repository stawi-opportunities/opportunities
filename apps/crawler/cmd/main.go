package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	env "github.com/caarlos0/env/v11"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"stawi.jobs/apps/crawler/config"
	"stawi.jobs/apps/crawler/service"
	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/dedupe"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/normalize"
	"stawi.jobs/pkg/pipeline"
	"stawi.jobs/pkg/quality"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/seeds"
)

func main() {
	var cfg config.CrawlerConfig
	if err := env.Parse(&cfg); err != nil {
		log.Fatalf("parse config: %v", err)
	}

	// Database connection.
	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	// Auto-migrate all domain tables.
	if err := db.AutoMigrate(
		&domain.Source{},
		&domain.CrawlJob{},
		&domain.RawPayload{},
		&domain.JobVariant{},
		&domain.JobCluster{},
		&domain.JobClusterMember{},
		&domain.CanonicalJob{},
		&domain.CrawlPageState{},
		&domain.RejectedJob{},
	); err != nil {
		log.Fatalf("auto-migrate: %v", err)
	}

	// Build the DB accessor that repositories expect.
	dbFn := func(_ context.Context, _ bool) *gorm.DB {
		return db
	}

	// Repositories.
	sourceRepo := repository.NewSourceRepository(dbFn)
	crawlRepo := repository.NewCrawlRepository(dbFn)
	jobRepo := repository.NewJobRepository(dbFn)
	rejectedRepo := repository.NewRejectedJobRepository(dbFn)

	// Load seed sources.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n, err := seeds.LoadAndUpsert(ctx, cfg.SeedsDir, sourceRepo)
	if err != nil {
		log.Printf("WARN: seed loading: %v (loaded %d)", err, n)
	} else {
		log.Printf("loaded %d seed sources", n)
	}

	// HTTP client for connectors.
	httpClient := httpx.NewClient(
		time.Duration(cfg.HTTPTimeoutSec)*time.Second,
		cfg.UserAgent,
	)

	// Connector registry.
	registry := service.BuildRegistry(httpClient)

	// Dedupe engine.
	dedupeEngine := dedupe.NewEngine(jobRepo)

	// Batch buffer.
	batchBuf := pipeline.NewBatchBuffer(
		jobRepo,
		cfg.BatchSize,
		time.Duration(cfg.BatchFlushSec)*time.Second,
	)

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

	// Start HTTP server in background.
	go func() {
		log.Printf("HTTP server listening on %s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	// Start crawl scheduling loop.
	go crawlLoop(ctx, cfg.WorkerConcurrency, sourceRepo, crawlRepo, rejectedRepo, registry, dedupeEngine, batchBuf, httpClient)

	// Graceful shutdown.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down...")

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	if err := batchBuf.Close(shutdownCtx); err != nil {
		log.Printf("batch buffer close: %v", err)
	}

	log.Println("crawler stopped")
}

// crawlLoop runs a ticker that finds due sources and processes them with
// bounded concurrency.
func crawlLoop(
	ctx context.Context,
	concurrency int,
	sourceRepo *repository.SourceRepository,
	crawlRepo *repository.CrawlRepository,
	rejectedRepo *repository.RejectedJobRepository,
	registry *connectors.Registry,
	dedupeEngine *dedupe.Engine,
	batchBuf *pipeline.BatchBuffer,
	_ *httpx.Client,
) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Process once immediately, then on every tick.
	processDueSources(ctx, concurrency, sourceRepo, crawlRepo, rejectedRepo, registry, dedupeEngine, batchBuf)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processDueSources(ctx, concurrency, sourceRepo, crawlRepo, rejectedRepo, registry, dedupeEngine, batchBuf)
		}
	}
}

func processDueSources(
	ctx context.Context,
	concurrency int,
	sourceRepo *repository.SourceRepository,
	crawlRepo *repository.CrawlRepository,
	rejectedRepo *repository.RejectedJobRepository,
	registry *connectors.Registry,
	dedupeEngine *dedupe.Engine,
	batchBuf *pipeline.BatchBuffer,
) {
	now := time.Now().UTC()
	sources, err := sourceRepo.ListDue(ctx, now, 100)
	if err != nil {
		log.Printf("ERROR: list due sources: %v", err)
		return
	}
	if len(sources) == 0 {
		return
	}

	log.Printf("found %d due sources", len(sources))

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, src := range sources {
		wg.Add(1)
		sem <- struct{}{}

		go func(s *domain.Source) {
			defer wg.Done()
			defer func() { <-sem }()

			processSource(ctx, s, sourceRepo, crawlRepo, rejectedRepo, registry, dedupeEngine, batchBuf)
		}(src)
	}

	wg.Wait()
}

func processSource(
	ctx context.Context,
	src *domain.Source,
	sourceRepo *repository.SourceRepository,
	crawlRepo *repository.CrawlRepository,
	rejectedRepo *repository.RejectedJobRepository,
	registry *connectors.Registry,
	dedupeEngine *dedupe.Engine,
	batchBuf *pipeline.BatchBuffer,
) {
	conn, ok := registry.Get(src.Type)
	if !ok {
		log.Printf("WARN: no connector for source type %q (source %d)", src.Type, src.ID)
		return
	}

	now := time.Now().UTC()

	// Create crawl job record.
	crawlJob := &domain.CrawlJob{
		SourceID:       src.ID,
		ScheduledAt:    now,
		Status:         domain.CrawlScheduled,
		Attempt:        1,
		IdempotencyKey: fmt.Sprintf("%d-%d", src.ID, now.UnixNano()),
	}
	if err := crawlRepo.Create(ctx, crawlJob); err != nil {
		log.Printf("ERROR: create crawl job for source %d: %v", src.ID, err)
		return
	}
	if err := crawlRepo.Start(ctx, crawlJob.ID); err != nil {
		log.Printf("ERROR: start crawl job %d: %v", crawlJob.ID, err)
		return
	}

	// Run the connector.
	iter := conn.Crawl(ctx, *src)
	var jobsFound, jobsStored int

	for iter.Next(ctx) {
		for _, ext := range iter.Jobs() {
			jobsFound++

			// Quality gate.
			if err := quality.Check(ext); err != nil {
				_ = rejectedRepo.Create(ctx, &domain.RejectedJob{
					CrawlJobID: crawlJob.ID,
					SourceID:   src.ID,
					ExternalID: ext.ExternalID,
					Reason:     err.Error(),
					RejectedAt: time.Now().UTC(),
				})
				continue
			}

			// Normalize to variant.
			variant := normalize.ExternalToVariant(ext, src.ID, src.Country, string(src.Type), time.Now().UTC())

			// Dedupe and persist canonical.
			if _, err := dedupeEngine.UpsertAndCluster(ctx, &variant); err != nil {
				log.Printf("ERROR: dedupe source %d ext %s: %v", src.ID, ext.ExternalID, err)
				continue
			}

			// Add to batch buffer.
			if err := batchBuf.Add(ctx, variant); err != nil {
				log.Printf("ERROR: batch add source %d: %v", src.ID, err)
			}

			jobsStored++
		}
	}

	if err := iter.Err(); err != nil {
		log.Printf("ERROR: crawl source %d: %v", src.ID, err)
		_ = crawlRepo.Finish(ctx, crawlJob.ID, domain.CrawlFailed, err.Error())
	} else {
		_ = crawlRepo.Finish(ctx, crawlJob.ID, domain.CrawlSucceeded, "")
	}

	// Update source scheduling.
	nextCrawl := time.Now().UTC().Add(time.Duration(src.CrawlIntervalSec) * time.Second)
	_ = sourceRepo.UpdateNextCrawl(ctx, src.ID, nextCrawl, time.Now().UTC(), src.HealthScore)

	log.Printf("source %d (%s): found=%d stored=%d", src.ID, src.Type, jobsFound, jobsStored)
}
