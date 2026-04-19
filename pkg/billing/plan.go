// Package billing wires stawi.jobs to the antinvestor payment + billing
// services so that subscription checkouts flow:
//
//	stawi.jobs  →  service_billing.CreateSubscription (PENDING)
//	           →  service_payment.InitiatePrompt      (queued)
//	           →  payment-service integration (Polar / M-PESA / AIRTEL / MTN)
//	           →  user completes payment at provider
//	           →  provider webhook → service_payment handlers update prompt
//	           →  billing reconciler in stawi promotes SubscriptionPaid
//
// We do NOT talk to Polar.sh or DusuPay directly — all provider
// integrations live inside service-payment's per-provider apps
// (see /home/j/code/antinvestor/service-payment/apps/integrations).
// This package is a thin orchestrator over paymentv1 + billingv1
// Connect RPCs plus a local plan catalog for the pricing page.
package billing

import (
	"errors"
	"fmt"
	"strings"
)

// PlanID identifies a subscription tier. The strings double as the
// external_id recorded on service_billing's Plan rows (see
// docs/billing/provisioning.md for the one-time catalog bootstrap).
type PlanID string

const (
	PlanStarter PlanID = "starter"
	PlanPro     PlanID = "pro"
	PlanManaged PlanID = "managed"
)

// Plan is a billable tier, used by the pricing page and /billing/plans.
// Field semantics mirror what service_billing stores — this struct is
// the local cache so the Hugo site can render prices without a RPC
// round-trip per page load.
type Plan struct {
	ID          PlanID
	Name        string
	Description string
	// USDCents is the canonical price in US-dollar cents. Used as
	// the charge amount in service_payment.InitiatePrompt — the
	// payment service converts to the rail-appropriate currency
	// when it routes to M-PESA / AIRTEL / MTN.
	USDCents int64
	// Interval is the billing cadence; only "month" is live today.
	Interval string
	// PolarProductID is the Polar.sh product this plan maps to.
	// Populated from env (POLAR_PRODUCT_ID_<TIER>) at boot because
	// product ids are per-environment.  Empty in tests.
	PolarProductID string
	// LocalPrices is a display-only hint (ISO-4217 → cents) used by
	// /billing/plans to show African users a local-currency line.
	// The actual charge amount is always USDCents — service_payment
	// handles FX at the integration layer.
	LocalPrices map[string]int64
}

// Catalog is the canonical plan set. Keep these IDs in sync with:
//   - ui/layouts/partials/pricing-cards.html  (button destinations)
//   - ui/app/src/utils/plans.ts                (frontend plan meta)
//   - apps/candidates/cmd/main.go              (onboarding form plan field)
//   - service_billing Plan rows (external_id)   (cluster bootstrap)
var Catalog = map[PlanID]Plan{
	PlanStarter: {
		ID:          PlanStarter,
		Name:        "Starter",
		Description: "Five AI-matched jobs a week, delivered.",
		USDCents:    1000,
		Interval:    "month",
		LocalPrices: map[string]int64{
			"KES": 150000,
			"UGX": 3800000,
			"NGN": 1500000,
			"ZAR": 18000,
			"GHS": 15000,
			"TZS": 2600000,
			"RWF": 1400000,
		},
	},
	PlanPro: {
		ID:          PlanPro,
		Name:        "Pro",
		Description: "Priority queue, cover-letter drafts, unlimited ATS reports.",
		USDCents:    5000,
		Interval:    "month",
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
		Interval:    "month",
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

// LookupPlan resolves a plan-id string (case-insensitive) to a Plan.
// Unknown / "free" ids return ErrUnknownPlan.
func LookupPlan(id string) (Plan, error) {
	normalised := PlanID(strings.ToLower(strings.TrimSpace(id)))
	plan, ok := Catalog[normalised]
	if !ok {
		return Plan{}, fmt.Errorf("%w: %q", ErrUnknownPlan, id)
	}
	return plan, nil
}

// PriceFor returns the display price (amount in minor units, currency)
// for a given country. Africa → local currency when the plan has a
// column for it; everywhere else → USD. The *charge* is always USD —
// this function only feeds the pricing page display.
func (p Plan) PriceFor(country string) (amountMinorUnits int64, currency string) {
	if cur, ok := MobileMoneyCurrency(country); ok {
		if v, has := p.LocalPrices[cur]; has {
			return v, cur
		}
	}
	return p.USDCents, "USD"
}

// ErrUnknownPlan is returned by LookupPlan for unrecognised ids. HTTP
// handlers map this to 400.
var ErrUnknownPlan = errors.New("unknown plan")
