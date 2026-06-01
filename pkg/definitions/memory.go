package definitions

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryLoader is the in-memory test implementation. Production
// uses NewR2Loader; tests use this directly so they don't need R2.
type MemoryLoader struct {
	mu      sync.RWMutex
	data    map[Type]map[string]Entry
	bodies  map[string][]byte // key: type+":"+name+":"+version
	subs    map[Type][]func(name, version string)
	nextVer int
}

// NewMemoryLoader constructs an empty MemoryLoader.
func NewMemoryLoader() *MemoryLoader {
	return &MemoryLoader{
		data:   make(map[Type]map[string]Entry),
		bodies: make(map[string][]byte),
		subs:   make(map[Type][]func(name, version string)),
	}
}

// Start is a no-op for the in-memory implementation.
func (m *MemoryLoader) Start(_ context.Context) error { return nil }

// Stop is a no-op for the in-memory implementation.
func (m *MemoryLoader) Stop() {}

// Put is the test-only setter. Production R2 path uses
// admin-endpoint PUT + refresh to update the cache.
func (m *MemoryLoader) Put(t Type, name string, body []byte) string {
	m.mu.Lock()
	m.nextVer++
	version := fmt.Sprintf("v%d", m.nextVer)
	if m.data[t] == nil {
		m.data[t] = make(map[string]Entry)
	}
	m.data[t][name] = Entry{
		Type:      t,
		Name:      name,
		Version:   version,
		UpdatedAt: time.Now().UTC(),
		Size:      int64(len(body)),
	}
	m.bodies[string(t)+":"+name+":"+version] = body
	subs := append([]func(string, string){}, m.subs[t]...)
	m.mu.Unlock()
	for _, fn := range subs {
		fn(name, version)
	}
	return version
}

// Delete is the test-only remover. Production fires on
// definitions.changed.v1 with Action=delete.
func (m *MemoryLoader) Delete(t Type, name string) {
	m.mu.Lock()
	delete(m.data[t], name)
	subs := append([]func(string, string){}, m.subs[t]...)
	m.mu.Unlock()
	for _, fn := range subs {
		fn(name, "deleted")
	}
}

// Get returns the cached body + version for (type, name).
func (m *MemoryLoader) Get(_ context.Context, t Type, name string) ([]byte, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.data[t][name]
	if !ok {
		return nil, "", ErrNotFound
	}
	body := m.bodies[string(t)+":"+name+":"+entry.Version]
	return body, entry.Version, nil
}

// List returns every cached entry of a type, sorted by Name.
func (m *MemoryLoader) List(_ context.Context, t Type) ([]Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Entry, 0, len(m.data[t]))
	for _, e := range m.data[t] {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Invalidate is a no-op for the in-memory loader (cache is the source
// of truth). Exists to satisfy the admin-handler's loader interface
// without forcing tests to spin up R2.
func (m *MemoryLoader) Invalidate(_ context.Context, _ Type, _ string) error {
	return nil
}

// Subscribe registers a callback fired on Put/Delete.
func (m *MemoryLoader) Subscribe(t Type, fn func(name, version string)) func() {
	m.mu.Lock()
	m.subs[t] = append(m.subs[t], fn)
	idx := len(m.subs[t]) - 1
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		// Best-effort removal — slice tombstone OK for tests.
		if idx < len(m.subs[t]) {
			m.subs[t][idx] = func(string, string) {}
		}
	}
}
