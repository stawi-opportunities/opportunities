package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/frame/security"

	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// fakeS3 records every PutObject / DeleteObject call so tests can
// assert the handlers wrote through to R2 without depending on a
// live S3 endpoint.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte // key → body
	putErr  error
	delErr  error
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string][]byte{}} }

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if f.putErr != nil {
		return nil, f.putErr
	}
	body, _ := io.ReadAll(in.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[aws.ToString(in.Key)] = body
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	if f.delErr != nil {
		return nil, f.delErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, aws.ToString(in.Key))
	return &s3.DeleteObjectOutput{}, nil
}

// recordingEmitter captures every emit so tests can assert the
// broadcast fired with the expected payload.
type recordingEmitter struct {
	mu    sync.Mutex
	calls []recordedEmit
}

type recordedEmit struct {
	topic   string
	payload any
}

func (e *recordingEmitter) Emit(_ context.Context, topic string, payload any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, recordedEmit{topic: topic, payload: payload})
	return nil
}

// definitionsTestHarness wires a definitionsAdmin with fakes + a
// MemoryLoader. Returns the mux, the s3 fake, and the emitter so the
// caller can assert side effects.
func definitionsTestHarness(t *testing.T) (*http.ServeMux, *fakeS3, *recordingEmitter, *definitions.MemoryLoader) {
	t.Helper()
	loader := definitions.NewMemoryLoader()
	s3c := newFakeS3()
	emitter := &recordingEmitter{}
	mux := http.NewServeMux()
	registerDefinitionsAdmin(mux, loader, s3c, "test-bucket", "definitions", emitter)
	return mux, s3c, emitter, loader
}

// withAdminContext injects an admin-role JWT context onto the request
// so requireAdmin lets the handler run.
func withAdminContext(r *http.Request) *http.Request {
	r.Header.Set("Authorization", "Bearer fake-token")
	claims := &security.AuthenticationClaims{}
	claims.Roles = []string{"admin"}
	return r.WithContext(claims.ClaimsToContext(r.Context()))
}

const validJobKindYAML = `kind: job
display_name: Job
issuing_entity_label: Company
amount_kind: salary
url_prefix: jobs
universal_required: [title, description]
kind_required: []
kind_optional: [employment_type]
categories: [Programming]
search_facets: [employment_type]
extraction_prompt: |
  Extract job posting details.
onboarding_flow: job-onboarding-v1
matcher: job-matcher-v1
`

// TestDefinitionsAdmin_Put_ValidKind_204 — happy path: a valid kind
// YAML is accepted, written to R2 (the fake), and a definitions
// changed event is broadcast.
func TestDefinitionsAdmin_Put_ValidKind_204(t *testing.T) {
	mux, s3c, emitter, _ := definitionsTestHarness(t)

	req := httptest.NewRequest("PUT", "/admin/definitions/kind/job", strings.NewReader(validJobKindYAML))
	req = withAdminContext(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want 204; body=%s", rec.Code, rec.Body.String())
	}
	gotBody, ok := s3c.objects["definitions/kind/job.yaml"]
	if !ok {
		t.Fatalf("R2 fake missing key; have=%v", keysOf(s3c.objects))
	}
	if string(gotBody) != validJobKindYAML {
		t.Errorf("R2 body mismatch; got=%q", string(gotBody))
	}
	if len(emitter.calls) != 1 {
		t.Fatalf("emitter calls = %d; want 1", len(emitter.calls))
	}
	if emitter.calls[0].topic != eventsv1.TopicDefinitionsChanged {
		t.Errorf("emit topic = %q; want %q", emitter.calls[0].topic, eventsv1.TopicDefinitionsChanged)
	}
	env, ok := emitter.calls[0].payload.(eventsv1.Envelope[eventsv1.DefinitionsChangedV1])
	if !ok {
		t.Fatalf("emit payload wrong type: %T", emitter.calls[0].payload)
	}
	if env.Payload.Type != "kind" || env.Payload.Name != "job" || env.Payload.Action != "upsert" {
		t.Errorf("emit payload mismatch: %+v", env.Payload)
	}
}

// TestDefinitionsAdmin_Put_InvalidKind_400 — invalid YAML (missing
// required spec fields) is rejected with 400 and never reaches R2.
func TestDefinitionsAdmin_Put_InvalidKind_400(t *testing.T) {
	mux, s3c, emitter, _ := definitionsTestHarness(t)

	bad := "kind: \"\"\ndisplay_name: missing-everything\n"
	req := httptest.NewRequest("PUT", "/admin/definitions/kind/broken", strings.NewReader(bad))
	req = withAdminContext(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body=%s", rec.Code, rec.Body.String())
	}
	if len(s3c.objects) != 0 {
		t.Errorf("R2 fake should be empty; got %d objects", len(s3c.objects))
	}
	if len(emitter.calls) != 0 {
		t.Errorf("emitter should not have fired; got %d calls", len(emitter.calls))
	}
}

