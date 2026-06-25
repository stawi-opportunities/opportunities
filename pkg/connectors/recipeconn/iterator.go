package recipeconn

import (
	"context"
	"encoding/json"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

// ConnectorIterator binds an Executor to a single source and exposes it as a
// connectors.CrawlIterator so the crawl pipeline can drive it with no changes.
type ConnectorIterator struct {
	exec *recipe.Executor
	src  domain.Source

	state recipe.PageState
	done  bool

	items  []domain.ExternalOpportunity
	raw    []byte
	status int
	err    error
}

var (
	_ connectors.CrawlIterator         = (*ConnectorIterator)(nil)
	_ connectors.CheckpointableIterator = (*ConnectorIterator)(nil)
)

// NewConnectorIterator binds exec to src, starting from the beginning.
func NewConnectorIterator(exec *recipe.Executor, src domain.Source) *ConnectorIterator {
	return &ConnectorIterator{exec: exec, src: src}
}

// NewConnectorIteratorResume binds exec to src and resumes from a cursor
// previously produced by Checkpoint() (stored in a crawl_runs row). An empty
// cursor is equivalent to NewConnectorIterator. This is how a recipe source —
// like the rest of the pipeline now — picks up a bounded crawl across slices
// and across process restarts instead of restarting from page 0 / tenant 0.
func NewConnectorIteratorResume(exec *recipe.Executor, src domain.Source, cursor json.RawMessage) (*ConnectorIterator, error) {
	st, err := recipe.UnmarshalState(cursor)
	if err != nil {
		return nil, err
	}
	return &ConnectorIterator{exec: exec, src: src, state: st}, nil
}

// Checkpoint serializes the next-page state so the crawl handler can persist it
// and resume later. it.state is the position of the page to fetch next (Next()
// advances it on every successful, non-final page), which is exactly the resume
// point. Returns nil only if the opaque state fails to serialize (never expected).
func (it *ConnectorIterator) Checkpoint() *connectors.CheckpointState {
	cur, err := recipe.MarshalState(it.state)
	if err != nil {
		return nil
	}
	pageIdx, lastURL := recipe.StateProgress(it.state)
	return &connectors.CheckpointState{Cursor: cur, PageIdx: pageIdx, LastURL: lastURL}
}

func (it *ConnectorIterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}
	items, raw, status, next, done, err := it.exec.Page(ctx, it.src, it.state)
	it.items, it.raw, it.status = items, raw, status
	if err != nil {
		it.err = err
		return false
	}
	if done {
		it.done = true
	} else {
		it.state = next
	}
	return true
}

func (it *ConnectorIterator) Items() []domain.ExternalOpportunity { return it.items }
func (it *ConnectorIterator) RawPayload() []byte                  { return it.raw }
func (it *ConnectorIterator) HTTPStatus() int                     { return it.status }
func (it *ConnectorIterator) Err() error                          { return it.err }
func (it *ConnectorIterator) Cursor() json.RawMessage             { return nil }
func (it *ConnectorIterator) Content() *content.Extracted         { return nil }
