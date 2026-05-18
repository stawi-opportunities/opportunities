package v1

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/authmanifest"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// AuthManifestStore is the slice of *authmanifest.Store the handlers
// need. Declaring the interface locally lets the handler tests use a
// trivial fake without depending on the YAML loader.
type AuthManifestStore interface {
	Lookup(sourceType domain.SourceType) (*authmanifest.Manifest, bool)
	ExtensionView() []authmanifest.ExtensionView
}

// AuthManifestListHandler returns an http.HandlerFunc implementing:
//
//	GET /sources/auth-manifest
//
// Returns the extension projection of every loaded manifest. This is
// what the browser extension polls at install time and on a slow
// rotation thereafter so it knows which domains to watch and which
// cookies to capture. The instructions_md field and apply_flow.fields
// map are deliberately omitted — they are UI / server-only concerns.
//
// Auth: Stawi access token (extension). The handler itself does no
// auth check — middleware in cmd/main.go is responsible for that.
func AuthManifestListHandler(store AuthManifestStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		views := store.ExtensionView()
		// Always emit an array, even when empty, so the extension can
		// rely on a stable response shape without a presence guard.
		if views == nil {
			views = []authmanifest.ExtensionView{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sources": views})
	}
}

// SourceAuthHandler returns an http.HandlerFunc implementing:
//
//	GET /sources/{source_type}/auth
//
// Returns the candidate-facing UIView for one source — display name,
// login URL, and instructions_md. Used by the "Connected Accounts" UI
// to render a per-source onboarding card.
//
// Auth: gateway-verified JWT (web UI). This handler does not need the
// candidate identity — the manifest content is the same for every
// candidate — but routing it behind the JWT path keeps the surface
// consistent and lets us instrument per-profile telemetry later.
func SourceAuthHandler(store AuthManifestStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		raw := strings.TrimSpace(r.PathValue("source_type"))
		if raw == "" {
			http.Error(w, `{"error":"source_type is required"}`, http.StatusBadRequest)
			return
		}
		m, ok := store.Lookup(domain.SourceType(raw))
		if !ok {
			http.Error(w, `{"error":"unknown source_type"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(m.UIView())
	}
}
