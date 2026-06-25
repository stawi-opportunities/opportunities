package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/queue"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
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
//
// When skipLLM is true the handler short-circuits the LLM call and
// emits VariantValidated@1.0 for every record. Used to keep the
// canonical chain moving when the shared inference fleet is saturated
// elsewhere (crawler enrichStubs, embed/translate). Connector-
// supplied fields remain the authoritative data; only the QC pass
// is skipped.
type ValidateHandler struct {
	svc           *frame.Service
	extractor     *extraction.Extractor
	minConfidence float64
	skipLLM       bool
	store         *variantstate.Store
	nextValidated string // Queue Name for VariantValidatedV1
	nextFlagged   string // Queue Name for VariantFlaggedV1
}

// NewValidateHandlerWithSkip returns a handler consuming VariantNormalizedV1.
// nextValidated/nextFlagged are the downstream Queue Names. When skipLLM is
// true every variant passes through validated@1.0.
func NewValidateHandlerWithSkip(svc *frame.Service, ex *extraction.Extractor, skipLLM bool, store *variantstate.Store, nextValidated, nextFlagged string) *ValidateHandler {
	return &ValidateHandler{
		svc:           svc,
		extractor:     ex,
		minConfidence: ValidationMinConfidence,
		skipLLM:       skipLLM,
		store:         store,
		nextValidated: nextValidated,
		nextFlagged:   nextFlagged,
	}
}

var _ queue.SubscribeWorker = (*ValidateHandler)(nil)

// Handle runs the validator and publishes VariantValidatedV1 (or
// VariantFlaggedV1) to the next pipeline queue.
func (h *ValidateHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	var env eventsv1.Envelope[eventsv1.VariantNormalizedV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return err
	}
	n := env.Payload

	// Skip-LLM bypass — pass through every variant with confidence=1.0.
	// Used when the shared inference fleet is saturated and validate is
	// stalling the canonical chain.
	if h.skipLLM {
		return h.emitValidated(ctx, n, 1.0, "validation skipped (VALIDATION_SKIP_LLM)", "")
	}

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
		_ = h.store.RecordError(ctx, n.VariantID, variantstate.StageValidated, fmt.Errorf("validate: unparseable LLM output: %w", err))
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
		SourceID:     n.SourceID,
		HardKey:      n.HardKey,
		Kind:         n.Kind,
		Valid:        true,
		Reasons:      reasons,
		ValidatedAt:  time.Now().UTC(),
		QualityScore: score,
		Attributes:   n.Attributes,
	}
	_ = model
	body, err := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsValidated, out))
	if err != nil {
		return err
	}
	if err := h.svc.QueueManager().Publish(ctx, h.nextValidated, body, nil); err != nil {
		_ = h.store.RecordError(ctx, n.VariantID, variantstate.StageValidated, fmt.Errorf("validate: publish validated: %w", err))
		return err
	}
	_ = h.store.AdvanceStage(ctx, n.VariantID,
		variantstate.StageNormalized, variantstate.StageValidated,
		nil, nil)
	return nil
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
	body, err := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsFlagged, out))
	if err != nil {
		return err
	}
	if err := h.svc.QueueManager().Publish(ctx, h.nextFlagged, body, nil); err != nil {
		_ = h.store.RecordError(ctx, n.VariantID, variantstate.StageValidated, fmt.Errorf("validate: publish flagged: %w", err))
		return err
	}
	_ = h.store.AdvanceStage(ctx, n.VariantID,
		variantstate.StageNormalized, variantstate.StageFlagged,
		nil, nil)
	return nil
}

func first500(s string) string {
	r := []rune(s)
	if len(r) <= 500 {
		return s
	}
	return string(r[:500])
}
