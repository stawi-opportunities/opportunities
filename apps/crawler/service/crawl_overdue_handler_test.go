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

// overdueFakes implements CrawlOverdueLister + NextCrawlBumper.
type overdueFakes struct {
	due     []*domain.Source
	gotNow  time.Time
	bumped  map[string]time.Time
	updates []string
}

func (f *overdueFakes) ListDue(_ context.Context, now time.Time, _ int) ([]*domain.Source, error) {
	f.gotNow = now
	return f.due, nil
}

func (f *overdueFakes) Update(_ context.Context, id string, fields map[string]any) error {
	if f.bumped == nil {
		f.bumped = map[string]time.Time{}
	}
	if next, ok := fields["next_crawl_at"].(time.Time); ok {
		f.bumped[id] = next
	}
	f.updates = append(f.updates, id)
	return nil
}

func overdueSource(id string, status domain.SourceStatus) *domain.Source {
	s := &domain.Source{Type: domain.SourceGenericHTML, Status: status, CrawlIntervalSec: 3600}
	s.ID = id
	return s
}

func newOverdueTestSvc(t *testing.T) (context.Context, *frame.Service, *envCollector[eventsv1.CrawlRequestV1], context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-overdue-test"),
		frametests.WithNoopDriver(),
	)
	col := &envCollector[eventsv1.CrawlRequestV1]{topic: eventsv1.TopicCrawlRequests}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	frametest.WaitPublisherReady(t, svc, eventsv1.TopicCrawlRequests, 2*time.Second)
	return ctx, svc, col, cancel
}

func TestCrawlOverdueDispatchesAndLeases(t *testing.T) {
	ctx, svc, col, cancel := newOverdueTestSvc(t)
	defer cancel()
	defer svc.Stop(ctx)

	fakes := &overdueFakes{due: []*domain.Source{
		overdueSource("od1", domain.SourceActive),
		overdueSource("od2", domain.SourceDegraded),
		overdueSource("od3", domain.SourcePaused), // skipped: not crawlable
	}}
	admit := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) {
		return want, 0
	})

	h := CrawlOverdueHandler(svc, fakes, fakes, admit, 25)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/sources/crawl-overdue", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Dispatched int  `json:"dispatched"`
		Throttled  bool `json:"throttled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Dispatched != 2 || resp.Throttled {
		t.Fatalf("dispatched=%d throttled=%v, want 2/false", resp.Dispatched, resp.Throttled)
	}

	// The slack window must be applied: the lister was asked for sources
	// due as of (now - OverdueSlack), not now.
	if time.Since(fakes.gotNow) < OverdueSlack {
		t.Fatalf("ListDue cutoff %v not shifted back by OverdueSlack", fakes.gotNow)
	}

	// Both dispatched sources got an optimistic lease at least the 12h floor out.
	for _, id := range []string{"od1", "od2"} {
		next, ok := fakes.bumped[id]
		if !ok {
			t.Fatalf("source %s not lease-stamped; updates=%v", id, fakes.updates)
		}
		if until := time.Until(next); until < (MinCrawlIntervalHours-1)*time.Hour {
			t.Fatalf("source %s lease only %v out; want >= ~%dh", id, until, MinCrawlIntervalHours)
		}
	}
	if _, ok := fakes.bumped["od3"]; ok {
		t.Fatalf("paused source od3 must not be dispatched or leased")
	}

	// Both crawl.requests.v1 envelopes actually went out.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && col.Len() < 2 {
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 2 {
		t.Fatalf("crawl.requests emitted=%d, want 2", col.Len())
	}
}

func TestCrawlOverdueStopsBatchWhenGateCloses(t *testing.T) {
	ctx, svc, col, cancel := newOverdueTestSvc(t)
	defer cancel()
	defer svc.Stop(ctx)

	fakes := &overdueFakes{due: []*domain.Source{
		overdueSource("g1", domain.SourceActive),
		overdueSource("g2", domain.SourceActive),
		overdueSource("g3", domain.SourceActive),
	}}
	grants := 0
	admit := admitterFunc(func(_ context.Context, _ string, want int) (int, time.Duration) {
		grants++
		if grants > 1 {
			return 0, 30 * time.Second // gate closes after the first grant
		}
		return want, 0
	})

	h := CrawlOverdueHandler(svc, fakes, fakes, admit, 25)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/sources/crawl-overdue", nil))

	var resp struct {
		Dispatched int  `json:"dispatched"`
		Throttled  bool `json:"throttled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Dispatched != 1 || !resp.Throttled {
		t.Fatalf("dispatched=%d throttled=%v, want 1/true", resp.Dispatched, resp.Throttled)
	}
	// g2/g3 keep their past-due next_crawl_at — no lease, so the next tick
	// retries them.
	if len(fakes.bumped) != 1 {
		t.Fatalf("leases=%v, want only g1", fakes.bumped)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && col.Len() < 1 {
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("crawl.requests emitted=%d, want 1", col.Len())
	}
}

