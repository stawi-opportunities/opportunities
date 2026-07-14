// Package config loads apps/frontier-worker runtime configuration.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/v2/config"
)

// Config for apps/frontier-worker. Frame base handles Postgres +
// pub/sub + OTEL. The worker dequeues URLs from url_frontier, fetches each,
// extracts schema.org JobPosting JSON-LD, and enqueues accepted rows.
type Config struct {
	fconfig.ConfigurationDefault

	// OpportunityKindsDir is the on-disk fallback when R2-backed
	// definitions aren't configured. Mirrors apps/crawler.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"./definitions/opportunity-kinds"`

	// UserAgent for outbound HTTP. Mirrors crawler.
	UserAgent string `env:"USER_AGENT" envDefault:"StawiBot/1.0 (+https://stawi.org)"`

	// HTTPTimeoutSec bounds the per-URL fetch. Default 20s.
	HTTPTimeoutSec int `env:"HTTP_TIMEOUT_SEC" envDefault:"20"`

	// Unblocker fallback — same contract as the crawler.
	UnblockerProxyURL   string `env:"UNBLOCKER_PROXY_URL"`
	UnblockerCACert     string `env:"UNBLOCKER_CA_CERT"`
	UnblockerTimeoutSec int    `env:"UNBLOCKER_TIMEOUT_SEC" envDefault:"60"`

	ScrapeDoToken   string `env:"SCRAPEDO_TOKEN"`
	ScrapeDoRender  bool   `env:"SCRAPEDO_RENDER" envDefault:"false"`
	ScrapeDoSuper   bool   `env:"SCRAPEDO_SUPER" envDefault:"false"`
	ScrapeDoGeoCode string `env:"SCRAPEDO_GEOCODE"`

	DequeueBatch    int           `env:"DEQUEUE_BATCH" envDefault:"5"`
	MaxAttempts     int           `env:"MAX_ATTEMPTS" envDefault:"5"`
	IdleTickSeconds int           `env:"IDLE_TICK_SECONDS" envDefault:"5"`
	IngestMaxPending   int64         `env:"INGEST_MAX_PENDING" envDefault:"100000"`
	IngestMaxOldestAge time.Duration `env:"INGEST_MAX_OLDEST_AGE" envDefault:"30m"`

	// Inference back-end for peeling how_to_apply after accept. Empty
	// disables peel (listings keep a combined description until backfill).
	InferenceBaseURL    string `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey     string `env:"INFERENCE_API_KEY" envDefault:""`
	InferenceModel      string `env:"INFERENCE_MODEL" envDefault:""`
	InferenceTimeoutSec int    `env:"INFERENCE_TIMEOUT_SEC" envDefault:"120"`
}

// Load parses env into Config using Frame's env loader.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
