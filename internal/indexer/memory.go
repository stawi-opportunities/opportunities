package indexer

import (
	"context"
	"sync"

	"stawi.jobs/internal/domain"
)

type MemoryIndexer struct {
	mu   sync.RWMutex
	jobs map[int64]domain.CanonicalJob
}

func NewMemoryIndexer() *MemoryIndexer {
	return &MemoryIndexer{jobs: make(map[int64]domain.CanonicalJob)}
}

func (m *MemoryIndexer) IndexCanonicalJob(_ context.Context, job domain.CanonicalJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ClusterID] = job
	return nil
}

func (m *MemoryIndexer) Search(_ context.Context, query string, limit int) ([]domain.CanonicalJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 20
	}
	out := make([]domain.CanonicalJob, 0, limit)
	for _, j := range m.jobs {
		out = append(out, j)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
