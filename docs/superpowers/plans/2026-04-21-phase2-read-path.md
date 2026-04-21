# Phase 2 — Read Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the read path — Manticore provisioning, an `apps/materializer` service that consumes Parquet from R2 and keeps Manticore in sync, and a new `/api/v2/search` endpoint on `apps/api` that reads from Manticore. End state: a manually-published `CanonicalUpsertedV1` event lands in the Parquet log (Phase 1), gets materialized into Manticore (Phase 2), and is returned by the new search endpoint.

**Architecture:** Materializer is a disposable pod; it polls R2 every 15 s for new Parquet objects in tracked partition prefixes, downloads each one, decodes with `pkg/eventlog.ReadParquet`, and issues `REPLACE INTO` against Manticore's RT index using the event's `canonical_id` as the primary key (idempotent upsert). Watermark per partition prefix is stored in a tiny Postgres table so restarts resume cleanly. Manticore `idx_jobs_rt` matches the design doc schema (FTS + attributes + HNSW vector attribute). The API gains `/api/v2/search` next to the existing `/api/search` so Phase 6's cutover is a routing flip, not a rewrite.

**Tech stack:**
- Go 1.26, Frame (`github.com/pitabwire/frame`), `pitabwire/util` logging
- Manticore Search 6.x (Docker image `manticoresearch/manticore:6.3.2` or later) via its **HTTP JSON API** on port 9308 — no MySQL driver, stdlib `net/http` only
- `github.com/parquet-go/parquet-go` (existing, from Phase 1)
- `github.com/aws/aws-sdk-go-v2/service/s3` (existing)
- `testcontainers-go/modules/minio` (existing) + a direct `testcontainers-go` wrapper for Manticore exposing port 9308
- Postgres via GORM for watermark table (existing)

**What's in this plan:**
- Extend `pkg/events/v1/` with `CanonicalUpsertedV1` and `EmbeddingV1` payload types
- Extend `apps/writer/service/service.go` `uploadBatch` encoder switch to cover the new event types
- `pkg/searchindex/` — Manticore client wrapper + DDL + schema provisioning helper
- `pkg/eventlog/reader.go` — list + download + decode helpers for the materializer
- `pkg/repository/materializer_watermark.go` + Postgres migration — per-prefix watermark tracking
- `apps/materializer/` — new service (config, service, entrypoint, Dockerfile, Makefile target)
- `apps/api/cmd/search_v2.go` — new `GET /api/v2/search` handler backed by Manticore
- Docker-compose update — add Manticore container
- Integration tests: publish canonical → writer → R2 → materializer → Manticore → API v2 search returns result

**What's NOT in this plan:**
- Compaction / `canonicals_current/` rebuild — Phase 6 ops
- KV integration — Phase 3 (dedup + rerank cache)
- Candidate Manticore index (`idx_candidates_rt`) — v1.1
- Full-filter parity with `/api/search` — Phase 2 ships q + country + remote_type + category + limit, enough to validate the path end-to-end. Salary ranges, sorts, tiered feed stay on the legacy Postgres path until Phase 6.
- Embedding vector search (KNN queries) — schema includes the vector attribute, and the materializer writes vectors when it sees embedding events, but `/api/v2/search` does BM25 + filter only. Hybrid BM25+KNN lands in Phase 3 alongside the worker pipeline.

---

## File structure

**Create:**

| File | Responsibility |
|---|---|
| `pkg/events/v1/canonicals.go` | `CanonicalUpsertedV1`, `EmbeddingV1` payload structs |
| `pkg/eventlog/reader.go` | R2 list + download + Parquet decode helpers used by materializer |
| `pkg/searchindex/manticore.go` | MySQL-protocol client wrapper (connect, exec DDL, REPLACE INTO, SELECT) |
| `pkg/searchindex/schema.go` | `idx_jobs_rt` DDL + `Apply(db)` function |
| `pkg/searchindex/schema_test.go` | Idempotent DDL apply test |
| `db/migrations/0002_materializer_watermark.sql` | `materializer_watermarks` table |
| `pkg/repository/materializer_watermark.go` | `WatermarkRepository` with `Get/Set` |
| `apps/materializer/config/config.go` | Env-backed config (R2, Manticore URL, Postgres DSN, poll interval) |
| `apps/materializer/service/indexer.go` | Per-collection upsert — translates Parquet rows → Manticore `REPLACE` statements |
| `apps/materializer/service/service.go` | Poll loop: list new files → download → decode → indexer.Apply → advance watermark |
| `apps/materializer/service/service_test.go` | End-to-end: seed R2 with a canonical Parquet → wait for poll → assert Manticore has it |
| `apps/materializer/cmd/main.go` | Entrypoint |
| `apps/materializer/Dockerfile` | Multi-stage build mirror of `apps/writer/Dockerfile` |
| `apps/api/cmd/search_v2.go` | `GET /api/v2/search` handler backed by Manticore |
| `apps/api/cmd/search_v2_test.go` | HTTP-level test using a Manticore testcontainer |

**Modify:**

| File | Change |
|---|---|
| `apps/writer/service/service.go` | Extend `uploadBatch` switch to cover `TopicCanonicalsUpserted` and `TopicEmbeddings` |
| `apps/writer/service/handler.go` | Already covers the new topics via `extractHint` (cluster_id / canonical_id); confirm |
| `apps/api/cmd/main.go` | Register `/api/v2/search` route + wire Manticore client |
| `Makefile` | Add `apps/materializer` to `APP_DIRS`, add `run-materializer` target |
| `deploy/docker-compose.yml` | Add Manticore service |
| `go.mod` / `go.sum` | No new deps — Manticore client uses stdlib `net/http` |

---

## Task 1: Add new event payload types

**Files:**
- Create: `pkg/events/v1/canonicals.go`
- Modify: `pkg/events/v1/envelope_test.go` (one new test)

- [ ] **Step 1: Write the failing test**

Append to `pkg/events/v1/envelope_test.go`:

```go
func TestCanonicalUpsertedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCanonicalsUpserted, CanonicalUpsertedV1{
		CanonicalID: "can_1",
		ClusterID:   "clu_1",
		Slug:        "senior-backend-engineer-acme-ke",
		Title:       "Senior Backend Engineer",
		Company:     "Acme",
		Country:     "KE",
		RemoteType:  "remote",
		Status:      "active",
		PostedAt:    time.Date(2026, 4, 21, 9, 0, 0, 0, time.UTC),
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[CanonicalUpsertedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CanonicalID != "can_1" || back.Payload.Title != "Senior Backend Engineer" {
		t.Fatalf("round-trip lost fields: %+v", back.Payload)
	}
}

func TestEmbeddingRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicEmbeddings, EmbeddingV1{
		CanonicalID:  "can_1",
		Vector:       []float32{0.1, 0.2, 0.3},
		ModelVersion: "text-embed-3-small",
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[EmbeddingV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CanonicalID != "can_1" || len(back.Payload.Vector) != 3 {
		t.Fatalf("round-trip lost fields: %+v", back.Payload)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
cd /home/j/code/stawi.jobs
go test ./pkg/events/v1/...
```

