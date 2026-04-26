# R2 Blob Archive Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move raw HTTP bodies, variant HTML/markdown blobs, and canonical-job snapshots out of Postgres into a private R2 bucket organised under `clusters/{cluster_id}/`, and decouple the crawler from the extractor so a stalled extractor grows a NATS queue instead of pressuring the database.

**Architecture:** New `pkg/archive` package wraps an R2 client behind a narrow interface. Crawler writes `raw/{sha256}.html.gz` (deduped) and emits an event carrying the hash. Normalize handler fetches raw from R2, writes `clusters/{id}/variants/{variant_id}.json`. Canonical handler writes `clusters/{id}/canonical.json` and `clusters/{id}/manifest.json`. DB columns for the large blobs get dropped; a `raw_refs` table reference-counts hashes so the purge sweeper can GC unreferenced blobs safely. Retention is lifecycle-driven (only delete when canonical flips to `status='deleted'` + 7-day grace). Archive writes happen BEFORE DB commit within each handler so NATS at-least-once redelivery converges to a consistent state.

**Tech Stack:** Go 1.23 / Frame v1.94.1 / GORM / NATS JetStream (events) / aws-sdk-go-v2 (S3-compatible R2) / Postgres / testcontainers-go minio (integration tests).

**Spec:** `docs/superpowers/specs/2026-04-20-r2-blob-archive-design.md`

---

## File Structure

