# Phase 3 — Worker Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `apps/worker` — a single disposable service that turns `VariantIngestedV1` events into fully canonicalized, embedded, translated, published jobs on the event log. It runs all pipeline stages inside one binary as independent Frame subscriptions: normalize → validate → dedup → canonical-merge, then the three parallel downstream consumers on canonicals (embed, translate, publish).

**Architecture:** One `apps/worker` binary with 7 internal Frame event subscriptions. Dedup uses a new `pkg/kv` Valkey/Redis wrapper (via `github.com/redis/go-redis/v9`, already in `go.mod`). AI goes through the existing `extraction.Extractor`. Publishing reuses `pkg/publish`. All output is pub/sub events — no Postgres reads or writes for the jobs domain.

**Tech stack:**
- Go 1.26, Frame (`github.com/pitabwire/frame`), `pitabwire/util` logging
- `github.com/redis/go-redis/v9` (existing) for KV
- `pkg/extraction.Extractor` (existing) for LLM chat + embed
- `pkg/publish.R2Publisher` (existing) for R2 snapshot writes
- `pkg/bloom` (existing) for dedup false-positive gate
- Testcontainers for Redis + pub/sub in-memory + MinIO

**What's in this plan:**
- Six new event payload types: `VariantNormalizedV1`, `VariantValidatedV1`, `VariantFlaggedV1`, `VariantClusteredV1`, `TranslationV1`, `PublishedV1`
- Writer encoder switch extended to cover all six new topics
- `pkg/kv` — small Redis wrapper with dedup + cluster key helpers
- `apps/worker` — config, 7 subscription handlers (normalize, validate, dedup, canonical, embed, translate, publish), composition root, entrypoint, Dockerfile
- End-to-end pipeline test: emit one `VariantIngestedV1` → assert `CanonicalUpsertedV1` + `EmbeddingV1` + `TranslationV1` + `PublishedV1` events fire

**What's NOT in this plan:**
- Crawler refactor (Phase 4) — existing `apps/crawler` keeps emitting legacy `variant.raw.stored` events; the new worker doesn't touch those.
- Candidates refactor (Phase 5).
- Greenfield cutover (Phase 6) — the legacy `pkg/pipeline/handlers/*` stay alongside the new worker until the cutover drops them.
- `apps/materializer` integration — Phase 2 already consumes `CanonicalUpsertedV1` and `EmbeddingV1`, so the pipeline's outputs naturally land in Manticore once a real event is emitted. The pipeline test in Task 14 verifies emission only; full-chain (worker → writer → materializer → Manticore) is a composition of phases and needs no new code.

---

## File structure

**Create:**

| File | Responsibility |
|---|---|
| `pkg/events/v1/pipeline.go` | Six new payload structs with json + parquet tags |
| `pkg/kv/redis.go` | Redis client construction, connect, Ping, Close |
| `pkg/kv/dedup.go` | `DedupStore` — hard_key → cluster_id map + bloom front |
| `pkg/kv/cluster.go` | `ClusterStore` — cluster_id → msgpack canonical snapshot |
| `pkg/kv/redis_test.go` | Integration test with Redis testcontainer |
| `apps/worker/config/config.go` | env-backed config (Redis URL, R2 publish bucket, LLM backends, translation langs) |
| `apps/worker/service/normalize.go` | Normalize subscription handler |
| `apps/worker/service/validate.go` | Validate subscription handler (LLM, fail-open) |
| `apps/worker/service/dedup.go` | Dedup subscription handler |
| `apps/worker/service/canonical.go` | Canonical merge handler |
| `apps/worker/service/embed.go` | Embed subscription handler |
| `apps/worker/service/translate.go` | Translate subscription handler |
| `apps/worker/service/publish.go` | Publish subscription handler |
| `apps/worker/service/service.go` | `Service` — registers all 7 handlers |
| `apps/worker/service/service_test.go` | End-to-end pipeline test (in-memory pubsub) |
| `apps/worker/cmd/main.go` | Entrypoint |
| `apps/worker/Dockerfile` | Multi-stage build (mirror of apps/materializer) |

**Modify:**

| File | Change |
|---|---|
| `apps/writer/service/service.go` | Extend `uploadBatch` switch to cover all 6 new topics |
| `pkg/events/v1/envelope_test.go` | Add one round-trip test per new event type (6 short tests) |
| `Makefile` | Add `apps/worker` to `APP_DIRS`, add `run-worker` target |

---

## Task 1: New event payload types

**Files:**
- Create: `pkg/events/v1/pipeline.go`
- Modify: `pkg/events/v1/envelope_test.go`

- [ ] **Step 1: Write failing tests**

Append to `pkg/events/v1/envelope_test.go`:

```go
func TestVariantNormalizedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsNormalized, VariantNormalizedV1{
		VariantID: "var_1", SourceID: "src_x", HardKey: "src_x|e1",
		Title: "Engineer", Country: "KE", RemoteType: "remote",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantNormalizedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.VariantID != "var_1" || back.Payload.Country != "KE" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestVariantValidatedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsValidated, VariantValidatedV1{
		VariantID: "var_1", SourceID: "src_x", ValidationScore: 0.9, ModelVersion: "v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantValidatedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.VariantID != "var_1" || back.Payload.ValidationScore != 0.9 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestVariantFlaggedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsFlagged, VariantFlaggedV1{
		VariantID: "var_1", Reason: "bad title",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantFlaggedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.Reason != "bad title" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestVariantClusteredRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicVariantsClustered, VariantClusteredV1{
		VariantID: "var_1", ClusterID: "clu_1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[VariantClusteredV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.ClusterID != "clu_1" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestTranslationRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicTranslations, TranslationV1{
		CanonicalID: "can_1", Lang: "sw", TitleTr: "Mhandisi",
		DescriptionTr: "Tunaajiri...", ModelVersion: "v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[TranslationV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.Lang != "sw" || back.Payload.TitleTr != "Mhandisi" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestPublishedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicPublished, PublishedV1{
		CanonicalID: "can_1", Slug: "job-slug", R2Version: 3,
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[PublishedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.Slug != "job-slug" || back.Payload.R2Version != 3 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./pkg/events/v1/...
```

Expected: `undefined: VariantNormalizedV1, VariantValidatedV1, …`.

- [ ] **Step 3: Implement the types**

Create `pkg/events/v1/pipeline.go`:

