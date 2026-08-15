package tests

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/business"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

type BusinessSuite struct {
	ATSBaseTestSuite
}

func TestBusinessSuite(t *testing.T) {
	suite.Run(t, new(BusinessSuite))
}

func (s *BusinessSuite) TestCreateJobApplicationAdvanceHire_PartitionIsolation() {
	t := s.T()
	ctx, deps := s.CreateService(t)
	rec := ClaimsContext(ctx, "tenant-a", "part-a", "recruiter-1")

	j, err := deps.Svc.CreateJob(rec, business.CreateJobInput{
		Title: "Backend", Description: "Go Postgres", Status: models.JobStatusOpen,
	})
	require.NoError(t, err)
	require.Equal(t, "tenant-a", j.TenantID)
	require.Equal(t, "part-a", j.PartitionID)

	a, err := deps.Svc.CreateApplication(rec, business.CreateApplicationInput{
		JobID: j.ID, ProfileID: "cand-1", Summary: "Go engineer",
	})
	require.NoError(t, err)
	require.Equal(t, models.StageApplied, a.Stage)

	_, err = deps.Svc.CreateApplication(rec, business.CreateApplicationInput{
		JobID: j.ID, ProfileID: "cand-1",
	})
	require.ErrorIs(t, err, models.ErrConflict)

	other := ClaimsContext(ctx, "tenant-a", "part-b", "recruiter-2")
	_, err = deps.Svc.GetJob(other, j.ID)
	require.ErrorIs(t, err, models.ErrNotFound)

	for _, st := range []string{models.StageScreen, models.StageInterview, models.StageOffer} {
		a, err = deps.Svc.Advance(rec, a.ID, st, "")
		require.NoError(t, err)
	}
	a, h, err := deps.Svc.Hire(rec, a.ID)
	require.NoError(t, err)
	require.Equal(t, models.AppStatusHired, a.Status)
	require.NotNil(t, h)
	require.Contains(t, h.BillingRef, "result_hire_")
	require.Contains(t, h.BillingRef, a.ID)

	// Idempotent hire
	_, h2, err := deps.Svc.Hire(rec, a.ID)
	require.NoError(t, err)
	require.Equal(t, h.ID, h2.ID)
}

