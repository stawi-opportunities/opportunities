package config

import fconfig "github.com/pitabwire/frame/config"

// CandidatesConfig embeds Frame's ConfigurationDefault and adds
// candidate-service-specific settings.
type CandidatesConfig struct {
	fconfig.ConfigurationDefault
	OllamaURL         string `env:"OLLAMA_URL" envDefault:""`
	OllamaModel       string `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`
	MaxFreeMatches    int    `env:"MAX_FREE_MATCHES" envDefault:"5"`
	PaymentServiceURL string `env:"PAYMENT_SERVICE_URL" envDefault:""`
	ProfileServiceURL string `env:"PROFILE_SERVICE_URL" envDefault:""`
}
