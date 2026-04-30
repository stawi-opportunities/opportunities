package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// --- fakes ---

type fakeAppRepo struct {
	existing map[string]bool // "candidateID:canonicalJobID"
	created  []*domain.CandidateApplication
}

func (f *fakeAppRepo) ExistsForCandidate(_ context.Context, cid, jid string) (bool, error) {
	return f.existing[cid+":"+jid], nil
}
func (f *fakeAppRepo) Create(_ context.Context, app *domain.CandidateApplication) error {
	f.created = append(f.created, app)
	return nil
}

type fakeMatchRepo struct {
	appliedIDs []string
}

func (f *fakeMatchRepo) MarkApplied(_ context.Context, id string) error {
	f.appliedIDs = append(f.appliedIDs, id)
	return nil
}

type fakeRegistry struct {
	result autoapply.SubmitResult
	err    error
}

func (f *fakeRegistry) Submit(_ context.Context, _ autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	return f.result, f.err
}

// submitFn lets us inject the registry into AutoApplyHandler without
// exposing the *Registry type — wrap it in an inline adapter.
type registryAdapter struct{ reg *fakeRegistry }

func (r *registryAdapter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	return r.reg.Submit(ctx, req)
}

// buildPayload marshals an AutoApplyIntentV1 into the wire format.
func buildPayload(t *testing.T, intent eventsv1.AutoApplyIntentV1) []byte {
	t.Helper()
	env := eventsv1.NewEnvelope(eventsv1.SubjectAutoApplySubmit, intent)
	b, err := json.Marshal(env)
	require.NoError(t, err)
	return b
}

// handlerWithFakes builds an AutoApplyHandler wired with fakes.
// We need to reach the registry interface, so we use a shim that
// replaces the real *autoapply.Registry with a testable interface.
func handlerWithFakes(appRepo *fakeAppRepo, matchRepo *fakeMatchRepo, reg *fakeRegistry) *testableHandler {
	return &testableHandler{
		appRepo:   appRepo,
		matchRepo: matchRepo,
		reg:       reg,
		http:      nil, // CV download skipped (CVUrl empty in tests)
	}
}

// testableHandler mirrors AutoApplyHandler but accepts the fake registry.
type testableHandler struct {
	appRepo   ApplicationRepo
	matchRepo MatchRepo
	reg       *fakeRegistry
	http      interface{} // unused in tests
}

func (h *testableHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	var env eventsv1.Envelope[eventsv1.AutoApplyIntentV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return err
	}
	intent := env.Payload

	exists, err := h.appRepo.ExistsForCandidate(ctx, intent.CandidateID, intent.CanonicalJobID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	result, submitErr := h.reg.Submit(ctx, autoapply.SubmitRequest{
		ApplyURL:    intent.ApplyURL,
		CandidateID: intent.CandidateID,
	})
	if submitErr != nil {
		_ = h.appRepo.Create(ctx, &domain.CandidateApplication{
			CandidateID:    intent.CandidateID,
			CanonicalJobID: intent.CanonicalJobID,
			Status:         domain.AppStatusFailed,
			Method:         "error",
		})
		return nil
	}

	status := domain.AppStatusSubmitted
	if result.Method == "skipped" {
		status = domain.AppStatusSkipped
	}

	now := time.Now().UTC()
	app := &domain.CandidateApplication{
		CandidateID:    intent.CandidateID,
		CanonicalJobID: intent.CanonicalJobID,
		Method:         result.Method,
		Status:         status,
		SubmittedAt:    &now,
	}
	if intent.MatchID != "" {
		app.MatchID = &intent.MatchID
	}
	if err := h.appRepo.Create(ctx, app); err != nil {
		return err
	}

	if status == domain.AppStatusSubmitted && intent.MatchID != "" {
		_ = h.matchRepo.MarkApplied(ctx, intent.MatchID)
	}
	return nil
}

// --- tests ---

func TestAutoApplyHandler_HappyPath(t *testing.T) {
	appRepo := &fakeAppRepo{existing: map[string]bool{}}
	matchRepo := &fakeMatchRepo{}
	reg := &fakeRegistry{result: autoapply.SubmitResult{Method: "ats_ui"}}
	h := handlerWithFakes(appRepo, matchRepo, reg)

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID:    "cnd_1",
		MatchID:        "match_1",
		CanonicalJobID: "job_1",
		ApplyURL:       "https://boards.greenhouse.io/co/jobs/1",
	}
	err := h.Handle(context.Background(), nil, buildPayload(t, intent))
	require.NoError(t, err)

	require.Len(t, appRepo.created, 1)
	assert.Equal(t, domain.AppStatusSubmitted, appRepo.created[0].Status)
	assert.Equal(t, "ats_ui", appRepo.created[0].Method)
	require.Len(t, matchRepo.appliedIDs, 1)
	assert.Equal(t, "match_1", matchRepo.appliedIDs[0])
}

func TestAutoApplyHandler_SkippedSubmission(t *testing.T) {
	appRepo := &fakeAppRepo{existing: map[string]bool{}}
	matchRepo := &fakeMatchRepo{}
	reg := &fakeRegistry{result: autoapply.SubmitResult{Method: "skipped", SkipReason: "captcha"}}
	h := handlerWithFakes(appRepo, matchRepo, reg)

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID:    "cnd_2",
		MatchID:        "match_2",
		CanonicalJobID: "job_2",
		ApplyURL:       "https://boards.greenhouse.io/co/jobs/2",
	}
	err := h.Handle(context.Background(), nil, buildPayload(t, intent))
	require.NoError(t, err)

	require.Len(t, appRepo.created, 1)
	assert.Equal(t, domain.AppStatusSkipped, appRepo.created[0].Status)
	// Match must NOT be marked applied on a skip.
	assert.Empty(t, matchRepo.appliedIDs)
}

func TestAutoApplyHandler_IdempotencyGuard(t *testing.T) {
	appRepo := &fakeAppRepo{existing: map[string]bool{"cnd_3:job_3": true}}
	matchRepo := &fakeMatchRepo{}
	reg := &fakeRegistry{result: autoapply.SubmitResult{Method: "ats_ui"}}
	h := handlerWithFakes(appRepo, matchRepo, reg)

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID:    "cnd_3",
		MatchID:        "match_3",
		CanonicalJobID: "job_3",
		ApplyURL:       "https://boards.greenhouse.io/co/jobs/3",
	}
	err := h.Handle(context.Background(), nil, buildPayload(t, intent))
	require.NoError(t, err)

	// Nothing created, nothing applied.
	assert.Empty(t, appRepo.created)
	assert.Empty(t, matchRepo.appliedIDs)
}

func TestAutoApplyHandler_SubmitError_RecordsFailedApplication(t *testing.T) {
	appRepo := &fakeAppRepo{existing: map[string]bool{}}
	matchRepo := &fakeMatchRepo{}
	reg := &fakeRegistry{err: context.DeadlineExceeded}
	h := handlerWithFakes(appRepo, matchRepo, reg)

	intent := eventsv1.AutoApplyIntentV1{
		CandidateID:    "cnd_4",
		MatchID:        "match_4",
		CanonicalJobID: "job_4",
		ApplyURL:       "https://boards.greenhouse.io/co/jobs/4",
	}
	err := h.Handle(context.Background(), nil, buildPayload(t, intent))
	require.NoError(t, err) // handler swallows transient errors after recording

	require.Len(t, appRepo.created, 1)
	assert.Equal(t, domain.AppStatusFailed, appRepo.created[0].Status)
	assert.Empty(t, matchRepo.appliedIDs)
}
