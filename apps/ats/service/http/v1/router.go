package v1

import (
	"net/http"

	"github.com/stawi-opportunities/opportunities/pkg/ats"
)

// Deps wires HTTP handlers.
type Deps struct {
	Svc  *ats.Service
	Auth func(http.Handler) http.Handler
}

// Mount registers ATS routes under /v1.
func Mount(mux *http.ServeMux, deps *Deps) {
	auth := deps.Auth
	if auth == nil {
		auth = TenancyAuth(nil, true)
	}
	h := &handlers{svc: deps.Svc}

	mux.Handle("GET /v1/dashboard", auth(http.HandlerFunc(h.dashboard)))
	mux.Handle("POST /v1/demo/seed", auth(http.HandlerFunc(h.seedDemo)))

	mux.Handle("GET /v1/jobs", auth(http.HandlerFunc(h.listJobs)))
	mux.Handle("POST /v1/jobs", auth(http.HandlerFunc(h.createJob)))
	mux.Handle("GET /v1/jobs/{id}", auth(http.HandlerFunc(h.getJob)))
	mux.Handle("PATCH /v1/jobs/{id}", auth(http.HandlerFunc(h.patchJob)))
	mux.Handle("POST /v1/jobs/{id}/close", auth(http.HandlerFunc(h.closeJob)))
	mux.Handle("POST /v1/jobs/{id}/publish", auth(http.HandlerFunc(h.publishJob)))
	mux.Handle("POST /v1/jobs/{id}/unpublish", auth(http.HandlerFunc(h.unpublishJob)))
	mux.Handle("GET /v1/jobs/{id}/applications", auth(http.HandlerFunc(h.listApplications)))
	mux.Handle("POST /v1/jobs/{id}/applications", auth(http.HandlerFunc(h.createApplication)))
	mux.Handle("GET /v1/jobs/{id}/talent", auth(http.HandlerFunc(h.listTalent)))
	mux.Handle("POST /v1/jobs/{id}/talent", auth(http.HandlerFunc(h.addTalent)))

	mux.Handle("GET /v1/applications/{id}", auth(http.HandlerFunc(h.getApplication)))
	mux.Handle("POST /v1/applications/{id}/advance", auth(http.HandlerFunc(h.advance)))
	mux.Handle("POST /v1/applications/{id}/hire", auth(http.HandlerFunc(h.hire)))
	mux.Handle("GET /v1/applications/{id}/interviews", auth(http.HandlerFunc(h.listAppInterviews)))
	mux.Handle("POST /v1/applications/{id}/interviews", auth(http.HandlerFunc(h.proposeInterview)))
	mux.Handle("POST /v1/ai/applications/{id}/screen-summary", auth(http.HandlerFunc(h.screenSummary)))

	mux.Handle("GET /v1/interviews/{id}/slots", auth(http.HandlerFunc(h.listSlots)))
	mux.Handle("POST /v1/interviews/{id}/book", auth(http.HandlerFunc(h.bookInterview)))
	mux.Handle("GET /v1/interviews/{id}/ics", auth(http.HandlerFunc(h.interviewICS)))

	mux.Handle("GET /v1/me/availability", auth(http.HandlerFunc(h.getAvailability)))
	mux.Handle("PUT /v1/me/availability", auth(http.HandlerFunc(h.putAvailability)))
	mux.Handle("GET /v1/me/applications", auth(http.HandlerFunc(h.myApplications)))
}
