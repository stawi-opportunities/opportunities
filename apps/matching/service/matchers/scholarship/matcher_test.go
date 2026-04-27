package scholarship

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

func TestSearchFilter_KindScopedToScholarship(t *testing.T) {
	prefs := json.RawMessage(`{"degree_levels":["masters"],"fields_of_study":["Climate"]}`)
	got, err := New().SearchFilter(prefs)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := got.(searchindex.Filter)
	if !ok {
		t.Fatalf("got %T, want searchindex.Filter", got)
	}
	sql := f.SQL()
	if !strings.Contains(sql, "kind = 'scholarship'") {
		t.Errorf("missing kind clause: %s", sql)
	}
	if !strings.Contains(sql, "degree_level IN ('masters')") {
		t.Errorf("missing degree_level clause: %s", sql)
	}
	if !strings.Contains(sql, "field_of_study IN ('Climate')") {
		t.Errorf("missing field_of_study clause: %s", sql)
	}
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
