package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/config"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/setup"
	"github.com/pitabwire/util"

	atsconfig "github.com/stawi-opportunities/opportunities/apps/ats/config"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/business"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/handlers"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/repository"
)

func main() {
	const serviceName = "service_ats"
	ctx := context.Background()
	log := util.Log(ctx)

	cfg, err := config.FromEnv[atsconfig.Config]()
	if err != nil {
		log.WithError(err).Warn("ats: FromEnv incomplete; using defaults + process env")
		cfg = atsconfig.Config{}
	}
	if cfg.Name() == "" {
		cfg.ServiceName = serviceName
	}
	applyLocalEnvOverrides(&cfg)

	ctx, svc := frame.NewServiceWithContext(
		ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)
	log = util.Log(ctx)

	migrationPath := cfg.MigrationPath
	if migrationPath == "" {
		migrationPath = "apps/ats/migrations/0001"
	}
	svc.Setup().RegisterFunc(setup.NameMigrate, func(mctx context.Context) error {
		return repository.Migrate(mctx, svc.DatastoreManager(), migrationPath)
	})

	// Setup Job only: migrate, then exit. Runtime never migrates.
	if frame.ShouldRunSetup(&cfg) {
		if setupErr := svc.RunSetupForProcess(ctx, &cfg); setupErr != nil {
			log.WithError(setupErr).Fatal("ats: setup plan failed")
		}
		log.Info("ats: setup plan complete — exiting")
		return
	}

	opts, err := initRuntime(ctx, svc, &cfg)
	if err != nil {
		log.WithError(err).Fatal("ats: runtime init")
	}
	svc.Init(ctx, opts...)
	if err := svc.Run(ctx, ""); err != nil {
		log.WithError(err).Fatal("ats: could not run server")
	}
}

func applyLocalEnvOverrides(cfg *atsconfig.Config) {
	if v := os.Getenv("AUTH_REQUIRE_JWT"); v == "false" || v == "0" {
		cfg.AuthRequireJWT = false
	}
	if v := os.Getenv("ATS_AUTO_SEED"); v == "true" || v == "1" {
		cfg.AutoSeed = true
	}
	if v := os.Getenv("ATS_MIGRATION_PATH"); v != "" {
		cfg.MigrationPath = v
	}
	if cfg.ServerPort == "" {
		if addr := os.Getenv("HTTP_ADDR"); len(addr) > 1 && addr[0] == ':' {
			cfg.ServerPort = addr[1:]
		} else {
			cfg.ServerPort = "8095"
		}
	}
}

func initRuntime(ctx context.Context, svc *frame.Service, cfg *atsconfig.Config) ([]frame.Option, error) {
	log := util.Log(ctx)
	dbPool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if dbPool == nil {
		return nil, errors.New("ats: DATABASE_URL / datastore pool required (Postgres only)")
	}
	workMan := svc.WorkManager()

	jobRepo := repository.NewJobRepository(ctx, dbPool, workMan)
	appRepo := repository.NewApplicationRepository(ctx, dbPool, workMan)
	stageRepo := repository.NewStageEventRepository(ctx, dbPool, workMan)
	availRepo := repository.NewAvailabilityRepository(ctx, dbPool, workMan)
	interviewRepo := repository.NewInterviewRepository(ctx, dbPool, workMan)
	hireRepo := repository.NewHireOutcomeRepository(ctx, dbPool, workMan)
	outboxRepo := repository.NewOutboxRepository(ctx, dbPool, workMan)
	aiRepo := repository.NewAiRunRepository(ctx, dbPool, workMan)

	biz := business.NewService(business.Deps{
		Jobs: jobRepo, Applications: appRepo, StageEvents: stageRepo,
		Availability: availRepo, Interviews: interviewRepo, Hires: hireRepo,
		Outbox: outboxRepo, AiRuns: aiRepo,
	})

	var authMW func(http.Handler) http.Handler
	if cfg.AuthRequireJWT {
		sec := svc.SecurityManager()
		if sec == nil || sec.GetAuthenticator(ctx) == nil {
			return nil, errors.New("ats: OIDC authenticator required when AUTH_REQUIRE_JWT=true")
		}
		authMW = handlers.TenancyAuth(sec.GetAuthenticator(ctx), false)
		log.Info("ats: private routes require JWT")
	} else {
		authMW = handlers.TenancyAuth(nil, true)
		log.Warn("ats: AUTH_REQUIRE_JWT=false — tenancy headers allowed (dev only)")
	}

	api := handlers.NewServer(biz, authMW)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "ats"})
	})
	api.Mount(mux)

	return []frame.Option{frame.WithHTTPHandler(corsMiddleware(mux))}, nil
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
