// Package handlers is the HTTP interaction layer (validation/serialization only).
// Structure mirrors service-profile Connect handlers: thin → business → repository.
// SPA and agents share this surface; platform Connect peer API can wrap the same business layer later.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/business"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// Server is the ATS HTTP API.
type Server struct {
	svc  *business.Service
	auth func(http.Handler) http.Handler
}

// NewServer wires business + auth middleware.
func NewServer(svc *business.Service, auth func(http.Handler) http.Handler) *Server {
	if auth == nil {
		auth = TenancyAuth(nil, true)
	}
	return &Server{svc: svc, auth: auth}
}

// Handler returns the fully mounted mux (health is registered by main).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

// Mount registers /v1 routes on mux.
func (s *Server) Mount(mux *http.ServeMux) {
	a := s.auth
	mux.Handle("GET /v1/dashboard", a(http.HandlerFunc(s.dashboard)))
	mux.Handle("POST /v1/demo/seed", a(http.HandlerFunc(s.seedDemo)))

	mux.Handle("GET /v1/jobs", a(http.HandlerFunc(s.listJobs)))
	mux.Handle("POST /v1/jobs", a(http.HandlerFunc(s.createJob)))
	mux.Handle("GET /v1/jobs/{id}", a(http.HandlerFunc(s.getJob)))
	mux.Handle("PATCH /v1/jobs/{id}", a(http.HandlerFunc(s.patchJob)))
	mux.Handle("POST /v1/jobs/{id}/close", a(http.HandlerFunc(s.closeJob)))
	mux.Handle("POST /v1/jobs/{id}/publish", a(http.HandlerFunc(s.publishJob)))
	mux.Handle("POST /v1/jobs/{id}/unpublish", a(http.HandlerFunc(s.unpublishJob)))
	mux.Handle("GET /v1/jobs/{id}/applications", a(http.HandlerFunc(s.listApplications)))
	mux.Handle("POST /v1/jobs/{id}/applications", a(http.HandlerFunc(s.createApplication)))
	mux.Handle("GET /v1/jobs/{id}/talent", a(http.HandlerFunc(s.listTalent)))
	mux.Handle("POST /v1/jobs/{id}/talent", a(http.HandlerFunc(s.addTalent)))

	mux.Handle("GET /v1/applications/{id}", a(http.HandlerFunc(s.getApplication)))
	mux.Handle("POST /v1/applications/{id}/advance", a(http.HandlerFunc(s.advance)))
	mux.Handle("POST /v1/applications/{id}/hire", a(http.HandlerFunc(s.hire)))
	mux.Handle("GET /v1/applications/{id}/interviews", a(http.HandlerFunc(s.listAppInterviews)))
	mux.Handle("POST /v1/applications/{id}/interviews", a(http.HandlerFunc(s.proposeInterview)))
	mux.Handle("POST /v1/ai/applications/{id}/screen-summary", a(http.HandlerFunc(s.screenSummary)))

	mux.Handle("GET /v1/interviews/{id}/slots", a(http.HandlerFunc(s.listSlots)))
	mux.Handle("POST /v1/interviews/{id}/book", a(http.HandlerFunc(s.bookInterview)))
	mux.Handle("GET /v1/interviews/{id}/ics", a(http.HandlerFunc(s.interviewICS)))

	mux.Handle("GET /v1/me/availability", a(http.HandlerFunc(s.getAvailability)))
	mux.Handle("PUT /v1/me/availability", a(http.HandlerFunc(s.putAvailability)))
	mux.Handle("GET /v1/me/applications", a(http.HandlerFunc(s.myApplications)))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func mapErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, models.ErrNotFound):
		httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, models.ErrConflict):
		httpmw.ProblemJSON(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, models.ErrInvalid), errors.Is(err, models.ErrEmptyAvail):
		httpmw.ProblemJSON(w, http.StatusUnprocessableEntity, "invalid", err.Error())
	case errors.Is(err, models.ErrForbidden):
		httpmw.ProblemJSON(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		httpmw.ProblemJSON(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := s.svc.Dashboard(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) seedDemo(w http.ResponseWriter, r *http.Request) {
	if err := business.SeedDemoWorkspace(r.Context(), s.svc); err != nil {
		mapErr(w, err)
		return
	}
	d, err := s.svc.Dashboard(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seeded": true, "dashboard": d})
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.svc.ListJobs(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]models.JobDTO, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, models.JobToDTO(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title, Description, Location, Status string
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	j, err := s.svc.CreateJob(r.Context(), business.CreateJobInput{
		Title: body.Title, Description: body.Description, Location: body.Location, Status: body.Status,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, models.JobToDTO(j))
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.JobToDTO(j))
}

func (s *Server) patchJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title, Description, Location, Status *string
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	j, err := s.svc.UpdateJob(r.Context(), r.PathValue("id"), business.UpdateJobInput{
		Title: body.Title, Description: body.Description, Location: body.Location, Status: body.Status,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.JobToDTO(j))
}

func (s *Server) closeJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.CloseJob(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.JobToDTO(j))
}

func (s *Server) publishJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.PublishJob(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.JobToDTO(j))
}

func (s *Server) unpublishJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.UnpublishJob(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.JobToDTO(j))
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := s.svc.ListApplications(r.Context(), r.PathValue("id"), r.URL.Query().Get("stage"))
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]models.ApplicationDTO, 0, len(apps))
	for _, a := range apps {
		out = append(out, models.ApplicationToDTO(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": out})
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProfileID, CandidateID, Source, SourceRef, Summary string
		Score                                              float32
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	a, err := s.svc.CreateApplication(r.Context(), business.CreateApplicationInput{
		JobID: r.PathValue("id"), ProfileID: body.ProfileID, CandidateID: body.CandidateID,
		Source: body.Source, SourceRef: body.SourceRef, Summary: body.Summary, Score: body.Score,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, models.ApplicationToDTO(a))
}

func (s *Server) listTalent(w http.ResponseWriter, r *http.Request) {
	hits, err := s.svc.ListTalent(r.Context(), r.PathValue("id"), 20)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"talent": hits})
}

func (s *Server) addTalent(w http.ResponseWriter, r *http.Request) {
	var body models.TalentHit
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	a, err := s.svc.AddTalent(r.Context(), r.PathValue("id"), body)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, models.ApplicationToDTO(a))
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	a, err := s.svc.GetApplication(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.ApplicationToDTO(a))
}

