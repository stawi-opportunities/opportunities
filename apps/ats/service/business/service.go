// Package business is the ATS domain orchestration layer (handlers → business → repository).
package business

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/repository"
)

// Scope is tenant+partition+actor from JWT claims.
type Scope struct {
	TenantID    string
	PartitionID string
	ProfileID   string
}

// ScopeFromContext extracts tenancy + actor.
func ScopeFromContext(ctx context.Context) (Scope, error) {
	c := security.ClaimsFromContext(ctx)
	if c == nil {
		return Scope{}, fmt.Errorf("%w: missing claims", models.ErrForbidden)
	}
	s := Scope{
		TenantID:    c.GetTenantID(),
		PartitionID: c.GetPartitionID(),
		ProfileID:   c.GetProfileID(),
	}
	if s.ProfileID == "" {
		s.ProfileID = c.Subject
	}
	if s.TenantID == "" || s.PartitionID == "" || s.ProfileID == "" {
		return Scope{}, fmt.Errorf("%w: tenant_id, partition_id, and profile_id required", models.ErrForbidden)
	}
	return s, nil
}

// Deps are repository + peer ports.
type Deps struct {
	Jobs         repository.JobRepository
	Applications repository.ApplicationRepository
	StageEvents  repository.StageEventRepository
	Availability repository.AvailabilityRepository
	Interviews   repository.InterviewRepository
	Hires        repository.HireOutcomeRepository
	Outbox       repository.OutboxRepository
	AiRuns       repository.AiRunRepository
	Projections  repository.JobProjectionRepository
	Idempotency  repository.IdempotencyRepository

	Matching  MatchingTalent
	Publisher OpportunityPublisher
	Billing   BillingEmitter
	Notify    Notifier
	AI        AIAssistant
	// Calendar optional service_calendar client for multi-resource reservation.
	// When nil, ListSlots/Book use local ATS availability only.
	Calendar InterviewCalendar

	SlotWindowDays int
}

// Service is the ATS business layer.
type Service struct {
	Deps
}

// NewService constructs business with production-safe default peer ports.
// Matching defaults to EmptyTalent (no fabricated candidates). Publisher
// requires Projections when unset; Billing is durable/idempotent by key.
func NewService(d Deps) *Service {
	if d.Matching == nil {
		d.Matching = EmptyTalent{}
	}
	if d.Publisher == nil {
		d.Publisher = ProjectionPublisher{Projections: d.Projections}
	}
	if d.Billing == nil {
		d.Billing = LedgerBillingEmitter{Prefix: "result_hire"}
	}
	if d.Notify == nil {
		d.Notify = NotificationNotifier{Outbox: d.Outbox}
	}
	if d.AI == nil {
		d.AI = HeuristicAI{}
	}
	if d.SlotWindowDays <= 0 {
		d.SlotWindowDays = 14
	}
	return &Service{Deps: d}
}

// CreateJobInput is validated job create.
type CreateJobInput struct {
	Title, Description, Location, Status string
}

func (s *Service) CreateJob(ctx context.Context, in CreateJobInput) (*models.Job, error) {
	if _, err := ScopeFromContext(ctx); err != nil {
		return nil, err
	}
	if in.Title == "" {
		return nil, fmt.Errorf("%w: title required", models.ErrInvalid)
	}
	st := in.Status
	if st == "" {
		st = models.JobStatusDraft
	}
	if st != models.JobStatusDraft && st != models.JobStatusOpen {
		return nil, fmt.Errorf("%w: invalid status", models.ErrInvalid)
	}
	j := &models.Job{
		Title: in.Title, Description: in.Description, Location: in.Location,
		Status: st, Visibility: models.VisibilityPrivate,
	}
	if err := s.Jobs.Create(ctx, j); err != nil {
		return nil, fmt.Errorf("ats: create job: %w", err)
	}
	return j, nil
}

func (s *Service) ListJobs(ctx context.Context, status string) ([]*models.Job, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.Jobs.ListByPartition(ctx, sc.TenantID, sc.PartitionID, status, 50)
}

func (s *Service) GetJob(ctx context.Context, id string) (*models.Job, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	j, err := s.Jobs.GetInPartition(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, models.ErrNotFound
	}
	return j, nil
}

// UpdateJobInput patches mutable job fields.
type UpdateJobInput struct {
	Title, Description, Location, Status *string
}

