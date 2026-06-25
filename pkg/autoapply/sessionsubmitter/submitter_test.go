package sessionsubmitter_test

import (
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/authmanifest"
	"github.com/stawi-opportunities/opportunities/pkg/authsession"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/sessionsubmitter"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

type manifestStub struct {
	m *authmanifest.Manifest
}

func (s *manifestStub) Lookup(st domain.SourceType) (*authmanifest.Manifest, bool) {
	if s.m == nil || s.m.SourceType != st {
		return nil, false
	}
	return s.m, true
}

type sessionStub struct {
	mu       sync.Mutex
	session  *authsession.Session
	err      error
	revoked  bool
	revokeOK bool
}

func (s *sessionStub) Session(_ context.Context, _ string, _ domain.SourceType) (*authsession.Session, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.session, nil
}
func (s *sessionStub) Record(_ context.Context, _ authsession.Capture) error { return nil }
func (s *sessionStub) Revoke(_ context.Context, _ string, _ domain.SourceType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked = true
	if !s.revokeOK {
		return errors.New("revoke disabled in test")
	}
	return nil
}

func brighterManifest(t *testing.T, applyPattern string) *authmanifest.Manifest {
	t.Helper()
	m := &authmanifest.Manifest{
		SourceType:    domain.SourceBrighterMonday,
		AuthMethod:    authmanifest.AuthExtension,
		DisplayName:   "BrighterMonday",
		LoginURL:      "https://www.brightermonday.co.ke/login",
		CookieDomains: []string{".brightermonday.co.ke"},
		ApplyFlow: authmanifest.ApplyFlow{
			Type:           authmanifest.ApplyHTTPForm,
			FormURLPattern: applyPattern,
			Fields: map[string]authmanifest.FieldMap{
				"cv":   {Name: "resume", Source: "cv_bytes"},
				"name": {Name: "full_name", Source: "full_name"},
			},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest validate: %v", err)
	}
	return m
}

func sampleSession() *authsession.Session {
	return &authsession.Session{
		CandidateID: "cnd_1",
		SourceType:  domain.SourceBrighterMonday,
		CapturedAt:  time.Now(),
		Payload: domain.SessionPayload{
			Cookies: []domain.SessionCookie{
				{Name: "laravel_session", Value: "abc", Domain: ".brightermonday.co.ke", Path: "/"},
			},
			Headers: map[string]string{
				"User-Agent":      "Mozilla/5.0",
				"Accept-Language": "en-US",
			},
		},
	}
}

func TestCanHandle(t *testing.T) {
	m := brighterManifest(t, `^https://www\.brightermonday\.co\.ke/job/.+/apply$`)
	sub := sessionsubmitter.New(sessionsubmitter.Config{
		Manifests: &manifestStub{m: m},
		Sessions:  &sessionStub{},
	})
	if !sub.CanHandle(domain.SourceBrighterMonday, "https://www.brightermonday.co.ke/job/123/apply") {
		t.Fatal("CanHandle should be true for matching URL")
	}
	if sub.CanHandle(domain.SourceBrighterMonday, "https://www.brightermonday.co.ke/login") {
		t.Fatal("CanHandle should be false for non-apply URL")
	}
	if sub.CanHandle("greenhouse", "https://www.brightermonday.co.ke/job/123/apply") {
		t.Fatal("CanHandle should be false for unknown source")
	}
}

func TestSubmit_SessionRequired(t *testing.T) {
	m := brighterManifest(t, `^https://example.test/apply$`)
	sub := sessionsubmitter.New(sessionsubmitter.Config{
		Manifests: &manifestStub{m: m},
		Sessions:  &sessionStub{err: authsession.ErrSessionRequired},
	})
	res, err := sub.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceBrighterMonday,
		ApplyURL:   "https://example.test/apply",
	})
	if err != nil {
		t.Fatalf("submit err = %v", err)
	}
	if res.Method != "skipped" || res.SkipReason != sessionsubmitter.ReasonSessionRequired {
		t.Fatalf("got %+v", res)
	}
}