func (s *BusinessSuite) TestInterviewSlotsBookAndOutbox() {
	t := s.T()
	ctx, deps := s.CreateService(t)
	rec := ClaimsContext(ctx, "t1", "p1", "rec-1")

	_, err := deps.Svc.SetAvailability(rec, business.SetAvailabilityInput{
		Timezone: "UTC",
		Rules: []models.WeekRule{
			{Weekday: int(time.Monday), Start: "09:00", End: "17:00"},
			{Weekday: int(time.Tuesday), Start: "09:00", End: "17:00"},
			{Weekday: int(time.Wednesday), Start: "09:00", End: "17:00"},
			{Weekday: int(time.Thursday), Start: "09:00", End: "17:00"},
			{Weekday: int(time.Friday), Start: "09:00", End: "17:00"},
		},
	})
	require.NoError(t, err)

	j, err := deps.Svc.CreateJob(rec, business.CreateJobInput{Title: "X", Status: models.JobStatusOpen})
	require.NoError(t, err)
	a, err := deps.Svc.CreateApplication(rec, business.CreateApplicationInput{JobID: j.ID, ProfileID: "c1"})
	require.NoError(t, err)

	iv, err := deps.Svc.ProposeInterview(rec, business.ProposeInterviewInput{
		ApplicationID: a.ID, DurationMin: 30, Panel: []string{"rec-1"},
	})
	require.NoError(t, err)

	slots, err := deps.Svc.ListSlots(rec, iv.ID)
	require.NoError(t, err)
	require.NotEmpty(t, slots)

	booked, err := deps.Svc.BookInterview(rec, business.BookInterviewInput{
		InterviewID: iv.ID, Start: slots[0].Start, End: slots[0].End,
	})
	require.NoError(t, err)
	require.Equal(t, models.InterviewScheduled, booked.Status)

	ics, err := deps.Svc.GetInterviewICS(rec, booked.ID)
	require.NoError(t, err)
	require.Contains(t, ics, "BEGIN:VCALENDAR")

	// Notification intent must land in outbox (no notification client in tests).
	pending, err := deps.Outbox.ListPending(t.Context(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, pending)
	require.Equal(t, models.OutboxKindInterviewScheduled, pending[0].Kind)
}

func (s *BusinessSuite) TestPublishProjection() {
	t := s.T()
	ctx, deps := s.CreateService(t)
	rec := ClaimsContext(ctx, "t1", "p1", "rec-1")
	j, err := deps.Svc.CreateJob(rec, business.CreateJobInput{Title: "Pub", Description: "Role"})
	require.NoError(t, err)
	j, err = deps.Svc.PublishJob(rec, j.ID)
	require.NoError(t, err)
	require.Equal(t, models.VisibilityPublished, j.Visibility)
	require.NotEmpty(t, j.OpportunityID)
	require.Equal(t, models.JobStatusOpen, j.Status)

	proj, err := deps.Projections.GetByJob(rec, j.TenantID, j.PartitionID, j.ID)
	require.NoError(t, err)
	require.NotNil(t, proj)
	require.Equal(t, "published", proj.Status)
	require.Equal(t, j.OpportunityID, proj.OpportunityID)

	j, err = deps.Svc.UnpublishJob(rec, j.ID)
	require.NoError(t, err)
	require.Equal(t, models.VisibilityPrivate, j.Visibility)
	proj, err = deps.Projections.GetByJob(rec, j.TenantID, j.PartitionID, j.ID)
	require.NoError(t, err)
	require.Equal(t, "unpublished", proj.Status)
}

func (s *BusinessSuite) TestEmptyTalentDefault() {
	t := s.T()
	ctx, deps := s.CreateService(t)
	rec := ClaimsContext(ctx, "t1", "p1", "rec-1")
	j, err := deps.Svc.CreateJob(rec, business.CreateJobInput{Title: "Eng", Status: models.JobStatusOpen})
	require.NoError(t, err)
	hits, err := deps.Svc.ListTalent(rec, j.ID, 5)
	require.NoError(t, err)
	require.Empty(t, hits)
}

func (s *BusinessSuite) TestCandidateMyApplicationsAndBook() {
	t := s.T()
	ctx, deps := s.CreateService(t)
	rec := ClaimsContext(ctx, "t1", "p1", "rec-1")
	cand := ClaimsContext(ctx, "t1", "p1", "cand-profile")

	_, err := deps.Svc.SetAvailability(rec, business.SetAvailabilityInput{
		Timezone: "UTC",
		Rules: []models.WeekRule{
			{Weekday: int(time.Monday), Start: "09:00", End: "17:00"},
			{Weekday: int(time.Tuesday), Start: "09:00", End: "17:00"},
			{Weekday: int(time.Wednesday), Start: "09:00", End: "17:00"},
			{Weekday: int(time.Thursday), Start: "09:00", End: "17:00"},
			{Weekday: int(time.Friday), Start: "09:00", End: "17:00"},
		},
	})
	require.NoError(t, err)

	j, err := deps.Svc.CreateJob(rec, business.CreateJobInput{Title: "CandRole", Status: models.JobStatusOpen})
	require.NoError(t, err)
	a, err := deps.Svc.CreateApplication(rec, business.CreateApplicationInput{
		JobID: j.ID, ProfileID: "cand-profile",
	})
	require.NoError(t, err)
	iv, err := deps.Svc.ProposeInterview(rec, business.ProposeInterviewInput{
		ApplicationID: a.ID, DurationMin: 30, Panel: []string{"rec-1"},
	})
	require.NoError(t, err)

	mine, err := deps.Svc.MyApplications(cand)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	require.Equal(t, "CandRole", mine[0].JobTitle)

	slots, err := deps.Svc.ListSlots(cand, iv.ID)
	require.NoError(t, err)
	require.NotEmpty(t, slots)
	booked, err := deps.Svc.BookInterview(cand, business.BookInterviewInput{
		InterviewID: iv.ID, Start: slots[0].Start, End: slots[0].End,
	})
	require.NoError(t, err)
	require.Equal(t, models.InterviewScheduled, booked.Status)
}