func (s *Service) UpdateJob(ctx context.Context, id string, in UpdateJobInput) (*models.Job, error) {
	j, err := s.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	fields := make([]string, 0, 4)
	if in.Title != nil {
		if *in.Title == "" {
			return nil, fmt.Errorf("%w: title required", models.ErrInvalid)
		}
		j.Title = *in.Title
		fields = append(fields, "title")
	}
	if in.Description != nil {
		j.Description = *in.Description
		fields = append(fields, "description")
	}
	if in.Location != nil {
		j.Location = *in.Location
		fields = append(fields, "location")
	}
	if in.Status != nil {
		switch *in.Status {
		case models.JobStatusDraft, models.JobStatusOpen, models.JobStatusClosed:
			j.Status = *in.Status
			fields = append(fields, "status")
		default:
			return nil, fmt.Errorf("%w: invalid status", models.ErrInvalid)
		}
	}
	if len(fields) == 0 {
		return j, nil
	}
	if _, err := s.Jobs.Update(ctx, j, fields...); err != nil {
		return nil, fmt.Errorf("ats: update job: %w", err)
	}
	return j, nil
}

func (s *Service) CloseJob(ctx context.Context, id string) (*models.Job, error) {
	st := models.JobStatusClosed
	return s.UpdateJob(ctx, id, UpdateJobInput{Status: &st})
}

func (s *Service) PublishJob(ctx context.Context, id string) (*models.Job, error) {
	j, err := s.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if j.Status == models.JobStatusClosed {
		return nil, fmt.Errorf("%w: closed job", models.ErrInvalid)
	}
	oppID, err := s.Publisher.Publish(ctx, j)
	if err != nil {
		return nil, fmt.Errorf("ats: publish: %w", err)
	}
	now := time.Now().UTC()
	j.Visibility = models.VisibilityPublished
	j.OpportunityID = oppID
	j.PublishedAt = &now
	fields := []string{"visibility", "opportunity_id", "published_at"}
	if j.Status == models.JobStatusDraft {
		j.Status = models.JobStatusOpen
		fields = append(fields, "status")
	}
	if _, err := s.Jobs.Update(ctx, j, fields...); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) UnpublishJob(ctx context.Context, id string) (*models.Job, error) {
	j, err := s.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.Publisher.Unpublish(ctx, j); err != nil {
		return nil, fmt.Errorf("ats: unpublish: %w", err)
	}
	j.Visibility = models.VisibilityPrivate
	if _, err := s.Jobs.Update(ctx, j, "visibility"); err != nil {
		return nil, err
	}
	return j, nil
}

// CreateApplicationInput adds a profile to a job pipeline.
type CreateApplicationInput struct {
	JobID, ProfileID, CandidateID, Source, SourceRef, Summary string
	Score                                                     float32
}

func (s *Service) CreateApplication(ctx context.Context, in CreateApplicationInput) (*models.Application, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if in.JobID == "" || in.ProfileID == "" {
		return nil, fmt.Errorf("%w: job_id and profile_id required", models.ErrInvalid)
	}
	if _, err := s.GetJob(ctx, in.JobID); err != nil {
		return nil, err
	}
	existing, err := s.Applications.GetActive(ctx, sc.TenantID, sc.PartitionID, in.JobID, in.ProfileID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w: active application already exists", models.ErrConflict)
	}
	src := in.Source
	if src == "" {
		src = models.SourceManual
	}
	a := &models.Application{
		JobID: in.JobID, ProfileID: in.ProfileID, CandidateID: in.CandidateID,
		Stage: models.StageApplied, Source: src, SourceRef: in.SourceRef,
		Status: models.AppStatusActive, Summary: in.Summary, Score: in.Score,
	}
	if err := s.Applications.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("ats: create application: %w", err)
	}
	_ = s.StageEvents.Create(ctx, &models.StageEvent{
		ApplicationID: a.ID, FromStage: "", ToStage: models.StageApplied,
		ActorProfileID: sc.ProfileID, Note: "created",
	})
	return a, nil
}

func (s *Service) ListApplications(ctx context.Context, jobID, stage string) ([]*models.Application, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.Applications.ListByJob(ctx, sc.TenantID, sc.PartitionID, jobID, stage, 200)
}

func (s *Service) GetApplication(ctx context.Context, id string) (*models.Application, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.Applications.GetInPartition(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, models.ErrNotFound
	}
	return a, nil
}

