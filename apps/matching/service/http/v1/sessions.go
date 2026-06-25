package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/authmanifest"
	"github.com/stawi-opportunities/opportunities/pkg/authsession"
	"github.com/stawi-opportunities/opportunities/pkg/authz"
	"github.com/stawi-opportunities/opportunities/pkg/authz/stawitok"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// SessionRecorder is the slice of authsession.Store the upload handler
// needs. Declared locally so tests substitute a fake without
// instantiating the real Store + crypto.
type SessionRecorder interface {
	Record(ctx context.Context, c authsession.Capture) error
	Revoke(ctx context.Context, candidateID string, sourceType domain.SourceType) error
}

// SessionLister is the read-side companion. We deliberately use the
// repository row directly here instead of the Store's decrypted
// Session because the list endpoint never decrypts — it shows metadata
// (status / captured_at / expires_at) only.
type SessionLister interface {
	ListForCandidate(ctx context.Context, candidateID string) ([]*domain.CandidateSession, error)
}

// SessionDeps bundles the collaborators for the session handlers.
type SessionDeps struct {
	Svc       *frame.Service
	Issuer    *stawitok.Issuer
	Resolver  ProfileResolver
	Recorder  SessionRecorder
	Lister    SessionLister
	Manifests AuthManifestStore
}

// captureBody is the wire format for POST /candidates/me/sessions/:t.
//
// All fields are user-supplied. The handler enforces:
//   - source_type matches a known manifest with auth_method=extension
//   - len(cookies) is bounded (reject pathological uploads)
//   - required_cookies from the manifest are all present
type captureBody struct {
	CapturedAt time.Time             `json:"captured_at"`
	UserAgent  string                `json:"user_agent"`
	Cookies    []domain.SessionCookie `json:"cookies"`
	Headers    map[string]string     `json:"headers"`
	Storage    map[string]string     `json:"storage"`
}

const (
	maxCookies     = 100
	maxStorageKeys = 100
	maxBodyBytes   = 256 << 10 // 256 KiB cap; way more than any real session
)

// SessionCaptureHandler returns the handler for
// POST /candidates/me/sessions/{source_type}.
//
// Auth: Stawi access token (extension).
func SessionCaptureHandler(deps SessionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)

		info, err := verifyAccessToken(ctx, deps.Issuer, r)
		if err != nil {
			writePairingError(w, http.StatusUnauthorized, "unauthorized", "invalid access token")
			return
		}

		st, manifest, ok := lookupExtensionManifest(deps.Manifests, r)
		if !ok {
			writePairingError(w, http.StatusNotFound, "unknown_source", "source_type not in manifest")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		var body captureBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writePairingError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
			return
		}
		if len(body.Cookies) == 0 {
			writePairingError(w, http.StatusBadRequest, "bad_request", "at least one cookie is required")
			return
		}
		if len(body.Cookies) > maxCookies || len(body.Storage) > maxStorageKeys {
			writePairingError(w, http.StatusRequestEntityTooLarge, "too_large", "too many cookies or storage entries")
			return
		}
		if err := checkRequiredCookies(manifest, body.Cookies); err != nil {
			writePairingError(w, http.StatusBadRequest, "missing_cookies", err.Error())
			return
		}

		captured := body.CapturedAt
		if captured.IsZero() {
			captured = time.Now().UTC()
		}
		payload := domain.SessionPayload{
			Cookies: body.Cookies,
			Headers: body.Headers,
			Storage: body.Storage,
		}
		exp := authsession.InferExpiry(payload, captured, manifest.SessionTTL)

		cap := authsession.Capture{
			CandidateID:   info.CandidateID,
			SourceType:    st,
			Payload:       payload,
			CapturedAt:    captured,
			ExpiresAt:     exp,
			UserAgent:     body.UserAgent,
			CaptureOrigin: domain.SessionOriginExtension,
		}
		if err := deps.Recorder.Record(ctx, cap); err != nil {
			log.WithError(err).Error("sessions: record failed")
			writePairingError(w, http.StatusInternalServerError, "internal", "record failed")
			return
		}

		emitSessionCaptured(ctx, deps.Svc, cap)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidate_id": info.CandidateID,
			"source_type":  st,
			"captured_at":  captured,
			"expires_at":   exp,
			"accepted":     true,
		})
	}
}

