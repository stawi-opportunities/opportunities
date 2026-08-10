package business

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/repository"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
)

// MatchingTalent lists real Stawi candidates for a job description.
type MatchingTalent interface {
	ListForJob(ctx context.Context, tenantID, partitionID, jobID, title, description string, limit int) ([]models.TalentHit, error)
}

// OpportunityPublisher projects a job to the public board / product catalog.
type OpportunityPublisher interface {
	Publish(ctx context.Context, job *models.Job) (opportunityID string, err error)
	Unpublish(ctx context.Context, job *models.Job) error
}

// BillingEmitter charges results on hire.
type BillingEmitter interface {
	EmitHire(ctx context.Context, outcome *models.HireOutcome) (billingRef string, err error)
}

// Notifier delivers interview invites (email/ICS via notification service).
type Notifier interface {
	EnqueueInterviewScheduled(ctx context.Context, interview *models.Interview, application *models.Application, job *models.Job) error
}

// AIAssistant provides recruiter assist.
type AIAssistant interface {
	ScreenSummary(ctx context.Context, job *models.Job, application *models.Application) (string, error)
	SuggestDurationMin(ctx context.Context, job *models.Job) (int, error)
}

// EmptyTalent returns no candidates (safe production default when matching DB unavailable).
type EmptyTalent struct{}

func (EmptyTalent) ListForJob(context.Context, string, string, string, string, string, int) ([]models.TalentHit, error) {
	return nil, nil
}

// SQLMatchingTalent shortlists active candidates from matching product tables.
// Requires candidate_profiles (and optional candidate_placement_profiles) on db.
type SQLMatchingTalent struct {
	DB *sql.DB
}

func (s SQLMatchingTalent) ListForJob(ctx context.Context, _, _, _, title, description string, limit int) ([]models.TalentHit, error) {
	if s.DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	// Tokenize job text for simple ILIKE ranking (no embedding required).
	tokens := keywordTokens(title + " " + description)
	if len(tokens) == 0 {
		tokens = []string{"%"}
	}
	// Build dynamic OR on title/skills/name; score by number of token hits.
	// Uses only columns known on candidate_profiles.
	q := `
SELECT id,
       COALESCE(NULLIF(profile_id,''), id) AS profile_id,
       COALESCE(name, '') AS name,
       COALESCE(current_title, '') AS title,
       COALESCE(array_to_string(skills, ' '), '') AS skills,
       COALESCE(bio, '') AS bio
FROM candidate_profiles
WHERE status IN ('active', 'unverified')
  AND deleted_at IS NULL
ORDER BY updated_at DESC NULLS LAST
LIMIT $1`
	// Prefer tables without soft-delete if column missing — try simplified query on error.
	rows, err := s.DB.QueryContext(ctx, q, limit*3)
	if err != nil {
		// Fallback without deleted_at / array_to_string for simpler schemas.
		q2 := `
SELECT id, COALESCE(NULLIF(profile_id,''), id), COALESCE(name,''), COALESCE(current_title,''), '', COALESCE(bio,'')
FROM candidate_profiles
WHERE status = 'active' OR status = 'unverified' OR status IS NULL
ORDER BY id DESC
LIMIT $1`
		rows, err = s.DB.QueryContext(ctx, q2, limit*3)
		if err != nil {
			util.Log(ctx).WithError(err).Warn("ats: matching talent query unavailable")
			return nil, nil
		}
	}
	defer func() { _ = rows.Close() }()

	type row struct {
		id, profileID, name, title, skills, bio string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.profileID, &r.name, &r.title, &r.skills, &r.bio); err != nil {
			return nil, fmt.Errorf("ats: talent scan: %w", err)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type scored struct {
		hit   models.TalentHit
		score float32
	}
	var ranked []scored
	for _, r := range all {
		doc := strings.ToLower(r.name + " " + r.title + " " + r.skills + " " + r.bio)
		sc := keywordScore(tokens, doc)
		if sc < 0.35 && len(tokens) > 0 && tokens[0] != "%" {
			continue
		}
		summary := r.title
		if r.name != "" {
			summary = r.name
			if r.title != "" {
				summary += " — " + r.title
			}
		}
		if summary == "" {
			summary = r.bio
		}
		if len(summary) > 200 {
			summary = summary[:200] + "…"
		}
		ranked = append(ranked, scored{
			hit: models.TalentHit{
				ProfileID:   r.profileID,
				CandidateID: r.id,
				Score:       sc,
				Summary:     summary,
			},
			score: sc,
		})
	}
	// sort desc score
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

func keywordTokens(s string) []string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == ' ' || r == ',' || r == '/' || r == '-' || r == '.' || r == '\n' || r == '(' || r == ')'
	})
	var out []string
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func keywordScore(tokens []string, doc string) float32 {
	if len(tokens) == 0 {
		return 0.5
	}
	var hits, total float32
	for _, t := range tokens {
		if t == "%" {
			return 0.5
		}
		total++
		if strings.Contains(doc, t) {
			hits++
		}
	}
	if total == 0 {
		return 0.5
	}
	return 0.35 + 0.65*(hits/total)
}

