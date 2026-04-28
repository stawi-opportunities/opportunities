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
	matchersreg "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	dealm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/deal"
	fundingm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/funding"
	jobm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/job"
	scholarshipm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/scholarship"
	tenderm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/tender"
	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/pkg/cv"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
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
	search, err := httpv1.NewManticoreSearch(cfg.ManticoreURL, "idx_opportunities_rt")
	if err != nil {
		log.WithError(err).Fatal("candidates: Manticore adapter init failed")
	}
	matchSvc := httpv1.NewMatchService(candStore, search, 20)
	candidateLister := adminv1.NewRepoCandidateLister(candidateRepo, 1000)
	matchRunner := adminv1.NewServiceMatchRunner(svc, matchSvc)
	staleReader := candidatestore.NewStaleReader(cat)
	staleLister := adminv1.NewR2StaleLister(staleReader, 60*24*time.Hour, 500)

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
	mux.HandleFunc("POST /_admin/matches/weekly_digest",
		adminv1.MatchesWeeklyHandler(adminv1.MatchesWeeklyDeps{
			Lister: candidateLister,
			Runner: matchRunner,
		}))
	mux.HandleFunc("POST /_admin/cv/stale_nudge",
		adminv1.CVStaleNudgeHandler(adminv1.CVStaleNudgeDeps{
			Svc:        svc,
			Lister:     staleLister,
			StaleAfter: 60 * 24 * time.Hour,
		}))

	svc.Init(ctx, frame.WithHTTPHandler(mux))

	if err := svc.Run(ctx, ""); err != nil {
		log.WithError(err).Error("candidates: service run failed")
		os.Exit(1)
	}
}

// --- Adapters — wire concrete pkg/* types to the v1 handler interfaces. ---

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
