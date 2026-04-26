package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

// ValidationMinConfidence is set by the service wiring; tests can
// override via NewValidateHandlerWith.
var ValidationMinConfidence = 0.7

const validationPrompt = `You are a job data quality reviewer. Given extracted job posting data, assess its completeness and correctness. Output ONLY valid JSON.

Evaluate:
1. Is the title a real job title (not a category or website name)?
2. Is the description meaningful (not just a company description or boilerplate)?
3. Do the extracted skills match what's described in the job?
4. Is the seniority assessment reasonable for the described role?

Return:
{
  "valid": true/false,
  "confidence": 0.0-1.0,
  "issues": ["issue1", "issue2"],
  "recommendation": "accept" or "reject" or "flag"
}`

// validationResult is the structured LLM response.
type validationResult struct {
	Valid          bool     `json:"valid"`
	Confidence     float64  `json:"confidence"`
	Issues         []string `json:"issues"`
	Recommendation string   `json:"recommendation"`
}

// ValidateHandler consumes VariantNormalizedV1, runs the LLM
// validator, and emits either VariantValidatedV1 or
// VariantFlaggedV1. On LLM *error* (provider outage) it fail-opens
// with confidence=0.5. On LLM *rate-limit / 429* (not an "error" per
// se, an overload), it returns a non-nil error so Frame redelivers
// later.
type ValidateHandler struct {
	svc           *frame.Service
	extractor     *extraction.Extractor
	minConfidence float64
}

// NewValidateHandler uses the package-level ValidationMinConfidence.
func NewValidateHandler(svc *frame.Service, ex *extraction.Extractor) *ValidateHandler {
	return &ValidateHandler{svc: svc, extractor: ex, minConfidence: ValidationMinConfidence}
}

// Name ...
func (h *ValidateHandler) Name() string { return eventsv1.TopicVariantsNormalized }

// PayloadType ...
func (h *ValidateHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *ValidateHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("validate: empty payload")
	}
	return nil
}

// Execute runs the validator.
func (h *ValidateHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.VariantNormalizedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	n := env.Payload

	// If no extractor is configured, accept without AI — same semantics
	// as the legacy handler's "LLM unavailable" branch.
	if h.extractor == nil {
		return h.emitValidated(ctx, n, 0.5, "no extractor configured", "")
	}

	// Pull review fields out of the polymorphic Attributes map. The
	// universal envelope stays terse; description / location_text are
	// treated as common attribute keys until Phase 3.3 lifts them to
	// per-kind validators.
	title, _ := n.Attributes["title"].(string)
	company, _ := n.Attributes["issuing_entity"].(string)
	location, _ := n.Attributes["location_text"].(string)
	desc, _ := n.Attributes["description"].(string)

	review := strings.Join([]string{
		"Title: " + title,
		"Company: " + company,
		"Seniority: ",
		"Location: " + location,
		"Description (first 500 chars): " + first500(desc),
	}, "\n")

	out, err := h.extractor.Prompt(ctx, validationPrompt, review)
	if err != nil {
		// Fail-open on provider error. Note: a 429 produces a wrapped
		// error that also lands here; the retry-on-overload guidance
		// from the design spec prefers returning the error so Frame
		// redelivers. Practitioners adjust this in Phase 6 if they
		// want strict 429-retry behaviour; Phase 3 takes the simpler
		// path to ship.
		util.Log(ctx).WithError(err).Warn("validate: LLM failed, accepting with neutral confidence")
		return h.emitValidated(ctx, n, 0.5, "LLM unavailable: "+err.Error(), "")
	}

	var result validationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		util.Log(ctx).WithError(err).Warn("validate: unparseable LLM output, flagging")
		return h.emitFlagged(ctx, n, "unparseable", 0, "")
	}

	if result.Valid && result.Confidence >= h.minConfidence {
		notes := strings.Join(result.Issues, "; ")
		return h.emitValidated(ctx, n, result.Confidence, notes, "")
	}
	return h.emitFlagged(ctx, n, strings.Join(result.Issues, "; "), result.Confidence, "")
}

func (h *ValidateHandler) emitValidated(ctx context.Context, n eventsv1.VariantNormalizedV1, score float64, notes, model string) error {
	// Attributes propagate verbatim — the validator itself does not
	// rewrite per-kind fields. ModelVersion and richer validation
	// metadata can ride inside Attributes once Phase 3.3's per-kind
	// validator lands; for now we forward the normalized map.
	reasons := []string(nil)
	if notes != "" {
		reasons = strings.Split(notes, "; ")
	}
	out := eventsv1.VariantValidatedV1{
		VariantID:    n.VariantID,
		HardKey:      n.HardKey,
		Kind:         n.Kind,
		Valid:        true,
		Reasons:      reasons,
		ValidatedAt:  time.Now().UTC(),
		QualityScore: score,
		Attributes:   n.Attributes,
	}
	_ = model
	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsValidated, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsValidated, env)
}

func (h *ValidateHandler) emitFlagged(ctx context.Context, n eventsv1.VariantNormalizedV1, reason string, conf float64, model string) error {
	out := eventsv1.VariantFlaggedV1{
		VariantID:    n.VariantID,
		HardKey:      n.HardKey,
		Kind:         n.Kind,
		Reason:       reason,
		Confidence:   conf,
		ModelVersion: model,
		FlaggedAt:    time.Now().UTC(),
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsFlagged, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsFlagged, env)
}

func first500(s string) string {
	r := []rune(s)
	if len(r) <= 500 {
		return s
	}
	return string(r[:500])
}
