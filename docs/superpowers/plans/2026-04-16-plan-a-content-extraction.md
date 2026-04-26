# Plan A: Content Extraction + Data Model + Bloom Filter

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add content extraction pipeline (trafilatura + markdown), bloom filter for fast dedup, stage tracking on variants, and source quality window fields. This is the foundation for the staged pipeline (Plan B).

**Architecture:** New `pkg/content/` package handles HTML → main content extraction → markdown conversion. Bloom filter uses Valkey (with DB fallback when unavailable). Stage field on `job_variants` tracks pipeline position. Source quality sliding window tracks validation rates. The crawler stores raw_html + clean_html + markdown on every variant.

**Tech Stack:** go-trafilatura v1.12.2, html-to-markdown v1.6.0, Valkey (Redis-compatible), GORM

---

## File Structure

### New Files
```
pkg/content/extract.go          ← main content extraction (trafilatura + markdown)
pkg/content/extract_test.go     ← tests
pkg/bloom/filter.go             ← bloom filter via Valkey with DB fallback
pkg/bloom/filter_test.go        ← tests
```

### Modified Files
```
pkg/domain/models.go            ← add stage, raw_html, clean_html, markdown, validation fields to JobVariant; quality window fields to Source
pkg/connectors/httpx/client.go  ← add encoding normalization / decompression
go.mod                          ← add go-trafilatura, html-to-markdown
```

---

## Task 1: Content Extraction Package

**Files:**
- Create: `pkg/content/extract.go`
- Create: `pkg/content/extract_test.go`

- [ ] **Step 1: Add dependencies**

```bash
cd /home/j/code/stawi.opportunities
go get github.com/markusmobius/go-trafilatura@latest
go get github.com/JohannesKaufmann/html-to-markdown/v2@latest
go mod tidy
```

- [ ] **Step 2: Create content extraction package**

Create `pkg/content/extract.go`:

```go
package content

import (
    "bytes"
    "strings"

    "github.com/JohannesKaufmann/html-to-markdown/v2"
    "github.com/markusmobius/go-trafilatura"
)

// Extracted holds the three forms of content from a web page.
type Extracted struct {
    RawHTML   string // original HTTP response
    CleanHTML string // main content extracted (nav/ads/footer removed)
    Markdown  string // clean markdown for AI and humans
}

// ExtractFromHTML takes raw HTML and returns all three content forms.
// Uses go-trafilatura to extract main content, then converts to markdown.
func ExtractFromHTML(rawHTML string) (*Extracted, error) {
    result := &Extracted{
        RawHTML: rawHTML,
    }

    // Extract main content using trafilatura
    reader := strings.NewReader(rawHTML)
    opts := trafilatura.Options{
        IncludeLinks:  true,
        IncludeImages: false,
        IncludeTables: true,
    }

    extracted, err := trafilatura.Extract(reader, opts)
    if err != nil || extracted == nil {
        // Fallback: use raw HTML as clean HTML if extraction fails
        result.CleanHTML = rawHTML
    } else {
        result.CleanHTML = extracted.ContentHTML
        // If trafilatura returned text but no HTML, use the text
        if result.CleanHTML == "" {
            result.CleanHTML = extracted.ContentText
        }
    }

    // Convert clean HTML to markdown
    md, mdErr := htmltomarkdown.ConvertString(result.CleanHTML)
    if mdErr != nil {
        // Fallback: strip tags manually
        result.Markdown = stripToText(result.CleanHTML)
    } else {
        result.Markdown = strings.TrimSpace(md)
    }

    // If markdown is empty, use raw text extraction
    if result.Markdown == "" && extracted != nil {
        result.Markdown = extracted.ContentText
    }

    return result, nil
}

// ExtractFromJSON creates an Extracted from structured JSON API response.
// No trafilatura needed — just generate markdown from description.
func ExtractFromJSON(jsonResponse string, description string) *Extracted {
    md, err := htmltomarkdown.ConvertString(description)
    if err != nil {
        md = stripToText(description)
    }
    return &Extracted{
        RawHTML:   jsonResponse,
        CleanHTML: "", // not applicable for JSON APIs
        Markdown:  strings.TrimSpace(md),
    }
}

// stripToText removes HTML tags as a last resort fallback.
func stripToText(html string) string {
    // Simple tag stripping — not used if html-to-markdown works
    var buf bytes.Buffer
    inTag := false
    for _, r := range html {
        if r == '<' {
            inTag = true
            continue
        }
        if r == '>' {
            inTag = false
            buf.WriteRune(' ')
            continue
        }
        if !inTag {
            buf.WriteRune(r)
        }
    }
    // Collapse whitespace
    result := buf.String()
    fields := strings.Fields(result)
    return strings.Join(fields, " ")
}
```