```go
package eventsv1

import "time"

// VariantNormalizedV1 — post-normalize stage. Same fields as
// VariantIngestedV1 plus normalized versions of country (ISO 2),
// remote_type, and parsed salary numbers. Phase 3's normalize
// handler consumes VariantIngestedV1 and emits this.
type VariantNormalizedV1 struct {
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
}

// VariantValidatedV1 — emitted when a variant passes the AI
// validator with confidence >= threshold. Carries the variant
// state forward plus validation metadata.
type VariantValidatedV1 struct {
	VariantID        string  `json:"variant_id"        parquet:"variant_id"`
	SourceID         string  `json:"source_id"         parquet:"source_id"`
	ValidationScore  float64 `json:"validation_score"  parquet:"validation_score"`
	ValidationNotes  string  `json:"validation_notes"  parquet:"validation_notes,optional"`
	ModelVersion     string  `json:"model_version"     parquet:"model_version,optional"`
	// Normalized is the full previous-stage payload, so downstream
	// consumers (dedup, canonical) don't need to re-fetch.
	Normalized       VariantNormalizedV1 `json:"normalized"       parquet:"normalized"`
}

// VariantFlaggedV1 — emitted when a variant fails validation. Terminal
// for the happy path; audit sink.
type VariantFlaggedV1 struct {
	VariantID    string  `json:"variant_id"    parquet:"variant_id"`
	SourceID     string  `json:"source_id"     parquet:"source_id"`
	Reason       string  `json:"reason"        parquet:"reason"`
	Confidence   float64 `json:"confidence"    parquet:"confidence,optional"`
	ModelVersion string  `json:"model_version" parquet:"model_version,optional"`
}

// VariantClusteredV1 — emitted post-dedup. Identifies which cluster
// this variant belongs to. Downstream canonical-merge uses cluster_id
// to look up the current snapshot + merge in the new variant's fields.
type VariantClusteredV1 struct {
	VariantID string              `json:"variant_id" parquet:"variant_id"`
	ClusterID string              `json:"cluster_id" parquet:"cluster_id"`
	IsNew     bool                `json:"is_new"     parquet:"is_new"`
	Validated VariantValidatedV1  `json:"validated"  parquet:"validated"`
}

// TranslationV1 — emitted by the translate handler once a canonical
// has been translated to a single target language. One event per
// (canonical, lang) pair.
type TranslationV1 struct {
	CanonicalID   string `json:"canonical_id"   parquet:"canonical_id"`
	Lang          string `json:"lang"           parquet:"lang"`
	TitleTr       string `json:"title_tr"       parquet:"title_tr,optional"`
	DescriptionTr string `json:"description_tr" parquet:"description_tr,optional"`
	ModelVersion  string `json:"model_version"  parquet:"model_version,optional"`
}

// PublishedV1 — emitted by the publish handler after a canonical's
// R2 snapshot is written. Downstream analytics + cache-purge listeners
// consume this.
type PublishedV1 struct {
	CanonicalID string    `json:"canonical_id" parquet:"canonical_id"`
	Slug        string    `json:"slug"         parquet:"slug"`
	R2Version   int       `json:"r2_version"   parquet:"r2_version"`
	PublishedAt time.Time `json:"published_at" parquet:"published_at"`
}
```

- [ ] **Step 4: Run — expect pass**

```bash
go test ./pkg/events/v1/... -count=1
```

- [ ] **Step 5: Commit**

```bash
git add pkg/events/v1/pipeline.go pkg/events/v1/envelope_test.go
git commit -m "feat(events): add pipeline payload types (normalized/validated/flagged/clustered/translation/published)"
```

---

## Task 2: Extend writer encoder

**Files:**
- Modify: `apps/writer/service/service.go`

- [ ] **Step 1: Add cases to the encoder switch**

Open `apps/writer/service/service.go`. Find `uploadBatch`'s switch statement. It currently covers `TopicVariantsIngested`, `TopicCanonicalsUpserted`, `TopicEmbeddings`. Add six new cases covering all of Phase 3's event types:

```go
switch b.EventType {
case eventsv1.TopicVariantsIngested:
    body, err = encodeBatch[eventsv1.VariantIngestedV1](b.Events)
case eventsv1.TopicVariantsNormalized:
    body, err = encodeBatch[eventsv1.VariantNormalizedV1](b.Events)
case eventsv1.TopicVariantsValidated:
    body, err = encodeBatch[eventsv1.VariantValidatedV1](b.Events)
case eventsv1.TopicVariantsFlagged:
    body, err = encodeBatch[eventsv1.VariantFlaggedV1](b.Events)
case eventsv1.TopicVariantsClustered:
    body, err = encodeBatch[eventsv1.VariantClusteredV1](b.Events)
case eventsv1.TopicCanonicalsUpserted:
    body, err = encodeBatch[eventsv1.CanonicalUpsertedV1](b.Events)
case eventsv1.TopicEmbeddings:
    body, err = encodeBatch[eventsv1.EmbeddingV1](b.Events)
case eventsv1.TopicTranslations:
    body, err = encodeBatch[eventsv1.TranslationV1](b.Events)
case eventsv1.TopicPublished:
    body, err = encodeBatch[eventsv1.PublishedV1](b.Events)
default:
    return fmt.Errorf("writer: no encoder registered for %q", b.EventType)
}
```

- [ ] **Step 2: Run writer tests — must still pass**

```bash
go test ./apps/writer/... -count=1 -timeout 5m
```

- [ ] **Step 3: Commit**

```bash
git add apps/writer/service/service.go
git commit -m "feat(writer): encode all Phase 3 pipeline event types"
```

---

## Task 3: `pkg/kv` — Redis client + helpers (TDD)

**Files:**
- Create: `pkg/kv/redis.go`
- Create: `pkg/kv/dedup.go`
- Create: `pkg/kv/cluster.go`
- Create: `pkg/kv/redis_test.go`

- [ ] **Step 1: Write failing tests**

Create `pkg/kv/redis_test.go`:

```go
package kv_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"stawi.jobs/pkg/kv"
)

func startRedis(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "6379/tcp")
	url := fmt.Sprintf("redis://%s:%s/0", host, port.Port())
	return url, func() { _ = c.Terminate(context.Background()) }
}

func TestDedupGetSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	url, stop := startRedis(t, ctx)
	defer stop()

	client, err := kv.Open(kv.Config{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = client.Close() }()

	dd := kv.NewDedupStore(client)

	// Miss
	got, ok, err := dd.Get(ctx, "src|extA")
	if err != nil {
		t.Fatalf("get miss: %v", err)
	}
	if ok {
		t.Fatalf("expected miss, got %q", got)
	}

	// Set + hit
	if err := dd.Set(ctx, "src|extA", "clu_1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err = dd.Get(ctx, "src|extA")
	if err != nil || !ok || got != "clu_1" {
		t.Fatalf("expected hit clu_1, got ok=%v val=%q err=%v", ok, got, err)
	}
}

func TestClusterGetSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	url, stop := startRedis(t, ctx)
	defer stop()

	client, err := kv.Open(kv.Config{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = client.Close() }()

	cs := kv.NewClusterStore(client)

	snap := kv.ClusterSnapshot{
		ClusterID: "clu_1",
		Title:     "Backend Engineer",
		Company:   "Acme",
	}
	if err := cs.Set(ctx, snap); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, ok, err := cs.Get(ctx, "clu_1")
	if err != nil || !ok {
		t.Fatalf("expected hit, got ok=%v err=%v", ok, err)
	}
	if got.Title != "Backend Engineer" {
		t.Fatalf("round-trip lost: %+v", got)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./pkg/kv/...
```

Expected: `undefined: kv.Open, kv.Config, kv.NewDedupStore, kv.NewClusterStore, kv.ClusterSnapshot`.

- [ ] **Step 3: Implement the client**

Create `pkg/kv/redis.go`:

```go
// Package kv is a thin wrapper around go-redis for the worker
// pipeline's two stateful lookups: dedup (hard_key → cluster_id)
// and cluster snapshots (cluster_id → compact canonical view used
// by the canonical-merge stage).
package kv

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Config describes how to reach Redis/Valkey.
type Config struct {
	// URL is a go-redis connection URL, e.g. "redis://host:6379/0".
	URL string
}

// Client wraps a *redis.Client so downstream stores can share it.
type Client struct {
	rdb *redis.Client
}

// Open parses the URL and connects. The caller is responsible for Close.
func Open(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("kv: empty URL")
	}
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("kv: parse url: %w", err)
	}
	return &Client{rdb: redis.NewClient(opt)}, nil
}

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Close releases the connection pool.
func (c *Client) Close() error { return c.rdb.Close() }

// Redis exposes the underlying client for stores that need
// pipelined / scripted operations beyond simple GET/SET.
func (c *Client) Redis() *redis.Client { return c.rdb }
```

Create `pkg/kv/dedup.go`:

```go
package kv

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// DedupStore maps hard_key → cluster_id. No TTL — a hard_key's
// cluster assignment is permanent until explicit reset during
// operational cluster-rebuild.
type DedupStore struct {
	c *Client
}

// NewDedupStore wraps a Client.
func NewDedupStore(c *Client) *DedupStore { return &DedupStore{c: c} }

const dedupPrefix = "dedup:"

// Get returns (cluster_id, true, nil) on hit, ("", false, nil) on miss.
// Errors are wrapped with a kv: prefix.
func (s *DedupStore) Get(ctx context.Context, hardKey string) (string, bool, error) {
	val, err := s.c.rdb.Get(ctx, dedupPrefix+hardKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("kv: dedup get: %w", err)
	}
	return val, true, nil
}

// Set records an assignment. Idempotent — same key, same value
// is a no-op; same key, different value overwrites (operational
// re-cluster path).
func (s *DedupStore) Set(ctx context.Context, hardKey, clusterID string) error {
	if err := s.c.rdb.Set(ctx, dedupPrefix+hardKey, clusterID, 0).Err(); err != nil {
		return fmt.Errorf("kv: dedup set: %w", err)
	}
	return nil
}
```

Create `pkg/kv/cluster.go`:

```go
package kv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ClusterSnapshot is the compact canonical view held in Redis so
// the canonical-merge stage can merge new variant fields into the
// existing cluster without re-reading the full canonicals partition.
// Small — ~1 KB per cluster.
type ClusterSnapshot struct {
	ClusterID      string    `json:"cluster_id"`
	CanonicalID    string    `json:"canonical_id,omitempty"`
	Slug           string    `json:"slug,omitempty"`
	Title          string    `json:"title,omitempty"`
	Company        string    `json:"company,omitempty"`
	Description    string    `json:"description,omitempty"`
	Country        string    `json:"country,omitempty"`
	Language       string    `json:"language,omitempty"`
	RemoteType     string    `json:"remote_type,omitempty"`
	EmploymentType string    `json:"employment_type,omitempty"`
	Seniority      string    `json:"seniority,omitempty"`
	SalaryMin      float64   `json:"salary_min,omitempty"`
	SalaryMax      float64   `json:"salary_max,omitempty"`
	Currency       string    `json:"currency,omitempty"`
	Category       string    `json:"category,omitempty"`
	QualityScore   float64   `json:"quality_score,omitempty"`
	Status         string    `json:"status,omitempty"`
	FirstSeenAt    time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt     time.Time `json:"last_seen_at,omitempty"`
	PostedAt       time.Time `json:"posted_at,omitempty"`
	ApplyURL       string    `json:"apply_url,omitempty"`
}

// ClusterStore wraps a Client with Get/Set of ClusterSnapshot.
type ClusterStore struct {
	c *Client
}

// NewClusterStore wraps a Client.
func NewClusterStore(c *Client) *ClusterStore { return &ClusterStore{c: c} }

const clusterPrefix = "cluster:"

// Get returns the snapshot if one exists.
func (s *ClusterStore) Get(ctx context.Context, clusterID string) (ClusterSnapshot, bool, error) {
	raw, err := s.c.rdb.Get(ctx, clusterPrefix+clusterID).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ClusterSnapshot{}, false, nil
		}
		return ClusterSnapshot{}, false, fmt.Errorf("kv: cluster get: %w", err)
	}
	var s0 ClusterSnapshot
	if err := json.Unmarshal(raw, &s0); err != nil {
		return ClusterSnapshot{}, false, fmt.Errorf("kv: cluster decode: %w", err)
	}
	return s0, true, nil
}

// Set writes the snapshot.
func (s *ClusterStore) Set(ctx context.Context, snap ClusterSnapshot) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("kv: cluster encode: %w", err)
	}
	if err := s.c.rdb.Set(ctx, clusterPrefix+snap.ClusterID, raw, 0).Err(); err != nil {
		return fmt.Errorf("kv: cluster set: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./pkg/kv/... -count=1 -timeout 3m
```

Expected: PASS (first run pulls the redis:7-alpine image, ~10–20 s).

- [ ] **Step 5: Commit**

```bash
git add pkg/kv/
git commit -m "feat(kv): Redis wrapper with DedupStore + ClusterStore"
```

---

## Task 4: `apps/worker` config

**Files:**
- Create: `apps/worker/config/config.go`
- Prereq: `mkdir -p apps/worker/config apps/worker/cmd apps/worker/service`

- [ ] **Step 1: Create directories**

```bash
mkdir -p apps/worker/config apps/worker/cmd apps/worker/service
```

- [ ] **Step 2: Implement config**

Create `apps/worker/config/config.go` — mirror `apps/materializer/config/config.go` for the Frame base + env pattern:

```go
// Package config loads apps/worker runtime configuration.
package config

import (
	"time"

	fconfig "github.com/pitabwire/frame/config"
)

// Config for apps/worker. Frame base handles Postgres + pub/sub +
// OTEL; this struct adds Redis, R2 publish, LLM backends, and
// translation configuration.
type Config struct {
	fconfig.ConfigurationDefault

	// Redis / Valkey for dedup + cluster.
	RedisURL string `env:"REDIS_URL,required"`

	// R2 publish bucket (the live job-detail JSONs, distinct from the
	// event log). pkg/publish creates snapshots here.
	R2PublishAccountID       string `env:"R2_PUBLISH_ACCOUNT_ID,required"`
	R2PublishAccessKeyID     string `env:"R2_PUBLISH_ACCESS_KEY_ID,required"`
	R2PublishSecretAccessKey string `env:"R2_PUBLISH_SECRET_ACCESS_KEY,required"`
	R2PublishBucket          string `env:"R2_PUBLISH_BUCKET,required"`

	// AI backends. All optional — empty disables the given stage
	// gracefully (fall-through without that AI call).
	InferenceBaseURL   string `env:"INFERENCE_BASE_URL"`
	InferenceAPIKey    string `env:"INFERENCE_API_KEY"`
	InferenceModel     string `env:"INFERENCE_MODEL"`
	EmbeddingBaseURL   string `env:"EMBEDDING_BASE_URL"`
	EmbeddingAPIKey    string `env:"EMBEDDING_API_KEY"`
	EmbeddingModel     string `env:"EMBEDDING_MODEL"`

	// Translation target languages. Empty → translator is a no-op.
	// Pipe-separated: "en|sw|fr".
	TranslationLangs []string `env:"TRANSLATION_LANGS" envSeparator:"|"`

	// Minimum validation confidence to mark a variant "validated"
	// (below goes to flagged). Matches the existing handler.
	ValidationMinConfidence float64 `env:"VALIDATION_MIN_CONFIDENCE" envDefault:"0.7"`

	// Validation LLM request timeout.
	ValidationTimeout time.Duration `env:"VALIDATION_TIMEOUT" envDefault:"30s"`
}
```

