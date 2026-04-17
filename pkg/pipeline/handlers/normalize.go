package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/content"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/telemetry"
)

var normalizeTracer = otel.Tracer("stawi.jobs.pipeline")

// NormalizeHandler processes variant.deduped events, runs AI extraction on the
// variant content, and advances variants to the normalized stage.
type NormalizeHandler struct {
	jobRepo    *repository.JobRepository
	sourceRepo *repository.SourceRepository
	extractor  *extraction.Extractor
	httpClient *httpx.Client
	svc        *frame.Service
}

// NewNormalizeHandler creates a NormalizeHandler wired to the given dependencies.
func NewNormalizeHandler(
	jobRepo *repository.JobRepository,
	sourceRepo *repository.SourceRepository,
	extractor *extraction.Extractor,
	httpClient *httpx.Client,
	svc *frame.Service,
) *NormalizeHandler {
	return &NormalizeHandler{
		jobRepo:    jobRepo,
		sourceRepo: sourceRepo,
		extractor:  extractor,
		httpClient: httpClient,
		svc:        svc,
	}
}

// Name returns the event name this handler processes.
func (h *NormalizeHandler) Name() string {
	return EventVariantDeduped
}

// PayloadType returns a zero-value pointer for JSON deserialization.
func (h *NormalizeHandler) PayloadType() any {
	return &VariantPayload{}
}

// Validate checks the payload before execution.
func (h *NormalizeHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*VariantPayload)
	if !ok {
		return errors.New("invalid payload type, expected *VariantPayload")
	}
	if p.VariantID == 0 {
		return errors.New("variant_id is required")
	}
	return nil
}

