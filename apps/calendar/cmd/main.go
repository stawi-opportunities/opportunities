package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/config"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/frame/v2/setup"
	"github.com/pitabwire/util"

	calconfig "github.com/stawi-opportunities/opportunities/apps/calendar/config"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/business"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/handlers"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/repository"
)

func main() {
	const serviceName = "service_calendar"
	ctx := context.Background()
	log := util.Log(ctx)

	cfg, err := config.FromEnv[calconfig.Config]()
	if err != nil {
		log.WithError(err).Warn("calendar: FromEnv incomplete; using defaults")
		cfg = calconfig.Config{}
	}
	if cfg.Name() == "" {
		cfg.ServiceName = serviceName
	}
	if v := os.Getenv("AUTH_REQUIRE_JWT"); v == "false" || v == "0" {
		cfg.AuthRequireJWT = false
	}
	if cfg.ServerPort == "" {
		if addr := os.Getenv("HTTP_ADDR"); len(addr) > 1 && addr[0] == ':' {
			cfg.ServerPort = addr[1:]
		} else {
			cfg.ServerPort = "8096"
		}
	}

	ctx, svc := frame.NewServiceWithContext(ctx, frame.WithConfig(&cfg), frame.WithDatastore())
	defer svc.Stop(ctx)
	log = util.Log(ctx)

	migrationPath := cfg.MigrationPath
	if migrationPath == "" {
		migrationPath = "apps/calendar/migrations/0001"
	}
	svc.Setup().RegisterFunc(setup.NameMigrate, func(mctx context.Context) error {
		return repository.Migrate(mctx, svc.DatastoreManager(), migrationPath)
	})

	if frame.ShouldRunSetup(&cfg) {
		sd := handlers.ServiceDescriptor()
		svc.Init(ctx, frame.WithPermissionRegistration(sd))
		if setupErr := svc.RunSetupForProcess(ctx, &cfg); setupErr != nil {
			log.WithError(setupErr).Fatal("calendar: setup plan failed")
		}
		log.Info("calendar: setup complete (migrate + permissions) — exiting")
		return
	}

	opts, err := initRuntime(ctx, svc, &cfg)
	if err != nil {
		log.WithError(err).Fatal("calendar: runtime init")
	}
	svc.Init(ctx, opts...)
	if err := svc.Run(ctx, ""); err != nil {
		log.WithError(err).Fatal("calendar: could not run server")
	}
}

func initRuntime(ctx context.Context, svc *frame.Service, cfg *calconfig.Config) ([]frame.Option, error) {
	log := util.Log(ctx)
	dbPool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if dbPool == nil {
		return nil, errors.New("calendar: DATABASE_URL / datastore pool required")
	}
	workMan := svc.WorkManager()

	// Timed HTTP client for external calendar APIs (never http.DefaultClient long-lived).
	httpClient := svc.HTTPClientManager().Client(ctx)
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	providers := business.ProviderRegistry{}
	// Live providers: Ready when env enabled (+ optional client id for OAuth products).
	// Access tokens live on ExternalConnection.credentials_json per connection.
	providers[models.ProviderGoogle] = business.GoogleCalendarProvider{
		HTTP:    httpClient,
		Enabled: cfg.GoogleCalendarEnabled,
	}
	providers[models.ProviderMicrosoft] = business.MicrosoftCalendarProvider{
		HTTP:    httpClient,
		Enabled: cfg.MicrosoftCalendarEnabled,
	}
	providers[models.ProviderCalDAV] = business.CalDAVProvider{
		HTTP:    httpClient,
		Enabled: cfg.CalDAVEnabled,
	}
	if cfg.EnableMemoryProvider {
		providers["memory"] = business.NewMemoryProvider()
		log.Info("calendar: memory external provider enabled")
	}
	if cfg.GoogleCalendarEnabled {
		log.Info("calendar: Google Calendar provider enabled")
	}
	if cfg.MicrosoftCalendarEnabled {
		log.Info("calendar: Microsoft Graph calendar provider enabled")
	}
	if cfg.CalDAVEnabled {
		log.Info("calendar: CalDAV provider enabled")
	}

	biz := business.NewService(business.Deps{
		Resources:    repository.NewResourceRepository(ctx, dbPool, workMan),
		Availability: repository.NewAvailabilityRepository(ctx, dbPool, workMan),
		Busy:         repository.NewBusyRepository(ctx, dbPool, workMan),
		Bookings:     repository.NewBookingRepository(ctx, dbPool, workMan),
		Lines:        repository.NewBookingLineRepository(ctx, dbPool, workMan),
		Connections:  repository.NewExternalConnectionRepository(ctx, dbPool, workMan),
		SyncOutbox:   repository.NewSyncOutboxRepository(ctx, dbPool, workMan),
		Providers:    providers,
	})

	poll := time.Duration(cfg.SyncPollSeconds) * time.Second
	if poll <= 0 {
		poll = 60 * time.Second
	}
	go (&business.SyncWorker{Service: biz, Interval: poll}).Run(ctx)

	allowDev := !cfg.AuthRequireJWT
	var authenticator security.Authenticator
	var authz security.Authorizer
	enforcePerms := false
	if cfg.AuthRequireJWT {
		sec := svc.SecurityManager()
		if sec == nil || sec.GetAuthenticator(ctx) == nil {
			return nil, errors.New("calendar: OIDC authenticator required when AUTH_REQUIRE_JWT=true")
		}
		authenticator = sec.GetAuthenticator(ctx)
		authz = sec.GetAuthorizer(ctx)
		enforcePerms = os.Getenv("CALENDAR_ENFORCE_PERMISSIONS") != "false" &&
			os.Getenv("CALENDAR_ENFORCE_PERMISSIONS") != "0"
		if enforcePerms && authz == nil {
			log.Warn("calendar: authorizer nil — permissions disabled")
			enforcePerms = false
		}
	}

	mux, err := handlers.NewConnectMux(ctx, biz, handlers.ConnectOptions{
		Authenticator: authenticator, Authorizer: authz,
		AllowDevHeaders: allowDev, EnforcePermissions: enforcePerms,
	})
	if err != nil {
		return nil, err
	}
	log.Info("calendar: Connect API mounted (calendar.v1.CalendarService)")
	return []frame.Option{frame.WithHTTPHandler(handlers.CORSMiddleware(mux))}, nil
}
