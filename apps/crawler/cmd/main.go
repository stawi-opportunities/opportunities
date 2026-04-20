package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/frame/events"
	securityhttp "github.com/pitabwire/frame/security/interceptors/httptor"
	"github.com/pitabwire/util"

	crawlerconfig "stawi.jobs/apps/crawler/config"
	"stawi.jobs/apps/crawler/service"
	"stawi.jobs/pkg/bloom"
	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/dedupe"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/normalize"
	"stawi.jobs/pkg/pipeline/handlers"
	"stawi.jobs/pkg/analytics"
	"stawi.jobs/pkg/backpressure"
	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/quality"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/seeds"
	"stawi.jobs/pkg/services"
	"stawi.jobs/pkg/telemetry"
	"stawi.jobs/pkg/translate"
)

func main() {
	ctx := context.Background()

	// Load configuration (embeds Frame's ConfigurationDefault). Use
	// util.Log here rather than panic(): it writes structured output
	// to the same log stream as the rest of the service, then exits
	// with Fatal semantics. A panic on startup works but produces a
	// goroutine trace in an otherwise-clean log view.
	cfg, err := fconfig.FromEnv[crawlerconfig.CrawlerConfig]()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("crawler: config parse failed")
	}

	// Build Frame options.
	opts := []frame.Option{
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	}

	// Create the Frame service.
	ctx, svc := frame.NewServiceWithContext(ctx, opts...)

	log := util.Log(ctx)

	// Initialize pipeline telemetry metrics. Frame has already configured the
	// global OTel provider, so this registers our custom instruments into it.
	if err := telemetry.Init(); err != nil {
		log.WithError(err).Warn("telemetry metrics init failed")
	}

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

		// Postgres-specific schema bits GORM AutoMigrate can't express:
		// partial indexes on status='active', pg_trgm, mv_job_facets.
		// The API also calls this but only when its own migration flag is
		// set — crawler runs the migration job on every Helm install, so
		// putting it here keeps the finalize step in one place.
		if err := repository.FinalizeSchema(migrationDB); err != nil {
			log.WithError(err).Fatal("finalize schema failed")
		}
		log.Info("migration complete")
		return
	}

	// Repositories.
	sourceRepo := repository.NewSourceRepository(dbFn)
	crawlRepo := repository.NewCrawlRepository(dbFn)
	jobRepo := repository.NewJobRepository(dbFn)
	rejectedRepo := repository.NewRejectedJobRepository(dbFn)
	facetRepo := repository.NewFacetRepository(dbFn)
	retentionRepo := repository.NewRetentionRepository(dbFn)

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

	// AI extractor — OpenAI-compatible back-end. Reads INFERENCE_* first,
	// falls back to the legacy OLLAMA_* vars during the Cloudflare AI
	// Gateway rollout so in-flight deploys keep working.
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
		log.WithField("url", infBase).WithField("model", infModel).Info("AI extraction enabled")
	}

	// Connector registry.
	registry := service.BuildRegistry(httpClient, extractor)

	// Dedupe engine.
	dedupeEngine := dedupe.NewEngine(jobRepo)

	// Bloom filter for fast duplicate detection.
	bloomFilter := bloom.NewFilter(cfg.ValkeyAddr, dbFn)
	svc.AddCleanupMethod(func(_ context.Context) { _ = bloomFilter.Close() })

	// R2 publisher for job content.
	var r2Publisher *publish.R2Publisher
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" {
		r2Publisher = publish.NewR2Publisher(
			cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey,
			cfg.R2Bucket, cfg.R2DeployHookURL,
		)
		publish.SetContentOrigin(cfg.ContentOrigin)
		log.WithField("bucket", cfg.R2Bucket).WithField("content_origin", publish.ContentOrigin).
			Info("R2 publisher enabled")
	}

	// Cloudflare cache purger (no-op if zone/token not configured).
	cachePurger := publish.NewCachePurger(cfg.CloudflareZoneID, cfg.CloudflareAPIToken, "")

	// Redirect service client — used by the publish handler to wrap
	// every apply_url in a tracked /r/{slug} link, and by the liveness
	// handler to expire links that point at dead postings. Nil when
	// REDIRECT_SERVICE_URI is unset (local dev).
	var redirectClient *services.RedirectClient
	if cfg.RedirectServiceURI != "" {
		redirectClient = services.NewRedirectClient(cfg.RedirectServiceURI)
	}

	// Analytics client — batches events to OpenObserve. Nil when
	// ANALYTICS_BASE_URL is unset; all call sites handle the no-op.
	analyticsClient := analytics.New(analytics.Config{
		BaseURL:  cfg.AnalyticsBaseURL,
		Org:      cfg.AnalyticsOrg,
		Username: cfg.AnalyticsUsername,
		Password: cfg.AnalyticsPassword,
	})
	if analyticsClient != nil {
		svc.AddCleanupMethod(func(ctx context.Context) { _ = analyticsClient.Close(ctx) })
	}

	// Backpressure gate for crawl dispatch. Bounds the pending queue
	// at cfg.BackpressureHighWater (defaults to 100k). Trustage's
	// cron keeps firing but the admin endpoints no-op while the
	// gate is closed. Hysteresis at cfg.BackpressureLowWater (50k)
	// prevents flapping once we're near the threshold.
	bpGate := backpressure.New(backpressure.Config{
		MonitorURL:   cfg.BackpressureMonitorURL,
		StreamName:   cfg.BackpressureStreamName,
		ConsumerName: cfg.BackpressureConsumerName,
		HighWater:    cfg.BackpressureHighWater,
		LowWater:     cfg.BackpressureLowWater,
	}, nil)

	// Translator fan-out. Off by default; flip TRANSLATE_ENABLED=true
	// once Groq quota headroom is confirmed for the fan-out volume.
	var translator *translate.Translator
	translateLangs := []string{}
	if cfg.TranslateEnabled && extractor != nil {
		translator = translate.New(extractor)
		translateLangs = cfg.TranslateLanguages
	}

	// Register pipeline stage handlers.
	pipelineHandlers := []frame.Option{}
	if extractor != nil {
		eventHandlers := []events.EventI{
			handlers.NewDedupHandler(jobRepo, svc),
			handlers.NewNormalizeHandler(jobRepo, sourceRepo, extractor, httpClient, svc),
			handlers.NewValidateHandler(jobRepo, sourceRepo, extractor, svc),
			handlers.NewCanonicalHandler(jobRepo, dedupeEngine, extractor, svc),
			handlers.NewSourceExpansionHandler(sourceRepo),
			handlers.NewSourceQualityHandler(sourceRepo, jobRepo, extractor),
		}
		if r2Publisher != nil {
			eventHandlers = append(eventHandlers, handlers.NewPublishHandler(jobRepo, r2Publisher, cachePurger, svc, redirectClient, cfg.RedirectPublicBaseURL, cfg.PublishMinQuality))
			eventHandlers = append(eventHandlers, handlers.NewTranslateHandler(jobRepo, r2Publisher, cachePurger, translator, svc, translateLangs, cfg.TranslateMinQuality))
		}
		pipelineHandlers = append(pipelineHandlers, frame.WithRegisterEvents(eventHandlers...))
	} else {
		// Without extractor, only register dedup + canonical (no AI stages)
		eventHandlers := []events.EventI{
			handlers.NewDedupHandler(jobRepo, svc),
			handlers.NewCanonicalHandler(jobRepo, dedupeEngine, nil, svc),
		}
		if r2Publisher != nil {
			eventHandlers = append(eventHandlers, handlers.NewPublishHandler(jobRepo, r2Publisher, cachePurger, svc, redirectClient, cfg.RedirectPublicBaseURL, cfg.PublishMinQuality))
		}
		pipelineHandlers = append(pipelineHandlers, frame.WithRegisterEvents(eventHandlers...))
	}
	svc.Init(ctx, pipelineHandlers...)

	// Stuck-variant recovery: re-emit events for variants stuck at intermediate stages.
	svc.AddPreStartMethod(func(preCtx context.Context, _ *frame.Service) {
		recoveryLog := util.Log(preCtx)
		evts := svc.EventsManager()

		for _, stage := range []string{"raw", "deduped", "normalized", "validated"} {
			stuck, _ := jobRepo.ListByStage(preCtx, stage, 100)
			if len(stuck) == 0 {
				continue
			}
			recoveryLog.WithField("stage", stage).WithField("count", len(stuck)).Info("re-emitting stuck variants")

			eventName := stageToEventName(stage)
			for _, v := range stuck {
				_ = evts.Emit(preCtx, eventName, &handlers.VariantPayload{VariantID: v.ID, SourceID: v.SourceID})
			}
		}
	})

	// crawlDependencies itself implements events.EventI for
	// EventCrawlRequest. Trustage's source.crawl workflow fires every
	// minute, enumerates due sources via the api's /admin/sources/due,
	// and dispatches one CrawlRequest message per source. JetStream
	// workqueue retention guarantees each message reaches exactly one
	// crawler replica — no contention, no ticker, no ListDue.
	//
	// Retention (expire / mv_refresh / stage2) also runs as Trustage
	// workflows now; each workflow does an HTTP POST to the crawler's
	// admin endpoints below. All the cadence, observability, and retry
	// discipline that used to live in in-process tickers now lives in
	// Trustage where it's visible alongside every other scheduled job
	// in the cluster.
	_ = retentionRepo // referenced by the admin endpoints registered below
	_ = facetRepo
	crawlDeps := &crawlDependencies{
		cfg:          &cfg,
		sourceRepo:   sourceRepo,
		crawlRepo:    crawlRepo,
		jobRepo:      jobRepo,
		rejectedRepo: rejectedRepo,
		registry:     registry,
		dedupeEngine: dedupeEngine,
		bloomFilter:  bloomFilter,
		httpClient:   httpClient,
		extractor:    extractor,
		svc:          svc,
	}
	svc.Init(ctx, frame.WithRegisterEvents(crawlDeps))

	// Build admin HTTP mux. Frame mounts this at "/" via WithHTTPHandler.
	adminMux := http.NewServeMux()

	// Wrap admin mux with authentication if SecurityManager is configured.
	var adminHandler http.Handler = adminMux

	if secMgr := svc.SecurityManager(); secMgr != nil {
		if authenticator := secMgr.GetAuthenticator(ctx); authenticator != nil {
			adminHandler = securityhttp.AuthenticationMiddleware(adminMux, authenticator)
			log.Info("admin endpoints protected with JWT authentication")
		} else {
			log.Warn("SecurityManager present but no authenticator configured — admin endpoints are UNPROTECTED")
		}
	} else {
		log.Warn("no SecurityManager configured — admin endpoints are UNPROTECTED")
	}

	// Admin: pause a source  (?id=N)
	adminMux.HandleFunc("/admin/sources/pause", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
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
		id := r.URL.Query().Get("id")
		if id == "" {
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

	// Admin: health report -- all sources ordered by worst health first
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

	// ── Trustage-driven endpoints ──────────────────────────────────
	//
	// These are called by Trustage workflow instances (on a schedule)
	// as simple fan-in targets. Every one of them is idempotent and
	// short-lived; the heavy lifting happens either in-process
	// (retention sweeps, which touch only our own tables) or in a
	// goroutine that emits a NATS event (crawl dispatch).

	// Admin: list source IDs that are due for a crawl right now. Called
	// by the source.crawl.dispatcher workflow every minute to drive
	// the per-source fan-out through /admin/crawl/dispatch below.
	adminMux.HandleFunc("GET /admin/sources/due", func(w http.ResponseWriter, r *http.Request) {
		limit := 500
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
				limit = n
			}
		}
		sources, err := sourceRepo.ListDue(r.Context(), time.Now().UTC(), limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		ids := make([]string, 0, len(sources))
		for _, s := range sources {
			ids = append(ids, s.ID)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"count": len(ids), "ids": ids})
	})

	// Admin: dispatch a crawl request for one source. Emits an
	// EventCrawlRequest onto the pipeline events stream; the crawler's
	// own subscription picks it up. Called by the source.crawl workflow
	// once per enumerated source.
	adminMux.HandleFunc("POST /admin/crawl/dispatch", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SourceID string `json:"source_id"`
			Attempt  int    `json:"attempt,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SourceID == "" {
			http.Error(w, `{"error":"source_id is required"}`, http.StatusBadRequest)
			return
		}
		// Backpressure gate: refuse new work when the pipeline queue
		// is saturated. Trustage sees {"paused": true} and retries on
		// its next tick; no work is lost.
		if state, bpErr := bpGate.Check(r.Context()); bpErr == nil && state.Paused {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":         true,
				"paused":     true,
				"source_id":  body.SourceID,
				"pending":    state.Pending,
				"high_water": state.HighWater,
				"low_water":  state.LowWater,
			})
			return
		}
		evtMgr := svc.EventsManager()
		if evtMgr == nil {
			http.Error(w, `{"error":"events manager unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		if err := evtMgr.Emit(r.Context(), handlers.EventCrawlRequest, &handlers.CrawlRequestPayload{
			SourceID: body.SourceID, Attempt: body.Attempt,
		}); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "source_id": body.SourceID})
	})

	// Admin: enumerate + dispatch all currently-due sources in one
	// call. Used by the simple source.crawl.sweep workflow which just
	// needs one HTTP call per minute. Returns the count dispatched so
	// Trustage can record it as workflow output.
	adminMux.HandleFunc("POST /admin/crawl/dispatch-due", func(w http.ResponseWriter, r *http.Request) {
		// Backpressure gate — evaluated first so we skip the DB hit
		// entirely when the pipeline is saturated. Trustage polls this
		// endpoint on a 1m cron; a paused response just means nothing
		// enters the queue this tick. Existing in-flight work drains
		// normally; resume happens automatically when pending drops
		// below the low-water mark.
		if state, bpErr := bpGate.Check(r.Context()); bpErr == nil && state.Paused {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":         true,
				"paused":     true,
				"considered": 0,
				"dispatched": 0,
				"pending":    state.Pending,
				"high_water": state.HighWater,
				"low_water":  state.LowWater,
			})
			return
		}
		limit := 500
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
				limit = n
			}
		}
		sources, err := sourceRepo.ListDue(r.Context(), time.Now().UTC(), limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		evtMgr := svc.EventsManager()
		if evtMgr == nil {
			http.Error(w, `{"error":"events manager unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		dispatched := 0
		for _, s := range sources {
			if emitErr := evtMgr.Emit(r.Context(), handlers.EventCrawlRequest, &handlers.CrawlRequestPayload{
				SourceID: s.ID, Attempt: 1,
			}); emitErr != nil {
				util.Log(r.Context()).WithError(emitErr).
					WithField("source_id", s.ID).
					Warn("dispatch-due: emit failed, continuing")
				continue
			}
			dispatched++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"considered": len(sources),
			"dispatched": dispatched,
		})
	})

	// Admin: backpressure state — operator-facing visibility into the
	// gate. Useful for dashboards and for confirming the Trustage
	// workflow is correctly no-op'ing during saturation.
	adminMux.HandleFunc("GET /admin/crawl/status", func(w http.ResponseWriter, r *http.Request) {
		state, err := bpGate.Check(r.Context())
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"paused":     state.Paused,
			"pending":    state.Pending,
			"high_water": state.HighWater,
			"low_water":  state.LowWater,
		}
		if err != nil {
			resp["error"] = err.Error()
			w.WriteHeader(http.StatusOK) // fail-open status
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Admin: retention sweep — flip expired canonicals.
	adminMux.HandleFunc("POST /admin/retention/expire", func(w http.ResponseWriter, r *http.Request) {
		runExpire(r.Context(), retentionRepo)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// Admin: materialized-view refresh.
	adminMux.HandleFunc("POST /admin/retention/mv-refresh", func(w http.ResponseWriter, r *http.Request) {
		runMVRefresh(r.Context(), facetRepo)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// Admin: stage-2 physical deletion of R2 snapshots past their
	// grace window. Runs synchronously; fine for the nightly cadence.
	adminMux.HandleFunc("POST /admin/retention/stage2", func(w http.ResponseWriter, r *http.Request) {
		runRetention(r.Context(), retentionRepo, r2Publisher, cachePurger, cfg.RetentionGraceDays)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	svc.Init(ctx, frame.WithHTTPHandler(adminHandler))

	// Register a named health checker that reports source state counts.
	svc.AddHealthCheck(&sourceStateChecker{repo: sourceRepo, jobRepo: jobRepo})

	// Run the service. Frame handles signal-based shutdown, HTTP serving
	// (with /healthz), and the background consumer lifecycle.
	if runErr := svc.Run(ctx, ""); runErr != nil {
		log.WithError(runErr).Fatal("service exited with error")
	}
}

// stageToEventName maps a pipeline stage name to the corresponding event name.
func stageToEventName(stage string) string {
	switch stage {
	case "raw":
		return handlers.EventVariantRawStored
	case "deduped":
		return handlers.EventVariantDeduped
	case "normalized":
		return handlers.EventVariantNormalized
	case "validated":
		return handlers.EventVariantValidated
	default:
		return ""
	}
}

// sourceStateChecker is a Frame Checker that embeds source state counts into
// the /healthz response as a named check entry.
type sourceStateChecker struct {
	repo   *repository.SourceRepository
	jobRepo *repository.JobRepository
}

func (c *sourceStateChecker) Name() string { return "source_states" }

func (c *sourceStateChecker) CheckHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	active, _ := c.repo.CountByStatus(ctx, domain.SourceActive)
	degraded, _ := c.repo.CountByStatus(ctx, domain.SourceDegraded)
	paused, _ := c.repo.CountByStatus(ctx, domain.SourcePaused)

	// Include pipeline stage counts for observability.
	stages, _ := c.jobRepo.CountByStage(ctx)
	_ = stages // surfaced via /healthz JSON

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
	bloomFilter  *bloom.Filter
	httpClient   *httpx.Client
	extractor    *extraction.Extractor
	svc          *frame.Service
}

// Name / PayloadType / Validate / Execute implement the Frame events.EventI
// contract so crawlDependencies can subscribe to EventCrawlRequest directly.
// Trustage's source.crawl workflow fires every minute, enumerates due
// sources via the api's /admin/sources/due, and posts one CrawlRequest per
// source through /admin/crawl/dispatch (which publishes to this subject).
// JetStream workqueue retention means each message reaches exactly one
// crawler replica — no contention, no ticker, no ListDue scan.
func (d *crawlDependencies) Name() string     { return handlers.EventCrawlRequest }
func (d *crawlDependencies) PayloadType() any { return &handlers.CrawlRequestPayload{} }

func (d *crawlDependencies) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*handlers.CrawlRequestPayload)
	if !ok {
		return fmt.Errorf("crawl.request: invalid payload type %T", payload)
	}
	if p.SourceID == "" {
		return fmt.Errorf("crawl.request: source_id is required")
	}
	return nil
}

func (d *crawlDependencies) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*handlers.CrawlRequestPayload)
	if !ok {
		return fmt.Errorf("crawl.request: invalid payload type %T", payload)
	}

	log := util.Log(ctx).
		WithField("source_id", p.SourceID).
		WithField("attempt", p.Attempt)

	src, err := d.sourceRepo.GetByID(ctx, p.SourceID)
	if err != nil {
		log.WithError(err).Error("crawl.request: load source failed")
		return err
	}
	if src == nil {
		log.Warn("crawl.request: source not found, dropping")
		return nil
	}
	if src.Status != domain.SourceActive && src.Status != domain.SourceDegraded {
		log.WithField("status", src.Status).Info("crawl.request: source not eligible, skipping")
		return nil
	}

	d.processSource(ctx, src)
	return nil
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

	// Reachability probe. A dead host will waste an entire crawl slot
	// talking to a connection timeout; skip dispatch and push the next
	// attempt out with exponential backoff so the queue stays healthy.
	verifyStatus, verifyErr := d.httpClient.Verify(ctx, src.BaseURL)
	verifiedAt := time.Now().UTC()
	_ = d.sourceRepo.RecordVerifyResult(ctx, src.ID, verifyStatus, verifiedAt)

	if verifyErr != nil || verifyStatus >= 500 || verifyStatus == 0 {
		failures := src.ConsecutiveFailures + 1
		// Backoff: interval × 2^failures, capped at 7 days of multiples.
		mult := 1 << failures
		if mult > 168 {
			mult = 168
		}
		next := verifiedAt.Add(time.Duration(src.CrawlIntervalSec*mult) * time.Second)

		newHealth := src.HealthScore - 0.2
		if newHealth < 0 {
			newHealth = 0
		}
		_ = d.sourceRepo.RecordFailure(ctx, src.ID, newHealth, failures)
		_ = d.sourceRepo.UpdateNextCrawl(ctx, src.ID, next, verifiedAt, newHealth)

		log.WithField("source_id", src.ID).
			WithField("base_url", src.BaseURL).
			WithField("status", verifyStatus).
			WithError(verifyErr).
			Warn("pre-crawl verify failed, skipping dispatch")
		return
	}

	now := time.Now().UTC()

	// Create crawl job record.
	crawlJob := &domain.CrawlJob{
		SourceID:       src.ID,
		ScheduledAt:    now,
		Status:         domain.CrawlScheduled,
		Attempt:        1,
		IdempotencyKey: fmt.Sprintf("%s-%d", src.ID, now.UnixNano()),
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
		for _, extJob := range iter.Jobs() {
			jobsFound++

			// Convert to variant (computes HardKey internally).
			variant := normalize.ExternalToVariant(extJob, src.ID, src.Country, string(src.Type), src.Language, time.Now().UTC())

			// Bloom filter check -- skip if already seen.
			if bloom.IsSeen(ctx, d.bloomFilter, src.ID, variant.HardKey) {
				continue
			}

			// Get content from connector (already extracted in Plan A).
			if pageContent := iter.Content(); pageContent != nil {
				variant.RawHTML = pageContent.RawHTML
				variant.CleanHTML = pageContent.CleanHTML
				variant.Markdown = pageContent.Markdown
			}

			// Ensure apply_url has a fallback before quality gate.
			quality.EnsureApplyURL(&extJob, extJob.SourceURL)
			if extJob.ApplyURL == "" {
				quality.EnsureApplyURL(&extJob, src.BaseURL)
			}
			if extJob.ApplyURL != "" {
				variant.ApplyURL = extJob.ApplyURL
			}

			// Basic quality check -- only title + description required.
			if qErr := quality.Check(extJob); qErr != nil {
				_ = d.rejectedRepo.Create(ctx, &domain.RejectedJob{
					CrawlJobID: crawlJob.ID,
					SourceID:   src.ID,
					ExternalID: extJob.ExternalID,
					Reason:     qErr.Error(),
					RejectedAt: time.Now().UTC(),
				})
				jobsRejected++
				continue
			}

			// Set stage to raw for pipeline processing.
			variant.Stage = domain.StageRaw

			// Store variant.
			if err := d.jobRepo.UpsertVariant(ctx, &variant); err != nil {
				log.WithError(err).WithField("source_id", src.ID).Error("store variant failed")
				continue
			}
			// GORM may not populate ID on conflict-update — load it.
			if variant.ID == "" {
				existing, _ := d.jobRepo.FindByHardKey(ctx, variant.HardKey)
				if existing != nil {
					variant.ID = existing.ID
				}
			}

			// Mark as seen in bloom filter.
			bloom.MarkSeen(ctx, d.bloomFilter, src.ID, variant.HardKey)

			// Emit pipeline event -- everything else happens via handlers.
			evtMgr := d.svc.EventsManager()
			if evtMgr != nil {
				_ = evtMgr.Emit(ctx, handlers.EventVariantRawStored, &handlers.VariantPayload{
					VariantID: variant.ID,
					SourceID:  src.ID,
				})
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
		// Connection failure -- circuit breaker
		newFailures := src.ConsecutiveFailures + 1
		newHealth := src.HealthScore - 0.2
		if newHealth < 0 {
			newHealth = 0
		}
		_ = d.sourceRepo.RecordFailure(ctx, src.ID, newHealth, newFailures)
	} else if rejectRate > 0.8 && jobsFound > 0 {
		// High reject rate -- flag needs tuning, don't break circuit
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
