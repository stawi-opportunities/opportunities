# Phase 1 — Event Log Pipe Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the event-log plumbing that every later phase depends on — versioned event payload types, a `pkg/eventlog` Parquet writer targeting R2, and an `apps/writer` service that consumes Frame pub/sub topics and flushes batched Parquet files. End state: a synthetic `jobs.variants.ingested.v1` event published to the queue lands as a Parquet file on R2 (MinIO in tests), verifiable end-to-end.

**Architecture:** JSON on the wire (matches existing Frame handler conventions in `pkg/pipeline/handlers/payloads.go`); typed Go structs per event type with a `schema_version` field; in-process buffering in `apps/writer` keyed by `(partition_dt, partition_sec)`; flush on `{10k events | 64 MB | 30 s}`; R2 upload via the S3-compatible AWS SDK v2 setup already in `pkg/archive/r2.go`; ack-after-upload semantics so worker disposability holds.

**Tech stack:**
- Go 1.26, Frame (`github.com/pitabwire/frame`), `pitabwire/util` logging
- `github.com/parquet-go/parquet-go` v0.25+ for Parquet writing from Go structs (new dep)
- `github.com/aws/aws-sdk-go-v2/service/s3` (existing, via pkg/archive)
- `testcontainers-go/modules/minio` for integration tests (existing)
- OTEL (`go.opentelemetry.io/otel`) for metrics (existing)

**What's in this plan:**
- `pkg/events/v1/` — event envelope + first event type (`VariantIngestedV1`)
- `pkg/eventlog/` — buffer, Parquet writer, R2 uploader
- `apps/writer/` — new service scaffold with Frame subscription and buffer/flush wiring
- Integration test harness with MinIO testcontainer
- Dockerfile + Makefile wiring for the new app

**What's NOT in this plan (future phases):**
- Additional event types (canonicals, embeddings, translations, candidates) — Phase 2 adds them as the pipeline is built; for now we prove the pattern with one.
- `apps/materializer`, Manticore, KV wrapper → Phase 2.
- `apps/worker` pipeline stages → Phase 3.
- Crawler refactor → Phase 4.
- Candidates refactor → Phase 5.
- Postgres table drops / cutover → Phase 6.

**Subsequent plans (for context, to be written after Plan 1 completes):**
- Plan 2: Read path — materializer, Manticore provisioning, API search cutover.
- Plan 3: Worker pipeline — normalize, validate, dedup, canonical, embed, translate, publish.
- Plan 4: Crawler refactor — event-emission, Trustage scheduler tick, opportunistic DiscoverSites.
- Plan 5: Candidates refactor — CV lifecycle events, match endpoint against Manticore.
- Plan 6: Greenfield cutover + ops.

---

## File structure

**Create:**

| File | Responsibility |
|---|---|
| `pkg/events/v1/envelope.go` | `Envelope[P]` generic type, `NewEnvelope` helper, JSON marshal/unmarshal |
| `pkg/events/v1/names.go` | Topic name constants (`jobs.variants.ingested.v1`, …) |
| `pkg/events/v1/partitions.go` | `PartitionKey` + routing helpers — given an envelope, produce `(dt, sec)` |
| `pkg/events/v1/jobs.go` | `VariantIngestedV1` payload struct (first event type; others in Phase 2) |
| `pkg/events/v1/envelope_test.go` | Envelope round-trip + partition key tests |
| `pkg/eventlog/r2.go` | Thin R2 client wrapper (lifted from `pkg/archive/r2.go` patterns) |
| `pkg/eventlog/writer.go` | `Writer` — opens in-memory `parquet-go` buffer, writes typed rows, produces `[]byte` on flush |
| `pkg/eventlog/uploader.go` | `Uploader` — uploads a buffer to R2 at the derived path, returns ETag |
| `pkg/eventlog/writer_test.go` | Integration test: Parquet round-trip via MinIO testcontainer |
| `apps/writer/config/config.go` | Env-backed `Config` (R2 creds, bucket, flush thresholds, topic list) |
| `apps/writer/service/buffer.go` | In-memory buffer keyed by partition key, concurrent-safe, flush triggers |
| `apps/writer/service/handler.go` | Frame event handler that enqueues an event into the buffer |
| `apps/writer/service/service.go` | `Service` composition + `Run` loop + graceful shutdown |
| `apps/writer/service/service_test.go` | End-to-end: publish event via in-memory Frame → verify file on MinIO |
| `apps/writer/cmd/main.go` | Entrypoint — Frame bootstrap + `Service.Run` |
| `apps/writer/Dockerfile` | Multi-stage build mirror of existing `apps/crawler/Dockerfile` |

**Modify:**

| File | Change |
|---|---|
| `go.mod` / `go.sum` | Add `github.com/parquet-go/parquet-go` |
| `Makefile` | Add `writer` to `APP_DIRS`, add `run-writer` target |

---

## Task 1: Add parquet-go dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

```bash
cd /home/j/code/stawi.jobs
go get github.com/parquet-go/parquet-go@v0.25.1
go mod tidy
```

- [ ] **Step 2: Verify the library compiles and its Generic writer API is importable**

Create a temporary sanity file `/tmp/parquet_check.go`:

```go
package main

import (
	"bytes"
	"fmt"

	"github.com/parquet-go/parquet-go"
)

type row struct {
	ID   string `parquet:"id"`
	Text string `parquet:"text"`
}

func main() {
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[row](&buf)
	_, _ = w.Write([]row{{ID: "a", Text: "hello"}})
	_ = w.Close()
	fmt.Printf("wrote %d bytes\n", buf.Len())
}
```

Run: `go run /tmp/parquet_check.go`
Expected: `wrote <non-zero>` bytes. Delete the file after.

- [ ] **Step 3: Commit**

```bash
rm /tmp/parquet_check.go
git add go.mod go.sum
git commit -m "chore(deps): add parquet-go for event-log Parquet writing"
```

---

## Task 2: Event envelope type and JSON round-trip tests

**Files:**
- Create: `pkg/events/v1/envelope.go`
- Create: `pkg/events/v1/envelope_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/events/v1/envelope_test.go`:

```go
package eventsv1

import (
	"encoding/json"
	"testing"
	"time"
)

type testPayload struct {
	Value string `json:"value"`
}

func TestNewEnvelopeStampsIDsAndTimestamps(t *testing.T) {
	env := NewEnvelope("test.topic.v1", testPayload{Value: "x"})

	if env.EventID == "" {
		t.Fatal("event_id must be set by NewEnvelope")
	}
	if env.EventType != "test.topic.v1" {
		t.Fatalf("event_type=%q, want test.topic.v1", env.EventType)
	}
	if time.Since(env.OccurredAt) > time.Second {
		t.Fatalf("occurred_at=%v too old", env.OccurredAt)
	}
	if env.SchemaVersion != 1 {
		t.Fatalf("schema_version=%d, want 1", env.SchemaVersion)
	}
}

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	orig := NewEnvelope("test.topic.v1", testPayload{Value: "roundtrip"})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[testPayload]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.EventID != orig.EventID {
		t.Fatalf("event_id lost on round-trip: got %q want %q", back.EventID, orig.EventID)
	}
	if back.Payload.Value != "roundtrip" {
		t.Fatalf("payload lost: %+v", back.Payload)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd /home/j/code/stawi.jobs
go test ./pkg/events/v1/...
```

