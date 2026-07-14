package extraction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// Production NVIDIA Build (build.nvidia.com) defaults — used by crawler,
// frontier-worker, matching, recipe-gen, how-to-apply peel/backfill.
// Deploy sets these via Helm env; CLI tools may fall back when env is partial.
const (
	// NVIDIABuildBaseURL is the OpenAI-compatible root for hosted NIM.
	NVIDIABuildBaseURL = "https://integrate.api.nvidia.com"
	// NVIDIABuildChatModel is the default instruct model for extraction,
	// recipe generation, CV parse, and how_to_apply peel.
	NVIDIABuildChatModel = "meta/llama-3.1-8b-instruct"
	// NVIDIABuildEmbedModel is the default embedding model (native 1024-d).
	NVIDIABuildEmbedModel = "nvidia/nv-embedqa-e5-v5"
)

// ResolveInference returns the configured chat-completion backend.
// Empty baseURL means "AI extraction disabled". Callers that want the
// production NVIDIA Build endpoint must set INFERENCE_BASE_URL (deploy
// already pins https://integrate.api.nvidia.com + meta/llama-3.1-8b-instruct).
func ResolveInference(inferenceURL, inferenceModel, inferenceKey string) (string, string, string) {
	return inferenceURL, inferenceModel, inferenceKey
}

// ResolveInferenceOrNVIDIA returns the configured chat backend, falling
// back to NVIDIA Build URL/model when baseURL or model are empty but a
// key is present (CLI tools: how-to-apply-backfill, recipe-gen).
func ResolveInferenceOrNVIDIA(inferenceURL, inferenceModel, inferenceKey string) (string, string, string) {
	if inferenceKey == "" && inferenceURL == "" {
		return "", "", ""
	}
	if inferenceURL == "" {
		inferenceURL = NVIDIABuildBaseURL
	}
	if inferenceModel == "" {
		inferenceModel = NVIDIABuildChatModel
	}
	return inferenceURL, inferenceModel, inferenceKey
}

// ResolveEmbedding returns the configured embedding backend. baseURL is empty when embeddings are
// disabled — callers skip storing the vector.
func ResolveEmbedding(embedURL, embedModel, embedKey string) (string, string, string) {
	return embedURL, embedModel, embedKey
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
	// EmbeddingDimensions, when >0, is sent as the OpenAI-compatible
	// "dimensions" field so Matryoshka-capable models (SiliconFlow's
	// Qwen3-Embedding family) emit a fixed-width vector matching the
	// pgvector column. 0 omits it (native width — e5-large is 1024).
	EmbeddingDimensions int
	// EmbeddingInputType, when set (e.g. "passage" / "query"), is sent as
	// NVIDIA NIM / asymmetric-E5 `input_type`. Required by nv-embedqa-e5-v5
	// on integrate.api.nvidia.com; omit for providers that reject unknown
	// fields (TEI, SiliconFlow).
	EmbeddingInputType string
	// EmbeddingMaxConcurrency bounds simultaneous embedding HTTP calls made by
	// this process. 0 disables the in-process semaphore.
	EmbeddingMaxConcurrency int
	// EmbeddingMinInterval spaces embedding HTTP calls made by this process.
	// 0 disables pacing.
	EmbeddingMinInterval time.Duration

	// Reranker (cross-encoder). Hosted alongside the embedder in v1 —
	// TEI serves one model per process so we run two deployments.
	RerankBaseURL string
	RerankAPIKey  string
	RerankModel   string
	// RerankDialect selects the rerank wire format: "" / "tei" = TEI's
	// POST /rerank {query,texts} → [{index,score}]; "openai" /
	// "siliconflow" = POST /v1/rerank {model,query,documents} →
	// {results:[{index,relevance_score}]}.
	RerankDialect string

	// Registry drives per-kind extraction prompts and classifier
	// candidates. Optional — when nil the extractor uses the universal
	// prefix alone and defaults the kind to "job".
	Registry *opportunity.Registry

	// HTTPClient overrides the underlying HTTP client. Production callers
	// should pass svc.HTTPClientManager().Client(ctx) so OTEL trace
	// propagation applies to LLM/embedding/rerank requests; nil falls
	// back to a stdlib client with the package-level extractionTimeout.
	HTTPClient HTTPDoer
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
		// Headroom for the largest JSON outputs: a full extraction recipe maps
		// rules for ~50 fields (selectors + transforms + fallbacks) and overran
		// 4096, truncating the JSON ("unexpected end of JSON input"). 8192 fits
		// recipes and ordinary extractions still stop early.
		"max_tokens": 8192,
	}
	if expectJSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("chat: marshal: %w", err)
	}
	// Retry 429 (provider rate limit) and 5xx with backoff: under bursty load a
	// shared inference tier RPM/TPM-limits us, and failing the whole extraction
	// on the first 429 drops the job. Self-pacing lets calls through.
	const maxAttempts = 4
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/chat/completions", bytes.NewReader(raw))
		if rerr != nil {
			return "", fmt.Errorf("chat: new request: %w", rerr)
		}
		req.Header.Set("Content-Type", "application/json")
		if e.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+e.apiKey)
		}
		resp, derr := e.client.Do(req)
		if derr != nil {
			return "", fmt.Errorf("chat: request: %w", derr)
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			ra := resp.Header.Get("Retry-After")
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("chat: status %d: %s", resp.StatusCode, string(b))
			if attempt == maxAttempts {
				break
			}
			if werr := backoffWait(ctx, attempt, ra); werr != nil {
				return "", werr
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			return "", fmt.Errorf("chat: status %d: %s", resp.StatusCode, string(b))
		}

		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		derr = json.NewDecoder(resp.Body).Decode(&out)
		_ = resp.Body.Close()
		if derr != nil {
			return "", fmt.Errorf("chat: decode: %w", derr)
		}
		if len(out.Choices) == 0 {
			return "", fmt.Errorf("chat: empty choices")
		}
		return extractJSONPayload(out.Choices[0].Message.Content), nil
	}
	return "", fmt.Errorf("chat: rate-limited after %d attempts: %w", maxAttempts, lastErr)
}

