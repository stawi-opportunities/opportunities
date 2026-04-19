package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	billingv1 "buf.build/gen/go/antinvestor/billing/protocolbuffers/go/billing/v1"
	"github.com/pitabwire/frame/security"
	"github.com/pitabwire/util"

	"stawi.jobs/apps/candidates/config"
	"stawi.jobs/pkg/billing"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/services"
)

// buildBillingClient wires a billing.Client from the candidates
// config + the shared antinvestor Connect clients. Returns nil when
// mandatory settings are missing — callers treat nil as "billing not
// configured" and the checkout endpoint 503s with a clean error.
func buildBillingClient(cfg *config.CandidatesConfig, svcClients *services.Clients) *billing.Client {
	if svcClients == nil || svcClients.Billing == nil || svcClients.Payment == nil {
		return nil
	}
	return billing.NewClient(billing.ClientConfig{
		CatalogVersionID:   cfg.BillingCatalogVersionID,
		RecipientProfileID: cfg.BillingRecipientProfileID,
		SuccessURL:         strings.TrimRight(cfg.PublicSiteURL, "/") + "/dashboard/?billing=success",
		CancelURL:          strings.TrimRight(cfg.PublicSiteURL, "/") + "/pricing/?billing=cancelled",
		PolarProducts: map[billing.PlanID]string{
			billing.PlanStarter: cfg.PolarProductStarter,
			billing.PlanPro:     cfg.PolarProductPro,
			billing.PlanManaged: cfg.PolarProductManaged,
		},
	}, svcClients.Billing, svcClients.Payment)
}

// plansHandler returns the public plan catalog enriched with the
// per-country resolved price. The frontend hits this before rendering
// the pricing page so each user sees the amount they'll actually be
// charged.
func plansHandler() http.HandlerFunc {
	order := []billing.PlanID{billing.PlanStarter, billing.PlanPro, billing.PlanManaged}
	return func(w http.ResponseWriter, r *http.Request) {
		country := countryFromRequest(r)
		out := make([]map[string]any, 0, len(order))
		for _, id := range order {
			plan := billing.Catalog[id]
			amt, cur := plan.PriceFor(country)
			out = append(out, map[string]any{
				"id":          string(plan.ID),
				"name":        plan.Name,
				"description": plan.Description,
				"interval":    plan.Interval,
				"amount":      amt,
				"currency":    cur,
				"usd_cents":   plan.USDCents,
			})
		}
		route := billing.RouteForCountry(country, "")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"country": country,
			"route":   string(route),
			"plans":   out,
		})
	}
}

