package storage

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"stawi.jobs/internal/domain"
)

type MemoryStore struct {
	mu              sync.RWMutex
	nextSourceID    int64
	nextCrawlID     int64
	nextPayloadID   int64
	nextVariantID   int64
	nextClusterID   int64
	nextCanonicalID int64
	sources         map[int64]domain.Source
	crawlJobs       map[int64]domain.CrawlJob
	payloads        map[int64]domain.RawPayload
	variants        map[int64]domain.JobVariant
	hardKeys        map[string]int64
	clusters        map[int64]domain.JobCluster
	canonical       map[int64]domain.CanonicalJob
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sources:   make(map[int64]domain.Source),
		crawlJobs: make(map[int64]domain.CrawlJob),
		payloads:  make(map[int64]domain.RawPayload),
		variants:  make(map[int64]domain.JobVariant),
		hardKeys:  make(map[string]int64),
		clusters:  make(map[int64]domain.JobCluster),
		canonical: make(map[int64]domain.CanonicalJob),
	}
}

func (m *MemoryStore) UpsertSource(_ context.Context, src domain.Source) (domain.Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.sources {
		if existing.BaseURL == src.BaseURL && existing.Type == src.Type {
			src.ID = existing.ID
			src.CreatedAt = existing.CreatedAt
			src.UpdatedAt = time.Now().UTC()
			m.sources[src.ID] = src
			return src, nil
		}
	}
	m.nextSourceID++
	src.ID = m.nextSourceID
	now := time.Now().UTC()
	src.CreatedAt = now
	src.UpdatedAt = now
	if src.Status == "" {
		src.Status = domain.SourceActive
	}
	m.sources[src.ID] = src
	return src, nil
}

func (m *MemoryStore) ListDueSources(_ context.Context, now time.Time, limit int) ([]domain.Source, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.Source, 0, len(m.sources))
	for _, src := range m.sources {
		if src.Status == domain.SourceActive && !src.NextCrawlAt.After(now) {
			items = append(items, src)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].NextCrawlAt.Before(items[j].NextCrawlAt) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (m *MemoryStore) ListSources(_ context.Context, limit int) ([]domain.Source, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.Source, 0, len(m.sources))
	for _, src := range m.sources {
		items = append(items, src)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (m *MemoryStore) GetSource(_ context.Context, id int64) (domain.Source, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sources[id]
	if !ok {
		return domain.Source{}, errors.New("source not found")
	}
	return s, nil
}

func (m *MemoryStore) TouchSource(_ context.Context, id int64, next time.Time, health float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sources[id]
	if !ok {
		return errors.New("source not found")
	}
	now := time.Now().UTC()
	s.LastSeenAt = &now
	s.NextCrawlAt = next
	s.HealthScore = health
	s.UpdatedAt = now
	m.sources[id] = s
	return nil
}

func (m *MemoryStore) CreateCrawlJob(_ context.Context, job domain.CrawlJob) (domain.CrawlJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextCrawlID++
	now := time.Now().UTC()
	job.ID = m.nextCrawlID
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.Status == "" {
		job.Status = domain.CrawlScheduled
	}
	m.crawlJobs[job.ID] = job
	return job, nil
}

func (m *MemoryStore) StartCrawlJob(_ context.Context, id int64, startedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.crawlJobs[id]
	if !ok {
		return errors.New("crawl job not found")
	}
	job.StartedAt = &startedAt
	job.Status = domain.CrawlRunning
	job.UpdatedAt = time.Now().UTC()
	m.crawlJobs[id] = job
	return nil
}

func (m *MemoryStore) FinishCrawlJob(_ context.Context, id int64, status domain.CrawlJobStatus, finishedAt time.Time, errCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.crawlJobs[id]
	if !ok {
		return errors.New("crawl job not found")
	}
	job.FinishedAt = &finishedAt
	job.Status = status
	job.ErrorCode = errCode
	job.UpdatedAt = time.Now().UTC()
	m.crawlJobs[id] = job
	return nil
}

func (m *MemoryStore) StoreRawPayload(_ context.Context, payload domain.RawPayload) (domain.RawPayload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextPayloadID++
	payload.ID = m.nextPayloadID
	m.payloads[payload.ID] = payload
	return payload, nil
}

func (m *MemoryStore) UpsertVariant(_ context.Context, variant domain.JobVariant) (domain.JobVariant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hardKey := domain.BuildHardKey(variant.Company, variant.Title, variant.LocationText, variant.ExternalJobID)
	if id, ok := m.hardKeys[hardKey]; ok {
		existing := m.variants[id]
		variant.ID = existing.ID
		m.variants[id] = variant
		return variant, nil
	}
	m.nextVariantID++
	variant.ID = m.nextVariantID
	m.variants[variant.ID] = variant
	m.hardKeys[hardKey] = variant.ID
	return variant, nil
}

func (m *MemoryStore) GetVariantByHardKey(_ context.Context, hardKey string) (*domain.JobVariant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.hardKeys[hardKey]
	if !ok {
		return nil, nil
	}
	v := m.variants[id]
	return &v, nil
}

func (m *MemoryStore) CreateCluster(_ context.Context, canonicalVariantID int64, confidence float64) (domain.JobCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextClusterID++
	c := domain.JobCluster{
		ID:                 m.nextClusterID,
		CanonicalVariantID: canonicalVariantID,
		Confidence:         confidence,
		UpdatedAt:          time.Now().UTC(),
	}
	m.clusters[c.ID] = c
	return c, nil
}

func (m *MemoryStore) BindVariantToCluster(_ context.Context, variantID, clusterID int64, matchType string, score float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, vok := m.variants[variantID]
	_, cok := m.clusters[clusterID]
	if !vok || !cok {
		return errors.New("cluster or variant not found")
	}
	_ = matchType
	_ = score
	return nil
}

func (m *MemoryStore) UpdateCanonicalJob(_ context.Context, job domain.CanonicalJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job.ID == 0 {
		m.nextCanonicalID++
		job.ID = m.nextCanonicalID
	}
	m.canonical[job.ID] = job
	return nil
}

func (m *MemoryStore) SearchCanonicalJobs(_ context.Context, query string, limit int) ([]domain.CanonicalJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 20
	}
	result := make([]domain.CanonicalJob, 0, limit)
	for _, job := range m.canonical {
		if query == "" || contains(job.Title, query) || contains(job.Company, query) || contains(job.Description, query) {
			result = append(result, job)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func contains(a, b string) bool {
	a = domain.NormalizeToken(a)
	b = domain.NormalizeToken(b)
	return b == "" || (a != "" && b != "" && (a == b || len(a) >= len(b) && (index(a, b) >= 0)))
}

func index(s, t string) int {
	for i := 0; i+len(t) <= len(s); i++ {
		if s[i:i+len(t)] == t {
			return i
		}
	}
	return -1
}
