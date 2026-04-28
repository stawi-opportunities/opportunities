package counters

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// startRedis stands up an in-process miniredis and returns a wired
// *Counters plus the addr (so callers can inspect keys directly).
func startRedis(t *testing.T) (*Counters, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := NewClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c == nil {
		t.Fatalf("NewClient returned nil")
	}
	return c, mr
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
	c, mr := startRedis(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := c.IncrView(ctx, "abc"); err != nil {
			t.Fatalf("IncrView: %v", err)
		}
	}
	if got, err := mr.Get("view:abc"); err != nil || got != "3" {
		t.Errorf("view:abc=%q err=%v want 3", got, err)
	}
	if got, err := mr.Get("view:abc:24h"); err != nil || got != "3" {
		t.Errorf("view:abc:24h=%q err=%v want 3", got, err)
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
	c, mr := startRedis(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = c.IncrApply(ctx, "abc")
	}
	if got, err := mr.Get("apply:abc"); err != nil || got != "5" {
		t.Errorf("apply:abc=%q err=%v want 5", got, err)
	}
	stats, _ := c.GetStats(ctx, "abc")
	if stats.AppliesTotal != 5 {
		t.Errorf("AppliesTotal=%d want 5", stats.AppliesTotal)
	}
}

func TestCounters_GetStatsBatch(t *testing.T) {
	c, _ := startRedis(t)
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
	c, _ := startRedis(t)
	stats, err := c.GetStatsBatch(context.Background(), nil)
	if err != nil {
		t.Errorf("err on empty slugs: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty map, got %+v", stats)
	}
}
