package otprendezvous

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPutThenPollConsumes(t *testing.T) {
	r := NewInMemory()
	ctx := context.Background()
	key := Key("a@b.com", "cloudflare")

	if err := r.Put(ctx, key, "51iL5qrq"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := r.Poll(ctx, key, time.Second)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got != "51iL5qrq" { // case must be preserved
		t.Fatalf("Poll = %q, want 51iL5qrq", got)
	}

	// Single-use: a second Poll must time out, proving consume deleted it.
	if _, err := r.Poll(ctx, key, 50*time.Millisecond); !errors.Is(err, ErrTimeout) {
		t.Fatalf("second Poll err = %v, want ErrTimeout", err)
	}
}

func TestPollTimeout(t *testing.T) {
	r := NewInMemory()
	_, err := r.Poll(context.Background(), Key("x@y.com", "acme"), 50*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Poll err = %v, want ErrTimeout", err)
	}
}

func TestClearDropsStaleCode(t *testing.T) {
	r := NewInMemory()
	ctx := context.Background()
	key := Key("a@b.com", "cloudflare")

	_ = r.Put(ctx, key, "stale")
	if err := r.Clear(ctx, key); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := r.Poll(ctx, key, 50*time.Millisecond); !errors.Is(err, ErrTimeout) {
		t.Fatalf("after Clear, Poll err = %v, want ErrTimeout", err)
	}
}

func TestPollCancelled(t *testing.T) {
	r := NewInMemory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Poll(ctx, Key("a@b.com", "x"), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Poll err = %v, want context.Canceled", err)
	}
}
