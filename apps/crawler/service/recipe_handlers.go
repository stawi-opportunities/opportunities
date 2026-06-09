package service

import (
	"context"
	"encoding/json"
	"fmt"

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

// RecipeHandlerDeps wires the generate/regenerate handlers.
type RecipeHandlerDeps struct {
	Sources       SourceByID
	Recipes       RecipeStore
	Generator     *recipe.Generator
	Registry      *opportunity.Registry
	Fetcher       recipe.Fetcher
	Flagger       SourceFlagger
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
		sampleURLs = []string{src.BaseURL}
	}
	rec, samples, err := h.deps.Generator.Generate(ctx, *src, sampleURLs)
	if err != nil {
		log.WithError(err).WithField("source", sourceID).Warn("recipe.generate: synthesis failed")
		return err
	}
	report := recipe.ValidateRecipe(rec, *src, samples, h.deps.Registry)
	if report.PassRate < h.deps.PassThreshold {
		log.WithField("source", sourceID).WithField("pass_rate", report.PassRate).
			Warn("recipe.generate: below pass threshold; flagging for operator review")
		if h.deps.Flagger != nil {
			if ferr := h.deps.Flagger.FlagNeedsTuning(ctx, sourceID, true); ferr != nil {
				log.WithError(ferr).Warn("recipe.generate: flag needs_tuning failed")
			}
		}
		return nil // ack: regenerating from the same samples won't help; operator reviews
	}
	if err := h.deps.Recipes.Activate(ctx, sourceID, rec, report.PassRate, h.deps.Model, report); err != nil {
		return fmt.Errorf("recipe.generate: activate: %w", err)
	}
	log.WithField("source", sourceID).WithField("pass_rate", report.PassRate).Info("recipe.generate: activated")
	return nil
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
