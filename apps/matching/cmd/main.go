package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/frame/security"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	candidatesconfig "github.com/stawi-opportunities/opportunities/apps/matching/config"
	adminv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/admin/v1"
	eventv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/events/v1"
	httpv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	meV1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/me/v1"
	matchingv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/matching/v1"
	matchersreg "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	dealm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/deal"
	fundingm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/funding"
	jobm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/job"
	scholarshipm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/scholarship"
	tenderm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/tender"
	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/pkg/cv"
	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/savedjobs"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

func main() {
	ctx := context.Background()

	cfg, err := fconfig.FromEnv[candidatesconfig.CandidatesConfig]()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("candidates: config parse failed")
	}

	opts := []frame.Option{
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	}
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

	// Matcher registry: per-kind preference scorers. Phase 7.5 only
	// constructs + logs; Phase 7.6/7.7 will route the candidates/match
	// pipeline through it.
	matcherReg := matchersreg.NewRegistry()
	matcherReg.Register(jobm.New())
	matcherReg.Register(scholarshipm.New())
	matcherReg.Register(tenderm.New())
	matcherReg.Register(dealm.New())
	matcherReg.Register(fundingm.New())
	log.WithField("matchers", matcherReg.Kinds()).Info("matcher registry: loaded")

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	dbFn := pool.DB

	// Handle database migration if configured (colony Helm chart sets
	// DO_DATABASE_MIGRATE=true for the pre-install migration Job). Runs
	// AutoMigrate on every model this service reads/writes, then exits.
	// Without this branch the matching binary kept starting the full
	// service inside the migration Job and never returning, which the
	// Helm controller flagged as InProgress and the upgrade rolled back
	// (the production HR was looping on this — migration-21, -22, ...).
	if cfg.DoDatabaseMigrate() {
		migrationDB := dbFn(ctx, false)
		if err := migrationDB.AutoMigrate(
			&domain.CandidateProfile{},
			&domain.CandidateApplication{},
			&domain.OpportunityFlag{},
		); err != nil {
			log.WithError(err).Fatal("auto-migrate failed")
		}
		if err := repository.FinalizeSchema(migrationDB); err != nil {
			log.WithError(err).Fatal("finalize schema failed")
		}
		log.Info("migration complete")
		return
	}

	if err := telemetry.Init(); err != nil {
		log.WithError(err).Warn("candidates: telemetry init failed")
	}

	// --- Archive (raw CV bytes → R2) ---
	// Same R2 account token used by the rest of the platform; only
	// the bucket name differs.
	arch := archive.NewR2Archive(archive.R2Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2ArchiveBucket,
	})

	// --- Iceberg catalog (for candidatestore Reader + StaleReader) ---
	cat, err := icebergclient.LoadCatalog(ctx, icebergclient.CatalogConfig{
		Name:       cfg.IcebergCatalogName,
		URI:        cfg.IcebergCatalogURI,
		Warehouse:  cfg.IcebergWarehouse,
		OAuthToken: cfg.IcebergCatalogToken,
	})
	if err != nil {
		log.WithError(err).Fatal("candidates: iceberg catalog load failed")
	}
	candStore := candidatestore.NewReader(cat)

	// --- AI extractor ---
	var extractor *extraction.Extractor
	infBase, infModel, infKey := extraction.ResolveInference(
		cfg.InferenceBaseURL, cfg.InferenceModel, cfg.InferenceAPIKey,
		"", "")
	if infBase != "" {
		embBase, embModel, embKey := extraction.ResolveEmbedding(
			cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingAPIKey,
			"", "")
		extractor = extraction.New(extraction.Config{
			BaseURL:          infBase,
			APIKey:           infKey,
			Model:            infModel,
			EmbeddingBaseURL:    embBase,
			EmbeddingAPIKey:     embKey,
			EmbeddingModel:      embModel,
			EmbeddingDimensions: cfg.EmbeddingDimensions,
			RerankBaseURL:       cfg.RerankBaseURL,
			RerankAPIKey:        cfg.RerankAPIKey,
			RerankModel:         cfg.RerankModel,
			RerankDialect:       cfg.RerankDialect,
			Registry:            reg,
			HTTPClient:          svc.HTTPClientManager().Client(ctx),
		})
		log.WithField("url", infBase).Info("AI extraction enabled")
	}

	// --- Production adapters (Tasks 13-17) ---
	candidateRepo := repository.NewCandidateRepository(dbFn)
	// Post-Manticore: the search adapter speaks pgvector + JSONB
	// filters directly against the `opportunities` table over the
	// existing read-only pool. No new dialer, no extra service.
	search := httpv1.NewPostgresSearch(dbFn)
	matchSvc := httpv1.NewMatchService(candStore, search, 20)
	staleReader := candidatestore.NewStaleReader(cat)
	staleLister := adminv1.NewR2StaleLister(staleReader, 60*24*time.Hour, 500)
	// Weekly jobs digest collaborators — non-personalised email for
	// candidates who completed signup but not checkout. The same
	// pgsearch.Search backs the match service; we reuse its handle
	// rather than redial.
	unpaidLister := adminv1.NewRepoUnpaidCandidateLister(candidateRepo, 5000)
	newJobsLister := adminv1.NewPostgresJobsLister(search.Search())
	weeklyStats := adminv1.NewPostgresWeeklyStatsLister(search.Search())

	// --- Subscription handlers ---
	// CV-pipeline handlers (cv-extract / cv-improve / cv-embed) are
	// durable Frame Queue subscribers, not Frame Events. Each calls
	// an external LLM/embedding endpoint that may take seconds and may
	// fail; the Queue gives us retry-with-backoff + per-subject dead
	// letters per the Frame async decision tree.
	//
	// Skip when extractor is unconfigured so the binary still serves
	// the upload + preferences + match endpoints in a degraded mode
	// (uploads archive but don't enrich).
	prefMatchH := eventv1.NewPreferenceMatchHandler(eventv1.PreferenceMatchDeps{
		Svc:      svc,
		Match:    matchSvc,
		Matchers: matcherReg,
		TopK:     50,
	})
	if extractor != nil {
		scorer := cv.NewScorer(extractor)
		extractH := eventv1.NewCVExtractHandler(eventv1.CVExtractDeps{
			Svc:                   svc,
			Extractor:             cvExtractorAdapter{extractor},
			Scorer:                cvScorerAdapter{scorer},
			ExtractorModelVersion: cfg.InferenceModel,
			ScorerModelVersion:    "cv-scorer-v1",
		})
		improveH := eventv1.NewCVImproveHandler(eventv1.CVImproveDeps{
			Svc:          svc,
			Fixes:        cvFixAdapter{scorer: scorer},
			ModelVersion: cfg.InferenceModel,
		})
		embedH := eventv1.NewCVEmbedHandler(eventv1.CVEmbedDeps{
			Svc:          svc,
			Embedder:     embedderAdapter{extractor},
			ModelVersion: cfg.EmbeddingModel,
		})
		// Wire the cv-pipeline as durable queue subscribers. The upload
		// HTTP handler publishes onto SubjectCVExtract; cv-extract fans
		// out to SubjectCVImprove + SubjectCVEmbed; both terminate by
		// emitting their own events for the writer + materialiser.
		svc.Init(ctx,
			frame.WithRegisterPublisher(eventsv1.SubjectCVExtract, cfg.CVExtractQueueURL),
			frame.WithRegisterPublisher(eventsv1.SubjectCVImprove, cfg.CVImproveQueueURL),
			frame.WithRegisterPublisher(eventsv1.SubjectCVEmbed, cfg.CVEmbedQueueURL),
			frame.WithRegisterSubscriber(eventsv1.SubjectCVExtract, cfg.CVExtractQueueURL, extractH),
			frame.WithRegisterSubscriber(eventsv1.SubjectCVImprove, cfg.CVImproveQueueURL, improveH),
			frame.WithRegisterSubscriber(eventsv1.SubjectCVEmbed, cfg.CVEmbedQueueURL, embedH),
			frame.WithRegisterEvents(prefMatchH),
		)
	} else {
		// Even without an extractor we still want preference-update
		// re-matching, since that path runs the existing match service
		// (which uses a previously-stored embedding, not a live LLM
		// call). The upload handler still needs the cv-extract publisher
		// registered so POST /candidates/cv/upload doesn't fail; with no
		// subscriber the message lands and is dropped (or retained for
		// later replay) — explicitly degraded mode.
		svc.Init(ctx,
			frame.WithRegisterPublisher(eventsv1.SubjectCVExtract, cfg.CVExtractQueueURL),
			frame.WithRegisterEvents(prefMatchH),
		)
		log.Warn("candidates: no extractor configured — cv-extract/improve/embed subscribers disabled; uploads will archive + enqueue but not enrich")
	}

	// --- Debouncer (shared by Phase-2 and Phase-4 paths) ---
	// Constructed here so both the candidate-change consumers (Phase 2) and
	// the /api/me/* extension routes (Phase 4) share the same distributed
	// debouncer when VALKEY_URL is configured.
	var deb matching.Debouncer = matching.NewMemoryDebouncer()
	if cfg.ValkeyURL != "" {
		valkey, valkeyErr := matching.NewValkeyDebouncer(cfg.ValkeyURL)
		if valkeyErr != nil {
			log.WithError(valkeyErr).Fatal("matching: valkey debouncer init failed")
		}
		if valkey != nil {
			deb = valkey
			log.WithField("url", cfg.ValkeyURL).Info("matching: valkey debouncer enabled")
		}
	}

	// --- Phase-2 continuous matching pipeline (flag-gated per spec §5.5) ---

	if cfg.MatchingFanoutEnabled || cfg.MatchingCandidateChangeEnabled {
		// Extract *sql.DB from the existing GORM pool so the new pipeline
		// can use database/sql directly. pool.DB returns *gorm.DB; .DB()
		// unwraps the underlying connection pool.
		gdb := dbFn(ctx, false)
		sqlDB, err := gdb.DB()
		if err != nil {
			log.WithError(err).Fatal("matching: unwrap sql.DB from pool")
		}

		matchStore := matching.NewStore(sqlDB)
		matchEvents := matching.NewEventLog(sqlDB)
		matchIdx := matching.NewIndexStore(sqlDB)
		matchKNN := matching.NewKNN(sqlDB)

		var rerank matching.Reranker = matching.NoopReranker{}
		if cfg.MatchingRerankerEnabled {
			// Drive the real cross-encoder when an extractor (with a rerank
			// backend) is configured; otherwise fall back to the pooled noop
			// so the rest of the wiring behaves identically.
			if extractor != nil && extractor.RerankerVersion() != "" {
				rerank = matching.NewPooledReranker(
					matching.NewExtractorReranker(extractor, cfg.RerankTopK), 8, time.Second)
				log.WithField("model", extractor.RerankerVersion()).
					Info("matching: cross-encoder reranker enabled")
			} else {
				rerank = matching.NewPooledReranker(matching.NoopReranker{}, 8, time.Second)
				log.Warn("matching: reranker enabled but no rerank backend configured — using no-op")
			}
		}

		dlqPub := &queuePublisherAdapter{svc: svc}
		dlq := matchingv1.NewDLQGuard(dlqPub, eventsv1.SubjectMatchingDeadletter, cfg.MatchingDLQThreshold)

		if cfg.MatchingFanoutEnabled {
			fanout := matchingv1.NewFanOutConsumer(matchingv1.FanOutConsumerDeps{
				Store:    matchStore,
				EventLog: matchEvents,
				KNN:      matchKNN,
				Reranker: rerank,
				Weights:  matching.DefaultWeights(),
				DLQ:      dlq,
				OppEmbedQ: matchingv1.NewSQLOppEmbeddingQuery(sqlDB),
				// Phase 5: daily-cap enforcement via the continuous aggregate.
				DailyCap: matching.NewPGDailyCapQuery(sqlDB),
			})
			// Subscribe to the worker's canonical pipeline queue (same subject
			// the worker publishes CanonicalUpsertedV1 to). Frame derives a
			// matching-service-specific consumer_durable_name, so the worker's
			// publish stage and the writer sink each keep their own cursor —
			// this is a true fan-out, not a steal.
			svc.Init(ctx,
				frame.WithRegisterSubscriber(cfg.QueuePipelineCanonicalName, cfg.QueuePipelineCanonical, fanout))
			log.WithField("queue", cfg.QueuePipelineCanonicalName).Info("matching: fan-out (Path A) enabled")
		}

		if cfg.MatchingCandidateChangeEnabled {
			debounceTTL := time.Duration(cfg.MatchingDebounceTTLSeconds) * time.Second
			candText := matchingv1.NewSQLCandidateText(sqlDB)
			for _, topic := range []string{
				eventsv1.TopicCandidatePreferencesUpdated,
				eventsv1.TopicCandidateEmbedding,
			} {
				cc := matchingv1.NewCandidateChangeConsumer(matchingv1.CandidateChangeConsumerDeps{
					IndexStore: matchIdx,
					KNN:        matchKNN,
					Store:      matchStore,
					EventLog:   matchEvents,
					Reranker:   rerank,
					Weights:    matching.DefaultWeights(),
					Debouncer:  deb,
					DLQ:        dlq,
					Topic:      topic,
					CandText:   candText,
				})
				_ = debounceTTL // TTL carried per-candidate via CandidateChange.DebounceTTL in Phase 5
				svc.Init(ctx,
					frame.WithRegisterSubscriber(cc.Name(), cc.Name(), cc))
			}

			// Auto-populate candidate_match_indexes from embedding events.
			// DISTINCT durable name so it fans out alongside (does not steal
			// from) the CandidateChangeConsumer on TopicCandidateEmbedding.
			const embeddingIndexerName = "matching-candidate-embedding-indexer"
			indexer := matchingv1.NewCandidateEmbeddingIndexer(matchingv1.CandidateEmbeddingIndexerDeps{
				IndexStore: matchIdx,
				DLQ:        dlq,
				Name:       embeddingIndexerName,
			})
			svc.Init(ctx,
				frame.WithRegisterSubscriber(embeddingIndexerName, eventsv1.TopicCandidateEmbedding, indexer))

			log.Info("matching: candidate-change (Path C) enabled")
		}
	}

	// --- HTTP mux ---
	// Resolve Frame's JWT authenticator from the SecurityManager. With
	// the matching HelmRelease's oauth2.enabled=true block,
	// frame.NewServiceWithContext wires this from the OIDC env vars
	// (OAUTH2_SERVICE_URI, OAUTH2_JWT_VERIFY_AUDIENCE, ...). If the
	// service is started without OIDC configured (local dev, tests),
	// authenticator stays nil and NewCandidateAuth degrades to
	// header-only — same behaviour the existing unit tests rely on.
	var authenticator security.Authenticator
	if secMgr := svc.SecurityManager(); secMgr != nil {
		authenticator = secMgr.GetAuthenticator(ctx)
	}
	if authenticator != nil {
		log.Info("matching: /me/* routes protected with JWT authentication")
	} else {
		log.Warn("matching: no JWT authenticator configured — /me/* routes accept X-Candidate-ID header only")
	}
	authMW := httpmw.NewCandidateAuth(authenticator)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /candidates/match-kinds", func(w http.ResponseWriter, _ *http.Request) {
		kinds := matcherReg.EnabledKinds()
		sort.Strings(kinds)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]string{"enabled_kinds": kinds})
	})
	mux.HandleFunc("POST /candidates/cv/upload", httpv1.UploadHandler(httpv1.UploadDeps{
		Svc:     svc,
		Archive: arch,
		Text:    textExtractor{},
	}))
	mux.HandleFunc("POST /candidates/preferences", httpv1.PreferencesHandler(svc))
	mux.HandleFunc("GET /candidates/match", httpv1.MatchHandler(httpv1.MatchDeps{
		Svc:    svc,
		Store:  candStore,
		Search: search,
	}))

	// --- /me/subscription (dashboard summary) ---
	// The gateway HTTPRoute forwards /me/* unchanged to this backend.
	// The Phase-4 router uses /api/me/* paths which aren't exposed
	// through the gateway today, so register on the gateway-visible
	// path. A nil MatchSummarizer is acceptable — the handler returns
	// zero counts and the dashboard renders its setup-incomplete state.
	var meSubMatches httpv1.MatchSummarizer
	if gdb := dbFn(ctx, true); gdb != nil {
		if sqlDB, dbErr := gdb.DB(); dbErr == nil {
			meSubMatches = matching.NewStore(sqlDB)
		} else {
			log.WithError(dbErr).Warn("me/subscription: sql.DB unwrap failed; counts will be zero")
		}
	}
	mux.Handle("GET /me/subscription", authMW(
		httpv1.SubscriptionHandler(httpv1.SubscriptionDeps{
			Candidates: candidateRepo,
			Matches:    meSubMatches,
		}),
	))

	// /me/onboarding — resumable wizard. Same handler serves GET +
	// PUT; the underlying repo type implements both interfaces.
	// authMW wraps the registration with JWT verification + subject
	// extraction; the inner middleware populates the candidate ID
	// into the request context.
	onboardingHandler := authMW(httpv1.OnboardingHandler(httpv1.OnboardingDeps{
		Drafts: candidateRepo,
	}))
	mux.Handle("GET /me/onboarding", onboardingHandler)
	mux.Handle("PUT /me/onboarding", onboardingHandler)

	// /candidates/onboard — wizard final submit. Promotes the draft
	// into the canonical profile columns and clears the draft in one
	// transaction. The candidate's subscription tier stays "free"
	// here; service-payment flips it to "active" via webhook when
	// the checkout the wizard kicked off completes.
	onboardStore := &candidateOnboardAdapter{repo: candidateRepo}
	mux.Handle("POST /candidates/onboard", authMW(
		httpv1.CandidatesOnboardHandler(httpv1.CandidatesOnboardDeps{Store: onboardStore}),
	))

	// /me/saved-jobs — star/unstar. Both verbs share the same handler;
	// the handler dispatches on r.Method internally.
	var savedJobsStore *savedjobs.Store
	if gdb := dbFn(ctx, false); gdb != nil {
		if sqlDB, dbErr := gdb.DB(); dbErr == nil {
			savedJobsStore = savedjobs.NewStore(sqlDB)
		} else {
			log.WithError(dbErr).Warn("me/saved-jobs: sql.DB unwrap failed; star/unstar will 502")
		}
	}
	savedJobsHandler := authMW(httpv1.SavedJobsHandler(httpv1.SavedJobsDeps{
		Store: savedJobsStore,
	}))
	mux.Handle("POST /me/saved-jobs", savedJobsHandler)
	mux.Handle("DELETE /me/saved-jobs/{opportunity_id}", savedJobsHandler)

	// /me/opportunities — unified feed (joins matches + saved + applications).
	// Constructs a fresh *matching.Store from the same pool the subscription
	// handler uses. Guard against nil: if the DB is unavailable, skip the
	// route rather than panic on first request — the dashboard degrades
	// gracefully without the feed.
	if gdb := dbFn(ctx, true); gdb != nil {
		if sqlDB, dbErr := gdb.DB(); dbErr == nil {
			oppFeedStore := matching.NewStore(sqlDB)
			mux.Handle("GET /me/opportunities", authMW(
				httpv1.OpportunitiesHandler(httpv1.OpportunitiesDeps{Store: oppFeedStore}),
			))
		} else {
			log.WithError(dbErr).Warn("me/opportunities: sql.DB unwrap failed; GET /me/opportunities not registered")
		}
	} else {
		log.Warn("me/opportunities: DB pool unavailable; GET /me/opportunities not registered")
	}

	// /me/applications — manual apply. Writes directly to the applications
	// table via directApplicationStarter; the standalone apps/applications
	// service isn't deployed yet (see paid-flow spec, "Reality check on
	// what's already shipped").
	var appStarter httpv1.ApplicationStarter
	if gdb := dbFn(ctx, false); gdb != nil {
		if sqlDB, dbErr := gdb.DB(); dbErr == nil {
			appStarter = &directApplicationStarter{db: sqlDB}
		} else {
			log.WithError(dbErr).Warn("me/applications: sql.DB unwrap failed; apply will 502")
		}
	}
	mux.Handle("POST /me/applications", authMW(
		httpv1.ApplicationsHandler(httpv1.ApplicationsDeps{Starter: appStarter}),
	))

	// --- Trustage admin endpoints ---
	mux.HandleFunc("POST /_admin/cv/stale_nudge",
		adminv1.CVStaleNudgeHandler(adminv1.CVStaleNudgeDeps{
			Svc:        svc,
			Lister:     staleLister,
			StaleAfter: 60 * 24 * time.Hour,
		}))
	mux.HandleFunc("POST /_admin/candidates/weekly_jobs_digest",
		adminv1.WeeklyJobsDigestHandler(adminv1.WeeklyJobsDigestDeps{
			Svc:      svc,
			Lister:   unpaidLister,
			Jobs:     newJobsLister,
			Stats:    weeklyStats,
			PlansURL: cfg.PlansURL,
			Window:   7 * 24 * time.Hour,
			JobLimit: 10,
		}))

	// --- Phase-4 extension-facing /api/me/* routes (flag-gated per spec §5.5) ---
	if cfg.MatchingExtensionEnabled {
		// Open *sql.DB for the new pkg/matching stores. The same pattern
		// already used by the Phase-2 fan-out wiring above.
		gdb := dbFn(ctx, false)
		sqlDB, err := gdb.DB()
		if err != nil {
			log.WithError(err).Fatal("matching: open sql.DB for /api/me/* routes")
		}
		extDeps := &meV1.Deps{
			DB:               sqlDB,
			Matches:          matching.NewStore(sqlDB),
			MatchEvents:      matching.NewEventLog(sqlDB),
			Rules:            matching.NewRulesStore(sqlDB),
			IndexStore:       matching.NewIndexStore(sqlDB),
			KNN:              matching.NewKNN(sqlDB),
			Reranker:         matching.NoopReranker{},
			Weights:          matching.DefaultWeights(),
			Debouncer:        deb,
			IdempotencyStore: applications.NewIdempotencyStore(sqlDB, 24*time.Hour),
		}
		meV1.Mount(mux, extDeps, authMW)
		log.Info("matching: /api/me/* routes enabled")
	}

	// definitions.changed.v1 broadcast — invalidates the loader cache
	// and live-rebuilds the kind registry on admin edits. Registered as
	// a separate Init pass so it doesn't tangle with the flag-gated CV
	// subscriber wiring above.
	if loader != nil {
		rebuild := func(ctx context.Context) error {
			fresh, err := opportunity.LoadFromDefinitions(ctx, loader)
			if err != nil {
				return err
			}
			reg.Replace(fresh)
			return nil
		}
		svc.Init(ctx, frame.WithRegisterEvents(definitions.NewBroadcastConsumer(loader, rebuild)))
		if mgr := svc.EventsManager(); mgr != nil {
			mgr.SetStrict(false)
		}
		log.WithField("topic", eventsv1.TopicDefinitionsChanged).Info("definitions: broadcast consumer wired")
	}

	svc.Init(ctx, frame.WithHTTPHandler(mux))

	if err := svc.Run(ctx, ""); err != nil {
		log.WithError(err).Error("candidates: service run failed")
		os.Exit(1)
	}
}

