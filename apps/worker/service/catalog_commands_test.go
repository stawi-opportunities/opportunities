package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

type fakeCommandStore struct {
	queue   []jobqueue.CatalogCommand
	runErr  error
	claimed []string
	failed  []string
	ran     []string

	dead          int64
	backlogChecks int
}

func (f *fakeCommandStore) ClaimCatalogCommand(context.Context, string, time.Duration) (*jobqueue.CatalogCommand, error) {
	if len(f.queue) == 0 {
		return nil, nil
	}
	c := f.queue[0]
	f.queue = f.queue[1:]
	f.claimed = append(f.claimed, c.ID)
	return &c, nil
}

func (f *fakeCommandStore) RunCatalogCommand(_ context.Context, c jobqueue.CatalogCommand) (jobqueue.CommandOutcome, error) {
	if f.runErr != nil {
		return "", f.runErr
	}
	f.ran = append(f.ran, c.ID)
	return jobqueue.CommandApplied, nil
}

func (f *fakeCommandStore) CommandBacklog(context.Context, time.Duration) (int64, int64, error) {
	f.backlogChecks++
	return f.dead, 0, nil
}

func (f *fakeCommandStore) FailCatalogCommand(_ context.Context, c jobqueue.CatalogCommand, _ error, _ int) error {
	f.failed = append(f.failed, c.ID)
	return nil
}

func TestCatalogCommandRunner_DrainsAllRunnable(t *testing.T) {
	st := &fakeCommandStore{queue: []jobqueue.CatalogCommand{{ID: "a"}, {ID: "b"}}}
	NewCatalogCommandRunner(st, time.Second).drain(context.Background())
	if len(st.ran) != 2 || len(st.failed) != 0 {
		t.Fatalf("ran=%v failed=%v", st.ran, st.failed)
	}
}

func TestCatalogCommandRunner_RecordsFailureAndContinues(t *testing.T) {
	st := &fakeCommandStore{queue: []jobqueue.CatalogCommand{{ID: "a"}, {ID: "b"}}, runErr: errors.New("product db down")}
	NewCatalogCommandRunner(st, time.Second).drain(context.Background())
	if len(st.failed) != 2 {
		t.Fatalf("failed=%v, want both recorded", st.failed)
	}
}

func TestCatalogCommandRunner_StopsOnCancelledContext(t *testing.T) {
	st := &fakeCommandStore{queue: []jobqueue.CatalogCommand{{ID: "a"}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewCatalogCommandRunner(st, time.Millisecond).Run(ctx); err != nil {
		t.Fatalf("Run returned %v on shutdown", err)
	}
	if len(st.claimed) != 0 {
		t.Fatalf("claimed %v after cancellation", st.claimed)
	}
}

func TestCatalogCommandRunner_BacklogAlertIsRateLimited(t *testing.T) {
	st := &fakeCommandStore{dead: 1}
	r := NewCatalogCommandRunner(st, time.Second)
	r.drain(context.Background())
	r.drain(context.Background())
	if st.backlogChecks != 1 {
		t.Fatalf("backlog checked %d times within the alert window, want 1", st.backlogChecks)
	}
}
