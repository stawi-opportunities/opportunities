package v1

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// BillingLifecycleDeps powers cancel / change-plan / history.
type BillingLifecycleDeps struct {
	Candidates *repository.CandidateRepository
	Store      *billing.Store
}

// --- POST /billing/cancel -----------------------------------------------

type cancelInput struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

type cancelResponse struct {
	Success       bool   `json:"success"`
	EffectiveDate string `json:"effective_date"`
}

// CancelHandler schedules cancel-at-period-end. Access stays active until
// current_period_end; a reconciler/cron flips to cancelled after that.
func CancelHandler(deps BillingLifecycleDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		candidateID := httpmw.CandidateFromContext(ctx)
		if deps.Candidates == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "unavailable", "subscription store unavailable")
			return
		}

		// Reason is optional analytics — parse best-effort.
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8*1024))
		var in cancelInput
		_ = json.Unmarshal(body, &in)
		if in.Reason != "" {
			util.Log(ctx).WithField("candidate_id", candidateID).
				WithField("reason", in.Reason).
				Info("billing/cancel: scheduled")
		}

		end, err := deps.Candidates.ScheduleCancelAtPeriodEnd(ctx, candidateID)
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusConflict, "cancel_failed", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cancelResponse{
			Success:       true,
			EffectiveDate: end.UTC().Format(time.RFC3339),
		})
	}
}

// --- POST /billing/change-plan ------------------------------------------

type changePlanInput struct {
	PlanID string `json:"plan_id"`
}

type changePlanResponse struct {
	Success        bool    `json:"success"`
	NewPlan        string  `json:"new_plan"`
	ProratedAmount float64 `json:"prorated_amount"`
	ProratedCredit float64 `json:"prorated_credit"`
	NextBilling    string  `json:"next_billing"`
}

// ChangePlanHandler handles same-tier plan metadata updates for paid users.
// Upgrades that raise price (e.g. starter → managed) must go through checkout —
// free entitlement upgrades without payment are rejected.
func ChangePlanHandler(deps BillingLifecycleDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		candidateID := httpmw.CandidateFromContext(ctx)
		if deps.Candidates == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "unavailable", "subscription store unavailable")
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 8*1024))
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "body_read_failed", "could not read body")
			return
		}
		var in changePlanInput
		if err := json.Unmarshal(body, &in); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "invalid JSON")
			return
		}
		planID, ok := billing.NormalizePlan(in.PlanID)
		if !ok {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_plan", "plan must be starter, pro, or managed")
			return
		}

		cand, err := deps.Candidates.GetByID(ctx, candidateID)
		if err != nil || cand == nil {
			httpmw.ProblemJSON(w, http.StatusBadGateway, "candidate_lookup_failed", "could not load candidate")
			return
		}
		if cand.Subscription != domain.SubscriptionPaid {
			httpmw.ProblemJSON(w, http.StatusConflict, "not_subscribed", "active subscription required to change plan")
			return
		}
		if strings.EqualFold(cand.PlanID, string(planID)) {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "same_plan", "already on that plan")
			return
		}
		// Block free upgrades: starter → managed must use checkout until proration exists.
		cur, _ := billing.NormalizePlan(cand.PlanID)
		if cur == billing.PlanStarter && (planID == billing.PlanManaged || planID == billing.PlanPro) {
			httpmw.ProblemJSON(w, http.StatusPaymentRequired, "checkout_required",
				"upgrade to Managed requires a new checkout — open Billing and pay for Managed")
			return
		}

		if err := deps.Candidates.ChangePlan(ctx, candidateID, string(planID), true); err != nil {
			httpmw.ProblemJSON(w, http.StatusConflict, "change_failed", err.Error())
			return
		}

		next := time.Now().UTC().AddDate(0, 1, 0)
		if cand.CurrentPeriodEnd != nil && cand.CurrentPeriodEnd.After(time.Now().UTC()) {
			next = cand.CurrentPeriodEnd.UTC()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(changePlanResponse{
			Success:     true,
			NewPlan:     string(planID),
			NextBilling: next.Format(time.RFC3339),
		})
	}
}

// --- GET /billing/invoices  (subscription payment history) --------------

type invoiceItem struct {
	ID       string  `json:"id"`
	Date     string  `json:"date"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Status   string  `json:"status"` // paid | pending | failed
	PlanID   string  `json:"plan_id,omitempty"`
}

// InvoicesHandler lists the candidate's checkout ledger as payment history.
func InvoicesHandler(deps BillingLifecycleDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		candidateID := httpmw.CandidateFromContext(ctx)
		if deps.Store == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]invoiceItem{})
			return
		}
		rows, err := deps.Store.ListByCandidate(ctx, candidateID, 50)
		if err != nil {
			util.Log(ctx).WithError(err).Warn("billing/invoices: list failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "list_failed", "could not load history")
			return
		}
		out := make([]invoiceItem, 0, len(rows))
		for _, c := range rows {
			st := string(c.Status)
			switch st {
			case string(billing.StatusPaid):
				st = "paid"
			case string(billing.StatusFailed):
				st = "failed"
			default:
				st = "pending"
			}
			amount := float64(c.AmountCents) / 100.0
			out = append(out, invoiceItem{
				ID:       c.PromptID,
				Date:     c.CreatedAt.UTC().Format(time.RFC3339),
				Amount:   amount,
				Currency: firstNonEmptyStr(c.Currency, "USD"),
				Status:   st,
				PlanID:   c.PlanID,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// --- GET /billing/usage-history ----------------------------------------

// UsageHistoryHandler returns a minimal weekly usage series for the billing panel.
// When match metrics are unavailable, returns an empty list (UI handles empty).
func UsageHistoryHandler(matches MatchSummarizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		candidateID := httpmw.CandidateFromContext(ctx)
		type week struct {
			Week      string `json:"week"`
			Delivered int    `json:"delivered"`
			Queued    int    `json:"queued"`
		}
		out := []week{}
		if matches != nil {
			queued, delivered, err := matches.SubscriptionSummary(ctx, candidateID)
			if err == nil {
				// Single current-week snapshot until we store weekly history.
				out = append(out, week{
					Week:      time.Now().UTC().Format("2006-01-02"),
					Delivered: delivered,
					Queued:    queued,
				})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