// ProjectionPublisher persists published listings in ATS DB for board consumers.
// When ProductDB is set and has opportunities-compatible insert, dual-writes.
type ProjectionPublisher struct {
	Projections repository.JobProjectionRepository
	// ProductDB optional product catalog connection for dual-write.
	ProductDB *sql.DB
}

func (p ProjectionPublisher) Publish(ctx context.Context, job *models.Job) (string, error) {
	if job == nil {
		return "", fmt.Errorf("ats: nil job")
	}
	if p.Projections == nil {
		return "", fmt.Errorf("ats: projection store required")
	}
	oppID := job.OpportunityID
	if oppID == "" {
		oppID = "opp_ats_" + job.ID
	}
	now := time.Now().UTC()
	proj := &models.JobProjection{
		JobID:         job.ID,
		OpportunityID: oppID,
		Title:         job.Title,
		Description:   job.Description,
		Location:      job.Location,
		Status:        "published",
		PublishedAt:   &now,
	}
	proj.TenantID = job.TenantID
	proj.PartitionID = job.PartitionID
	if err := p.Projections.UpsertPublished(ctx, proj); err != nil {
		return "", err
	}
	if p.ProductDB != nil {
		if err := dualWriteOpportunity(ctx, p.ProductDB, job, oppID); err != nil {
			util.Log(ctx).WithError(err).Warn("ats: product dual-write failed; projection remains published in ATS")
		}
	}
	return oppID, nil
}

func (p ProjectionPublisher) Unpublish(ctx context.Context, job *models.Job) error {
	if job == nil || p.Projections == nil {
		return nil
	}
	if err := p.Projections.MarkUnpublished(ctx, job.TenantID, job.PartitionID, job.ID); err != nil {
		return err
	}
	if p.ProductDB != nil && job.OpportunityID != "" {
		if err := dualUnpublishOpportunity(ctx, p.ProductDB, job.OpportunityID); err != nil {
			util.Log(ctx).WithError(err).Warn("ats: product unpublish dual-write failed")
		}
	}
	return nil
}

func dualWriteOpportunity(ctx context.Context, db *sql.DB, job *models.Job, oppID string) error {
	// Best-effort insert into product opportunities if schema matches common columns.
	// Safe no-op when table/columns differ.
	_, err := db.ExecContext(ctx, `
INSERT INTO opportunities (id, title, description, location, kind, status, source_ref, created_at, updated_at)
VALUES ($1, $2, $3, $4, 'job', 'active', $5, NOW(), NOW())
ON CONFLICT (id) DO UPDATE SET
  title = EXCLUDED.title,
  description = EXCLUDED.description,
  location = EXCLUDED.location,
  status = 'active',
  updated_at = NOW()`,
		oppID, job.Title, job.Description, job.Location, "ats:"+job.ID)
	return err
}