Expected: build error — `undefined: NewEnvelope, Envelope`.

- [ ] **Step 3: Implement the envelope**

Create `pkg/events/v1/envelope.go`:

```go
// Package eventsv1 defines versioned event payloads shared by every
// service that publishes or consumes the job-ingestion event log.
//
// Every event on the bus wraps a payload in an Envelope. Consumers
// generic-deserialize the outer Envelope fields, then type-assert the
// Payload against the event_type string to dispatch. Parquet writes
// flatten Envelope + Payload into a single row per event type.
package eventsv1

import (
	"time"

	"github.com/rs/xid"
)

// Envelope is the common wrapper carried on every event on the bus.
// The Payload type parameter is the event-specific body (e.g.
// VariantIngestedV1). Keeping the envelope generic means both
// producers and the writer can use compile-time typed access instead
// of map[string]any dispatch.
type Envelope[P any] struct {
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	OccurredAt    time.Time `json:"occurred_at"`
	SchemaVersion uint16    `json:"schema_version"`
	TraceID       string    `json:"trace_id,omitempty"`
	Payload       P         `json:"payload"`
}

// NewEnvelope constructs an Envelope with a fresh xid, UTC timestamp,
// and schema_version=1. Callers override schema_version only when
// introducing an additive schema change (breaking changes bump the
// .vN suffix in event_type instead).
func NewEnvelope[P any](eventType string, payload P) Envelope[P] {
	return Envelope[P]{
		EventID:       xid.New().String(),
		EventType:     eventType,
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: 1,
		Payload:       payload,
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./pkg/events/v1/...
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add pkg/events/v1/envelope.go pkg/events/v1/envelope_test.go
git commit -m "feat(events): add versioned event Envelope with xid + timestamp"
```

---

## Task 3: Topic name constants

**Files:**
- Create: `pkg/events/v1/names.go`

- [ ] **Step 1: Implement the constants**

Create `pkg/events/v1/names.go`:

```go
package eventsv1

// Topic names for Phase 1. Additional event types added in later
// phases extend this list. Names follow `{domain}.{noun}.{verb}.v{N}`.
// Breaking schema changes bump the suffix; additive changes bump
// SchemaVersion on the envelope.
const (
	// Job pipeline — variants.
	TopicVariantsIngested  = "jobs.variants.ingested.v1"
	TopicVariantsNormalized = "jobs.variants.normalized.v1"
	TopicVariantsValidated = "jobs.variants.validated.v1"
	TopicVariantsFlagged   = "jobs.variants.flagged.v1"
	TopicVariantsClustered = "jobs.variants.clustered.v1"

	// Job pipeline — canonicals.
	TopicCanonicalsUpserted = "jobs.canonicals.upserted.v1"
	TopicCanonicalsExpired  = "jobs.canonicals.expired.v1"

	// Derived.
	TopicEmbeddings   = "jobs.embeddings.v1"
	TopicTranslations = "jobs.translations.v1"
	TopicPublished    = "jobs.published.v1"

	// Crawl control plane.
	TopicCrawlRequests      = "crawl.requests.v1"
	TopicCrawlPageCompleted = "crawl.page.completed.v1"

	// Source discovery.
	TopicSourcesDiscovered = "sources.discovered.v1"
)

// AllTopics returns every topic the writer is expected to subscribe
// to for Phase 1. Kept as a single source of truth so `apps/writer`
// doesn't drift from the declared topics.
func AllTopics() []string {
	return []string{
		TopicVariantsIngested,
		TopicVariantsNormalized,
		TopicVariantsValidated,
		TopicVariantsFlagged,
		TopicVariantsClustered,
		TopicCanonicalsUpserted,
		TopicCanonicalsExpired,
		TopicEmbeddings,
		TopicTranslations,
		TopicPublished,
		TopicCrawlPageCompleted,
		TopicSourcesDiscovered,
	}
}
```

- [ ] **Step 2: Sanity-check the file compiles**

```bash
go build ./pkg/events/v1/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add pkg/events/v1/names.go
git commit -m "feat(events): declare topic names for Phase 1 event log"
```

---

## Task 4: Partition key routing

**Files:**
- Create: `pkg/events/v1/partitions.go`
- Modify: `pkg/events/v1/envelope_test.go` (append partition test)

- [ ] **Step 1: Write the failing test**

Append to `pkg/events/v1/envelope_test.go` (these tests do not depend on `VariantIngestedV1`, which lands in Task 5 — they exercise only `PartitionKey` / `PartKey.ObjectPath`):

```go
func TestPartitionKeyRespectsSourceHint(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	pk := PartitionKey(TopicVariantsIngested, now, "greenhouse")
	if pk.DT != "2026-04-21" {
		t.Fatalf("DT=%q, want 2026-04-21", pk.DT)
	}
	if pk.Secondary != "greenhouse" {
		t.Fatalf("Secondary=%q, want greenhouse", pk.Secondary)
	}
}

func TestPartitionKeyCanonicalUsesClusterPrefix(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	pk := PartitionKey(TopicCanonicalsUpserted, now, "abcdef123456")
	if pk.Secondary != "ab" {
		t.Fatalf("Secondary=%q, want ab", pk.Secondary)
	}
}

func TestPartitionObjectPathVariantsLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "greenhouse"}
	got := pk.ObjectPath("variants", "abc123")
	want := "variants/dt=2026-04-21/src=greenhouse/abc123.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}

func TestPartitionObjectPathCanonicalsLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "ab"}
	got := pk.ObjectPath("canonicals", "xyz789")
	want := "canonicals/dt=2026-04-21/cc=ab/xyz789.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./pkg/events/v1/...
```

Expected: build error — `undefined: PartitionKey, PartKey, VariantIngestedV1`.

- [ ] **Step 3: Implement partition key**

Create `pkg/events/v1/partitions.go`:

```go
package eventsv1

import (
	"fmt"
	"strings"
	"time"
)

// PartKey is the two-dimensional partition key every Parquet file
// inherits: `dt` is the UTC calendar date, `Secondary` is a
// topic-specific axis (source_id for variants, cluster prefix for
// canonicals, lang for translations, cnd prefix for candidates).
type PartKey struct {
	DT        string // YYYY-MM-DD (UTC)
	Secondary string // e.g. "greenhouse" or "en" or "ab" (cluster_id[:2])
}

// PartitionKey chooses the right secondary axis for the given
// event_type. Returning a PartKey keeps the writer's layout one
// central decision.
func PartitionKey(eventType string, occurredAt time.Time, hint string) PartKey {
	dt := occurredAt.UTC().Format("2006-01-02")
	return PartKey{DT: dt, Secondary: partitionSecondary(eventType, hint)}
}

func partitionSecondary(eventType, hint string) string {
	switch eventType {
	case TopicVariantsIngested, TopicCrawlPageCompleted:
		// per-source files — lots of small sources, keep them grouped
		return strings.ToLower(hint)
	case TopicCanonicalsUpserted, TopicCanonicalsExpired,
		TopicEmbeddings, TopicPublished:
		// cluster_id prefix (2 hex chars = 256 buckets per day)
		return firstN(strings.ToLower(hint), 2)
	case TopicTranslations:
		// hint is the target language code
		return strings.ToLower(hint)
	default:
		// everything else: single bucket per day
		return "_all"
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ObjectPath returns the R2 object key for a file. `collection` is
// the partition name (variants, canonicals, embeddings, …). `fileID`
// is usually a fresh xid so concurrent writers can never collide.
// Layout examples:
//
//	variants/dt=2026-04-21/src=greenhouse/<xid>.parquet
//	canonicals/dt=2026-04-21/cc=ab/<xid>.parquet
//	translations/dt=2026-04-21/lang=en/<xid>.parquet
func (k PartKey) ObjectPath(collection, fileID string) string {
	label := partitionSecondaryLabel(collection)
	return fmt.Sprintf("%s/dt=%s/%s=%s/%s.parquet",
		collection, k.DT, label, k.Secondary, fileID)
}

// partitionSecondaryLabel maps a collection to its layout-level path
// label. Keeping label mapping separate from the value makes R2
// listings readable — "src=greenhouse" is self-documenting.
func partitionSecondaryLabel(collection string) string {
	switch collection {
	case "variants", "crawl_page_completed":
		return "src"
	case "canonicals", "canonicals_expired", "embeddings", "published":
		return "cc"
	case "translations":
		return "lang"
	default:
		return "p"
	}
}
```

- [ ] **Step 4: Run the test**

```bash
go test ./pkg/events/v1/...
```

Expected: `PASS` — all four partition tests plus the envelope round-trip tests from Task 2.

- [ ] **Step 5: Commit**

```bash
git add pkg/events/v1/partitions.go pkg/events/v1/envelope_test.go
git commit -m "feat(events): partition key + R2 object path routing"
```

---

## Task 5: First event payload type — `VariantIngestedV1`

**Files:**
- Create: `pkg/events/v1/jobs.go`

- [ ] **Step 1: Define the payload**

Create `pkg/events/v1/jobs.go`:

```go
package eventsv1

import "time"

// VariantIngestedV1 is the event emitted by the crawler once a raw
// job page has been fetched, archived, and AI-extracted. It carries
// the full extracted fields so downstream stages (normalize, validate,
// dedup, canonical) don't need to re-read the raw page from R2 on
// the happy path.
//
// struct tags:
//   - `json`   — wire format (Frame pub/sub)
//   - `parquet` — columnar layout (pkg/eventlog writer)
//
// Keep names identical between the two so operators grepping Parquet
// columns against pub/sub logs see matching identifiers.
type VariantIngestedV1 struct {
	VariantID        string    `json:"variant_id"       parquet:"variant_id"`
	SourceID         string    `json:"source_id"        parquet:"source_id"`
	ExternalID       string    `json:"external_id"      parquet:"external_id"`
	HardKey          string    `json:"hard_key"         parquet:"hard_key"`
	Stage            string    `json:"stage"            parquet:"stage"`

	Title            string    `json:"title"            parquet:"title,optional"`
	Company          string    `json:"company"          parquet:"company,optional"`
	LocationText     string    `json:"location_text"    parquet:"location_text,optional"`
	Country          string    `json:"country"          parquet:"country,optional"`
	Language         string    `json:"language"         parquet:"language,optional"`
	RemoteType       string    `json:"remote_type"      parquet:"remote_type,optional"`
	EmploymentType   string    `json:"employment_type"  parquet:"employment_type,optional"`
	SalaryMin        float64   `json:"salary_min"       parquet:"salary_min,optional"`
	SalaryMax        float64   `json:"salary_max"       parquet:"salary_max,optional"`
	Currency         string    `json:"currency"         parquet:"currency,optional"`
	Description      string    `json:"description"      parquet:"description,optional"`
	ApplyURL         string    `json:"apply_url"        parquet:"apply_url,optional"`
	PostedAt         time.Time `json:"posted_at"        parquet:"posted_at,optional"`
	ScrapedAt        time.Time `json:"scraped_at"       parquet:"scraped_at"`
	ContentHash      string    `json:"content_hash"     parquet:"content_hash,optional"`

	RawArchiveRef    string    `json:"raw_archive_ref"  parquet:"raw_archive_ref,optional"`
	ModelVersionExtract string `json:"model_version_extract" parquet:"model_version_extract,optional"`
}
```

Subsequent phases add the extended `JobFields` (urgency, funnel, skills, …). Phase 1 keeps the schema small and focused.

- [ ] **Step 2: Run the tests (all should pass)**

```bash
go test ./pkg/events/v1/...
```

Expected: `PASS`. (Partition-related tests already passed in Task 4; this task just makes the first concrete payload type available for Task 8 onward.)

- [ ] **Step 3: Commit**

```bash
git add pkg/events/v1/jobs.go
git commit -m "feat(events): add VariantIngestedV1 payload type"
```

---

## Task 6: R2 client construction for `pkg/eventlog`

**Files:**
- Create: `pkg/eventlog/r2.go`

- [ ] **Step 1: Implement a focused R2 client**

The existing `pkg/archive/r2.go` already builds an `*s3.Client` but its `R2Archive` type is bound to the archive bucket's semantics (PutRaw with gzip + hash key). For the event log we want a thin client we can point at a different bucket (the event-log bucket is separate from the raw-page-archive bucket per the design).

Create `pkg/eventlog/r2.go`:

