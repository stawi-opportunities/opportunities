package notify

// Definition is one service-notification template the opportunities product owns.
// Name is the template id used in Notification.Template (and MESSAGE_TEMPLATE_* env).
// Data keys become TemplateData.Type (email channel uses "subject", "html", "text").
// Detail strings are Go text/templates executed against the notification payload.
type Definition struct {
	// Name is the stable template id (e.g. template.opportunities.matches.digest).
	Name string
	// LanguageCode defaults to "en" when empty.
	LanguageCode string
	// Description is stored for operators (extra metadata).
	Description string
	// Data maps channel/part type → Go template body.
	// Email routing expects at least "subject" and "html" (see service-notification emailsmtp).
	Data map[string]string
}

// Catalog returns every opportunities template that must exist in service-notification.
// Names come from cfg (resolved via Templates helpers) so env overrides stay consistent.
func Catalog(cfg Templates) []Definition {
	return []Definition{
		{
			Name:         cfg.Ready(),
			LanguageCode: "en",
			Description:  "Per-match alert when match_alerts is enabled (Path A / preference rematch).",
			Data: map[string]string{
				"subject": "New match: {{if .title}}{{.title}}{{else}}a role for you{{end}}",
				"html": `<!DOCTYPE html>
<html><body style="font-family:system-ui,sans-serif;line-height:1.5;color:#0f172a">
  <p>We found a strong fit for your profile.</p>
  {{if .title}}<p style="font-size:18px;font-weight:600">{{.title}}</p>{{end}}
  {{if .company}}<p style="color:#64748b">{{.company}}</p>{{end}}
  {{if .score}}<p>Match score: <strong>{{.score}}</strong></p>{{end}}
  <p><a href="{{.dashboard_url}}" style="display:inline-block;padding:10px 16px;background:#219c3f;color:#fff;text-decoration:none;border-radius:8px">View matches</a></p>
  <p style="font-size:12px;color:#94a3b8">Stawi Opportunities</p>
</body></html>`,
				"text": "New match{{if .title}}: {{.title}}{{end}}{{if .company}} at {{.company}}{{end}}.\nOpen: {{.dashboard_url}}\n",
			},
		},
		{
			Name:         cfg.Digest(),
			LanguageCode: "en",
			Description:  "Paid/trial match digest: up to 3 highest-scoring unseen matches.",
			Data: map[string]string{
				"subject": "Your top matches ({{if .count}}{{printf \"%.0f\" .count}}{{else}}new{{end}})",
				"html": `<!DOCTYPE html>
<html><body style="font-family:system-ui,sans-serif;line-height:1.5;color:#0f172a">
  <p>Here are your highest-fit roles right now.</p>
  <ul style="padding-left:18px">
  {{range .matches}}
    <li style="margin-bottom:12px">
      <strong>{{.title}}</strong>{{if .company}} — {{.company}}{{end}}
      {{if .score}} <span style="color:#219c3f">({{.score}})</span>{{end}}
      {{if .apply_url}}<br/><a href="{{.apply_url}}">Apply</a>{{else if .slug}}<br/><a href="{{$.dashboard_url}}">Open dashboard</a>{{end}}
    </li>
  {{else}}
    <li>No new matches this period — keep your CV fresh for better results.</li>
  {{end}}
  </ul>
  <p><a href="{{.dashboard_url}}" style="display:inline-block;padding:10px 16px;background:#219c3f;color:#fff;text-decoration:none;border-radius:8px">Open dashboard</a></p>
  <p style="font-size:12px;color:#94a3b8">Stawi Opportunities · You can change digest frequency in Settings</p>
</body></html>`,
				"text": "Your top matches ({{if .count}}{{printf \"%.0f\" .count}}{{end}}):\n{{range .matches}}- {{.title}}{{if .company}} @ {{.company}}{{end}}\n{{end}}\nDashboard: {{.dashboard_url}}\n",
			},
		},
		{
			Name:         cfg.WeeklyJobs(),
			LanguageCode: "en",
			Description:  "Unpaid re-engagement: sample of new jobs (not personalised match scores).",
			Data: map[string]string{
				"subject": "New roles this week{{if .country}} in {{.country}}{{end}}",
				"html": `<!DOCTYPE html>
<html><body style="font-family:system-ui,sans-serif;line-height:1.5;color:#0f172a">
  <p>Fresh openings you might like{{if .country}} ({{.country}}){{end}}.</p>
  <ul style="padding-left:18px">
  {{range .jobs}}
    <li style="margin-bottom:10px">
      <strong>{{.title}}</strong>{{if .company}} — {{.company}}{{end}}
      {{if .apply_url}}<br/><a href="{{.apply_url}}">View</a>{{end}}
    </li>
  {{else}}
    <li>No new listings in this window.</li>
  {{end}}
  </ul>
  <p>Subscribe for personalised AI matches scored against your CV.</p>
  <p><a href="{{.plans_url}}" style="display:inline-block;padding:10px 16px;background:#219c3f;color:#fff;text-decoration:none;border-radius:8px">See plans</a></p>
</body></html>`,
				"text": "New roles{{if .country}} in {{.country}}{{end}}:\n{{range .jobs}}- {{.title}}{{if .company}} @ {{.company}}{{end}}\n{{end}}\nPlans: {{.plans_url}}\n",
			},
		},
		{
			Name:         cfg.CVStale(),
			LanguageCode: "en",
			Description:  "Nudge when CV has not been updated for a long period.",
			Data: map[string]string{
				"subject": "Keep your CV fresh for better matches",
				"html": `<!DOCTYPE html>
<html><body style="font-family:system-ui,sans-serif;line-height:1.5;color:#0f172a">
  <p>Your CV on Stawi looks out of date{{if .days_since_upload}} (about {{printf "%.0f" .days_since_upload}} days){{end}}.</p>
  <p>Updating skills and recent roles improves match quality.</p>
  <p><a href="{{.dashboard_url}}" style="display:inline-block;padding:10px 16px;background:#219c3f;color:#fff;text-decoration:none;border-radius:8px">Update CV</a></p>
</body></html>`,
				"text": "Your CV may be stale{{if .days_since_upload}} (~{{printf \"%.0f\" .days_since_upload}} days){{end}}. Update: {{.dashboard_url}}\n",
			},
		},
		{
			Name:         cfg.ATSReportEmail(),
			LanguageCode: "en",
			Description:  "One-time paid CV ATS report delivery.",
			Data: map[string]string{
				"subject": "Your Stawi ATS report (score {{if .overall_score}}{{printf \"%.0f\" .overall_score}}{{end}})",
				"html": `<!DOCTYPE html>
<html><body style="font-family:system-ui,sans-serif;line-height:1.5;color:#0f172a">
  <p>Your ATS report is ready.</p>
  <p>Overall score: <strong>{{if .overall_score}}{{printf "%.0f" .overall_score}}{{end}}</strong>
  {{if .jobs_scored}} · Jobs scored: {{printf "%.0f" .jobs_scored}}{{end}}
  {{if .avg_match_fit}} · Avg job fit: {{printf "%.0f" .avg_match_fit}}{{end}}</p>
  {{if .target_role}}<p>Target role: {{.target_role}}</p>{{end}}
  <p><a href="{{.dashboard_url}}" style="display:inline-block;padding:10px 16px;background:#219c3f;color:#fff;text-decoration:none;border-radius:8px">Open CV hub</a></p>
  {{if .report_html}}<hr/><div>{{.report_html}}</div>{{end}}
</body></html>`,
				"text": "Your ATS report is ready. Score: {{if .overall_score}}{{printf \"%.0f\" .overall_score}}{{end}}. Dashboard: {{.dashboard_url}}\n",
			},
		},
	}
}

// RequiredTemplateNames is the default set of template ids (no env overrides).
func RequiredTemplateNames() []string {
	empty := Templates{}
	out := make([]string, 0, 5)
	for _, d := range Catalog(empty) {
		out = append(out, d.Name)
	}
	return out
}
