package main

import (
	"context"
	"fmt"
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

	// Create the Frame service. This sets up signal handling, telemetry,
	// logging, datastore, worker pool, queue manager, and events queue.
	ctx, svc := frame.NewServiceWithContext(ctx, opts...)

	log := util.Log(ctx)

	// Obtain the database pool and build the accessor function.
	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	dbFn := pool.DB

	// Run auto-migrations using the write connection.
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

	// Register the embedding event handler through Frame's events system.
	if extractor != nil {
		embeddingHandler := events.NewEmbeddingGenerationHandler(extractor, jobRepo)
		svc.Init(ctx, frame.WithRegisterEvents(embeddingHandler))

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

	// Run the service. Frame handles signal-based shutdown, HTTP serving
	// (with /healthz), and the background consumer lifecycle.
	if runErr := svc.Run(ctx, ""); runErr != nil {
		log.WithError(runErr).Fatal("service exited with error")
	}
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
	var jobsFound, jobsStored int

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

			// Quality gate.
			if qErr := quality.Check(ext); qErr != nil {
				_ = d.rejectedRepo.Create(ctx, &domain.RejectedJob{
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
			canonical, dedupeErr := d.dedupeEngine.UpsertAndCluster(ctx, &variant)
			if dedupeErr != nil {
				log.WithError(dedupeErr).
					WithField("source_id", src.ID).
					WithField("external_id", ext.ExternalID).
					Error("dedupe failed")
				continue
			}

			// Emit embedding event through Frame's events manager.
			if canonical != nil && canonical.ID > 0 {
				evtMgr := d.svc.EventsManager()
				if evtMgr != nil {
					payload := &events.EmbeddingPayload{
						CanonicalJobID: canonical.ID,
						Title:          canonical.Title,
						Company:        canonical.Company,
						Description:    canonical.Description,
					}
					if emitErr := evtMgr.Emit(ctx, events.EmbeddingGenerationEventName, payload); emitErr != nil {
						log.WithError(emitErr).
							WithField("canonical_id", canonical.ID).
							Warn("embedding event emission failed")
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
		log.WithError(err).WithField("source_id", src.ID).Error("crawl iteration failed")
		_ = d.crawlRepo.Finish(ctx, crawlJob.ID, domain.CrawlFailed, err.Error())
	} else {
		_ = d.crawlRepo.Finish(ctx, crawlJob.ID, domain.CrawlSucceeded, "")
	}

	// Update source scheduling.
	nextCrawl := time.Now().UTC().Add(time.Duration(src.CrawlIntervalSec) * time.Second)
	_ = d.sourceRepo.UpdateNextCrawl(ctx, src.ID, nextCrawl, time.Now().UTC(), src.HealthScore)

	log.WithField("source_id", src.ID).
		WithField("source_type", src.Type).
		WithField("found", jobsFound).
		WithField("stored", jobsStored).
		Info("source processing complete")
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
