package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/frametests"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
)

// digestCollector mirrors the pattern in cv_stale_nudge_test.go: a
// Frame-bus subscriber that captures every envelope published on the
// weekly-jobs-digest topic. Lets us assert end-to-end that the
// handler publishes the right number of events without standing up a
// real NATS instance.
type digestCollector struct {
	mu  sync.Mutex
	got []json.RawMessage
}

func (c *digestCollector) Name() string                        { return eventsv1.TopicCandidateWeeklyJobsDigest }
func (c *digestCollector) PayloadType() any                    { var raw json.RawMessage; return &raw }
func (c *digestCollector) Validate(context.Context, any) error { return nil }
func (c *digestCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	c.mu.Lock()
	c.got = append(c.got, append(json.RawMessage(nil), *raw...))
	c.mu.Unlock()
	return nil
}
func (c *digestCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func waitForEmit(t *testing.T, col *digestCollector, want int) {
	t.Helper()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if col.Len() >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func newDigestSvc(t *testing.T) (context.Context, *frame.Service, *digestCollector) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("digest-test"),
		frametests.WithNoopDriver(),
	)
	t.Cleanup(func() { svc.Stop(ctx) })
	col := &digestCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	frametest.WaitPublisherReady(t, svc, eventsv1.TopicCandidateWeeklyJobsDigest, 2*time.Second)
	return ctx, svc, col
}

type fakeUnpaidLister struct {
	rows []UnpaidCandidate
	err  error
}

func (f *fakeUnpaidLister) ListUnpaid(_ context.Context) ([]UnpaidCandidate, error) {
	return f.rows, f.err
}

// fakeNewJobsLister returns a per-country fixture. Used to assert
// that the handler filters by candidate country.
type fakeNewJobsLister struct {
	byCountry map[string][]eventsv1.DigestJob
	calls     []string
	failOn    string
}

func (f *fakeNewJobsLister) ListNewJobs(_ context.Context, _ time.Time, country string, _ []string, _ int) ([]eventsv1.DigestJob, error) {
	f.calls = append(f.calls, country)
	if country == f.failOn {
		return nil, errors.New("stats backend down")
	}
	return f.byCountry[country], nil
}

type fakeWeeklyStats struct {
	out eventsv1.DigestStats
	err error
}

func (f *fakeWeeklyStats) GlobalStats(_ context.Context, _ time.Time) (eventsv1.DigestStats, error) {
	return f.out, f.err
}

