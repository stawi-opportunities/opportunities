package models

import (
	"encoding/json"
	"time"
)

// JobDTO is the API-facing job shape.
type JobDTO struct {
	ID            string     `json:"id"`
	TenantID      string     `json:"tenant_id"`
	PartitionID   string     `json:"partition_id"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Location      string     `json:"location"`
	Status        string     `json:"status"`
	Visibility    string     `json:"visibility"`
	OpportunityID string     `json:"opportunity_id,omitempty"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func JobToDTO(j *Job) JobDTO {
	if j == nil {
		return JobDTO{}
	}
	return JobDTO{
		ID: j.ID, TenantID: j.TenantID, PartitionID: j.PartitionID,
		Title: j.Title, Description: j.Description, Location: j.Location,
		Status: j.Status, Visibility: j.Visibility, OpportunityID: j.OpportunityID,
		PublishedAt: j.PublishedAt, CreatedAt: j.CreatedAt, UpdatedAt: j.ModifiedAt,
	}
}

// ApplicationDTO is the API-facing application shape.
type ApplicationDTO struct {
	ID          string    `json:"id"`
	JobID       string    `json:"job_id"`
	ProfileID   string    `json:"profile_id"`
	CandidateID string    `json:"candidate_id,omitempty"`
	Stage       string    `json:"stage"`
	Source      string    `json:"source"`
	SourceRef   string    `json:"source_ref,omitempty"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary,omitempty"`
	Score       float32   `json:"score,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	JobTitle    string    `json:"job_title,omitempty"`
}

func ApplicationToDTO(a *Application) ApplicationDTO {
	if a == nil {
		return ApplicationDTO{}
	}
	return ApplicationDTO{
		ID: a.ID, JobID: a.JobID, ProfileID: a.ProfileID, CandidateID: a.CandidateID,
		Stage: a.Stage, Source: a.Source, SourceRef: a.SourceRef, Status: a.Status,
		Summary: a.Summary, Score: a.Score, CreatedAt: a.CreatedAt, UpdatedAt: a.ModifiedAt,
	}
}

// InterviewDTO is the API-facing interview shape.
type InterviewDTO struct {
	ID            string     `json:"id"`
	ApplicationID string     `json:"application_id"`
	Type          string     `json:"type"`
	DurationMin   int        `json:"duration_min"`
	Panel         []string   `json:"panel"`
	Status        string     `json:"status"`
	SlotStart     *time.Time `json:"slot_start,omitempty"`
	SlotEnd       *time.Time `json:"slot_end,omitempty"`
	Location      string     `json:"location,omitempty"`
	VideoURL      string     `json:"video_url,omitempty"`
	ICSUID        string     `json:"ics_uid,omitempty"`
	JobID         string     `json:"job_id,omitempty"`
	JobTitle      string     `json:"job_title,omitempty"`
	CandidateID   string     `json:"candidate_profile_id,omitempty"`
}

func InterviewToDTO(iv *Interview) InterviewDTO {
	if iv == nil {
		return InterviewDTO{}
	}
	var panel []string
	_ = json.Unmarshal([]byte(iv.PanelJSON), &panel)
	return InterviewDTO{
		ID: iv.ID, ApplicationID: iv.ApplicationID, Type: iv.Type,
		DurationMin: iv.DurationMin, Panel: panel, Status: iv.Status,
		SlotStart: iv.SlotStart, SlotEnd: iv.SlotEnd,
		Location: iv.Location, VideoURL: iv.VideoURL, ICSUID: iv.ICSUID,
	}
}

// AvailabilityDTO for GET/PUT availability.
type AvailabilityDTO struct {
	ProfileID  string         `json:"profile_id"`
	Timezone   string         `json:"timezone"`
	Rules      []WeekRule     `json:"rules"`
	Exceptions []ExceptionDay `json:"exceptions"`
}

func AvailabilityToDTO(a *Availability) AvailabilityDTO {
	if a == nil {
		return AvailabilityDTO{}
	}
	var rules []WeekRule
	var ex []ExceptionDay
	_ = json.Unmarshal([]byte(a.RulesJSON), &rules)
	_ = json.Unmarshal([]byte(a.ExceptionsJSON), &ex)
	if rules == nil {
		rules = []WeekRule{}
	}
	if ex == nil {
		ex = []ExceptionDay{}
	}
	return AvailabilityDTO{
		ProfileID: a.ProfileID, Timezone: a.Timezone, Rules: rules, Exceptions: ex,
	}
}

// HireOutcomeDTO for hire responses.
type HireOutcomeDTO struct {
	ID             string    `json:"id"`
	ApplicationID  string    `json:"application_id"`
	JobID          string    `json:"job_id"`
	ProfileID      string    `json:"profile_id"`
	BillingRef     string    `json:"billing_ref,omitempty"`
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
}

func HireOutcomeToDTO(h *HireOutcome) *HireOutcomeDTO {
	if h == nil {
		return nil
	}
	return &HireOutcomeDTO{
		ID: h.ID, ApplicationID: h.ApplicationID, JobID: h.JobID, ProfileID: h.ProfileID,
		BillingRef: h.BillingRef, IdempotencyKey: h.IdempotencyKey, CreatedAt: h.CreatedAt,
	}
}

// DashboardDTO powers the Today tab.
type DashboardDTO struct {
	OpenJobs           int            `json:"open_jobs"`
	ActiveApplications int            `json:"active_applications"`
	InterviewsThisWeek int            `json:"interviews_this_week"`
	UpcomingInterviews []InterviewDTO `json:"upcoming_interviews"`
	NeedsAttention     []string       `json:"needs_attention"`
}

// TalentHit is a shortlist entry (no PII copy of profile service).
type TalentHit struct {
	ProfileID   string  `json:"profile_id"`
	CandidateID string  `json:"candidate_id,omitempty"`
	Score       float32 `json:"score,omitempty"`
	Summary     string  `json:"summary,omitempty"`
}
