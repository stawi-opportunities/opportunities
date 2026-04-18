package billing

import "strings"

// africanCountries is the ISO-3166 alpha-2 set Cloudflare returns in
// CF-IPCountry for African IPs. Comprehensive because payment routing
// must not miss any African user — a wrong route silently pushes
// someone who should have had mobile money onto the card-only rail.
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

// IsAfrica returns true when the country code (ISO-3166 alpha-2) is
// on the African continent. Empty / "XX" / anything unknown returns
// false, so unknown geography falls through to the non-African path
// (Plar.sh) rather than sending users to a rail that can't bill them.
func IsAfrica(country string) bool {
	if country == "" {
		return false
	}
	normalised := strings.ToUpper(strings.TrimSpace(country))
	_, ok := africanCountries[normalised]
	return ok
}

// mobileMoneyCurrencyByCountry maps the subset of African countries
// where we have published local-price columns in the plan catalog to
// their ISO-4217 code. A country on this list AND in the catalog's
// LocalPrices map gets billed in local currency; anything else on
// the continent falls back to USD via DusuPay's FX.
var mobileMoneyCurrencyByCountry = map[string]string{
	"KE": "KES",
	"UG": "UGX",
	"NG": "NGN",
	"ZA": "ZAR",
	"GH": "GHS",
	"TZ": "TZS",
	"RW": "RWF",
}

// MobileMoneyCurrency returns the ISO-4217 currency for a country
// that has a local-currency column in the catalog, and (nil, false)
// otherwise. Used by Plan.PriceFor.
func MobileMoneyCurrency(country string) (string, bool) {
	if country == "" {
		return "", false
	}
	code, ok := mobileMoneyCurrencyByCountry[strings.ToUpper(strings.TrimSpace(country))]
	return code, ok
}
