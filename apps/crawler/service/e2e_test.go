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
	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/frame/frametests"

	"stawi.jobs/pkg/archive"
	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// fakeCrawlerRepo implements every narrow interface the Phase 4 crawler
// handlers use (SourceLister, SourceGetter, SourceHealthRepo, SourceUpserter)
// in a single fake so the e2e test can wire one repo into every handler.
type fakeCrawlerRepo struct {
	mu   sync.Mutex
	rows map[string]*domain.Source
}

func newFakeCrawlerRepo(src ...*domain.Source) *fakeCrawlerRepo {
	r := &fakeCrawlerRepo{rows: make(map[string]*domain.Source, len(src))}
	for _, s := range src {
		r.rows[s.ID] = s
	}
	return r
}

func (r *fakeCrawlerRepo) ListDue(_ context.Context, _ time.Time, limit int) ([]*domain.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.Source, 0, len(r.rows))
	for _, s := range r.rows {
		cp := *s
		out = append(out, &cp)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (r *fakeCrawlerRepo) GetByID(_ context.Context, id string) (*domain.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (r *fakeCrawlerRepo) UpdateNextCrawl(_ context.Context, id string, next, verified time.Time, health float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.NextCrawlAt = next
		if !verified.IsZero() {
			s.LastVerifiedAt = &verified
		}
		s.HealthScore = health
	}
	return nil
}

func (r *fakeCrawlerRepo) RecordSuccess(_ context.Context, id string, h float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.HealthScore = h
		s.ConsecutiveFailures = 0
	}
	return nil
}

func (r *fakeCrawlerRepo) RecordFailure(_ context.Context, id string, h float64, cf int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.HealthScore = h
		s.ConsecutiveFailures = cf
	}
	return nil
}

func (r *fakeCrawlerRepo) FlagNeedsTuning(_ context.Context, id string, flag bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.rows[id]; ok {
		s.NeedsTuning = flag
	}
	return nil
}

func (r *fakeCrawlerRepo) Upsert(_ context.Context, src *domain.Source) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Upserted sources may not have an ID yet (the real GORM repo
	// assigns one on insert). Fall back to a Type|BaseURL key so
	// lookups by ID via other methods still work when the ID is
	// populated and any later test that exercises the discover
	// path can find the row.
	key := src.ID
	if key == "" {
		key = string(src.Type) + "|" + src.BaseURL
	}
	r.rows[key] = src
	return nil
}

// pageCompletedFanout fans a single crawl.page.completed.v1 delivery out
// to both the real PageCompletedHandler (health bookkeeping) and the
// envCollector (test assertions). The Frame event registry is a simple map
// keyed by Name(), so only one handler may register per topic; this wrapper
// satisfies that constraint while exercising both paths.
type pageCompletedFanout struct {
	handler *PageCompletedHandler
	col     *envCollector[eventsv1.CrawlPageCompletedV1]
}

func (f *pageCompletedFanout) Name() string     { return eventsv1.TopicCrawlPageCompleted }
func (f *pageCompletedFanout) PayloadType() any { return f.handler.PayloadType() }
func (f *pageCompletedFanout) Validate(ctx context.Context, p any) error {
	return f.handler.Validate(ctx, p)
}
func (f *pageCompletedFanout) Execute(ctx context.Context, p any) error {
	if err := f.col.Execute(ctx, p); err != nil {
		return err
	}
	return f.handler.Execute(ctx, p)
}

