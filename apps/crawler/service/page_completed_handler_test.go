package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// fakeSourceHealthRepo captures calls so tests can assert which
// "record" method ran.
type fakeSourceHealthRepo struct {
	success  []string
	failure  []string
	tuning   map[string]bool
	nextSeen map[string]time.Time
	statuses map[string]domain.SourceStatus

	// src, when set, is returned by GetByID (with the requested ID
	// stamped) so tests can shape status / failure counters.
	src *domain.Source
}

func newFakeHealthRepo() *fakeSourceHealthRepo {
	return &fakeSourceHealthRepo{
		tuning:   map[string]bool{},
		nextSeen: map[string]time.Time{},
		statuses: map[string]domain.SourceStatus{},
	}
}

func (r *fakeSourceHealthRepo) GetByID(_ context.Context, id string) (*domain.Source, error) {
	if r.src != nil {
		s := *r.src
		s.ID = id
		return &s, nil
	}
	return &domain.Source{BaseModel: domain.BaseModel{ID: id}, CrawlIntervalSec: 60, HealthScore: 1.0}, nil
}

// TransitionStatus is a real compare-and-set against the fake's current
// status (statuses map, else the shaped src, else active).
func (r *fakeSourceHealthRepo) TransitionStatus(_ context.Context, id string, from []domain.SourceStatus, to domain.SourceStatus) (bool, error) {
	cur, ok := r.statuses[id]
	if !ok {
		cur = domain.SourceActive
		if r.src != nil && r.src.Status != "" {
			cur = r.src.Status
		}
	}
	for _, f := range from {
		if f == cur {
			r.statuses[id] = to
			return true, nil
		}
	}
	return false, nil
}
func (r *fakeSourceHealthRepo) RecordSuccess(_ context.Context, id string, _ float64) error {
	r.success = append(r.success, id)
	return nil
}
func (r *fakeSourceHealthRepo) RecordFailure(_ context.Context, id string, _ float64, _ int) error {
	r.failure = append(r.failure, id)
	return nil
}
func (r *fakeSourceHealthRepo) FlagNeedsTuning(_ context.Context, id string, flag bool) error {
	r.tuning[id] = flag
	return nil
}
func (r *fakeSourceHealthRepo) UpdateNextCrawl(_ context.Context, id string, next, _ time.Time, _ float64) error {
	r.nextSeen[id] = next
	return nil
}