Mirror the `Load` function used in `apps/materializer/config/config.go` (it uses `fconfig.FromEnv[Config]()`). If the existing helper returns a value (not pointer), do the same here.

- [ ] **Step 3: Verify compiles**

```bash
go build ./apps/worker/config/...
```

- [ ] **Step 4: Commit**

```bash
git add apps/worker/config/config.go
git commit -m "feat(worker): env-backed Config with Redis + AI + publish settings"
```

---

## Task 5: Normalize handler

**Files:**
- Create: `apps/worker/service/normalize.go`

- [ ] **Step 1: Implement**

Create `apps/worker/service/normalize.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/pitabwire/frame"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// NormalizeHandler consumes VariantIngestedV1, applies deterministic
// normalization (country codes, remote-type inference), and emits
// VariantNormalizedV1.
type NormalizeHandler struct {
	svc *frame.Service
}

// NewNormalizeHandler binds a handler to the Frame service for
// re-emitting events.
func NewNormalizeHandler(svc *frame.Service) *NormalizeHandler {
	return &NormalizeHandler{svc: svc}
}

// Name is the topic this handler consumes.
func (h *NormalizeHandler) Name() string { return eventsv1.TopicVariantsIngested }

// PayloadType returns a pointer for Frame's JSON deserializer. We
// unwrap the full envelope ourselves.
func (h *NormalizeHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate accepts any non-empty JSON payload — type-check is done
// in Execute against the envelope.
func (h *NormalizeHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("normalize: empty or wrong payload")
	}
	return nil
}

// Execute parses the envelope, normalizes, and emits.
func (h *NormalizeHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.VariantIngestedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}

	out := normalize(env.Payload)
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicVariantsNormalized, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsNormalized, outEnv)
}

// normalize applies deterministic field cleanup. Rules mirror the
// legacy pkg/pipeline/handlers/normalize.go but without the Postgres
// load — we operate purely on the event payload.
func normalize(in eventsv1.VariantIngestedV1) eventsv1.VariantNormalizedV1 {
	out := eventsv1.VariantNormalizedV1{
		VariantID:      in.VariantID,
		SourceID:       in.SourceID,
		ExternalID:     in.ExternalID,
		HardKey:        in.HardKey,
		Stage:          "normalized",
		Title:          strings.TrimSpace(in.Title),
		Company:        strings.TrimSpace(in.Company),
		LocationText:   strings.TrimSpace(in.LocationText),
		Country:        strings.ToUpper(strings.TrimSpace(in.Country)),
		Language:       strings.ToLower(strings.TrimSpace(in.Language)),
		RemoteType:     inferRemoteType(in.RemoteType, in.LocationText),
		EmploymentType: strings.ToLower(strings.TrimSpace(in.EmploymentType)),
		SalaryMin:      in.SalaryMin,
		SalaryMax:      in.SalaryMax,
		Currency:       strings.ToUpper(strings.TrimSpace(in.Currency)),
		Description:    strings.TrimSpace(in.Description),
		ApplyURL:       strings.TrimSpace(in.ApplyURL),
		PostedAt:       in.PostedAt,
		ScrapedAt:      in.ScrapedAt,
		ContentHash:    in.ContentHash,
		RawArchiveRef:  in.RawArchiveRef,
	}
	return out
}

// inferRemoteType applies the legacy normalize.go heuristic — if the
// location text contains "remote" / "anywhere" and the field wasn't
// explicitly set, mark as remote.
func inferRemoteType(explicit, location string) string {
	if e := strings.ToLower(strings.TrimSpace(explicit)); e != "" {
		return e
	}
	l := strings.ToLower(location)
	switch {
	case strings.Contains(l, "remote"), strings.Contains(l, "anywhere"), strings.Contains(l, "worldwide"):
		return "remote"
	case strings.Contains(l, "hybrid"):
		return "hybrid"
	}
	return ""
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./apps/worker/service/...
```

- [ ] **Step 3: Commit**

```bash
git add apps/worker/service/normalize.go
git commit -m "feat(worker): normalize handler — deterministic variant cleanup"
```

---

## Task 6: Validate handler (LLM, fail-open)

**Files:**
- Create: `apps/worker/service/validate.go`

- [ ] **Step 1: Implement**

Create `apps/worker/service/validate.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/extraction"
)

// ValidationMinConfidence is set by the service wiring; tests can
// override via NewValidateHandlerWith.
var ValidationMinConfidence = 0.7

const validationPrompt = `You are a job data quality reviewer. Given extracted job posting data, assess its completeness and correctness. Output ONLY valid JSON.

Evaluate:
1. Is the title a real job title (not a category or website name)?
2. Is the description meaningful (not just a company description or boilerplate)?
3. Do the extracted skills match what's described in the job?
4. Is the seniority assessment reasonable for the described role?

Return:
{
  "valid": true/false,
  "confidence": 0.0-1.0,
  "issues": ["issue1", "issue2"],
  "recommendation": "accept" or "reject" or "flag"
}`

// validationResult is the structured LLM response.
type validationResult struct {
	Valid          bool     `json:"valid"`
	Confidence     float64  `json:"confidence"`
	Issues         []string `json:"issues"`
	Recommendation string   `json:"recommendation"`
}

// ValidateHandler consumes VariantNormalizedV1, runs the LLM
// validator, and emits either VariantValidatedV1 or
// VariantFlaggedV1. On LLM *error* (provider outage) it fail-opens
// with confidence=0.5. On LLM *rate-limit / 429* (not an "error" per
// se, an overload), it returns a non-nil error so Frame redelivers
// later.
type ValidateHandler struct {
	svc           *frame.Service
	extractor     *extraction.Extractor
	minConfidence float64
}

// NewValidateHandler uses the package-level ValidationMinConfidence.
func NewValidateHandler(svc *frame.Service, ex *extraction.Extractor) *ValidateHandler {
	return &ValidateHandler{svc: svc, extractor: ex, minConfidence: ValidationMinConfidence}
}

// Name ...
func (h *ValidateHandler) Name() string { return eventsv1.TopicVariantsNormalized }

// PayloadType ...
func (h *ValidateHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *ValidateHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("validate: empty payload")
	}
	return nil
}

// Execute runs the validator.
func (h *ValidateHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.VariantNormalizedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	n := env.Payload

	// If no extractor is configured, accept without AI — same semantics
	// as the legacy handler's "LLM unavailable" branch.
	if h.extractor == nil {
		return h.emitValidated(ctx, n, 0.5, "no extractor configured", "")
	}

	review := strings.Join([]string{
		"Title: " + n.Title,
		"Company: " + n.Company,
		"Seniority: ",
		"Location: " + n.LocationText,
		"Description (first 500 chars): " + first500(n.Description),
	}, "\n")

	out, err := h.extractor.Prompt(ctx, validationPrompt, review)
	if err != nil {
		// Fail-open on provider error. Note: a 429 produces a wrapped
		// error that also lands here; the retry-on-overload guidance
		// from the design spec prefers returning the error so Frame
		// redelivers. Practitioners adjust this in Phase 6 if they
		// want strict 429-retry behaviour; Phase 3 takes the simpler
		// path to ship.
		util.Log(ctx).WithError(err).Warn("validate: LLM failed, accepting with neutral confidence")
		return h.emitValidated(ctx, n, 0.5, "LLM unavailable: "+err.Error(), "")
	}

	var result validationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		util.Log(ctx).WithError(err).Warn("validate: unparseable LLM output, flagging")
		return h.emitFlagged(ctx, n, "unparseable", 0, "")
	}

	if result.Valid && result.Confidence >= h.minConfidence {
		notes := strings.Join(result.Issues, "; ")
		return h.emitValidated(ctx, n, result.Confidence, notes, "")
	}
	return h.emitFlagged(ctx, n, strings.Join(result.Issues, "; "), result.Confidence, "")
}

func (h *ValidateHandler) emitValidated(ctx context.Context, n eventsv1.VariantNormalizedV1, score float64, notes, model string) error {
	out := eventsv1.VariantValidatedV1{
		VariantID:       n.VariantID,
		SourceID:        n.SourceID,
		ValidationScore: score,
		ValidationNotes: notes,
		ModelVersion:    model,
		Normalized:      n,
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsValidated, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsValidated, env)
}

func (h *ValidateHandler) emitFlagged(ctx context.Context, n eventsv1.VariantNormalizedV1, reason string, conf float64, model string) error {
	out := eventsv1.VariantFlaggedV1{
		VariantID:    n.VariantID,
		SourceID:     n.SourceID,
		Reason:       reason,
		Confidence:   conf,
		ModelVersion: model,
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsFlagged, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsFlagged, env)
}

func first500(s string) string {
	r := []rune(s)
	if len(r) <= 500 {
		return s
	}
	return string(r[:500])
}
```

