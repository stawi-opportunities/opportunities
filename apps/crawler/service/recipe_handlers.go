package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pitabwire/util"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

// RecipeStore is the subset of repository.RecipeRepository the handler needs.
type RecipeStore interface {
	Activate(ctx context.Context, sourceID string, rec *recipe.Recipe, passRate float64, model string, validationReport any) error
}

// SourceByID looks up a source by id (repository.SourceRepository satisfies it).
type SourceByID interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
}

// SourceFlagger marks a source for operator review (repository.SourceRepository satisfies it).
type SourceFlagger interface {
	FlagNeedsTuning(ctx context.Context, id string, needsTuning bool) error
}

// SampleURLProvider yields recently-crawled page URLs for a source — real,
// known-fetchable samples for recipe generation. repository.RecipeRepository
// satisfies it (raw_payloads-backed). Far more reliable than the bare BaseURL,
// which often redirects or 403s a non-connector request.
type SampleURLProvider interface {
	RecentURLs(ctx context.Context, sourceID string, limit int) ([]string, error)
}

// RecipeHandlerDeps wires the generate/regenerate handlers.
type RecipeHandlerDeps struct {
	Sources       SourceByID
	Recipes       RecipeStore
	Generator     *recipe.Generator
	Registry      *opportunity.Registry
	Fetcher       recipe.Fetcher
	Flagger       SourceFlagger
	Samples       SampleURLProvider
	SampleCount   int
	Model         string
	PassThreshold float64
}

// RecipeGenerateHandler handles recipe.generate.v1: synthesize, validate, activate.
type RecipeGenerateHandler struct{ deps RecipeHandlerDeps }

func NewRecipeGenerateHandler(d RecipeHandlerDeps) *RecipeGenerateHandler {
	return &RecipeGenerateHandler{deps: d}
}

func (h *RecipeGenerateHandler) Name() string     { return eventsv1.TopicRecipeGenerate }
func (h *RecipeGenerateHandler) PayloadType() any { return &json.RawMessage{} }
func (h *RecipeGenerateHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return fmt.Errorf("recipe.generate: empty payload")
	}
	return nil
}

func (h *RecipeGenerateHandler) Execute(ctx context.Context, payload any) error {
	raw, _ := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.RecipeGenerateV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("recipe.generate: decode: %w", err)
	}
	return h.generate(ctx, env.Payload.SourceID, env.Payload.SampleURLs)
}

func (h *RecipeGenerateHandler) generate(ctx context.Context, sourceID string, sampleURLs []string) error {
	log := util.Log(ctx)
	src, err := h.deps.Sources.GetByID(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("recipe.generate: source %s: %w", sourceID, err)
	}
	if len(sampleURLs) == 0 {
		sampleURLs = h.deriveSampleURLs(ctx, src)
	}
	rec, samples, err := h.deps.Generator.Generate(ctx, *src, sampleURLs)
	if err != nil {
		// Transient provider rate limit: NOT a property of the source. Don't
		// flag it (a busy minute would drain the whole queue into needs_tuning);
		// ACK and let a later backfill tick retry it naturally.
		if isRateLimited(err) {
			log.WithError(err).WithField("source", sourceID).
				Warn("recipe.generate: rate-limited; leaving source queued for a later tick")
			return nil
		}
		// Persistent failure (samples unfetchable, or the repair loop exhausted)
		// — flag for operator review and ACK. Returning the error would make
		// Frame redeliver this event indefinitely (re-fetching + re-calling the
		// LLM each time); flagging needs_tuning also makes the backfill cron skip
		// the source until an operator clears it.
		log.WithError(err).WithField("source", sourceID).
			Warn("recipe.generate: synthesis failed; flagging for operator review")
		h.flagForTuning(ctx, sourceID)
		return nil
	}
	report := recipe.ValidateRecipe(rec, *src, samples, h.deps.Registry)
	if report.PassRate < h.deps.PassThreshold {
		log.WithField("source", sourceID).WithField("pass_rate", report.PassRate).
			Warn("recipe.generate: below pass threshold; flagging for operator review")
		h.flagForTuning(ctx, sourceID)
		return nil // ack: regenerating from the same samples won't help; operator reviews
	}
	if err := h.deps.Recipes.Activate(ctx, sourceID, rec, report.PassRate, h.deps.Model, report); err != nil {
		return fmt.Errorf("recipe.generate: activate: %w", err)
	}
	log.WithField("source", sourceID).WithField("pass_rate", report.PassRate).Info("recipe.generate: activated")
	return nil
}