Expected: build error — `undefined: CanonicalUpsertedV1, EmbeddingV1`.

- [ ] **Step 3: Implement the types**

Create `pkg/events/v1/canonicals.go`:

```go
package eventsv1

import "time"

// CanonicalUpsertedV1 is the event emitted by the canonical-merge
// stage once a cluster of variants has been merged into a single
// user-facing job row. Phase 2 consumes this event in the materializer
// to populate idx_jobs_rt — the Manticore index that backs search,
// browse, and detail.
//
// Fields mirror the design doc (§5.2 canonicals partition). Phase 2
// ships a minimum-viable set sufficient for BM25 search + country /
// remote_type / category filters + posted_at sort. Phases 3/4 add
// the extended intelligence fields (skills, required_skills, benefits,
// translated_langs, etc.) when the worker pipeline starts emitting
// them.
type CanonicalUpsertedV1 struct {
	CanonicalID    string    `json:"canonical_id"    parquet:"canonical_id"`
	ClusterID      string    `json:"cluster_id"      parquet:"cluster_id"`
	Slug           string    `json:"slug"            parquet:"slug"`
	Title          string    `json:"title"           parquet:"title,optional"`
	Company        string    `json:"company"         parquet:"company,optional"`
	Description    string    `json:"description"     parquet:"description,optional"`
	LocationText   string    `json:"location_text"   parquet:"location_text,optional"`
	Country        string    `json:"country"         parquet:"country,optional"`
	Language       string    `json:"language"        parquet:"language,optional"`
	RemoteType     string    `json:"remote_type"     parquet:"remote_type,optional"`
	EmploymentType string    `json:"employment_type" parquet:"employment_type,optional"`
	Seniority      string    `json:"seniority"       parquet:"seniority,optional"`
	SalaryMin      float64   `json:"salary_min"      parquet:"salary_min,optional"`
	SalaryMax      float64   `json:"salary_max"      parquet:"salary_max,optional"`
	Currency       string    `json:"currency"        parquet:"currency,optional"`
	Category       string    `json:"category"        parquet:"category,optional"`
	QualityScore   float64   `json:"quality_score"   parquet:"quality_score,optional"`
	Status         string    `json:"status"          parquet:"status"`
	PostedAt       time.Time `json:"posted_at"       parquet:"posted_at,optional"`
	FirstSeenAt    time.Time `json:"first_seen_at"   parquet:"first_seen_at,optional"`
	LastSeenAt     time.Time `json:"last_seen_at"    parquet:"last_seen_at,optional"`
	ExpiresAt      time.Time `json:"expires_at"      parquet:"expires_at,optional"`
	ApplyURL       string    `json:"apply_url"       parquet:"apply_url,optional"`
}

// EmbeddingV1 is the event emitted by the embedder stage once a
// canonical job's semantic vector has been computed. Materializer
// updates the `embedding` HNSW attribute on idx_jobs_rt; Phase 3+
// adds hybrid BM25+KNN queries to /api/v2/search.
type EmbeddingV1 struct {
	CanonicalID  string    `json:"canonical_id"  parquet:"canonical_id"`
	Vector       []float32 `json:"vector"        parquet:"vector"`
	ModelVersion string    `json:"model_version" parquet:"model_version"`
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./pkg/events/v1/...
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add pkg/events/v1/canonicals.go pkg/events/v1/envelope_test.go
git commit -m "feat(events): add CanonicalUpsertedV1 + EmbeddingV1 payload types"
```

---

## Task 2: Extend writer encoder to cover new event types

**Files:**
- Modify: `apps/writer/service/service.go:uploadBatch` switch

- [ ] **Step 1: Add cases to the switch**

In `apps/writer/service/service.go`, the `uploadBatch` function has a switch on `b.EventType`. Add cases for the two new topics. Open the file and find the existing switch:

```go
switch b.EventType {
case eventsv1.TopicVariantsIngested:
    body, err = encodeBatch[eventsv1.VariantIngestedV1](b.Events)
default:
    return fmt.Errorf("writer: no encoder registered for %q", b.EventType)
}
```

Replace with:

```go
switch b.EventType {
case eventsv1.TopicVariantsIngested:
    body, err = encodeBatch[eventsv1.VariantIngestedV1](b.Events)
case eventsv1.TopicCanonicalsUpserted:
    body, err = encodeBatch[eventsv1.CanonicalUpsertedV1](b.Events)
case eventsv1.TopicEmbeddings:
    body, err = encodeBatch[eventsv1.EmbeddingV1](b.Events)
default:
    return fmt.Errorf("writer: no encoder registered for %q", b.EventType)
}
```

- [ ] **Step 2: Verify writer tests still pass**

```bash
go test ./apps/writer/... -v -count=1 -timeout 5m
```

Expected: all tests pass (including the Phase 1 E2E test).

- [ ] **Step 3: Commit**

```bash
git add apps/writer/service/service.go
git commit -m "feat(writer): encode canonicals + embeddings Parquet partitions"
```

---

## Task 3: No new dependency — use Manticore HTTP JSON API

**Files:** none (design choice only).

Manticore exposes a JSON HTTP API on port 9308 that covers everything this plan needs: `/sql` for DDL, `/replace` + `/update` for upserts, `/search` for queries. Using stdlib `net/http` avoids pulling the MySQL driver (and avoids the MVS issues that come with it dragging in gocloud.dev transitive bumps).

No commit for this task — it's a decision. Task 4 implements the HTTP client directly.

---

## Task 4: Manticore HTTP client wrapper

**Files:**
- Create: `pkg/searchindex/manticore.go`

- [ ] **Step 1: Implement the HTTP client**

Create `pkg/searchindex/manticore.go`:

