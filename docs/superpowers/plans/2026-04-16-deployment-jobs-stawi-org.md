# Deployment: jobs.stawi.org Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy jobs.stawi.org as a fully functional Hugo static site on Cloudflare Pages, with event-driven job publishing to R2, permanent slugs on canonical jobs, and OIDC authentication via a dedicated tenant in service-authentication.

**Architecture:** The crawl pipeline emits `job.ready` events which a new `PublishHandler` consumes to upload individual markdown files to Cloudflare R2. CF Pages syncs from R2 on build and runs Hugo. Authentication uses a new child tenant under Thesa with public OIDC clients for staging and production.

**Tech Stack:** Go (aws-sdk-go-v2 for R2), Hugo, Cloudflare Pages + R2, OIDC (Ory Hydra via service-authentication)

**Spec:** `docs/superpowers/specs/2026-04-16-deployment-jobs-stawi-org.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `pkg/domain/models.go` | Modify | Add `Slug` field to `CanonicalJob`, add `BuildSlug` + `Slugify` functions |
| `pkg/pipeline/handlers/payloads.go` | Modify | Add `EventJobPublished` constant |
| `pkg/pipeline/handlers/canonical.go` | Modify | Generate slug on first canonical creation |
| `pkg/pipeline/handlers/publish.go` | Create | PublishHandler: subscribes to `job.ready`, renders markdown, uploads to R2 |
| `pkg/publish/r2.go` | Create | R2Publisher: S3-compatible client for Cloudflare R2 |
| `pkg/publish/markdown.go` | Create | RenderJobMarkdown: generates Hugo-compatible markdown with frontmatter |
| `apps/crawler/config/config.go` | Modify | Add R2 config fields |
| `apps/crawler/cmd/main.go` | Modify | Wire PublishHandler + R2Publisher |
| `apps/sitegen/cmd/main.go` | Modify | Add `--r2-upload` backfill mode |
| `apps/api/cmd/main.go` | Modify | Include `slug` in canonical job API responses |
| `ui/hugo.toml` | Modify | Update baseURL, OIDC params for staging default |
| `ui/scripts/sync-r2.sh` | Create | Pre-build script: syncs content from R2 to local dirs |
| `ui/.gitignore` | Modify | Ignore synced content files |
| `.github/workflows/ci.yaml` | Modify | Add Hugo build verification |
| Service-auth migration (prod) | Create | `20260416_create_stawi_jobs_tenant.sql` |
| Service-auth migration (staging) | Create | `20260416_create_stawi_jobs_test_tenant.sql` |

## Task Dependency Graph

```
Task 1 (Slug on CanonicalJob) → Task 2 (Slug in CanonicalHandler)
                                         ↓
Task 3 (R2Publisher) → Task 4 (Markdown renderer) → Task 5 (PublishHandler)
                                                              ↓
Task 6 (Wire in crawler) → Task 7 (Sitegen backfill)

