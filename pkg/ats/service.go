package ats

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/util"
)

// Scope is tenant+partition+actor from claims.
type Scope struct {
	TenantID    string
	PartitionID string
	ProfileID   string
}

// ScopeFromContext extracts tenancy + actor from JWT claims.
func ScopeFromContext(ctx context.Context) (Scope, error) {
	c := security.ClaimsFromContext(ctx)
	if c == nil {
		return Scope{}, fmt.Errorf("%w: missing claims", ErrForbidden)
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
		return Scope{}, fmt.Errorf("%w: tenant_id, partition_id, and profile_id required", ErrForbidden)
	}
	return s, nil
}

// Service is the ATS business layer.
type Service struct {
	Store     *Store
	Matching  MatchingTalent
	Publisher OpportunityPublisher
	Billing   BillingEmitter
	Notify    Notifier
	AI        AIAssistant
	// SlotWindowDays controls how far ahead slots are offered.
	SlotWindowDays int
}

func NewService(store *Store) *Service {
	return &Service{
		Store:          store,
		Matching:       NewDemoTalent(),
		Publisher:      LocalPublisher{},
		Billing:        RecordingBilling{},
		Notify:         OutboxNotifier{Store: store},
		AI:             HeuristicAI{},
		SlotWindowDays: 14,
	}
}

// UpdateJobInput patches mutable job fields.
type UpdateJobInput struct {
	Title       *string
	Description *string
	Location    *string
	Status      *string
}

func (s *Service) UpdateJob(ctx context.Context, id string, in UpdateJobInput) (*Job, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	j, err := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrNotFound
	}
	if in.Title != nil {
		if *in.Title == "" {
			return nil, fmt.Errorf("%w: title required", ErrInvalid)
		}
		j.Title = *in.Title
	}
	if in.Description != nil {
		j.Description = *in.Description
	}
	if in.Location != nil {
		j.Location = *in.Location
	}
	if in.Status != nil {
		switch *in.Status {
		case JobStatusDraft, JobStatusOpen, JobStatusClosed:
			j.Status = *in.Status
		default:
			return nil, fmt.Errorf("%w: invalid status", ErrInvalid)
		}
	}
	if err := s.Store.Save(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) CloseJob(ctx context.Context, id string) (*Job, error) {
	st := JobStatusClosed
	return s.UpdateJob(ctx, id, UpdateJobInput{Status: &st})
}

func (s *Service) Dashboard(ctx context.Context) (*DashboardDTO, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	open, err := s.Store.CountJobs(ctx, sc.TenantID, sc.PartitionID, JobStatusOpen)
	if err != nil {
		return nil, err
	}
	active, err := s.Store.CountApplications(ctx, sc.TenantID, sc.PartitionID, AppStatusActive)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	weekEnd := now.AddDate(0, 0, 7)
	nWeek, err := s.Store.CountInterviewsInRange(ctx, sc.TenantID, sc.PartitionID, now, weekEnd)
	if err != nil {
		return nil, err
	}
	upcoming, err := s.Store.ListUpcomingInterviews(ctx, sc.TenantID, sc.PartitionID, now, weekEnd, 20)
	if err != nil {
		return nil, err
	}
	dtos := make([]InterviewDTO, 0, len(upcoming))
	for i := range upcoming {
		d := InterviewToDTO(&upcoming[i])
		if a, _ := s.Store.GetApplication(ctx, sc.TenantID, sc.PartitionID, upcoming[i].ApplicationID); a != nil {
			d.CandidateID = a.ProfileID
			d.JobID = a.JobID
			if j, _ := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, a.JobID); j != nil {
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
	av, _ := s.Store.GetAvailability(ctx, sc.TenantID, sc.PartitionID, sc.ProfileID)
	if av == nil || av.RulesJSON == "" || av.RulesJSON == "[]" {
		attention = append(attention, "Set your interview availability so candidates can book")
	}
	return &DashboardDTO{
		OpenJobs:           int(open),
		ActiveApplications: int(active),
		InterviewsThisWeek: int(nWeek),
		UpcomingInterviews: dtos,
		NeedsAttention:     attention,
	}, nil
}

func (s *Service) ListInterviews(ctx context.Context, applicationID string) ([]Interview, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.ListInterviewsByApplication(ctx, sc.TenantID, sc.PartitionID, applicationID)
}

// GetInterviewICS returns ICS text for a scheduled interview (recruiter or candidate).
func (s *Service) GetInterviewICS(ctx context.Context, interviewID string) (string, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return "", err
	}
	iv, err := s.Store.GetInterview(ctx, sc.TenantID, sc.PartitionID, interviewID)
	if err != nil {
		return "", err
	}
	if iv == nil {
		return "", ErrNotFound
	}
	jobTitle, cand := "", ""
	if a, _ := s.Store.GetApplication(ctx, sc.TenantID, sc.PartitionID, iv.ApplicationID); a != nil {
		cand = a.ProfileID
		if j, _ := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, a.JobID); j != nil {
			jobTitle = j.Title
		}
	}
	return BuildICS(iv, jobTitle, cand, ""), nil
}