// --- Adapters — wire concrete pkg/* types to the v1 handler interfaces. ---

// queuePublisherAdapter bridges *frame.Service to matchingv1.DeadLetterPublisher.
// It publishes raw bytes directly onto the named queue subject so the DLQGuard
// can drop poisoned messages without re-encoding them.
type queuePublisherAdapter struct{ svc *frame.Service }

func (a *queuePublisherAdapter) Publish(ctx context.Context, subject string, payload []byte) error {
	return a.svc.QueueManager().Publish(ctx, subject, payload)
}

type textExtractor struct{}

func (textExtractor) FromPDF(b []byte) (string, error)  { return extraction.ExtractTextFromPDF(b) }
func (textExtractor) FromDOCX(b []byte) (string, error) { return extraction.ExtractTextFromDOCX(b) }

type cvExtractorAdapter struct{ e *extraction.Extractor }

func (a cvExtractorAdapter) ExtractCV(ctx context.Context, text string) (*extraction.CVFields, error) {
	return a.e.ExtractCV(ctx, text)
}

// cvScorerAdapter bridges cv.Scorer → eventv1.CVScorer.
// cv.Scorer.Score returns *cv.CVStrengthReport; we map its Components +
// OverallScore into the handler's local ScoreComponents type.
type cvScorerAdapter struct{ s *cv.Scorer }

