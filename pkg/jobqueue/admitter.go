package jobqueue

import (
	"context"
	"time"
)

type Admitter interface {
	Admit(context.Context, string, int) (int, time.Duration)
}

// CapacityAdmitter prevents crawl requests from starting while PostgreSQL
// processing work is beyond its depth or age budget.
type CapacityAdmitter struct {
	store     *Store
	max       int64
	oldestMax time.Duration
	next      Admitter
}

func NewCapacityAdmitter(store *Store, max int64, oldestMax time.Duration, next Admitter) *CapacityAdmitter {
	return &CapacityAdmitter{store: store, max: max, oldestMax: oldestMax, next: next}
}

func (a *CapacityAdmitter) Admit(ctx context.Context, topic string, want int) (int, time.Duration) {
	stats, err := a.store.Stats(ctx)
	if err != nil || (a.max > 0 && stats.Pending >= a.max) || (a.oldestMax > 0 && stats.OldestAge >= a.oldestMax) {
		return 0, 30 * time.Second
	}
	if a.next != nil {
		return a.next.Admit(ctx, topic, want)
	}
	return want, 0
}
