package config

import "github.com/pitabwire/frame/v2/config"

// Config drives the employer ATS service (Frame ConfigurationDefault + OIDC).
type Config struct {
	config.ConfigurationDefault

	// HTTPPathPrefix optional reverse-proxy prefix.
	HTTPPathPrefix string `env:"ATS_HTTP_PATH_PREFIX" envDefault:""`

	// AuthRequireJWT requires OIDC at runtime (default true). Set false only
	// for local/tests so tenancy headers work (X-Profile-ID / X-Tenant-ID / X-Partition-ID).
	AuthRequireJWT bool `env:"AUTH_REQUIRE_JWT" envDefault:"true"`

	// MigrationPath is relative to process CWD for setup Job.
	MigrationPath string `env:"ATS_MIGRATION_PATH" envDefault:"apps/ats/migrations/0001"`

	// NotificationServiceURI dials service-notification for interview emails/ICS.
	NotificationServiceURI string `env:"NOTIFICATION_SERVICE_URI" envDefault:""`
	// NotificationServiceWorkloadAPITargetPath optional SPIFFE path.
	NotificationServiceWorkloadAPITargetPath string `env:"NOTIFICATION_SERVICE_WORKLOAD_API_TARGET_PATH" envDefault:"/ns/notifications/sa/service-notification"`
	// MessageTemplateInterviewScheduled is the notification template name.
	MessageTemplateInterviewScheduled string `env:"MESSAGE_TEMPLATE_ATS_INTERVIEW_SCHEDULED" envDefault:"template.opportunities.ats.interview.scheduled"`

	// PublicSiteURL is used for deep links in interview emails.
	PublicSiteURL string `env:"PUBLIC_SITE_URL" envDefault:"https://opportunities.stawi.org"`

	// MatchingDatabaseURL optional separate read DB for candidate_profiles shortlist.
	// When empty, matching uses the primary ATS datastore (no-op if tables absent).
	MatchingDatabaseURL string `env:"ATS_MATCHING_DATABASE_URL" envDefault:""`

	// ProductDatabaseURL optional dual-write target for public opportunities board.
	ProductDatabaseURL string `env:"ATS_PRODUCT_DATABASE_URL" envDefault:""`

	// OutboxPollIntervalSeconds between outbox drain cycles (default 15).
	OutboxPollIntervalSeconds int `env:"ATS_OUTBOX_POLL_SECONDS" envDefault:"15"`

	// CalendarServiceURI dials service_calendar for panel slots/bookings.
	// Empty keeps local ATS availability-only scheduling.
	CalendarServiceURI string `env:"CALENDAR_SERVICE_URI" envDefault:""`
	// CalendarServiceWorkloadAPITargetPath optional SPIFFE path for mesh.
	CalendarServiceWorkloadAPITargetPath string `env:"CALENDAR_SERVICE_WORKLOAD_API_TARGET_PATH" envDefault:""`
	// CalendarDirect when true uses plain HTTP (local/dev) without OAuth mesh.
	CalendarDirect bool `env:"CALENDAR_SERVICE_DIRECT" envDefault:"false"`
}
