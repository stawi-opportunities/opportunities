package business

import (
	"context"
	"fmt"
	"strings"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/repository"
)

// MatchingTalent lists Stawi talent for a job description.
type MatchingTalent interface {
	ListForJob(ctx context.Context, tenantID, partitionID, jobID, title, description string, limit int) ([]models.TalentHit, error)
}

// OpportunityPublisher projects a job to the public board.
type OpportunityPublisher interface {
	Publish(ctx context.Context, job *models.Job) (opportunityID string, err error)
	Unpublish(ctx context.Context, job *models.Job) error
}

// BillingEmitter charges results on hire.
type BillingEmitter interface {
	EmitHire(ctx context.Context, outcome *models.HireOutcome) (billingRef string, err error)
}

// Notifier delivers interview invites.
type Notifier interface {
	EnqueueInterviewScheduled(ctx context.Context, interview *models.Interview, application *models.Application) error
}

// AIAssistant provides recruiter assist.
type AIAssistant interface {
	ScreenSummary(ctx context.Context, job *models.Job, application *models.Application) (string, error)
	SuggestDurationMin(ctx context.Context, job *models.Job) (int, error)
}

// DemoTalent is an in-process talent pool until matching KNN is wired.
type DemoTalent struct {
	pool []models.TalentHit
}

// NewDemoTalent returns keyword-rankable sample candidates.
func NewDemoTalent() *DemoTalent {
	return &DemoTalent{pool: []models.TalentHit{
		{ProfileID: "prof_amina_okello", CandidateID: "cand_amina", Score: 0.91, Summary: "Senior Go engineer, 7y fintech Nairobi. Kubernetes, Postgres, payments."},
		{ProfileID: "prof_james_mwangi", CandidateID: "cand_james", Score: 0.88, Summary: "Full-stack TypeScript/React, 5y. Built hiring tools and mobile-first dashboards."},
		{ProfileID: "prof_fatima_hassan", CandidateID: "cand_fatima", Score: 0.86, Summary: "Product designer (UI/UX), mobile-first. Design systems, Figma, user research."},
		{ProfileID: "prof_david_ochieng", CandidateID: "cand_david", Score: 0.84, Summary: "Data engineer Python/Spark, warehouse + dbt. Matching and ranking pipelines."},
		{ProfileID: "prof_grace_wambui", CandidateID: "cand_grace", Score: 0.83, Summary: "Recruiter-turned-ops; ATS workflows, interview coordination, agency placements."},
		{ProfileID: "prof_kevin_mutiso", CandidateID: "cand_kevin", Score: 0.81, Summary: "Backend Java/Spring, microservices. B2B SaaS, multi-tenant platforms."},
		{ProfileID: "prof_linda_atieno", CandidateID: "cand_linda", Score: 0.79, Summary: "ML engineer NLP, ranking models, embeddings. Production LLM tooling."},
		{ProfileID: "prof_brian_kamau", CandidateID: "cand_brian", Score: 0.77, Summary: "DevOps/SRE, Cloud Run, Terraform, observability."},
		{ProfileID: "prof_sarah_njoroge", CandidateID: "cand_sarah", Score: 0.75, Summary: "Frontend React/Vite, accessibility, i18n."},
		{ProfileID: "prof_peter_otieno", CandidateID: "cand_peter", Score: 0.72, Summary: "Mobile Flutter engineer, offline-first."},
	}}
}

func (d *DemoTalent) ListForJob(_ context.Context, _, _, _, title, description string, limit int) ([]models.TalentHit, error) {
	if limit <= 0 {
		limit = 10
	}
	q := strings.ToLower(title + " " + description)
	type scored struct {
		hit   models.TalentHit
		score float32
	}
	var ranked []scored
	for _, h := range d.pool {
		s := keywordScore(q, strings.ToLower(h.Summary+" "+h.ProfileID))
		s = s*0.7 + h.Score*0.3
		hit := h
		hit.Score = s
		ranked = append(ranked, scored{hit: hit, score: s})
	}
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	out := make([]models.TalentHit, 0, limit)
	for i := 0; i < len(ranked) && i < limit; i++ {
		out = append(out, ranked[i].hit)
	}
	return out, nil
}

func keywordScore(query, doc string) float32 {
	if query == "" {
		return 0.5
	}
	tokens := strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == ',' || r == '/' || r == '-' || r == '.'
	})
	var hits, total float32
	for _, t := range tokens {
		if len(t) < 3 {
			continue
		}
		total++
		if strings.Contains(doc, t) {
			hits++
		}
	}
	if total == 0 {
		return 0.5
	}
	return 0.4 + 0.6*(hits/total)
}

// LocalPublisher marks published with a stable projected opportunity id
// until opportunities writer is injected.
type LocalPublisher struct{}

func (LocalPublisher) Publish(_ context.Context, job *models.Job) (string, error) {
	if job == nil {
		return "", fmt.Errorf("ats: nil job")
	}
	if job.OpportunityID != "" {
		return job.OpportunityID, nil
	}
	return "opp_proj_" + job.ID, nil
}

func (LocalPublisher) Unpublish(context.Context, *models.Job) error { return nil }

// RecordingBilling returns a deterministic hire ref (results-based placeholder).
type RecordingBilling struct{}

func (RecordingBilling) EmitHire(_ context.Context, outcome *models.HireOutcome) (string, error) {
	if outcome == nil {
		return "", fmt.Errorf("ats: nil outcome")
	}
	return "result_hire_" + outcome.ApplicationID, nil
}

// OutboxNotifier writes interview.scheduled payloads including ICS.
type OutboxNotifier struct {
	Outbox repository.OutboxRepository
}

func (n OutboxNotifier) EnqueueInterviewScheduled(ctx context.Context, interview *models.Interview, application *models.Application) error {
	if n.Outbox == nil || interview == nil {
		return nil
	}
	appID := ""
	if application != nil {
		appID = application.ID
	}
	ics := models.BuildICS(interview, "", "", "")
	payload := fmt.Sprintf(`{"interview_id":%q,"application_id":%q,"ics":%q}`,
		interview.ID, appID, ics)
	return n.Outbox.Create(ctx, &models.OutboxMessage{
		Kind:           "interview.scheduled",
		PayloadJSON:    payload,
		IdempotencyKey: "interview.scheduled:" + interview.ID,
		Status:         "pending",
	})
}

// HeuristicAI produces useful text without an LLM key.
type HeuristicAI struct{}

func (HeuristicAI) ScreenSummary(_ context.Context, job *models.Job, application *models.Application) (string, error) {
	jt, js := "the role", ""
	if job != nil {
		jt = job.Title
		js = job.Description
	}
	sum := "(no candidate summary yet)"
	if application != nil && application.Summary != "" {
		sum = application.Summary
	}
	return fmt.Sprintf(
		"Screen for %q\n\nCandidate signal:\n%s\n\nSuggested focus:\n"+
			"1. Confirm must-have skills vs JD\n2. Probe recent delivery similar to: %s\n"+
			"3. Logistics (notice period, comp, location)\n4. Decision: advance to interview or reject with reason",
		jt, sum, trimRunes(js, 120),
	), nil
}

func (HeuristicAI) SuggestDurationMin(_ context.Context, job *models.Job) (int, error) {
	if job != nil && strings.Contains(strings.ToLower(job.Title), "design") {
		return 45, nil
	}
	return 30, nil
}

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