```go
// Package searchindex is a narrow HTTP client for Manticore Search.
// Manticore exposes a JSON API on port 9308 — we use /sql for DDL
// and introspection, /replace + /update for row upserts, and /search
// for queries. Using the HTTP API (rather than the MySQL wire
// protocol) keeps the dependency footprint to stdlib net/http.
package searchindex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config describes how to reach Manticore.
type Config struct {
	// URL is the root of Manticore's HTTP endpoint,
	// e.g. "http://manticore:9308". Trailing slashes are trimmed.
	URL string

	// Timeout bounds every request. Default 10s.
	Timeout time.Duration
}

// Client is the high-level Manticore handle. Safe for concurrent use
// (delegates to http.Client which is goroutine-safe).
type Client struct {
	http *http.Client
	base string
}

// Open constructs a Client. No network I/O — call Ping to verify
// reachability.
func Open(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("searchindex: empty URL")
	}
	t := cfg.Timeout
	if t == 0 {
		t = 10 * time.Second
	}
	return &Client{
		http: &http.Client{Timeout: t},
		base: strings.TrimRight(cfg.URL, "/"),
	}, nil
}

// Close is a no-op for the HTTP client but kept for symmetry with
// other resource-owning packages.
func (c *Client) Close() error { return nil }

// Ping checks that Manticore is reachable. Uses the /sql endpoint
// with a cheap SHOW STATUS query because there's no universally-
// available dedicated ping endpoint across Manticore versions.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.SQL(ctx, "SHOW STATUS")
	return err
}

// SQL runs an arbitrary SQL statement via /sql?mode=raw. Used for
// DDL (CREATE TABLE, DROP TABLE) and introspection (SHOW TABLES).
// Returns the raw response body — callers parse as needed.
func (c *Client) SQL(ctx context.Context, stmt string) ([]byte, error) {
	body := strings.NewReader("query=" + url.QueryEscape(stmt))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/sql?mode=raw", body)
	if err != nil {
		return nil, fmt.Errorf("searchindex: sql req: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searchindex: sql send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("searchindex: sql status %d: %s", resp.StatusCode, string(out))
	}
	// Manticore sometimes returns 200 with a JSON error payload like
	// [{"error":"table idx_jobs_rt already exists"}] — treat that
	// as an error so callers can detect it.
	if bytes.Contains(out, []byte(`"error"`)) {
		return out, fmt.Errorf("searchindex: sql error: %s", strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Replace inserts or replaces a document by id. Uses /replace JSON.
// Manticore treats replace as upsert; providing a stable id makes
// the operation idempotent.
func (c *Client) Replace(ctx context.Context, index string, id uint64, doc map[string]any) error {
	return c.postJSON(ctx, "/replace", map[string]any{
		"index": index,
		"id":    id,
		"doc":   doc,
	})
}

// Update patches named fields on an existing document. Uses /update.
// A missing id is a no-op (Manticore returns {updated: 0}).
func (c *Client) Update(ctx context.Context, index string, id uint64, doc map[string]any) error {
	return c.postJSON(ctx, "/update", map[string]any{
		"index": index,
		"id":    id,
		"doc":   doc,
	})
}

// Search issues a JSON query against /search. Returns the raw
// response body — callers parse hits as needed (shape documented
// at https://manual.manticoresearch.com/Searching/Intro).
func (c *Client) Search(ctx context.Context, query map[string]any) ([]byte, error) {
	raw, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("searchindex: search marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/search", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("searchindex: search req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searchindex: search send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("searchindex: search status %d: %s", resp.StatusCode, string(out))
	}
	return out, nil
}

func (c *Client) postJSON(ctx context.Context, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("searchindex: marshal %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("searchindex: req %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("searchindex: send %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("searchindex: %s status %d: %s", path, resp.StatusCode, string(msg))
	}
	return nil
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./pkg/searchindex/...
```

- [ ] **Step 3: Commit**

```bash
git add pkg/searchindex/manticore.go
git commit -m "feat(searchindex): Manticore HTTP client (no MySQL driver)"
```

---

## Task 5: Manticore `idx_jobs_rt` schema + idempotent apply

**Files:**
- Create: `pkg/searchindex/schema.go`
- Create: `pkg/searchindex/schema_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/searchindex/schema_test.go`:

```go
package searchindex_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"stawi.jobs/pkg/searchindex"
)

// startManticore boots a Manticore container and returns the HTTP
// URL (e.g. "http://127.0.0.1:42719") + a stop function.
func startManticore(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "manticoresearch/manticore:6.3.2",
		ExposedPorts: []string{"9308/tcp"},
		WaitingFor:   wait.ForListeningPort("9308/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start manticore: %v", err)
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "9308/tcp")
	url := fmt.Sprintf("http://%s:%s", host, port.Port())
	return url, func() { _ = c.Terminate(context.Background()) }
}

func TestApplySchemaIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	url, stop := startManticore(t, ctx)
	defer stop()

	client, err := searchindex.Open(searchindex.Config{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if err := searchindex.Apply(ctx, client); err != nil {
		t.Fatalf("apply #1: %v", err)
	}
	if err := searchindex.Apply(ctx, client); err != nil {
		t.Fatalf("apply #2 (must be idempotent): %v", err)
	}

	// Confirm the table exists by asking SHOW TABLES via /sql.
	raw, err := client.SQL(ctx, "SHOW TABLES")
	if err != nil {
		t.Fatalf("show tables: %v", err)
	}
	if !strings.Contains(string(raw), "idx_jobs_rt") {
		t.Fatalf("idx_jobs_rt not present after Apply — SHOW TABLES returned:\n%s", string(raw))
	}
}
```

- [ ] **Step 2: Run test, expect failure**

```bash
go test ./pkg/searchindex/... -v -count=1 -timeout 3m
```

Expected: build error — `undefined: searchindex.Apply`.

- [ ] **Step 3: Implement `Apply`**

Create `pkg/searchindex/schema.go`:

```go
package searchindex

import (
	"context"
	"strings"
)

// idxJobsRTDDL is the schema for the primary job search index.
// Embedding dimension is pinned to 1536 (OpenAI text-embedding-3-small)
// for Phase 2; changing it is a rebuild, not a migration. Phase 6 will
// pin this via config and rebuild protocol.
//
// Single-line form — Manticore's /sql endpoint accepts newlines, but
// keeping it one line makes query-string escaping trivial.
const idxJobsRTDDL = `CREATE TABLE idx_jobs_rt (canonical_id string attribute, slug string attribute, title text indexed, company text indexed, description text indexed stored, location_text text indexed, category string attribute, country string attribute, language string attribute, remote_type string attribute, employment_type string attribute, seniority string attribute, salary_min uint, salary_max uint, currency string attribute, quality_score float, is_featured bool, posted_at timestamp, last_seen_at timestamp, expires_at timestamp, status string attribute, embedding float_vector knn_type='hnsw' knn_dims='1536' hnsw_similarity='COSINE', embedding_model string attribute)`

// Apply creates idx_jobs_rt if it does not exist. Idempotent — on
// re-run it swallows Manticore's "table already exists" error which
// the HTTP /sql endpoint returns as an error JSON payload.
func Apply(ctx context.Context, c *Client) error {
	_, err := c.SQL(ctx, idxJobsRTDDL)
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return nil
	}
	return err
}
```

- [ ] **Step 4: Run test to verify pass**

```bash
go test ./pkg/searchindex/... -v -count=1 -timeout 3m
```

Expected: `PASS` (takes ~30 s on first run while the Manticore image downloads).

- [ ] **Step 5: Commit**

