# Crawl System Robustness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the crawling system robust and self-healing. Fix broken connectors, relax the quality gate, fix the dedupe bug, add a circuit breaker for failing sources, and rebuild canonical data from scratch.

**Architecture:** Modify existing packages in-place. No new services. Changes span: quality gate, dedupe engine, source health management, connector fixes, extraction timeouts, and a data rebuild admin endpoint. Crawlers stay stopped until all fixes are deployed.

**Tech Stack:** Go 1.26, GORM, Frame events, existing Ollama integration

---

## File Structure

### Modified Files

```
pkg/quality/gate.go                  ← relax: only title + description required
pkg/quality/gate_test.go             ← update tests for new rules
pkg/dedupe/dedupe.go                 ← fix: reuse existing cluster instead of always creating new
pkg/domain/models.go                 ← add: consecutive_failures, needs_tuning on Source; add SourceDegraded status
pkg/repository/source.go             ← add: RecordFailure, RecordSuccess, ListHealthReport, PauseSource, EnableSource
pkg/repository/job.go                ← add: RebuildCanonicals (truncate + re-cluster)
pkg/extraction/extractor.go          ← increase timeouts to 10min/5min
pkg/connectors/himalayas/himalayas.go ← fix field mapping
pkg/connectors/jobicy/jobicy.go      ← fix jobType flex unmarshal
pkg/connectors/lever/lever.go        ← remove (API dead)
pkg/connectors/findwork/findwork.go  ← remove (API requires auth now)
pkg/connectors/universal/universal.go ← add pattern matching fallback
apps/crawler/cmd/main.go             ← wire health management into crawl loop; add admin endpoints
apps/crawler/service/setup.go        ← remove lever/findwork from registry
```

### New Files

```
pkg/connectors/flexjson.go           ← flexString type for resilient JSON parsing
```

### Deleted Files

```
pkg/connectors/lever/lever.go        ← API returns 404 for all boards
pkg/connectors/findwork/findwork.go  ← API requires auth now
```

---

## Task 1: Relax Quality Gate

**Files:**
- Modify: `pkg/quality/gate.go`
- Modify: `pkg/quality/gate_test.go`

- [ ] **Step 1: Read current gate.go and gate_test.go**

Read both files to understand current implementation.

- [ ] **Step 2: Update gate.go**

Replace the `Check` function. New rules:
- title: required, > 3 chars → `ErrMissingTitle`
- description: required, > 50 chars → `ErrShortDescription`
- apply_url: if empty, NO error (caller should set fallback before calling Check)
- company: NOT checked
- location: NOT checked

Remove `ErrMissingCompany`, `ErrMissingLocation`, `ErrMissingApplyURL`, `ErrInvalidApplyURL` sentinel errors.

Add a new function `EnsureApplyURL(job *domain.ExternalJob, fallbackURL string)` that sets `job.ApplyURL = fallbackURL` if apply_url is empty.

- [ ] **Step 3: Update gate_test.go**

Update tests:
- `TestValidJobPasses` — still passes
- `TestMissingTitle` — still fails
- `TestShortDescription` — still fails  
- Remove: `TestMissingCompany`, `TestMissingLocation`, `TestMissingApplyURL`, `TestInvalidApplyURL`
- Add: `TestMissingCompanyPasses` — job with empty company should pass
- Add: `TestMissingLocationPasses` — job with empty location should pass
- Add: `TestEnsureApplyURL` — sets fallback when empty, leaves existing alone

- [ ] **Step 4: Run tests**

Run: `go test ./pkg/quality/... -v`
Expected: All pass

- [ ] **Step 5: Update pipeline worker to call EnsureApplyURL**

In `pkg/pipeline/worker.go`, before calling `quality.Check(extJob)`, add:
```go
quality.EnsureApplyURL(&extJob, extJob.SourceURL)
if extJob.ApplyURL == "" {
    quality.EnsureApplyURL(&extJob, source.BaseURL)
}
```

Also update `apps/crawler/cmd/main.go` `processSource` function with the same pattern.

- [ ] **Step 6: Verify full build and tests**

Run: `go build ./... && go test ./...`

- [ ] **Step 7: Commit**

```bash
git add pkg/quality/ pkg/pipeline/worker.go apps/crawler/cmd/main.go
git commit -m "fix: relax quality gate - only title and description required, apply_url has fallback"
```

---

## Task 2: Fix Connector Bugs

