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
	// PlanPro is legacy — no longer sold. Kept so existing rows and tests
	// still normalize; entitlements map to Managed (auto-apply + unlimited).
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
	// Amount is the major-unit price for display (10 = US$10, 200 = US$200).
	// Callers must not treat this as cents — use USDCents for charge/display math.
	Amount int `json:"amount"`
	// Currency is the display/charge currency code.
	Currency string `json:"currency"`
	// USDCents is the authoritative charge amount in USD cents
	// (1000 = US$10, 20000 = US$200). Prefer this for any /100 formatting.
	USDCents int `json:"usd_cents"`
}

// catalog is the sellable catalog (two tiers). Mirrors ui/app/src/utils/plans.ts.
var catalog = []Plan{
	{
		ID: PlanStarter, Name: "Starter",
		Description: "AI-matched jobs and digests — review and apply yourself from the dashboard.",
		Interval:    "month", Amount: 10, Currency: "USD", USDCents: 1000,
	},
	{
		ID: PlanManaged, Name: "Managed",
		Description: "Unlimited AI discovery, priority match alerts, and uncapped match feed.",
		Interval:    "month", Amount: 200, Currency: "USD", USDCents: 20000,
	},
}

// Catalog returns the plan catalog in display order.
func Catalog() []Plan {
	out := make([]Plan, len(catalog))
	copy(out, catalog)
	return out
}

// PlanByID returns a sellable plan for id. Legacy "pro" is not returned
// (not sold); use EntitlementsFor for entitlement mapping of old rows.
func PlanByID(id PlanID) (Plan, bool) {
	for _, p := range catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Plan{}, false
}

// NormalizePlan trims + validates a raw plan string into a known sellable tier.
// Legacy "pro" maps to managed for checkout/upgrade paths.
func NormalizePlan(raw string) (PlanID, bool) {
	id := PlanID(strings.TrimSpace(strings.ToLower(raw)))
	if id == PlanPro {
		return PlanManaged, true
	}
	if _, ok := PlanByID(id); ok {
		return id, true
	}
	return "", false
}

// Entitlements are the server-enforced limits for a paid plan.
// Matches marketing copy in ui/app/src/utils/plans.ts.
type Entitlements struct {
	// DailyCap / WeeklyCap bound match generation (candidate_match_indexes).
	// WeeklyCap 0 means uncapped (managed).
	DailyCap  int
	WeeklyCap int
	// AutoApply is reserved for future employer-side automation.
	// All plans false until a reliable auto-apply product ships.
	AutoApply bool
	// Priority is a qualitative queue hint for future scheduling.
	Priority string
}

// EntitlementsFor returns server-side entitlements for a plan.
// Empty / unknown plan IDs get free-proof caps (value before pay).
// Legacy "pro" inherits Managed entitlements.
func EntitlementsFor(plan PlanID) Entitlements {
	switch plan {
	case PlanManaged, PlanPro:
		// Unlimited discovery + higher daily generation budget.
		// AutoApply remains false until a real apply automation product ships.
		return Entitlements{DailyCap: 50, WeeklyCap: 0, AutoApply: false, Priority: "agent"}
	case PlanStarter:
		return Entitlements{DailyCap: 2, WeeklyCap: 5, AutoApply: false, Priority: "standard"}
	default:
		// Free proof: enough matches to prove quality before checkout.
		return Entitlements{DailyCap: 1, WeeklyCap: 3, AutoApply: false, Priority: "proof"}
	}
}

// EntitlementsForProfile maps subscription + plan_id to caps.
// Free/cancelled/empty subscription always get free-proof caps even if
// onboard stored a sellable plan_id (value-before-pay honesty).
func EntitlementsForProfile(subscription, planID string) Entitlements {
	switch strings.ToLower(strings.TrimSpace(subscription)) {
	case "paid", "past_due", "trial":
		if strings.TrimSpace(planID) == "" {
			return EntitlementsFor(PlanStarter)
		}
		return EntitlementsFor(PlanID(planID))
	default:
		return EntitlementsFor("")
	}
}
