package config

import "github.com/pitabwire/frame/v2/config"

// Config drives the applications service. Defaults are conservative:
// the service ships flag-off so production deploys are reversible per
// spec §5.5 Step 2.
type Config struct {
	config.ConfigurationDefault

	HTTPAddr            string `env:"HTTP_ADDR" envDefault:":8090"`
	// Enabled by default so the applications tracking API is available when
	// the binary is deployed. Set APPLICATIONS_ENABLED=false to expose only
	// healthz during emergency rollbacks.
	ApplicationsEnabled bool   `env:"APPLICATIONS_ENABLED" envDefault:"true"`
	IdempotencyTTLHours int    `env:"APPLICATIONS_IDEMPOTENCY_TTL_HOURS" envDefault:"24"`

	// R2 blob storage for attachments. Empty values disable R2; the
	// in-memory MemoryBlobStore is used for tests and local dev.
	R2AccountID         string `env:"R2_ACCOUNT_ID"`
	R2AccessKeyID       string `env:"R2_ACCESS_KEY_ID"`
	R2SecretAccessKey   string `env:"R2_SECRET_ACCESS_KEY"`
	R2AttachmentsBucket string `env:"R2_ATTACHMENTS_BUCKET" envDefault:"opportunities-attachments"`
}
