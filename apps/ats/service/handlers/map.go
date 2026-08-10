package handlers

import (
	"time"

	atsv1 "github.com/stawi-opportunities/opportunities/apps/ats/gen/ats/v1"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func fmtTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

func jobProto(j *models.Job) *atsv1.Job {
	if j == nil {
		return nil
	}
	return &atsv1.Job{
		Id: j.ID, TenantId: j.TenantID, PartitionId: j.PartitionID,
		Title: j.Title, Description: j.Description, Location: j.Location,
		Status: j.Status, Visibility: j.Visibility, OpportunityId: j.OpportunityID,
		PublishedAt: fmtTimePtr(j.PublishedAt),
		CreatedAt:   fmtTime(j.CreatedAt), UpdatedAt: fmtTime(j.ModifiedAt),
	}
}

func appProto(a *models.Application) *atsv1.Application {
	if a == nil {
		return nil
	}
	return &atsv1.Application{
		Id: a.ID, JobId: a.JobID, ProfileId: a.ProfileID, CandidateId: a.CandidateID,
		Stage: a.Stage, Source: a.Source, SourceRef: a.SourceRef, Status: a.Status,
		Summary: a.Summary, Score: a.Score,
		CreatedAt: fmtTime(a.CreatedAt), UpdatedAt: fmtTime(a.ModifiedAt),
	}
}

func appDTOProto(a models.ApplicationDTO) *atsv1.Application {
	return &atsv1.Application{
		Id: a.ID, JobId: a.JobID, ProfileId: a.ProfileID, CandidateId: a.CandidateID,
		Stage: a.Stage, Source: a.Source, SourceRef: a.SourceRef, Status: a.Status,
		Summary: a.Summary, Score: a.Score, JobTitle: a.JobTitle,
		CreatedAt: fmtTime(a.CreatedAt), UpdatedAt: fmtTime(a.UpdatedAt),
	}
}

func interviewProto(iv *models.Interview) *atsv1.Interview {
	if iv == nil {
		return nil
	}
	d := models.InterviewToDTO(iv)
	return interviewDTOProto(d)
}

func interviewDTOProto(d models.InterviewDTO) *atsv1.Interview {
	return &atsv1.Interview{
		Id: d.ID, ApplicationId: d.ApplicationID, Type: d.Type, DurationMin: int32(d.DurationMin),
		Panel: d.Panel, Status: d.Status,
		SlotStart: fmtTimePtr(d.SlotStart), SlotEnd: fmtTimePtr(d.SlotEnd),
		Location: d.Location, VideoUrl: d.VideoURL, IcsUid: d.ICSUID,
		JobId: d.JobID, JobTitle: d.JobTitle, CandidateProfileId: d.CandidateID,
	}
}

func hireProto(h *models.HireOutcome) *atsv1.HireOutcome {
	if h == nil {
		return nil
	}
	return &atsv1.HireOutcome{
		Id: h.ID, ApplicationId: h.ApplicationID, JobId: h.JobID, ProfileId: h.ProfileID,
		BillingRef: h.BillingRef, IdempotencyKey: h.IdempotencyKey, CreatedAt: fmtTime(h.CreatedAt),
	}
}

func dashboardProto(d *models.DashboardDTO) *atsv1.Dashboard {
	if d == nil {
		return nil
	}
	ivs := make([]*atsv1.Interview, 0, len(d.UpcomingInterviews))
	for _, iv := range d.UpcomingInterviews {
		ivs = append(ivs, interviewDTOProto(iv))
	}
	return &atsv1.Dashboard{
		OpenJobs: int32(d.OpenJobs), ActiveApplications: int32(d.ActiveApplications),
		InterviewsThisWeek: int32(d.InterviewsThisWeek),
		UpcomingInterviews: ivs, NeedsAttention: d.NeedsAttention,
	}
}

func availabilityProto(a *models.Availability) *atsv1.Availability {
	if a == nil {
		return nil
	}
	d := models.AvailabilityToDTO(a)
	rules := make([]*atsv1.WeekRule, 0, len(d.Rules))
	for _, r := range d.Rules {
		rules = append(rules, &atsv1.WeekRule{Weekday: int32(r.Weekday), Start: r.Start, End: r.End})
	}
	ex := make([]*atsv1.ExceptionDay, 0, len(d.Exceptions))
	for _, e := range d.Exceptions {
		ex = append(ex, &atsv1.ExceptionDay{Date: e.Date, Blocked: e.Blocked})
	}
	return &atsv1.Availability{
		ProfileId: d.ProfileID, Timezone: d.Timezone, Rules: rules, Exceptions: ex,
	}
}

func talentProto(h models.TalentHit) *atsv1.TalentHit {
	return &atsv1.TalentHit{
		ProfileId: h.ProfileID, CandidateId: h.CandidateID, Score: h.Score, Summary: h.Summary,
	}
}
