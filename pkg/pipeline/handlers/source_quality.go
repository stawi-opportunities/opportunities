package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
)

const sourceReviewPrompt = `You are a job source quality assessor. Given sample job postings from a source, assess whether this source provides valuable job data worth continuing to crawl.

Evaluate:
1. Are the job postings real and specific (not spam, duplicates, or aggregated noise)?
2. Is the extracted data generally complete (titles, companies, descriptions)?
3. Are there consistent patterns of bad data (missing fields, garbled text, irrelevant content)?

Return ONLY valid JSON:
{
  "recommendation": "continue" or "reduce_frequency" or "pause" or "disable",
  "reason": "brief explanation",
  "quality_score": 0.0-1.0
}`

// sourceReviewResponse is the parsed AI response for a quality review.
type sourceReviewResponse struct {
	Recommendation string  `json:"recommendation"`
	Reason         string  `json:"reason"`
	QualityScore   float64 `json:"quality_score"`
}

// SourceQualityHandler processes source.quality.review events and uses AI to
// assess source quality, adjusting crawl frequency or pausing low-quality sources.
type SourceQualityHandler struct {
	sourceRepo *repository.SourceRepository
	jobRepo    *repository.JobRepository
	extractor  *extraction.Extractor
}

// NewSourceQualityHandler creates a SourceQualityHandler wired to the given repos
// and extractor.
func NewSourceQualityHandler(
	sourceRepo *repository.SourceRepository,
	jobRepo *repository.JobRepository,
	extractor *extraction.Extractor,
) *SourceQualityHandler {
	return &SourceQualityHandler{
		sourceRepo: sourceRepo,
		jobRepo:    jobRepo,
		extractor:  extractor,
	}
}

// Name returns the event name this handler processes.
func (h *SourceQualityHandler) Name() string {
	return EventSourceQualityReview
}

// PayloadType returns a zero-value pointer for JSON deserialization.
func (h *SourceQualityHandler) PayloadType() any {
	return &SourceQualityPayload{}
}

// Validate checks the payload before execution.
func (h *SourceQualityHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*SourceQualityPayload)
	if !ok {
		return errors.New("invalid payload type, expected *SourceQualityPayload")
	}
	if p.SourceID == 0 {
		return errors.New("source_id is required")
	}
	return nil
}

// Execute loads sample variants for the source, asks the AI to review quality,
// then applies the recommendation (continue / reduce_frequency / pause / disable).
func (h *SourceQualityHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*SourceQualityPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	// 1. Load the source.
	src, err := h.sourceRepo.GetByID(ctx, p.SourceID)
	if err != nil {
		return fmt.Errorf("source_quality: load source %d: %w", p.SourceID, err)
	}

	// 2. Load up to 10 validated variants from this source.
	validated, err := h.jobRepo.ListByStageAndSource(ctx, p.SourceID, string(domain.StageValidated), 10)
	if err != nil {
		return fmt.Errorf("source_quality: list validated variants: %w", err)
	}

	// Also load validated via "ready" stage.
	ready, err := h.jobRepo.ListByStageAndSource(ctx, p.SourceID, string(domain.StageReady), 10)
	if err != nil {
		return fmt.Errorf("source_quality: list ready variants: %w", err)
	}

	// 3. Load up to 10 flagged variants from this source.
	flagged, err := h.jobRepo.ListByStageAndSource(ctx, p.SourceID, string(domain.StageFlagged), 10)
	if err != nil {
		return fmt.Errorf("source_quality: list flagged variants: %w", err)
	}

	// 4. Build sample text from all variants.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Source: %s (ID: %d)\n\n", src.BaseURL, src.ID))

	goodSamples := append(validated, ready...)
	if len(goodSamples) > 10 {
		goodSamples = goodSamples[:10]
	}

	sb.WriteString(fmt.Sprintf("Good samples (%d):\n", len(goodSamples)))
	for i, v := range goodSamples {
		desc := v.Description
		if len([]rune(desc)) > 200 {
			desc = string([]rune(desc)[:200])
		}
		sb.WriteString(fmt.Sprintf("%d. Title: %s | Company: %s | Description: %s\n",
			i+1, v.Title, v.Company, desc))
	}

	sb.WriteString(fmt.Sprintf("\nFlagged samples (%d):\n", len(flagged)))
	for i, v := range flagged {
		desc := v.Description
		if len([]rune(desc)) > 200 {
			desc = string([]rune(desc)[:200])
		}
		sb.WriteString(fmt.Sprintf("%d. Title: %s | Company: %s | Description: %s\n",
			i+1, v.Title, v.Company, desc))
	}

	// 5–6. Call AI for review.
	rawResponse, err := h.extractor.Prompt(ctx, sourceReviewPrompt, sb.String())
	if err != nil {
		return fmt.Errorf("source_quality: extractor prompt: %w", err)
	}

	// 7. Parse the AI response.
	var review sourceReviewResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawResponse)), &review); err != nil {
		return fmt.Errorf("source_quality: parse ai response: %w (raw: %.200s)", err, rawResponse)
	}

	// 8. Apply the recommendation.
	switch review.Recommendation {
	case "continue":
		if err := h.sourceRepo.ResetQualityWindow(ctx, p.SourceID); err != nil {
			return fmt.Errorf("source_quality: reset quality window: %w", err)
		}

	case "reduce_frequency":
		if err := h.sourceRepo.ReduceCrawlFrequency(ctx, p.SourceID); err != nil {
			return fmt.Errorf("source_quality: reduce crawl frequency: %w", err)
		}

	case "pause":
		if err := h.sourceRepo.PauseSource(ctx, p.SourceID); err != nil {
			return fmt.Errorf("source_quality: pause source: %w", err)
		}

	case "disable":
		// Safety guard: only disable if already paused — can't jump straight to disabled.
		if src.Status == domain.SourcePaused {
			if err := h.sourceRepo.DisableSource(ctx, p.SourceID); err != nil {
				return fmt.Errorf("source_quality: disable source: %w", err)
			}
		} else {
			log.Printf("source_quality: source %d not paused (status=%s), pausing first instead of disabling", p.SourceID, src.Status)
			if err := h.sourceRepo.PauseSource(ctx, p.SourceID); err != nil {
				return fmt.Errorf("source_quality: pause source (before disable): %w", err)
			}
		}

	default:
		log.Printf("source_quality: unknown recommendation %q for source %d, ignoring",
			review.Recommendation, p.SourceID)
	}

	log.Printf("source_quality: source %d recommendation=%q score=%.2f reason=%q",
		p.SourceID, review.Recommendation, review.QualityScore, review.Reason)

	return nil
}