// Execute runs AI extraction on the variant, persists normalized fields, and
// emits variant.normalized (and optionally source.urls.discovered).
func (h *NormalizeHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*VariantPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	ctx, span := normalizeTracer.Start(ctx, "pipeline.normalize")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("variant_id", p.VariantID),
		attribute.Int64("source_id", p.SourceID),
	)

	start := time.Now()
	defer func() {
		if telemetry.StageDuration != nil {
			telemetry.StageDuration.Record(ctx, time.Since(start).Seconds(),
				metric.WithAttributes(attribute.String("stage", "normalize")),
			)
		}
	}()

	// 1. Load the variant.
	variant, err := h.jobRepo.GetVariantByID(ctx, p.VariantID)
	if err != nil {
		return err
	}
	if variant == nil {
		log.Printf("normalize: variant %d not found, skipping", p.VariantID)
		return nil
	}

	// 2. Idempotency guard — only process deduped variants.
	if variant.Stage != domain.StageDeduped {
		return nil
	}

	// 3. Choose content source: prefer Markdown, fall back to Description.
	// If both are empty (sitemap stubs), fetch the detail page.
	contentText := variant.Markdown
	if contentText == "" {
		contentText = variant.Description
	}
	if contentText == "" && variant.ApplyURL != "" && h.httpClient != nil {
		log.Printf("normalize: fetching detail page for variant %d: %s", variant.ID, variant.ApplyURL)
		if raw, _, fetchErr := h.httpClient.Get(ctx, variant.ApplyURL, nil); fetchErr == nil {
			extracted, _ := content.ExtractFromHTML(string(raw))
			if extracted != nil {
				contentText = extracted.Markdown
				// Store the fetched content for future reprocessing
				_ = h.jobRepo.UpdateStageWithContent(ctx, variant.ID, string(domain.StageDeduped),
					string(raw), extracted.CleanHTML, extracted.Markdown)
			}
		}
	}

	if contentText == "" {
		log.Printf("normalize: variant %d has no content to extract, skipping", variant.ID)
		return nil
	}

	// 4. Build the extraction input.
	input := fmt.Sprintf("Job Title: %s\nCompany: %s\n\n%s",
		variant.Title, variant.Company, contentText)

	// 5. Call AI extractor — no artificial timeout, let the model finish.
	if telemetry.AIExtractions != nil {
		telemetry.AIExtractions.Add(ctx, 1)
	}
	fields, err := h.extractor.Extract(ctx, input, variant.ApplyURL)
	if err != nil {
		if telemetry.AIFailures != nil {
			telemetry.AIFailures.Add(ctx, 1)
		}
		return fmt.Errorf("normalize: extraction failed for variant %d: %w", variant.ID, err)
	}

	// 6. Build update map from extracted fields (only overwrite if non-empty).
	updates := map[string]any{
		"stage": string(domain.StageNormalized),
	}

	if fields.Title != "" {
		updates["title"] = fields.Title
	}
	if fields.Company != "" {
		updates["company"] = fields.Company
	}
	if fields.Location != "" {
		updates["location_text"] = fields.Location
	}
	if fields.Description != "" {
		updates["description"] = fields.Description
	}
	if fields.ApplyURL != "" {
		updates["apply_url"] = fields.ApplyURL
	}
	if fields.EmploymentType != "" {
		updates["employment_type"] = fields.EmploymentType
	}
	if fields.RemoteType != "" {
		updates["remote_type"] = fields.RemoteType
	}
	if fields.Currency != "" {
		updates["currency"] = fields.Currency
	}
	if fields.SalaryMin != "" {
		var salMin float64
		if _, scanErr := fmt.Sscanf(fields.SalaryMin, "%f", &salMin); scanErr == nil && salMin > 0 {
			updates["salary_min"] = salMin
		}
	}
	if fields.SalaryMax != "" {
		var salMax float64
		if _, scanErr := fmt.Sscanf(fields.SalaryMax, "%f", &salMax); scanErr == nil && salMax > 0 {
			updates["salary_max"] = salMax
		}
	}
	if fields.Seniority != "" {
		updates["seniority"] = fields.Seniority
	}
	if len(fields.Skills) > 0 {
		updates["skills"] = strings.Join(fields.Skills, ", ")
	}
	if len(fields.Roles) > 0 {
		updates["roles"] = strings.Join(fields.Roles, ", ")
	}
	if len(fields.Benefits) > 0 {
		updates["benefits"] = strings.Join(fields.Benefits, ", ")
	}
	if fields.ContactName != "" {
		updates["contact_name"] = fields.ContactName
	}
	if fields.ContactEmail != "" {
		updates["contact_email"] = fields.ContactEmail
	}
	if fields.Department != "" {
		updates["department"] = fields.Department
	}
	if fields.Industry != "" {
		updates["industry"] = fields.Industry
	}
	if fields.Education != "" {
		updates["education"] = fields.Education
	}
	if fields.Experience != "" {
		updates["experience"] = fields.Experience
	}
	if fields.Deadline != "" {
		updates["deadline"] = fields.Deadline
	}
	if fields.UrgencyLevel != "" {
		updates["urgency_level"] = fields.UrgencyLevel
	}
	if len(fields.UrgencySignals) > 0 {
		updates["urgency_signals"] = strings.Join(fields.UrgencySignals, ", ")
	}
	if fields.HiringTimeline != "" {
		updates["hiring_timeline"] = fields.HiringTimeline
	}
	if fields.InterviewStages > 0 {
		updates["interview_stages"] = fields.InterviewStages
	}
	if fields.HasTakeHome {
		updates["has_take_home"] = fields.HasTakeHome
	}
	if fields.FunnelComplexity != "" {
		updates["funnel_complexity"] = fields.FunnelComplexity
	}
	if fields.CompanySize != "" {
		updates["company_size"] = fields.CompanySize
	}
	if fields.FundingStage != "" {
		updates["funding_stage"] = fields.FundingStage
	}
	if len(fields.RequiredSkills) > 0 {
		updates["required_skills"] = strings.Join(fields.RequiredSkills, ", ")
	}
	if len(fields.NiceToHaveSkills) > 0 {
		updates["nice_to_have_skills"] = strings.Join(fields.NiceToHaveSkills, ", ")
	}
	if len(fields.ToolsFrameworks) > 0 {
		updates["tools_frameworks"] = strings.Join(fields.ToolsFrameworks, ", ")
	}
	if fields.GeoRestrictions != "" {
		updates["geo_restrictions"] = fields.GeoRestrictions
	}
	if fields.TimezoneReq != "" {
		updates["timezone_req"] = fields.TimezoneReq
	}
	if fields.ApplicationType != "" {
		updates["application_type"] = fields.ApplicationType
	}
	if fields.ATSPlatform != "" {
		updates["ats_platform"] = fields.ATSPlatform
	}
	if fields.RoleScope != "" {
		updates["role_scope"] = fields.RoleScope
	}
	if fields.TeamSize != "" {
		updates["team_size"] = fields.TeamSize
	}
	if fields.ReportsTo != "" {
		updates["reports_to"] = fields.ReportsTo
	}

	// 7. Persist all normalized fields + stage change in a single update.
	if err := h.jobRepo.UpdateVariantFields(ctx, variant.ID, updates); err != nil {
		return fmt.Errorf("normalize: persist fields for variant %d: %w", variant.ID, err)
	}

	if telemetry.StageTransitions != nil {
		telemetry.StageTransitions.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("from", "deduped"),
				attribute.String("to", "normalized"),
			),
		)
	}

	// 8. Emit variant.normalized.
	if err := h.svc.EventsManager().Emit(ctx, EventVariantNormalized, &VariantPayload{
		VariantID: variant.ID,
		SourceID:  variant.SourceID,
	}); err != nil {
		return err
	}

	// 9. If the AI surfaced any apply-URL style discovered links, emit them too.
	//    We reuse the apply_url field as the primary discovered URL when it differs
	//    from the stored one, and collect any extra URLs from the extracted fields.
	var discoveredURLs []string
	if fields.ApplyURL != "" && fields.ApplyURL != variant.ApplyURL {
		discoveredURLs = append(discoveredURLs, fields.ApplyURL)
	}

	if len(discoveredURLs) > 0 {
		if emitErr := h.svc.EventsManager().Emit(ctx, EventSourceURLsDiscovered, &SourceURLsPayload{
			SourceID: variant.SourceID,
			URLs:     discoveredURLs,
		}); emitErr != nil {
			log.Printf("normalize: failed to emit %s for variant %d: %v",
				EventSourceURLsDiscovered, variant.ID, emitErr)
		}
	}

	return nil
}
