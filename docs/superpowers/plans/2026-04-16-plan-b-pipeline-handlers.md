# Plan B: Pipeline Event Handlers

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current inline crawl→dedupe→store pipeline with 6 event handlers that process jobs through discrete stages (raw → deduped → normalized → validated → ready). Each stage is a Frame event. AI stages wait patiently for Ollama. No data loss.

**Architecture:** The crawler's `processSource` function is simplified to: fetch page → extract content → bloom check → store raw variant → emit event. Six Frame event handlers chain together via events, each advancing the variant's stage. The existing enrichment and embedding handlers are replaced by the normalize and canonical handlers.

**Tech Stack:** Frame events, Ollama (gemma3:1b), advisory locks, existing content/bloom/extraction packages from Plan A

**Depends on:** Plan A (content extraction, bloom filter, stage model, repository methods)

---

## File Structure

### New Files
```
pkg/pipeline/handlers/dedup.go          ← DedupHandler: variant.raw.stored → variant.deduped
pkg/pipeline/handlers/normalize.go      ← NormalizeHandler: variant.deduped → variant.normalized
pkg/pipeline/handlers/validate.go       ← ValidateHandler: variant.normalized → variant.validated
pkg/pipeline/handlers/canonical.go      ← CanonicalHandler: variant.validated → job.ready
pkg/pipeline/handlers/source_expand.go  ← SourceExpansionHandler: source.urls.discovered
pkg/pipeline/handlers/source_quality.go ← SourceQualityHandler: source.quality.review
pkg/pipeline/handlers/payloads.go       ← shared event payloads and constants
```

### Modified Files
```
apps/crawler/cmd/main.go                ← rewrite processSource to emit events, register handlers
apps/crawler/service/events/            ← remove old embedding.go and enrichment.go (replaced)
```

### Deleted Files
```
apps/crawler/service/events/embedding.go    ← replaced by canonical handler
apps/crawler/service/events/enrichment.go   ← replaced by normalize handler
```

---

## Task 1: Event Payloads & Constants

**Files:**
- Create: `pkg/pipeline/handlers/payloads.go`

- [ ] **Step 1: Create shared payloads**

Create `pkg/pipeline/handlers/payloads.go` with all event names, payload structs, and constants used across handlers:

```go
package handlers

// Event names for the staged pipeline.
const (
    EventVariantRawStored     = "variant.raw.stored"
    EventVariantDeduped       = "variant.deduped"
    EventVariantNormalized    = "variant.normalized"
    EventVariantValidated     = "variant.validated"
    EventSourceURLsDiscovered = "source.urls.discovered"
    EventSourceQualityReview  = "source.quality.review"
    EventJobReady             = "job.ready"
)

// VariantPayload carries a variant ID through the pipeline stages.
type VariantPayload struct {
    VariantID int64 `json:"variant_id"`
    SourceID  int64 `json:"source_id"`
}

// SourceURLsPayload carries discovered URLs from the normalize stage.
type SourceURLsPayload struct {
    SourceID int64    `json:"source_id"`
    URLs     []string `json:"urls"`
}

// SourceQualityPayload triggers an AI review of a source.
type SourceQualityPayload struct {
    SourceID int64 `json:"source_id"`
}

// JobReadyPayload signals a job is ready for matching.
type JobReadyPayload struct {
    CanonicalJobID int64 `json:"canonical_job_id"`
}
```

- [ ] **Step 2: Verify build, commit**

```bash
go build ./pkg/pipeline/...
git add pkg/pipeline/handlers/payloads.go
git commit -m "feat: add pipeline event payloads and constants"
```

---

## Task 2: DedupHandler

**Files:**
- Create: `pkg/pipeline/handlers/dedup.go`

- [ ] **Step 1: Create handler**

The DedupHandler receives `variant.raw.stored`, acquires an advisory lock on the hard_key, confirms uniqueness within the source, advances to `deduped`, and emits `variant.deduped`.

Read `pkg/repository/job.go` for `FindByHardKey`, `UpdateStage`, `AdvisoryLock`, `AdvisoryUnlock`.
Read `pkg/domain/models.go` for `VariantStage` constants.

