package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	apis "github.com/antinvestor/common/v2"
	"github.com/antinvestor/common/v2/connection"
	"github.com/antinvestor/common/v2/servicecatalog"
	_ "github.com/jackc/pgx/v5/stdlib"
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
	"github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1/calendarv1connect"
	"github.com/stawi-opportunities/opportunities/pkg/calendarclient"
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

	projections := repository.NewJobProjectionRepository(ctx, dbPool, workMan)
	outbox := repository.NewOutboxRepository(ctx, dbPool, workMan)
	idem := repository.NewIdempotencyRepository(ctx, dbPool, workMan)

	// Matching talent: optional separate DB, else primary pool SQL (graceful empty).
	var matching business.MatchingTalent = business.EmptyTalent{}
	if matchDB, err := openOptionalSQL(ctx, cfg.MatchingDatabaseURL); err != nil {
		log.WithError(err).Warn("ats: matching DB open failed; talent shortlist empty")
	} else if matchDB != nil {
		matching = business.SQLMatchingTalent{DB: matchDB}
		log.Info("ats: matching talent via ATS_MATCHING_DATABASE_URL")
	} else if gdb := dbPool.DB(ctx, true); gdb != nil {
		if sqlDB, err := gdb.DB(); err == nil && sqlDB != nil {
			matching = business.SQLMatchingTalent{DB: sqlDB}
			log.Info("ats: matching talent via primary datastore (candidate_profiles when present)")
		}
	}

	var productDB *sql.DB
	if pdb, err := openOptionalSQL(ctx, cfg.ProductDatabaseURL); err != nil {
		log.WithError(err).Warn("ats: product DB open failed; projection-only publish")
	} else {
		productDB = pdb
	}

	publisher := business.ProjectionPublisher{
		Projections: projections,
		ProductDB:   productDB,
	}

	notifyClient, err := setupNotificationClient(ctx, cfg)
	if err != nil {
		log.WithError(err).Warn("ats: notification client unavailable; outbox will retry")
	}

	if cfg.CalendarServiceURI == "" {
		return nil, errors.New("ats: CALENDAR_SERVICE_URI is required (service_calendar is the only scheduling plane)")
	}
	calCli, cerr := setupCalendarClient(ctx, svc, cfg)
	if cerr != nil {
		return nil, fmt.Errorf("ats: calendar client: %w", cerr)
	}
	if calCli == nil {
		return nil, errors.New("ats: calendar client is nil")
	}
	interviewCal := &business.RemoteInterviewCalendar{Client: calCli}
	log.Info("ats: service_calendar required and wired for all interview scheduling")

	biz := business.NewService(business.Deps{
		Jobs:         repository.NewJobRepository(ctx, dbPool, workMan),
		Applications: repository.NewApplicationRepository(ctx, dbPool, workMan),
		StageEvents:  repository.NewStageEventRepository(ctx, dbPool, workMan),
		Availability: repository.NewAvailabilityRepository(ctx, dbPool, workMan),
		Interviews:   repository.NewInterviewRepository(ctx, dbPool, workMan),
		Hires:        repository.NewHireOutcomeRepository(ctx, dbPool, workMan),
		Outbox:       outbox,
		AiRuns:       repository.NewAiRunRepository(ctx, dbPool, workMan),
		Projections:  projections,
		Idempotency:  idem,
		Matching:     matching,
		Publisher:    publisher,
		Billing:      business.LedgerBillingEmitter{Prefix: "result_hire"},
		Notify: business.NotificationNotifier{
			Outbox:      outbox,
			Notify:      notifyClient,
			Template:    cfg.MessageTemplateInterviewScheduled,
			SiteBaseURL: cfg.PublicSiteURL,
		},
		Calendar: interviewCal,
	})

	// Background outbox drain (email/ICS).
	poll := time.Duration(cfg.OutboxPollIntervalSeconds) * time.Second
	if poll <= 0 {
		poll = 15 * time.Second
	}
	worker := &business.OutboxWorker{
		Outbox:   outbox,
		Notify:   notifyClient,
		Template: cfg.MessageTemplateInterviewScheduled,
		Interval: poll,
	}
	go worker.Run(ctx)

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
		Idempotency:        idem,
	})
	if err != nil {
		return nil, err
	}
	log.Info("ats: Connect API mounted (ats.v1.AtsService, namespace service_ats)")
	return []frame.Option{frame.WithHTTPHandler(handlers.CORSMiddleware(mux))}, nil
}

func setupCalendarClient(
	ctx context.Context,
	svc *frame.Service,
	cfg *atsconfig.Config,
) (calendarv1connect.CalendarServiceClient, error) {
	if cfg.CalendarServiceURI == "" {
		return nil, nil
	}
	// Direct HTTP for local/dev (or until servicecatalog ships ServiceCalendar).
	if cfg.CalendarDirect || !cfg.AuthRequireJWT {
		httpClient := svc.HTTPClientManager().Client(ctx)
		return calendarclient.NewDirectClient(httpClient, cfg.CalendarServiceURI), nil
	}
	// Mesh path: use ServiceJobs audience as temporary product peer until
	// antinvestor/common adds ServiceCalendar (CALENDAR_OAUTH_USE_JOBS_AUDIENCE).
	// Prefer WithoutAuthentication + direct if dial fails.
	cli, err := calendarclient.NewClient(ctx, cfg, cfg.CalendarServiceURI, cfg.CalendarServiceWorkloadAPITargetPath)
	if err != nil {
		log := util.Log(ctx)
		log.WithError(err).Warn("ats: calendar mesh dial failed; trying direct HTTP")
		httpClient := svc.HTTPClientManager().Client(ctx)
		return calendarclient.NewDirectClient(httpClient, cfg.CalendarServiceURI), nil
	}
	return cli, nil
}

func setupNotificationClient(
	ctx context.Context,
	cfg *atsconfig.Config,
) (notificationv1connect.NotificationServiceClient, error) {
	if cfg.NotificationServiceURI == "" {
		return nil, nil
	}
	return connection.NewServiceClient(ctx, cfg, apis.ServiceTarget{
		Endpoint:              cfg.NotificationServiceURI,
		WorkloadAPITargetPath: cfg.NotificationServiceWorkloadAPITargetPath,
		ServiceID:             servicecatalog.ServiceNotification,
	}, notificationv1connect.NewNotificationServiceClient)
}

func openOptionalSQL(ctx context.Context, dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, nil
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
