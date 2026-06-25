package v1_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/authmanifest"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

type fakeManifestStore struct {
	manifests map[domain.SourceType]*authmanifest.Manifest
}

func newFakeStore(ms ...*authmanifest.Manifest) *fakeManifestStore {
	out := &fakeManifestStore{manifests: map[domain.SourceType]*authmanifest.Manifest{}}
	for _, m := range ms {
		out.manifests[m.SourceType] = m
	}
	return out
}

func (f *fakeManifestStore) Lookup(s domain.SourceType) (*authmanifest.Manifest, bool) {
	m, ok := f.manifests[s]
	return m, ok
}

func (f *fakeManifestStore) ExtensionView() []authmanifest.ExtensionView {
	out := []authmanifest.ExtensionView{}
	for _, m := range f.manifests {
		out = append(out, m.ExtensionView())
	}
	return out
}

func brightermondayManifest(t *testing.T) *authmanifest.Manifest {
	t.Helper()
	m := &authmanifest.Manifest{
		SourceType:    domain.SourceBrighterMonday,
		AuthMethod:    authmanifest.AuthExtension,
		DisplayName:   "BrighterMonday",
		LoginURL:      "https://www.brightermonday.co.ke/login",
		CookieDomains: []string{".brightermonday.co.ke"},
		ApplyFlow: authmanifest.ApplyFlow{
			Type:           authmanifest.ApplyHTTPForm,
			FormURLPattern: `^https://www\.brightermonday\.co\.ke/job/.+/apply$`,
			Fields: map[string]authmanifest.FieldMap{
				"cv": {Name: "resume", Source: "cv_bytes"},
			},
		},
		InstructionsMD: "### Connect BrighterMonday\n1. Sign in",
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return m
}

func TestAuthManifestListHandler_ReturnsExtensionView(t *testing.T) {
	store := newFakeStore(brightermondayManifest(t))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sources/auth-manifest", nil)
	httpv1.AuthManifestListHandler(store).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Sources []authmanifest.ExtensionView `json:"sources"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(resp.Sources))
	}
	if resp.Sources[0].SourceType != domain.SourceBrighterMonday {
		t.Fatalf("source_type = %q", resp.Sources[0].SourceType)
	}
	// instructions_md must not appear in the extension projection.
	if strings.Contains(rr.Body.String(), "instructions_md") {
		t.Fatalf("ExtensionView leaked instructions_md: %s", rr.Body.String())
	}
}

func TestAuthManifestListHandler_EmptyStore(t *testing.T) {
	store := newFakeStore()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sources/auth-manifest", nil)
	httpv1.AuthManifestListHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"sources":[]`) {
		t.Fatalf("empty store should emit empty array, got %s", rr.Body.String())
	}
}

func TestAuthManifestListHandler_WrongMethod(t *testing.T) {
	store := newFakeStore()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sources/auth-manifest", nil)
	httpv1.AuthManifestListHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

func TestSourceAuthHandler_ReturnsUIView(t *testing.T) {
	store := newFakeStore(brightermondayManifest(t))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /sources/{source_type}/auth", httpv1.SourceAuthHandler(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sources/brightermonday/auth", nil)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var got authmanifest.UIView
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SourceType != domain.SourceBrighterMonday {
		t.Fatalf("source_type = %q", got.SourceType)
	}
	if !strings.Contains(got.InstructionsMD, "Connect BrighterMonday") {
		t.Fatalf("instructions_md missing: %q", got.InstructionsMD)
	}
}

func TestSourceAuthHandler_Unknown(t *testing.T) {
	store := newFakeStore(brightermondayManifest(t))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sources/{source_type}/auth", httpv1.SourceAuthHandler(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sources/nonsuch/auth", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestSourceAuthHandler_MissingPathParam(t *testing.T) {
	store := newFakeStore(brightermondayManifest(t))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sources/{source_type}/auth", httpv1.SourceAuthHandler(store))

	rr := httptest.NewRecorder()
	// Trailing slash with empty {source_type} segment — go 1.22 mux
	// treats this as a no-route, so we hit the default 404. That's
	// fine — the path-param-empty branch is reachable via direct
	// handler invocation, not via the mux.
	req := httptest.NewRequest(http.MethodGet, "/sources//auth", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("expected non-200, got %d", rr.Code)
	}
}