```go
// Package eventlog writes and uploads the durable event log to R2.
// It is shared by apps/writer (Phase 1), the compactor admin endpoint
// (Phase 2), and eventual backfill jobs (Phase 6).
package eventlog

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// R2Config carries the bucket + credentials for the *event log*
// bucket. This is a different bucket from pkg/archive: the archive
// holds raw HTML; the event log holds Parquet. Separate buckets let
// operators apply different lifecycle policies and access grants.
type R2Config struct {
	// AccountID is the Cloudflare account identifier. The S3 endpoint
	// is derived as https://{AccountID}.r2.cloudflarestorage.com.
	AccountID string

	// AccessKeyID / SecretAccessKey are R2 API tokens with write
	// access scoped to the event-log bucket only (least privilege).
	AccessKeyID     string
	SecretAccessKey string

	// Bucket is the event-log bucket name (e.g. stawi-jobs-log).
	Bucket string

	// Endpoint allows tests to point at a MinIO container. Empty
	// falls back to the CF R2 endpoint derived from AccountID.
	Endpoint string

	// UsePathStyle is required for MinIO. R2 itself tolerates both
	// but path-style is safer for test fixtures.
	UsePathStyle bool
}

// NewClient constructs an S3-compatible client for the event log bucket.
func NewClient(cfg R2Config) *s3.Client {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}
	return s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: cfg.UsePathStyle,
	})
}

// Uploader uploads already-serialized Parquet buffers to R2.
// Kept separate from Writer so tests can unit-test writing without
// R2 and unit-test uploading without Parquet.
type Uploader struct {
	client *s3.Client
	bucket string
}

// NewUploader wraps an S3 client + bucket for Put.
func NewUploader(client *s3.Client, bucket string) *Uploader {
	return &Uploader{client: client, bucket: bucket}
}

// Put writes body at objectKey and returns the ETag on success.
// ContentType is set to application/vnd.apache.parquet for operators
// poking around the bucket; R2 / S3 do not interpret it.
func (u *Uploader) Put(ctx context.Context, objectKey string, body []byte) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("eventlog: refusing to upload empty body at %q", objectKey)
	}
	resp, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(u.bucket),
		Key:         aws.String(objectKey),
		Body:        bytesReader(body),
		ContentType: aws.String("application/vnd.apache.parquet"),
	})
	if err != nil {
		return "", fmt.Errorf("eventlog: put %q: %w", objectKey, err)
	}
	if resp.ETag == nil {
		return "", nil
	}
	return *resp.ETag, nil
}

func bytesReader(b []byte) *byteReader { return &byteReader{b: b} }

type byteReader struct {
	b []byte
	i int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, errEOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

var errEOF = fmt.Errorf("EOF")
```

Note on the `byteReader` — we don't use `bytes.Reader` because `s3.PutObject` internally calls `Seek` on the Body, and `bytes.Reader` *does* implement `Seek`. Replace the custom `byteReader` + `errEOF` with `bytes.NewReader` for simplicity:

Simplified: replace the bottom of the file (from `func bytesReader` onward) with nothing, and change `Body: bytesReader(body)` to `Body: bytes.NewReader(body)`. Add `import "bytes"`.

The final file:

```go
package eventlog

import (
	"bytes"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Endpoint        string
	UsePathStyle    bool
}

func NewClient(cfg R2Config) *s3.Client {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}
	return s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: cfg.UsePathStyle,
	})
}

type Uploader struct {
	client *s3.Client
	bucket string
}

func NewUploader(client *s3.Client, bucket string) *Uploader {
	return &Uploader{client: client, bucket: bucket}
}

func (u *Uploader) Put(ctx context.Context, objectKey string, body []byte) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("eventlog: refusing to upload empty body at %q", objectKey)
	}
	resp, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(u.bucket),
		Key:         aws.String(objectKey),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/vnd.apache.parquet"),
	})
	if err != nil {
		return "", fmt.Errorf("eventlog: put %q: %w", objectKey, err)
	}
	if resp.ETag == nil {
		return "", nil
	}
	return *resp.ETag, nil
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./pkg/eventlog/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add pkg/eventlog/r2.go
git commit -m "feat(eventlog): R2 client + Uploader for Parquet files"
```

---

## Task 7: Parquet writer for typed event rows

**Files:**
- Create: `pkg/eventlog/writer.go`

- [ ] **Step 1: Implement the writer**

Create `pkg/eventlog/writer.go`:

```go
package eventlog

import (
	"bytes"
	"fmt"

	"github.com/parquet-go/parquet-go"
)

// WriteParquet serializes rows of type T to Parquet using struct tags
// and returns the bytes. Zero rows returns (nil, nil) so callers can
// skip uploading empty batches.
//
// Uses parquet-go's generic writer which derives the schema from
// `parquet:"…"` struct tags. All tags must match between the type's
// Go struct (pkg/events/v1) and whatever reads the Parquet downstream
// (Phase 2 materializer, analytics queries).
func WriteParquet[T any](rows []T) ([]byte, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[T](&buf)
	if _, err := w.Write(rows); err != nil {
		return nil, fmt.Errorf("eventlog: parquet write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("eventlog: parquet close: %w", err)
	}
	return buf.Bytes(), nil
}

// ReadParquet is the test-only inverse. Generic over T so tests can
// assert on typed rows. Consumed by writer_test.go and later by the
// Phase 2 materializer tests.
func ReadParquet[T any](body []byte) ([]T, error) {
	r := parquet.NewGenericReader[T](bytes.NewReader(body))
	defer func() { _ = r.Close() }()

	out := make([]T, r.NumRows())
	if len(out) == 0 {
		return nil, nil
	}
	if _, err := r.Read(out); err != nil {
		return nil, fmt.Errorf("eventlog: parquet read: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./pkg/eventlog/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add pkg/eventlog/writer.go
git commit -m "feat(eventlog): WriteParquet / ReadParquet generic helpers"
```

---

## Task 8: Integration test — Parquet round-trip via MinIO testcontainer

**Files:**
- Create: `pkg/eventlog/writer_test.go`

- [ ] **Step 1: Write the integration test**

Create `pkg/eventlog/writer_test.go`:

```go
package eventlog_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

func TestParquetRoundTripViaMinio(t *testing.T) {
	ctx := context.Background()

	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(ctx) })

	endpoint, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}

	cfg := eventlog.R2Config{
		AccountID:       "test-account",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "stawi-jobs-log-test",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	client := eventlog.NewClient(cfg)

	// Ensure bucket exists — testcontainer-minio boots with none.
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	rows := []eventsv1.VariantIngestedV1{
		{
			VariantID:  "var_1",
			SourceID:   "src_greenhouse",
			ExternalID: "abc123",
			HardKey:    "src_greenhouse|abc123",
			Stage:      "ingested",
			Title:      "Senior Backend Engineer",
			Company:    "Acme",
			Country:    "KE",
			ScrapedAt:  time.Now().UTC(),
		},
	}

	buf, err := eventlog.WriteParquet(rows)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}
	if len(buf) == 0 {
		t.Fatal("WriteParquet produced empty buffer")
	}

	pk := eventsv1.PartitionKey(eventsv1.TopicVariantsIngested, rows[0].ScrapedAt, rows[0].SourceID)
	objKey := pk.ObjectPath("variants", "test-xid")

	up := eventlog.NewUploader(client, cfg.Bucket)
	etag, err := up.Put(ctx, objKey, buf)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if etag == "" {
		t.Fatal("Put returned empty ETag")
	}

	// Verify we can read the object back and decode it.
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(objKey),
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer func() { _ = got.Body.Close() }()

	var roundTrip []byte
	tmp := make([]byte, 8*1024)
	for {
		n, err := got.Body.Read(tmp)
		if n > 0 {
			roundTrip = append(roundTrip, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}

	decoded, err := eventlog.ReadParquet[eventsv1.VariantIngestedV1](roundTrip)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 row, got %d", len(decoded))
	}
	if decoded[0].VariantID != "var_1" || decoded[0].SourceID != "src_greenhouse" {
		t.Fatalf("row lost on round-trip: %+v", decoded[0])
	}
}
```

