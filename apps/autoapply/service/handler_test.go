package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// --- fakes ---

type fakeAppRepo struct {
	mu          sync.Mutex
	existing    map[string]bool // "candidateID:canonicalJobID"
	createErr   error
	created     []*domain.CandidateApplication
	updates     []updateCall
	updateErr   error
}

type updateCall struct {
	id, status, method, externalRef string
}

func (f *fakeAppRepo) ExistsForCandidate(_ context.Context, cid, jid string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.existing[cid+":"+jid], nil
}

func (f *fakeAppRepo) Create(_ context.Context, app *domain.CandidateApplication) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	if app.ID == "" {
		app.ID = "app_test_" + string(rune('a'+len(f.created)))
	}
	f.created = append(f.created, app)
	return nil
}

func (f *fakeAppRepo) UpdateStatus(_ context.Context, id, status, method, externalRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, updateCall{id, status, method, externalRef})
	return f.updateErr
}

type fakeMatchRepo struct {
	mu         sync.Mutex
	appliedIDs []string
}

func (f *fakeMatchRepo) MarkApplied(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appliedIDs = append(f.appliedIDs, id)
	return nil
}

type fakeRouter struct {
	result autoapply.SubmitResult
	err    error
	calls  int
	lastReq autoapply.SubmitRequest
}

func (r *fakeRouter) Submit(_ context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	r.calls++
	r.lastReq = req
	return r.result, r.err
}

type fakeCV struct {
	data []byte
	name string
	err  error
}

func (f *fakeCV) Fetch(_ context.Context, _ string) ([]byte, string, error) {
	return f.data, f.name, f.err
}

func payload(t *testing.T, intent eventsv1.AutoApplyIntentV1) []byte {
	t.Helper()
	env := eventsv1.NewEnvelope(eventsv1.SubjectAutoApplySubmit, intent)
	b, err := json.Marshal(env)
	require.NoError(t, err)
	return b
}

func newHandler(app ApplicationRepo, m MatchRepo, r SubmitterRouter, cv CVFetcher, cfg Config) *AutoApplyHandler {
	return NewAutoApplyHandler(HandlerDeps{
		Router:    r,
		AppRepo:   app,
		MatchRepo: m,
		CV:        cv,
		Config:    cfg,
	})
}

func defaultCfg() Config {
	return Config{Enabled: true}
}

// --- tests ---

func TestHandle_HappyPath(t *testing.T) {
	app := &fakeAppRepo{existing: map[string]bool{}}
	m := &fakeMatchRepo{}
	r := &fakeRouter{result: autoapply.SubmitResult{Method: "ats_ui", ExternalRef: "ats-123"}}
	h := newHandler(app, m, r, &fakeCV{}, defaultCfg())

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID:    "cnd_1",
		MatchID:        "match_1",
		CanonicalJobID: "job_1",
		ApplyURL:       "https://boards.greenhouse.io/co/jobs/1",
	}
	require.NoError(t, h.Handle(context.Background(), nil, payload(t, intent)))

	require.Len(t, app.created, 1, "should insert pending row")
	assert.Equal(t, domain.AppStatusPending, app.created[0].Status)
	require.Len(t, app.updates, 1, "should update to terminal status")
	assert.Equal(t, domain.AppStatusSubmitted, app.updates[0].status)
	assert.Equal(t, "ats_ui", app.updates[0].method)
	assert.Equal(t, "ats-123", app.updates[0].externalRef)
	require.Len(t, m.appliedIDs, 1)
	assert.Equal(t, "match_1", m.appliedIDs[0])
	assert.Equal(t, 1, r.calls)
}

func TestHandle_SkippedDoesNotMarkMatchApplied(t *testing.T) {
	app := &fakeAppRepo{existing: map[string]bool{}}
	m := &fakeMatchRepo{}
	r := &fakeRouter{result: autoapply.SubmitResult{Method: "skipped", SkipReason: "captcha"}}
	h := newHandler(app, m, r, &fakeCV{}, defaultCfg())

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID:    "cnd_2",
		MatchID:        "match_2",
		CanonicalJobID: "job_2",
		ApplyURL:       "https://x/y",
	}
	require.NoError(t, h.Handle(context.Background(), nil, payload(t, intent)))

	require.Len(t, app.updates, 1)
	assert.Equal(t, domain.AppStatusSkipped, app.updates[0].status)
	assert.Empty(t, m.appliedIDs)
}

func TestHandle_AlreadyAppliedShortCircuits(t *testing.T) {
	app := &fakeAppRepo{existing: map[string]bool{"cnd_3:job_3": true}}
	r := &fakeRouter{result: autoapply.SubmitResult{Method: "ats_ui"}}
	h := newHandler(app, &fakeMatchRepo{}, r, &fakeCV{}, defaultCfg())

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID: "cnd_3", CanonicalJobID: "job_3", MatchID: "m3",
		ApplyURL: "https://x",
	}
	require.NoError(t, h.Handle(context.Background(), nil, payload(t, intent)))

	assert.Empty(t, app.created, "no pending row when already applied")
	assert.Empty(t, app.updates)
	assert.Equal(t, 0, r.calls, "submitter must not run")
}

func TestHandle_TransientSubmitErrorReturnsError(t *testing.T) {
	app := &fakeAppRepo{existing: map[string]bool{}}
	r := &fakeRouter{err: errors.New("browser: connect refused")}
	h := newHandler(app, &fakeMatchRepo{}, r, &fakeCV{}, defaultCfg())

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID: "cnd_4", CanonicalJobID: "job_4",
		ApplyURL: "https://x",
	}
	err := h.Handle(context.Background(), nil, payload(t, intent))
	require.Error(t, err, "transient errors must propagate so the queue redelivers")

	require.Len(t, app.created, 1, "pending row inserted")
	require.Len(t, app.updates, 1, "pending row marked failed before redelivery")
	assert.Equal(t, domain.AppStatusFailed, app.updates[0].status)
}