```bash
git add pkg/searchindex/schema.go pkg/searchindex/schema_test.go
git commit -m "feat(searchindex): idx_jobs_rt DDL + idempotent Apply"
```

---

## Task 6: Add Manticore to docker-compose

**Files:**
- Modify: `deploy/docker-compose.yml`

- [ ] **Step 1: Add the service**

Open `deploy/docker-compose.yml`. Add the following service (preserving any existing formatting conventions in the file):

```yaml
  manticore:
    image: manticoresearch/manticore:6.3.2
    restart: unless-stopped
    ports:
      - "9306:9306"   # MySQL SQL protocol
      - "9308:9308"   # HTTP / JSON API
    environment:
      - EXTRA=1
    volumes:
      - manticore-data:/var/lib/manticore
```

And add `manticore-data:` under the top-level `volumes:` key (create that section if it doesn't exist).

- [ ] **Step 2: Verify parses**

```bash
docker compose -f deploy/docker-compose.yml config >/dev/null
```

Expected: exit 0 (config valid).

- [ ] **Step 3: Commit**

```bash
git add deploy/docker-compose.yml
git commit -m "chore(deploy): add Manticore service for local dev"
```

---

## Task 7: Watermark Postgres table

**Files:**
- Create: `db/migrations/0002_materializer_watermark.sql`

- [ ] **Step 1: Write the migration**

Check the existing `db/migrations/0001_init.sql` to match the repo's migration style (schema conventions, if any). Then create `db/migrations/0002_materializer_watermark.sql`:

```sql
-- Per-prefix watermark for apps/materializer. One row per
-- collection prefix (e.g. "canonicals", "embeddings"). last_r2_key
-- is the most-recently-processed R2 object key; subsequent polls
-- use it as ListObjectsV2 StartAfter.
--
-- Small table, low write volume — one UPDATE per poll tick per
-- collection. Postgres is fine; a future phase may move this to
-- the KV for crisper ops bundling.

CREATE TABLE IF NOT EXISTS materializer_watermarks (
    prefix        text PRIMARY KEY,
    last_r2_key   text NOT NULL DEFAULT '',
    updated_at    timestamptz NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Commit**

```bash
git add db/migrations/0002_materializer_watermark.sql
git commit -m "feat(db): migration for materializer_watermarks table"
```

---

## Task 8: Watermark repository

**Files:**
- Create: `pkg/repository/materializer_watermark.go`

- [ ] **Step 1: Implement the repository**

Create `pkg/repository/materializer_watermark.go`, following the existing repository pattern (see `pkg/repository/job.go` for how `db func(ctx, readOnly) *gorm.DB` is wired):

```go
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaterializerWatermark holds the most-recent R2 object key the
// materializer has applied to Manticore for a given collection prefix.
// Stored in Postgres for simplicity; a future phase may migrate to KV.
type MaterializerWatermark struct {
	Prefix     string    `gorm:"type:text;primaryKey"       json:"prefix"`
	LastR2Key  string    `gorm:"type:text;not null;default:''" json:"last_r2_key"`
	UpdatedAt  time.Time `gorm:"not null;default:now()"     json:"updated_at"`
}

// TableName pins to the migration's table name (plural).
func (MaterializerWatermark) TableName() string { return "materializer_watermarks" }

// WatermarkRepository persists materializer progress per collection prefix.
type WatermarkRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewWatermarkRepository constructs a WatermarkRepository.
func NewWatermarkRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *WatermarkRepository {
	return &WatermarkRepository{db: db}
}

// Get returns the last-applied R2 key for prefix, or "" if no row
// exists yet (first-boot path).
func (r *WatermarkRepository) Get(ctx context.Context, prefix string) (string, error) {
	var w MaterializerWatermark
	err := r.db(ctx, true).Where("prefix = ?", prefix).First(&w).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	return w.LastR2Key, nil
}

// Set advances the watermark. Upserts on conflict — safe to call
// from one materializer pod at a time (which is the expected v1
// deployment; concurrent advancement requires a different strategy).
func (r *WatermarkRepository) Set(ctx context.Context, prefix, r2Key string) error {
	w := MaterializerWatermark{Prefix: prefix, LastR2Key: r2Key, UpdatedAt: time.Now()}
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "prefix"}},
			DoUpdates: clause.AssignmentColumns([]string{"last_r2_key", "updated_at"}),
		}).
		Create(&w).Error
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./pkg/repository/...
```

- [ ] **Step 3: Commit**

```bash
git add pkg/repository/materializer_watermark.go
git commit -m "feat(repository): WatermarkRepository for materializer progress"
```

---

## Task 9: R2 reader helpers for the materializer

**Files:**
- Create: `pkg/eventlog/reader.go`

- [ ] **Step 1: Implement the reader**

Create `pkg/eventlog/reader.go`:

```go
package eventlog

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Reader lists + downloads R2 objects under a given prefix. Kept
// separate from Uploader so tests can stub each side independently.
type Reader struct {
	client *s3.Client
	bucket string
}

// NewReader wraps an S3 client + bucket for list/get.
func NewReader(client *s3.Client, bucket string) *Reader {
	return &Reader{client: client, bucket: bucket}
}

// ListNewObjects returns object keys under `prefix` that sort after
// `startAfter`. Limit caps the batch size so one poll tick can't
// hog the materializer for an unbounded duration on cold starts.
// Results are sorted lexicographically (R2 returns them that way).
func (r *Reader) ListNewObjects(ctx context.Context, prefix, startAfter string, limit int32) ([]s3types.Object, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(r.bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(limit),
	}
	if startAfter != "" {
		input.StartAfter = aws.String(startAfter)
	}
	out, err := r.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("eventlog: list %q: %w", prefix, err)
	}
	return out.Contents, nil
}

// Get fetches an object's bytes. Parses nothing — callers use
// ReadParquet on the body.
func (r *Reader) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("eventlog: get %q: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, out.Body); err != nil {
		return nil, fmt.Errorf("eventlog: read %q: %w", key, err)
	}
	return buf.Bytes(), nil
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./pkg/eventlog/...
```

- [ ] **Step 3: Commit**

```bash
git add pkg/eventlog/reader.go
git commit -m "feat(eventlog): R2 reader for materializer list + download"
```

---

## Task 10: Materializer config

**Files:**
- Create: `apps/materializer/config/config.go`
- Prereq: `mkdir -p apps/materializer/config apps/materializer/cmd apps/materializer/service`

- [ ] **Step 1: Create the directories**

```bash
mkdir -p apps/materializer/config apps/materializer/cmd apps/materializer/service
```

- [ ] **Step 2: Implement config**

Create `apps/materializer/config/config.go`. Mirror `apps/writer/config/config.go` for Frame embedding + env-parsing style:

```go
// Package config loads apps/materializer runtime configuration from
// environment variables. Same pattern as apps/writer/config.
package config

