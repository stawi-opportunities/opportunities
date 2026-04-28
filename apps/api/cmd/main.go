// apps/api/cmd/main.go (full replacement)
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	env "github.com/caarlos0/env/v11"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/analytics"
	"github.com/stawi-opportunities/opportunities/pkg/counters"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/publish"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type apiConfig struct {
	ServerPort   string `env:"SERVER_PORT"   envDefault:":8082"`
	ManticoreURL string `env:"MANTICORE_URL" envDefault:""`

	// Cloudflare R2 — one account token authorised on all three
	// product-opportunities buckets. The api publishes Hugo snapshots
	// to the content bucket via POST /admin/backfill.
	R2AccountID       string `env:"R2_ACCOUNT_ID"        envDefault:""`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID"     envDefault:""`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2ContentBucket   string `env:"R2_CONTENT_BUCKET"    envDefault:"product-opportunities-content"`
	R2Endpoint        string `env:"R2_ENDPOINT"          envDefault:""`
	R2Region          string `env:"R2_REGION"            envDefault:"auto"`
	R2DeployHookURL   string `env:"R2_DEPLOY_HOOK_URL"   envDefault:""`

	PublishMinQuality float64 `env:"PUBLISH_MIN_QUALITY"  envDefault:"50"`
	AnalyticsBaseURL  string  `env:"ANALYTICS_BASE_URL"   envDefault:""`
	AnalyticsOrg      string  `env:"ANALYTICS_ORG"        envDefault:"default"`
	AnalyticsUsername string  `env:"ANALYTICS_USERNAME"   envDefault:""`
	AnalyticsPassword string  `env:"ANALYTICS_PASSWORD"   envDefault:""`

	// OpportunityKindsDir is the directory holding the opportunity-kinds YAML
	// registry. Mounted as a ConfigMap in production at this path.
	OpportunityKindsDir string `env:"OPPORTUNITY_KINDS_DIR" envDefault:"/etc/opportunity-kinds"`

	// SourceAdminEnabled gates the /admin/sources/* endpoints. Defaults
	// off so plain api deployments (no DB credentials) keep working
	// unchanged. Set to true in clusters where api should also serve
	// the source-management surface.
	SourceAdminEnabled bool `env:"SOURCE_ADMIN_ENABLED" envDefault:"false"`

	// HTTPTimeoutSec controls outbound HTTP timeout for the
	// source-verifier (reachability + robots probes). Sane default of
	// 15s keeps verification responsive even when the target is slow.
	HTTPTimeoutSec int `env:"HTTP_TIMEOUT_SEC" envDefault:"15"`

	// UserAgent is the User-Agent header the verifier sends on
	// reachability and robots.txt probes.
	UserAgent string `env:"USER_AGENT" envDefault:"stawi-source-verifier/1.0"`

	// ValkeyURL points at the platform Valkey instance. Used by the
	// view + apply counters and the /opportunities/{slug}/stats
	// endpoint. Empty disables the counters surface — the analytics
	// log path keeps working unchanged.
	ValkeyURL string `env:"VALKEY_URL" envDefault:""`
}

