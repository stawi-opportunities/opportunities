package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServiceName          string
	HTTPAddr             string
	PostgresDSN          string
	RedisAddr            string
	OpenSearchURL        string
	OTLPEndpoint         string
	LogLevel             string
	QueueName            string
	WorkerConcurrency    int
	ScheduleBatchSize    int
	DefaultCountry       string
	UserAgent            string
	SerpAPIKey           string
	AdzunaAppID          string
	AdzunaAppKey         string
	USAJobsKey           string
	USAJobsEmail         string
	SmartRecruitersToken string
	HTTPTimeout          time.Duration
}

func Load(service string) (Config, error) {
	cfg := Config{
		ServiceName:          service,
		HTTPAddr:             get("HTTP_ADDR", ":8080"),
		PostgresDSN:          os.Getenv("POSTGRES_DSN"),
		RedisAddr:            get("REDIS_ADDR", "127.0.0.1:6379"),
		OpenSearchURL:        get("OPENSEARCH_URL", "http://127.0.0.1:9200"),
		OTLPEndpoint:         os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		LogLevel:             strings.ToLower(get("LOG_LEVEL", "info")),
		QueueName:            get("QUEUE_NAME", "crawl_requests"),
		WorkerConcurrency:    getInt("WORKER_CONCURRENCY", 20),
		ScheduleBatchSize:    getInt("SCHEDULE_BATCH_SIZE", 500),
		DefaultCountry:       get("DEFAULT_COUNTRY", "US"),
		UserAgent:            get("USER_AGENT", "stawi.jobs-bot/1.0 (+https://stawi.jobs)"),
		SerpAPIKey:           os.Getenv("SERPAPI_KEY"),
		AdzunaAppID:          os.Getenv("ADZUNA_APP_ID"),
		AdzunaAppKey:         os.Getenv("ADZUNA_APP_KEY"),
		USAJobsKey:           os.Getenv("USAJOBS_KEY"),
		USAJobsEmail:         os.Getenv("USAJOBS_EMAIL"),
		SmartRecruitersToken: os.Getenv("SMARTRECRUITERS_TOKEN"),
		HTTPTimeout:          getDuration("HTTP_TIMEOUT", 20*time.Second),
	}
	if cfg.ServiceName == "" {
		return Config{}, fmt.Errorf("service name required")
	}
	return cfg, nil
}

func get(k, d string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return d
	}
	return v
}

func getInt(k string, d int) int {
	raw := strings.TrimSpace(os.Getenv(k))
	if raw == "" {
		return d
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return d
	}
	return v
}

func getDuration(k string, d time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(k))
	if raw == "" {
		return d
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return d
	}
	return v
}