// --- watchdog ---

type fakeLastCrawl struct{ at time.Time }

func (f fakeLastCrawl) LastCrawlAt(_ context.Context) (time.Time, error) { return f.at, nil }

func TestCrawlWatchdogStalledWhenDueAndSilent(t *testing.T) {
	lister := &overdueFakes{due: []*domain.Source{overdueSource("w1", domain.SourceActive)}}
	h := CrawlWatchdogHandler(fakeLastCrawl{at: time.Now().Add(-2 * CrawlStallThreshold)}, lister)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/crawl/watchdog", nil))

	var resp struct {
		Stalled bool `json:"stalled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Stalled {
		t.Fatalf("want stalled=true, body=%s", rec.Body.String())
	}
}

func TestCrawlWatchdogOKWhenCrawlsRecent(t *testing.T) {
	lister := &overdueFakes{due: []*domain.Source{overdueSource("w2", domain.SourceActive)}}
	h := CrawlWatchdogHandler(fakeLastCrawl{at: time.Now().Add(-10 * time.Minute)}, lister)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/crawl/watchdog", nil))

	var resp struct {
		Stalled bool `json:"stalled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Stalled {
		t.Fatalf("want stalled=false, body=%s", rec.Body.String())
	}
}

func TestCrawlWatchdogOKWhenNothingDue(t *testing.T) {
	lister := &overdueFakes{} // nothing due — an idle fleet is not a stall
	h := CrawlWatchdogHandler(fakeLastCrawl{at: time.Now().Add(-24 * time.Hour)}, lister)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/crawl/watchdog", nil))

	var resp struct {
		Stalled bool `json:"stalled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Stalled {
		t.Fatalf("want stalled=false when nothing due, body=%s", rec.Body.String())
	}
}

// --- retention ---

type fakeSweeper struct {
	hidden, restored int64
	calls            []string
}

func (f *fakeSweeper) HideStale(_ context.Context, _ time.Duration) (int64, error) {
	f.calls = append(f.calls, "hide")
	return f.hidden, nil
}

func (f *fakeSweeper) RestoreReseen(_ context.Context) (int64, error) {
	f.calls = append(f.calls, "restore")
	return f.restored, nil
}

func TestRetentionExpireRunsRestoreThenHide(t *testing.T) {
	sw := &fakeSweeper{hidden: 3, restored: 1}
	h := RetentionExpireHandler(sw)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/retention/expire", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(sw.calls) != 2 || sw.calls[0] != "restore" || sw.calls[1] != "hide" {
		t.Fatalf("call order=%v, want [restore hide]", sw.calls)
	}
	var resp struct {
		Hidden   int64 `json:"hidden"`
		Restored int64 `json:"restored"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Hidden != 3 || resp.Restored != 1 {
		t.Fatalf("hidden=%d restored=%d, want 3/1", resp.Hidden, resp.Restored)
	}
}
