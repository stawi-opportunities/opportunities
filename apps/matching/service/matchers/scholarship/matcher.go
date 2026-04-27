package scholarship

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher { return &Matcher{} }

func (*Matcher) Kind() string   { return "scholarship" }
func (*Matcher) Disabled() bool { return false }

func (m *Matcher) SearchFilter(prefs json.RawMessage) (any, error) {
	var p ScholarshipPreferences
	if len(prefs) > 0 {
		if err := json.Unmarshal(prefs, &p); err != nil {
			return searchindex.Filter{}, err
		}
	}
	f := searchindex.Filter{Kind: "scholarship"}
	if len(p.DegreeLevels) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "degree_level", Values: p.DegreeLevels})
	}
	if len(p.FieldsOfStudy) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "field_of_study", Values: p.FieldsOfStudy})
	}
	if len(p.Locations.Countries) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "country", Values: p.Locations.Countries})
	}
	return f, nil
}

func (m *Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (matchers.ScoreResult, error) {
	var p ScholarshipPreferences
	if len(prefs) > 0 {
		if err := json.Unmarshal(prefs, &p); err != nil {
			return matchers.ScoreResult{}, err
		}
	}
	o, _ := opp.(map[string]any)
	score := 0.0
	reasons := make([]string, 0, 3)

	if dl, _ := o["degree_level"].(string); dl != "" {
		for _, want := range p.DegreeLevels {
			if dl == want {
				score += 0.4
				reasons = append(reasons, fmt.Sprintf("degree level matches %q", want))
				break
			}
		}
	}
	if fos, _ := o["field_of_study"].(string); fos != "" {
		for _, want := range p.FieldsOfStudy {
			if fos == want {
				score += 0.4
				reasons = append(reasons, fmt.Sprintf("field of study matches %q", want))
				break
			}
		}
	}
	if dl, _ := o["deadline"].(string); dl != "" {
		if t, err := time.Parse(time.RFC3339, dl); err == nil {
			if time.Until(t) > 30*24*time.Hour {
				score += 0.2
				reasons = append(reasons, "deadline more than 30 days away")
			}
		}
	}
	if score > 1.0 {
		score = 1.0
	}
	if len(reasons) == 0 {
		reasons = nil
	}
	return matchers.ScoreResult{Score: score, Reasons: reasons}, nil
}

var _ matchers.Matcher = (*Matcher)(nil)
