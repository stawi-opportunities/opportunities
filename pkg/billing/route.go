package billing

import "strings"

// Route is the string service_payment uses to route a prompt to one
// of its integration apps. See
// /home/j/code/antinvestor/service-payment/apps/integrations/ for the
// live set.
type Route string

const (
	// RoutePolar dispatches through the Polar.sh integration
	// (hosted checkout at polar.sh — card / PayPal / Apple Pay in
	// USD). Default for non-African users.
	RoutePolar Route = "POLAR"
	// RouteMpesa pushes an M-Pesa STK prompt to the user's phone.
	// Used for Kenyan and Tanzanian numbers.
	RouteMpesa Route = "M-PESA"
	// RouteAirtel pushes an Airtel Money prompt. Used for Uganda,
	// Kenya, Tanzania, Rwanda, Malawi, Zambia.
	RouteAirtel Route = "AIRTEL"
	// RouteMtn pushes an MTN MoMo prompt. Used for Uganda, Ghana,
	// Rwanda, Côte d'Ivoire, Cameroon, Benin.
	RouteMtn Route = "MTN"
)

// mobileMoneyRouteByCountry maps a country to the mobile-money rail
// we default to when the user hasn't told us which operator they
// prefer. The integration set lives in service_payment; this table
// only needs to cover cases where that service has a configured
// integration. We keep the list conservative — when in doubt, fall
// back to Polar so the user never sees "this country has no route"
// on a subscribe click.
var mobileMoneyRouteByCountry = map[string]Route{
	"KE": RouteMpesa,  // Safaricom M-Pesa dominates
	"TZ": RouteMpesa,  // Vodacom M-Pesa
	"UG": RouteMtn,    // MTN dominant; Airtel available too
	"GH": RouteMtn,    // MTN MoMo dominant
	"RW": RouteMtn,    // MTN Rwanda
	"CI": RouteMtn,    // MTN Côte d'Ivoire
	"CM": RouteMtn,    // MTN Cameroon
	"BJ": RouteMtn,    // MTN Benin
	"NG": RouteMtn,    // MTN Nigeria is largest mobile-money footprint
	"MW": RouteAirtel, // Airtel Money Malawi
	"ZM": RouteAirtel, // Airtel Money Zambia
}

// RouteForCountry picks a service_payment route for a CF-IPCountry
// value. If the caller supplies a non-empty `routeHint` (e.g. the
// user explicitly ticked "Pay with M-Pesa" on the checkout form),
// that overrides the country-based default. Unknown / non-African
// countries always get Polar.
func RouteForCountry(country, routeHint string) Route {
	if hint := normaliseRoute(routeHint); hint != "" {
		return hint
	}
	country = strings.ToUpper(strings.TrimSpace(country))
	if r, ok := mobileMoneyRouteByCountry[country]; ok {
		return r
	}
	return RoutePolar
}

// normaliseRoute accepts a free-form hint ("mpesa", "m-pesa",
// "mobile_money", "card", "polar") and maps it to a canonical Route
// or empty when the hint isn't recognised.
func normaliseRoute(hint string) Route {
	switch strings.ToLower(strings.TrimSpace(hint)) {
	case "mpesa", "m-pesa", "m_pesa":
		return RouteMpesa
	case "airtel", "airtel_money":
		return RouteAirtel
	case "mtn", "mtn_momo", "momo":
		return RouteMtn
	case "polar", "polar.sh", "card":
		return RoutePolar
	}
	return ""
}

// IsHostedCheckout returns true when the route results in a hosted
// provider page the user must be redirected to (Polar). STK-push
// routes (M-Pesa/Airtel/MTN) instead prompt the user's phone and
// need no browser redirect.
func (r Route) IsHostedCheckout() bool {
	return r == RoutePolar
}
