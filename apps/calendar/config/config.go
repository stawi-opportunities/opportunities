package config

import "github.com/pitabwire/frame/v2/config"

// Config drives the resource calendar service.
type Config struct {
	config.ConfigurationDefault

	AuthRequireJWT bool   `env:"AUTH_REQUIRE_JWT" envDefault:"true"`
	MigrationPath  string `env:"CALENDAR_MIGRATION_PATH" envDefault:"apps/calendar/migrations/0001"`

	// SyncPollSeconds is the export outbox drain interval.
	SyncPollSeconds int `env:"CALENDAR_SYNC_POLL_SECONDS" envDefault:"60"`

	// Google Calendar (optional live provider).
	GoogleCalendarEnabled      bool   `env:"GOOGLE_CALENDAR_ENABLED" envDefault:"false"`
	GoogleCalendarClientID     string `env:"GOOGLE_CALENDAR_CLIENT_ID" envDefault:""`
	GoogleCalendarClientSecret string `env:"GOOGLE_CALENDAR_CLIENT_SECRET" envDefault:""`

	// Microsoft Graph (optional live provider).
	MicrosoftCalendarEnabled  bool   `env:"MICROSOFT_CALENDAR_ENABLED" envDefault:"false"`
	MicrosoftCalendarClientID string `env:"MICROSOFT_CALENDAR_CLIENT_ID" envDefault:""`
	MicrosoftCalendarSecret   string `env:"MICROSOFT_CALENDAR_CLIENT_SECRET" envDefault:""`

	// CalDAV (optional).
	CalDAVEnabled bool `env:"CALDAV_ENABLED" envDefault:"false"`

	// EnableMemoryProvider registers the in-process memory provider (tests/dev).
	EnableMemoryProvider bool `env:"CALENDAR_MEMORY_PROVIDER" envDefault:"false"`
}