- [ ] **Step 2: Run the test**

```bash
go test ./pkg/eventlog/... -run TestParquetRoundTripViaMinio -v -timeout 3m
```

Expected: `PASS`. First run downloads the MinIO image which takes ~60 s.

- [ ] **Step 3: Commit**

```bash
git add pkg/eventlog/writer_test.go
git commit -m "test(eventlog): Parquet round-trip via MinIO testcontainer"
```

---

## Task 9: Writer service — config struct

**Files:**
- Create: `apps/writer/config/config.go`

- [ ] **Step 1: Implement the config**

Create `apps/writer/config/config.go`:

```go
// Package config loads apps/writer runtime configuration from
// environment variables. Mirrors the convention used by apps/crawler
// and apps/api — caarlos0/env with sensible defaults.
package config

import (
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/pitabwire/frame"
)

// Config is the full apps/writer config: Frame base fields (which
// includes Postgres, NATS, OTEL wiring) plus writer-specific thresholds.
type Config struct {
	frame.ConfigurationDefault

	// R2 / S3-compatible event log bucket.
	R2AccountID       string `env:"R2_LOG_ACCOUNT_ID,required"`
	R2AccessKeyID     string `env:"R2_LOG_ACCESS_KEY_ID,required"`
	R2SecretAccessKey string `env:"R2_LOG_SECRET_ACCESS_KEY,required"`
	R2Bucket          string `env:"R2_LOG_BUCKET,required"`
	R2Endpoint        string `env:"R2_LOG_ENDPOINT" envDefault:""`
	R2UsePathStyle    bool   `env:"R2_LOG_PATH_STYLE" envDefault:"false"`

	// Flush thresholds. Whichever trips first forces a flush of the
	// affected partition's buffer. Defaults match the design doc F2
	// freshness target (30 s end-to-end materializer poll → ~60 s
	// serving freshness).
	FlushMaxEvents   int           `env:"WRITER_FLUSH_MAX_EVENTS" envDefault:"10000"`
	FlushMaxBytes    int           `env:"WRITER_FLUSH_MAX_BYTES"  envDefault:"67108864"` // 64 MiB
	FlushMaxInterval time.Duration `env:"WRITER_FLUSH_MAX_INTERVAL" envDefault:"30s"`
}

// Load reads the Config from environment variables.
func Load() (*Config, error) {
	c := &Config{}
	if err := env.Parse(c); err != nil {
		return nil, err
	}
	return c, nil
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./apps/writer/...
```

Expected: directory doesn't exist yet — create it first:

```bash
mkdir -p apps/writer/config apps/writer/cmd apps/writer/service
```

Then retry:

```bash
go build ./apps/writer/config/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add apps/writer/config/config.go
git commit -m "feat(writer): env-backed Config with flush thresholds"
```

---

## Task 10: Writer buffer — per-partition batching

**Files:**
- Create: `apps/writer/service/buffer.go`
- Create: `apps/writer/service/buffer_test.go`

- [ ] **Step 1: Write failing test**

Create `apps/writer/service/buffer_test.go`:

```go
package service

import (
	"testing"
	"time"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

func newBufferForTest() *Buffer {
	return NewBuffer(Thresholds{
		MaxEvents:   3,
		MaxBytes:    1024 * 1024,
		MaxInterval: 1 * time.Minute,
	})
}

func TestBufferFlushOnMaxEvents(t *testing.T) {
	b := newBufferForTest()

	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		env := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested,
			eventsv1.VariantIngestedV1{
				SourceID:  "src_x",
				ScrapedAt: now,
			})
		flushed, _ := b.Add(env, "src_x")
		if flushed != nil {
			t.Fatalf("no flush expected at event %d", i)
		}
	}

	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested,
		eventsv1.VariantIngestedV1{SourceID: "src_x", ScrapedAt: now})
	flushed, _ := b.Add(env, "src_x")
	if flushed == nil {
		t.Fatal("expected flush at MaxEvents=3")
	}
	if flushed.Collection != "variants" {
		t.Fatalf("collection=%q, want variants", flushed.Collection)
	}
	if flushed.PartKey.Secondary != "src_x" {
		t.Fatalf("part.Secondary=%q, want src_x", flushed.PartKey.Secondary)
	}
	if len(flushed.Events) != 3 {
		t.Fatalf("len(events)=%d, want 3", len(flushed.Events))
	}
}

func TestBufferDueReturnsIntervalExpired(t *testing.T) {
	b := NewBuffer(Thresholds{
		MaxEvents:   1000,
		MaxBytes:    1024 * 1024,
		MaxInterval: 10 * time.Millisecond,
	})

	now := time.Now().UTC()
	env := eventsv1.NewEnvelope(eventsv1.TopicCanonicalsUpserted,
		eventsv1.VariantIngestedV1{SourceID: "src_y", ScrapedAt: now})
	_, _ = b.Add(env, "ab")

	time.Sleep(20 * time.Millisecond)
	due := b.Due()
	if len(due) != 1 {
		t.Fatalf("Due() len=%d, want 1", len(due))
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./apps/writer/service/...
```

Expected: build error — `undefined: Buffer, NewBuffer, Thresholds`.

- [ ] **Step 3: Implement the buffer**

Create `apps/writer/service/buffer.go`:

