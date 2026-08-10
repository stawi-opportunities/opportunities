package ats

import "context"

// TalentHit is a matching candidate shortlist entry (no PII copy).
type TalentHit struct {
	ProfileID   string  `json:"profile_id"`
	CandidateID string  `json:"candidate_id,omitempty"`
	Score       float32 `json:"score,omitempty"`
	Summary     string  `json:"summary,omitempty"`
}

// MatchingTalent lists Stawi talent for a job description.
type MatchingTalent interface {
	ListForJob(ctx context.Context, tenantID, partitionID, jobID, title, description string, limit int) ([]TalentHit, error)
}

// OpportunityPublisher projects a job to the public board.
type OpportunityPublisher interface {
	Publish(ctx context.Context, job *Job) (opportunityID string, err error)
	Unpublish(ctx context.Context, job *Job) error
}

// BillingEmitter charges results on hire (idempotent by key).
type BillingEmitter interface {
	EmitHire(ctx context.Context, outcome *HireOutcome) (billingRef string, err error)
}

// Notifier delivers interview invites (email/ICS).
type Notifier interface {
	EnqueueInterviewScheduled(ctx context.Context, interview *Interview, application *Application) error
}

// AIAssistant provides recruiter assist (may be stub).
type AIAssistant interface {
	ScreenSummary(ctx context.Context, job *Job, application *Application) (string, error)
	SuggestDurationMin(ctx context.Context, job *Job) (int, error)
}

// Nop implementations for wiring without peers.

type NopMatching struct{}

func (NopMatching) ListForJob(context.Context, string, string, string, string, string, int) ([]TalentHit, error) {
	return nil, nil
}

type NopPublisher struct{}

func (NopPublisher) Publish(context.Context, *Job) (string, error) { return "", nil }
func (NopPublisher) Unpublish(context.Context, *Job) error         { return nil }

type NopBilling struct{}

func (NopBilling) EmitHire(context.Context, *HireOutcome) (string, error) { return "nop", nil }

type NopNotifier struct{}

func (NopNotifier) EnqueueInterviewScheduled(context.Context, *Interview, *Application) error {
	return nil
}

type NopAI struct{}

func (NopAI) ScreenSummary(context.Context, *Job, *Application) (string, error) {
	return "No summary available.", nil
}
func (NopAI) SuggestDurationMin(context.Context, *Job) (int, error) { return 30, nil }
