package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
)

// --- multi-page resumable fake connector ---

type pageConnector struct{ total int }

func (c *pageConnector) Type() domain.SourceType { return domain.SourceGenericHTML }
func (c *pageConnector) Crawl(_ context.Context, _ domain.Source) connectors.CrawlIterator {
	return &pageIter{total: c.total}
}

// CrawlResume seeds the iterator at the persisted page index — exactly how a
// real resumable connector continues from a crawl_runs cursor.
func (c *pageConnector) CrawlResume(_ context.Context, _ domain.Source, cp *connectors.CheckpointState) connectors.CrawlIterator {
	start := 0
	if cp != nil {
		start = cp.PageIdx
	}
	return &pageIter{total: c.total, next: start}
}

type pageIter struct {
	total, next, cur int
}

func (it *pageIter) Next(_ context.Context) bool {
	if it.next >= it.total {
		return false
	}
	it.cur = it.next
	it.next++
	return true
}

func (it *pageIter) Items() []domain.ExternalOpportunity {
	return []domain.ExternalOpportunity{{
		Kind:          "job",
		ExternalID:    fmt.Sprintf("ext-%d", it.cur),
		Title:         fmt.Sprintf("Job %d", it.cur),
		IssuingEntity: "Acme",
		ApplyURL:      fmt.Sprintf("https://acme.example/jobs/%d", it.cur),
		Description:   "We are looking for a skilled backend engineer to join our team and build scalable services.",
	}}
}

func (it *pageIter) RawPayload() []byte              { return []byte("<html>page</html>") }
func (it *pageIter) HTTPStatus() int                 { return 200 }
func (it *pageIter) Err() error                      { return nil }
func (it *pageIter) Cursor() json.RawMessage         { return nil }
func (it *pageIter) Content() *content.Extracted     { return nil }
func (it *pageIter) Checkpoint() *connectors.CheckpointState {
	return &connectors.CheckpointState{
		Cursor:  json.RawMessage(fmt.Sprintf(`{"next":%d}`, it.next)),
		PageIdx: it.next,
	}
}

// --- in-memory CrawlRunStore enforcing single-flight + cursor persistence ---

type fakeRunStore struct {
	mu             sync.Mutex
	byID           map[string]*domain.CrawlRun
	activeBySource map[string]string
	seq            int
}

func newFakeRunStore() *fakeRunStore {
	return &fakeRunStore{byID: map[string]*domain.CrawlRun{}, activeBySource: map[string]string{}}
}

func cloneRun(r *domain.CrawlRun) *domain.CrawlRun { c := *r; return &c }

func (f *fakeRunStore) StartRun(_ context.Context, sourceID string, _ time.Time, owner string, _ time.Duration) (*domain.CrawlRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.activeBySource[sourceID]; ok {
		return cloneRun(f.byID[id]), false, nil
	}
	f.seq++
	run := &domain.CrawlRun{SourceID: sourceID, Status: domain.CrawlRunRunning, Owner: owner}
	run.ID = "run-" + sourceID + "-" + strconv.Itoa(f.seq)
	f.byID[run.ID] = run
	f.activeBySource[sourceID] = run.ID
	return cloneRun(run), true, nil
}

func (f *fakeRunStore) Claim(_ context.Context, id, owner string, _ time.Duration) (*domain.CrawlRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run, ok := f.byID[id]
	if !ok || (run.Status != domain.CrawlRunRunning && run.Status != domain.CrawlRunPaused) {
		return nil, false, nil
	}
	run.Owner = owner
	return cloneRun(run), true, nil
}

func (f *fakeRunStore) Progress(_ context.Context, id string, cursor json.RawMessage, pageIdx int, lastURL string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if run, ok := f.byID[id]; ok {
		run.Cursor, run.PageIdx, run.LastURL = cursor, pageIdx, lastURL
	}
	return nil
}

