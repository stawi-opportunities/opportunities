// Package locale groups countries into regions and resolves languages
// for the local-first job cascade.
//
// Usage:
//
//	region := locale.RegionOf("KE")                // → "east_africa"
//	neighbours := locale.CountriesIn(region)        // → [KE UG TZ RW ET SO SS DJ ER]
//	isMatch := locale.LanguageMatches("en-US", "en") // → true (base-tag match)
//
// Regions are chosen for *labour-market proximity* (shared languages,
// time-zones, frequent cross-border hiring) rather than strict
// geographic neighbours. "East Africa" includes Ethiopia even though
// it doesn't share a border with Kenya because senior talent routinely
// moves between Addis and Nairobi on our source feeds.
package locale

import "strings"

// Region is an opaque identifier for a labour-market cluster. Stable
// across versions so any downstream caches / analytics that key on
// region don't break.
type Region string

const (
	RegionEastAfrica       Region = "east_africa"
	RegionWestAfrica       Region = "west_africa"
	RegionSouthernAfrica   Region = "southern_africa"
	RegionNorthAfrica      Region = "north_africa"
	RegionCentralAfrica    Region = "central_africa"
	RegionNorthAmerica     Region = "north_america"
	RegionLatinAmerica     Region = "latin_america"
	RegionWesternEurope    Region = "western_europe"
	RegionNorthernEurope   Region = "northern_europe"
	RegionSouthernEurope   Region = "southern_europe"
	RegionEasternEurope    Region = "eastern_europe"
	RegionMiddleEast       Region = "middle_east"
	RegionSouthAsia        Region = "south_asia"
	RegionSoutheastAsia    Region = "southeast_asia"
	RegionEastAsia         Region = "east_asia"
	RegionOceania          Region = "oceania"
	RegionUnknown          Region = "unknown"
)

