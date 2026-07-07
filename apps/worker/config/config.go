// Package config defines the PostgreSQL ingestion worker configuration.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/config"
)

type Config struct {
	fconfig.ConfigurationDefault

	PostgresBatchSize    int           `env:"POSTGRES_BATCH_SIZE" envDefault:"100"`
	PostgresConcurrency  int           `env:"POSTGRES_CONCURRENCY" envDefault:"8"`
	PostgresPollInterval time.Duration `env:"POSTGRES_POLL_INTERVAL" envDefault:"1s"`
	PostgresLease        time.Duration `env:"POSTGRES_LEASE" envDefault:"2m"`
	PostgresMaxAttempts  int           `env:"POSTGRES_MAX_ATTEMPTS" envDefault:"10"`
}

func Load() (Config, error) { return fconfig.FromEnv[Config]() }
