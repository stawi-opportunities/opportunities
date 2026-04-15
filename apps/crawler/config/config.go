package config

// CrawlerConfig holds environment-driven configuration for the crawler service.
type CrawlerConfig struct {
	WorkerConcurrency int    `env:"WORKER_CONCURRENCY" envDefault:"4"`
	BatchSize         int    `env:"BATCH_SIZE" envDefault:"500"`
	BatchFlushSec     int    `env:"BATCH_FLUSH_SEC" envDefault:"10"`
	SeedsDir          string `env:"SEEDS_DIR" envDefault:"/seeds"`
	UserAgent         string `env:"USER_AGENT" envDefault:"stawi.jobs-bot/2.0 (+https://stawi.jobs)"`
	HTTPTimeoutSec    int    `env:"HTTP_TIMEOUT_SEC" envDefault:"20"`
	ServerPort        string `env:"SERVER_PORT" envDefault:":8080"`
	DatabaseURL       string `env:"DATABASE_URL" envDefault:""`
	OllamaURL         string `env:"OLLAMA_URL" envDefault:""`
	OllamaModel       string `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`
}
