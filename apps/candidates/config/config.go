package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/config"
)

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

	// Reranker (cross-encoder, e.g. BAAI/bge-reranker-v2-m3 via TEI).
	// Matcher falls back to retrieval-order when unset.
	RerankBaseURL string `env:"RERANK_BASE_URL" envDefault:""`
	RerankAPIKey  string `env:"RERANK_API_KEY" envDefault:""`
	RerankModel   string `env:"RERANK_MODEL" envDefault:""`

	// Matching-stage feature flags. Default off — matcher stays on the
	// bi-encoder path until ops flip this on after a TEI smoke-test.
	RerankEnabled     bool    `env:"RERANK_ENABLED" envDefault:"false"`
	RerankSampleRatio float64 `env:"RERANK_SAMPLE_RATIO" envDefault:"1.0"`
	RerankTopK        int     `env:"RERANK_TOP_K" envDefault:"100"`

	MaxFreeMatches    int    `env:"MAX_FREE_MATCHES" envDefault:"5"`
	PaymentServiceURL string `env:"PAYMENT_SERVICE_URL" envDefault:""`
	ProfileServiceURL string `env:"PROFILE_SERVICE_URL" envDefault:""`

	// Antinvestor service URIs (optional — nil client on empty URI).
	NotificationServiceURI string `env:"NOTIFICATION_SERVICE_URI" envDefault:""`
	FileServiceURI         string `env:"FILE_SERVICE_URI" envDefault:""`
	RedirectServiceURI     string `env:"REDIRECT_SERVICE_URI" envDefault:""`
	BillingServiceURI      string `env:"BILLING_SERVICE_URI" envDefault:""`
	ProfileServiceURI      string `env:"PROFILE_SERVICE_URI" envDefault:""`

	// Public site URL — used to build provider redirect/cancel URLs.
	PublicSiteURL string `env:"PUBLIC_SITE_URL" envDefault:"https://jobs.stawi.org"`

	// Billing orchestration.  service_payment + service_billing are
	// co-deployed and reached via BillingServiceURI above; stawi
	// never talks to Polar / M-Pesa / Airtel / MTN directly —
	// those providers are the payment service's concern. We only
	// need a catalog id + merchant recipient + per-tier Polar
	// product ids so InitiatePrompt's extras round-trip correctly.
	BillingCatalogVersionID   string `env:"BILLING_CATALOG_VERSION_ID" envDefault:"stawi-jobs-v1"`
	BillingRecipientProfileID string `env:"BILLING_RECIPIENT_PROFILE_ID" envDefault:""`

	// Polar.sh product ids per tier (env-specific — staging and
	// production have different dashboards). Empty product ids
	// cause OpenCheckout to fail for RoutePolar users on that tier.
	PolarProductStarter string `env:"POLAR_PRODUCT_STARTER" envDefault:""`
	PolarProductPro     string `env:"POLAR_PRODUCT_PRO" envDefault:""`
	PolarProductManaged string `env:"POLAR_PRODUCT_MANAGED" envDefault:""`

	// BillingReconcileInterval controls how often the candidates
	// service polls service_billing for PENDING subscriptions to
	// promote them to "paid". Zero disables the reconciler.
	BillingReconcileInterval time.Duration `env:"BILLING_RECONCILE_INTERVAL" envDefault:"30s"`
}
