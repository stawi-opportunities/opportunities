package main

import (
	"context"
	"errors"
	"os"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/config"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/security"
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

	// Setup Job: migrate + permission namespace registration, then exit.
	if frame.ShouldRunSetup(&cfg) {
		sd := handlers.ServiceDescriptor()
		svc.Init(ctx, frame.WithPermissionRegistration(sd))
		if setupErr := svc.RunSetupForProcess(ctx, &cfg); setupErr != nil {
			log.WithError(setupErr).Fatal("ats: setup plan failed")
		}
		log.Info("ats: setup plan complete (migrate + permissions) — exiting")
		return
	}

	opts, err := initRuntime(ctx, svc, &cfg)
	if err != nil {
		log.WithError(err).Fatal("ats: runtime init")
	}
	// Runtime: never register permissions or migrate.
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
	// ATS_ENFORCE_PERMISSIONS=false disables Keto function checks even with JWT
	// (useful for partial envs). Default: enforce when JWT required.
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

	biz := business.NewService(business.Deps{
		Jobs:         repository.NewJobRepository(ctx, dbPool, workMan),
		Applications: repository.NewApplicationRepository(ctx, dbPool, workMan),
		StageEvents:  repository.NewStageEventRepository(ctx, dbPool, workMan),
		Availability: repository.NewAvailabilityRepository(ctx, dbPool, workMan),
		Interviews:   repository.NewInterviewRepository(ctx, dbPool, workMan),
		Hires:        repository.NewHireOutcomeRepository(ctx, dbPool, workMan),
		Outbox:       repository.NewOutboxRepository(ctx, dbPool, workMan),
		AiRuns:       repository.NewAiRunRepository(ctx, dbPool, workMan),
	})

	allowDev := !cfg.AuthRequireJWT
	var authenticator security.Authenticator
	var authz security.Authorizer
	enforcePerms := false

	if cfg.AuthRequireJWT {
		sec := svc.SecurityManager()
		if sec == nil || sec.GetAuthenticator(ctx) == nil {
			return nil, errors.New("ats: OIDC authenticator required when AUTH_REQUIRE_JWT=true")
		}
		authenticator = sec.GetAuthenticator(ctx)
		authz = sec.GetAuthorizer(ctx)
		// Enforce unless explicitly disabled.
		enforcePerms = os.Getenv("ATS_ENFORCE_PERMISSIONS") != "false" &&
			os.Getenv("ATS_ENFORCE_PERMISSIONS") != "0"
		if enforcePerms && authz == nil {
			log.Warn("ats: authorizer nil — function permissions disabled")
			enforcePerms = false
		}
	}

	mux, err := handlers.NewConnectMux(ctx, biz, handlers.ConnectOptions{
		Authenticator:      authenticator,
		Authorizer:         authz,
		AllowDevHeaders:    allowDev,
		EnforcePermissions: enforcePerms,
	})
	if err != nil {
		return nil, err
	}
	log.Info("ats: Connect API mounted (ats.v1.AtsService, namespace service_ats)")
	return []frame.Option{frame.WithHTTPHandler(handlers.CORSMiddleware(mux))}, nil
}
