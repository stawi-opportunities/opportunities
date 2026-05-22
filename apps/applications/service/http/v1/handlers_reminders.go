package v1

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

type createReminderReq struct {
	DueAt time.Time `json:"due_at"`
	Note  string    `json:"note,omitempty"`
}

type patchReminderReq struct {
	DueAt  *time.Time `json:"due_at,omitempty"`
	Status *string    `json:"status,omitempty"`
	Note   *string    `json:"note,omitempty"`
}

type reminderResp struct {
	ReminderID    string     `json:"reminder_id"`
	ApplicationID string     `json:"application_id"`
	DueAt         time.Time  `json:"due_at"`
	Status        string     `json:"status"`
	Note          string     `json:"note,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func toReminderResp(r *applications.Reminder) reminderResp {
	return reminderResp{
		ReminderID: r.ReminderID, ApplicationID: r.ApplicationID,
		DueAt: r.DueAt, Status: string(r.Status), Note: r.Note,
		CompletedAt: r.CompletedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func createReminder(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, stop := loadOwnedApplication(w, r, d, r.PathValue("id"))
		if stop {
			return
		}
		var req createReminderReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DueAt.IsZero() {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "due_at required")
			return
		}
		rid := d.newID()
		out, err := d.RemindersStore.Create(r.Context(), applications.Reminder{
			ReminderID: rid, ApplicationID: app.ApplicationID,
			DueAt: req.DueAt, Note: req.Note, Status: applications.ReminderPending,
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
			Kind:          applications.EventKindReminderSet,
			Actor:         applications.ActorExtension,
			Data:          map[string]any{"reminder_id": rid, "due_at": req.DueAt},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toReminderResp(out))
	}
}

func updateReminder(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, stop := loadOwnedApplication(w, r, d, r.PathValue("id"))
		if stop {
			return
		}
		rid := r.PathValue("rid")
		existing, err := d.RemindersStore.Get(r.Context(), rid)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if existing.ApplicationID != app.ApplicationID || existing.DeletedAt != nil {
			ProblemJSON(w, http.StatusNotFound, "not_found", "reminder not found")
			return
		}
		var req patchReminderReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "invalid body")
			return
		}
		patch := applications.ReminderPatch{DueAt: req.DueAt, Note: req.Note}
		if req.Status != nil {
			s := applications.ReminderStatus(*req.Status)
			patch.Status = &s
		}
		updated, err := d.RemindersStore.Update(r.Context(), rid, patch)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if updated.Status == applications.ReminderDone && existing.Status != applications.ReminderDone {
			_ = d.EventLog.Write(r.Context(), applications.Event{
				EventID:       d.newID(),
				OccurredAt:    d.now(),
				ApplicationID: app.ApplicationID,
				CandidateID:   app.CandidateID,
				Kind:          applications.EventKindReminderDone,
				Actor:         applications.ActorExtension,
				Data:          map[string]any{"reminder_id": rid},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toReminderResp(updated))
	}
}

func deleteReminder(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, stop := loadOwnedApplication(w, r, d, r.PathValue("id"))
		if stop {
			return
		}
		rid := r.PathValue("rid")
		existing, err := d.RemindersStore.Get(r.Context(), rid)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if existing.ApplicationID != app.ApplicationID {
			ProblemJSON(w, http.StatusNotFound, "not_found", "reminder not found")
			return
		}
		if err := d.RemindersStore.SoftDelete(r.Context(), rid); err != nil {
			ProblemFromError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listAllReminders(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := CandidateFromContext(r.Context())
		// Enumerate the candidate's applications and concatenate.
		// For Phase 3 the candidate-side reminder list is short
		// (<100 in practice); a JOIN-based query lands in Phase 5.
		page, err := d.Store.ListByCandidate(r.Context(), applications.ListByCandidateParams{
			CandidateID: cand,
			Limit:       200,
		})
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		out := make([]reminderResp, 0)
		for _, app := range page.Items {
			rems, err := d.RemindersStore.ListActiveByApplication(r.Context(), app.ApplicationID)
			if err != nil {
				ProblemFromError(w, err)
				return
			}
			for _, rem := range rems {
				rem := rem
				out = append(out, toReminderResp(&rem))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
	}
}
