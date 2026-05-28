package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// captureReparseEmitter records every Emit call so the test can assert
// the reparse endpoint published exactly the expected envelope.
type captureReparseEmitter struct {
	emitted []eventsv1.Envelope[eventsv1.CrawlRequestV1]
	err     error // when set, every Emit returns this and skips recording
}

func (c *captureReparseEmitter) Emit(_ context.Context, _ string, payload any) error {
	if c.err != nil {
		return c.err
	}
	if env, ok := payload.(eventsv1.Envelope[eventsv1.CrawlRequestV1]); ok {
		c.emitted = append(c.emitted, env)
		return nil
	}
	// Be lenient on type assertion to mirror frameEmitter's any-typed Emit;
	// the production emitter doesn't care about generic type erasure.
	return errors.New("unexpected payload type")
}

// fakeReparseCrawlRepo is an in-memory implementation of reparseCrawlRepo
// so the handler tests don't need a Postgres container.
type fakeReparseCrawlRepo struct {
	byID    map[string]*domain.RawPayload
	bySrc   map[string][]domain.RawPayload
	listErr error
}

func (f *fakeReparseCrawlRepo) GetRawPayload(_ context.Context, id string) (*domain.RawPayload, error) {
	if rp, ok := f.byID[id]; ok {
		return rp, nil
	}
	return nil, nil
}

func (f *fakeReparseCrawlRepo) ListRawPayloadsBySource(_ context.Context, sourceID string, _ time.Duration, _ int) ([]domain.RawPayload, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.bySrc[sourceID], nil
}

