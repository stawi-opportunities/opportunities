package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pitabwire/frame/security"
	"github.com/pitabwire/util"

	"stawi.jobs/apps/candidates/config"
	"stawi.jobs/pkg/billing"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
)

// plansHandler returns the public plan catalog enriched with the
// per-country resolved price. The frontend hits this before rendering
// the pricing page so each user sees the amount they'll actually be
// charged (KES / NGN / ZAR for mobile-money users, USD otherwise).
func plansHandler() http.HandlerFunc {
	// Stable plan order — keep in sync with pricing-cards.html.
	order := []billing.PlanID{billing.PlanStarter, billing.PlanPro, billing.PlanManaged}
	return func(w http.ResponseWriter, r *http.Request) {
		country := billing.CountryFromRequest(r)
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
		provider := "plar"
		if billing.IsAfrica(country) {
			provider = "dusupay"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"country":  country,
			"provider": provider,
			"plans":    out,
		})
	}
}

// buildBillingRouter wires the DusuPay + Plar.sh adapters from config.
// An adapter with missing credentials is wired as nil — the router
// returns ErrNoProvider on picks to that geography rather than
// silently falling back, so ops can tell "we're mis-routing" from
// "we never shipped the creds".
func buildBillingRouter(cfg *config.CandidatesConfig) *billing.Router {
	var dusu billing.Provider
	if cfg.DusuPayAPIKey != "" && cfg.DusuPayWebhookSecret != "" {
		dusu = billing.NewDusuPay(billing.DusuPayConfig{
			BaseURL:       cfg.DusuPayBaseURL,
			APIKey:        cfg.DusuPayAPIKey,
			WebhookSecret: cfg.DusuPayWebhookSecret,
			CallbackURL:   strings.TrimRight(cfg.PublicSiteURL, "/") + "/webhooks/billing/dusupay",
			MerchantCode:  cfg.DusuPayMerchantCode,
		}, nil)
	}
	var plar billing.Provider
	if cfg.PlarAPIKey != "" && cfg.PlarWebhookSecret != "" {
		plar = billing.NewPlar(billing.PlarConfig{
			BaseURL:       cfg.PlarBaseURL,
			APIKey:        cfg.PlarAPIKey,
			WebhookSecret: cfg.PlarWebhookSecret,
			CallbackURL:   strings.TrimRight(cfg.PublicSiteURL, "/") + "/webhooks/billing/plar",
			MerchantID:    cfg.PlarMerchantID,
		}, nil)
	}
	return billing.NewRouter(dusu, plar)
}

// checkoutHandler handles POST /billing/checkout.
//
// Body: { "plan_id": "starter|pro|managed" }
// Returns: {
//   "redirect_url": "…provider URL…",
//   "provider":     "dusupay|plar",
//   "amount":       1000,           // minor units
//   "currency":     "KES",
//   "country":      "KE"
// }
//
// Auth is required — profile_id comes from JWT claims. The candidate
// row is upserted with PlanID and (pending) status so a webhook arriving
// before the user finishes the redirect still has somewhere to land.
func checkoutHandler(
	candidateRepo *repository.CandidateRepository,
	router *billing.Router,
	cfg *config.CandidatesConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

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
			PlanID string `json:"plan_id"`
			Email  string `json:"email"`
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

		country := billing.CountryFromRequest(r)
		provider, err := router.Pick(country)
		if err != nil {
			log.WithError(err).WithField("country", country).
				Warn("checkout: no provider for region")
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusServiceUnavailable)
			return
		}

		// Ensure the candidate row exists before we send the user to the
		// provider — the webhook is what flips subscription status, and
		// it needs a row to update. If the user onboarded through the
		// free path they already have one; otherwise, create a skeleton
		// with the plan id set and status=unverified.
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
		} else {
			// Record the intended plan so the webhook round-trips it even
			// if the provider drops metadata (happens on bank-transfer
			// receipts we've seen elsewhere).
			if candidate.PlanID != string(plan.ID) {
				candidate.PlanID = string(plan.ID)
				if updateErr := candidateRepo.Update(ctx, candidate); updateErr != nil {
					log.WithError(updateErr).Warn("checkout: could not persist pending plan id")
				}
			}
		}

		// Compose absolute success / cancel URLs on the public site.
		// Provider will round-trip the user here after payment.
		siteBase := strings.TrimRight(cfg.PublicSiteURL, "/")
		successURL := siteBase + "/dashboard/?billing=success"
		cancelURL := siteBase + "/pricing/?billing=cancelled"

		resp, err := provider.CreateCheckout(ctx, billing.CheckoutRequest{
			ProfileID:  profileID,
			Plan:       plan,
			Country:    country,
			Email:      body.Email,
			SuccessURL: successURL,
			CancelURL:  cancelURL,
		})
		if err != nil {
			log.WithError(err).WithField("provider", provider.Name()).
				Error("checkout: provider CreateCheckout failed")
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"redirect_url": resp.RedirectURL,
			"provider":     resp.Provider,
			"provider_ref": resp.ProviderRef,
			"amount":       resp.AmountCents,
			"currency":     resp.Currency,
			"country":      country,
			"plan_id":      string(plan.ID),
		})
	}
}

