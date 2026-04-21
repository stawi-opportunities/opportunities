package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// admitterFunc is a minimal Admitter used for unit tests so we don't
// stand up a NATS monitor stub just to drive Admit.
type admitterFunc func(ctx context.Context, topic string, want int) (int, time.Duration)

func (f admitterFunc) Admit(ctx context.Context, topic string, want int) (int, time.Duration) {
	return f(ctx, topic, want)
}

// tickCollector subscribes to a topic and records every envelope it sees.
type tickCollector struct {
	mu    sync.Mutex
	topic string
	got   []eventsv1.Envelope[eventsv1.CrawlRequestV1]
}

func (c *tickCollector) Name() string     { return c.topic }
func (c *tickCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *tickCollector) Validate(_ context.Context, _ any) error { return nil }
func (c *tickCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CrawlRequestV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}

// Snapshot returns a copy of the envelopes seen so far. Safe to call
// concurrently with Execute.
func (c *tickCollector) Snapshot() []eventsv1.Envelope[eventsv1.CrawlRequestV1] {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]eventsv1.Envelope[eventsv1.CrawlRequestV1], len(c.got))
	copy(out, c.got)
	return out
}

func TestSchedulerTickEmitsOneRequestPerAdmittedSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("scheduler-tick-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &tickCollector{topic: eventsv1.TopicCrawlRequests}
	svc.EventsManager().Add(col)

	// Run Frame so the in-memory subscriber becomes ready before
	// the scheduler emits. Same pattern as apps/writer/service_test.go.
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	// Seed two due sources in the fake repo (defined below).
	repo := newFakeSourceRepo(
		&domain.Source{BaseModel: domain.BaseModel{ID: "s1"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
		&domain.Source{BaseModel: domain.BaseModel{ID: "s2"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
	)

	admit := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) {
		return want, 0 // grant everything
	})

	handler := SchedulerTickHandler(svc, repo, admit)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/tick", nil)
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp schedulerTickResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Considered != 2 || resp.Admitted != 2 || resp.Dispatched != 2 {
		t.Fatalf("unexpected counts: %+v", resp)
	}

	// Both sources should have next_crawl_at pushed forward.
	for _, id := range []string{"s1", "s2"} {
		src, _ := repo.GetByID(ctx, id)
		if src == nil || !src.NextCrawlAt.After(time.Now().UTC()) {
			t.Fatalf("source %s next_crawl_at not advanced: %+v", id, src)
		}
	}

	// Give the in-memory pub/sub a moment to deliver.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(col.Snapshot()) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := col.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("emitted envelopes=%d, want 2", len(snap))
	}
	for _, env := range snap {
		if env.Payload.SourceID == "" || env.Payload.RequestID == "" {
			t.Fatalf("bad envelope: %+v", env)
		}
	}
}

func TestSchedulerTickRespectsAdmitDecisions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("scheduler-tick-deferred"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	svc.EventsManager().Add(&tickCollector{topic: eventsv1.TopicCrawlRequests})

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	repo := newFakeSourceRepo(
		&domain.Source{BaseModel: domain.BaseModel{ID: "s1"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
		&domain.Source{BaseModel: domain.BaseModel{ID: "s2"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
		&domain.Source{BaseModel: domain.BaseModel{ID: "s3"}, Status: domain.SourceActive, CrawlIntervalSec: 60},
	)

	admit := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) {
		return 1, 0 // only one of the three gets admitted this tick
	})

	handler := SchedulerTickHandler(svc, repo, admit)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/tick", nil)
	handler(rec, req)

	var resp schedulerTickResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Considered != 3 || resp.Admitted != 1 || resp.Dispatched != 1 {
		t.Fatalf("unexpected counts: %+v", resp)
	}

	// Only one source should have next_crawl_at advanced.
	advanced := 0
	for _, id := range []string{"s1", "s2", "s3"} {
		src, _ := repo.GetByID(ctx, id)
		if src != nil && src.NextCrawlAt.After(time.Now().UTC()) {
			advanced++
		}
	}
	if advanced != 1 {
		t.Fatalf("advanced=%d, want 1", advanced)
	}
}

// fakeSourceRepo implements SourceLister.
type fakeSourceRepo struct {
	mu   sync.Mutex
	rows map[string]*domain.Source
}

func newFakeSourceRepo(src ...*domain.Source) *fakeSourceRepo {
	r := &fakeSourceRepo{rows: make(map[string]*domain.Source, len(src))}
	for _, s := range src {
		r.rows[s.ID] = s
	}
	return r
}

func (r *fakeSourceRepo) ListDue(_ context.Context, _ time.Time, limit int) ([]*domain.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.Source, 0, len(r.rows))
	for _, s := range r.rows {
		out = append(out, s)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (r *fakeSourceRepo) UpdateNextCrawl(_ context.Context, id string, next, verified time.Time, health float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.NextCrawlAt = next
		s.LastVerifiedAt = &verified
		s.HealthScore = health
	}
	return nil
}

func (r *fakeSourceRepo) GetByID(_ context.Context, id string) (*domain.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.rows[id]
	if !ok {
		return nil, nil
	}
	// Return a copy so tests can compare before/after state.
	cp := *s
	return &cp, nil
}
