package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

// Deps gathers everything the handlers need. Constructed once in
// main.go and passed to Mount.
type Deps struct {
	Store            *applications.Store
	EventLog         *applications.EventLog
	NotesStore       *applications.NotesStore
	RemindersStore   *applications.RemindersStore
	AttachmentsStore *applications.AttachmentsStore
	BlobStore        applications.BlobStore
	Idempotency      *applications.IdempotencyStore

	// Now is the time source — tests inject; defaults to time.Now.
	Now func() time.Time
	// NewID generates IDs (applications, events, notes, ...). Defaults
	// to xid-style hex.
	NewID func() string
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Deps) newID() string {
	if d.NewID != nil {
		return d.NewID()
	}
	return xid.New().String()
}

// ---- request / response shapes ----

type createApplicationReq struct {
	MatchID       string         `json:"match_id"`
	OpportunityID string         `json:"opportunity_id"`
	Status        string         `json:"status,omitempty"`
	CurrentStage  string         `json:"current_stage,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type applicationResp struct {
	ApplicationID string         `json:"application_id"`
	CandidateID   string         `json:"candidate_id"`
	OpportunityID string         `json:"opportunity_id"`
	MatchID       string         `json:"match_id"`
	Status        string         `json:"status"`
	CurrentStage  string         `json:"current_stage,omitempty"`
	Metadata      map[string]any `json:"metadata"`
	SubmittedAt   *time.Time     `json:"submitted_at,omitempty"`
	LastEventID   string         `json:"last_event_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func toAppResp(a *applications.Application) applicationResp {
	return applicationResp{
		ApplicationID: a.ApplicationID,
		CandidateID:   a.CandidateID,
		OpportunityID: a.OpportunityID,
		MatchID:       a.MatchID,
		Status:        string(a.Status),
		CurrentStage:  a.CurrentStage,
		Metadata:      a.Metadata,
		SubmittedAt:   a.SubmittedAt,
		LastEventID:   a.LastEventID,
		CreatedAt:     a.CreatedAt,
		UpdatedAt:     a.UpdatedAt,
	}
}

type eventResp struct {
	EventID       string         `json:"event_id"`
	OccurredAt    time.Time      `json:"occurred_at"`
	ApplicationID string         `json:"application_id"`
	CandidateID   string         `json:"candidate_id"`
	Kind          string         `json:"kind"`
	FromStatus    string         `json:"from_status,omitempty"`
	ToStatus      string         `json:"to_status,omitempty"`
	Actor         string         `json:"actor"`
	Data          map[string]any `json:"data"`
}

func toEventResp(e applications.Event) eventResp {
	return eventResp{
		EventID:       e.EventID,
		OccurredAt:    e.OccurredAt,
		ApplicationID: e.ApplicationID,
		CandidateID:   e.CandidateID,
		Kind:          string(e.Kind),
		FromStatus:    string(e.FromStatus),
		ToStatus:      string(e.ToStatus),
		Actor:         string(e.Actor),
		Data:          e.Data,
	}
}

// ---- handlers ----

func createApplication(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := CandidateFromContext(r.Context())
		var req createApplicationReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "invalid JSON body")
			return
		}
		if req.MatchID == "" || req.OpportunityID == "" {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "match_id and opportunity_id required")
			return
		}
		appID := d.newID()
		eventID := d.newID()
		app, err := d.Store.Create(r.Context(), applications.Application{
			ApplicationID: appID,
			CandidateID:   cand,
			OpportunityID: req.OpportunityID,
			MatchID:       req.MatchID,
			Status:        applications.Status(req.Status),
			CurrentStage:  req.CurrentStage,
			Metadata:      req.Metadata,
			LastEventID:   eventID,
		})
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		_ = d.EventLog.Write(r.Context(), applications.Event{
			EventID:       eventID,
			OccurredAt:    d.now(),
			ApplicationID: app.ApplicationID,
			CandidateID:   cand,
			Kind:          applications.EventKindCreated,
			ToStatus:      app.Status,
			Actor:         applications.ActorExtension,
			Data:          map[string]any{"opportunity_id": app.OpportunityID, "match_id": app.MatchID},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toAppResp(app))
	}
}

