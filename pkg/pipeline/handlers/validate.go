package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/pitabwire/frame"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
)

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
	if p.VariantID == 0 {
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

	// 1. Load the variant.
	variant, err := h.jobRepo.GetVariantByID(ctx, p.VariantID)
	if err != nil {
		return err
	}
	if variant == nil {
		log.Printf("validate: variant %d not found, skipping", p.VariantID)
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

	// 4. Call the LLM with the validation prompt.
	raw, err := h.extractor.Prompt(ctx, validationPrompt, reviewInput)
	if err != nil {
		return fmt.Errorf("validate: prompt failed for variant %d: %w", p.VariantID, err)
	}

	// 5. Parse response.
	var result validationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return fmt.Errorf("validate: parse response for variant %d: %w", p.VariantID, err)
	}

	// 6. Accept or flag the variant.
	if result.Valid && result.Confidence >= 0.7 {
		if err := h.jobRepo.UpdateValidation(ctx, variant.ID, string(domain.StageValidated), result.Confidence, ""); err != nil {
			return err
		}
		if err := h.sourceRepo.IncrementQualityValidated(ctx, variant.SourceID); err != nil {
			log.Printf("validate: increment quality validated for source %d: %v", variant.SourceID, err)
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
		log.Printf("validate: increment quality flagged for source %d: %v", variant.SourceID, err)
	}

	// Check if the source quality rate warrants a review alert.
	rate, total, err := h.sourceRepo.GetQualityRate(ctx, variant.SourceID)
	if err != nil {
		log.Printf("validate: get quality rate for source %d: %v", variant.SourceID, err)
		return nil
	}
	if rate > 0.5 && total >= 10 {
		_ = h.svc.EventsManager().Emit(ctx, EventSourceQualityReview, &SourceQualityPayload{
			SourceID: variant.SourceID,
		})
	}

	return nil
}