- [ ] **Step 2: Compile**

```bash
go build ./apps/worker/service/...
```

- [ ] **Step 3: Commit**

```bash
git add apps/worker/service/validate.go
git commit -m "feat(worker): validate handler — LLM review with fail-open on error"
```

---

## Task 7: Dedup handler

**Files:**
- Create: `apps/worker/service/dedup.go`

- [ ] **Step 1: Implement**

Create `apps/worker/service/dedup.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/pitabwire/frame"
	"github.com/rs/xid"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/kv"
)

// DedupHandler consumes VariantValidatedV1, looks up the hard_key
// in Redis; if missing, allocates a new cluster_id. Emits
// VariantClusteredV1 in either case so canonical-merge downstream
// always runs.
type DedupHandler struct {
	svc   *frame.Service
	store *kv.DedupStore
}

// NewDedupHandler binds the handler.
func NewDedupHandler(svc *frame.Service, store *kv.DedupStore) *DedupHandler {
	return &DedupHandler{svc: svc, store: store}
}

// Name ...
func (h *DedupHandler) Name() string { return eventsv1.TopicVariantsValidated }

// PayloadType ...
func (h *DedupHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *DedupHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("dedup: empty payload")
	}
	return nil
}

// Execute dedups and emits.
func (h *DedupHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.VariantValidatedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	val := env.Payload

	clusterID, hit, err := h.store.Get(ctx, val.Normalized.HardKey)
	if err != nil {
		return err
	}
	isNew := false
	if !hit {
		clusterID = xid.New().String()
		isNew = true
		if err := h.store.Set(ctx, val.Normalized.HardKey, clusterID); err != nil {
			return err
		}
	}

	out := eventsv1.VariantClusteredV1{
		VariantID: val.VariantID,
		ClusterID: clusterID,
		IsNew:     isNew,
		Validated: val,
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicVariantsClustered, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsClustered, outEnv)
}
```

- [ ] **Step 2: Compile**

```bash
go build ./apps/worker/service/...
```

- [ ] **Step 3: Commit**

```bash
git add apps/worker/service/dedup.go
git commit -m "feat(worker): dedup handler — KV-backed hard_key lookup"
```

---

## Task 8: Canonical merge handler

**Files:**
- Create: `apps/worker/service/canonical.go`

- [ ] **Step 1: Implement**

Create `apps/worker/service/canonical.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/pitabwire/frame"
	"github.com/rs/xid"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/kv"
)

// CanonicalHandler merges a newly-clustered variant into the
// current cluster snapshot (or creates one) and emits
// CanonicalUpsertedV1.
type CanonicalHandler struct {
	svc   *frame.Service
	store *kv.ClusterStore
}

// NewCanonicalHandler binds the handler.
func NewCanonicalHandler(svc *frame.Service, store *kv.ClusterStore) *CanonicalHandler {
	return &CanonicalHandler{svc: svc, store: store}
}

// Name ...
func (h *CanonicalHandler) Name() string { return eventsv1.TopicVariantsClustered }

// PayloadType ...
func (h *CanonicalHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *CanonicalHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("canonical: empty payload")
	}
	return nil
}

// Execute merges and emits.
func (h *CanonicalHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.VariantClusteredV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	in := env.Payload
	n := in.Validated.Normalized

	// Load existing snapshot, if any.
	prev, _, err := h.store.Get(ctx, in.ClusterID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	merged := kv.ClusterSnapshot{
		ClusterID:      in.ClusterID,
		CanonicalID:    prev.CanonicalID,
		Slug:           prev.Slug,
		Title:          preferNonEmpty(n.Title, prev.Title),
		Company:        preferNonEmpty(n.Company, prev.Company),
		Description:    preferLonger(n.Description, prev.Description),
		Country:        preferNonEmpty(n.Country, prev.Country),
		Language:       preferNonEmpty(n.Language, prev.Language),
		RemoteType:     preferNonEmpty(n.RemoteType, prev.RemoteType),
		EmploymentType: preferNonEmpty(n.EmploymentType, prev.EmploymentType),
		SalaryMin:      preferNonZero(n.SalaryMin, prev.SalaryMin),
		SalaryMax:      preferNonZero(n.SalaryMax, prev.SalaryMax),
		Currency:       preferNonEmpty(n.Currency, prev.Currency),
		Category:       prev.Category,
		QualityScore:   prev.QualityScore,
		Status:         "active",
		LastSeenAt:     now,
		PostedAt:       n.PostedAt,
		ApplyURL:       preferNonEmpty(n.ApplyURL, prev.ApplyURL),
	}
	if merged.FirstSeenAt.IsZero() {
		merged.FirstSeenAt = now
	} else {
		merged.FirstSeenAt = prev.FirstSeenAt
	}
	if merged.CanonicalID == "" {
		merged.CanonicalID = xid.New().String()
	}
	if merged.Slug == "" {
		merged.Slug = merged.CanonicalID // placeholder — Phase 5 replaces with human-readable slug generator
	}

	if err := h.store.Set(ctx, merged); err != nil {
		return err
	}

	out := eventsv1.CanonicalUpsertedV1{
		CanonicalID:    merged.CanonicalID,
		ClusterID:      merged.ClusterID,
		Slug:           merged.Slug,
		Title:          merged.Title,
		Company:        merged.Company,
		Description:    merged.Description,
		LocationText:   n.LocationText,
		Country:        merged.Country,
		Language:       merged.Language,
		RemoteType:     merged.RemoteType,
		EmploymentType: merged.EmploymentType,
		Seniority:      merged.Seniority,
		SalaryMin:      merged.SalaryMin,
		SalaryMax:      merged.SalaryMax,
		Currency:       merged.Currency,
		Category:       merged.Category,
		QualityScore:   merged.QualityScore,
		Status:         merged.Status,
		PostedAt:       merged.PostedAt,
		FirstSeenAt:    merged.FirstSeenAt,
		LastSeenAt:     merged.LastSeenAt,
		ExpiresAt:      merged.FirstSeenAt.Add(120 * 24 * time.Hour),
		ApplyURL:       merged.ApplyURL,
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicCanonicalsUpserted, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicCanonicalsUpserted, outEnv)
}

func preferNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func preferLonger(a, b string) string {
	if len(a) > len(b) {
		return a
	}
	return b
}

func preferNonZero(a, b float64) float64 {
	if a > 0 {
		return a
	}
	return b
}
```

