package config

import fconfig "github.com/pitabwire/frame/config"

// CandidatesConfig embeds Frame's ConfigurationDefault and adds
// candidate-service-specific settings.
type CandidatesConfig struct {
	fconfig.ConfigurationDefault

	// Inference back-end (OpenAI-compatible).
	InferenceBaseURL string `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY" envDefault:""`
	InferenceModel   string `env:"INFERENCE_MODEL" envDefault:""`
	OllamaURL        string `env:"OLLAMA_URL" envDefault:""`
	OllamaModel      string `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`

	// Embeddings — optional, graceful no-op when empty.
	EmbeddingBaseURL string `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey  string `env:"EMBEDDING_API_KEY" envDefault:""`
	EmbeddingModel   string `env:"EMBEDDING_MODEL" envDefault:""`

	MaxFreeMatches    int    `env:"MAX_FREE_MATCHES" envDefault:"5"`
	PaymentServiceURL string `env:"PAYMENT_SERVICE_URL" envDefault:""`
	ProfileServiceURL string `env:"PROFILE_SERVICE_URL" envDefault:""`

	// Antinvestor service URIs (optional — nil client on empty URI).
	NotificationServiceURI string `env:"NOTIFICATION_SERVICE_URI" envDefault:""`
	FileServiceURI         string `env:"FILE_SERVICE_URI" envDefault:""`
	RedirectServiceURI     string `env:"REDIRECT_SERVICE_URI" envDefault:""`
	BillingServiceURI      string `env:"BILLING_SERVICE_URI" envDefault:""`
	ProfileServiceURI      string `env:"PROFILE_SERVICE_URI" envDefault:""`
}
