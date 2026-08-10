# Opportunities notification templates

These templates are registered in **service-notification** under stable ids.
The matching service **ensures** they exist during setup/migrate
(`DO_DATABASE_MIGRATE=true`) via `notify.EnsureFromConfig`.

| Template id | Env override | Used by |
|-------------|--------------|---------|
| `template.opportunities.matches.ready` | `MESSAGE_TEMPLATE_MATCHES_READY` | Per-match alerts (`match_alerts`) |
| `template.opportunities.matches.digest` | `MESSAGE_TEMPLATE_MATCHES_DIGEST` | Paid match digest (≤3 unseen) |
| `template.opportunities.weekly_jobs.digest` | `MESSAGE_TEMPLATE_WEEKLY_JOBS_DIGEST` | Free weekly jobs re-engagement |
| `template.opportunities.cv.stale_nudge` | `MESSAGE_TEMPLATE_CV_STALE_NUDGE` | CV freshness Trustage job |
| `template.opportunities.cv.ats_report` | `MESSAGE_TEMPLATE_ATS_REPORT` | Paid $2 ATS report email |

Source of truth for bodies: `pkg/notify/catalog.go` (subject / html / text Go templates).

## Setup

1. Deploy matching with `NOTIFICATION_SERVICE_URI` (and SPIFFE path if used).
2. Run the migrate/setup Job (`DO_DATABASE_MIGRATE=true`).
3. Logs should include `setup: notification templates ensured`.

Runtime also soft-ensures on boot (warn-only if incomplete).

## Payload variables (by template)

**matches.ready:** `title`, `company`, `score`, `dashboard_url`, …  
**matches.digest:** `count`, `dashboard_url`, `matches[]` (`title`, `company`, `score`, `apply_url`, `slug`)  
**weekly_jobs.digest:** `country`, `count`, `plans_url`, `jobs[]`  
**cv.stale_nudge:** `days_since_upload`, `dashboard_url`  
**cv.ats_report:** `overall_score`, `jobs_scored`, `avg_match_fit`, `target_role`, `report_html`, `dashboard_url`
