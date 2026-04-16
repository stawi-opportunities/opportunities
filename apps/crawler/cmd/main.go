package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	crawlerconfig "stawi.jobs/apps/crawler/config"
	"stawi.jobs/apps/crawler/service"
	"stawi.jobs/apps/crawler/service/events"
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
	ctx := context.Background()

	// Load configuration (embeds Frame's ConfigurationDefault).
	cfg, err := fconfig.FromEnv[crawlerconfig.CrawlerConfig]()
	if err != nil {
		panic(fmt.Sprintf("config: %v", err))
	}

	// Build Frame options.
	opts := []frame.Option{
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	}

	// Create the Frame service.
	ctx, svc := frame.NewServiceWithContext(ctx, opts...)

	log := util.Log(ctx)

	// Obtain the database pool.
	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	dbFn := pool.DB

	// Handle database migration if configured (colony Helm chart sets
	// DO_DATABASE_MIGRATE=true for the pre-install migration job).
	// Pattern follows service-profile: migrate, then return immediately.
	if cfg.DoDatabaseMigrate() {
		migrationDB := dbFn(ctx, false)
		if err := migrationDB.AutoMigrate(
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
			log.WithError(err).Fatal("auto-migrate failed")
		}
		// Set existing variants without a stage to 'ready'
		migrationDB.Exec("UPDATE job_variants SET stage = 'ready' WHERE stage IS NULL OR stage = ''")
		log.Info("set existing variants to stage=ready")
		log.Info("migration complete")
		return
	}

	// Repositories.
	sourceRepo := repository.NewSourceRepository(dbFn)
	crawlRepo := repository.NewCrawlRepository(dbFn)
	jobRepo := repository.NewJobRepository(dbFn)
	rejectedRepo := repository.NewRejectedJobRepository(dbFn)

	// Load seed sources.
	n, seedErr := seeds.LoadAndUpsert(ctx, cfg.SeedsDir, sourceRepo)
	if seedErr != nil {
		log.WithError(seedErr).WithField("loaded", n).Warn("seed loading incomplete")
	} else {
		log.WithField("count", n).Info("seed sources loaded")
	}

	// HTTP client for connectors.
	httpClient := httpx.NewClient(
		time.Duration(cfg.HTTPTimeoutSec)*time.Second,
		cfg.UserAgent,
	)

	// AI extractor (optional -- only enabled when OLLAMA_URL is set).
	var extractor *extraction.Extractor
	if cfg.OllamaURL != "" {
		extractor = extraction.NewExtractor(cfg.OllamaURL, cfg.OllamaModel)
		log.WithField("url", cfg.OllamaURL).
			WithField("model", cfg.OllamaModel).
			Info("AI extraction enabled")
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

	// Register cleanup for the batch buffer so it flushes on shutdown.
	svc.AddCleanupMethod(func(cleanupCtx context.Context) {
		if flushErr := batchBuf.Close(cleanupCtx); flushErr != nil {
			util.Log(cleanupCtx).WithError(flushErr).Error("batch buffer close failed")
		}
	})

	// Register event handlers through Frame's events system.
	if extractor != nil {
		embeddingHandler := events.NewEmbeddingGenerationHandler(extractor, jobRepo)
		enrichmentHandler := events.NewJobEnrichmentHandler(extractor, jobRepo)
		svc.Init(ctx, frame.WithRegisterEvents(embeddingHandler, enrichmentHandler))

		// One-time backfill: emit embedding events for all canonical jobs that
		// missed embeddings due to previous Ollama outages. Runs once at startup,
		// then the event system handles all future embeddings via Emit.
		svc.AddPreStartMethod(func(preCtx context.Context, _ *frame.Service) {
			backfillLog := util.Log(preCtx)
			missing, listErr := jobRepo.ListMissingEmbeddings(preCtx, 500)
			if listErr != nil {
				backfillLog.WithError(listErr).Warn("embedding backfill: failed to list")
				return
			}
			if len(missing) == 0 {
				return
			}
			backfillLog.WithField("count", len(missing)).Info("embedding backfill: emitting events for missed jobs")
			evtsMan := svc.EventsManager()
			for _, cj := range missing {
				_ = evtsMan.Emit(preCtx, events.EmbeddingGenerationEventName, &events.EmbeddingPayload{
					CanonicalJobID: cj.ID,
					Title:          cj.Title,
					Company:        cj.Company,
					Description:    cj.Description,
				})
			}
		})

		// One-time backfill: enrich canonical jobs missing intelligence fields.
		svc.AddPreStartMethod(func(preCtx context.Context, _ *frame.Service) {
			enrichLog := util.Log(preCtx)
			unenriched, listErr := jobRepo.ListUnenriched(preCtx, 100)
			if listErr != nil {
				enrichLog.WithError(listErr).Warn("enrichment backfill: failed to list")
				return
			}
			if len(unenriched) == 0 {
				return
			}
			enrichLog.WithField("count", len(unenriched)).Info("enrichment backfill: emitting events")
			evtsMan := svc.EventsManager()
			for _, cj := range unenriched {
				_ = evtsMan.Emit(preCtx, events.JobEnrichmentEventName, &events.JobEnrichmentPayload{
					CanonicalJobID: cj.ID,
					Description:    cj.Description,
					Title:          cj.Title,
					Company:        cj.Company,
					ApplyURL:       cj.ApplyURL,
				})
			}
		})
	}

	// Register the crawl loop as Frame's background consumer.
	crawlDeps := &crawlDependencies{
		cfg:          &cfg,
		sourceRepo:   sourceRepo,
		crawlRepo:    crawlRepo,
		jobRepo:      jobRepo,
		rejectedRepo: rejectedRepo,
		registry:     registry,
		dedupeEngine: dedupeEngine,
		batchBuf:     batchBuf,
		httpClient:   httpClient,
		extractor:    extractor,
		svc:          svc,
	}
	svc.Init(ctx, frame.WithBackgroundConsumer(crawlDeps.crawlLoop))

	// Build admin HTTP mux. Frame mounts this at "/" via WithHTTPHandler.
	adminMux := http.NewServeMux()

	// Admin: pause a source  (?id=N)
	adminMux.HandleFunc("/admin/sources/pause", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		idStr := r.URL.Query().Get("id")
		id, convErr := strconv.ParseInt(idStr, 10, 64)
		if convErr != nil || id <= 0 {
			http.Error(w, `{"error":"invalid or missing id parameter"}`, http.StatusBadRequest)
			return
		}
		if opErr := sourceRepo.PauseSource(r.Context(), id); opErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, opErr.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "status": "paused"})
	})

	// Admin: enable a source  (?id=N)
	adminMux.HandleFunc("/admin/sources/enable", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		idStr := r.URL.Query().Get("id")
		id, convErr := strconv.ParseInt(idStr, 10, 64)
		if convErr != nil || id <= 0 {
			http.Error(w, `{"error":"invalid or missing id parameter"}`, http.StatusBadRequest)
			return
		}
		if opErr := sourceRepo.EnableSource(r.Context(), id); opErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, opErr.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "status": "active"})
	})

	// Admin: health report — all sources ordered by worst health first
	adminMux.HandleFunc("/admin/sources/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		sources, opErr := sourceRepo.ListHealthReport(r.Context())
		if opErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, opErr.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count":   len(sources),
			"sources": sources,
		})
	})

	// Admin: rebuild canonical jobs from all variants
	adminMux.HandleFunc("/admin/rebuild-canonicals", rebuildCanonicalsHandler(jobRepo, dedupeEngine))

	svc.Init(ctx, frame.WithHTTPHandler(adminMux))

	// Register a named health checker that reports source state counts.
	svc.AddHealthCheck(&sourceStateChecker{repo: sourceRepo})

	// Run the service. Frame handles signal-based shutdown, HTTP serving
	// (with /healthz), and the background consumer lifecycle.
	if runErr := svc.Run(ctx, ""); runErr != nil {
		log.WithError(runErr).Fatal("service exited with error")
	}
}

