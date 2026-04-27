package job

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher { return &Matcher{} }

func (m *Matcher) Kind() string { return "job" }
func (*Matcher) Disabled() bool { return false }

func (m *Matcher) SearchFilter(prefs json.RawMessage) (any, error) {
	var p JobPreferences
	if len(prefs) > 0 {
		if err := json.Unmarshal(prefs, &p); err != nil {
			return searchindex.Filter{}, err
		}
	}
	f := searchindex.Filter{Kind: "job"}
	if len(p.EmploymentTypes) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "employment_type", Values: p.EmploymentTypes})
	}
	if len(p.SeniorityLevels) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "seniority", Values: p.SeniorityLevels})
	}
	if p.SalaryMin > 0 {
		f.RangeMin = append(f.RangeMin, searchindex.RangeMin{Field: "amount_min", Value: p.SalaryMin})
	}
	if len(p.Locations.Countries) > 0 {
		f.AnyOf = append(f.AnyOf, searchindex.AnyOf{Field: "country", Values: p.Locations.Countries})
	}
	if p.Locations.RemoteOK {
		f.OrTerm = "remote = 1"
	}
	if p.Locations.NearLat != 0 && p.Locations.RadiusKm > 0 {
		f.GeoDist = &searchindex.GeoDist{Lat: p.Locations.NearLat, Lon: p.Locations.NearLon, RadiusKm: p.Locations.RadiusKm}
	}
	return f, nil
}

func (m *Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (matchers.ScoreResult, error) {
	var p JobPreferences
	if len(prefs) > 0 {
		if err := json.Unmarshal(prefs, &p); err != nil {
			return matchers.ScoreResult{}, err
		}
	}
	o, _ := opp.(map[string]any)
	score := 0.0
	reasons := make([]string, 0, 4)

	if title, _ := o["title"].(string); title != "" {
		for _, role := range p.TargetRoles {
			if role != "" && strings.Contains(strings.ToLower(title), strings.ToLower(role)) {
				score += 0.4
				reasons = append(reasons, fmt.Sprintf("title contains target role %q", role))
				break
			}
		}
	}
	if amt, _ := o["amount_min"].(float64); amt >= p.SalaryMin && p.SalaryMin > 0 {
		score += 0.3
		reasons = append(reasons, "salary above floor")
	}
	if amt, _ := o["amount_min"].(float64); p.SalaryMin > 0 && amt >= 1.5*p.SalaryMin {
		score += 0.2
		reasons = append(reasons, "salary 1.5x above floor")
	}
	if country, _ := o["country"].(string); country != "" && len(p.Locations.Countries) > 0 {
		for _, want := range p.Locations.Countries {
			if strings.EqualFold(country, want) {
				reasons = append(reasons, "country in preferred list")
				break
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