**Files:**
- Modify: `pkg/connectors/himalayas/himalayas.go`
- Modify: `pkg/connectors/jobicy/jobicy.go`
- Create: `pkg/connectors/flexjson.go`
- Delete: `pkg/connectors/lever/` directory
- Delete: `pkg/connectors/findwork/` directory
- Modify: `apps/crawler/service/setup.go`

- [ ] **Step 1: Create flexjson helper**

Create `pkg/connectors/flexjson.go`:

```go
package connectors

import (
    "encoding/json"
    "strings"
)

// FlexString unmarshals a JSON value that may be a string, an array of strings,
// or null into a single comma-separated string. This prevents connectors from
// breaking when an API changes a field's type.
type FlexString string

func (f *FlexString) UnmarshalJSON(data []byte) error {
    // Try string first
    var s string
    if err := json.Unmarshal(data, &s); err == nil {
        *f = FlexString(s)
        return nil
    }

    // Try array of strings
    var arr []string
    if err := json.Unmarshal(data, &arr); err == nil {
        *f = FlexString(strings.Join(arr, ", "))
        return nil
    }

    // Null or unparseable — empty string
    *f = ""
    return nil
}

func (f FlexString) String() string { return string(f) }
```

- [ ] **Step 2: Fix Himalayas connector**

Read `pkg/connectors/himalayas/himalayas.go`. The API returns:
- `excerpt` not `description`
- `minSalary`/`maxSalary` not `salaryMin`/`salaryMax`
- `employmentType` as string (not missing)
- `seniority` as array of strings
- `currency` (correct)
- `locationRestrictions` as array

Fix the `himalayasJob` struct to match the real API response. Use `connectors.FlexString` for fields that might be string or array (like `seniority`). Map `excerpt` to `Description` in the ExternalJob.

- [ ] **Step 3: Fix Jobicy connector**

Read `pkg/connectors/jobicy/jobicy.go`. Change `JobType string` to `JobType connectors.FlexString` in the response struct. Update the mapping to use `item.JobType.String()`.

- [ ] **Step 4: Remove dead connectors**

Delete `pkg/connectors/lever/` directory (API returns 404 for all boards).
Delete `pkg/connectors/findwork/` directory (API requires auth now).

Move the 3 Lever board URLs in seeds to use the universal AI connector (source_type: `generic_html`).
Remove FindWork from seeds entirely.

Update `seeds/lever_boards.json` — change `source_type` from `lever` to `generic_html` for all entries.
Delete `seeds/apis.json` entry for findwork.

- [ ] **Step 5: Update connector registry**

In `apps/crawler/service/setup.go`, remove `lever.New(client)` and `findwork.New(client)` registrations. Remove their imports.

- [ ] **Step 6: Verify build**

Run: `go build ./...`

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "fix: fix Himalayas/Jobicy field mapping, remove dead Lever/FindWork connectors, add FlexString"
```

---

## Task 3: Source Health & Circuit Breaker

**Files:**
- Modify: `pkg/domain/models.go`
- Modify: `pkg/repository/source.go`
- Modify: `apps/crawler/cmd/main.go`

- [ ] **Step 1: Update Source model**

In `pkg/domain/models.go`:
- Add `SourceDegraded SourceStatus = "degraded"` to the status constants
- Add fields to `Source` struct:
  ```go
  ConsecutiveFailures int  `gorm:"not null;default:0" json:"consecutive_failures"`
  NeedsTuning         bool `gorm:"not null;default:false" json:"needs_tuning"`
  ```

- [ ] **Step 2: Add health management methods to SourceRepository**

In `pkg/repository/source.go`, add:

```go
// RecordSuccess resets failure count, bumps health score, and returns source to active.
func (r *SourceRepository) RecordSuccess(ctx context.Context, id int64, healthScore float64) error

// RecordFailure increments consecutive_failures, drops health score,
// and transitions status according to circuit breaker rules:
// active (3 failures) → degraded (2 more) → paused
func (r *SourceRepository) RecordFailure(ctx context.Context, id int64, healthScore float64, consecutiveFailures int) error

// FlagNeedsTuning marks a source as having connector quality issues
// (>80% reject rate) without triggering the circuit breaker.
func (r *SourceRepository) FlagNeedsTuning(ctx context.Context, id int64, needsTuning bool) error

// PauseSource manually pauses a source.
func (r *SourceRepository) PauseSource(ctx context.Context, id int64) error

