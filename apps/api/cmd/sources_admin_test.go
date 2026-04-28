package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// fakeSourceRepo is an in-memory sourceAdminRepo for handler tests.
type fakeSourceRepo struct {
	rows map[string]*domain.Source
}

func newFakeSourceRepo() *fakeSourceRepo { return &fakeSourceRepo{rows: map[string]*domain.Source{}} }

func (f *fakeSourceRepo) GetByID(_ context.Context, id string) (*domain.Source, error) {
	s, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (f *fakeSourceRepo) Create(_ context.Context, s *domain.Source) error {
	if s.ID == "" {
		s.ID = "id-" + strconvI(len(f.rows)+1)
	}
	f.rows[s.ID] = s
	return nil
}

func strconvI(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func (f *fakeSourceRepo) Update(_ context.Context, id string, fields map[string]any) error {
	s, ok := f.rows[id]
	if !ok {
		return nil
	}
	if v, ok := fields["name"].(string); ok {
		s.Name = v
	}
	if v, ok := fields["country"].(string); ok {
		s.Country = v
	}
	if v, ok := fields["language"].(string); ok {
		s.Language = v
	}
	if v, ok := fields["priority"].(domain.Priority); ok {
		s.Priority = v
	}
	if v, ok := fields["crawl_interval_sec"].(int); ok {
		s.CrawlIntervalSec = v
	}
	if v, ok := fields["kinds"].([]string); ok {
		s.Kinds = pq.StringArray(v)
	}
	if v, ok := fields["required_attributes_by_kind"].(map[string][]string); ok {
		s.RequiredAttributesByKind = v
	}
	if v, ok := fields["auto_approve"].(bool); ok {
		s.AutoApprove = v
	}
	return nil
}

func (f *fakeSourceRepo) HardDelete(_ context.Context, id string) error {
	delete(f.rows, id)
	return nil
}

func (f *fakeSourceRepo) DisableSource(_ context.Context, id string) error {
	if s, ok := f.rows[id]; ok {
		s.Status = domain.SourceDisabled
	}
	return nil
}

func (f *fakeSourceRepo) PauseSource(_ context.Context, id string) error {
	if s, ok := f.rows[id]; ok {
		s.Status = domain.SourcePaused
	}
	return nil
}

func (f *fakeSourceRepo) EnableSource(_ context.Context, id string) error {
	if s, ok := f.rows[id]; ok {
		s.Status = domain.SourceActive
	}
	return nil
}

func (f *fakeSourceRepo) Approve(_ context.Context, id, operator string, at time.Time) error {
	if s, ok := f.rows[id]; ok {
		s.Status = domain.SourceActive
		s.ApprovedAt = &at
		s.ApprovedBy = operator
	}
	return nil
}

func (f *fakeSourceRepo) Reject(_ context.Context, id, reason string) error {
	if s, ok := f.rows[id]; ok {
		s.Status = domain.SourceRejected
		s.RejectionReason = reason
	}
	return nil
}

func (f *fakeSourceRepo) ListWithFilters(_ context.Context, fl repository.ListFilter) ([]*domain.Source, int64, error) {
	out := []*domain.Source{}
	for _, s := range f.rows {
		if fl.Status != "" && s.Status != fl.Status {
			continue
		}
		if fl.Type != "" && s.Type != fl.Type {
			continue
		}
		if fl.Country != "" && s.Country != fl.Country {
			continue
		}
		if fl.Kind != "" {
			match := false
			for _, k := range s.Kinds {
				if k == fl.Kind {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, s)
	}
	return out, int64(len(out)), nil
}

func (f *fakeSourceRepo) ListByStatuses(_ context.Context, statuses []domain.SourceStatus, _ int) ([]*domain.Source, error) {
	want := map[domain.SourceStatus]bool{}
	for _, s := range statuses {
		want[s] = true
	}
	out := []*domain.Source{}
	for _, s := range f.rows {
		if want[s.Status] {
			out = append(out, s)
		}
	}
	return out, nil
}

// fakeDispatcher records VerifyAndPersist calls and returns a canned report.
type fakeDispatcher struct {
	calls       []string
	report      *domain.VerificationReport
	err         error
	persistFail bool
}

func (f *fakeDispatcher) VerifyAndPersist(_ context.Context, id string) (*domain.VerificationReport, error) {
	f.calls = append(f.calls, id)
	rep := f.report
	if rep == nil {
		t := time.Now().UTC()
		rep = &domain.VerificationReport{
			StartedAt:      t,
			CompletedAt:    &t,
			URLValid:       true,
			BlocklistClean: true,
			KindsKnown:     true,
			Reachable:      true,
			RobotsAllowed:  true,
			OverallPass:    true,
		}
	}
	if f.persistFail {
		return rep, f.err
	}
	return rep, f.err
}

// adminTestHarness builds a sourcesAdmin with fakes and registers the
// handlers on a fresh ServeMux.
func adminTestHarness(t *testing.T) (*sourcesAdmin, *fakeSourceRepo, *fakeDispatcher, *http.ServeMux) {
	t.Helper()

	yaml := `
kind: job
display_name: Job
issuing_entity_label: Company
url_prefix: jobs
universal_required: [title]
kind_required: []
kind_optional: []
extraction_prompt: ""
`
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/job.yaml", []byte(yaml), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	reg, err := opportunity.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	repo := newFakeSourceRepo()
	disp := &fakeDispatcher{}
	a := &sourcesAdmin{repo: repo, dispatcher: disp, registry: reg}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/sources/discovered", requireAdmin(a.handleListDiscovered))
	mux.HandleFunc("POST /admin/sources/{id}/verify", requireAdmin(a.handleVerify))
	mux.HandleFunc("POST /admin/sources/{id}/approve", requireAdmin(a.handleApprove))
	mux.HandleFunc("POST /admin/sources/{id}/reject", requireAdmin(a.handleReject))
	mux.HandleFunc("POST /admin/sources/{id}/pause", requireAdmin(a.handlePause))
	mux.HandleFunc("POST /admin/sources/{id}/resume", requireAdmin(a.handleResume))
	mux.HandleFunc("GET /admin/sources/{id}", requireAdmin(a.handleGet))
	mux.HandleFunc("PUT /admin/sources/{id}", requireAdmin(a.handleUpdate))
	mux.HandleFunc("DELETE /admin/sources/{id}", requireAdmin(a.handleDelete))
	mux.HandleFunc("GET /admin/sources", requireAdmin(a.handleList))
	mux.HandleFunc("POST /admin/sources", requireAdmin(a.handleCreate))

	return a, repo, disp, mux
}

func authHeader() string {
	return "Bearer fake.jwt.token"
}

func TestSourcesAdmin_RequireAdmin(t *testing.T) {
	_, _, _, mux := adminTestHarness(t)
	req := httptest.NewRequest("GET", "/admin/sources", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestSourcesAdmin_CreateAndGet(t *testing.T) {
	_, repo, _, mux := adminTestHarness(t)

	body := map[string]any{
		"type":     "remoteok",
		"base_url": "https://remoteok.com",
		"kinds":    []string{"job"},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/admin/sources", bytes.NewReader(raw))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d body=%s", rr.Code, rr.Body.String())
	}

	var created domain.Source
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Status != domain.SourcePending {
		t.Errorf("status=%q want pending", created.Status)
	}
	if !created.AutoApprove {
		t.Errorf("AutoApprove=false; operator-created sources should default to true")
	}
	if _, ok := repo.rows[created.ID]; !ok {
		t.Errorf("created source not in repo")
	}

	// GET it back.
	getReq := httptest.NewRequest("GET", "/admin/sources/"+created.ID, nil)
	getReq.Header.Set("Authorization", authHeader())
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, getReq)
	if rr2.Code != http.StatusOK {
		t.Fatalf("get: %d", rr2.Code)
	}
}

func TestSourcesAdmin_CreateRejectsUnknownKind(t *testing.T) {
	_, _, _, mux := adminTestHarness(t)

	body := map[string]any{
		"type":     "remoteok",
		"base_url": "https://example.com",
		"kinds":    []string{"definitely-not-a-kind"},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/admin/sources", bytes.NewReader(raw))
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("unknown_kind")) {
		t.Errorf("expected unknown_kind error, got %s", rr.Body.String())
	}
}

func TestSourcesAdmin_CreateRejectsBlockedURL(t *testing.T) {
	_, _, _, mux := adminTestHarness(t)
	body := map[string]any{
		"type":     "generic_html",
		"base_url": "https://linkedin.com/jobs",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/admin/sources", bytes.NewReader(raw))
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestSourcesAdmin_GetNotFound(t *testing.T) {
	_, _, _, mux := adminTestHarness(t)
	req := httptest.NewRequest("GET", "/admin/sources/nope", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestSourcesAdmin_Verify(t *testing.T) {
	_, repo, disp, mux := adminTestHarness(t)
	repo.rows["s1"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s1"},
		Type:      domain.SourceRemoteOK,
		BaseURL:   "https://remoteok.com",
		Status:    domain.SourcePending,
		Kinds:     pq.StringArray{"job"},
	}
	req := httptest.NewRequest("POST", "/admin/sources/s1/verify", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("verify: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(disp.calls) != 1 {
		t.Errorf("dispatcher not invoked: %v", disp.calls)
	}
}

func TestSourcesAdmin_ApproveRequiresVerified(t *testing.T) {
	_, repo, _, mux := adminTestHarness(t)
	repo.rows["s1"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s1"},
		Status:    domain.SourcePending, // not verified yet
	}
	req := httptest.NewRequest("POST", "/admin/sources/s1/approve", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rr.Code)
	}
}

func TestSourcesAdmin_ApproveTransitionsToActive(t *testing.T) {
	_, repo, _, mux := adminTestHarness(t)
	repo.rows["s1"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s1"},
		Status:    domain.SourceVerified,
	}
	req := httptest.NewRequest("POST", "/admin/sources/s1/approve", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if repo.rows["s1"].Status != domain.SourceActive {
		t.Errorf("not active: %q", repo.rows["s1"].Status)
	}
}

func TestSourcesAdmin_Reject(t *testing.T) {
	_, repo, _, mux := adminTestHarness(t)
	repo.rows["s1"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s1"},
		Status:    domain.SourceVerifying,
	}
	req := httptest.NewRequest("POST", "/admin/sources/s1/reject?reason=bad+robots", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if repo.rows["s1"].Status != domain.SourceRejected {
		t.Errorf("not rejected: %q", repo.rows["s1"].Status)
	}
	if repo.rows["s1"].RejectionReason != "bad robots" {
		t.Errorf("reason=%q", repo.rows["s1"].RejectionReason)
	}
}

func TestSourcesAdmin_PauseAndResume(t *testing.T) {
	_, repo, _, mux := adminTestHarness(t)
	repo.rows["s1"] = &domain.Source{
		BaseModel: domain.BaseModel{ID: "s1"},
		Status:    domain.SourceActive,
	}
	pause := httptest.NewRequest("POST", "/admin/sources/s1/pause", nil)
	pause.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, pause)
	if rr.Code != http.StatusOK {
		t.Fatalf("pause: %d", rr.Code)
	}
	if repo.rows["s1"].Status != domain.SourcePaused {
		t.Errorf("not paused: %q", repo.rows["s1"].Status)
	}

	resume := httptest.NewRequest("POST", "/admin/sources/s1/resume", nil)
	resume.Header.Set("Authorization", authHeader())
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, resume)
	if rr2.Code != http.StatusOK {
		t.Fatalf("resume: %d", rr2.Code)
	}
	if repo.rows["s1"].Status != domain.SourceActive {
		t.Errorf("not active: %q", repo.rows["s1"].Status)
	}
}

func TestSourcesAdmin_DeleteSoftAndHard(t *testing.T) {
	_, repo, _, mux := adminTestHarness(t)
	repo.rows["s1"] = &domain.Source{BaseModel: domain.BaseModel{ID: "s1"}, Status: domain.SourceActive}
	repo.rows["s2"] = &domain.Source{BaseModel: domain.BaseModel{ID: "s2"}, Status: domain.SourceActive}

	soft := httptest.NewRequest("DELETE", "/admin/sources/s1", nil)
	soft.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, soft)
	if rr.Code != http.StatusOK {
		t.Fatalf("soft delete: %d", rr.Code)
	}
	if repo.rows["s1"].Status != domain.SourceDisabled {
		t.Errorf("soft-deleted source must end in disabled state, got %q", repo.rows["s1"].Status)
	}

	hard := httptest.NewRequest("DELETE", "/admin/sources/s2?hard=true", nil)
	hard.Header.Set("Authorization", authHeader())
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, hard)
	if rr2.Code != http.StatusOK {
		t.Fatalf("hard delete: %d", rr2.Code)
	}
	if _, ok := repo.rows["s2"]; ok {
		t.Errorf("hard delete left row in repo")
	}
}

func TestSourcesAdmin_ListWithFilters(t *testing.T) {
	_, repo, _, mux := adminTestHarness(t)
	repo.rows["a"] = &domain.Source{BaseModel: domain.BaseModel{ID: "a"}, Status: domain.SourceActive, Kinds: pq.StringArray{"job"}}
	repo.rows["b"] = &domain.Source{BaseModel: domain.BaseModel{ID: "b"}, Status: domain.SourcePending, Kinds: pq.StringArray{"job"}}

	req := httptest.NewRequest("GET", "/admin/sources?status=pending", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}
	var resp struct {
		Sources []*domain.Source `json:"sources"`
		Total   int64            `json:"total"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Total != 1 || len(resp.Sources) != 1 || resp.Sources[0].ID != "b" {
		t.Errorf("unexpected list result: %+v", resp)
	}
}

func TestSourcesAdmin_ListDiscovered(t *testing.T) {
	_, repo, _, mux := adminTestHarness(t)
	repo.rows["a"] = &domain.Source{BaseModel: domain.BaseModel{ID: "a"}, Status: domain.SourceActive}
	repo.rows["b"] = &domain.Source{BaseModel: domain.BaseModel{ID: "b"}, Status: domain.SourcePending}
	repo.rows["c"] = &domain.Source{BaseModel: domain.BaseModel{ID: "c"}, Status: domain.SourceVerified}

	req := httptest.NewRequest("GET", "/admin/sources/discovered", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("discovered: %d", rr.Code)
	}
	var resp struct {
		Sources []*domain.Source `json:"sources"`
		Count   int              `json:"count"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Errorf("count=%d want 2", resp.Count)
	}
}

func TestSourcesAdmin_UpdateValidates(t *testing.T) {
	_, repo, _, mux := adminTestHarness(t)
	repo.rows["s1"] = &domain.Source{BaseModel: domain.BaseModel{ID: "s1"}, Status: domain.SourceActive}

	body := map[string]any{"crawl_interval_sec": 30} // below 60s minimum
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/admin/sources/s1", bytes.NewReader(raw))
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