// regionByCountry maps ISO-3166 alpha-2 → Region. The empty string
// key lets a missing country fall through to RegionUnknown via the
// lookup helper.
var regionByCountry = map[string]Region{
	// East Africa — M-Pesa belt, English lingua franca, Swahili strong.
	"KE": RegionEastAfrica, "UG": RegionEastAfrica, "TZ": RegionEastAfrica,
	"RW": RegionEastAfrica, "BI": RegionEastAfrica, "ET": RegionEastAfrica,
	"SO": RegionEastAfrica, "SS": RegionEastAfrica, "DJ": RegionEastAfrica,
	"ER": RegionEastAfrica,

	// West Africa — mixed English / French, large remote markets.
	"NG": RegionWestAfrica, "GH": RegionWestAfrica, "SN": RegionWestAfrica,
	"CI": RegionWestAfrica, "BJ": RegionWestAfrica, "TG": RegionWestAfrica,
	"BF": RegionWestAfrica, "ML": RegionWestAfrica, "NE": RegionWestAfrica,
	"GN": RegionWestAfrica, "SL": RegionWestAfrica, "LR": RegionWestAfrica,
	"GM": RegionWestAfrica, "GW": RegionWestAfrica, "CV": RegionWestAfrica,
	"MR": RegionWestAfrica,

	// Southern Africa — English dominant, strong bank infrastructure.
	"ZA": RegionSouthernAfrica, "ZW": RegionSouthernAfrica,
	"ZM": RegionSouthernAfrica, "MW": RegionSouthernAfrica,
	"BW": RegionSouthernAfrica, "NA": RegionSouthernAfrica,
	"MZ": RegionSouthernAfrica, "SZ": RegionSouthernAfrica,
	"LS": RegionSouthernAfrica, "AO": RegionSouthernAfrica,
	"MG": RegionSouthernAfrica, "MU": RegionSouthernAfrica,

	// North Africa — Arabic + French/English mix.
	"EG": RegionNorthAfrica, "MA": RegionNorthAfrica,
	"DZ": RegionNorthAfrica, "TN": RegionNorthAfrica,
	"LY": RegionNorthAfrica, "SD": RegionNorthAfrica,
	"EH": RegionNorthAfrica,

	// Central Africa — French-speaking core.
	"CM": RegionCentralAfrica, "GA": RegionCentralAfrica,
	"CG": RegionCentralAfrica, "CD": RegionCentralAfrica,
	"CF": RegionCentralAfrica, "TD": RegionCentralAfrica,
	"GQ": RegionCentralAfrica, "ST": RegionCentralAfrica,
	"KM": RegionCentralAfrica,

	// North America.
	"US": RegionNorthAmerica, "CA": RegionNorthAmerica,
	"MX": RegionNorthAmerica, "PR": RegionNorthAmerica,

	// Latin America — ES + PT + indigenous.
	"BR": RegionLatinAmerica, "AR": RegionLatinAmerica,
	"CL": RegionLatinAmerica, "CO": RegionLatinAmerica,
	"PE": RegionLatinAmerica, "VE": RegionLatinAmerica,
	"UY": RegionLatinAmerica, "PY": RegionLatinAmerica,
	"BO": RegionLatinAmerica, "EC": RegionLatinAmerica,
	"CR": RegionLatinAmerica, "PA": RegionLatinAmerica,
	"DO": RegionLatinAmerica, "CU": RegionLatinAmerica,
	"GT": RegionLatinAmerica, "HN": RegionLatinAmerica,
	"SV": RegionLatinAmerica, "NI": RegionLatinAmerica,
	"JM": RegionLatinAmerica, "TT": RegionLatinAmerica,
	"HT": RegionLatinAmerica,

	// Western Europe — EUR core + UK/IE.
	"GB": RegionWesternEurope, "IE": RegionWesternEurope,
	"FR": RegionWesternEurope, "BE": RegionWesternEurope,
	"NL": RegionWesternEurope, "LU": RegionWesternEurope,
	"DE": RegionWesternEurope, "AT": RegionWesternEurope,
	"CH": RegionWesternEurope, "LI": RegionWesternEurope,
	"MC": RegionWesternEurope,

	// Northern Europe — Scandinavian + Baltics.
	"SE": RegionNorthernEurope, "NO": RegionNorthernEurope,
	"DK": RegionNorthernEurope, "FI": RegionNorthernEurope,
	"IS": RegionNorthernEurope, "EE": RegionNorthernEurope,
	"LV": RegionNorthernEurope, "LT": RegionNorthernEurope,

	// Southern Europe.
	"ES": RegionSouthernEurope, "PT": RegionSouthernEurope,
	"IT": RegionSouthernEurope, "GR": RegionSouthernEurope,
	"CY": RegionSouthernEurope, "MT": RegionSouthernEurope,
	"AD": RegionSouthernEurope, "SM": RegionSouthernEurope,

	// Eastern Europe.
	"PL": RegionEasternEurope, "CZ": RegionEasternEurope,
	"SK": RegionEasternEurope, "HU": RegionEasternEurope,
	"RO": RegionEasternEurope, "BG": RegionEasternEurope,
	"HR": RegionEasternEurope, "SI": RegionEasternEurope,
	"RS": RegionEasternEurope, "BA": RegionEasternEurope,
	"MK": RegionEasternEurope, "AL": RegionEasternEurope,
	"ME": RegionEasternEurope, "XK": RegionEasternEurope,
	"UA": RegionEasternEurope, "MD": RegionEasternEurope,
	"BY": RegionEasternEurope, "RU": RegionEasternEurope,

	// Middle East.
	"AE": RegionMiddleEast, "SA": RegionMiddleEast,
	"QA": RegionMiddleEast, "BH": RegionMiddleEast,
	"KW": RegionMiddleEast, "OM": RegionMiddleEast,
	"YE": RegionMiddleEast, "JO": RegionMiddleEast,
	"LB": RegionMiddleEast, "SY": RegionMiddleEast,
	"IQ": RegionMiddleEast, "IR": RegionMiddleEast,
	"IL": RegionMiddleEast, "PS": RegionMiddleEast,
	"TR": RegionMiddleEast,

	// South Asia.
	"IN": RegionSouthAsia, "PK": RegionSouthAsia,
	"BD": RegionSouthAsia, "LK": RegionSouthAsia,
	"NP": RegionSouthAsia, "BT": RegionSouthAsia,
	"MV": RegionSouthAsia, "AF": RegionSouthAsia,

	// Southeast Asia.
	"SG": RegionSoutheastAsia, "MY": RegionSoutheastAsia,
	"ID": RegionSoutheastAsia, "TH": RegionSoutheastAsia,
	"VN": RegionSoutheastAsia, "PH": RegionSoutheastAsia,
	"MM": RegionSoutheastAsia, "KH": RegionSoutheastAsia,
	"LA": RegionSoutheastAsia, "BN": RegionSoutheastAsia,
	"TL": RegionSoutheastAsia,

	// East Asia.
	"CN": RegionEastAsia, "JP": RegionEastAsia,
	"KR": RegionEastAsia, "TW": RegionEastAsia,
	"HK": RegionEastAsia, "MO": RegionEastAsia,
	"MN": RegionEastAsia, "KP": RegionEastAsia,

	// Oceania.
	"AU": RegionOceania, "NZ": RegionOceania,
	"FJ": RegionOceania, "PG": RegionOceania,
	"SB": RegionOceania, "VU": RegionOceania,
	"WS": RegionOceania, "TO": RegionOceania,
	"KI": RegionOceania, "FM": RegionOceania,
}

