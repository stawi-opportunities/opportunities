package v1_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	httpv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/authmanifest"
	"github.com/stawi-opportunities/opportunities/pkg/authsession"
	"github.com/stawi-opportunities/opportunities/pkg/authz/stawitok"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// fakeRecorder is a no-database authsession.Store stand-in.
type fakeRecorder struct {
	mu       sync.Mutex
	captures []authsession.Capture
	revokes  []string // "<candidate>|<source>"
	recErr   error
	revErr   error
}

func (f *fakeRecorder) Record(_ context.Context, c authsession.Capture) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recErr != nil {
		return f.recErr
	}
	f.captures = append(f.captures, c)
	return nil
}
func (f *fakeRecorder) Revoke(_ context.Context, cid string, st domain.SourceType) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revErr != nil {
		return f.revErr
	}
	f.revokes = append(f.revokes, cid+"|"+string(st))
	return nil
}

type fakeLister struct {
	rows []*domain.CandidateSession
	err  error
}

func (f *fakeLister) ListForCandidate(_ context.Context, _ string) ([]*domain.CandidateSession, error) {
	return f.rows, f.err
}

func newSessionDeps(t *testing.T) (httpv1.SessionDeps, *stawitok.Issuer, *fakeRecorder, *fakeLister, *fakeManifestStore) {
	t.Helper()
	kv := newPairKV()
	key := make([]byte, stawitok.SigningKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	is, err := stawitok.NewIssuer(kv, key)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	recorder := &fakeRecorder{}
	lister := &fakeLister{}
	manifests := newFakeStore(brightermondayManifest(t))
	return httpv1.SessionDeps{
		Svc:    nil, // emit is best-effort; nil svc short-circuits
		Issuer: is,
		Resolver: &fakeResolver{byProfile: map[string]*domain.CandidateProfile{
			"profile-1": {BaseModel: domain.BaseModel{ID: "cnd_1"}, ProfileID: "profile-1"},
		}},
		Recorder:  recorder,
		Lister:    lister,
		Manifests: manifests,
	}, is, recorder, lister, manifests
}

func TestSessionCapture_Happy(t *testing.T) {
	deps, is, recorder, _, _ := newSessionDeps(t)
	access, err := is.Mint(context.Background(), "cnd_1", stawitok.KindAccess, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	body := strings.NewReader(`{
        "user_agent":"Mozilla/5.0",
        "cookies":[
            {"name":"laravel_session","value":"abc","domain":".brightermonday.co.ke"},
            {"name":"XSRF-TOKEN","value":"xyz","domain":".brightermonday.co.ke"}
        ],
        "headers":{"Accept-Language":"en"}
    }`)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /candidates/me/sessions/{source_type}", httpv1.SessionCaptureHandler(deps))

	req := httptest.NewRequest(http.MethodPost, "/candidates/me/sessions/brightermonday", body)
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.captures) != 1 {
		t.Fatalf("captures = %d, want 1", len(recorder.captures))
	}
	if recorder.captures[0].CandidateID != "cnd_1" {
		t.Fatalf("candidate_id = %q", recorder.captures[0].CandidateID)
	}
	if recorder.captures[0].SourceType != domain.SourceBrighterMonday {
		t.Fatalf("source_type = %q", recorder.captures[0].SourceType)
	}
}

func TestSessionCapture_NoToken(t *testing.T) {
	deps, _, _, _, _ := newSessionDeps(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /candidates/me/sessions/{source_type}", httpv1.SessionCaptureHandler(deps))
	req := httptest.NewRequest(http.MethodPost, "/candidates/me/sessions/brightermonday", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestSessionCapture_RefreshTokenRejected(t *testing.T) {
	deps, is, _, _, _ := newSessionDeps(t)
	refresh, _ := is.Mint(context.Background(), "cnd_1", stawitok.KindRefresh, time.Hour)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /candidates/me/sessions/{source_type}", httpv1.SessionCaptureHandler(deps))
	req := httptest.NewRequest(http.MethodPost, "/candidates/me/sessions/brightermonday",
		strings.NewReader(`{"cookies":[{"name":"x","value":"y"}]}`))
	req.Header.Set("Authorization", "Bearer "+refresh)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestSessionCapture_UnknownSource(t *testing.T) {
	deps, is, _, _, _ := newSessionDeps(t)
	access, _ := is.Mint(context.Background(), "cnd_1", stawitok.KindAccess, time.Hour)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /candidates/me/sessions/{source_type}", httpv1.SessionCaptureHandler(deps))
	req := httptest.NewRequest(http.MethodPost, "/candidates/me/sessions/unknown",
		strings.NewReader(`{"cookies":[{"name":"x","value":"y"}]}`))
	req.Header.Set("Authorization", "Bearer "+access)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestSessionCapture_MissingRequiredCookie(t *testing.T) {
	deps, is, _, _, _ := newSessionDeps(t)
	// Replace the manifest with one that has required_cookies set.
	deps.Manifests = newFakeStore(&authmanifest.Manifest{
		SourceType:      domain.SourceBrighterMonday,
		AuthMethod:      authmanifest.AuthExtension,
		DisplayName:     "BrighterMonday",
		LoginURL:        "https://www.brightermonday.co.ke/login",
		CookieDomains:   []string{".brightermonday.co.ke"},
		RequiredCookies: []string{"laravel_session", "XSRF-TOKEN"},
		ApplyFlow: authmanifest.ApplyFlow{
			Type:           authmanifest.ApplyHTTPForm,
			FormURLPattern: `^https://www\.brightermonday\.co\.ke/job/.+/apply$`,
			Fields:         map[string]authmanifest.FieldMap{"cv": {Name: "n", Source: "cv_bytes"}},
		},
	})
	// Re-validate so MatchesApplyURL still works for other tests that
	// share the fakeManifestStore — but fakeManifestStore just holds
	// the pointer, so explicit Validate is enough.
	for _, m := range deps.Manifests.(*fakeManifestStore).manifests {
		if err := m.Validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
	}

	access, _ := is.Mint(context.Background(), "cnd_1", stawitok.KindAccess, time.Hour)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /candidates/me/sessions/{source_type}", httpv1.SessionCaptureHandler(deps))
	body := strings.NewReader(`{"cookies":[{"name":"laravel_session","value":"abc"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/candidates/me/sessions/brightermonday", body)
	req.Header.Set("Authorization", "Bearer "+access)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "XSRF-TOKEN") {
		t.Fatalf("error should name the missing cookie: %s", rr.Body.String())
	}
}

func TestSessionList_Happy(t *testing.T) {
	deps, _, _, lister, _ := newSessionDeps(t)
	cap := time.Now().UTC().Add(-time.Hour)
	exp := time.Now().UTC().Add(24 * time.Hour)
	lister.rows = []*domain.CandidateSession{
		{
			BaseModel:     domain.BaseModel{ID: "s1"},
			CandidateID:   "cnd_1",
			SourceType:    domain.SourceBrighterMonday,
			CapturedAt:    cap,
			ExpiresAt:     &exp,
			UserAgent:     "Mozilla/5.0",
			CaptureOrigin: domain.SessionOriginExtension,
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /candidates/me/sessions", httpv1.SessionListHandler(deps))
	req := httptest.NewRequest(http.MethodGet, "/candidates/me/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+stubJWT(t, "profile-1"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Sessions []struct {
			SourceType string `json:"source_type"`
			Status     string `json:"status"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sessions) != 1 {
		t.Fatalf("got %d sessions", len(body.Sessions))
	}
	if body.Sessions[0].Status != "connected" {
		t.Fatalf("status = %q", body.Sessions[0].Status)
	}
}

func TestSessionList_ExpiredLabel(t *testing.T) {
	deps, _, _, lister, _ := newSessionDeps(t)
	past := time.Now().UTC().Add(-time.Hour)
	lister.rows = []*domain.CandidateSession{
		{
			BaseModel:   domain.BaseModel{ID: "s1"},
			CandidateID: "cnd_1",
			SourceType:  domain.SourceBrighterMonday,
			CapturedAt:  past.Add(-time.Hour),
			ExpiresAt:   &past,
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /candidates/me/sessions", httpv1.SessionListHandler(deps))
	req := httptest.NewRequest(http.MethodGet, "/candidates/me/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+stubJWT(t, "profile-1"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"status":"expired"`) {
		t.Fatalf("expected expired status: %s", rr.Body.String())
	}
}

func TestSessionRevoke(t *testing.T) {
	deps, _, recorder, _, _ := newSessionDeps(t)
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /candidates/me/sessions/{source_type}", httpv1.SessionRevokeHandler(deps))
	req := httptest.NewRequest(http.MethodDelete, "/candidates/me/sessions/brightermonday", nil)
	req.Header.Set("Authorization", "Bearer "+stubJWT(t, "profile-1"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if got := recorder.revokes; len(got) != 1 || got[0] != "cnd_1|brightermonday" {
		t.Fatalf("revokes = %v", got)
	}
}
