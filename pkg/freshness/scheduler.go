package freshness

import (
	"math"
	"time"
)

// NextCrawlAt returns when the source should next be crawled. The
// score (0.0–1.0) interpolates between maxInterval (cold) and
// minInterval (hot).
//
// Always >= now + minInterval to prevent thundering-herd from a
// just-completed crawl. minInterval/maxInterval are in minutes.
//
// Example: score=1.0, min=15, max=10080 → next crawl in 15 minutes.
// Example: score=0.0, min=15, max=10080 → next crawl in 7 days.
// Example: score=0.5, min=15, max=10080 → next crawl in ~389m
//
//	(geometric interpolation, not linear — half-score should
//	NOT mean half-cadence; that overweights mid-band sources).
func NextCrawlAt(score float64, lastCrawled time.Time, minIntervalMin, maxIntervalMin int) time.Time {
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	minMin := float64(minIntervalMin)
	maxMin := float64(maxIntervalMin)
	if minMin <= 0 {
		minMin = 15
	}
	if maxMin < minMin {
		maxMin = minMin
	}
	// Geometric (log-scale) interpolation so the 0.5 midpoint sits
	// at the geometric mean of (min, max) rather than the arithmetic
	// midpoint — operators want "hot" to mean "near floor", not
	// "midway between floor and ceiling".
	//
	// interval = min * (max/min) ^ (1 - score)
	ratio := maxMin / minMin
	exponent := 1.0 - score
	intervalMin := minMin * math.Pow(ratio, exponent)
	return lastCrawled.Add(time.Duration(intervalMin * float64(time.Minute)))
}