// EnableSource re-enables a paused or disabled source back to active.
func (r *SourceRepository) EnableSource(ctx context.Context, id int64) error

// ListHealthReport returns all sources with their health info.
func (r *SourceRepository) ListHealthReport(ctx context.Context) ([]domain.Source, error)
```

The `RecordFailure` method implements the state machine:
```go
func (r *SourceRepository) RecordFailure(ctx context.Context, id int64, healthScore float64, consecutiveFailures int) error {
    updates := map[string]any{
        "health_score":         healthScore,
        "consecutive_failures": consecutiveFailures,
    }
    
    if consecutiveFailures >= 5 {
        updates["status"] = domain.SourcePaused
    } else if consecutiveFailures >= 3 {
        updates["status"] = domain.SourceDegraded
    }
    
    return r.db(ctx, false).Model(&domain.Source{}).Where("id = ?", id).Updates(updates).Error
}
```

- [ ] **Step 3: Update ListDue to respect source states**

Modify `ListDue` in `pkg/repository/source.go`:
- Only return sources with status `active` or `degraded`
- For `degraded` sources, multiply the crawl interval by 6 in the query
- Exclude `paused` and `disabled` sources entirely

- [ ] **Step 4: Wire health management into crawl loop**

In `apps/crawler/cmd/main.go` `processSource` function, after a crawl completes:

```go
// Compute reject rate
rejectRate := float64(jobsRejected) / float64(max(jobsFound, 1))

if crawlErr != nil {
    // Connection failure — circuit breaker
    newFailures := src.ConsecutiveFailures + 1
    newHealth := max(0, src.HealthScore - 0.2)
    sourceRepo.RecordFailure(ctx, src.ID, newHealth, newFailures)
} else if rejectRate > 0.8 && jobsFound > 0 {
    // High reject rate — flag as needs tuning, don't break circuit
    sourceRepo.FlagNeedsTuning(ctx, src.ID, true)
    sourceRepo.RecordSuccess(ctx, src.ID, src.HealthScore) // don't penalize
} else {
    // Success
    newHealth := min(1.0, src.HealthScore + 0.1)
    sourceRepo.RecordSuccess(ctx, src.ID, newHealth)
    if src.NeedsTuning && rejectRate < 0.5 {
        sourceRepo.FlagNeedsTuning(ctx, src.ID, false) // connector fixed
    }
}
```

- [ ] **Step 5: Add admin endpoints**

Add to the crawler's HTTP mux:
```go
POST /admin/sources/:id/pause   → sourceRepo.PauseSource(id)
POST /admin/sources/:id/enable  → sourceRepo.EnableSource(id)
GET  /admin/sources/health      → sourceRepo.ListHealthReport()
```

- [ ] **Step 6: Update /healthz**

Enhance the healthz endpoint to include source state counts and connector quality metrics.

- [ ] **Step 7: Verify build and tests**

Run: `go build ./... && go test ./...`

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat: add circuit breaker with smart decay, source health management, admin endpoints"
```

---

## Task 4: Fix Dedupe Engine

**Files:**
- Modify: `pkg/dedupe/dedupe.go`
- Modify: `pkg/repository/job.go`

- [ ] **Step 1: Read the current dedupe.go**

The bug: `UpsertAndCluster` always creates a new cluster (step 4) even when `FindByHardKey` finds an existing match. It should reuse the existing cluster.

- [ ] **Step 2: Rewrite UpsertAndCluster**