- [ ] **Step 3: Create tests**

Create `pkg/content/extract_test.go`:

```go
package content

import (
    "strings"
    "testing"
)

func TestExtractFromHTML_BasicPage(t *testing.T) {
    html := `<html><head><title>Test</title></head>
    <body>
        <nav><a href="/">Home</a><a href="/about">About</a></nav>
        <main>
            <h1>Software Engineer</h1>
            <p>We are looking for a talented engineer to join our team.</p>
            <p>Requirements: Go, PostgreSQL, Kubernetes</p>
        </main>
        <footer>Copyright 2026</footer>
    </body></html>`

    result, err := ExtractFromHTML(html)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if result.RawHTML != html {
        t.Error("RawHTML should be original HTML")
    }

    if result.Markdown == "" {
        t.Error("Markdown should not be empty")
    }

    if !strings.Contains(result.Markdown, "Software Engineer") {
        t.Error("Markdown should contain the job title")
    }
}

func TestExtractFromHTML_EmptyHTML(t *testing.T) {
    result, err := ExtractFromHTML("")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if result.RawHTML != "" {
        t.Error("RawHTML should be empty")
    }
}

func TestExtractFromJSON(t *testing.T) {
    json := `{"title": "Engineer", "description": "<p>Build things</p>"}`
    desc := "<p>Build <strong>amazing</strong> things with <a href='#'>Go</a></p>"

    result := ExtractFromJSON(json, desc)

    if result.RawHTML != json {
        t.Error("RawHTML should be JSON response")
    }
    if result.CleanHTML != "" {
        t.Error("CleanHTML should be empty for JSON")
    }
    if !strings.Contains(result.Markdown, "Build") {
        t.Error("Markdown should contain description content")
    }
    if strings.Contains(result.Markdown, "<p>") {
        t.Error("Markdown should not contain HTML tags")
    }
}

func TestStripToText(t *testing.T) {
    html := "<h1>Title</h1><p>Some <strong>bold</strong> text</p>"
    text := stripToText(html)
    if !strings.Contains(text, "Title") || !strings.Contains(text, "bold") {
        t.Errorf("stripToText failed: %q", text)
    }
    if strings.Contains(text, "<") {
        t.Error("should not contain HTML tags")
    }
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./pkg/content/... -v`

- [ ] **Step 5: Commit**

```bash
git add pkg/content/ go.mod go.sum
git commit -m "feat: add content extraction package (trafilatura + markdown conversion)"
```

---

## Task 2: Data Model Changes

**Files:**
- Modify: `pkg/domain/models.go`

- [ ] **Step 1: Add stage and content fields to JobVariant**

Read `pkg/domain/models.go`. Add these fields to the `JobVariant` struct, after the existing `ContentHash` field and before `CreatedAt`:

```go
    // Pipeline stage tracking
    Stage            string    `gorm:"type:varchar(20);not null;default:'raw';index" json:"stage"`

    // Stored content forms (for reprocessing without recrawling)
    RawHTML          string    `gorm:"type:text" json:"-"`
    CleanHTML        string    `gorm:"type:text" json:"-"`
    Markdown         string    `gorm:"type:text" json:"-"`

    // Validation (populated in Stage 3)
    ValidationScore  *float64  `gorm:"type:real" json:"validation_score"`
    ValidationNotes  string    `gorm:"type:text" json:"validation_notes"`

    // Source discovery (populated in Stage 2)
    DiscoveredURLs   string    `gorm:"type:text" json:"discovered_urls"`
```

Also add pipeline stage constants:

```go
// Pipeline stage for job variants.
type VariantStage string

const (
    StageRaw        VariantStage = "raw"
    StageDeduped    VariantStage = "deduped"
    StageNormalized VariantStage = "normalized"
    StageValidated  VariantStage = "validated"
    StageReady      VariantStage = "ready"
    StageFlagged    VariantStage = "flagged"
)
```

- [ ] **Step 2: Add quality window fields to Source**

Add these fields to the `Source` struct, before `CreatedAt`:

```go
    // Source quality sliding window
    QualityWindowStart  *time.Time `json:"quality_window_start"`
    QualityWindowDays   int        `gorm:"not null;default:1" json:"quality_window_days"`
    QualityValidated    int        `gorm:"not null;default:0" json:"quality_validated"`
    QualityFlagged      int        `gorm:"not null;default:0" json:"quality_flagged"`
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git add pkg/domain/models.go
git commit -m "feat: add pipeline stage, content fields, and quality window to domain models"
```

---

## Task 3: Bloom Filter Package

**Files:**
- Create: `pkg/bloom/filter.go`
- Create: `pkg/bloom/filter_test.go`

- [ ] **Step 1: Add Valkey/Redis dependency**

```bash
go get github.com/redis/go-redis/v9@latest
```

- [ ] **Step 2: Create bloom filter**

Create `pkg/bloom/filter.go`:

```go
package bloom

import (
    "context"
    "hash/crc32"
    "fmt"

    "github.com/redis/go-redis/v9"
    "gorm.io/gorm"

    "stawi.opportunities/pkg/domain"
)

const bitmapSize = 1 << 20 // 1M bits per source, ~128KB

// Filter provides fast duplicate detection using Valkey bitmap with DB fallback.
type Filter struct {
    valkey *redis.Client // nil if Valkey unavailable
    db     func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewFilter creates a bloom filter. If valkeyAddr is empty, uses DB-only mode.
func NewFilter(valkeyAddr string, db func(ctx context.Context, readOnly bool) *gorm.DB) *Filter {
    f := &Filter{db: db}

    if valkeyAddr != "" {
        client := redis.NewClient(&redis.Options{
            Addr: valkeyAddr,
        })
        // Test connection
        if err := client.Ping(context.Background()).Err(); err == nil {
            f.valkey = client
        }
    }

    return f
}

// IsSeen checks if a hard_key has been seen for a source.
// Returns true if definitely seen (skip this job).
// Returns false if possibly new (caller should check DB to confirm).
func (f *Filter) IsSeen(ctx context.Context, sourceID int64, hardKey string) bool {
    // Step 1: Check Valkey bloom filter (fast)
    if f.valkey != nil {
        key := fmt.Sprintf("bloom:seen:%d", sourceID)
        offset := bloomOffset(hardKey)
        val, err := f.valkey.GetBit(ctx, key, int64(offset)).Result()
        if err == nil && val == 1 {
            return true // definitely seen (or false positive, <1%)
        }
        // If Valkey says not seen or errored, fall through to DB
    }

    // Step 2: Check DB (accurate)
    var count int64
    f.db(ctx, true).Model(&domain.JobVariant{}).
        Where("hard_key = ?", hardKey).
        Count(&count)
    return count > 0
}

// MarkSeen marks a hard_key as seen in the bloom filter.
func (f *Filter) MarkSeen(ctx context.Context, sourceID int64, hardKey string) {
    if f.valkey == nil {
        return // DB-only mode, no bloom to update
    }
    key := fmt.Sprintf("bloom:seen:%d", sourceID)
    offset := bloomOffset(hardKey)
    f.valkey.SetBit(ctx, key, int64(offset), 1)
}

// Close cleans up the Valkey connection.
func (f *Filter) Close() error {
    if f.valkey != nil {
        return f.valkey.Close()
    }
    return nil
}

// bloomOffset computes the bit offset from a hard_key string.
func bloomOffset(hardKey string) uint32 {
    return crc32.ChecksumIEEE([]byte(hardKey)) % bitmapSize
}
```

- [ ] **Step 3: Create tests**

Create `pkg/bloom/filter_test.go`:

```go
package bloom

import (
    "testing"
)

func TestBloomOffset_Deterministic(t *testing.T) {
    key := "abc123def456"
    offset1 := bloomOffset(key)
    offset2 := bloomOffset(key)
    if offset1 != offset2 {
        t.Errorf("bloom offset not deterministic: %d != %d", offset1, offset2)
    }
}

func TestBloomOffset_InRange(t *testing.T) {
    keys := []string{"test1", "test2", "a-very-long-key-with-lots-of-characters"}
    for _, k := range keys {
        offset := bloomOffset(k)
        if offset >= bitmapSize {
            t.Errorf("offset %d exceeds bitmap size %d for key %q", offset, bitmapSize, k)
        }
    }
}

func TestBloomOffset_Different(t *testing.T) {
    offset1 := bloomOffset("key1")
    offset2 := bloomOffset("key2")
    // Not guaranteed different, but very likely with distinct inputs
    if offset1 == offset2 {
        t.Log("WARN: collision detected (possible but unlikely)")
    }
}

func TestFilter_DBOnly_NotSeen(t *testing.T) {
    // With nil Valkey and nil DB func, IsSeen should not panic
    // This is a smoke test — real DB tests need a test database
    f := &Filter{valkey: nil, db: nil}
    // Can't call IsSeen without a DB func, but MarkSeen with nil should be safe
    f.MarkSeen(nil, 1, "test-key")
    // No panic = pass
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./pkg/bloom/... -v`

