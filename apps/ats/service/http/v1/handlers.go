package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/ats"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type handlers struct {
	svc *ats.Service
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func mapErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ats.ErrNotFound):
		httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ats.ErrConflict):
		httpmw.ProblemJSON(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ats.ErrInvalid), errors.Is(err, ats.ErrEmptyAvail):
		httpmw.ProblemJSON(w, http.StatusUnprocessableEntity, "invalid", err.Error())
	case errors.Is(err, ats.ErrForbidden):
		httpmw.ProblemJSON(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		httpmw.ProblemJSON(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func (h *handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Dashboard(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *handlers) listJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	jobs, err := h.svc.ListJobs(r.Context(), status)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]ats.JobDTO, 0, len(jobs))
	for i := range jobs {
		out = append(out, ats.JobToDTO(&jobs[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (h *handlers) createJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Location    string `json:"location"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	j, err := h.svc.CreateJob(r.Context(), ats.CreateJobInput{
		Title: body.Title, Description: body.Description, Location: body.Location, Status: body.Status,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ats.JobToDTO(j))
}

func (h *handlers) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := h.svc.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ats.JobToDTO(j))
}

func (h *handlers) patchJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Location    *string `json:"location"`
		Status      *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	j, err := h.svc.UpdateJob(r.Context(), r.PathValue("id"), ats.UpdateJobInput{
		Title: body.Title, Description: body.Description, Location: body.Location, Status: body.Status,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ats.JobToDTO(j))
}

func (h *handlers) closeJob(w http.ResponseWriter, r *http.Request) {
	j, err := h.svc.CloseJob(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ats.JobToDTO(j))
}

func (h *handlers) publishJob(w http.ResponseWriter, r *http.Request) {
	j, err := h.svc.PublishJob(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ats.JobToDTO(j))
}

func (h *handlers) unpublishJob(w http.ResponseWriter, r *http.Request) {
	j, err := h.svc.UnpublishJob(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ats.JobToDTO(j))
}

func (h *handlers) listApplications(w http.ResponseWriter, r *http.Request) {
	stage := r.URL.Query().Get("stage")
	apps, err := h.svc.ListApplications(r.Context(), r.PathValue("id"), stage)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]ats.ApplicationDTO, 0, len(apps))
	for i := range apps {
		out = append(out, ats.ApplicationToDTO(&apps[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": out})
}

func (h *handlers) createApplication(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProfileID   string  `json:"profile_id"`
		CandidateID string  `json:"candidate_id"`
		Source      string  `json:"source"`
		SourceRef   string  `json:"source_ref"`
		Summary     string  `json:"summary"`
		Score       float32 `json:"score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	a, err := h.svc.CreateApplication(r.Context(), ats.CreateApplicationInput{
		JobID: r.PathValue("id"), ProfileID: body.ProfileID, CandidateID: body.CandidateID,
		Source: body.Source, SourceRef: body.SourceRef, Summary: body.Summary, Score: body.Score,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ats.ApplicationToDTO(a))
}

func (h *handlers) listTalent(w http.ResponseWriter, r *http.Request) {
	hits, err := h.svc.ListTalent(r.Context(), r.PathValue("id"), 20)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"talent": hits})
}

func (h *handlers) addTalent(w http.ResponseWriter, r *http.Request) {
	var body ats.TalentHit
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	a, err := h.svc.AddTalent(r.Context(), r.PathValue("id"), body)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ats.ApplicationToDTO(a))
}

func (h *handlers) getApplication(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.GetApplication(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ats.ApplicationToDTO(a))
}

func (h *handlers) advance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ToStage string `json:"to_stage"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	a, err := h.svc.Advance(r.Context(), r.PathValue("id"), body.ToStage, body.Note)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ats.ApplicationToDTO(a))
}

func (h *handlers) hire(w http.ResponseWriter, r *http.Request) {
	a, outcome, err := h.svc.Hire(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"application":  ats.ApplicationToDTO(a),
		"hire_outcome": ats.HireOutcomeToDTO(outcome),
	})
}

func (h *handlers) proposeInterview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type        string   `json:"type"`
		DurationMin int      `json:"duration_min"`
		Panel       []string `json:"panel"`
		Location    string   `json:"location"`
		VideoURL    string   `json:"video_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	iv, err := h.svc.ProposeInterview(r.Context(), ats.ProposeInterviewInput{
		ApplicationID: r.PathValue("id"),
		Type:          body.Type,
		DurationMin:   body.DurationMin,
		Panel:         body.Panel,
		Location:      body.Location,
		VideoURL:      body.VideoURL,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ats.InterviewToDTO(iv))
}

func (h *handlers) listAppInterviews(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.ListInterviews(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]ats.InterviewDTO, 0, len(rows))
	for i := range rows {
		out = append(out, ats.InterviewToDTO(&rows[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"interviews": out})
}

func (h *handlers) listSlots(w http.ResponseWriter, r *http.Request) {
	slots, err := h.svc.ListSlots(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

func (h *handlers) bookInterview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	iv, err := h.svc.BookInterview(r.Context(), ats.BookInterviewInput{
		InterviewID: r.PathValue("id"),
		Start:       body.Start,
		End:         body.End,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ats.InterviewToDTO(iv))
}

func (h *handlers) interviewICS(w http.ResponseWriter, r *http.Request) {
	ics, err := h.svc.GetInterviewICS(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"interview.ics\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(ics))
}

func (h *handlers) getAvailability(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.GetMyAvailability(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if a == nil {
		writeJSON(w, http.StatusOK, map[string]any{"availability": nil})
		return
	}
	writeJSON(w, http.StatusOK, ats.AvailabilityToDTO(a))
}

func (h *handlers) putAvailability(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Timezone   string             `json:"timezone"`
		Rules      []ats.WeekRule     `json:"rules"`
		Exceptions []ats.ExceptionDay `json:"exceptions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	a, err := h.svc.SetAvailability(r.Context(), ats.SetAvailabilityInput{
		Timezone: body.Timezone, Rules: body.Rules, Exceptions: body.Exceptions,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ats.AvailabilityToDTO(a))
}

func (h *handlers) screenSummary(w http.ResponseWriter, r *http.Request) {
	text, err := h.svc.ScreenSummary(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": text})
}

func (h *handlers) myApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := h.svc.MyApplications(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": apps})
}

func (h *handlers) seedDemo(w http.ResponseWriter, r *http.Request) {
	if err := ats.SeedDemoWorkspace(r.Context(), h.svc); err != nil {
		mapErr(w, err)
		return
	}
	d, err := h.svc.Dashboard(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seeded": true, "dashboard": d})
}