// MyApplications lists applications for the acting profile (candidate view).
func (s *Service) MyApplications(ctx context.Context) ([]ApplicationDTO, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.ListApplicationsForProfile(ctx, sc.TenantID, sc.PartitionID, sc.ProfileID)
	if err != nil {
		return nil, err
	}
	out := make([]ApplicationDTO, 0, len(rows))
	for i := range rows {
		d := ApplicationToDTO(&rows[i])
		if j, _ := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, rows[i].JobID); j != nil {
			d.JobTitle = j.Title
		}
		out = append(out, d)
	}
	return out, nil
}

// CreateJobInput is validated job create.
type CreateJobInput struct {
	Title       string
	Description string
	Location    string
	Status      string // draft|open
}

func (s *Service) CreateJob(ctx context.Context, in CreateJobInput) (*Job, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if in.Title == "" {
		return nil, fmt.Errorf("%w: title required", ErrInvalid)
	}
	st := in.Status
	if st == "" {
		st = JobStatusDraft
	}
	if st != JobStatusDraft && st != JobStatusOpen {
		return nil, fmt.Errorf("%w: invalid status", ErrInvalid)
	}
	j := &Job{
		Title:       in.Title,
		Description: in.Description,
		Location:    in.Location,
		Status:      st,
		Visibility:  VisibilityPrivate,
	}
	if err := s.Store.CreateJob(ctx, j); err != nil {
		return nil, err
	}
	// Ensure tenancy fields present even if GenID claims were thin.
	if j.TenantID == "" {
		j.TenantID = sc.TenantID
		j.PartitionID = sc.PartitionID
		_ = s.Store.Save(ctx, j)
	}
	return j, nil
}

func (s *Service) ListJobs(ctx context.Context, status string) ([]Job, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.ListJobs(ctx, sc.TenantID, sc.PartitionID, status, 50)
}

func (s *Service) GetJob(ctx context.Context, id string) (*Job, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	j, err := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrNotFound
	}
	return j, nil
}