- [ ] **Step 5: Commit**

```bash
git add pkg/bloom/ go.mod go.sum
git commit -m "feat: add bloom filter package with Valkey bitmap + DB fallback"
```

---

## Task 4: Repository Updates for Stage Tracking

**Files:**
- Modify: `pkg/repository/job.go`
- Modify: `pkg/repository/source.go`

- [ ] **Step 1: Add stage management methods to JobRepository**

Read `pkg/repository/job.go`. Add:

```go
// UpdateStage sets the pipeline stage for a variant.
func (r *JobRepository) UpdateStage(ctx context.Context, variantID int64, stage domain.VariantStage) error {
    return r.db(ctx, false).Model(&domain.JobVariant{}).
        Where("id = ?", variantID).
        Update("stage", stage).Error
}

// UpdateStageWithContent sets stage and content fields together.
func (r *JobRepository) UpdateStageWithContent(ctx context.Context, variantID int64, stage domain.VariantStage, rawHTML, cleanHTML, markdown string) error {
    return r.db(ctx, false).Model(&domain.JobVariant{}).
        Where("id = ?", variantID).
        Updates(map[string]any{
            "stage":      stage,
            "raw_html":   rawHTML,
            "clean_html": cleanHTML,
            "markdown":   markdown,
        }).Error
}

// UpdateValidation sets validation results on a variant.
func (r *JobRepository) UpdateValidation(ctx context.Context, variantID int64, stage domain.VariantStage, score float64, notes string) error {
    return r.db(ctx, false).Model(&domain.JobVariant{}).
        Where("id = ?", variantID).
        Updates(map[string]any{
            "stage":            stage,
            "validation_score": score,
            "validation_notes": notes,
        }).Error
}

// ListByStage returns variants at a given pipeline stage.
func (r *JobRepository) ListByStage(ctx context.Context, stage domain.VariantStage, limit int) ([]*domain.JobVariant, error) {
    var variants []*domain.JobVariant
    err := r.db(ctx, true).
        Where("stage = ?", stage).
        Order("id ASC").
        Limit(limit).
        Find(&variants).Error
    return variants, err
}

// CountByStage returns the count of variants at each stage.
func (r *JobRepository) CountByStage(ctx context.Context) (map[string]int64, error) {
    type result struct {
        Stage string
        Count int64
    }
    var results []result
    err := r.db(ctx, true).
        Model(&domain.JobVariant{}).
        Select("stage, count(*) as count").
        Group("stage").
        Find(&results).Error
    if err != nil {
        return nil, err
    }
    m := make(map[string]int64, len(results))
    for _, r := range results {
        m[r.Stage] = r.Count
    }
    return m, nil
}
```

- [ ] **Step 2: Add quality window methods to SourceRepository**

Read `pkg/repository/source.go`. Add:

```go
// IncrementQualityValidated increments the validated count in the quality window.
func (r *SourceRepository) IncrementQualityValidated(ctx context.Context, id int64) error {
    return r.db(ctx, false).Model(&domain.Source{}).
        Where("id = ?", id).
        UpdateColumn("quality_validated", gorm.Expr("quality_validated + 1")).Error
}

// IncrementQualityFlagged increments the flagged count in the quality window.
func (r *SourceRepository) IncrementQualityFlagged(ctx context.Context, id int64) error {
    return r.db(ctx, false).Model(&domain.Source{}).
        Where("id = ?", id).
        UpdateColumn("quality_flagged", gorm.Expr("quality_flagged + 1")).Error
}

// GetQualityRate returns the failure rate for a source's quality window.
// Returns (rate, total) where rate = flagged / total.
func (r *SourceRepository) GetQualityRate(ctx context.Context, id int64) (float64, int, error) {
    var src domain.Source
    err := r.db(ctx, true).Select("quality_validated, quality_flagged").Where("id = ?", id).First(&src).Error
    if err != nil {
        return 0, 0, err
    }
    total := src.QualityValidated + src.QualityFlagged
    if total == 0 {
        return 0, 0, nil
    }
    return float64(src.QualityFlagged) / float64(total), total, nil
}

// ResetQualityWindow resets the quality counters and doubles the window (cap at 14 days).
func (r *SourceRepository) ResetQualityWindow(ctx context.Context, id int64) error {
    now := time.Now()
    return r.db(ctx, false).Model(&domain.Source{}).
        Where("id = ?", id).
        Updates(map[string]any{
            "quality_window_start": now,
            "quality_window_days":  gorm.Expr("LEAST(quality_window_days * 2, 14)"),
            "quality_validated":    0,
            "quality_flagged":      0,
        }).Error
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git add pkg/repository/job.go pkg/repository/source.go
git commit -m "feat: add stage tracking and quality window repository methods"
```

---

## Task 5: Wire Content Extraction into Connectors

**Files:**
- Modify: `pkg/connectors/httpx/client.go`
- Modify: `pkg/connectors/universal/universal.go`
- Modify: `pkg/connectors/connector.go`

- [ ] **Step 1: Update CrawlIterator to include content**

Read `pkg/connectors/connector.go`. Add a `Content()` method to the `CrawlIterator` interface:

```go
import "stawi.opportunities/pkg/content"

type CrawlIterator interface {
    Next(ctx context.Context) bool
    Jobs() []domain.ExternalJob
    RawPayload() []byte
    HTTPStatus() int
    Err() error
    Cursor() json.RawMessage
    Content() *content.Extracted  // NEW: extracted content for current page
}
```

Update `SinglePageIterator` to implement the new method:

```go
type SinglePageIterator struct {
    // existing fields...
    content *content.Extracted
}

func (it *SinglePageIterator) Content() *content.Extracted { return it.content }
```

- [ ] **Step 2: Update universal connector to extract content**

Read `pkg/connectors/universal/universal.go`. In the `Next()` method, after fetching the page:

```go
// Extract main content from HTML
extracted, _ := content.ExtractFromHTML(string(raw))
it.content = extracted
```

The AI link discovery should now use `extracted.Markdown` instead of raw HTML for better results.

- [ ] **Step 3: Update JSON API connectors**

For each JSON API connector (greenhouse, arbeitnow, himalayas, etc.), update the iterator to generate content from the structured response. The simplest approach: set content in `NewSinglePageIterator`:

```go
func NewSinglePageIterator(jobs []domain.ExternalJob, raw []byte, status int, err error) *SinglePageIterator {
    return &SinglePageIterator{
        jobs:    jobs,
        raw:     raw,
        status:  status,
        err:     err,
        content: content.ExtractFromJSON(string(raw), ""),
    }
}
```

Individual connectors can pass the description if they want per-job content.

- [ ] **Step 4: Verify build**

Run: `go build ./...`

- [ ] **Step 5: Commit**

```bash
git add pkg/connectors/ pkg/content/
git commit -m "feat: wire content extraction into connector iterators"
```

---

## Task 6: Migration for Existing Data

**Files:**
- Modify: `apps/crawler/cmd/main.go`

- [ ] **Step 1: Add stage column migration**

In the AutoMigrate section of main.go, GORM will automatically add the new columns. No manual migration needed. But set existing variants to `stage = 'ready'`:

After AutoMigrate, add:

```go
// Set existing variants without a stage to 'ready' (they've been processed)
migrationDB.Exec("UPDATE job_variants SET stage = 'ready' WHERE stage = '' OR stage IS NULL")
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`

- [ ] **Step 3: Commit**

```bash
git add apps/crawler/cmd/main.go
git commit -m "feat: migrate existing variants to stage=ready"
```

---

## Execution Notes

- Tasks 1-4 are independent and can run in parallel
- Task 5 depends on Task 1 (content package) and Task 4 (connector interface)
- Task 6 depends on Task 2 (model changes)
- Total: 6 tasks for Plan A

**After Plan A completes**, Plan B (pipeline event handlers) can begin. Plan B will create the DedupHandler, NormalizeHandler, ValidateHandler, CanonicalHandler, SourceExpansionHandler, and SourceQualityHandler — all using the content extraction and stage tracking built in Plan A.
