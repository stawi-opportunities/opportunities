package v1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// --- GET /billing/plans (public) ---------------------------------------

// billingPlan mirrors ui/app/src/api/billing.ts BillingPlan.
// Amount is major units (10 = US$10); USDCents is minor units (1000 = US$10).
// Pricing pages must format with usd_cents/100 (or amount as-is) — never amount/100.
type billingPlan struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Interval    string `json:"interval"`
	Amount      int    `json:"amount"`
	Currency    string `json:"currency"`
	USDCents    int    `json:"usd_cents"`
}

// billingPlansResponse mirrors ui/app/src/api/billing.ts BillingPlansResponse.
type billingPlansResponse struct {
	Country string        `json:"country"`
	Route   string        `json:"route"`
	Plans   []billingPlan `json:"plans"`
}

// PlansHandler serves GET /billing/plans. Public (no auth) — the UI fetches
// it with credentials omitted. Returns the static plan catalog plus the
// payment route (always Flutterwave). country is sniffed from CF-IPCountry
// for display only.
func PlansHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		country := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
		plans := billing.Catalog()
		out := billingPlansResponse{
			Country: country,
			Route:   string(billing.RouteFlutterwave),
			Plans:   make([]billingPlan, 0, len(plans)),
		}
		for _, p := range plans {
			out.Plans = append(out.Plans, billingPlan{
				ID:          string(p.ID),
				Name:        p.Name,
				Description: p.Description,
				Interval:    p.Interval,
				Amount:      p.Amount,
				Currency:    p.Currency,
				USDCents:    p.USDCents,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// --- POST /billing/checkout (auth'd) -----------------------------------

// CheckoutDeps bundles what the checkout handler needs.
type CheckoutDeps struct {
	Gateway billing.Gateway
	// Store persists the pending checkout. nil → the handler still calls
	// the gateway but skips persistence (degraded: the reconciler/poller
	// can't see the checkout). Production always wires it.
	Store *billing.Store
	// Candidates is optional; when set, already-paid users are refused a
	// new checkout so the dashboard never re-prompts payment after success.
	Candidates CandidateProfileReader
}

// checkoutInput is the SPA body for POST /billing/checkout.
type checkoutInput struct {
	PlanID string `json:"plan_id"`
	Email  string `json:"email"`
	Phone  string `json:"phone"`
}

// checkoutResponse is the SPA envelope for POST /billing/checkout.
// Happy path: status=redirect + redirect_url → browser goes to Flutterwave.
type checkoutResponse struct {
	Status         string `json:"status"`
	Route          string `json:"route"`
	RedirectURL    string `json:"redirect_url"`
	PromptID       string `json:"prompt_id"`
	SubscriptionID string `json:"subscription_id"`
	PlanID         string `json:"plan_id"`
	Error          string `json:"error"`
}

// CheckoutHandler serves POST /billing/checkout (auth'd).
//
// Required steps:
//  1. Validate plan
//  2. Gateway.CreateCheckout (Flutterwave prompt + short-poll URL)
//  3. Persist checkout ledger row (best-effort)
//  4. Return {status, redirect_url, prompt_id} for the SPA
func CheckoutHandler(deps CheckoutDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		// Never start a second payment while the candidate is already paid.
		// Also resolve platform profile_id for checkout (contacts + au_name).
		profileID := candidateID
		candPhone := ""
		if deps.Candidates != nil {
			if cand, cerr := deps.Candidates.GetByID(ctx, candidateID); cerr == nil && cand != nil {
				if cand.Subscription == domain.SubscriptionPaid || cand.Subscription == domain.SubscriptionTrial {
					httpmw.ProblemJSON(w, http.StatusConflict, "already_subscribed",
						"you already have an active subscription; manage it under Billing")
					return
				}
				if p := strings.TrimSpace(cand.ProfileID); p != "" {
					profileID = p
				}
				candPhone = strings.TrimSpace(cand.Phone)
			}
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024))
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "body_read_failed", "could not read request body")
			return
		}
		var in checkoutInput
		if err := json.Unmarshal(body, &in); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
			return
		}

		plan, ok := billing.PlanByID(billing.PlanID(strings.ToLower(strings.TrimSpace(in.PlanID))))
		if !ok {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_plan", "plan must be one of: starter, pro, managed")
			return
		}

		// Prefer body hints; fall back to verified OIDC claims / profile phone.
		// Hosted checkout seeds empty profile Contact rows from these values
		// (never free-text on pay.stawi.org).
		email := strings.TrimSpace(in.Email)
		if email == "" {
			email = oidcEmailFromContext(ctx)
		}
		phone := strings.TrimSpace(in.Phone)
		if phone == "" {
			phone = candPhone
		}

		country := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
		res, err := deps.Gateway.CreateCheckout(ctx, billing.CheckoutRequest{
			CandidateID: candidateID,
			// Hosted checkout calls ProfileService.GetById(profileID) for
			// contacts[] and properties.au_name (service_authentication name).
			ProfileID: profileID,
			Plan:      plan,
			Country:   country,
			Email:     email,
			Phone:     phone,
		})
		if errors.Is(err, billing.ErrGatewayUnavailable) {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "billing_unavailable", "payment provider is not configured")
			return
		}
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).Error("billing/checkout: gateway create failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "checkout_failed", "could not start checkout")
			return
		}

		// Ledger powers webhook activation + status poll. Without a store row
		// activation is impossible — fail the request (never return redirect only).
		if res.PromptID != "" {
			if deps.Store == nil {
				log.WithField("prompt_id", res.PromptID).
					Error("billing/checkout: gateway ok but checkout store is nil")
				httpmw.ProblemJSON(w, http.StatusBadGateway, "checkout_store_unavailable",
					"could not record checkout — try again shortly")
				return
			}
			persistStatus := res.Status
			if persistStatus == billing.StatusRedirect {
				persistStatus = billing.StatusPending
			}
			if perr := deps.Store.Create(ctx, billing.Checkout{
				PromptID:       res.PromptID,
				CandidateID:    candidateID,
				PlanID:         string(plan.ID),
				Route:          string(res.Route),
				Status:         persistStatus,
				SubscriptionID: res.SubscriptionID,
				AmountCents:    int64(plan.USDCents),
				Currency:       plan.Currency,
				Country:        country,
				RedirectURL:    res.RedirectURL,
				Error:          res.Error,
			}); perr != nil {
				log.WithError(perr).WithField("prompt_id", res.PromptID).
					Error("billing/checkout: persist pending checkout failed")
				httpmw.ProblemJSON(w, http.StatusBadGateway, "checkout_persist_failed",
					"could not record checkout — try again shortly")
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(checkoutResponse{
			Status:         string(res.Status),
			Route:          string(res.Route),
			RedirectURL:    res.RedirectURL,
			PromptID:       res.PromptID,
			SubscriptionID: res.SubscriptionID,
			PlanID:         string(plan.ID),
			Error:          res.Error,
		})
	}
}

