package scholarship

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/pgsearch"
)

func TestSearchFilter_KindScopedToScholarship(t *testing.T) {
	prefs := json.RawMessage(`{"degree_levels":["masters"],"fields_of_study":["Climate"]}`)
	got, err := New().SearchFilter(prefs)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := got.(pgsearch.Filter)
	if !ok {
		t.Fatalf("got %T, want pgsearch.Filter", got)
	}
	if f.Kind != "scholarship" {
		t.Errorf("Kind=%q, want scholarship", f.Kind)
	}
	wantFields := map[string][]string{
		"degree_level":   {"masters"},
		"field_of_study": {"Climate"},
	}
	got2 := map[string][]string{}
	for _, a := range f.AttributeAnyOf {
		got2[a.Field] = a.Values
	}
	for k, v := range wantFields {
		if !equalSlice(got2[k], v) {
			t.Errorf("attribute %s: got %v, want %v", k, got2[k], v)
		}
	}
	sql, _ := f.Build(1)
	if !strings.Contains(sql, "kind = $") {
		t.Errorf("missing kind clause in %q", sql)
	}
	if !strings.Contains(sql, "attributes->>'degree_level'") {
		t.Errorf("missing degree_level clause in %q", sql)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScore_DegreeAndFieldAndDeadlineHorizon(t *testing.T) {
	prefs := json.RawMessage(`{"degree_levels":["masters"],"fields_of_study":["Climate"]}`)
	deadline := time.Now().Add(60 * 24 * time.Hour).UTC().Format(time.RFC3339)
	opp := map[string]any{"degree_level": "masters", "field_of_study": "Climate", "deadline": deadline}
	got, err := New().Score(context.Background(), prefs, opp)
	if err != nil || got.Score < 0.9 {
		t.Fatalf("Score=%v err=%v, want >= 0.9", got.Score, err)
	}
	if len(got.Reasons) < 2 {
		t.Fatalf("expected at least 2 reasons, got %v", got.Reasons)
	}
}
