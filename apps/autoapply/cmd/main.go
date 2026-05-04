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
	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/ats"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
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
		log.Fatal("autoapply: no datastore pool available")
	}

	if err := pool.DB(ctx, false).AutoMigrate(&domain.CandidateApplication{}); err != nil {
		log.WithError(err).Warn("autoapply: auto-migrate candidate_applications failed")
	}

	appRepo := repository.NewApplicationRepository(pool.DB)
	matchRepo := repository.NewMatchRepository(pool.DB)

	browserClient := browser.NewPool(
		cfg.BrowserConcurrency,
		time.Duration(cfg.BrowserTimeoutSec)*time.Second,
		cfg.BrowserUserAgent,
	)
	defer browserClient.Close()

	// Build tier list. LLM tier is inserted before email only when
	// InferenceBaseURL is configured (Validate already enforced
	// InferenceModel != "" in that case).
	tiers := []autoapply.Submitter{
		ats.NewGreenhouseSubmitter(browserClient),
		ats.NewLeverSubmitter(browserClient),
		ats.NewWorkdaySubmitter(browserClient),
		ats.NewSmartRecruitersSubmitter(browserClient),
	}
	if cfg.InferenceBaseURL != "" {
		llm := newOpenAIClient(cfg.InferenceBaseURL, cfg.InferenceAPIKey, cfg.InferenceModel)
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

	queueHandler := service.NewAutoApplyHandler(service.HandlerDeps{
		Svc:       svc,
		Router:    registry,
		AppRepo:   appRepo,
		MatchRepo: matchRepo,
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
		_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"queued","event_id":%q}`, env.EventID)))
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
	defer resp.Body.Close()

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