import (
	"time"

	"github.com/caarlos0/env/v11"
	fconfig "github.com/pitabwire/frame/config"
)

// Config wires Frame defaults (DB + NATS + OTEL) plus Manticore URL
// and materializer-specific knobs.
type Config struct {
	fconfig.ConfigurationDefault

	// R2 / S3-compatible event log bucket (reader side).
	R2AccountID       string `env:"R2_LOG_ACCOUNT_ID,required"`
	R2AccessKeyID     string `env:"R2_LOG_ACCESS_KEY_ID,required"`
	R2SecretAccessKey string `env:"R2_LOG_SECRET_ACCESS_KEY,required"`
	R2Bucket          string `env:"R2_LOG_BUCKET,required"`
	R2Endpoint        string `env:"R2_LOG_ENDPOINT" envDefault:""`
	R2UsePathStyle    bool   `env:"R2_LOG_PATH_STYLE" envDefault:"false"`

	// Manticore HTTP JSON endpoint, e.g. "http://manticore:9308".
	ManticoreURL     string        `env:"MANTICORE_URL,required"`
	ManticoreTimeout time.Duration `env:"MANTICORE_TIMEOUT" envDefault:"10s"`

	// Polling cadence + batch cap.
	PollInterval  time.Duration `env:"MATERIALIZER_POLL_INTERVAL" envDefault:"15s"`
	ListBatchSize int32         `env:"MATERIALIZER_LIST_BATCH"   envDefault:"100"`

	// Partition prefixes to track. Pipe-separated list; Phase 2 covers
	// canonicals + embeddings. Later phases extend this.
	Prefixes []string `env:"MATERIALIZER_PREFIXES" envSeparator:"|" envDefault:"canonicals/|embeddings/"`
}

// Load parses env → Config.
func Load() (Config, error) {
	c := Config{}
	if err := env.Parse(&c); err != nil {
		return c, err
	}
	return c, nil
}
```

- [ ] **Step 3: Confirm compiles**

```bash
go build ./apps/materializer/config/...
```

- [ ] **Step 4: Commit**

```bash
git add apps/materializer/config/config.go
git commit -m "feat(materializer): env-backed Config with poll + prefix settings"
```

---

## Task 11: Indexer — translate Parquet rows to Manticore upserts

**Files:**
- Create: `apps/materializer/service/indexer.go`

- [ ] **Step 1: Implement**

Create `apps/materializer/service/indexer.go`:

```go
package service

import (
	"context"
	"fmt"
	"hash/fnv"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/searchindex"
)

// Indexer converts Parquet-decoded event rows into Manticore writes
// via the HTTP JSON API. Each public method corresponds to one
// collection (canonicals, embeddings). Methods are idempotent:
// using the same canonical_id → hashID as the Manticore row id
// makes /replace act as upsert.
type Indexer struct {
	client *searchindex.Client
}

// NewIndexer wraps a Manticore client.
func NewIndexer(c *searchindex.Client) *Indexer { return &Indexer{client: c} }

// ApplyCanonicalsParquet decodes a Parquet body of CanonicalUpsertedV1
// rows and issues one /replace per row.
func (i *Indexer) ApplyCanonicalsParquet(ctx context.Context, body []byte) (int, error) {
	rows, err := eventlog.ReadParquet[eventsv1.CanonicalUpsertedV1](body)
	if err != nil {
		return 0, fmt.Errorf("indexer: decode canonicals parquet: %w", err)
	}
	n := 0
	for _, r := range rows {
		if err := i.replaceOne(ctx, r); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// replaceOne issues a single /replace against idx_jobs_rt. The row
// id is a stable hash of canonical_id — Manticore requires a bigint
// pk and keying on hash(canonical_id) gives us idempotent upsert.
func (i *Indexer) replaceOne(ctx context.Context, r eventsv1.CanonicalUpsertedV1) error {
	doc := map[string]any{
		"canonical_id":    r.CanonicalID,
		"slug":            r.Slug,
		"title":           r.Title,
		"company":         r.Company,
		"description":     r.Description,
		"location_text":   r.LocationText,
		"category":        r.Category,
		"country":         r.Country,
		"language":        r.Language,
		"remote_type":     r.RemoteType,
		"employment_type": r.EmploymentType,
		"seniority":       r.Seniority,
		"salary_min":      uint64(r.SalaryMin),
		"salary_max":      uint64(r.SalaryMax),
		"currency":        r.Currency,
		"quality_score":   float32(r.QualityScore),
		"is_featured":     r.QualityScore >= 80,
		"posted_at":       r.PostedAt.Unix(),
		"last_seen_at":    r.LastSeenAt.Unix(),
		"expires_at":      r.ExpiresAt.Unix(),
		"status":          r.Status,
	}
	return i.client.Replace(ctx, "idx_jobs_rt", hashID(r.CanonicalID), doc)
}

// ApplyEmbeddingsParquet updates the `embedding` attribute on
// idx_jobs_rt rows by canonical_id. If the row doesn't exist yet
// (embeddings can arrive slightly ahead of canonical for a fresh
// job), Manticore's /update returns updated=0 and the call is
// a no-op — the next canonical event will land the base row, and
// the next embedding event for that canonical will re-apply.
func (i *Indexer) ApplyEmbeddingsParquet(ctx context.Context, body []byte) (int, error) {
	rows, err := eventlog.ReadParquet[eventsv1.EmbeddingV1](body)
	if err != nil {
		return 0, fmt.Errorf("indexer: decode embeddings parquet: %w", err)
	}
	n := 0
	for _, r := range rows {
		id := hashID(r.CanonicalID)
		doc := map[string]any{
			"embedding":       r.Vector,
			"embedding_model": r.ModelVersion,
		}
		if err := i.client.Update(ctx, "idx_jobs_rt", id, doc); err != nil {
			return n, fmt.Errorf("indexer: update embedding: %w", err)
		}
		n++
	}
	return n, nil
}

// hashID maps a canonical_id (xid) to a stable bigint for Manticore's
// primary key.
func hashID(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./apps/materializer/service/...
```

- [ ] **Step 3: Commit**

```bash
git add apps/materializer/service/indexer.go
git commit -m "feat(materializer): Indexer upserts canonicals + embeddings into Manticore"
```

---

## Task 12: Materializer service — poll loop

**Files:**
- Create: `apps/materializer/service/service.go`

- [ ] **Step 1: Implement the poll loop**

Create `apps/materializer/service/service.go`:

```go
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/repository"
)

// Service drives the materializer's main poll loop. Each tick it
// asks R2 for new objects under each tracked prefix (since last
// watermark), downloads + applies them to Manticore, and advances
// the watermark on success.
type Service struct {
	reader       *eventlog.Reader
	indexer      *Indexer
	watermarks   *repository.WatermarkRepository
	prefixes     []string
	pollInterval time.Duration
	listBatch    int32
}

// NewService assembles the materializer.
func NewService(
	reader *eventlog.Reader,
	indexer *Indexer,
	watermarks *repository.WatermarkRepository,
	prefixes []string,
	pollInterval time.Duration,
	listBatch int32,
) *Service {
	return &Service{
		reader:       reader,
		indexer:      indexer,
		watermarks:   watermarks,
		prefixes:     prefixes,
		pollInterval: pollInterval,
		listBatch:    listBatch,
	}
}

// Run polls until ctx is cancelled. Errors are logged + swallowed so
// one broken prefix doesn't stall the others.
func (s *Service) Run(ctx context.Context) error {
	t := time.NewTicker(s.pollInterval)
	defer t.Stop()

	// First tick fires immediately — skips one idle interval on boot.
	if err := s.pollOnce(ctx); err != nil {
		util.Log(ctx).WithError(err).Warn("materializer: initial poll failed")
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.pollOnce(ctx); err != nil {
				util.Log(ctx).WithError(err).Warn("materializer: poll failed")
			}
		}
	}
}

func (s *Service) pollOnce(ctx context.Context) error {
	for _, p := range s.prefixes {
		if err := s.pollPrefix(ctx, p); err != nil {
			util.Log(ctx).WithError(err).WithField("prefix", p).Error("materializer: prefix poll failed")
		}
	}
	return nil
}

func (s *Service) pollPrefix(ctx context.Context, prefix string) error {
	lastKey, err := s.watermarks.Get(ctx, prefix)
	if err != nil {
		return fmt.Errorf("get watermark: %w", err)
	}
	objs, err := s.reader.ListNewObjects(ctx, prefix, lastKey, s.listBatch)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if len(objs) == 0 {
		return nil
	}

	for _, o := range objs {
		key := ""
		if o.Key != nil {
			key = *o.Key
		}
		body, err := s.reader.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("get %s: %w", key, err)
		}

		switch {
		case strings.HasPrefix(prefix, "canonicals"):
			if _, err := s.indexer.ApplyCanonicalsParquet(ctx, body); err != nil {
				return fmt.Errorf("apply canonicals %s: %w", key, err)
			}
		case strings.HasPrefix(prefix, "embeddings"):
			if _, err := s.indexer.ApplyEmbeddingsParquet(ctx, body); err != nil {
				return fmt.Errorf("apply embeddings %s: %w", key, err)
			}
		default:
			util.Log(ctx).WithField("prefix", prefix).Warn("materializer: unknown prefix, skipping")
		}

		if err := s.watermarks.Set(ctx, prefix, key); err != nil {
			return fmt.Errorf("advance watermark to %s: %w", key, err)
		}
	}
	return nil
}
```

- [ ] **Step 2: Confirm compiles**

```bash
go build ./apps/materializer/service/...
```

- [ ] **Step 3: Commit**

```bash
git add apps/materializer/service/service.go
git commit -m "feat(materializer): poll loop — R2 → Parquet → Manticore"
```

---

## Task 13: Materializer entrypoint

**Files:**
- Create: `apps/materializer/cmd/main.go`

- [ ] **Step 1: Implement**

Create `apps/materializer/cmd/main.go`:

```go
// apps/materializer/cmd — entrypoint for the event-log → Manticore
// materializer. The pod polls R2 every 15 s for new Parquet files
// under tracked prefixes and upserts their rows into Manticore's
// idx_jobs_rt RT index.
package main