func TestReparseOne_EnqueuesEvent(t *testing.T) {
	rp := &domain.RawPayload{
		ID:          "rp-1",
		SourceID:    "src-1",
		ContentHash: "deadbeef",
	}
	repo := &fakeReparseCrawlRepo{byID: map[string]*domain.RawPayload{"rp-1": rp}}
	emitter := &captureReparseEmitter{}
	a := reparseAdmin{crawl: repo, emitter: emitter}

	req := httptest.NewRequest("POST", "/admin/raw_payloads/rp-1/reparse", nil)
	req.SetPathValue("id", "rp-1")
	setAdminAuth(req)
	rec := httptest.NewRecorder()
	a.reparseOne(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["queued"].(float64) != 1 {
		t.Fatalf("queued = %v; want 1", resp["queued"])
	}
	if resp["raw_payload_id"].(string) != "rp-1" {
		t.Fatalf("raw_payload_id = %v; want rp-1", resp["raw_payload_id"])
	}
	if len(emitter.emitted) != 1 {
		t.Fatalf("emitted = %d; want 1", len(emitter.emitted))
	}
	got := emitter.emitted[0]
	if got.EventType != eventsv1.TopicCrawlRequests {
		t.Fatalf("envelope event_type = %q; want %q", got.EventType, eventsv1.TopicCrawlRequests)
	}
	if got.Payload.RawPayloadID != "rp-1" {
		t.Fatalf("payload.RawPayloadID = %q; want rp-1", got.Payload.RawPayloadID)
	}
	if got.Payload.SourceID != "src-1" {
		t.Fatalf("payload.SourceID = %q; want src-1", got.Payload.SourceID)
	}
	if got.Payload.Mode != "reparse" {
		t.Fatalf("payload.Mode = %q; want reparse", got.Payload.Mode)
	}
}

func TestReparseOne_NotFound(t *testing.T) {
	a := reparseAdmin{
		crawl:   &fakeReparseCrawlRepo{byID: map[string]*domain.RawPayload{}},
		emitter: &captureReparseEmitter{},
	}
	req := httptest.NewRequest("POST", "/admin/raw_payloads/nope/reparse", nil)
	req.SetPathValue("id", "nope")
	setAdminAuth(req)
	rec := httptest.NewRecorder()
	a.reparseOne(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
}

func TestReparseOne_MissingID(t *testing.T) {
	a := reparseAdmin{
		crawl:   &fakeReparseCrawlRepo{},
		emitter: &captureReparseEmitter{},
	}
	req := httptest.NewRequest("POST", "/admin/raw_payloads//reparse", nil)
	req.SetPathValue("id", "")
	setAdminAuth(req)
	rec := httptest.NewRecorder()
	a.reparseOne(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}

func TestReparseOne_EmitFailure_Returns502(t *testing.T) {
	rp := &domain.RawPayload{ID: "rp-1", SourceID: "src-1"}
	repo := &fakeReparseCrawlRepo{byID: map[string]*domain.RawPayload{"rp-1": rp}}
	emitter := &captureReparseEmitter{err: errors.New("nats down")}
	a := reparseAdmin{crawl: repo, emitter: emitter}

	req := httptest.NewRequest("POST", "/admin/raw_payloads/rp-1/reparse", nil)
	req.SetPathValue("id", "rp-1")
	setAdminAuth(req)
	rec := httptest.NewRecorder()
	a.reparseOne(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502", rec.Code)
	}
}

func TestReparseSource_EnqueuesAllInWindow(t *testing.T) {
	rows := []domain.RawPayload{
		{ID: "rp-a", SourceID: "src-batch"},
		{ID: "rp-b", SourceID: "src-batch"},
		{ID: "rp-c", SourceID: "src-batch"},
	}
	repo := &fakeReparseCrawlRepo{bySrc: map[string][]domain.RawPayload{"src-batch": rows}}
	emitter := &captureReparseEmitter{}
	a := reparseAdmin{crawl: repo, emitter: emitter}

	req := httptest.NewRequest("POST", "/admin/sources/src-batch/reparse?since=24h", nil)
	req.SetPathValue("id", "src-batch")
	setAdminAuth(req)
	rec := httptest.NewRecorder()
	a.reparseSource(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["queued"].(float64) != 3 {
		t.Fatalf("queued = %v; want 3", resp["queued"])
	}
	if resp["source_id"].(string) != "src-batch" {
		t.Fatalf("source_id = %v; want src-batch", resp["source_id"])
	}
	if resp["window_seconds"].(float64) != float64((24 * time.Hour).Seconds()) {
		t.Fatalf("window_seconds = %v; want 86400", resp["window_seconds"])
	}
	if len(emitter.emitted) != 3 {
		t.Fatalf("emitted = %d; want 3", len(emitter.emitted))
	}
	ids := map[string]bool{}
	for _, env := range emitter.emitted {
		ids[env.Payload.RawPayloadID] = true
	}
	for _, want := range []string{"rp-a", "rp-b", "rp-c"} {
		if !ids[want] {
			t.Fatalf("missing emit for %s; got %v", want, ids)
		}
	}
}

func TestReparseSource_SwallowsIndividualEmitFailures(t *testing.T) {
	rows := []domain.RawPayload{
		{ID: "rp-a", SourceID: "src-batch"},
		{ID: "rp-b", SourceID: "src-batch"},
	}
	repo := &fakeReparseCrawlRepo{bySrc: map[string][]domain.RawPayload{"src-batch": rows}}
	emitter := &captureReparseEmitter{err: errors.New("transport flake")}
	a := reparseAdmin{crawl: repo, emitter: emitter}

	req := httptest.NewRequest("POST", "/admin/sources/src-batch/reparse?since=24h", nil)
	req.SetPathValue("id", "src-batch")
	setAdminAuth(req)
	rec := httptest.NewRecorder()
	a.reparseSource(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d; want 202 (failures must not 5xx)", rec.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["queued"].(float64) != 0 {
		t.Fatalf("queued = %v; want 0 (all emit failures)", resp["queued"])
	}
}

func TestReparseSource_MissingID(t *testing.T) {
	a := reparseAdmin{crawl: &fakeReparseCrawlRepo{}, emitter: &captureReparseEmitter{}}
	req := httptest.NewRequest("POST", "/admin/sources//reparse", nil)
	req.SetPathValue("id", "")
	setAdminAuth(req)
	rec := httptest.NewRecorder()
	a.reparseSource(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}
