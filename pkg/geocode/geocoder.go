// Package geocode provides a tiny offline gazetteer that resolves a
// (city, country) pair into Lat/Lon. The dataset is a hand-curated
// trim of GeoNames cities5000 (CC BY 4.0), bundled via go:embed and
// parsed once at construction.
//
// Misses leave coordinates zero — callers that consume Lat/Lon (e.g.
// Manticore GEODIST radius search) treat zero as "no anchor" and skip
// the record from radius queries. That's the correct behaviour: a
// rural village we can't resolve simply doesn't show up in
// "opportunities near me", but is still searchable by every other
// facet.
package geocode

import (
	"bufio"
	_ "embed"
	"strconv"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

//go:embed cities.tsv
var citiesTSV string

// Geocoder resolves city names to coordinates using a bundled
// gazetteer. Constructed once at boot; safe for concurrent reads
// after construction (maps are read-only).
type Geocoder struct {
	// byKey is the primary lookup: lower(name) + "|" + upper(country).
	// A (Nairobi, KE) hit lands here.
	byKey map[string]domain.Location

	// byCity is the country-less fallback: lower(name) → first-seen
	// (highest-population) Location with that name. Used when the
	// extractor produced a city but no country.
	byCity map[string]domain.Location
}

// New parses the embedded cities.tsv at construction. Cheap (~300
// rows in v1, designed to scale to ~10k without measurable cost).
// The TSV format is:
//
//	name\tcountry\tregion\tlat\tlon\tpopulation
//
// Rows missing required columns are skipped silently; a malformed
// row should not poison the whole gazetteer.
func New() *Geocoder {
	g := &Geocoder{
		byKey:  map[string]domain.Location{},
		byCity: map[string]domain.Location{},
	}
	scanner := bufio.NewScanner(strings.NewReader(citiesTSV))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 5 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		country := strings.ToUpper(strings.TrimSpace(parts[1]))
		region := strings.TrimSpace(parts[2])
		lat, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
		if err != nil {
			continue
		}
		lon, err := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
		if err != nil {
			continue
		}
		if name == "" {
			continue
		}
		loc := domain.Location{
			Country: country,
			Region:  region,
			City:    name,
			Lat:     lat,
			Lon:     lon,
		}
		// Country-qualified key always wins on insertion order, but
		// since the source TSV is sorted by population descending the
		// first row for a given (name|country) is the most populous.
		key := strings.ToLower(name) + "|" + country
		if _, exists := g.byKey[key]; !exists {
			g.byKey[key] = loc
		}
		// First-seen wins on the country-less map, which because of
		// the population sort gives us the largest city of that name
		// globally — typically what someone means by "Paris" with no
		// country qualifier.
		nameKey := strings.ToLower(name)
		if _, exists := g.byCity[nameKey]; !exists {
			g.byCity[nameKey] = loc
		}
	}
	return g
}

// Lookup returns a Location for the given city + country. country
// is optional; if blank, we return the highest-population
// (first-seen) city with that name globally. Returns ok=false if
// the city is unknown or empty.
func (g *Geocoder) Lookup(city, country string) (domain.Location, bool) {
	city = strings.TrimSpace(city)
	if city == "" {
		return domain.Location{}, false
	}
	if country != "" {
		loc, ok := g.byKey[strings.ToLower(city)+"|"+strings.ToUpper(strings.TrimSpace(country))]
		return loc, ok
	}
	loc, ok := g.byCity[strings.ToLower(city)]
	return loc, ok
}

// Enrich populates AnchorLocation.Lat/Lon (and Region if missing)
// on opp when the gazetteer has a hit. No-op when extraction
// already supplied coords or when the city/country combination is
// unknown. Safe to call when AnchorLocation is nil — it short-
// circuits.
func (g *Geocoder) Enrich(opp *domain.ExternalOpportunity) {
	if opp == nil || opp.AnchorLocation == nil {
		return
	}
	if opp.AnchorLocation.City == "" {
		return
	}
	if opp.AnchorLocation.HasCoords() {
		return
	}
	loc, ok := g.Lookup(opp.AnchorLocation.City, opp.AnchorLocation.Country)
	if !ok {
		return
	}
	opp.AnchorLocation.Lat = loc.Lat
	opp.AnchorLocation.Lon = loc.Lon
	if opp.AnchorLocation.Region == "" {
		opp.AnchorLocation.Region = loc.Region
	}
}
