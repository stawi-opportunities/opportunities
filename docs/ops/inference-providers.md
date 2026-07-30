# Inference providers (cluster)

Chat/recipe/peel and embeddings talk to any **OpenAI-compatible** HTTP API.
Select a preset with env, or set URLs explicitly.

## Env

| Variable | Purpose |
|----------|---------|
| `INFERENCE_PROVIDER` | `nvidia` \| `google` \| `custom` (or empty) |
| `INFERENCE_BASE_URL` | OpenAI-compat root (optional if provider set) |
| `INFERENCE_MODEL` | Chat model id |
| `INFERENCE_API_KEY` | Bearer token |
| `EMBEDDING_PROVIDER` | Same as inference (independent) |
| `EMBEDDING_BASE_URL` / `MODEL` / `API_KEY` | Embed backend |
| `EMBEDDING_DIMENSIONS` | Optional MRL width (must match `opportunities.embedding`) |
| `EMBEDDING_INPUT_TYPE` | `passage` / `query` for NVIDIA asymmetric E5; omit for Google |

## Presets (`pkg/extraction`)

| Provider | Base URL | Default chat | Default embed |
|----------|----------|--------------|---------------|
| **nvidia** | `https://integrate.api.nvidia.com` | `meta/llama-3.1-8b-instruct` | `nvidia/nv-embedqa-e5-v5` (1024-d) |
| **google** | `https://generativelanguage.googleapis.com/v1beta/openai` | `gemini-2.0-flash` | `text-embedding-004` (768-d) |
| **custom** | (required) | (required) | (required) |

## Switch cluster chat to Google (rate-limit relief)

1. Put a **Gemini / AI Studio API key** in the inference secret:

```bash
# Replace value — do not reuse the NVIDIA nvapi key with Google URLs.
kubectl -n product-opportunities create secret generic inference-credentials-opportunities \
  --from-literal=INFERENCE_API_KEY='AIza...' \
  --dry-run=client -o yaml | kubectl apply -f -
```

2. Set on **crawler** and **frontier-worker** (and matching Cloud Run if desired):

```yaml
- name: INFERENCE_PROVIDER
  value: "google"
- name: INFERENCE_BASE_URL
  value: "https://generativelanguage.googleapis.com/v1beta/openai"
- name: INFERENCE_MODEL
  value: "gemini-2.0-flash"
```

3. **Keep embeddings on NVIDIA** unless you migrate the vector column width
   (product HNSW is 1024-d; Google `text-embedding-004` is 768-d by default).

```yaml
- name: EMBEDDING_PROVIDER
  value: "nvidia"
- name: EMBEDDING_BASE_URL
  value: "https://integrate.api.nvidia.com"
- name: EMBEDDING_MODEL
  value: "nvidia/nv-embedqa-e5-v5"
- name: EMBEDDING_INPUT_TYPE
  value: "passage"
```

4. Roll the deployments (or let Helm/Flux reconcile).

## Switch back to NVIDIA

```yaml
- name: INFERENCE_PROVIDER
  value: "nvidia"
- name: INFERENCE_BASE_URL
  value: "https://integrate.api.nvidia.com"
- name: INFERENCE_MODEL
  value: "meta/llama-3.1-8b-instruct"
```

Restore the NVIDIA key in `inference-credentials-opportunities`.
