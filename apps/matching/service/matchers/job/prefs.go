package job

import "github.com/stawi-opportunities/opportunities/pkg/matching/locationpref"

// JobPreferences is the per-candidate preferences blob captured by the
// job onboarding flow. Stored as JSON inside PreferencesUpdatedV1.OptIns["job"].
type JobPreferences struct {
	TargetRoles     []string                        `json:"target_roles"`
	EmploymentTypes []string                        `json:"employment_types"`
	SeniorityLevels []string                        `json:"seniority_levels"`
	SalaryMin       float64                         `json:"salary_min"`
	Currency        string                          `json:"currency"`
	Locations       locationpref.LocationPreference `json:"locations"`
}
