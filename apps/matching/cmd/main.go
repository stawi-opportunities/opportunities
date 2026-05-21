package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

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
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
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

	// Load the opportunity-kinds registry at boot. Phase 1 only loads + logs;
	// later phases consult the registry on the publish/index paths.
	reg, err := opportunity.LoadFromDir(cfg.OpportunityKindsDir)
	if err != nil {
		log.WithError(err).Fatal("opportunity registry: load failed")
	}
	log.WithField("kinds", reg.Known()).Info("opportunity registry: loaded")

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
			EmbeddingBaseURL: embBase,
			EmbeddingAPIKey:  embKey,
			EmbeddingModel:   embModel,
			Registry:         reg,
			HTTPClient:       svc.HTTPClientManager().Client(ctx),
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
			// Upstream reranker client is Phase 5. Until then, wrap the noop
			// in the pool so the rest of the wiring behaves identically.
			// TODO(phase-5): drive concurrency + timeout from
			// cfg.MatchingRerankerConcurrency / cfg.MatchingRerankerTimeoutSeconds.
			rerank = matching.NewPooledReranker(matching.NoopReranker{}, 8, time.Second)
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
			svc.Init(ctx,
				frame.WithRegisterSubscriber(fanout.Name(), fanout.Name(), fanout))
			log.Info("matching: fan-out (Path A) enabled")
		}

		if cfg.MatchingCandidateChangeEnabled {
			debounceTTL := time.Duration(cfg.MatchingDebounceTTLSeconds) * time.Second
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
				})
				_ = debounceTTL // TTL carried per-candidate via CandidateChange.DebounceTTL in Phase 5
				svc.Init(ctx,
					frame.WithRegisterSubscriber(cc.Name(), cc.Name(), cc))
			}
			log.Info("matching: candidate-change (Path C) enabled")
		}
	}

	// --- HTTP mux ---
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
		meV1.Mount(mux, extDeps)
		log.Info("matching: /api/me/* routes enabled")
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
