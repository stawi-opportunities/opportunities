package funding

import "github.com/stawi-opportunities/opportunities/pkg/matching/locationpref"

type FundingPreferences struct {
	OrganisationType       string                          `json:"organisation_type"`
	FocusAreas             []string                        `json:"focus_areas"`
	GeographicScope        locationpref.LocationPreference `json:"geographic_scope"`
	FundingAmountNeededMin float64                         `json:"funding_amount_needed_min"`
	FundingAmountNeededMax float64                         `json:"funding_amount_needed_max"`
	Currency               string                          `json:"currency"`
}