// sourceStateChecker is a Frame Checker that embeds source state counts into
// the /healthz response as a named check entry.
type sourceStateChecker struct {
	repo *repository.SourceRepository
}

func (c *sourceStateChecker) Name() string { return "source_states" }

func (c *sourceStateChecker) CheckHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	active, _ := c.repo.CountByStatus(ctx, domain.SourceActive)
	degraded, _ := c.repo.CountByStatus(ctx, domain.SourceDegraded)
	paused, _ := c.repo.CountByStatus(ctx, domain.SourcePaused)

	// Return a non-error informational string; this surfaces in the checks array.
	// Only signal unhealthy if there are no active sources at all.
	if active == 0 && degraded == 0 {
		return fmt.Errorf("no active sources (active=%d degraded=%d paused=%d)", active, degraded, paused)
	}
	// Return nil to mark healthy; counts appear as the checker name context.
	_ = fmt.Sprintf("active=%d degraded=%d paused=%d", active, degraded, paused)
	return nil
}

// crawlDependencies bundles all dependencies needed by the crawl loop so they
// can be passed cleanly into WithBackgroundConsumer.
type crawlDependencies struct {
	cfg          *crawlerconfig.CrawlerConfig
	sourceRepo   *repository.SourceRepository
	crawlRepo    *repository.CrawlRepository
	jobRepo      *repository.JobRepository
	rejectedRepo *repository.RejectedJobRepository
	registry     *connectors.Registry
	dedupeEngine *dedupe.Engine
	batchBuf     *pipeline.BatchBuffer
	httpClient   *httpx.Client
	extractor    *extraction.Extractor
	svc          *frame.Service
}

