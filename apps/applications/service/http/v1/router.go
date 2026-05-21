package v1

import (
	"net/http"
)

// Mount registers every Phase-3 route onto the given mux with the
// standard middleware stack: CandidateAuth applies to every /api/me/*
// route; Idempotency wraps mutating routes.
func Mount(mux *http.ServeMux, deps *Deps) {
	idem := func(group string, h http.Handler) http.Handler {
		return Idempotency(IdempotencyConfig{Store: deps.Idempotency, RouteGroup: group}, h)
	}
	auth := CandidateAuth

	mux.Handle("POST /api/me/applications",
		auth(idem("applications.create", createApplication(deps))))
	mux.Handle("GET /api/me/applications",
		auth(listApplications(deps)))
	mux.Handle("GET /api/me/applications/{id}",
		auth(getApplication(deps)))
	mux.Handle("PATCH /api/me/applications/{id}",
		auth(idem("applications.patch", patchApplication(deps))))
	mux.Handle("GET /api/me/applications/{id}/events",
		auth(listEvents(deps)))
	mux.Handle("POST /api/me/applications/{id}/events",
		auth(idem("applications.events.append", appendEvent(deps))))
}
