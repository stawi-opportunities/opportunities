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
	securityhttp "github.com/pitabwire/frame/security/interceptors/httptor"
	"github.com/pitabwire/util"

	crawlerconfig "stawi.jobs/apps/crawler/config"
	"stawi.jobs/apps/crawler/service"
	"stawi.jobs/pkg/analytics"
	"stawi.jobs/pkg/archive"
	"stawi.jobs/pkg/backpressure"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/seeds"
	"stawi.jobs/pkg/telemetry"
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
			&domain.RawRef{},
		); err != nil {
			log.WithError(err).Fatal("auto-migrate failed")
		}
		// Postgres-specific schema bits GORM AutoMigrate can't express.
		// Crawler runs the migration job on every Helm install, so
		// putting it here keeps the finalize step in one place.
		if err := repository.FinalizeSchema(migrationDB); err != nil {
			log.WithError(err).Fatal("finalize schema failed")
		}
		log.Info("migration complete")
		return
	}

	// Repositories.
	sourceRepo := repository.NewSourceRepository(dbFn)
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

	// Archive R2 client + raw_ref repository. Separate bucket/creds
	// from the public job-repo above; carries raw HTML + variant
	// blobs + canonical snapshots. rawRefRepo tracks which variants
	// reference which raw/{hash} blobs so the purge sweeper can GC
	// orphans safely.
	arch := archive.NewR2Archive(archive.R2Config{
		AccountID:       cfg.ArchiveR2AccountID,
		AccessKeyID:     cfg.ArchiveR2AccessKeyID,
		SecretAccessKey: cfg.ArchiveR2SecretAccessKey,
		Bucket:          cfg.ArchiveR2Bucket,
	})
	rawRefRepo := repository.NewRawRefRepository(dbFn)

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
	crawlReqH := service.NewCrawlRequestHandler(service.CrawlRequestDeps{
		Svc:            svc,
		Sources:        sourceRepo,
		Registry:       registry,
		Archive:        arch,
		Extractor:      extractor,
		DiscoverSample: 0.05, // roughly 1-in-20 pages get DiscoverSites
	})
	pageDoneH := service.NewPageCompletedHandler(sourceRepo)
	srcDiscH := service.NewSourceDiscoveredHandler(sourceRepo)

	svc.Init(ctx, frame.WithRegisterEvents(crawlReqH, pageDoneH, srcDiscH))

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

	// Trustage fires this every 30 s; see definitions/trustage/scheduler-tick.json.
	adminMux.HandleFunc("POST /admin/scheduler/tick",
		service.SchedulerTickHandler(svc, sourceRepo, bpGate))

	// Admin: bulk reset quality-window counters on all active sources.
	// Trustage fires this weekly; see definitions/trustage/sources-quality-window-reset.json.
	adminMux.HandleFunc("POST /admin/sources/quality-reset",
		service.QualityResetHandler(sourceRepo))

	// Admin: nudge health_score toward 1.0 for all active sources.
	// Trustage fires this hourly; see definitions/trustage/sources-health-decay.json.
	adminMux.HandleFunc("POST /admin/sources/health-decay",
		service.HealthDecayHandler(sourceRepo))

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
