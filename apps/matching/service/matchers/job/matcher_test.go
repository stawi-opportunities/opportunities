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
	if err != nil || got < 0.8 {
		t.Fatalf("Score=%v err=%v, want >= 0.8", got, err)
	}
}