import (
	"context"
	"log"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/searchindex"

	matcfg "stawi.jobs/apps/materializer/config"
	matsvc "stawi.jobs/apps/materializer/service"
)

func main() {
	ctx := context.Background()

	cfg, err := matcfg.Load()
	if err != nil {
		log.Fatalf("materializer: load config: %v", err)
	}

	ctx, svc := frame.NewServiceWithContext(ctx, frame.WithConfig(&cfg))
	defer svc.Stop(ctx)

	// Reader side — R2 list + get.
	r2Client := eventlog.NewClient(eventlog.R2Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2Bucket,
		Endpoint:        cfg.R2Endpoint,
		UsePathStyle:    cfg.R2UsePathStyle,
	})
	reader := eventlog.NewReader(r2Client, cfg.R2Bucket)

	// Manticore client (HTTP JSON API).
	mc, err := searchindex.Open(searchindex.Config{
		URL:     cfg.ManticoreURL,
		Timeout: cfg.ManticoreTimeout,
	})
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: open manticore failed")
	}
	defer func() { _ = mc.Close() }()

	if err := searchindex.Apply(ctx, mc); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: apply schema failed")
	}

	// Watermarks from Postgres (the svc already wires a *gorm.DB).
	dbFn := func(ctx context.Context, readOnly bool) *gorm.DB {
		return svc.DB(ctx, readOnly)
	}
	watermarks := repository.NewWatermarkRepository(dbFn)

	indexer := matsvc.NewIndexer(mc)
	service := matsvc.NewService(reader, indexer, watermarks, cfg.Prefixes, cfg.PollInterval, cfg.ListBatchSize)

	go func() {
		if err := service.Run(ctx); err != nil {
			util.Log(ctx).WithError(err).Error("materializer: run exited")
		}
	}()

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: frame.Run failed")
	}
}
```

**NOTE on the Gorm DB accessor:** the exact signature `svc.DB(ctx, readOnly)` may vary — mirror whatever `apps/crawler/cmd/main.go` does for `WatermarkRepository`-style wiring. Add the `gorm.io/gorm` import if needed. If the Frame service exposes a different DB accessor pattern, adapt minimally.

- [ ] **Step 2: Confirm compiles**

```bash
go build ./apps/materializer/cmd/...
```

If the GORM DB accessor shape differs, fix by referencing `apps/crawler/cmd/main.go` or `apps/api/cmd/main.go` for the canonical pattern.

- [ ] **Step 3: Commit**

```bash
git add apps/materializer/cmd/main.go
git commit -m "feat(materializer): entrypoint wiring R2 + Manticore + watermarks"
```

---

## Task 14: Materializer end-to-end test

**Files:**
- Create: `apps/materializer/service/service_test.go`

- [ ] **Step 1: Write the test**

Create `apps/materializer/service/service_test.go`. The test stands up MinIO + Manticore, seeds a Parquet file under `canonicals/` manually, runs one poll tick, and asserts the Manticore `idx_jobs_rt` table contains the expected row.

For the Postgres watermark: use the existing `glebarez/sqlite` driver for tests (already in go.mod) to avoid a Postgres container just for a tiny table. Open an in-memory SQLite, `AutoMigrate` the `MaterializerWatermark` model.

```go
package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/glebarez/sqlite"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"gorm.io/gorm"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/searchindex"

	matsvc "stawi.jobs/apps/materializer/service"
)

