// Package locationpref defines the shared LocationPreference block embedded
// by every kind's preferences blob.
package locationpref

// LocationPreference is the shape every kind's onboarding flow uses to
// capture geographic preferences. Embedded by JobPreferences,
// ScholarshipPreferences, etc.
type LocationPreference struct {
	Countries []string `json:"countries"`
	Regions   []string `json:"regions"`
	Cities    []string `json:"cities"`
	NearLat   float64  `json:"near_lat,omitempty"`
	NearLon   float64  `json:"near_lon,omitempty"`
	RadiusKm  int      `json:"radius_km,omitempty"`
	RemoteOK  bool     `json:"remote_ok"`
}
