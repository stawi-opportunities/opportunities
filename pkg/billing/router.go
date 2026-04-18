package billing

import (
	"fmt"
	"net/http"
	"strings"
)

// Router picks a Provider based on the CF-IPCountry header. African
// users get DusuPay; everyone else gets Plar.sh. The router is
// construction-time-immutable: adapters are wired once at boot from
// config and held here.
type Router struct {
	africa   Provider
	elsewhere Provider
}

// NewRouter returns a Router with the two adapters pre-bound. Either
// can be nil — a nil adapter means "no provider configured for this
// geography" and Pick returns an error rather than silently falling
// back to the other rail (which would bill users in the wrong
// currency). Tests pass nils explicitly; production wires both.
func NewRouter(africa, elsewhere Provider) *Router {
	return &Router{africa: africa, elsewhere: elsewhere}
}

// Pick selects a provider for the given country. Returns
// ErrNoProvider when the geography's configured adapter is nil (e.g.
// DusuPay creds missing in staging) — callers should 503 in that
// case, never silently reroute.
func (r *Router) Pick(country string) (Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: router not configured", ErrNoProvider)
	}
	if IsAfrica(country) {
		if r.africa == nil {
			return nil, fmt.Errorf("%w: africa provider not configured (country=%s)", ErrNoProvider, country)
		}
		return r.africa, nil
	}
	if r.elsewhere == nil {
		return nil, fmt.Errorf("%w: default provider not configured (country=%s)", ErrNoProvider, country)
	}
	return r.elsewhere, nil
}

// Africa returns the African adapter (or nil when unset). Exposed for
// the dedicated webhook endpoint so the service can call
// VerifyWebhook/ParseWebhook without going through the router.
func (r *Router) Africa() Provider {
	if r == nil {
		return nil
	}
	return r.africa
}

// Elsewhere returns the non-African adapter (or nil when unset).
func (r *Router) Elsewhere() Provider {
	if r == nil {
		return nil
	}
	return r.elsewhere
}

// CountryFromRequest extracts the Cloudflare-supplied country for the
// caller's IP. CF-IPCountry is the authoritative header; we also
// accept a query-string override ?country=KE for local testing.
// Empty return means "unknown" — the router treats that as non-Africa.
func CountryFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	// Query override — useful in dev/staging where CF isn't in front.
	if v := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("country"))); v != "" {
		return v
	}
	if v := r.Header.Get("CF-IPCountry"); v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	// Fastly-style fallback; harmless elsewhere.
	if v := r.Header.Get("X-Country-Code"); v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

// ErrNoProvider is surfaced when a geography has no adapter wired.
// Handlers should map this to 503 — not an auth error, not a 400.
var ErrNoProvider = fmt.Errorf("no payment provider configured for this region")