Task 8 (OIDC migrations) — independent
Task 9 (CF Pages setup) — depends on Task 3 (R2 bucket exists)
Task 10 (Hugo config + sync script) — depends on Task 9
```

**Parallel tracks:**
- **Track A (Go pipeline):** Tasks 1→2→3→4→5→6→7
- **Track B (OIDC):** Task 8 (independent)
- **Track C (Deployment):** Tasks 9→10

---

## Task 1: Add Slug to CanonicalJob Model

**Files:**
- Modify: `pkg/domain/models.go`

- [ ] **Step 1: Add Slug field to CanonicalJob struct**

In `pkg/domain/models.go`, add the `Slug` field after `ClusterID` in the `CanonicalJob` struct:

```go
Slug           string     `gorm:"type:varchar(255);uniqueIndex" json:"slug"`
```

The full line context — add after line 298 (`ClusterID`):

```go
type CanonicalJob struct {
	ID             int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	ClusterID      int64      `gorm:"not null;uniqueIndex" json:"cluster_id"`
	Slug           string     `gorm:"type:varchar(255);uniqueIndex" json:"slug"`
	Title          string     `gorm:"type:text;not null" json:"title"`
```

- [ ] **Step 2: Add BuildSlug and Slugify functions**

Add these functions at the end of `pkg/domain/models.go`, after the existing `containsAny` function:

```go
// Slugify converts a string into a URL-safe slug.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		" ", "-", "/", "-", "\\", "-", ".", "-",
		",", "", "'", "", "\"", "", "(", "", ")", "",
		"&", "and", "+", "plus",
	)
	s = replacer.Replace(s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// BuildSlug creates a permanent, unique, human-readable slug for a canonical job.
// Format: {title}-at-{company}-{6-char-hash}
// The hash is deterministic: SHA256(company|title|id), first 6 hex chars.
func BuildSlug(title, company string, id int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", company, title, id)))
	shortHash := hex.EncodeToString(h[:3])
	slug := fmt.Sprintf("%s-at-%s-%s", Slugify(title), Slugify(company), shortHash)
	if len(slug) > 250 {
		slug = slug[:250]
	}
	return slug
}
```

Add `"crypto/sha256"`, `"encoding/hex"`, and `"fmt"` to the import block if not already present.

- [ ] **Step 3: Verify build**

Run: `go build ./pkg/...`
Expected: clean compilation

- [ ] **Step 4: Commit**

```bash
git add pkg/domain/models.go
git commit -m "feat: add permanent Slug field to CanonicalJob with BuildSlug helper"
```

---

## Task 2: Generate Slug in CanonicalHandler

**Files:**
- Modify: `pkg/pipeline/handlers/canonical.go`

- [ ] **Step 1: Add slug generation after UpsertAndCluster**

In `pkg/pipeline/handlers/canonical.go`, in the `Execute` method, after the `dedupeEngine.UpsertAndCluster` call (around line 111) and inside the `if canonical != nil` block, add slug generation before the embedding step:

Find this code:

```go
	// 4. Generate and store embedding (non-fatal on failure).
	if canonical != nil {
```

Insert before the embedding comment:

```go
	// 3b. Generate permanent slug if not yet set.
	if canonical != nil && canonical.Slug == "" {
		canonical.Slug = domain.BuildSlug(canonical.Title, canonical.Company, canonical.ID)
		if slugErr := h.jobRepo.UpdateCanonicalFields(ctx, canonical.ID, map[string]any{"slug": canonical.Slug}); slugErr != nil {
			log.Printf("canonical: set slug for canonical %d (non-fatal): %v", canonical.ID, slugErr)
		}
	}
```

- [ ] **Step 2: Verify build**

Run: `go build ./pkg/... && go build ./apps/crawler/cmd`
Expected: clean compilation

- [ ] **Step 3: Commit**

```bash
git add pkg/pipeline/handlers/canonical.go
git commit -m "feat: generate permanent slug on first canonical job creation"
```

---

## Task 3: R2Publisher — S3-Compatible Upload Client

**Files:**
- Create: `pkg/publish/r2.go`

- [ ] **Step 1: Add aws-sdk-go-v2 dependencies**

```bash
cd /home/j/code/stawi.opportunities
go get github.com/aws/aws-sdk-go-v2@latest
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/credentials@latest
go get github.com/aws/aws-sdk-go-v2/service/s3@latest
```

- [ ] **Step 2: Create R2Publisher**

Create `pkg/publish/r2.go`:

```go
package publish

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// R2Publisher uploads content to a Cloudflare R2 bucket via the S3-compatible API.
type R2Publisher struct {
	client       *s3.Client
	bucket       string
	deployHookURL string
}

// NewR2Publisher creates an R2Publisher configured for the given Cloudflare account.
func NewR2Publisher(accountID, accessKeyID, secretKey, bucket, deployHookURL string) *R2Publisher {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)

	client := s3.New(s3.Options{
		Region:      "auto",
		Credentials: credentials.NewStaticCredentialsProvider(accessKeyID, secretKey, ""),
		BaseEndpoint: aws.String(endpoint),
	})

	return &R2Publisher{
		client:       client,
		bucket:       bucket,
		deployHookURL: deployHookURL,
	}
}

// Upload writes content to the given key in the R2 bucket.
func (p *R2Publisher) Upload(ctx context.Context, key string, content []byte) error {
	_, err := p.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("text/markdown; charset=utf-8"),
	})
	return err
}

// UploadJSON writes JSON content to the given key.
func (p *R2Publisher) UploadJSON(ctx context.Context, key string, content []byte) error {
	_, err := p.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("application/json; charset=utf-8"),
	})
	return err
}

// Delete removes a key from the R2 bucket.
func (p *R2Publisher) Delete(ctx context.Context, key string) error {
	_, err := p.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
	})
	return err
}

