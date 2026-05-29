// Package freshness computes the adaptive-recrawl score for a source.
//
// Score is a pure function of recent observed signals plus the
// source's tier classification. Higher score = crawl sooner.
// 1.0 = crawl at min_interval; 0.0 = crawl at max_interval; values
// in between interpolate.
//
// Signals (over the last 7 days):
//   - Crawls7d         : how many crawls actually ran
//   - Variants7d       : variants discovered (any current_stage)
//   - Accepted7d       : variants that reached current_stage='published'
//   - Rejected7d       : variants the extractor explicitly rejected
//   - LastNewVariantAt : age of the most recent fresh variant
//
// Tier (1/2/3 per the future source classification — accepted as
// an int so the caller can pass 2 (neutral) until the column exists):
//   - Tier 1 (structured ATS API) gets a +0.1 floor; we always want
//     to revisit these.
//   - Tier 2 (default / unset) is neutral.
//   - Tier 3 (manual curation) gets a -0.1 ceiling; they rarely
//     pay off above weekly cadence.
package freshness

import (
	"math"
	"time"
)

// SourceSignals is the rolling 7-day rollup the scheduler reads from
// the crawl_signals materialized view.
type SourceSignals struct {
	Crawls7d         int
	Variants7d       int
	Accepted7d       int
	Rejected7d       int
	LastNewVariantAt *time.Time // nil = no new variant in 7d
}

// Score returns a value in [0.0, 1.0]. Pure function — no I/O, no
// time.Now(); the caller passes "now" so the same inputs always
// produce the same output (testability).
//
// The model is intentionally simple — we can replace it without
// changing the scheduler:
//
//	freshness = clamp(variants_per_crawl * 0.5 + accept_rate * 0.3 + recency * 0.2)
//	then nudge ± 0.1 by tier.
func Score(s SourceSignals, tier int, now time.Time) float64 {
	// variants_per_crawl — capped at 5 because beyond that we're
	// already at maximum cadence.
	var vpc float64
	if s.Crawls7d > 0 {
		vpc = float64(s.Variants7d) / float64(s.Crawls7d)
	}
	if vpc > 5 {
		vpc = 5
	}
	vpcSignal := vpc / 5.0 // normalized to [0,1]

	// accept_rate — penalises sources that yield mostly rejections.
	var acceptRate float64
	if s.Variants7d > 0 {
		acceptRate = float64(s.Accepted7d) / float64(s.Variants7d)
	}

	// recency — exponential decay from last new variant. Half-life
	// 24h. No new variant in 7d → ~0.
	var recency float64
	if s.LastNewVariantAt != nil {
		ageHours := now.Sub(*s.LastNewVariantAt).Hours()
		if ageHours < 0 {
			ageHours = 0
		}
		// 2^(-age/24) — at 0h = 1.0, 24h = 0.5, 48h = 0.25, ...
		recency = math.Pow(2, -ageHours/24.0)
	}

	raw := vpcSignal*0.5 + acceptRate*0.3 + recency*0.2

	// Tier nudge. Tiers other than 1 and 3 are treated as neutral.
	switch tier {
	case 1:
		raw += 0.10
	case 3:
		raw -= 0.10
	}

	return clamp(raw, 0.0, 1.0)
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
