package freshness_test

import (
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/freshness"
)

func TestScore_NoSignals_NeutralWithTierNudge(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	s := freshness.SourceSignals{}

	if got := freshness.Score(s, 2, now); got != 0 {
		t.Errorf("tier 2 zero signals: got %f; want 0", got)
	}
	if got := freshness.Score(s, 1, now); got != 0.10 {
		t.Errorf("tier 1 zero signals: got %f; want 0.10 (floor nudge)", got)
	}
	if got := freshness.Score(s, 3, now); got != 0 {
		t.Errorf("tier 3 zero signals: got %f; want clamped 0", got)
	}
}

func TestScore_HotSource_NearOne(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-1 * time.Hour)
	s := freshness.SourceSignals{
		Crawls7d:         100,
		Variants7d:       500, // 5 per crawl = max vpc signal
		Accepted7d:       400, // 80% accept
		Rejected7d:       100,
		LastNewVariantAt: &fresh,
	}
	got := freshness.Score(s, 1, now)
	if got < 0.85 {
		t.Errorf("hot tier-1 source: got %f; want > 0.85", got)
	}
}

func TestScore_ColdSource_NearZero(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	s := freshness.SourceSignals{
		Crawls7d:         50,
		Variants7d:       0,
		LastNewVariantAt: nil,
	}
	got := freshness.Score(s, 3, now)
	if got > 0.05 {
		t.Errorf("cold tier-3 source: got %f; want ~0", got)
	}
}

func TestScore_RecencyDecay(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	base := freshness.SourceSignals{
		Crawls7d:   10,
		Variants7d: 10,
		Accepted7d: 10,
	}
	fresh := now
	day1 := now.Add(-24 * time.Hour)
	day3 := now.Add(-72 * time.Hour)
	base.LastNewVariantAt = &fresh
	sNow := freshness.Score(base, 2, now)
	base.LastNewVariantAt = &day1
	s1d := freshness.Score(base, 2, now)
	base.LastNewVariantAt = &day3
	s3d := freshness.Score(base, 2, now)
	if !(sNow > s1d && s1d > s3d) {
		t.Errorf("recency decay broken: now=%f day1=%f day3=%f", sNow, s1d, s3d)
	}
}