func TestMaterializerE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// --- MinIO (R2) ---
	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })

	endpoint, _ := mc.ConnectionString(ctx)
	r2cfg := eventlog.R2Config{
		AccountID:       "test",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "stawi-jobs-log-mat",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	r2c := eventlog.NewClient(r2cfg)
	if _, err := r2c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(r2cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	// --- Manticore (HTTP JSON API) ---
	url, stopManticore := startManticoreForMat(t, ctx)
	defer stopManticore()
	mtc, err := searchindex.Open(searchindex.Config{URL: url})
	if err != nil {
		t.Fatalf("manticore open: %v", err)
	}
	defer func() { _ = mtc.Close() }()
	if err := searchindex.Apply(ctx, mtc); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	// --- SQLite watermarks ---
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(&repository.MaterializerWatermark{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	watermarks := repository.NewWatermarkRepository(func(_ context.Context, _ bool) *gorm.DB { return db })

	// --- Seed one canonical Parquet file ---
	now := time.Now().UTC()
	can := eventsv1.CanonicalUpsertedV1{
		CanonicalID:  "can_e2e_1",
		ClusterID:    "clu_1",
		Slug:         "e2e-senior-engineer-acme",
		Title:        "E2E Senior Engineer",
		Company:      "Acme",
		Description:  "We are hiring an engineer",
		Country:      "KE",
		RemoteType:   "remote",
		Category:     "programming",
		Status:       "active",
		QualityScore: 75,
		PostedAt:     now,
		LastSeenAt:   now,
		ExpiresAt:    now.Add(120 * 24 * time.Hour),
	}
	body, err := eventlog.WriteParquet([]eventsv1.CanonicalUpsertedV1{can})
	if err != nil {
		t.Fatalf("write parquet: %v", err)
	}
	pk := eventsv1.PartitionKey(eventsv1.TopicCanonicalsUpserted, now, can.ClusterID)
	objKey := pk.ObjectPath("canonicals", "e2e-mat")
	up := eventlog.NewUploader(r2c, r2cfg.Bucket)
	if _, err := up.Put(ctx, objKey, body); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// --- Run one poll tick ---
	reader := eventlog.NewReader(r2c, r2cfg.Bucket)
	indexer := matsvc.NewIndexer(mtc)
	service := matsvc.NewService(reader, indexer, watermarks, []string{"canonicals/"}, 100*time.Millisecond, 50)

	pollCtx, cancelPoll := context.WithTimeout(ctx, 10*time.Second)
	done := make(chan struct{})
	go func() { _ = service.Run(pollCtx); close(done) }()

	// Wait until Manticore has the row. Query via the HTTP /search
	// endpoint rather than /sql so we exercise the same path the API
	// will use.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		raw, qerr := mtc.Search(ctx, map[string]any{
			"index": "idx_jobs_rt",
			"query": map[string]any{
				"equals": map[string]any{"country": "KE"},
			},
			"limit": 10,
		})
		if qerr == nil && strings.Contains(string(raw), `"can_e2e_1"`) {
			cancelPoll()
			<-done
			return // success
		}
		time.Sleep(200 * time.Millisecond)
	}
	cancelPoll()
	<-done
	t.Fatal("materializer did not propagate canonical into Manticore within deadline")
}

// startManticoreForMat starts a Manticore testcontainer and returns
// (httpURL, stopFn). Mirrors pkg/searchindex/schema_test.go's helper;
// duplicated here because _test files can't be imported across
// packages and the helper is cheap to repeat.
func startManticoreForMat(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "manticoresearch/manticore:6.3.2",
		ExposedPorts: []string{"9308/tcp"},
		WaitingFor:   wait.ForListeningPort("9308/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start manticore: %v", err)
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "9308/tcp")
	url := fmt.Sprintf("http://%s:%s", host, port.Port())
	return url, func() { _ = c.Terminate(context.Background()) }
}
```

Additional imports for this file:
```go
import (
    "fmt"
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/wait"
)
```

- [ ] **Step 2: Run the test**

```bash
go test ./apps/materializer/service/... -run TestMaterializerE2E -v -count=1 -timeout 5m
```

Expected: `PASS` (may take 60–90 s on first run while both MinIO and Manticore images download).

- [ ] **Step 3: Commit**

```bash
git add apps/materializer/service/service_test.go
git commit -m "test(materializer): end-to-end publish → R2 → poll → Manticore row"
```

---

## Task 15: `GET /api/v2/search` handler on `apps/api`

**Files:**
- Create: `apps/api/cmd/search_v2.go`
- Modify: `apps/api/cmd/main.go` (register route + wire Manticore client)

- [ ] **Step 1: Implement the handler**

Create `apps/api/cmd/search_v2.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"stawi.jobs/pkg/searchindex"
)

// searchV2Hit is the JSON shape returned by /api/v2/search. Kept
// minimal for Phase 2; converges with legacy /api/search response
// fields as more intelligence is wired through Manticore.
type searchV2Hit struct {
	CanonicalID string `json:"canonical_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Company     string `json:"company"`
	Country     string `json:"country"`
	RemoteType  string `json:"remote_type"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
}

type searchV2Response struct {
	Hits  []searchV2Hit `json:"hits"`
	Total int           `json:"total"`
}