// sessionStatus is the per-source row returned by the list endpoint.
// Never includes the encrypted payload — only metadata the UI needs to
// render the Connected Accounts card.
type sessionStatus struct {
	SourceType    domain.SourceType `json:"source_type"`
	Status        string            `json:"status"` // connected | expired | revoked
	CapturedAt    time.Time         `json:"captured_at"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
	LastUsedAt    *time.Time        `json:"last_used_at,omitempty"`
	UserAgent     string            `json:"user_agent,omitempty"`
	CaptureOrigin string            `json:"capture_origin,omitempty"`
}

// SessionListHandler returns the handler for GET /candidates/me/sessions.
//
// Auth: gateway-verified JWT (web UI).
func SessionListHandler(deps SessionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		profileID := authz.ProfileIDFromJWT(r)
		if profileID == "" {
			writePairingError(w, http.StatusUnauthorized, "unauthorized", "missing Bearer token")
			return
		}
		cand, err := deps.Resolver.GetByProfileID(ctx, profileID)
		if err != nil {
			log.WithError(err).Warn("sessions: resolver failed")
			writePairingError(w, http.StatusInternalServerError, "internal", "resolver failed")
			return
		}
		if cand == nil {
			writePairingError(w, http.StatusForbidden, "no_candidate", "no candidate profile for this user")
			return
		}
		rows, err := deps.Lister.ListForCandidate(ctx, cand.ID)
		if err != nil {
			log.WithError(err).Warn("sessions: list failed")
			writePairingError(w, http.StatusInternalServerError, "internal", "list failed")
			return
		}
		now := time.Now().UTC()
		out := make([]sessionStatus, 0, len(rows))
		for _, r := range rows {
			out = append(out, sessionStatus{
				SourceType:    r.SourceType,
				Status:        sessionStatusLabel(r, now),
				CapturedAt:    r.CapturedAt,
				ExpiresAt:     r.ExpiresAt,
				LastUsedAt:    r.LastUsedAt,
				UserAgent:     r.UserAgent,
				CaptureOrigin: r.CaptureOrigin,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sessions": out})
	}
}

// SessionRevokeHandler returns the handler for
// DELETE /candidates/me/sessions/{source_type}.
//
// Auth: gateway-verified JWT (web UI).
func SessionRevokeHandler(deps SessionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		profileID := authz.ProfileIDFromJWT(r)
		if profileID == "" {
			writePairingError(w, http.StatusUnauthorized, "unauthorized", "missing Bearer token")
			return
		}
		cand, err := deps.Resolver.GetByProfileID(ctx, profileID)
		if err != nil {
			log.WithError(err).Warn("sessions: resolver failed")
			writePairingError(w, http.StatusInternalServerError, "internal", "resolver failed")
			return
		}
		if cand == nil {
			writePairingError(w, http.StatusForbidden, "no_candidate", "no candidate profile for this user")
			return
		}
		st := strings.TrimSpace(r.PathValue("source_type"))
		if st == "" {
			writePairingError(w, http.StatusBadRequest, "bad_request", "source_type required")
			return
		}
		if err := deps.Recorder.Revoke(ctx, cand.ID, domain.SourceType(st)); err != nil {
			log.WithError(err).Warn("sessions: revoke failed")
			writePairingError(w, http.StatusInternalServerError, "internal", "revoke failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"revoked": true, "source_type": st})
	}
}

// ── internals ───────────────────────────────────────────────────────

func verifyAccessToken(ctx context.Context, issuer *stawitok.Issuer, r *http.Request) (*stawitok.Info, error) {
	tok := bearerToken(r)
	if tok == "" {
		return nil, errors.New("no token")
	}
	info, err := issuer.Verify(ctx, tok)
	if err != nil {
		return nil, err
	}
	if info.Kind != stawitok.KindAccess {
		return nil, errors.New("not an access token")
	}
	return info, nil
}

func lookupExtensionManifest(store AuthManifestStore, r *http.Request) (domain.SourceType, *authmanifest.Manifest, bool) {
	raw := strings.TrimSpace(r.PathValue("source_type"))
	if raw == "" {
		return "", nil, false
	}
	st := domain.SourceType(raw)
	m, ok := store.Lookup(st)
	if !ok {
		return st, nil, false
	}
	if m.AuthMethod != authmanifest.AuthExtension {
		return st, nil, false
	}
	return st, m, true
}

func checkRequiredCookies(m *authmanifest.Manifest, got []domain.SessionCookie) error {
	if len(m.RequiredCookies) == 0 {
		return nil
	}
	have := map[string]struct{}{}
	for _, c := range got {
		have[c.Name] = struct{}{}
	}
	missing := []string{}
	for _, req := range m.RequiredCookies {
		if _, ok := have[req]; !ok {
			missing = append(missing, req)
		}
	}
	if len(missing) > 0 {
		return errors.New("missing required cookies: " + strings.Join(missing, ", "))
	}
	return nil
}

func sessionStatusLabel(r *domain.CandidateSession, now time.Time) string {
	if r.RevokedAt != nil {
		return "revoked"
	}
	if r.ExpiresAt != nil && !now.Before(*r.ExpiresAt) {
		return "expired"
	}
	return "connected"
}

func emitSessionCaptured(ctx context.Context, svc *frame.Service, c authsession.Capture) {
	if svc == nil || svc.EventsManager() == nil {
		return
	}
	payload := eventsv1.SessionCapturedV1{
		CandidateID:   c.CandidateID,
		SourceType:    string(c.SourceType),
		CapturedAt:    c.CapturedAt,
		UserAgent:     c.UserAgent,
		CaptureOrigin: c.CaptureOrigin,
	}
	if c.ExpiresAt != nil {
		payload.ExpiresAt = *c.ExpiresAt
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicSessionCaptured, payload)
	// Detach from the request ctx so a flush after we've already
	// persisted doesn't get cancelled by a client hang-up.
	emitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := svc.EventsManager().Emit(emitCtx, eventsv1.TopicSessionCaptured, env); err != nil {
		util.Log(ctx).WithError(err).Warn("sessions: emit SessionCapturedV1 failed")
	}
}