func main() {
	ctx := context.Background()
	log := util.Log(ctx)

	var cfg apiConfig
	if err := env.Parse(&cfg); err != nil {
		log.WithError(err).Fatal("api: parse config failed")
	}

	// Load the opportunity-kinds registry at boot. Phase 1 only loads + logs;
	// later phases consult the registry on the publish/index paths.
	reg, err := opportunity.LoadFromDir(cfg.OpportunityKindsDir)
	if err != nil {
		log.WithError(err).Fatal("opportunity registry: load failed")
	}
	log.WithField("kinds", reg.Known()).Info("opportunity registry: loaded")

	if cfg.ManticoreURL == "" {
		log.Fatal("api: MANTICORE_URL is required")
	}
	manticore, err := searchindex.Open(searchindex.Config{URL: cfg.ManticoreURL})
	if err != nil {
		log.WithError(err).Fatal("api: manticore open failed")
	}
	defer func() { _ = manticore.Close() }()
	jm := newJobsManticore(manticore)

	var r2Publisher *publish.R2Publisher
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" {
		r2Publisher = publish.NewR2Publisher(
			cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey,
			cfg.R2ContentBucket, cfg.R2DeployHookURL,
		)
		log.WithField("bucket", cfg.R2ContentBucket).Info("R2 publisher enabled")
	}

	analyticsClient := analytics.New(analytics.Config{
		BaseURL:  cfg.AnalyticsBaseURL,
		Org:      cfg.AnalyticsOrg,
		Username: cfg.AnalyticsUsername,
		Password: cfg.AnalyticsPassword,
	})

	// Counters (Valkey-backed view + apply counts). nil when ValkeyURL
	// is empty — every counter call becomes a no-op so the api still
	// serves the search/detail surface without Valkey wired.
	countersClient, err := counters.NewClient(cfg.ValkeyURL)
	if err != nil {
		log.WithError(err).Warn("api: counters init failed; view/apply counters disabled")
		countersClient = nil
	}
	if countersClient != nil {
		log.Info("api: view/apply counters enabled")
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, req *http.Request) {
		total, err := jm.Count(req.Context(), nil)
		status := "ok"
		if err != nil {
			status = "degraded"
			total = -1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     status,
			"total_jobs": total,
		})
	})

	// v2 endpoints (Manticore-only). Search results are post-decorated
	// with view/apply counters when Valkey is configured.
	mux.HandleFunc("GET /api/v2/search", v2SearchHandler(jm, reg, countersClient))
	mux.HandleFunc("GET /api/v2/jobs/{id}", v2JobByIDHandler(jm))
	mux.HandleFunc("GET /api/v2/jobs/top", v2TopHandler(jm))
	mux.HandleFunc("GET /api/v2/jobs/latest", v2LatestHandler(jm))
	mux.HandleFunc("GET /api/v2/categories", v2CategoriesHandler(jm))
	mux.HandleFunc("GET /api/v2/stats", v2StatsHandler(jm))
	mux.HandleFunc("GET /api/v2/feed", v2FeedHandler(jm))
	mux.HandleFunc("GET /api/v2/feed/tier", v2FeedTierHandler(jm))

	// Legacy shims - v1 paths route to v2 handlers during transition.
	mux.HandleFunc("GET /api/search", v2SearchHandler(jm, reg, countersClient))
	mux.HandleFunc("GET /api/categories", v2CategoriesHandler(jm))
	mux.HandleFunc("GET /api/jobs/latest", v2LatestHandler(jm))
	mux.HandleFunc("GET /api/stats/summary", v2StatsHandler(jm))
	mux.HandleFunc("GET /api/feed", v2FeedHandler(jm))
	mux.HandleFunc("GET /api/feed/tier", v2FeedTierHandler(jm))
	mux.HandleFunc("GET /jobs/top", v2TopHandler(jm))
	mux.HandleFunc("GET /jobs/{id}", v2JobByIDHandler(jm))
	mux.HandleFunc("GET /categories", v2CategoriesHandler(jm))
	mux.HandleFunc("GET /stats", v2StatsHandler(jm))
	mux.HandleFunc("GET /search", v2SearchHandler(jm, reg, countersClient))

	// Per-slug stats — Valkey-backed view + apply counters. Public,
	// no auth. When Valkey is not configured the response is the
	// well-formed all-zero shape (callers can branch on that).
	mux.HandleFunc("GET /opportunities/{slug}/stats", statsHandler(countersClient))

	// Hugo snapshot publishing — sources from Manticore idx_opportunities_rt.
	if r2Publisher != nil {
		mux.HandleFunc("POST /admin/backfill",
			backfillManticoreHandler(jm, r2Publisher, cfg.PublishMinQuality))
	}

	// Source-management admin surface. Optional: only wired when the
	// operator opts in via SOURCE_ADMIN_ENABLED=true and the cluster
	// supplies Frame's database config (POSTGRES_*). The handlers run
	// the source-level verifier (reachability + robots + sample
	// extraction) and own the pending → verified → active lifecycle.
	//
	// Flag-admin shares the same toggle: both surfaces need the api's
	// own Postgres connection, so wiring them together keeps the
	// "should the api own a DB?" decision a single env var.
	if cfg.SourceAdminEnabled {
		registerSourcesAdmin(ctx, mux, &cfg, reg)
		registerFlagsAdmin(ctx, mux, jm)
		// Top-applied lives on the api side (Valkey-backed) — wired
		// alongside the flag-admin toggle so operators get both
		// dashboards (top-flagged + top-applied) at the same time.
		mux.HandleFunc("GET /admin/opportunities/top-applied",
			requireAdmin(topAppliedHandler(jm, countersClient)))
	}

	// Verify-stage rejection visibility — operator-facing read of the
	// opportunities.variants_rejected Iceberg sink. Currently stubbed
	// to 501; see endpoints_v2.go for the implementation note.
	mux.HandleFunc("GET /admin/variants/rejected", v2VariantsRejectedHandler())

	// Analytics beacon — view path. Increments the Valkey counters
	// (atomic INCR + 24h-windowed key with TTL) AND ships an
	// OpenObserve event so analytics queries can dedup by profile_id
	// downstream. Both writes are best-effort; the response is always
	// 204 so a slow Valkey can't add user-facing latency.
	mux.HandleFunc("OPTIONS /jobs/{slug}/view", corsBeaconPreflight)
	mux.HandleFunc("POST /jobs/{slug}/view", viewBeaconHandler(analyticsClient, countersClient))

	// Mirror beacon for the apply surface. The redirect service
	// (or the UI when it owns the apply click) fires this on every
	// /r/{slug} hit so the apply count tracks alongside the view
	// count. Auth is optional; the JWT (when present) attributes the
	// apply to a profile_id in OpenObserve.
	mux.HandleFunc("OPTIONS /opportunities/{slug}/apply", corsBeaconPreflight)
	mux.HandleFunc("POST /opportunities/{slug}/apply", applyBeaconHandler(analyticsClient, countersClient))

	srv := &http.Server{Addr: cfg.ServerPort, Handler: mux}
	go func() {
		log.WithField("port", cfg.ServerPort).Info("API server listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("http server exited")
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("shutting down API")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("API stopped")
}

// hashIP returns an opaque, one-way hash of the client IP.
func hashIP(req *http.Request) string {
	ip := req.Header.Get("CF-Connecting-IP")
	if ip == "" {
		ip = req.Header.Get("X-Forwarded-For")
		if i := strings.IndexByte(ip, ','); i >= 0 {
			ip = ip[:i]
		}
		ip = strings.TrimSpace(ip)
	}
	if ip == "" {
		ip = req.RemoteAddr
	}
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:12])
}

// profileIDFromJWT extracts the sub claim from a Bearer token without
// verification. Verification happens at the gateway layer.
func profileIDFromJWT(req *http.Request) string {
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	token := auth[len("Bearer "):]
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Sub
}

// parseLimit shared by endpoints_v2.go handlers.
func parseLimit(s string, def, max int) int {
	if s == "" {
		return def
	}
	v := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		v = v*10 + int(c-'0')
	}
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}