func TestPageCompletedSuccessRecordsSuccess(t *testing.T) {
	repo := newFakeHealthRepo()
	h := NewPageCompletedHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		SourceID: "s1", JobsFound: 10, JobsEmitted: 9, JobsRejected: 1,
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	if err := h.Execute(context.Background(), &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(repo.success) != 1 || repo.success[0] != "s1" {
		t.Fatalf("success not recorded: %+v", repo.success)
	}
	if len(repo.failure) != 0 {
		t.Fatalf("failure unexpectedly recorded: %+v", repo.failure)
	}
	if _, ok := repo.nextSeen["s1"]; !ok {
		t.Fatalf("next_crawl_at not set")
	}
}

func TestPageCompletedIteratorErrorRecordsFailure(t *testing.T) {
	repo := newFakeHealthRepo()
	h := NewPageCompletedHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		SourceID: "s2", ErrorCode: "iterator_failed", ErrorMessage: "timeout",
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	if err := h.Execute(context.Background(), &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(repo.success) != 0 {
		t.Fatalf("success unexpectedly recorded: %+v", repo.success)
	}
	if len(repo.failure) != 1 {
		t.Fatalf("failure not recorded: %+v", repo.failure)
	}
	if _, ok := repo.nextSeen["s2"]; !ok {
		t.Fatalf("next_crawl_at not stamped on error path")
	}
}

func TestPageCompletedHighRejectRateFlagsTuning(t *testing.T) {
	repo := newFakeHealthRepo()
	h := NewPageCompletedHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		SourceID: "s3", JobsFound: 10, JobsEmitted: 1, JobsRejected: 9, // 90% rejects
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	_ = h.Execute(context.Background(), &rm)

	if !repo.tuning["s3"] {
		t.Fatalf("needs_tuning not flagged")
	}
}

func TestPageCompletedPersistentFailureDegradesSource(t *testing.T) {
	repo := newFakeHealthRepo()
	repo.src = &domain.Source{
		CrawlIntervalSec:    60,
		HealthScore:         0.2,
		Status:              domain.SourceActive,
		ConsecutiveFailures: DegradeAfterFailures - 1, // this failure crosses the line
	}
	h := NewPageCompletedHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		SourceID: "s4", ErrorCode: "iterator_failed",
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	_ = h.Execute(context.Background(), &rm)

	if repo.statuses["s4"] != domain.SourceDegraded {
		t.Fatalf("source not demoted to degraded; statuses=%v", repo.statuses)
	}
}

func TestPageCompletedFewFailuresDoesNotDegrade(t *testing.T) {
	repo := newFakeHealthRepo()
	repo.src = &domain.Source{
		CrawlIntervalSec: 60, HealthScore: 0.8,
		Status: domain.SourceActive, ConsecutiveFailures: 0,
	}
	h := NewPageCompletedHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		SourceID: "s5", ErrorCode: "iterator_failed",
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	_ = h.Execute(context.Background(), &rm)

	if _, ok := repo.statuses["s5"]; ok {
		t.Fatalf("source demoted on first failure; statuses=%v", repo.statuses)
	}
}

func TestPageCompletedRecoveredSourcePromotedToActive(t *testing.T) {
	repo := newFakeHealthRepo()
	repo.src = &domain.Source{
		CrawlIntervalSec: 60,
		HealthScore:      0.75, // +0.1 success bump crosses the 0.8 recovery bar
		Status:           domain.SourceDegraded,
	}
	h := NewPageCompletedHandler(repo)

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, eventsv1.CrawlPageCompletedV1{
		SourceID: "s6", JobsFound: 10, JobsEmitted: 10,
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	_ = h.Execute(context.Background(), &rm)

	if repo.statuses["s6"] != domain.SourceActive {
		t.Fatalf("recovered source not promoted; statuses=%v", repo.statuses)
	}
}

func TestPageCompletedSourceNotEligibleIsNotAFailure(t *testing.T) {
	repo := newFakeHealthRepo()
	repo.src = &domain.Source{Status: domain.SourceDisabled, ConsecutiveFailures: 4, CrawlIntervalSec: 60, HealthScore: 0.5}
	h := NewPageCompletedHandler(repo)

	runPageCompleted(t, h, eventsv1.CrawlPageCompletedV1{SourceID: "src-stopped", ErrorCode: "source_not_eligible"})

	if len(repo.failure) != 0 || len(repo.success) != 0 {
		t.Fatalf("health bookkeeping ran: failure=%v success=%v", repo.failure, repo.success)
	}
	if _, changed := repo.statuses["src-stopped"]; changed {
		t.Fatalf("status changed to %q", repo.statuses["src-stopped"])
	}
	if _, touched := repo.nextSeen["src-stopped"]; touched {
		t.Fatal("next_crawl_at rescheduled for a refused source")
	}
}

func TestPageCompletedFailureNeverOverwritesStoppedSource(t *testing.T) {
	repo := newFakeHealthRepo()
	// A real crawl failure lands for a source the operator just stopped.
	repo.src = &domain.Source{Status: domain.SourceDisabled, ConsecutiveFailures: 10, CrawlIntervalSec: 60, HealthScore: 0.5}
	h := NewPageCompletedHandler(repo)

	runPageCompleted(t, h, eventsv1.CrawlPageCompletedV1{SourceID: "src-stopped", ErrorCode: "iterator_failed"})

	if st, changed := repo.statuses["src-stopped"]; changed {
		t.Fatalf("stopped source was moved to %q", st)
	}
}

func runPageCompleted(t *testing.T, h *PageCompletedHandler, p eventsv1.CrawlPageCompletedV1) {
	t.Helper()
	raw, err := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicCrawlPageCompleted, p))
	if err != nil {
		t.Fatal(err)
	}
	rm := json.RawMessage(raw)
	if err := h.Execute(context.Background(), &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}
