package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/cv"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
)

// ATSReportDeps wires paid ATS report checkout + fulfillment.
type ATSReportDeps struct {
	DB      *sql.DB
	Scorer  *cv.Scorer
	Gateway billing.Gateway
	Store   *billing.Store
	Matches *matching.Store
	Notify  notificationv1connect.NotificationServiceClient
	// Template is the service-notification template for the report email.
	// Empty → DefaultTemplateATSReport.
	Template string
	// PublicSiteURL for return links in email.
	PublicSiteURL string
}

// DefaultTemplateATSReport is the notification template id for the paid report.
const DefaultTemplateATSReport = "template.opportunities.cv.ats_report"

// ATSReportCheckoutHandler serves POST /me/tools/ats-report.
// Starts a $2 one-time hosted checkout; after payment, fulfillment emails
// a comprehensive ATS report vs the candidate's matched preference jobs.
func ATSReportCheckoutHandler(deps ATSReportDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		if deps.Gateway == nil || deps.Store == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "billing_unavailable",
				"checkout is not configured")
			return
		}

		// Require some CV signal before charging.
		cvText, _ := loadCVForScore(ctx, deps.DB, candidateID)
		if len(strings.TrimSpace(cvText)) < 40 {
			httpmw.ProblemJSON(w, http.StatusConflict, "cv_required",
				"upload or complete your CV on the Details tab before purchasing a report")
			return
		}

		product := billing.ATSReportProduct()
		country := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
		res, err := deps.Gateway.CreateCheckout(ctx, billing.CheckoutRequest{
			CandidateID: candidateID,
			ProfileID:   candidateID,
			Plan:        product,
			Country:     country,
		})
		if err != nil {
			log.WithError(err).Error("ats-report: create checkout failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "checkout_failed", "could not start checkout")
			return
		}
		if res.PromptID == "" {
			httpmw.ProblemJSON(w, http.StatusBadGateway, "checkout_failed",
				strings.TrimSpace(res.Error)+" — could not start checkout")
			return
		}

		persistStatus := res.Status
		if persistStatus == billing.StatusRedirect {
			persistStatus = billing.StatusPending
		}
		if perr := deps.Store.Create(ctx, billing.Checkout{
			PromptID:    res.PromptID,
			CandidateID: candidateID,
			PlanID:      string(product.ID),
			Route:       string(res.Route),
			Status:      persistStatus,
			AmountCents: int64(product.USDCents),
			Currency:    product.Currency,
			Country:     country,
			RedirectURL: res.RedirectURL,
			Error:       res.Error,
		}); perr != nil {
			log.WithError(perr).Error("ats-report: persist checkout failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "checkout_persist_failed",
				"could not record checkout")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":           true,
			"product_id":   string(product.ID),
			"amount_usd":   product.Amount,
			"usd_cents":    product.USDCents,
			"currency":     product.Currency,
			"status":       string(res.Status),
			"redirect_url": res.RedirectURL,
			"prompt_id":    res.PromptID,
			"message":      "Complete payment to receive your ATS report by email.",
		})
	}
}

// ATSReportFulfiller implements billing.OneTimeFulfiller for ats_report.
type ATSReportFulfiller struct {
	Deps ATSReportDeps
}

