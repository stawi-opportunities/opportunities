package authmanifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/authmanifest"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const brighterYAML = `
source_type: brightermonday
auth_method: extension
display_name: BrighterMonday
login_url: https://www.brightermonday.co.ke/login
cookie_domains:
  - .brightermonday.co.ke
required_cookies:
  - laravel_session
session_ttl: 720h
apply_flow:
  type: http_form
  form_url_pattern: '^https://www\.brightermonday\.co\.ke/job/.+/apply$'
  fields:
    cv_file:
      name: resume
      source: cv_bytes
instructions_md: |
  ### Connect BrighterMonday
  1. Sign in to BrighterMonday
`

func TestLoadFromDir_Happy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "brightermonday.yaml", brighterYAML)
	store, err := authmanifest.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, ok := store.Lookup(domain.SourceBrighterMonday)
	if !ok {
		t.Fatal("manifest not found after load")
	}
	if got.DisplayName != "BrighterMonday" {
		t.Fatalf("display_name = %q", got.DisplayName)
	}
	if got.SessionTTL.Hours() != 720 {
		t.Fatalf("session_ttl: got %v, want 720h", got.SessionTTL)
	}
	if !got.MatchesApplyURL("https://www.brightermonday.co.ke/job/12345/apply") {
		t.Fatal("apply URL pattern not compiled")
	}
}

func TestLoadFromDir_MissingDir(t *testing.T) {
	store, err := authmanifest.LoadFromDir("/nonexistent/path")
	if err != nil {
		t.Fatalf("missing dir should not error, got %v", err)
	}
	if len(store.Known()) != 0 {
		t.Fatal("expected empty store")
	}
}

func TestLoadFromDir_DuplicateSourceTypeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", brighterYAML)
	writeFile(t, dir, "b.yaml", brighterYAML)
	if _, err := authmanifest.LoadFromDir(dir); err == nil {
		t.Fatal("expected duplicate source_type to error")
	}
}

func TestLoadFromDir_InvalidYAMLRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.yaml", "source_type: brightermonday\nauth_method: [unterminated")
	if _, err := authmanifest.LoadFromDir(dir); err == nil {
		t.Fatal("expected yaml parse error")
	}
}

func TestLoadFromDir_FailsValidation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.yaml", `
source_type: brightermonday
auth_method: extension
`)
	if _, err := authmanifest.LoadFromDir(dir); err == nil {
		t.Fatal("expected validation error (missing login_url + cookie_domains)")
	}
}

func TestLoadFromDir_SkipsNonYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "brightermonday.yaml", brighterYAML)
	writeFile(t, dir, "README.md", "this should be ignored")
	writeFile(t, dir, "extra.txt", "ignored too")
	store, err := authmanifest.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(store.Known()) != 1 {
		t.Fatalf("expected 1 manifest, got %d", len(store.Known()))
	}
}

func TestStore_All_DeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "brightermonday.yaml", brighterYAML)
	writeFile(t, dir, "jobberman.yaml", `
source_type: jobberman
auth_method: extension
display_name: Jobberman
login_url: https://www.jobberman.com/login
cookie_domains:
  - .jobberman.com
apply_flow:
  type: http_form
  form_url_pattern: '^https://www\.jobberman\.com/job/.+/apply$'
  fields:
    cv:
      name: resume
      source: cv_bytes
`)
	store, err := authmanifest.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	all := store.All()
	if len(all) != 2 {
		t.Fatalf("got %d manifests, want 2", len(all))
	}
	if all[0].SourceType != domain.SourceBrighterMonday || all[1].SourceType != domain.SourceJobberman {
		t.Fatalf("ordering: got %v, %v", all[0].SourceType, all[1].SourceType)
	}
}

func TestStore_ExtensionView_HasNoInstructions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.yaml", brighterYAML)
	store, err := authmanifest.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	views := store.ExtensionView()
	if len(views) != 1 {
		t.Fatalf("got %d views", len(views))
	}
	if views[0].LoginURL == "" {
		t.Fatal("login_url missing from ExtensionView")
	}
}
