package opportunity

import (
	"os"
	"path/filepath"
	"testing"
)

func writeYAML(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFromDir_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "job.yaml", `
kind: job
display_name: Job
issuing_entity_label: Company
url_prefix: jobs
universal_required: [title, description, issuing_entity, apply_url, anchor_country]
kind_required: [employment_type]
categories: [Programming, Design]
`)
	writeYAML(t, dir, "scholarship.yaml", `
kind: scholarship
display_name: Scholarship
issuing_entity_label: Institution
url_prefix: scholarships
universal_required: [title, description, issuing_entity]
kind_required: [deadline, field_of_study]
categories: [STEM, Arts]
`)

	reg, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	if got := len(reg.Known()); got != 2 {
		t.Errorf("Known()=%d, want 2", got)
	}
	job, ok := reg.Lookup("job")
	if !ok || job.URLPrefix != "jobs" {
		t.Errorf("Lookup(job) = %+v, %v", job, ok)
	}
}

func TestLoadFromDir_DuplicateKindRejected(t *testing.T) {
	dir := t.TempDir()
	body := `
kind: job
display_name: Job
issuing_entity_label: Company
url_prefix: jobs
`
	writeYAML(t, dir, "a.yaml", body)
	writeYAML(t, dir, "b.yaml", body)
	if _, err := LoadFromDir(dir); err == nil {
		t.Fatal("expected duplicate-kind error")
	}
}

func TestLoadFromDir_DuplicateURLPrefixRejected(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", `
kind: job
display_name: Job
issuing_entity_label: Company
url_prefix: jobs
`)
	writeYAML(t, dir, "b.yaml", `
kind: opening
display_name: Opening
issuing_entity_label: Company
url_prefix: jobs
`)
	if _, err := LoadFromDir(dir); err == nil {
		t.Fatal("expected duplicate-url_prefix error")
	}
}

func TestLoadFromDir_InvalidYAMLRejected(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "broken.yaml", "kind: job\nurl_prefix: [")
	if _, err := LoadFromDir(dir); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestRegistry_ResolveFallback(t *testing.T) {
	reg := &Registry{specs: map[string]Spec{}}
	got := reg.Resolve("unknown")
	if got.Kind != "unknown" {
		t.Errorf("Resolve fallback Kind=%q, want %q", got.Kind, "unknown")
	}
	if got.URLPrefix != "unknown" {
		t.Errorf("Resolve fallback URLPrefix=%q, want %q", got.URLPrefix, "unknown")
	}
}