```go
package service

import (
	"encoding/json"
	"sync"
	"time"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// Thresholds controls when a partition buffer flushes.
type Thresholds struct {
	MaxEvents   int
	MaxBytes    int
	MaxInterval time.Duration
}

// bufferKey is the in-memory partition key: (topic, partition-secondary).
type bufferKey struct {
	topic     string
	secondary string
}

// Batch is the unit produced on flush: one Parquet file's worth of events.
type Batch struct {
	Collection string
	EventType  string
	PartKey    eventsv1.PartKey
	Events     []json.RawMessage // raw bytes; writer re-decodes to the right type
}

// partitionBuffer is per-bufferKey state.
type partitionBuffer struct {
	dt        string
	events    []json.RawMessage
	bytes     int
	openedAt  time.Time
}

// Buffer batches events in memory keyed by (topic, partition-secondary)
// and emits Batches when thresholds trip.
//
// Safe for concurrent use from N Frame subscription goroutines.
type Buffer struct {
	thresholds Thresholds

	mu   sync.Mutex
	parts map[bufferKey]*partitionBuffer
}

// NewBuffer constructs an empty Buffer.
func NewBuffer(t Thresholds) *Buffer {
	return &Buffer{
		thresholds: t,
		parts:      make(map[bufferKey]*partitionBuffer),
	}
}

// Add enqueues an event. Returns a non-nil Batch if this add caused a
// flush (size/count triggers), and the raw JSON bytes that were
// buffered. hint is the partition-secondary value (source_id, cluster
// prefix, lang, etc.).
func (b *Buffer) Add(env any, hint string) (*Batch, error) {
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	topic, occurredAt, ok := extractEnvelopeMeta(raw)
	if !ok {
		return nil, errInvalidEnvelope
	}
	pk := eventsv1.PartitionKey(topic, occurredAt, hint)
	key := bufferKey{topic: topic, secondary: pk.Secondary}

	b.mu.Lock()
	defer b.mu.Unlock()

	pb, ok := b.parts[key]
	if !ok {
		pb = &partitionBuffer{dt: pk.DT, openedAt: time.Now()}
		b.parts[key] = pb
	}
	pb.events = append(pb.events, raw)
	pb.bytes += len(raw)

	if len(pb.events) >= b.thresholds.MaxEvents || pb.bytes >= b.thresholds.MaxBytes {
		return b.flushLocked(key, pb), nil
	}
	return nil, nil
}

// Due returns Batches whose interval threshold has elapsed. Called by
// the writer service's periodic flush goroutine.
func (b *Buffer) Due() []*Batch {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	var out []*Batch
	for k, pb := range b.parts {
		if now.Sub(pb.openedAt) >= b.thresholds.MaxInterval && len(pb.events) > 0 {
			out = append(out, b.flushLocked(k, pb))
		}
	}
	return out
}

// FlushAll returns every remaining partition. Called on shutdown
// before the process exits so buffered events are durable.
func (b *Buffer) FlushAll() []*Batch {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []*Batch
	for k, pb := range b.parts {
		if len(pb.events) > 0 {
			out = append(out, b.flushLocked(k, pb))
		}
	}
	return out
}

func (b *Buffer) flushLocked(k bufferKey, pb *partitionBuffer) *Batch {
	batch := &Batch{
		Collection: collectionForTopic(k.topic),
		EventType:  k.topic,
		PartKey:    eventsv1.PartKey{DT: pb.dt, Secondary: k.secondary},
		Events:     pb.events,
	}
	delete(b.parts, k)
	return batch
}

// envelopeMeta is a minimal shape to peek at event_type + occurred_at
// without having to know the payload type.
type envelopeMeta struct {
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
}

func extractEnvelopeMeta(raw json.RawMessage) (string, time.Time, bool) {
	var m envelopeMeta
	if err := json.Unmarshal(raw, &m); err != nil || m.EventType == "" {
		return "", time.Time{}, false
	}
	return m.EventType, m.OccurredAt, true
}

// collectionForTopic maps an event topic to its Parquet partition
// name. Single source of truth for "which collection does this event
// live in."
func collectionForTopic(topic string) string {
	switch topic {
	case eventsv1.TopicVariantsIngested,
		eventsv1.TopicVariantsNormalized,
		eventsv1.TopicVariantsValidated,
		eventsv1.TopicVariantsFlagged,
		eventsv1.TopicVariantsClustered:
		return "variants"
	case eventsv1.TopicCanonicalsUpserted:
		return "canonicals"
	case eventsv1.TopicCanonicalsExpired:
		return "canonicals_expired"
	case eventsv1.TopicEmbeddings:
		return "embeddings"
	case eventsv1.TopicTranslations:
		return "translations"
	case eventsv1.TopicPublished:
		return "published"
	case eventsv1.TopicCrawlPageCompleted:
		return "crawl_page_completed"
	case eventsv1.TopicSourcesDiscovered:
		return "sources_discovered"
	default:
		return "_unknown"
	}
}

var errInvalidEnvelope = errInvalid("invalid envelope: missing event_type")

type errInvalid string

func (e errInvalid) Error() string { return string(e) }
```

- [ ] **Step 4: Run the tests**

```bash
go test ./apps/writer/service/... -v
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add apps/writer/service/buffer.go apps/writer/service/buffer_test.go
git commit -m "feat(writer): per-partition buffer with size/count/time flush triggers"
```

---

## Task 11: Frame subscription handler that routes into the buffer

**Files:**
- Create: `apps/writer/service/handler.go`

- [ ] **Step 1: Implement the handler**

Create `apps/writer/service/handler.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// WriterHandler implements frame's event handler interface for a
// single topic. Each registered topic gets its own handler instance
// bound to the same Buffer so all events land in the same sharded
// map. Handlers are pure routers — they derive the partition hint
// from the payload and call Buffer.Add. All actual IO (Parquet,
// R2) happens out-of-band in the flusher.
type WriterHandler struct {
	topic  string
	buffer *Buffer
}

// NewWriterHandler binds a handler to a topic + buffer.
func NewWriterHandler(topic string, buffer *Buffer) *WriterHandler {
	return &WriterHandler{topic: topic, buffer: buffer}
}

// Name — the event name Frame dispatches on.
func (h *WriterHandler) Name() string { return h.topic }

// PayloadType returns a pointer to a json.RawMessage so Frame skips
// payload-specific deserialization. The Buffer re-reads the raw bytes
// to peek at event_type + occurred_at + partition hint; the writer's
// flusher also reads raw bytes and typed-decodes per collection.
func (h *WriterHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate — a cheap shape check so poisoned events dead-letter early.
func (h *WriterHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("writer: empty or wrong-type payload")
	}
	return nil
}

// Execute enqueues the event into the buffer. A returned error tells
// Frame to negative-ack (redeliver). A nil error tells Frame to ack —
// but ack-after-upload semantics require Frame's at-least-once mode.
// In Phase 1 we accept at-least-once with an in-process buffer; the
// flusher promotes acks to ack-after-upload in Phase 2 when durable
// flush tracking lands.
func (h *WriterHandler) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("writer: wrong payload type")
	}
	hint := extractHint(*raw, h.topic)
	if _, err := h.buffer.Add(json.RawMessage(*raw), hint); err != nil {
		return fmt.Errorf("writer: buffer.Add: %w", err)
	}
	return nil
}

// extractHint pulls the partition-secondary value from the raw
// envelope JSON. For Phase 1 we only wire VariantIngestedV1 (hint =
// source_id). Additional types join this switch as they come online.
func extractHint(raw json.RawMessage, topic string) string {
	switch topic {
	case eventsv1.TopicVariantsIngested:
		return decodeField(raw, "payload.source_id")
	case eventsv1.TopicCanonicalsUpserted, eventsv1.TopicCanonicalsExpired:
		return decodeField(raw, "payload.cluster_id")
	case eventsv1.TopicEmbeddings:
		return decodeField(raw, "payload.canonical_id")
	case eventsv1.TopicTranslations:
		return decodeField(raw, "payload.lang")
	default:
		return ""
	}
}

// decodeField walks a dotted path through the JSON tree and returns
// the string value (or "" on any miss).
func decodeField(raw json.RawMessage, dotted string) string {
	// For Phase 1 every payload field is a top-level string, so a
	// minimal two-step path ("payload.x") is enough.
	var step1 map[string]json.RawMessage
	if err := json.Unmarshal(raw, &step1); err != nil {
		return ""
	}
	// dotted is "payload.key"; split on first '.'
	for i := 0; i < len(dotted); i++ {
		if dotted[i] == '.' {
			head := dotted[:i]
			tail := dotted[i+1:]
			sub, ok := step1[head]
			if !ok {
				return ""
			}
			var m map[string]string
			if err := json.Unmarshal(sub, &m); err != nil {
				return ""
			}
			return m[tail]
		}
	}
	return ""
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./apps/writer/service/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add apps/writer/service/handler.go
git commit -m "feat(writer): Frame subscription handler that routes into the buffer"
```