// backoffWait sleeps before a retry: the provider's Retry-After when
// present (capped 60s), else exponential (2,4,8s capped 30s). Returns ctx.Err()
// if the context is cancelled while waiting.
func backoffWait(ctx context.Context, attempt int, retryAfter string) error {
	wait := min(time.Duration(1<<uint(attempt))*time.Second, 30*time.Second)
	if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
		wait = min(time.Duration(secs)*time.Second, 60*time.Second)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
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
	openAI := e.rerankDialect == "openai" || e.rerankDialect == "siliconflow"
	payload := map[string]any{"query": query}
	path := "/rerank"
	if openAI {
		// OpenAI/Cohere-style rerank (e.g. SiliconFlow Qwen3-Reranker):
		// POST /v1/rerank with `documents`; `model` is required.
		path = "/v1/rerank"
		payload["documents"] = texts
		payload["model"] = e.rerankModel
		payload["return_documents"] = false
	} else {
		// TEI: POST /rerank with `texts`; model optional (single-model
		// per process, so the field is ignored there).
		payload["texts"] = texts
		if e.rerankModel != "" {
			payload["model"] = e.rerankModel
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("rerank: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.rerankBaseURL+path, bytes.NewReader(raw))
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
	// Some deployments wrap under {"results": [...]} (and OpenAI/Cohere/
	// SiliconFlow name the field "relevance_score"); handle all shapes.
	type scored struct {
		Index          int      `json:"index"`
		Score          float32  `json:"score"`
		RelevanceScore *float32 `json:"relevance_score"`
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
			if s.RelevanceScore != nil {
				out[s.Index] = *s.RelevanceScore
			} else {
				out[s.Index] = s.Score
			}
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
	// Matryoshka truncation for models that support it (Qwen3-Embedding):
	// pin the output width to the pgvector column so a model whose native
	// dimension differs can't silently break the dim guard.
	if e.embeddingDimensions > 0 {
		body["dimensions"] = e.embeddingDimensions
	}
	// NVIDIA nv-embedqa-e5-v5 (and other asymmetric E5 NIMs) require
	// input_type; TEI/OpenAI-compat hosts typically ignore or omit it.
	if e.embeddingInputType != "" {
		body["input_type"] = e.embeddingInputType
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("embed: marshal: %w", err)
	}

	const maxAttempts = 4
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		release, err := e.waitForEmbeddingTurn(ctx)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.embeddingBaseURL+"/v1/embeddings", bytes.NewReader(raw))
		if err != nil {
			release()
			return nil, fmt.Errorf("embed: new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if e.embeddingAPIKey != "" {
			req.Header.Set("Authorization", "Bearer "+e.embeddingAPIKey)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			release()
			return nil, fmt.Errorf("embed: request: %w", err)
		}
		release()

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			ra := resp.Header.Get("Retry-After")
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("embed: status %d: %s", resp.StatusCode, string(b))
			if attempt == maxAttempts {
				break
			}
			if werr := backoffWait(ctx, attempt, ra); werr != nil {
				return nil, werr
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("embed: status %d: %s", resp.StatusCode, string(b))
		}
		// OpenAI-compatible embedding responses: {data: [{embedding: [...]}]}
		var out struct {
			Data []struct {
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("embed: decode: %w", err)
		}
		if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
			return nil, fmt.Errorf("embed: empty embedding in response")
		}
		return out.Data[0].Embedding, nil
	}
	return nil, fmt.Errorf("embed: rate-limited after %d attempts: %w", maxAttempts, lastErr)
}

func (e *Extractor) waitForEmbeddingTurn(ctx context.Context) (func(), error) {
	release := func() {}
	if e.embeddingSlots != nil {
		select {
		case e.embeddingSlots <- struct{}{}:
			release = func() { <-e.embeddingSlots }
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if e.embeddingMinInterval <= 0 {
		return release, nil
	}
	e.embeddingMu.Lock()
	defer e.embeddingMu.Unlock()

	if !e.embeddingLast.IsZero() {
		wait := e.embeddingMinInterval - time.Since(e.embeddingLast)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				release()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	e.embeddingLast = time.Now()
	return release, nil
}