// TestDefinitionsAdmin_Get_Unknown_404 — missing definition returns 404.
func TestDefinitionsAdmin_Get_Unknown_404(t *testing.T) {
	mux, _, _, _ := definitionsTestHarness(t)

	req := httptest.NewRequest("GET", "/admin/definitions/kind/does-not-exist", nil)
	req = withAdminContext(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestDefinitionsAdmin_Get_Returns_Body — once a definition is in
// the loader's cache, GET streams the body with the matching ETag.
func TestDefinitionsAdmin_Get_Returns_Body(t *testing.T) {
	mux, _, _, loader := definitionsTestHarness(t)
	ver := loader.Put(definitions.TypeKind, "job", []byte(validJobKindYAML))

	req := httptest.NewRequest("GET", "/admin/definitions/kind/job", nil)
	req = withAdminContext(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != ver {
		t.Errorf("ETag = %q; want %q", got, ver)
	}
	if rec.Body.String() != validJobKindYAML {
		t.Errorf("body mismatch; got=%q", rec.Body.String())
	}
}

// TestDefinitionsAdmin_List_AggregatesAllTypes — GET /admin/definitions
// with no query param returns every type-keyed slice (possibly empty).
func TestDefinitionsAdmin_List_AggregatesAllTypes(t *testing.T) {
	mux, _, _, loader := definitionsTestHarness(t)
	loader.Put(definitions.TypeKind, "job", []byte(validJobKindYAML))

	req := httptest.NewRequest("GET", "/admin/definitions", nil)
	req = withAdminContext(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string][]definitions.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	if len(out["kind"]) != 1 || out["kind"][0].Name != "job" {
		t.Errorf("kind entries wrong: %+v", out["kind"])
	}
}

// TestDefinitionsAdmin_Delete_204_RemovesObject — DELETE removes the
// R2 object and broadcasts a delete action.
func TestDefinitionsAdmin_Delete_204_RemovesObject(t *testing.T) {
	mux, s3c, emitter, _ := definitionsTestHarness(t)
	s3c.objects["definitions/kind/job.yaml"] = []byte(validJobKindYAML)

	req := httptest.NewRequest("DELETE", "/admin/definitions/kind/job", nil)
	req = withAdminContext(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want 204; body=%s", rec.Code, rec.Body.String())
	}
	if _, present := s3c.objects["definitions/kind/job.yaml"]; present {
		t.Error("R2 object still present after delete")
	}
	if len(emitter.calls) != 1 {
		t.Fatalf("emitter calls = %d; want 1", len(emitter.calls))
	}
	env, ok := emitter.calls[0].payload.(eventsv1.Envelope[eventsv1.DefinitionsChangedV1])
	if !ok || env.Payload.Action != "delete" {
		t.Errorf("delete action not broadcast: %+v", emitter.calls[0])
	}
}

// TestDefinitionsAdmin_Reload_202_Broadcasts — POST reload only
// broadcasts the wildcard event; no R2 IO happens.
func TestDefinitionsAdmin_Reload_202_Broadcasts(t *testing.T) {
	mux, s3c, emitter, _ := definitionsTestHarness(t)

	req := httptest.NewRequest("POST", "/admin/definitions/reload", nil)
	req = withAdminContext(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d; want 202; body=%s", rec.Code, rec.Body.String())
	}
	if len(s3c.objects) != 0 {
		t.Errorf("R2 fake should be empty; got %d objects", len(s3c.objects))
	}
	if len(emitter.calls) != 1 {
		t.Fatalf("emitter calls = %d; want 1", len(emitter.calls))
	}
	env, ok := emitter.calls[0].payload.(eventsv1.Envelope[eventsv1.DefinitionsChangedV1])
	if !ok || env.Payload.Action != "reload" || env.Payload.Type != "*" {
		t.Errorf("reload broadcast wrong: %+v", emitter.calls[0])
	}
}

// TestDefinitionsAdmin_Put_TooLarge_400 — body over 64 KB is rejected.
func TestDefinitionsAdmin_Put_TooLarge_400(t *testing.T) {
	mux, s3c, _, _ := definitionsTestHarness(t)
	big := bytes.Repeat([]byte("x"), maxDefinitionBytes+1)

	req := httptest.NewRequest("PUT", "/admin/definitions/seed/big", bytes.NewReader(big))
	req = withAdminContext(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body=%s", rec.Code, rec.Body.String())
	}
	if len(s3c.objects) != 0 {
		t.Errorf("R2 fake should be empty; got %d objects", len(s3c.objects))
	}
}

// TestDefinitionsAdmin_NoAuth_Returns_401 — no Bearer token means 401.
func TestDefinitionsAdmin_NoAuth_Returns_401(t *testing.T) {
	mux, _, _, _ := definitionsTestHarness(t)

	req := httptest.NewRequest("GET", "/admin/definitions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
