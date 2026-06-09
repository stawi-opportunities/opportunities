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

var _ connectors.CrawlIterator = (*ConnectorIterator)(nil)

// NewConnectorIterator binds exec to src.
func NewConnectorIterator(exec *recipe.Executor, src domain.Source) *ConnectorIterator {
	return &ConnectorIterator{exec: exec, src: src}
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
