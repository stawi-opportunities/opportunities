package scholarship

import (
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/matching/locationpref"
)

// ScholarshipPreferences is the per-candidate preferences blob captured by
// the scholarship onboarding flow. Stored as JSON inside
// PreferencesUpdatedV1.OptIns["scholarship"].
type ScholarshipPreferences struct {
	DegreeLevels   []string                        `json:"degree_levels"`
	FieldsOfStudy  []string                        `json:"fields_of_study"`
	Nationality    string                          `json:"nationality"`
	GPAMin         float64                         `json:"gpa_min"`
	Locations      locationpref.LocationPreference `json:"locations"`
	DeadlineWithin time.Duration                   `json:"deadline_within"`
}
