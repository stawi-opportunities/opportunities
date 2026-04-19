package billing

import "strings"

// africanCountries is the ISO-3166 alpha-2 set Cloudflare returns in
// CF-IPCountry for African IPs. Used by the pricing page display
// and by RouteForCountry to prefer mobile-money rails when a user
// supplies a local phone number at checkout.
var africanCountries = map[string]struct{}{
	"DZ": {}, "AO": {}, "BJ": {}, "BW": {}, "BF": {}, "BI": {},
	"CM": {}, "CV": {}, "CF": {}, "TD": {}, "KM": {}, "CG": {},
	"CD": {}, "CI": {}, "DJ": {}, "EG": {}, "GQ": {}, "ER": {},
	"SZ": {}, "ET": {}, "GA": {}, "GM": {}, "GH": {}, "GN": {},
	"GW": {}, "KE": {}, "LS": {}, "LR": {}, "LY": {}, "MG": {},
	"MW": {}, "ML": {}, "MR": {}, "MU": {}, "YT": {}, "MA": {},
	"MZ": {}, "NA": {}, "NE": {}, "NG": {}, "RE": {}, "RW": {},
	"SH": {}, "ST": {}, "SN": {}, "SC": {}, "SL": {}, "SO": {},
	"ZA": {}, "SS": {}, "SD": {}, "TZ": {}, "TG": {}, "TN": {},
	"UG": {}, "EH": {}, "ZM": {}, "ZW": {},
}

// IsAfrica returns true when the country (ISO-3166 alpha-2) is on
// the African continent. Empty / unknown → false.
func IsAfrica(country string) bool {
	if country == "" {
		return false
	}
	_, ok := africanCountries[strings.ToUpper(strings.TrimSpace(country))]
	return ok
}

// mobileMoneyCurrencyByCountry maps countries we have published a
// LocalPrices column for onto their ISO-4217 code. Display hint only.
var mobileMoneyCurrencyByCountry = map[string]string{
	"KE": "KES",
	"UG": "UGX",
	"NG": "NGN",
	"ZA": "ZAR",
	"GH": "GHS",
	"TZ": "TZS",
	"RW": "RWF",
}

// MobileMoneyCurrency returns (ISO-4217, true) when the country has
// a local-currency column in the catalog, (empty, false) otherwise.
// Used by Plan.PriceFor.
func MobileMoneyCurrency(country string) (string, bool) {
	if country == "" {
		return "", false
	}
	code, ok := mobileMoneyCurrencyByCountry[strings.ToUpper(strings.TrimSpace(country))]
	return code, ok
}
