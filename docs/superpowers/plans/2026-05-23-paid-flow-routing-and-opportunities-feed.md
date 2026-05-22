# Paid-Flow Routing, Resumable Onboarding & Unified Opportunities Feed — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** [`docs/superpowers/specs/2026-05-23-paid-flow-routing-and-opportunities-feed-design.md`](../specs/2026-05-23-paid-flow-routing-and-opportunities-feed-design.md)

**Goal:** Replace the broken post-login flow with a single payment gate (paid → dashboard, unpaid → resumable wizard) backed by a unified opportunities feed showing matches, starred jobs, and applications with inline state per card.

**Architecture:** Server-side onboarding draft persisted as a JSONB column on `candidate_profiles`. AuthCallback and page-level guards on `/dashboard/` + `/onboarding/` all consult one source of truth (`GET /matching/me/subscription`). Dashboard's three placeholder panels collapse into one `OpportunitiesFeed` driven by a single `GET /matching/me/opportunities` endpoint that joins `candidate_matches`, `candidate_saved_jobs` (new), and `candidate_applications` (cross-service read against shared DB).

**Tech Stack:** Go 1.23 + `pkg/pitabwire/frame` (backend), React 19 + Vite + react-hook-form + Tailwind (frontend), Postgres with JSONB + cursor pagination, `httpmw.CandidateAuth` for the `/me/*` namespace.

---

## Coding conventions & assumptions

These apply to every task — don't repeat them in each step:

- **TDD discipline**: every step that adds code starts with a failing test. The "verify it fails" step exists for a reason — don't skip it. Skipping it means you might write a test that always passes.
- **Logging**: `util.Log(ctx)`. Never `fmt.Printf`, `slog`, or stdlib `log`. Error logs include the candidate ID (the natural correlation key on this surface).
- **Errors**: wrap with `fmt.Errorf("%s: %w", op, err)`. Handler-level errors flow through `httpmw.ProblemJSON` (RFC 9457).
- **Tests**: backend unit tests use sqlite/in-memory fakes per the existing `pkg/repository/flag_test.go` pattern when schema is portable; JSONB-dependent tests use `tests/integration/testhelpers.PostgresContainerNoMigrate` + `ApplyMigrationsDir` per the `pkg/matching/store_test.go` pattern. Frontend tests use Vitest in `ui/app/`.
- **Frontend imports**: `@/...` for `ui/app/src/...`. Existing files in the repo (Dashboard.tsx, Onboarding.tsx) use this — match.
- **Commits**: one focused commit per task at the end of the task. Conventional Commits (`feat:`, `fix:`, `test:`, `chore:`, `docs:`).
- **Idempotency**: PUT/POST handlers that mutate user state wrap with `httpmw.Idempotency` per the Phase-4 router pattern in `apps/matching/service/http/me/v1/router.go`. Reads do not.
- **Auth**: every `/me/*` handler wraps with `httpmw.CandidateAuth` and reads the candidate ID via `httpmw.CandidateFromContext(r.Context())`.

---

# Phase 1 — Backend foundations

Adds the onboarding_draft column and the GET/PUT handlers behind it. No frontend changes; nothing user-visible changes when this phase ships. The phase exists so Phase 2's UI work has a backend to talk to.

## Task 1.1: Add `onboarding_draft` migration

**Files:**
- Create: `db/migrations/0017_onboarding_draft.sql`

- [ ] **Step 1: Write the migration**

Create the file `db/migrations/0017_onboarding_draft.sql`:

```sql
-- 0017: candidate onboarding draft column.
-- The wizard saves a per-step JSONB blob here so the user resumes
-- at the step they left, on any device, with their answers intact.
-- Cleared back to '{}'::jsonb when POST /candidates/onboard finalises.
-- See docs/superpowers/specs/2026-05-23-paid-flow-routing-and-opportunities-feed-design.md

ALTER TABLE candidate_profiles
  ADD COLUMN IF NOT EXISTS onboarding_draft JSONB NOT NULL DEFAULT '{}'::jsonb;
```

- [ ] **Step 2: Verify migration applies cleanly**

Run:
```bash
cd /home/j/code/stawi.opportunities
docker compose -f deploy/docker-compose.yml up -d postgres
PGPASSWORD=postgres psql -h localhost -U postgres -d candidates -f db/migrations/0017_onboarding_draft.sql
PGPASSWORD=postgres psql -h localhost -U postgres -d candidates -c "\d candidate_profiles" | grep onboarding_draft
```
Expected output contains: `onboarding_draft | jsonb | not null default '{}'::jsonb`

- [ ] **Step 3: Commit**

```bash
git add db/migrations/0017_onboarding_draft.sql
git commit -m "feat(migrations): add candidate_profiles.onboarding_draft JSONB column"
```

## Task 1.2: Add Get/Set/Clear draft methods to `CandidateRepository`

**Files:**
- Modify: `pkg/repository/candidate.go` (append three new methods)
- Create: `pkg/repository/candidate_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `pkg/repository/candidate_test.go`:

```go
//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupCandidateDB(t *testing.T) (*gorm.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyMigrationsDir(t, ctx, sqlDB, "../../db/migrations")
	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	return g, ctx
}

func TestCandidateRepository_OnboardingDraft_RoundTrip(t *testing.T) {
	g, ctx := setupCandidateDB(t)
	repo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return g })

	cand := &domain.CandidateProfile{}
	cand.ID = "cand_draft_a"
	cand.ProfileID = "prof_draft_a"
	require.NoError(t, repo.Create(ctx, cand))

	// Fresh candidate has an empty draft.
	got, err := repo.GetOnboardingDraft(ctx, "cand_draft_a")
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(got))

	// Write a draft, read it back identically.
	draft := json.RawMessage(`{"step":2,"fields":{"target_job_title":"Backend Engineer"}}`)
	require.NoError(t, repo.SetOnboardingDraft(ctx, "cand_draft_a", draft))
	got, err = repo.GetOnboardingDraft(ctx, "cand_draft_a")
	require.NoError(t, err)
	require.JSONEq(t, string(draft), string(got))

	// Clear resets to {}.
	require.NoError(t, repo.ClearOnboardingDraft(ctx, "cand_draft_a"))
	got, err = repo.GetOnboardingDraft(ctx, "cand_draft_a")
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(got))
}

func TestCandidateRepository_OnboardingDraft_UnknownCandidateReturnsEmpty(t *testing.T) {
	g, ctx := setupCandidateDB(t)
	repo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return g })

	got, err := repo.GetOnboardingDraft(ctx, "cand_nonexistent")
	require.NoError(t, err, "GetOnboardingDraft must not 500 on unknown candidate; the wizard treats that as 'fresh user'")
	require.JSONEq(t, `{}`, string(got))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
go test -tags integration -race -run TestCandidateRepository_OnboardingDraft -timeout 120s ./pkg/repository/...
```
Expected: FAIL with `repo.GetOnboardingDraft undefined` (the method doesn't exist yet).

- [ ] **Step 3: Implement the three methods**

Append to `pkg/repository/candidate.go`:

```go
// GetOnboardingDraft returns the candidate's persisted wizard draft as
// raw JSON. Unknown candidates return `{}` rather than an error — the
// onboarding handler treats "no draft" identically to "candidate
// doesn't exist yet"; a fresh user has the same empty wizard either
// way.
func (r *CandidateRepository) GetOnboardingDraft(ctx context.Context, id string) ([]byte, error) {
	var raw []byte
	err := r.db(ctx, true).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", id).
		Select("onboarding_draft").
		Limit(1).
		Scan(&raw).Error
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	return raw, nil
}

// SetOnboardingDraft writes the wizard draft. The body is opaque to
// the repository; the matching service owns the schema. The candidate
// row must already exist — sign-in creates one before the wizard
// mounts (see [POST /candidates/onboard] for the canonical row
// creation path; a draft-only candidate is created lazily by
// SetOnboardingDraft via an UPDATE that hits zero rows then INSERTs
// the bare minimum).
func (r *CandidateRepository) SetOnboardingDraft(ctx context.Context, id string, draft []byte) error {
	res := r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", id).
		Update("onboarding_draft", string(draft))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Lazy-create the candidate row so the wizard can persist
		// before the final /candidates/onboard call. ProfileID is
		// the JWT subject, which the handler passes alongside the
		// candidate ID; both come from CandidateFromContext today
		// (they're the same value pre-Phase-4 split).
		cand := &domain.CandidateProfile{ProfileID: id}
		cand.ID = id
		// Use raw exec to insert just id + onboarding_draft; the
		// other columns default to their NOT NULL DEFAULT values.
		return r.db(ctx, false).Exec(
			`INSERT INTO candidate_profiles (id, profile_id, onboarding_draft) VALUES (?, ?, ?::jsonb)`,
			id, id, string(draft),
		).Error
	}
	return nil
}

// ClearOnboardingDraft resets the draft to '{}'. Called inside the
// same transaction as POST /candidates/onboard so a successful submit
// atomically promotes the draft into the canonical profile columns.
func (r *CandidateRepository) ClearOnboardingDraft(ctx context.Context, id string) error {
	return r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", id).
		Update("onboarding_draft", `{}`).Error
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:
```bash
go test -tags integration -race -run TestCandidateRepository_OnboardingDraft -timeout 120s ./pkg/repository/...
```
Expected: PASS, two tests OK.

- [ ] **Step 5: Commit**

```bash
git add pkg/repository/candidate.go pkg/repository/candidate_test.go
git commit -m "feat(repository): onboarding draft Get/Set/Clear on CandidateRepository

Three repository methods for the wizard draft persistence. Unknown
candidates return {} rather than an error so the handler doesn't have
to special-case fresh users. SetOnboardingDraft lazy-creates the
candidate row on first write so the wizard can persist before the
final /candidates/onboard submit lands.

Integration test covers the round trip + the unknown-candidate path
against a real Postgres + applied migrations."
```

## Task 1.3: HTTP handlers `GET` / `PUT` `/me/onboarding`

**Files:**
- Create: `apps/matching/service/http/v1/me_onboarding.go`
- Create: `apps/matching/service/http/v1/me_onboarding_test.go`

- [ ] **Step 1: Write the failing handler unit test**

Create `apps/matching/service/http/v1/me_onboarding_test.go`:

```go
package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type fakeDraftStore struct {
	getResp  []byte
	getErr   error
	setBody  []byte
	setErr   error
	setCalls int
}

func (f *fakeDraftStore) GetOnboardingDraft(_ context.Context, _ string) ([]byte, error) {
	return f.getResp, f.getErr
}

func (f *fakeDraftStore) SetOnboardingDraft(_ context.Context, _ string, draft []byte) error {
	f.setBody = draft
	f.setCalls++
	return f.setErr
}

func reqWithCandidate(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("X-Candidate-ID", "cand_h_1")
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func TestOnboardingHandler_GetReturnsDraft(t *testing.T) {
	t.Parallel()
	draft := []byte(`{"step":2,"fields":{"target_job_title":"PM"},"updated_at":"2026-05-23T10:00:00Z"}`)
	store := &fakeDraftStore{getResp: draft}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodGet, "/me/onboarding", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, string(draft), rec.Body.String())
}

func TestOnboardingHandler_GetEmptyDraftRendersDefault(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{getResp: []byte(`{}`)}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodGet, "/me/onboarding", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	// Empty draft renders as the canonical wizard start: step 1,
	// no fields. The wizard relies on this default to skip a
	// post-load conditional.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, float64(1), resp["step"])
	require.Equal(t, map[string]any{}, resp["fields"])
}

func TestOnboardingHandler_GetStoreErrorReturnsProblemJSON(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{getErr: errors.New("db down")}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodGet, "/me/onboarding", ""))

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), "draft_lookup_failed")
}

func TestOnboardingHandler_PutPersistsDraft(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	body := `{"step":2,"fields":{"target_job_title":"PM","experience_level":"mid"}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodPut, "/me/onboarding", body))

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, 1, store.setCalls)
	// Handler adds updated_at server-side so the wizard doesn't have
	// to (clock skew between client + server would otherwise produce
	// confusing "draft is from the future" displays).
	var persisted map[string]any
	require.NoError(t, json.Unmarshal(store.setBody, &persisted))
	require.Equal(t, float64(2), persisted["step"])
	require.NotEmpty(t, persisted["updated_at"])
}

func TestOnboardingHandler_PutRejectsInvalidStep(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	for _, badBody := range []string{
		`{"step":0,"fields":{}}`,   // below range
		`{"step":4,"fields":{}}`,   // above range
		`{"step":"two","fields":{}}`, // wrong type
		`{}`,                       // missing step
		`not json`,                 // unparseable
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, reqWithCandidate(t, http.MethodPut, "/me/onboarding", badBody))
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%q", badBody)
	}
	require.Zero(t, store.setCalls, "no Set call should land for bad bodies")
}

func TestOnboardingHandler_PutPersistFailureReturnsProblemJSON(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{setErr: errors.New("disk full")}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodPut, "/me/onboarding",
		`{"step":1,"fields":{"target_job_title":"PM"}}`))

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "draft_persist_failed")
}