```go
func (e *Engine) UpsertAndCluster(ctx context.Context, variant *domain.JobVariant) (*domain.CanonicalJob, error) {
    // 1. Persist the variant.
    if err := e.jobRepo.UpsertVariant(ctx, variant); err != nil {
        return nil, err
    }

    // 2. Look for an existing variant with the same hard key.
    existing, err := e.jobRepo.FindByHardKey(ctx, variant.HardKey)
    if err != nil {
        return nil, err
    }

    var clusterID int64

    if existing != nil && existing.ID != variant.ID {
        // 3a. Existing match — find its cluster and add this variant to it.
        existingCluster, err := e.jobRepo.FindClusterByVariantID(ctx, existing.ID)
        if err != nil || existingCluster == nil {
            // No cluster yet — create one
            cluster := &domain.JobCluster{
                CanonicalVariantID: existing.ID,
                Confidence:         0.98,
            }
            if err := e.jobRepo.CreateCluster(ctx, cluster); err != nil {
                return nil, err
            }
            clusterID = cluster.ID
        } else {
            clusterID = existingCluster.ID
        }

        // Add new variant to existing cluster
        member := &domain.JobClusterMember{
            ClusterID: clusterID,
            VariantID: variant.ID,
            MatchType: "hard",
            Score:     0.98,
        }
        e.jobRepo.AddClusterMember(ctx, member)
    } else {
        // 3b. New job — create a new cluster.
        cluster := &domain.JobCluster{
            CanonicalVariantID: variant.ID,
            Confidence:         1.0,
        }
        if err := e.jobRepo.CreateCluster(ctx, cluster); err != nil {
            return nil, err
        }
        clusterID = cluster.ID

        member := &domain.JobClusterMember{
            ClusterID: clusterID,
            VariantID: variant.ID,
            MatchType: "hard",
            Score:     1.0,
        }
        e.jobRepo.AddClusterMember(ctx, member)
    }

    // 4. Build and upsert canonical job.
    now := time.Now().UTC()
    canonical := buildCanonicalFromVariant(variant, clusterID, now)
    canonical.QualityScore = scoring.Score(canonical)

    if err := e.jobRepo.UpsertCanonical(ctx, canonical); err != nil {
        return nil, err
    }

    return canonical, nil
}
```

Extract `buildCanonicalFromVariant` as a helper function to keep it clean.

- [ ] **Step 3: Add FindClusterByVariantID to JobRepository**

In `pkg/repository/job.go`:
```go
func (r *JobRepository) FindClusterByVariantID(ctx context.Context, variantID int64) (*domain.JobCluster, error) {
    var member domain.JobClusterMember
    err := r.db(ctx, true).Where("variant_id = ?", variantID).First(&member).Error
    if err == gorm.ErrRecordNotFound {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }
    var cluster domain.JobCluster
    err = r.db(ctx, true).Where("id = ?", member.ClusterID).First(&cluster).Error
    if err == gorm.ErrRecordNotFound {
        return nil, nil
    }
    return &cluster, err
}
```

- [ ] **Step 4: Verify build and tests**

Run: `go build ./... && go test ./...`

- [ ] **Step 5: Commit**

```bash
git add pkg/dedupe/dedupe.go pkg/repository/job.go
git commit -m "fix: dedupe engine reuses existing clusters instead of always creating new ones"
```

---

## Task 5: Data Rebuild

**Files:**
- Modify: `pkg/repository/job.go`
- Modify: `apps/crawler/cmd/main.go`

- [ ] **Step 1: Add RebuildCanonicals to JobRepository**

```go
// RebuildCanonicals truncates canonical_jobs, job_clusters, and
// job_cluster_members, then returns all job_variants ordered by
// scraped_at for re-processing through the dedupe engine.
func (r *JobRepository) TruncateCanonicals(ctx context.Context) error {
    db := r.db(ctx, false)
    if err := db.Exec("DELETE FROM job_cluster_members").Error; err != nil {
        return err
    }
    if err := db.Exec("DELETE FROM canonical_jobs").Error; err != nil {
        return err
    }
    if err := db.Exec("DELETE FROM job_clusters").Error; err != nil {
        return err
    }
    return nil
}

// ListAllVariants returns all variants ordered by scraped_at for rebuild.
func (r *JobRepository) ListAllVariants(ctx context.Context, batchSize, offset int) ([]*domain.JobVariant, error) {
    var variants []*domain.JobVariant
    err := r.db(ctx, true).
        Order("scraped_at ASC, id ASC").
        Limit(batchSize).
        Offset(offset).
        Find(&variants).Error
    return variants, err
}
```

- [ ] **Step 2: Add admin rebuild endpoint**

In `apps/crawler/cmd/main.go`, add `POST /admin/rebuild-canonicals`:

```go
mux.HandleFunc("POST /admin/rebuild-canonicals", func(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    log := util.Log(ctx)

    // Truncate
    if err := jobRepo.TruncateCanonicals(ctx); err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    log.Info("truncated canonical tables")

    // Rebuild in batches
    offset := 0
    batchSize := 500
    total := 0
    for {
        variants, err := jobRepo.ListAllVariants(ctx, batchSize, offset)
        if err != nil || len(variants) == 0 {
            break
        }
        for _, v := range variants {
            dedupeEngine.UpsertAndCluster(ctx, v)
            total++
        }
        offset += batchSize
        log.WithField("processed", total).Info("rebuild progress")
    }

    json.NewEncoder(w).Encode(map[string]any{"rebuilt": total})
})
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git add pkg/repository/job.go apps/crawler/cmd/main.go
git commit -m "feat: add admin endpoint to rebuild canonical jobs from variants"
```

