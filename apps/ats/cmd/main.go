package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/util"

	atsconfig "github.com/stawi-opportunities/opportunities/apps/ats/config"
	v1 "github.com/stawi-opportunities/opportunities/apps/ats/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/ats"
)

func main() {
	ctx := context.Background()
	log := util.Log(ctx)

	cfg := atsconfig.Config{}
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		log.Fatal("ats: DATABASE_URL required")
	}
	gdb := pool.DB(ctx, false)
	if gdb == nil {
		log.Fatal("ats: no gorm DB from pool")
	}

	store := ats.NewStore(gdb)
	if err := store.Migrate(ctx); err != nil {
		log.WithError(err).Fatal("ats: migrate")
	}

	atsSvc := ats.NewService(store)

	var authenticator security.Authenticator
	if secMgr := svc.SecurityManager(); secMgr != nil {
		authenticator = secMgr.GetAuthenticator(ctx)
	}
	allowHeaders := false
	if authenticator != nil {
		log.Info("ats: private routes require JWT")
	} else if cfg.AuthRequireJWT {
		log.Fatal("ats: no OIDC authenticator — configure OIDC or set AUTH_REQUIRE_JWT=false for local/tests")
	} else {
		allowHeaders = true
		log.Warn("ats: AUTH_REQUIRE_JWT=false — accepting X-Profile-ID / X-Tenant-ID / X-Partition-ID (dev only)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"service": "ats",
			"enabled": cfg.ATSEnabled,
		})
	})

	if cfg.ATSEnabled {
		v1.Mount(mux, &v1.Deps{
			Svc:  atsSvc,
			Auth: v1.TenancyAuth(authenticator, allowHeaders),
		})
		log.Info("ats: HTTP routes mounted")
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.WithField("addr", cfg.HTTPAddr).Info("ats: starting http server")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.WithError(err).Error("ats: http server crashed")
		os.Exit(1)
	}
}