func (a cvScorerAdapter) Score(ctx context.Context, cvText string, fields *extraction.CVFields, targetRole string) *eventv1.ScoreComponents {
	rep := a.s.Score(ctx, cvText, fields, targetRole)
	return &eventv1.ScoreComponents{
		ATS:      rep.Components.ATS,
		Keywords: rep.Components.Keywords,
		Impact:   rep.Components.Impact,
		RoleFit:  rep.Components.RoleFit,
		Clarity:  rep.Components.Clarity,
		Overall:  rep.OverallScore,
	}
}

// cvFixAdapter bridges cv.Scorer → eventv1.FixGenerator.
// detectPriorityFixes is unexported in pkg/cv; we call Scorer.Score
// which already runs it and returns the fixes in report.PriorityFixes.
type cvFixAdapter struct{ scorer *cv.Scorer }

func (a cvFixAdapter) Generate(ctx context.Context, in *eventsv1.CVExtractedV1) ([]eventv1.PriorityFix, error) {
	fields := &extraction.CVFields{
		Name: in.Name, Email: in.Email, Phone: in.Phone, Location: in.Location,
		CurrentTitle: in.CurrentTitle, Bio: in.Bio, Seniority: in.Seniority,
		YearsExperience: in.YearsExperience, PrimaryIndustry: in.PrimaryIndustry,
		StrongSkills: in.StrongSkills, WorkingSkills: in.WorkingSkills,
		ToolsFrameworks: in.ToolsFrameworks, Certifications: in.Certifications,
		PreferredRoles: in.PreferredRoles, Languages: in.Languages,
		Education: in.Education, PreferredLocations: in.PreferredLocations,
		RemotePreference: in.RemotePreference,
	}
	// Re-run Score to compute PriorityFixes deterministically.
	// Passes empty cvText since the CVExtractedV1 event doesn't carry it;
	// fixes driven by field-level checks still fire correctly.
	report := a.scorer.Score(ctx, "", fields, in.CurrentTitle)
	out := make([]eventv1.PriorityFix, 0, len(report.PriorityFixes))
	for _, f := range report.PriorityFixes {
		out = append(out, eventv1.PriorityFix{
			FixID:          f.ID,
			Title:          f.Title,
			ImpactLevel:    f.Impact,
			Category:       f.Category,
			Why:            f.Why,
			AutoApplicable: f.AutoApplicable,
			Rewrite:        "", // v1: skip AI rewrites to conserve quota
		})
	}
	return out, nil
}