func (f *fakeRunStore) Yield(_ context.Context, id string, cursor json.RawMessage, pageIdx int, lastURL string, _ time.Duration, dF, dE, dR int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if run, ok := f.byID[id]; ok {
		run.Cursor, run.PageIdx, run.LastURL = cursor, pageIdx, lastURL
		run.SliceCount++
		run.Attempt++
		run.JobsFound += dF
		run.JobsEmitted += dE
		run.JobsRejected += dR
		run.Owner = ""
		run.Status = domain.CrawlRunRunning
	}
	return nil
}

func (f *fakeRunStore) Complete(_ context.Context, id string, cursor json.RawMessage, pageIdx int, lastURL string, dF, dE, dR int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if run, ok := f.byID[id]; ok {
		run.Cursor, run.PageIdx, run.LastURL = cursor, pageIdx, lastURL
		run.SliceCount++
		run.JobsFound += dF
		run.JobsEmitted += dE
		run.JobsRejected += dR
		run.Owner = ""
		run.Status = domain.CrawlRunCompleted
		delete(f.activeBySource, run.SourceID)
	}
	return nil
}

func (f *fakeRunStore) Fail(_ context.Context, id, code, message string, dF, dE, dR int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if run, ok := f.byID[id]; ok {
		run.Status = domain.CrawlRunFailed
		run.ErrorCode, run.ErrorMessage = code, message
		run.Owner = ""
		delete(f.activeBySource, run.SourceID)
	}
	return nil
}

func (f *fakeRunStore) active(sourceID string) *domain.CrawlRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.activeBySource[sourceID]; ok {
		return cloneRun(f.byID[id])
	}
	return nil
}

func (f *fakeRunStore) get(id string) *domain.CrawlRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.byID[id]; ok {
		return cloneRun(r)
	}
	return nil
}

// TestCrawlRequestHandler_BoundedSliceResumeAndComplete drives a 5-page source
// with a 2-page slice budget: it must take three slices, emit every page's job
// exactly once (no duplication across slice boundaries), self-re-enqueue a
// continuation between slices, and complete the run at the end.
func TestCrawlRequestHandler_BoundedSliceResumeAndComplete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-resume-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	variantCol := &envCollector[eventsv1.VariantIngestedV1]{topic: eventsv1.TopicVariantsIngested}
	contCol := &envCollector[eventsv1.CrawlRequestV1]{topic: eventsv1.TopicCrawlRequests}
	svc.EventsManager().Add(contCol)
	ingestedQ := wireIngestedQueue(ctx, svc, variantCol)

	go func() { _ = svc.Run(ctx, "") }()
	frametest.WaitPublisherReady(t, svc, ingestedQ, 2*time.Second)

	reg := connectors.NewRegistry()
	reg.Register(&pageConnector{total: 5})
	srcs := &fakeSourceGetter{rows: map[string]*domain.Source{
		"s1": {BaseModel: domain.BaseModel{ID: "s1"}, Type: domain.SourceGenericHTML,
			BaseURL: "https://acme.example/jobs", Status: domain.SourceActive, Country: "KE", Language: "en"},
	}}
	runStore := newFakeRunStore()

	h := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc:            svc,
		Sources:        srcs,
		Registry:       reg,
		Archive:        archive.NewFakeArchive(),
		IngestedQueue:  ingestedQ,
		RunRepo:        runStore,
		SliceMaxPages:  2,
		RunLeaseTTLSec: 300,
		// Admitter nil → continuations always admitted; Kinds nil → Verify skipped.
	})

	exec := func(req eventsv1.CrawlRequestV1) {
		env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, req)
		raw, _ := json.Marshal(env)
		rm := json.RawMessage(raw)
		if err := h.Execute(ctx, &rm); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}

	// Slice 1 — fresh scheduled tick. Processes 2 pages, then yields.
	exec(eventsv1.CrawlRequestV1{RequestID: "req-1", SourceID: "s1", Mode: "auto", Attempt: 1})
	run := runStore.active("s1")
	if run == nil || run.Status != domain.CrawlRunRunning {
		t.Fatalf("after slice 1: want running run, got %+v", run)
	}
	if run.SliceCount != 1 {
		t.Fatalf("after slice 1: slice_count=%d want 1", run.SliceCount)
	}
	runID := run.ID

	// Slice 2 — continuation. Two more pages, still running.
	exec(eventsv1.CrawlRequestV1{RequestID: "req-2", SourceID: "s1", Mode: "auto", Attempt: 2, RunID: runID, IsContinuation: true})
	run = runStore.get(runID)
	if run.SliceCount != 2 || run.Status != domain.CrawlRunRunning {
		t.Fatalf("after slice 2: %+v", run)
	}

	// Slice 3 — continuation. Final page, iterator exhausted → completed.
	exec(eventsv1.CrawlRequestV1{RequestID: "req-3", SourceID: "s1", Mode: "auto", Attempt: 3, RunID: runID, IsContinuation: true})
	run = runStore.get(runID)
	if run.Status != domain.CrawlRunCompleted {
		t.Fatalf("after slice 3: want completed, got %s (slice_count=%d)", run.Status, run.SliceCount)
	}
	if runStore.active("s1") != nil {
		t.Fatalf("completed run should free the source's single-flight slot")
	}

	// Every page's job emitted exactly once, no duplication across slices.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if variantCol.Len() == 5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if variantCol.Len() != 5 {
		t.Fatalf("variants=%d want 5 (one per page, no dup/loss)", variantCol.Len())
	}
	seen := map[string]int{}
	for _, env := range variantCol.Snapshot() {
		seen[env.Payload.Title]++
	}
	for i := 0; i < 5; i++ {
		if got := seen[fmt.Sprintf("Job %d", i)]; got != 1 {
			t.Fatalf("Job %d emitted %d times, want exactly 1", i, got)
		}
	}

	// Two continuations were self-re-enqueued (after slices 1 and 2), each
	// carrying the run id and the continuation flag.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if contCol.Len() == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if contCol.Len() != 2 {
		t.Fatalf("continuations=%d want 2", contCol.Len())
	}
	for _, env := range contCol.Snapshot() {
		if env.Payload.RunID != runID || !env.Payload.IsContinuation {
			t.Fatalf("bad continuation payload: %+v", env.Payload)
		}
	}
}

