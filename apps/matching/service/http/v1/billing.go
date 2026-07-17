package v1

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

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
		if deps.Candidates != nil {
			if cand, cerr := deps.Candidates.GetByID(ctx, candidateID); cerr == nil && cand != nil {
				if cand.Subscription == domain.SubscriptionPaid || cand.Subscription == domain.SubscriptionTrial {
					httpmw.ProblemJSON(w, http.StatusConflict, "already_subscribed",
						"you already have an active subscription; manage it under Billing")
					return
				}
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

		country := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
		res, err := deps.Gateway.CreateCheckout(ctx, billing.CheckoutRequest{
			CandidateID: candidateID,
			Plan:        plan,
			Country:     country,
			Email:       in.Email,
			Phone:       in.Phone,
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

		// Ledger powers webhook activation + status poll. Persist failure after
		// a successful gateway create must fail the HTTP request so users are
		// never left "paid but free" with no row to activate.
		if res.PromptID != "" {
			if deps.Store == nil {
				log.WithField("prompt_id", res.PromptID).
					Error("billing/checkout: gateway ok but checkout store is nil — activation will fail")
			} else {
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