func (s *Service) PublishJob(ctx context.Context, id string) (*Job, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	j, err := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrNotFound
	}
	if j.Status == JobStatusClosed {
		return nil, fmt.Errorf("%w: closed job", ErrInvalid)
	}
	oppID, err := s.Publisher.Publish(ctx, j)
	if err != nil {
		return nil, fmt.Errorf("ats: publish: %w", err)
	}
	now := time.Now().UTC()
	j.Visibility = VisibilityPublished
	j.OpportunityID = oppID
	j.PublishedAt = &now
	if j.Status == JobStatusDraft {
		j.Status = JobStatusOpen
	}
	if err := s.Store.Save(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) UnpublishJob(ctx context.Context, id string) (*Job, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	j, err := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrNotFound
	}
	if err := s.Publisher.Unpublish(ctx, j); err != nil {
		return nil, fmt.Errorf("ats: unpublish: %w", err)
	}
	j.Visibility = VisibilityPrivate
	// Keep opportunity_id for audit; listing hidden by publisher.
	if err := s.Store.Save(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// CreateApplicationInput adds a profile to a job pipeline.
type CreateApplicationInput struct {
	JobID       string
	ProfileID   string
	CandidateID string
	Source      string
	SourceRef   string
	Summary     string
	Score       float32
}

func (s *Service) CreateApplication(ctx context.Context, in CreateApplicationInput) (*Application, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if in.JobID == "" || in.ProfileID == "" {
		return nil, fmt.Errorf("%w: job_id and profile_id required", ErrInvalid)
	}
	j, err := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, in.JobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrNotFound
	}
	existing, err := s.Store.GetActiveApplication(ctx, sc.TenantID, sc.PartitionID, in.JobID, in.ProfileID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w: active application already exists", ErrConflict)
	}
	src := in.Source
	if src == "" {
		src = SourceManual
	}
	a := &Application{
		JobID:       in.JobID,
		ProfileID:   in.ProfileID,
		CandidateID: in.CandidateID,
		Stage:       StageApplied,
		Source:      src,
		SourceRef:   in.SourceRef,
		Status:      AppStatusActive,
		Summary:     in.Summary,
		Score:       in.Score,
	}
	if err := s.Store.CreateApplication(ctx, a); err != nil {
		return nil, err
	}
	_ = s.Store.AppendStageEvent(ctx, &StageEvent{
		ApplicationID:  a.ID,
		FromStage:      "",
		ToStage:        StageApplied,
		ActorProfileID: sc.ProfileID,
		Note:           "created",
	})
	return a, nil
}

func (s *Service) ListApplications(ctx context.Context, jobID, stage string) ([]Application, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.ListApplicationsByJob(ctx, sc.TenantID, sc.PartitionID, jobID, stage, 200)
}

func (s *Service) GetApplication(ctx context.Context, id string) (*Application, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.Store.GetApplication(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrNotFound
	}
	return a, nil
}