func TestHandle_UniqueViolationOnPendingIsAcked(t *testing.T) {
	app := &fakeAppRepo{
		existing: map[string]bool{},
		// Simulate the partial-unique-index race: a concurrent worker
		// already inserted the pending row, so our INSERT trips 23505.
		createErr: &pgconn.PgError{Code: "23505"},
	}
	r := &fakeRouter{}
	h := newHandler(app, &fakeMatchRepo{}, r, &fakeCV{}, defaultCfg())

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID: "cnd_5", CanonicalJobID: "job_5",
		ApplyURL: "https://x",
	}
	require.NoError(t, h.Handle(context.Background(), nil, payload(t, intent)),
		"race-dup must be acked, not redelivered")
	assert.Equal(t, 0, r.calls, "submitter must not run when another worker holds the slot")
}

func TestHandle_ExistsCheckErrorIsTransient(t *testing.T) {
	// We can't easily inject into ExistsForCandidate via fakeAppRepo's
	// current shape; substitute a custom impl.
	app := &existsErrRepo{err: errors.New("db down")}
	r := &fakeRouter{}
	h := newHandler(app, &fakeMatchRepo{}, r, &fakeCV{}, defaultCfg())

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID: "cnd_6", CanonicalJobID: "job_6",
		ApplyURL: "https://x",
	}
	err := h.Handle(context.Background(), nil, payload(t, intent))
	require.Error(t, err)
	assert.Equal(t, 0, r.calls)
}

type existsErrRepo struct{ err error }

func (e *existsErrRepo) ExistsForCandidate(_ context.Context, _, _ string) (bool, error) {
	return false, e.err
}
func (e *existsErrRepo) Create(_ context.Context, _ *domain.CandidateApplication) error {
	return nil
}
func (e *existsErrRepo) UpdateStatus(_ context.Context, _, _, _, _ string) error { return nil }

func TestHandle_DisabledByConfigAcks(t *testing.T) {
	app := &fakeAppRepo{existing: map[string]bool{}}
	r := &fakeRouter{}
	h := newHandler(app, &fakeMatchRepo{}, r, &fakeCV{}, Config{Enabled: false})

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID: "cnd_7", CanonicalJobID: "job_7", ApplyURL: "https://x",
	}
	require.NoError(t, h.Handle(context.Background(), nil, payload(t, intent)))
	assert.Empty(t, app.created)
	assert.Equal(t, 0, r.calls)
}

func TestHandle_ScoreBackstopAcks(t *testing.T) {
	app := &fakeAppRepo{existing: map[string]bool{}}
	r := &fakeRouter{}
	h := newHandler(app, &fakeMatchRepo{}, r, &fakeCV{}, Config{Enabled: true, ScoreMinBackstop: 0.9})

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID: "cnd_8", CanonicalJobID: "job_8", ApplyURL: "https://x",
		Score: 0.7,
	}
	require.NoError(t, h.Handle(context.Background(), nil, payload(t, intent)))
	assert.Empty(t, app.created)
}

func TestHandle_UnsafeCVURLFails(t *testing.T) {
	app := &fakeAppRepo{existing: map[string]bool{}}
	r := &fakeRouter{result: autoapply.SubmitResult{Method: "ats_ui"}}
	cv := &fakeCV{err: ErrCVUnsafeURL}
	h := newHandler(app, &fakeMatchRepo{}, r, cv, defaultCfg())

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID: "cnd_9", CanonicalJobID: "job_9", ApplyURL: "https://x",
		CVUrl: "http://169.254.169.254/latest/meta-data/",
	}
	require.NoError(t, h.Handle(context.Background(), nil, payload(t, intent)))

	require.Len(t, app.updates, 1)
	assert.Equal(t, domain.AppStatusFailed, app.updates[0].status)
	assert.Equal(t, 0, r.calls, "must not submit when CV URL is unsafe")
}

func TestHandle_CVDownloadSoftFailureProceedsWithoutCV(t *testing.T) {
	app := &fakeAppRepo{existing: map[string]bool{}}
	r := &fakeRouter{result: autoapply.SubmitResult{Method: "ats_ui"}}
	cv := &fakeCV{err: errors.New("network blip")}
	h := newHandler(app, &fakeMatchRepo{}, r, cv, defaultCfg())

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID: "cnd_10", CanonicalJobID: "job_10", ApplyURL: "https://x",
		CVUrl: "https://cv.example.com/foo.pdf",
	}
	require.NoError(t, h.Handle(context.Background(), nil, payload(t, intent)))

	assert.Equal(t, 1, r.calls)
	assert.Empty(t, r.lastReq.CVBytes, "missing CV must not block submit")
}

func TestHandle_EmptyPayloadReturnsError(t *testing.T) {
	h := newHandler(&fakeAppRepo{existing: map[string]bool{}}, &fakeMatchRepo{}, &fakeRouter{}, &fakeCV{}, defaultCfg())
	require.Error(t, h.Handle(context.Background(), nil, nil))
}

func TestHandle_BadJSONIsAcked(t *testing.T) {
	h := newHandler(&fakeAppRepo{existing: map[string]bool{}}, &fakeMatchRepo{}, &fakeRouter{}, &fakeCV{}, defaultCfg())
	require.NoError(t, h.Handle(context.Background(), nil, []byte("not json")))
}
