package config

import (
	"errors"

	fconfig "github.com/pitabwire/frame/config"
)

// AutoApplyConfig holds all settings for the autoapply service.
type AutoApplyConfig struct {
	fconfig.ConfigurationDefault

	// Enabled is the consumer-side kill switch. When false the service
	// still consumes (and acks) messages but performs no submission —
	// useful for emergency drain without removing the deployment.
	Enabled bool `env:"AUTO_APPLY_ENABLED" envDefault:"true"`

	// Queue subject URL for incoming auto-apply intents.
	// Defaults to in-memory driver for local dev / tests.
	AutoApplyQueueURL string `env:"AUTO_APPLY_QUEUE_URL" envDefault:"mem://svc.opportunities.autoapply.submit.v1"`

	// Backstop limits enforced on the consumer side in addition to the
	// matching producer's gates. Provides defence in depth against a
	// misconfigured matcher that emits too many intents.
	DailyLimitBackstop int     `env:"AUTO_APPLY_DAILY_LIMIT_BACKSTOP" envDefault:"10"`
	ScoreMinBackstop   float64 `env:"AUTO_APPLY_SCORE_MIN_BACKSTOP"   envDefault:"0.0"`

	// SMTP email fallback (optional). When SMTPHost is empty the email
	// submitter degrades gracefully to a "skipped/no_smtp" result.
	SMTPHost     string `env:"SMTP_HOST"     envDefault:""`
	SMTPPort     int    `env:"SMTP_PORT"     envDefault:"587"`
	SMTPFrom     string `env:"SMTP_FROM"     envDefault:""`
	SMTPPassword string `env:"SMTP_PASSWORD" envDefault:""`

	// Browser automation timeout per submission (seconds).
	BrowserTimeoutSec int `env:"BROWSER_TIMEOUT_SEC" envDefault:"30"`
	// BrowserConcurrency caps the number of headless browsers running
	// in parallel inside one autoapply pod. Each browser holds ~150MB
	// RSS so this is a memory/throughput tradeoff.
	BrowserConcurrency int `env:"BROWSER_CONCURRENCY" envDefault:"2"`
	// BrowserUserAgent overrides the default Chrome UA string.
	BrowserUserAgent string `env:"BROWSER_USER_AGENT" envDefault:""`

	// CV download bounds. CVMaxBytes guards against memory blow-up;
	// CVDownloadTimeoutSec bounds a slow signed-URL fetch.
	CVMaxBytes           int64 `env:"CV_MAX_BYTES"            envDefault:"10485760"` // 10 MiB
	CVDownloadTimeoutSec int   `env:"CV_DOWNLOAD_TIMEOUT_SEC" envDefault:"15"`

	// LLM for Tier-2 generic form-fill (same OpenAI-compatible endpoint
	// as the matching service). When InferenceBaseURL is empty the LLM
	// submitter is disabled and falls through to email/skip. When the
	// base URL is set, InferenceModel must also be non-empty (Validate
	// enforces this at startup).
	InferenceBaseURL string `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY"  envDefault:""`
	InferenceModel   string `env:"INFERENCE_MODEL"    envDefault:""`
}

// Validate checks invariants that env tags can't express. Called from
// main after FromEnv so an obvious misconfiguration fails the boot
// instead of degrading silently per-message.
func (c *AutoApplyConfig) Validate() error {
	if c.InferenceBaseURL != "" && c.InferenceModel == "" {
		return errors.New("autoapply config: INFERENCE_MODEL is required when INFERENCE_BASE_URL is set")
	}
	if c.BrowserConcurrency < 1 {
		return errors.New("autoapply config: BROWSER_CONCURRENCY must be >= 1")
	}
	if c.BrowserTimeoutSec < 1 {
		return errors.New("autoapply config: BROWSER_TIMEOUT_SEC must be >= 1")
	}
	if c.CVMaxBytes <= 0 {
		return errors.New("autoapply config: CV_MAX_BYTES must be > 0")
	}
	if c.CVDownloadTimeoutSec < 1 {
		return errors.New("autoapply config: CV_DOWNLOAD_TIMEOUT_SEC must be >= 1")
	}
	return nil
}