func (s *Service) Advance(ctx context.Context, applicationID, toStage, note string) (*Application, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.Store.GetApplication(ctx, sc.TenantID, sc.PartitionID, applicationID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrNotFound
	}
	if a.Status != AppStatusActive {
		return nil, fmt.Errorf("%w: application not active", ErrInvalid)
	}
	if err := ValidateAdvance(a.Stage, toStage); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	from := a.Stage
	a.Stage = toStage
	switch toStage {
	case StageRejected:
		a.Status = AppStatusRejected
	case StageWithdrawn:
		a.Status = AppStatusWithdrawn
	case StageHired:
		// Hire path also via Hire(); allow advance to hired then outcome.
		a.Status = AppStatusHired
	}
	if err := s.Store.Save(ctx, a); err != nil {
		return nil, err
	}
	if err := s.Store.AppendStageEvent(ctx, &StageEvent{
		ApplicationID:  a.ID,
		FromStage:      from,
		ToStage:        toStage,
		ActorProfileID: sc.ProfileID,
		Note:           note,
	}); err != nil {
		return nil, err
	}
	if toStage == StageHired {
		if _, err := s.ensureHireOutcome(ctx, sc, a); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// Hire moves to hired and emits results billing (idempotent).
func (s *Service) Hire(ctx context.Context, applicationID string) (*Application, *HireOutcome, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	a, err := s.Store.GetApplication(ctx, sc.TenantID, sc.PartitionID, applicationID)
	if err != nil {
		return nil, nil, err
	}
	if a == nil {
		return nil, nil, ErrNotFound
	}
	if a.Status == AppStatusHired {
		h, err := s.Store.GetHireOutcomeByApp(ctx, a.ID)
		return a, h, err
	}
	if a.Status != AppStatusActive {
		return nil, nil, fmt.Errorf("%w: application not active", ErrInvalid)
	}
	// Allow hire from offer or interview per product flexibility; use ValidateAdvance when from offer.
	if a.Stage != StageOffer && a.Stage != StageInterview {
		if err := ValidateAdvance(a.Stage, StageHired); err != nil {
			// try via offer
			if err2 := ValidateAdvance(a.Stage, StageOffer); err2 != nil {
				return nil, nil, fmt.Errorf("%w: must be at interview or offer to hire", ErrInvalid)
			}
			if _, err := s.Advance(ctx, applicationID, StageOffer, "auto before hire"); err != nil {
				return nil, nil, err
			}
			return s.Hire(ctx, applicationID)
		}
	}
	if a.Stage == StageInterview {
		if _, err := s.Advance(ctx, applicationID, StageOffer, "auto before hire"); err != nil {
			return nil, nil, err
		}
		return s.Hire(ctx, applicationID)
	}
	a, err = s.Advance(ctx, applicationID, StageHired, "hired")
	if err != nil {
		return nil, nil, err
	}
	h, err := s.Store.GetHireOutcomeByApp(ctx, a.ID)
	return a, h, err
}

func (s *Service) ensureHireOutcome(ctx context.Context, sc Scope, a *Application) (*HireOutcome, error) {
	existing, err := s.Store.GetHireOutcomeByApp(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	key := "hire:" + a.ID
	h := &HireOutcome{
		ApplicationID:  a.ID,
		JobID:          a.JobID,
		ProfileID:      a.ProfileID,
		IdempotencyKey: key,
	}
	ref, err := s.Billing.EmitHire(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("ats: billing: %w", err)
	}
	h.BillingRef = ref
	if err := s.Store.CreateHireOutcome(ctx, h); err != nil {
		// concurrent insert — re-read
		if existing, e2 := s.Store.GetHireOutcomeByApp(ctx, a.ID); e2 == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	_ = sc
	return h, nil
}

// SetAvailabilityInput updates interviewer windows.
type SetAvailabilityInput struct {
	Timezone   string
	Rules      []WeekRule
	Exceptions []ExceptionDay
}

func (s *Service) SetAvailability(ctx context.Context, in SetAvailabilityInput) (*Availability, error) {
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
	a := &Availability{
		ProfileID:      sc.ProfileID,
		Timezone:       tz,
		RulesJSON:      string(rj),
		ExceptionsJSON: string(ej),
	}
	a.TenantID = sc.TenantID
	a.PartitionID = sc.PartitionID
	if err := s.Store.UpsertAvailability(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) GetMyAvailability(ctx context.Context) (*Availability, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.GetAvailability(ctx, sc.TenantID, sc.PartitionID, sc.ProfileID)
}

// ProposeInterviewInput creates a proposed interview.
type ProposeInterviewInput struct {
	ApplicationID string
	Type          string
	DurationMin   int
	Panel         []string
	Location      string
	VideoURL      string
}

func (s *Service) ProposeInterview(ctx context.Context, in ProposeInterviewInput) (*Interview, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.Store.GetApplication(ctx, sc.TenantID, sc.PartitionID, in.ApplicationID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrNotFound
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
	iv := &Interview{
		ApplicationID: a.ID,
		Type:          typ,
		DurationMin:   dur,
		PanelJSON:     string(pj),
		Status:        InterviewProposed,
		Location:      in.Location,
		VideoURL:      in.VideoURL,
	}
	if err := s.Store.CreateInterview(ctx, iv); err != nil {
		return nil, err
	}
	return iv, nil
}

func (s *Service) ListSlots(ctx context.Context, interviewID string) ([]Slot, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	iv, err := s.Store.GetInterview(ctx, sc.TenantID, sc.PartitionID, interviewID)
	if err != nil {
		return nil, err
	}
	if iv == nil {
		return nil, ErrNotFound
	}
	var panel []string
	_ = json.Unmarshal([]byte(iv.PanelJSON), &panel)
	rulesBy := map[string][]WeekRule{}
	exBy := map[string][]ExceptionDay{}
	for _, pid := range panel {
		av, err := s.Store.GetAvailability(ctx, sc.TenantID, sc.PartitionID, pid)
		if err != nil {
			return nil, err
		}
		if av == nil || av.RulesJSON == "" || av.RulesJSON == "[]" {
			return nil, fmt.Errorf("%w: profile %s", ErrEmptyAvail, pid)
		}
		var rules []WeekRule
		_ = json.Unmarshal([]byte(av.RulesJSON), &rules)
		var ex []ExceptionDay
		_ = json.Unmarshal([]byte(av.ExceptionsJSON), &ex)
		rulesBy[pid] = rules
		exBy[pid] = ex
		locName := av.Timezone
		if locName == "" {
			locName = "UTC"
		}
		// Use first panelist timezone for window.
		_ = locName
	}
	tzName := "UTC"
	if len(panel) > 0 {
		if av, _ := s.Store.GetAvailability(ctx, sc.TenantID, sc.PartitionID, panel[0]); av != nil && av.Timezone != "" {
			tzName = av.Timezone
		}
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	days := s.SlotWindowDays
	if days <= 0 {
		days = 14
	}
	now := time.Now().In(loc)
	winStart := now
	winEnd := now.AddDate(0, 0, days)
	busy, err := s.Store.ListScheduledBusy(ctx, sc.TenantID, sc.PartitionID, panel, winStart.UTC(), winEnd.UTC())
	if err != nil {
		return nil, err
	}
	return ComputeSlots(loc, rulesBy, exBy, busy, winStart, winEnd, iv.DurationMin)
}

// BookInterviewInput selects a slot.
type BookInterviewInput struct {
	InterviewID string
	Start       time.Time
	End         time.Time
}

func (s *Service) BookInterview(ctx context.Context, in BookInterviewInput) (*Interview, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	iv, err := s.Store.GetInterview(ctx, sc.TenantID, sc.PartitionID, in.InterviewID)
	if err != nil {
		return nil, err
	}
	if iv == nil {
		return nil, ErrNotFound
	}
	if iv.Status == InterviewScheduled {
		if iv.SlotStart != nil && iv.SlotStart.Equal(in.Start) {
			return iv, nil // idempotent
		}
		return nil, fmt.Errorf("%w: already scheduled", ErrConflict)
	}
	if iv.Status != InterviewProposed {
		return nil, fmt.Errorf("%w: interview not bookable", ErrInvalid)
	}
	slots, err := s.ListSlots(ctx, iv.ID)
	if err != nil {
		return nil, err
	}
	ok := false
	for _, sl := range slots {
		if sl.Start.Equal(in.Start) && sl.End.Equal(in.End) {
			ok = true
			break
		}
	}
	if !ok {
		// also accept matching start only
		for _, sl := range slots {
			if sl.Start.Equal(in.Start) {
				in.End = sl.End
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("%w: slot not available", ErrConflict)
	}
	start, end := in.Start, in.End
	iv.SlotStart = &start
	iv.SlotEnd = &end
	iv.Status = InterviewScheduled
	if iv.ICSUID == "" {
		iv.ICSUID = util.IDString()
	}
	if err := s.Store.Save(ctx, iv); err != nil {
		return nil, err
	}
	a, _ := s.Store.GetApplication(ctx, sc.TenantID, sc.PartitionID, iv.ApplicationID)
	if a != nil {
		// Enrich ICS via notifier (writes outbox with ICS body).
		_ = s.Notify.EnqueueInterviewScheduled(ctx, iv, a)
	}
	return iv, nil
}

func (s *Service) ListTalent(ctx context.Context, jobID string, limit int) ([]TalentHit, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	j, err := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, jobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 20
	}
	return s.Matching.ListForJob(ctx, sc.TenantID, sc.PartitionID, j.ID, j.Title, j.Description, limit)
}

func (s *Service) AddTalent(ctx context.Context, jobID string, hit TalentHit) (*Application, error) {
	return s.CreateApplication(ctx, CreateApplicationInput{
		JobID:       jobID,
		ProfileID:   hit.ProfileID,
		CandidateID: hit.CandidateID,
		Source:      SourceStawiMatch,
		Summary:     hit.Summary,
		Score:       hit.Score,
	})
}

func (s *Service) ScreenSummary(ctx context.Context, applicationID string) (string, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return "", err
	}
	a, err := s.Store.GetApplication(ctx, sc.TenantID, sc.PartitionID, applicationID)
	if err != nil {
		return "", err
	}
	if a == nil {
		return "", ErrNotFound
	}
	j, _ := s.Store.GetJob(ctx, sc.TenantID, sc.PartitionID, a.JobID)
	text, err := s.AI.ScreenSummary(ctx, j, a)
	if err != nil {
		return "", err
	}
	_ = s.Store.Create(ctx, &AiRun{
		Purpose:        "screen_summary",
		ActorProfileID: sc.ProfileID,
		OutputJSON:     text,
	})
	return text, nil
}