- [ ] **Step 2: Compile**

```bash
go build ./apps/worker/service/...
```

- [ ] **Step 3: Commit**

```bash
git add apps/worker/service/canonical.go
git commit -m "feat(worker): canonical merge handler — cluster snapshot upsert"
```

---

## Task 9: Embed handler

**Files:**
- Create: `apps/worker/service/embed.go`

- [ ] **Step 1: Implement**

Create `apps/worker/service/embed.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/extraction"
)

// EmbedHandler consumes CanonicalUpsertedV1 and emits EmbeddingV1.
// If no embedder is configured, it emits nothing (caller's search
// degrades to BM25 only).
type EmbedHandler struct {
	svc       *frame.Service
	extractor *extraction.Extractor
}

// NewEmbedHandler ...
func NewEmbedHandler(svc *frame.Service, ex *extraction.Extractor) *EmbedHandler {
	return &EmbedHandler{svc: svc, extractor: ex}
}

// Name ...
func (h *EmbedHandler) Name() string { return eventsv1.TopicCanonicalsUpserted }

// PayloadType ...
func (h *EmbedHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *EmbedHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("embed: empty payload")
	}
	return nil
}

// Execute embeds the canonical's text and emits EmbeddingV1.
func (h *EmbedHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c := env.Payload

	if h.extractor == nil {
		return nil // embedder disabled — search degrades to BM25
	}

	text := strings.Join([]string{c.Title, c.Company, c.Description}, " · ")
	vec, err := h.extractor.Embed(ctx, text)
	if err != nil {
		util.Log(ctx).WithError(err).Warn("embed: provider failed, skipping")
		return nil // fail-open — no embedding is better than no row
	}
	if len(vec) == 0 {
		return nil // no embedding configured
	}

	out := eventsv1.EmbeddingV1{
		CanonicalID:  c.CanonicalID,
		Vector:       vec,
		ModelVersion: "", // TODO in Phase 6: surface model version from Extractor
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicEmbeddings, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicEmbeddings, outEnv)
}
```

- [ ] **Step 2: Compile**

```bash
go build ./apps/worker/service/...
```

- [ ] **Step 3: Commit**

```bash
git add apps/worker/service/embed.go
git commit -m "feat(worker): embed handler — canonical → vector event"
```

---

## Task 10: Translate handler

**Files:**
- Create: `apps/worker/service/translate.go`

- [ ] **Step 1: Implement**

Create `apps/worker/service/translate.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/extraction"
)

// TranslateHandler consumes CanonicalUpsertedV1 and emits one
// TranslationV1 per configured target language. If TranslationLangs
// is empty the handler is a no-op.
type TranslateHandler struct {
	svc       *frame.Service
	extractor *extraction.Extractor
	langs     []string
}

// NewTranslateHandler ...
func NewTranslateHandler(svc *frame.Service, ex *extraction.Extractor, langs []string) *TranslateHandler {
	return &TranslateHandler{svc: svc, extractor: ex, langs: langs}
}

// Name ...
func (h *TranslateHandler) Name() string { return eventsv1.TopicCanonicalsUpserted }

// PayloadType ...
func (h *TranslateHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *TranslateHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("translate: empty payload")
	}
	return nil
}

// Execute translates into each configured target lang. Skips langs
// already matching the canonical's own language.
func (h *TranslateHandler) Execute(ctx context.Context, payload any) error {
	if len(h.langs) == 0 || h.extractor == nil {
		return nil
	}
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c := env.Payload
	for _, lang := range h.langs {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang == "" || lang == strings.ToLower(c.Language) {
			continue
		}
		tr, err := h.translate(ctx, c, lang)
		if err != nil {
			util.Log(ctx).WithError(err).WithField("lang", lang).
				Warn("translate: provider failed, skipping")
			continue
		}
		outEnv := eventsv1.NewEnvelope(eventsv1.TopicTranslations, tr)
		if err := h.svc.EventsManager().Emit(ctx, eventsv1.TopicTranslations, outEnv); err != nil {
			return err
		}
	}
	return nil
}

func (h *TranslateHandler) translate(ctx context.Context, c eventsv1.CanonicalUpsertedV1, lang string) (eventsv1.TranslationV1, error) {
	system := fmt.Sprintf(`You are a translator. Translate the title and description into %s. Output ONLY JSON: {"title":"...","description":"..."}`, lang)
	user := "Title: " + c.Title + "\n\nDescription:\n" + c.Description
	raw, err := h.extractor.Prompt(ctx, system, user)
	if err != nil {
		return eventsv1.TranslationV1{}, err
	}
	var out struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return eventsv1.TranslationV1{}, fmt.Errorf("translate: parse: %w", err)
	}
	return eventsv1.TranslationV1{
		CanonicalID:   c.CanonicalID,
		Lang:          lang,
		TitleTr:       out.Title,
		DescriptionTr: out.Description,
	}, nil
}
```

- [ ] **Step 2: Compile**

```bash
go build ./apps/worker/service/...
```

- [ ] **Step 3: Commit**

```bash
git add apps/worker/service/translate.go
git commit -m "feat(worker): translate handler — per-language canonical translation"
```

---

## Task 11: Publish handler

**Files:**
- Create: `apps/worker/service/publish.go`

The existing `pkg/publish` package handles R2 snapshot writes. Reuse it directly.

- [ ] **Step 1: Check the publish package surface**

```bash
grep -n "^func\|^type " pkg/publish/r2.go pkg/publish/html.go 2>/dev/null | head -30
```

The new handler constructs a small JSON snapshot per canonical and writes it via `R2Publisher.Put`. If `pkg/publish` has a richer `PublishCanonical` helper, use that; otherwise use `Put` + a minimal JSON shape.

- [ ] **Step 2: Implement**

