package config

import "github.com/pitabwire/frame/v2/config"

// Config drives the employer ATS service (Frame ConfigurationDefault + OIDC).
type Config struct {
	config.ConfigurationDefault

	// ServiceName used when unset in env.
	// Matches platform convention service_* namespaces.
	// Override with SERVICE_NAME / frame name.
	HTTPPathPrefix string `env:"ATS_HTTP_PATH_PREFIX" envDefault:""`

	// AuthRequireJWT requires OIDC at runtime (default true). Set false only
	// for local/tests so tenancy headers work (X-Profile-ID / X-Tenant-ID / X-Partition-ID).
	AuthRequireJWT bool `env:"AUTH_REQUIRE_JWT" envDefault:"true"`

	// AutoSeed creates demo job/talent/availability when workspace empty (dev only).
	AutoSeed bool `env:"ATS_AUTO_SEED" envDefault:"false"`

	// MigrationPath is relative to process CWD for setup Job.
	MigrationPath string `env:"ATS_MIGRATION_PATH" envDefault:"apps/ats/migrations/0001"`
}
