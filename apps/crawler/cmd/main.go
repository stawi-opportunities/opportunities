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

	"github.com/antinvestor/service-trustage/client/workflows"
	crawlerconfig "github.com/stawi-opportunities/opportunities/apps/crawler/config"
	"github.com/stawi-opportunities/opportunities/apps/crawler/service"
	"github.com/stawi-opportunities/opportunities/pkg/analytics"
	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/backpressure"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/crawlinbox"
	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/pkg/geocode"
	"github.com/stawi-opportunities/opportunities/pkg/normalize"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/seeds"
	"github.com/stawi-opportunities/opportunities/pkg/services"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
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

	// Load the opportunity-kinds registry. Prefer the R2-backed
	// definitions loader (admin can edit kind YAMLs in the cluster);
	// fall back to the on-disk ConfigMap when R2 isn't configured so
	// dev/OSS deploys keep working.
	loader, err := definitions.NewR2LoaderFromEnv(ctx)
	if err != nil {
		log.WithError(err).Fatal("definitions: env config failed")
	}
	var reg *opportunity.Registry
	if loader != nil {
		if err := loader.Start(ctx); err != nil {
			log.WithError(err).Fatal("definitions: loader start failed")
		}
		reg, err = opportunity.LoadFromDefinitions(ctx, loader)
		if err != nil {
			log.WithError(err).Fatal("definitions: registry load failed")
		}
		log.WithField("kinds", reg.Known()).Info("opportunity registry: loaded from R2 definitions")
	} else {
		log.Warn("definitions: R2 not configured; falling back to OpportunityKindsDir")
		reg, err = opportunity.LoadFromDir(cfg.OpportunityKindsDir)
		if err != nil {
			log.WithError(err).Fatal("opportunity registry: load failed")
		}
		log.WithField("kinds", reg.Known()).Info("opportunity registry: loaded from disk")
	}

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
	// Pattern follows service-trustage: pool.Migrate scans
	// MIGRATION_PATH for *.sql files and applies any not yet recorded
	// in the migrations tracking table, then GORM AutoMigrate keeps
	// the two historically-GORM-managed tables (sources + raw_refs)
	// in shape. FinalizeSchema applies pg_trgm + other extensions GORM
	// cannot express. Returns immediately after migration completes.
	if cfg.DoDatabaseMigrate() {
		if err := repository.Migrate(ctx, svc.DatastoreManager(), cfg.GetDatabaseMigrationPath()); err != nil {
			log.WithError(err).Fatal("database migration failed")
		}
		// Sync Trustage workflow definitions from the mounted ConfigMap.
		if cfg.TrustageURL != "" && cfg.TrustageWorkflowsDir != "" {
			trustageCli, cliErr := services.NewTrustageWorkflowClient(ctx, &cfg, cfg.TrustageURL)
			if cliErr != nil {
				log.WithError(cliErr).Fatal("trustage workflow client init failed")
			}
			if syncErr := workflows.SyncFromDir(ctx, trustageCli, cfg.TrustageWorkflowsDir); syncErr != nil {
				log.WithError(syncErr).Fatal("trustage workflow sync failed")
			}
		}
		log.Info("migration complete")
		return
	}

	// Repositories.
	sourceRepo := repository.NewSourceRepository(dbFn)
	// Load seed sources. The registry is consulted to validate every
	// seed's declared kinds — boot fails fast on a typo or a kind YAML
	// that was disabled.
	n, seedErr := seeds.LoadAndUpsert(ctx, cfg.SeedsDir, sourceRepo, reg)
	if seedErr != nil {
		log.WithError(seedErr).WithField("loaded", n).Warn("seed loading incomplete")
	} else {
		log.WithField("count", n).Info("seed sources loaded")
	}

	// HTTP client for connectors — stdlib client, no OAuth wrapping.
	// Frame's NewHTTPClient auto-attaches an OAuth2 token source when
	// OAUTH2_* env is configured, which sends Bearer headers to every
	// outbound request including public job-board URLs. Public sites
	// reject (or worse, accept) those tokens, and our own signer
	// endpoint correctly refuses to mint assertions for arbitrary
	// audiences (Hydra returns invalid_client). Use a plain stdlib
	// client; the connector retry/backoff loop in pkg/connectors/httpx
	// already provides resilience.
	httpDoer := &http.Client{
		Timeout: time.Duration(cfg.HTTPTimeoutSec) * time.Second,
	}
	// Unblocker fallback: route blocked requests (WAF/anti-bot 403s on HTML job
	// boards) through a residential-unblocker proxy, while requests that succeed
	// directly (most JSON APIs) stay off the paid path.
	var doer httpx.HTTPDoer = httpDoer
	if cfg.UnblockerProxyURL != "" {
		unblocker, insecure, perr := httpx.NewProxyDoer(
			cfg.UnblockerProxyURL, cfg.UnblockerCACert,
			time.Duration(cfg.UnblockerTimeoutSec)*time.Second)
		if perr != nil {
			log.WithError(perr).Warn("unblocker fallback disabled: invalid UNBLOCKER_PROXY_URL")
		} else {
			doer = httpx.NewFallbackDoer(httpDoer, unblocker)
			if insecure {
				log.Warn("unblocker fallback enabled WITHOUT a pinned CA — proxy TLS is not verified; set UNBLOCKER_CA_CERT")
			} else {
				log.Info("unblocker fallback enabled (CA-pinned) for blocked requests")
			}
		}
	}
	httpClient := httpx.NewClientFromDoer(doer, cfg.UserAgent)

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
			BaseURL:             infBase,
			APIKey:              infKey,
			Model:               infModel,
			EmbeddingBaseURL:    embBase,
			EmbeddingAPIKey:     embKey,
			EmbeddingModel:      embModel,
			EmbeddingDimensions: cfg.EmbeddingDimensions,
			RerankBaseURL:       cfg.RerankBaseURL,
			RerankAPIKey:        cfg.RerankAPIKey,
			RerankModel:         cfg.RerankModel,
			RerankDialect:       cfg.RerankDialect,
			Registry:            reg,
			// Plain stdlib client: the inference back-end is an external
			// API (Groq, OpenAI, Cloudflare AI Gateway) that authenticates
			// with INFERENCE_API_KEY directly, not Hydra-issued JWTs.
			// Frame's HTTPClientManager would attach an OAuth Bearer
			// targeting our own audience list and fail at the signer.
			//
			// Its own timeout (InferenceTimeoutSec) — NOT the 20s page-fetch
			// httpDoer, which timed out every real extraction mid-generation.
			HTTPClient: &http.Client{Timeout: time.Duration(cfg.InferenceTimeoutSec) * time.Second},
		})
		log.WithField("url", infBase).WithField("model", infModel).Info("AI extraction enabled")
	}

	// Connector registry.
	registry := service.BuildRegistry(ctx, httpClient, extractor, loader)

	// Archive R2 client + raw_ref repository. Same R2 account token
	// as the public content bucket — Cloudflare R2 IAM scopes the
	// token to specific buckets, so the platform doesn't need to
	// manage two key sets. rawRefRepo tracks which variants reference
	// which raw/{hash} blobs so the purge sweeper can GC orphans
	// safely.
	arch := archive.NewR2Archive(archive.R2Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2ArchiveBucket,
	})
	rawRefRepo := repository.NewRawRefRepository(dbFn)

	// Analytics client — batches events to OpenObserve. Nil when
	// ANALYTICS_BASE_URL is unset; all call sites handle the no-op.
	analyticsClient := analytics.New(analytics.Config{
		BaseURL:    cfg.AnalyticsBaseURL,
		Org:        cfg.AnalyticsOrg,
		Username:   cfg.AnalyticsUsername,
		Password:   cfg.AnalyticsPassword,
		HTTPClient: svc.HTTPClientManager().Client(ctx),
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
	}, svc.HTTPClientManager().Client(ctx))

	// ── Task 5: per-topic drain-time policy ────────────────────────
	//
	// Configure the five worker-input topics with a 15m/45m drain-time
	// window. HPA ceiling awareness is enabled so the gate collapses to
	// zero-admit as soon as the HPA has nowhere to scale to.
	workerTopicPolicy := backpressure.Policy{
		MaxDrainTime:     15 * time.Minute,
		HardCeilingDrain: 45 * time.Minute,
		HPACeilingKnown:  true,
	}
	for _, t := range []string{
		eventsv1.TopicVariantsIngested,
		eventsv1.TopicVariantsNormalized,
		eventsv1.TopicVariantsValidated,
		eventsv1.TopicVariantsClustered,
		eventsv1.TopicCanonicalsUpserted,
	} {
		bpGate.ConfigTopic(t, workerTopicPolicy)
	}

	// Lag poller: samples queue depth via the NATS monitor every 10 s and
	// feeds UpdateLag for each worker-input topic. The gate already has the
	// full drain-time policy configured above; this ticker keeps lag data
	// fresh so Admit can apply throttle fractions rather than falling back
	// to the binary hysteresis path.
	//
	// NOTE: the monitor's /jsz endpoint exposes a single "pending" field
	// that aggregates across all consumers on the stream. There is no per-
	// topic pending count available without per-consumer queries. We feed
	// the aggregate depth uniformly to all five topics as a conservative
	// upper-bound; the throttle applies to all topics equally when the
	// shared queue is deep. A more granular approach (per-consumer /jsz
	// calls) can be added in a later task without changing the gate API.
	lagPoller := service.NewLagPoller()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		workerTopics := []string{
			eventsv1.TopicVariantsIngested,
			eventsv1.TopicVariantsNormalized,
			eventsv1.TopicVariantsValidated,
			eventsv1.TopicVariantsClustered,
			eventsv1.TopicCanonicalsUpserted,
		}
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				state, err := bpGate.Check(ctx)
				if err != nil {
					continue
				}
				for _, topic := range workerTopics {
					depth, rate := lagPoller.Sample(topic, int64(state.Pending), now)
					bpGate.UpdateLag(topic, depth, rate, false)
				}
			}
		}
	}()

	// ── Phase 4 event handlers ──────────────────────────────────────
	//
	// Three internal subscriptions. Frame gives each its own consumer
	// group so a slow reconciliation never back-pressures the fetch path.
	//
	//   crawl.requests.v1       → CrawlRequestHandler (fetch + archive + extract + emit)
	//   crawl.page.completed.v1 → PageCompletedHandler (self-consumed; cursor + health)
	//   sources.discovered.v1   → SourceDiscoveredHandler (self-consumed; upsert)
	// Bundled-gazetteer geocoder. Singleton — parses ~300 rows once at
	// boot and reads concurrently thereafter. The Normalizer wraps it
	// so AnchorLocation gets Lat/Lon enriched on every variant whose
	// LLM-extracted city is recognised.
	geocoder := geocode.New()
	normalizer := normalize.New(geocoder)

	// pipeline_variants store — soft-fails on Postgres outage. Crawler
	// already has dbFn, so we wire the store directly. Writes the
	// ingested ledger row before the NATS emit (and rejected rows on
	// Verify failures).
	variantStore := variantstate.NewStore(dbFn)

	// crawl_jobs + raw_payloads audit ledger. Writes propagate errors
	// (unlike VariantStore) — the ledger is the source of truth for
	// what the pipeline ever attempted, so a Postgres outage MUST fail
	// the crawl rather than silently diverge.
	crawlRepo := repository.NewCrawlRepository(dbFn)

	// Iterator checkpoint store — feeds the resume path on the next
	// NATS redelivery after a crash. Soft-fail throughout: a Postgres
	// outage degrades resumption to "always start fresh" rather than
	// stalling the crawl.
	checkpointRepo := repository.NewCheckpointRepository(dbFn)
	recipeRepo := repository.NewRecipeRepository(dbFn)

	// URL frontier (D2) — discovered URLs from frontier-enabled
	// sources land here; apps/frontier-worker pulls + fetches them.
	// OnEnqueue wires the wake-up event so workers don't idle when
	// new URLs land. Best-effort emit — a missed event still drains
	// via the worker's heartbeat ticker.
	urlFrontier := frontier.NewPostgresFrontier(dbFn)
	urlFrontier.OnEnqueue = func(emitCtx context.Context, u frontier.URL) {
		evtMgr := svc.EventsManager()
		if evtMgr == nil {
			return
		}
		env := eventsv1.NewEnvelope(eventsv1.TopicURLEnqueued, eventsv1.URLEnqueuedV1{
			URLID:        u.URLID,
			CanonicalURL: u.CanonicalURL,
			Host:         u.Host,
			SourceID:     u.SourceID,
			Priority:     u.Priority,
			DiscoveredAt: u.EnqueuedAt,
		})
		if err := evtMgr.Emit(emitCtx, eventsv1.TopicURLEnqueued, env); err != nil {
			util.Log(emitCtx).WithError(err).WithField("url_id", u.URLID).
				Warn("crawler: frontier enqueue wake-up emit failed")
		}
	}

	// crawl_inbox buffer + pump (decouples crawl bursts from JetStream).
	var inboxStore *crawlinbox.Store
	if cfg.CrawlInboxEnabled {
		inboxStore = crawlinbox.NewStore(dbFn)
		log.Info("crawl-inbox: enabled — crawled variants buffered in Postgres, drained to pl_ingested by the pump")
	}

	crawlReqH := service.NewCrawlRequestHandler(service.CrawlRequestDeps{
		Svc:               svc,
		IngestedQueue:     cfg.QueuePipelineIngestedName,
		Inbox:             inboxStore,
		Sources:           sourceRepo,
		Registry:          registry,
		Kinds:             reg,
		Archive:           arch,
		Extractor:         extractor,
		Normalizer:        normalizer,
		PageFetcher:       httpClient,
		EnrichConcurrency: cfg.EnrichConcurrency,
		DiscoverSample:    0.05, // roughly 1-in-20 pages get DiscoverSites
		VariantStore:      variantStore,
		CrawlRepo:         crawlRepo,
		CheckpointRepo:    checkpointRepo,
		Frontier:          urlFrontier,
		RecipeRepo:        recipeRepo,
		RecipeEnabled:     cfg.RecipeEnabled,
	})
	pageDoneH := service.NewPageCompletedHandler(sourceRepo)
	// Drift-triggered recipe regeneration: when a recipe-driven source's
	// reject rate crosses the threshold, the page-completed handler emits
	// recipe.regenerate.v1 so the generator re-synthesises off fresh pages.
	// Only wired when recipe generation is enabled.
	if cfg.RecipeEnabled {
		pageDoneH.RegenRejectRate = cfg.RecipeRegenRejectRate
		pageDoneH.RegenMinPages = cfg.RecipeRegenMinPages
		pageDoneH.EmitRegenerate = func(emitCtx context.Context, sourceID, reason string) {
			evtMgr := svc.EventsManager()
			if evtMgr == nil {
				return
			}
			env := eventsv1.NewEnvelope(eventsv1.TopicRecipeRegenerate, eventsv1.RecipeRegenerateV1{SourceID: sourceID})
			if err := evtMgr.Emit(emitCtx, eventsv1.TopicRecipeRegenerate, env); err != nil {
				util.Log(emitCtx).WithError(err).WithField("source_id", sourceID).
					WithField("reason", reason).Warn("recipe: drift regenerate emit failed")
			}
		}
	}
	srcDiscH := service.NewSourceDiscoveredHandler(sourceRepo, reg)

	// Inbox pump: drain crawl_inbox → pl_ingested, but only while the queue has
	// headroom — so JetStream holds just the working set and the DB absorbs the
	// burst. Replicas claim disjoint batches via FOR UPDATE SKIP LOCKED.
	if inboxStore != nil {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if st, err := bpGate.Check(ctx); err == nil && st.Pending >= cfg.CrawlInboxPumpHighWater {
						continue // queue full enough — let the pipeline drain first
					}
					rows, err := inboxStore.ClaimBatch(ctx, cfg.CrawlInboxPumpBatch, 5*time.Minute)
					if err != nil {
						log.WithError(err).Warn("crawl-inbox pump: claim failed")
						continue
					}
					done := make([]string, 0, len(rows))
					for _, r := range rows {
						if pErr := svc.QueueManager().Publish(ctx, cfg.QueuePipelineIngestedName, r.Payload, nil); pErr != nil {
							log.WithError(pErr).Warn("crawl-inbox pump: publish failed; row stays claimed for re-drive")
							continue
						}
						done = append(done, r.VariantID)
					}
					if dErr := inboxStore.Delete(ctx, done); dErr != nil {
						log.WithError(dErr).Warn("crawl-inbox pump: delete drained rows failed")
					}
				}
			}
		}()
	}

	// The crawler subscribes to svc.opportunities.events.> (catch-all)
	// but only handles three topics. Frame v1.97.3 loose-mode acks-and-
	// skips every other topic on the shared stream — replaces the
	// per-topic NoopHandler block that used to live here and prevents
	// the "permanent NATS retry storm" wedge from a bare strict-mode
	// catch-all.
	//
	// definitions.changed.v1 — invalidates the loader cache and live-
	// rebuilds the kind registry so admin edits propagate within
	// seconds (vs. the 5-min refresh tick).
	handlers := []events.EventI{crawlReqH, pageDoneH, srcDiscH}
	if loader != nil {
		rebuild := func(ctx context.Context) error {
			fresh, err := opportunity.LoadFromDefinitions(ctx, loader)
			if err != nil {
				return err
			}
			reg.Replace(fresh)
			return nil
		}
		handlers = append(handlers, definitions.NewBroadcastConsumer(loader, rebuild))
		log.WithField("topic", eventsv1.TopicDefinitionsChanged).Info("definitions: broadcast consumer wired")
	}

	// Recipe generate/regenerate handlers. Only wired when RECIPE_ENABLED
	// and an AI extractor is configured — the Generator needs an LLM seam
	// (extractor.Complete) to synthesise recipes. The two handlers consume
	// recipe.generate.v1 / recipe.regenerate.v1 and run the
	// Generator → Validator → Store pipeline, activating on a pass-rate gate.
	if cfg.RecipeEnabled && extractor != nil {
		recipeGen := recipe.NewGenerator(extractor, recipe.NewHTTPFetcher(httpClient), reg, cfg.RecipeMaxGenAttempts)
		recipeGen.SetPassThreshold(cfg.RecipePassThreshold)
		recipeDeps := service.RecipeHandlerDeps{
			Sources: sourceRepo, Recipes: recipeRepo, Generator: recipeGen, Registry: reg,
			Fetcher: recipe.NewHTTPFetcher(httpClient), Flagger: sourceRepo,
			Samples: recipeRepo, SampleCount: cfg.RecipeSampleCount, Model: cfg.InferenceModel,
			PassThreshold: cfg.RecipePassThreshold,
		}
		handlers = append(handlers,
			service.NewRecipeGenerateHandler(recipeDeps),
			service.NewRecipeRegenerateHandler(recipeDeps),
		)
		log.WithField("topics", []string{eventsv1.TopicRecipeGenerate, eventsv1.TopicRecipeRegenerate}).
			Info("recipe: generate/regenerate handlers wired")
	}
	// Per-source Trustage schedule sync (the model that replaces the central
	// scheduler tick). When enabled + a TrustageURL is set, source lifecycle
	// mutations emit sources.scheduling.changed.v1 and the
	// SourceSchedulingHandler creates/archives the per-source crawl schedule to
	// match the source's live status; a boot reconcile + the
	// POST /admin/sources/schedules/reconcile backstop heal drift.
	var scheduleClient service.WorkflowClient
	if cfg.SourceSchedulesEnabled && cfg.TrustageURL != "" {
		if cli, cliErr := services.NewTrustageWorkflowClient(ctx, &cfg, cfg.TrustageURL); cliErr != nil {
			log.WithError(cliErr).Error("source-schedules: trustage client init failed; per-source schedules disabled")
		} else {
			scheduleClient = cli
			log.Info("source-schedules: per-source Trustage scheduling enabled")
		}
	}
	// The scheduling handler is the consumer side of the event the admin
	// handlers emit: it re-derives the desired schedule from the source's live
	// status. Only wired when scheduling is on (scheduleClient != nil).
	if scheduleClient != nil {
		handlers = append(handlers,
			service.NewSourceSchedulingHandler(scheduleClient, sourceRepo, cfg.CrawlBaseURL))
		log.WithField("topic", eventsv1.TopicSourceSchedulingChanged).
			Info("source-schedules: scheduling-changed handler wired")
	}

	svc.Init(ctx,
		frame.WithRegisterEvents(handlers...),
		// Pipeline head: publish VariantIngestedV1 to the opportunity chain.
		frame.WithRegisterPublisher(cfg.QueuePipelineIngestedName, cfg.QueuePipelineIngested),
	)
	if mgr := svc.EventsManager(); mgr != nil {
		mgr.SetStrict(false)
	}

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

	// ensureSchedule / removeSchedule are nil-safe no-ops when scheduling is
	// off, so the admin handlers can call them unconditionally. Both now EMIT
	// sources.scheduling.changed.v1 rather than touching Trustage inline — the
	// SourceSchedulingHandler (registered below) re-derives the desired state
	// from the source's live status and does the ensure/archive. This keeps the
	// schedule sync idempotent and order-independent across replicas.
	emitSchedulingChanged := func(ctx context.Context, id string) {
		if scheduleClient == nil {
			return
		}
		evtMgr := svc.EventsManager()
		if evtMgr == nil {
			return
		}
		env := eventsv1.NewEnvelope(eventsv1.TopicSourceSchedulingChanged,
			eventsv1.SourceSchedulingChangedV1{SourceID: id})
		if err := evtMgr.Emit(ctx, eventsv1.TopicSourceSchedulingChanged, env); err != nil {
			log.WithError(err).WithField("source_id", id).Warn("source-schedules: scheduling-changed emit failed")
		}
	}
	ensureSchedule := emitSchedulingChanged
	removeSchedule := emitSchedulingChanged
	// Boot reconcile: drive every source's schedule to match its status. Runs
	// detached so a slow/unavailable Trustage never blocks crawler startup.
	if scheduleClient != nil {
		go func() {
			bgCtx := context.WithoutCancel(ctx)
			rctx, cancel := context.WithTimeout(bgCtx, 5*time.Minute)
			defer cancel()
			// One-time retirement of the legacy static-cron sweep
			// (definitions/trustage/source-crawl-tick.json). It has been
			// replaced by per-source schedules; archive it so Trustage stops
			// firing POST /admin/sources/crawl-due (which no longer exists).
			// Idempotent + best-effort.
			if aerr := service.ArchiveWorkflowByName(rctx, scheduleClient, "opportunities.source.crawl-tick"); aerr != nil {
				log.WithError(aerr).Warn("source-schedules: archive legacy crawl-tick workflow failed")
			}
			all, err := sourceRepo.ListAll(rctx)
			if err != nil {
				log.WithError(err).Warn("source-schedules: boot reconcile ListAll failed")
				return
			}
			service.ReconcileSourceSchedules(rctx, scheduleClient, all, cfg.CrawlBaseURL)
		}()
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
		removeSchedule(r.Context(), id)
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
		ensureSchedule(r.Context(), id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "status": "active"})
	})

	// Admin: stop a source AND cascade-delete its jobs from search.
	//
	// Two-step contract:
	//   1. Flip sources.status to `disabled` with audit stamps
	//      (last_stopped_at / last_stopped_by) so future scheduler
	//      ticks skip it. ListDue's WHERE clause already excludes
	//      `disabled`, so this halts new crawls without further
	//      coordination.
	//   2. Emit SourceStoppedV1. The materializer's subscriber runs
	//      DELETE FROM idx_opportunities_rt WHERE source_id =
	//      hashID(id) — removing every historical job attributed to
	//      this source from search in one round-trip.
	//
	// Query params: ?id=<source_id>&reason=<text>&operator=<actor>
	// `operator` is the audit identity (defaults to "unknown" when
	// the caller hasn't propagated one).
	adminMux.HandleFunc("/admin/sources/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, `{"error":"invalid or missing id parameter"}`, http.StatusBadRequest)
			return
		}
		operator := r.URL.Query().Get("operator")
		if operator == "" {
			operator = "unknown"
		}
		reason := r.URL.Query().Get("reason")
		now := time.Now().UTC()

		if stopErr := sourceRepo.StopSource(r.Context(), id, operator, now); stopErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, stopErr.Error()), http.StatusInternalServerError)
			return
		}
		removeSchedule(r.Context(), id)

		// Emit the cascade-delete event. Best-effort; the source is
		// already marked disabled so missing the emit only delays
		// Manticore cleanup (it would normally happen on the
		// canonical_expired path once the retention sweep runs).
		if evtMgr := svc.EventsManager(); evtMgr != nil {
			env := eventsv1.NewEnvelope(eventsv1.TopicSourcesStopped, eventsv1.SourceStoppedV1{
				SourceID:  id,
				Reason:    reason,
				StoppedBy: operator,
				StoppedAt: now,
			})
			if emitErr := svc.EventsManager().Emit(r.Context(), eventsv1.TopicSourcesStopped, env); emitErr != nil {
				log.WithError(emitErr).WithField("source_id", id).Warn("source-stop: emit failed; jobs will be removed only by retention sweep")
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"id":         id,
			"status":     "stopped",
			"operator":   operator,
			"reason":     reason,
			"stopped_at": now,
		})
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

	// ── Trustage-driven endpoints ──────────────────────────────────
	//
	// These are called by Trustage workflow instances (on a schedule)
	// as simple fan-in targets. Every one of them is idempotent and
	// short-lived; the heavy lifting happens either in-process
	// (retention sweeps, which touch only our own tables) or in a
	// goroutine that emits a NATS event (crawl dispatch).

	// Admin: list source IDs that are due for a crawl right now.
	// Kept for operator tooling and dashboards.
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

	// Per-source crawl: each source's own Trustage schedule POSTs here at the
	// source's cadence. Emits exactly one crawl.requests.v1 for {id}, gated by
	// backpressure (429 + Retry-After when the pipeline is saturated).
	adminMux.HandleFunc("POST /admin/sources/{id}/crawl",
		service.SourceCrawlHandler(svc, sourceRepo, bpGate))

	// Schedule reconcile backstop: Trustage fires this periodically to heal
	// drift between sources.status and the per-source Trustage schedules.
	adminMux.HandleFunc("POST /admin/sources/schedules/reconcile",
		service.ScheduleReconcileHandler(sourceRepo, scheduleClient, cfg.CrawlBaseURL))

	// Admin: bulk reset quality-window counters on all active sources.
	// Trustage fires this weekly; see definitions/trustage/sources-quality-window-reset.json.
	adminMux.HandleFunc("POST /admin/sources/quality-reset",
		service.QualityResetHandler(sourceRepo))

	// Admin: nudge health_score toward 1.0 for all active sources.
	// Trustage fires this hourly; see definitions/trustage/sources-health-decay.json.
	adminMux.HandleFunc("POST /admin/sources/health-decay",
		service.HealthDecayHandler(sourceRepo))

	// Admin: enqueue AI recipe generation for recipe-less sources. Trustage fires
	// this every 15 min; see definitions/trustage/sources-recipe-backfill.json.
	// No-op while RECIPE_ENABLED=false. The endpoint only enumerates + emits
	// recipe.generate.v1; generation runs on the event consumers, so it scales by
	// adding crawler replicas.
	adminMux.HandleFunc("POST /admin/recipes/backfill",
		service.RecipeBackfillHandler(service.RecipeBackfillDeps{
			Sources: sourceRepo,
			Enabled: cfg.RecipeEnabled,
			Targets: service.UniversalRecipeTargets,
			Limit:   cfg.RecipeBackfillLimit,
			Emit: func(emitCtx context.Context, sourceID string) error {
				evtMgr := svc.EventsManager()
				if evtMgr == nil {
					return fmt.Errorf("recipe-backfill: events manager not configured")
				}
				env := eventsv1.NewEnvelope(eventsv1.TopicRecipeGenerate, eventsv1.RecipeGenerateV1{SourceID: sourceID})
				return evtMgr.Emit(emitCtx, eventsv1.TopicRecipeGenerate, env)
			},
		}))

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

	// Admin: R2 archive purge — drop cluster bundles + orphan raw/
	// blobs for canonicals past the grace window. Fired by Trustage
	// on the same cadence as stage2 retention.
	adminMux.HandleFunc("POST /admin/r2/purge", func(w http.ResponseWriter, r *http.Request) {
		grace := 7
		if v := r.URL.Query().Get("grace_days"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 90 {
				grace = n
			}
		}
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
				limit = n
			}
		}
		purgeR2Archive(r.Context(), dbFn, arch, rawRefRepo, grace, limit)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "grace_days": grace, "limit": limit})
	})

	// Admin: nightly orphan reconciliation. Walks clusters/* in the
	// archive bucket and deletes any cluster directory whose ID is
	// missing from canonical_jobs. Catches orphan bundles from failed
	// DB commits and objects missed by the purge sweeper.
	adminMux.HandleFunc("POST /admin/r2/reconcile", func(w http.ResponseWriter, r *http.Request) {
		reconcileOrphans(r.Context(), arch.Client(), arch.Bucket(), dbFn, arch)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// Admin: recent crawl_jobs for a source, each with a count of the
	// raw_payloads it produced. Triage view: "when did we last crawl
	// source X, what happened, how many raw_payloads landed?"
	adminMux.HandleFunc("GET /admin/crawl_jobs",
		service.CrawlJobsAdminHandler(crawlRepo))

	svc.Init(ctx, frame.WithHTTPHandler(adminHandler))

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

	// Only signal unhealthy if there are no active sources at all.
	if active == 0 && degraded == 0 {
		return fmt.Errorf("no active sources (active=%d degraded=%d paused=%d)", active, degraded, paused)
	}
	_ = fmt.Sprintf("active=%d degraded=%d paused=%d", active, degraded, paused)
	return nil
}
