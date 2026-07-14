package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/v2/config"
)

// CrawlerConfig embeds Frame's ConfigurationDefault to get database, logging,
// telemetry, HTTP server, worker pool, events, and security configuration for
// free. Crawler-specific fields are added on top.
type CrawlerConfig struct {
	fconfig.ConfigurationDefault

	SeedsDir string `env:"SEEDS_DIR" envDefault:"/seeds"`

	UserAgent      string `env:"USER_AGENT" envDefault:"opportunities-bot/2.0 (+https://opportunities.stawi.org)"`
	HTTPTimeoutSec int    `env:"HTTP_TIMEOUT_SEC" envDefault:"20"`

	// InferenceTimeoutSec bounds a single LLM call (extraction / recipe
	// synthesis). It must be far larger than HTTPTimeoutSec: page fetches want
	// to fail fast, but an LLM generating full JSON from a long page routinely
	// takes 10–60s. Reusing the 20s page-fetch client here silently timed out
	// every real extraction ("Client.Timeout exceeded while awaiting headers").
	InferenceTimeoutSec int `env:"INFERENCE_TIMEOUT_SEC" envDefault:"120"`

	// RecipeBackfillLimit caps how many recipe-less sources the backfill cron
	// enqueues for generation per run. Each generation makes several LLM calls,
	// so enqueuing all sources at once bursts past a shared inference tier's
	// rate limit. Keep this small (e.g. 2–3) on a rate-limited tier so
	// generation paces under the cap; raise it with in-cluster/high-tier
	// inference. The cron fires every 15 min, so N here ≈ N recipes per 15 min.
	RecipeBackfillLimit int `env:"RECIPE_BACKFILL_LIMIT" envDefault:"25"`

	// CrawlOverdueBatch caps how many overdue sources the catch-up sweep
	// (POST /admin/sources/crawl-overdue) dispatches per tick. The sweep
	// only sees sources ≥1h past due — ticks the per-source schedules
	// already lost — so the batch mostly matters when recovering from an
	// outage, where it paces the backlog through the backpressure gate.
	CrawlOverdueBatch int `env:"CRAWL_OVERDUE_BATCH" envDefault:"25"`

	// Resumable bounded-slice crawling (crawl_runs state machine). A crawl is
	// driven in slices that end after CrawlSliceMaxPages pages OR
	// CrawlSliceMaxSeconds seconds — whichever first — then the handler
	// self-re-enqueues a backpressure-gated continuation. This keeps every
	// message well under NATS ack_wait and makes a millions-of-jobs board
	// resumable across slices and process restarts.
	CrawlSliceMaxPages   int `env:"CRAWL_SLICE_MAX_PAGES" envDefault:"50"`
	CrawlSliceMaxSeconds int `env:"CRAWL_SLICE_MAX_SECONDS" envDefault:"120"`

	// CrawlRunLeaseTTLSec is the per-source run lease. Renewed every page so a
	// slow page never expires a live slice; a crashed owner's lease lapses
	// within one TTL and the watchdog reclaims the run. Keep ≥
	// CrawlSliceMaxSeconds so the watchdog never races an in-flight slice.
	CrawlRunLeaseTTLSec int `env:"CRAWL_RUN_LEASE_TTL_SEC" envDefault:"300"`

	// CrawlRunStuckMaxAttempts fails a run after this many slices without
	// completing — a backstop against a run that never converges (e.g. a
	// source that always errors mid-pagination) holding the single-flight slot.
	// Default 5: fail runs that never converge so single-flight does not
	// block a source forever. Set 0 only for emergency debugging.
	CrawlRunStuckMaxAttempts int `env:"CRAWL_RUN_STUCK_MAX_ATTEMPTS" envDefault:"5"`

	// CrawlRunWatchdogBatch caps how many lapsed runs the watchdog re-drives
	// per tick (each emit is backpressure-gated).
	CrawlRunWatchdogBatch int `env:"CRAWL_RUN_WATCHDOG_BATCH" envDefault:"50"`

	// Unblocker fallback: when a direct fetch is blocked (403/429/451/503 or a
	// transport error), the request is retried through this proxy — a Bright
	// Data Web Unlocker / Oxylabs / similar endpoint, e.g.
	// http://brd-customer-<id>-zone-<zone>:<pass>@brd.superproxy.io:33335
	// Empty disables the fallback (direct-only, current behaviour). Pin the
	// provider's CA via UnblockerCACert to verify the proxy's re-signed TLS;
	// without it, verification on the proxy transport is skipped.
	UnblockerProxyURL   string `env:"UNBLOCKER_PROXY_URL"`
	UnblockerCACert     string `env:"UNBLOCKER_CA_CERT"`
	UnblockerTimeoutSec int    `env:"UNBLOCKER_TIMEOUT_SEC" envDefault:"60"`

	// scrape.do unblocker (API mode). Takes precedence over the generic
	// proxy above. Render runs a headless browser (JS/Cloudflare
	// challenges); Super uses the residential pool (harder IP blocks);
	// both cost more credits, so default off — plain mode already
	// unblocks Cloudflare boards like libyanjobs. GeoCode pins egress
	// country (e.g. "us"). Empty token disables.
	ScrapeDoToken   string `env:"SCRAPEDO_TOKEN"`
	ScrapeDoRender  bool   `env:"SCRAPEDO_RENDER" envDefault:"false"`
	ScrapeDoSuper   bool   `env:"SCRAPEDO_SUPER" envDefault:"false"`
	ScrapeDoGeoCode string `env:"SCRAPEDO_GEOCODE"`

	// Inference back-end (OpenAI-compatible). Used for recipe generation
	// only — not for crawl-time job extraction.
	InferenceBaseURL string `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY" envDefault:""`
	InferenceModel   string `env:"INFERENCE_MODEL" envDefault:""`

	// Embeddings. When EMBEDDING_BASE_URL is empty, Extract.Embed() returns
	// an empty slice and the pipeline skips storing the vector — matching
	// degrades to skills+keyword scoring without the embedding term.
	EmbeddingBaseURL string `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey  string `env:"EMBEDDING_API_KEY" envDefault:""`
	EmbeddingModel   string `env:"EMBEDDING_MODEL" envDefault:""`
	// EmbeddingDimensions pins the embeddings API "dimensions" field for
	// Matryoshka models (Qwen3-Embedding); 0 omits it. Must equal EMBEDDING_DIM.
	EmbeddingDimensions int `env:"EMBEDDING_DIMENSIONS" envDefault:"0"`
	// EmbeddingInputType: "passage"/"query" for NVIDIA asymmetric E5; empty omits.
	EmbeddingInputType string `env:"EMBEDDING_INPUT_TYPE" envDefault:""`

	// Reranker — carried for consistency with the other apps. Crawler
	// doesn't currently rerank, but having the knobs in one config struct
	// keeps copy-paste safe.
	RerankBaseURL string `env:"RERANK_BASE_URL" envDefault:""`
	RerankAPIKey  string `env:"RERANK_API_KEY" envDefault:""`
	RerankModel   string `env:"RERANK_MODEL" envDefault:""`
	// RerankDialect: "tei" (default) or "openai"/"siliconflow" for /v1/rerank.
	RerankDialect string `env:"RERANK_DIALECT" envDefault:""`

	// Redirect service (for tracked /r/{slug} apply links).
	// RedirectServiceURI is the cluster-internal Connect RPC endpoint,
	// RedirectPublicBaseURL is the public origin /r/{slug} resolves at.
	// Leaving either blank keeps PublishHandler falling back to the raw
	// apply_url on every job.
	RedirectServiceURI    string `env:"REDIRECT_SERVICE_URI" envDefault:""`
	RedirectPublicBaseURL string `env:"REDIRECT_PUBLIC_BASE_URL" envDefault:""`

	// Trustage workflow loader — syncs definitions baked into the image
	// at /workflows into Trustage during the migration Job.
	TrustageURL          string `env:"TRUSTAGE_URL" envDefault:""`
	TrustageWorkflowsDir string `env:"TRUSTAGE_WORKFLOWS_DIR" envDefault:"/workflows"`

	// CrawlBaseURL is the in-cluster URL Trustage uses to reach THIS crawler's
	// admin API for per-source crawl schedules (POST {base}/admin/sources/{id}/crawl).
	// Must be resolvable from the operations namespace where Trustage runs.
	CrawlBaseURL string `env:"CRAWL_BASE_URL" envDefault:"http://opportunities-crawler.product-opportunities.svc"`

	// PostgreSQL queue limits bound crawl production by actual processing
	// backlog. A slice finishes its current page, checkpoints it, then yields.
	IngestMaxPending   int64         `env:"INGEST_MAX_PENDING" envDefault:"100000"`
	IngestMaxOldestAge time.Duration `env:"INGEST_MAX_OLDEST_AGE" envDefault:"30m"`

	// SourceSchedulesEnabled gates the per-source Trustage schedule sync — the
	// event-driven crawl scheduling model. When true and a
	// TrustageURL is configured, source lifecycle mutations emit
	// sources.scheduling.changed.v1 and the crawler creates/archives a
	// per-source Trustage schedule to match the source's live status; a boot
	// reconcile + reconcile backstop heal drift. Defaults on.
	SourceSchedulesEnabled bool `env:"SOURCE_SCHEDULES_ENABLED" envDefault:"true"`

	// InternalOverdueInterval runs an in-process overdue crawl sweep so job
	// ingestion continues even when Trustage schedule reconcile or HTTP
	// callbacks fail (auth outages, missing workflows). Zero disables.
	// Default 15m matches definitions/trustage/source-crawl-overdue.json.
	InternalOverdueInterval time.Duration `env:"INTERNAL_OVERDUE_INTERVAL" envDefault:"15m"`

	// AdminToken is a shared secret accepted via X-Admin-Token (or Bearer)
	// for machine callers such as Trustage crons. Empty = JWT-only.
	AdminToken string `env:"ADMIN_TOKEN" envDefault:""`

	// Recipe generation knobs. RECIPE_ENABLED defaults on so active
	// extraction recipes (structured, deterministic) are preferred over
	// connectors when present. Generation still requires an LLM for
	// synthesising new recipes; execution does not.
	RecipeEnabled          bool    `env:"RECIPE_ENABLED"            envDefault:"true"`
	RecipeSampleCount      int     `env:"RECIPE_SAMPLE_COUNT"       envDefault:"4"` // reserved for Plan 5 backfill sampling
	RecipePassThreshold    float64 `env:"RECIPE_PASS_THRESHOLD"     envDefault:"0.8"`
	RecipeMaxGenAttempts   int     `env:"RECIPE_MAX_GEN_ATTEMPTS"   envDefault:"3"`
	RecipeRegenRejectRate  float64 `env:"RECIPE_REGEN_REJECT_RATE"  envDefault:"0.5"`
	RecipeRegenMinPages    int     `env:"RECIPE_REGEN_MIN_PAGES"    envDefault:"20"`
	RecipeMaxRegenFailures int     `env:"RECIPE_MAX_REGEN_FAILURES" envDefault:"3"`

	// Analytics (OpenObserve) — shared across every opportunities service.
	AnalyticsBaseURL  string `env:"ANALYTICS_BASE_URL" envDefault:""`
	AnalyticsOrg      string `env:"ANALYTICS_ORG" envDefault:"default"`
	AnalyticsUsername string `env:"ANALYTICS_USERNAME" envDefault:""`
	AnalyticsPassword string `env:"ANALYTICS_PASSWORD" envDefault:""`

	// ContentOrigin is the public CDN origin used when building cache-purge URLs.
	ContentOrigin string `env:"CONTENT_ORIGIN" envDefault:"https://opportunities-data.stawi.org"`

	// CloudflareZoneID / CloudflareAPIToken: when both set, the publish handler
	// best-effort purges opportunities-data.stawi.org edge cache after each
	// upload. When either is empty, the purger is a silent no-op (useful for
	// local dev).
	CloudflareZoneID   string `env:"CLOUDFLARE_ZONE_ID" envDefault:""`
	CloudflareAPIToken string `env:"CLOUDFLARE_API_TOKEN" envDefault:""`

	// OpportunityKindsDir is the directory holding the opportunity-kinds YAML
	// registry. Mounted as a ConfigMap in production at this path.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`
}
