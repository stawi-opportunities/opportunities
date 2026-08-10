package config

import "github.com/pitabwire/frame/v2/config"

// Config drives the employer ATS service.
type Config struct {
	config.ConfigurationDefault

	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8095"`

	// AuthRequireJWT requires OIDC at boot (default true). Set false only for
	// local/tests so X-Profile-ID / X-Tenant-ID / X-Partition-ID headers work.
	AuthRequireJWT bool `env:"AUTH_REQUIRE_JWT" envDefault:"true"`

	// ATSEnabled gates private routes (healthz always on).
	ATSEnabled bool `env:"ATS_ENABLED" envDefault:"true"`

	// SQLitePath when set uses a local sqlite file instead of Frame DATABASE_URL.
	// Ideal for `make run-ats` demos without Postgres.
	SQLitePath string `env:"ATS_SQLITE_PATH" envDefault:""`

	// AutoSeed creates demo job + talent + availability on empty workspace (dev).
	AutoSeed bool `env:"ATS_AUTO_SEED" envDefault:"false"`
}
