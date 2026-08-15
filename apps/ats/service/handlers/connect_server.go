package handlers

import (
	"context"

	"connectrpc.com/connect"

	atsv1 "github.com/stawi-opportunities/opportunities/apps/ats/gen/ats/v1"
	"github.com/stawi-opportunities/opportunities/apps/ats/gen/ats/v1/atsv1connect"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/business"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

// ConnectServer implements atsv1connect.AtsServiceHandler.
type ConnectServer struct {
	atsv1connect.UnimplementedAtsServiceHandler
	svc *business.Service
}

// NewConnectServer constructs the Connect ATS service.
func NewConnectServer(svc *business.Service) *ConnectServer {
	return &ConnectServer{svc: svc}
}

func (s *ConnectServer) GetDashboard(ctx context.Context, _ *connect.Request[atsv1.GetDashboardRequest]) (*connect.Response[atsv1.GetDashboardResponse], error) {
	d, err := s.svc.Dashboard(ctx)
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.GetDashboardResponse{Dashboard: dashboardProto(d)}), nil
}

func (s *ConnectServer) ListJobs(ctx context.Context, req *connect.Request[atsv1.ListJobsRequest]) (*connect.Response[atsv1.ListJobsResponse], error) {
	jobs, err := s.svc.ListJobs(ctx, req.Msg.GetStatus())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	out := make([]*atsv1.Job, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobProto(j))
	}
	return connect.NewResponse(&atsv1.ListJobsResponse{Jobs: out}), nil
}

func (s *ConnectServer) CreateJob(ctx context.Context, req *connect.Request[atsv1.CreateJobRequest]) (*connect.Response[atsv1.CreateJobResponse], error) {
	j, err := s.svc.CreateJob(ctx, business.CreateJobInput{
		Title: req.Msg.GetTitle(), Description: req.Msg.GetDescription(),
		Location: req.Msg.GetLocation(), Status: req.Msg.GetStatus(),
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.CreateJobResponse{Job: jobProto(j)}), nil
}

func (s *ConnectServer) GetJob(ctx context.Context, req *connect.Request[atsv1.GetJobRequest]) (*connect.Response[atsv1.GetJobResponse], error) {
	j, err := s.svc.GetJob(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.GetJobResponse{Job: jobProto(j)}), nil
}

func (s *ConnectServer) UpdateJob(ctx context.Context, req *connect.Request[atsv1.UpdateJobRequest]) (*connect.Response[atsv1.UpdateJobResponse], error) {
	in := business.UpdateJobInput{}
	if req.Msg.Title != nil {
		v := req.Msg.GetTitle()
		in.Title = &v
	}
	if req.Msg.Description != nil {
		v := req.Msg.GetDescription()
		in.Description = &v
	}
	if req.Msg.Location != nil {
		v := req.Msg.GetLocation()
		in.Location = &v
	}
	if req.Msg.Status != nil {
		v := req.Msg.GetStatus()
		in.Status = &v
	}
	j, err := s.svc.UpdateJob(ctx, req.Msg.GetId(), in)
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.UpdateJobResponse{Job: jobProto(j)}), nil
}

func (s *ConnectServer) CloseJob(ctx context.Context, req *connect.Request[atsv1.CloseJobRequest]) (*connect.Response[atsv1.CloseJobResponse], error) {
	j, err := s.svc.CloseJob(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.CloseJobResponse{Job: jobProto(j)}), nil
}

func (s *ConnectServer) PublishJob(ctx context.Context, req *connect.Request[atsv1.PublishJobRequest]) (*connect.Response[atsv1.PublishJobResponse], error) {
	j, err := s.svc.PublishJob(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.PublishJobResponse{Job: jobProto(j)}), nil
}

func (s *ConnectServer) UnpublishJob(ctx context.Context, req *connect.Request[atsv1.UnpublishJobRequest]) (*connect.Response[atsv1.UnpublishJobResponse], error) {
	j, err := s.svc.UnpublishJob(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.UnpublishJobResponse{Job: jobProto(j)}), nil
}

