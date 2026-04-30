package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	browserTimeout := time.Duration(cfg.BrowserTimeoutSec) * time.Second
	browserClient := browser.NewApplyClient(browserTimeout, "")
	defer browserClient.Close()

	// Build tier list. LLM tier is inserted before email only when
	// InferenceBaseURL is configured.
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
	queueHandler := service.NewAutoApplyHandler(svc, registry, appRepo, matchRepo)

	svc.Init(ctx,
		frame.WithRegisterPublisher(eventsv1.SubjectAutoApplySubmit, cfg.AutoApplyQueueURL),
		frame.WithRegisterSubscriber(eventsv1.SubjectAutoApplySubmit, cfg.AutoApplyQueueURL, queueHandler),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	svc.Init(ctx, frame.WithHTTPHandler(mux))

	log.Info("autoapply: service starting")
	if err := svc.Run(ctx, ""); err != nil {
		log.WithError(err).Error("autoapply: service run failed")
		os.Exit(1)
	}
}

// openAIClient implements autoapply.LLMClient via a minimal OpenAI-compatible
// chat completion call. Reuses the same pattern as extraction.Extractor.
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
