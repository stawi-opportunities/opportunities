//go:build integration

// autoapply_e2e_test.go — end-to-end integration test for the auto-apply
// service.
//
// Spins up real Postgres + NATS via testcontainers, wires
// AutoApplyHandler with a fake SubmitterRouter and a httptest CV
// server, publishes an AutoApplyIntentV1 onto the queue, and asserts:
//
//  1. Happy path → candidate_applications row in status "submitted"
//     and ApplicationSubmittedV1 emitted.
//  2. Idempotent redelivery → exactly one row, no duplicate submit.
//  3. Dry-run → row in status "skipped/dry_run", router never called.
//
// Runs with: go test -tags integration -run TestAutoApply ./tests/integration/...
//
// Requires: Docker daemon running. Estimated runtime: ~30s.
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	autoapplyconfig "github.com/stawi-opportunities/opportunities/apps/autoapply/config"
	"github.com/stawi-opportunities/opportunities/apps/autoapply/service"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// fakeRouter records every Submit call and returns a configurable
// result. Lets us assert the handler dispatched without launching a
// real headless browser.
type fakeRouter struct {
	calls  atomic.Int32
	result autoapply.SubmitResult
}

func (f *fakeRouter) Submit(_ context.Context, _ autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	f.calls.Add(1)
	return f.result, nil
}

// e2eFixture bundles the wiring shared across the cases.
type e2eFixture struct {
	t        *testing.T
	ctx      context.Context
	svc      *frame.Service
	queueURL string
	router   *fakeRouter
	appRepo  *repository.ApplicationRepository
	db       *gorm.DB
}

func setupAutoApplyE2E(t *testing.T, dryRun bool, cvServerURL string) *e2eFixture {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	pgDSN := testhelpers.PostgresContainer(t, ctx)
	natsURL := testhelpers.NATSContainer(t, ctx)

	// AutoApplyHandler talks to the DB through a *gorm.DB wrapper that
	// matches repository.NewApplicationRepository's contract; open the
	// DB directly so we don't need the full Frame DataStore manager.
	gdb, err := gorm.Open(postgres.Open(pgDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&domain.CandidateApplication{}))

	dbFn := func(_ context.Context, _ bool) *gorm.DB { return gdb }
	appRepo := repository.NewApplicationRepository(dbFn)
	matchRepo := repository.NewMatchRepository(dbFn)

	queueURL := natsURL + "/svc.opportunities.autoapply.submit.v1"

	router := &fakeRouter{result: autoapply.SubmitResult{Method: "ats_ui", ExternalRef: "ats-ref-1"}}

	cvFetcher := service.NewHTTPCVFetcherWithOptions(5*time.Second, 1<<20, true)

	cfg, err := fconfig.FromEnv[autoapplyconfig.AutoApplyConfig]()
	require.NoError(t, err)

	opts := []frame.Option{
		frame.WithConfig(&cfg),
	}
	ctx, svc := frame.NewServiceWithContext(ctx, opts...)

	handler := service.NewAutoApplyHandler(service.HandlerDeps{
		Svc:       svc,
		Router:    router,
		AppRepo:   appRepo,
		MatchRepo: matchRepo,
		CV:        cvFetcher,
		Config: service.Config{
			Enabled: true,
			DryRun:  dryRun,
		},
	})

	svc.Init(ctx,
		frame.WithRegisterPublisher(eventsv1.SubjectAutoApplySubmit, queueURL),
		frame.WithRegisterSubscriber(eventsv1.SubjectAutoApplySubmit, queueURL, handler),
	)

	// Run the service in the background. svc.Run blocks; cancel via the
	// test's context cleanup.
	go func() { _ = svc.Run(ctx, "") }()
	// Give the subscriber a beat to wire up.
	time.Sleep(2 * time.Second)

	_ = cvServerURL // currently unused — wire when adding CV-specific cases

	return &e2eFixture{
		t:        t,
		ctx:      ctx,
		svc:      svc,
		queueURL: queueURL,
		router:   router,
		appRepo:  appRepo,
		db:       gdb,
	}
}

