package freshness_test

import (
	"math"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/freshness"
)

func TestNextCrawlAt_HotScore_FloorInterval(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	next := freshness.NextCrawlAt(1.0, now, 15, 10080)
	delta := next.Sub(now).Minutes()
	if math.Abs(delta-15) > 1 {
		t.Errorf("score=1.0: next-now = %fm; want ~15m", delta)
	}
}

func TestNextCrawlAt_ColdScore_CeilingInterval(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	next := freshness.NextCrawlAt(0.0, now, 15, 10080)
	delta := next.Sub(now).Minutes()
	if math.Abs(delta-10080) > 100 {
		t.Errorf("score=0.0: next-now = %fm; want ~10080m", delta)
	}
}

func TestNextCrawlAt_Midpoint_GeometricNotLinear(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	next := freshness.NextCrawlAt(0.5, now, 15, 10080)
	delta := next.Sub(now).Minutes()
	// Geometric midpoint of (15, 10080) = sqrt(15*10080) ~ 388.85
	if math.Abs(delta-388.85) > 50 {
		t.Errorf("score=0.5: next-now = %fm; want ~389m (geometric mid)", delta)
	}
}

func TestNextCrawlAt_NeverBelowMin(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	next := freshness.NextCrawlAt(99.0, now, 15, 10080) // bogus score
	if next.Sub(now).Minutes() < 15 {
		t.Errorf("clamp failed: next-now = %f", next.Sub(now).Minutes())
	}
}
