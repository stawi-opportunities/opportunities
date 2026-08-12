package tests

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/config"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/frametests"
	"github.com/pitabwire/frame/v2/frametests/definition"
	"github.com/pitabwire/frame/v2/frametests/deps/testpostgres"
	"github.com/pitabwire/frame/v2/security"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	calconfig "github.com/stawi-opportunities/opportunities/apps/calendar/config"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/business"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/repository"
)

type CalendarBaseTestSuite struct {
	frametests.FrameBaseTestSuite
}

func initResources(_ context.Context) []definition.TestResource {
	return []definition.TestResource{
		testpostgres.NewWithOpts("service_calendar", definition.WithUserName("test")),
	}
}

func (s *CalendarBaseTestSuite) SetupSuite() {
	s.InitResourceFunc = initResources
	s.FrameBaseTestSuite.SetupSuite()
}

type Deps struct {
	Svc    *business.Service
	Frame  *frame.Service
	Memory *business.MemoryProvider
}

func (s *CalendarBaseTestSuite) CreateService(t *testing.T) (context.Context, *Deps) {
	t.Helper()
	ctx := t.Context()
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("AUTH_REQUIRE_JWT", "false")

	cfg, err := config.FromEnv[calconfig.Config]()
	if err != nil {
		cfg = calconfig.Config{}
	}
	cfg.LogLevel = "error"
	cfg.RunServiceSecurely = false
	cfg.AuthRequireJWT = false
	cfg.ServerPort = ""
	cfg.ServiceName = "service_calendar"
	cfg.MigrationPath = "../migrations/0001"

	depOpts := definition.NewDependancyOption("cal", "cal_", s.Resources())
	res := depOpts.ByIsDatabase(ctx)
	require.NotNil(t, res)

	testDS, cleanup, err := res.GetRandomisedDS(ctx, depOpts.Prefix())
	require.NoError(t, err)
	t.Cleanup(func() { cleanup(ctx) })

	cfg.DatabasePrimaryURL = []string{testDS.String()}
	cfg.DatabaseReplicaURL = []string{testDS.String()}

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("calendar tests"),
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
		frametests.WithNoopDriver(),
	)
	t.Cleanup(func() { svc.Stop(ctx) })

	require.NoError(t, repository.Migrate(ctx, svc.DatastoreManager(), cfg.MigrationPath))

	dbPool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	require.NotNil(t, dbPool)
	workMan := svc.WorkManager()

	mem := business.NewMemoryProvider()
	providers := business.ProviderRegistry{"memory": mem}

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

	return ctx, &Deps{Svc: biz, Frame: svc, Memory: mem}
}

func ClaimsContext(ctx context.Context, tenant, partition, profile string) context.Context {
	c := &security.AuthenticationClaims{
		TenantID: tenant, PartitionID: partition, ProfileID: profile,
		RegisteredClaims: jwt.RegisteredClaims{Subject: profile},
	}
	return c.ClaimsToContext(ctx)
}

var _ suite.SetupAllSuite = (*CalendarBaseTestSuite)(nil)
