package billing

import "strings"

// mobileMoneyCountries maps an ISO-3166 alpha-2 country to its default
// mobile-money route. These markets get an STK-push (InitiatePrompt) flow;
// everywhere else falls back to a card / Polar payment link.
var mobileMoneyCountries = map[string]Route{
	"KE": RouteMpesa,  // Kenya — M-PESA
	"TZ": RouteMpesa,  // Tanzania — M-PESA
	"UG": RouteMTN,    // Uganda — MTN MoMo
	"GH": RouteMTN,    // Ghana — MTN MoMo
	"RW": RouteMTN,    // Rwanda — MTN MoMo
	"ZM": RouteAirtel, // Zambia — Airtel Money
	"MW": RouteAirtel, // Malawi — Airtel Money
}

// RouteForCountry picks the payment rail. An explicit, valid routeHint
// wins; otherwise the country is mapped to its mobile-money route,
// defaulting to Polar (card) for the rest of the world. Exported so the
// plans handler can advertise the route a country would take.
func RouteForCountry(country, routeHint string) Route {
	switch Route(strings.ToUpper(strings.TrimSpace(routeHint))) {
	case RoutePolar, RouteMpesa, RouteAirtel, RouteMTN:
		return Route(strings.ToUpper(strings.TrimSpace(routeHint)))
	}
	if r, ok := mobileMoneyCountries[strings.ToUpper(strings.TrimSpace(country))]; ok {
		return r
	}
	return RoutePolar
}

// isMobileMoney reports whether a route is an STK-push rail.
func isMobileMoney(r Route) bool {
	return r == RouteMpesa || r == RouteAirtel || r == RouteMTN
}
