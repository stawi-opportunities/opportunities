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
	"stawi.jobs/pkg/extraction"
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

	// AI extractor (optional — only enabled when OLLAMA_URL is set).
	var extractor *extraction.Extractor
	if cfg.OllamaURL != "" {
		extractor = extraction.NewExtractor(cfg.OllamaURL, cfg.OllamaModel)
		log.Printf("AI extraction enabled: url=%s model=%s", cfg.OllamaURL, cfg.OllamaModel)
	}

	// Connector registry.
	registry := service.BuildRegistry(httpClient, extractor)

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
	go crawlLoop(ctx, cfg.WorkerConcurrency, sourceRepo, crawlRepo, jobRepo, rejectedRepo, registry, dedupeEngine, batchBuf, httpClient, extractor)


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
	jobRepo *repository.JobRepository,
	rejectedRepo *repository.RejectedJobRepository,
	registry *connectors.Registry,
	dedupeEngine *dedupe.Engine,
	batchBuf *pipeline.BatchBuffer,
	httpClient *httpx.Client,
	extractor *extraction.Extractor,
) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Process once immediately, then on every tick.
	processDueSources(ctx, concurrency, sourceRepo, crawlRepo, jobRepo, rejectedRepo, registry, dedupeEngine, batchBuf, httpClient, extractor)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processDueSources(ctx, concurrency, sourceRepo, crawlRepo, jobRepo, rejectedRepo, registry, dedupeEngine, batchBuf, httpClient, extractor)
		}
	}
}

func processDueSources(
	ctx context.Context,
	concurrency int,
	sourceRepo *repository.SourceRepository,
	crawlRepo *repository.CrawlRepository,
	jobRepo *repository.JobRepository,
	rejectedRepo *repository.RejectedJobRepository,
	registry *connectors.Registry,
	dedupeEngine *dedupe.Engine,
	batchBuf *pipeline.BatchBuffer,
	httpClient *httpx.Client,
	extractor *extraction.Extractor,
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

			processSource(ctx, s, sourceRepo, crawlRepo, jobRepo, rejectedRepo, registry, dedupeEngine, batchBuf, httpClient, extractor)
		}(src)
	}

	wg.Wait()
}

func processSource(
	ctx context.Context,
	src *domain.Source,
	sourceRepo *repository.SourceRepository,
	crawlRepo *repository.CrawlRepository,
	jobRepo *repository.JobRepository,
	rejectedRepo *repository.RejectedJobRepository,
	registry *connectors.Registry,
	dedupeEngine *dedupe.Engine,
	batchBuf *pipeline.BatchBuffer,
	httpClient *httpx.Client,
	extractor *extraction.Extractor,
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

			// For HTML-based sources, fetch the detail page and use AI to extract fields.
			if extractor != nil && needsAIExtraction(src.Type) && ext.ApplyURL != "" {
				detailHTML, _, fetchErr := httpClient.Get(ctx, ext.ApplyURL, nil)
				if fetchErr == nil {
					if fields, aiErr := extractor.Extract(ctx, string(detailHTML), ext.ApplyURL); aiErr == nil {
						mergeExtractedFields(&ext, fields)
					} else {
						log.Printf("processSource: AI extraction failed for %s: %v", ext.ApplyURL, aiErr)
					}
				} else {
					log.Printf("processSource: fetch detail page failed for %s: %v", ext.ApplyURL, fetchErr)
				}
			}

			// Quality gate.
			if qErr := quality.Check(ext); qErr != nil {
				_ = rejectedRepo.Create(ctx, &domain.RejectedJob{
					CrawlJobID: crawlJob.ID,
					SourceID:   src.ID,
					ExternalID: ext.ExternalID,
					Reason:     qErr.Error(),
					RejectedAt: time.Now().UTC(),
				})
				continue
			}

			// Normalize to variant.
			variant := normalize.ExternalToVariant(ext, src.ID, src.Country, string(src.Type), time.Now().UTC())

			// Dedupe and persist canonical.
			canonical, err := dedupeEngine.UpsertAndCluster(ctx, &variant)
			if err != nil {
				log.Printf("ERROR: dedupe source %d ext %s: %v", src.ID, ext.ExternalID, err)
				continue
			}

			// Generate embedding asynchronously if extractor is available.
			if extractor != nil && canonical != nil && canonical.ID > 0 {
				go generateEmbedding(ctx, extractor, jobRepo, canonical)
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

// generateEmbedding creates an embedding for a canonical job and stores it.
// This runs asynchronously and logs errors silently to avoid blocking the pipeline.
func generateEmbedding(ctx context.Context, ext *extraction.Extractor, repo *repository.JobRepository, cj *domain.CanonicalJob) {
	text := cj.Title + " " + cj.Company + " " + cj.Description
	embedding, err := ext.Embed(ctx, text)
	if err != nil {
		log.Printf("WARN: embedding for canonical %d: %v", cj.ID, err)
		return
	}
	embJSON, err := json.Marshal(embedding)
	if err != nil {
		log.Printf("WARN: marshal embedding for canonical %d: %v", cj.ID, err)
		return
	}
	if err := repo.UpdateEmbedding(ctx, cj.ID, string(embJSON)); err != nil {
		log.Printf("WARN: store embedding for canonical %d: %v", cj.ID, err)
	}
}

// mergeExtractedFields copies non-empty fields from JobFields into an ExternalJob,
// only filling fields that are currently empty (AI enrichment, not overwrite).
func mergeExtractedFields(job *domain.ExternalJob, fields *extraction.JobFields) {
	if job.Title == "" && fields.Title != "" {
		job.Title = fields.Title
	}
	if job.Company == "" && fields.Company != "" {
		job.Company = fields.Company
	}
	if job.LocationText == "" && fields.Location != "" {
		job.LocationText = fields.Location
	}
	if job.Description == "" && fields.Description != "" {
		job.Description = fields.Description
	}
	if job.ApplyURL == "" && fields.ApplyURL != "" {
		job.ApplyURL = fields.ApplyURL
	}
	if job.EmploymentType == "" && fields.EmploymentType != "" {
		job.EmploymentType = fields.EmploymentType
	}
	if job.RemoteType == "" && fields.RemoteType != "" {
		job.RemoteType = fields.RemoteType
	}
	if job.Currency == "" && fields.Currency != "" {
		job.Currency = fields.Currency
	}
}

// needsAIExtraction returns true for HTML-based source types where the connector
// only discovers links and AI should be used to extract fields from detail pages.
func needsAIExtraction(st domain.SourceType) bool {
	switch st {
	case domain.SourceBrighterMonday, domain.SourceJobberman,
		domain.SourceMyJobMag, domain.SourceNjorku,
		domain.SourceCareers24, domain.SourcePNet,
		domain.SourceSchemaOrg, domain.SourceSitemap,
		domain.SourceHostedBoards, domain.SourceGenericHTML:
		return true
	default:
		return false
	}
}
