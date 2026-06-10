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
	"github.com/pitabwire/frame"
	frameclient "github.com/pitabwire/frame/client"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/analytics"
	"github.com/stawi-opportunities/opportunities/pkg/counters"
	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

type apiConfig struct {
	// Embed Frame's ConfigurationDefault so DATABASE_URL is parsed by
	// the same path Worker/Crawler/Materializer use. Frame's
	// WithDatastore() reads from this struct via the FrameConfig
	// adapter exposed below.
	fconfig.ConfigurationDefault

	ServerPort string `env:"SERVER_PORT" envDefault:":8082"`

	// DatabaseURL is the CNPG pooler-rw connection string.
	DatabaseURL string `env:"DATABASE_URL" envDefault:""`

	AnalyticsBaseURL  string `env:"ANALYTICS_BASE_URL"   envDefault:""`
	AnalyticsOrg      string `env:"ANALYTICS_ORG"        envDefault:"default"`
	AnalyticsUsername string `env:"ANALYTICS_USERNAME"   envDefault:""`
	AnalyticsPassword string `env:"ANALYTICS_PASSWORD"   envDefault:""`

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

	// R2* config powers /admin/raw_payloads/{id}/body — the operator
	// drill-down that streams the original archived HTML from R2. The
	// same credentials secret is mounted on the crawler/worker pods;
	// the api just needs read access on the raw/* prefix inside the
	// archive bucket (where crawler.PutRaw stores the gzipped HTML).
	// All fields are optional — when AccountID or ArchiveBucket is
	// empty the endpoint is not wired and the rest of the admin
	// surface keeps working unchanged. R2Endpoint mirrors the
	// crawler/worker config for parity, even though pkg/archive
	// derives the endpoint URL from R2AccountID.
	R2AccountID       string `env:"R2_ACCOUNT_ID" envDefault:""`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID" envDefault:""`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Endpoint        string `env:"R2_ENDPOINT" envDefault:""`
	R2ContentBucket   string `env:"R2_CONTENT_BUCKET" envDefault:"product-opportunities-content"`
	R2ArchiveBucket   string `env:"R2_ARCHIVE_BUCKET" envDefault:"product-opportunities-archive"`
}