// deriveSampleURLs picks the URLs to learn a recipe from: the source's most
// recently-crawled pages (real detail/listing URLs the crawler already fetched
// successfully), falling back to the BaseURL when none are recorded yet.
func (h *RecipeGenerateHandler) deriveSampleURLs(ctx context.Context, src *domain.Source) []string {
	n := h.deps.SampleCount
	if n <= 0 {
		n = 4
	}
	if h.deps.Samples != nil {
		if urls, err := h.deps.Samples.RecentURLs(ctx, src.ID, n); err == nil && len(urls) > 0 {
			return urls
		} else if err != nil {
			util.Log(ctx).WithError(err).WithField("source", src.ID).
				Warn("recipe.generate: sample URL lookup failed")
		}
	}
	// No recorded crawl history: discover detail pages off the listing.
	// Validation dry-runs DETAIL extraction per sample, so a bare listing
	// page (BaseURL) can never pass the gate — it produced pass_rate=0.
	if h.deps.Fetcher != nil {
		if urls, err := recipe.DiscoverDetailURLs(ctx, h.deps.Fetcher, src.BaseURL, n); err == nil && len(urls) > 0 {
			util.Log(ctx).WithField("source", src.ID).WithField("samples", len(urls)).
				Info("recipe.generate: sampled detail pages from listing")
			return urls
		} else if err != nil {
			util.Log(ctx).WithError(err).WithField("source", src.ID).
				Warn("recipe.generate: detail-link discovery failed; using base URL")
		}
	}
	return []string{src.BaseURL}
}

// isRateLimited reports whether a generation error is a transient provider
// rate limit (the extraction client's retry exhaustion wraps the 429 text).
func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "status 429") || strings.Contains(msg, "rate-limited") ||
		strings.Contains(msg, "rate limiting")
}

// flagForTuning marks a source for operator review (best-effort).
func (h *RecipeGenerateHandler) flagForTuning(ctx context.Context, sourceID string) {
	if h.deps.Flagger == nil {
		return
	}
	if err := h.deps.Flagger.FlagNeedsTuning(ctx, sourceID, true); err != nil {
		util.Log(ctx).WithError(err).WithField("source", sourceID).
			Warn("recipe.generate: flag needs_tuning failed")
	}
}

// RecipeRegenerateHandler handles recipe.regenerate.v1 by re-running generation.
type RecipeRegenerateHandler struct{ gen *RecipeGenerateHandler }

func NewRecipeRegenerateHandler(d RecipeHandlerDeps) *RecipeRegenerateHandler {
	return &RecipeRegenerateHandler{gen: NewRecipeGenerateHandler(d)}
}

func (h *RecipeRegenerateHandler) Name() string     { return eventsv1.TopicRecipeRegenerate }
func (h *RecipeRegenerateHandler) PayloadType() any { return &json.RawMessage{} }
func (h *RecipeRegenerateHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return fmt.Errorf("recipe.regenerate: empty payload")
	}
	return nil
}
func (h *RecipeRegenerateHandler) Execute(ctx context.Context, payload any) error {
	raw, _ := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.RecipeRegenerateV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("recipe.regenerate: decode: %w", err)
	}
	return h.gen.generate(ctx, env.Payload.SourceID, nil)
}
