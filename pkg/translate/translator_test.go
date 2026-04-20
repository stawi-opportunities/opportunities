package translate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"trim and lower", []string{" EN ", "Fr", "DE"}, []string{"en", "fr", "de"}},
		{"dedupe", []string{"en", "EN", "en"}, []string{"en"}},
		{"drop empties", []string{"en", "", " ", "es"}, []string{"en", "es"}},
		{"nil input", nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Normalize(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLanguageName_known(t *testing.T) {
	if n := languageName("fr"); n != "French" {
		t.Errorf("languageName(fr) = %q, want French", n)
	}
	if n := languageName("zh"); n != "Chinese (Simplified)" {
		t.Errorf("languageName(zh) = %q", n)
	}
}

func TestLanguageName_unknown(t *testing.T) {
	if n := languageName("xx"); n != "xx" {
		t.Errorf("languageName(xx) = %q, want fall-through", n)
	}
}

func TestDefaultLanguages_hasAllEight(t *testing.T) {
	want := map[string]struct{}{"en": {}, "es": {}, "fr": {}, "de": {}, "pt": {}, "ja": {}, "ar": {}, "zh": {}}
	if len(DefaultLanguages) != len(want) {
		t.Fatalf("DefaultLanguages has %d entries, want %d", len(DefaultLanguages), len(want))
	}
	for _, c := range DefaultLanguages {
		if _, ok := want[c]; !ok {
			t.Errorf("DefaultLanguages contains unexpected code %q", c)
		}
	}
}

// newStubLLMServer returns an httptest server that answers /v1/chat/completions
// with the OpenAI-style envelope, filling the response content with `content`
// or returning `status` when status != 0.
func newStubLLMServer(t *testing.T, status int, content string, callCount *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if callCount != nil {
			*callCount++
		}
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		env := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": content}},
			},
		}
		body, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func sampleSnapshot() domain.JobSnapshot {
	return domain.JobSnapshot{
		SchemaVersion:   1,
		ID:              "job_test_42",
		Slug:            "backend-eng",
		Title:           "Backend Engineer",
		DescriptionHTML: "<p>Build APIs.</p>",
		Location:        domain.LocationRef{Text: "Remote"},
		Skills: domain.SkillsRef{
			Required:   []string{"Go", "Postgres"},
			NiceToHave: []string{"Kubernetes"},
		},
		Language: "en",
	}
}

func TestTranslate_sameSrcTargetNoop(t *testing.T) {
	calls := 0
	srv := newStubLLMServer(t, 0, `{"title":"should never be used"}`, &calls)
	defer srv.Close()

	ext := extraction.New(extraction.Config{BaseURL: srv.URL, Model: "test"})
	tr := New(ext)

	snap := sampleSnapshot()
	got, err := tr.Translate(context.Background(), snap, "en", "EN")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reflect.DeepEqual(got, snap) {
		t.Errorf("same-lang translate should return snap unchanged")
	}
	if calls != 0 {
		t.Errorf("LLM should not have been called, got %d calls", calls)
	}
}

func TestTranslate_nilLLMErrors(t *testing.T) {
	tr := New(nil)
	_, err := tr.Translate(context.Background(), sampleSnapshot(), "en", "fr")
	if err == nil {
		t.Fatal("expected error from nil LLM")
	}
}

func TestTranslate_emptyTargetErrors(t *testing.T) {
	srv := newStubLLMServer(t, 0, `{}`, nil)
	defer srv.Close()
	ext := extraction.New(extraction.Config{BaseURL: srv.URL, Model: "test"})
	tr := New(ext)

	_, err := tr.Translate(context.Background(), sampleSnapshot(), "en", "")
	if err == nil {
		t.Fatal("expected error from empty target")
	}
}

func TestTranslate_llmFailurePropagates(t *testing.T) {
	srv := newStubLLMServer(t, http.StatusInternalServerError, "", nil)
	defer srv.Close()
	ext := extraction.New(extraction.Config{BaseURL: srv.URL, Model: "test"})
	tr := New(ext)

	_, err := tr.Translate(context.Background(), sampleSnapshot(), "en", "fr")
	if err == nil {
		t.Fatal("expected error from 500 status")
	}
	if !strings.Contains(err.Error(), "en") || !strings.Contains(err.Error(), "fr") {
		t.Errorf("error should mention src/tgt lang pair, got: %v", err)
	}
}

func TestTranslate_malformedJSONPropagates(t *testing.T) {
	srv := newStubLLMServer(t, 0, "not json at all", nil)
	defer srv.Close()
	ext := extraction.New(extraction.Config{BaseURL: srv.URL, Model: "test"})
	tr := New(ext)

	_, err := tr.Translate(context.Background(), sampleSnapshot(), "en", "fr")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected unmarshal-related err, got: %v", err)
	}
}

func TestTranslate_overlaysFields(t *testing.T) {
	// Partial response: only title set. The rest must be preserved from the
	// input snapshot, and result.Language must switch to the target.
	partial, _ := json.Marshal(TranslatableFields{Title: "Ingénieur Backend"})
	srv := newStubLLMServer(t, 0, string(partial), nil)
	defer srv.Close()
	ext := extraction.New(extraction.Config{BaseURL: srv.URL, Model: "test"})
	tr := New(ext)

	in := sampleSnapshot()
	got, err := tr.Translate(context.Background(), in, "en", "fr")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Title != "Ingénieur Backend" {
		t.Errorf("title = %q, want overlay", got.Title)
	}
	if got.DescriptionHTML != in.DescriptionHTML {
		t.Errorf("DescriptionHTML should be preserved: got %q, want %q", got.DescriptionHTML, in.DescriptionHTML)
	}
	if !reflect.DeepEqual(got.Skills.Required, in.Skills.Required) {
		t.Errorf("required skills should be preserved: got %v, want %v", got.Skills.Required, in.Skills.Required)
	}
	if !reflect.DeepEqual(got.Skills.NiceToHave, in.Skills.NiceToHave) {
		t.Errorf("nice-to-have skills should be preserved: got %v, want %v", got.Skills.NiceToHave, in.Skills.NiceToHave)
	}
	if got.Language != "fr" {
		t.Errorf("language should be set to target: got %q", got.Language)
	}
}