```go
package handlers

// DedupHandler processes variant.raw.stored events.
// It acquires an advisory lock, checks hard_key uniqueness,
// and advances the variant to stage=deduped.
type DedupHandler struct {
    jobRepo *repository.JobRepository
}
```

Implement Name/PayloadType/Validate/Execute following the Frame event handler pattern (see `apps/crawler/service/events/embedding.go` for the pattern).

Execute logic:
1. Load variant by ID
2. If variant.Stage != "raw", skip (already processed)
3. Acquire advisory lock on hard_key (using `advisoryLockKey` from fnv hash)
4. Check FindByHardKey — if exists AND different ID AND same source, update scraped_at on existing, done
5. If new or different source → UpdateStage to "deduped"
6. Release advisory lock
7. Emit `variant.deduped` event

The handler needs access to the Frame events manager for emitting. Pass `svc *frame.Service` or the events manager directly.

- [ ] **Step 2: Verify build, commit**

```bash
go build ./pkg/pipeline/...
git commit -m "feat: add DedupHandler for stage raw→deduped"
```

---

## Task 3: NormalizeHandler

**Files:**
- Create: `pkg/pipeline/handlers/normalize.go`

- [ ] **Step 1: Create handler**

The NormalizeHandler receives `variant.deduped`, calls Ollama to extract intelligence fields from the variant's markdown content, stores extracted fields, advances to `normalized`, and emits `variant.normalized`. If discovered URLs are found, also emits `source.urls.discovered`.

Read `pkg/extraction/extractor.go` for `Extract` method and `JobFields` struct.
Read `pkg/pipeline/worker.go` for `mergeExtractedFields` pattern.

Execute logic:
1. Load variant by ID
2. If variant.Stage != "deduped", skip
3. Get content to analyze: use `variant.Markdown` if available, fall back to `variant.Description`
4. Call `extractor.Extract(ctx, content, variant.ApplyURL)` — NO TIMEOUT, let AI finish
5. Map extracted JobFields to variant fields (seniority, skills, industry, salary, etc.)
6. Store discovered URLs in `variant.DiscoveredURLs`
7. UpdateStage to "normalized" with all extracted fields
8. Emit `variant.normalized`
9. If DiscoveredURLs non-empty → emit `source.urls.discovered`

If Ollama fails → return error (Frame retries the event, variant stays at "deduped")

- [ ] **Step 2: Verify build, commit**

```bash
go build ./pkg/pipeline/...
git commit -m "feat: add NormalizeHandler for stage deduped→normalized with AI extraction"
```

---

## Task 4: ValidateHandler

**Files:**
- Create: `pkg/pipeline/handlers/validate.go`

- [ ] **Step 1: Create handler**

The ValidateHandler receives `variant.normalized`, calls Ollama with a reviewer prompt to independently validate the extracted data, and either advances to `validated` or marks as `flagged`.

Execute logic:
1. Load variant by ID
2. If variant.Stage != "normalized", skip
3. Build validation prompt: "Review this job data for completeness and correctness: title={}, company={}, seniority={}, skills={}, ..."
4. Call Ollama with validation prompt (format: json)
5. Parse AI response: `{valid: bool, confidence: float, issues: [], recommendation: "accept"/"reject"/"flag"}`
6. If valid AND confidence >= 0.7:
   - UpdateValidation(variantID, "validated", confidence, "")
   - Emit `variant.validated`
   - IncrementQualityValidated on source
7. If invalid:
   - UpdateValidation(variantID, "flagged", confidence, issues_text)
   - IncrementQualityFlagged on source
   - Check GetQualityRate — if > 0.5 → emit `source.quality.review`

Create a separate `validationPrompt` constant for the reviewer prompt. The prompt must instruct the AI to evaluate:
- Are title and description present and meaningful?
- Does seniority match the description?
- Are skills relevant to the role?
- Is salary estimate reasonable for the location/seniority?

If Ollama fails → return error (retry)

- [ ] **Step 2: Verify build, commit**