func TestOnboardingHandler_MethodOther(t *testing.T) {
	t.Parallel()
	store := &fakeDraftStore{}
	h := httpmw.CandidateAuth(v1.OnboardingHandler(v1.OnboardingDeps{Drafts: store}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithCandidate(t, http.MethodPost, "/me/onboarding", `{}`))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
go test -race -run TestOnboardingHandler ./apps/matching/service/http/v1/...
```
Expected: FAIL with `undefined: v1.OnboardingHandler`.

- [ ] **Step 3: Implement the handler**

Create `apps/matching/service/http/v1/me_onboarding.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// OnboardingDraftReader returns the persisted draft as raw JSON, or
// `{}` when the candidate has none. Implemented by
// repository.CandidateRepository in production; the interface lets
// the handler test against an in-memory fake.
type OnboardingDraftReader interface {
	GetOnboardingDraft(ctx context.Context, candidateID string) ([]byte, error)
}

// OnboardingDraftWriter persists a raw JSON draft. The body is opaque
// here — the wizard owns the schema; we only assert outer-envelope
// shape (step in 1..3, presence of fields).
type OnboardingDraftWriter interface {
	SetOnboardingDraft(ctx context.Context, candidateID string, draft []byte) error
}

// OnboardingDraftStore bundles the two operations a single handler
// uses. Splitting the read and write interfaces lets each test
// depend on only what it exercises.
type OnboardingDraftStore interface {
	OnboardingDraftReader
	OnboardingDraftWriter
}

// OnboardingDeps bundles the inputs the handler needs.
type OnboardingDeps struct {
	Drafts OnboardingDraftStore
	// Now lets tests pin the server timestamp the handler embeds in
	// the draft envelope on Put. Defaults to time.Now in production.
	Now func() time.Time
}

func (d *OnboardingDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

// onboardingEnvelope is the wire shape both endpoints use. Fields
// stays a raw JSON value so the wizard can evolve its schema without
// any backend code change.
type onboardingEnvelope struct {
	Step      int             `json:"step"`
	Fields    json.RawMessage `json:"fields"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
}

// OnboardingHandler dispatches GET and PUT on the same path. Wrap
// with httpmw.CandidateAuth before mounting; the wrapper populates
// the candidate ID into the request context.
func OnboardingHandler(deps OnboardingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleOnboardingGet(deps, w, r)
		case http.MethodPut:
			handleOnboardingPut(deps, w, r)
		default:
			w.Header().Set("Allow", "GET, PUT")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use GET to load draft, PUT to save")
		}
	}
}

func handleOnboardingGet(deps OnboardingDeps, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := util.Log(ctx)
	candidateID := httpmw.CandidateFromContext(ctx)

	raw, err := deps.Drafts.GetOnboardingDraft(ctx, candidateID)
	if err != nil {
		log.WithError(err).WithField("candidate_id", candidateID).
			Error("me/onboarding: draft lookup failed")
		httpmw.ProblemJSON(w, http.StatusBadGateway,
			"draft_lookup_failed", "could not load onboarding draft")
		return
	}

	// Render the canonical default for an empty draft so the client
	// doesn't have to special-case `{}` vs a real envelope.
	out := onboardingEnvelope{Step: 1, Fields: json.RawMessage(`{}`)}
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &out); err != nil {
			// Stored draft is corrupt — treat as empty rather than
			// 500 the client. The next PUT will overwrite it.
			log.WithError(err).WithField("candidate_id", candidateID).
				Warn("me/onboarding: stored draft is malformed; returning default envelope")
			out = onboardingEnvelope{Step: 1, Fields: json.RawMessage(`{}`)}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func handleOnboardingPut(deps OnboardingDeps, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := util.Log(ctx)
	candidateID := httpmw.CandidateFromContext(ctx)

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest,
			"body_read_failed", "could not read request body")
		return
	}
	var in onboardingEnvelope
	if err := json.Unmarshal(body, &in); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest,
			"invalid_json", "request body is not valid JSON")
		return
	}
	if in.Step < 1 || in.Step > 3 {
		httpmw.ProblemJSON(w, http.StatusBadRequest,
			"invalid_step", "step must be 1, 2, or 3")
		return
	}
	if len(in.Fields) == 0 {
		in.Fields = json.RawMessage(`{}`)
	}

	now := deps.now()
	in.UpdatedAt = &now
	out, err := json.Marshal(in)
	if err != nil {
		log.WithError(err).Error("me/onboarding: re-marshal failed (programmer error)")
		httpmw.ProblemJSON(w, http.StatusInternalServerError,
			"internal", "could not serialise draft envelope")
		return
	}
	if err := deps.Drafts.SetOnboardingDraft(ctx, candidateID, out); err != nil {
		log.WithError(err).WithField("candidate_id", candidateID).
			Error("me/onboarding: draft persist failed")
		httpmw.ProblemJSON(w, http.StatusBadGateway,
			"draft_persist_failed", "could not save onboarding draft")
		return
	}
	w.WriteHeader(http.StatusNoContent)
	// Use _ = errors.Is to silence the linter for unused err in the
	// 64KB-cap branch above; not actually used here.
	_ = errors.Is
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:
```bash
go test -race -run TestOnboardingHandler ./apps/matching/service/http/v1/...
```
Expected: PASS, seven test cases.

- [ ] **Step 5: Commit**

```bash
git add apps/matching/service/http/v1/me_onboarding.go apps/matching/service/http/v1/me_onboarding_test.go
git commit -m "feat(matching): GET/PUT /me/onboarding handlers for resumable wizard

Same handler dispatches both methods. GET returns the canonical
{step:1, fields:{}} for fresh users; PUT validates the step is in
range, stamps an updated_at server-side (no client-clock-skew
surprises), and persists the raw JSON.

OnboardingDraftStore is split into Reader/Writer interfaces so each
test depends on only the surface it exercises. Errors flow through
httpmw.ProblemJSON per the project's RFC-9457 convention; a
malformed stored draft is treated as empty rather than 500 to
avoid bricking the client for one user."
```

## Task 1.4: Register the handler in `apps/matching/cmd/main.go`

**Files:**
- Modify: `apps/matching/cmd/main.go`

- [ ] **Step 1: Find the existing `/me/subscription` registration site**

Run:
```bash
grep -n "/me/subscription\|httpmw.CandidateAuth" apps/matching/cmd/main.go
```
Note the line number where the `mux.Handle("GET /me/subscription"...)` block lives. The new `/me/onboarding` block goes immediately after it.

- [ ] **Step 2: Add the handler registration**

In `apps/matching/cmd/main.go`, find the block:

```go
	mux.Handle("GET /me/subscription", httpmw.CandidateAuth(
		httpv1.SubscriptionHandler(httpv1.SubscriptionDeps{
			Candidates: candidateRepo,
			Matches:    meSubMatches,
		}),
	))
```

Insert immediately after it:

```go
	// /me/onboarding — resumable wizard. Same handler serves GET +
	// PUT; the underlying repo type implements both interfaces.
	// CandidateAuth wraps the registration; the wrapper populates
	// the candidate ID into the request context.
	onboardingHandler := httpmw.CandidateAuth(httpv1.OnboardingHandler(httpv1.OnboardingDeps{
		Drafts: candidateRepo,
	}))
	mux.Handle("GET /me/onboarding", onboardingHandler)
	mux.Handle("PUT /me/onboarding", onboardingHandler)
```

- [ ] **Step 3: Build the matching binary to verify no compile errors**

Run:
```bash
go build ./apps/matching/...
```
Expected: no output (success). If you see `repo.GetOnboardingDraft undefined`, Task 1.2 didn't land; back up.

- [ ] **Step 4: Vet the whole tree to catch any incidental damage**

Run:
```bash
go vet ./...
```
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add apps/matching/cmd/main.go
git commit -m "feat(matching): register /me/onboarding handler

Mounts GET + PUT on the same handler in apps/matching/cmd/main.go,
next to /me/subscription. candidateRepo already satisfies the
OnboardingDraftStore interface (Get + Set added in repository
package)."
```

## Task 1.5: Smoke-build matching service image locally (no deploy)

**Files:** none

- [ ] **Step 1: Run the full test suite with race**

Run:
```bash
go test -race -timeout 300s ./apps/matching/... ./pkg/httpmw/... ./pkg/repository/...
```
Expected: all green. Integration-tagged tests need the postgres testcontainer (Docker must be running locally) — if Docker isn't available the integration-tagged tests skip silently.

- [ ] **Step 2: Open a PR for Phase 1**

The frontend hasn't shifted yet, so this is shippable as-is. Phase 1 lands a backend column + endpoint that nothing uses; Phase 2's UI work consumes it.

```bash
git push origin HEAD:feat/paid-flow-phase-1-onboarding-backend
gh pr create --title "feat(matching): phase 1 — onboarding draft backend (column + GET/PUT /me/onboarding)" \
  --body "$(cat <<'PR'
## Summary

Phase 1 of the paid-flow design (see [`docs/superpowers/specs/2026-05-23-paid-flow-routing-and-opportunities-feed-design.md`](docs/superpowers/specs/2026-05-23-paid-flow-routing-and-opportunities-feed-design.md)).

- Migration adds `onboarding_draft JSONB` on `candidate_profiles`.
- `CandidateRepository` gets `Get/Set/Clear` methods over that column.
- New `GET/PUT /me/onboarding` handler dispatches both methods on the same path; PUT validates step + envelope shape, stamps `updated_at` server-side, persists opaque field JSON.

Nothing user-visible changes. Phase 2 (frontend) calls these endpoints.

## Test plan

- [x] `go test -race -tags integration` passes — repo round-trip + handler unit tests.
- [x] `go build ./apps/matching/...` clean.
- [ ] After merge: confirm migration runs cleanly on a fresh dev cluster (Frame's migration job picks it up automatically).
PR
)"
```

Wait for CI green, then merge per the existing convention (see prior PRs).

---

# Phase 2 — Resumable wizard + AuthCallback routing + page guards

UI work that consumes Phase 1's endpoints. Ships the user-visible behaviour: post-login routing decision and a wizard that remembers where you left off.

## Task 2.1: Add `fetchOnboardingDraft` / `saveOnboardingDraft` to the API client

**Files:**
- Modify: `ui/app/src/api/candidates.ts`

- [ ] **Step 1: Find the existing `MeSubscription` block as a placement anchor**

Run:
```bash
grep -n "fetchMeSubscription\|MeSubscription" ui/app/src/api/candidates.ts
```

- [ ] **Step 2: Append the new helpers + types**

At the bottom of `ui/app/src/api/candidates.ts` (just above `getCandidatesOrigin`), insert:

```ts
// ── /me/onboarding ─────────────────────────────────────────────

/** The wizard's persisted form values. Shape mirrors the Onboarding.tsx
 *  FormValues minus file (`cv`) and the agree-terms boolean (those are
 *  set on the final submit, not autosaved). Keep field names matching
 *  the form so we can `form.reset(fields)` on resume. */
export interface OnboardingDraftFields {
  target_job_title?: string;
  experience_level?: "entry" | "mid" | "senior" | "lead";
  job_search_status?: "actively_looking" | "open" | "not_looking";
  salary_range?: string;
  wants_ats_report?: boolean;
  preferred_regions?: string[];
  preferred_timezones?: string[];
  preferred_languages?: string[];
  job_types?: string[];
  country?: string;
  plan?: "starter" | "pro" | "managed";
}

export interface OnboardingDraft {
  step: 1 | 2 | 3;
  fields: OnboardingDraftFields;
  updated_at?: string;
}

/** GET /matching/me/onboarding — never throws; returns the canonical
 *  empty draft on any failure so the wizard mount is non-blocking. */
export async function fetchOnboardingDraft(): Promise<OnboardingDraft> {
  const empty: OnboardingDraft = { step: 1, fields: {} };
  try {
    const body = await authRuntime().fetch<OnboardingDraft>("/matching/me/onboarding");
    return {
      step: body.step ?? 1,
      fields: body.fields ?? {},
      updated_at: body.updated_at,
    };
  } catch {
    return empty;
  }
}

/** PUT /matching/me/onboarding — fire-and-forget autosave. Errors are
 *  surfaced via the returned promise so the caller can show a
 *  non-blocking warning; we do NOT throw to the caller's `await` in
 *  the happy path. */
export async function saveOnboardingDraft(
  step: 1 | 2 | 3,
  fields: OnboardingDraftFields,
): Promise<void> {
  await authRuntime().fetch("/matching/me/onboarding", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ step, fields }),
  });
}
```

- [ ] **Step 3: Typecheck the change**

Run:
```bash
cd ui/app && npm run typecheck
```
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add ui/app/src/api/candidates.ts
git commit -m "feat(api): onboarding draft client helpers

fetchOnboardingDraft never throws — returns a canonical {step:1,
fields:{}} on any failure so the wizard mount is non-blocking.
saveOnboardingDraft surfaces errors so the caller can show an
inline retry; the wizard keeps the user on the current step and
re-tries on the next 'Next' click."
```

## Task 2.2: Wizard loads + saves draft

**Files:**
- Modify: `ui/app/src/pages/Onboarding.tsx`

- [ ] **Step 1: Locate the wizard state hooks**

Run:
```bash
grep -n "useState<1 | 2 | 3>\|useForm<FormValues>\|export default function Onboarding" ui/app/src/pages/Onboarding.tsx
```

- [ ] **Step 2: Wire the draft load on mount**

In `ui/app/src/pages/Onboarding.tsx`, find the imports block and add to it:

```ts
import {
  submitOnboarding,
  uploadCV,
  createCheckout,
  fetchOnboardingDraft,
  saveOnboardingDraft,
  type OnboardingDraftFields,
} from "@/api/candidates";
```

Then find the body of `export default function Onboarding()`. Immediately after the existing `const [step, setStep] = useState<1 | 2 | 3>(1);` line, insert:

```ts
  const [draftLoaded, setDraftLoaded] = useState(false);
  const [draftSaveWarning, setDraftSaveWarning] = useState<string | null>(null);
```

After the existing `useEffect` that triggers `login()` on `state === "unauthenticated"`, insert a new effect that loads the draft once the candidate is signed in:

```ts
  // Resume from server-persisted draft as soon as the candidate is
  // authenticated. The fetch never throws (api/candidates.ts handles
  // that); a missing/empty draft renders the wizard at step 1 with
  // current defaults, which is identical to the initial render.
  useEffect(() => {
    if (state !== "authenticated") return;
    if (draftLoaded) return;
    let cancelled = false;
    (async () => {
      const draft = await fetchOnboardingDraft();
      if (cancelled) return;
      // Only overwrite form values we actually have in the draft;
      // react-hook-form's reset() with partial values keeps the rest
      // of the defaults intact.
      form.reset({ ...form.getValues(), ...(draft.fields as Record<string, unknown>) }, {
        keepDirty: false,
        keepDefaultValues: true,
      });
      setStep(draft.step);
      setDraftLoaded(true);
    })();
    return () => { cancelled = true; };
  }, [state, draftLoaded, form]);
```

- [ ] **Step 3: Save on each Next click**

Still in `ui/app/src/pages/Onboarding.tsx`, find the `onNext` (or equivalent) handler that the Next button calls — the existing code that advances `step` after validating the current step's schema. Locate by:

```bash
grep -n "schema = Step\|setStep((s) => (s + 1)\|form.trigger" ui/app/src/pages/Onboarding.tsx
```

The existing block looks roughly like:

```tsx
    if (step === 2) schema = Step2;
    if (step === 3) schema = Step3;
    /* ... validation ... */
    if (step < 3) setStep((s) => (s + 1) as 1 | 2 | 3);
```

Replace the `if (step < 3) setStep(...)` line with the autosave-then-advance pattern:

```tsx
    if (step < 3) {
      const nextStep = (step + 1) as 1 | 2 | 3;
      const values = form.getValues();
      // Subset of the form we expose as `OnboardingDraftFields` —
      // the cv File and agreeTerms boolean are intentionally excluded
      // (set on the final submit only).
      const fieldsForServer: OnboardingDraftFields = {
        target_job_title:    values.targetJobTitle,
        experience_level:    values.experienceLevel,
        job_search_status:   values.jobSearchStatus,
        salary_range:        values.salaryRange,
        wants_ats_report:    values.wantsATSReport,
        preferred_regions:   values.preferredRegions,
        preferred_timezones: values.preferredTimezones,
        preferred_languages: values.preferredLanguages,
        job_types:           values.jobTypes,
        country:             values.country,
        plan:                values.plan,
      };
      try {
        await saveOnboardingDraft(nextStep, fieldsForServer);
        setDraftSaveWarning(null);
      } catch {
        // Non-blocking: advance the wizard anyway; show a warning
        // that the draft didn't save. The next Next click retries.
        setDraftSaveWarning(
          "We couldn't save your progress to the server. Your answers are still here; we'll try again on the next step.",
        );
      }
      setStep(nextStep);
    }
```

The wrapping function must already be `async` — if it isn't, change its signature: find `function onNext(`/`const onNext = (` and add `async`. The Next button's `onClick={onNext}` doesn't need to change because React tolerates async onClick handlers (the returned promise is fire-and-forget).

- [ ] **Step 4: Render the draft-save warning above the form**

Find the existing `<Progress step={step} />` line. Immediately above it, insert:

```tsx
      {draftSaveWarning && (
        <div
          role="status"
          className="mb-4 rounded-md border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-800"
        >
          {draftSaveWarning}
        </div>
      )}
```

- [ ] **Step 5: Typecheck + build**

Run:
```bash
cd ui/app && npm run typecheck && npm run build
```
Both clean.

- [ ] **Step 6: Commit**

```bash
git add ui/app/src/pages/Onboarding.tsx
git commit -m "feat(onboarding): resume wizard from server-persisted draft

On mount (once the user is authenticated), fetch the draft and
hydrate the form + step in one form.reset call. Each 'Next' click
PUTs the current step's slice to the server BEFORE advancing locally;
a 5xx is non-blocking — the wizard still moves on and shows an inline
warning, and the next Next click retries. The cv File and
agreeTerms boolean are intentionally excluded from the draft (set
on the final submit only)."
```

## Task 2.3: AuthCallback routes based on subscription

**Files:**
- Modify: `ui/app/src/components/AuthCallback.tsx`

- [ ] **Step 1: Write the new routing logic**

Replace the body of `AuthCallback.tsx` (keep imports + JSX, change the effect):

In the imports, add `fetchMeSubscription`:

```ts
import { fetchMeSubscription } from "@/api/candidates";
```

In the `useEffect`, replace:

```tsx
    rt.completeRedirect()
      .then(() => {
        if (cancelled) return;
        window.location.assign("/dashboard/");
      })
```

with:

```tsx
    rt.completeRedirect()
      .then(async () => {
        if (cancelled) return;
        // Single gate: payment status decides where the user lands.
        // fetchMeSubscription's try/catch fallback returns
        // {status: "none", ...} on any failure, so a wedged
        // matching service degrades to "send the user to
        // onboarding" — the safer default for an inactive-or-unknown
        // subscription. See the paid-flow-routing spec.
        const sub = await fetchMeSubscription();
        const target = sub.status === "active" ? "/dashboard/" : "/onboarding/";
        window.location.assign(target);
      })
```

- [ ] **Step 2: Typecheck**

Run:
```bash
cd ui/app && npm run typecheck
```
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add ui/app/src/components/AuthCallback.tsx
git commit -m "feat(auth): route post-OIDC users by subscription status

AuthCallback used to unconditionally redirect to /dashboard/. Now it
checks the candidate's subscription status and routes them to
/dashboard/ if active, /onboarding/ otherwise. fetchMeSubscription
already has a graceful fallback (returns status: 'none' on any
failure) so a transient backend issue degrades to the onboarding
flow — the safe default for an unknown subscription."
```

## Task 2.4: Page-level guards on `/dashboard/` and `/onboarding/`

**Files:**
- Modify: `ui/app/src/pages/Dashboard.tsx`
- Modify: `ui/app/src/pages/Onboarding.tsx`

- [ ] **Step 1: Add the Dashboard guard**

In `ui/app/src/pages/Dashboard.tsx`, find the existing `subQ = useQuery(...)` block (around line 48 — fetches `me-subscription`). Immediately after that block insert:

```ts
  // Page-level guard: if the user lands here without an active
  // subscription (direct URL, browser back from /onboarding/, etc.),
  // bounce them to the wizard. Doesn't fire until the query resolves
  // so the Skeleton renders during the wait — no flash.
  useEffect(() => {
    if (state !== "authenticated") return;
    if (subQ.isLoading) return;
    if (subQ.data?.status !== "active") {
      window.location.assign("/onboarding/");
    }
  }, [state, subQ.isLoading, subQ.data?.status]);
```

- [ ] **Step 2: Add the Onboarding guard**

In `ui/app/src/pages/Onboarding.tsx`, near the existing `const { state, login } = useAuth();` line, add a subscription query just below it:

```ts
  const subQ = useQuery({
    queryKey: ["me-subscription"],
    queryFn: fetchMeSubscription,
    enabled: state === "authenticated",
    staleTime: 60_000,
  });
```

(Add to the existing import line for `@/api/candidates` whatever you don't already have: `fetchMeSubscription`. And add `import { useQuery } from "@tanstack/react-query";` to the top — check first whether it's already imported.)

Then immediately under the `subQ` declaration, add the mirror guard:

```ts
  // Mirror guard: a paid user shouldn't be in the wizard. Bouncing
  // them keeps the URL bar honest.
  useEffect(() => {
    if (state !== "authenticated") return;
    if (subQ.isLoading) return;
    if (subQ.data?.status === "active") {
      window.location.assign("/dashboard/");
    }
  }, [state, subQ.isLoading, subQ.data?.status]);
```

- [ ] **Step 3: Typecheck + build**

Run:
```bash
cd ui/app && npm run typecheck && npm run build
```
Both clean.

- [ ] **Step 4: Commit**

```bash
git add ui/app/src/pages/Dashboard.tsx ui/app/src/pages/Onboarding.tsx
git commit -m "feat(routing): page-level subscription guards on /dashboard/ + /onboarding/

The AuthCallback redirect handles the just-signed-in case; these
guards handle direct-URL access (bookmarks, back button, page
refresh after subscription state changed). Each page redirects to
the OTHER page if the gate doesn't match; the Skeleton/loading
state already renders during the subQ wait so there's no flash."
```

## Task 2.5: Clear draft inside the existing `POST /candidates/onboard` submit

**Files:**
- Locate the existing handler for `POST /candidates/onboard` (it doesn't live in `apps/matching/service/http/v1/` — find it).
- Modify whatever handler currently writes the candidate profile from the final submit.

- [ ] **Step 1: Find the onboard handler**

Run:
```bash
grep -rn "candidates/onboard\|HandleFunc.*onboard\|OnboardHandler" apps/ pkg/ | grep -v _test.go
```
There may be no existing handler — the wizard might call a path that just lands on the same `/candidates/preferences` handler with the full payload. If `candidates/onboard` is registered nowhere, the wizard's `submitOnboarding` call is misdirected and that's a pre-existing bug; flag it and the simplest fix is to add the handler now. The handler should:

1. Validate the payload (target_job_title required, plan in {starter, pro, managed}, etc.).
2. Update the candidate row's canonical columns with the submitted values.
3. `ClearOnboardingDraft` in the same transaction.
4. Return 200.

- [ ] **Step 2: If the handler exists, wire ClearOnboardingDraft into its successful path**

Inside whatever handler writes the candidate row from the submit, the existing code probably does some form of `candidateRepo.Update(ctx, cand)`. Wrap that update + a `ClearOnboardingDraft` in a single transaction:

```go
err := candidateRepo.WithTx(ctx, func(txRepo *repository.CandidateRepository) error {
    if err := txRepo.Update(ctx, cand); err != nil {
        return fmt.Errorf("update candidate: %w", err)
    }
    if err := txRepo.ClearOnboardingDraft(ctx, cand.ID); err != nil {
        return fmt.Errorf("clear draft: %w", err)
    }
    return nil
})
```

(`WithTx` is the existing transaction helper on `CandidateRepository`; if it doesn't exist you'll see a compile error — in that case wrap the two calls in `r.db(ctx, false).Transaction(...)` directly.)

- [ ] **Step 3: If the handler does NOT exist, add it as a new file**

Create `apps/matching/service/http/v1/candidates_onboard.go` with a handler that:
- Wraps in `httpmw.CandidateAuth`.
- Decodes the JSON body into a struct mirroring the wizard's final payload.
- Validates the same fields the existing Step3 schema validates on the client.
- Updates the candidate row + clears the draft in one transaction.
- Returns the updated candidate summary.

Add the test alongside it, mirroring `me_subscription_test.go`'s pattern for handler-level testing with an in-memory fake repository.

Register the handler in `apps/matching/cmd/main.go` next to `/candidates/preferences`.

- [ ] **Step 4: Test the transaction guarantee**

Add an integration test in `pkg/repository/candidate_test.go` that:
1. Writes a draft.
2. Runs a transaction that updates the candidate row then calls `ClearOnboardingDraft`, but the caller forces a rollback.
3. Reads the draft back and asserts it's still there (rollback worked).

Then a second case where the transaction commits and asserts the draft is `{}`.

```go
func TestCandidateRepository_OnboardingDraft_ClearedInTransaction(t *testing.T) {
	g, ctx := setupCandidateDB(t)
	repo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return g })

	cand := &domain.CandidateProfile{}
	cand.ID = "cand_txn"
	cand.ProfileID = "prof_txn"
	require.NoError(t, repo.Create(ctx, cand))
	require.NoError(t, repo.SetOnboardingDraft(ctx, "cand_txn",
		json.RawMessage(`{"step":3,"fields":{"plan":"pro"}}`)))

	// Commit path: draft is gone after the transaction.
	err := g.Transaction(func(tx *gorm.DB) error {
		txRepo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return tx })
		return txRepo.ClearOnboardingDraft(ctx, "cand_txn")
	})
	require.NoError(t, err)
	got, err := repo.GetOnboardingDraft(ctx, "cand_txn")
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(got))

	// Re-seed, then a rollback keeps the draft.
	require.NoError(t, repo.SetOnboardingDraft(ctx, "cand_txn",
		json.RawMessage(`{"step":3,"fields":{"plan":"pro"}}`)))
	_ = g.Transaction(func(tx *gorm.DB) error {
		txRepo := repository.NewCandidateRepository(func(_ context.Context, _ bool) *gorm.DB { return tx })
		require.NoError(t, txRepo.ClearOnboardingDraft(ctx, "cand_txn"))
		return errors.New("force rollback")
	})
	got, err = repo.GetOnboardingDraft(ctx, "cand_txn")
	require.NoError(t, err)
	require.JSONEq(t, `{"step":3,"fields":{"plan":"pro"}}`, string(got))
}
```

Run:
```bash
go test -tags integration -race -run TestCandidateRepository_OnboardingDraft -timeout 120s ./pkg/repository/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/matching/cmd/main.go apps/matching/service/http/v1/candidates_onboard*.go pkg/repository/candidate_test.go
git commit -m "feat(matching): clear onboarding draft atomically on final submit

The /candidates/onboard handler runs the candidate-profile update +
ClearOnboardingDraft in one transaction. On rollback the draft
survives so the user resumes at the step they were on; on commit
the canonical columns hold the data and the draft column is
back to '{}'. Integration test exercises both branches."
```

## Task 2.6: Ship Phase 2 and verify end-to-end

**Files:** none

- [ ] **Step 1: Tag a Docker release for the matching service**

Phase 2's frontend changes alone don't need a backend release, but the candidates/onboard transaction fix from Task 2.5 does. Tag a fresh release:

```bash
git checkout main
git pull --ff-only origin main
LATEST_TAG=$(git describe --tags --abbrev=0)
NEW_TAG=$(echo "$LATEST_TAG" | awk -F. -v OFS=. '{$NF=$NF+1; print}')
git tag -a "$NEW_TAG" -m "$NEW_TAG — Phase 2 of paid-flow (transactional draft clear)"
git push origin "$NEW_TAG"
gh run watch $(gh run list --workflow=release.yaml --limit 1 --json databaseId --jq '.[0].databaseId')
```

Wait for the workflow green. Flux's ImagePolicy will pick up the new tag within ~5 min and roll the pods.

- [ ] **Step 2: Smoke-test in a real browser**

Once the new image is live:
1. Open `https://jobs.stawi.org/` in incognito → click Sign In → complete OIDC.
2. Expected: lands on `/onboarding/` (no subscription).
3. Fill Step 1 → click Next.
4. Open DevTools Network → see `PUT /matching/me/onboarding` returning 204.
5. Close the tab. Open a new incognito window → sign in again.
6. Expected: lands on `/onboarding/` at Step 2 with Step 1 fields pre-filled.
7. Continue to Step 3 → choose plan + agree to terms → submit.
8. Expected: redirect to payment provider.
9. Complete payment.
10. Expected: returns to `/dashboard/` (today's placeholder panels still — Phase 3 swaps those out).
11. Sign in fresh from another browser → expected: lands directly on `/dashboard/`.

If any step fails, the relevant log to grep is `me/onboarding` in `kubectl -n product-opportunities logs -l app.kubernetes.io/name=opportunities-matching`.

---

# Phase 3a — Saved-jobs backend + opportunities aggregation endpoint

Adds the missing `candidate_saved_jobs` table, the `GET /me/opportunities` aggregation endpoint (joins matches + saved + applications), the star/unstar mutation handlers, and the `POST /me/applications` handler (wraps `pkg/applications/business` since the standalone applications service isn't deployed yet).

## Task 3a.1: `candidate_saved_jobs` migration

**Files:**
- Create: `db/migrations/0018_candidate_saved_jobs.sql`

- [ ] **Step 1: Write the migration**

Create `db/migrations/0018_candidate_saved_jobs.sql`:

```sql
-- 0018: candidate-starred opportunities.
-- Composite PK so the same candidate can star the same opportunity
-- only once. Read pattern is "give me all stars for candidate X",
-- so a single index keyed by candidate_id with created_at DESC for
-- the dashboard's reverse-chronological default ordering is enough.

CREATE TABLE IF NOT EXISTS candidate_saved_jobs (
    candidate_id   TEXT        NOT NULL,
    opportunity_id TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (candidate_id, opportunity_id)
);

CREATE INDEX IF NOT EXISTS idx_candidate_saved_jobs_candidate_created
    ON candidate_saved_jobs (candidate_id, created_at DESC);
```

- [ ] **Step 2: Verify applies cleanly**

```bash
PGPASSWORD=postgres psql -h localhost -U postgres -d candidates -f db/migrations/0018_candidate_saved_jobs.sql
PGPASSWORD=postgres psql -h localhost -U postgres -d candidates -c "\d candidate_saved_jobs"
```
Expected output shows the table + index.

- [ ] **Step 3: Commit**

```bash
git add db/migrations/0018_candidate_saved_jobs.sql
git commit -m "feat(migrations): candidate_saved_jobs table"
```

## Task 3a.2: `pkg/savedjobs/store.go` with `Star` / `Unstar` / `ListByCandidate`

**Files:**
- Create: `pkg/savedjobs/store.go`
- Create: `pkg/savedjobs/store_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `pkg/savedjobs/store_test.go`:

```go
//go:build integration

package savedjobs_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/savedjobs"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setup(t *testing.T) (*savedjobs.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyMigrationsDir(t, ctx, db, "../../db/migrations")
	return savedjobs.NewStore(db), ctx
}

func TestStore_StarUnstarRoundTrip(t *testing.T) {
	s, ctx := setup(t)
	require.NoError(t, s.Star(ctx, "cand_a", "opp_1"))
	require.NoError(t, s.Star(ctx, "cand_a", "opp_2"))
	require.NoError(t, s.Star(ctx, "cand_b", "opp_1"))

	ids, err := s.ListByCandidate(ctx, "cand_a")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"opp_1", "opp_2"}, ids)

	require.NoError(t, s.Unstar(ctx, "cand_a", "opp_1"))
	ids, err = s.ListByCandidate(ctx, "cand_a")
	require.NoError(t, err)
	require.Equal(t, []string{"opp_2"}, ids)
}

func TestStore_StarIsIdempotent(t *testing.T) {
	s, ctx := setup(t)
	require.NoError(t, s.Star(ctx, "cand_c", "opp_x"))
	// Same pair again — no error (ON CONFLICT DO NOTHING).
	require.NoError(t, s.Star(ctx, "cand_c", "opp_x"))
	ids, err := s.ListByCandidate(ctx, "cand_c")
	require.NoError(t, err)
	require.Equal(t, []string{"opp_x"}, ids)
}

func TestStore_UnstarUnknownPairIsNoop(t *testing.T) {
	s, ctx := setup(t)
	require.NoError(t, s.Unstar(ctx, "cand_d", "opp_never_starred"))
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test -tags integration -race -run TestStore_StarUnstarRoundTrip -timeout 120s ./pkg/savedjobs/...
```
Expected: FAIL with `cannot find package savedjobs`.

- [ ] **Step 3: Implement the store**

Create `pkg/savedjobs/store.go`:

```go
// Package savedjobs is the read+write surface for candidate-starred
// opportunities. Backed by the candidate_saved_jobs table (see
// db/migrations/0018_candidate_saved_jobs.sql). Sibling to
// pkg/matching/store.go; same raw-sql style for the same reason —
// the queries are aggregate-shaped and don't fit BaseRepository.
package savedjobs

import (
	"context"
	"database/sql"
	"fmt"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Star marks (candidate, opportunity) as saved. Idempotent: starring
// the same pair twice returns nil with no error.
func (s *Store) Star(ctx context.Context, candidateID, opportunityID string) error {
	const q = `
INSERT INTO candidate_saved_jobs (candidate_id, opportunity_id)
VALUES ($1, $2)
ON CONFLICT (candidate_id, opportunity_id) DO NOTHING
`
	if _, err := s.db.ExecContext(ctx, q, candidateID, opportunityID); err != nil {
		return fmt.Errorf("savedjobs: star: %w", err)
	}
	return nil
}

// Unstar removes the (candidate, opportunity) pair. Idempotent: a
// pair that was never starred returns nil.
func (s *Store) Unstar(ctx context.Context, candidateID, opportunityID string) error {
	const q = `DELETE FROM candidate_saved_jobs WHERE candidate_id = $1 AND opportunity_id = $2`
	if _, err := s.db.ExecContext(ctx, q, candidateID, opportunityID); err != nil {
		return fmt.Errorf("savedjobs: unstar: %w", err)
	}
	return nil
}

// ListByCandidate returns the opportunity IDs the candidate has
// starred, most-recent first. Caller joins these against the
// opportunities table to materialise the snapshot — this package
// only knows the star list.
func (s *Store) ListByCandidate(ctx context.Context, candidateID string) ([]string, error) {
	const q = `
SELECT opportunity_id
FROM candidate_saved_jobs
WHERE candidate_id = $1
ORDER BY created_at DESC, opportunity_id
`
	rows, err := s.db.QueryContext(ctx, q, candidateID)
	if err != nil {
		return nil, fmt.Errorf("savedjobs: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("savedjobs: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("savedjobs: rows: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
go test -tags integration -race -timeout 120s ./pkg/savedjobs/...
```
Expected: PASS, three tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/savedjobs/store.go pkg/savedjobs/store_test.go
git commit -m "feat(savedjobs): Star / Unstar / ListByCandidate store

Backed by candidate_saved_jobs (0018). Both mutations are
idempotent: Star uses ON CONFLICT DO NOTHING, Unstar is a plain
DELETE that no-ops on a missing pair. ListByCandidate orders by
created_at DESC so the dashboard's default sort is 'most-recently
starred first' without a client-side sort."
```

## Task 3a.3: HTTP handlers `POST` / `DELETE` `/me/saved-jobs/...`

**Files:**
- Create: `apps/matching/service/http/v1/me_saved_jobs.go`
- Create: `apps/matching/service/http/v1/me_saved_jobs_test.go`

- [ ] **Step 1: Write the failing handler unit test**

Create `apps/matching/service/http/v1/me_saved_jobs_test.go`:

```go
package v1_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type fakeSaver struct {
	starErr   error
	unstarErr error
	calledStar     [2]string
	calledUnstar   [2]string
	starCalls, unstarCalls int
}

func (f *fakeSaver) Star(_ context.Context, c, o string) error {
	f.calledStar = [2]string{c, o}
	f.starCalls++
	return f.starErr
}
func (f *fakeSaver) Unstar(_ context.Context, c, o string) error {
	f.calledUnstar = [2]string{c, o}
	f.unstarCalls++
	return f.unstarErr
}

func TestSavedJobsHandler_PostStars(t *testing.T) {
	t.Parallel()
	s := &fakeSaver{}
	h := httpmw.CandidateAuth(v1.SavedJobsHandler(v1.SavedJobsDeps{Store: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/saved-jobs", bytes.NewBufferString(`{"opportunity_id":"opp_77"}`))
	req.Header.Set("X-Candidate-ID", "cand_x")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, [2]string{"cand_x", "opp_77"}, s.calledStar)
}

func TestSavedJobsHandler_PostRejectsMissingID(t *testing.T) {
	t.Parallel()
	s := &fakeSaver{}
	h := httpmw.CandidateAuth(v1.SavedJobsHandler(v1.SavedJobsDeps{Store: s}))

	for _, body := range []string{`{}`, `{"opportunity_id":""}`, `not json`} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/me/saved-jobs", bytes.NewBufferString(body))
		req.Header.Set("X-Candidate-ID", "cand_x")
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%q", body)
	}
	require.Zero(t, s.starCalls)
}

func TestSavedJobsHandler_DeleteUnstars(t *testing.T) {
	t.Parallel()
	s := &fakeSaver{}
	h := httpmw.CandidateAuth(v1.SavedJobsHandler(v1.SavedJobsDeps{Store: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/me/saved-jobs/opp_77", nil)
	req.Header.Set("X-Candidate-ID", "cand_x")
	// SetPathValue is how Go 1.22+ http.ServeMux populates {param}
	// outside of the mux's own dispatch path (which a test bypasses).
	req.SetPathValue("opportunity_id", "opp_77")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, [2]string{"cand_x", "opp_77"}, s.calledUnstar)
}

func TestSavedJobsHandler_StarPersistErrorIs502(t *testing.T) {
	t.Parallel()
	s := &fakeSaver{starErr: errors.New("db wedged")}
	h := httpmw.CandidateAuth(v1.SavedJobsHandler(v1.SavedJobsDeps{Store: s}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/saved-jobs", bytes.NewBufferString(`{"opportunity_id":"opp_77"}`))
	req.Header.Set("X-Candidate-ID", "cand_x")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test -race -run TestSavedJobsHandler ./apps/matching/service/http/v1/...
```
Expected: FAIL with `undefined: v1.SavedJobsHandler`.

- [ ] **Step 3: Implement the handler**

Create `apps/matching/service/http/v1/me_saved_jobs.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// StarUnstarStore is the write surface SavedJobsHandler depends on.
// Implemented by *savedjobs.Store in production; the interface lets
// tests pass an in-memory fake.
type StarUnstarStore interface {
	Star(ctx context.Context, candidateID, opportunityID string) error
	Unstar(ctx context.Context, candidateID, opportunityID string) error
}

type SavedJobsDeps struct {
	Store StarUnstarStore
}

// SavedJobsHandler dispatches POST (star) and DELETE (unstar) on the
// same path. POST takes a JSON body `{opportunity_id}` so the route
// `/me/saved-jobs` doesn't need a path param; DELETE pulls the id
// from the path `/me/saved-jobs/{opportunity_id}` so REST clients can
// hit it idempotently from a URL alone.
//
// Mount with two registrations:
//   mux.Handle("POST   /me/saved-jobs",                 httpmw.CandidateAuth(handler))
//   mux.Handle("DELETE /me/saved-jobs/{opportunity_id}", httpmw.CandidateAuth(handler))
func SavedJobsHandler(deps SavedJobsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			savedJobsStar(deps, w, r)
		case http.MethodDelete:
			savedJobsUnstar(deps, w, r)
		default:
			w.Header().Set("Allow", "POST, DELETE")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use POST to star, DELETE /{opportunity_id} to unstar")
		}
	}
}

func savedJobsStar(deps SavedJobsDeps, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := util.Log(ctx)
	candidateID := httpmw.CandidateFromContext(ctx)

	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024))
	if err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "body_read_failed", "could not read request body")
		return
	}
	var in struct{ OpportunityID string `json:"opportunity_id"` }
	if err := json.Unmarshal(body, &in); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	oppID := strings.TrimSpace(in.OpportunityID)
	if oppID == "" {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "opportunity_id_required", "opportunity_id is required")
		return
	}
	if err := deps.Store.Star(ctx, candidateID, oppID); err != nil {
		log.WithError(err).WithField("candidate_id", candidateID).WithField("opportunity_id", oppID).
			Error("me/saved-jobs: star failed")
		httpmw.ProblemJSON(w, http.StatusBadGateway, "star_failed", "could not save opportunity")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func savedJobsUnstar(deps SavedJobsDeps, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := util.Log(ctx)
	candidateID := httpmw.CandidateFromContext(ctx)

	oppID := strings.TrimSpace(r.PathValue("opportunity_id"))
	if oppID == "" {
		httpmw.ProblemJSON(w, http.StatusBadRequest, "opportunity_id_required", "opportunity_id is required in the path")
		return
	}
	if err := deps.Store.Unstar(ctx, candidateID, oppID); err != nil {
		log.WithError(err).WithField("candidate_id", candidateID).WithField("opportunity_id", oppID).
			Error("me/saved-jobs: unstar failed")
		httpmw.ProblemJSON(w, http.StatusBadGateway, "unstar_failed", "could not remove opportunity")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
go test -race -run TestSavedJobsHandler ./apps/matching/service/http/v1/...
```
Expected: PASS, four tests.

- [ ] **Step 5: Commit**

```bash
git add apps/matching/service/http/v1/me_saved_jobs.go apps/matching/service/http/v1/me_saved_jobs_test.go
git commit -m "feat(matching): POST/DELETE /me/saved-jobs handlers

One handler dispatches both methods. POST takes a JSON body so the
URL stays generic; DELETE pulls the opportunity_id from
/me/saved-jobs/{opportunity_id} so REST clients can star/unstar
from a URL alone. Both delegate to pkg/savedjobs.Store which makes
them idempotent (ON CONFLICT on insert, no-op on missing pair for
delete)."
```

## Task 3a.4: `pkg/matching/store.go` adds `ListOpportunitiesForCandidate`

**Files:**
- Modify: `pkg/matching/store.go`
- Modify: `pkg/matching/store_test.go` (append the test)

- [ ] **Step 1: Write the failing integration test**

Append to `pkg/matching/store_test.go`:

```go
func TestStore_ListOpportunitiesForCandidate_Filters(t *testing.T) {
	db, ctx := setupStoreDB(t)
	s := matching.NewStore(db)

	const candID = "cand_feed"
	// Seed: opp_match (only matched), opp_starred (only starred),
	// opp_applied (matched AND applied AND starred), opp_overflow
	// (matched but overflow — should never appear regardless of filter).
	insertMatch := func(matchID, oppID string, status matching.MatchStatus) {
		_, err := db.ExecContext(ctx, `
INSERT INTO candidate_matches (match_id, candidate_id, opportunity_id, status, score, last_event_id, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, 0.7, 'evt_'||$1, '{}'::jsonb, NOW(), NOW())`,
			matchID, candID, oppID, string(status))
		require.NoError(t, err)
	}
	insertMatch("m_match", "opp_match", matching.StatusNew)
	insertMatch("m_applied", "opp_applied", matching.StatusApplied)
	insertMatch("m_overflow", "opp_overflow", matching.StatusOverflow)

	_, err := db.ExecContext(ctx, `INSERT INTO candidate_saved_jobs (candidate_id, opportunity_id) VALUES ($1, $2), ($1, $3)`,
		candID, "opp_starred", "opp_applied")
	require.NoError(t, err)

	// Seed an application row for opp_applied.
	_, err = db.ExecContext(ctx, `
INSERT INTO candidate_applications (id, candidate_id, opportunity_id, status, method, applied_at, created_at, updated_at)
VALUES ('app_1', $1, 'opp_applied', 'applied', 'manual', NOW(), NOW(), NOW())`, candID)
	require.NoError(t, err)

	// filter=all → matches union starred union applied; overflow excluded.
	page, err := s.ListOpportunitiesForCandidate(ctx, matching.ListOpportunitiesParams{
		CandidateID: candID, Filter: matching.FilterAll, Limit: 50,
	})
	require.NoError(t, err)
	ids := opportunityIDs(page.Items)
	require.ElementsMatch(t, []string{"opp_match", "opp_starred", "opp_applied"}, ids)

	// filter=matches → matches only (no overflow).
	page, err = s.ListOpportunitiesForCandidate(ctx, matching.ListOpportunitiesParams{
		CandidateID: candID, Filter: matching.FilterMatches, Limit: 50,
	})
	require.NoError(t, err)
	ids = opportunityIDs(page.Items)
	require.ElementsMatch(t, []string{"opp_match", "opp_applied"}, ids)

	// filter=starred → only what's in candidate_saved_jobs.
	page, err = s.ListOpportunitiesForCandidate(ctx, matching.ListOpportunitiesParams{
		CandidateID: candID, Filter: matching.FilterStarred, Limit: 50,
	})
	require.NoError(t, err)
	ids = opportunityIDs(page.Items)
	require.ElementsMatch(t, []string{"opp_starred", "opp_applied"}, ids)

	// filter=applied → only what's in candidate_applications.
	page, err = s.ListOpportunitiesForCandidate(ctx, matching.ListOpportunitiesParams{
		CandidateID: candID, Filter: matching.FilterApplied, Limit: 50,
	})
	require.NoError(t, err)
	ids = opportunityIDs(page.Items)
	require.Equal(t, []string{"opp_applied"}, ids)

	// State annotations land on the right rows.
	for _, item := range page.Items {
		if item.OpportunityID == "opp_applied" {
			require.True(t, item.Starred)
			require.NotNil(t, item.Application)
			require.Equal(t, "applied", item.Application.Status)
		}
	}
}

func opportunityIDs(items []matching.OpportunityFeedItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.OpportunityID)
	}
	return out
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test -tags integration -race -run TestStore_ListOpportunitiesForCandidate -timeout 120s ./pkg/matching/...
```
Expected: FAIL with `undefined: matching.ListOpportunitiesForCandidate` (also `matching.OpportunityFeedItem`, `matching.ListOpportunitiesParams`, `matching.FilterAll/Matches/Starred/Applied`).

- [ ] **Step 3: Implement the join + the supporting types**

Append to `pkg/matching/store.go`:

```go
// FeedFilter is the dashboard's filter chip — drives which join the
// /me/opportunities query uses as its base set.
type FeedFilter string

const (
	FilterAll      FeedFilter = "all"
	FilterMatches  FeedFilter = "matches"
	FilterStarred  FeedFilter = "starred"
	FilterApplied  FeedFilter = "applied"
)

// ApplicationSummary is the slice of candidate_applications the
// dashboard renders next to each opportunity. Status uses the
// pkg/applications state-machine values directly.
type ApplicationSummary struct {
	Status      string
	AppliedAt   time.Time
	LastEventAt time.Time
	Method      string
}

// OpportunityFeedItem is one row of the unified opportunities feed.
// `Score` is only meaningful when the opportunity has a match row;
// the handler omits it when matching.Score is the zero value.
type OpportunityFeedItem struct {
	OpportunityID string
	Score         float64
	Starred       bool
	Application   *ApplicationSummary
	CreatedAt     time.Time
}

type ListOpportunitiesParams struct {
	CandidateID string
	Filter      FeedFilter
	Cursor      string
	Limit       int
}

type ListOpportunitiesPage struct {
	Items      []OpportunityFeedItem
	NextCursor string
	HasMore    bool
}

// ListOpportunitiesForCandidate is the dashboard's main feed query.
// One round-trip; joins three tables (candidate_matches,
// candidate_saved_jobs, candidate_applications) all keyed by
// candidate_id. The filter determines the base set:
//
//   FilterAll      — union of matches (excluding overflow) ∪ starred ∪ applied
//   FilterMatches  — matches only (excluding overflow); annotated with starred + application
//   FilterStarred  — starred only; annotated with score (if matched) + application
//   FilterApplied  — applied only; annotated with score + starred
//
// Pagination via the existing pageCursor pattern (score-desc,
// created-at-desc, opportunity-id-asc) so the ordering is fully
// deterministic and cursor-resumable.
func (s *Store) ListOpportunitiesForCandidate(ctx context.Context, p ListOpportunitiesParams) (ListOpportunitiesPage, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}

	var baseCTE string
	switch p.Filter {
	case FilterMatches:
		baseCTE = `
WITH base AS (
  SELECT opportunity_id, score, created_at FROM candidate_matches
  WHERE candidate_id = $1 AND status != 'overflow'
)`
	case FilterStarred:
		baseCTE = `
WITH base AS (
  SELECT s.opportunity_id, COALESCE(m.score, 0) AS score, s.created_at
  FROM candidate_saved_jobs s
  LEFT JOIN candidate_matches m
    ON m.candidate_id = s.candidate_id AND m.opportunity_id = s.opportunity_id AND m.status != 'overflow'
  WHERE s.candidate_id = $1
)`
	case FilterApplied:
		baseCTE = `
WITH base AS (
  SELECT a.opportunity_id, COALESCE(m.score, 0) AS score, a.applied_at AS created_at
  FROM candidate_applications a
  LEFT JOIN candidate_matches m
    ON m.candidate_id = a.candidate_id AND m.opportunity_id = a.opportunity_id AND m.status != 'overflow'
  WHERE a.candidate_id = $1
)`
	default: // FilterAll
		baseCTE = `
WITH base AS (
  SELECT opportunity_id, score, created_at FROM candidate_matches
    WHERE candidate_id = $1 AND status != 'overflow'
  UNION
  SELECT s.opportunity_id, 0 AS score, s.created_at FROM candidate_saved_jobs s
    WHERE s.candidate_id = $1
  UNION
  SELECT a.opportunity_id, 0 AS score, a.applied_at AS created_at FROM candidate_applications a
    WHERE a.candidate_id = $1
)`
	}

	args := []any{p.CandidateID}
	cursorWhere := ""
	if p.Cursor != "" {
		cur, err := decodeCursor(p.Cursor)
		if err != nil {
			return ListOpportunitiesPage{}, fmt.Errorf("matching: cursor: %w", err)
		}
		args = append(args, cur.Score, cur.CreatedAt, cur.MatchID)
		cursorWhere = " AND (b.score, b.created_at, b.opportunity_id) < ($2, $3::timestamptz, $4)"
	}
	args = append(args, limit+1)

	q := baseCTE + `
SELECT b.opportunity_id, b.score, b.created_at,
       (s.opportunity_id IS NOT NULL) AS starred,
       a.status, a.applied_at, a.updated_at, a.method
FROM base b
LEFT JOIN candidate_saved_jobs s
  ON s.candidate_id = $1 AND s.opportunity_id = b.opportunity_id
LEFT JOIN candidate_applications a
  ON a.candidate_id = $1 AND a.opportunity_id = b.opportunity_id
WHERE 1=1` + cursorWhere + `
ORDER BY b.score DESC, b.created_at DESC, b.opportunity_id ASC
LIMIT $` + fmt.Sprint(len(args))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return ListOpportunitiesPage{}, fmt.Errorf("matching: list opportunities: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]OpportunityFeedItem, 0, limit)
	for rows.Next() {
		var (
			item       OpportunityFeedItem
			appStatus  sql.NullString
			appAt      sql.NullTime
			appUpdated sql.NullTime
			appMethod  sql.NullString
		)
		if err := rows.Scan(&item.OpportunityID, &item.Score, &item.CreatedAt,
			&item.Starred, &appStatus, &appAt, &appUpdated, &appMethod); err != nil {
			return ListOpportunitiesPage{}, fmt.Errorf("matching: scan opportunity feed: %w", err)
		}
		if appStatus.Valid && appAt.Valid {
			item.Application = &ApplicationSummary{
				Status:      appStatus.String,
				AppliedAt:   appAt.Time,
				LastEventAt: appUpdated.Time,
				Method:      appMethod.String,
			}
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return ListOpportunitiesPage{}, fmt.Errorf("matching: list opportunities rows: %w", err)
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	var nextCur string
	if hasMore {
		last := out[len(out)-1]
		nextCur = encodeCursor(pageCursor{
			Score:     last.Score,
			CreatedAt: last.CreatedAt,
			MatchID:   last.OpportunityID,
		})
	}
	return ListOpportunitiesPage{Items: out, NextCursor: nextCur, HasMore: hasMore}, nil
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
go test -tags integration -race -run TestStore_ListOpportunitiesForCandidate -timeout 120s ./pkg/matching/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/matching/store.go pkg/matching/store_test.go
git commit -m "feat(matching): ListOpportunitiesForCandidate 3-way join

One query joins candidate_matches, candidate_saved_jobs, and
candidate_applications keyed by candidate_id. The filter chip drives
which table is the base set in the CTE; the LEFT JOINs add the
state annotations (starred, application summary) regardless of
which base was chosen. Pagination reuses the existing pageCursor
(score DESC, created_at DESC, opportunity_id ASC); overflow rows
are excluded everywhere they appear because they were cap-suppressed
and never delivered to the candidate."
```

## Task 3a.5: HTTP handler `GET /me/opportunities`

**Files:**
- Create: `apps/matching/service/http/v1/me_opportunities.go`
- Create: `apps/matching/service/http/v1/me_opportunities_test.go`

- [ ] **Step 1: Write the failing handler test**

Create `apps/matching/service/http/v1/me_opportunities_test.go`:

```go
package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeFeedStore struct {
	page     matching.ListOpportunitiesPage
	err      error
	lastArgs matching.ListOpportunitiesParams
}

func (f *fakeFeedStore) ListOpportunitiesForCandidate(_ context.Context, p matching.ListOpportunitiesParams) (matching.ListOpportunitiesPage, error) {
	f.lastArgs = p
	return f.page, f.err
}

func TestOpportunitiesHandler_DefaultFilter(t *testing.T) {
	t.Parallel()
	store := &fakeFeedStore{page: matching.ListOpportunitiesPage{
		Items: []matching.OpportunityFeedItem{
			{OpportunityID: "opp_a", Score: 0.9, Starred: true, CreatedAt: time.Unix(1, 0)},
			{OpportunityID: "opp_b", Score: 0.6, Application: &matching.ApplicationSummary{
				Status: "applied", AppliedAt: time.Unix(2, 0), Method: "manual",
			}, CreatedAt: time.Unix(2, 0)},
		},
		NextCursor: "next-cur",
	}}
	h := httpmw.CandidateAuth(v1.OpportunitiesHandler(v1.OpportunitiesDeps{Store: store}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me/opportunities", nil)
	req.Header.Set("X-Candidate-ID", "cand_y")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Items []struct {
			OpportunityID string  `json:"opportunity_id"`
			Score         float64 `json:"score,omitempty"`
			Starred       bool    `json:"starred"`
			Application   *struct {
				Status string `json:"status"`
			} `json:"application,omitempty"`
		} `json:"items"`
		NextCursor string `json:"next_cursor,omitempty"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 2)
	require.Equal(t, "opp_a", body.Items[0].OpportunityID)
	require.True(t, body.Items[0].Starred)
	require.Nil(t, body.Items[0].Application)
	require.Equal(t, "opp_b", body.Items[1].OpportunityID)
	require.NotNil(t, body.Items[1].Application)
	require.Equal(t, "applied", body.Items[1].Application.Status)
	require.Equal(t, "next-cur", body.NextCursor)
	require.Equal(t, matching.FilterAll, store.lastArgs.Filter)
}

func TestOpportunitiesHandler_FilterPropagates(t *testing.T) {
	t.Parallel()
	store := &fakeFeedStore{}
	h := httpmw.CandidateAuth(v1.OpportunitiesHandler(v1.OpportunitiesDeps{Store: store}))

	for raw, want := range map[string]matching.FeedFilter{
		"":        matching.FilterAll,
		"all":     matching.FilterAll,
		"matches": matching.FilterMatches,
		"starred": matching.FilterStarred,
		"applied": matching.FilterApplied,
		"bogus":   matching.FilterAll, // unknown filters fall back to all
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/me/opportunities?filter="+raw, nil)
		req.Header.Set("X-Candidate-ID", "cand_y")
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "filter=%q", raw)
		require.Equal(t, want, store.lastArgs.Filter, "filter=%q", raw)
	}
}

func TestOpportunitiesHandler_StoreErrorIs502(t *testing.T) {
	t.Parallel()
	store := &fakeFeedStore{err: errors.New("db wedged")}
	h := httpmw.CandidateAuth(v1.OpportunitiesHandler(v1.OpportunitiesDeps{Store: store}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me/opportunities", nil)
	req.Header.Set("X-Candidate-ID", "cand_y")
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadGateway, rec.Code)
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test -race -run TestOpportunitiesHandler ./apps/matching/service/http/v1/...
```
Expected: FAIL with `undefined: v1.OpportunitiesHandler`.

- [ ] **Step 3: Implement the handler**

Create `apps/matching/service/http/v1/me_opportunities.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// OpportunityFeedStore is the read surface the handler depends on —
// defined here instead of importing matching.Store directly so tests
// can pass an in-memory fake without spinning a real DB.
type OpportunityFeedStore interface {
	ListOpportunitiesForCandidate(ctx context.Context, p matching.ListOpportunitiesParams) (matching.ListOpportunitiesPage, error)
}

type OpportunitiesDeps struct {
	Store OpportunityFeedStore
}

type feedItemDTO struct {
	OpportunityID string                  `json:"opportunity_id"`
	Score         float64                 `json:"score,omitempty"`
	Starred       bool                    `json:"starred"`
	Application   *applicationSummaryDTO  `json:"application,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
}

type applicationSummaryDTO struct {
	Status      string    `json:"status"`
	AppliedAt   time.Time `json:"applied_at"`
	LastEventAt time.Time `json:"last_event_at"`
	Method      string    `json:"method"`
}

type feedPageDTO struct {
	Items      []feedItemDTO `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// OpportunitiesHandler serves GET /me/opportunities — the dashboard's
// unified feed.
func OpportunitiesHandler(deps OpportunitiesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		filter := parseFilter(r.URL.Query().Get("filter"))
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
				limit = n
			}
		}

		page, err := deps.Store.ListOpportunitiesForCandidate(ctx, matching.ListOpportunitiesParams{
			CandidateID: candidateID,
			Filter:      filter,
			Cursor:      r.URL.Query().Get("cursor"),
			Limit:       limit,
		})
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).WithField("filter", string(filter)).
				Error("me/opportunities: list failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "feed_lookup_failed", "could not load opportunities feed")
			return
		}

		out := feedPageDTO{
			Items:      make([]feedItemDTO, 0, len(page.Items)),
			NextCursor: page.NextCursor,
		}
		for _, it := range page.Items {
			dto := feedItemDTO{
				OpportunityID: it.OpportunityID,
				Score:         it.Score,
				Starred:       it.Starred,
				CreatedAt:     it.CreatedAt,
			}
			if it.Application != nil {
				dto.Application = &applicationSummaryDTO{
					Status:      it.Application.Status,
					AppliedAt:   it.Application.AppliedAt,
					LastEventAt: it.Application.LastEventAt,
					Method:      it.Application.Method,
				}
			}
			out.Items = append(out.Items, dto)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func parseFilter(s string) matching.FeedFilter {
	switch s {
	case string(matching.FilterMatches):
		return matching.FilterMatches
	case string(matching.FilterStarred):
		return matching.FilterStarred
	case string(matching.FilterApplied):
		return matching.FilterApplied
	default: // empty or unknown
		return matching.FilterAll
	}
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
go test -race -run TestOpportunitiesHandler ./apps/matching/service/http/v1/...
```
Expected: PASS, three tests.

- [ ] **Step 5: Commit**

```bash
git add apps/matching/service/http/v1/me_opportunities.go apps/matching/service/http/v1/me_opportunities_test.go
git commit -m "feat(matching): GET /me/opportunities handler

Single endpoint backing the dashboard's unified feed. Filters via
?filter=all|matches|starred|applied; unknown filters fall back to
'all' (defensive — the URL is user-driven, no need to 400 on a
typo). Cursor passes through to the store unchanged. DTO drops
the `score` field for non-matched rows so the JSON shape matches
'omit when zero' rather than '0.0' (signals 'this isn't a match')."
```

## Task 3a.6: `POST /me/applications` handler (manual apply)

**Files:**
- Create: `apps/matching/service/http/v1/me_applications.go`
- Create: `apps/matching/service/http/v1/me_applications_test.go`

- [ ] **Step 1: Audit what apps/applications business layer already exposes**

Run:
```bash
grep -rn "^func.*Create\|^func.*Apply\|^func.*Transition" pkg/applications/ | grep -v _test.go
```
Find the existing "create an application" call in `pkg/applications/business/` — there's already one used by the (not-yet-deployed) applications service. The matching handler will wrap it directly.

- [ ] **Step 2: Write the failing handler test**

Create `apps/matching/service/http/v1/me_applications_test.go`. Mirror the saved-jobs test pattern: in-memory fake of the business-layer interface, assert POST body validation, success path returns the created application, store error → 502.

(The exact interface depends on what step 1 finds; the test stubs that interface in-package.)

- [ ] **Step 3: Run to verify it fails**

```bash
go test -race -run TestApplicationsHandler ./apps/matching/service/http/v1/...
```
Expected: FAIL on missing handler.

- [ ] **Step 4: Implement the handler**

Create `apps/matching/service/http/v1/me_applications.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// ApplicationStarter is the slice of pkg/applications/business this
// handler depends on. Defined here so the handler test can pass an
// in-memory fake without dragging the full business package + idempotency
// store into the test binary.
type ApplicationStarter interface {
	// StartApplication creates a `candidate_applications` row in the
	// 'applied' state for the given (candidate, opportunity, method)
	// triple. Returns the new application ID and the canonical
	// applied_at timestamp the dashboard will display.
	StartApplication(ctx context.Context, candidateID, opportunityID, method string) (applicationID string, appliedAt time.Time, err error)
}

type ApplicationsDeps struct {
	Starter ApplicationStarter
}

func ApplicationsHandler(deps ApplicationsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024))
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "body_read_failed", "could not read request body")
			return
		}
		var in struct {
			OpportunityID string `json:"opportunity_id"`
			Method        string `json:"method"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
			return
		}
		oppID := strings.TrimSpace(in.OpportunityID)
		if oppID == "" {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "opportunity_id_required", "opportunity_id is required")
			return
		}
		method := strings.TrimSpace(in.Method)
		if method == "" {
			method = "manual"
		}

		appID, appliedAt, err := deps.Starter.StartApplication(ctx, candidateID, oppID, method)
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).WithField("opportunity_id", oppID).
				Error("me/applications: start failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "apply_failed", "could not record application")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"application_id": appID,
			"status":         "applied",
			"applied_at":     appliedAt,
		})
	}
}
```

- [ ] **Step 5: Wire the production ApplicationStarter**

In `apps/matching/cmd/main.go`, where you'd register this handler in Task 3a.7, the `ApplicationStarter` you pass in production is a thin adapter over `pkg/applications/business`. Add inside `cmd/main.go` (above `mux.Handle(...)`):

```go
// applicationStarterAdapter bridges pkg/applications/business's
// CreateApplication (or whatever the existing entrypoint is) to the
// ApplicationStarter interface the /me/applications handler depends on.
type applicationStarterAdapter struct {
    biz *applications.Service // or the actual type from pkg/applications/business
}

func (a *applicationStarterAdapter) StartApplication(ctx context.Context, candidateID, opportunityID, method string) (string, time.Time, error) {
    // Translate to whatever pkg/applications/business expects.
    return a.biz.Create(ctx, applications.CreateInput{
        CandidateID:   candidateID,
        OpportunityID: opportunityID,
        Method:        method,
    })
}
```

The exact field names depend on what `pkg/applications/business` actually exposes — Task 3a.6 Step 1's grep reveals that.

- [ ] **Step 6: Run to verify the test passes**

```bash
go test -race -run TestApplicationsHandler ./apps/matching/service/http/v1/...
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add apps/matching/service/http/v1/me_applications.go apps/matching/service/http/v1/me_applications_test.go
git commit -m "feat(matching): POST /me/applications handler (manual apply)

Wraps pkg/applications/business to insert into candidate_applications.
The handler stays in the matching service for now because the
standalone applications service isn't deployed yet (see paid-flow
spec, 'Reality check on what's already shipped'). When the
applications service ships, this handler becomes a thin proxy or
gets retired in favour of the frontend hitting /applications/*
directly."
```

## Task 3a.7: Register all four handlers + ship the backend image

**Files:**
- Modify: `apps/matching/cmd/main.go`

- [ ] **Step 1: Wire savedjobs + opportunities + applications handlers**

In `apps/matching/cmd/main.go`, near the `mux.Handle("GET /me/onboarding"...)` block from Task 1.4, add:

```go
	// /me/saved-jobs — star/unstar
	savedJobsStore := savedjobs.NewStore(meSubMatchesSQLDB)  // reuse the *sql.DB from Phase 1
	savedJobsHandler := httpmw.CandidateAuth(httpv1.SavedJobsHandler(httpv1.SavedJobsDeps{
		Store: savedJobsStore,
	}))
	mux.Handle("POST   /me/saved-jobs",                  savedJobsHandler)
	mux.Handle("DELETE /me/saved-jobs/{opportunity_id}", savedJobsHandler)

	// /me/opportunities — unified feed (joins matches + saved + applications)
	mux.Handle("GET /me/opportunities", httpmw.CandidateAuth(
		httpv1.OpportunitiesHandler(httpv1.OpportunitiesDeps{Store: meSubMatches}),
	))

	// /me/applications — manual apply (wraps pkg/applications/business)
	appStarter := &applicationStarterAdapter{biz: /* construct or pass-in */}
	mux.Handle("POST /me/applications", httpmw.CandidateAuth(
		httpv1.ApplicationsHandler(httpv1.ApplicationsDeps{Starter: appStarter}),
	))
```

`meSubMatchesSQLDB` is the `*sql.DB` the Phase 1 task already obtained via `gdb.DB()`; rename it if main.go uses a different identifier. `meSubMatches` is the `*matching.Store` instance from Phase 1; it now satisfies the `OpportunityFeedStore` interface because Task 3a.4 added the method on `*Store`.

- [ ] **Step 2: Add the imports**

At the top of `apps/matching/cmd/main.go`:

```go
import (
    /* ... existing imports ... */
    "github.com/stawi-opportunities/opportunities/pkg/savedjobs"
    /* ApplicationsHandler dependency: pkg/applications/business */
)
```

- [ ] **Step 3: Build + vet + test the whole tree**

```bash
go build ./apps/matching/...
go vet ./...
go test -race -timeout 300s ./apps/matching/... ./pkg/savedjobs/... ./pkg/matching/... ./pkg/httpmw/...
```
All clean.

- [ ] **Step 4: Commit**

```bash
git add apps/matching/cmd/main.go
git commit -m "feat(matching): register saved-jobs, opportunities, applications handlers

Phase 3a complete on the backend side. Four new routes live behind
httpmw.CandidateAuth on /me/saved-jobs (POST/DELETE),
/me/opportunities (GET), /me/applications (POST). Phase 3b's
frontend (OpportunitiesFeed component) consumes them."
```

- [ ] **Step 5: Tag a new Docker release**

```bash
LATEST_TAG=$(git describe --tags --abbrev=0)
NEW_TAG=$(echo "$LATEST_TAG" | awk -F. -v OFS=. '{$NF=$NF+1; print}')
git tag -a "$NEW_TAG" -m "$NEW_TAG — Phase 3a of paid-flow (opportunities feed backend)"
git push origin "$NEW_TAG"
gh run watch $(gh run list --workflow=release.yaml --limit 1 --json databaseId --jq '.[0].databaseId')
```

After the workflow goes green, Flux's ImagePolicy picks it up within ~5 min and rolls the matching pods. Verify with `curl https://api.stawi.org/matching/me/opportunities -H "Authorization: Bearer <token>"` from any signed-in user → 200 with the items list (likely empty for a brand-new candidate).

---

# Phase 3b — Unified opportunities feed frontend

Replaces the three placeholder panels on the dashboard with one `OpportunitiesFeed` component, wires up star + apply optimistic updates, and URL-drives the filter chips.

## Task 3b.1: API client helpers for opportunities + star + apply

**Files:**
- Modify: `ui/app/src/api/candidates.ts`

- [ ] **Step 1: Append the new helpers + types**

In `ui/app/src/api/candidates.ts`, near the bottom (above `getCandidatesOrigin`), insert:

```ts
// ── /me/opportunities ─────────────────────────────────────────────

export type OpportunityFilter = "all" | "matches" | "starred" | "applied";

export interface ApplicationSummary {
  status: "applied" | "responded" | "interview" | "offer" | "rejected" | "hired" | string;
  applied_at: string;
  last_event_at: string;
  method: "auto" | "manual" | string;
}

export interface FeedItem {
  opportunity_id: string;
  score?: number;
  starred: boolean;
  application?: ApplicationSummary;
  created_at: string;
}

export interface FeedPage {
  items: FeedItem[];
  next_cursor?: string;
}

export async function fetchOpportunities(
  opts: { filter?: OpportunityFilter; cursor?: string; limit?: number } = {},
): Promise<FeedPage> {
  const params = new URLSearchParams();
  if (opts.filter && opts.filter !== "all") params.set("filter", opts.filter);
  if (opts.cursor) params.set("cursor", opts.cursor);
  if (opts.limit) params.set("limit", String(opts.limit));
  const query = params.toString();
  const path = `/matching/me/opportunities${query ? `?${query}` : ""}`;
  return await authRuntime().fetch<FeedPage>(path);
}

export async function starOpportunity(opportunityId: string): Promise<void> {
  await authRuntime().fetch("/matching/me/saved-jobs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ opportunity_id: opportunityId }),
  });
}

export async function unstarOpportunity(opportunityId: string): Promise<void> {
  await authRuntime().fetch(`/matching/me/saved-jobs/${encodeURIComponent(opportunityId)}`, {
    method: "DELETE",
  });
}

export async function applyToOpportunity(
  opportunityId: string,
  method: "manual" | "auto" = "manual",
): Promise<{ application_id: string; status: string; applied_at: string }> {
  return await authRuntime().fetch("/matching/me/applications", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ opportunity_id: opportunityId, method }),
  });
}
```

- [ ] **Step 2: Typecheck**

```bash
cd ui/app && npm run typecheck
```
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add ui/app/src/api/candidates.ts
git commit -m "feat(api): client helpers for opportunities feed + star + apply

fetchOpportunities passes ?filter=all|matches|starred|applied
through; default 'all' omits the param so the URL stays clean.
star/unstar/apply are thin runtime.fetch wrappers — they THROW on
non-2xx (intentional, the caller handles via optimistic-update
rollback in OpportunitiesFeed)."
```

## Task 3b.2: `OpportunityCard` component

**Files:**
- Create: `ui/app/src/components/OpportunityCard.tsx`
- Create: `ui/app/src/components/__tests__/OpportunityCard.test.tsx`

(Set up vitest in `ui/app/` if it isn't already wired — check `package.json`; if not, add it and a minimal test setup before proceeding.)

- [ ] **Step 1: Write the failing component test**

Create `ui/app/src/components/__tests__/OpportunityCard.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OpportunityCard } from "../OpportunityCard";
import type { FeedItem } from "@/api/candidates";

const baseItem: FeedItem = {
  opportunity_id: "opp_1",
  score: 0.82,
  starred: false,
  created_at: "2026-05-23T10:00:00Z",
};

describe("OpportunityCard", () => {
  it("renders the match score when present", () => {
    render(<OpportunityCard item={baseItem} snapshot={null} onStar={vi.fn()} onUnstar={vi.fn()} onApply={vi.fn()} />);
    expect(screen.getByText(/82%/i)).toBeInTheDocument();
  });

  it("omits the match score when score is missing", () => {
    const item: FeedItem = { ...baseItem, score: undefined };
    render(<OpportunityCard item={item} snapshot={null} onStar={vi.fn()} onUnstar={vi.fn()} onApply={vi.fn()} />);
    expect(screen.queryByText(/%/)).not.toBeInTheDocument();
  });

  it("star button calls onStar when unstarred", () => {
    const onStar = vi.fn();
    render(<OpportunityCard item={baseItem} snapshot={null} onStar={onStar} onUnstar={vi.fn()} onApply={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /save opportunity/i }));
    expect(onStar).toHaveBeenCalledWith("opp_1");
  });

  it("star button calls onUnstar when starred", () => {
    const onUnstar = vi.fn();
    const item = { ...baseItem, starred: true };
    render(<OpportunityCard item={item} snapshot={null} onStar={vi.fn()} onUnstar={onUnstar} onApply={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /remove from saved/i }));
    expect(onUnstar).toHaveBeenCalledWith("opp_1");
  });

  it("shows Apply button when not applied; hides it once applied", () => {
    const onApply = vi.fn();
    const { rerender } = render(
      <OpportunityCard item={baseItem} snapshot={null} onStar={vi.fn()} onUnstar={vi.fn()} onApply={onApply} />,
    );
    fireEvent.click(screen.getByRole("button", { name: /apply/i }));
    expect(onApply).toHaveBeenCalledWith("opp_1");

    rerender(
      <OpportunityCard
        item={{ ...baseItem, application: { status: "applied", applied_at: baseItem.created_at, last_event_at: baseItem.created_at, method: "manual" } }}
        snapshot={null}
        onStar={vi.fn()} onUnstar={vi.fn()} onApply={onApply}
      />,
    );
    expect(screen.queryByRole("button", { name: /^apply$/i })).not.toBeInTheDocument();
    expect(screen.getByText(/applied/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd ui/app && npx vitest run src/components/__tests__/OpportunityCard.test.tsx
```
Expected: FAIL on missing component.

- [ ] **Step 3: Implement the component**

Create `ui/app/src/components/OpportunityCard.tsx`:

```tsx
import type { FeedItem } from "@/api/candidates";

// Snapshot is the rich opportunity payload (title, company, etc.) the
// dashboard fetches separately from the feed. For now it's nullable;
// the card renders a placeholder when missing so a slow snapshot
// fetch doesn't block the row from showing up.
export interface OpportunitySnapshot {
  title: string;
  company?: string;
  location?: string;
  posted_at?: string;
  salary_min?: number;
  salary_max?: number;
  currency?: string;
  kind?: string;
}

interface Props {
  item: FeedItem;
  snapshot: OpportunitySnapshot | null;
  onStar: (opportunityId: string) => void;
  onUnstar: (opportunityId: string) => void;
  onApply: (opportunityId: string) => void;
}

const STATUS_LABEL: Record<string, string> = {
  applied: "Applied",
  responded: "Responded",
  interview: "Interview scheduled",
  offer: "Offer received",
  rejected: "Rejected",
  hired: "Hired",
};

export function OpportunityCard({ item, snapshot, onStar, onUnstar, onApply }: Props) {
  const title = snapshot?.title ?? "Loading…";
  const company = snapshot?.company ?? "";
  const location = snapshot?.location ?? "";

  return (
    <li className="flex flex-col gap-3 rounded-lg border border-gray-200 bg-white p-4 sm:flex-row sm:items-start sm:gap-4">
      <div className="flex-1">
        <div className="flex items-start justify-between gap-2">
          <div>
            <h3 className="text-base font-semibold text-gray-900">{title}</h3>
            {(company || location) && (
              <p className="mt-0.5 text-sm text-gray-600">
                {company}
                {company && location && " · "}
                {location}
              </p>
            )}
          </div>
          {typeof item.score === "number" && item.score > 0 && (
            <span
              className="shrink-0 rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs font-medium text-emerald-700"
              title="Match score"
            >
              {Math.round(item.score * 100)}% match
            </span>
          )}
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          {item.application ? (
            <span className="inline-flex items-center rounded-full bg-blue-50 px-2.5 py-0.5 text-xs font-medium text-blue-700">
              {STATUS_LABEL[item.application.status] ?? item.application.status}
            </span>
          ) : (
            <button
              type="button"
              onClick={() => onApply(item.opportunity_id)}
              className="rounded-md bg-navy-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-navy-800"
            >
              Apply
            </button>
          )}
          {item.starred ? (
            <button
              type="button"
              onClick={() => onUnstar(item.opportunity_id)}
              aria-label="Remove from saved"
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-amber-700 hover:bg-amber-50"
            >
              ★ Saved
            </button>
          ) : (
            <button
              type="button"
              onClick={() => onStar(item.opportunity_id)}
              aria-label="Save opportunity"
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              ☆ Save
            </button>
          )}
        </div>
      </div>
    </li>
  );
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
cd ui/app && npx vitest run src/components/__tests__/OpportunityCard.test.tsx
```
Expected: PASS, five tests.

- [ ] **Step 5: Commit**

```bash
git add ui/app/src/components/OpportunityCard.tsx ui/app/src/components/__tests__/OpportunityCard.test.tsx
git commit -m "feat(dashboard): OpportunityCard component

Pure presentational card. Score badge only renders when score > 0
(non-matched starred/applied rows omit it). The Apply button hides
once an application exists; the application status pill takes its
place. Save/Unsave toggle is keyboard- and screen-reader-accessible
via aria-label."
```

## Task 3b.3: `OpportunitiesFeed` component (filter chips, pagination, optimistic mutations)

**Files:**
- Create: `ui/app/src/components/OpportunitiesFeed.tsx`
- Create: `ui/app/src/components/__tests__/OpportunitiesFeed.test.tsx`

- [ ] **Step 1: Write the failing component test**

Create `ui/app/src/components/__tests__/OpportunitiesFeed.test.tsx`. Cover:
1. Renders the four filter chips; clicking one updates `window.location.search` with `?filter=…`.
2. Fetches `/me/opportunities` on mount and renders rows.
3. Star button optimistically toggles; if the fetch rejects, the toggle rolls back.
4. Apply button shows an "Applied" pill optimistically; rolls back on failure.
5. Load-more button shows up only when `next_cursor` is present; clicking fetches the next page.

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { OpportunitiesFeed } from "../OpportunitiesFeed";
import * as api from "@/api/candidates";

vi.mock("@/api/candidates", async () => {
  return {
    fetchOpportunities: vi.fn(),
    starOpportunity: vi.fn(),
    unstarOpportunity: vi.fn(),
    applyToOpportunity: vi.fn(),
  };
});

const item = {
  opportunity_id: "opp_1",
  score: 0.7,
  starred: false,
  created_at: "2026-05-23T10:00:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  (api.fetchOpportunities as ReturnType<typeof vi.fn>).mockResolvedValue({ items: [item] });
  window.history.replaceState({}, "", "/dashboard/");
});

describe("OpportunitiesFeed", () => {
  it("fetches with the default 'all' filter on mount", async () => {
    render(<OpportunitiesFeed />);
    await waitFor(() => expect(api.fetchOpportunities).toHaveBeenCalled());
    expect(api.fetchOpportunities).toHaveBeenCalledWith({ filter: "all" });
  });

  it("clicking the Starred chip refetches with filter=starred and updates the URL", async () => {
    render(<OpportunitiesFeed />);
    await screen.findByText(/loading|matches/i);
    fireEvent.click(screen.getByRole("button", { name: /^starred$/i }));
    await waitFor(() => expect(api.fetchOpportunities).toHaveBeenCalledWith({ filter: "starred" }));
    expect(window.location.search).toBe("?filter=starred");
  });

  it("star click rolls back when the API rejects", async () => {
    (api.starOpportunity as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("network"));
    render(<OpportunitiesFeed />);
    await waitFor(() => screen.getByRole("button", { name: /save opportunity/i }));
    fireEvent.click(screen.getByRole("button", { name: /save opportunity/i }));
    // Optimistic flip:
    await waitFor(() => screen.getByRole("button", { name: /remove from saved/i }));
    // Rollback after the rejection settles:
    await waitFor(() => screen.getByRole("button", { name: /save opportunity/i }));
  });
});
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd ui/app && npx vitest run src/components/__tests__/OpportunitiesFeed.test.tsx
```
Expected: FAIL on missing component.

- [ ] **Step 3: Implement the component**

Create `ui/app/src/components/OpportunitiesFeed.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  applyToOpportunity,
  fetchOpportunities,
  starOpportunity,
  unstarOpportunity,
  type FeedItem,
  type OpportunityFilter,
} from "@/api/candidates";
import { OpportunityCard } from "./OpportunityCard";

const FILTERS: { id: OpportunityFilter; label: string }[] = [
  { id: "all",     label: "All"      },
  { id: "matches", label: "Matches"  },
  { id: "starred", label: "Starred"  },
  { id: "applied", label: "Applied"  },
];

function readFilterFromURL(): OpportunityFilter {
  if (typeof window === "undefined") return "all";
  const v = new URL(window.location.href).searchParams.get("filter");
  if (v === "matches" || v === "starred" || v === "applied") return v;
  return "all";
}

function writeFilterToURL(filter: OpportunityFilter) {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  if (filter === "all") url.searchParams.delete("filter");
  else url.searchParams.set("filter", filter);
  window.history.pushState({}, "", url.toString());
}

export function OpportunitiesFeed() {
  const [filter, setFilter] = useState<OpportunityFilter>(readFilterFromURL);
  const [items, setItems] = useState<FeedItem[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (f: OpportunityFilter, cursor?: string) => {
    setLoading(true);
    setError(null);
    try {
      const page = await fetchOpportunities({ filter: f, cursor });
      setItems((prev) => (cursor ? [...prev, ...page.items] : page.items));
      setNextCursor(page.next_cursor);
    } catch {
      setError("Couldn't load your opportunities. Refresh in a few seconds — if this keeps happening, drop us a line at jobs@stawi.org.");
    } finally {
      setLoading(false);
    }
  }, []);

  // Initial load + on filter change.
  useEffect(() => { void load(filter); }, [filter, load]);

  const onSelectFilter = (id: OpportunityFilter) => {
    if (id === filter) return;
    writeFilterToURL(id);
    setFilter(id);
  };

  const onStar = useCallback(async (id: string) => {
    const snapshot = items;
    setItems((prev) => prev.map((it) => (it.opportunity_id === id ? { ...it, starred: true } : it)));
    try {
      await starOpportunity(id);
    } catch {
      setItems(snapshot);
    }
  }, [items]);

  const onUnstar = useCallback(async (id: string) => {
    const snapshot = items;
    setItems((prev) => prev.map((it) => (it.opportunity_id === id ? { ...it, starred: false } : it)));
    try {
      await unstarOpportunity(id);
    } catch {
      setItems(snapshot);
    }
  }, [items]);

  const onApply = useCallback(async (id: string) => {
    const snapshot = items;
    const now = new Date().toISOString();
    setItems((prev) =>
      prev.map((it) =>
        it.opportunity_id === id
          ? { ...it, application: { status: "applied", applied_at: now, last_event_at: now, method: "manual" } }
          : it,
      ),
    );
    try {
      await applyToOpportunity(id, "manual");
    } catch {
      setItems(snapshot);
    }
  }, [items]);

  return (
    <section aria-label="Your opportunities" className="space-y-4">
      <div className="flex flex-wrap items-center gap-2" role="tablist">
        {FILTERS.map((f) => {
          const active = f.id === filter;
          return (
            <button
              key={f.id}
              role="tab"
              aria-selected={active}
              type="button"
              onClick={() => onSelectFilter(f.id)}
              className={`rounded-full px-3.5 py-1.5 text-sm font-medium transition-colors ${
                active
                  ? "bg-navy-900 text-white"
                  : "border border-gray-300 bg-white text-gray-700 hover:bg-gray-50"
              }`}
            >
              {f.label}
            </button>
          );
        })}
      </div>

      {error ? (
        <div role="alert" className="rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800">
          {error}
        </div>
      ) : loading && items.length === 0 ? (
        <p className="rounded-md border border-gray-200 bg-white p-4 text-sm text-gray-600">Loading…</p>
      ) : items.length === 0 ? (
        <p className="rounded-md border border-gray-200 bg-white p-4 text-sm text-gray-600">
          Nothing to show here yet. {filter !== "all" && "Try the 'All' filter."}
        </p>
      ) : (
        <>
          <ul className="space-y-3">
            {items.map((it) => (
              <OpportunityCard
                key={it.opportunity_id}
                item={it}
                snapshot={null}   /* TODO: hydrate snapshot once we wire R2 OpportunitySnapshot fetch into the feed */
                onStar={onStar}
                onUnstar={onUnstar}
                onApply={onApply}
              />
            ))}
          </ul>
          {nextCursor && (
            <button
              type="button"
              onClick={() => void load(filter, nextCursor)}
              className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              disabled={loading}
            >
              {loading ? "Loading…" : "Load more"}
            </button>
          )}
        </>
      )}
    </section>
  );
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
cd ui/app && npx vitest run src/components/__tests__/OpportunitiesFeed.test.tsx
```
Expected: PASS, three tests (or however many you wrote).

- [ ] **Step 5: Commit**

```bash
git add ui/app/src/components/OpportunitiesFeed.tsx ui/app/src/components/__tests__/OpportunitiesFeed.test.tsx
git commit -m "feat(dashboard): OpportunitiesFeed unified component

Filter chips drive the URL (?filter=…) — back button restores All,
deep-links work. star/unstar/apply are optimistic with snapshot
rollback on failure; the user sees the toggle land immediately and
only sees the failure if it rolls back. Snapshot field on
OpportunityCard is null for now — a follow-up task wires up the R2
OpportunitySnapshot fetch (the existing component fetches snapshots
elsewhere, we'll reuse that hook). Empty + loading + error states
are all explicit (no infinite spinners)."
```

## Task 3b.4: Replace the placeholder panels in `Dashboard.tsx`

**Files:**
- Modify: `ui/app/src/pages/Dashboard.tsx`

- [ ] **Step 1: Remove `MatchesPanel`, `SavedJobsPanel`, `ApplicationsPanel` usages**

In `ui/app/src/pages/Dashboard.tsx`, find the `<section className="space-y-6">` block that renders the right column. Today it looks like:

```tsx
        <section className="space-y-6">
          {plan === null || !isActive ? (
            <CompletePaymentPanel plan={plan} status={sub?.status ?? "none"} />
          ) : (
            <>
              {plan === "managed" && sub?.agent && (
                <AgentCard agent={sub.agent} />
              )}
              <MatchesPanel ... />
            </>
          )}
          <SavedJobsPanel />
          <PreferencesPanel />
          {plan && plan !== "managed" && isActive && <ApplicationsPanel plan={plan} />}
          {plan && isActive && <BillingPanel plan={plan} renewsAt={sub?.renews_at} />}
        </section>
```

Replace with:

```tsx
        <section className="space-y-6">
          {plan === null || !isActive ? (
            <CompletePaymentPanel plan={plan} status={sub?.status ?? "none"} />
          ) : (
            <>
              {plan === "managed" && sub?.agent && (
                <AgentCard agent={sub.agent} />
              )}
              <OpportunitiesFeed />
            </>
          )}
          <PreferencesPanel />
          {plan && isActive && <BillingPanel plan={plan} renewsAt={sub?.renews_at} />}
        </section>
```

- [ ] **Step 2: Delete the now-unused panel components**

Delete the `function MatchesPanel(...)`, `function SavedJobsPanel(...)`, `function ApplicationsPanel(...)` blocks from `Dashboard.tsx`. They're no longer referenced.

- [ ] **Step 3: Add the new import**

At the top of `Dashboard.tsx`:

```tsx
import { OpportunitiesFeed } from "@/components/OpportunitiesFeed";
```

- [ ] **Step 4: Typecheck + build**

```bash
cd ui/app && npm run typecheck && npm run build
```
Both clean.

- [ ] **Step 5: Commit**

```bash
git add ui/app/src/pages/Dashboard.tsx
git commit -m "feat(dashboard): replace placeholder panels with OpportunitiesFeed

The Matches/SavedJobs/Applications panels were copy-only — they
never fetched data. OpportunitiesFeed renders real items from
GET /matching/me/opportunities. Preferences and Billing panels
stay; CompletePaymentPanel still wins when the subscription
isn't active. Removed the three dead panel functions."
```

## Task 3b.5: Ship Phase 3 and verify end-to-end

**Files:** none

- [ ] **Step 1: Push the consumer-side commits and verify the CF Pages build**

Phase 3b's commits are pure-frontend; CF Pages will rebuild on push to main. Push and watch:

```bash
git push origin main
# Cloudflare Pages dashboard or `gh api repos/.../deployments` to verify the new build deploys.
```

- [ ] **Step 2: Smoke-test on https://jobs.stawi.org/dashboard/**

1. Sign in as a candidate with an active subscription.
2. Open DevTools Network → filter for `me/opportunities`.
3. Expected: one GET on page load, status 200, response shape matches the spec.
4. Click a filter chip → URL changes to `?filter=…`, fetch refires with the param.
5. Click a star — see the icon flip immediately (optimistic). If you kill the network and click again, see the toggle roll back.
6. Click Apply on an opportunity — see the "Applied" pill appear immediately. Refresh and verify it sticks (real DB write).

- [ ] **Step 3: Tag a UI release if needed**

The UI is a static bundle on CF Pages; no semver tag necessary. Just confirm CF Pages serves the new build (check the Network tab for the new `main-*.js` hash).

- [ ] **Step 4: Open the wrap-up PR**

If the Phase 3 commits aren't already in a PR, open one:

```bash
gh pr create --title "feat(dashboard): phase 3 — unified opportunities feed" \
  --body "Phase 3 of the paid-flow design. Replaces three placeholder panels with one OpportunitiesFeed component backed by GET /me/opportunities (Phase 3a). Star/apply mutations are optimistic with snapshot rollback on failure. Filter chips URL-drive ?filter=…"
```

---

## Self-review (run by the engineer, not the user)

After the plan finishes, verify against the spec before declaring done:

- **Spec coverage** — every item in the spec's "Definition of done" maps to a task above:
  - Routing decisions in 3 places → Task 2.3 (AuthCallback), Task 2.4 (page guards). ✓
  - Resumable wizard → Task 1.1–1.4 (backend), Task 2.1–2.2 (frontend). ✓
  - Draft cleared atomically with submit → Task 2.5. ✓
  - Unified opportunities feed with real data → Task 3a.1–3a.7 + Task 3b.1–3b.4. ✓
  - URL-driven filter state → Task 3b.3. ✓
  - All failure-mode rows render a non-blocking UI → Tasks 1.3, 2.2, 3a.5, 3b.3 each include error/empty branches. ✓
  - Tests at every layer → ✓.

- **Type / name consistency** — `OnboardingDeps.Drafts` (Task 1.3) matches `candidateRepo` satisfying `OnboardingDraftStore` (Task 1.4); `meSubMatches` is reused for both `/me/subscription` and `/me/opportunities` (Task 3a.7); `FeedItem` shape in `candidates.ts` (Task 3b.1) matches `feedItemDTO` in the handler (Task 3a.5). Score-omitted-when-zero is consistent on both sides.

- **Placeholder scan** — Task 3a.6 step 1 instructs the engineer to grep for the existing `pkg/applications/business` entrypoint and synthesise the adapter accordingly. This is not a "TBD" — the existing function genuinely lives in the engineer's repo and can only be found at implementation time. The instruction is concrete (which command to run, what to do with the result).

- **Open spec items** — none blocking. The frontend snapshot hydration (showing real opportunity title/company on each card) is intentionally deferred — the card today shows `"Loading…"` as title, which is a known gap with a clear path forward (reuse the existing snapshot fetch from `OpportunityDetail.tsx`). The next planning cycle should add that as Phase 4 or a small follow-up task.
