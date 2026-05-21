package matching

import (
	"context"
	"sync"
	"time"
)

// MemoryDebouncer is a process-local debouncer suitable for development
// and single-instance test runs. Production deploys must replace it with
// a Valkey-backed implementation that is consistent across pods.
type MemoryDebouncer struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewMemoryDebouncer constructs a MemoryDebouncer.
func NewMemoryDebouncer() *MemoryDebouncer {
	return &MemoryDebouncer{seen: map[string]time.Time{}}
}

// Acquire returns true if candidateID has not been seen within ttl.
// If the last Acquire was within ttl, returns false (debounced).
// Implements Debouncer.
func (m *MemoryDebouncer) Acquire(_ context.Context, candidateID string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if last, ok := m.seen[candidateID]; ok && now.Sub(last) < ttl {
		return false, nil
	}
	m.seen[candidateID] = now
	return true, nil
}