type embedderAdapter struct{ e *extraction.Extractor }

func (a embedderAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	return a.e.Embed(ctx, text)
}

// candidateOnboardAdapter satisfies httpv1.CandidatesOnboardStore by
// running the wizard-finalisation work inside a single
// CandidateRepository.Transaction. The transaction guarantees the
// canonical profile update and the draft clear commit together — a
// crash between them otherwise leaves a "completed onboarding but
// the draft still says step 2" state that confuses the resume path.
type candidateOnboardAdapter struct {
	repo *repository.CandidateRepository
}

func (a *candidateOnboardAdapter) OnboardAtomically(ctx context.Context, candidateID string, mutate func(*domain.CandidateProfile)) error {
	return a.repo.Transaction(ctx, func(txRepo *repository.CandidateRepository) error {
		// Load (or lazy-create) the candidate row inside the tx so
		// the read is consistent with the subsequent writes.
		cand, err := txRepo.GetByID(ctx, candidateID)
		if err != nil {
			return fmt.Errorf("candidate lookup: %w", err)
		}
		if cand == nil {
			cand = &domain.CandidateProfile{ProfileID: candidateID}
			cand.ID = candidateID
			if err := txRepo.Create(ctx, cand); err != nil {
				return fmt.Errorf("candidate create: %w", err)
			}
		}
		mutate(cand)
		if err := txRepo.Update(ctx, cand); err != nil {
			return fmt.Errorf("candidate update: %w", err)
		}
		if err := txRepo.ClearOnboardingDraft(ctx, candidateID); err != nil {
			return fmt.Errorf("draft clear: %w", err)
		}
		return nil
	})
}

