// Package connectors defines the Connector interface and supporting types used
// by all source-specific crawl implementations.
package connectors

import (
	"context"
	"encoding/json"
	"sync"

	"stawi.jobs/pkg/domain"
)

// CrawlIterator is the page-level iterator returned by Connector.Crawl.
// Callers advance the iterator with Next and read results via Jobs.
type CrawlIterator interface {
	// Next fetches the next batch of jobs. Returns false when there are no
	// more pages or when an error has occurred.
	Next(ctx context.Context) bool

	// Jobs returns the batch of jobs fetched by the most recent Next call.
	Jobs() []domain.ExternalJob

	// RawPayload returns the raw HTTP response body for the current page.
	RawPayload() []byte

	// HTTPStatus returns the HTTP status code of the most recent request.
	HTTPStatus() int

	// Err returns the first error encountered during iteration, if any.
	Err() error

	// Cursor returns an opaque JSON token that can be used to resume
	// pagination from the current position. Nil means "start from beginning".
	Cursor() json.RawMessage
}

// Connector is implemented by every source-specific crawl driver.
type Connector interface {
	// Type returns the SourceType this connector handles.
	Type() domain.SourceType

	// Crawl begins a crawl against the given source and returns an iterator
	// over the resulting job pages.
	Crawl(ctx context.Context, source domain.Source) CrawlIterator
}

// ----------------------------------------------------------------------------
// Registry
// ----------------------------------------------------------------------------

// Registry is a thread-safe map of SourceType to Connector.
type Registry struct {
	mu         sync.RWMutex
	connectors map[domain.SourceType]Connector
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		connectors: make(map[domain.SourceType]Connector),
	}
}

// Register adds or replaces the connector for the given SourceType.
func (r *Registry) Register(c Connector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connectors[c.Type()] = c
}

// Get returns the connector for the given SourceType and whether it exists.
func (r *Registry) Get(t domain.SourceType) (Connector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.connectors[t]
	return c, ok
}

// Types returns a snapshot of all registered SourceTypes.
func (r *Registry) Types() []domain.SourceType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]domain.SourceType, 0, len(r.connectors))
	for t := range r.connectors {
		types = append(types, t)
	}
	return types
}

// ----------------------------------------------------------------------------
// SinglePageIterator
// ----------------------------------------------------------------------------

// SinglePageIterator wraps a single batch of jobs for connectors that do not
// paginate (i.e., the entire result set arrives in one HTTP response).
type SinglePageIterator struct {
	jobs       []domain.ExternalJob
	raw        []byte
	httpStatus int
	err        error
	consumed   bool
}

// NewSinglePageIterator creates a SinglePageIterator for a single-page result.
func NewSinglePageIterator(jobs []domain.ExternalJob, raw []byte, status int, err error) *SinglePageIterator {
	return &SinglePageIterator{
		jobs:       jobs,
		raw:        raw,
		httpStatus: status,
		err:        err,
	}
}

// Next returns true the first time it is called (yielding the wrapped batch),
// and false on all subsequent calls. If the iterator was constructed with a
// non-nil error, Next always returns false.
func (s *SinglePageIterator) Next(_ context.Context) bool {
	if s.err != nil || s.consumed {
		return false
	}
	s.consumed = true
	return true
}

// Jobs returns the single batch of jobs.
func (s *SinglePageIterator) Jobs() []domain.ExternalJob { return s.jobs }

// RawPayload returns the raw HTTP response body.
func (s *SinglePageIterator) RawPayload() []byte { return s.raw }

// HTTPStatus returns the HTTP status code.
func (s *SinglePageIterator) HTTPStatus() int { return s.httpStatus }

// Err returns the error provided at construction time, if any.
func (s *SinglePageIterator) Err() error { return s.err }

// Cursor always returns nil — single-page sources have no pagination state.
func (s *SinglePageIterator) Cursor() json.RawMessage { return nil }
