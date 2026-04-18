// Package billing embeds subscription payment flows for stawi.jobs.
//
// Architecture:
//
//   - Plan catalog — a fixed set of tiers (starter/pro/managed) priced
//     in USD with local-currency overrides for African mobile-money
//     rails.
//   - Provider interface — the payment-agnostic contract every adapter
//     implements: CreateCheckout, ParseWebhook, VerifyWebhook.
//   - Router — picks an adapter per request based on the
//     Cloudflare-supplied CF-IPCountry header. Africa → DusuPay,
//     everywhere else → Plar.sh.
//   - Adapters — DusuPay (server-side redirect flow, direct REST) and
//     Plar.sh (hosted-checkout redirect, direct REST).
//
// All adapters emit a normalised WebhookEvent that the candidates
// service maps to the existing billing webhook contract
// {profile_id, status, plan_id, subscription_id}.
package billing

import (
	"errors"
	"fmt"
	"strings"
)

// PlanID identifies a subscription tier.
type PlanID string

const (
	PlanStarter PlanID = "starter"
	PlanPro     PlanID = "pro"
	PlanManaged PlanID = "managed"
)

// Plan is a billable tier in the catalog.
type Plan struct {
	ID          PlanID
	Name        string
	Description string
	// USD is the canonical price in US dollar cents. All non-Africa
	// checkouts bill in USD; Africa uses the LocalPrices map below.
	USDCents int64
	// Interval is the billing cadence — currently only "monthly" is
	// supported but the field lets us add annual tiers later without
	// breaking the adapter contract.
	Interval string
	// LocalPrices is an ISO-4217 currency -> cents map used when
	// routing through DusuPay's mobile-money rails. An entry here
	// lets us bill in KES/UGX/NGN etc. instead of forcing USD FX.
	LocalPrices map[string]int64
}

// Catalog is the canonical plan set. Keep these IDs in sync with:
//   - ui/layouts/partials/pricing-cards.html  (button destinations)
//   - ui/app/src/utils/plans.ts                (frontend plan meta)
//   - apps/candidates/cmd/main.go              (onboarding form plan field)
var Catalog = map[PlanID]Plan{
	PlanStarter: {
		ID:          PlanStarter,
		Name:        "Starter",
		Description: "Five AI-matched jobs a week, delivered.",
		USDCents:    1000,
		Interval:    "monthly",
		LocalPrices: map[string]int64{
			"KES": 150000,  // ~KSh 1,500
			"UGX": 3800000, // ~USh 38,000
			"NGN": 1500000, // ~N 15,000
			"ZAR": 18000,   // ~R 180
			"GHS": 15000,   // ~GH¢ 150
			"TZS": 2600000, // ~TSh 26,000
			"RWF": 1400000, // ~RF 14,000
		},
	},
	PlanPro: {
		ID:          PlanPro,
		Name:        "Pro",
		Description: "Priority queue, cover-letter drafts, unlimited ATS reports.",
		USDCents:    5000,
		Interval:    "monthly",
		LocalPrices: map[string]int64{
			"KES": 750000,
			"UGX": 19000000,
			"NGN": 7500000,
			"ZAR": 90000,
			"GHS": 75000,
			"TZS": 13000000,
			"RWF": 7000000,
		},
	},
	PlanManaged: {
		ID:          PlanManaged,
		Name:        "Managed",
		Description: "A dedicated recruiter runs your search end-to-end.",
		USDCents:    20000,
		Interval:    "monthly",
		LocalPrices: map[string]int64{
			"KES": 3000000,
			"UGX": 76000000,
			"NGN": 30000000,
			"ZAR": 360000,
			"GHS": 300000,
			"TZS": 52000000,
			"RWF": 28000000,
		},
	},
}

// LookupPlan resolves a plan ID string (case-insensitive) to a Plan.
// Returns an error for unknown or free plans — callers that allow
// "free" should short-circuit before calling this.
func LookupPlan(id string) (Plan, error) {
	normalised := PlanID(strings.ToLower(strings.TrimSpace(id)))
	plan, ok := Catalog[normalised]
	if !ok {
		return Plan{}, fmt.Errorf("unknown plan: %q", id)
	}
	return plan, nil
}

// PriceFor returns the price (amount, currency) to charge a user in
// the given country. African countries supported by DusuPay's mobile-
// money rails get billed in their local currency; everyone else
// defaults to USD.
func (p Plan) PriceFor(country string) (amountCents int64, currency string) {
	if cur, ok := MobileMoneyCurrency(country); ok {
		if v, has := p.LocalPrices[cur]; has {
			return v, cur
		}
	}
	return p.USDCents, "USD"
}

// ErrUnknownPlan is returned by LookupPlan when the id doesn't match
// the catalog. Exported so HTTP handlers can map it to 400.
var ErrUnknownPlan = errors.New("unknown plan")
