package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/util"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	atsconfig "github.com/stawi-opportunities/opportunities/apps/ats/config"
	v1 "github.com/stawi-opportunities/opportunities/apps/ats/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/ats"
)

func main() {
	ctx := context.Background()
	log := util.Log(ctx)

	cfg := atsconfig.Config{}
	ctx, frameSvc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer frameSvc.Stop(ctx)

	// Env overrides that may not bind if ConfigurationDefault races local flags.
	if v := os.Getenv("ATS_SQLITE_PATH"); v != "" {
		cfg.SQLitePath = v
	}
	if os.Getenv("ATS_AUTO_SEED") == "true" || os.Getenv("ATS_AUTO_SEED") == "1" {
		cfg.AutoSeed = true
	}
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if os.Getenv("AUTH_REQUIRE_JWT") == "false" || os.Getenv("AUTH_REQUIRE_JWT") == "0" {
		cfg.AuthRequireJWT = false
	}
	// Frame config binding can leave bool zero-values; default ATS on unless explicitly disabled.
	if os.Getenv("ATS_ENABLED") == "false" || os.Getenv("ATS_ENABLED") == "0" {
		cfg.ATSEnabled = false
	} else {
		cfg.ATSEnabled = true
	}

	gdb, err := openDB(ctx, frameSvc, &cfg)
	if err != nil {
		log.WithError(err).Fatal("ats: database")
	}
	if gdb == nil {
		log.Fatal("ats: database handle is nil")
	}

	store := ats.NewStore(gdb)
	if err := store.Migrate(ctx); err != nil {
		log.WithError(err).Fatal("ats: migrate")
	}

	atsSvc := ats.NewService(store)

	var authenticator security.Authenticator
	allowHeaders := false
	if cfg.AuthRequireJWT {
		if secMgr := frameSvc.SecurityManager(); secMgr != nil {
			authenticator = secMgr.GetAuthenticator(ctx)
		}
		if authenticator == nil {
			log.Fatal("ats: no OIDC authenticator — configure OIDC or set AUTH_REQUIRE_JWT=false for local/tests")
		}
		log.Info("ats: private routes require JWT")
	} else {
		// Dev mode: never require JWT even if a security manager exists.
		allowHeaders = true
		log.Warn("ats: AUTH_REQUIRE_JWT=false — accepting X-Profile-ID / X-Tenant-ID / X-Partition-ID (dev only)")
	}

	if cfg.AutoSeed && allowHeaders {
		seedCtx := devClaimsContext(ctx, "dev-recruiter", "dev-tenant", "dev-partition")
		if err := ats.SeedDemoWorkspace(seedCtx, atsSvc); err != nil {
			log.WithError(err).Warn("ats: auto-seed failed")
		} else {
			log.Info("ats: demo workspace seeded (or already present)")
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"service": "ats",
			"enabled": cfg.ATSEnabled,
			"sqlite":  cfg.SQLitePath != "",
		})
	})

	if cfg.ATSEnabled {
		v1.Mount(mux, &v1.Deps{
			Svc:  atsSvc,
			Auth: v1.TenancyAuth(authenticator, allowHeaders),
		})
		log.Info("ats: HTTP routes mounted")
	}

	handler := corsMiddleware(mux)
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.WithField("addr", cfg.HTTPAddr).Info("ats: starting http server")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.WithError(err).Error("ats: http server crashed")
		os.Exit(1)
	}
}

func openDB(ctx context.Context, frameSvc *frame.Service, cfg *atsconfig.Config) (*gorm.DB, error) {
	log := util.Log(ctx)
	if cfg.SQLitePath != "" {
		return openSQLite(cfg.SQLitePath)
	}
	// Prefer explicit sqlite for local ATS even if a global DATABASE_URL exists,
	// when AUTH is in dev mode and user asked for auto-seed demos.
	if !cfg.AuthRequireJWT && os.Getenv("ATS_FORCE_POSTGRES") != "true" {
		// Try postgres first only if DATABASE_URL is set and looks usable.
		if os.Getenv("DATABASE_URL") == "" {
			path := "data/ats.sqlite"
			cfg.SQLitePath = path
			log.WithField("path", path).Info("ats: using local sqlite (no DATABASE_URL)")
			return openSQLite(path)
		}
	}

	pool := frameSvc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		path := "data/ats.sqlite"
		cfg.SQLitePath = path
		log.WithField("path", path).Warn("ats: no datastore pool — using sqlite")
		return openSQLite(path)
	}
	gdb := pool.DB(ctx, false)
	if gdb == nil {
		path := "data/ats.sqlite"
		cfg.SQLitePath = path
		log.WithField("path", path).Warn("ats: pool DB nil — using sqlite")
		return openSQLite(path)
	}
	// Verify connectivity; fall back to sqlite for local usefulness.
	if sqlDB, err := gdb.DB(); err != nil || sqlDB.PingContext(ctx) != nil {
		path := "data/ats.sqlite"
		cfg.SQLitePath = path
		log.WithField("path", path).Warn("ats: postgres unreachable — using sqlite")
		return openSQLite(path)
	}
	log.Info("ats: using Frame datastore (postgres)")
	return gdb, nil
}

func openSQLite(path string) (*gorm.DB, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
}

func devClaimsContext(ctx context.Context, profile, tenant, partition string) context.Context {
	c := &security.AuthenticationClaims{
		TenantID:    tenant,
		PartitionID: partition,
		ProfileID:   profile,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: profile,
		},
	}
	return c.ClaimsToContext(ctx)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Profile-ID, X-Tenant-ID, X-Partition-ID, Idempotency-Key")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