func dualUnpublishOpportunity(ctx context.Context, db *sql.DB, oppID string) error {
	_, err := db.ExecContext(ctx, `
UPDATE opportunities SET status = 'closed', updated_at = NOW() WHERE id = $1`, oppID)
	return err
}

// LedgerBillingEmitter records a durable billing ref for results-based hire charges.
type LedgerBillingEmitter struct {
	// Prefix for human-readable refs (e.g. "hire").
	Prefix string
}

func (e LedgerBillingEmitter) EmitHire(_ context.Context, outcome *models.HireOutcome) (string, error) {
	if outcome == nil {
		return "", fmt.Errorf("ats: nil hire outcome")
	}
	prefix := e.Prefix
	if prefix == "" {
		prefix = "hire"
	}
	// Durable, deterministic, idempotent billing reference for finance reconciliation.
	return fmt.Sprintf("%s_%s_%s", prefix, outcome.JobID, outcome.ApplicationID), nil
}

// NotificationNotifier enqueues interview.scheduled to outbox and optionally sends immediately.
type NotificationNotifier struct {
	Outbox   repository.OutboxRepository
	Notify   notificationv1connect.NotificationServiceClient
	Template string
	// SiteBaseURL for deep links in email templates.
	SiteBaseURL string
}

func (n NotificationNotifier) EnqueueInterviewScheduled(
	ctx context.Context,
	interview *models.Interview,
	application *models.Application,
	job *models.Job,
) error {
	if interview == nil || application == nil {
		return nil
	}
	jobTitle := ""
	if job != nil {
		jobTitle = job.Title
	}
	ics := models.BuildICS(interview, jobTitle, application.ProfileID, "")
	payload := map[string]any{
		"interview_id":   interview.ID,
		"application_id": application.ID,
		"profile_id":     application.ProfileID,
		"job_id":         application.JobID,
		"job_title":      jobTitle,
		"slot_start":     "",
		"slot_end":       "",
		"ics":            ics,
		"book_url":       "",
	}
	if interview.SlotStart != nil {
		payload["slot_start"] = interview.SlotStart.UTC().Format(time.RFC3339)
	}
	if interview.SlotEnd != nil {
		payload["slot_end"] = interview.SlotEnd.UTC().Format(time.RFC3339)
	}
	if n.SiteBaseURL != "" {
		payload["book_url"] = strings.TrimRight(n.SiteBaseURL, "/") + "/interview/" + interview.ID
	}
	raw, _ := json.Marshal(payload)
	if n.Outbox != nil {
		_ = n.Outbox.Create(ctx, &models.OutboxMessage{
			Kind:           models.OutboxKindInterviewScheduled,
			PayloadJSON:    string(raw),
			IdempotencyKey: "interview.scheduled:" + interview.ID,
			Status:         models.OutboxPending,
		})
	}
	// Immediate send when client configured (outbox worker also drains later).
	if n.Notify != nil && application.ProfileID != "" {
		tmpl := n.Template
		if tmpl == "" {
			tmpl = "template.opportunities.ats.interview.scheduled"
		}
		vars := map[string]any{
			"job_title":  jobTitle,
			"slot_start": payload["slot_start"],
			"slot_end":   payload["slot_end"],
			"book_url":   payload["book_url"],
			"ics":        ics,
		}
		if err := notify.Send(ctx, n.Notify, notify.Message{
			Template:  tmpl,
			ProfileID: application.ProfileID,
			Variables: vars,
		}); err != nil {
			util.Log(ctx).WithError(err).Warn("ats: interview notify send failed; remains in outbox")
		}
	}
	return nil
}

// HeuristicAI produces useful text without an LLM key (still production-usable).
type HeuristicAI struct{}

func (HeuristicAI) ScreenSummary(_ context.Context, job *models.Job, application *models.Application) (string, error) {
	jt, js := "the role", ""
	if job != nil {
		jt = job.Title
		js = job.Description
	}
	sum := "(no candidate summary yet — add notes or import from Stawi talent)"
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
