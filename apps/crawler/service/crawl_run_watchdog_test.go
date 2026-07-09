package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/frametests"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
)

// fakeResumableStore records which runs the watchdog touched/failed.
type fakeResumableStore struct {
	resumable []*domain.CrawlRun
	touched   map[string]bool
	failed    map[string]bool
}

func newFakeResumableStore(runs ...*domain.CrawlRun) *fakeResumableStore {
	return &fakeResumableStore{resumable: runs, touched: map[string]bool{}, failed: map[string]bool{}}
}

func (f *fakeResumableStore) FindResumable(_ context.Context, _ int) ([]*domain.CrawlRun, error) {
	return f.resumable, nil
}
func (f *fakeResumableStore) TouchLease(_ context.Context, id string, _ time.Duration) error {
	f.touched[id] = true
	return nil
}
func (f *fakeResumableStore) Fail(_ context.Context, id, _, _ string, _, _, _ int) error {
	f.failed[id] = true
	return nil
}

func runWatchdog(t *testing.T, store ResumableRunStore, admit Admitter, stuckMax int) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx, frame.WithName("run-watchdog-test"), frametests.WithNoopDriver())
	defer svc.Stop(ctx)
	// Register a handler for the continuation topic so Emit resolves it.
	svc.EventsManager().Add(&envCollector[eventsv1.CrawlRequestV1]{topic: eventsv1.TopicCrawlRequests})
	go func() { _ = svc.Run(ctx, "") }()
	frametest.WaitPublisherReady(t, svc, eventsv1.TopicCrawlRequests, 2*time.Second)

	h := CrawlRunWatchdogHandler(svc, store, admit, 50, stuckMax, 300)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/crawl/runs/sweep", nil).WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("watchdog status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func mkRun(id, source string, attempt int) *domain.CrawlRun {
	r := &domain.CrawlRun{SourceID: source, Status: domain.CrawlRunRunning, Attempt: attempt}
	r.ID = id
	return r
}

func TestCrawlRunWatchdog_RedrivesLapsedRun(t *testing.T) {
	store := newFakeResumableStore(mkRun("run-1", "s1", 3))
	allow := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) { return want, 0 })

	out := runWatchdog(t, store, allow, 200)

	if got := out["redriven"].(float64); got != 1 {
		t.Fatalf("redriven=%v want 1", out["redriven"])
	}
	if !store.touched["run-1"] {
		t.Fatal("lapsed run should have its lease touched (debounce) before re-emit")
	}
	if store.failed["run-1"] {
		t.Fatal("a non-stuck run must not be failed")
	}
}

func TestCrawlRunWatchdog_FailsStuckRun(t *testing.T) {
	// Attempt (50) is at the stuck ceiling (50) → fail, not re-drive.
	store := newFakeResumableStore(mkRun("run-stuck", "s1", 50))
	allow := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) { return want, 0 })

	out := runWatchdog(t, store, allow, 50)

	if got := out["failed"].(float64); got != 1 {
		t.Fatalf("failed=%v want 1", out["failed"])
	}
	if got := out["redriven"].(float64); got != 0 {
		t.Fatalf("redriven=%v want 0", out["redriven"])
	}
	if !store.failed["run-stuck"] {
		t.Fatal("stuck run should be failed")
	}
	if store.touched["run-stuck"] {
		t.Fatal("stuck run should not be re-driven")
	}
}

func TestCrawlRunWatchdog_BackpressureDefers(t *testing.T) {
	store := newFakeResumableStore(mkRun("run-1", "s1", 1))
	deny := admitterFunc(func(_ context.Context, _ string, _ int) (int, time.Duration) { return 0, time.Second })

	out := runWatchdog(t, store, deny, 200)

	if got := out["deferred"].(float64); got != 1 {
		t.Fatalf("deferred=%v want 1", out["deferred"])
	}
	if got := out["redriven"].(float64); got != 0 {
		t.Fatalf("redriven=%v want 0", out["redriven"])
	}
	if store.touched["run-1"] {
		t.Fatal("a backpressure-deferred run must not be touched or re-emitted")
	}
}
