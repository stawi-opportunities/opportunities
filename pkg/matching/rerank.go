package matching

import (
	"context"
	"sort"
	"time"

	"github.com/pitabwire/util"
)

// RerankItem is one input/output of the reranker.
type RerankItem struct {
	ID          string
	Text        string // document text to score against the query
	Score       float64
	RerankScore *float64
}

// Reranker re-ranks a slice of items against a query. The bool return
// indicates whether reranker scores were actually applied (false =
// retrieval scores only).
type Reranker interface {
	Rerank(ctx context.Context, query string, items []RerankItem) ([]RerankItem, bool, error)
}

// NoopReranker returns the input unchanged and reports used=false.
type NoopReranker struct{}

func (NoopReranker) Rerank(_ context.Context, _ string, items []RerankItem) ([]RerankItem, bool, error) {
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

func (p *PooledReranker) Rerank(ctx context.Context, query string, items []RerankItem) ([]RerankItem, bool, error) {
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	default:
		util.Log(ctx).WithField("count", len(items)).Warn("reranker pool full; falling back to retrieval scores")
		return items, false, nil
	}

	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	out, used, err := p.upstream.Rerank(cctx, query, items)
	if err != nil {
		util.Log(ctx).WithError(err).Warn("reranker error; falling back to retrieval scores")
		return items, false, nil
	}
	return out, used, nil
}

// rerankClient is the minimal cross-encoder contract ExtractorReranker
// needs. The SiliconFlow/TEI rerank client in pkg/extraction satisfies it.
type rerankClient interface {
	Rerank(ctx context.Context, query string, texts []string) ([]float32, error)
}

// ExtractorReranker scores the top-K retrieved items with a real
// cross-encoder, ordering them by relevance to the query text.
type ExtractorReranker struct {
	client rerankClient
	topK   int
}

// NewExtractorReranker wraps a cross-encoder client. topK<=0 means score
// every item.
func NewExtractorReranker(c rerankClient, topK int) *ExtractorReranker {
	return &ExtractorReranker{client: c, topK: topK}
}

// Rerank scores the first n items (n = min(len, topK)) against query and
// reorders them by descending cross-encoder score. The tail (beyond n) is
// left untouched. Returns used=false when there's nothing to score or the
// upstream is unusable, so callers fall back to retrieval scores.
func (r *ExtractorReranker) Rerank(ctx context.Context, query string, items []RerankItem) ([]RerankItem, bool, error) {
	if query == "" || len(items) == 0 {
		return items, false, nil
	}

	n := len(items)
	if r.topK > 0 && r.topK < n {
		n = r.topK
	}

	anyText := false
	texts := make([]string, n)
	for i := 0; i < n; i++ {
		texts[i] = items[i].Text
		if items[i].Text != "" {
			anyText = true
		}
	}
	if !anyText {
		return items, false, nil
	}

	scores, err := r.client.Rerank(ctx, query, texts)
	if err != nil || len(scores) < n {
		return items, false, err
	}

	out := make([]RerankItem, len(items))
	copy(out, items)
	for i := 0; i < n; i++ {
		s := float64(scores[i])
		out[i].RerankScore = &s
	}
	sort.SliceStable(out[:n], func(a, b int) bool {
		var sa, sb float64
		if out[a].RerankScore != nil {
			sa = *out[a].RerankScore
		}
		if out[b].RerankScore != nil {
			sb = *out[b].RerankScore
		}
		return sa > sb
	})
	return out, true, nil
}