func (s *ConnectServer) ListApplications(ctx context.Context, req *connect.Request[atsv1.ListApplicationsRequest]) (*connect.Response[atsv1.ListApplicationsResponse], error) {
	apps, err := s.svc.ListApplications(ctx, req.Msg.GetJobId(), req.Msg.GetStage())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	out := make([]*atsv1.Application, 0, len(apps))
	for _, a := range apps {
		out = append(out, appProto(a))
	}
	return connect.NewResponse(&atsv1.ListApplicationsResponse{Applications: out}), nil
}

func (s *ConnectServer) CreateApplication(ctx context.Context, req *connect.Request[atsv1.CreateApplicationRequest]) (*connect.Response[atsv1.CreateApplicationResponse], error) {
	a, err := s.svc.CreateApplication(ctx, business.CreateApplicationInput{
		JobID: req.Msg.GetJobId(), ProfileID: req.Msg.GetProfileId(), CandidateID: req.Msg.GetCandidateId(),
		Source: req.Msg.GetSource(), SourceRef: req.Msg.GetSourceRef(), Summary: req.Msg.GetSummary(), Score: req.Msg.GetScore(),
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.CreateApplicationResponse{Application: appProto(a)}), nil
}

func (s *ConnectServer) GetApplication(ctx context.Context, req *connect.Request[atsv1.GetApplicationRequest]) (*connect.Response[atsv1.GetApplicationResponse], error) {
	a, err := s.svc.GetApplication(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.GetApplicationResponse{Application: appProto(a)}), nil
}

func (s *ConnectServer) AdvanceApplication(ctx context.Context, req *connect.Request[atsv1.AdvanceApplicationRequest]) (*connect.Response[atsv1.AdvanceApplicationResponse], error) {
	a, err := s.svc.Advance(ctx, req.Msg.GetId(), req.Msg.GetToStage(), req.Msg.GetNote())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.AdvanceApplicationResponse{Application: appProto(a)}), nil
}

func (s *ConnectServer) HireApplication(ctx context.Context, req *connect.Request[atsv1.HireApplicationRequest]) (*connect.Response[atsv1.HireApplicationResponse], error) {
	a, h, err := s.svc.Hire(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.HireApplicationResponse{
		Application: appProto(a), HireOutcome: hireProto(h),
	}), nil
}

func (s *ConnectServer) ListTalent(ctx context.Context, req *connect.Request[atsv1.ListTalentRequest]) (*connect.Response[atsv1.ListTalentResponse], error) {
	hits, err := s.svc.ListTalent(ctx, req.Msg.GetJobId(), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapConnectErr(err)
	}
	out := make([]*atsv1.TalentHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, talentProto(h))
	}
	return connect.NewResponse(&atsv1.ListTalentResponse{Talent: out}), nil
}

func (s *ConnectServer) AddTalent(ctx context.Context, req *connect.Request[atsv1.AddTalentRequest]) (*connect.Response[atsv1.AddTalentResponse], error) {
	hit := req.Msg.GetHit()
	if hit == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, models.ErrInvalid)
	}
	a, err := s.svc.AddTalent(ctx, req.Msg.GetJobId(), models.TalentHit{
		ProfileID: hit.GetProfileId(), CandidateID: hit.GetCandidateId(),
		Score: hit.GetScore(), Summary: hit.GetSummary(),
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.AddTalentResponse{Application: appProto(a)}), nil
}

