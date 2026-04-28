package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// fakeFlagRepo is an in-memory flagAdminRepo for handler tests.
type fakeFlagRepo struct {
	mu   sync.Mutex
	rows map[string]*domain.OpportunityFlag
	seq  int
}

func newFakeFlagRepo() *fakeFlagRepo {
	return &fakeFlagRepo{rows: map[string]*domain.OpportunityFlag{}}
}

func (f *fakeFlagRepo) Create(_ context.Context, fl *domain.OpportunityFlag) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.OpportunitySlug == fl.OpportunitySlug && r.SubmittedBy == fl.SubmittedBy && r.ResolvedAt == nil {
			return errFakeDup
		}
	}
	f.seq++
	if fl.ID == "" {
		fl.ID = "flag-" + strconvI(f.seq)
	}
	if fl.CreatedAt.IsZero() {
		fl.CreatedAt = time.Now().UTC()
	}
	f.rows[fl.ID] = fl
	return nil
}

func (f *fakeFlagRepo) GetByID(_ context.Context, id string) (*domain.OpportunityFlag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeFlagRepo) ExistsForUser(_ context.Context, slug, by string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.OpportunitySlug == slug && r.SubmittedBy == by {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeFlagRepo) ListByOpportunity(_ context.Context, slug string) ([]*domain.OpportunityFlag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*domain.OpportunityFlag{}
	for _, r := range f.rows {
		if r.OpportunitySlug == slug {
			cp := *r
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeFlagRepo) ListUnresolved(_ context.Context, reason domain.FlagReason, slug string, _ int, _ int) ([]*domain.OpportunityFlag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*domain.OpportunityFlag{}
	for _, r := range f.rows {
		if r.ResolvedAt != nil {
			continue
		}
		if reason != "" && r.Reason != reason {
			continue
		}
		if slug != "" && r.OpportunitySlug != slug {
			continue
		}
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeFlagRepo) CountUnresolvedByOpportunity(_ context.Context, slug string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.rows {
		if r.OpportunitySlug == slug && r.Reason == domain.FlagScam && r.ResolvedAt == nil {
			n++
		}
	}
	return n, nil
}

func (f *fakeFlagRepo) Resolve(_ context.Context, id, by string, action domain.FlagResolutionAction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	r.ResolvedAt = &now
	r.ResolvedBy = by
	r.ResolutionAction = string(action)
	return nil
}

func (f *fakeFlagRepo) ResolveAllForOpportunity(_ context.Context, slug, by string, action domain.FlagResolutionAction) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	var n int64
	for _, r := range f.rows {
		if r.OpportunitySlug == slug && r.ResolvedAt == nil {
			r.ResolvedAt = &now
			r.ResolvedBy = by
			r.ResolutionAction = string(action)
			n++
		}
	}
	return n, nil
}

func (f *fakeFlagRepo) TopFlagged(_ context.Context, _ int) ([]repository.TopFlaggedRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]*repository.TopFlaggedRow{}
	for _, r := range f.rows {
		if r.ResolvedAt != nil {
			continue
		}
		key := r.OpportunitySlug + "|" + r.OpportunityKind
		row, ok := counts[key]
		if !ok {
			row = &repository.TopFlaggedRow{
				OpportunitySlug: r.OpportunitySlug,
				Kind:            r.OpportunityKind,
			}
			counts[key] = row
		}
		row.FlagCount++
	}
	out := make([]repository.TopFlaggedRow, 0, len(counts))
	for _, row := range counts {
		out = append(out, *row)
	}
	return out, nil
}

// errFakeDup mimics the unique-constraint violation isUniqueViolation
// recognises in the production handler.
var errFakeDup = &uniqueViolationError{}

type uniqueViolationError struct{}

func (uniqueViolationError) Error() string { return "duplicate key value violates unique constraint" }

// fakeJobsLookup serves a single canned slug → job row for the kind
// resolution path.
type fakeJobsLookup struct{ rows map[string]*job }

func (f *fakeJobsLookup) GetByID(_ context.Context, id string) (*job, error) {
	if j, ok := f.rows[id]; ok {
		return j, nil
	}
	return nil, nil
}

// fakeEmitter records every emitted event for assertion.
type fakeEmitter struct {
	mu    sync.Mutex
	calls []struct {
		topic   string
		payload any
	}
	err error
}

func (f *fakeEmitter) Emit(_ context.Context, topic string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct {
		topic   string
		payload any
	}{topic: topic, payload: payload})
	return f.err
}

// flagsTestHarness wires a flagsAdmin with fakes. The source-admin
// fake from sources_admin_test.go is reused for the ban_source path.
func flagsTestHarness(t *testing.T) (*flagsAdmin, *fakeFlagRepo, *fakeSourceRepo, *fakeJobsLookup, *fakeEmitter, *http.ServeMux) {
	t.Helper()
	repo := newFakeFlagRepo()
	srcRepo := newFakeSourceRepo()
	jobs := &fakeJobsLookup{rows: map[string]*job{}}
	emitter := &fakeEmitter{}

	a := &flagsAdmin{
		repo:       repo,
		sourceRepo: srcRepo,
		jobs:       jobs,
		emitter:    emitter,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /opportunities/{slug}/flag", a.handleUserFlag)
	mux.HandleFunc("GET /admin/flags", requireAdmin(a.handleListUnresolved))
	mux.HandleFunc("GET /admin/flags/{id}", requireAdmin(a.handleGetFlag))
	mux.HandleFunc("POST /admin/flags/{id}/resolve", requireAdmin(a.handleResolve))
	mux.HandleFunc("GET /admin/opportunities/{slug}/flags", requireAdmin(a.handleListForOpportunity))
	mux.HandleFunc("GET /admin/opportunities/top-flagged", requireAdmin(a.handleTopFlagged))
	return a, repo, srcRepo, jobs, emitter, mux
}

// fakeJWT builds a Bearer token with the given sub claim. Header /
// signature are unverified by the api-side middleware so we only
// need a well-formed three-segment payload.
func fakeJWT(sub string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"` + sub + `"}`))
	return "Bearer " + header + "." + body + ".x"
}

func TestFlags_UserFlag_RequiresAuth(t *testing.T) {
	_, _, _, _, _, mux := flagsTestHarness(t)
	body := `{"reason":"scam","description":"phishing"}`
	req := httptest.NewRequest("POST", "/opportunities/abc/flag", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestFlags_UserFlag_HappyPath(t *testing.T) {
	_, repo, _, jobs, _, mux := flagsTestHarness(t)
	jobs.rows["abc"] = &job{Slug: "abc", Kind: "job", CanonicalID: "c1"}

	body := `{"reason":"scam","description":"this is a phishing site"}`
	req := httptest.NewRequest("POST", "/opportunities/abc/flag", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", fakeJWT("user-1"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(repo.rows) != 1 {
		t.Errorf("expected 1 row in repo, got %d", len(repo.rows))
	}
	for _, r := range repo.rows {
		if r.OpportunityKind != "job" {
			t.Errorf("kind=%q want job", r.OpportunityKind)
		}
		if r.SubmittedBy != "user-1" {
			t.Errorf("submitted_by=%q want user-1", r.SubmittedBy)
		}
	}
}

func TestFlags_UserFlag_DuplicateReturns409(t *testing.T) {
	_, _, _, _, _, mux := flagsTestHarness(t)
	body := `{"reason":"scam","description":"first"}`
	for i, want := range []int{http.StatusCreated, http.StatusConflict} {
		req := httptest.NewRequest("POST", "/opportunities/abc/flag", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", fakeJWT("user-1"))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != want {
			t.Fatalf("attempt %d: got %d want %d body=%s", i+1, rr.Code, want, rr.Body.String())
		}
	}
}

func TestFlags_UserFlag_InvalidReason(t *testing.T) {
	_, _, _, _, _, mux := flagsTestHarness(t)
	body := `{"reason":"not-a-reason"}`
	req := httptest.NewRequest("POST", "/opportunities/abc/flag", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", fakeJWT("user-1"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestFlags_UserFlag_DescriptionTooLong(t *testing.T) {
	_, _, _, _, _, mux := flagsTestHarness(t)
	desc := make([]byte, domain.FlagDescriptionMaxLen+1)
	for i := range desc {
		desc[i] = 'a'
	}
	payload := map[string]any{"reason": "spam", "description": string(desc)}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/opportunities/abc/flag", bytes.NewReader(raw))
	req.Header.Set("Authorization", fakeJWT("user-1"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestFlags_UserFlag_ThresholdEmitsAutoFlag(t *testing.T) {
	_, _, _, _, emitter, mux := flagsTestHarness(t)
	users := []string{"u1", "u2", "u3"}
	for i, u := range users {
		body := `{"reason":"scam","description":"bad"}`
		req := httptest.NewRequest("POST", "/opportunities/abc/flag", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", fakeJWT(u))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("user %d: expected 201, got %d", i, rr.Code)
		}
	}
	// First two flags must NOT trigger emit; the third must.
	if got := len(emitter.calls); got != 1 {
		t.Errorf("expected exactly 1 auto-flag emit, got %d", got)
	}
	if len(emitter.calls) > 0 && emitter.calls[0].topic == "" {
		t.Errorf("emitted topic was empty")
	}
}

func TestFlags_AdminList_RequiresAuth(t *testing.T) {
	_, _, _, _, _, mux := flagsTestHarness(t)
	req := httptest.NewRequest("GET", "/admin/flags", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestFlags_AdminList_FiltersByReason(t *testing.T) {
	_, repo, _, _, _, mux := flagsTestHarness(t)
	repo.rows["f1"] = &domain.OpportunityFlag{
		BaseModel:       domain.BaseModel{ID: "f1"},
		OpportunitySlug: "a", Reason: domain.FlagScam, SubmittedBy: "u1",
	}
	repo.rows["f2"] = &domain.OpportunityFlag{
		BaseModel:       domain.BaseModel{ID: "f2"},
		OpportunitySlug: "b", Reason: domain.FlagSpam, SubmittedBy: "u2",
	}
	req := httptest.NewRequest("GET", "/admin/flags?reason=scam", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Flags []*domain.OpportunityFlag `json:"flags"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Flags) != 1 || resp.Flags[0].ID != "f1" {
		t.Errorf("unexpected flags: %+v", resp.Flags)
	}
}

