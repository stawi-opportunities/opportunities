// Package config loads apps/frontier-worker runtime configuration.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/v2/config"
)

// Config for apps/frontier-worker. Frame base handles Postgres +
// pub/sub + OTEL. The worker dequeues URLs from url_frontier, fetches each,
// parses the response in memory, discards it, and enqueues the extracted row.
type Config struct {
	fconfig.ConfigurationDefault

	// AI extractor. Optional — when unset the worker skips the LLM
	// pass and forwards the URL stub with whatever metadata the
	// connector supplied. Same env knobs as apps/crawler so the
	// same secrets work for both.
	InferenceBaseURL string `env:"INFERENCE_BASE_URL"`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY"`
	InferenceModel   string `env:"INFERENCE_MODEL"`
	EmbeddingBaseURL string `env:"EMBEDDING_BASE_URL"`
	EmbeddingAPIKey  string `env:"EMBEDDING_API_KEY"`
	EmbeddingModel   string `env:"EMBEDDING_MODEL"`
	// EmbeddingDimensions pins the embeddings "dimensions" field (Qwen3 MRL);
	// 0 omits it. Must equal EMBEDDING_DIM.
	EmbeddingDimensions int `env:"EMBEDDING_DIMENSIONS" envDefault:"0"`

	// OpportunityKindsDir is the on-disk fallback when R2-backed
	// definitions aren't configured. Mirrors apps/crawler.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"./definitions/opportunity-kinds"`

	// UserAgent for outbound HTTP. Mirrors crawler.
	UserAgent string `env:"USER_AGENT" envDefault:"StawiBot/1.0 (+https://stawi.org)"`

	// HTTPTimeoutSec bounds the per-URL fetch. Default 20s.
	HTTPTimeoutSec int `env:"HTTP_TIMEOUT_SEC" envDefault:"20"`

	// Unblocker fallback — same contract as the crawler (see
	// apps/crawler/config): blocked fetches (403/429/451/503 or a
	// transport error) retry through this residential-unblocker proxy.
	// The frontier-worker fetches DETAIL pages on WAF-fronted boards, so
	// it needs the fallback at least as much as the listing crawler.
	// Empty disables (direct-only).
	UnblockerProxyURL   string `env:"UNBLOCKER_PROXY_URL"`
	UnblockerCACert     string `env:"UNBLOCKER_CA_CERT"`
	UnblockerTimeoutSec int    `env:"UNBLOCKER_TIMEOUT_SEC" envDefault:"60"`

	// scrape.do unblocker (API mode); takes precedence over the proxy
	// above. See apps/crawler/config for the option semantics.
	ScrapeDoToken   string `env:"SCRAPEDO_TOKEN"`
	ScrapeDoRender  bool   `env:"SCRAPEDO_RENDER" envDefault:"false"`
	ScrapeDoSuper   bool   `env:"SCRAPEDO_SUPER" envDefault:"false"`
	ScrapeDoGeoCode string `env:"SCRAPEDO_GEOCODE"`

	// DequeueBatch caps the URLs claimed per Dequeue call.
	// Default 5 — large enough to amortise the txn cost, small
	// enough that a slow fetch doesn't park other URLs in
	// in_flight for too long.
	DequeueBatch int `env:"DEQUEUE_BATCH" envDefault:"5"`

	// MaxAttempts is the Fail-path retry budget per URL.
	MaxAttempts int `env:"MAX_ATTEMPTS" envDefault:"5"`

	// IdleTickSeconds drives the heartbeat poll cadence — the
	// worker calls Dequeue every N seconds as a fallback in case
	// the NATS wake-up signal misses. Default 5s.
	IdleTickSeconds    int           `env:"IDLE_TICK_SECONDS" envDefault:"5"`
	IngestMaxPending   int64         `env:"INGEST_MAX_PENDING" envDefault:"100000"`
	IngestMaxOldestAge time.Duration `env:"INGEST_MAX_OLDEST_AGE" envDefault:"30m"`
}

// Load parses env into Config using Frame's env loader.
func Load() (Config, error) {
	return fconfig.FromEnv[Config]()
}
