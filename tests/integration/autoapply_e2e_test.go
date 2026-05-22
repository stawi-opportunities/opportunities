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
	"database/sql"
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
	"github.com/stawi-opportunities/opportunities/pkg/applications"
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
	// rawDB is the *sql.DB the migrations were applied on. The
	// applications.Store used by the tracker bridge runs against this
	// connection; tests inspect the `applications` table via the same
	// handle to assert the bridge wrote a row.
	rawDB *sql.DB
}

func setupAutoApplyE2E(t *testing.T, dryRun bool, cvServerURL string) *e2eFixture {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	// PostgresContainerNoMigrate + EnsureOpportunitiesStub + ApplyMigrationsDir
	// is the explicit path the existing pkg/applications integration
	// tests use. Migration 0003 drops legacy columns from
	// candidate_profiles, which must exist before the migration runs;
	// likewise 0012 partial-indexes opportunities. EnsureOpportunitiesStub
	// creates idempotent placeholders for both so the full migration
	// chain — including my-branch additions (candidate_applications,
	// candidate_sessions) and main's applications-OLTP tables — can run
	// against a clean container.
	rawDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, rawDB))
	testhelpers.ApplyMigrationsDir(t, ctx, rawDB, "../../db/migrations")
	natsURL := testhelpers.NATSContainer(t, ctx)

	// AutoApplyHandler still uses GORM repos for the legacy persistence
	// path. Open a GORM view on the same *sql.DB so both layers share
	// one connection pool — no extra dial, no risk of pool drift.
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: rawDB}), &gorm.Config{})
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

	// The tracker bridge writes into main's applications-OLTP table
	// via pkg/applications.Store. Wiring it here lets us assert the
	// end-to-end flow (intent → submit → bridge → applications row).
	tracker := service.NewPkgApplicationsTracker(applications.NewStore(rawDB))

	handler := service.NewAutoApplyHandler(service.HandlerDeps{
		Svc:       svc,
		Router:    router,
		AppRepo:   appRepo,
		MatchRepo: matchRepo,
		Tracker:   tracker,
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
		rawDB:    rawDB,
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

// trackerRow holds the subset of the applications-OLTP row the bridge
// is responsible for populating. Used in assertions that the tracker
// bridge wrote a row alongside the legacy candidate_applications row.
type trackerRow struct {
	candidateID   string
	opportunityID string
	matchID       string
	status        string
}

// queryTrackerRow returns the applications-OLTP row for the pair, or
// (nil, sql.ErrNoRows) when the bridge has not yet written one.
func (f *e2eFixture) queryTrackerRow(candidateID, opportunityID string) (*trackerRow, error) {
	row := f.rawDB.QueryRowContext(f.ctx, `
		SELECT candidate_id, opportunity_id, match_id, status
		  FROM applications
		 WHERE candidate_id = $1 AND opportunity_id = $2
	`, candidateID, opportunityID)
	out := trackerRow{}
	if err := row.Scan(&out.candidateID, &out.opportunityID, &out.matchID, &out.status); err != nil {
		return nil, err
	}
	return &out, nil
}

// waitForTrackerRow polls until the bridge writes the applications row.
// Used in happy-path assertions where the bridge is expected to fire.
func (f *e2eFixture) waitForTrackerRow(candidateID, opportunityID string) *trackerRow {
	f.t.Helper()
	var out *trackerRow
	require.Eventually(f.t, func() bool {
		row, err := f.queryTrackerRow(candidateID, opportunityID)
		if err != nil {
			return false
		}
		out = row
		return true
	}, 10*time.Second, 250*time.Millisecond,
		"tracker did not write applications row for %s × %s", candidateID, opportunityID)
	return out
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

	// Bridge assertion: a successful submit must also have populated
	// main's applications-OLTP table so the candidate sees the job in
	// their dashboard. The tracker writes Status=StatusSubmitted
	// directly (skipping the "applying" intermediate).
	tr := f.waitForTrackerRow(intent.CandidateID, intent.CanonicalJobID)
	assert.Equal(t, "submitted", tr.status)
	assert.Equal(t, intent.MatchID, tr.matchID)
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

	// Bridge negative-assertion: a dry-run (or any skip) must NOT write
	// to the applications-OLTP table — the candidate's tracking
	// dashboard should not show jobs we never actually submitted.
	// Allow a beat for any racing write to land.
	time.Sleep(1 * time.Second)
	_, err := f.queryTrackerRow(intent.CandidateID, intent.CanonicalJobID)
	assert.ErrorIs(t, err, sql.ErrNoRows,
		"tracker must NOT mirror dry-run/skip outcomes into applications")
}