func TestFlags_AdminGet_NotFound(t *testing.T) {
	_, _, _, _, _, mux := flagsTestHarness(t)
	req := httptest.NewRequest("GET", "/admin/flags/missing", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestFlags_AdminResolve_Ignore(t *testing.T) {
	_, repo, _, _, _, mux := flagsTestHarness(t)
	repo.rows["f1"] = &domain.OpportunityFlag{
		BaseModel:       domain.BaseModel{ID: "f1"},
		OpportunitySlug: "a", Reason: domain.FlagScam, SubmittedBy: "u1",
	}
	body := `{"action":"ignore","note":"false alarm"}`
	req := httptest.NewRequest("POST", "/admin/flags/f1/resolve", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if repo.rows["f1"].ResolvedAt == nil {
		t.Errorf("flag was not marked resolved")
	}
	if repo.rows["f1"].ResolutionAction != string(domain.FlagActionIgnore) {
		t.Errorf("action=%q want ignore", repo.rows["f1"].ResolutionAction)
	}
}

func TestFlags_AdminResolve_BanSource(t *testing.T) {
	_, repo, srcRepo, _, _, mux := flagsTestHarness(t)
	srcRepo.rows["src-1"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "src-1"},
		Status:    domain.SourceActive,
	}
	repo.rows["f1"] = &domain.OpportunityFlag{
		BaseModel:       domain.BaseModel{ID: "f1"},
		OpportunitySlug: "abc", Reason: domain.FlagScam, SubmittedBy: "u1",
	}
	repo.rows["f2"] = &domain.OpportunityFlag{
		BaseModel:       domain.BaseModel{ID: "f2"},
		OpportunitySlug: "abc", Reason: domain.FlagScam, SubmittedBy: "u2",
	}
	body := `{"action":"ban_source","source_id":"src-1"}`
	req := httptest.NewRequest("POST", "/admin/flags/f1/resolve", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if srcRepo.rows["src-1"].Status != domain.SourceDisabled {
		t.Errorf("source not stopped: %q", srcRepo.rows["src-1"].Status)
	}
	// All flags on the slug should be resolved.
	for id, r := range repo.rows {
		if r.OpportunitySlug == "abc" && r.ResolvedAt == nil {
			t.Errorf("flag %s was not resolved", id)
		}
	}
}

func TestFlags_TopFlagged(t *testing.T) {
	_, repo, _, _, _, mux := flagsTestHarness(t)
	repo.rows["f1"] = &domain.OpportunityFlag{
		BaseModel:       domain.BaseModel{ID: "f1"},
		OpportunitySlug: "a", OpportunityKind: "job", Reason: domain.FlagScam, SubmittedBy: "u1",
	}
	repo.rows["f2"] = &domain.OpportunityFlag{
		BaseModel:       domain.BaseModel{ID: "f2"},
		OpportunitySlug: "a", OpportunityKind: "job", Reason: domain.FlagScam, SubmittedBy: "u2",
	}
	repo.rows["f3"] = &domain.OpportunityFlag{
		BaseModel:       domain.BaseModel{ID: "f3"},
		OpportunitySlug: "b", OpportunityKind: "job", Reason: domain.FlagSpam, SubmittedBy: "u3",
	}
	req := httptest.NewRequest("GET", "/admin/opportunities/top-flagged", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Results []repository.TopFlaggedRow `json:"results"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Results) == 0 {
		t.Errorf("expected results, got none")
	}
}
