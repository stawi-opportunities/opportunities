package v1

import (
	"net/http"
)

// Mount registers every Phase-3 route onto the given mux with the
// standard middleware stack: CandidateAuth (or JWT via deps.Auth)
// applies to every /api/me/* route; Idempotency wraps mutating routes.
func Mount(mux *http.ServeMux, deps *Deps) {
	idem := func(group string, h http.Handler) http.Handler {
		return Idempotency(IdempotencyConfig{Store: deps.Idempotency, RouteGroup: group}, h)
	}
	// Production injects NewCandidateAuth(authenticator); tests leave Auth
	// nil and get header-only CandidateAuth via NewCandidateAuth(nil).
	auth := deps.Auth
	if auth == nil {
		auth = CandidateAuth
	}

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

	// Notes
	mux.Handle("POST /api/me/applications/{id}/notes",
		auth(idem("applications.notes.create", createNote(deps))))
	mux.Handle("PATCH /api/me/applications/{id}/notes/{note_id}",
		auth(idem("applications.notes.update", updateNote(deps))))
	mux.Handle("DELETE /api/me/applications/{id}/notes/{note_id}",
		auth(deleteNote(deps)))

	// Reminders
	mux.Handle("GET /api/me/reminders",
		auth(listAllReminders(deps)))
	mux.Handle("POST /api/me/applications/{id}/reminders",
		auth(idem("applications.reminders.create", createReminder(deps))))
	mux.Handle("PATCH /api/me/applications/{id}/reminders/{rid}",
		auth(idem("applications.reminders.update", updateReminder(deps))))
	mux.Handle("DELETE /api/me/applications/{id}/reminders/{rid}",
		auth(deleteReminder(deps)))

	// Attachments
	mux.Handle("POST /api/me/applications/{id}/attachments",
		auth(uploadAttachment(deps)))
	mux.Handle("DELETE /api/me/applications/{id}/attachments/{att_id}",
		auth(deleteAttachment(deps)))
	mux.Handle("GET /api/me/attachments/{att_id}",
		auth(presignAttachment(deps)))
}
