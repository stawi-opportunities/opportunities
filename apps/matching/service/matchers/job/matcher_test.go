package job

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

func TestSearchFilter_BuildsKindClause(t *testing.T) {
	prefs := json.RawMessage(`{"employment_types":["full-time"],"salary_min":80000}`)
	got, err := New().SearchFilter(prefs)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := got.(searchindex.Filter)
	if !ok {
		t.Fatalf("got %T, want searchindex.Filter", got)
	}
	sql := f.SQL()
	if !strings.Contains(sql, "kind = 'job'") {
		t.Errorf("missing kind clause: %s", sql)
	}
	if !strings.Contains(sql, "employment_type IN ('full-time')") {
		t.Errorf("missing employment_type clause: %s", sql)
	}
	if !strings.Contains(sql, "amount_min >= 80000") {
		t.Errorf("missing salary clause: %s", sql)
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