```bash
go build ./pkg/pipeline/...
git commit -m "feat: add ValidateHandler for stage normalized→validated with AI review"
```

---

## Task 5: CanonicalHandler

**Files:**
- Create: `pkg/pipeline/handlers/canonical.go`

- [ ] **Step 1: Create handler**

The CanonicalHandler receives `variant.validated`, creates/updates the canonical job cluster, generates an embedding, computes quality score, and advances to `ready`.

Read `pkg/dedupe/dedupe.go` for `UpsertAndCluster`.
Read `pkg/scoring/scorer.go` for `Score`.

Execute logic:
1. Load variant by ID
2. If variant.Stage != "validated", skip
3. Call `dedupeEngine.UpsertAndCluster(ctx, variant)` — this handles cluster creation with advisory locks
4. Generate embedding: `extractor.Embed(ctx, canonical.Title + " " + canonical.Company + " " + canonical.Description)`
5. Store embedding on canonical job
6. Compute quality score, store on canonical
7. Update tsvector search index
8. UpdateStage(variantID, "ready")
9. Emit `job.ready`

If Ollama fails on embedding → return error (retry). The canonical is already created, embedding can be retried.

- [ ] **Step 2: Verify build, commit**

```bash
go build ./pkg/pipeline/...
git commit -m "feat: add CanonicalHandler for stage validated→ready with embedding + scoring"
```

---

## Task 6: SourceExpansionHandler

**Files:**
- Create: `pkg/pipeline/handlers/source_expand.go`

- [ ] **Step 1: Create handler**

The SourceExpansionHandler receives `source.urls.discovered`, follows redirect chains, validates destinations, and upserts new sources.

Read `pkg/connectors/httpx/client.go` for HTTP client.

Execute logic:
1. Parse SourceURLsPayload — list of discovered URLs
2. For each URL:
   a. Follow redirect chain: send HTTP HEAD requests, follow Location headers, max 10 hops
   b. Extract final destination domain
   c. Filter out known non-job sites: google.com, facebook.com, twitter.com, linkedin.com/in/, instagram.com, youtube.com, wikipedia.org, github.com (but keep github.com/careers)
   d. Filter out the source's own domain (don't re-add the source we discovered from)
   e. Check if source already exists in DB (by base URL)
   f. If new → upsert source with type=generic_html, status=active, crawl_interval=7200

Use a simple HTTP client (not the full httpx.Client with retries) for redirect following — just need HEAD requests with redirect disabled, manually follow Location.

- [ ] **Step 2: Verify build, commit**

```bash
go build ./pkg/pipeline/...
git commit -m "feat: add SourceExpansionHandler for discovering new job sites"
```

---

## Task 7: SourceQualityHandler

**Files:**
- Create: `pkg/pipeline/handlers/source_quality.go`

- [ ] **Step 1: Create handler**

The SourceQualityHandler receives `source.quality.review`, loads recent validated+flagged samples, asks AI to assess source quality, and applies the recommendation.

Execute logic:
1. Load source by ID
2. Load last 20 validated variants + last 20 flagged variants from this source
3. Build prompt: "Based on these job samples from {source_url}, assess the quality of this source..."
4. AI returns: `{recommendation: "continue"/"reduce_frequency"/"pause"/"disable", reason: "..."}`
5. Apply recommendation:
   - continue → ResetQualityWindow (double window, reset counters)
   - reduce_frequency → multiply crawl_interval by 3
   - pause → PauseSource
   - disable → set status=disabled
6. Log the decision with reason

- [ ] **Step 2: Verify build, commit**

```bash
go build ./pkg/pipeline/...
git commit -m "feat: add SourceQualityHandler for AI-driven source assessment"
```

---

## Task 8: Rewrite Crawler to Use Pipeline Events

**Files:**
- Modify: `apps/crawler/cmd/main.go`
- Delete: `apps/crawler/service/events/embedding.go`
- Delete: `apps/crawler/service/events/enrichment.go`

- [ ] **Step 1: Register pipeline handlers**

In main.go, replace the existing event handler registration:

