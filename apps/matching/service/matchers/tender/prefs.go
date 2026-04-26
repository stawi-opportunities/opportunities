package tender

import "github.com/stawi-opportunities/opportunities/pkg/matching/locationpref"

type TenderPreferences struct {
	CompanyName              string                          `json:"company_name"`
	RegistrationCountry      string                          `json:"registration_country"`
	Capabilities             []string                        `json:"capabilities"`
	Certifications           []string                        `json:"certifications"`
	BudgetCapacityMin        float64                         `json:"budget_capacity_min"`
	BudgetCapacityMax        float64                         `json:"budget_capacity_max"`
	ServiceCapabilityRegions locationpref.LocationPreference `json:"service_capability_regions"`
}