func main() {
	ctx := context.Background()
	log := util.Log(ctx)

	var cfg apiConfig
	if err := env.Parse(&cfg); err != nil {
		log.WithError(err).Fatal("api: parse config failed")
	}

	// Load the opportunity-kinds registry. Prefer the R2-backed
	// definitions loader (admin can edit kind YAMLs in the cluster);
	// fall back to the on-disk ConfigMap when R2 isn't configured so
	// dev/OSS deploys keep working.
	loader, err := definitions.NewR2LoaderFromEnv(ctx)
	if err != nil {
		log.WithError(err).Fatal("definitions: env config failed")
	}
	var reg *opportunity.Registry
	if loader != nil {
		if err := loader.Start(ctx); err != nil {
			log.WithError(err).Fatal("definitions: loader start failed")
		}
		reg, err = opportunity.LoadFromDefinitions(ctx, loader)
		if err != nil {
			log.WithError(err).Fatal("definitions: registry load failed")
		}
		log.WithField("kinds", reg.Known()).Info("opportunity registry: loaded from R2 definitions")
	} else {
		log.Warn("definitions: R2 not configured; falling back to OpportunityKindsDir")
		reg, err = opportunity.LoadFromDir(cfg.OpportunityKindsDir)
		if err != nil {
			log.WithError(err).Fatal("opportunity registry: load failed")
		}
		log.WithField("kinds", reg.Known()).Info("opportunity registry: loaded from disk")
	}

	// Frame service — pulls in DatastoreManager which gives us the
	// CNPG pool the rest of the platform uses. We bring the api under
	// the same wiring as worker/crawler/materializer per the golang-
	// patterns skill ("Database — frame.WithDatastore()"). HTTP serving
	// stays in the existing http.Server below; Frame's HTTP handler is
	// not registered because the api owns its own mux + middleware.
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)

	// Subscribe to definitions.changed.v1 broadcasts so this replica
	// picks up admin edits within seconds (vs. the 5-min refresh tick).
	// On kind events the rebuild callback live-swaps the registry —
	// readers see either old or new state, never a half-built map.
	if loader != nil {
		rebuild := func(ctx context.Context) error {
			fresh, err := opportunity.LoadFromDefinitions(ctx, loader)
			if err != nil {
				return err
			}
			reg.Replace(fresh)
			return nil
		}
		svc.Init(ctx, frame.WithRegisterEvents(definitions.NewBroadcastConsumer(loader, rebuild)))
		if mgr := svc.EventsManager(); mgr != nil {
			mgr.SetStrict(false)
		}
		log.WithField("topic", eventsv1.TopicDefinitionsChanged).Info("definitions: broadcast consumer wired")
	}

	// Postgres backend — opportunities table populated by worker.canonical
	// + materializer admin handlers. Manticore path retired in Phase 6.
	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		log.Fatal("api: DATABASE_URL required")
	}
	jm := newJobsPostgres(pool.DB)
	log.Info("api: jobs backend = postgres")

	// Frame-managed HTTP client (OTEL trace propagation + retry policy)
	// for analytics + R2 deploy-hook wiring.
	frameHTTPClient := frameclient.NewHTTPClient(ctx,
		frameclient.WithHTTPTraceRequests(),
	)

	analyticsClient := analytics.New(analytics.Config{
		BaseURL:    cfg.AnalyticsBaseURL,
		Org:        cfg.AnalyticsOrg,
		Username:   cfg.AnalyticsUsername,
		Password:   cfg.AnalyticsPassword,
		HTTPClient: frameHTTPClient,
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

	// Flat /api/* endpoints. Search results are post-decorated
	// with view/apply counters when Valkey is configured.
	mux.HandleFunc("GET /api/search", searchHandler(jm, reg, countersClient))
	mux.HandleFunc("GET /api/jobs/{id}", jobByIDHandler(jm))
	mux.HandleFunc("GET /api/jobs/top", topHandler(jm))
	mux.HandleFunc("GET /api/jobs/latest", latestHandler(jm))
	mux.HandleFunc("GET /api/categories", categoriesHandler(jm))
	mux.HandleFunc("GET /api/stats", jobStatsHandler(jm))
	mux.HandleFunc("GET /api/feed", feedHandler(jm))
	mux.HandleFunc("GET /api/feed/tier", feedTierHandler(jm))

	// Per-slug stats — Valkey-backed view + apply counters. Public,
	// no auth. When Valkey is not configured the response is the
	// well-formed all-zero shape (callers can branch on that).
	mux.HandleFunc("GET /opportunities/{slug}/stats", statsHandler(countersClient))

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
		// Source lifecycle mutations emit sources.scheduling.changed.v1 so the
		// crawler creates/archives the per-source Trustage crawl schedule. The
		// api otherwise registers no events surface, so wire the events plugin
		// here (this registers the shared events-queue publisher Emit targets,
		// mirroring how crawler/worker register publishers) and thread a
		// best-effort emit function into the source-admin handlers.
		svc.Init(ctx, frame.WithRegisterEvents())
		if mgr := svc.EventsManager(); mgr != nil {
			mgr.SetStrict(false)
		}
		emitSchedulingChanged := func(emitCtx context.Context, sourceID string) {
			mgr := svc.EventsManager()
			if mgr == nil {
				return
			}
			env := eventsv1.NewEnvelope(eventsv1.TopicSourceSchedulingChanged,
				eventsv1.SourceSchedulingChangedV1{SourceID: sourceID})
			if err := mgr.Emit(emitCtx, eventsv1.TopicSourceSchedulingChanged, env); err != nil {
				util.Log(emitCtx).WithError(err).WithField("source_id", sourceID).
					Warn("source admin: scheduling-changed emit failed")
			}
		}
		registerSourcesAdmin(ctx, mux, &cfg, reg, loader, emitSchedulingChanged)
		registerFlagsAdmin(ctx, mux, jm)
	}

	// Verify-stage rejection visibility — operator-facing read of the
	// opportunities.variants_rejected Iceberg sink. Currently stubbed
	// to 501; see endpoints_v2.go for the implementation note.
	mux.HandleFunc("GET /admin/variants/rejected", variantsRejectedHandler())

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