```go
// OLD: embeddingHandler + enrichmentHandler
// NEW: pipeline stage handlers
```

Register all 6 handlers:
```go
svc.Init(ctx, frame.WithRegisterEvents(
    handlers.NewDedupHandler(jobRepo, svc),
    handlers.NewNormalizeHandler(jobRepo, extractor, sourceRepo, svc),
    handlers.NewValidateHandler(jobRepo, extractor, sourceRepo, svc),
    handlers.NewCanonicalHandler(jobRepo, dedupeEngine, extractor, svc),
    handlers.NewSourceExpansionHandler(sourceRepo, httpClient),
    handlers.NewSourceQualityHandler(sourceRepo, jobRepo, extractor),
))
```

- [ ] **Step 2: Simplify processSource**

Rewrite the `processSource` function in the `crawlDependencies` method. The new logic:

```
For each job from connector:
  1. Extract content (connector.Content() or generate from description)
  2. Compute hard_key
  3. Check bloom filter (IsSeen) → skip if seen
  4. Store variant with stage='raw', raw_html, clean_html, markdown
  5. Mark bloom filter (MarkSeen)
  6. Emit variant.raw.stored event
  7. Done. Move to next job.
```

No more inline dedupe, no more inline AI extraction, no more inline canonical creation. Everything happens via events.

- [ ] **Step 3: Add bloom filter to crawlDependencies**

Add `bloom *bloom.Filter` to the `crawlDependencies` struct. Initialize in main.go:

```go
bloomFilter := bloom.NewFilter(cfg.ValkeyAddr, dbFn)
defer bloomFilter.Close()
```

Add `ValkeyAddr string` to the crawler config:
```go
ValkeyAddr string `env:"VALKEY_ADDR" envDefault:""`
```

- [ ] **Step 4: Remove old event handlers**

Delete `apps/crawler/service/events/embedding.go` and `apps/crawler/service/events/enrichment.go`. Their functionality is now in the pipeline handlers.

- [ ] **Step 5: Remove old backfill logic**

Remove the embedding and enrichment backfill `AddPreStartMethod` blocks. The pipeline handles retries via events — stuck variants will be re-emitted on startup if needed.

Add a simpler startup check: emit events for any variants stuck at intermediate stages:

```go
svc.AddPreStartMethod(func(preCtx context.Context, _ *frame.Service) {
    for _, stage := range []string{"raw", "deduped", "normalized", "validated"} {
        stuck, _ := jobRepo.ListByStage(preCtx, stage, 100)
        if len(stuck) == 0 { continue }
        log.WithField("stage", stage).WithField("count", len(stuck)).Info("re-emitting stuck variants")
        evts := svc.EventsManager()
        eventName := stageToEvent(stage)
        for _, v := range stuck {
            evts.Emit(preCtx, eventName, &handlers.VariantPayload{VariantID: v.ID, SourceID: v.SourceID})
        }
    }
})
```

With helper:
```go
func stageToEvent(stage string) string {
    switch stage {
    case "raw": return handlers.EventVariantRawStored
    case "deduped": return handlers.EventVariantDeduped
    case "normalized": return handlers.EventVariantNormalized
    case "validated": return handlers.EventVariantValidated
    default: return ""
    }
}
```

- [ ] **Step 6: Update /healthz with pipeline stats**

Update the health endpoint to include stage counts from `jobRepo.CountByStage()`.

- [ ] **Step 7: Verify full build and tests**

```bash
go build ./...
go test ./...
```

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat: replace inline pipeline with staged event handlers, bloom filter, content extraction"
```

---

## Execution Notes

- **Tasks 1-7** are independent (handler files don't depend on each other at compile time)
- **Task 8** depends on all previous tasks (wires everything together)
- Tasks 2-5 can run in parallel (each creates one handler file)
- Tasks 6-7 can run in parallel
- Task 8 must be last

**After Plan B completes:** The crawler will process jobs through the full pipeline:
```
Crawl (fast) → Dedup (code) → Normalize (AI) → Validate (AI) → Canonical (embed+score) → Ready
```

Each stage is an event. AI stages wait patiently. Nothing is lost.