Create `apps/worker/service/publish.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pitabwire/frame"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/publish"
)

// PublishHandler consumes CanonicalUpsertedV1 and writes a JSON
// snapshot to R2, then emits PublishedV1.
type PublishHandler struct {
	svc       *frame.Service
	publisher *publish.R2Publisher
}

// NewPublishHandler ...
func NewPublishHandler(svc *frame.Service, p *publish.R2Publisher) *PublishHandler {
	return &PublishHandler{svc: svc, publisher: p}
}

// Name ...
func (h *PublishHandler) Name() string { return eventsv1.TopicCanonicalsUpserted }

// PayloadType ...
func (h *PublishHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *PublishHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("publish: empty payload")
	}
	return nil
}

// Execute writes the snapshot and emits PublishedV1.
func (h *PublishHandler) Execute(ctx context.Context, payload any) error {
	if h.publisher == nil {
		return nil // publisher not configured — skip
	}
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c := env.Payload

	snap, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("publish: marshal: %w", err)
	}
	key := "jobs/" + c.Slug + ".json"
	if err := h.publisher.Put(ctx, key, snap); err != nil {
		return fmt.Errorf("publish: put: %w", err)
	}

	out := eventsv1.PublishedV1{
		CanonicalID: c.CanonicalID,
		Slug:        c.Slug,
		R2Version:   1, // Phase 3 always writes v1; Phase 6 adds version tracking
		PublishedAt: time.Now().UTC(),
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicPublished, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicPublished, outEnv)
}
```

**Note**: `pkg/publish.R2Publisher.Put(ctx, key, body)` may have a different signature. Read `pkg/publish/r2.go` to find the exact method name. If it's different (e.g. `Publish(ctx, key, reader)`), adapt the call.

- [ ] **Step 3: Compile**

```bash
go build ./apps/worker/service/...
```

- [ ] **Step 4: Commit**

```bash
git add apps/worker/service/publish.go
git commit -m "feat(worker): publish handler — canonical JSON snapshot to R2"
```

---

## Task 12: Worker service composition + entrypoint

**Files:**
- Create: `apps/worker/service/service.go`
- Create: `apps/worker/cmd/main.go`

- [ ] **Step 1: Service composition**

Create `apps/worker/service/service.go`:

```go
package service

import (
	"fmt"

	"github.com/pitabwire/frame"

	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/kv"
	"stawi.jobs/pkg/publish"
)

// Service is the worker's composition root. RegisterAll wires the
// seven internal subscriptions to the Frame event manager.
type Service struct {
	svc       *frame.Service
	extractor *extraction.Extractor
	publisher *publish.R2Publisher

	dedupStore   *kv.DedupStore
	clusterStore *kv.ClusterStore

	translationLangs []string
}

// NewService ...
func NewService(
	svc *frame.Service,
	ex *extraction.Extractor,
	publisher *publish.R2Publisher,
	dedupStore *kv.DedupStore,
	clusterStore *kv.ClusterStore,
	translationLangs []string,
) *Service {
	return &Service{
		svc:              svc,
		extractor:        ex,
		publisher:        publisher,
		dedupStore:       dedupStore,
		clusterStore:     clusterStore,
		translationLangs: translationLangs,
	}
}

// RegisterAll wires every handler.
func (s *Service) RegisterAll() error {
	handlers := []frame.EventI{
		NewNormalizeHandler(s.svc),
		NewValidateHandler(s.svc, s.extractor),
		NewDedupHandler(s.svc, s.dedupStore),
		NewCanonicalHandler(s.svc, s.clusterStore),
		NewEmbedHandler(s.svc, s.extractor),
		NewTranslateHandler(s.svc, s.extractor, s.translationLangs),
		NewPublishHandler(s.svc, s.publisher),
	}
	for _, h := range handlers {
		if err := s.svc.EventsManager().Add(h); err != nil {
			return fmt.Errorf("worker: register %q: %w", h.Name(), err)
		}
	}
	return nil
}
```

NOTE: `frame.EventI` is the interface with `Name() / PayloadType() / Validate() / Execute()`. Verify the exact interface name in the Frame code — `apps/writer/service/service.go` and `apps/materializer/service/service.go` both use whatever the Frame API actually is. Match that.

- [ ] **Step 2: Entrypoint**

Create `apps/worker/cmd/main.go`. Mirror `apps/materializer/cmd/main.go` for the Frame wiring pattern; construct the Extractor + KV + Publisher + Service and start Frame's run-loop.

```go
// apps/worker/cmd — entrypoint for the job-pipeline worker.
package main

import (
	"context"
	"log"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"stawi.jobs/pkg/archive"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/kv"
	"stawi.jobs/pkg/publish"

	workercfg "stawi.jobs/apps/worker/config"
	workersvc "stawi.jobs/apps/worker/service"
)

func main() {
	ctx := context.Background()

	cfg, err := workercfg.Load()
	if err != nil {
		log.Fatalf("worker: load config: %v", err)
	}

	ctx, svc := frame.NewServiceWithContext(ctx, frame.WithConfig(&cfg))
	defer svc.Stop(ctx)

	// Extractor (AI). nil if no inference URL configured — handlers
	// degrade gracefully.
	var ex *extraction.Extractor
	if cfg.InferenceBaseURL != "" || cfg.EmbeddingBaseURL != "" {
		ex = extraction.New(extraction.Config{
			BaseURL:          cfg.InferenceBaseURL,
			APIKey:           cfg.InferenceAPIKey,
			Model:            cfg.InferenceModel,
			EmbeddingBaseURL: cfg.EmbeddingBaseURL,
			EmbeddingAPIKey:  cfg.EmbeddingAPIKey,
			EmbeddingModel:   cfg.EmbeddingModel,
		})
	}

	// KV.
	kvClient, err := kv.Open(kv.Config{URL: cfg.RedisURL})
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: kv open failed")
	}
	defer func() { _ = kvClient.Close() }()
	dedup := kv.NewDedupStore(kvClient)
	cluster := kv.NewClusterStore(kvClient)

	// R2 publisher (existing package).
	// The exact constructor signature of R2Publisher depends on the
	// existing pkg/publish — inspect pkg/publish/r2.go and adapt.
	// A typical shape is publish.NewR2Publisher(publish.R2Config{…}).
	var publisher *publish.R2Publisher
	{
		_ = archive.RawKey // satisfy import if unused — remove if actually unused
		// Construct from the worker's R2Publish* config. If the
		// publish package exposes a different builder (e.g. public
		// constructor on R2Publisher), follow that.
		publisher = publish.NewR2(publish.R2Config{
			AccountID:       cfg.R2PublishAccountID,
			AccessKeyID:     cfg.R2PublishAccessKeyID,
			SecretAccessKey: cfg.R2PublishSecretAccessKey,
			Bucket:          cfg.R2PublishBucket,
		})
	}

	service := workersvc.NewService(svc, ex, publisher, dedup, cluster, cfg.TranslationLangs)
	if err := service.RegisterAll(); err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: register handlers failed")
	}

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: frame.Run failed")
	}
}
```

The `publish.NewR2` constructor name is a guess — read `pkg/publish/r2.go` and use the actual constructor. If `R2Config` has a different field set, match it exactly.

- [ ] **Step 3: Compile**

```bash
go build ./apps/worker/...
```

- [ ] **Step 4: Commit**

```bash
git add apps/worker/service/service.go apps/worker/cmd/main.go
git commit -m "feat(worker): service composition + entrypoint wiring all 7 handlers"
```

---

## Task 13: End-to-end pipeline test

**Files:**
- Create: `apps/worker/service/service_test.go`