func (s *ConnectServer) ListInterviews(ctx context.Context, req *connect.Request[atsv1.ListInterviewsRequest]) (*connect.Response[atsv1.ListInterviewsResponse], error) {
	rows, err := s.svc.ListInterviews(ctx, req.Msg.GetApplicationId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	out := make([]*atsv1.Interview, 0, len(rows))
	for _, iv := range rows {
		out = append(out, interviewProto(iv))
	}
	return connect.NewResponse(&atsv1.ListInterviewsResponse{Interviews: out}), nil
}

func (s *ConnectServer) ProposeInterview(ctx context.Context, req *connect.Request[atsv1.ProposeInterviewRequest]) (*connect.Response[atsv1.ProposeInterviewResponse], error) {
	iv, err := s.svc.ProposeInterview(ctx, business.ProposeInterviewInput{
		ApplicationID: req.Msg.GetApplicationId(), Type: req.Msg.GetType(),
		DurationMin: int(req.Msg.GetDurationMin()), Panel: req.Msg.GetPanel(),
		Location: req.Msg.GetLocation(), VideoURL: req.Msg.GetVideoUrl(),
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.ProposeInterviewResponse{Interview: interviewProto(iv)}), nil
}

func (s *ConnectServer) ListInterviewSlots(ctx context.Context, req *connect.Request[atsv1.ListInterviewSlotsRequest]) (*connect.Response[atsv1.ListInterviewSlotsResponse], error) {
	slots, err := s.svc.ListSlots(ctx, req.Msg.GetInterviewId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	out := make([]*atsv1.Slot, 0, len(slots))
	for _, sl := range slots {
		out = append(out, &atsv1.Slot{Start: fmtTime(sl.Start), End: fmtTime(sl.End)})
	}
	return connect.NewResponse(&atsv1.ListInterviewSlotsResponse{Slots: out}), nil
}

func (s *ConnectServer) BookInterview(ctx context.Context, req *connect.Request[atsv1.BookInterviewRequest]) (*connect.Response[atsv1.BookInterviewResponse], error) {
	start, err := parseTime(req.Msg.GetStart())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	end, err := parseTime(req.Msg.GetEnd())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	iv, err := s.svc.BookInterview(ctx, business.BookInterviewInput{
		InterviewID: req.Msg.GetInterviewId(), Start: start, End: end,
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.BookInterviewResponse{Interview: interviewProto(iv)}), nil
}

func (s *ConnectServer) GetInterviewICS(ctx context.Context, req *connect.Request[atsv1.GetInterviewICSRequest]) (*connect.Response[atsv1.GetInterviewICSResponse], error) {
	ics, err := s.svc.GetInterviewICS(ctx, req.Msg.GetInterviewId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.GetInterviewICSResponse{Ics: ics}), nil
}

func (s *ConnectServer) GetMyAvailability(ctx context.Context, _ *connect.Request[atsv1.GetMyAvailabilityRequest]) (*connect.Response[atsv1.GetMyAvailabilityResponse], error) {
	a, err := s.svc.GetMyAvailability(ctx)
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.GetMyAvailabilityResponse{Availability: availabilityProto(a)}), nil
}

func (s *ConnectServer) SetMyAvailability(ctx context.Context, req *connect.Request[atsv1.SetMyAvailabilityRequest]) (*connect.Response[atsv1.SetMyAvailabilityResponse], error) {
	rules := make([]models.WeekRule, 0, len(req.Msg.GetRules()))
	for _, r := range req.Msg.GetRules() {
		rules = append(rules, models.WeekRule{Weekday: int(r.GetWeekday()), Start: r.GetStart(), End: r.GetEnd()})
	}
	ex := make([]models.ExceptionDay, 0, len(req.Msg.GetExceptions()))
	for _, e := range req.Msg.GetExceptions() {
		ex = append(ex, models.ExceptionDay{Date: e.GetDate(), Blocked: e.GetBlocked()})
	}
	a, err := s.svc.SetAvailability(ctx, business.SetAvailabilityInput{
		Timezone: req.Msg.GetTimezone(), Rules: rules, Exceptions: ex,
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.SetMyAvailabilityResponse{Availability: availabilityProto(a)}), nil
}

func (s *ConnectServer) ListMyApplications(ctx context.Context, _ *connect.Request[atsv1.ListMyApplicationsRequest]) (*connect.Response[atsv1.ListMyApplicationsResponse], error) {
	apps, err := s.svc.MyApplications(ctx)
	if err != nil {
		return nil, mapConnectErr(err)
	}
	out := make([]*atsv1.Application, 0, len(apps))
	for _, a := range apps {
		out = append(out, appDTOProto(a))
	}
	return connect.NewResponse(&atsv1.ListMyApplicationsResponse{Applications: out}), nil
}

func (s *ConnectServer) ScreenSummary(ctx context.Context, req *connect.Request[atsv1.ScreenSummaryRequest]) (*connect.Response[atsv1.ScreenSummaryResponse], error) {
	text, err := s.svc.ScreenSummary(ctx, req.Msg.GetApplicationId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&atsv1.ScreenSummaryResponse{Summary: text}), nil
}