// oidcEmailFromContext reads the email claim from the already-verified JWT
// payload (AuthenticationClaims does not surface email as a first-class field).
// Empty when no JWT / claim present.
func oidcEmailFromContext(ctx context.Context) string {
	if claims := security.ClaimsFromContext(ctx); claims != nil && claims.Ext != nil {
		for _, key := range []string{"email", "preferred_username"} {
			if v, ok := claims.Ext[key].(string); ok {
				if e := strings.TrimSpace(v); e != "" && strings.Contains(e, "@") {
					return e
				}
			}
		}
	}
	raw := strings.TrimSpace(security.JwtFromContext(ctx))
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some issuers pad; try standard encoding.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	for _, key := range []string{"email", "preferred_username"} {
		if v, ok := m[key].(string); ok {
			if e := strings.TrimSpace(v); e != "" && strings.Contains(e, "@") {
				return e
			}
		}
	}
	if ext, ok := m["ext"].(map[string]any); ok {
		if v, ok := ext["email"].(string); ok {
			if e := strings.TrimSpace(v); e != "" && strings.Contains(e, "@") {
				return e
			}
		}
	}
	return ""
}

// --- GET /billing/checkout/status (auth'd) -----------------------------

// CheckoutStatusDeps bundles what the status poller needs.
type CheckoutStatusDeps struct {
	Gateway billing.Gateway
	Store   *billing.Store
	// Activator is optional. When set, a poll that observes a terminal
	// state drives activation inline, so a candidate who never receives a
	// webhook is activated the moment the dashboard polls a paid status.
	Activator *billing.Activator
}