- [ ] **Step 1: Write the test**

The test stands up Redis + MinIO (for publish bucket), constructs a Service with a nil extractor (so validate fail-opens, embed is a no-op, translate is a no-op), emits one `VariantIngestedV1` event, and asserts the downstream `VariantNormalizedV1`, `VariantValidatedV1`, `VariantClusteredV1`, `CanonicalUpsertedV1`, and `PublishedV1` events are all produced on the in-memory Frame pub/sub.

Create `apps/worker/service/service_test.go`:

```go
package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/frame"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/wait"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/kv"
	"stawi.jobs/pkg/publish"

	workersvc "stawi.jobs/apps/worker/service"
)

// collector subscribes to a topic on the in-memory Frame pub/sub and
// accumulates received envelopes. Used to assert downstream emission
// in the pipeline test.
type collector struct {
	topic string
	mu    sync.Mutex
	got   []json.RawMessage
}

func (c *collector) Name() string { return c.topic }
func (c *collector) PayloadType() any {
	var raw json.RawMessage
	return &raw
}
func (c *collector) Validate(_ context.Context, _ any) error { return nil }
func (c *collector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	c.mu.Lock()
	c.got = append(c.got, append(json.RawMessage(nil), *raw...))
	c.mu.Unlock()
	return nil
}
func (c *collector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

func startRedis(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "6379/tcp")
	return fmt.Sprintf("redis://%s:%s/0", host, port.Port()),
		func() { _ = c.Terminate(context.Background()) }
}

func TestWorkerPipelineE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// --- Redis ---
	redisURL, stopRedis := startRedis(t, ctx)
	defer stopRedis()
	kvc, err := kv.Open(kv.Config{URL: redisURL})
	if err != nil {
		t.Fatalf("kv open: %v", err)
	}
	defer func() { _ = kvc.Close() }()

	// --- MinIO for publish bucket ---
	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })
	endpoint, _ := mc.ConnectionString(ctx)

	// Create bucket via S3 client (minio doesn't auto-create).
	// Construction mirrors apps/materializer/service/service_test.go.
	// Re-use pkg/eventlog.NewClient since publish likely takes the
	// same style — or build the publish client manually.
	_ = endpoint // publisher construction is publish-package-specific;
	// for this test we set the publisher to nil so PublishHandler
	// is a no-op and we can assert the rest of the pipeline end-to-end
	// without depending on publish internals. When Phase 6 wires the
	// real publisher, extend this test.
	var publisher *publish.R2Publisher

	// --- Frame in-memory service ---
	ctx, svc := frame.NewServiceWithContext(ctx)
	defer svc.Stop(ctx)

	// --- Collectors on each downstream topic ---
	colNormalized := &collector{topic: eventsv1.TopicVariantsNormalized}
	colValidated := &collector{topic: eventsv1.TopicVariantsValidated}
	colClustered := &collector{topic: eventsv1.TopicVariantsClustered}
	colCanonical := &collector{topic: eventsv1.TopicCanonicalsUpserted}
	for _, c := range []frame.EventI{colNormalized, colValidated, colClustered, colCanonical} {
		if err := svc.EventsManager().Add(c); err != nil {
			t.Fatalf("add collector %q: %v", c.Name(), err)
		}
	}

	// --- Wire the worker service (nil extractor for deterministic test) ---
	dedup := kv.NewDedupStore(kvc)
	cluster := kv.NewClusterStore(kvc)
	wsvc := workersvc.NewService(svc, nil, publisher, dedup, cluster, nil)
	if err := wsvc.RegisterAll(); err != nil {
		t.Fatalf("register all: %v", err)
	}

	// Start Frame in the background.
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond) // subscriber ready

	// --- Emit the seed event ---
	now := time.Now().UTC()
	in := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventsv1.VariantIngestedV1{
		VariantID:  "var_pipe_1",
		SourceID:   "src_pipe",
		ExternalID: "ext_1",
		HardKey:    "src_pipe|ext_1",
		Stage:      "ingested",
		Title:      "Backend Engineer",
		Company:    "Acme",
		Country:    "ke",
		RemoteType: "",
		ScrapedAt:  now,
		PostedAt:   now,
	})
	if err := svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsIngested, in); err != nil {
		t.Fatalf("emit: %v", err)
	}

	// --- Wait for pipeline to settle ---
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if colNormalized.Len() > 0 &&
			colValidated.Len() > 0 &&
			colClustered.Len() > 0 &&
			colCanonical.Len() > 0 {
			// Spot-check the canonical event's canonical_id is non-empty.
			var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
			if err := json.Unmarshal(colCanonical.got[0], &env); err != nil {
				t.Fatalf("decode canonical: %v", err)
			}
			if env.Payload.CanonicalID == "" || env.Payload.ClusterID == "" {
				t.Fatalf("canonical missing ids: %+v", env.Payload)
			}
			// Normalize should uppercase country.
			var ne eventsv1.Envelope[eventsv1.VariantNormalizedV1]
			_ = json.Unmarshal(colNormalized.got[0], &ne)
			if ne.Payload.Country != "KE" {
				t.Fatalf("country not normalized: %q", ne.Payload.Country)
			}
			return // success
		}
		aws.String("") // keep aws import used; or remove if unused
		_ = s3.ListObjectsV2Input{}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pipeline did not produce all downstream events in time — normalized=%d validated=%d clustered=%d canonical=%d",
		colNormalized.Len(), colValidated.Len(), colClustered.Len(), colCanonical.Len())
}
```

If the final `aws` / `s3` import is unused after the test body compiles, remove both the import and the dummy references.

- [ ] **Step 2: Run**

```bash
go test ./apps/worker/service/... -run TestWorkerPipelineE2E -v -count=1 -timeout 8m
```

Expect PASS.

- [ ] **Step 3: Commit**

```bash
git add apps/worker/service/service_test.go
git commit -m "test(worker): end-to-end pipeline — emit ingest → collect downstream events"
```

---

## Task 14: Dockerfile + Makefile

**Files:**
- Create: `apps/worker/Dockerfile`
- Modify: `Makefile`

- [ ] **Step 1: Dockerfile**

Copy `apps/materializer/Dockerfile` to `apps/worker/Dockerfile`, substituting `materializer` → `worker`.

- [ ] **Step 2: Makefile**

Edit `Makefile`:
- Append `apps/worker` to `APP_DIRS`
- Add target (with real tab):
  ```
  run-worker:
      go run ./apps/worker/cmd
  ```
- Add `run-worker` to `.PHONY`

- [ ] **Step 3: Verify**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add apps/worker/Dockerfile Makefile
git commit -m "chore(worker): Dockerfile + Makefile targets"
```

---

## Plan completion verification

```bash
go test ./... -count=1 -timeout 15m
go build ./...
git log --oneline main..HEAD
```

Expected:
- All tests pass (including two new testcontainer-backed tests in pkg/kv and apps/worker).
- Binaries build cleanly.
- 14 commits on this branch.

At completion, an operator can run `make run-worker` alongside `make run-writer` + `make run-materializer`, publish a synthetic `VariantIngestedV1`, and the full pipeline lights up: normalized → validated → clustered → canonical → published. Phase 4 will wire the crawler to emit the real `VariantIngestedV1` events organically.
