package publish_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/publish"
)

func TestCachePurger_PurgeURL_PostsExpectedRequest(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	p := publish.NewCachePurger("zone123", "token-xyz", srv.URL)
	if err := p.PurgeURL(context.Background(), "https://job-repo.stawi.org/jobs/abc.json"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if !strings.Contains(gotPath, "zone123/purge_cache") {
		t.Errorf("path = %q, want .../zone123/purge_cache", gotPath)
	}
	if gotAuth != "Bearer token-xyz" {
		t.Errorf("auth header = %q", gotAuth)
	}
	files := gotBody["files"]
	if len(files) != 1 || files[0] != "https://job-repo.stawi.org/jobs/abc.json" {
		t.Errorf("body.files = %v", files)
	}
}

func TestCachePurger_EmptyConfigIsNoOp(t *testing.T) {
	p := publish.NewCachePurger("", "", "")
	if err := p.PurgeURL(context.Background(), "https://x"); err != nil {
		t.Fatalf("no-op purger should not error, got %v", err)
	}
}

func TestCachePurger_NilReceiverIsNoOp(t *testing.T) {
	var p *publish.CachePurger
	if err := p.PurgeURL(context.Background(), "https://x"); err != nil {
		t.Fatalf("nil purger should not error, got %v", err)
	}
}

func TestCachePurger_Non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p := publish.NewCachePurger("z", "t", srv.URL)
	if err := p.PurgeURL(context.Background(), "https://x"); err == nil {
		t.Error("expected error on 403")
	}
}