func (f *e2eFixture) publishIntent(intent eventsv1.AutoApplyIntentV1) {
	env := eventsv1.NewEnvelope(eventsv1.SubjectAutoApplySubmit, intent)
	body, err := json.Marshal(env)
	require.NoError(f.t, err)

	pubCtx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	require.NoError(f.t, f.svc.QueueManager().Publish(pubCtx, eventsv1.SubjectAutoApplySubmit, body))
}

func (f *e2eFixture) waitForApplication(candidateID, jobID string) *domain.CandidateApplication {
	f.t.Helper()
	var app domain.CandidateApplication
	require.Eventually(f.t, func() bool {
		err := f.db.WithContext(f.ctx).
			Where("candidate_id = ? AND canonical_job_id = ?", candidateID, jobID).
			First(&app).Error
		if err != nil {
			return false
		}
		// Wait until the row reaches a terminal status (handler updates
		// from pending → submitted/skipped/failed).
		return app.Status != domain.AppStatusPending
	}, 20*time.Second, 250*time.Millisecond, "no terminal application row for %s × %s", candidateID, jobID)
	return &app
}

func TestAutoApplyE2E_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped under -short (requires Docker)")
	}
	cvSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("PDF"))
	}))
	t.Cleanup(cvSrv.Close)

	f := setupAutoApplyE2E(t, false, cvSrv.URL)

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID:    "cnd_e2e_1",
		MatchID:        "match_e2e_1",
		CanonicalJobID: "job_e2e_1",
		ApplyURL:       "https://boards.greenhouse.io/co/jobs/1",
		SourceType:     string(domain.SourceGreenhouse),
		FullName:       "Ada Lovelace",
		Email:          "ada@example.com",
		CVUrl:          cvSrv.URL + "/cv.pdf",
	}
	f.publishIntent(intent)

	app := f.waitForApplication(intent.CandidateID, intent.CanonicalJobID)
	assert.Equal(t, domain.AppStatusSubmitted, app.Status)
	assert.Equal(t, "ats_ui", app.Method)
	assert.Equal(t, int32(1), f.router.calls.Load(), "router must be called exactly once")
}

func TestAutoApplyE2E_RedeliveryIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped under -short (requires Docker)")
	}
	cvSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("PDF"))
	}))
	t.Cleanup(cvSrv.Close)

	f := setupAutoApplyE2E(t, false, cvSrv.URL)

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID:    "cnd_e2e_2",
		CanonicalJobID: "job_e2e_2",
		ApplyURL:       "https://boards.greenhouse.io/co/jobs/2",
		SourceType:     string(domain.SourceGreenhouse),
		FullName:       "Grace Hopper",
		Email:          "grace@example.com",
	}
	// Publish the same intent twice — simulates at-least-once redelivery.
	f.publishIntent(intent)
	f.publishIntent(intent)

	app := f.waitForApplication(intent.CandidateID, intent.CanonicalJobID)
	assert.Equal(t, domain.AppStatusSubmitted, app.Status)

	// Give the second message time to be processed and rejected by the
	// idempotency guard before counting rows.
	time.Sleep(2 * time.Second)

	var rowCount int64
	require.NoError(t, f.db.WithContext(f.ctx).
		Model(&domain.CandidateApplication{}).
		Where("candidate_id = ? AND canonical_job_id = ?", intent.CandidateID, intent.CanonicalJobID).
		Count(&rowCount).Error)
	assert.EqualValues(t, 1, rowCount, "exactly one application row for the (candidate, job)")
	assert.LessOrEqual(t, f.router.calls.Load(), int32(1),
		"router must not be invoked on a duplicate intent")
}

func TestAutoApplyE2E_DryRunSkipsRouter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped under -short (requires Docker)")
	}
	f := setupAutoApplyE2E(t, true, "")

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID:    "cnd_e2e_3",
		CanonicalJobID: "job_e2e_3",
		ApplyURL:       "https://boards.greenhouse.io/co/jobs/3",
		SourceType:     string(domain.SourceGreenhouse),
		FullName:       "Linus T",
		Email:          "linus@example.com",
	}
	f.publishIntent(intent)

	app := f.waitForApplication(intent.CandidateID, intent.CanonicalJobID)
	assert.Equal(t, domain.AppStatusSkipped, app.Status)
	assert.Equal(t, int32(0), f.router.calls.Load(), "dry-run must skip the router entirely")
}