func TestCrawlerE2ETickToVariantEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawler-e2e"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	// Fake source repo + connector. Description is padded past the
	// 50-char gate so the job passes quality.Check and counts as emitted.
	repo := newFakeCrawlerRepo(&domain.Source{
		BaseModel:        domain.BaseModel{ID: "src_e2e"},
		Type:             domain.SourceGenericHTML,
		BaseURL:          "https://acme.example/jobs",
		Status:           domain.SourceActive,
		Country:          "KE",
		Language:         "en",
		CrawlIntervalSec: 60,
		HealthScore:      1.0,
	})

	reg := connectors.NewRegistry()
	reg.Register(&fakeConnector{
		jobs: []domain.ExternalJob{
			{
				ExternalID:  "ext-a",
				Title:       "Backend Engineer",
				Company:     "Acme",
				ApplyURL:    "https://acme.example/jobs/ext-a",
				Description: "We are hiring a backend engineer to own our Go services across the stack.",
			},
			{
				ExternalID:  "ext-b",
				Title:       "Data Scientist",
				Company:     "Acme",
				ApplyURL:    "https://acme.example/jobs/ext-b",
				Description: "We are hiring a data scientist focused on analytics tooling and experiment design.",
			},
		},
		raw: []byte("<html>page</html>"),
	})

	// variantCol observes variants ingested. envCollector is defined in
	// crawl_request_handler_test.go (same package).
	variantCol := &envCollector[eventsv1.VariantIngestedV1]{topic: eventsv1.TopicVariantsIngested}
	svc.EventsManager().Add(variantCol)

	// pageCol + the real PageCompletedHandler share one fanout registration
	// because Frame's event registry is a map[name]EventI — one entry per
	// topic name. The fanout calls both in sequence.
	pageCol := &envCollector[eventsv1.CrawlPageCompletedV1]{topic: eventsv1.TopicCrawlPageCompleted}
	pageH := NewPageCompletedHandler(repo)
	fanout := &pageCompletedFanout{handler: pageH, col: pageCol}

	// Wire the three production handlers using the one composite repo
	// and the in-memory fake archive.
	reqH := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc: svc, Sources: repo, Registry: reg, Archive: archive.NewFakeArchive(),
	})
	discH := NewSourceDiscoveredHandler(repo)
	for _, c := range []events.EventI{reqH, fanout, discH} {
		svc.EventsManager().Add(c)
	}

	// Start Frame so every subscription is live before the tick emits.
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(250 * time.Millisecond)

	// Hit the tick endpoint; the admitter grants everything.
	admit := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) {
		return want, 0
	})
	handler := SchedulerTickHandler(svc, repo, admit)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/tick", nil)
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tick status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp schedulerTickResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Dispatched != 1 {
		t.Fatalf("dispatched=%d, want 1", resp.Dispatched)
	}

	// Wait for the pipeline:
	//   crawl.requests.v1 → reqH → (2 variants + 1 page-completed)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if variantCol.Len() == 2 && pageCol.Len() == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if variantCol.Len() != 2 {
		t.Fatalf("variant events=%d, want 2", variantCol.Len())
	}
	if pageCol.Len() != 1 {
		t.Fatalf("page-completed events=%d, want 1", pageCol.Len())
	}

	// Verify both emitted variants carry the fake connector's data end-to-end.
	gotTitles := map[string]bool{}
	for _, env := range variantCol.Snapshot() {
		if env.Payload.SourceID != "src_e2e" {
			t.Fatalf("variant SourceID=%q, want src_e2e", env.Payload.SourceID)
		}
		if env.Payload.VariantID == "" || env.Payload.HardKey == "" {
			t.Fatalf("variant missing ids: %+v", env.Payload)
		}
		gotTitles[env.Payload.Title] = true
	}
	for _, want := range []string{"Backend Engineer", "Data Scientist"} {
		if !gotTitles[want] {
			t.Fatalf("missing variant title %q; got %v", want, gotTitles)
		}
	}

	// Page-completed should summarise the same two jobs.
	pc := pageCol.Snapshot()[0].Payload
	if pc.SourceID != "src_e2e" {
		t.Fatalf("page-completed SourceID=%q, want src_e2e", pc.SourceID)
	}
	if pc.JobsFound != 2 || pc.JobsEmitted != 2 || pc.JobsRejected != 0 {
		t.Fatalf("page-completed counts wrong: %+v", pc)
	}

	// After PageCompletedHandler processes the page-completed event, the
	// source's HealthScore should have been recorded as a success (≥ 1.0).
	// Wait up to 3 s for that reconciliation to run.
	deadline = time.Now().Add(3 * time.Second)
	var finalSrc *domain.Source
	for time.Now().Before(deadline) {
		s, _ := repo.GetByID(ctx, "src_e2e")
		finalSrc = s
		if s != nil && s.ConsecutiveFailures == 0 && s.HealthScore >= 1.0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finalSrc == nil {
		t.Fatalf("src_e2e disappeared from the fake repo")
		return
	}
	if finalSrc.HealthScore < 1.0 {
		t.Fatalf("HealthScore=%v, want >= 1.0 after successful crawl", finalSrc.HealthScore)
	}
}