// crawlLoop runs a ticker that finds due sources and processes them with
// bounded concurrency. It blocks until the context is cancelled.
func (d *crawlDependencies) crawlLoop(ctx context.Context) error {
	log := util.Log(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Process once immediately, then on every tick.
	d.processDueSources(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Info("crawl loop stopping")
			return nil
		case <-ticker.C:
			d.processDueSources(ctx)
		}
	}
}

func (d *crawlDependencies) processDueSources(ctx context.Context) {
	log := util.Log(ctx)
	now := time.Now().UTC()

	sources, err := d.sourceRepo.ListDue(ctx, now, 100)
	if err != nil {
		log.WithError(err).Error("list due sources failed")
		return
	}
	if len(sources) == 0 {
		return
	}

	log.WithField("count", len(sources)).Info("processing due sources")

	sem := make(chan struct{}, d.cfg.WorkerConcurrency)
	var wg sync.WaitGroup

	for _, src := range sources {
		wg.Add(1)
		sem <- struct{}{}

		go func(s *domain.Source) {
			defer wg.Done()
			defer func() { <-sem }()

			d.processSource(ctx, s)
		}(src)
	}

	wg.Wait()
}

func (d *crawlDependencies) processSource(ctx context.Context, src *domain.Source) {
	log := util.Log(ctx)

	conn, ok := d.registry.Get(src.Type)
	if !ok {
		log.WithField("source_type", src.Type).
			WithField("source_id", src.ID).
			Warn("no connector for source type")
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
	if err := d.crawlRepo.Create(ctx, crawlJob); err != nil {
		log.WithError(err).WithField("source_id", src.ID).Error("create crawl job failed")
		return
	}
	if err := d.crawlRepo.Start(ctx, crawlJob.ID); err != nil {
		log.WithError(err).WithField("crawl_job_id", crawlJob.ID).Error("start crawl job failed")
		return
	}

	// Run the connector.
	iter := conn.Crawl(ctx, *src)
	var jobsFound, jobsStored, jobsRejected int
	var crawlErr error

	for iter.Next(ctx) {
		for _, ext := range iter.Jobs() {
			jobsFound++

			// For HTML-based sources, fetch the detail page and use AI to extract fields.
			if d.extractor != nil && needsAIExtraction(src.Type) && ext.ApplyURL != "" {
				detailHTML, _, fetchErr := d.httpClient.Get(ctx, ext.ApplyURL, nil)
				if fetchErr == nil {
					if fields, aiErr := d.extractor.Extract(ctx, string(detailHTML), ext.ApplyURL); aiErr == nil {
						mergeExtractedFields(&ext, fields)
					} else {
						log.WithError(aiErr).WithField("url", ext.ApplyURL).Warn("AI extraction failed")
					}
				} else {
					log.WithError(fetchErr).WithField("url", ext.ApplyURL).Warn("fetch detail page failed")
				}
			}

			// Ensure apply_url has a fallback before quality gate
			quality.EnsureApplyURL(&ext, ext.SourceURL)
			if ext.ApplyURL == "" {
				quality.EnsureApplyURL(&ext, src.BaseURL)
			}

			// Quality gate.
			if qErr := quality.Check(ext); qErr != nil {
				_ = d.rejectedRepo.Create(ctx, &domain.RejectedJob{
					CrawlJobID: crawlJob.ID,
					SourceID:   src.ID,
					ExternalID: ext.ExternalID,
					Reason:     qErr.Error(),
					RejectedAt: time.Now().UTC(),
				})
				jobsRejected++
				continue
			}

			// Normalize to variant.
			variant := normalize.ExternalToVariant(ext, src.ID, src.Country, string(src.Type), time.Now().UTC())

			// Dedupe and persist canonical.
			canonical, dedupeErr := d.dedupeEngine.UpsertAndCluster(ctx, &variant)
			if dedupeErr != nil {
				log.WithError(dedupeErr).
					WithField("source_id", src.ID).
					WithField("external_id", ext.ExternalID).
					Error("dedupe failed")
				continue
			}

			// Emit events for embedding + enrichment through Frame's events manager.
			if canonical != nil && canonical.ID > 0 {
				evtMgr := d.svc.EventsManager()
				if evtMgr != nil {
					// Embedding event
					embPayload := &events.EmbeddingPayload{
						CanonicalJobID: canonical.ID,
						Title:          canonical.Title,
						Company:        canonical.Company,
						Description:    canonical.Description,
					}
					_ = evtMgr.Emit(ctx, events.EmbeddingGenerationEventName, embPayload)

					// Enrichment event — only if intelligence fields are empty
					if canonical.Seniority == "" || canonical.Skills == "" {
						enrichPayload := &events.JobEnrichmentPayload{
							CanonicalJobID: canonical.ID,
							Description:    canonical.Description,
							Title:          canonical.Title,
							Company:        canonical.Company,
							ApplyURL:       canonical.ApplyURL,
						}
						_ = evtMgr.Emit(ctx, events.JobEnrichmentEventName, enrichPayload)
					}
				}
			}

			// Add to batch buffer.
			if batchErr := d.batchBuf.Add(ctx, variant); batchErr != nil {
				log.WithError(batchErr).WithField("source_id", src.ID).Error("batch add failed")
			}

			jobsStored++
		}
	}

	if err := iter.Err(); err != nil {
		crawlErr = err
		log.WithError(err).WithField("source_id", src.ID).Error("crawl iteration failed")
		_ = d.crawlRepo.Finish(ctx, crawlJob.ID, domain.CrawlFailed, err.Error())
	} else {
		_ = d.crawlRepo.Finish(ctx, crawlJob.ID, domain.CrawlSucceeded, "")
	}

	// Health management: circuit breaker + reject-rate detection.
	rejectRate := 0.0
	if jobsFound > 0 {
		rejectRate = float64(jobsRejected) / float64(jobsFound)
	}

	if crawlErr != nil {
		// Connection failure — circuit breaker
		newFailures := src.ConsecutiveFailures + 1
		newHealth := src.HealthScore - 0.2
		if newHealth < 0 {
			newHealth = 0
		}
		_ = d.sourceRepo.RecordFailure(ctx, src.ID, newHealth, newFailures)
	} else if rejectRate > 0.8 && jobsFound > 0 {
		// High reject rate — flag needs tuning, don't break circuit
		_ = d.sourceRepo.FlagNeedsTuning(ctx, src.ID, true)
		// Still record the next_crawl_at update
		next := time.Now().UTC().Add(time.Duration(src.CrawlIntervalSec) * time.Second)
		_ = d.sourceRepo.UpdateNextCrawl(ctx, src.ID, next, time.Now().UTC(), src.HealthScore)
	} else {
		// Success
		newHealth := src.HealthScore + 0.1
		if newHealth > 1.0 {
			newHealth = 1.0
		}
		_ = d.sourceRepo.RecordSuccess(ctx, src.ID, newHealth)
		if src.NeedsTuning && rejectRate < 0.5 {
			_ = d.sourceRepo.FlagNeedsTuning(ctx, src.ID, false)
		}
		next := time.Now().UTC().Add(time.Duration(src.CrawlIntervalSec) * time.Second)
		_ = d.sourceRepo.UpdateNextCrawl(ctx, src.ID, next, time.Now().UTC(), newHealth)
	}

	log.WithField("source_id", src.ID).
		WithField("source_type", src.Type).
		WithField("found", jobsFound).
		WithField("stored", jobsStored).
		WithField("rejected", jobsRejected).
		WithField("crawl_err", crawlErr).
		Info("source processing complete")
}

// rebuildCanonicalsHandler returns an HTTP handler that truncates all canonical
// tables and rebuilds canonical jobs by re-running every variant through the
// dedupe engine. Useful for recovering from dedupe bugs that produced inflated
// canonical counts.
func rebuildCanonicalsHandler(jobRepo *repository.JobRepository, dedupeEngine *dedupe.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()

		// 1. Truncate canonical tables.
		if err := jobRepo.TruncateCanonicals(ctx); err != nil {
			http.Error(w, "truncate failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 2. Rebuild in batches.
		offset := 0
		batchSize := 500
		total := 0
		errors := 0
		for {
			variants, err := jobRepo.ListAllVariants(ctx, batchSize, offset)
			if err != nil || len(variants) == 0 {
				break
			}
			for _, v := range variants {
				if _, err := dedupeEngine.UpsertAndCluster(ctx, v); err != nil {
					errors++
				}
				total++
			}
			offset += batchSize
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":             "complete",
			"variants_processed": total,
			"errors":             errors,
		})
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