// FulfillOneTime generates the match-aware ATS report and emails HTML.
func (f *ATSReportFulfiller) FulfillOneTime(ctx context.Context, candidateID, productID, promptID string) error {
	if productID != string(billing.ProductATSReport) {
		return nil
	}
	log := util.Log(ctx).WithField("candidate_id", candidateID).WithField("prompt_id", promptID)
	deps := f.Deps
	if deps.Scorer == nil {
		return nil
	}

	cvText, fields := loadCVForScore(ctx, deps.DB, candidateID)
	if len(strings.TrimSpace(cvText)) < 40 {
		log.Warn("ats-report fulfill: no CV text")
		return nil
	}

	targetRole := strings.TrimSpace(fields.CurrentTitle)
	if targetRole == "" && len(fields.PreferredRoles) > 0 {
		targetRole = strings.TrimSpace(fields.PreferredRoles[0])
	}
	// Prefer profile target_job_title when extract fields are sparse.
	if deps.DB != nil {
		var tt, ct string
		if err := deps.DB.QueryRowContext(ctx,
			`SELECT COALESCE(target_job_title,''), COALESCE(current_title,'') FROM candidate_profiles WHERE id = $1`,
			candidateID,
		).Scan(&tt, &ct); err == nil {
			if strings.TrimSpace(tt) != "" {
				targetRole = strings.TrimSpace(tt)
			} else if targetRole == "" {
				targetRole = strings.TrimSpace(ct)
			}
		}
	}

	overall := deps.Scorer.Score(ctx, cvText, fields, targetRole)

	jobs := loadMatchedJobs(ctx, deps, candidateID, 15)
	report := cv.BuildMatchAwareReport(overall, cvText, jobs)
	htmlDoc := cv.RenderMatchAwareHTML(report)

	// Persist last report score on profile for UI badge.
	if deps.DB != nil {
		raw, _ := json.Marshal(report)
		_, _ = deps.DB.ExecContext(ctx, `
UPDATE candidate_profiles SET
  cv_score = $2,
  cv_report_json = $3::jsonb,
  cv_scored_at = NOW(),
  cv_scored_version = $4,
  updated_at = NOW()
WHERE id = $1`,
			candidateID, overall.OverallScore, string(raw), overall.CVVersion)
	}

	if deps.Notify == nil {
		log.Warn("ats-report fulfill: notify client nil — report generated but not emailed")
		return nil
	}
	profileID := notify.ProfileID(ctx, deps.DB, candidateID)
	tpl := strings.TrimSpace(deps.Template)
	if tpl == "" {
		tpl = DefaultTemplateATSReport
	}
	// service-notification templates render variables; include full HTML as
	// attachment body + summary fields. If the template only supports body
	// vars, operators map report_html / report_attachment_html accordingly.
	vars := map[string]any{
		"overall_score":          overall.OverallScore,
		"avg_match_fit":          report.AvgMatchFit,
		"jobs_scored":            report.JobsScored,
		"target_role":            overall.TargetRole,
		"report_html":            htmlDoc,
		"report_attachment_html": htmlDoc,
		"attachment_filename":    "stawi-ats-report.html",
		"prompt_id":              promptID,
	}
	if deps.PublicSiteURL != "" {
		vars["dashboard_url"] = strings.TrimRight(deps.PublicSiteURL, "/") + "/dashboard/#cv"
	}
	err := notify.Send(ctx, deps.Notify, notify.Message{
		Template:    tpl,
		ProfileID:   profileID,
		Variables:   vars,
		Priority:    notificationv1.PRIORITY_HIGH,
		PrioritySet: true,
	})
	if err != nil {
		log.WithError(err).Error("ats-report fulfill: email failed")
		return err
	}
	log.WithField("score", overall.OverallScore).WithField("jobs", report.JobsScored).
		Info("ats-report fulfill: emailed")
	return nil
}

func loadMatchedJobs(ctx context.Context, deps ATSReportDeps, candidateID string, limit int) []cv.MatchedJob {
	out := make([]cv.MatchedJob, 0, limit)
	if deps.Matches != nil {
		page, err := deps.Matches.ListByCandidate(ctx, matching.ListByCandidateParams{
			CandidateID: candidateID,
			Limit:       limit,
		})
		if err == nil {
			n := 0
			for _, m := range page.Items {
				if n >= limit {
					break
				}
				title, company, snippet := opportunitySnippet(ctx, deps.DB, m.OpportunityID)
				out = append(out, cv.MatchedJob{
					OpportunityID: m.OpportunityID,
					Title:         title,
					Company:       company,
					Score:         m.Score,
					ApplyURL:      m.ApplyURL,
					Snippet:       snippet,
				})
				n++
			}
			return out
		}
	}
	// Fallback SQL if Match store unavailable.
	if deps.DB == nil {
		return out
	}
	rows, err := deps.DB.QueryContext(ctx, `
SELECT m.opportunity_id, m.score,
       COALESCE(o.title,''), COALESCE(o.issuing_entity,''), COALESCE(o.apply_url,''),
       COALESCE(o.description,'')
FROM candidate_matches m
LEFT JOIN opportunities o ON o.canonical_id = m.opportunity_id
WHERE m.candidate_id = $1 AND m.status NOT IN ('dismissed')
ORDER BY m.score DESC
LIMIT $2`, candidateID, limit)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, title, company, apply, desc string
		var score float64
		if err := rows.Scan(&id, &score, &title, &company, &apply, &desc); err != nil {
			continue
		}
		out = append(out, cv.MatchedJob{
			OpportunityID: id,
			Title:         title,
			Company:       company,
			Score:         score,
			ApplyURL:      apply,
			Snippet:       title + "\n" + stripHTMLLite(desc),
		})
	}
	return out
}

func opportunitySnippet(ctx context.Context, db *sql.DB, opportunityID string) (title, company, snippet string) {
	if db == nil || opportunityID == "" {
		return "", "", ""
	}
	var desc string
	err := db.QueryRowContext(ctx, `
SELECT COALESCE(title,''), COALESCE(issuing_entity,''), COALESCE(description,'')
FROM opportunities WHERE canonical_id = $1 LIMIT 1`, opportunityID).Scan(&title, &company, &desc)
	if err != nil {
		return "", "", ""
	}
	plain := stripHTMLLite(desc)
	if len(plain) > 2000 {
		plain = plain[:2000]
	}
	return title, company, title + "\n" + company + "\n" + plain
}
