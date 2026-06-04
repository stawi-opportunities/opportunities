package extraction

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEmbed_SendsDimensionsWhenConfigured asserts the embeddings request
// carries the OpenAI "dimensions" field only when EmbeddingDimensions > 0
// (Matryoshka truncation for Qwen3-Embedding → fixed pgvector width).
func TestEmbed_SendsDimensionsWhenConfigured(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dims    int
		wantKey bool
		wantVal float64
	}{
		{"configured", 1024, true, 1024},
		{"omitted", 0, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/embeddings" {
					t.Errorf("path = %q, want /v1/embeddings", r.URL.Path)
				}
				b, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(b, &got)
				_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
			}))
			defer srv.Close()

			e := New(Config{EmbeddingBaseURL: srv.URL, EmbeddingModel: "Qwen/Qwen3-Embedding-0.6B", EmbeddingDimensions: tc.dims})
			if _, err := e.Embed(context.Background(), "passage: hello"); err != nil {
				t.Fatalf("Embed: %v", err)
			}
			v, ok := got["dimensions"]
			if ok != tc.wantKey {
				t.Fatalf("dimensions present = %v, want %v (body=%v)", ok, tc.wantKey, got)
			}
			if tc.wantKey {
				if f, _ := v.(float64); f != tc.wantVal {
					t.Errorf("dimensions = %v, want %v", v, tc.wantVal)
				}
			}
		})
	}
}

// TestRerank_SiliconFlowDialect asserts the openai/siliconflow dialect posts
// to /v1/rerank with `documents` + `model` and reads `relevance_score`,
// preserving input order.
func TestRerank_SiliconFlowDialect(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		// Return out-of-order results to prove index-based placement.
		_, _ = w.Write([]byte(`{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.1}]}`))
	}))
	defer srv.Close()

	e := New(Config{RerankBaseURL: srv.URL, RerankModel: "Qwen/Qwen3-Reranker-8B", RerankDialect: "siliconflow"})
	scores, err := e.Rerank(context.Background(), "python dev", []string{"designer", "python engineer"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if gotPath != "/v1/rerank" {
		t.Errorf("path = %q, want /v1/rerank", gotPath)
	}
	if _, ok := gotBody["documents"]; !ok {
		t.Errorf("body missing `documents`: %v", gotBody)
	}
	if gotBody["model"] != "Qwen/Qwen3-Reranker-8B" {
		t.Errorf("model = %v, want Qwen/Qwen3-Reranker-8B", gotBody["model"])
	}
	if len(scores) != 2 || scores[0] != 0.1 || scores[1] != 0.9 {
		t.Errorf("scores = %v, want [0.1 0.9] (placed by index)", scores)
	}
}

// TestRerank_TEIDialectUnchanged guards the default TEI path: POST /rerank
// with `texts` and a top-level [{index,score}] array.
func TestRerank_TEIDialectUnchanged(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`[{"index":0,"score":0.7}]`))
	}))
	defer srv.Close()

	e := New(Config{RerankBaseURL: srv.URL})
	scores, err := e.Rerank(context.Background(), "q", []string{"a"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if gotPath != "/rerank" {
		t.Errorf("path = %q, want /rerank", gotPath)
	}
	if _, ok := gotBody["texts"]; !ok {
		t.Errorf("body missing `texts`: %v", gotBody)
	}
	if len(scores) != 1 || scores[0] != 0.7 {
		t.Errorf("scores = %v, want [0.7]", scores)
	}
}
