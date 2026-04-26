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
	UserAgent         string `env:"USER_AGENT" envDefault:"opportunities-bot/2.0 (+https://stawi.jobs)"`
	HTTPTimeoutSec    int    `env:"HTTP_TIMEOUT_SEC" envDefault:"20"`

	// Inference back-end (OpenAI-compatible). INFERENCE_BASE_URL /
	// INFERENCE_MODEL are the current knobs; OLLAMA_URL / OLLAMA_MODEL
	// are accepted as fallbacks so existing deploys keep working during
	// the cutover to Cloudflare AI Gateway.
	InferenceBaseURL string `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY" envDefault:""`
	InferenceModel   string `env:"INFERENCE_MODEL" envDefault:""`
	OllamaURL        string `env:"OLLAMA_URL" envDefault:""`
	OllamaModel      string `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`

	// Embeddings. When EMBEDDING_BASE_URL is empty, Extract.Embed() returns
	// an empty slice and the pipeline skips storing the vector — matching
	// degrades to skills+keyword scoring without the embedding term.
	EmbeddingBaseURL string `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey  string `env:"EMBEDDING_API_KEY" envDefault:""`
	EmbeddingModel   string `env:"EMBEDDING_MODEL" envDefault:""`

	// Reranker — carried for consistency with the other apps. Crawler
	// doesn't currently rerank, but having the knobs in one config struct
	// keeps copy-paste safe.
	RerankBaseURL     string `env:"RERANK_BASE_URL" envDefault:""`
	RerankAPIKey      string `env:"RERANK_API_KEY" envDefault:""`
	RerankModel       string `env:"RERANK_MODEL" envDefault:""`
	ValkeyAddr        string `env:"VALKEY_ADDR" envDefault:""`
	R2AccountID       string `env:"R2_ACCOUNT_ID" envDefault:""`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID" envDefault:""`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Bucket          string `env:"R2_BUCKET" envDefault:"opportunities-content"`
	R2DeployHookURL   string `env:"R2_DEPLOY_HOOK_URL" envDefault:""`

	// Archive R2 — separate bucket (and separate credentials) for
	// raw HTML + variant blobs + canonical snapshots. Distinct from
	// the public job-repo bucket above so least-privilege leaks
	// stay contained.
	ArchiveR2AccountID       string `env:"ARCHIVE_R2_ACCOUNT_ID" envDefault:""`
	ArchiveR2AccessKeyID     string `env:"ARCHIVE_R2_ACCESS_KEY_ID" envDefault:""`
	ArchiveR2SecretAccessKey string `env:"ARCHIVE_R2_SECRET_ACCESS_KEY" envDefault:""`
	ArchiveR2Bucket          string `env:"ARCHIVE_R2_BUCKET" envDefault:"opportunities-archive"`

	PublishMinQuality float64 `env:"PUBLISH_MIN_QUALITY" envDefault:"50"`

	// RetentionGraceDays bounds how long a canonical job stays in the
	// "expired" status before stage-2 retention physically deletes its
	// R2 snapshot. 7 days is long enough that a user who bookmarked the
	// page between crawls still sees the dead-link UX before it 404s.
	RetentionGraceDays int `env:"RETENTION_GRACE_DAYS" envDefault:"7"`

	// Back-pressure: pause new crawl dispatch when the NATS consumer's
	// pending depth crosses HighWater; resume when it drops below
	// LowWater. Read from NATS's http monitor (port 8222). Leave
	// BackpressureMonitorURL blank to disable entirely — the gate
	// becomes a no-op that always reports open.
	BackpressureMonitorURL   string `env:"BACKPRESSURE_MONITOR_URL" envDefault:"http://core-queue-headless.queue-system.svc.cluster.local:8222"`
	BackpressureStreamName   string `env:"BACKPRESSURE_STREAM_NAME" envDefault:"svc_opportunities_events"`
	BackpressureConsumerName string `env:"BACKPRESSURE_CONSUMER_NAME" envDefault:"crawler-events"`
	BackpressureHighWater    int    `env:"BACKPRESSURE_HIGH_WATER" envDefault:"100000"`
	BackpressureLowWater     int    `env:"BACKPRESSURE_LOW_WATER" envDefault:"50000"`

	// Translation fan-out. TranslateEnabled is the master switch. When
	// true, every successful publish triggers LLM translation to each
	// TranslateLanguages entry (source language is skipped automatically)
	// and the translated JobSnapshot is uploaded to R2 at
	// jobs/{slug}.{lang}.json. TranslateMinQuality sets a floor so we
	// don't burn LLM quota on low-scoring jobs.
	TranslateEnabled    bool     `env:"TRANSLATE_ENABLED" envDefault:"false"`
	TranslateLanguages  []string `env:"TRANSLATE_LANGUAGES" envSeparator:"," envDefault:"en,es,fr,de,pt,ja,ar,zh"`
	TranslateMinQuality float64  `env:"TRANSLATE_MIN_QUALITY" envDefault:"70"`

	// Redirect service (for tracked /r/{slug} apply links).
	// RedirectServiceURI is the cluster-internal Connect RPC endpoint,
	// RedirectPublicBaseURL is the public origin /r/{slug} resolves at.
	// Leaving either blank keeps PublishHandler falling back to the raw
	// apply_url on every job.
	RedirectServiceURI    string `env:"REDIRECT_SERVICE_URI" envDefault:""`
	RedirectPublicBaseURL string `env:"REDIRECT_PUBLIC_BASE_URL" envDefault:""`

	// Analytics (OpenObserve) — shared across every opportunities service.
	AnalyticsBaseURL  string `env:"ANALYTICS_BASE_URL" envDefault:""`
	AnalyticsOrg      string `env:"ANALYTICS_ORG" envDefault:"default"`
	AnalyticsUsername string `env:"ANALYTICS_USERNAME" envDefault:""`
	AnalyticsPassword string `env:"ANALYTICS_PASSWORD" envDefault:""`

	// ContentOrigin is the public CDN origin used when building cache-purge URLs.
	ContentOrigin string `env:"CONTENT_ORIGIN" envDefault:"https://job-repo.stawi.org"`

	// CloudflareZoneID / CloudflareAPIToken: when both set, the publish handler
	// best-effort purges job-repo.stawi.org edge cache after each upload. When
	// either is empty, the purger is a silent no-op (useful for local dev).
	CloudflareZoneID   string `env:"CLOUDFLARE_ZONE_ID" envDefault:""`
	CloudflareAPIToken string `env:"CLOUDFLARE_API_TOKEN" envDefault:""`
}
