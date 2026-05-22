package job

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/pgsearch"
)

func TestSearchFilter_BuildsKindClause(t *testing.T) {
	prefs := json.RawMessage(`{"employment_types":["full-time"],"salary_min":80000}`)
	got, err := New().SearchFilter(prefs)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := got.(pgsearch.Filter)
	if !ok {
		t.Fatalf("got %T, want pgsearch.Filter", got)
	}
	if f.Kind != "job" {
		t.Errorf("Kind=%q, want %q", f.Kind, "job")
	}
	if len(f.AttributeAnyOf) == 0 || f.AttributeAnyOf[0].Field != "employment_type" {
		t.Errorf("expected AttributeAnyOf employment_type, got %+v", f.AttributeAnyOf)
	}
	if len(f.RangeMin) == 0 || f.RangeMin[0].Field != "amount_min" || f.RangeMin[0].Value != 80000 {
		t.Errorf("expected RangeMin amount_min=80000, got %+v", f.RangeMin)
	}
	// Sanity: Build() produces a syntactically reasonable WHERE
	// fragment that names the columns we care about.
	sql, _ := f.Build(1)
	if !strings.Contains(sql, "kind = $") {
		t.Errorf("missing kind clause in %q", sql)
	}
	if !strings.Contains(sql, "attributes->>'employment_type'") {
		t.Errorf("missing employment_type clause in %q", sql)
	}
	if !strings.Contains(sql, "amount_min >= $") {
		t.Errorf("missing amount_min clause in %q", sql)
	}
}

func TestScore_TitleAndSalaryContribute(t *testing.T) {
	prefs := json.RawMessage(`{"target_roles":["Go Engineer"],"salary_min":80000}`)
	opp := map[string]any{"title": "Senior Go Engineer", "amount_min": 130000.0}
	got, err := New().Score(context.Background(), prefs, opp)
	if err != nil || got.Score < 0.8 {
		t.Fatalf("Score=%v err=%v, want >= 0.8", got.Score, err)
	}
	if len(got.Reasons) == 0 {
		t.Fatalf("Reasons empty, expected at least one")
	}
}

func TestScore_ReasonsIncludeRoleAndSalary(t *testing.T) {
	prefs := json.RawMessage(`{"target_roles":["Go Engineer"],"salary_min":80000,"locations":{"countries":["KE"]}}`)
	opp := map[string]any{"title": "Senior Go Engineer", "amount_min": 130000.0, "country": "KE"}
	got, err := New().Score(context.Background(), prefs, opp)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got.Reasons, "|")
	if !strings.Contains(joined, "title contains target role") {
		t.Errorf("expected title reason, got %v", got.Reasons)
	}
	if !strings.Contains(joined, "salary above floor") {
		t.Errorf("expected salary reason, got %v", got.Reasons)
	}
	if !strings.Contains(joined, "country in preferred list") {
		t.Errorf("expected country reason, got %v", got.Reasons)
	}
}