// checkoutHandler handles POST /billing/checkout.
//
// Body: { "plan_id": "starter|pro|managed", "email": "...", "phone": "+254...", "route_hint": "mpesa"|"card"|"" }
// Returns one of:
//
//	{ "status": "redirect", "redirect_url": "https://polar.sh/...", ... }
//	{ "status": "pending",  "prompt_id": "...", ... }   // caller polls /billing/checkout/status
//	{ "status": "failed",   "error": "..." }
//
// Auth required — profile_id comes from JWT. The candidate row is
// upserted with PlanID so the reconciler can flip state once the
// billing service confirms the charge.
func checkoutHandler(
	candidateRepo *repository.CandidateRepository,
	cli *billing.Client,
	cfg *config.CandidatesConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if cli == nil {
			log.Warn("checkout: billing client not wired")
			http.Error(w, `{"error":"billing not configured"}`, http.StatusServiceUnavailable)
			return
		}

		claims := security.ClaimsFromContext(ctx)
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		profileID := claims.GetProfileID()
		if profileID == "" {
			http.Error(w, `{"error":"no profile_id in claims"}`, http.StatusUnauthorized)
			return
		}

		var body struct {
			PlanID    string `json:"plan_id"`
			Email     string `json:"email"`
			Phone     string `json:"phone"`
			RouteHint string `json:"route_hint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
			return
		}
		plan, err := billing.LookupPlan(body.PlanID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}

		country := countryFromRequest(r)

		// Ensure the candidate row exists and records the intended
		// plan. The reconciler needs this hook to flip state once
		// the webhook says the payment succeeded.
		candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if candidate == nil {
			candidate = &domain.CandidateProfile{
				ProfileID:    profileID,
				Status:       domain.CandidateUnverified,
				Subscription: domain.SubscriptionFree,
				PlanID:       string(plan.ID),
			}
			if createErr := candidateRepo.Create(ctx, candidate); createErr != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, createErr.Error()), http.StatusInternalServerError)
				return
			}
		} else if candidate.PlanID != string(plan.ID) {
			candidate.PlanID = string(plan.ID)
			if updateErr := candidateRepo.Update(ctx, candidate); updateErr != nil {
				log.WithError(updateErr).Warn("checkout: could not persist pending plan id")
			}
		}

		result, err := cli.OpenCheckout(ctx, billing.CheckoutRequest{
			ProfileID: profileID,
			PlanID:    billing.PlanID(plan.ID),
			Country:   country,
			Phone:     body.Phone,
			Email:     body.Email,
			RouteHint: body.RouteHint,
		})

		// Stamp the subscription id onto the candidate row as soon
		// as CreateSubscription succeeds — even if InitiatePrompt
		// later fails, we want the row pointing at the right
		// service_billing record so CancelSubscription works and
		// the reconciler can GC dangling pending subs.
		if result.SubscriptionID != "" && candidate.SubscriptionID != result.SubscriptionID {
			candidate.SubscriptionID = result.SubscriptionID
			if updateErr := candidateRepo.Update(ctx, candidate); updateErr != nil {
				log.WithError(updateErr).Warn("checkout: could not persist subscription id")
			}
		}

		if err != nil {
			log.WithError(err).WithField("profile_id", profileID).
				Error("checkout: OpenCheckout failed")
			status := http.StatusBadGateway
			if errors.Is(err, billing.ErrNotConfigured) {
				status = http.StatusServiceUnavailable
			}
			if errors.Is(err, billing.ErrUnknownPlan) {
				status = http.StatusBadRequest
			}
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), status)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":          result.Status,
			"route":           string(result.Route),
			"redirect_url":    result.RedirectURL,
			"prompt_id":       result.PromptID,
			"subscription_id": result.SubscriptionID,
			"amount":          result.AmountCents,
			"currency":        result.Currency,
			"country":         country,
			"plan_id":         string(plan.ID),
			"error":           result.Error,
		})
	}
}

// checkoutStatusHandler handles GET /billing/checkout/status?prompt_id=…
// for long-poll scenarios — STK push routes don't produce a redirect
// URL, so the frontend polls this endpoint while the user completes
// the prompt on their phone. Also handles Polar's "session still
// being created" window.
func checkoutStatusHandler(
	candidateRepo *repository.CandidateRepository,
	cli *billing.Client,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if cli == nil {
			http.Error(w, `{"error":"billing not configured"}`, http.StatusServiceUnavailable)
			return
		}

		promptID := strings.TrimSpace(r.URL.Query().Get("prompt_id"))
		if promptID == "" {
			http.Error(w, `{"error":"prompt_id required"}`, http.StatusBadRequest)
			return
		}

		result, err := cli.PollPromptStatus(ctx, promptID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
			return
		}

		// When status is "paid" we synchronously reconcile the
		// candidate row so the UI sees updated state on the next
		// render, without waiting for the periodic reconciler.
		if result.Status == "paid" && result.SubscriptionID != "" {
			claims := security.ClaimsFromContext(ctx)
			if claims != nil {
				_ = reconcileSubscriptionForProfile(ctx, candidateRepo, cli, claims.GetProfileID(), result.SubscriptionID)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":          result.Status,
			"redirect_url":    result.RedirectURL,
			"subscription_id": result.SubscriptionID,
			"error":           result.Error,
		})
	}
}

// reconcileSubscriptionForProfile fetches a single subscription from
// service_billing and updates the candidate row accordingly. Used
// inline after a "paid" status and by the periodic reconciler.
func reconcileSubscriptionForProfile(
	ctx context.Context,
	candidateRepo *repository.CandidateRepository,
	cli *billing.Client,
	profileID, subscriptionID string,
) error {
	if profileID == "" || subscriptionID == "" {
		return nil
	}
	sub, err := cli.FetchSubscription(ctx, subscriptionID)
	if err != nil {
		return err
	}
	candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
	if err != nil || candidate == nil {
		return err
	}
	return applySubscriptionState(ctx, candidateRepo, candidate, sub)
}

// applySubscriptionState maps a service_billing Subscription onto
// the candidate row. Kept here so the reconciler, the sync path on
// checkout status, and any future webhook bridge all share one
// transition table.
func applySubscriptionState(
	ctx context.Context,
	candidateRepo *repository.CandidateRepository,
	candidate *domain.CandidateProfile,
	sub *billingv1.Subscription,
) error {
	if candidate == nil || sub == nil {
		return nil
	}
	changed := false

	// Keep the catalog plan id reflected on the candidate row.
	if sub.GetPlanId() != "" && candidate.PlanID != sub.GetPlanId() {
		candidate.PlanID = sub.GetPlanId()
		changed = true
	}
	if sub.GetId() != "" && candidate.SubscriptionID != sub.GetId() {
		candidate.SubscriptionID = sub.GetId()
		changed = true
	}

	switch sub.GetState() {
	case billingv1.SubscriptionState_SUBSCRIPTION_ACTIVE:
		if candidate.Subscription != domain.SubscriptionPaid {
			candidate.Subscription = domain.SubscriptionPaid
			changed = true
		}
		if !candidate.AutoApply {
			candidate.AutoApply = true
			changed = true
		}
	case billingv1.SubscriptionState_SUBSCRIPTION_CANCELLED,
		billingv1.SubscriptionState_SUBSCRIPTION_EXPIRED:
		if candidate.Subscription != domain.SubscriptionCancelled {
			candidate.Subscription = domain.SubscriptionCancelled
			changed = true
		}
		if candidate.AutoApply {
			candidate.AutoApply = false
			changed = true
		}
	case billingv1.SubscriptionState_SUBSCRIPTION_PENDING:
		// Keep the row as-is (Free or whatever it was before the
		// pending signal arrived). The reconciler will revisit
		// once service_billing flips it to ACTIVE / CANCELLED.
	}

	if !changed {
		return nil
	}
	return candidateRepo.Update(ctx, candidate)
}

// runBillingReconciler runs in the background and periodically syncs
// candidates with SubscriptionID != "" against service_billing. This
// gives us durable state reconciliation even if a webhook from
// service_payment → stawi is missed (network hiccup, pod restart
// during delivery window, etc.).
func runBillingReconciler(
	ctx context.Context,
	candidateRepo *repository.CandidateRepository,
	cli *billing.Client,
	interval time.Duration,
) {
	if cli == nil || interval <= 0 {
		return
	}
	log := util.Log(ctx).WithField("worker", "billing-reconciler")
	log.WithField("interval", interval).Info("starting")

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("reconciler stopping")
			return
		case <-tick.C:
			reconcileOneBatch(ctx, candidateRepo, cli)
		}
	}
}

// reconcileOneBatch fetches candidates that have a SubscriptionID but
// haven't yet transitioned to "paid" and asks service_billing for the
// authoritative state of each.  Non-fatal: per-candidate errors are
// logged and we move on.
func reconcileOneBatch(
	ctx context.Context,
	candidateRepo *repository.CandidateRepository,
	cli *billing.Client,
) {
	log := util.Log(ctx).WithField("worker", "billing-reconciler")
	// Bounded batch — we don't want a 10k-candidate sweep on every
	// tick. 200/tick at 30s = 24k/hour which comfortably handles
	// the forecast for MVP.
	candidates, err := candidateRepo.ListPendingSubscriptions(ctx, 200)
	if err != nil {
		log.WithError(err).Warn("list pending subs failed")
		return
	}
	if len(candidates) == 0 {
		return
	}
	for _, c := range candidates {
		if c.SubscriptionID == "" {
			continue
		}
		sub, err := cli.FetchSubscription(ctx, c.SubscriptionID)
		if err != nil {
			log.WithError(err).WithField("subscription_id", c.SubscriptionID).
				Debug("fetch subscription failed, will retry next tick")
			continue
		}
		if applyErr := applySubscriptionState(ctx, candidateRepo, c, sub); applyErr != nil {
			log.WithError(applyErr).WithField("candidate_id", c.ID).
				Warn("apply subscription state failed")
		}
	}
}

// countryFromRequest extracts Cloudflare's CF-IPCountry header plus
// a query-string override (?country=KE) for local-dev testing.
func countryFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("country"))); v != "" {
		return v
	}
	if v := r.Header.Get("CF-IPCountry"); v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	if v := r.Header.Get("X-Country-Code"); v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}
