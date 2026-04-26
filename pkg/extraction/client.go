package extraction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// ResolveInference picks the active chat-completion back-end. Preference:
//
//  1. INFERENCE_BASE_URL / INFERENCE_MODEL / INFERENCE_API_KEY (new)
//  2. OLLAMA_URL / OLLAMA_MODEL (legacy; no auth key)
//
// Returns (baseURL, model, apiKey). baseURL is empty when neither back-end
// is configured — callers treat that as "AI extraction disabled".
func ResolveInference(inferenceURL, inferenceModel, inferenceKey, ollamaURL, ollamaModel string) (string, string, string) {
	if inferenceURL != "" {
		model := inferenceModel
		if model == "" {
			model = ollamaModel
		}
		return inferenceURL, model, inferenceKey
	}
	if ollamaURL != "" {
		return ollamaURL, ollamaModel, ""
	}
	return "", "", ""
}

// ResolveEmbedding picks the embedding back-end. Preference:
//
//  1. EMBEDDING_BASE_URL / EMBEDDING_MODEL / EMBEDDING_API_KEY (new)
//  2. OLLAMA_URL / OLLAMA_MODEL (legacy; Ollama serves embeddings on the
//     same port/host as chat, so any configured Ollama URL doubles as an
//     embedding source)
//
// Returns (baseURL, model, apiKey). baseURL is empty when embeddings are
// disabled — callers skip storing the vector.
func ResolveEmbedding(embedURL, embedModel, embedKey, ollamaURL, ollamaModel string) (string, string, string) {
	if embedURL != "" {
		model := embedModel
		if model == "" {
			model = ollamaModel
		}
		return embedURL, model, embedKey
	}
	if ollamaURL != "" {
		return ollamaURL, ollamaModel, ""
	}
	return "", "", ""
}

// ResolveRerank picks the active reranker back-end. Preference:
//
//  1. RERANK_BASE_URL / RERANK_MODEL / RERANK_API_KEY (new)
//  2. EMBEDDING_BASE_URL as a shared fallback — both services often live
//     behind the same TEI sidecar deployment in the cluster, so reusing
//     the embedding URL is the sane default when the operator didn't set
//     a dedicated one.
//
// Returns (baseURL, model, apiKey). baseURL empty means reranking is
// disabled and the matcher will fall back to the bi-encoder score.
func ResolveRerank(rerankURL, rerankModel, rerankKey, embedURL, embedKey string) (string, string, string) {
	if rerankURL != "" {
		return rerankURL, rerankModel, rerankKey
	}
	if embedURL != "" {
		// Caller opted into "same host, different model". The default model
		// is deliberately empty so callers notice and set RERANK_MODEL
		// explicitly — picking the embed model would be a silent bug.
		return embedURL, rerankModel, embedKey
	}
	return "", "", ""
}

// Config selects the inference back-end. BaseURL is the OpenAI-compatible
// root (e.g. https://gateway.ai.cloudflare.com/v1/<account>/<gateway>/workers-ai
// or http://ollama.ollama.svc:11434). APIKey is appended as Bearer when set;
// leave empty for Ollama.
//
// Embedding* fields are optional. If EmbeddingBaseURL is empty, Embed()
// returns nil with no error — callers treat that as "no vector stored"
// instead of failing the pipeline.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string

	EmbeddingBaseURL string
	EmbeddingAPIKey  string
	EmbeddingModel   string

	// Reranker (cross-encoder). Hosted alongside the embedder in v1 —
	// TEI serves one model per process so we run two deployments.
	RerankBaseURL string
	RerankAPIKey  string
	RerankModel   string

	// Registry drives per-kind extraction prompts and classifier
	// candidates. Optional — when nil the extractor uses the universal
	// prefix alone and defaults the kind to "job" so legacy callers
	// (and tests not wired through boot) keep working.
	Registry *opportunity.Registry
}

