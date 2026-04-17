package config

import (
	fconfig "github.com/pitabwire/frame/config"
)

// CrawlerConfig embeds Frame's ConfigurationDefault to get database, logging,
// telemetry, HTTP server, worker pool, events, and security configuration for
// free. Crawler-specific fields are added on top.
type CrawlerConfig struct {
	fconfig.ConfigurationDefault

	WorkerConcurrency int    `env:"WORKER_CONCURRENCY" envDefault:"4"`
	BatchSize         int    `env:"BATCH_SIZE" envDefault:"500"`
	BatchFlushSec     int    `env:"BATCH_FLUSH_SEC" envDefault:"10"`
	SeedsDir          string `env:"SEEDS_DIR" envDefault:"/seeds"`
	UserAgent         string `env:"USER_AGENT" envDefault:"stawi.jobs-bot/2.0 (+https://stawi.jobs)"`
	HTTPTimeoutSec    int    `env:"HTTP_TIMEOUT_SEC" envDefault:"20"`
	OllamaURL         string `env:"OLLAMA_URL" envDefault:""`
	OllamaModel       string `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`
	ValkeyAddr        string  `env:"VALKEY_ADDR" envDefault:""`
	R2AccountID       string  `env:"R2_ACCOUNT_ID" envDefault:""`
	R2AccessKeyID     string  `env:"R2_ACCESS_KEY_ID" envDefault:""`
	R2SecretAccessKey string  `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Bucket          string  `env:"R2_BUCKET" envDefault:"stawi-jobs-content"`
	R2DeployHookURL   string  `env:"R2_DEPLOY_HOOK_URL" envDefault:""`
	PublishMinQuality float64 `env:"PUBLISH_MIN_QUALITY" envDefault:"50"`

	// ContentOrigin is the public CDN origin used when building cache-purge URLs.
	ContentOrigin string `env:"CONTENT_ORIGIN" envDefault:"https://jobs-repo.stawi.org"`

	// CloudflareZoneID / CloudflareAPIToken: when both set, the publish handler
	// best-effort purges jobs-repo.stawi.org edge cache after each upload. When
	// either is empty, the purger is a silent no-op (useful for local dev).
	CloudflareZoneID   string `env:"CLOUDFLARE_ZONE_ID" envDefault:""`
	CloudflareAPIToken string `env:"CLOUDFLARE_API_TOKEN" envDefault:""`
}