// directApplicationStarter is the pragmatic ApplicationStarter while
// apps/applications isn't deployed as its own service. It writes
// directly to the shared `applications` table via pkg/applications.Store.
//
// Idempotent: if the (candidate, opportunity) pair already has an
// application row, StartApplication returns the existing row's ID and
// submitted_at rather than 409-ing. This mirrors the idempotent style
// the rest of the /me/* surface uses.
//
// match_id is NOT NULL in the schema. We do a best-effort lookup of an
// existing match_id from candidate_matches for the pair; if nothing is
// found we synthesise one ("manual_"+xid) so the insert can proceed.
type directApplicationStarter struct {
	db *sql.DB
}

func (a *directApplicationStarter) StartApplication(ctx context.Context, candidateID, opportunityID, method string) (string, time.Time, error) {
	if a.db == nil {
		return "", time.Time{}, fmt.Errorf("applications: sql.DB unavailable")
	}

	// Best-effort: reuse an existing match's id if there is one.
	matchID := "manual_" + xid.New().String()
	_ = a.db.QueryRowContext(ctx,
		`SELECT match_id FROM candidate_matches WHERE candidate_id=$1 AND opportunity_id=$2 LIMIT 1`,
		candidateID, opportunityID,
	).Scan(&matchID)

	store := applications.NewStore(a.db)
	app, err := store.Create(ctx, applications.Application{
		ApplicationID: xid.New().String(),
		CandidateID:   candidateID,
		OpportunityID: opportunityID,
		MatchID:       matchID,
		Status:        applications.Status("applied"),
		Metadata:      map[string]any{"method": method},
	})
	if errors.Is(err, applications.ErrAlreadyExists) {
		// Idempotent: return the existing row.
		existing, getErr := store.GetByPair(ctx, candidateID, opportunityID)
		if getErr != nil {
			return "", time.Time{}, fmt.Errorf("applications: fetch existing after conflict: %w", getErr)
		}
		appliedAt := existing.CreatedAt
		if existing.SubmittedAt != nil {
			appliedAt = *existing.SubmittedAt
		}
		return existing.ApplicationID, appliedAt, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("applications: create: %w", err)
	}
	appliedAt := app.CreatedAt
	if app.SubmittedAt != nil {
		appliedAt = *app.SubmittedAt
	}
	return app.ApplicationID, appliedAt, nil
}
