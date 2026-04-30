package config

import (
	fconfig "github.com/pitabwire/frame/config"
)

// AutoApplyConfig holds all settings for the autoapply service.
type AutoApplyConfig struct {
	fconfig.ConfigurationDefault

	// Queue subject URL for incoming auto-apply intents.
	// Defaults to in-memory driver for local dev / tests.
	AutoApplyQueueURL string `env:"AUTO_APPLY_QUEUE_URL" envDefault:"mem://svc.opportunities.autoapply.submit.v1"`

	// SMTP email fallback (optional). When SMTPHost is empty the email
	// submitter degrades gracefully to a "skipped/no_smtp" result.
	SMTPHost     string `env:"SMTP_HOST"     envDefault:""`
	SMTPPort     int    `env:"SMTP_PORT"     envDefault:"587"`
	SMTPFrom     string `env:"SMTP_FROM"     envDefault:""`
	SMTPPassword string `env:"SMTP_PASSWORD" envDefault:""`

	// Browser automation timeout per submission (seconds).
	BrowserTimeoutSec int `env:"BROWSER_TIMEOUT_SEC" envDefault:"30"`

	// LLM for Tier-2 generic form-fill (same OpenAI-compatible endpoint
	// as the matching service). When InferenceBaseURL is empty the LLM
	// submitter is disabled and falls through to email/skip.
	InferenceBaseURL string `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY"  envDefault:""`
	InferenceModel   string `env:"INFERENCE_MODEL"    envDefault:""`
}
