package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	autoapplyconfig "github.com/stawi-opportunities/opportunities/apps/autoapply/config"
	"github.com/stawi-opportunities/opportunities/apps/autoapply/service"
	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/authmanifest"
	"github.com/stawi-opportunities/opportunities/pkg/authsession"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/ats"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/captcha"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/roamsubmitter"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/sessionsubmitter"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

func main() {
	ctx := context.Background()

	cfg, err := fconfig.FromEnv[autoapplyconfig.AutoApplyConfig]()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("autoapply: config parse failed")
	}
	if err := cfg.Validate(); err != nil {
		util.Log(ctx).WithError(err).Fatal("autoapply: config invalid")
	}

	// Refuse to start a non-devmode binary with any dev flag enabled
	// — those bypass the SSRF guard and add a public-ish HTTP endpoint
	// that can publish to the queue, neither of which should ever be
	// reachable in production.
	if !service.DevModeBuild && (cfg.DevAllowInsecureCV || cfg.DevIntentEndpoint) {
		util.Log(ctx).Fatal(
			"autoapply: AUTO_APPLY_DEV_ALLOW_INSECURE_CV / AUTO_APPLY_DEV_INTENT_ENDPOINT " +
				"are only honoured when the binary is built with -tags=devmode")
	}

	opts := []frame.Option{
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	}
	ctx, svc := frame.NewServiceWithContext(ctx, opts...)
	log := util.Log(ctx)

	if err := telemetry.Init(); err != nil {
		log.WithError(err).Warn("autoapply: telemetry init failed")
	}

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		log.Fatal("autoapply: no datastore pool available — set DATABASE_URL")
	}
	gdb := pool.DB(ctx, false)
	if gdb == nil {
		log.Fatal("autoapply: datastore pool returned a nil DB — DATABASE_URL likely unset or unreachable")
	}

	if err := gdb.AutoMigrate(&domain.CandidateApplication{}); err != nil {
		log.WithError(err).Warn("autoapply: auto-migrate candidate_applications failed")
	}

	appRepo := repository.NewApplicationRepository(pool.DB)
	matchRepo := repository.NewMatchRepository(pool.DB)

	// CAPTCHA solver (optional). When configured, detected challenges are
	// solved via the service instead of skipping the application. Attached
	// to every browser pool below.
	var captchaSolver browser.CaptchaSolver
	if cfg.CaptchaProvider == "2captcha" && cfg.CaptchaAPIKey != "" {
		captchaSolver = captcha.NewTwoCaptcha(cfg.CaptchaAPIKey).
			WithTimeout(time.Duration(cfg.CaptchaSolveTimeoutSec) * time.Second)
		log.Info("autoapply: 2captcha solver enabled")
	}

	browserClient := browser.NewPool(
		cfg.BrowserConcurrency,
		time.Duration(cfg.BrowserTimeoutSec)*time.Second,
		cfg.BrowserUserAgent,
	).WithCaptchaSolver(captchaSolver)
	defer browserClient.Close()

	// Session-replay submitter — registered ahead of every other tier
	// for sources with an extension-auth manifest. When the candidate
	// has no captured session for the source, this returns
	// Method=skipped/session_required and the next tier gets a chance.
	// The autoapply handler then emits SessionRequiredV1 so the UI
	// surfaces a reconnect CTA.
	authManifests, err := authmanifest.LoadFromDir(cfg.SourceAuthDir)
	if err != nil {
		log.WithError(err).Fatal("autoapply: source-auth manifest load failed")
	}
	log.WithField("source_auth_count", len(authManifests.Known())).
		WithField("source_auth_dir", cfg.SourceAuthDir).
		Info("autoapply: source-auth manifests loaded")

	var sessionProvider authsession.SessionProvider
	if cfg.SessionMasterKey != "" {
		wrapper, werr := authsession.NewLocalWrapperFromBase64(cfg.SessionMasterKey, cfg.SessionMasterKeyID)
		if werr != nil {
			log.WithError(werr).Fatal("autoapply: session master key invalid")
		}
		if err := gdb.AutoMigrate(&domain.CandidateSession{}); err != nil {
			log.WithError(err).Warn("autoapply: auto-migrate candidate_sessions failed")
		}
		sessionRepo := repository.NewCandidateSessionRepository(pool.DB)
		sessionProvider = authsession.NewStore(sessionRepo, wrapper)
		log.Info("autoapply: session-replay submitter enabled")
	} else {
		log.Warn("autoapply: SESSION_MASTER_KEY unset — session-replay disabled")
	}

	// Build tier list. LLM tier is inserted before email only when
	// InferenceBaseURL is configured (Validate already enforced
	// InferenceModel != "" in that case).
	tiers := []autoapply.Submitter{}
	if sessionProvider != nil {
		// Register the ROAM Africa boards (BrighterMonday KE/UG, Jobberman
		// NG/GH) BEFORE the generic session-replay submitter. They share one
		// engine (roamsubmitter) and differ only by site config (name/
		// source/origin), one per country. The apply is a Laravel form POST
		// to a templated URL extracted from the listing page's
		// <form action="…">, which the generic submitter can't infer from
		// the manifest's form_url_pattern alone — so these must take
		// precedence.
		roamCfg := roamsubmitter.Config{Sessions: sessionProvider, HTTPTimeout: 30 * time.Second}
		for _, site := range roamsubmitter.AllSites() {
			tiers = append(tiers, roamsubmitter.New(site, roamCfg))
		}
		tiers = append(tiers, sessionsubmitter.New(sessionsubmitter.Config{
			Manifests:   authManifests,
			Sessions:    sessionProvider,
			HTTPTimeout: 30 * time.Second,
		}))
	}
	// Shared LLM client (when inference is configured) powers both the
	// Greenhouse custom-question answering and the generic LLM form tier.
	var llm autoapply.LLMClient
	if cfg.InferenceBaseURL != "" {
		llm = newOpenAIClient(cfg.InferenceBaseURL, cfg.InferenceAPIKey, cfg.InferenceModel)
	}

	tiers = append(tiers,
		ats.NewLeverSubmitter(browserClient),
		ats.NewWorkdaySubmitter(browserClient),
		ats.NewSmartRecruitersSubmitter(browserClient),
	)
	if llm != nil {
		tiers = append(tiers, autoapply.NewLLMFormSubmitter(browserClient, llm))
	}
	tiers = append(tiers,
		autoapply.NewEmailFallback(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom, cfg.SMTPPassword),
	)

	registry := autoapply.NewRegistry(tiers...)
	cvFetcher := service.NewHTTPCVFetcherWithOptions(
		time.Duration(cfg.CVDownloadTimeoutSec)*time.Second,
		cfg.CVMaxBytes,
		cfg.DevAllowInsecureCV,
	)

	// Bridge to main's apps/applications tracking surface. We open a
	// *sql.DB view of the same pool the GORM repos use, so this is the
	// same connection-pool footprint as the legacy persistence path —
	// no extra pool, no second DB env var.
	//
	// A failure to grab the *sql.DB here is fatal: the bridge is part
	// of the v1 contract once SESSION_MASTER_KEY is set, and silently
	// degrading to "we apply but the candidate never sees it on their
	// dashboard" is a worse outcome than crashing the boot.
	sqlDB, err := gdb.DB()
	if err != nil {
		log.WithError(err).Fatal("autoapply: open *sql.DB for applications tracker")
	}
	tracker := service.NewPkgApplicationsTracker(applications.NewStore(sqlDB))

	queueHandler := service.NewAutoApplyHandler(service.HandlerDeps{
		Svc:       svc,
		Router:    registry,
		AppRepo:   appRepo,
		MatchRepo: matchRepo,
		Tracker:   tracker,
		CV:        cvFetcher,
		Config: service.Config{
			Enabled:            cfg.Enabled,
			DryRun:             cfg.DryRun,
			DailyLimitBackstop: cfg.DailyLimitBackstop,
			ScoreMinBackstop:   cfg.ScoreMinBackstop,
		},
	})

	svc.Init(ctx,
		frame.WithRegisterPublisher(eventsv1.SubjectAutoApplySubmit, cfg.AutoApplyQueueURL),
		frame.WithRegisterSubscriber(eventsv1.SubjectAutoApplySubmit, cfg.AutoApplyQueueURL, queueHandler),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	if cfg.DevIntentEndpoint && service.DevModeBuild {
		mux.Handle("POST /dev/intent", devIntentHandler(svc, cfg.AutoApplyQueueURL))
		log.Warn("autoapply: POST /dev/intent enabled — devmode build only")
	}
	svc.Init(ctx, frame.WithHTTPHandler(mux))

	log.Info("autoapply: service starting")
	if err := svc.Run(ctx, ""); err != nil {
		log.WithError(err).Error("autoapply: service run failed")
		os.Exit(1)
	}
}

