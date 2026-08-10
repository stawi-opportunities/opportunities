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

	atsconfig "github.com/stawi-opportunities/opportunities/apps/ats/config"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/business"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/repository"
)

// ATSBaseTestSuite boots Postgres via testcontainers (golang-patterns / testing-go).
type ATSBaseTestSuite struct {
	frametests.FrameBaseTestSuite
}

func initResources(_ context.Context) []definition.TestResource {
	return []definition.TestResource{
		testpostgres.NewWithOpts("service_ats", definition.WithUserName("test")),
	}
}

// SetupSuite starts shared containers once per suite.
func (s *ATSBaseTestSuite) SetupSuite() {
	s.InitResourceFunc = initResources
	s.FrameBaseTestSuite.SetupSuite()
}

// Deps holds wired business for a test.
type Deps struct {
	Svc         *business.Service
	Frame       *frame.Service
	Projections repository.JobProjectionRepository
	Outbox      repository.OutboxRepository
	Idempotency repository.IdempotencyRepository
}

// CreateService builds a Frame service against an isolated randomised Postgres DS.
func (s *ATSBaseTestSuite) CreateService(t *testing.T) (context.Context, *Deps) {
	t.Helper()
	ctx := t.Context()
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("AUTH_REQUIRE_JWT", "false")

	cfg, err := config.FromEnv[atsconfig.Config]()
	if err != nil {
		cfg = atsconfig.Config{}
	}
	cfg.LogLevel = "error"
	cfg.RunServiceSecurely = false
	cfg.AuthRequireJWT = false
	cfg.ServerPort = ""
	cfg.ServiceName = "service_ats"
	cfg.MigrationPath = "../migrations/0001"

	depOpts := definition.NewDependancyOption("ats", "ats_", s.Resources())
	res := depOpts.ByIsDatabase(ctx)
	require.NotNil(t, res, "postgres test resource required")

	testDS, cleanup, err := res.GetRandomisedDS(ctx, depOpts.Prefix())
	require.NoError(t, err)
	t.Cleanup(func() { cleanup(ctx) })

	cfg.DatabasePrimaryURL = []string{testDS.String()}
	cfg.DatabaseReplicaURL = []string{testDS.String()}

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("ats tests"),
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
		frametests.WithNoopDriver(),
	)
	t.Cleanup(func() { svc.Stop(ctx) })

	require.NoError(t, repository.Migrate(ctx, svc.DatastoreManager(), cfg.MigrationPath))

	dbPool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	require.NotNil(t, dbPool)
	workMan := svc.WorkManager()

	projections := repository.NewJobProjectionRepository(ctx, dbPool, workMan)
	outbox := repository.NewOutboxRepository(ctx, dbPool, workMan)
	idem := repository.NewIdempotencyRepository(ctx, dbPool, workMan)

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
		Matching:     business.EmptyTalent{},
		Publisher:    business.ProjectionPublisher{Projections: projections},
		Billing:      business.LedgerBillingEmitter{Prefix: "result_hire"},
		Notify:       business.NotificationNotifier{Outbox: outbox},
	})

	return ctx, &Deps{
		Svc:         biz,
		Frame:       svc,
		Projections: projections,
		Outbox:      outbox,
		Idempotency: idem,
	}
}

// ClaimsContext returns a context with tenancy claims for a recruiter.
func ClaimsContext(ctx context.Context, tenant, partition, profile string) context.Context {
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

// Ensure suite embedding works with testify.
var _ suite.SetupAllSuite = (*ATSBaseTestSuite)(nil)
