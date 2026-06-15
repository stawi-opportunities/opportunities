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

	// DryRun makes the handler do everything except call the
	// submitter: it still inserts the pending row, persists a
	// "skipped/dry_run" outcome, emits ApplicationSubmittedV1, and
	// records metrics. Use it locally to exercise the full pipeline
	// without sending any browser traffic to a real ATS.
	DryRun bool `env:"AUTO_APPLY_DRY_RUN" envDefault:"false"`

	// DevAllowInsecureCV bypasses the https-only / private-IP guard on
	// CV downloads. Only honoured when the binary is built with the
	// "devmode" build tag — production binaries hard-fail at boot if
	// it's set, so this is safe to leave in env files.
	DevAllowInsecureCV bool `env:"AUTO_APPLY_DEV_ALLOW_INSECURE_CV" envDefault:"false"`

	// DevIntentEndpoint mounts POST /dev/intent on the HTTP mux so a
	// developer can publish an AutoApplyIntentV1 with curl instead of
	// learning the NATS CLI. Off by default; enable only in local dev.
	DevIntentEndpoint bool `env:"AUTO_APPLY_DEV_INTENT_ENDPOINT" envDefault:"false"`

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

	// CaptchaProvider selects the CAPTCHA solver. "2captcha" routes
	// detected challenges (reCAPTCHA v2/enterprise, hCaptcha, Turnstile)
	// to 2captcha.com. Empty keeps the skip-on-CAPTCHA behaviour.
	CaptchaProvider string `env:"AUTO_APPLY_CAPTCHA_PROVIDER" envDefault:""`
	// CaptchaAPIKey is the solving-service API key.
	CaptchaAPIKey string `env:"AUTO_APPLY_CAPTCHA_API_KEY" envDefault:""`
	// CaptchaSolveTimeoutSec bounds a single end-to-end solve. A browser
	// slot is held for up to this long, so size pools accordingly.
	CaptchaSolveTimeoutSec int `env:"AUTO_APPLY_CAPTCHA_SOLVE_TIMEOUT_SEC" envDefault:"180"`

	// OTPEnabled turns on the email-OTP hold-open path for Greenhouse
	// applications that gate submission behind an emailed security code.
	// When on, a matching submission holds a browser slot open until the
	// code arrives (via the OTP-email ingress) or OTPWaitSec elapses.
	OTPEnabled bool `env:"AUTO_APPLY_OTP_ENABLED" envDefault:"false"`
	// OTPWaitSec bounds how long a browser slot is held waiting for the
	// emailed security code before giving up (otp_timeout). Keep this
	// comfortably under the queue's ack/visibility deadline so a held
	// message is not redelivered mid-wait.
	OTPWaitSec int `env:"AUTO_APPLY_OTP_WAIT_SEC" envDefault:"60"`
	// OTPBrowserConcurrency sizes a dedicated browser pool for the
	// hold-open OTP path, so long OTP waits don't starve the fast
	// (non-OTP) submitters sharing BrowserConcurrency. Each slot can be
	// occupied for up to OTPWaitSec, so size against expected OTP volume
	// and RAM (~150MB/browser).
	OTPBrowserConcurrency int `env:"AUTO_APPLY_OTP_BROWSER_CONCURRENCY" envDefault:"4"`
	// OTPInjectSecret guards the manual Phase-2 OTP injector endpoint
	// (POST /internal/otp), the human stand-in for the OTP-email ingress.
	// Empty disables the route.
	OTPInjectSecret string `env:"AUTO_APPLY_OTP_INJECT_SECRET" envDefault:""`
	// OTPWebhookSecret guards the inbound OTP-email webhook
	// (POST /webhooks/otp). Empty disables the route.
	OTPWebhookSecret string `env:"AUTO_APPLY_OTP_WEBHOOK_SECRET" envDefault:""`
	// OTPSenderDomain is the allowed From-domain for OTP emails; subdomains
	// are accepted (e.g. us.greenhouse-mail.io). Empty disables the check.
	OTPSenderDomain string `env:"AUTO_APPLY_OTP_SENDER_DOMAIN" envDefault:"greenhouse-mail.io"`
	// OTPRedisURL backs the OTP rendezvous with Valkey so the webhook and
	// the browser-holding submitter can run on separate instances. Empty
	// falls back to an in-process cache (single-instance only).
	OTPRedisURL string `env:"AUTO_APPLY_OTP_REDIS_URL" envDefault:""`

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

	// Session-replay submitter inputs. Mirrors the matching service:
	//   SOURCE_AUTH_DIR     — per-source manifests (pkg/authmanifest)
	//   SESSION_MASTER_KEY  — base64-encoded 32 bytes; needed to
	//                         decrypt captured session payloads
	//   SESSION_MASTER_KEY_ID — opaque ID stored alongside each row
	//                           (must match the value the matching
	//                           service used when capturing)
	// When SESSION_MASTER_KEY is empty the session-replay submitter
	// is omitted from the registry and traffic falls through to the
	// existing tiers, matching the pre-Phase-5 behaviour.
	SourceAuthDir      string `env:"SOURCE_AUTH_DIR"       envDefault:"/etc/source-auth"`
	SessionMasterKey   string `env:"SESSION_MASTER_KEY"    envDefault:""`
	SessionMasterKeyID string `env:"SESSION_MASTER_KEY_ID" envDefault:"v1"`
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
