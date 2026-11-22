package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

func TestRunRetentionLoop_SweepsEachTickAndSurvivesErrors(t *testing.T) {
	var calls atomic.Int32
	orig := retentionSweeper
	t.Cleanup(func() { retentionSweeper = orig })
	origDelay := retentionFirstDelay
	t.Cleanup(func() { retentionFirstDelay = origDelay })
	retentionFirstDelay = func(time.Duration) time.Duration { return time.Millisecond }
	retentionSweeper = func(context.Context, func(context.Context, bool) *gorm.DB, repository.RetentionPolicy) (repository.RetentionResult, error) {
		if calls.Add(1) == 1 {
			return repository.RetentionResult{"job_ingest_queue": 3}, errors.New("transient")
		}
		return repository.RetentionResult{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunRetentionLoop(ctx, nil, repository.DefaultRetentionPolicy(), 5*time.Millisecond) }()

	deadline := time.After(2 * time.Second)
	for calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d sweeps ran; a failed sweep must not stop the loop", calls.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop returned %v on shutdown, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("loop did not stop on cancellation")
	}
}

func TestRunRetentionLoop_FirstSweepWaitsItsDelay(t *testing.T) {
	var calls atomic.Int32
	orig := retentionSweeper
	t.Cleanup(func() { retentionSweeper = orig })
	retentionSweeper = func(context.Context, func(context.Context, bool) *gorm.DB, repository.RetentionPolicy) (repository.RetentionResult, error) {
		calls.Add(1)
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = RunRetentionLoop(ctx, nil, repository.DefaultRetentionPolicy(), time.Hour)
	if calls.Load() != 0 {
		t.Fatalf("swept %d times at startup; the first sweep waits its randomised delay", calls.Load())
	}
}

func TestRetentionFirstDelayBounds(t *testing.T) {
	for i := 0; i < 200; i++ {
		d := retentionFirstDelay(time.Hour)
		if d < time.Minute || d >= retentionFirstDelayMax {
			t.Fatalf("first delay %v outside [1m, 10m)", d)
		}
	}
	if d := retentionFirstDelay(30 * time.Second); d != 30*time.Second {
		t.Fatalf("short interval: first delay %v, want the interval", d)
	}
}
