package ats

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// DemoTalent is an in-process talent pool so recruiters get useful shortlists
// without matching/pgvector. Keyword-scores title/summary against the job.
type DemoTalent struct {
	mu   sync.RWMutex
	pool []TalentHit
}

// NewDemoTalent returns a pool with realistic sample candidates.
func NewDemoTalent() *DemoTalent {
	return &DemoTalent{pool: defaultDemoPool()}
}

func defaultDemoPool() []TalentHit {
	return []TalentHit{
		{ProfileID: "prof_amina_okello", CandidateID: "cand_amina", Score: 0.91, Summary: "Senior Go engineer, 7y fintech Nairobi. Kubernetes, Postgres, payments."},
		{ProfileID: "prof_james_mwangi", CandidateID: "cand_james", Score: 0.88, Summary: "Full-stack TypeScript/React, 5y. Built hiring tools and mobile-first dashboards."},
		{ProfileID: "prof_fatima_hassan", CandidateID: "cand_fatima", Score: 0.86, Summary: "Product designer (UI/UX), mobile-first. Design systems, Figma, user research."},
		{ProfileID: "prof_david_ochieng", CandidateID: "cand_david", Score: 0.84, Summary: "Data engineer Python/Spark, warehouse + dbt. Matching and ranking pipelines."},
		{ProfileID: "prof_grace_wambui", CandidateID: "cand_grace", Score: 0.83, Summary: "Recruiter-turned-ops; ATS workflows, interview coordination, agency placements."},
		{ProfileID: "prof_kevin_mutiso", CandidateID: "cand_kevin", Score: 0.81, Summary: "Backend Java/Spring, microservices. B2B SaaS, multi-tenant platforms."},
		{ProfileID: "prof_linda_atieno", CandidateID: "cand_linda", Score: 0.79, Summary: "ML engineer NLP, ranking models, embeddings. Production LLM tooling."},
		{ProfileID: "prof_brian_kamau", CandidateID: "cand_brian", Score: 0.77, Summary: "DevOps/SRE, Cloud Run, Terraform, observability. CI/CD for multi-service monorepos."},
		{ProfileID: "prof_sarah_njoroge", CandidateID: "cand_sarah", Score: 0.75, Summary: "Frontend React/Vite, accessibility, i18n. Job-seeker product experience."},
		{ProfileID: "prof_peter_otieno", CandidateID: "cand_peter", Score: 0.72, Summary: "Mobile Flutter engineer, offline-first. Push notifications and chat."},
	}
}

