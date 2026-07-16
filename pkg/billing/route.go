package billing

// RouteForCountry always returns Flutterwave. Country and routeHint are
// ignored (kept for call-site compatibility with older code/tests).
func RouteForCountry(country, routeHint string) Route {
	_, _ = country, routeHint
	return RouteFlutterwave
}