// manticoreSearchResponse mirrors the /search JSON response shape.
// Full docs: https://manual.manticoresearch.com/Searching/Intro
type manticoreSearchResponse struct {
	Hits struct {
		Total int `json:"total"`
		Hits  []struct {
			ID     int64           `json:"_id"`
			Score  float64         `json:"_score"`
			Source json.RawMessage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// searchV2Handler returns an HTTP handler that queries Manticore's
// idx_jobs_rt via the /search JSON endpoint. Params: q (text,
// optional), country (alpha-2), remote_type, category, limit
// (default 20, max 50). Always filters status='active'.
//
// Phase 2 scope — no vectors, no rerank, no tiered cascade. Those
// arrive in Phase 3+.
func searchV2Handler(client *searchindex.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		q := strings.TrimSpace(req.URL.Query().Get("q"))
		country := strings.ToUpper(strings.TrimSpace(req.URL.Query().Get("country")))
		remote := strings.TrimSpace(req.URL.Query().Get("remote_type"))
		category := strings.TrimSpace(req.URL.Query().Get("category"))

		limit := 20
		if s := req.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 50 {
			limit = 50
		}

		// Build Manticore JSON query. "bool" with "must" (full-text)
		// and "filter" (exact-match attributes). Empty q → match-all.
		filter := []map[string]any{{"equals": map[string]any{"status": "active"}}}
		if country != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"country": country}})
		}
		if remote != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"remote_type": remote}})
		}
		if category != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"category": category}})
		}

		boolQ := map[string]any{"filter": filter}
		if q != "" {
			boolQ["must"] = []map[string]any{{"match": map[string]any{"*": q}}}
		} else {
			boolQ["must"] = []map[string]any{{"match_all": map[string]any{}}}
		}

		query := map[string]any{
			"index": "idx_jobs_rt",
			"query": map[string]any{"bool": boolQ},
			"sort":  []any{map[string]any{"posted_at": "desc"}},
			"limit": limit,
		}

		raw, err := client.Search(ctx, query)
		if err != nil {
			http.Error(w, "search failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var parsed manticoreSearchResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			http.Error(w, "search decode failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		resp := searchV2Response{Total: parsed.Hits.Total}
		for _, h := range parsed.Hits.Hits {
			var src searchV2Hit
			if err := json.Unmarshal(h.Source, &src); err != nil {
				continue
			}
			resp.Hits = append(resp.Hits, src)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
```

- [ ] **Step 2: Register the route and wire the Manticore client in main.go**

Open `apps/api/cmd/main.go`. Find where the HTTP mux is wired and where existing handlers are registered (look for `mux.HandleFunc("GET /api/search"`). Add:

1. Near the top of the main function (after other clients are created), add:

```go
manticoreURL := os.Getenv("MANTICORE_URL")
var manticoreClient *searchindex.Client
if manticoreURL != "" {
    mc, err := searchindex.Open(searchindex.Config{URL: manticoreURL})
    if err != nil {
        util.Log(ctx).WithError(err).Fatal("api: manticore open failed")
    }
    defer func() { _ = mc.Close() }()
    manticoreClient = mc
}
```

2. Next to the `mux.HandleFunc("GET /api/search", …)` registration, add:

```go
if manticoreClient != nil {
    mux.HandleFunc("GET /api/v2/search", searchV2Handler(manticoreClient))
}
```

3. Add the import for `stawi.jobs/pkg/searchindex` at the top.

The conditional registration means the new endpoint only exists when `MANTICORE_URL` is set, so existing deploys that haven't provisioned Manticore yet continue to work unchanged.

- [ ] **Step 3: Confirm compiles**

```bash
go build ./apps/api/...
```

- [ ] **Step 4: Commit**

```bash
git add apps/api/cmd/search_v2.go apps/api/cmd/main.go
git commit -m "feat(api): add /api/v2/search backed by Manticore"
```

---

## Task 16: API v2 search integration test

**Files:**
- Create: `apps/api/cmd/search_v2_test.go`

- [ ] **Step 1: Write the test**

The test stands up Manticore, applies the schema, seeds one row directly via `searchindex.Client.Exec` (not through the materializer — we test the handler in isolation), then hits the HTTP handler and asserts the response.

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"stawi.jobs/pkg/searchindex"
)

func startManticoreForAPITest(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "manticoresearch/manticore:6.3.2",
		ExposedPorts: []string{"9308/tcp"},
		WaitingFor:   wait.ForListeningPort("9308/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start manticore: %v", err)
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "9308/tcp")
	url := fmt.Sprintf("http://%s:%s", host, port.Port())
	return url, func() { _ = c.Terminate(context.Background()) }
}

func TestSearchV2HandlerReturnsManticoreRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	url, stop := startManticoreForAPITest(t, ctx)
	defer stop()

	client, err := searchindex.Open(searchindex.Config{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := searchindex.Apply(ctx, client); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Seed one row via /replace.
	now := time.Now().Unix()
	doc := map[string]any{
		"canonical_id":    "can_api_1",
		"slug":            "senior-backend-acme",
		"title":           "Senior Backend Engineer",
		"company":         "Acme",
		"description":     "We are hiring",
		"category":        "programming",
		"country":         "KE",
		"language":        "en",
		"remote_type":     "remote",
		"employment_type": "full-time",
		"seniority":       "senior",
		"salary_min":      100000,
		"salary_max":      180000,
		"currency":        "USD",
		"quality_score":   float32(85),
		"is_featured":     true,
		"posted_at":       now,
		"last_seen_at":    now,
		"expires_at":      now + 86400,
		"status":          "active",
	}
	if err := client.Replace(ctx, "idx_jobs_rt", 1, doc); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handler := searchV2Handler(client)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/search?q=backend&country=KE", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp searchV2Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Total != 1 {
		t.Fatalf("total=%d want 1, body=%s", resp.Total, rec.Body.String())
	}
	if resp.Hits[0].CanonicalID != "can_api_1" {
		t.Fatalf("hit mismatch: %+v", resp.Hits[0])
	}
}
```

Copy the Manticore testcontainer helper body from `pkg/searchindex/schema_test.go`.

- [ ] **Step 2: Run the test**

```bash
go test ./apps/api/cmd/... -run TestSearchV2HandlerReturnsManticoreRows -v -count=1 -timeout 5m
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add apps/api/cmd/search_v2_test.go
git commit -m "test(api): /api/v2/search handler against live Manticore"
```

---

## Task 17: Dockerfile + Makefile for materializer

**Files:**
- Create: `apps/materializer/Dockerfile`
- Modify: `Makefile`

- [ ] **Step 1: Dockerfile**

Copy `apps/writer/Dockerfile` to `apps/materializer/Dockerfile` and substitute `writer` → `materializer` in the binary name and paths.

- [ ] **Step 2: Makefile**

Edit `Makefile`:

1. Extend `APP_DIRS`: `apps/crawler apps/scheduler apps/api apps/writer apps/materializer`
2. Add target:
   ```
   run-materializer:
       go run ./apps/materializer/cmd
   ```
   (Use a real tab.)
3. Add `run-materializer` to `.PHONY`.

- [ ] **Step 3: Verify**

```bash
go build ./...
```

Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add apps/materializer/Dockerfile Makefile
git commit -m "chore(materializer): Dockerfile + Makefile targets"
```

---

## Plan completion verification

After all tasks are complete:

```bash
# All tests green (Phase 2 tests pull two containers on first run)
go test ./... -count=1 -timeout 10m

# Both new binaries build cleanly
go build -o /tmp/mat-bin ./apps/materializer/cmd && rm /tmp/mat-bin

# Git history
git log --oneline main..HEAD
```

Expected:
- Every test passes (envelope round-trip, Manticore schema apply, materializer E2E, API v2 handler).
- `apps/materializer` binary builds.
- 17 commits on this branch, one per task.

At this point, an operator can:
1. Start local stack with `docker compose -f deploy/docker-compose.yml up -d` (now includes Manticore).
2. Run `make run-writer` and `make run-materializer` in two terminals.
3. Publish a synthetic `CanonicalUpsertedV1` envelope via a small test emitter or a unit test, and observe it appear in `GET /api/v2/search?q=…`.

Phase 3 builds `apps/worker` which emits canonical events organically from the crawler pipeline.