func (d *DemoTalent) ListForJob(_ context.Context, _, _, _, title, description string, limit int) ([]TalentHit, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if limit <= 0 {
		limit = 10
	}
	q := strings.ToLower(title + " " + description)
	type scored struct {
		hit   TalentHit
		score float32
	}
	var ranked []scored
	for _, h := range d.pool {
		s := keywordScore(q, strings.ToLower(h.Summary+" "+h.ProfileID))
		// Keep base score influence.
		s = s*0.7 + h.Score*0.3
		hit := h
		hit.Score = s
		ranked = append(ranked, scored{hit: hit, score: s})
	}
	// simple selection sort top N
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	out := make([]TalentHit, 0, limit)
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
	var hits float32
	var total float32
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

// SeedDemoWorkspace creates a sample open job + availability so the UI is useful immediately.
func SeedDemoWorkspace(ctx context.Context, svc *Service) error {
	jobs, err := svc.ListJobs(ctx, "")
	if err != nil {
		return err
	}
	if len(jobs) > 0 {
		return nil // already has data
	}
	j, err := svc.CreateJob(ctx, CreateJobInput{
		Title:       "Senior Backend Engineer (Go)",
		Description: "Build multi-tenant hiring APIs, Postgres, Cloud Run. Experience with matching, payments, or ATS a plus. Mobile-first product mindset.",
		Location:    "Nairobi / Remote East Africa",
		Status:      JobStatusOpen,
	})
	if err != nil {
		return fmt.Errorf("seed job: %w", err)
	}
	_, err = svc.SetAvailability(ctx, SetAvailabilityInput{
		Timezone: "Africa/Nairobi",
		Rules: []WeekRule{
			{Weekday: 1, Start: "09:00", End: "12:00"},
			{Weekday: 1, Start: "14:00", End: "17:00"},
			{Weekday: 2, Start: "09:00", End: "12:00"},
			{Weekday: 2, Start: "14:00", End: "17:00"},
			{Weekday: 3, Start: "09:00", End: "12:00"},
			{Weekday: 3, Start: "14:00", End: "17:00"},
			{Weekday: 4, Start: "09:00", End: "12:00"},
			{Weekday: 4, Start: "14:00", End: "17:00"},
			{Weekday: 5, Start: "09:00", End: "13:00"},
		},
	})
	if err != nil {
		return fmt.Errorf("seed availability: %w", err)
	}
	// Pre-add top demo talent into pipeline so Pipeline is not empty.
	hits, _ := svc.ListTalent(ctx, j.ID, 3)
	for _, h := range hits {
		if _, err := svc.AddTalent(ctx, j.ID, h); err != nil {
			// ignore conflicts
			continue
		}
	}
	// Second job for variety
	_, _ = svc.CreateJob(ctx, CreateJobInput{
		Title:       "Product Designer — Mobile hiring UX",
		Description: "Design simple recruiter flows: pipeline, interview scheduling, candidate self-serve. Portfolio required.",
		Location:    "Remote",
		Status:      JobStatusOpen,
	})
	return nil
}

// LocalPublisher marks jobs published with a stable projected opportunity id
// without requiring the crawl/opportunities stack (useful for local ATS).
type LocalPublisher struct{}

func (LocalPublisher) Publish(_ context.Context, job *Job) (string, error) {
	if job == nil {
		return "", fmt.Errorf("ats: nil job")
	}
	if job.OpportunityID != "" {
		return job.OpportunityID, nil
	}
	return "opp_proj_" + job.ID, nil
}

func (LocalPublisher) Unpublish(context.Context, *Job) error { return nil }

// RecordingBilling stores ref for audit (results-based placeholder).
type RecordingBilling struct{}

func (RecordingBilling) EmitHire(_ context.Context, outcome *HireOutcome) (string, error) {
	if outcome == nil {
		return "", fmt.Errorf("ats: nil outcome")
	}
	return "result_hire_" + outcome.ApplicationID, nil
}

// OutboxNotifier writes interview.scheduled payloads including ICS text.
type OutboxNotifier struct {
	Store *Store
}

func (n OutboxNotifier) EnqueueInterviewScheduled(ctx context.Context, interview *Interview, application *Application) error {
	if n.Store == nil || interview == nil {
		return nil
	}
	jobTitle := ""
	if application != nil {
		// optional: caller may enrich
		_ = application
	}
	ics := BuildICS(interview, jobTitle, "", "")
	payload := fmt.Sprintf(`{"interview_id":%q,"application_id":%q,"ics":%q}`,
		interview.ID, interview.ApplicationID, ics)
	return n.Store.CreateOutbox(ctx, &OutboxMessage{
		Kind:           "interview.scheduled",
		PayloadJSON:    payload,
		IdempotencyKey: "interview.scheduled:" + interview.ID,
		Status:         "pending",
	})
}

// HeuristicAI produces useful text without an LLM key.
type HeuristicAI struct{}

func (HeuristicAI) ScreenSummary(_ context.Context, job *Job, application *Application) (string, error) {
	jt, js := "the role", ""
	if job != nil {
		jt = job.Title
		js = job.Description
	}
	sum := ""
	if application != nil {
		sum = application.Summary
		if sum == "" {
			sum = "(no candidate summary yet — add notes or import from Stawi talent)"
		}
	}
	return fmt.Sprintf(
		"Screen for %q\n\nCandidate signal:\n%s\n\nSuggested focus:\n"+
			"1. Confirm must-have skills vs JD\n2. Probe recent delivery similar to: %s\n3. Logistics (notice period, comp, location)\n4. Decision: advance to interview or reject with reason",
		jt, sum, trimRunes(js, 120),
	), nil
}

func (HeuristicAI) SuggestDurationMin(_ context.Context, job *Job) (int, error) {
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
