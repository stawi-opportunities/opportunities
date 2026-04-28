package counters

import (
	"context"
	"testing"

	"github.com/pitabwire/frame/cache"
)

// startCounters returns a *Counters wired against Frame's
// in-memory raw cache. The InMemoryCache implements the same
// cache.RawCache contract as the production valkey driver
// (Increment, Get, Expire) so the tests exercise the package's full
// surface without an external Redis-compatible server.
func startCounters(t *testing.T) (*Counters, cache.RawCache) {
	t.Helper()
	raw := cache.NewInMemoryCache()
	t.Cleanup(func() { _ = raw.Close() })
	c := NewFromCache(raw)
	if c == nil {
		t.Fatalf("NewFromCache returned nil")
	}
	return c, raw
}

func TestCounters_NilSafe(t *testing.T) {
	var c *Counters
	if err := c.IncrView(context.Background(), "abc"); err != nil {
		t.Errorf("nil IncrView returned err: %v", err)
	}
	if err := c.IncrApply(context.Background(), "abc"); err != nil {
		t.Errorf("nil IncrApply returned err: %v", err)
	}
	stats, err := c.GetStats(context.Background(), "abc")
	if err != nil {
		t.Errorf("nil GetStats returned err: %v", err)
	}
	if stats != (Stats{}) {
		t.Errorf("nil GetStats returned %+v want zero-value", stats)
	}
}

func TestCounters_NewClient_EmptyURL(t *testing.T) {
	c, err := NewClient("")
	if err != nil {
		t.Errorf("expected nil error for empty URL, got %v", err)
	}
	if c != nil {
		t.Errorf("expected nil *Counters for empty URL, got %+v", c)
	}
}

func TestCounters_IncrViewAndStats(t *testing.T) {
	c, _ := startCounters(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := c.IncrView(ctx, "abc"); err != nil {
			t.Fatalf("IncrView: %v", err)
		}
	}

	stats, err := c.GetStats(ctx, "abc")
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.ViewsTotal != 3 || stats.Views24h != 3 {
		t.Errorf("stats=%+v want views_total=3 views_24h=3", stats)
	}
}

func TestCounters_IncrApply(t *testing.T) {
	c, _ := startCounters(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = c.IncrApply(ctx, "abc")
	}
	stats, _ := c.GetStats(ctx, "abc")
	if stats.AppliesTotal != 5 {
		t.Errorf("AppliesTotal=%d want 5", stats.AppliesTotal)
	}
}

func TestCounters_GetStatsBatch(t *testing.T) {
	c, _ := startCounters(t)
	ctx := context.Background()

	_ = c.IncrView(ctx, "a")
	_ = c.IncrView(ctx, "a")
	_ = c.IncrApply(ctx, "a")
	_ = c.IncrView(ctx, "b")

	stats, err := c.GetStatsBatch(ctx, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("GetStatsBatch: %v", err)
	}
	if stats["a"].ViewsTotal != 2 || stats["a"].AppliesTotal != 1 {
		t.Errorf("a=%+v want views=2 applies=1", stats["a"])
	}
	if stats["b"].ViewsTotal != 1 {
		t.Errorf("b views=%d want 1", stats["b"].ViewsTotal)
	}
	if stats["c"].ViewsTotal != 0 || stats["c"].Views24h != 0 {
		t.Errorf("c expected zero, got %+v", stats["c"])
	}
}

func TestCounters_GetStatsBatch_EmptySlugs(t *testing.T) {
	c, _ := startCounters(t)
	stats, err := c.GetStatsBatch(context.Background(), nil)
	if err != nil {
		t.Errorf("err on empty slugs: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty map, got %+v", stats)
	}
}
