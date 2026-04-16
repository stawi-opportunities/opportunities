package events

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
)

// JobEnrichmentEventName is the event for async AI enrichment of canonical jobs.
const JobEnrichmentEventName = "job.canonical.enrichment"

// JobEnrichmentPayload carries the canonical job ID and its description text.
type JobEnrichmentPayload struct {
	CanonicalJobID int64  `json:"canonical_job_id"`
	Description    string `json:"description"`
	Title          string `json:"title"`
	Company        string `json:"company"`
	ApplyURL       string `json:"apply_url"`
}

// JobEnrichmentHandler extracts intelligence fields (seniority, skills, industry, etc.)
// from a job's description using AI, then updates the canonical job.
type JobEnrichmentHandler struct {
	extractor *extraction.Extractor
	jobRepo   *repository.JobRepository
}

func NewJobEnrichmentHandler(
	extractor *extraction.Extractor,
	jobRepo *repository.JobRepository,
) *JobEnrichmentHandler {
	return &JobEnrichmentHandler{extractor: extractor, jobRepo: jobRepo}
}

func (h *JobEnrichmentHandler) Name() string    { return JobEnrichmentEventName }
func (h *JobEnrichmentHandler) PayloadType() any { return &JobEnrichmentPayload{} }

func (h *JobEnrichmentHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*JobEnrichmentPayload)
	if !ok {
		return errors.New("invalid payload")
	}
	if p.CanonicalJobID == 0 {
		return errors.New("canonical_job_id required")
	}
	return nil
}

func (h *JobEnrichmentHandler) Execute(ctx context.Context, payload any) error {
	p := payload.(*JobEnrichmentPayload)
	log := util.Log(ctx)

	// Build content for AI — use description + title + company for context
	content := fmt.Sprintf("Job Title: %s\nCompany: %s\n\n%s", p.Title, p.Company, p.Description)

	fields, err := h.extractor.Extract(ctx, content, p.ApplyURL)
	if err != nil {
		log.WithError(err).WithField("canonical_id", p.CanonicalJobID).Warn("enrichment failed")
		return err // returning error triggers retry
	}

	// Build update map for only non-empty extracted fields
	updates := make(map[string]any)

	if fields.Seniority != "" {
		updates["seniority"] = strings.ToLower(fields.Seniority)
	}
	if len(fields.Skills) > 0 {
		updates["skills"] = strings.Join(fields.Skills, ", ")
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
	if fields.Industry != "" {
		updates["industry"] = fields.Industry
	}
	if fields.Department != "" {
		updates["department"] = fields.Department
	}
	if fields.Education != "" {
		updates["education"] = fields.Education
	}
	if fields.Experience != "" {
		updates["experience"] = fields.Experience
	}
	if fields.RemoteType != "" {
		updates["remote_type"] = strings.ToLower(fields.RemoteType)
	}
	if fields.EmploymentType != "" {
		updates["employment_type"] = strings.ToLower(fields.EmploymentType)
	}
	if fields.SalaryMin != "" {
		updates["salary_min"] = fields.SalaryMin
	}
	if fields.SalaryMax != "" {
		updates["salary_max"] = fields.SalaryMax
	}
	if fields.Currency != "" {
		updates["currency"] = strings.ToUpper(fields.Currency)
	}
	if fields.UrgencyLevel != "" {
		updates["urgency_level"] = strings.ToLower(fields.UrgencyLevel)
	}
	if fields.CompanySize != "" {
		updates["company_size"] = strings.ToLower(fields.CompanySize)
	}
	if fields.RoleScope != "" {
		updates["role_scope"] = strings.ToLower(fields.RoleScope)
	}
	if fields.GeoRestrictions != "" {
		updates["geo_restrictions"] = strings.ToLower(fields.GeoRestrictions)
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

	if len(updates) == 0 {
		return nil // nothing to update
	}

	// Update the canonical job
	if err := h.jobRepo.UpdateCanonicalFields(ctx, p.CanonicalJobID, updates); err != nil {
		return err
	}

	// Recompute quality score with new data
	h.jobRepo.RecomputeQualityScore(ctx, p.CanonicalJobID)

	log.WithField("canonical_id", p.CanonicalJobID).
		WithField("fields_updated", len(updates)).
		Info("job enriched")

	return nil
}