// chat posts an OpenAI-compatible chat completion request and returns the
// assistant's message content. When expectJSON is true we ask for strict
// JSON mode — Groq, OpenAI, and Ollama 0.3+ all accept the OpenAI shape
// `response_format: {"type":"json_object"}`. Cloudflare Workers AI wants
// a `json_schema` variant instead, so if you ever point this at CF,
// drop the flag at the call site.
func (e *Extractor) chat(ctx context.Context, prompt string, expectJSON bool) (string, error) {
	if e.baseURL == "" {
		return "", fmt.Errorf("chat: no inference base URL configured")
	}
	body := map[string]any{
		"model":    e.model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   false,
		// Enough for the 50-field JobFields extraction without provider
		// defaults (~256) cutting us off.
		"max_tokens": 4096,
	}
	if expectJSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("chat: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("chat: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("chat: status %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("chat: decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("chat: empty choices")
	}
	return extractJSONPayload(out.Choices[0].Message.Content), nil
}

// extractJSONPayload returns the most likely JSON payload from a model
// response that may include markdown fences or prose. Small models
// (notably llama-3.1-8b on Workers AI) often wrap JSON in ```json ```
// and surround it with explanatory text despite "Output ONLY JSON"
// instructions. We try, in order:
//
//  1. A ```json ... ``` or ``` ... ``` fenced block.
//  2. The substring between the first '{' or '[' and its matching
//     terminator at the top level (naive but enough for well-formed
//     JSON that doesn't contain stray closing braces inside strings).
//
// If nothing looks like JSON, the raw content is returned unchanged
// and the JSON decoder will surface a meaningful error upstream.
func extractJSONPayload(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// Step 1: strip surrounding ```…``` if present. Works even if the
	// closing fence was lost to a truncated response.
	if strings.HasPrefix(s, "```") {
		s = s[3:]
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)

	// Step 2: if prose leads, slice from the first { or [ we find.
	if i := strings.IndexAny(s, "{["); i > 0 {
		s = s[i:]
	}
	return s
}

// rerank calls TEI's POST /rerank endpoint and returns a score per input
// text in the same order as texts. Batches larger than the service can
// absorb in one call should be chunked by the caller (TEI handles up to
// a few hundred in practice with --max-batch-tokens=16384).
func (e *Extractor) rerank(ctx context.Context, query string, texts []string) ([]float32, error) {
	if e.rerankBaseURL == "" {
		return nil, fmt.Errorf("rerank: base URL not configured")
	}
	payload := map[string]any{
		"query": query,
		"texts": texts,
	}
	// When TEI is fronted by a shared gateway that needs a model hint in
	// the body — e.g. a self-hosted router — include it. TEI itself is
	// single-model-per-process so the field is ignored there.
	if e.rerankModel != "" {
		payload["model"] = e.rerankModel
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("rerank: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.rerankBaseURL+"/rerank", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("rerank: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.rerankAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.rerankAPIKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("rerank: status %d: %s", resp.StatusCode, string(b))
	}
	// TEI v1.x — results are a top-level array of {index, score}.
	// Some deployments wrap under {"results": [...]}; handle both.
	type scored struct {
		Index int     `json:"index"`
		Score float32 `json:"score"`
	}
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("rerank: read body: %w", err)
	}
	var items []scored
	if err := json.Unmarshal(rawBody, &items); err != nil {
		var wrap struct {
			Results []scored `json:"results"`
		}
		if werr := json.Unmarshal(rawBody, &wrap); werr != nil {
			return nil, fmt.Errorf("rerank: decode: %w", err)
		}
		items = wrap.Results
	}
	out := make([]float32, len(texts))
	for _, s := range items {
		if s.Index >= 0 && s.Index < len(out) {
			out[s.Index] = s.Score
		}
	}
	return out, nil
}

// embed calls /v1/embeddings. Returns (nil, nil) when embeddings aren't
// configured — callers treat an empty slice as "skip storing" rather than
// failing the whole pipeline stage.
func (e *Extractor) embed(ctx context.Context, text string) ([]float32, error) {
	if e.embeddingBaseURL == "" {
		return nil, nil
	}
	body := map[string]any{
		"model": e.embeddingModel,
		"input": text,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("embed: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.embeddingBaseURL+"/v1/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("embed: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.embeddingAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.embeddingAPIKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("embed: status %d: %s", resp.StatusCode, string(b))
	}
	// OpenAI-compatible embedding responses: {data: [{embedding: [...]}]}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed: decode: %w", err)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embed: empty embedding in response")
	}
	return out.Data[0].Embedding, nil
}
