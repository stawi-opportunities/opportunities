package v1

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

type noteReq struct {
	Body string `json:"body"`
}

type noteResp struct {
	NoteID        string    `json:"note_id"`
	ApplicationID string    `json:"application_id"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toNoteResp(n *applications.Note) noteResp {
	return noteResp{
		NoteID: n.NoteID, ApplicationID: n.ApplicationID, Body: n.Body,
		CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
	}
}

// loadOwnedApplication looks up an application and returns 404 if it
// doesn't belong to the authenticated candidate. Returns (nil, true) if
// the handler should stop (response already written).
func loadOwnedApplication(w http.ResponseWriter, r *http.Request, d *Deps, applicationID string) (*applications.Application, bool) {
	cand := CandidateFromContext(r.Context())
	app, err := d.Store.GetByID(r.Context(), applicationID)
	if err != nil {
		ProblemFromError(w, err)
		return nil, true
	}
	if app.CandidateID != cand {
		ProblemJSON(w, http.StatusNotFound, "not_found", "application not found")
		return nil, true
	}
	return app, false
}

func createNote(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, stop := loadOwnedApplication(w, r, d, r.PathValue("id"))
		if stop {
			return
		}
		var req noteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "body required")
			return
		}
		noteID := d.newID()
		n, err := d.NotesStore.Create(r.Context(), applications.Note{
			NoteID: noteID, ApplicationID: app.ApplicationID, Body: req.Body,
		})
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		_ = d.EventLog.Write(r.Context(), applications.Event{
			EventID:       d.newID(),
			OccurredAt:    d.now(),
			ApplicationID: app.ApplicationID,
			CandidateID:   app.CandidateID,
			Kind:          applications.EventKindNoteAdded,
			Actor:         applications.ActorExtension,
			Data:          map[string]any{"note_id": noteID},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toNoteResp(n))
	}
}

func updateNote(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, stop := loadOwnedApplication(w, r, d, r.PathValue("id"))
		if stop {
			return
		}
		noteID := r.PathValue("note_id")
		var req noteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "body required")
			return
		}
		// ownership: the note must belong to this application
		existing, err := d.NotesStore.Get(r.Context(), noteID)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if existing.ApplicationID != app.ApplicationID || existing.DeletedAt != nil {
			ProblemJSON(w, http.StatusNotFound, "not_found", "note not found")
			return
		}
		n, err := d.NotesStore.Update(r.Context(), noteID, req.Body)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		_ = d.EventLog.Write(r.Context(), applications.Event{
			EventID:       d.newID(),
			OccurredAt:    d.now(),
			ApplicationID: app.ApplicationID,
			CandidateID:   app.CandidateID,
			Kind:          applications.EventKindNoteEdited,
			Actor:         applications.ActorExtension,
			Data:          map[string]any{"note_id": noteID},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toNoteResp(n))
	}
}

func deleteNote(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, stop := loadOwnedApplication(w, r, d, r.PathValue("id"))
		if stop {
			return
		}
		noteID := r.PathValue("note_id")
		existing, err := d.NotesStore.Get(r.Context(), noteID)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if existing.ApplicationID != app.ApplicationID {
			ProblemJSON(w, http.StatusNotFound, "not_found", "note not found")
			return
		}
		if err := d.NotesStore.SoftDelete(r.Context(), noteID); err != nil {
			ProblemFromError(w, err)
			return
		}
		_ = d.EventLog.Write(r.Context(), applications.Event{
			EventID:       d.newID(),
			OccurredAt:    d.now(),
			ApplicationID: app.ApplicationID,
			CandidateID:   app.CandidateID,
			Kind:          applications.EventKindNoteDeleted,
			Actor:         applications.ActorExtension,
			Data:          map[string]any{"note_id": noteID},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