// devIntentHandler accepts a JSON AutoApplyIntentV1 (NOT an envelope —
// the handler wraps it) and publishes it to the autoapply queue. Mounts
// only when AUTO_APPLY_DEV_INTENT_ENDPOINT=true and the binary was
// built with -tags=devmode.
//
// curl example:
//
//	curl -X POST localhost:8080/dev/intent \
//	     -H 'content-type: application/json' \
//	     -d '{"candidate_id":"cnd_1","canonical_job_id":"job_1",
//	          "apply_url":"https://boards.greenhouse.io/co/jobs/1"}'
func devIntentHandler(svc *frame.Service, _ string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var intent eventsv1.AutoApplyIntentV1
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&intent); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if intent.CandidateID == "" || intent.CanonicalJobID == "" || intent.ApplyURL == "" {
			http.Error(w, "candidate_id, canonical_job_id, apply_url required", http.StatusBadRequest)
			return
		}
		env := eventsv1.NewEnvelope(eventsv1.SubjectAutoApplySubmit, intent)
		body, err := json.Marshal(env)
		if err != nil {
			http.Error(w, "marshal: "+err.Error(), http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := svc.QueueManager().Publish(ctx, eventsv1.SubjectAutoApplySubmit, body); err != nil {
			http.Error(w, "publish: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(w, `{"status":"queued","event_id":%q}`, env.EventID)
	})
}

// openAIClient implements autoapply.LLMClient via a minimal
// OpenAI-compatible chat completion call. Reuses the same pattern as
// extraction.Extractor.
type openAIClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func newOpenAIClient(baseURL, apiKey, model string) autoapply.LLMClient {
	return &openAIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *openAIClient) Complete(ctx context.Context, system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return "", fmt.Errorf("llm: http %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: empty choices")
	}
	return out.Choices[0].Message.Content, nil
}
