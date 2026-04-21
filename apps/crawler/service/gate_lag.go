package service

import (
	"sync"
	"time"
)

// LagPoller converts consecutive depth samples into a consume-rate
// estimate. Used by the crawler to feed backpressure.Gate.UpdateLag.
// First sample for a topic returns rate=0 (the gate fails-open on
// unknown rate); subsequent samples produce real data.
type LagPoller struct {
	mu        sync.Mutex
	lastDepth map[string]int64
	lastAt    map[string]time.Time
}

// NewLagPoller constructs a ready-to-use LagPoller.
func NewLagPoller() *LagPoller {
	return &LagPoller{
		lastDepth: map[string]int64{},
		lastAt:    map[string]time.Time{},
	}
}

// Sample records (depth, now) for topic and returns (depth, rate).
// rate is the events-drained-per-second since the previous sample,
// clamped to [0.01, +inf) to keep drain_time bounded on idle topics.
// The first call for a topic returns rate=0, signalling "unknown" so
// the gate uses its fail-open back-compat path.
func (p *LagPoller) Sample(topic string, depth int64, now time.Time) (int64, float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	prev, ok := p.lastDepth[topic]
	prevAt := p.lastAt[topic]

	p.lastDepth[topic] = depth
	p.lastAt[topic] = now

	if !ok || prevAt.IsZero() {
		return depth, 0
	}

	dt := now.Sub(prevAt).Seconds()
	if dt <= 0 {
		return depth, 0
	}

	consumed := float64(prev-depth) / dt
	if consumed < 0.01 {
		consumed = 0.01
	}
	return depth, consumed
}