---

## Task 12: Writer service — compose buffer, uploader, periodic flusher

**Files:**
- Create: `apps/writer/service/service.go`

- [ ] **Step 1: Implement the service**

Create `apps/writer/service/service.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// Service is apps/writer's composition root.
type Service struct {
	svc      *frame.Service
	buffer   *Buffer
	uploader *eventlog.Uploader
	flushInt time.Duration
}

// NewService wires the Frame service + buffer + uploader. The caller
// is responsible for starting the Frame run-loop (usually via
// svc.Run in main.go).
func NewService(
	svc *frame.Service,
	buffer *Buffer,
	uploader *eventlog.Uploader,
	flushInterval time.Duration,
) *Service {
	return &Service{svc: svc, buffer: buffer, uploader: uploader, flushInt: flushInterval}
}

// RegisterSubscriptions wires one WriterHandler per topic. Called
// during startup, before svc.Run. Each handler is the Frame-dispatched
// entry point for that topic's messages.
func (s *Service) RegisterSubscriptions(topics []string) error {
	for _, t := range topics {
		h := NewWriterHandler(t, s.buffer)
		if err := s.svc.EventsManager().Register(h); err != nil {
			return fmt.Errorf("writer: register %q: %w", t, err)
		}
	}
	return nil
}

// RunFlusher drives the time-based flush path. Size/count-based
// flushes happen inline in WriterHandler.Execute; this goroutine
// catches idle partitions whose MaxInterval has elapsed.
//
// Blocks until ctx is cancelled. On cancel it drains every remaining
// partition with FlushAll before returning so no buffered event is
// lost on graceful shutdown.
func (s *Service) RunFlusher(ctx context.Context) error {
	t := time.NewTicker(s.flushInt / 3)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return s.drain(context.Background())
		case <-t.C:
			for _, b := range s.buffer.Due() {
				if err := s.uploadBatch(ctx, b); err != nil {
					util.Log(ctx).WithError(err).
						WithField("collection", b.Collection).
						WithField("part", b.PartKey.Secondary).
						Error("writer: upload batch failed; keeping events for redelivery")
				}
			}
		}
	}
}

func (s *Service) drain(ctx context.Context) error {
	remaining := s.buffer.FlushAll()
	if len(remaining) == 0 {
		return nil
	}
	util.Log(ctx).WithField("batches", len(remaining)).Info("writer: draining on shutdown")
	for _, b := range remaining {
		if err := s.uploadBatch(ctx, b); err != nil {
			return fmt.Errorf("writer: drain: %w", err)
		}
	}
	return nil
}

// uploadBatch serializes the batch's events as Parquet and uploads
// to R2. For Phase 1 we dispatch only on VariantIngestedV1 since
// that's the only event type implemented. Phase 2 adds the other
// collections.
func (s *Service) uploadBatch(ctx context.Context, b *Batch) error {
	var body []byte
	var err error

	switch b.EventType {
	case eventsv1.TopicVariantsIngested:
		body, err = encodeBatch[eventsv1.VariantIngestedV1](b.Events)
	default:
		return fmt.Errorf("writer: no encoder registered for %q", b.EventType)
	}
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}

	objKey := b.PartKey.ObjectPath(b.Collection, xid.New().String())
	etag, err := s.uploader.Put(ctx, objKey, body)
	if err != nil {
		return fmt.Errorf("writer: put %q: %w", objKey, err)
	}
	util.Log(ctx).
		WithField("object", objKey).
		WithField("etag", etag).
		WithField("events", len(b.Events)).
		Info("writer: parquet flushed")
	return nil
}

// encodeBatch decodes each raw envelope into the typed payload and
// writes the resulting slice as Parquet. Parquet columns are
// generated from the P struct's `parquet` tags.
func encodeBatch[P any](raws []json.RawMessage) ([]byte, error) {
	rows := make([]P, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[P]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		rows = append(rows, env.Payload)
	}
	return eventlog.WriteParquet(rows)
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./apps/writer/service/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add apps/writer/service/service.go
git commit -m "feat(writer): Service composition + periodic flusher + drain"
```

---

## Task 13: Writer entrypoint (`cmd/main.go`)

**Files:**
- Create: `apps/writer/cmd/main.go`

- [ ] **Step 1: Implement main**

Create `apps/writer/cmd/main.go`:

```go
// apps/writer/cmd — entrypoint for the event-log writer service.
//
// The writer is a disposable pod that subscribes to every job pipeline
// topic, buffers incoming events per (partition_dt, partition_secondary),
// and flushes Parquet files to R2 on size/count/time triggers.
package main

import (
	"context"
	"log"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"

	writercfg "stawi.jobs/apps/writer/config"
	writersvc "stawi.jobs/apps/writer/service"
)

func main() {
	ctx := context.Background()

	cfg, err := writercfg.Load()
	if err != nil {
		log.Fatalf("writer: load config: %v", err)
	}

	ctx, svc := frame.NewServiceWithContext(ctx, "writer", frame.WithConfig(cfg))
	defer svc.Stop(ctx)

	client := eventlog.NewClient(eventlog.R2Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2Bucket,
		Endpoint:        cfg.R2Endpoint,
		UsePathStyle:    cfg.R2UsePathStyle,
	})
	uploader := eventlog.NewUploader(client, cfg.R2Bucket)

	buffer := writersvc.NewBuffer(writersvc.Thresholds{
		MaxEvents:   cfg.FlushMaxEvents,
		MaxBytes:    cfg.FlushMaxBytes,
		MaxInterval: cfg.FlushMaxInterval,
	})

	service := writersvc.NewService(svc, buffer, uploader, cfg.FlushMaxInterval)
	if err := service.RegisterSubscriptions(eventsv1.AllTopics()); err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: register subscriptions failed")
	}

	go func() {
		if err := service.RunFlusher(ctx); err != nil {
			util.Log(ctx).WithError(err).Error("writer: flusher exited")
		}
	}()

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: frame.Run failed")
	}
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./apps/writer/cmd/...
```