// countriesByRegion is the inverse of regionByCountry, built once at
// init. Used to answer "what countries are nearby this one?".
var countriesByRegion = func() map[Region][]string {
	out := make(map[Region][]string, 16)
	for cc, r := range regionByCountry {
		out[r] = append(out[r], cc)
	}
	return out
}()

// RegionOf returns the region for a country, or RegionUnknown for
// unmapped / empty input. Case-insensitive.
func RegionOf(country string) Region {
	if country == "" {
		return RegionUnknown
	}
	r, ok := regionByCountry[strings.ToUpper(strings.TrimSpace(country))]
	if !ok {
		return RegionUnknown
	}
	return r
}

// CountriesIn returns the ISO-3166 alpha-2 codes in a region, sorted
// alphabetically so SQL IN () clauses are deterministic across calls
// (query-plan cache friendlier). Returned slice is safe to mutate —
// it's a fresh copy on every call.
func CountriesIn(r Region) []string {
	src := countriesByRegion[r]
	out := make([]string, len(src))
	copy(out, src)
	// Stable order aids prepared-statement cache hits downstream.
	// Tiny input sets (<30) so insertion sort would suffice, but
	// stdlib sort is clearer and fast enough.
	sortStrings(out)
	return out
}

// sortStrings is a local insertion sort — fewer deps than pulling
// sort.Strings, and our slices are <= 30 elements.
func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

// RegionLabel returns a human-readable label suitable for UI section
// headings. Matches the region constants in ui/app/src/utils/locale.ts
// — keep the two in sync.
func RegionLabel(r Region) string {
	switch r {
	case RegionEastAfrica:
		return "East Africa"
	case RegionWestAfrica:
		return "West Africa"
	case RegionSouthernAfrica:
		return "Southern Africa"
	case RegionNorthAfrica:
		return "North Africa"
	case RegionCentralAfrica:
		return "Central Africa"
	case RegionNorthAmerica:
		return "North America"
	case RegionLatinAmerica:
		return "Latin America"
	case RegionWesternEurope:
		return "Western Europe"
	case RegionNorthernEurope:
		return "Northern Europe"
	case RegionSouthernEurope:
		return "Southern Europe"
	case RegionEasternEurope:
		return "Eastern Europe"
	case RegionMiddleEast:
		return "Middle East"
	case RegionSouthAsia:
		return "South Asia"
	case RegionSoutheastAsia:
		return "Southeast Asia"
	case RegionEastAsia:
		return "East Asia"
	case RegionOceania:
		return "Oceania"
	default:
		return "Worldwide"
	}
}
