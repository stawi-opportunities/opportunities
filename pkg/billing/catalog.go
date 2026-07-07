// Package billing implements the candidate-facing payments + subscription
// surface for the matching service: the plan catalog, checkout creation
// against the antinvestor payment service, a checkout ledger, and the
// activation path that flips candidate_profiles.subscription free→paid on
// a confirmed payment.
//
// The package depends on a small internal Gateway interface rather than
// the antinvestor Connect client directly, so the handlers + reconciler
// are unit-testable without a live payment provider. The production
// Gateway (NewPaymentGateway) wraps the real PaymentServiceClient.
package billing

import "strings"

// PlanID is the candidate-facing subscription tier. Mirrors
// ui/app/src/utils/plans.ts PlanId — there is no "free" tier; "free" on a
// candidate row means "has not paid yet".
type PlanID string

const (
	PlanStarter PlanID = "starter"
	PlanPro     PlanID = "pro"
	PlanManaged PlanID = "managed"
)

// Plan is one catalog entry. The amounts are the source of truth the
// checkout handler charges against — the UI's plans.ts prices are display
// copy and must agree with these, but the server never trusts a
// client-supplied amount.
type Plan struct {
	ID          PlanID `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Interval    string `json:"interval"`
	// Amount is the major-unit price (e.g. 10 for $10) for display.
	Amount int `json:"amount"`
	// Currency is the display/charge currency code.
	Currency string `json:"currency"`
	// USDCents is the authoritative charge amount in USD cents.
	USDCents int `json:"usd_cents"`
}

// catalog mirrors ui/app/src/utils/plans.ts. Kept here (not proxied from
// service-billing) because service-billing's catalog API is a versioned
// CreateCatalogVersion/Plan/Tier model with no simple "list plans for a
// pricing page" RPC; this product owns its three fixed tiers. If/when the
// catalog moves to service-billing, swap Catalog() for a proxy.
var catalog = []Plan{
	{ID: PlanStarter, Name: "Starter", Description: "Five AI-matched jobs a week, delivered.", Interval: "month", Amount: 10, Currency: "USD", USDCents: 1000},
	{ID: PlanPro, Name: "Pro", Description: "5× the matches. Priority queue. Better tools.", Interval: "month", Amount: 50, Currency: "USD", USDCents: 5000},
	{ID: PlanManaged, Name: "Managed", Description: "A real person running your job search.", Interval: "month", Amount: 200, Currency: "USD", USDCents: 20000},
}

// Catalog returns the plan catalog in display order.
func Catalog() []Plan {
	out := make([]Plan, len(catalog))
	copy(out, catalog)
	return out
}

// PlanByID returns the plan for id and whether it is a known tier.
func PlanByID(id PlanID) (Plan, bool) {
	for _, p := range catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Plan{}, false
}

// NormalizePlan trims + validates a raw plan string into a known tier.
// Anything outside the current paid catalog returns false.
func NormalizePlan(raw string) (PlanID, bool) {
	id := PlanID(strings.TrimSpace(strings.ToLower(raw)))
	if _, ok := PlanByID(id); ok {
		return id, true
	}
	return "", false
}
