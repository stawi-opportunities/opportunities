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
}