func listApplications(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := CandidateFromContext(r.Context())
		statuses := []applications.Status{}
		if s := r.URL.Query().Get("status"); s != "" {
			for _, raw := range strings.Split(s, ",") {
				raw = strings.TrimSpace(raw)
				if raw != "" {
					statuses = append(statuses, applications.Status(raw))
				}
			}
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, err := d.Store.ListByCandidate(r.Context(), applications.ListByCandidateParams{
			CandidateID: cand,
			Statuses:    statuses,
			Cursor:      r.URL.Query().Get("cursor"),
			Limit:       limit,
		})
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		out := struct {
			Items      []applicationResp `json:"items"`
			NextCursor string            `json:"next_cursor,omitempty"`
			HasMore    bool              `json:"has_more"`
		}{HasMore: page.HasMore, NextCursor: page.NextCursor}
		out.Items = make([]applicationResp, 0, len(page.Items))
		for _, a := range page.Items {
			out.Items = append(out.Items, toAppResp(&a))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func getApplication(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := CandidateFromContext(r.Context())
		id := r.PathValue("id")
		app, err := d.Store.GetByID(r.Context(), id)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if app.CandidateID != cand {
			ProblemJSON(w, http.StatusNotFound, "not_found", "application not found")
			return
		}
		evts, _ := d.EventLog.ListByApplication(r.Context(), app.ApplicationID, 10)
		evtResps := make([]eventResp, 0, len(evts))
		for _, e := range evts {
			evtResps = append(evtResps, toEventResp(e))
		}
		out := struct {
			Application applicationResp `json:"application"`
			Events      []eventResp     `json:"events"`
		}{
			Application: toAppResp(app),
			Events:      evtResps,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

type patchApplicationReq struct {
	Status       *string        `json:"status,omitempty"`
	CurrentStage *string        `json:"current_stage,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

func patchApplication(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := CandidateFromContext(r.Context())
		id := r.PathValue("id")
		// scope check first to avoid leaking existence
		cur, err := d.Store.GetByID(r.Context(), id)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if cur.CandidateID != cand {
			ProblemJSON(w, http.StatusNotFound, "not_found", "application not found")
			return
		}
		var req patchApplicationReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "invalid JSON body")
			return
		}

		patch := applications.Patch{Metadata: req.Metadata, LastEventID: d.newID()}
		if req.Status != nil {
			s := applications.Status(*req.Status)
			patch.Status = &s
		}
		if req.CurrentStage != nil {
			patch.CurrentStage = req.CurrentStage
		}

		next, err := d.Store.ApplyTransition(r.Context(), id, patch)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		// Emit state_changed when status moved.
		if next.Status != cur.Status {
			_ = d.EventLog.Write(r.Context(), applications.Event{
				EventID:       patch.LastEventID,
				OccurredAt:    d.now(),
				ApplicationID: id,
				CandidateID:   cand,
				Kind:          applications.EventKindStateChanged,
				FromStatus:    cur.Status,
				ToStatus:      next.Status,
				Actor:         applications.ActorExtension,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAppResp(next))
	}
}

type appendEventReq struct {
	Kind  string         `json:"kind"`
	Data  map[string]any `json:"data,omitempty"`
	Actor string         `json:"actor,omitempty"`
}

func appendEvent(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := CandidateFromContext(r.Context())
		id := r.PathValue("id")
		cur, err := d.Store.GetByID(r.Context(), id)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if cur.CandidateID != cand {
			ProblemJSON(w, http.StatusNotFound, "not_found", "application not found")
			return
		}
		var req appendEventReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "invalid JSON body")
			return
		}
		if req.Kind == "" {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "kind required")
			return
		}
		actor := applications.Actor(req.Actor)
		if actor == "" {
			actor = applications.ActorExtension
		}
		evt := applications.Event{
			EventID:       d.newID(),
			OccurredAt:    d.now(),
			ApplicationID: id,
			CandidateID:   cand,
			Kind:          applications.EventKind(req.Kind),
			Actor:         actor,
			Data:          req.Data,
		}
		if err := d.EventLog.Write(r.Context(), evt); err != nil {
			ProblemFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toEventResp(evt))
	}
}

func listEvents(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := CandidateFromContext(r.Context())
		id := r.PathValue("id")
		cur, err := d.Store.GetByID(r.Context(), id)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if cur.CandidateID != cand {
			ProblemJSON(w, http.StatusNotFound, "not_found", "application not found")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		evts, err := d.EventLog.ListByApplication(r.Context(), id, limit)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		out := make([]eventResp, 0, len(evts))
		for _, e := range evts {
			out = append(out, toEventResp(e))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
	}
}

// dummy import guard so errors stays referenced in compiled file
var _ = errors.New