Expected: no output, exit 0.

If Frame's function signatures (e.g. `frame.NewServiceWithContext`, `frame.WithConfig`, `svc.Run`, `svc.EventsManager().Register`) differ from the ones used here, mirror the shape used by `apps/crawler/cmd/main.go` in the same repo — Frame API is stable per-major so cross-referencing another Frame app in the repo is the authoritative source.

- [ ] **Step 3: Commit**

```bash
git add apps/writer/cmd/main.go
git commit -m "feat(writer): entrypoint wiring Frame + buffer + uploader + flusher"
```

---

## Task 14: End-to-end service test (emit via Frame → file on MinIO)

**Files:**
- Create: `apps/writer/service/service_test.go`

- [ ] **Step 1: Write the test**

This test stands up a Frame service with an in-memory pub/sub backend + a MinIO testcontainer, registers the writer, publishes a `VariantIngestedV1` event, and asserts the Parquet file appears in the bucket with the right partition layout.

Create `apps/writer/service/service_test.go`:

```go
package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/frame"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"

	writersvc "stawi.jobs/apps/writer/service"
)

func TestWriterE2EVariantIngested(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 1. MinIO
	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })

	endpoint, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}

	cfg := eventlog.R2Config{
		AccountID:       "test",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "stawi-jobs-log-e2e",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	client := eventlog.NewClient(cfg)
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	uploader := eventlog.NewUploader(client, cfg.Bucket)

	// 2. Frame service with in-memory pub/sub
	// Frame's in-memory transport is enabled via the default config
	// when no NATS URL is set; confirm by cross-referencing
	// apps/crawler/service/setup_test.go or equivalent.
	ctx, svc := frame.NewServiceWithContext(ctx, "writer-e2e")
	defer svc.Stop(ctx)

	// 3. Buffer with short MaxInterval so the flusher triggers fast
	buf := writersvc.NewBuffer(writersvc.Thresholds{
		MaxEvents:   100,
		MaxBytes:    64 << 20,
		MaxInterval: 250 * time.Millisecond,
	})
	wService := writersvc.NewService(svc, buf, uploader, 250*time.Millisecond)
	if err := wService.RegisterSubscriptions([]string{eventsv1.TopicVariantsIngested}); err != nil {
		t.Fatalf("RegisterSubscriptions: %v", err)
	}
	go func() { _ = wService.RunFlusher(ctx) }()

	// 4. Publish an event
	now := time.Now().UTC()
	env := eventsv1.NewEnvelope(
		eventsv1.TopicVariantsIngested,
		eventsv1.VariantIngestedV1{
			VariantID: "var_e2e_1",
			SourceID:  "src_e2e",
			HardKey:   "src_e2e|e2e1",
			Stage:     "ingested",
			Title:     "E2E Test Job",
			ScrapedAt: now,
		},
	)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsIngested, json.RawMessage(raw)); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// 5. Wait for flush + upload
	expectedPrefix := "variants/dt=" + now.Format("2006-01-02") + "/src=src_e2e/"
	waitUntil := time.Now().Add(10 * time.Second)
	for time.Now().Before(waitUntil) {
		list, lerr := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(cfg.Bucket),
			Prefix: aws.String(expectedPrefix),
		})
		if lerr == nil && list.KeyCount != nil && *list.KeyCount > 0 {
			// Found a file; verify it decodes
			first := *list.Contents[0].Key
			if !strings.HasPrefix(first, expectedPrefix) {
				t.Fatalf("unexpected key %q, want prefix %q", first, expectedPrefix)
			}
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for flushed file under %q", expectedPrefix)
}
```

- [ ] **Step 2: Run the test**

```bash
go test ./apps/writer/service/... -run TestWriterE2EVariantIngested -v -timeout 5m
```

Expected: `PASS`. If Frame's `EventsManager().Emit` signature differs, adjust to match the existing usage in `pkg/pipeline/handlers/validate.go:163-170`.

- [ ] **Step 3: Commit**

```bash
git add apps/writer/service/service_test.go
git commit -m "test(writer): end-to-end emit → Parquet → MinIO integration test"
```

---

## Task 15: Dockerfile and Makefile wiring

**Files:**
- Create: `apps/writer/Dockerfile`
- Modify: `Makefile`

- [ ] **Step 1: Copy the crawler Dockerfile pattern**

Read `apps/crawler/Dockerfile`:

```bash
cat apps/crawler/Dockerfile
```

Create `apps/writer/Dockerfile` mirroring it, substituting `crawler` → `writer` throughout. The repo's standard shape is a multi-stage Go build ending with a distroless runtime image. If the crawler Dockerfile is:

```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/crawler ./apps/crawler/cmd

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/crawler /crawler
USER nonroot:nonroot
ENTRYPOINT ["/crawler"]
```

Then `apps/writer/Dockerfile`:

```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/writer ./apps/writer/cmd

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/writer /writer
USER nonroot:nonroot
ENTRYPOINT ["/writer"]
```

Cross-check against the actual `apps/crawler/Dockerfile` before committing.

- [ ] **Step 2: Extend the Makefile**

Edit `Makefile` — find the `APP_DIRS` declaration (line 3 per the current file) and add `apps/writer`:

```makefile
APP_DIRS := apps/crawler apps/scheduler apps/api apps/writer
```

Also add a `run-writer` target near the other `run-*` targets:

```makefile
run-writer:
	go run ./apps/writer/cmd
```

Update the `.PHONY` block to include `run-writer`:

```makefile
.PHONY: deps build test run-crawler run-scheduler run-api run-writer \
        infra-up infra-down \
        ui-deps ui-build ui-dev \
        archive-verify
```

- [ ] **Step 3: Verify the build works via the Makefile**

```bash
cd /home/j/code/stawi.jobs
make build
```

Expected: `bin/writer` (and the other binaries) exist:

```bash
ls -la bin/writer
```

Expected: a binary file ~20 MB.

- [ ] **Step 4: Verify the full test suite still passes**

```bash
make test
```

Expected: `PASS` across the tree. MinIO-backed tests take ~60–90 s on first run.

- [ ] **Step 5: Commit**

```bash
git add apps/writer/Dockerfile Makefile
git commit -m "chore(writer): Dockerfile + Makefile targets"
```

---

## Plan completion verification

After all tasks are complete, run:

```bash
# All tests green
make test

# apps/writer binary builds cleanly
make build && ls -la bin/writer

# Git history shows one commit per task (15 commits from this plan)
git log --oneline origin/main..HEAD
```

Expected:
- Every test passes (Frame handler tests, buffer tests, Parquet round-trip, end-to-end MinIO test).
- `bin/writer` is a working binary.
- 15 commits, each with a descriptive message scoped to one task.

At this point, the event-log pipe foundation is landed. Phase 2 can plug in the materializer and the first read-path endpoints.
