// Package connectors defines the Connector interface and supporting types used
// by all source-specific crawl implementations.
package connectors

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// CrawlIterator is the page-level iterator returned by Connector.Crawl.
// Callers advance the iterator with Next and read results via Items.
type CrawlIterator interface {
	// Next fetches the next batch of items. Returns false when there are no
	// more pages or when an error has occurred.
	Next(ctx context.Context) bool

	// Items returns the batch of opportunity items fetched by the most recent
	// Next call.
	Items() []domain.ExternalOpportunity

	// RawPayload returns the raw HTTP response body for the current page.
	RawPayload() []byte

	// HTTPStatus returns the HTTP status code of the most recent request.
	HTTPStatus() int

	// Err returns the first error encountered during iteration, if any.
	Err() error

	// Cursor returns an opaque JSON token that can be used to resume
	// pagination from the current position. Nil means "start from beginning".
	Cursor() json.RawMessage

	// Content returns the extracted content for the current page, or nil if
	// content extraction was not performed.
	Content() *content.Extracted
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
	jobs       []domain.ExternalOpportunity
	raw        []byte
	httpStatus int
	err        error
	consumed   bool
	extracted  *content.Extracted
}

// NewSinglePageIterator creates a SinglePageIterator for a single-page result.
// If raw bytes are provided it will attempt content extraction: HTML payloads
// are run through content.ExtractFromHTML; everything else is treated as JSON.
func NewSinglePageIterator(jobs []domain.ExternalOpportunity, raw []byte, status int, err error) *SinglePageIterator {
	var ext *content.Extracted
	if len(raw) > 0 {
		rawStr := string(raw)
		if strings.Contains(rawStr, "<") {
			ext, _ = content.ExtractFromHTML(rawStr)
		} else {
			ext = content.ExtractFromJSON(rawStr, "")
		}
	}
	return &SinglePageIterator{
		jobs:       jobs,
		raw:        raw,
		httpStatus: status,
		err:        err,
		extracted:  ext,
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

// Items returns the single batch of opportunity items.
func (s *SinglePageIterator) Items() []domain.ExternalOpportunity { return s.jobs }

// RawPayload returns the raw HTTP response body.
func (s *SinglePageIterator) RawPayload() []byte { return s.raw }

// HTTPStatus returns the HTTP status code.
func (s *SinglePageIterator) HTTPStatus() int { return s.httpStatus }

// Err returns the error provided at construction time, if any.
func (s *SinglePageIterator) Err() error { return s.err }

// Cursor always returns nil — single-page sources have no pagination state.
func (s *SinglePageIterator) Cursor() json.RawMessage { return nil }

// Content returns the extracted content for the single page.
func (s *SinglePageIterator) Content() *content.Extracted { return s.extracted }