func TestSubmit_HappyMultipart(t *testing.T) {
	// Stub server that pretends to be BrighterMonday's apply endpoint.
	var (
		gotMethod string
		gotFields = map[string]string{}
		gotFile   []byte
		gotName   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><form action="/apply/submit"></form></body></html>`))
			return
		}
		gotMethod = r.Method
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				gotFields[k] = v[0]
			}
		}
		for _, fhdrs := range r.MultipartForm.File {
			fh := fhdrs[0]
			f, _ := fh.Open()
			defer func() { _ = f.Close() }()
			buf := make([]byte, fh.Size)
			_, _ = f.Read(buf)
			gotFile = buf
			gotName = fh.Filename
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	pattern := "^" + strings.ReplaceAll(srv.URL, ".", `\.`) + "/job/123/apply$"
	m := brighterManifest(t, pattern)
	sess := sampleSession()
	sub := sessionsubmitter.New(sessionsubmitter.Config{
		Manifests: &manifestStub{m: m},
		Sessions:  &sessionStub{session: sess},
	})

	res, err := sub.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceBrighterMonday,
		ApplyURL:   srv.URL + "/job/123/apply",
		FullName:   "Ada Lovelace",
		CVBytes:    []byte("PDF-1.4\n…"),
		CVFilename: "ada.pdf",
	})
	if err != nil {
		t.Fatalf("submit err = %v", err)
	}
	if res.Method != "session_replay" {
		t.Fatalf("got %+v", res)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST to apply, got %q", gotMethod)
	}
	if gotFields["full_name"] != "Ada Lovelace" {
		t.Fatalf("full_name not posted: %+v", gotFields)
	}
	if string(gotFile) != "PDF-1.4\n…" {
		t.Fatalf("cv bytes mismatch: %q", string(gotFile))
	}
	if gotName != "ada.pdf" {
		t.Fatalf("cv filename = %q", gotName)
	}
}

func TestSubmit_CaptchaResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><form></form></body></html>`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body><div class="g-recaptcha"></div></body></html>`))
	}))
	defer srv.Close()

	pattern := "^" + strings.ReplaceAll(srv.URL, ".", `\.`) + "/job/.+/apply$"
	m := brighterManifest(t, pattern)
	sub := sessionsubmitter.New(sessionsubmitter.Config{
		Manifests: &manifestStub{m: m},
		Sessions:  &sessionStub{session: sampleSession()},
	})
	res, err := sub.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceBrighterMonday,
		ApplyURL:   srv.URL + "/job/1/apply",
	})
	if err != nil {
		t.Fatalf("submit err = %v", err)
	}
	if res.Method != "skipped" || res.SkipReason != sessionsubmitter.ReasonCaptcha {
		t.Fatalf("got %+v", res)
	}
}

func TestSubmit_DetectsLoggedOutOnGetAndRevokes(t *testing.T) {
	loginURL := "https://example.test/login"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", loginURL)
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	pattern := "^" + strings.ReplaceAll(srv.URL, ".", `\.`) + "/job/.+/apply$"
	m := brighterManifest(t, pattern)
	m.LoginURL = loginURL
	stub := &sessionStub{session: sampleSession(), revokeOK: true}
	sub := sessionsubmitter.New(sessionsubmitter.Config{
		Manifests: &manifestStub{m: m},
		Sessions:  stub,
	})
	res, err := sub.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceBrighterMonday,
		ApplyURL:   srv.URL + "/job/1/apply",
	})
	if err != nil {
		t.Fatalf("submit err = %v", err)
	}
	if res.SkipReason != sessionsubmitter.ReasonSessionExpired {
		t.Fatalf("got %+v", res)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if !stub.revoked {
		t.Fatal("expected sessions.Revoke to be called")
	}
}

func TestBuildMultipartIsParseable(t *testing.T) {
	// Indirect smoke: send via the happy path and confirm the
	// multipart encoded by the submitter round-trips through
	// mime/multipart on the server side without any quirks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<form></form>`))
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("multipart reader: %v", err)
		}
		for {
			_, err := mr.NextPart()
			if err == nil {
				continue
			}
			break
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	pattern := "^" + strings.ReplaceAll(srv.URL, ".", `\.`) + "/job/.+/apply$"
	m := brighterManifest(t, pattern)
	sub := sessionsubmitter.New(sessionsubmitter.Config{
		Manifests: &manifestStub{m: m},
		Sessions:  &sessionStub{session: sampleSession()},
	})
	res, err := sub.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceBrighterMonday,
		ApplyURL:   srv.URL + "/job/123/apply",
		FullName:   "Test",
		CVBytes:    []byte("CV"),
		CVFilename: "x.pdf",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.Method != "session_replay" {
		t.Fatalf("res = %+v", res)
	}
}

// Use the multipart package in a way that ensures we drag it into the
// test binary (silences unused-import warnings on dev environments
// where the import is added during iteration).
var _ = multipart.ErrMessageTooLarge