// checkoutStatusResponse mirrors ui/app/src/api/billing.ts CheckoutStatusResponse.
type checkoutStatusResponse struct {
	Status         string `json:"status"`
	RedirectURL    string `json:"redirect_url"`
	SubscriptionID string `json:"subscription_id"`
	Error          string `json:"error"`
}

// CheckoutStatusHandler serves GET /billing/checkout/status?prompt_id=...
// Authenticated. It verifies the checkout belongs to the calling candidate
// (so one candidate can't poll another's payment), polls the gateway for
// the live status, and — when terminal and an Activator is wired — flips
// the subscription inline.
func CheckoutStatusHandler(deps CheckoutStatusDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		promptID := strings.TrimSpace(r.URL.Query().Get("prompt_id"))
		if promptID == "" {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "missing_prompt_id", "prompt_id query parameter is required")
			return
		}

		// Ownership is enforced via the stored checkout below, so the store is
		// REQUIRED: without it we'd poll the provider by a guessable prompt_id
		// with no owner check, leaking another candidate's checkout (IDOR).
		// Refuse rather than fall through. main.go also declines to register
		// this route when the store is unavailable.
		if deps.Store == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "status_unavailable", "checkout store not configured")
			return
		}

		// Resolve the stored checkout to enforce ownership + provide a
		// fallback status if the gateway is unavailable.
		var stored billing.Checkout
		haveStored := false
		if deps.Store != nil {
			c, err := deps.Store.GetByPromptID(ctx, promptID)
			switch {
			case errors.Is(err, billing.ErrNotFound):
				httpmw.ProblemJSON(w, http.StatusNotFound, "checkout_not_found", "no checkout for that prompt_id")
				return
			case err != nil:
				log.WithError(err).WithField("prompt_id", promptID).Error("billing/status: lookup failed")
				httpmw.ProblemJSON(w, http.StatusBadGateway, "checkout_lookup_failed", "could not load checkout")
				return
			}
			if c.CandidateID != candidateID {
				// Don't leak existence to other candidates.
				httpmw.ProblemJSON(w, http.StatusNotFound, "checkout_not_found", "no checkout for that prompt_id")
				return
			}
			stored, haveStored = c, true
		}

		st, err := deps.Gateway.CheckoutStatus(ctx, promptID)
		if err != nil {
			// Degrade to the stored status rather than 5xx — the UI keeps
			// polling and a transient provider blip shouldn't error the page.
			if haveStored {
				log.WithError(err).WithField("prompt_id", promptID).
					Warn("billing/status: gateway poll failed; serving stored status")
				writeStatus(w, checkoutStatusResponse{
					Status:         string(stored.Status),
					RedirectURL:    stored.RedirectURL,
					SubscriptionID: stored.SubscriptionID,
					Error:          stored.Error,
				})
				return
			}
			log.WithError(err).WithField("prompt_id", promptID).Error("billing/status: gateway poll failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "status_failed", "could not load checkout status")
			return
		}

		// Drive activation inline on a terminal status (idempotent).
		if deps.Activator != nil && (st.Status == billing.StatusPaid || st.Status == billing.StatusFailed) {
			if actErr := deps.Activator.Activate(ctx, promptID, st.Status, st.SubscriptionID, st.Error); actErr != nil {
				log.WithError(actErr).WithField("prompt_id", promptID).
					Warn("billing/status: inline activation failed (reconciler will retry)")
			}
		}

		redirect := st.RedirectURL
		if redirect == "" && haveStored {
			redirect = stored.RedirectURL
		}
		// Prefer provider subscription id; fall back to ledger.
		subID := st.SubscriptionID
		if subID == "" && haveStored {
			subID = stored.SubscriptionID
		}
		writeStatus(w, checkoutStatusResponse{
			Status:         string(st.Status),
			RedirectURL:    redirect,
			SubscriptionID: subID,
			Error:          st.Error,
		})
	}
}

func writeStatus(w http.ResponseWriter, resp checkoutStatusResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