func (s *Server) advance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ToStage string `json:"to_stage"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	a, err := s.svc.Advance(r.Context(), r.PathValue("id"), body.ToStage, body.Note)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.ApplicationToDTO(a))
}

func (s *Server) hire(w http.ResponseWriter, r *http.Request) {
	a, outcome, err := s.svc.Hire(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"application": models.ApplicationToDTO(a), "hire_outcome": models.HireOutcomeToDTO(outcome),
	})
}

func (s *Server) proposeInterview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type, Location, VideoURL string
		DurationMin              int
		Panel                    []string
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	iv, err := s.svc.ProposeInterview(r.Context(), business.ProposeInterviewInput{
		ApplicationID: r.PathValue("id"), Type: body.Type, DurationMin: body.DurationMin,
		Panel: body.Panel, Location: body.Location, VideoURL: body.VideoURL,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, models.InterviewToDTO(iv))
}

func (s *Server) listAppInterviews(w http.ResponseWriter, r *http.Request) {
	rows, err := s.svc.ListInterviews(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]models.InterviewDTO, 0, len(rows))
	for _, iv := range rows {
		out = append(out, models.InterviewToDTO(iv))
	}
	writeJSON(w, http.StatusOK, map[string]any{"interviews": out})
}

func (s *Server) listSlots(w http.ResponseWriter, r *http.Request) {
	slots, err := s.svc.ListSlots(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

func (s *Server) bookInterview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Start, End time.Time
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	iv, err := s.svc.BookInterview(r.Context(), business.BookInterviewInput{
		InterviewID: r.PathValue("id"), Start: body.Start, End: body.End,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.InterviewToDTO(iv))
}

func (s *Server) interviewICS(w http.ResponseWriter, r *http.Request) {
	ics, err := s.svc.GetInterviewICS(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"interview.ics\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(ics))
}

func (s *Server) getAvailability(w http.ResponseWriter, r *http.Request) {
	a, err := s.svc.GetMyAvailability(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if a == nil {
		writeJSON(w, http.StatusOK, map[string]any{"availability": nil})
		return
	}
	writeJSON(w, http.StatusOK, models.AvailabilityToDTO(a))
}

func (s *Server) putAvailability(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Timezone   string
		Rules      []models.WeekRule
		Exceptions []models.ExceptionDay
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	a, err := s.svc.SetAvailability(r.Context(), business.SetAvailabilityInput{
		Timezone: body.Timezone, Rules: body.Rules, Exceptions: body.Exceptions,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.AvailabilityToDTO(a))
}

func (s *Server) screenSummary(w http.ResponseWriter, r *http.Request) {
	text, err := s.svc.ScreenSummary(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": text})
}

func (s *Server) myApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := s.svc.MyApplications(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": apps})
}
