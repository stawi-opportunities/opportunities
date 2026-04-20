package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/telemetry"
)

var validateTracer = otel.Tracer("stawi.jobs.pipeline")

const validationPrompt = `You are a job data quality reviewer. Given extracted job posting data, assess its completeness and correctness. Output ONLY valid JSON.

Evaluate:
1. Is the title a real job title (not a category or website name)?
2. Is the description meaningful (not just a company description or boilerplate)?
3. Do the extracted skills match what's described in the job?
4. Is the seniority assessment reasonable for the described role?
5. Is the salary estimate plausible for the location and seniority?

Return:
{
  "valid": true/false,
  "confidence": 0.0-1.0,
  "issues": ["issue1", "issue2"],
  "recommendation": "accept" or "reject" or "flag"
}`

// validationResult is the structured response from the validation LLM call.
type validationResult struct {
	Valid          bool     `json:"valid"`
	Confidence     float64  `json:"confidence"`
	Issues         []string `json:"issues"`
	Recommendation string   `json:"recommendation"`
}

// ValidateHandler processes variant.normalized events and advances variants to
// variant.validated or variant.flagged depending on LLM quality review.
type ValidateHandler struct {
	jobRepo    *repository.JobRepository
	sourceRepo *repository.SourceRepository
	extractor  *extraction.Extractor
	svc        *frame.Service
}

// NewValidateHandler creates a ValidateHandler wired to the given dependencies.
func NewValidateHandler(
	jobRepo *repository.JobRepository,
	sourceRepo *repository.SourceRepository,
	extractor *extraction.Extractor,
	svc *frame.Service,
) *ValidateHandler {
	return &ValidateHandler{
		jobRepo:    jobRepo,
		sourceRepo: sourceRepo,
		extractor:  extractor,
		svc:        svc,
	}
}

// Name returns the event name this handler processes.
func (h *ValidateHandler) Name() string {
	return EventVariantNormalized
}

// PayloadType returns a zero-value pointer for JSON deserialization.
func (h *ValidateHandler) PayloadType() any {
	return &VariantPayload{}
}

// Validate checks the payload before execution.
func (h *ValidateHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*VariantPayload)
	if !ok {
		return errors.New("invalid payload type, expected *VariantPayload")
	}
	if p.VariantID == "" {
		return errors.New("variant_id is required")
	}
	return nil
}

// Execute reviews the normalized variant with an LLM and advances it to
// validated or flagged depending on the quality assessment.
func (h *ValidateHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*VariantPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	ctx, span := validateTracer.Start(ctx, "pipeline.validate")
	defer span.End()
	span.SetAttributes(
		attribute.String("variant_id", p.VariantID),
		attribute.String("source_id", p.SourceID),
	)

	start := time.Now()
	defer func() {
		if telemetry.StageDuration != nil {
			telemetry.StageDuration.Record(ctx, time.Since(start).Seconds(),
				metric.WithAttributes(attribute.String("stage", "validate")),
			)
		}
	}()

	// 1. Load the variant.
	variant, err := h.jobRepo.GetVariantByID(ctx, p.VariantID)
	if err != nil {
		return err
	}
	if variant == nil {
		util.Log(ctx).WithField("variant_id", p.VariantID).Info("validate: variant not found, skipping")
		return nil
	}

	// 2. Idempotency guard — only process normalized variants.
	if variant.Stage != domain.StageNormalized {
		return nil
	}

	// 3. Build review input from the variant's extracted fields.
	desc := variant.Description
	if len([]rune(desc)) > 500 {
		desc = string([]rune(desc)[:500])
	}
	reviewInput := strings.Join([]string{
		"Title: " + variant.Title,
		"Company: " + variant.Company,
		"Seniority: " + variant.Seniority,
		"Skills: " + variant.Skills,
		"Location: " + variant.LocationText,
		"Description (first 500 chars): " + desc,
	}, "\n")

	// 4. Call the LLM with the validation prompt. On failure (rate
	//    limit, provider outage), accept the variant with a neutral
	//    confidence rather than pile it on the retry queue — the
	//    deterministic quality gate already filtered obvious junk
	//    upstream, and blocking the pipeline on a transient LLM
	//    outage is worse than letting through a slightly-less-scrubbed
	//    job. Auto-validated variants can be re-scored later.
	raw, err := h.extractor.Prompt(ctx, validationPrompt, reviewInput)
	if err != nil {
		util.Log(ctx).WithError(err).
			WithField("variant_id", p.VariantID).
			Warn("validate: LLM failed, accepting with neutral confidence")
		if updateErr := h.jobRepo.UpdateValidation(ctx, variant.ID, string(domain.StageValidated), 0.5, "LLM unavailable: "+err.Error()); updateErr != nil {
			return updateErr
		}
		if emitErr := h.svc.EventsManager().Emit(ctx, EventVariantValidated, &VariantPayload{
			VariantID: variant.ID,
			SourceID:  variant.SourceID,
		}); emitErr != nil {
			util.Log(ctx).WithError(emitErr).
				WithField("variant_id", variant.ID).
				Warn("validate: emit degraded-validate event failed")
		}
		return nil
	}

	// 5. Parse response. If the AI returned non-JSON, flag the variant
	//    instead of returning an error that would cause infinite retries.
	var result validationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		util.Log(ctx).WithError(err).WithField("variant_id", p.VariantID).Warn("validate: AI response not parseable, flagging")
		if updateErr := h.jobRepo.UpdateValidation(ctx, variant.ID, string(domain.StageFlagged), 0, "AI response not parseable: "+err.Error()); updateErr != nil {
			return updateErr
		}
		_ = h.sourceRepo.IncrementQualityFlagged(ctx, variant.SourceID)
		return nil
	}

	// 6. Accept or flag the variant.
	if result.Valid && result.Confidence >= 0.7 {
		if err := h.jobRepo.UpdateValidation(ctx, variant.ID, string(domain.StageValidated), result.Confidence, ""); err != nil {
			return err
		}
		if err := h.sourceRepo.IncrementQualityValidated(ctx, variant.SourceID); err != nil {
			util.Log(ctx).WithError(err).WithField("source_id", variant.SourceID).Warn("validate: increment quality validated failed")
		}
		if telemetry.StageTransitions != nil {
			telemetry.StageTransitions.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("from", "normalized"),
					attribute.String("to", "validated"),
				),
			)
		}
		return h.svc.EventsManager().Emit(ctx, EventVariantValidated, &VariantPayload{
			VariantID: variant.ID,
			SourceID:  variant.SourceID,
		})
	}

	// 7. Flag the variant.
	issuesText := strings.Join(result.Issues, "; ")
	if err := h.jobRepo.UpdateValidation(ctx, variant.ID, string(domain.StageFlagged), result.Confidence, issuesText); err != nil {
		return err
	}
	if err := h.sourceRepo.IncrementQualityFlagged(ctx, variant.SourceID); err != nil {
		util.Log(ctx).WithError(err).WithField("source_id", variant.SourceID).Warn("validate: increment quality flagged failed")
	}

	// Check if the source quality rate warrants a review alert.
	rate, total, err := h.sourceRepo.GetQualityRate(ctx, variant.SourceID)
	if err != nil {
		util.Log(ctx).WithError(err).WithField("source_id", variant.SourceID).Warn("validate: get quality rate failed")
		return nil
	}
	if rate > 0.5 && total >= 10 {
		_ = h.svc.EventsManager().Emit(ctx, EventSourceQualityReview, &SourceQualityPayload{
			SourceID: variant.SourceID,
		})
	}

	return nil
}