---

## Task 6: AI Connector Resilience + Timeouts

**Files:**
- Modify: `pkg/extraction/extractor.go`
- Modify: `pkg/connectors/universal/universal.go`

- [ ] **Step 1: Increase timeouts**

In `pkg/extraction/extractor.go`:
```go
const extractionTimeout = 10 * time.Minute  // was 120s — let AI finish on CPU
const embeddingTimeout = 5 * time.Minute     // separate timeout for embeddings
```

Update `NewExtractor` to use `extractionTimeout` for the main client.
Add a separate `embedClient` with `embeddingTimeout` for the `Embed` method, or use a per-request context timeout.

- [ ] **Step 2: Add pattern matching fallback to universal connector**

In `pkg/connectors/universal/universal.go`, after AI link discovery returns 0 links, try pattern matching:

```go
// If AI found no links, fall back to simple pattern matching
if len(jobLinks) == 0 {
    jobLinks = patternMatchLinks(html, source.BaseURL)
}
```

Add `patternMatchLinks` function:
```go
var commonJobPatterns = regexp.MustCompile(
    `href=["']([^"']*/(?:jobs?|listings?|vacancies|careers?|positions?)/[^"']+)["']`,
)

func patternMatchLinks(html string, baseURL string) []string {
    matches := commonJobPatterns.FindAllStringSubmatch(html, -1)
    seen := make(map[string]bool)
    var links []string
    for _, m := range matches {
        link := m[1]
        if !strings.HasPrefix(link, "http") {
            // Resolve relative URL
            origin := baseURL
            if idx := strings.Index(baseURL[8:], "/"); idx >= 0 {
                origin = baseURL[:8+idx]
            }
            link = origin + link
        }
        if !seen[link] {
            seen[link] = true
            links = append(links, link)
        }
    }
    return links
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git add pkg/extraction/extractor.go pkg/connectors/universal/universal.go
git commit -m "fix: increase AI timeouts to 10min/5min, add pattern matching fallback for link discovery"
```

---

## Task 7: Deploy, Rebuild, and Verify

- [ ] **Step 1: Push and tag**

```bash
go mod tidy
go build ./...
go test ./...
git push origin main
git tag v1.0.0
git push origin v1.0.0
```

v1.0.0 marks the first robust, correct release.

- [ ] **Step 2: Wait for GitHub Actions build**

Monitor the release workflow. All 3 images must build successfully.

- [ ] **Step 3: Update deployment image tags**

Update all image tags in `antinvestor/deployments` to v1.0.0. Push and reconcile FluxCD.

- [ ] **Step 4: Verify pods are running**

```bash
kubectl get pods -n stawi-jobs
```

All 3 crawler pods + scheduler + api should be Running.

- [ ] **Step 5: Trigger data rebuild**

```bash
kubectl port-forward -n stawi-jobs deploy/stawi-jobs-crawler 8080:8080
curl -X POST http://localhost:8080/admin/rebuild-canonicals
```

Wait for rebuild to complete. Check logs for progress.

- [ ] **Step 6: Verify data integrity**

```bash
# Canonical count should now be <= variant count
SELECT count(*) FROM canonical_jobs;
SELECT count(*) FROM job_variants;
SELECT count(*) FROM job_clusters;
# These three should be roughly equal
```

- [ ] **Step 7: Monitor crawl cycle**

Watch one full crawl cycle:
- Sources with connection failures should start degrading
- Himalayas should now produce accepted jobs (fixed field mapping)
- Jobicy should work (fixed jobType)
- Quality gate should accept more jobs (company/location optional)
- Circuit breaker should pause dead sources after a few cycles

---

## Execution Notes

- **Tasks 1-6** should be executed sequentially — each builds on the previous
- **Task 7** (deploy) depends on all fixes being committed
- Tasks 1 and 2 are independent and could run in parallel
- Tasks 3 and 4 are independent and could run in parallel
- Task 5 depends on Task 4 (needs fixed dedupe)
- Task 6 is independent