func TestWeeklyJobsDigestEmitsOneEnvelopePerCandidate(t *testing.T) {
	_, svc, col := newDigestSvc(t)

	lister := &fakeUnpaidLister{rows: []UnpaidCandidate{
		{ID: "cnd_1", Country: "KE", Locale: "en", Kinds: []string{"job"}, EmailDigest: "weekly", WeeklySummary: true, CommEmail: true},
		{ID: "cnd_2", Country: "NG", Locale: "en", Kinds: []string{"job", "scholarship"}, EmailDigest: "weekly", WeeklySummary: true, CommEmail: true},
	}}
	jobs := &fakeNewJobsLister{byCountry: map[string][]eventsv1.DigestJob{
		"KE": {{CanonicalID: "j1", Title: "Engineer KE", Country: "KE", Kind: "job", Slug: "engineer-ke"}},
		"NG": {{CanonicalID: "j2", Title: "Engineer NG", Country: "NG", Kind: "job", Slug: "engineer-ng"}},
	}}
	stats := &fakeWeeklyStats{out: eventsv1.DigestStats{TotalNewThisWeek: 42}}

	handler := WeeklyJobsDigestHandler(WeeklyJobsDigestDeps{
		Svc:            svc,
		Lister:         lister,
		Jobs:           jobs,
		Stats:          stats,
		DefaultCadence: "weekly",
		WeeklyWeekday:  time.Monday,
		PlansURL:       "https://example.test/pricing/",
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/candidates/weekly_jobs_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp weeklyJobsDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Emitted != 2 || resp.Failed != 0 || resp.Skipped != 0 {
		t.Fatalf("resp=%+v, want emitted=2 failed=0 skipped=0", resp)
	}
	if len(jobs.calls) != 2 {
		t.Fatalf("ListNewJobs called %d times, want 2", len(jobs.calls))
	}
	waitForEmit(t, col, 2)
	if col.Len() != 2 {
		t.Fatalf("subscriber saw %d envelopes, want 2", col.Len())
	}
}

func TestWeeklyJobsDigestSkipsCandidatesWithNoJobs(t *testing.T) {
	_, svc, _ := newDigestSvc(t)
	handler := WeeklyJobsDigestHandler(WeeklyJobsDigestDeps{
		Svc: svc,
		Lister: &fakeUnpaidLister{rows: []UnpaidCandidate{
			{ID: "cnd_1", Country: "ZW", Kinds: []string{"job"}, EmailDigest: "weekly", WeeklySummary: true, CommEmail: true},
		}},
		Jobs:           &fakeNewJobsLister{byCountry: map[string][]eventsv1.DigestJob{}},
		Stats:          &fakeWeeklyStats{},
		DefaultCadence: "weekly",
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/candidates/weekly_jobs_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp weeklyJobsDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Emitted != 0 || resp.Skipped != 1 {
		t.Fatalf("resp=%+v, want emitted=0 skipped=1 (skip on empty jobs list)", resp)
	}
}

func TestWeeklyJobsDigestPerCandidateFailureDoesNotAbortSweep(t *testing.T) {
	_, svc, _ := newDigestSvc(t)
	handler := WeeklyJobsDigestHandler(WeeklyJobsDigestDeps{
		Svc: svc,
		Lister: &fakeUnpaidLister{rows: []UnpaidCandidate{
			{ID: "cnd_a", Country: "KE", Kinds: []string{"job"}, EmailDigest: "weekly", WeeklySummary: true, CommEmail: true},
			{ID: "cnd_b", Country: "NG", Kinds: []string{"job"}, EmailDigest: "weekly", WeeklySummary: true, CommEmail: true},
		}},
		Jobs: &fakeNewJobsLister{
			byCountry: map[string][]eventsv1.DigestJob{
				"NG": {{CanonicalID: "j2", Title: "Engineer NG", Country: "NG", Kind: "job", Slug: "engineer-ng"}},
			},
			failOn: "KE",
		},
		Stats:          &fakeWeeklyStats{},
		DefaultCadence: "weekly",
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/candidates/weekly_jobs_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp weeklyJobsDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Failed != 1 || resp.Emitted != 1 {
		t.Fatalf("resp=%+v, want failed=1 emitted=1 (KE fails, NG succeeds)", resp)
	}
}

func TestWeeklyJobsDigestEmitsEvenWhenStatsErrors(t *testing.T) {
	_, svc, _ := newDigestSvc(t)
	handler := WeeklyJobsDigestHandler(WeeklyJobsDigestDeps{
		Svc: svc,
		Lister: &fakeUnpaidLister{rows: []UnpaidCandidate{
			{ID: "cnd_x", Country: "KE", Kinds: []string{"job"}, EmailDigest: "weekly", WeeklySummary: true, CommEmail: true},
		}},
		Jobs: &fakeNewJobsLister{byCountry: map[string][]eventsv1.DigestJob{
			"KE": {{CanonicalID: "j1", Title: "T", Country: "KE", Kind: "job", Slug: "t"}},
		}},
		Stats:          &fakeWeeklyStats{err: errors.New("weekly stats down")},
		DefaultCadence: "weekly",
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/candidates/weekly_jobs_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp weeklyJobsDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Emitted != 1 {
		t.Fatalf("resp=%+v, want emitted=1 even when stats lister errors", resp)
	}
}

func TestWeeklyJobsDigestRejectsNonPost(t *testing.T) {
	_, svc, _ := newDigestSvc(t)
	handler := WeeklyJobsDigestHandler(WeeklyJobsDigestDeps{
		Svc:    svc,
		Lister: &fakeUnpaidLister{},
		Jobs:   &fakeNewJobsLister{byCountry: map[string][]eventsv1.DigestJob{}},
		Stats:  &fakeWeeklyStats{},
	})
	req := httptest.NewRequest(http.MethodGet, "/_admin/candidates/weekly_jobs_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405", rec.Code)
	}
}
