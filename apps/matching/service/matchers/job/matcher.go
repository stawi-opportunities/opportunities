package job

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher { return &Matcher{} }

func (m *Matcher) Kind() string { return "job" }

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

func (m *Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (float64, error) {
	var p JobPreferences
	if len(prefs) > 0 {
		if err := json.Unmarshal(prefs, &p); err != nil {
			return 0, err
		}
	}
	o, _ := opp.(map[string]any)
	score := 0.0
	if title, _ := o["title"].(string); title != "" {
		for _, role := range p.TargetRoles {
			if role != "" && strings.Contains(strings.ToLower(title), strings.ToLower(role)) {
				score += 0.4
				break
			}
		}
	}
	if amt, _ := o["amount_min"].(float64); amt >= p.SalaryMin && p.SalaryMin > 0 {
		score += 0.3
	}
	if amt, _ := o["amount_min"].(float64); p.SalaryMin > 0 && amt >= 1.5*p.SalaryMin {
		score += 0.2
	}
	if score > 1.0 {
		score = 1.0
	}
	return score, nil
}

var _ matchers.Matcher = (*Matcher)(nil)