// TestCrawlRequestHandler_SingleFlightDropsDuplicateTick verifies a scheduled
// tick that arrives while a run is already active is dropped (no second pass).
func TestCrawlRequestHandler_SingleFlightDropsDuplicateTick(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-singleflight-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	variantCol := &envCollector[eventsv1.VariantIngestedV1]{topic: eventsv1.TopicVariantsIngested}
	ingestedQ := wireIngestedQueue(ctx, svc, variantCol)
	go func() { _ = svc.Run(ctx, "") }()
	frametest.WaitPublisherReady(t, svc, ingestedQ, 2*time.Second)

	reg := connectors.NewRegistry()
	reg.Register(&pageConnector{total: 10})
	srcs := &fakeSourceGetter{rows: map[string]*domain.Source{
		"s1": {BaseModel: domain.BaseModel{ID: "s1"}, Type: domain.SourceGenericHTML,
			BaseURL: "https://acme.example/jobs", Status: domain.SourceActive, Country: "KE", Language: "en"},
	}}
	runStore := newFakeRunStore()
	// Pre-open an active run for s1 so the scheduled tick sees one in flight.
	if _, started, _ := runStore.StartRun(ctx, "s1", time.Now(), "other-owner", time.Minute); !started {
		t.Fatal("precondition: StartRun should have started")
	}

	h := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc: svc, Sources: srcs, Registry: reg, Archive: archive.NewFakeArchive(),
		IngestedQueue: ingestedQ, RunRepo: runStore, SliceMaxPages: 2, RunLeaseTTLSec: 300,
	})

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
		RequestID: "dup", SourceID: "s1", Mode: "auto", Attempt: 1,
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if variantCol.Len() != 0 {
		t.Fatalf("duplicate scheduled tick should emit nothing, got %d variants", variantCol.Len())
	}
}