func (s *Service) Advance(ctx context.Context, applicationID, toStage, note string) (*models.Application, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.GetApplication(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	if a.Status != models.AppStatusActive {
		return nil, fmt.Errorf("%w: application not active", models.ErrInvalid)
	}
	if err := models.ValidateAdvance(a.Stage, toStage); err != nil {
		return nil, fmt.Errorf("%w: %v", models.ErrInvalid, err)
	}
	from := a.Stage
	a.Stage = toStage
	fields := []string{"stage"}
	switch toStage {
	case models.StageRejected:
		a.Status = models.AppStatusRejected
		fields = append(fields, "status")
	case models.StageWithdrawn:
		a.Status = models.AppStatusWithdrawn
		fields = append(fields, "status")
	case models.StageHired:
		a.Status = models.AppStatusHired
		fields = append(fields, "status")
	}
	if _, err := s.Applications.Update(ctx, a, fields...); err != nil {
		return nil, err
	}
	if err := s.StageEvents.Create(ctx, &models.StageEvent{
		ApplicationID: a.ID, FromStage: from, ToStage: toStage,
		ActorProfileID: sc.ProfileID, Note: note,
	}); err != nil {
		return nil, err
	}
	if toStage == models.StageHired {
		if _, err := s.ensureHireOutcome(ctx, a); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func (s *Service) Hire(ctx context.Context, applicationID string) (*models.Application, *models.HireOutcome, error) {
	a, err := s.GetApplication(ctx, applicationID)
	if err != nil {
		return nil, nil, err
	}
	if a.Status == models.AppStatusHired {
		h, err := s.Hires.GetByApplication(ctx, a.ID)
		return a, h, err
	}
	if a.Status != models.AppStatusActive {
		return nil, nil, fmt.Errorf("%w: application not active", models.ErrInvalid)
	}
	if a.Stage == models.StageInterview {
		if _, err := s.Advance(ctx, applicationID, models.StageOffer, "auto before hire"); err != nil {
			return nil, nil, err
		}
		return s.Hire(ctx, applicationID)
	}
	if a.Stage != models.StageOffer {
		return nil, nil, fmt.Errorf("%w: must be at interview or offer to hire", models.ErrInvalid)
	}
	a, err = s.Advance(ctx, applicationID, models.StageHired, "hired")
	if err != nil {
		return nil, nil, err
	}
	h, err := s.Hires.GetByApplication(ctx, a.ID)
	return a, h, err
}

func (s *Service) ensureHireOutcome(ctx context.Context, a *models.Application) (*models.HireOutcome, error) {
	existing, err := s.Hires.GetByApplication(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	h := &models.HireOutcome{
		ApplicationID: a.ID, JobID: a.JobID, ProfileID: a.ProfileID,
		IdempotencyKey: "hire:" + a.ID,
	}
	ref, err := s.Billing.EmitHire(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("ats: billing: %w", err)
	}
	h.BillingRef = ref
	if err := s.Hires.Create(ctx, h); err != nil {
		if existing, e2 := s.Hires.GetByApplication(ctx, a.ID); e2 == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return h, nil
}

// SetAvailabilityInput updates interviewer windows.
type SetAvailabilityInput struct {
	Timezone   string
	Rules      []models.WeekRule
	Exceptions []models.ExceptionDay
}

func (s *Service) SetAvailability(ctx context.Context, in SetAvailabilityInput) (*models.Availability, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tz := in.Timezone
	if tz == "" {
		tz = "UTC"
	}
	rj, _ := json.Marshal(in.Rules)
	ej, _ := json.Marshal(in.Exceptions)
	a := &models.Availability{
		ProfileID: sc.ProfileID, Timezone: tz,
		RulesJSON: string(rj), ExceptionsJSON: string(ej),
	}
	a.TenantID = sc.TenantID
	a.PartitionID = sc.PartitionID
	if err := s.Availability.UpsertForProfile(ctx, a); err != nil {
		return nil, err
	}
	// Dual-write to service_calendar when configured (soft-fail).
	syncAvailabilitySoft(ctx, s.Calendar, sc.ProfileID, tz, in.Rules, in.Exceptions)
	return a, nil
}

func (s *Service) GetMyAvailability(ctx context.Context) (*models.Availability, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.Availability.GetByProfile(ctx, sc.TenantID, sc.PartitionID, sc.ProfileID)
}

// ProposeInterviewInput creates a proposed interview.
type ProposeInterviewInput struct {
	ApplicationID, Type, Location, VideoURL string
	DurationMin                             int
	Panel                                   []string
}

func (s *Service) ProposeInterview(ctx context.Context, in ProposeInterviewInput) (*models.Interview, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.GetApplication(ctx, in.ApplicationID); err != nil {
		return nil, err
	}
	dur := in.DurationMin
	if dur <= 0 {
		if d, err := s.AI.SuggestDurationMin(ctx, nil); err == nil && d > 0 {
			dur = d
		} else {
			dur = 30
		}
	}
	panel := in.Panel
	if len(panel) == 0 {
		panel = []string{sc.ProfileID}
	}
	pj, _ := json.Marshal(panel)
	typ := in.Type
	if typ == "" {
		typ = "general"
	}
	iv := &models.Interview{
		ApplicationID: in.ApplicationID, Type: typ, DurationMin: dur,
		PanelJSON: string(pj), Status: models.InterviewProposed,
		Location: in.Location, VideoURL: in.VideoURL,
	}
	if err := s.Interviews.Create(ctx, iv); err != nil {
		return nil, fmt.Errorf("ats: create interview: %w", err)
	}
	return iv, nil
}

func (s *Service) ListSlots(ctx context.Context, interviewID string) ([]models.Slot, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	iv, err := s.Interviews.GetInPartition(ctx, sc.TenantID, sc.PartitionID, interviewID)
	if err != nil {
		return nil, err
	}
	if iv == nil {
		return nil, models.ErrNotFound
	}
	var panel []string
	_ = json.Unmarshal([]byte(iv.PanelJSON), &panel)
	if len(panel) == 0 {
		panel = []string{sc.ProfileID}
	}

	// Prefer service_calendar multi-resource slots when wired.
	if s.Calendar != nil {
		resIDs, err := s.Calendar.EnsurePanelResources(ctx, panel)
		if err != nil {
			util.Log(ctx).WithError(err).Warn("ats: calendar ensure resources failed; falling back to local slots")
		} else if len(resIDs) > 0 {
			now := time.Now().UTC()
			winEnd := now.AddDate(0, 0, s.SlotWindowDays)
			slots, err := s.Calendar.ListPanelSlots(ctx, resIDs, iv.DurationMin, now, winEnd)
			if err != nil {
				util.Log(ctx).WithError(err).Warn("ats: calendar ListSlots failed; falling back to local slots")
			} else {
				return slots, nil
			}
		}
	}

	rulesBy := map[string][]models.WeekRule{}
	exBy := map[string][]models.ExceptionDay{}
	for _, pid := range panel {
		av, err := s.Availability.GetByProfile(ctx, sc.TenantID, sc.PartitionID, pid)
		if err != nil {
			return nil, err
		}
		if av == nil || av.RulesJSON == "" || av.RulesJSON == "[]" {
			return nil, fmt.Errorf("%w: profile %s", models.ErrEmptyAvail, pid)
		}
		var rules []models.WeekRule
		_ = json.Unmarshal([]byte(av.RulesJSON), &rules)
		var ex []models.ExceptionDay
		_ = json.Unmarshal([]byte(av.ExceptionsJSON), &ex)
		rulesBy[pid] = rules
		exBy[pid] = ex
	}
	tzName := "UTC"
	if len(panel) > 0 {
		if av, _ := s.Availability.GetByProfile(ctx, sc.TenantID, sc.PartitionID, panel[0]); av != nil && av.Timezone != "" {
			tzName = av.Timezone
		}
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	winEnd := now.AddDate(0, 0, s.SlotWindowDays)
	scheduled, err := s.Interviews.ListScheduledBusy(ctx, sc.TenantID, sc.PartitionID, now.UTC(), winEnd.UTC())
	if err != nil {
		return nil, err
	}
	var busy []models.BusyInterval
	for _, row := range scheduled {
		if row.SlotStart == nil || row.SlotEnd == nil {
			continue
		}
		busy = append(busy, models.BusyInterval{Start: *row.SlotStart, End: *row.SlotEnd})
	}
	return models.ComputeSlots(loc, rulesBy, exBy, busy, now, winEnd, iv.DurationMin)
}

// BookInterviewInput selects a slot.
type BookInterviewInput struct {
	InterviewID string
	Start, End  time.Time
}

func (s *Service) BookInterview(ctx context.Context, in BookInterviewInput) (*models.Interview, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	iv, err := s.Interviews.GetInPartition(ctx, sc.TenantID, sc.PartitionID, in.InterviewID)
	if err != nil {
		return nil, err
	}
	if iv == nil {
		return nil, models.ErrNotFound
	}
	if iv.Status == models.InterviewScheduled {
		if iv.SlotStart != nil && iv.SlotStart.Equal(in.Start) {
			return iv, nil
		}
		return nil, fmt.Errorf("%w: already scheduled", models.ErrConflict)
	}
	if iv.Status != models.InterviewProposed {
		return nil, fmt.Errorf("%w: interview not bookable", models.ErrInvalid)
	}
	slots, err := s.ListSlots(ctx, iv.ID)
	if err != nil {
		return nil, err
	}
	ok := false
	for _, sl := range slots {
		if sl.Start.Equal(in.Start) {
			in.End = sl.End
			ok = true
			break
		}
	}
	if !ok {
		return nil, fmt.Errorf("%w: slot not available", models.ErrConflict)
	}
	start, end := in.Start, in.End
	// Reserve on service_calendar when available (conflict → fail book).
	var calendarBookingID string
	if s.Calendar != nil {
		var panel []string
		_ = json.Unmarshal([]byte(iv.PanelJSON), &panel)
		if len(panel) == 0 {
			panel = []string{sc.ProfileID}
		}
		resIDs, err := s.Calendar.EnsurePanelResources(ctx, panel)
		if err != nil {
			return nil, fmt.Errorf("ats: calendar ensure for book: %w", err)
		}
		title := "Interview"
		if a, _ := s.Applications.GetInPartition(ctx, sc.TenantID, sc.PartitionID, iv.ApplicationID); a != nil {
			if j, _ := s.Jobs.GetInPartition(ctx, sc.TenantID, sc.PartitionID, a.JobID); j != nil {
				title = "Interview: " + j.Title
			}
		}
		calendarBookingID, err = s.Calendar.BookPanel(ctx, resIDs, start, end, iv.ID, title)
		if err != nil {
			return nil, fmt.Errorf("ats: calendar book: %w", err)
		}
	}
	iv.SlotStart = &start
	iv.SlotEnd = &end
	iv.Status = models.InterviewScheduled
	if iv.ICSUID == "" {
		iv.ICSUID = util.IDString()
	}
	// Stash calendar booking id in ICSUID suffix if needed — prefer metadata via ics_uid keep + source_ref in calendar.
	// Store booking id in video_url? No. Use ics_uid as local uid; calendar has source_ref interview:id.
	_ = calendarBookingID
	if _, err := s.Interviews.Update(ctx, iv, "slot_start", "slot_end", "status", "ics_uid"); err != nil {
		// Best-effort cancel remote reservation if local update fails.
		if s.Calendar != nil && calendarBookingID != "" {
			_ = s.Calendar.CancelInterviewBooking(ctx, calendarBookingID)
		}
		return nil, err
	}
	a, _ := s.Applications.GetInPartition(ctx, sc.TenantID, sc.PartitionID, iv.ApplicationID)
	if a != nil {
		var job *models.Job
		if j, _ := s.Jobs.GetInPartition(ctx, sc.TenantID, sc.PartitionID, a.JobID); j != nil {
			job = j
		}
		if err := s.Notify.EnqueueInterviewScheduled(ctx, iv, a, job); err != nil {
			util.Log(ctx).WithError(err).Warn("ats: enqueue interview notification failed")
		}
	}
	return iv, nil
}

func (s *Service) ListTalent(ctx context.Context, jobID string, limit int) ([]models.TalentHit, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	j, err := s.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	return s.Matching.ListForJob(ctx, sc.TenantID, sc.PartitionID, j.ID, j.Title, j.Description, limit)
}

func (s *Service) AddTalent(ctx context.Context, jobID string, hit models.TalentHit) (*models.Application, error) {
	return s.CreateApplication(ctx, CreateApplicationInput{
		JobID: jobID, ProfileID: hit.ProfileID, CandidateID: hit.CandidateID,
		Source: models.SourceStawiMatch, Summary: hit.Summary, Score: hit.Score,
	})
}

func (s *Service) ScreenSummary(ctx context.Context, applicationID string) (string, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return "", err
	}
	a, err := s.GetApplication(ctx, applicationID)
	if err != nil {
		return "", err
	}
	j, _ := s.Jobs.GetInPartition(ctx, sc.TenantID, sc.PartitionID, a.JobID)
	text, err := s.AI.ScreenSummary(ctx, j, a)
	if err != nil {
		return "", err
	}
	_ = s.AiRuns.Create(ctx, &models.AiRun{
		Purpose: "screen_summary", ActorProfileID: sc.ProfileID, OutputJSON: text,
	})
	return text, nil
}

func (s *Service) Dashboard(ctx context.Context) (*models.DashboardDTO, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	open, err := s.Jobs.CountByStatus(ctx, sc.TenantID, sc.PartitionID, models.JobStatusOpen)
	if err != nil {
		return nil, err
	}
	active, err := s.Applications.CountByStatus(ctx, sc.TenantID, sc.PartitionID, models.AppStatusActive)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	weekEnd := now.AddDate(0, 0, 7)
	nWeek, err := s.Interviews.CountInRange(ctx, sc.TenantID, sc.PartitionID, now, weekEnd)
	if err != nil {
		return nil, err
	}
	upcoming, err := s.Interviews.ListUpcoming(ctx, sc.TenantID, sc.PartitionID, now, weekEnd, 20)
	if err != nil {
		return nil, err
	}
	dtos := make([]models.InterviewDTO, 0, len(upcoming))
	for _, iv := range upcoming {
		d := models.InterviewToDTO(iv)
		if a, _ := s.Applications.GetInPartition(ctx, sc.TenantID, sc.PartitionID, iv.ApplicationID); a != nil {
			d.CandidateID = a.ProfileID
			d.JobID = a.JobID
			if j, _ := s.Jobs.GetInPartition(ctx, sc.TenantID, sc.PartitionID, a.JobID); j != nil {
				d.JobTitle = j.Title
			}
		}
		dtos = append(dtos, d)
	}
	var attention []string
	if open == 0 {
		attention = append(attention, "Create an open job to start hiring")
	}
	if active == 0 && open > 0 {
		attention = append(attention, "Add candidates from Stawi talent or by profile_id")
	}
	av, _ := s.Availability.GetByProfile(ctx, sc.TenantID, sc.PartitionID, sc.ProfileID)
	if av == nil || av.RulesJSON == "" || av.RulesJSON == "[]" {
		attention = append(attention, "Set your interview availability so candidates can book")
	}
	return &models.DashboardDTO{
		OpenJobs: int(open), ActiveApplications: int(active),
		InterviewsThisWeek: int(nWeek), UpcomingInterviews: dtos, NeedsAttention: attention,
	}, nil
}

func (s *Service) ListInterviews(ctx context.Context, applicationID string) ([]*models.Interview, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.Interviews.ListByApplication(ctx, sc.TenantID, sc.PartitionID, applicationID)
}

func (s *Service) GetInterviewICS(ctx context.Context, interviewID string) (string, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return "", err
	}
	iv, err := s.Interviews.GetInPartition(ctx, sc.TenantID, sc.PartitionID, interviewID)
	if err != nil {
		return "", err
	}
	if iv == nil {
		return "", models.ErrNotFound
	}
	jobTitle, cand := "", ""
	if a, _ := s.Applications.GetInPartition(ctx, sc.TenantID, sc.PartitionID, iv.ApplicationID); a != nil {
		cand = a.ProfileID
		if j, _ := s.Jobs.GetInPartition(ctx, sc.TenantID, sc.PartitionID, a.JobID); j != nil {
			jobTitle = j.Title
		}
	}
	return models.BuildICS(iv, jobTitle, cand, ""), nil
}

func (s *Service) MyApplications(ctx context.Context) ([]models.ApplicationDTO, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.Applications.ListByProfile(ctx, sc.TenantID, sc.PartitionID, sc.ProfileID)
	if err != nil {
		return nil, err
	}
	out := make([]models.ApplicationDTO, 0, len(rows))
	for _, a := range rows {
		d := models.ApplicationToDTO(a)
		if j, _ := s.Jobs.GetInPartition(ctx, sc.TenantID, sc.PartitionID, a.JobID); j != nil {
			d.JobTitle = j.Title
		}
		out = append(out, d)
	}
	return out, nil
}