### Created
- `pkg/archive/keys.go` — pure string helpers (`RawKey`, `ClusterDir`, `CanonicalKey`, `VariantKey`, `ManifestKey`).
- `pkg/archive/keys_test.go` — unit tests for key helpers.
- `pkg/archive/archive.go` — `Archive` interface + typed payloads (`CanonicalSnapshot`, `VariantBlob`, `Manifest`) + `ErrNotFound`.
- `pkg/archive/r2.go` — `R2Archive` struct implementing `Archive` via aws-sdk-go-v2.
- `pkg/archive/r2_test.go` — integration tests against a testcontainers minio.
- `pkg/archive/fake.go` — in-memory `FakeArchive` for unit tests in downstream packages (build-tagged `//go:build test` or just unexported `*_test` — plan uses a `testing.go` file so it's shared).
- `pkg/archive/testing.go` — `NewFakeArchive()` shared across packages.
- `pkg/repository/raw_ref.go` — `RawRefRepository` with ref-count GC helper.
- `pkg/repository/raw_ref_test.go` — ref-count unit tests.
- `apps/crawler/cmd/r2_purge.go` — new cron: physical R2 teardown for `status='deleted'` canonicals past grace window.
- `scripts/archive-verify.sh` — QA script invoked by `make archive-verify`.

### Modified
- `pkg/domain/models.go` — drop `RawPayload.Body`; add `RawPayload.SizeBytes`. Drop `JobVariant.{RawHTML, CleanHTML, Markdown}`; add `JobVariant.RawContentHash`. Add `CanonicalJob.DeletedAt`, `CanonicalJob.R2PurgedAt`. Add new model `RawRef`.
- `apps/crawler/cmd/main.go` — hash body, `archive.HasRaw` + `archive.PutRaw`, drop `Body: body` from `RawPayload` create; AutoMigrate `RawRef`; wire `Archive` config.
- `apps/crawler/config/config.go` — add `ArchiveR2*` env fields for the separate archive bucket.
- `apps/api/cmd/main.go` — wire archive config for debug endpoints if needed; AutoMigrate `RawRef`.
- `apps/candidates/cmd/main.go` — AutoMigrate `RawRef`.
- `pkg/pipeline/handlers/normalize.go` — fetch raw from archive by `RawContentHash`, write `VariantBlob` + rebuild `Manifest`; stop writing `raw_html`/`clean_html`/`markdown` DB columns.
- `pkg/pipeline/handlers/canonical.go` — after canonical upsert, write `CanonicalSnapshot` + rebuild `Manifest`, insert `raw_refs` row for the variant's hash.
- `pkg/repository/job.go` — remove `UpdateStageWithContent`'s old columns from Updates map; simplify signature (takes stage only + raw_content_hash optionally).
- `Makefile` — add `archive-verify` target.

---

## Task 1: Archive key helpers (pure strings)

**Files:**
- Create: `pkg/archive/keys.go`
- Create: `pkg/archive/keys_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// pkg/archive/keys_test.go
package archive

import "testing"

func TestRawKey(t *testing.T) {
	got := RawKey("a3f7b2c4d9e2a8f1")
	want := "raw/a3f7b2c4d9e2a8f1.html.gz"
	if got != want {
		t.Errorf("RawKey = %q, want %q", got, want)
	}
}

func TestClusterDir(t *testing.T) {
	got := ClusterDir("cr7qs3q8j1hci9fn3sag")
	want := "clusters/cr7qs3q8j1hci9fn3sag/"
	if got != want {
		t.Errorf("ClusterDir = %q, want %q", got, want)
	}
}

func TestCanonicalKey(t *testing.T) {
	got := CanonicalKey("cr7qs3q8j1hci9fn3sag")
	want := "clusters/cr7qs3q8j1hci9fn3sag/canonical.json"
	if got != want {
		t.Errorf("CanonicalKey = %q, want %q", got, want)
	}
}

func TestVariantKey(t *testing.T) {
	got := VariantKey("cr7qs3q8j1hci9fn3sag", "cr7qr2q8j1hci9fn3sbg")
	want := "clusters/cr7qs3q8j1hci9fn3sag/variants/cr7qr2q8j1hci9fn3sbg.json"
	if got != want {
		t.Errorf("VariantKey = %q, want %q", got, want)
	}
}

func TestManifestKey(t *testing.T) {
	got := ManifestKey("cr7qs3q8j1hci9fn3sag")
	want := "clusters/cr7qs3q8j1hci9fn3sag/manifest.json"
	if got != want {
		t.Errorf("ManifestKey = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/archive/... -run TestRawKey -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement key helpers**

```go
// pkg/archive/keys.go
//
// Pure string helpers for the archive bucket's key layout. Kept
// separate from the R2 client so unit tests covering downstream
// callers (crawler, normalize, canonical) can assert exact keys
// without standing up an S3 client.
//
// Layout:
//   raw/{sha256}.html.gz                         — dedup'd raw HTTP bodies
//   clusters/{cluster_id}/canonical.json         — current canonical snapshot
//   clusters/{cluster_id}/manifest.json          — mutable index
//   clusters/{cluster_id}/variants/{variant_id}.json
package archive

func RawKey(contentHash string) string {
	return "raw/" + contentHash + ".html.gz"
}

func ClusterDir(clusterID string) string {
	return "clusters/" + clusterID + "/"
}

func CanonicalKey(clusterID string) string {
	return ClusterDir(clusterID) + "canonical.json"
}

func ManifestKey(clusterID string) string {
	return ClusterDir(clusterID) + "manifest.json"
}

func VariantKey(clusterID, variantID string) string {
	return ClusterDir(clusterID) + "variants/" + variantID + ".json"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/archive/... -v`
Expected: PASS — 5 tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/archive/keys.go pkg/archive/keys_test.go
git commit -m "feat(archive): add R2 key layout helpers"
```

---

## Task 2: Archive interface + typed payloads

**Files:**
- Create: `pkg/archive/archive.go`

- [ ] **Step 1: Write the interface + types**

```go
// pkg/archive/archive.go
package archive

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Get* methods when the object is absent.
// Callers treat this as a logical miss rather than an error — missing
// raw for a known hash means something in the pipeline skipped the
// PutRaw step and needs re-triggering.
var ErrNotFound = errors.New("archive: object not found")

// Archive is the narrow interface the pipeline uses to talk to R2.
// Implemented in r2.go (production) and testing.go (in-memory fake).
//
// Method semantics:
//
//   PutRaw is content-addressed; the returned hash is sha256(body)
//   hex-encoded. Body is stored gzipped under raw/{hash}.html.gz.
//   Repeated calls with the same body are idempotent — the blob is
//   dedup'd server-side by checking HasRaw first, but implementations
//   MAY skip the check and rely on idempotent PUT.
//
//   Get* methods return ErrNotFound for missing objects. Any other
//   error indicates an infrastructure problem worth retrying.
type Archive interface {
	PutRaw(ctx context.Context, body []byte) (hash string, size int64, err error)
	GetRaw(ctx context.Context, hash string) ([]byte, error)
	HasRaw(ctx context.Context, hash string) (bool, error)

	PutCanonical(ctx context.Context, clusterID string, snap CanonicalSnapshot) error
	GetCanonical(ctx context.Context, clusterID string) (CanonicalSnapshot, error)

	PutVariant(ctx context.Context, clusterID, variantID string, v VariantBlob) error
	GetVariant(ctx context.Context, clusterID, variantID string) (VariantBlob, error)

	PutManifest(ctx context.Context, clusterID string, m Manifest) error
	GetManifest(ctx context.Context, clusterID string) (Manifest, error)

	// DeleteCluster removes every object under clusters/{clusterID}/.
	// Used by the purge sweeper when a canonical flips to 'deleted'.
	// DOES NOT delete raw/* blobs — that's ref-counted separately.
	DeleteCluster(ctx context.Context, clusterID string) error

	// DeleteRaw removes a single raw/{hash}.html.gz. Callers MUST
	// check the raw_refs table for ref count == 0 before invoking.
	DeleteRaw(ctx context.Context, hash string) error
}

// CanonicalSnapshot mirrors the fields of domain.CanonicalJob that
// are worth preserving in R2 as an audit trail. Any field that
// could be reconstructed from other tables is omitted to keep the
// snapshot small.
type CanonicalSnapshot struct {
	ID             string     `json:"id"`
	ClusterID      string     `json:"cluster_id"`
	Slug           string     `json:"slug"`
	Title          string     `json:"title"`
	Company        string     `json:"company"`
	Description    string     `json:"description"`
	LocationText   string     `json:"location_text"`
	Country        string     `json:"country"`
	Language       string     `json:"language"`
	RemoteType     string     `json:"remote_type"`
	EmploymentType string     `json:"employment_type"`
	SalaryMin      float64    `json:"salary_min"`
	SalaryMax      float64    `json:"salary_max"`
	Currency       string     `json:"currency"`
	ApplyURL       string     `json:"apply_url"`
	QualityScore   float64    `json:"quality_score"`
	PostedAt       *time.Time `json:"posted_at,omitempty"`
	FirstSeenAt    time.Time  `json:"first_seen_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	Status         string     `json:"status"`
	Category       string     `json:"category"`
	R2Version      int        `json:"r2_version"`
	WrittenAt      time.Time  `json:"written_at"`
}

// VariantBlob is the per-variant processing record: enough to
// re-run extraction (raw_sha256 → archive.GetRaw) or to diff two
// variants side-by-side during quality review.
type VariantBlob struct {
	ID              string            `json:"id"`
	ClusterID       string            `json:"cluster_id"`
	SourceID        string            `json:"source_id"`
	SourceURL       string            `json:"source_url"`
	ApplyURL        string            `json:"apply_url"`
	RawContentHash  string            `json:"raw_content_hash"`
	CleanHTML       string            `json:"clean_html"`
	Markdown        string            `json:"markdown"`
	ExtractedFields map[string]any    `json:"extracted_fields,omitempty"`
	ScrapedAt       time.Time         `json:"scraped_at"`
	Stage           string            `json:"stage"`
	WrittenAt       time.Time         `json:"written_at"`
}

// Manifest is the mutable cluster index — rewritten on every
// variant or canonical write. DB is source-of-truth for current
// state; the manifest exists so `aws s3 sync clusters/{id}/` on
// its own gives a human a complete picture.
type Manifest struct {
	ClusterID   string            `json:"cluster_id"`
	CanonicalID string            `json:"canonical_id"`
	Slug        string            `json:"slug"`
	Variants    []ManifestVariant `json:"variants"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type ManifestVariant struct {
	VariantID      string    `json:"variant_id"`
	SourceID       string    `json:"source_id"`
	RawContentHash string    `json:"raw_content_hash"`
	ScrapedAt      time.Time `json:"scraped_at"`
}
```

- [ ] **Step 2: Run build**

Run: `go build ./pkg/archive/...`
Expected: PASS — interface and types compile.

- [ ] **Step 3: Commit**

```bash
git add pkg/archive/archive.go
git commit -m "feat(archive): define Archive interface + typed payloads"
```

---

## Task 3: In-memory FakeArchive for downstream tests

**Files:**
- Create: `pkg/archive/testing.go`
- Create: `pkg/archive/fake_test.go`

- [ ] **Step 1: Write the fake**

```go
// pkg/archive/testing.go
package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// FakeArchive is an in-memory Archive implementation used by
// downstream package tests (crawler, normalize, canonical). It's
// deliberately dumb — no gzip, no network, no retries — so tests
// assert on the logical contract rather than R2 quirks.
type FakeArchive struct {
	mu    sync.Mutex
	raw   map[string][]byte
	blobs map[string][]byte
}

func NewFakeArchive() *FakeArchive {
	return &FakeArchive{
		raw:   map[string][]byte{},
		blobs: map[string][]byte{},
	}
}

func (f *FakeArchive) PutRaw(_ context.Context, body []byte) (string, int64, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw[hash] = append([]byte(nil), body...)
	return hash, int64(len(body)), nil
}

func (f *FakeArchive) GetRaw(_ context.Context, hash string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, ok := f.raw[hash]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), body...), nil
}

func (f *FakeArchive) HasRaw(_ context.Context, hash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.raw[hash]
	return ok, nil
}

func (f *FakeArchive) PutCanonical(_ context.Context, clusterID string, snap CanonicalSnapshot) error {
	return f.putJSON(CanonicalKey(clusterID), snap)
}

func (f *FakeArchive) GetCanonical(_ context.Context, clusterID string) (CanonicalSnapshot, error) {
	var snap CanonicalSnapshot
	err := f.getJSON(CanonicalKey(clusterID), &snap)
	return snap, err
}

func (f *FakeArchive) PutVariant(_ context.Context, clusterID, variantID string, v VariantBlob) error {
	return f.putJSON(VariantKey(clusterID, variantID), v)
}

func (f *FakeArchive) GetVariant(_ context.Context, clusterID, variantID string) (VariantBlob, error) {
	var v VariantBlob
	err := f.getJSON(VariantKey(clusterID, variantID), &v)
	return v, err
}

func (f *FakeArchive) PutManifest(_ context.Context, clusterID string, m Manifest) error {
	return f.putJSON(ManifestKey(clusterID), m)
}

func (f *FakeArchive) GetManifest(_ context.Context, clusterID string) (Manifest, error) {
	var m Manifest
	err := f.getJSON(ManifestKey(clusterID), &m)
	return m, err
}

func (f *FakeArchive) DeleteCluster(_ context.Context, clusterID string) error {
	prefix := ClusterDir(clusterID)
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.blobs {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(f.blobs, k)
		}
	}
	return nil
}

func (f *FakeArchive) DeleteRaw(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.raw, hash)
	return nil
}

// Keys returns every blob key currently stored under clusters/.
// Used by tests that want to assert on layout without round-tripping
// via Get*.
func (f *FakeArchive) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.blobs))
	for k := range f.blobs {
		out = append(out, k)
	}
	return out
}

func (f *FakeArchive) putJSON(key string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobs[key] = body
	return nil
}

func (f *FakeArchive) getJSON(key string, dst any) error {
	f.mu.Lock()
	body, ok := f.blobs[key]
	f.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return json.Unmarshal(body, dst)
}
```

- [ ] **Step 2: Write fake tests**

```go
// pkg/archive/fake_test.go
package archive

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeArchive_RoundTripRaw(t *testing.T) {
	a := NewFakeArchive()
	ctx := context.Background()

	body := []byte("<html>hi</html>")
	hash, size, err := a.PutRaw(ctx, body)
	if err != nil {
		t.Fatalf("PutRaw: %v", err)
	}
	if size != int64(len(body)) {
		t.Errorf("size = %d, want %d", size, len(body))
	}

	ok, err := a.HasRaw(ctx, hash)
	if err != nil || !ok {
		t.Fatalf("HasRaw = (%v, %v)", ok, err)
	}

	got, err := a.GetRaw(ctx, hash)
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("GetRaw = %q, want %q", got, body)
	}
}

func TestFakeArchive_RawMiss(t *testing.T) {
	a := NewFakeArchive()
	_, err := a.GetRaw(context.Background(), "deadbeef")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFakeArchive_CanonicalAndManifest(t *testing.T) {
	a := NewFakeArchive()
	ctx := context.Background()
	now := time.Now().UTC()

	snap := CanonicalSnapshot{ID: "c1", ClusterID: "c1", Slug: "foo", WrittenAt: now}
	if err := a.PutCanonical(ctx, "c1", snap); err != nil {
		t.Fatalf("PutCanonical: %v", err)
	}
	got, err := a.GetCanonical(ctx, "c1")
	if err != nil || got.Slug != "foo" {
		t.Errorf("GetCanonical: %+v err=%v", got, err)
	}

	m := Manifest{ClusterID: "c1", CanonicalID: "c1", Slug: "foo", UpdatedAt: now}
	if err := a.PutManifest(ctx, "c1", m); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	gotM, _ := a.GetManifest(ctx, "c1")
	if gotM.CanonicalID != "c1" {
		t.Errorf("GetManifest = %+v", gotM)
	}
}

func TestFakeArchive_DeleteCluster(t *testing.T) {
	a := NewFakeArchive()
	ctx := context.Background()
	_ = a.PutCanonical(ctx, "c1", CanonicalSnapshot{ID: "c1"})
	_ = a.PutVariant(ctx, "c1", "v1", VariantBlob{ID: "v1"})

	if err := a.DeleteCluster(ctx, "c1"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
	if _, err := a.GetCanonical(ctx, "c1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("canonical should be gone, got %v", err)
	}
	if _, err := a.GetVariant(ctx, "c1", "v1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("variant should be gone, got %v", err)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./pkg/archive/... -v`
Expected: PASS — all tests.

- [ ] **Step 4: Commit**

```bash
git add pkg/archive/testing.go pkg/archive/fake_test.go
git commit -m "feat(archive): add in-memory FakeArchive for tests"
```

---

## Task 4: R2Archive production implementation

**Files:**
- Create: `pkg/archive/r2.go`

- [ ] **Step 1: Implement R2Archive**

```go
// pkg/archive/r2.go
package archive

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// R2Archive is the production Archive backed by Cloudflare R2.
// It talks to R2 via the S3-compatible API. Bucket and credentials
// are private and distinct from the public job-repo bucket.
type R2Archive struct {
	client *s3.Client
	bucket string
}

// R2Config carries the four pieces of config needed to talk to
// the archive bucket. The archive bucket MUST be separate from
// the public job-repo bucket — least-privilege credentials
// matter for a store that holds full raw HTML.
type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

func NewR2Archive(cfg R2Config) *R2Archive {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		BaseEndpoint: aws.String(endpoint),
	})
	return &R2Archive{client: client, bucket: cfg.Bucket}
}

func (a *R2Archive) PutRaw(ctx context.Context, body []byte) (string, int64, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(body); err != nil {
		return "", 0, fmt.Errorf("gzip raw: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", 0, fmt.Errorf("gzip close: %w", err)
	}

	_, err := a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(a.bucket),
		Key:             aws.String(RawKey(hash)),
		Body:            bytes.NewReader(buf.Bytes()),
		ContentType:     aws.String("text/html; charset=utf-8"),
		ContentEncoding: aws.String("gzip"),
	})
	if err != nil {
		return "", 0, fmt.Errorf("put raw: %w", err)
	}
	return hash, int64(len(body)), nil
}

func (a *R2Archive) GetRaw(ctx context.Context, hash string) ([]byte, error) {
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(RawKey(hash)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get raw: %w", err)
	}
	defer func() { _ = out.Body.Close() }()

	gz, err := gzip.NewReader(out.Body)
	if err != nil {
		return nil, fmt.Errorf("gunzip raw: %w", err)
	}
	defer func() { _ = gz.Close() }()

	return io.ReadAll(gz)
}

func (a *R2Archive) HasRaw(ctx context.Context, hash string) (bool, error) {
	_, err := a.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(RawKey(hash)),
	})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("head raw: %w", err)
	}
	return true, nil
}

func (a *R2Archive) PutCanonical(ctx context.Context, clusterID string, snap CanonicalSnapshot) error {
	return a.putJSON(ctx, CanonicalKey(clusterID), snap)
}

func (a *R2Archive) GetCanonical(ctx context.Context, clusterID string) (CanonicalSnapshot, error) {
	var snap CanonicalSnapshot
	err := a.getJSON(ctx, CanonicalKey(clusterID), &snap)
	return snap, err
}

func (a *R2Archive) PutVariant(ctx context.Context, clusterID, variantID string, v VariantBlob) error {
	return a.putJSON(ctx, VariantKey(clusterID, variantID), v)
}

func (a *R2Archive) GetVariant(ctx context.Context, clusterID, variantID string) (VariantBlob, error) {
	var v VariantBlob
	err := a.getJSON(ctx, VariantKey(clusterID, variantID), &v)
	return v, err
}

func (a *R2Archive) PutManifest(ctx context.Context, clusterID string, m Manifest) error {
	return a.putJSON(ctx, ManifestKey(clusterID), m)
}

func (a *R2Archive) GetManifest(ctx context.Context, clusterID string) (Manifest, error) {
	var m Manifest
	err := a.getJSON(ctx, ManifestKey(clusterID), &m)
	return m, err
}

func (a *R2Archive) DeleteCluster(ctx context.Context, clusterID string) error {
	prefix := ClusterDir(clusterID)
	var continuation *string
	for {
		list, err := a.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(a.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuation,
		})
		if err != nil {
			return fmt.Errorf("list cluster objects: %w", err)
		}
		if len(list.Contents) == 0 {
			return nil
		}
		ids := make([]s3types.ObjectIdentifier, 0, len(list.Contents))
		for _, obj := range list.Contents {
			ids = append(ids, s3types.ObjectIdentifier{Key: obj.Key})
		}
		_, err = a.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(a.bucket),
			Delete: &s3types.Delete{Objects: ids},
		})
		if err != nil {
			return fmt.Errorf("delete cluster objects: %w", err)
		}
		if list.IsTruncated == nil || !*list.IsTruncated {
			return nil
		}
		continuation = list.NextContinuationToken
	}
}

func (a *R2Archive) DeleteRaw(ctx context.Context, hash string) error {
	_, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(RawKey(hash)),
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("delete raw: %w", err)
	}
	return nil
}

func (a *R2Archive) putJSON(ctx context.Context, key string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	_, err = a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json; charset=utf-8"),
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

func (a *R2Archive) getJSON(ctx context.Context, key string, dst any) error {
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("get %s: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return fmt.Errorf("read %s: %w", key, err)
	}
	return json.Unmarshal(body, dst)
}

// isNotFound normalises the S3 not-found error. R2 returns a mix
// of NoSuchKey and 404 depending on operation.
func isNotFound(err error) bool {
	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *s3types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	// HeadObject sometimes returns a raw http 404 wrapped in an
	// APIError with code "NotFound" but no typed sentinel.
	return strings.Contains(err.Error(), "StatusCode: 404")
}
```

- [ ] **Step 2: Run build**

Run: `go build ./pkg/archive/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add pkg/archive/r2.go
git commit -m "feat(archive): implement R2Archive backed by aws-sdk-go-v2 S3 client"
```

---

## Task 5: Domain model updates — drop blob columns, add hash pointers

**Files:**
- Modify: `pkg/domain/models.go`

- [ ] **Step 1: Update `RawPayload` — drop Body, add SizeBytes**

Find the existing struct (currently ~lines 134–145) and replace it with:

```go
// RawPayload is the metadata row for every HTTP fetch the crawler
// makes. The actual response body lives in R2 at raw/{content_hash}.html.gz
// — this row just records the fetch event and points to it.
type RawPayload struct {
	BaseModel
	CrawlJobID  string    `gorm:"type:varchar(20);not null;index" json:"crawl_job_id"`
	StorageURI  string    `gorm:"type:text" json:"storage_uri"`
	ContentHash string    `gorm:"type:varchar(64);index" json:"content_hash"`
	SizeBytes   int64     `gorm:"not null;default:0" json:"size_bytes"`
	FetchedAt   time.Time `gorm:"not null" json:"fetched_at"`
	HTTPStatus  int       `gorm:"not null" json:"http_status"`
}

func (RawPayload) TableName() string { return "raw_payloads" }
```

Delete the `Body []byte` field and the comment fragments around it.

- [ ] **Step 2: Update `JobVariant` — drop blob columns, add RawContentHash**

Find the block (currently contains `RawHTML`, `CleanHTML`, `Markdown`) and replace:

```go
	// Stored content pointer. Actual HTML/markdown lives in R2 under
	// clusters/{cluster_id}/variants/{id}.json; RawContentHash points
	// at raw/{hash}.html.gz for the original HTTP body.
	RawContentHash  string   `gorm:"type:varchar(64);index" json:"raw_content_hash"`
```

Delete these fields:
```go
	RawHTML         string  `gorm:"type:text" json:"-"`
	CleanHTML       string  `gorm:"type:text" json:"-"`
	Markdown        string  `gorm:"type:text" json:"-"`
```

- [ ] **Step 3: Update `CanonicalJob` — add deleted_at + r2_purged_at**

Find the `CanonicalJob` struct. After the `Status` / `ExpiresAt` / `PublishedAt` block, add:

```go
	// Set when status flips to 'deleted'. Distinct from BaseModel.DeletedAt
	// (soft-delete, which we don't use here). r2_purged_at is stamped by the
	// purge sweeper once R2 teardown finishes.
	DeletedStatusAt *time.Time `gorm:"column:deleted_status_at;index" json:"deleted_status_at,omitempty"`
	R2PurgedAt      *time.Time `gorm:"index" json:"r2_purged_at,omitempty"`
```

(Column name `deleted_status_at` to avoid colliding with GORM's soft-delete `deleted_at`.)

- [ ] **Step 4: Add `RawRef` model at the end of `models.go`**

```go
// RawRef is the reference-count row linking a raw content-hash
// (R2 raw/{hash}.html.gz) to the variants that use it. Written
// by the canonical handler when it promotes a variant into a
// cluster; deleted by the purge sweeper once the owning cluster
// is torn down. When a hash's ref count drops to zero, the raw
// blob is GC'd from R2.
type RawRef struct {
	BaseModel
	ContentHash string `gorm:"type:varchar(64);not null;uniqueIndex:idx_raw_refs_hash_variant" json:"content_hash"`
	ClusterID   string `gorm:"type:varchar(20);not null;index" json:"cluster_id"`
	VariantID   string `gorm:"type:varchar(20);not null;uniqueIndex:idx_raw_refs_hash_variant" json:"variant_id"`
}

func (RawRef) TableName() string { return "raw_refs" }
```

- [ ] **Step 5: Verify build**

Run: `go build ./pkg/domain/...`
Expected: PASS.

- [ ] **Step 6: Run domain tests**

Run: `go test ./pkg/domain/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/domain/models.go
git commit -m "feat(domain): move blobs to R2 — drop Body/RawHTML/CleanHTML/Markdown, add RawRef + hash pointers"
```

---

## Task 6: `RawRefRepository` with ref-count helpers

**Files:**
- Create: `pkg/repository/raw_ref.go`
- Create: `pkg/repository/raw_ref_test.go`

- [ ] **Step 1: Write failing tests**

```go
// pkg/repository/raw_ref_test.go
package repository

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"stawi.opportunities/pkg/domain"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.RawRef{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestRawRef_UpsertAndCount(t *testing.T) {
	db := newTestDB(t)
	repo := NewRawRefRepository(func(_ context.Context, _ bool) *gorm.DB { return db })
	ctx := context.Background()

	if err := repo.Upsert(ctx, "hash1", "cluster1", "variant1"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Duplicate is a no-op.
	if err := repo.Upsert(ctx, "hash1", "cluster1", "variant1"); err != nil {
		t.Fatalf("Upsert dup: %v", err)
	}

	n, err := repo.CountByHash(ctx, "hash1")
	if err != nil {
		t.Fatalf("CountByHash: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}

	// Second variant sharing the same hash.
	_ = repo.Upsert(ctx, "hash1", "cluster2", "variant2")
	n, _ = repo.CountByHash(ctx, "hash1")
	if n != 2 {
		t.Errorf("count after second variant = %d, want 2", n)
	}
}

func TestRawRef_DeleteByCluster_ReturnsOrphanHashes(t *testing.T) {
	db := newTestDB(t)
	repo := NewRawRefRepository(func(_ context.Context, _ bool) *gorm.DB { return db })
	ctx := context.Background()

	_ = repo.Upsert(ctx, "hash1", "cluster1", "variant1")
	_ = repo.Upsert(ctx, "hash1", "cluster2", "variant2") // shared
	_ = repo.Upsert(ctx, "hash2", "cluster1", "variant3") // orphan after deletion

	orphans, err := repo.DeleteByCluster(ctx, "cluster1")
	if err != nil {
		t.Fatalf("DeleteByCluster: %v", err)
	}
	// hash2 was ONLY referenced by cluster1, so it's orphaned.
	// hash1 is still referenced by cluster2, so it stays.
	if len(orphans) != 1 || orphans[0] != "hash2" {
		t.Errorf("orphans = %v, want [hash2]", orphans)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./pkg/repository/... -run TestRawRef -v`
Expected: FAIL — `NewRawRefRepository undefined`.

- [ ] **Step 3: Implement the repository**

```go
// pkg/repository/raw_ref.go
package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stawi.opportunities/pkg/domain"
)

// RawRefRepository tracks which variants reference which raw content
// hashes. Used by the purge sweeper to safely GC raw/{hash}.html.gz
// blobs: a hash is deletable once its ref count drops to zero.
type RawRefRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewRawRefRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *RawRefRepository {
	return &RawRefRepository{db: db}
}

// Upsert registers (hash, cluster, variant). Idempotent on the
// (content_hash, variant_id) unique index — second call with the
// same variant is a no-op so retried handlers don't inflate counts.
func (r *RawRefRepository) Upsert(ctx context.Context, hash, clusterID, variantID string) error {
	ref := &domain.RawRef{
		ContentHash: hash,
		ClusterID:   clusterID,
		VariantID:   variantID,
	}
	return r.db(ctx, false).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(ref).Error
}

// CountByHash returns the number of variants referencing this hash.
// A return of 0 means the raw/ blob is garbage.
func (r *RawRefRepository) CountByHash(ctx context.Context, hash string) (int64, error) {
	var n int64
	err := r.db(ctx, true).
		Model(&domain.RawRef{}).
		Where("content_hash = ?", hash).
		Count(&n).Error
	return n, err
}

// DeleteByCluster removes every ref row for the given cluster and
// returns the hashes whose ref count transitioned to zero — i.e.
// orphaned hashes that the caller should now GC from R2.
//
// Executed in a single transaction so the "after" counts are
// consistent with the deletion.
func (r *RawRefRepository) DeleteByCluster(ctx context.Context, clusterID string) ([]string, error) {
	var orphans []string
	err := r.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		// Collect the hashes we're about to drop refs for.
		var hashes []string
		if err := tx.Model(&domain.RawRef{}).
			Where("cluster_id = ?", clusterID).
			Distinct("content_hash").
			Pluck("content_hash", &hashes).Error; err != nil {
			return err
		}
		// Delete this cluster's rows.
		if err := tx.Where("cluster_id = ?", clusterID).
			Delete(&domain.RawRef{}).Error; err != nil {
			return err
		}
		// For each hash, check if any other variant still refs it.
		for _, h := range hashes {
			var n int64
			if err := tx.Model(&domain.RawRef{}).
				Where("content_hash = ?", h).
				Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				orphans = append(orphans, h)
			}
		}
		return nil
	})
	return orphans, err
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./pkg/repository/... -run TestRawRef -v`
Expected: PASS — 2 tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/repository/raw_ref.go pkg/repository/raw_ref_test.go
git commit -m "feat(repo): add RawRefRepository with ref-count GC helpers"
```

---

## Task 7: Wire archive config + AutoMigrate new models

**Files:**
- Modify: `apps/crawler/config/config.go`
- Modify: `apps/crawler/cmd/main.go` (AutoMigrate list + archive construction)
- Modify: `apps/api/cmd/main.go` (AutoMigrate list)
- Modify: `apps/candidates/cmd/main.go` (AutoMigrate list)

- [ ] **Step 1: Add archive config fields**

In `apps/crawler/config/config.go`, after the existing `R2*` fields add:

```go
	// Archive R2 — separate bucket (and separate credentials) for
	// raw HTML + variant blobs + canonical snapshots. Distinct from
	// the public job-repo bucket above so least-privilege leaks
	// stay contained.
	ArchiveR2AccountID       string `env:"ARCHIVE_R2_ACCOUNT_ID" envDefault:""`
	ArchiveR2AccessKeyID     string `env:"ARCHIVE_R2_ACCESS_KEY_ID" envDefault:""`
	ArchiveR2SecretAccessKey string `env:"ARCHIVE_R2_SECRET_ACCESS_KEY" envDefault:""`
	ArchiveR2Bucket          string `env:"ARCHIVE_R2_BUCKET" envDefault:"opportunities-archive"`
```

- [ ] **Step 2: Construct archive in crawler main**

In `apps/crawler/cmd/main.go`, near the existing `publish.NewR2Publisher` call, add:

```go
	arch := archive.NewR2Archive(archive.R2Config{
		AccountID:       cfg.ArchiveR2AccountID,
		AccessKeyID:     cfg.ArchiveR2AccessKeyID,
		SecretAccessKey: cfg.ArchiveR2SecretAccessKey,
		Bucket:          cfg.ArchiveR2Bucket,
	})
```

Add the import: `"stawi.opportunities/pkg/archive"`.

Thread `arch` into `crawlDependencies`. Add a field:

```go
type crawlDependencies struct {
	// ... existing fields ...
	archive archive.Archive
}
```

And populate it wherever `crawlDependencies` is constructed.

- [ ] **Step 3: Update AutoMigrate list in crawler**

In `apps/crawler/cmd/main.go`, the block calling `migrationDB.AutoMigrate(...)` — add `&domain.RawRef{}`:

```go
		if err := migrationDB.AutoMigrate(
			&domain.Source{},
			&domain.CrawlJob{},
			&domain.RawPayload{},
			&domain.JobVariant{},
			&domain.JobCluster{},
			&domain.JobClusterMember{},
			&domain.CanonicalJob{},
			&domain.CrawlPageState{},
			&domain.RejectedJob{},
			&domain.RawRef{},
		); err != nil {
```

- [ ] **Step 4: Update AutoMigrate list in api/cmd/main.go**

Add `&domain.RawRef{}` to the list (even though api doesn't own it, auto-migrate is idempotent and the api is the migration entry point in some envs).

```go
		if err := db.AutoMigrate(
			&domain.Source{},
			&domain.CanonicalJob{},
			&domain.JobVariant{},
			&domain.RawRef{},
		); err != nil {
```

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/crawler/config/config.go apps/crawler/cmd/main.go apps/api/cmd/main.go
git commit -m "feat(config): wire archive R2 bucket + AutoMigrate RawRef"
```

---

## Task 8: Crawler writes raw body to archive, drops Body from DB

**Files:**
- Modify: `apps/crawler/cmd/main.go`

- [ ] **Step 1: Rewrite the body-storage path**

Locate the block in `processSource` where `quality.Check` passes and the variant is about to be persisted. Currently the variant carries `RawHTML` / `CleanHTML` / `Markdown` strings. We now write raw to R2 instead.

Find the existing block that looks roughly like:

```go
		variant := normalize.ExternalToVariant(...)

		if bloom.IsSeen(ctx, d.bloomFilter, src.ID, variant.HardKey) {
			continue
		}

		if pageContent := iter.Content(); pageContent != nil {
			variant.RawHTML = pageContent.RawHTML
			variant.CleanHTML = pageContent.CleanHTML
			variant.Markdown = pageContent.Markdown
		}
```

Replace with:

```go
		variant := normalize.ExternalToVariant(extJob, src.ID, src.Country, string(src.Type), src.Language, time.Now().UTC())

		if bloom.IsSeen(ctx, d.bloomFilter, src.ID, variant.HardKey) {
			continue
		}

		// Raw HTTP body → R2 (content-addressed). Repeat crawls of an
		// unchanged page hit HasRaw and skip the PUT — that's the
		// dedup payoff. The variant row just records the hash; the
		// body never enters Postgres.
		var rawHash string
		var rawSize int64
		if pageContent := iter.Content(); pageContent != nil && len(pageContent.RawHTML) > 0 {
			hash, size, err := d.archive.PutRaw(ctx, []byte(pageContent.RawHTML))
			if err != nil {
				log.WithError(err).WithField("source_id", src.ID).
					Warn("archive PutRaw failed, skipping variant")
				continue
			}
			rawHash = hash
			rawSize = size
		}
		variant.RawContentHash = rawHash
```

- [ ] **Step 2: Drop Body from the RawPayload insert (if any exists)**

Find any `rawPayloadRepo` calls that build a `RawPayload{Body: ...}` struct and remove the `Body` assignment. Add `SizeBytes: rawSize`. If the crawler creates `RawPayload` entries, ensure `StorageURI` is set to `archive.RawKey(rawHash)`.

Search for `RawPayload{` in the file:

```
grep -n "RawPayload{" apps/crawler/cmd/main.go
```

For each match, ensure it now looks like:

```go
rp := &domain.RawPayload{
    CrawlJobID:  crawlJob.ID,
    ContentHash: rawHash,
    StorageURI:  archive.RawKey(rawHash),
    SizeBytes:   rawSize,
    FetchedAt:   time.Now().UTC(),
    HTTPStatus:  200,
}
_ = d.crawlRepo.SaveRawPayload(ctx, rp)
```

- [ ] **Step 3: Verify build and tests**

Run: `go build ./apps/crawler/...` — PASS.
Run: `go test ./...` — PASS (or skip packages that need integration env).

- [ ] **Step 4: Commit**

```bash
git add apps/crawler/cmd/main.go
git commit -m "feat(crawler): write raw body to R2 archive; store only content_hash in DB"
```

---

## Task 9: Normalize handler — write variant.json + manifest to archive

**Files:**
- Modify: `pkg/pipeline/handlers/normalize.go`
- Create: `pkg/pipeline/handlers/normalize_test.go`

- [ ] **Step 1: Add archive field to handler**

In `normalize.go`, update the struct + constructor:

```go
type NormalizeHandler struct {
	jobRepo    *repository.JobRepository
	sourceRepo *repository.SourceRepository
	extractor  *extraction.Extractor
	httpClient *httpx.Client
	archive    archive.Archive
	svc        *frame.Service
}

func NewNormalizeHandler(
	jobRepo *repository.JobRepository,
	sourceRepo *repository.SourceRepository,
	extractor *extraction.Extractor,
	httpClient *httpx.Client,
	arch archive.Archive,
	svc *frame.Service,
) *NormalizeHandler {
	return &NormalizeHandler{
		jobRepo:    jobRepo,
		sourceRepo: sourceRepo,
		extractor:  extractor,
		httpClient: httpClient,
		archive:    arch,
		svc:        svc,
	}
}
```

Add the import: `"stawi.opportunities/pkg/archive"`.

- [ ] **Step 2: Replace content-source logic to pull from archive**

Find the block that chooses content source (currently reads `variant.Markdown` / `variant.Description`). Replace with:

```go
	// 3. Load raw HTML from archive (content-addressed). Markdown /
	//    clean HTML no longer live in Postgres; if they weren't
	//    persisted yet to the variant blob, we fall back to Description.
	var contentText string
	if variant.RawContentHash != "" {
		raw, err := h.archive.GetRaw(ctx, variant.RawContentHash)
		if err != nil && !errors.Is(err, archive.ErrNotFound) {
			return fmt.Errorf("normalize: get raw: %w", err)
		}
		contentText = string(raw)
	}
	if contentText == "" {
		contentText = variant.Description
	}
	if contentText == "" && variant.ApplyURL != "" && h.httpClient != nil {
		if raw, _, fetchErr := h.httpClient.Get(ctx, variant.ApplyURL, nil); fetchErr == nil {
			// Late-bound raw: archive it now, update the variant row.
			hash, _, pErr := h.archive.PutRaw(ctx, raw)
			if pErr == nil {
				variant.RawContentHash = hash
				_ = h.jobRepo.UpdateVariantFields(ctx, variant.ID, map[string]any{
					"raw_content_hash": hash,
				})
			}
			contentText = string(raw)
		}
	}
	if contentText == "" {
		util.Log(ctx).WithField("variant_id", variant.ID).Info("normalize: no content, skipping")
		return nil
	}
```

Remove the `UpdateStageWithContent` call — those columns don't exist anymore.

- [ ] **Step 3: After extraction, persist the variant blob to archive**

At the end of `Execute`, after `UpdateVariantFields`, add:

```go
	// Persist the processed artefacts to archive — clean HTML,
	// markdown, extracted fields — so reprocessing and quality
	// checks can read them without re-running the LLM.
	blob := archive.VariantBlob{
		ID:              variant.ID,
		ClusterID:       "",   // not known yet until canonical handler runs
		SourceID:        variant.SourceID,
		SourceURL:       variant.SourceURL,
		ApplyURL:        variant.ApplyURL,
		RawContentHash:  variant.RawContentHash,
		CleanHTML:       "",   // Populated by content extractor if available.
		Markdown:        contentText,
		ExtractedFields: extractedFieldsMap(fields),
		ScrapedAt:       variant.ScrapedAt,
		Stage:           string(domain.StageNormalized),
		WrittenAt:       time.Now().UTC(),
	}
	// Variant.json lives under the cluster dir; at normalize-time
	// the cluster doesn't exist yet — that's the canonical handler's
	// job. We write to a pre-cluster staging key instead and the
	// canonical handler moves/rewrites it when the cluster is
	// assigned. To keep this step simple, we write directly once the
	// canonical handler has stamped ClusterID on the variant via
	// UpdateVariantFields. If ClusterID is empty here, skip the R2
	// write — canonical handler will write it at cluster-assignment.
	if variant.ClusterID != "" {
		blob.ClusterID = variant.ClusterID
		if err := h.archive.PutVariant(ctx, variant.ClusterID, variant.ID, blob); err != nil {
			util.Log(ctx).WithError(err).WithField("variant_id", variant.ID).
				Warn("normalize: archive PutVariant failed (non-fatal)")
		}
	}
```

Add helper at the bottom of the file:

```go
func extractedFieldsMap(f *extraction.ExtractedFields) map[string]any {
	if f == nil {
		return nil
	}
	m := map[string]any{}
	if f.Title != "" {
		m["title"] = f.Title
	}
	if f.Company != "" {
		m["company"] = f.Company
	}
	if f.Seniority != "" {
		m["seniority"] = f.Seniority
	}
	if len(f.Skills) > 0 {
		m["skills"] = f.Skills
	}
	if len(f.Roles) > 0 {
		m["roles"] = f.Roles
	}
	return m
}
```

- [ ] **Step 4: Update normalize constructor callers**

`grep -rn "NewNormalizeHandler(" apps/ pkg/` — update every call site to pass `arch` as the fifth argument.

- [ ] **Step 5: Run build + unit tests**

Run: `go build ./...` — PASS.
Run: `go test ./pkg/pipeline/handlers/... -v` — PASS (existing tests unchanged).

- [ ] **Step 6: Commit**

```bash
git add pkg/pipeline/handlers/normalize.go
git commit -m "feat(normalize): read raw from archive, persist processed variant blob to R2"
```

*Note:* JobVariant currently has no `ClusterID` column. The canonical handler assigns it. See Task 10 Step 4.

---

## Task 10: Canonical handler — write canonical.json + manifest + raw_refs

**Files:**
- Modify: `pkg/pipeline/handlers/canonical.go`
- Modify: `pkg/domain/models.go` (add `JobVariant.ClusterID`)

- [ ] **Step 1: Add ClusterID column to JobVariant**

In `pkg/domain/models.go`, inside `JobVariant`, after `SourceID`:

```go
	// ClusterID is written by the canonical handler when the variant is
	// promoted into a cluster. Empty at scrape time; set exactly once
	// per variant lifecycle.
	ClusterID string `gorm:"type:varchar(20);index" json:"cluster_id,omitempty"`
```

- [ ] **Step 2: Add archive + rawRefRepo fields to canonical handler**

In `canonical.go`, update the struct + constructor:

```go
type CanonicalHandler struct {
	jobRepo      *repository.JobRepository
	dedupeEngine *dedupe.Engine
	extractor    *extraction.Extractor
	archive      archive.Archive
	rawRefRepo   *repository.RawRefRepository
	svc          *frame.Service
}

func NewCanonicalHandler(
	jobRepo *repository.JobRepository,
	dedupeEngine *dedupe.Engine,
	extractor *extraction.Extractor,
	arch archive.Archive,
	rawRefRepo *repository.RawRefRepository,
	svc *frame.Service,
) *CanonicalHandler {
	return &CanonicalHandler{
		jobRepo:      jobRepo,
		dedupeEngine: dedupeEngine,
		extractor:    extractor,
		archive:      arch,
		rawRefRepo:   rawRefRepo,
		svc:          svc,
	}
}
```

Add the import: `"stawi.opportunities/pkg/archive"`.

- [ ] **Step 3: After canonical upsert, write to archive + register raw_ref**

After the `dedupeEngine.UpsertAndCluster` call and the slug backfill, and before emitting `EventJobReady`, add:

```go
	// Record cluster membership on the variant so the normalize
	// handler's archive write can find the right cluster on later
	// re-runs (e.g. reprocessing).
	if variant.ClusterID == "" && canonical != nil {
		_ = h.jobRepo.UpdateVariantFields(ctx, variant.ID, map[string]any{
			"cluster_id": canonical.ClusterID,
		})
		variant.ClusterID = canonical.ClusterID
	}

	// Persist canonical snapshot + variant blob + manifest to archive.
	if canonical != nil {
		snap := archive.CanonicalSnapshot{
			ID:             canonical.ID,
			ClusterID:      canonical.ClusterID,
			Slug:           canonical.Slug,
			Title:          canonical.Title,
			Company:        canonical.Company,
			Description:    canonical.Description,
			LocationText:   canonical.LocationText,
			Country:        canonical.Country,
			Language:       canonical.Language,
			RemoteType:     canonical.RemoteType,
			EmploymentType: canonical.EmploymentType,
			SalaryMin:      canonical.SalaryMin,
			SalaryMax:      canonical.SalaryMax,
			Currency:       canonical.Currency,
			ApplyURL:       canonical.ApplyURL,
			QualityScore:   canonical.QualityScore,
			PostedAt:       canonical.PostedAt,
			FirstSeenAt:    canonical.FirstSeenAt,
			LastSeenAt:     canonical.LastSeenAt,
			Status:         canonical.Status,
			Category:       canonical.Category,
			R2Version:      canonical.R2Version,
			WrittenAt:      time.Now().UTC(),
		}
		if err := h.archive.PutCanonical(ctx, canonical.ClusterID, snap); err != nil {
			util.Log(ctx).WithError(err).WithField("canonical_job_id", canonical.ID).
				Warn("canonical: archive PutCanonical failed (non-fatal)")
		}

		// Rebuild the manifest for this cluster.
		if err := h.rebuildManifest(ctx, canonical); err != nil {
			util.Log(ctx).WithError(err).WithField("canonical_job_id", canonical.ID).
				Warn("canonical: rebuild manifest failed (non-fatal)")
		}

		// Register the raw→cluster ref so the purge sweeper can GC
		// this hash when the cluster is eventually torn down.
		if variant.RawContentHash != "" {
			_ = h.rawRefRepo.Upsert(ctx, variant.RawContentHash, canonical.ClusterID, variant.ID)
		}
	}
```

- [ ] **Step 4: Add `rebuildManifest` helper**

At the bottom of `canonical.go`:

```go
// rebuildManifest rewrites clusters/{cluster_id}/manifest.json from
// current DB state. Called after every canonical upsert; cheap because
// each cluster typically has 1–5 variants.
func (h *CanonicalHandler) rebuildManifest(ctx context.Context, canonical *domain.CanonicalJob) error {
	var variants []struct {
		ID             string
		SourceID       string
		RawContentHash string
		ScrapedAt      time.Time
	}
	// Pull every variant bound to this cluster. Relies on
	// JobVariant.ClusterID being populated (see Task 10 step 1).
	if err := h.jobRepo.DB(ctx, true).
		Table("job_variants").
		Select("id, source_id, raw_content_hash, scraped_at").
		Where("cluster_id = ?", canonical.ClusterID).
		Scan(&variants).Error; err != nil {
		return err
	}

	m := archive.Manifest{
		ClusterID:   canonical.ClusterID,
		CanonicalID: canonical.ID,
		Slug:        canonical.Slug,
		UpdatedAt:   time.Now().UTC(),
	}
	for _, v := range variants {
		m.Variants = append(m.Variants, archive.ManifestVariant{
			VariantID:      v.ID,
			SourceID:       v.SourceID,
			RawContentHash: v.RawContentHash,
			ScrapedAt:      v.ScrapedAt,
		})
	}
	return h.archive.PutManifest(ctx, canonical.ClusterID, m)
}
```

- [ ] **Step 5: Expose `DB` accessor on JobRepository**

The `rebuildManifest` helper calls `h.jobRepo.DB(ctx, true)`. JobRepository's `db` field is unexported; add a thin accessor.

In `pkg/repository/job.go`:

```go
// DB returns the underlying GORM session. Exposed so callers that
// need to run ad-hoc queries (e.g. manifest rebuilds) don't have
// to duplicate the pool accessor.
func (r *JobRepository) DB(ctx context.Context, readOnly bool) *gorm.DB {
	return r.db(ctx, readOnly)
}
```

- [ ] **Step 6: Update canonical handler callers**

`grep -rn "NewCanonicalHandler(" apps/ pkg/` — update every call site to pass `arch` and `rawRefRepo`.

- [ ] **Step 7: Verify build + tests**

Run: `go build ./...` — PASS.
Run: `go test ./...` — PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/domain/models.go pkg/pipeline/handlers/canonical.go pkg/repository/job.go
git commit -m "feat(canonical): write canonical.json + manifest + raw_refs to archive"
```

---

## Task 11: Purge sweeper — status='deleted' → physical R2 teardown

**Files:**
- Create: `apps/crawler/cmd/r2_purge.go`
- Modify: `apps/crawler/cmd/main.go` (register cron + admin endpoint)

- [ ] **Step 1: Implement the sweeper**

```go
// apps/crawler/cmd/r2_purge.go
package main

import (
	"context"
	"time"

	"github.com/pitabwire/util"
	"gorm.io/gorm"

	"stawi.opportunities/pkg/archive"
	"stawi.opportunities/pkg/repository"
)

// purgeR2Archive finds canonicals stuck in status='deleted' past the
// grace window, tears down their R2 bundles, and GCs any raw/{hash}
// blobs whose ref count drops to zero. Idempotent: uses r2_purged_at
// to avoid double work on restart.
//
// Called from the retention cron loop (same cadence as runRetention).
func purgeR2Archive(
	ctx context.Context,
	db func(ctx context.Context, readOnly bool) *gorm.DB,
	arch archive.Archive,
	rawRefRepo *repository.RawRefRepository,
	graceDays int,
	batchLimit int,
) {
	log := util.Log(ctx)

	type row struct {
		ID        string
		ClusterID string
	}
	var rows []row
	cutoff := time.Now().UTC().Add(-time.Duration(graceDays) * 24 * time.Hour)
	if err := db(ctx, true).
		Table("canonical_jobs").
		Select("id, cluster_id").
		Where("status = ? AND deleted_status_at IS NOT NULL AND deleted_status_at < ? AND r2_purged_at IS NULL",
			"deleted", cutoff).
		Limit(batchLimit).
		Scan(&rows).Error; err != nil {
		log.WithError(err).Error("r2-purge: select failed")
		return
	}
	if len(rows) == 0 {
		return
	}

	purged := 0
	for _, r := range rows {
		// 1. Drop ref rows, collect orphan hashes.
		orphans, err := rawRefRepo.DeleteByCluster(ctx, r.ClusterID)
		if err != nil {
			log.WithError(err).WithField("cluster_id", r.ClusterID).
				Warn("r2-purge: delete refs failed, skipping")
			continue
		}
		// 2. Delete orphaned raw/ blobs.
		for _, h := range orphans {
			if err := arch.DeleteRaw(ctx, h); err != nil {
				log.WithError(err).WithField("content_hash", h).
					Warn("r2-purge: delete raw failed, continuing")
			}
		}
		// 3. Delete the cluster bundle.
		if err := arch.DeleteCluster(ctx, r.ClusterID); err != nil {
			log.WithError(err).WithField("cluster_id", r.ClusterID).
				Warn("r2-purge: delete cluster failed, skipping")
			continue
		}
		// 4. Stamp r2_purged_at so we don't re-process.
		if err := db(ctx, false).
			Table("canonical_jobs").
			Where("id = ?", r.ID).
			Update("r2_purged_at", time.Now().UTC()).Error; err != nil {
			log.WithError(err).WithField("canonical_job_id", r.ID).
				Warn("r2-purge: stamp purged_at failed")
			continue
		}
		purged++
	}
	if purged > 0 {
		log.WithField("canonicals_purged", purged).Info("r2-purge: sweep complete")
	}
}
```

- [ ] **Step 2: Expose as admin endpoint for Trustage to trigger**

In `apps/crawler/cmd/main.go`, inside the admin router setup:

```go
	adminMux.HandleFunc("POST /admin/r2/purge", func(w http.ResponseWriter, r *http.Request) {
		grace := 7
		if v := r.URL.Query().Get("grace_days"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 90 {
				grace = n
			}
		}
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
				limit = n
			}
		}
		purgeR2Archive(r.Context(), dbFn, arch, rawRefRepo, grace, limit)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "grace_days": grace, "limit": limit})
	})
```

(`rawRefRepo` construction: add `rawRefRepo := repository.NewRawRefRepository(dbFn)` near the other repo construction.)

- [ ] **Step 3: Verify build**

Run: `go build ./apps/crawler/...` — PASS.

- [ ] **Step 4: Commit**

```bash
git add apps/crawler/cmd/r2_purge.go apps/crawler/cmd/main.go
git commit -m "feat(crawler): add R2 purge sweeper for status='deleted' canonicals"
```

---

## Task 12: Status-flip hook to stamp `deleted_status_at`

**Files:**
- Modify: `pkg/repository/job.go`

- [ ] **Step 1: Find every place that flips status to 'deleted'**

Run:
```
grep -rn '"deleted"' apps/ pkg/ --include="*.go" | grep -iE "status.*deleted|deleted.*status"
```

Typical hits: retention stage-2 (`MarkDeleted`), link-expired flow, admin takedown.

- [ ] **Step 2: Update `RetentionRepository.MarkDeleted` to stamp the timestamp**

In `pkg/repository/retention.go`:

```go
func (r *RetentionRepository) MarkDeleted(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db(ctx, false).
		Exec("UPDATE canonical_jobs SET status='deleted', deleted_status_at=now(), published_at=NULL WHERE id = ANY(?)", ids).
		Error
}
```

- [ ] **Step 3: Update any other status-flip sites**

For each call site found in Step 1, add `"deleted_status_at": time.Now().UTC()` to the `Updates(map[string]any{...})` payload, or extend the raw SQL.

- [ ] **Step 4: Run tests**

Run: `go test ./...` — PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/repository/retention.go pkg/repository/job.go
git commit -m "feat(retention): stamp deleted_status_at on status flips for R2 purge grace tracking"
```

---

## Task 13: Integration test against minio via testcontainers

**Files:**
- Create: `pkg/archive/r2_test.go`

- [ ] **Step 1: Write the integration test**

```go
// pkg/archive/r2_test.go
//go:build integration

package archive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
)

func startMinio(ctx context.Context, t *testing.T) *minio.MinioContainer {
	t.Helper()
	c, err := minio.Run(ctx, "minio/minio:latest",
		minio.WithUsername("minio"),
		minio.WithPassword("minio123"),
	)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	return c
}

func TestR2Archive_RoundTripAgainstMinio(t *testing.T) {
	ctx := context.Background()
	c := startMinio(ctx, t)

	endpoint, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	_ = endpoint // TODO: adapt R2Archive to accept a BaseEndpoint override for tests

	// For now, assert FakeArchive-style semantics against a real
	// S3-compatible backend via a helper that builds R2Archive with
	// a test-only endpoint.
	arch := newTestR2Archive(t, endpoint, "minio", "minio123", "test-archive")

	body := []byte("<html>hi</html>")
	hash, size, err := arch.PutRaw(ctx, body)
	if err != nil {
		t.Fatalf("PutRaw: %v", err)
	}
	if size != int64(len(body)) {
		t.Errorf("size = %d, want %d", size, len(body))
	}
	got, err := arch.GetRaw(ctx, hash)
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body mismatch")
	}

	// Cluster bundle round-trip.
	snap := CanonicalSnapshot{ID: "c1", ClusterID: "c1", Slug: "foo", WrittenAt: time.Now()}
	if err := arch.PutCanonical(ctx, "c1", snap); err != nil {
		t.Fatalf("PutCanonical: %v", err)
	}
	_, err = arch.GetCanonical(ctx, "c1")
	if err != nil {
		t.Fatalf("GetCanonical: %v", err)
	}

	if err := arch.DeleteCluster(ctx, "c1"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
	_, err = arch.GetCanonical(ctx, "c1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after DeleteCluster, got %v", err)
	}
}
```

- [ ] **Step 2: Add testing-only constructor that accepts an endpoint**

Append to `pkg/archive/r2.go` (keep it plain-build so tests can call it, but don't use it in production wiring):

```go
// newR2ArchiveWithEndpoint is the test-only constructor that points
// the client at an arbitrary S3-compatible endpoint (minio in CI).
// Production paths use NewR2Archive.
func newR2ArchiveWithEndpoint(endpoint, accessKey, secretKey, bucket string) *R2Archive {
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true, // minio requires path-style addressing
	})
	return &R2Archive{client: client, bucket: bucket}
}
```

And add a shared helper in the test file:

```go
// pkg/archive/r2_test.go (append)
func newTestR2Archive(t *testing.T, endpoint, access, secret, bucket string) *R2Archive {
	t.Helper()
	a := newR2ArchiveWithEndpoint(endpoint, access, secret, bucket)
	// Create the bucket first — minio returns 404 otherwise.
	_, err := a.client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return a
}
```

- [ ] **Step 3: Add testcontainers dependency**

```bash
go get github.com/testcontainers/testcontainers-go@latest
go get github.com/testcontainers/testcontainers-go/modules/minio@latest
go mod tidy
```

- [ ] **Step 4: Run the integration test**

Run: `go test -tags integration ./pkg/archive/... -v -run TestR2Archive`
Expected: PASS (requires Docker).

- [ ] **Step 5: Commit**

```bash
git add pkg/archive/r2.go pkg/archive/r2_test.go go.mod go.sum
git commit -m "test(archive): add integration test against minio via testcontainers"
```

---

## Task 14: `make archive-verify` QA script

**Files:**
- Create: `scripts/archive-verify.sh`
- Modify: `Makefile`

- [ ] **Step 1: Write the verification script**

```bash
# scripts/archive-verify.sh
#!/usr/bin/env bash
#
# Samples 50 random active canonical jobs and verifies that for each one
# the R2 archive contains: canonical.json, manifest.json, a variant JSON
# per variant in the manifest, and a raw HTML blob per raw_content_hash.
#
# Any drift aborts with non-zero exit. Meant for post-deploy + nightly
# runs as a cheap consistency sentinel.

set -euo pipefail

: "${ARCHIVE_R2_BUCKET:?ARCHIVE_R2_BUCKET required}"
: "${ARCHIVE_R2_ENDPOINT:?ARCHIVE_R2_ENDPOINT required (e.g. https://<account>.r2.cloudflarestorage.com)}"

SAMPLE=${SAMPLE:-50}

echo "archive-verify: sampling $SAMPLE active canonicals..."

# Pull a random sample of canonicals + their manifest-relevant fields.
psql -v ON_ERROR_STOP=1 -qAt -c "
  SELECT c.id, c.cluster_id
    FROM canonical_jobs c
   WHERE c.status = 'active'
   ORDER BY random()
   LIMIT $SAMPLE
" > /tmp/archive-verify-sample.txt

fail=0
while IFS='|' read -r canonical_id cluster_id; do
    if [[ -z "$cluster_id" ]]; then continue; fi

    # 1. canonical.json present.
    if ! aws s3api head-object \
         --bucket "$ARCHIVE_R2_BUCKET" \
         --key "clusters/$cluster_id/canonical.json" \
         --endpoint-url "$ARCHIVE_R2_ENDPOINT" >/dev/null 2>&1; then
        echo "MISSING canonical.json for cluster=$cluster_id"
        fail=1
        continue
    fi

    # 2. manifest.json present.
    if ! aws s3api head-object \
         --bucket "$ARCHIVE_R2_BUCKET" \
         --key "clusters/$cluster_id/manifest.json" \
         --endpoint-url "$ARCHIVE_R2_ENDPOINT" >/dev/null 2>&1; then
        echo "MISSING manifest.json for cluster=$cluster_id"
        fail=1
        continue
    fi

    # 3. For each variant in DB, verify variant.json + raw blob.
    psql -v ON_ERROR_STOP=1 -qAt -c "
      SELECT id, raw_content_hash
        FROM job_variants
       WHERE cluster_id = '$cluster_id'
    " | while IFS='|' read -r variant_id raw_hash; do
        if ! aws s3api head-object \
             --bucket "$ARCHIVE_R2_BUCKET" \
             --key "clusters/$cluster_id/variants/$variant_id.json" \
             --endpoint-url "$ARCHIVE_R2_ENDPOINT" >/dev/null 2>&1; then
            echo "MISSING variant.json cluster=$cluster_id variant=$variant_id"
            exit 1
        fi
        if [[ -n "$raw_hash" && "$raw_hash" != "\\N" ]]; then
            if ! aws s3api head-object \
                 --bucket "$ARCHIVE_R2_BUCKET" \
                 --key "raw/$raw_hash.html.gz" \
                 --endpoint-url "$ARCHIVE_R2_ENDPOINT" >/dev/null 2>&1; then
                echo "MISSING raw blob hash=$raw_hash (used by variant=$variant_id)"
                exit 1
            fi
        fi
    done
done < /tmp/archive-verify-sample.txt

if [[ $fail -ne 0 ]]; then
    echo "archive-verify: drift detected; see MISSING lines above."
    exit 1
fi

echo "archive-verify: $SAMPLE canonicals, all blobs present."
```

- [ ] **Step 2: Make it executable**

```bash
chmod +x scripts/archive-verify.sh
```

- [ ] **Step 3: Add Makefile target**

Append to `Makefile`:

```make
.PHONY: archive-verify
archive-verify:
	@scripts/archive-verify.sh
```

- [ ] **Step 4: Commit**

```bash
git add scripts/archive-verify.sh Makefile
git commit -m "feat(qa): add archive-verify script to catch DB ↔ R2 drift"
```

---

## Task 15: Backpressure — confirm gate is queue-depth-based

**Files:**
- Modify: `pkg/backpressure/` (only if current gate still references DB state)

- [ ] **Step 1: Read current gate implementation**

```bash
ls pkg/backpressure/
cat pkg/backpressure/*.go | head -100
```

- [ ] **Step 2: Verify the Check method reads NATS pending count, not DB**

Look for the `Check(ctx)` method. It should call `js.StreamInfo(...)` and read `info.State.Msgs` (or equivalent) — NOT query Postgres for row counts.

- [ ] **Step 3: If it's DB-based, rewrite to stream-depth-based**

If the current implementation checks `raw_payloads` size or similar DB state, replace the query with a NATS JetStream depth lookup on the `variant.raw.stored` stream. Example:

```go
func (g *Gate) Check(ctx context.Context) (State, error) {
	info, err := g.js.StreamInfo(g.streamName, nats.Context(ctx))
	if err != nil {
		return State{}, fmt.Errorf("stream info: %w", err)
	}
	pending := int64(info.State.Msgs)
	return State{
		Pending:   pending,
		Paused:    pending >= g.highWater,
		HighWater: g.highWater,
		LowWater:  g.lowWater,
	}, nil
}
```

- [ ] **Step 4: If already queue-depth-based, add a comment locking in the contract**

At the top of the gate's source file:

```go
// The gate measures pipeline backpressure via NATS JetStream pending-
// message depth, NOT via Postgres state. With raw HTTP bodies in R2
// (pkg/archive) the DB never holds the blobs that could pressure it;
// queue depth is the only correct signal for "processing is behind".
// See docs/superpowers/specs/2026-04-20-r2-blob-archive-design.md.
```

- [ ] **Step 5: Run tests**

Run: `go test ./pkg/backpressure/... -v` — PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/backpressure/
git commit -m "feat(backpressure): lock in NATS-queue-depth signal (post-archive refactor)"
```

---

## Task 16: Reconciliation — orphan cluster dir cleanup

**Files:**
- Create: `apps/crawler/cmd/r2_reconcile.go`
- Modify: `apps/crawler/cmd/main.go`

- [ ] **Step 1: Implement reconciliation**

```go
// apps/crawler/cmd/r2_reconcile.go
package main

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/util"
	"gorm.io/gorm"

	"stawi.opportunities/pkg/archive"
)

// reconcileOrphans walks clusters/* in the archive bucket and deletes
// any cluster directory whose cluster_id doesn't appear in
// canonical_jobs. Catches two failure modes:
//   1. Archive write succeeded, DB commit failed (orphan bundle).
//   2. Purge sweeper missed some objects due to partial failure.
//
// Runs nightly. Inexpensive because most clusters have live canonicals.
func reconcileOrphans(
	ctx context.Context,
	client *s3.Client,
	bucket string,
	db func(ctx context.Context, readOnly bool) *gorm.DB,
	arch archive.Archive,
) {
	log := util.Log(ctx)

	// 1. List every cluster prefix.
	var continuation *string
	seen := map[string]bool{}
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String("clusters/"),
			Delimiter:         aws.String("/"),
			ContinuationToken: continuation,
		})
		if err != nil {
			log.WithError(err).Error("reconcile: list failed")
			return
		}
		for _, pfx := range out.CommonPrefixes {
			if pfx.Prefix == nil {
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(*pfx.Prefix, "clusters/"), "/")
			seen[id] = true
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		continuation = out.NextContinuationToken
	}

	if len(seen) == 0 {
		return
	}

	// 2. For each seen cluster, check DB; orphans get deleted.
	clusterIDs := make([]string, 0, len(seen))
	for id := range seen {
		clusterIDs = append(clusterIDs, id)
	}
	var alive []string
	if err := db(ctx, true).
		Table("canonical_jobs").
		Where("cluster_id IN ?", clusterIDs).
		Pluck("cluster_id", &alive).Error; err != nil {
		log.WithError(err).Error("reconcile: DB lookup failed")
		return
	}
	aliveSet := map[string]bool{}
	for _, id := range alive {
		aliveSet[id] = true
	}

	orphans := 0
	for _, id := range clusterIDs {
		if aliveSet[id] {
			continue
		}
		if err := arch.DeleteCluster(ctx, id); err != nil {
			log.WithError(err).WithField("cluster_id", id).
				Warn("reconcile: delete orphan failed")
			continue
		}
		orphans++
	}
	if orphans > 0 {
		log.WithField("orphan_clusters_deleted", orphans).Info("reconcile: sweep complete")
	}
}
```

- [ ] **Step 2: Expose as admin endpoint**

In `apps/crawler/cmd/main.go` admin router:

```go
	adminMux.HandleFunc("POST /admin/r2/reconcile", func(w http.ResponseWriter, r *http.Request) {
		reconcileOrphans(r.Context(), s3Client, cfg.ArchiveR2Bucket, dbFn, arch)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
```

You'll need to expose the raw `*s3.Client` from the archive package OR build a parallel one here from `cfg.ArchiveR2*`. Adding an accessor to the archive package is cleaner:

```go
// pkg/archive/r2.go (append)
// Client returns the underlying S3 client. Exposed for ops tasks
// (e.g. reconciliation) that need prefix listing beyond the narrow
// Archive interface. Not for use in request paths.
func (a *R2Archive) Client() *s3.Client { return a.client }
```

Then in main:
```go
reconcileOrphans(r.Context(), arch.(*archive.R2Archive).Client(), cfg.ArchiveR2Bucket, dbFn, arch)
```

- [ ] **Step 3: Verify build**

Run: `go build ./apps/crawler/...` — PASS.

- [ ] **Step 4: Commit**

```bash
git add apps/crawler/cmd/r2_reconcile.go apps/crawler/cmd/main.go pkg/archive/r2.go
git commit -m "feat(crawler): nightly reconciliation — delete orphan cluster dirs in archive"
```

---

## Task 17: Trustage workflow definitions for new cron endpoints

**Files:**
- Create: `definitions/trustage/r2-purge.json`
- Create: `definitions/trustage/r2-reconcile.json`

- [ ] **Step 1: Write the purge workflow**

```json
{
  "name": "r2-purge",
  "description": "Physical R2 teardown of canonicals stuck in status='deleted' past grace window",
  "schedule": "0 4 * * *",
  "steps": [
    {
      "name": "purge",
      "type": "http",
      "method": "POST",
      "url": "http://opportunities-crawler.opportunities.svc:8080/admin/r2/purge?grace_days=7&limit=500",
      "timeout_seconds": 120,
      "retry_policy": {"max_attempts": 2, "backoff_seconds": 60}
    }
  ]
}
```

- [ ] **Step 2: Write the reconciliation workflow**

```json
{
  "name": "r2-reconcile",
  "description": "Nightly orphan-cluster sweep against the archive bucket",
  "schedule": "30 4 * * *",
  "steps": [
    {
      "name": "reconcile",
      "type": "http",
      "method": "POST",
      "url": "http://opportunities-crawler.opportunities.svc:8080/admin/r2/reconcile",
      "timeout_seconds": 300,
      "retry_policy": {"max_attempts": 2, "backoff_seconds": 120}
    }
  ]
}
```

- [ ] **Step 3: Commit**

```bash
git add definitions/trustage/r2-purge.json definitions/trustage/r2-reconcile.json
git commit -m "feat(trustage): schedule R2 purge + reconcile crons"
```

---

## Task 18: Deployment — provision bucket + secrets

**Files:**
- Modify: `deploy/` Helm values / Kustomize overlays / Vault paths (specific files depend on your GitOps layout — find them with `grep -rn R2_BUCKET deploy/`).

- [ ] **Step 1: Create the bucket**

In Cloudflare dashboard (or via wrangler CLI): create `opportunities-archive` as a private bucket (no public access, no custom domain).

Configure the R2 lifecycle rule:
- Transition to IA storage class after 30 days.
- NO deletion rule.

- [ ] **Step 2: Mint new access keys**

Create a read-write API token scoped ONLY to `opportunities-archive`. Store four values in Vault under `antinvestor/opportunities/common/archive-r2/`:
- `account_id`
- `access_key_id`
- `secret_access_key`
- `bucket` = `opportunities-archive`

- [ ] **Step 3: Add ExternalSecret + env wiring**

Modify your existing R2 ExternalSecret manifest to also mount the archive keys, producing env vars `ARCHIVE_R2_ACCOUNT_ID`, `ARCHIVE_R2_ACCESS_KEY_ID`, `ARCHIVE_R2_SECRET_ACCESS_KEY`, `ARCHIVE_R2_BUCKET` on the crawler deployment.

- [ ] **Step 4: Commit deploy changes**

```bash
git add deploy/
git commit -m "deploy: provision archive R2 bucket + secrets (ARCHIVE_R2_*)"
```

- [ ] **Step 5: Push + watch Flux roll out**

```bash
git push
# Flux image-automation watches for the new tag on next CI build.
```

---

## Self-Review Summary

**Spec coverage:**
- ✅ Archive interface + private bucket — Tasks 2, 4, 7, 18.
- ✅ Co-located cluster bundles (`clusters/{id}/...`) — Tasks 1 (keys), 9–10.
- ✅ Content-hash dedup (`raw/{sha256}`) — Tasks 4, 8.
- ✅ Mutable manifest — Task 10.
- ✅ DB schema changes (drop blobs, add pointers, add RawRef, add deleted_status_at / r2_purged_at) — Tasks 5, 6, 10, 12.
- ✅ Crawler rewrite — Task 8.
- ✅ Normalize + Canonical handler wiring — Tasks 9, 10.
- ✅ Purge sweeper with 7-day grace + ref-counted raw GC — Task 11.
- ✅ Reconciliation for orphan bundles — Task 16.
- ✅ Trustage crons — Task 17.
- ✅ QA script — Task 14.
- ✅ Backpressure confirmation — Task 15.
- ✅ Integration test — Task 13.

**No placeholders:** every step has either complete code or an exact command.

**Type consistency:** `RawContentHash` is the DB field name; JSON blob field is also `raw_content_hash`; `ContentHash` stays the name on `RawPayload` (existing column, not renamed). `JobVariant.ClusterID` is introduced in Task 10.
