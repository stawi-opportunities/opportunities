package matching_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// stubRerankClient returns a fixed score per text, in order.
type stubRerankClient struct {
	scores []float32
	gotQ   string
	gotN   int
}

func (s *stubRerankClient) Rerank(_ context.Context, query string, texts []string) ([]float32, error) {
	s.gotQ = query
	s.gotN = len(texts)
	return s.scores, nil
}

func TestExtractorReranker_ReordersByScore(t *testing.T) {
	// Item "a" has retrieval order first but the lowest cross-encoder score;
	// "c" should bubble to the top after reranking.
	client := &stubRerankClient{scores: []float32{0.1, 0.5, 0.9}}
	r := matching.NewExtractorReranker(client, 0)

	items := []matching.RerankItem{
		{ID: "a", Text: "alpha", Score: 0.7},
		{ID: "b", Text: "bravo", Score: 0.6},
		{ID: "c", Text: "charlie", Score: 0.5},
	}
	out, used, err := r.Rerank(context.Background(), "q", items)
	require.NoError(t, err)
	require.True(t, used)
	require.Equal(t, "q", client.gotQ)
	require.Equal(t, 3, client.gotN)

	// Descending by cross-encoder score: c (0.9), b (0.5), a (0.1).
	require.Equal(t, []string{"c", "b", "a"}, []string{out[0].ID, out[1].ID, out[2].ID})
	require.NotNil(t, out[0].RerankScore)
	require.InDelta(t, 0.9, *out[0].RerankScore, 1e-6)
	require.NotNil(t, out[2].RerankScore)
	require.InDelta(t, 0.1, *out[2].RerankScore, 1e-6)
}

func TestExtractorReranker_EmptyQuerySkips(t *testing.T) {
	client := &stubRerankClient{scores: []float32{0.9}}
	r := matching.NewExtractorReranker(client, 0)

	items := []matching.RerankItem{{ID: "a", Text: "alpha", Score: 0.7}}
	out, used, err := r.Rerank(context.Background(), "", items)
	require.NoError(t, err)
	require.False(t, used)
	require.Equal(t, items, out)
	require.Equal(t, 0, client.gotN) // client never called
}

func TestExtractorReranker_AllEmptyTextSkips(t *testing.T) {
	client := &stubRerankClient{scores: []float32{0.9, 0.8}}
	r := matching.NewExtractorReranker(client, 0)

	items := []matching.RerankItem{
		{ID: "a", Text: "", Score: 0.7},
		{ID: "b", Text: "", Score: 0.6},
	}
	out, used, err := r.Rerank(context.Background(), "q", items)
	require.NoError(t, err)
	require.False(t, used)
	require.Equal(t, items, out)
	require.Equal(t, 0, client.gotN) // client never called
}
