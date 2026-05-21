package matching_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestNoopReranker_PassesScoresThrough(t *testing.T) {
	r := matching.NoopReranker{}
	items := []matching.RerankItem{
		{ID: "a", Score: 0.7},
		{ID: "b", Score: 0.9},
	}
	out, used, err := r.Rerank(context.Background(), items)
	require.NoError(t, err)
	require.False(t, used)
	require.Equal(t, items, out)
}

func TestPooledReranker_FallsBackOnPoolFull(t *testing.T) {
	blocking := &blockingReranker{block: make(chan struct{})}
	pooled := matching.NewPooledReranker(blocking, 1, 5*time.Millisecond)

	items := []matching.RerankItem{{ID: "x", Score: 0.5}}

	// First call enters and blocks.
	go func() { _, _, _ = pooled.Rerank(context.Background(), items) }()
	time.Sleep(2 * time.Millisecond)

	// Second call cannot enter and falls back.
	out, used, err := pooled.Rerank(context.Background(), items)
	require.NoError(t, err)
	require.False(t, used)
	require.Equal(t, items, out)

	close(blocking.block)
}

func TestPooledReranker_FallsBackOnUpstreamError(t *testing.T) {
	err := errors.New("model down")
	pooled := matching.NewPooledReranker(&errorReranker{err: err}, 4, time.Second)
	items := []matching.RerankItem{{ID: "x", Score: 0.5}}
	out, used, gotErr := pooled.Rerank(context.Background(), items)
	require.NoError(t, gotErr)
	require.False(t, used)
	require.Equal(t, items, out)
}

type blockingReranker struct {
	block chan struct{}
}

func (b *blockingReranker) Rerank(ctx context.Context, items []matching.RerankItem) ([]matching.RerankItem, bool, error) {
	<-b.block
	return items, true, nil
}

type errorReranker struct{ err error }

func (e *errorReranker) Rerank(_ context.Context, _ []matching.RerankItem) ([]matching.RerankItem, bool, error) {
	return nil, false, e.err
}
