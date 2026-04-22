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

	"stawi.jobs/pkg/analytics"
	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/publish"
	"stawi.jobs/pkg/searchindex"
)

type apiConfig struct {
	ServerPort        string  `env:"SERVER_PORT"          envDefault:":8082"`
	ManticoreURL      string  `env:"MANTICORE_URL"        envDefault:""`
	R2AccountID       string  `env:"R2_ACCOUNT_ID"        envDefault:""`
	R2AccessKeyID     string  `env:"R2_ACCESS_KEY_ID"     envDefault:""`
	R2SecretAccessKey string  `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Bucket          string  `env:"R2_BUCKET"            envDefault:"stawi-jobs-content"`
	R2LogBucket       string  `env:"R2_LOG_BUCKET"        envDefault:"stawi-jobs-log"`
	R2Endpoint        string  `env:"R2_ENDPOINT"          envDefault:""`
	R2Region          string  `env:"R2_REGION"            envDefault:"auto"`
	R2DeployHookURL   string  `env:"R2_DEPLOY_HOOK_URL"   envDefault:""`
	PublishMinQuality float64 `env:"PUBLISH_MIN_QUALITY"  envDefault:"50"`
	AnalyticsBaseURL  string  `env:"ANALYTICS_BASE_URL"   envDefault:""`
	AnalyticsOrg      string  `env:"ANALYTICS_ORG"        envDefault:"default"`
	AnalyticsUsername string  `env:"ANALYTICS_USERNAME"   envDefault:""`
	AnalyticsPassword string  `env:"ANALYTICS_PASSWORD"   envDefault:""`
}

func main() {
	ctx := context.Background()
	log := util.Log(ctx)

	var cfg apiConfig
	if err := env.Parse(&cfg); err != nil {
		log.WithError(err).Fatal("api: parse config failed")
	}

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
	var r2LogReader *eventlog.Reader
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" {
		r2Publisher = publish.NewR2Publisher(
			cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey,
			cfg.R2Bucket, cfg.R2DeployHookURL,
		)
		log.WithField("bucket", cfg.R2Bucket).Info("R2 publisher enabled")

		logClient := eventlog.NewClient(eventlog.R2Config{
			AccountID:       cfg.R2AccountID,
			AccessKeyID:     cfg.R2AccessKeyID,
			SecretAccessKey: cfg.R2SecretAccessKey,
			Bucket:          cfg.R2LogBucket,
			Endpoint:        cfg.R2Endpoint,
			UsePathStyle:    cfg.R2Endpoint != "",
		})
		r2LogReader = eventlog.NewReader(logClient, cfg.R2LogBucket)
	}

	analyticsClient := analytics.New(analytics.Config{
		BaseURL:  cfg.AnalyticsBaseURL,
		Org:      cfg.AnalyticsOrg,
		Username: cfg.AnalyticsUsername,
		Password: cfg.AnalyticsPassword,
	})

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

	// v2 endpoints (Manticore-only).
	mux.HandleFunc("GET /api/v2/search", v2SearchHandler(jm))
	mux.HandleFunc("GET /api/v2/jobs/{id}", v2JobByIDHandler(jm))
	mux.HandleFunc("GET /api/v2/jobs/top", v2TopHandler(jm))
	mux.HandleFunc("GET /api/v2/jobs/latest", v2LatestHandler(jm))
	mux.HandleFunc("GET /api/v2/categories", v2CategoriesHandler(jm))
	mux.HandleFunc("GET /api/v2/stats", v2StatsHandler(jm))
	mux.HandleFunc("GET /api/v2/feed", v2FeedHandler(jm))
	mux.HandleFunc("GET /api/v2/feed/tier", v2FeedTierHandler(jm))

	// Legacy shims - v1 paths route to v2 handlers during transition.
	mux.HandleFunc("GET /api/search", v2SearchHandler(jm))
	mux.HandleFunc("GET /api/categories", v2CategoriesHandler(jm))
	mux.HandleFunc("GET /api/jobs/latest", v2LatestHandler(jm))
	mux.HandleFunc("GET /api/stats/summary", v2StatsHandler(jm))
	mux.HandleFunc("GET /api/feed", v2FeedHandler(jm))
	mux.HandleFunc("GET /api/feed/tier", v2FeedTierHandler(jm))
	mux.HandleFunc("GET /jobs/top", v2TopHandler(jm))
	mux.HandleFunc("GET /jobs/{id}", v2JobByIDHandler(jm))
	mux.HandleFunc("GET /categories", v2CategoriesHandler(jm))
	mux.HandleFunc("GET /stats", v2StatsHandler(jm))
	mux.HandleFunc("GET /search", v2SearchHandler(jm))

	// Hugo snapshot publishing — sources from R2 Parquet.
	if r2Publisher != nil && r2LogReader != nil {
		mux.HandleFunc("POST /admin/backfill",
			backfillParquetHandler(r2LogReader, r2Publisher, cfg.PublishMinQuality))
	}

	// Analytics beacon — no DB.
	mux.HandleFunc("OPTIONS /jobs/{slug}/view", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /jobs/{slug}/view", func(w http.ResponseWriter, req *http.Request) {
		slug := req.PathValue("slug")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if slug != "" && analyticsClient != nil {
			evt := map[string]any{
				"event":      "server_view",
				"slug":       slug,
				"ip_hash":    hashIP(req),
				"user_agent": req.Header.Get("User-Agent"),
				"referer":    req.Header.Get("Referer"),
				"cf_country": req.Header.Get("CF-IPCountry"),
				"cf_ray":     req.Header.Get("CF-Ray"),
			}
			if profileID := profileIDFromJWT(req); profileID != "" {
				evt["profile_id"] = profileID
			}
			analyticsClient.Send(req.Context(), "stawi_jobs_views", evt)
		}
		w.WriteHeader(http.StatusNoContent)
	})

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
