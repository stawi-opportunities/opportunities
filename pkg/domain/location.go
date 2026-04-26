package domain

// Location is the universal geographic block carried on every Opportunity.
// Each field is optional; a fully-zero Location is valid and means
// "no anchor — opportunity is global / location-irrelevant".
type Location struct {
	Country string  `json:"country,omitempty"` // ISO 3166-1 alpha-2
	Region  string  `json:"region,omitempty"`  // ISO 3166-2 or free-form
	City    string  `json:"city,omitempty"`
	Lat     float64 `json:"lat,omitempty"` // 0 = unset
	Lon     float64 `json:"lon,omitempty"` // 0 = unset
}

// IsZero reports whether every field is empty.
func (l Location) IsZero() bool {
	return l.Country == "" && l.Region == "" && l.City == "" && l.Lat == 0 && l.Lon == 0
}

// HasCoords reports whether Lat/Lon are populated. (0,0) is treated as
// unset — there is no real-world opportunity at the equator/prime
// meridian intersection in our domain.
func (l Location) HasCoords() bool { return l.Lat != 0 && l.Lon != 0 }
