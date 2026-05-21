package matching

import (
	"context"
	"time"

	"github.com/pitabwire/util"
)

// RerankItem is one input/output of the reranker.
type RerankItem struct {
	ID          string
	Score       float64
	RerankScore *float64
}

// Reranker re-ranks a slice of items. The bool return indicates whether
// reranker scores were actually applied (false = retrieval scores only).
type Reranker interface {
	Rerank(ctx context.Context, items []RerankItem) ([]RerankItem, bool, error)
}

// NoopReranker returns the input unchanged and reports used=false.
type NoopReranker struct{}

func (NoopReranker) Rerank(_ context.Context, items []RerankItem) ([]RerankItem, bool, error) {
	return items, false, nil
}

// PooledReranker bounds concurrent calls to an upstream reranker by
// concurrency and falls back to retrieval scores when the pool is full,
// when the upstream errors, or when the upstream times out.
type PooledReranker struct {
	upstream Reranker
	sem      chan struct{}
	timeout  time.Duration
}

func NewPooledReranker(upstream Reranker, concurrency int, timeout time.Duration) *PooledReranker {
	if concurrency < 1 {
		concurrency = 1
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	return &PooledReranker{
		upstream: upstream,
		sem:      make(chan struct{}, concurrency),
		timeout:  timeout,
	}
}

func (p *PooledReranker) Rerank(ctx context.Context, items []RerankItem) ([]RerankItem, bool, error) {
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	default:
		util.Log(ctx).WithField("count", len(items)).Warn("reranker pool full; falling back to retrieval scores")
		return items, false, nil
	}

	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	out, used, err := p.upstream.Rerank(cctx, items)
	if err != nil {
		util.Log(ctx).WithError(err).Warn("reranker error; falling back to retrieval scores")
		return items, false, nil
	}
	return out, used, nil
}