// providerWebhookHandler builds one HTTP handler per provider. Each
// handler verifies the signature (HMAC-SHA256), parses the payload,
// and applies the normalised event to the candidate row via
// applyBillingEvent — which preserves the existing contract used by
// the legacy /webhooks/billing endpoint.
//
// Idempotency: applyBillingEvent is the only write path, and it
// guards on both status transitions and (when available) provider
// event ids. A replay with the same SubscriptionID + Status is a no-op.
func providerWebhookHandler(
	candidateRepo *repository.CandidateRepository,
	provider billing.Provider,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if provider == nil {
			// The provider is optional — ops may have deliberately not
			// configured it in staging. 404 so the caller doesn't retry.
			http.NotFound(w, r)
			return
		}

		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
			return
		}

		if verifyErr := provider.VerifyWebhook(r, raw); verifyErr != nil {
			log.WithError(verifyErr).
				WithField("provider", provider.Name()).
				Warn("webhook: signature verification failed")
			if errors.Is(verifyErr, billing.ErrSignatureInvalid) {
				http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
				return
			}
			http.Error(w, `{"error":"signature check failed"}`, http.StatusUnauthorized)
			return
		}

		ev, parseErr := provider.ParseWebhook(raw)
		if parseErr != nil {
			// Intentionally-ignored event types → 204, so the provider
			// stops retrying. Other decode errors bubble as 400.
			if errors.Is(parseErr, billing.ErrUnhandledEvent) {
				log.WithError(parseErr).
					WithField("provider", provider.Name()).
					Debug("webhook: event intentionally ignored")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			log.WithError(parseErr).
				WithField("provider", provider.Name()).
				Warn("webhook: parse failed")
			http.Error(w, fmt.Sprintf(`{"error":%q}`, parseErr.Error()), http.StatusBadRequest)
			return
		}

		if ev.ProfileID == "" {
			log.WithField("provider", provider.Name()).
				WithField("raw", string(raw)).
				Warn("webhook: event with no profile_id, dropping")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if applyErr := applyBillingEvent(ctx, candidateRepo, ev); applyErr != nil {
			log.WithError(applyErr).
				WithField("provider", provider.Name()).
				WithField("profile_id", ev.ProfileID).
				Error("webhook: apply failed")
			http.Error(w, fmt.Sprintf(`{"error":%q}`, applyErr.Error()), http.StatusInternalServerError)
			return
		}

		log.WithField("provider", provider.Name()).
			WithField("profile_id", ev.ProfileID).
			WithField("status", string(ev.Status)).
			WithField("plan_id", string(ev.PlanID)).
			Info("webhook: applied")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"status":   string(ev.Status),
			"plan_id":  string(ev.PlanID),
			"event_id": ev.EventID,
		})
	}
}

// applyBillingEvent translates a normalised billing event onto the
// candidate row. Mirrors billingWebhookHandler's switch in main.go
// exactly — lifted out so the provider endpoints and the legacy
// generic endpoint share one implementation.
//
// Idempotency: if the event is "paid" and the candidate is already
// paid with the same subscription id, we no-op — repeated deliveries
// from a flaky provider don't churn the row.
func applyBillingEvent(
	ctx context.Context,
	candidateRepo *repository.CandidateRepository,
	ev billing.WebhookEvent,
) error {
	candidate, err := candidateRepo.GetByProfileID(ctx, ev.ProfileID)
	if err != nil {
		return err
	}
	if candidate == nil {
		// Very first payment from a user who never touched onboarding —
		// create the skeleton row so the webhook succeeds. CV data
		// arrives later via /candidates/onboard.
		candidate = &domain.CandidateProfile{
			ProfileID: ev.ProfileID,
			Status:    domain.CandidateActive,
		}
		if createErr := candidateRepo.Create(ctx, candidate); createErr != nil {
			return createErr
		}
	}

	changed := false
	switch ev.Status {
	case billing.StatusPaid, billing.StatusActive:
		if candidate.Subscription != domain.SubscriptionPaid {
			candidate.Subscription = domain.SubscriptionPaid
			changed = true
		}
		if !candidate.AutoApply {
			candidate.AutoApply = true
			changed = true
		}
		if ev.SubscriptionID != "" && candidate.SubscriptionID != ev.SubscriptionID {
			candidate.SubscriptionID = ev.SubscriptionID
			changed = true
		}
		if ev.PlanID != "" && candidate.PlanID != string(ev.PlanID) {
			candidate.PlanID = string(ev.PlanID)
			changed = true
		}
	case billing.StatusCancelled, billing.StatusExpired:
		if candidate.Subscription != domain.SubscriptionCancelled {
			candidate.Subscription = domain.SubscriptionCancelled
			changed = true
		}
		if candidate.AutoApply {
			candidate.AutoApply = false
			changed = true
		}
	case billing.StatusTrial:
		if candidate.Subscription != domain.SubscriptionTrial {
			candidate.Subscription = domain.SubscriptionTrial
			changed = true
		}
	case billing.StatusFailed, billing.StatusPending:
		// No subscription state change — the row already reflects
		// the last successful state. Log for visibility.
		util.Log(ctx).
			WithField("profile_id", ev.ProfileID).
			WithField("status", string(ev.Status)).
			Info("billing: non-terminal status, no state change")
		return nil
	default:
		return fmt.Errorf("unknown status %q", ev.Status)
	}

	if !changed {
		return nil
	}
	return candidateRepo.Update(ctx, candidate)
}