// Download reads content from R2. Returns nil, nil if not found.
func (p *R2Publisher) Download(ctx context.Context, key string) ([]byte, error) {
	out, err := p.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// TriggerDeploy POSTs to the Cloudflare Pages deploy hook to trigger a site rebuild.
func (p *R2Publisher) TriggerDeploy() error {
	if p.deployHookURL == "" {
		return nil
	}
	resp, err := http.Post(p.deployHookURL, "application/json", nil)
	if err != nil {
		return fmt.Errorf("deploy hook POST failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("deploy hook returned status %d", resp.StatusCode)
	}
	log.Printf("publish: deploy hook triggered successfully (status %d)", resp.StatusCode)
	return nil
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./pkg/publish/...`
Expected: clean compilation (may need `go mod tidy` first)

- [ ] **Step 4: Tidy modules and commit**

```bash
go mod tidy
git add pkg/publish/ go.mod go.sum
git commit -m "feat: add R2Publisher for Cloudflare R2 S3-compatible uploads"
```

---

## Task 4: Markdown Renderer

**Files:**
- Create: `pkg/publish/markdown.go`

- [ ] **Step 1: Create the markdown rendering function**

Create `pkg/publish/markdown.go`:

```go
package publish

import (
	"fmt"
	"strings"
	"time"

	"stawi.opportunities/pkg/domain"
)

// RenderJobMarkdown produces a Hugo-compatible markdown file with YAML frontmatter
// from a CanonicalJob. The slug is the permanent URL path for the job.
func RenderJobMarkdown(job *domain.CanonicalJob) []byte {
	var b strings.Builder

	postedAt := time.Now().Format(time.RFC3339)
	if job.PostedAt != nil {
		postedAt = job.PostedAt.Format(time.RFC3339)
	}

	category := string(domain.DeriveCategory(job.Roles, job.Industry))
	skills := formatSkillsYAML(job.Skills, job.RequiredSkills)
	isFeatured := job.QualityScore >= 80

	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("title: %q\n", job.Title))
	b.WriteString(fmt.Sprintf("date: %s\n", postedAt))
	b.WriteString(fmt.Sprintf("slug: %q\n", job.Slug))
	b.WriteString("params:\n")
	b.WriteString(fmt.Sprintf("  id: %d\n", job.ID))
	b.WriteString(fmt.Sprintf("  company: %q\n", job.Company))
	b.WriteString(fmt.Sprintf("  category: %q\n", category))
	b.WriteString(fmt.Sprintf("  location_text: %q\n", job.LocationText))
	b.WriteString(fmt.Sprintf("  remote_type: %q\n", job.RemoteType))
	b.WriteString(fmt.Sprintf("  employment_type: %q\n", job.EmploymentType))
	b.WriteString(fmt.Sprintf("  salary_min: %g\n", job.SalaryMin))
	b.WriteString(fmt.Sprintf("  salary_max: %g\n", job.SalaryMax))
	b.WriteString(fmt.Sprintf("  currency: %q\n", job.Currency))
	b.WriteString(fmt.Sprintf("  seniority: %q\n", job.Seniority))
	b.WriteString(skills)
	b.WriteString(fmt.Sprintf("  apply_url: %q\n", job.ApplyURL))
	b.WriteString(fmt.Sprintf("  quality_score: %g\n", job.QualityScore))
	b.WriteString(fmt.Sprintf("  is_featured: %t\n", isFeatured))
	b.WriteString("---\n\n")

	// Job description as markdown content
	if job.Description != "" {
		b.WriteString(job.Description)
		b.WriteString("\n")
	}

	return []byte(b.String())
}

// formatSkillsYAML renders skills as a YAML list under params.
func formatSkillsYAML(skills, requiredSkills string) string {
	raw := skills
	if raw == "" {
		raw = requiredSkills
	}
	if raw == "" {
		return "  skills: []\n"
	}

	parts := strings.Split(raw, ",")
	var cleaned []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return "  skills: []\n"
	}

	var b strings.Builder
	b.WriteString("  skills:\n")
	for _, s := range cleaned {
		b.WriteString(fmt.Sprintf("    - %q\n", s))
	}
	return b.String()
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./pkg/publish/...`
Expected: clean compilation

- [ ] **Step 3: Commit**

```bash
git add pkg/publish/markdown.go
git commit -m "feat: add markdown renderer for Hugo job pages"
```

---

## Task 5: PublishHandler — Event-Driven R2 Upload

**Files:**
- Create: `pkg/pipeline/handlers/publish.go`
- Modify: `pkg/pipeline/handlers/payloads.go`

- [ ] **Step 1: Add EventJobPublished constant to payloads.go**

In `pkg/pipeline/handlers/payloads.go`, add after `EventJobReady`:

```go
	EventJobPublished = "job.published"
```

- [ ] **Step 2: Create PublishHandler**

Create `pkg/pipeline/handlers/publish.go`:

```go
package handlers

import (
	"context"
	"errors"
	"log"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.opportunities/pkg/domain"
	"stawi.opportunities/pkg/publish"
	"stawi.opportunities/pkg/repository"
	"stawi.opportunities/pkg/telemetry"
)

var publishTracer = otel.Tracer("stawi.opportunities.publish")

// PublishHandler subscribes to job.ready events and uploads job markdown to R2.
type PublishHandler struct {
	jobRepo      *repository.JobRepository
	publisher    *publish.R2Publisher
	minQuality   float64
	publishCount atomic.Int64
	batchSize    int64
}

// NewPublishHandler creates a PublishHandler.
func NewPublishHandler(
	jobRepo *repository.JobRepository,
	publisher *publish.R2Publisher,
	minQuality float64,
) *PublishHandler {
	return &PublishHandler{
		jobRepo:    jobRepo,
		publisher:  publisher,
		minQuality: minQuality,
		batchSize:  50,
	}
}

func (h *PublishHandler) Name() string {
	return EventJobReady
}

func (h *PublishHandler) PayloadType() any {
	return &JobReadyPayload{}
}

func (h *PublishHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*JobReadyPayload)
	if !ok {
		return errors.New("invalid payload type, expected *JobReadyPayload")
	}
	if p.CanonicalJobID == 0 {
		return errors.New("canonical_job_id is required")
	}
	return nil
}

func (h *PublishHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*JobReadyPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	ctx, span := publishTracer.Start(ctx, "pipeline.publish")
	defer span.End()

	span.SetAttributes(attribute.Int64("canonical_job_id", p.CanonicalJobID))

	start := time.Now()
	defer func() {
		if telemetry.StageDuration != nil {
			telemetry.StageDuration.Record(ctx, time.Since(start).Seconds(),
				metric.WithAttributes(attribute.String("stage", "publish")),
			)
		}
	}()

	// 1. Load the canonical job.
	job, err := h.jobRepo.GetCanonicalByID(ctx, p.CanonicalJobID)
	if err != nil {
		return err
	}
	if job == nil {
		log.Printf("publish: canonical job %d not found, skipping", p.CanonicalJobID)
		return nil
	}

	// 2. Skip if below quality threshold or inactive.
	if job.QualityScore < h.minQuality {
		log.Printf("publish: canonical job %d quality %.1f below threshold %.1f, skipping",
			job.ID, job.QualityScore, h.minQuality)
		return nil
	}
	if !job.IsActive {
		log.Printf("publish: canonical job %d is inactive, skipping", job.ID)
		return nil
	}

	// 3. Ensure slug exists.
	if job.Slug == "" {
		job.Slug = domain.BuildSlug(job.Title, job.Company, job.ID)
		if slugErr := h.jobRepo.UpdateCanonicalFields(ctx, job.ID, map[string]any{"slug": job.Slug}); slugErr != nil {
			log.Printf("publish: set slug for job %d (non-fatal): %v", job.ID, slugErr)
		}
	}

	// 4. Render markdown.
	md := publish.RenderJobMarkdown(job)

	// 5. Upload to R2.
	key := "jobs/" + job.Slug + ".md"
	if err := h.publisher.Upload(ctx, key, md); err != nil {
		return fmt.Errorf("publish: upload %s to R2: %w", key, err)
	}

	log.Printf("publish: uploaded %s (%d bytes)", key, len(md))

	// 6. Trigger deploy hook every batchSize publishes.
	count := h.publishCount.Add(1)
	if count%h.batchSize == 0 {
		if hookErr := h.publisher.TriggerDeploy(); hookErr != nil {
			log.Printf("publish: deploy hook failed (non-fatal): %v", hookErr)
		}
	}

	return nil
}
```

Add `"fmt"` to the import block.

- [ ] **Step 3: Verify build**

Run: `go build ./pkg/pipeline/handlers/...`
Expected: clean compilation

- [ ] **Step 4: Commit**

```bash
git add pkg/pipeline/handlers/publish.go pkg/pipeline/handlers/payloads.go
git commit -m "feat: add PublishHandler for event-driven R2 job upload"
```

---

## Task 6: Wire PublishHandler in Crawler

**Files:**
- Modify: `apps/crawler/config/config.go`
- Modify: `apps/crawler/cmd/main.go`

- [ ] **Step 1: Add R2 config fields**

In `apps/crawler/config/config.go`, add after `ValkeyAddr`:

```go
	R2AccountID       string  `env:"R2_ACCOUNT_ID" envDefault:""`
	R2AccessKeyID     string  `env:"R2_ACCESS_KEY_ID" envDefault:""`
	R2SecretAccessKey string  `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Bucket          string  `env:"R2_BUCKET" envDefault:"opportunities-content"`
	R2DeployHookURL   string  `env:"R2_DEPLOY_HOOK_URL" envDefault:""`
	PublishMinQuality float64 `env:"PUBLISH_MIN_QUALITY" envDefault:"50"`
```

- [ ] **Step 2: Wire PublishHandler in crawler main.go**

In `apps/crawler/cmd/main.go`, add the import:

```go
	"stawi.opportunities/pkg/publish"
```

After the bloom filter initialization (around line 126) and before the pipeline handler registration, add:

```go
	// R2 publisher for job content.
	var r2Publisher *publish.R2Publisher
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" {
		r2Publisher = publish.NewR2Publisher(
			cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey,
			cfg.R2Bucket, cfg.R2DeployHookURL,
		)
		log.WithField("bucket", cfg.R2Bucket).Info("R2 publisher enabled")
	}
```

Then in the pipeline handler registration, add the PublishHandler. In the `if extractor != nil` block, add it after `NewSourceQualityHandler`:

```go
		pipelineHandlers = append(pipelineHandlers, frame.WithRegisterEvents(
			handlers.NewDedupHandler(jobRepo, svc),
			handlers.NewNormalizeHandler(jobRepo, sourceRepo, extractor, httpClient, svc),
			handlers.NewValidateHandler(jobRepo, sourceRepo, extractor, svc),
			handlers.NewCanonicalHandler(jobRepo, dedupeEngine, extractor, svc),
			handlers.NewSourceExpansionHandler(sourceRepo),
			handlers.NewSourceQualityHandler(sourceRepo, jobRepo, extractor),
		))
```

Change to:

```go
		eventHandlers := []any{
			handlers.NewDedupHandler(jobRepo, svc),
			handlers.NewNormalizeHandler(jobRepo, sourceRepo, extractor, httpClient, svc),
			handlers.NewValidateHandler(jobRepo, sourceRepo, extractor, svc),
			handlers.NewCanonicalHandler(jobRepo, dedupeEngine, extractor, svc),
			handlers.NewSourceExpansionHandler(sourceRepo),
			handlers.NewSourceQualityHandler(sourceRepo, jobRepo, extractor),
		}
		if r2Publisher != nil {
			eventHandlers = append(eventHandlers, handlers.NewPublishHandler(jobRepo, r2Publisher, cfg.PublishMinQuality))
		}
		pipelineHandlers = append(pipelineHandlers, frame.WithRegisterEvents(eventHandlers...))
```

Do the same for the `else` block (without extractor):

```go
		eventHandlers := []any{
			handlers.NewDedupHandler(jobRepo, svc),
			handlers.NewCanonicalHandler(jobRepo, dedupeEngine, nil, svc),
		}
		if r2Publisher != nil {
			eventHandlers = append(eventHandlers, handlers.NewPublishHandler(jobRepo, r2Publisher, cfg.PublishMinQuality))
		}
		pipelineHandlers = append(pipelineHandlers, frame.WithRegisterEvents(eventHandlers...))
```

- [ ] **Step 3: Verify build**

Run: `go build ./apps/crawler/cmd`
Expected: clean compilation

- [ ] **Step 4: Commit**

```bash
git add apps/crawler/config/config.go apps/crawler/cmd/main.go
git commit -m "feat: wire PublishHandler with R2 in crawler pipeline"
```

---

## Task 7: Sitegen R2 Backfill Mode

**Files:**
- Modify: `apps/sitegen/cmd/main.go`

- [ ] **Step 1: Read the existing sitegen file**

Read `apps/sitegen/cmd/main.go` to understand the current structure.

- [ ] **Step 2: Add R2 upload mode**

Add these flags after the existing flag definitions:

```go
	r2Upload    := flag.Bool("r2-upload", false, "Upload markdown files to R2 instead of local JSON")
	r2AccountID := flag.String("r2-account-id", os.Getenv("R2_ACCOUNT_ID"), "Cloudflare account ID")
	r2AccessKey := flag.String("r2-access-key", os.Getenv("R2_ACCESS_KEY_ID"), "R2 access key")
	r2SecretKey := flag.String("r2-secret-key", os.Getenv("R2_SECRET_ACCESS_KEY"), "R2 secret key")
	r2Bucket    := flag.String("r2-bucket", "opportunities-content", "R2 bucket name")
	deployHook  := flag.String("deploy-hook-url", os.Getenv("R2_DEPLOY_HOOK_URL"), "CF Pages deploy hook URL")
	batchSize   := flag.Int("batch-size", 500, "Upload batch size")
```

Add the import for the publish package:

```go
	"stawi.opportunities/pkg/publish"
```

After the `flag.Parse()` and database connection, add the R2 upload branch:

```go
	if *r2Upload {
		if *r2AccountID == "" || *r2AccessKey == "" {
			log.Fatal("--r2-account-id and --r2-access-key are required for R2 upload")
		}

		publisher := publish.NewR2Publisher(*r2AccountID, *r2AccessKey, *r2SecretKey, *r2Bucket, *deployHook)

		log.Println("R2 backfill: exporting jobs as markdown...")
		offset := 0
		total := 0
		for {
			jobs, err := jobRepo.ListActiveCanonical(ctx, *minQuality, *batchSize, offset)
			if err != nil {
				log.Fatalf("list jobs: %v", err)
			}
			if len(jobs) == 0 {
				break
			}

			for _, job := range jobs {
				// Ensure slug exists
				if job.Slug == "" {
					job.Slug = domain.BuildSlug(job.Title, job.Company, job.ID)
					_ = jobRepo.UpdateCanonicalFields(ctx, job.ID, map[string]any{"slug": job.Slug})
				}

				md := publish.RenderJobMarkdown(job)
				key := "jobs/" + job.Slug + ".md"
				if err := publisher.Upload(ctx, key, md); err != nil {
					log.Printf("  failed to upload %s: %v", key, err)
					continue
				}
				total++
			}
			log.Printf("  uploaded batch at offset %d (%d jobs so far)", offset, total)
			offset += *batchSize
		}

		log.Printf("R2 backfill: uploaded %d jobs", total)

		// Also upload stats and categories
		// (reuse existing stats/categories logic from the local export path)
		log.Println("R2 backfill: uploading stats and categories...")

		catCounts, _ := jobRepo.CountByCategory(ctx)
		categoryNames := map[string]string{
			"programming": "Programming", "design": "Design",
			"customer-support": "Customer Support", "marketing": "Marketing",
			"sales": "Sales", "devops": "DevOps & Infrastructure",
			"product": "Product", "data": "Data Science & Analytics",
			"management": "Management & Executive", "other": "Other",
		}
		var categories []categoryEntry
		for slug, count := range catCounts {
			name := categoryNames[slug]
			if name == "" {
				name = slug
			}
			categories = append(categories, categoryEntry{Slug: slug, Name: name, JobCount: count})
		}
		catJSON, _ := json.MarshalIndent(categories, "", "  ")
		_ = publisher.UploadJSON(ctx, "data/categories.json", catJSON)

		totalJobs, _ := jobRepo.CountCanonical(ctx)
		oneWeekAgo := time.Now().Add(-7 * 24 * time.Hour)
		var jobsThisWeek int64
		db.Model(&domain.CanonicalJob{}).Where("is_active = true AND created_at >= ?", oneWeekAgo).Count(&jobsThisWeek)
		statsJSON, _ := json.MarshalIndent(statsEntry{
			TotalJobs: totalJobs, TotalCompanies: int64(total),
			JobsThisWeek: jobsThisWeek, GeneratedAt: time.Now().Format(time.RFC3339),
		}, "", "  ")
		_ = publisher.UploadJSON(ctx, "data/stats.json", statsJSON)

		log.Println("R2 backfill: triggering deploy hook...")
		if err := publisher.TriggerDeploy(); err != nil {
			log.Printf("deploy hook failed: %v", err)
		}

		log.Println("R2 backfill: done")
		return
	}
```

Place this `if *r2Upload` block before the existing local export logic (the `// Export jobs` section).

- [ ] **Step 3: Verify build**

Run: `go build ./apps/sitegen/cmd`
Expected: clean compilation

- [ ] **Step 4: Commit**

```bash
git add apps/sitegen/cmd/main.go
git commit -m "feat: add --r2-upload backfill mode to sitegen CLI"
```

---

## Task 8: OIDC Migrations in service-authentication

**Files:**
- Create: `/home/j/code/antinvestor/service-authentication/apps/tenancy/migrations/0001/20260416_create_stawi_jobs_tenant.sql`
- Create: `/home/j/code/antinvestor/service-authentication/apps/tenancy/migrations/0001/20260416_create_stawi_jobs_test_tenant.sql`

- [ ] **Step 1: Generate XIDs for the new entities**

We need 10 unique XIDs (KSUID-like 20-char strings). Use a simple Go snippet or just create deterministic ones. For this plan, use these pre-generated values:

Production:
- Tenant ID: `ctqj8k0hijjg0sj0b01g`
- Partition ID: `ctqj8k0hijjg0sj0b020`
- Client row ID: `ctqj8k0hijjg0sj0b030`
- Client ID (OAuth2): `opportunities-web`
- Role owner: `ctqj8k0hijjg0sj0b040`
- Role admin: `ctqj8k0hijjg0sj0b041`
- Role member: `ctqj8k0hijjg0sj0b042`

Staging:
- Tenant ID: `ctqj8k0hijjg0sj0b050`
- Partition ID: `ctqj8k0hijjg0sj0b060`
- Client row ID: `ctqj8k0hijjg0sj0b070`
- Client ID (OAuth2): `opportunities-web-dev`
- Role owner: `ctqj8k0hijjg0sj0b080`
- Role admin: `ctqj8k0hijjg0sj0b081`
- Role member: `ctqj8k0hijjg0sj0b082`

**NOTE:** Before committing, generate real XIDs using `xid.New().String()` in a Go scratch file or use the project's ID generation pattern. The IDs above are placeholders for the plan structure.

- [ ] **Step 2: Create production migration**

Create `/home/j/code/antinvestor/service-authentication/apps/tenancy/migrations/0001/20260416_create_stawi_jobs_tenant.sql`:

```sql
--- Copyright 2023-2026 Ant Investor Ltd
---
--- Licensed under the Apache License, Version 2.0 (the "License");
--- you may not use this file except in compliance with the License.
--- You may obtain a copy of the License at
---
---      http://www.apache.org/licenses/LICENSE-2.0
---
--- Unless required by applicable law or agreed to in writing, software
--- distributed under the License is distributed on an "AS IS" BASIS,
--- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
--- See the License for the specific language governing permissions and
--- limitations under the License.

-- Tenant: Stawi Jobs (production)
INSERT INTO tenants (id, tenant_id, partition_id, name, description, environment)
VALUES ('ctqj8k0hijjg0sj0b01g', 'c2f4j7au6s7f91uqnojg', 'c2f4j7au6s7f91uqnokg',
        'Stawi Jobs', 'Remote job board platform for Africa and beyond', 'production');

-- Partition: Stawi Jobs (production)
INSERT INTO partitions (id, tenant_id, partition_id, parent_id, name, description, domain, allow_auto_access, properties)
VALUES ('ctqj8k0hijjg0sj0b020', 'ctqj8k0hijjg0sj0b01g', 'ctqj8k0hijjg0sj0b020',
        'c2f4j7au6s7f91uqnokg',
        'Stawi Jobs', 'Remote job board platform', 'jobs.stawi.org', 'true',
        '{
          "default_role": "user",
          "allow_auto_access": true,
          "support_contacts": {
            "email": "hello@stawi.opportunities"
          }
        }');

-- Standard partition roles (owner, admin, member)
INSERT INTO partition_roles (id, created_at, modified_at, version, tenant_id, partition_id, name, is_default, properties)
VALUES
    ('ctqj8k0hijjg0sj0b040', NOW(), NOW(), 1,
     'ctqj8k0hijjg0sj0b01g', 'ctqj8k0hijjg0sj0b020',
     'owner', false, '{"description": "Full control across all services"}'),
    ('ctqj8k0hijjg0sj0b041', NOW(), NOW(), 1,
     'ctqj8k0hijjg0sj0b01g', 'ctqj8k0hijjg0sj0b020',
     'admin', false, '{"description": "Manage partitions, access, roles, and pages"}'),
    ('ctqj8k0hijjg0sj0b042', NOW(), NOW(), 1,
     'ctqj8k0hijjg0sj0b01g', 'ctqj8k0hijjg0sj0b020',
     'member', true, '{"description": "Read-only access, auto-assigned on access creation"}')
ON CONFLICT (id) DO NOTHING;

-- Public OIDC client for Stawi Jobs (authorization_code + PKCE)
INSERT INTO clients (
    id, tenant_id, partition_id, name, client_id,
    type, grant_types, response_types, scopes, audiences, redirect_uris,
    post_logout_redirect_uris, token_endpoint_auth_method
) VALUES (
    'ctqj8k0hijjg0sj0b030',
    'ctqj8k0hijjg0sj0b01g',
    'ctqj8k0hijjg0sj0b020',
    'Stawi Jobs Web',
    'opportunities-web',
    'public',
    '{"types": ["authorization_code", "refresh_token"]}',
    '{"types": ["code"]}',
    'openid offline_access profile',
    '{"service_profile": ["*"]}',
    '{"uris": ["https://jobs.stawi.org/auth/callback/"]}',
    '{"uris": ["https://jobs.stawi.org/"]}',
    'none'
) ON CONFLICT (id) DO NOTHING;
```

- [ ] **Step 3: Create staging migration**

Create `/home/j/code/antinvestor/service-authentication/apps/tenancy/migrations/0001/20260416_create_stawi_jobs_test_tenant.sql`:

```sql
--- Copyright 2023-2026 Ant Investor Ltd
---
--- Licensed under the Apache License, Version 2.0 (the "License");
--- you may not use this file except in compliance with the License.
--- You may obtain a copy of the License at
---
---      http://www.apache.org/licenses/LICENSE-2.0
---
--- Unless required by applicable law or agreed to in writing, software
--- distributed under the License is distributed on an "AS IS" BASIS,
--- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
--- See the License for the specific language governing permissions and
--- limitations under the License.

-- Tenant: Stawi Jobs Development (staging)
INSERT INTO tenants (id, tenant_id, partition_id, name, description, environment)
VALUES ('ctqj8k0hijjg0sj0b050', 'c2f4j7au6s7f91uqnojg', 'c2f4j7au6s7f91uqnokg',
        'Stawi Jobs Development', 'Staging tenant for jobs.stawi.org', 'staging');

-- Partition: Stawi Jobs Development (staging)
INSERT INTO partitions (id, tenant_id, partition_id, parent_id, name, description, domain, allow_auto_access, properties)
VALUES ('ctqj8k0hijjg0sj0b060', 'ctqj8k0hijjg0sj0b050', 'ctqj8k0hijjg0sj0b060',
        'c2f4j7au6s7f91uqnokg',
        'Stawi Jobs Development', 'Staging partition for jobs.stawi.org', 'jobs-dev.stawi.org', 'true',
        '{
          "default_role": "user",
          "allow_auto_access": true,
          "support_contacts": {
            "email": "hello@stawi.opportunities"
          }
        }');

-- Standard partition roles (owner, admin, member)
INSERT INTO partition_roles (id, created_at, modified_at, version, tenant_id, partition_id, name, is_default, properties)
VALUES
    ('ctqj8k0hijjg0sj0b080', NOW(), NOW(), 1,
     'ctqj8k0hijjg0sj0b050', 'ctqj8k0hijjg0sj0b060',
     'owner', false, '{"description": "Full control across all services"}'),
    ('ctqj8k0hijjg0sj0b081', NOW(), NOW(), 1,
     'ctqj8k0hijjg0sj0b050', 'ctqj8k0hijjg0sj0b060',
     'admin', false, '{"description": "Manage partitions, access, roles, and pages"}'),
    ('ctqj8k0hijjg0sj0b082', NOW(), NOW(), 1,
     'ctqj8k0hijjg0sj0b050', 'ctqj8k0hijjg0sj0b060',
     'member', true, '{"description": "Read-only access, auto-assigned on access creation"}')
ON CONFLICT (id) DO NOTHING;

-- Public OIDC client for Stawi Jobs Development (authorization_code + PKCE)
INSERT INTO clients (
    id, tenant_id, partition_id, name, client_id,
    type, grant_types, response_types, scopes, audiences, redirect_uris,
    post_logout_redirect_uris, token_endpoint_auth_method
) VALUES (
    'ctqj8k0hijjg0sj0b070',
    'ctqj8k0hijjg0sj0b050',
    'ctqj8k0hijjg0sj0b060',
    'Stawi Jobs Development',
    'opportunities-web-dev',
    'public',
    '{"types": ["authorization_code", "refresh_token"]}',
    '{"types": ["code"]}',
    'openid offline_access profile',
    '{"service_profile": ["*"]}',
    '{"uris": ["https://jobs-dev.stawi.org/auth/callback/", "http://localhost:1313/auth/callback/"]}',
    '{"uris": ["https://jobs-dev.stawi.org/", "http://localhost:1313/"]}',
    'none'
) ON CONFLICT (id) DO NOTHING;
```

- [ ] **Step 4: Commit in service-authentication repo**

```bash
cd /home/j/code/antinvestor/service-authentication
git add apps/tenancy/migrations/0001/20260416_create_stawi_jobs_tenant.sql
git add apps/tenancy/migrations/0001/20260416_create_stawi_jobs_test_tenant.sql
git commit -m "feat: add Stawi Jobs tenant, partition, and OIDC client (prod + staging)"
```

---

## Task 9: Hugo Config + R2 Sync Script

**Files:**
- Modify: `ui/hugo.toml`
- Create: `ui/scripts/sync-r2.sh`
- Modify: `ui/.gitignore`

- [ ] **Step 1: Update hugo.toml**

In `ui/hugo.toml`, update these values:

Change `baseURL`:
```toml
baseURL = "https://jobs.stawi.org/"
```

Update `[params]` to use staging OIDC client by default:
```toml
[params]
  description = "Find remote jobs from top companies hiring worldwide. Browse 37,000+ positions in programming, design, marketing, and more."
  apiURL = "http://localhost:8082"
  candidatesAPIURL = "http://localhost:8080"
  oidcIssuer = "https://auth-dev.antinvestor.com"
  oidcClientID = "opportunities-web-dev"
  oidcRedirectURI = "http://localhost:1313/auth/callback/"
```

- [ ] **Step 2: Create sync-r2.sh**

```bash
mkdir -p ui/scripts
```

Create `ui/scripts/sync-r2.sh`:

```bash
#!/bin/bash
set -euo pipefail

# Pre-build script for Cloudflare Pages: syncs content from R2 bucket.
# Environment variables required:
#   AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, R2_ACCOUNT_ID, R2_BUCKET

if [ -z "${R2_ACCOUNT_ID:-}" ]; then
    echo "R2_ACCOUNT_ID not set, skipping R2 sync (using local content)"
    exit 0
fi

R2_ENDPOINT="https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com"
BUCKET="${R2_BUCKET:-opportunities-content}"

echo "Installing AWS CLI..."
pip install awscli --quiet 2>/dev/null

echo "Syncing job content from R2 (s3://${BUCKET}/jobs/)..."
mkdir -p content/jobs
aws s3 sync "s3://${BUCKET}/jobs/" content/jobs/ \
    --endpoint-url "$R2_ENDPOINT" --quiet

echo "Syncing category content from R2..."
mkdir -p content/categories
aws s3 sync "s3://${BUCKET}/categories/" content/categories/ \
    --endpoint-url "$R2_ENDPOINT" --quiet

echo "Syncing data files from R2..."
mkdir -p data
aws s3 cp "s3://${BUCKET}/data/stats.json" data/stats.json \
    --endpoint-url "$R2_ENDPOINT" --quiet 2>/dev/null || echo '{"total_jobs":0,"total_companies":0,"jobs_this_week":0}' > data/stats.json
aws s3 cp "s3://${BUCKET}/data/categories.json" data/categories.json \
    --endpoint-url "$R2_ENDPOINT" --quiet 2>/dev/null || echo '[]' > data/categories.json

JOB_COUNT=$(find content/jobs -name '*.md' -not -name '_index.md' 2>/dev/null | wc -l)
echo "Sync complete. ${JOB_COUNT} job files ready for Hugo build."
```

Make it executable:
```bash
chmod +x ui/scripts/sync-r2.sh
```

- [ ] **Step 3: Update .gitignore**

In `ui/.gitignore`, add a comment and entries for R2-synced content that should not be committed (they're pulled at build time):

```
# R2-synced content (pulled during CF Pages build)
# Keep sample files in git for local dev, but real content comes from R2
```

- [ ] **Step 4: Verify Hugo still builds locally**

Run: `cd /home/j/code/stawi.opportunities/ui && hugo --minify`
Expected: builds with local sample content (sync script skipped since R2_ACCOUNT_ID is not set)

- [ ] **Step 5: Commit**

```bash
git add ui/hugo.toml ui/scripts/sync-r2.sh ui/.gitignore
git commit -m "feat: add R2 sync script and update Hugo config for jobs.stawi.org"
```

---

## Task 10: API Slug Exposure + Final Wiring

**Files:**
- Modify: `apps/api/cmd/main.go`
- Modify: `Makefile`

- [ ] **Step 1: Ensure slug is in API responses**

The `CanonicalJob` struct already has `Slug` with a `json:"slug"` tag (added in Task 1), so it's automatically included in all JSON API responses. No code change needed — just verify.

Run: `grep -n 'Slug' /home/j/code/stawi.opportunities/pkg/domain/models.go`
Expected: `Slug string ... json:"slug"` is present.

- [ ] **Step 2: Add R2 backfill target to Makefile**

In `/home/j/code/stawi.opportunities/Makefile`, add after the existing `sitegen` target:

```makefile
r2-backfill:
	go run ./apps/sitegen/cmd --r2-upload
```

- [ ] **Step 3: Update the `ui-build` chain for CF Pages compatibility**

In the Makefile, update the `hugo-build` target to run the sync script first (for local builds against R2):

```makefile
hugo-build: sitegen
	cd ui && chmod +x scripts/sync-r2.sh && ./scripts/sync-r2.sh; hugo --minify
```

Note the `;` instead of `&&` after sync-r2.sh — the sync will skip gracefully if R2 env vars aren't set, and Hugo should still build with local content.

- [ ] **Step 4: Verify full build**

Run:
```bash
go build ./apps/... && go build ./pkg/...
cd /home/j/code/stawi.opportunities/ui && hugo --minify
```
Expected: both pass

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "feat: add r2-backfill target and update build pipeline for R2 sync"
```
