// Package recipeconn adapts a recipe.Executor to a page-by-page iterator the
// crawl pipeline can drive (a source-bound variant of connectors.CrawlIterator).
// Wiring the executor into source resolution is Plan 3, once Source carries its
// recipe.
package recipeconn

import (
	"context"

	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

// Iterator drives a recipe.Executor page-by-page and exposes each page through
// a CrawlIterator-style interface.
type Iterator struct {
	exec *recipe.Executor

	state recipe.PageState
	done  bool

	items  []domain.ExternalOpportunity
	raw    []byte
	status int
	err    error
}

// New builds an Iterator over the given executor.
func New(exec *recipe.Executor) *Iterator { return &Iterator{exec: exec} }

// Next fetches the next page. It returns false when iteration is complete or an
// error occurred. The source is passed per-call to match how the pipeline drives
// connectors.
func (it *Iterator) Next(ctx context.Context, src domain.Source) bool {
	if it.done || it.err != nil {
		return false
	}
	items, raw, status, next, done, err := it.exec.Page(ctx, src, it.state)
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

func (it *Iterator) Items() []domain.ExternalOpportunity { return it.items }
func (it *Iterator) RawPayload() []byte                  { return it.raw }
func (it *Iterator) HTTPStatus() int                     { return it.status }
func (it *Iterator) Err() error                          { return it.err }

// Content returns nil: recipe extraction works off the raw page directly and
// does not produce a content.Extracted (the pipeline tolerates nil here).
func (it *Iterator) Content() *content.Extracted { return nil }
