package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	appsconfig "github.com/stawi-opportunities/opportunities/apps/applications/config"
)

func main() {
	ctx := context.Background()
	log := util.Log(ctx)

	cfg := appsconfig.Config{}
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		log.Fatal("applications: DATABASE_URL required")
	}
	gdb := pool.DB(ctx, false)
	sqlDB, err := gdb.DB()
	if err != nil {
		log.WithError(err).Fatal("applications: open *sql.DB")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"enabled": cfg.ApplicationsEnabled,
		})
	})

	if cfg.ApplicationsEnabled {
		// Business routes are mounted by handlers.Register in Task 9-10
		// once those handlers land. Until then we expose only /healthz.
		log.Info("applications: enabled, but no business routes wired yet (Task 11 wires them)")
	} else {
		log.Info("applications: APPLICATIONS_ENABLED=false; only healthz exposed")
	}

	_ = sqlDB // used by handlers in Task 11
	_ = cfg.IdempotencyTTLHours

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.WithField("addr", cfg.HTTPAddr).Info("applications: starting http server")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.WithError(err).Error("applications: http server crashed")
		os.Exit(1)
	}
}
