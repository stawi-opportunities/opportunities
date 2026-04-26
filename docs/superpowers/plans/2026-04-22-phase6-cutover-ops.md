# Phase 6 — Greenfield Cutover + Ops Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close out the parquet+manticore greenfield refactor. Ship the last ops pieces (hourly/daily Parquet compaction, full backpressure policy, KV rebuild-from-R2, StaleLister R2 upgrade), rewrite `apps/api` to read exclusively from Manticore (legacy `/api/*` + `/search` + `/jobs/*` + `/admin/backfill` + `/admin/feeds/rebuild` all repointed), drop the legacy Postgres tables + candidate CV/preferences/embedding columns + `saved_jobs` table, delete all legacy code (`pkg/pipeline/handlers/*`, `pkg/billing/*`, legacy candidate handler files, dead repository adapters, dead domain models), and land the cutover runbook + verification harness. After this plan the platform is fully greenfield: Postgres holds only `sources`, `candidates` identity, `crawl_jobs` audit, and `raw_payloads` (content-hash dedup). Everything job-seeker-facing reads Manticore.

**Architecture:** No new services; this plan consolidates cleanups across five existing binaries.

- `apps/writer` gains two admin endpoints (`POST /_admin/compact/hourly`, `POST /_admin/compact/daily`) that read the day's raw Parquet partitions, dedup by `event_id`, and either write back a merged set (hourly) or rebuild `*_current/` snapshots per-key (daily). Both are Trustage-fired.
- `apps/worker` gains `POST /_admin/kv/rebuild` that scans `canonicals_current/` and repopulates `dedup:*`, `cluster:*`, and `bloom:dedup:*` (spec §9.4).
- `pkg/backpressure.Gate.Admit` gets its full policy (drain-time per topic, HPA-ceiling awareness). The Phase 4 stub stays backward-compatible — its wait-hint shape is unchanged.
- `apps/api`'s legacy `canonical_jobs`-reading endpoints are replaced with Manticore-backed equivalents under `/api/v2/*` or rewritten in place. `/admin/backfill` (Hugo snapshot publishing) sources from R2 `canonicals_current/` Parquet. `/admin/feeds/rebuild` (country-sharded feed manifests) sources from Manticore. `/admin/republish`, `/admin/backfill/country` are deleted.
- `apps/candidates/service/admin/v1/stale_lister.go` (Phase 5) stops using `candidates.updated_at` as a proxy — the new `pkg/candidatestore.StaleReader` reads `candidates_cv_current/` Parquet.
- Legacy candidate handler files (`embedding.go`, `profile_created.go`), legacy pipeline handlers, legacy event topic constants (`variant.raw.stored`, `source.urls.discovered`, `source.quality.review`), orphan packages (`pkg/billing/`), and dead repository adapters (`pkg/repository/{facets,rerank_cache,saved_job,rejected}.go`) all delete in one bundle.
- One consolidated SQL migration (`db/migrations/0003_cutover_drop_legacy.sql`) drops `canonical_jobs`, `job_variants`, `job_clusters`, `job_cluster_members`, `crawl_page_state`, `rerank_cache`, `mv_job_facets`, `saved_jobs`, plus the CV/preferences/embedding columns on `candidates`. Domain model + AutoMigrate lists in `apps/crawler/cmd/main.go` and `apps/api/cmd/main.go` update to match.

**Tech stack:**
- Go 1.26, Frame (`github.com/pitabwire/frame` v1.94.1), `pitabwire/util` logging
- Existing `pkg/searchindex.Client` (Manticore HTTP), `pkg/eventlog` (R2 list/get/put/Parquet), `pkg/archive` (raw R2)
- Existing `pkg/events/v1/` envelope + partition-key helpers
- Existing `pkg/dedupe.KV` (Valkey client used by apps/worker dedup handler)
- Existing `pkg/bloom` (in-memory bloom + KV-backed bloom used by dedup handler)
- `k6` for load smoke + post-cut verification — scripts under `tests/k6/`
- `testcontainers-go` for MinIO + Manticore + Valkey + Postgres integration tests (existing convention)

**What's in this plan:**
- Hourly + daily Parquet compaction on `apps/writer` with real dedup-by-`event_id`, real `*_current/` rebuild, and real partition-prefix scans.
- Four new Trustage trigger JSONs: `compact-hourly.json`, `compact-daily.json`, `sources-quality-window-reset.json`, `sources-health-decay.json`.
- Full `backpressure.Gate.Admit` policy with per-topic drain-time thresholds + HPA-ceiling awareness. Wait hint becomes drain-time-aware.
- KV rebuild admin endpoint on `apps/worker`.
- `pkg/candidatestore.StaleReader` — R2-backed listing of candidates whose latest CV upload is older than cutoff.
- Full `apps/api` rewrite to Manticore-only reads: expanded `/api/v2/search`, new `/api/v2/jobs/{id}`, `/api/v2/jobs/top`, `/api/v2/jobs/latest`, `/api/v2/categories`, `/api/v2/stats/summary`, Manticore-sourced `/api/feed` + `/api/feed/tier` + `/admin/feeds/rebuild`, R2-Parquet-sourced `/admin/backfill`. All legacy Postgres read endpoints deleted.
- Legacy code deletion: `apps/candidates/service/events/{embedding.go, profile_created.go}`, all of `pkg/pipeline/handlers/`, all of `pkg/billing/`, `pkg/repository/{facets,rerank_cache,saved_job,rejected}.go`, `apps/api/cmd/{country_backfill,manifest,tiered}.go` (rebuilt fresh), dead `domain.*` types.
- Consolidated SQL migration (`0003_cutover_drop_legacy.sql`) and aligned domain model + AutoMigrate calls.
- Cutover runbook (`docs/ops/cutover-runbook.md`) and rebuild runbooks (Manticore-from-zero, KV-from-R2, writer-backlog).
- End-to-end smoke test + k6 scripts for §11.3 post-cut verification.

**What's NOT in this plan (deferred to v1.1):**
- `idx_candidates_rt` Manticore index + recruiter-side candidate search (spec §2 Non-goals).
- Per-page connector fan-out (listing → detail URL pair via `crawl.requests.v1`). Current iterator-based connectors keep working; the refactor is orthogonal.
- Rerank pre-warmer Trustage trigger (spec §12.3).
- Multi-region R2 (spec §12.7) and Manticore sharding (spec §12.5).
- Re-embedding / replay jobs on model-version swap (spec §7.3).
- Audit-log retention tiering for `variants/`, `match_decisions/` (spec §12.6) — cheap enough on R2 at v1 scale.
- Chaos-test harness codification (spec §10.3). Manual rehearsal runbooks are in this plan; automated chaos is a v1.1 nice-to-have.

---

## File structure

**Create:**

| File | Responsibility |
|---|---|
| `apps/writer/service/compact.go` | `Compactor` struct + `CompactHourly` + `CompactDaily` methods — dedup-by-event_id, `*_current/` rebuild |
| `apps/writer/service/compact_test.go` | Unit tests with MinIO + seeded Parquet |
| `apps/writer/service/compact_admin.go` | `CompactHourlyHandler` + `CompactDailyHandler` — HTTP adapters |
| `apps/writer/service/compact_admin_test.go` | httptest-driven test |
| `apps/worker/service/kv_rebuild.go` | `KVRebuilder` struct + `Run` method — `canonicals_current/` scan → KV repopulate |
| `apps/worker/service/kv_rebuild_test.go` | Unit test with testcontainers MinIO + Valkey |
| `apps/worker/service/kv_rebuild_admin.go` | `KVRebuildHandler` HTTP adapter |
| `pkg/candidatestore/stale_reader.go` | `StaleReader` struct + `ListStale(asOf, cutoff)` — scans `candidates_cv_current/` for `occurred_at < cutoff` |
| `pkg/candidatestore/stale_reader_test.go` | MinIO-backed integration test |
| `apps/api/cmd/manticore_client.go` | Typed helpers over `pkg/searchindex.Client` — GetByID, Count, Facets, Top, Latest |
| `apps/api/cmd/manticore_client_test.go` | Unit test against a stub HTTP server |
| `apps/api/cmd/endpoints_v2.go` | New `/api/v2/*` handlers (search, jobs/{id}, top, latest, categories, stats) and the rewritten `/api/feed` + `/api/feed/tier` |
| `apps/api/cmd/endpoints_v2_test.go` | httptest-driven unit tests per endpoint |
| `apps/api/cmd/backfill_parquet.go` | Hugo-snapshot publisher that sources from R2 `canonicals_current/` Parquet |
| `apps/api/cmd/backfill_parquet_test.go` | Unit test with fake R2 + fake publisher |
| `definitions/trustage/compact-hourly.json` | Hourly compaction trigger |
| `definitions/trustage/compact-daily.json` | Nightly compaction trigger |
| `definitions/trustage/sources-quality-window-reset.json` | Weekly quality-window reset |
| `definitions/trustage/sources-health-decay.json` | Hourly health-decay |
| `db/migrations/0003_cutover_drop_legacy.sql` | Drop legacy tables + candidate columns + saved_jobs |
| `docs/ops/cutover-runbook.md` | Pre-cut / cut / post-cut / rollback runbooks |
| `docs/ops/runbook-manticore-rebuild.md` | Manticore-from-zero rebuild |
| `docs/ops/runbook-kv-rebuild.md` | Valkey rebuild-from-R2 |
| `docs/ops/runbook-writer-backlog.md` | Writer pub/sub backlog response |
| `tests/k6/smoke_post_cut.js` | k6 script for §11.3 search + facets checks |
| `tests/integration/cutover_e2e_test.go` | End-to-end crawl → search smoke |

**Modify:**

| File | Change |
|---|---|
| `pkg/backpressure/gate.go` | Replace `Admit` stub with full policy: per-topic drain-time thresholds + HPA-ceiling awareness. Add `UpdateLag` + `Config` methods from spec §8.3 |
| `pkg/backpressure/gate_test.go` | New tests for drain-time admit + per-topic config + HPA-ceiling behaviour |
| `apps/candidates/service/admin/v1/stale_lister.go` | Use `candidatestore.StaleReader` instead of `repository.CandidateRepository.ListInactiveSince` |
| `apps/candidates/service/admin/v1/stale_lister_test.go` | Rewrite test against `StaleReader` fake |
| `apps/candidates/cmd/main.go` | Wire `candidatestore.StaleReader` into `CVStaleNudgeDeps` (replaces `RepoStaleLister`) |
| `apps/writer/cmd/main.go` | Wire the compactor + mount the two admin endpoints |
| `apps/worker/cmd/main.go` | Wire the KV rebuilder + mount the admin endpoint |
| `apps/worker/service/embed.go:1204` | Surface `ModelVersion` from `Extractor` into `EmbeddingV1` |
| `apps/worker/service/publish.go:1442` | Plumb real `R2Version` into `PublishedV1` |
| `apps/api/cmd/main.go` | Delete every Postgres-backed read endpoint; wire the new Manticore client + v2 endpoints; simplify AutoMigrate list |
| `apps/api/cmd/search_v2.go` | Expanded filter set (salary, sort, cursor, facets) — merged into `endpoints_v2.go` and deleted here |
| `apps/api/cmd/country_backfill.go` | DELETE — dead code (country is computed by the worker normalize stage) |
| `apps/api/cmd/manifest.go`, `tiered.go` | DELETE — rewritten in `endpoints_v2.go` backed by Manticore |
| `apps/crawler/cmd/main.go` | Remove dropped models from `AutoMigrate` list |
| `pkg/repository/candidate.go` | Remove `UpdateEmbedding` + related dead methods; align with trimmed schema |
| `pkg/domain/models.go` | Remove `JobVariant`, `JobCluster`, `JobClusterMember`, `CanonicalJob`, `CrawlPageState`, `RejectedJob`, `SavedJob` types; trim CV/preferences/embedding fields from `CandidateProfile` |

**Delete:**

| File | Reason |
|---|---|
| `apps/candidates/service/events/embedding.go` | Legacy — Phase 5 replaced |
| `apps/candidates/service/events/profile_created.go` | Legacy — Phase 5 replaced |
| `pkg/pipeline/handlers/canonical.go` | Legacy worker pipeline — Phase 3 replaced |
| `pkg/pipeline/handlers/dedup.go` | Legacy — Phase 3 replaced |
| `pkg/pipeline/handlers/normalize.go` | Legacy — Phase 3 replaced |
| `pkg/pipeline/handlers/payloads.go` | Legacy topic constants + payload types — superseded by `pkg/events/v1/` |
| `pkg/pipeline/handlers/payloads_test.go` | Legacy test |
| `pkg/pipeline/handlers/publish.go` | Legacy — Phase 3 replaced |
| `pkg/pipeline/handlers/source_expand.go` | Legacy |
| `pkg/pipeline/handlers/source_quality.go` | Legacy |
| `pkg/pipeline/handlers/translate.go` | Legacy — Phase 3 replaced |
| `pkg/pipeline/handlers/validate.go` | Legacy — Phase 3 replaced |
| `pkg/billing/billing_test.go` + `client.go` + `geo.go` + `plan.go` + `route.go` | Zero importers post-Phase-5 |
| `pkg/repository/facets.go` | Reads `mv_job_facets`, dropped by SQL migration |
| `pkg/repository/rerank_cache.go` | Reads `rerank_cache`, dropped |
| `pkg/repository/saved_job.go` | Reads `saved_jobs`, dropped (user chose drop the feature) |
| `pkg/repository/rejected.go` | Reads a dead table |
| `pkg/repository/retention.go` | Operates on `canonical_jobs`, dropped |
| `pkg/repository/job.go` | Reads `canonical_jobs`, `job_variants`, dropped |
| `pkg/repository/match.go` | Reads legacy match tables if any (verify + delete) |

---

## Task 1: Hourly compaction — merge small Parquet files per day-partition with event_id dedup

**Files:**
- Create: `apps/writer/service/compact.go`
- Create: `apps/writer/service/compact_test.go`

The hourly compactor reads every raw Parquet file written during the current UTC hour for a given collection, concatenates their rows, dedups by `event_id` (keeping the earliest `occurred_at`), and rewrites them into one larger Parquet file. The small files are then deleted. This keeps the `variants/dt=/src=…/` directories from accumulating thousands of tiny files per hour and makes the daily `*_current/` rebuild cheap.

The compactor is structured generically over row type `T` so the same code path serves all ten collections written by the writer (`variants/`, `canonicals/`, `embeddings/`, `translations/`, `published/`, `crawl_page_completed/`, `sources_discovered/`, `candidates_cv/`, `candidates_improvements/`, `candidates_preferences/`, `candidates_embeddings/`, `candidates_matches_ready/`). Each collection declares its `event_id` field name via a small adapter because Parquet rows don't all use the same column name (e.g., canonicals use `canonical_id`, embeddings use `canonical_id`, variants use `variant_id`).

For deterministic testing we use UTC "now" passed in as a parameter rather than `time.Now()` inside the function.

- [ ] **Step 1: Write the failing test**

```go
// apps/writer/service/compact_test.go
package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

// TestCompactHourly_DedupAndMerge seeds three small Parquet files for
// the variants collection, two of which contain overlapping event_ids,
// and asserts that after compaction: (a) a single merged file exists
// with the de-duplicated rows, and (b) the three original files are
// gone. The duplicate's occurred_at resolution picks the earliest.
func TestCompactHourly_DedupAndMerge(t *testing.T) {
	ctx := context.Background()
	mc := startMinIO(t)
	defer mc.Close()

	uploader := eventlog.NewUploader(mc.Client, mc.Bucket)
	reader := eventlog.NewReader(mc.Client, mc.Bucket)

	hour := time.Date(2026, 4, 22, 13, 0, 0, 0, time.UTC)
	dt := hour.Format("2006-01-02")

	// Seed three files under variants/dt=2026-04-22/src=acme/
	rowsA := []eventsv1.VariantIngestedV1{
		{EventID: "v1", VariantID: "var-1", SourceID: "acme", OccurredAt: hour.Add(2 * time.Minute)},
		{EventID: "v2", VariantID: "var-2", SourceID: "acme", OccurredAt: hour.Add(3 * time.Minute)},
	}
	rowsB := []eventsv1.VariantIngestedV1{
		// v2 overlaps with rowsA — occurred_at earlier, so this one wins
		{EventID: "v2", VariantID: "var-2", SourceID: "acme", OccurredAt: hour.Add(1 * time.Minute)},
		{EventID: "v3", VariantID: "var-3", SourceID: "acme", OccurredAt: hour.Add(4 * time.Minute)},
	}
	rowsC := []eventsv1.VariantIngestedV1{
		{EventID: "v4", VariantID: "var-4", SourceID: "acme", OccurredAt: hour.Add(5 * time.Minute)},
	}
	for i, rows := range [][]eventsv1.VariantIngestedV1{rowsA, rowsB, rowsC} {
		body, err := eventlog.WriteParquet(rows)
		require.NoError(t, err)
		key := "variants/dt=" + dt + "/src=acme/part-" + string(rune('a'+i)) + ".parquet"
		_, err = uploader.Put(ctx, key, body)
		require.NoError(t, err)
	}

	c := NewCompactor(mc.Client, reader, uploader, mc.Bucket)

	got, err := c.CompactHourly(ctx, CompactHourlyInput{
		Collection: "variants",
		Hour:       hour,
	})
	require.NoError(t, err)
	require.Equal(t, 4, got.RowsAfter, "deduped: v1+v2+v3+v4")
	require.Equal(t, 5, got.RowsBefore, "seeded 5 total with one dup")
	require.Equal(t, 1, got.FilesAfter, "one merged file")
	require.Equal(t, 3, got.FilesDeleted, "three originals removed")

	// Verify the merged file contains the earliest v2 occurred_at.
	list, err := mc.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(mc.Bucket),
		Prefix: aws.String("variants/dt=" + dt + "/src=acme/"),
	})
	require.NoError(t, err)
	require.Len(t, list.Contents, 1)

	body := getObj(t, mc.Client, mc.Bucket, *list.Contents[0].Key)
	merged, err := eventlog.ReadParquet[eventsv1.VariantIngestedV1](body)
	require.NoError(t, err)
	var v2 eventsv1.VariantIngestedV1
	for _, r := range merged {
		if r.EventID == "v2" {
			v2 = r
		}
	}
	require.Equal(t, hour.Add(1*time.Minute), v2.OccurredAt.UTC(), "earliest occurred_at wins")
}

// getObj is a test helper.
func getObj(t *testing.T, cli *s3.Client, bucket, key string) []byte {
	t.Helper()
	out, err := cli.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	defer func() { _ = out.Body.Close() }()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(out.Body)
	require.NoError(t, err)
	return buf.Bytes()
}

// Minimal stub — uses the existing startMinIO helper from other tests
// in this package (buffer_test.go / service_test.go already ship one).
// Implement here if not already present.
var _ = s3types.Object{}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./apps/writer/service -run TestCompactHourly -v`
Expected: FAIL with `undefined: NewCompactor` / `undefined: CompactHourlyInput`.

- [ ] **Step 3: Implement the compactor**

```go
// apps/writer/service/compact.go
package service

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

// Compactor merges small Parquet files under a day-partition into one
// larger file, deduping by event_id (earliest occurred_at wins). Shared
// by the hourly admin endpoint (today's partitions) and the daily
// admin endpoint (yesterday's partitions + *_current/ rebuild).
type Compactor struct {
	s3       *s3.Client
	reader   *eventlog.Reader
	uploader *eventlog.Uploader
	bucket   string
}

// NewCompactor wires the compactor.
func NewCompactor(cli *s3.Client, r *eventlog.Reader, u *eventlog.Uploader, bucket string) *Compactor {
	return &Compactor{s3: cli, reader: r, uploader: u, bucket: bucket}
}

// CompactHourlyInput scopes one compaction run to a single (collection,
// hour). Callers iterate collections externally because each collection
// binds to a Go type.
type CompactHourlyInput struct {
	Collection string    // "variants", "canonicals", ...
	Hour       time.Time // UTC hour boundary — only for logging; we compact the whole day
}

// CompactHourlyResult reports counters for logging/metrics.
type CompactHourlyResult struct {
	RowsBefore   int
	RowsAfter    int
	FilesBefore  int
	FilesAfter   int
	FilesDeleted int
}

// CompactHourly reads every Parquet file under <collection>/dt=<today>/
// (all secondary-key sub-prefixes), dedups by event_id, and rewrites
// a single merged file per secondary-key. The number of output files
// equals the number of distinct secondary keys.
//
// Implemented as a dispatch on Collection — each branch picks up the
// right typed reader/writer. Keeping the generic type bound visible at
// the call site avoids a reflection-based dedup that would be hard to
// reason about.
func (c *Compactor) CompactHourly(ctx context.Context, in CompactHourlyInput) (CompactHourlyResult, error) {
	dt := in.Hour.UTC().Format("2006-01-02")
	switch in.Collection {
	case "variants":
		return compactHourlyGeneric[eventsv1.VariantIngestedV1](ctx, c, in.Collection, dt, variantKey)
	case "canonicals":
		return compactHourlyGeneric[eventsv1.CanonicalUpsertedV1](ctx, c, in.Collection, dt, canonicalKey)
	case "embeddings":
		return compactHourlyGeneric[eventsv1.EmbeddingV1](ctx, c, in.Collection, dt, embeddingKey)
	case "translations":
		return compactHourlyGeneric[eventsv1.TranslationV1](ctx, c, in.Collection, dt, translationKey)
	case "published":
		return compactHourlyGeneric[eventsv1.PublishedV1](ctx, c, in.Collection, dt, publishedKey)
	case "crawl_page_completed":
		return compactHourlyGeneric[eventsv1.CrawlPageCompletedV1](ctx, c, in.Collection, dt, crawlPageKey)
	case "sources_discovered":
		return compactHourlyGeneric[eventsv1.SourceDiscoveredV1](ctx, c, in.Collection, dt, sourceDiscoveredKey)
	case "candidates_cv":
		return compactHourlyGeneric[eventsv1.CVExtractedV1](ctx, c, in.Collection, dt, cvKey)
	case "candidates_improvements":
		return compactHourlyGeneric[eventsv1.CVImprovedV1](ctx, c, in.Collection, dt, cvImprovedKey)
	case "candidates_preferences":
		return compactHourlyGeneric[eventsv1.PreferencesUpdatedV1](ctx, c, in.Collection, dt, preferencesKey)
	case "candidates_embeddings":
		return compactHourlyGeneric[eventsv1.CandidateEmbeddingV1](ctx, c, in.Collection, dt, candidateEmbeddingKey)
	case "candidates_matches_ready":
		return compactHourlyGeneric[eventsv1.MatchesReadyV1](ctx, c, in.Collection, dt, matchesReadyKey)
	default:
		return CompactHourlyResult{}, fmt.Errorf("compact: unknown collection %q", in.Collection)
	}
}

// keyFn extracts (eventID, occurredAt) for dedup.
type keyFn[T any] func(T) (eventID string, occurredAt time.Time)

func variantKey(r eventsv1.VariantIngestedV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func canonicalKey(r eventsv1.CanonicalUpsertedV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func embeddingKey(r eventsv1.EmbeddingV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func translationKey(r eventsv1.TranslationV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func publishedKey(r eventsv1.PublishedV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func crawlPageKey(r eventsv1.CrawlPageCompletedV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func sourceDiscoveredKey(r eventsv1.SourceDiscoveredV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func cvKey(r eventsv1.CVExtractedV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func cvImprovedKey(r eventsv1.CVImprovedV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func preferencesKey(r eventsv1.PreferencesUpdatedV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func candidateEmbeddingKey(r eventsv1.CandidateEmbeddingV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}
func matchesReadyKey(r eventsv1.MatchesReadyV1) (string, time.Time) {
	return r.EventID, r.OccurredAt.UTC()
}

func compactHourlyGeneric[T any](
	ctx context.Context,
	c *Compactor,
	collection, dt string,
	kf keyFn[T],
) (CompactHourlyResult, error) {
	prefix := collection + "/dt=" + dt + "/"

	// List every file under today's partition. The listing paginates
	// via StartAfter; we loop until empty. 1000 per page matches the
	// ListObjectsV2 default.
	var keys []string
	cursor := ""
	for {
		page, err := c.reader.ListNewObjects(ctx, prefix, cursor, 1000)
		if err != nil {
			return CompactHourlyResult{}, fmt.Errorf("compact: list %q: %w", prefix, err)
		}
		if len(page) == 0 {
			break
		}
		for _, o := range page {
			if o.Key != nil {
				keys = append(keys, *o.Key)
			}
		}
		cursor = *page[len(page)-1].Key
	}
	if len(keys) == 0 {
		return CompactHourlyResult{}, nil
	}

	// Group files by their secondary-key sub-prefix (the path segment
	// right after dt=…/). Example key:
	//   variants/dt=2026-04-22/src=acme/part-xxx.parquet
	//                        ^^^^^^^^^^ secondaryDir
	groups := map[string][]string{}
	for _, k := range keys {
		rel := strings.TrimPrefix(k, prefix)
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) != 2 {
			// Top-level file (some collections don't use a secondary
			// dir, e.g. canonicals). Group under "".
			groups[""] = append(groups[""], k)
			continue
		}
		groups[parts[0]] = append(groups[parts[0]], k)
	}

	var result CompactHourlyResult
	result.FilesBefore = len(keys)

	for secondaryDir, groupKeys := range groups {
		sort.Strings(groupKeys) // deterministic ordering for reads

		// Read + merge
		merged := map[string]T{}
		earliest := map[string]time.Time{}
		rowsRead := 0

		for _, k := range groupKeys {
			body, err := c.reader.Get(ctx, k)
			if err != nil {
				return result, fmt.Errorf("compact: get %q: %w", k, err)
			}
			rows, err := eventlog.ReadParquet[T](body)
			if err != nil {
				return result, fmt.Errorf("compact: read parquet %q: %w", k, err)
			}
			rowsRead += len(rows)
			for _, r := range rows {
				id, occ := kf(r)
				if id == "" {
					// No event_id? Keep as-is under a synthetic key so we
					// don't drop it. Shouldn't happen with the Phase 1
					// envelope but be defensive.
					id = "__sans_id_" + xid.New().String()
				}
				if prev, ok := earliest[id]; !ok || occ.Before(prev) {
					merged[id] = r
					earliest[id] = occ
				}
			}
		}

		rowsAfter := len(merged)
		result.RowsBefore += rowsRead
		result.RowsAfter += rowsAfter

		if rowsAfter == 0 {
			// Pathological: every row had an empty event_id somehow. Bail
			// on this group without writing or deleting anything.
			continue
		}

		// Serialize back. Stable order by (event_id) so two compactions
		// of the same input produce byte-identical output.
		ordered := make([]T, 0, rowsAfter)
		ids := make([]string, 0, rowsAfter)
		for id := range merged {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			ordered = append(ordered, merged[id])
		}
		body, err := eventlog.WriteParquet(ordered)
		if err != nil {
			return result, fmt.Errorf("compact: write parquet: %w", err)
		}

		// Upload merged file with a fresh xid.
		var mergedKey string
		if secondaryDir == "" {
			mergedKey = path.Join(prefix, "compact-"+xid.New().String()+".parquet")
		} else {
			mergedKey = path.Join(prefix, secondaryDir, "compact-"+xid.New().String()+".parquet")
		}
		if _, err := c.uploader.Put(ctx, mergedKey, body); err != nil {
			return result, fmt.Errorf("compact: upload %q: %w", mergedKey, err)
		}
		result.FilesAfter++

		// Delete the source files — safe now that the merged file is
		// durable. A concurrent reader that listed the old files might
		// get a 404 on Get; the materializer handles that gracefully by
		// skipping.
		for _, k := range groupKeys {
			_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(c.bucket),
				Key:    aws.String(k),
			})
			if err != nil {
				util.Log(ctx).WithError(err).WithField("key", k).
					Warn("compact: delete source failed; orphaned")
				continue
			}
			result.FilesDeleted++
		}
	}

	return result, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./apps/writer/service -run TestCompactHourly -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/writer/service/compact.go apps/writer/service/compact_test.go
git commit -m "feat(writer): hourly Parquet compaction with event_id dedup"
```

---

## Task 2: Daily compaction — rebuild `*_current/` partitions

**Files:**
- Modify: `apps/writer/service/compact.go` (add `CompactDaily`)
- Modify: `apps/writer/service/compact_test.go`

`CompactDaily` runs once per day. For each collection that has a `*_current/` partition, it reads the full raw partition (every `dt=` sub-prefix), reduces to one row per business key (latest `occurred_at` wins), and writes one file per key-prefix bucket under `<collection>_current/<bucket>/`. This is what Phase 5's `candidatestore.Reader` reads for the match endpoint, and what the KV rebuilder (Task 5) reads to repopulate `dedup:*`.

Business-key schemas per spec §5.2:

| Raw collection | Current collection | Business key | Bucket prefix |
|---|---|---|---|
| `canonicals/` | `canonicals_current/` | `cluster_id` | `cc=<cluster_id[:2]>` |
| `embeddings/` | `embeddings_current/` | `canonical_id` | `cc=<cluster_id[:2]>` (from canonical) |
| `translations/` | `translations_current/` | `(canonical_id, lang)` | `cc=<cluster_id[:2]>/lang=<lang>` |
| `candidates_cv/` | `candidates_cv_current/` | `candidate_id` | `cnd=<candidate_id[:2]>` |
| `candidates_embeddings/` | `candidates_embeddings_current/` | `candidate_id` | `cnd=<candidate_id[:2]>` |
| `candidates_preferences/` | `candidates_preferences_current/` | `candidate_id` | `cnd=<candidate_id[:2]>` |

Raw partitions for append-only audit collections (`variants/`, `published/`, `jobs_expired/`, `match_decisions/`, `candidates_improvements/`, `candidates_matches_ready/`) have no `*_current/` counterpart — they're audit streams. The daily compactor skips them.

For embeddings the business key is `canonical_id` but the bucket prefix uses `cluster_id` (from the parent canonical). That requires a join against `canonicals_current/` built earlier in the same run. To keep Task 2 simple we use `canonical_id[:2]` as the bucket prefix. It differs from the Phase 5 design but preserves cluster-local colocation at the same grain (256 buckets). If operations demand strict `cc=<cluster_id[:2]>` bucketing later it's a single-line change.

For translations we bucket on `canonical_id[:2]/lang=<lang>` for the same reason.

- [ ] **Step 1: Write the failing test**

```go
// Add to apps/writer/service/compact_test.go
func TestCompactDaily_RebuildsCurrent(t *testing.T) {
	ctx := context.Background()
	mc := startMinIO(t)
	defer mc.Close()

	uploader := eventlog.NewUploader(mc.Client, mc.Bucket)
	reader := eventlog.NewReader(mc.Client, mc.Bucket)

	// Seed two days of canonicals. Two versions of cluster c1, one
	// version of c2. Latest occurred_at wins.
	day1 := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	older := eventsv1.CanonicalUpsertedV1{
		EventID: "e1", ClusterID: "c1abcdef", CanonicalID: "can-1",
		Title: "Older", OccurredAt: day1,
	}
	newer := eventsv1.CanonicalUpsertedV1{
		EventID: "e2", ClusterID: "c1abcdef", CanonicalID: "can-1",
		Title: "Newer", OccurredAt: day2,
	}
	other := eventsv1.CanonicalUpsertedV1{
		EventID: "e3", ClusterID: "c2xyz", CanonicalID: "can-2",
		Title: "Other", OccurredAt: day1,
	}
	for _, seed := range []struct {
		dt   string
		rows []eventsv1.CanonicalUpsertedV1
	}{
		{dt: "2026-04-21", rows: []eventsv1.CanonicalUpsertedV1{older, other}},
		{dt: "2026-04-22", rows: []eventsv1.CanonicalUpsertedV1{newer}},
	} {
		body, err := eventlog.WriteParquet(seed.rows)
		require.NoError(t, err)
		_, err = uploader.Put(ctx, "canonicals/dt="+seed.dt+"/part.parquet", body)
		require.NoError(t, err)
	}

	c := NewCompactor(mc.Client, reader, uploader, mc.Bucket)
	res, err := c.CompactDaily(ctx, CompactDailyInput{Collection: "canonicals"})
	require.NoError(t, err)
	require.Equal(t, 3, res.RowsBefore)
	require.Equal(t, 2, res.RowsAfter, "c1 has one latest; c2 has one")

	// c1's bucket is cc=c1 (first two hex chars of cluster_id).
	list, err := mc.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(mc.Bucket),
		Prefix: aws.String("canonicals_current/"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, list.Contents)

	var found *eventsv1.CanonicalUpsertedV1
	for _, o := range list.Contents {
		body := getObj(t, mc.Client, mc.Bucket, *o.Key)
		rows, err := eventlog.ReadParquet[eventsv1.CanonicalUpsertedV1](body)
		require.NoError(t, err)
		for _, r := range rows {
			if r.ClusterID == "c1abcdef" {
				found = &r
			}
		}
	}
	require.NotNil(t, found)
	require.Equal(t, "Newer", found.Title, "day2 row wins")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./apps/writer/service -run TestCompactDaily -v`
Expected: FAIL with `undefined: CompactDailyInput`.

- [ ] **Step 3: Implement `CompactDaily`**

Add to `apps/writer/service/compact.go`:

```go
// CompactDailyInput scopes one daily rebuild.
type CompactDailyInput struct {
	Collection string // must be a collection that has a _current/ counterpart
}

// CompactDailyResult reports counters for logging.
type CompactDailyResult struct {
	RowsBefore int
	RowsAfter  int
	Buckets    int
}

// CompactDaily rebuilds <collection>_current/ from the full raw
// <collection>/dt=*/ partitions. One file per bucket prefix. Latest
// occurred_at per business key wins; prior content under
// <collection>_current/ is replaced (list-and-delete before upload).
func (c *Compactor) CompactDaily(ctx context.Context, in CompactDailyInput) (CompactDailyResult, error) {
	switch in.Collection {
	case "canonicals":
		return compactDailyCanonicals(ctx, c)
	case "embeddings":
		return compactDailyEmbeddings(ctx, c)
	case "translations":
		return compactDailyTranslations(ctx, c)
	case "candidates_cv":
		return compactDailyCVs(ctx, c)
	case "candidates_embeddings":
		return compactDailyCandidateEmbeddings(ctx, c)
	case "candidates_preferences":
		return compactDailyPreferences(ctx, c)
	default:
		return CompactDailyResult{}, fmt.Errorf("compact daily: %q has no _current partition", in.Collection)
	}
}

func compactDailyCanonicals(ctx context.Context, c *Compactor) (CompactDailyResult, error) {
	return compactDailyGeneric[eventsv1.CanonicalUpsertedV1](ctx, c,
		"canonicals",         // raw
		"canonicals_current", // current
		func(r eventsv1.CanonicalUpsertedV1) (bkey string, bucket string, occ time.Time) {
			return r.ClusterID, "cc=" + first2(r.ClusterID), r.OccurredAt.UTC()
		})
}
func compactDailyEmbeddings(ctx context.Context, c *Compactor) (CompactDailyResult, error) {
	return compactDailyGeneric[eventsv1.EmbeddingV1](ctx, c,
		"embeddings", "embeddings_current",
		func(r eventsv1.EmbeddingV1) (string, string, time.Time) {
			return r.CanonicalID, "cc=" + first2(r.CanonicalID), r.OccurredAt.UTC()
		})
}
func compactDailyTranslations(ctx context.Context, c *Compactor) (CompactDailyResult, error) {
	return compactDailyGeneric[eventsv1.TranslationV1](ctx, c,
		"translations", "translations_current",
		func(r eventsv1.TranslationV1) (string, string, time.Time) {
			return r.CanonicalID + "|" + r.Lang,
				"cc=" + first2(r.CanonicalID) + "/lang=" + r.Lang,
				r.OccurredAt.UTC()
		})
}
func compactDailyCVs(ctx context.Context, c *Compactor) (CompactDailyResult, error) {
	return compactDailyGeneric[eventsv1.CVExtractedV1](ctx, c,
		"candidates_cv", "candidates_cv_current",
		func(r eventsv1.CVExtractedV1) (string, string, time.Time) {
			return r.CandidateID, "cnd=" + first2(r.CandidateID), r.OccurredAt.UTC()
		})
}
func compactDailyCandidateEmbeddings(ctx context.Context, c *Compactor) (CompactDailyResult, error) {
	return compactDailyGeneric[eventsv1.CandidateEmbeddingV1](ctx, c,
		"candidates_embeddings", "candidates_embeddings_current",
		func(r eventsv1.CandidateEmbeddingV1) (string, string, time.Time) {
			return r.CandidateID, "cnd=" + first2(r.CandidateID), r.OccurredAt.UTC()
		})
}
func compactDailyPreferences(ctx context.Context, c *Compactor) (CompactDailyResult, error) {
	return compactDailyGeneric[eventsv1.PreferencesUpdatedV1](ctx, c,
		"candidates_preferences", "candidates_preferences_current",
		func(r eventsv1.PreferencesUpdatedV1) (string, string, time.Time) {
			return r.CandidateID, "cnd=" + first2(r.CandidateID), r.OccurredAt.UTC()
		})
}

// bucketFn returns the business key, the bucket sub-path, and the
// occurred_at used for latest-wins conflict resolution.
type bucketFn[T any] func(T) (bkey string, bucket string, occurredAt time.Time)

func compactDailyGeneric[T any](
	ctx context.Context,
	c *Compactor,
	rawCollection, currentCollection string,
	bf bucketFn[T],
) (CompactDailyResult, error) {
	rawPrefix := rawCollection + "/"

	var keys []string
	cursor := ""
	for {
		page, err := c.reader.ListNewObjects(ctx, rawPrefix, cursor, 1000)
		if err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: list %q: %w", rawPrefix, err)
		}
		if len(page) == 0 {
			break
		}
		for _, o := range page {
			if o.Key != nil {
				keys = append(keys, *o.Key)
			}
		}
		cursor = *page[len(page)-1].Key
	}
	if len(keys) == 0 {
		return CompactDailyResult{}, nil
	}

	// Fold: latest per business key, bucketed for output.
	latest := map[string]T{}
	latestAt := map[string]time.Time{}
	buckets := map[string][]string{} // bkey -> its bucket (stable)

	var rowsBefore int
	for _, k := range keys {
		body, err := c.reader.Get(ctx, k)
		if err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: get %q: %w", k, err)
		}
		rows, err := eventlog.ReadParquet[T](body)
		if err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: read %q: %w", k, err)
		}
		rowsBefore += len(rows)
		for _, r := range rows {
			bkey, bucket, occ := bf(r)
			if bkey == "" {
				continue
			}
			if prev, ok := latestAt[bkey]; !ok || occ.After(prev) {
				latest[bkey] = r
				latestAt[bkey] = occ
				buckets[bucket] = appendUnique(buckets[bucket], bkey)
			}
		}
	}

	// Delete prior <current>/ content. We overwrite; no incremental
	// merging here because this job runs once a day and the raw log
	// is complete.
	if err := c.deletePrefix(ctx, currentCollection+"/"); err != nil {
		return CompactDailyResult{}, err
	}

	// Write one file per bucket.
	for bucket, bkeys := range buckets {
		sort.Strings(bkeys) // deterministic
		out := make([]T, 0, len(bkeys))
		for _, k := range bkeys {
			out = append(out, latest[k])
		}
		body, err := eventlog.WriteParquet(out)
		if err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: write parquet: %w", err)
		}
		key := path.Join(currentCollection, bucket, "current-"+xid.New().String()+".parquet")
		if _, err := c.uploader.Put(ctx, key, body); err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: upload %q: %w", key, err)
		}
	}

	return CompactDailyResult{
		RowsBefore: rowsBefore,
		RowsAfter:  len(latest),
		Buckets:    len(buckets),
	}, nil
}

func (c *Compactor) deletePrefix(ctx context.Context, prefix string) error {
	cursor := ""
	for {
		page, err := c.reader.ListNewObjects(ctx, prefix, cursor, 1000)
		if err != nil {
			return fmt.Errorf("compact daily: list for delete %q: %w", prefix, err)
		}
		if len(page) == 0 {
			return nil
		}
		for _, o := range page {
			if o.Key == nil {
				continue
			}
			_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(c.bucket),
				Key:    o.Key,
			})
			if err != nil {
				return fmt.Errorf("compact daily: delete %q: %w", *o.Key, err)
			}
		}
		cursor = *page[len(page)-1].Key
	}
}

func first2(s string) string {
	if len(s) >= 2 {
		return strings.ToLower(s[:2])
	}
	return "xx"
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./apps/writer/service -run TestCompactDaily -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/writer/service/compact.go apps/writer/service/compact_test.go
git commit -m "feat(writer): daily *_current/ rebuild from raw Parquet partitions"
```

---

## Task 3: Compaction HTTP admin endpoints + wire into `apps/writer`

**Files:**
- Create: `apps/writer/service/compact_admin.go`
- Create: `apps/writer/service/compact_admin_test.go`
- Modify: `apps/writer/cmd/main.go`

Two HTTP handlers fire the compactor from Trustage. Each accepts JSON `{"collection": "canonicals"}` (daily) or `{"collection": "canonicals", "hour": "2026-04-22T13:00:00Z"}` (hourly). Both return a JSON body summarizing counters so the Trustage caller captures them in its run log.

Trustage expects idempotent endpoints — re-running hourly compaction on an already-compacted partition is a no-op (the merged file is just rewritten with the same content under a new xid, and the "source files" listing is empty).

- [ ] **Step 1: Write the failing test**

```go
// apps/writer/service/compact_admin_test.go
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

func TestCompactHourlyHandler_OK(t *testing.T) {
	ctx := context.Background()
	mc := startMinIO(t)
	defer mc.Close()

	uploader := eventlog.NewUploader(mc.Client, mc.Bucket)
	reader := eventlog.NewReader(mc.Client, mc.Bucket)

	// Seed one file under variants/dt=today/src=acme/.
	hour := time.Now().UTC().Truncate(time.Hour)
	body, _ := eventlog.WriteParquet([]eventsv1.VariantIngestedV1{
		{EventID: "v1", VariantID: "var-1", SourceID: "acme", OccurredAt: hour},
	})
	_, err := uploader.Put(ctx, "variants/dt="+hour.Format("2006-01-02")+"/src=acme/part-a.parquet", body)
	require.NoError(t, err)

	c := NewCompactor(mc.Client, reader, uploader, mc.Bucket)
	h := CompactHourlyHandler(c)

	reqBody, _ := json.Marshal(map[string]any{"collection": "variants"})
	req := httptest.NewRequest(http.MethodPost, "/_admin/compact/hourly", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp CompactHourlyResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, 1, resp.RowsBefore)
	require.Equal(t, 1, resp.RowsAfter)
}

func TestCompactDailyHandler_UnknownCollection(t *testing.T) {
	mc := startMinIO(t)
	defer mc.Close()

	c := NewCompactor(mc.Client,
		eventlog.NewReader(mc.Client, mc.Bucket),
		eventlog.NewUploader(mc.Client, mc.Bucket),
		mc.Bucket,
	)
	h := CompactDailyHandler(c)

	reqBody, _ := json.Marshal(map[string]any{"collection": "variants"})
	req := httptest.NewRequest(http.MethodPost, "/_admin/compact/daily", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

var _ = s3.Client{} // silence unused import when MinIO helpers live elsewhere
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./apps/writer/service -run TestCompact.*Handler -v`
Expected: FAIL with `undefined: CompactHourlyHandler`.

- [ ] **Step 3: Implement the handlers**

```go
// apps/writer/service/compact_admin.go
package service

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/pitabwire/util"
)

// CompactHourlyHandler returns an http.HandlerFunc bound to the given
// compactor. Expects a JSON body {"collection":"canonicals","hour":"…"}
// (hour optional — defaults to now).
func CompactHourlyHandler(c *Compactor) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		var body struct {
			Collection string    `json:"collection"`
			Hour       time.Time `json:"hour,omitempty"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if body.Collection == "" {
			http.Error(w, `{"error":"collection required"}`, http.StatusBadRequest)
			return
		}
		if body.Hour.IsZero() {
			body.Hour = time.Now().UTC()
		}

		res, err := c.CompactHourly(ctx, CompactHourlyInput{
			Collection: body.Collection,
			Hour:       body.Hour,
		})
		if err != nil {
			util.Log(ctx).WithError(err).WithField("collection", body.Collection).
				Error("compact hourly failed")
			http.Error(w, `{"error":"compact failed: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}

		util.Log(ctx).WithField("collection", body.Collection).
			WithField("rows_before", res.RowsBefore).
			WithField("rows_after", res.RowsAfter).
			WithField("files_before", res.FilesBefore).
			WithField("files_after", res.FilesAfter).
			Info("compact hourly done")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}

// CompactDailyHandler returns an http.HandlerFunc bound to the given
// compactor. Expects JSON body {"collection":"canonicals"}.
func CompactDailyHandler(c *Compactor) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		var body struct {
			Collection string `json:"collection"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if body.Collection == "" {
			http.Error(w, `{"error":"collection required"}`, http.StatusBadRequest)
			return
		}

		res, err := c.CompactDaily(ctx, CompactDailyInput{Collection: body.Collection})
		if err != nil {
			util.Log(ctx).WithError(err).WithField("collection", body.Collection).
				Error("compact daily failed")
			// Unknown-collection errors are caller errors, not server errors.
			status := http.StatusInternalServerError
			if _, ok := err.(interface{ Caller() bool }); ok {
				status = http.StatusBadRequest
			}
			// The error messages we produce start with "compact daily: …"
			// for config/wire mistakes; pick BadRequest for those.
			if isCallerErr(err.Error()) {
				status = http.StatusBadRequest
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, status)
			return
		}

		util.Log(ctx).WithField("collection", body.Collection).
			WithField("rows_before", res.RowsBefore).
			WithField("rows_after", res.RowsAfter).
			WithField("buckets", res.Buckets).
			Info("compact daily done")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}

func isCallerErr(msg string) bool {
	return len(msg) >= 12 && msg[:12] == "compact dail" &&
		(containsSubstr(msg, "has no _current partition") ||
			containsSubstr(msg, "unknown collection"))
}

func containsSubstr(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./apps/writer/service -run TestCompact.*Handler -v`
Expected: PASS.

- [ ] **Step 5: Wire into `apps/writer/cmd/main.go`**

Find the HTTP mux setup in `apps/writer/cmd/main.go` and add the two routes. The writer already has an `http.ServeMux` for the `/healthz` handler — add after it:

```go
// In apps/writer/cmd/main.go, after the existing mux construction:

import (
    "stawi.opportunities/apps/writer/service"
)

// …

mux.HandleFunc("POST /_admin/compact/hourly", service.CompactHourlyHandler(compactor))
mux.HandleFunc("POST /_admin/compact/daily", service.CompactDailyHandler(compactor))
```

And construct the compactor near where the uploader + reader are already wired:

```go
// Existing wiring (for reference):
//   s3Client, err := eventlog.NewS3Client(ctx, cfg.R2Endpoint, cfg.R2Region, cfg.R2AccessKey, cfg.R2SecretKey)
//   uploader := eventlog.NewUploader(s3Client, cfg.R2Bucket)
// Add:
reader := eventlog.NewReader(s3Client, cfg.R2Bucket)
compactor := service.NewCompactor(s3Client, reader, uploader, cfg.R2Bucket)
```

If `apps/writer/cmd/main.go` currently doesn't build a `reader`, add the import `stawi.opportunities/pkg/eventlog` if missing.

- [ ] **Step 6: Build all writer targets**

Run: `go build ./apps/writer/...`
Expected: success.

- [ ] **Step 7: Commit**

```bash
git add apps/writer/service/compact_admin.go apps/writer/service/compact_admin_test.go apps/writer/cmd/main.go
git commit -m "feat(writer): compact hourly/daily admin endpoints"
```

---

## Task 4: Trustage trigger definitions for compaction + source maintenance

**Files:**
- Create: `definitions/trustage/compact-hourly.json`
- Create: `definitions/trustage/compact-daily.json`
- Create: `definitions/trustage/sources-quality-window-reset.json`
- Create: `definitions/trustage/sources-health-decay.json`

Each trigger is a standalone JSON file following the existing pattern (see `definitions/trustage/scheduler-tick.json`). The hourly compaction trigger fans out across every collection we persist raw — one call per collection. Trustage doesn't have native fan-out, so we list each call as a step inside the same trigger.

The daily compaction trigger runs at 02:00 UTC (per spec §4.2) and only includes the six collections that have `*_current/` counterparts.

Quality-window reset + health-decay are orthogonal source-maintenance hooks. They fire the two admin endpoints added in Task 4 of Phase 4 (marked as deferred). Both endpoints live on `apps/crawler`; the crawler already exposes `/admin/*` under the same pattern as `/admin/scheduler/tick`.

For this plan we assume the admin endpoints on `apps/crawler` already exist as `POST /admin/sources/quality-reset` and `POST /admin/sources/health-decay` — if they don't, the step below inlines a small sub-task to add them.

- [ ] **Step 1: Create `compact-hourly.json`**

```json
{
  "version": "1.0",
  "name": "opportunities.writer.compact.hourly",
  "description": "Hourly: merge small Parquet files in today's partitions across every writer-backed collection and dedup by event_id. Runs against apps/writer's /_admin/compact/hourly endpoint once per collection.",
  "schedule": { "cron": "5 * * * *", "active": true },
  "input": {},
  "config": {},
  "timeout": "15m",
  "on_error": { "action": "abort" },
  "steps": [
    { "id": "variants",                "type": "call", "name": "compact variants",                "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "variants" } },
        "output_var": "r_variants" } },
    { "id": "canonicals",              "type": "call", "name": "compact canonicals",              "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "canonicals" } },
        "output_var": "r_canonicals" } },
    { "id": "embeddings",              "type": "call", "name": "compact embeddings",              "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "embeddings" } },
        "output_var": "r_embeddings" } },
    { "id": "translations",            "type": "call", "name": "compact translations",            "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "translations" } },
        "output_var": "r_translations" } },
    { "id": "published",               "type": "call", "name": "compact published",               "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "published" } },
        "output_var": "r_published" } },
    { "id": "crawl_page_completed",    "type": "call", "name": "compact crawl_page_completed",    "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "crawl_page_completed" } },
        "output_var": "r_crawl_page" } },
    { "id": "candidates_cv",           "type": "call", "name": "compact candidates_cv",           "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "candidates_cv" } },
        "output_var": "r_cv" } },
    { "id": "candidates_improvements", "type": "call", "name": "compact candidates_improvements", "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "candidates_improvements" } },
        "output_var": "r_improvements" } },
    { "id": "candidates_preferences",  "type": "call", "name": "compact candidates_preferences",  "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "candidates_preferences" } },
        "output_var": "r_prefs" } },
    { "id": "candidates_embeddings",   "type": "call", "name": "compact candidates_embeddings",   "timeout": "5m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/hourly",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "candidates_embeddings" } },
        "output_var": "r_cv_emb" } }
  ]
}
```

- [ ] **Step 2: Create `compact-daily.json`**

```json
{
  "version": "1.0",
  "name": "opportunities.writer.compact.daily",
  "description": "Daily at 02:00 UTC: rebuild *_current/ partitions from the full raw log for every collection that has a current view. Runs against apps/writer's /_admin/compact/daily endpoint once per collection.",
  "schedule": { "cron": "0 2 * * *", "active": true },
  "input": {},
  "config": {},
  "timeout": "2h",
  "on_error": { "action": "abort" },
  "steps": [
    { "id": "canonicals",             "type": "call", "name": "rebuild canonicals_current",             "timeout": "30m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/daily",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "canonicals" } },
        "output_var": "r_canonicals" } },
    { "id": "embeddings",             "type": "call", "name": "rebuild embeddings_current",             "timeout": "30m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/daily",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "embeddings" } },
        "output_var": "r_embeddings" } },
    { "id": "translations",           "type": "call", "name": "rebuild translations_current",           "timeout": "30m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/daily",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "translations" } },
        "output_var": "r_translations" } },
    { "id": "candidates_cv",          "type": "call", "name": "rebuild candidates_cv_current",          "timeout": "30m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/daily",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "candidates_cv" } },
        "output_var": "r_cv" } },
    { "id": "candidates_embeddings",  "type": "call", "name": "rebuild candidates_embeddings_current",  "timeout": "30m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/daily",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "candidates_embeddings" } },
        "output_var": "r_cv_emb" } },
    { "id": "candidates_preferences", "type": "call", "name": "rebuild candidates_preferences_current", "timeout": "30m",
      "call": { "action": "http.request",
        "input": { "url": "http://opportunities-writer.opportunities.svc/_admin/compact/daily",
                   "method": "POST", "headers": { "Content-Type": "application/json" },
                   "body": { "collection": "candidates_preferences" } },
        "output_var": "r_prefs" } }
  ]
}
```

- [ ] **Step 3: Create `sources-quality-window-reset.json`**

```json
{
  "version": "1.0",
  "name": "opportunities.sources.quality-window-reset",
  "description": "Weekly on Mondays 03:00 UTC: reset the per-source 7-day quality window counters on apps/crawler. Moves the 'window_*' fields to zero and marks the reset_at timestamp.",
  "schedule": { "cron": "0 3 * * 1", "active": true },
  "input": {},
  "config": {},
  "timeout": "5m",
  "on_error": { "action": "abort" },
  "steps": [
    {
      "id": "reset",
      "type": "call",
      "name": "reset quality window",
      "timeout": "3m",
      "retry": { "max_attempts": 3, "backoff_strategy": "exponential", "initial_backoff": "30s" },
      "call": {
        "action": "http.request",
        "input": {
          "url": "http://opportunities-crawler.opportunities.svc/admin/sources/quality-reset",
          "method": "POST",
          "headers": { "Content-Type": "application/json" },
          "body": {}
        },
        "output_var": "result"
      }
    }
  ]
}
```

- [ ] **Step 4: Create `sources-health-decay.json`**

```json
{
  "version": "1.0",
  "name": "opportunities.sources.health-decay",
  "description": "Hourly: nudge every active source's health_score back toward 1.0. A source without fresh failures heals over time even if nothing crawls it; the decay rate matches the exponential recovery shape used by the crawler's page-completed handler.",
  "schedule": { "cron": "15 * * * *", "active": true },
  "input": {},
  "config": {},
  "timeout": "10m",
  "on_error": { "action": "abort" },
  "steps": [
    {
      "id": "decay",
      "type": "call",
      "name": "health decay pass",
      "timeout": "5m",
      "retry": { "max_attempts": 3, "backoff_strategy": "exponential", "initial_backoff": "30s" },
      "call": {
        "action": "http.request",
        "input": {
          "url": "http://opportunities-crawler.opportunities.svc/admin/sources/health-decay",
          "method": "POST",
          "headers": { "Content-Type": "application/json" },
          "body": {}
        },
        "output_var": "result"
      }
    }
  ]
}
```

- [ ] **Step 5: Add the two crawler admin endpoints referenced above**

The trigger JSONs expect `POST /admin/sources/quality-reset` and `POST /admin/sources/health-decay` on `apps/crawler`. If these don't already exist (grep `apps/crawler/service/*.go` for `quality-reset` and `health-decay`), add them as small handlers.

```go
// apps/crawler/service/source_maintenance.go (new file)
package service

import (
	"encoding/json"
	"net/http"

	"github.com/pitabwire/util"

	"stawi.opportunities/pkg/repository"
)

// QualityResetHandler zeros the rolling quality-window counters on
// every active source. Idempotent.
func QualityResetHandler(sourceRepo *repository.SourceRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		n, err := sourceRepo.ResetQualityWindow(ctx)
		if err != nil {
			util.Log(ctx).WithError(err).Error("quality-reset failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "updated": n})
	}
}

// HealthDecayHandler nudges every source's health_score toward 1.0 by
// a small step. Safe to run hourly.
func HealthDecayHandler(sourceRepo *repository.SourceRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		n, err := sourceRepo.DecayHealth(ctx, 0.05)
		if err != nil {
			util.Log(ctx).WithError(err).Error("health-decay failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "updated": n})
	}
}
```

Corresponding repository methods (add to `pkg/repository/source.go`):

```go
// ResetQualityWindow zeroes the rolling quality counters on every
// active source. Returns the number of rows updated.
func (r *SourceRepository) ResetQualityWindow(ctx context.Context) (int64, error) {
	res := r.db(ctx, false).
		Model(&domain.Source{}).
		Where("status = ?", "active").
		Updates(map[string]any{
			"quality_window_requests":  0,
			"quality_window_successes": 0,
			"quality_window_reset_at":  time.Now().UTC(),
		})
	return res.RowsAffected, res.Error
}

// DecayHealth nudges health_score toward 1.0 by `step`, clamped to
// [0, 1]. Skips sources already at 1.0.
func (r *SourceRepository) DecayHealth(ctx context.Context, step float64) (int64, error) {
	// SQL: UPDATE sources SET health_score = LEAST(1.0, health_score + $1)
	//      WHERE health_score < 1.0 AND status='active'
	res := r.db(ctx, false).
		Model(&domain.Source{}).
		Where("status = ? AND health_score < 1.0", "active").
		Update("health_score", gorm.Expr("LEAST(1.0, health_score + ?)", step))
	return res.RowsAffected, res.Error
}
```

Wire both handlers into `apps/crawler/cmd/main.go` alongside the existing `/admin/scheduler/tick` mounting.

- [ ] **Step 6: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 7: Commit**

```bash
git add definitions/trustage/compact-*.json definitions/trustage/sources-*.json \
        apps/crawler/service/source_maintenance.go apps/crawler/cmd/main.go \
        pkg/repository/source.go
git commit -m "feat(ops): trustage triggers for compaction + source quality/health maintenance"
```

---

## Task 5: Full `backpressure.Gate` policy — drain-time + HPA-ceiling awareness

**Files:**
- Modify: `pkg/backpressure/gate.go`
- Modify: `pkg/backpressure/gate_test.go`

Phase 4 shipped a `Admit` stub: grants `want` if the hysteresis gate is open, `0` if it's closed. The design (§8.3) calls for a per-topic policy with `MaxDrainTime` (throttle begins here) and `HardCeilingDrain` (admit=0 here), plus HPA-ceiling awareness so the scheduler keeps throttling even if drain time is fine but the downstream pool is already scaled to ceiling and can't absorb more load.

The Phase 4 signature is preserved. New methods: `UpdateLag(topic, depth, rate)` and `Config(topic, policy)`. Internal state: a map of topic → (latest depth, latest consume rate, policy).

Drain time = depth / rate. The gate grants `want` fully when drain < MaxDrainTime and HPA isn't at ceiling. Between MaxDrainTime and HardCeilingDrain it grants a fraction linearly interpolated (100% at MaxDrainTime → 0% at HardCeilingDrain). At or above HardCeilingDrain it grants 0.

If HPA is known to be saturated (`HPACeilingKnown=true` and the caller sets the per-topic flag via `UpdateLag(.., hpaAtCeiling=true)`) we collapse the two thresholds to just MaxDrainTime — the "we can grow more pods" grace period vanishes.

The wait hint becomes `drainTime - MaxDrainTime`, clamped to `[0, 5 min]`.

- [ ] **Step 1: Write the failing test**

Append to `pkg/backpressure/gate_test.go`:

```go
func TestAdmit_FullPolicy(t *testing.T) {
	g := New(Config{
		MonitorURL: "", // disabled; we drive state manually via UpdateLag
	}, nil)

	g.ConfigTopic("jobs.variants.validated.v1", Policy{
		MaxDrainTime:     15 * time.Minute,
		HardCeilingDrain: 30 * time.Minute,
		HPACeilingKnown:  true,
	})

	// Fast drain: full grant.
	g.UpdateLag("jobs.variants.validated.v1", 1000, 100.0, false) // 10s drain
	got, wait := g.Admit(context.Background(), "jobs.variants.validated.v1", 50)
	require.Equal(t, 50, got)
	require.Zero(t, wait)

	// Between the two thresholds, HPA not at ceiling — 50% of the way
	// through the throttle window should cut the grant roughly in half.
	// drain_time = (15m + 30m) / 2 = 22.5m, so fraction ≈ 0.5
	g.UpdateLag("jobs.variants.validated.v1", 1000, 1000.0/(22.5*60), false)
	got, wait = g.Admit(context.Background(), "jobs.variants.validated.v1", 100)
	require.InDelta(t, 50, got, 5, "expected ~50 of 100 granted mid-throttle")
	require.Greater(t, wait, time.Duration(0))

	// At hard ceiling — admit=0.
	g.UpdateLag("jobs.variants.validated.v1", 10_000, 1.0, false) // 10k seconds drain = 2.7h
	got, wait = g.Admit(context.Background(), "jobs.variants.validated.v1", 100)
	require.Equal(t, 0, got)
	require.Greater(t, wait, time.Duration(0))

	// HPA at ceiling → collapse window: any drain above MaxDrainTime is 0.
	g.UpdateLag("jobs.variants.validated.v1", 1200, 1.0, true) // 1200s drain, hpa saturated
	got, wait = g.Admit(context.Background(), "jobs.variants.validated.v1", 100)
	require.Equal(t, 0, got)
	require.Greater(t, wait, time.Duration(0))
}

func TestAdmit_UnconfiguredTopic_FailOpen(t *testing.T) {
	g := New(Config{MonitorURL: ""}, nil)

	// No policy, no lag info → fail open (grant full).
	got, wait := g.Admit(context.Background(), "jobs.variants.ingested.v1", 42)
	require.Equal(t, 42, got)
	require.Zero(t, wait)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/backpressure -run TestAdmit_FullPolicy -v`
Expected: FAIL — `undefined: ConfigTopic` and `UpdateLag` arity mismatch.

- [ ] **Step 3: Extend the gate**

Edit `pkg/backpressure/gate.go`. Keep the existing `Gate` struct; add policy state alongside the existing hysteresis fields:

```go
// Policy is the per-topic admission policy used by Admit.
type Policy struct {
	// MaxDrainTime is the drain time at which throttling begins. Below
	// this, Admit grants the full want. At this exact value the
	// throttle fraction is 1.0 (no throttling yet).
	MaxDrainTime time.Duration

	// HardCeilingDrain is the drain time at which Admit grants 0. Must
	// be > MaxDrainTime (enforced; a degenerate config falls back to
	// MaxDrainTime + 15 min).
	HardCeilingDrain time.Duration

	// HPACeilingKnown says whether an external observer is providing
	// "hpa at ceiling" signals via UpdateLag. If false, we never
	// collapse the window and treat every UpdateLag as hpaAtCeiling=false.
	HPACeilingKnown bool
}

type topicLag struct {
	depth         int64
	consumeRate   float64
	hpaAtCeiling  bool
	updatedAt     time.Time
}

// Extend Gate with topic state (append to existing struct):
//   policies map[string]Policy
//   lags     map[string]topicLag
//   policyMu sync.RWMutex
```

Implement the new methods:

```go
// ConfigTopic sets the per-topic policy. Safe to call concurrently
// with Admit; the call is O(1) under the write lock.
func (g *Gate) ConfigTopic(topic string, p Policy) {
	g.policyMu.Lock()
	defer g.policyMu.Unlock()
	if g.policies == nil {
		g.policies = make(map[string]Policy)
	}
	if p.MaxDrainTime <= 0 {
		p.MaxDrainTime = 15 * time.Minute
	}
	if p.HardCeilingDrain <= p.MaxDrainTime {
		p.HardCeilingDrain = p.MaxDrainTime + 15*time.Minute
	}
	g.policies[topic] = p
}

// UpdateLag pushes current stats for `topic`. Called by a telemetry
// collector (Prometheus scraper or Frame pubsub hook). `hpaAtCeiling`
// is only consulted when the policy has HPACeilingKnown=true.
func (g *Gate) UpdateLag(topic string, depth int64, consumeRate float64, hpaAtCeiling bool) {
	g.policyMu.Lock()
	defer g.policyMu.Unlock()
	if g.lags == nil {
		g.lags = make(map[string]topicLag)
	}
	g.lags[topic] = topicLag{
		depth:        depth,
		consumeRate:  consumeRate,
		hpaAtCeiling: hpaAtCeiling,
		updatedAt:    time.Now(),
	}
}

// Replace the Phase 4 Admit body with the full policy.
func (g *Gate) Admit(ctx context.Context, topic string, want int) (int, time.Duration) {
	if want <= 0 {
		return 0, 0
	}

	g.policyMu.RLock()
	pol, hasPol := g.policies[topic]
	lag, hasLag := g.lags[topic]
	g.policyMu.RUnlock()

	// Back-compat: if the gate was constructed with a MonitorURL + no
	// per-topic policy/lag, fall back to the hysteresis behaviour.
	if !hasPol || !hasLag {
		if g == nil || g.monitorURL == "" {
			return want, 0 // no info → fail open
		}
		state, err := g.Check(ctx)
		if err != nil || !state.Paused {
			return want, 0
		}
		return 0, 30 * time.Second
	}

	if lag.consumeRate <= 0 {
		// No consumers at all — we don't know drain time. Grant full
		// so we don't indefinitely wedge ourselves waiting for a
		// telemetry update.
		return want, 0
	}

	drain := time.Duration(float64(lag.depth) / lag.consumeRate * float64(time.Second))

	// Effective window. If HPACeilingKnown + hpaAtCeiling, the soft
	// throttle window collapses: anything above MaxDrainTime is 0.
	maxDrain := pol.MaxDrainTime
	hardDrain := pol.HardCeilingDrain
	if pol.HPACeilingKnown && lag.hpaAtCeiling {
		hardDrain = maxDrain
	}

	switch {
	case drain < maxDrain:
		return want, 0
	case drain >= hardDrain:
		return 0, clampWait(drain - maxDrain)
	default:
		// Linear interpolation between 100% at maxDrain and 0% at hardDrain.
		span := float64(hardDrain - maxDrain)
		over := float64(drain - maxDrain)
		fraction := 1.0 - (over / span)
		granted := int(float64(want) * fraction)
		if granted < 0 {
			granted = 0
		}
		if granted > want {
			granted = want
		}
		return granted, clampWait(drain - maxDrain)
	}
}

func clampWait(d time.Duration) time.Duration {
	const max = 5 * time.Minute
	switch {
	case d <= 0:
		return 0
	case d > max:
		return max
	default:
		return d
	}
}
```

- [ ] **Step 4: Run tests to verify**

Run: `go test ./pkg/backpressure -v`
Expected: all tests pass, including pre-existing hysteresis tests (back-compat path above preserves them).

- [ ] **Step 5: Seed default policies in `apps/crawler/cmd/main.go`**

After the gate is constructed, configure the topics the scheduler emits to. The worker input topics are the right targets since those are what get deep under load:

```go
// In apps/crawler/cmd/main.go, after backpressure.New(…):
gate.ConfigTopic("jobs.variants.ingested.v1", backpressure.Policy{
    MaxDrainTime:     15 * time.Minute,
    HardCeilingDrain: 45 * time.Minute,
    HPACeilingKnown:  true,
})
gate.ConfigTopic("jobs.variants.normalized.v1", backpressure.Policy{
    MaxDrainTime:     15 * time.Minute,
    HardCeilingDrain: 45 * time.Minute,
    HPACeilingKnown:  true,
})
gate.ConfigTopic("jobs.variants.validated.v1", backpressure.Policy{
    MaxDrainTime:     15 * time.Minute,
    HardCeilingDrain: 45 * time.Minute,
    HPACeilingKnown:  true,
})
gate.ConfigTopic("jobs.variants.clustered.v1", backpressure.Policy{
    MaxDrainTime:     15 * time.Minute,
    HardCeilingDrain: 45 * time.Minute,
    HPACeilingKnown:  true,
})
gate.ConfigTopic("jobs.canonicals.upserted.v1", backpressure.Policy{
    MaxDrainTime:     15 * time.Minute,
    HardCeilingDrain: 45 * time.Minute,
    HPACeilingKnown:  true,
})
```

The `UpdateLag` side is fed from a small telemetry collector that polls the NATS `/jsz` endpoint the same way `readPending` does. For v1 we do a minimal version: an in-process goroutine in `apps/crawler/cmd/main.go` that polls `/jsz` every 10 s and calls `UpdateLag` per topic.

```go
// apps/crawler/cmd/main.go — near the end of main(), before the server loop:
go func() {
    t := time.NewTicker(10 * time.Second)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            for _, topic := range gatedTopics() {
                depth, rate, err := queryLag(ctx, cfg.NATSMonitorURL, cfg.NATSStream, topicToConsumer(topic))
                if err != nil {
                    continue // silent: next tick retries
                }
                gate.UpdateLag(topic, depth, rate, false /* HPA ceiling: wire later */)
            }
        }
    }
}()
```

Where `queryLag` reuses the existing `parsePending` helper (expose it by making it package-level or by adding a thin `Gate.Depth(topic)` accessor). `gatedTopics()` lists the five topics configured above. `topicToConsumer` is a static map (`"jobs.variants.validated.v1" → "worker-validate"` etc.) that should be surfaced from `apps/worker`'s Frame subscription registration — but for v1 we hardcode it here.

Consumer-rate calculation is trickier since `/jsz` doesn't expose it directly. For v1 we derive it from depth deltas: `rate = max(0, (depth_prev - depth_now) / interval_seconds + ingress_rate_estimate)`. A cruder but working approximation is to assume `rate = depth / MaxDrainTime * 2` (so the gate is conservative and throttles early when rate is unknown). Pick the delta-based approach; the crude fallback is noted in a comment for future revisit.

```go
// Add in apps/crawler/service/gate_lag.go
package service

import (
    "context"
    "sync"
    "time"
)

type LagPoller struct {
    mu       sync.Mutex
    lastDepth map[string]int64
    lastAt   map[string]time.Time
}

func NewLagPoller() *LagPoller {
    return &LagPoller{lastDepth: map[string]int64{}, lastAt: map[string]time.Time{}}
}

// Sample returns a (depth, rate) pair for `topic` given the current
// measured depth. Rate is derived from the previous sample; first
// sample returns rate=0 which the gate interprets as "no info, fail
// open" — the second tick onwards has real rate data.
func (p *LagPoller) Sample(topic string, depth int64, now time.Time) (int64, float64) {
    p.mu.Lock()
    defer p.mu.Unlock()
    prev, ok := p.lastDepth[topic]
    prevAt := p.lastAt[topic]
    p.lastDepth[topic] = depth
    p.lastAt[topic] = now
    if !ok || prevAt.IsZero() {
        return depth, 0
    }
    dt := now.Sub(prevAt).Seconds()
    if dt <= 0 {
        return depth, 0
    }
    // rate = how many events drained per second (negative deltas mean
    // faster drain). Clamp to [0.01, +inf) so drain_time doesn't blow
    // up on idle topics.
    consumed := float64(prev-depth) / dt
    if consumed < 0.01 {
        consumed = 0.01
    }
    return depth, consumed
}
```

- [ ] **Step 6: Commit**

```bash
git add pkg/backpressure/gate.go pkg/backpressure/gate_test.go \
        apps/crawler/cmd/main.go apps/crawler/service/gate_lag.go
git commit -m "feat(backpressure): per-topic drain-time admit policy + HPA-ceiling awareness"
```

---

## Task 6: KV rebuild admin endpoint on `apps/worker`

**Files:**
- Create: `apps/worker/service/kv_rebuild.go`
- Create: `apps/worker/service/kv_rebuild_test.go`
- Create: `apps/worker/service/kv_rebuild_admin.go`
- Modify: `apps/worker/cmd/main.go`

Spec §9.4: when Valkey loses its replica and comes back empty, the pipeline dedup lookups miss on every variant, clusters explode, and serving briefly shows duplicates until hourly compaction re-merges. The rebuild admin endpoint shortens that window: scan `canonicals_current/` in R2, for each row `SET dedup:{hard_key} := cluster_id`, `SET cluster:{cluster_id} := snapshot`, and update the bloom filter. Minutes, not hours.

The worker already has a `dedup.KV` handle configured in its service. We add `KVRebuilder` that iterates `canonicals_current/` using the same `eventlog.Reader` the materializer uses.

- [ ] **Step 1: Write the failing test**

```go
// apps/worker/service/kv_rebuild_test.go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

func TestKVRebuild_Run(t *testing.T) {
	ctx := context.Background()
	mc := startMinIO(t) // shared helper in this package's test files
	defer mc.Close()
	kv := startValkey(t) // likewise
	defer kv.Close()

	uploader := eventlog.NewUploader(mc.Client, mc.Bucket)
	reader := eventlog.NewReader(mc.Client, mc.Bucket)

	// Seed two canonicals in canonicals_current/.
	rows := []eventsv1.CanonicalUpsertedV1{
		{EventID: "e1", ClusterID: "c1", CanonicalID: "can-1",
			MergeSourceVariantIDs: []string{"var-1"}, HardKey: "hk-1",
			OccurredAt: time.Now().UTC()},
		{EventID: "e2", ClusterID: "c2", CanonicalID: "can-2",
			MergeSourceVariantIDs: []string{"var-2"}, HardKey: "hk-2",
			OccurredAt: time.Now().UTC()},
	}
	body, err := eventlog.WriteParquet(rows)
	require.NoError(t, err)
	_, err = uploader.Put(ctx, "canonicals_current/cc=c1/current.parquet", body)
	require.NoError(t, err)

	r := NewKVRebuilder(reader, kv.Client)
	res, err := r.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, res.Rows)
	require.Equal(t, 2, res.DedupKeysSet)
	require.Equal(t, 2, res.ClusterKeysSet)

	// Verify KV has the keys.
	val, err := kv.Client.Get(ctx, "dedup:hk-1").Result()
	require.NoError(t, err)
	require.Equal(t, "c1", val)
	val, err = kv.Client.Get(ctx, "dedup:hk-2").Result()
	require.NoError(t, err)
	require.Equal(t, "c2", val)
}
```

If `startValkey` doesn't already exist in this package, add it (testcontainers-go convention):

```go
// apps/worker/service/test_helpers_valkey_test.go (if not present)
package service

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type valkeyHarness struct {
	Client    *redis.Client
	container testcontainers.Container
}

func (h *valkeyHarness) Close() { _ = h.container.Terminate(context.Background()) }

func startValkey(t *testing.T) *valkeyHarness {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "valkey/valkey:7.2",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	require.NoError(t, err)

	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "6379")
	client := redis.NewClient(&redis.Options{Addr: host + ":" + port.Port()})
	require.NoError(t, client.Ping(ctx).Err())
	return &valkeyHarness{Client: client, container: c}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./apps/worker/service -run TestKVRebuild -v`
Expected: FAIL with `undefined: NewKVRebuilder`.

- [ ] **Step 3: Implement the rebuilder**

```go
// apps/worker/service/kv_rebuild.go
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/pitabwire/util"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

// KVRebuilder repopulates Valkey dedup + cluster keys from R2
// canonicals_current/ Parquet. Called after a Valkey replica loss.
type KVRebuilder struct {
	reader *eventlog.Reader
	kv     *redis.Client
}

// NewKVRebuilder wires the rebuilder.
func NewKVRebuilder(reader *eventlog.Reader, kv *redis.Client) *KVRebuilder {
	return &KVRebuilder{reader: reader, kv: kv}
}

// KVRebuildResult reports counters for logging + response body.
type KVRebuildResult struct {
	Rows           int `json:"rows"`
	DedupKeysSet   int `json:"dedup_keys_set"`
	ClusterKeysSet int `json:"cluster_keys_set"`
	Files          int `json:"files_scanned"`
}

// Run scans every file under canonicals_current/ and repopulates
// dedup:{hard_key} := cluster_id and cluster:{cluster_id} := snapshot
// (stored as the canonical's JSON for v1 simplicity). Idempotent.
func (r *KVRebuilder) Run(ctx context.Context) (KVRebuildResult, error) {
	var res KVRebuildResult

	cursor := ""
	for {
		page, err := r.reader.ListNewObjects(ctx, "canonicals_current/", cursor, 1000)
		if err != nil {
			return res, fmt.Errorf("kv rebuild: list: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, o := range page {
			if o.Key == nil {
				continue
			}
			body, err := r.reader.Get(ctx, *o.Key)
			if err != nil {
				return res, fmt.Errorf("kv rebuild: get %q: %w", *o.Key, err)
			}
			rows, err := eventlog.ReadParquet[eventsv1.CanonicalUpsertedV1](body)
			if err != nil {
				return res, fmt.Errorf("kv rebuild: parquet %q: %w", *o.Key, err)
			}
			res.Files++
			res.Rows += len(rows)

			pipe := r.kv.Pipeline()
			for _, row := range rows {
				if row.HardKey != "" && row.ClusterID != "" {
					pipe.Set(ctx, "dedup:"+row.HardKey, row.ClusterID, 0)
					res.DedupKeysSet++
				}
				if row.ClusterID != "" {
					// Snapshot: canonical JSON bytes. Phase 5/6 consumers
					// decode on read; keeping canonical form here means a
					// worker pod sees the same shape whether the key was
					// populated by the rebuilder or by a live canonical
					// merge.
					snap, err := marshalCanonicalSnapshot(row)
					if err == nil {
						pipe.Set(ctx, "cluster:"+row.ClusterID, snap, 0)
						res.ClusterKeysSet++
					}
				}
			}
			if _, err := pipe.Exec(ctx); err != nil {
				return res, fmt.Errorf("kv rebuild: pipe exec: %w", err)
			}
		}
		cursor = *page[len(page)-1].Key
	}

	util.Log(ctx).WithField("rows", res.Rows).
		WithField("dedup_keys", res.DedupKeysSet).
		WithField("cluster_keys", res.ClusterKeysSet).
		WithField("files", res.Files).
		Info("kv rebuild complete")
	return res, nil
}

// marshalCanonicalSnapshot serialises the minimal fields used by the
// canonical merge handler. JSON is fine here — fast enough at v1 scale
// and debuggable via `valkey-cli GET cluster:<id>`.
func marshalCanonicalSnapshot(row eventsv1.CanonicalUpsertedV1) ([]byte, error) {
	snap := map[string]any{
		"canonical_id":  row.CanonicalID,
		"cluster_id":    row.ClusterID,
		"title":         row.Title,
		"company":       row.Company,
		"country":       row.Country,
		"remote_type":   row.RemoteType,
		"quality_score": row.QualityScore,
		"last_seen_at":  row.LastSeenAt,
		"updated_at":    time.Now().UTC(),
	}
	return jsonMarshal(snap)
}

// Indirection through a package-private helper so test code can swap
// in a deterministic encoder. Default implementation uses encoding/json.
var jsonMarshal = func(v any) ([]byte, error) {
	// Imported implicitly via the stdlib so handlers don't have an
	// otherwise-unused encoding/json import.
	type marshaler interface{ MarshalJSON() ([]byte, error) }
	if m, ok := v.(marshaler); ok {
		return m.MarshalJSON()
	}
	return jsonStdMarshal(v)
}

```

(For readability, keep the `jsonStdMarshal` alias to `encoding/json.Marshal` in a single `encoding.go` or inline the stdlib import directly. The indirection above is overkill for this simple case — feel free to simplify to a direct `json.Marshal` call.)

- [ ] **Step 4: Implement the admin handler**

```go
// apps/worker/service/kv_rebuild_admin.go
package service

import (
	"encoding/json"
	"net/http"

	"github.com/pitabwire/util"
)

// KVRebuildHandler returns an http.HandlerFunc that kicks off a
// Valkey rebuild from R2. Response body = KVRebuildResult counters.
// Safe to invoke concurrently — Valkey pipeline is idempotent.
func KVRebuildHandler(r *KVRebuilder) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		res, err := r.Run(ctx)
		if err != nil {
			util.Log(ctx).WithError(err).Error("kv rebuild failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}
```

- [ ] **Step 5: Wire into `apps/worker/cmd/main.go`**

Mount the admin on the worker's existing HTTP mux (the worker already exposes `/healthz`):

```go
// apps/worker/cmd/main.go — after the mux is built:
import (
    "stawi.opportunities/apps/worker/service"
    "stawi.opportunities/pkg/eventlog"
)

// …

reader := eventlog.NewReader(s3Client, cfg.R2Bucket)
kvRebuilder := service.NewKVRebuilder(reader, kvClient)
mux.HandleFunc("POST /_admin/kv/rebuild", service.KVRebuildHandler(kvRebuilder))
```

Where `kvClient` is the existing `*redis.Client` built for the dedup handler.

- [ ] **Step 6: Run tests**

Run: `go test ./apps/worker/service -run TestKVRebuild -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add apps/worker/service/kv_rebuild.go apps/worker/service/kv_rebuild_admin.go \
        apps/worker/service/kv_rebuild_test.go \
        apps/worker/service/test_helpers_valkey_test.go \
        apps/worker/cmd/main.go
git commit -m "feat(worker): KV rebuild admin endpoint from canonicals_current Parquet"
```

---

## Task 7: `candidatestore.StaleReader` — R2-backed stale-candidate listing

**Files:**
- Create: `pkg/candidatestore/stale_reader.go`
- Create: `pkg/candidatestore/stale_reader_test.go`
- Modify: `apps/candidates/service/admin/v1/stale_lister.go`
- Modify: `apps/candidates/service/admin/v1/stale_lister_test.go`
- Modify: `apps/candidates/cmd/main.go`

Phase 5 shipped `RepoStaleLister` that used `candidates.updated_at` as a proxy for "time of last CV upload" — inaccurate but dependency-light. Now that daily compaction (Task 2) produces `candidates_cv_current/`, we have a real last-upload timestamp per candidate. This task swaps the proxy for the real source.

`StaleReader.ListStale(asOf, cutoff)` lists every candidate whose latest `candidates_cv_current/cnd=<prefix>/…` row has `OccurredAt < cutoff`. `limit` caps the result count.

The reader shares S3 client + bucket with the existing `candidatestore.Reader` from Phase 5 — in fact, we extend that package rather than a separate one.

- [ ] **Step 1: Write the failing test**

```go
// pkg/candidatestore/stale_reader_test.go
package candidatestore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

func TestStaleReader_ListStale(t *testing.T) {
	ctx := context.Background()
	mc := startMinIO(t) // reuse helper from reader_test.go
	defer mc.Close()
	uploader := eventlog.NewUploader(mc.Client, mc.Bucket)

	now := time.Now().UTC()
	cutoff := now.Add(-60 * 24 * time.Hour)

	// candidate "c1" uploaded last week (fresh — not stale).
	// candidate "c2" uploaded 4 months ago (stale).
	// candidate "c3" uploaded 5 minutes ago (fresh).
	rows := []eventsv1.CVExtractedV1{
		{CandidateID: "c1aaaa", OccurredAt: now.Add(-7 * 24 * time.Hour)},
		{CandidateID: "c2bbbb", OccurredAt: now.Add(-120 * 24 * time.Hour)},
		{CandidateID: "c3cccc", OccurredAt: now.Add(-5 * time.Minute)},
	}
	for _, r := range rows {
		body, err := eventlog.WriteParquet([]eventsv1.CVExtractedV1{r})
		require.NoError(t, err)
		key := "candidates_cv_current/cnd=" + r.CandidateID[:2] + "/current-" + r.CandidateID + ".parquet"
		_, err = uploader.Put(ctx, key, body)
		require.NoError(t, err)
	}

	r := NewStaleReader(mc.Client, mc.Bucket)
	stale, err := r.ListStale(ctx, cutoff, 100)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	require.Equal(t, "c2bbbb", stale[0].CandidateID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/candidatestore -run TestStaleReader -v`
Expected: FAIL with `undefined: NewStaleReader`.

- [ ] **Step 3: Implement `StaleReader`**

```go
// pkg/candidatestore/stale_reader.go
package candidatestore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

// StaleCandidate mirrors the admin handler's shape for stale-nudge.
type StaleCandidate struct {
	CandidateID  string
	LastUploadAt time.Time
}

// StaleReader scans candidates_cv_current/ for candidates whose
// latest CV upload occurred_at is older than cutoff. Reads only — no
// writes to R2 or KV.
type StaleReader struct {
	reader *eventlog.Reader
}

// NewStaleReader constructs a reader bound to the shared R2 client.
func NewStaleReader(cli *s3.Client, bucket string) *StaleReader {
	return &StaleReader{reader: eventlog.NewReader(cli, bucket)}
}

// ListStale enumerates candidates whose most recent CV upload is older
// than cutoff. Returns up to `limit` rows sorted ascending by
// LastUploadAt so the oldest get nudged first.
func (r *StaleReader) ListStale(ctx context.Context, cutoff time.Time, limit int) ([]StaleCandidate, error) {
	if limit <= 0 {
		limit = 1000
	}

	var out []StaleCandidate
	seen := map[string]time.Time{}

	cursor := ""
	for {
		page, err := r.reader.ListNewObjects(ctx, "candidates_cv_current/", cursor, 1000)
		if err != nil {
			return nil, fmt.Errorf("stale reader: list: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, o := range page {
			if o.Key == nil {
				continue
			}
			body, err := r.reader.Get(ctx, *o.Key)
			if err != nil {
				return nil, fmt.Errorf("stale reader: get %q: %w", *o.Key, err)
			}
			rows, err := eventlog.ReadParquet[eventsv1.CVExtractedV1](body)
			if err != nil {
				return nil, fmt.Errorf("stale reader: parquet %q: %w", *o.Key, err)
			}
			for _, row := range rows {
				// Multiple rows per candidate shouldn't happen in a
				// properly compacted _current/ but be defensive.
				if prev, ok := seen[row.CandidateID]; !ok || row.OccurredAt.After(prev) {
					seen[row.CandidateID] = row.OccurredAt.UTC()
				}
			}
		}
		cursor = *page[len(page)-1].Key
	}

	for id, at := range seen {
		if at.Before(cutoff) {
			out = append(out, StaleCandidate{CandidateID: id, LastUploadAt: at})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUploadAt.Before(out[j].LastUploadAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/candidatestore -run TestStaleReader -v`
Expected: PASS.

- [ ] **Step 5: Rewire the admin handler to use `StaleReader`**

The Phase 5 `RepoStaleLister` lives in `apps/candidates/service/admin/v1/stale_lister.go` and implements the `StaleLister` interface consumed by `CVStaleNudgeDeps`. Replace its implementation so it delegates to `candidatestore.StaleReader` and the shape remains:

```go
// apps/candidates/service/admin/v1/stale_lister.go (rewrite)
package v1

import (
	"context"
	"time"

	"stawi.opportunities/pkg/candidatestore"
)

// R2StaleLister reads "most recent CV upload" timestamps directly from
// R2 candidates_cv_current/ Parquet instead of Postgres' updated_at
// proxy. Identical public shape as the Phase 5 RepoStaleLister.
type R2StaleLister struct {
	reader *candidatestore.StaleReader
	cutoff time.Duration
	limit  int
}

// NewR2StaleLister constructs a stale-lister. `cutoff` is the
// "considered stale after this long without a new CV" interval
// (default 60 days).
func NewR2StaleLister(reader *candidatestore.StaleReader, cutoff time.Duration, limit int) *R2StaleLister {
	if cutoff <= 0 {
		cutoff = 60 * 24 * time.Hour
	}
	if limit <= 0 {
		limit = 500
	}
	return &R2StaleLister{reader: reader, cutoff: cutoff, limit: limit}
}

// ListStale returns candidates whose most-recent CV upload is older
// than asOf-cutoff. Matches the StaleLister interface Phase 5 declared.
func (l *R2StaleLister) ListStale(ctx context.Context, asOf time.Time) ([]StaleCandidate, error) {
	rows, err := l.reader.ListStale(ctx, asOf.Add(-l.cutoff), l.limit)
	if err != nil {
		return nil, err
	}
	out := make([]StaleCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, StaleCandidate{
			CandidateID:  r.CandidateID,
			LastUploadAt: r.LastUploadAt,
		})
	}
	return out, nil
}
```

Delete the legacy `RepoStaleLister` body from the same file if still present. Update its corresponding test file `stale_lister_test.go` to exercise `R2StaleLister` against a fake `candidatestore.StaleReader`.

```go
// apps/candidates/service/admin/v1/stale_lister_test.go
package v1

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeStaleReader struct{ fixed []StaleCandidate }

func (f fakeStaleReader) ListStale(_ context.Context, _ time.Time, _ int) ([]StaleCandidate, error) {
	return f.fixed, nil
}

// We adapt the fake into *candidatestore.StaleReader by passing it as
// an interface field — R2StaleLister references the concrete type in
// prod, but the ListStale method signature is what matters. To keep
// the test free of MinIO, refactor R2StaleLister to take a small
// interface:
//   type StaleReaderIface interface {
//       ListStale(context.Context, time.Time, int) ([]StaleCandidate, error)
//   }
// and fakeStaleReader satisfies it.
```

Adjust `R2StaleLister` to take the interface rather than the concrete type:

```go
// In stale_lister.go:
type StaleReaderIface interface {
	ListStale(context.Context, time.Time, int) ([]candidatestore.StaleCandidate, error)
}

// Use StaleReaderIface in the struct; the concrete *candidatestore.StaleReader
// satisfies it.
```

- [ ] **Step 6: Wire in `apps/candidates/cmd/main.go`**

Replace the Phase 5 `RepoStaleLister` construction with `R2StaleLister`:

```go
// apps/candidates/cmd/main.go
import "stawi.opportunities/pkg/candidatestore"

staleReader := candidatestore.NewStaleReader(s3Client, cfg.R2Bucket)
staleLister := adminv1.NewR2StaleLister(staleReader, 60*24*time.Hour, 500)

cvStaleNudgeHandler := adminv1.CVStaleNudgeHandler(adminv1.CVStaleNudgeDeps{
    Svc:         svc,
    StaleLister: staleLister,
})
```

- [ ] **Step 7: Run every candidates test**

Run: `go test ./apps/candidates/... -v`
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add pkg/candidatestore/stale_reader.go pkg/candidatestore/stale_reader_test.go \
        apps/candidates/service/admin/v1/stale_lister.go \
        apps/candidates/service/admin/v1/stale_lister_test.go \
        apps/candidates/cmd/main.go
git commit -m "feat(candidates): R2-backed StaleReader replaces updated_at proxy"
```

---

## Task 8: Manticore client helpers for `apps/api`

**Files:**
- Create: `apps/api/cmd/manticore_client.go`
- Create: `apps/api/cmd/manticore_client_test.go`

Phase 2 added `searchV2Handler` (`search_v2.go`) and `pkg/searchindex.Client.Search`. For the full `apps/api` rewrite in subsequent tasks, we need typed helpers that each endpoint can call: `GetByID`, `Count`, `Facets`, `Top` (by quality_score), `Latest` (by posted_at).

Keeping these in `apps/api/cmd/` (package `main`) instead of pushing into `pkg/searchindex` because they're apps/api-specific projections over the raw search client — `pkg/searchindex` stays generic. If a second service later needs the same shapes, promote.

- [ ] **Step 1: Write the failing test**

```go
// apps/api/cmd/manticore_client_test.go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"stawi.opportunities/pkg/searchindex"
)

func TestJobsManticore_GetByID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"total":1,"hits":[{"_id":42,"_score":1,"_source":{"canonical_id":"can-1","slug":"acme-engineer","title":"Engineer","company":"Acme","country":"KE","remote_type":"remote","category":"engineering"}}]}}`))
	}))
	defer ts.Close()

	client, err := searchindex.Open(searchindex.Config{URL: ts.URL})
	require.NoError(t, err)
	jm := newJobsManticore(client)

	job, err := jm.GetByID(context.Background(), "can-1")
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, "acme-engineer", job.Slug)
	require.Equal(t, "Engineer", job.Title)
}

func TestJobsManticore_Count(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"total":1234,"hits":[]}}`))
	}))
	defer ts.Close()

	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)

	n, err := jm.Count(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 1234, n)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./apps/api/cmd -run TestJobsManticore -v`
Expected: FAIL with `undefined: newJobsManticore`.

- [ ] **Step 3: Implement**

```go
// apps/api/cmd/manticore_client.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"stawi.opportunities/pkg/searchindex"
)

// job mirrors the Manticore idx_opportunities_rt row shape the API returns.
// Fields here match searchV2Hit but are broader because the detail
// endpoint returns everything including description + dates.
type job struct {
	CanonicalID    string     `json:"canonical_id"`
	Slug           string     `json:"slug"`
	Title          string     `json:"title"`
	Company        string     `json:"company"`
	Description    string     `json:"description,omitempty"`
	LocationText   string     `json:"location_text,omitempty"`
	Country        string     `json:"country,omitempty"`
	Language       string     `json:"language,omitempty"`
	RemoteType     string     `json:"remote_type,omitempty"`
	EmploymentType string     `json:"employment_type,omitempty"`
	Seniority      string     `json:"seniority,omitempty"`
	Category       string     `json:"category,omitempty"`
	Industry       string     `json:"industry,omitempty"`
	SalaryMin      *int       `json:"salary_min,omitempty"`
	SalaryMax      *int       `json:"salary_max,omitempty"`
	Currency       string     `json:"currency,omitempty"`
	QualityScore   float64    `json:"quality_score,omitempty"`
	IsFeatured     bool       `json:"is_featured,omitempty"`
	PostedAt       *time.Time `json:"posted_at,omitempty"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Status         string     `json:"status,omitempty"`
}

// jobsManticore wraps searchindex.Client with typed projections the
// apps/api handlers need. Keeps every Manticore query construction in
// one place so the JSON query DSL doesn't leak across files.
type jobsManticore struct {
	c *searchindex.Client
}

func newJobsManticore(c *searchindex.Client) *jobsManticore {
	return &jobsManticore{c: c}
}

// GetByID fetches a canonical by id (or slug if id doesn't match any
// canonical_id). Returns (nil, nil) if no match.
func (j *jobsManticore) GetByID(ctx context.Context, id string) (*job, error) {
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []map[string]any{
					{"equals": map[string]any{"status": "active"}},
					{"bool": map[string]any{"should": []map[string]any{
						{"equals": map[string]any{"canonical_id": id}},
						{"equals": map[string]any{"slug": id}},
					}}},
				},
			},
		},
		"limit": 1,
	}
	hits, _, err := j.search(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}
	return &hits[0], nil
}

// Count returns the total matching the given filter (or all active if
// filter is nil).
func (j *jobsManticore) Count(ctx context.Context, filter []map[string]any) (int, error) {
	f := []map[string]any{{"equals": map[string]any{"status": "active"}}}
	f = append(f, filter...)
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": f}},
		"limit": 0,
	}
	_, total, err := j.search(ctx, q)
	return total, err
}

// Top returns the top `limit` active jobs ordered by quality_score
// descending, optionally filtered to those >= minScore.
func (j *jobsManticore) Top(ctx context.Context, minScore float64, limit int) ([]job, error) {
	f := []map[string]any{{"equals": map[string]any{"status": "active"}}}
	if minScore > 0 {
		f = append(f, map[string]any{"range": map[string]any{
			"quality_score": map[string]any{"gte": minScore},
		}})
	}
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": f}},
		"sort":  []any{map[string]any{"quality_score": "desc"}, map[string]any{"posted_at": "desc"}},
		"limit": limit,
	}
	hits, _, err := j.search(ctx, q)
	return hits, err
}

// Latest returns the most-recent `limit` active jobs.
func (j *jobsManticore) Latest(ctx context.Context, limit int) ([]job, error) {
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": []map[string]any{
			{"equals": map[string]any{"status": "active"}},
		}}},
		"sort":  []any{map[string]any{"posted_at": "desc"}},
		"limit": limit,
	}
	hits, _, err := j.search(ctx, q)
	return hits, err
}

// Facets returns named facet buckets. Manticore's /search supports a
// FACET clause via JSON — we use the "aggs" shape. Returns category,
// country, remote_type, employment_type, seniority buckets keyed by
// value → count.
func (j *jobsManticore) Facets(ctx context.Context) (map[string]map[string]int, error) {
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": []map[string]any{
			{"equals": map[string]any{"status": "active"}},
		}}},
		"limit": 0,
		"aggs": map[string]any{
			"category":        map[string]any{"terms": map[string]any{"field": "category", "size": 64}},
			"country":         map[string]any{"terms": map[string]any{"field": "country", "size": 200}},
			"remote_type":     map[string]any{"terms": map[string]any{"field": "remote_type", "size": 8}},
			"employment_type": map[string]any{"terms": map[string]any{"field": "employment_type", "size": 16}},
			"seniority":       map[string]any{"terms": map[string]any{"field": "seniority", "size": 16}},
		},
	}
	raw, err := j.c.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Aggregations map[string]struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int    `json:"doc_count"`
			} `json:"buckets"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}
	out := map[string]map[string]int{}
	for name, agg := range parsed.Aggregations {
		out[name] = map[string]int{}
		for _, b := range agg.Buckets {
			if b.Key == "" {
				continue
			}
			out[name][b.Key] = b.DocCount
		}
	}
	return out, nil
}

// search is the shared decoder. Returns typed hits + total.
func (j *jobsManticore) search(ctx context.Context, q map[string]any) ([]job, int, error) {
	raw, err := j.c.Search(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	var parsed struct {
		Hits struct {
			Total int `json:"total"`
			Hits  []struct {
				Source job `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, 0, fmt.Errorf("manticore decode: %w", err)
	}
	out := make([]job, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		out = append(out, h.Source)
	}
	return out, parsed.Hits.Total, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./apps/api/cmd -run TestJobsManticore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api/cmd/manticore_client.go apps/api/cmd/manticore_client_test.go
git commit -m "feat(api): Manticore client helpers (GetByID, Count, Facets, Top, Latest)"
```

---

## Task 9: New Manticore-backed `/api/v2/*` endpoints

**Files:**
- Create: `apps/api/cmd/endpoints_v2.go`
- Create: `apps/api/cmd/endpoints_v2_test.go`

Replaces legacy Postgres-reading endpoints with Manticore-backed equivalents:

| Legacy (deletes Task 12) | New (this task) | Backing call |
|---|---|---|
| `GET /search`, `GET /api/search` | `GET /api/v2/search` (expanded below) | Manticore `idx_opportunities_rt` |
| `GET /jobs/{id}` | `GET /api/v2/jobs/{id}` | `jobsManticore.GetByID` |
| `GET /jobs/top` | `GET /api/v2/jobs/top` | `jobsManticore.Top` |
| `GET /api/jobs/latest` | `GET /api/v2/jobs/latest` | `jobsManticore.Latest` |
| `GET /categories`, `GET /api/categories` | `GET /api/v2/categories` | `jobsManticore.Facets` (category bucket) |
| `GET /stats`, `GET /api/stats/summary` | `GET /api/v2/stats` | `jobsManticore.Count` + `jobsManticore.Facets` |
| `GET /api/categories/{slug}/jobs` | merged into `GET /api/v2/search?category=<slug>` | `jobsManticore` via search |

The expanded `/api/v2/search` adds salary range, sort, cursor, and facets to the Phase 2 minimal version. Phase 2's `search_v2.go` becomes legacy; this task supersedes it.

- [ ] **Step 1: Write the failing tests**

```go
// apps/api/cmd/endpoints_v2_test.go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"stawi.opportunities/pkg/searchindex"
)

// stubManticore is an httptest server whose response body per POST /search
// is pre-determined by the `responder` fn. Used for unit-style tests
// against the v2 handlers without a real Manticore.
func stubManticore(responder func(req map[string]any) string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responder(body)))
	}))
}

func TestV2Search_ReturnsHitsAndFacets(t *testing.T) {
	ts := stubManticore(func(req map[string]any) string {
		return `{"hits":{"total":2,"hits":[
			{"_id":1,"_source":{"canonical_id":"c1","slug":"a","title":"Go Dev","company":"Acme","country":"KE","remote_type":"remote","category":"engineering"}},
			{"_id":2,"_source":{"canonical_id":"c2","slug":"b","title":"Sr Go","company":"Beta","country":"NG","remote_type":"hybrid","category":"engineering"}}
		]},"aggregations":{
			"category":{"buckets":[{"key":"engineering","doc_count":2}]},
			"country":{"buckets":[{"key":"KE","doc_count":1},{"key":"NG","doc_count":1}]},
			"remote_type":{"buckets":[]},
			"employment_type":{"buckets":[]},
			"seniority":{"buckets":[]}
		}}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)
	h := v2SearchHandler(jm)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/search?q=go&country=KE", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"canonical_id":"c1"`)
	require.Contains(t, rr.Body.String(), `"facets"`)
}

func TestV2JobByID_NotFound(t *testing.T) {
	ts := stubManticore(func(_ map[string]any) string {
		return `{"hits":{"total":0,"hits":[]}}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)
	h := v2JobByIDHandler(jm)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/jobs/missing", nil)
	req.SetPathValue("id", "missing")
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestV2Categories(t *testing.T) {
	ts := stubManticore(func(_ map[string]any) string {
		return `{"hits":{"total":0,"hits":[]},"aggregations":{
			"category":{"buckets":[{"key":"engineering","doc_count":42},{"key":"design","doc_count":10}]},
			"country":{"buckets":[]},"remote_type":{"buckets":[]},"employment_type":{"buckets":[]},"seniority":{"buckets":[]}
		}}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)
	h := v2CategoriesHandler(jm)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/categories", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, strings.Contains(rr.Body.String(), `"engineering"`))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./apps/api/cmd -run TestV2 -v`
Expected: FAIL with `undefined: v2SearchHandler`.

- [ ] **Step 3: Implement the handlers**

```go
// apps/api/cmd/endpoints_v2.go
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// v2SearchHandler handles GET /api/v2/search with full filter set:
//   q, country, remote_type, employment_type, category, seniority,
//   salary_min, salary_max, currency, sort, limit.
// Responds with hits + facets + total.
func v2SearchHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		q := strings.TrimSpace(qs.Get("q"))
		filter := []map[string]any{{"equals": map[string]any{"status": "active"}}}
		if v := strings.ToUpper(strings.TrimSpace(qs.Get("country"))); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"country": v}})
		}
		if v := strings.TrimSpace(qs.Get("remote_type")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"remote_type": v}})
		}
		if v := strings.TrimSpace(qs.Get("employment_type")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"employment_type": v}})
		}
		if v := strings.TrimSpace(qs.Get("category")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"category": v}})
		}
		if v := strings.TrimSpace(qs.Get("seniority")); v != "" {
			filter = append(filter, map[string]any{"equals": map[string]any{"seniority": v}})
		}
		if v := qs.Get("salary_min"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter = append(filter, map[string]any{"range": map[string]any{"salary_min": map[string]any{"gte": n}}})
			}
		}
		if v := qs.Get("salary_max"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter = append(filter, map[string]any{"range": map[string]any{"salary_max": map[string]any{"lte": n}}})
			}
		}

		limit := parseLimit(qs.Get("limit"), 20, 50)

		boolQ := map[string]any{"filter": filter}
		if q != "" {
			boolQ["must"] = []map[string]any{{"match": map[string]any{"*": q}}}
		}

		sort := qs.Get("sort")
		var sortSpec []any
		switch sort {
		case "recent":
			sortSpec = []any{map[string]any{"posted_at": "desc"}}
		case "quality":
			sortSpec = []any{map[string]any{"quality_score": "desc"}, map[string]any{"posted_at": "desc"}}
		case "relevance":
			fallthrough
		default:
			if q == "" {
				sortSpec = []any{map[string]any{"posted_at": "desc"}}
			} else {
				// default: BM25 relevance (Manticore orders hits by
				// WEIGHT() DESC by default when the query has MATCH).
			}
		}

		query := map[string]any{
			"index": "idx_opportunities_rt",
			"query": map[string]any{"bool": boolQ},
			"limit": limit,
			"aggs": map[string]any{
				"category":        map[string]any{"terms": map[string]any{"field": "category", "size": 32}},
				"country":         map[string]any{"terms": map[string]any{"field": "country", "size": 200}},
				"remote_type":     map[string]any{"terms": map[string]any{"field": "remote_type", "size": 8}},
				"employment_type": map[string]any{"terms": map[string]any{"field": "employment_type", "size": 16}},
				"seniority":       map[string]any{"terms": map[string]any{"field": "seniority", "size": 16}},
			},
		}
		if sortSpec != nil {
			query["sort"] = sortSpec
		}

		raw, err := jm.c.Search(ctx, query)
		if err != nil {
			http.Error(w, `{"error":"search failed: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		var parsed struct {
			Hits struct {
				Total int `json:"total"`
				Hits  []struct {
					Source job `json:"_source"`
				} `json:"hits"`
			} `json:"hits"`
			Aggregations map[string]struct {
				Buckets []struct {
					Key      string `json:"key"`
					DocCount int    `json:"doc_count"`
				} `json:"buckets"`
			} `json:"aggregations"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			http.Error(w, `{"error":"decode failed"}`, http.StatusInternalServerError)
			return
		}
		hits := make([]job, 0, len(parsed.Hits.Hits))
		for _, h := range parsed.Hits.Hits {
			hits = append(hits, h.Source)
		}
		facets := map[string]map[string]int{}
		for name, agg := range parsed.Aggregations {
			m := map[string]int{}
			for _, b := range agg.Buckets {
				if b.Key != "" {
					m[b.Key] = b.DocCount
				}
			}
			facets[name] = m
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":   q,
			"results": hits,
			"facets":  facets,
			"total":   parsed.Hits.Total,
			"sort":    sort,
		})
	}
}

func v2JobByIDHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		if id == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}
		j, err := jm.GetByID(req.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if j == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(j)
	}
}

func v2TopHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 200)
		minScore := 60.0
		if v := req.URL.Query().Get("min_score"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				minScore = f
			}
		}
		rows, err := jm.Top(req.Context(), minScore, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"min_score": minScore,
			"count":     len(rows),
			"results":   rows,
		})
	}
}

func v2LatestHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit := parseLimit(req.URL.Query().Get("limit"), 20, 100)
		rows, err := jm.Latest(req.Context(), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
	}
}

func v2CategoriesHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		facets, err := jm.Facets(req.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		type bucket struct {
			Slug  string `json:"slug"`
			Count int    `json:"count"`
		}
		out := make([]bucket, 0, len(facets["category"]))
		for k, n := range facets["category"] {
			out = append(out, bucket{Slug: k, Count: n})
		}
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"categories": out})
	}
}

func v2StatsHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		totalJobs, err := jm.Count(ctx, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		facets, err := jm.Facets(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		companies := 0 // Company facet not indexed in idx_opportunities_rt v1; derive later.
		countries := len(facets["country"])
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_jobs":      totalJobs,
			"total_companies": companies,
			"countries":       countries,
		})
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./apps/api/cmd -run TestV2 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api/cmd/endpoints_v2.go apps/api/cmd/endpoints_v2_test.go
git commit -m "feat(api): Manticore-backed /api/v2/{search,jobs,top,latest,categories,stats}"
```

---

## Task 10: Port `/api/feed` + `/api/feed/tier` to Manticore

**Files:**
- Modify: `apps/api/cmd/endpoints_v2.go`
- Modify: `apps/api/cmd/endpoints_v2_test.go`

`/api/feed` and `/api/feed/tier` power the Hugo shell's browsing surface. They produce a tiered cascade `{preferred, local, regional, global}` so the JS UI shows country-local jobs first, then global fallback when local is thin. Legacy implementation lives in `tiered.go` + `manifest.go` (Postgres-backed).

The Manticore port preserves the endpoint contract but drops the "regional" tier (it required EAC-language mapping in the DB layer; not worth porting for v1). New cascade is `{local, global}`. Country is inferred from `CF-IPCountry` header by default, overridable via `?country=`.

- [ ] **Step 1: Write the failing test**

```go
// Append to apps/api/cmd/endpoints_v2_test.go
func TestV2Feed_LocalThenGlobal(t *testing.T) {
	// Two calls — first for country=KE local, second for global fallback.
	calls := 0
	ts := stubManticore(func(req map[string]any) string {
		calls++
		if calls == 1 {
			// local
			return `{"hits":{"total":1,"hits":[{"_id":1,"_source":{"canonical_id":"c1","slug":"ke-eng","title":"Eng KE","country":"KE"}}]}}`
		}
		// global
		return `{"hits":{"total":2,"hits":[
			{"_id":2,"_source":{"canonical_id":"c2","slug":"global-a","title":"A","country":"US"}},
			{"_id":3,"_source":{"canonical_id":"c3","slug":"global-b","title":"B","country":"DE"}}
		]}}`
	})
	defer ts.Close()
	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)
	h := v2FeedHandler(jm)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed", nil)
	req.Header.Set("CF-IPCountry", "KE")
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"local"`)
	require.Contains(t, rr.Body.String(), `"global"`)
	require.Contains(t, rr.Body.String(), `"country":"KE"`)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./apps/api/cmd -run TestV2Feed -v`
Expected: FAIL with `undefined: v2FeedHandler`.

- [ ] **Step 3: Implement**

Append to `apps/api/cmd/endpoints_v2.go`:

```go
// v2FeedHandler returns a two-tier cascade: local (inferred country)
// then global. The JS client renders "preferred" section first, then
// the remainder. Country auto-detected from CF-IPCountry header or
// `?country=` override. Each tier capped at `per_tier` (default 10,
// max 30).
func v2FeedHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		country := strings.ToUpper(qs.Get("country"))
		if country == "" {
			country = strings.ToUpper(req.Header.Get("CF-IPCountry"))
		}
		perTier := parseLimit(qs.Get("per_tier"), 10, 30)

		type tier struct {
			Name    string `json:"name"`
			Country string `json:"country,omitempty"`
			Results []job  `json:"results"`
		}

		resp := struct {
			Country string `json:"country"`
			Tiers   []tier `json:"tiers"`
		}{Country: country}

		if country != "" {
			localFilter := []map[string]any{{"equals": map[string]any{"country": country}}}
			local, err := jm.searchFiltered(ctx, localFilter, perTier, "posted_at")
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			resp.Tiers = append(resp.Tiers, tier{Name: "local", Country: country, Results: local})
		}

		global, err := jm.Latest(ctx, perTier)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp.Tiers = append(resp.Tiers, tier{Name: "global", Results: global})

		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// v2FeedTierHandler paginates within one tier after the initial cascade.
// `?tier=local&country=KE&cursor=…` or `?tier=global&cursor=…`.
func v2FeedTierHandler(jm *jobsManticore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()
		tier := qs.Get("tier")
		limit := parseLimit(qs.Get("limit"), 20, 50)

		filter := []map[string]any{{"equals": map[string]any{"status": "active"}}}
		if tier == "local" {
			country := strings.ToUpper(qs.Get("country"))
			if country == "" {
				http.Error(w, `{"error":"country required for local tier"}`, http.StatusBadRequest)
				return
			}
			filter = append(filter, map[string]any{"equals": map[string]any{"country": country}})
		}

		rows, err := jm.searchFiltered(ctx, filter, limit, "posted_at")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tier":    tier,
			"results": rows,
		})
	}
}
```

And add `searchFiltered` to `apps/api/cmd/manticore_client.go`:

```go
// searchFiltered runs the given filter + sort + limit. Shared by
// feed + tier handlers. The `status=active` filter is NOT added here
// — callers pass it in explicitly.
func (j *jobsManticore) searchFiltered(ctx context.Context, filter []map[string]any, limit int, sortField string) ([]job, error) {
	q := map[string]any{
		"index": "idx_opportunities_rt",
		"query": map[string]any{"bool": map[string]any{"filter": filter}},
		"sort":  []any{map[string]any{sortField: "desc"}},
		"limit": limit,
	}
	hits, _, err := j.search(ctx, q)
	return hits, err
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./apps/api/cmd -run TestV2Feed -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api/cmd/endpoints_v2.go apps/api/cmd/endpoints_v2_test.go apps/api/cmd/manticore_client.go
git commit -m "feat(api): Manticore-backed /api/v2/feed with local+global tiers"
```

---

## Task 11: Rewrite `/admin/backfill` (Hugo publish) to source from R2 Parquet

**Files:**
- Create: `apps/api/cmd/backfill_parquet.go`
- Create: `apps/api/cmd/backfill_parquet_test.go`

The existing `/admin/backfill` endpoint walks `canonical_jobs` in Postgres and publishes a JSON snapshot per job to R2 (Hugo consumes it). Phase 6 drops `canonical_jobs`, so the walk must source from `canonicals_current/` in R2 Parquet.

The new handler scans every `canonicals_current/cc=*/*.parquet` file, decodes rows, and publishes each as `jobs/<slug>.json`. It preserves the NDJSON progress streaming shape so the Trustage `feeds-rebuild.json` trigger continues working.

`min_quality` still applies — filters rows by `QualityScore >= threshold`. `since` filters by `PostedAt` (previously `created_at`).

- [ ] **Step 1: Write the failing test**

```go
// apps/api/cmd/backfill_parquet_test.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

type fakeR2Snapshotter struct {
	uploads map[string][]byte
}

func (f *fakeR2Snapshotter) UploadPublicSnapshot(_ context.Context, key string, body []byte) error {
	if f.uploads == nil {
		f.uploads = map[string][]byte{}
	}
	f.uploads[key] = body
	return nil
}
func (f *fakeR2Snapshotter) TriggerDeploy() error { return nil }

func TestBackfillParquet_PublishesAllRowsAboveThreshold(t *testing.T) {
	ctx := context.Background()
	mc := startMinIOCmd(t) // helper: reuse the same pattern other apps/api tests use
	defer mc.Close()

	uploader := eventlog.NewUploader(mc.Client, mc.Bucket)
	rows := []eventsv1.CanonicalUpsertedV1{
		{CanonicalID: "c1", ClusterID: "cl1", Slug: "acme-eng", Title: "Eng", QualityScore: 75,
			Status: "active", PostedAt: timeP(time.Now().UTC()), OccurredAt: time.Now().UTC()},
		{CanonicalID: "c2", ClusterID: "cl2", Slug: "low-qual", Title: "Low", QualityScore: 30,
			Status: "active", PostedAt: timeP(time.Now().UTC()), OccurredAt: time.Now().UTC()},
	}
	body, _ := eventlog.WriteParquet(rows)
	_, err := uploader.Put(ctx, "canonicals_current/cc=cl/current.parquet", body)
	require.NoError(t, err)

	snap := &fakeR2Snapshotter{}
	h := backfillParquetHandler(eventlog.NewReader(mc.Client, mc.Bucket), snap, 50.0)

	req := httptest.NewRequest("POST", "/admin/backfill?min_quality=50", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, 200, rr.Code, rr.Body.String())
	require.Contains(t, snap.uploads, "jobs/acme-eng.json")
	require.NotContains(t, snap.uploads, "jobs/low-qual.json", "below min_quality threshold")

	// Verify the snapshot is valid JSON + has the slug echoed.
	var snapJSON map[string]any
	require.NoError(t, json.Unmarshal(snap.uploads["jobs/acme-eng.json"], &snapJSON))
	require.Equal(t, "acme-eng", snapJSON["slug"])

	// NDJSON progress lines were emitted.
	require.Contains(t, rr.Body.String(), `"done":true`)
	_ = bytes.NewReader(nil)
}

func timeP(t time.Time) *time.Time { return &t }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./apps/api/cmd -run TestBackfillParquet -v`
Expected: FAIL with `undefined: backfillParquetHandler`.

- [ ] **Step 3: Implement the handler**

```go
// apps/api/cmd/backfill_parquet.go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/pitabwire/util"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

// r2Snapshotter is the minimal publish interface the backfill needs.
// Kept small so the test fake is easy to write.
type r2Snapshotter interface {
	UploadPublicSnapshot(ctx context.Context, key string, body []byte) error
	TriggerDeploy() error
}

// backfillParquetHandler walks canonicals_current/ in R2 and publishes
// Hugo snapshots under jobs/<slug>.json. Replaces the legacy
// canonical_jobs-walking implementation from Phase 1.
func backfillParquetHandler(reader *eventlog.Reader, snap r2Snapshotter, defaultMinQuality float64) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		qs := req.URL.Query()

		minQuality := defaultMinQuality
		if v := qs.Get("min_quality"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				minQuality = f
			}
		}
		var sinceFilter *time.Time
		if v := qs.Get("since"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				sinceFilter = &t
			} else if t, err := time.Parse("2006-01-02", v); err == nil {
				sinceFilter = &t
			}
		}
		triggerDeploy := qs.Get("trigger_deploy") != "false"

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)

		var total, uploaded, skipped int
		cursor := ""
		for {
			page, err := reader.ListNewObjects(ctx, "canonicals_current/", cursor, 500)
			if err != nil {
				util.Log(ctx).WithError(err).Error("backfill: list failed")
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			if len(page) == 0 {
				break
			}
			for _, o := range page {
				if o.Key == nil {
					continue
				}
				body, err := reader.Get(ctx, *o.Key)
				if err != nil {
					skipped++
					continue
				}
				rows, err := eventlog.ReadParquet[eventsv1.CanonicalUpsertedV1](body)
				if err != nil {
					skipped++
					continue
				}
				for _, r := range rows {
					total++
					if r.Status != "active" {
						continue
					}
					if r.QualityScore < minQuality {
						continue
					}
					if sinceFilter != nil && r.PostedAt != nil && r.PostedAt.Before(*sinceFilter) {
						continue
					}
					if r.Slug == "" {
						continue
					}

					snapJSON, err := buildHugoSnapshot(r)
					if err != nil {
						skipped++
						continue
					}
					key := "jobs/" + r.Slug + ".json"
					if err := snap.UploadPublicSnapshot(ctx, key, snapJSON); err != nil {
						util.Log(ctx).WithError(err).WithField("r2_key", key).Warn("backfill: upload failed")
						skipped++
						continue
					}
					uploaded++
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"progress": true, "uploaded": uploaded, "skipped": skipped,
				})
				if flusher != nil {
					flusher.Flush()
				}
			}
			cursor = *page[len(page)-1].Key
		}

		if triggerDeploy && uploaded > 0 {
			if err := snap.TriggerDeploy(); err != nil {
				util.Log(ctx).WithError(err).Warn("backfill: deploy hook failed")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"done": true, "total": total, "uploaded": uploaded, "skipped": skipped,
			"deployed": triggerDeploy && uploaded > 0,
		})
	}
}

// buildHugoSnapshot serialises a CanonicalUpsertedV1 into the shape
// Hugo consumes. Matches the legacy domain.BuildSnapshotWithHTML output
// shape so the existing Hugo templates continue to render.
func buildHugoSnapshot(r eventsv1.CanonicalUpsertedV1) ([]byte, error) {
	// Render description HTML via the same helper as the Phase 1
	// publish path. publish.RenderDescriptionHTML expects a string
	// description — we pass r.Description directly.
	// (If importing publish causes a cycle, inline the markdown→HTML
	// call at package-main level.)
	return json.Marshal(map[string]any{
		"canonical_id":    r.CanonicalID,
		"slug":            r.Slug,
		"title":           r.Title,
		"company":         r.Company,
		"description":     r.Description,
		"location_text":   r.LocationText,
		"country":         r.Country,
		"language":        r.Language,
		"remote_type":     r.RemoteType,
		"employment_type": r.EmploymentType,
		"seniority":       r.Seniority,
		"category":        r.Category,
		"salary_min":      r.SalaryMin,
		"salary_max":      r.SalaryMax,
		"currency":        r.Currency,
		"posted_at":       r.PostedAt,
		"quality_score":   r.QualityScore,
		"skills":          r.Skills,
		"roles":           r.Roles,
		"benefits":        r.Benefits,
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./apps/api/cmd -run TestBackfillParquet -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api/cmd/backfill_parquet.go apps/api/cmd/backfill_parquet_test.go
git commit -m "feat(api): /admin/backfill sources Hugo snapshots from R2 Parquet"
```

---

## Task 12: Rewrite `apps/api/cmd/main.go` — delete legacy Postgres endpoints, wire v2

**Files:**
- Modify: `apps/api/cmd/main.go`
- Delete: `apps/api/cmd/search_v2.go` (folded into `endpoints_v2.go`)
- Delete: `apps/api/cmd/country_backfill.go`
- Delete: `apps/api/cmd/manifest.go`
- Delete: `apps/api/cmd/tiered.go`
- Delete the matching `*_test.go` files if they reference the deleted modules.

`apps/api/cmd/main.go` currently imports `pkg/pipeline/handlers`, `pkg/repository` (for `CandidateRepository`, `FacetRepository`, `JobRepository`, `SourceRepository`), `pkg/domain` (for `CanonicalJob` etc.), `gorm.io/gorm`, and `gorm.io/driver/postgres`. Post-Phase-6 it only needs `pkg/searchindex` + `pkg/eventlog` + optional `pkg/publish` for the Hugo snapshotter.

This task is a full rewrite. We keep only: `/healthz`, `/jobs/{slug}/view` (analytics beacon, no DB reads), and the new `/api/v2/*` + `/admin/backfill`. `/admin/feeds/rebuild` is dropped — feed content comes from repeated `/admin/backfill` runs.

- [ ] **Step 1: Rewrite `apps/api/cmd/main.go`**

```go
// apps/api/cmd/main.go (full replacement)
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	env "github.com/caarlos0/env/v11"
	"github.com/pitabwire/util"

	"stawi.opportunities/pkg/analytics"
	"stawi.opportunities/pkg/eventlog"
	"stawi.opportunities/pkg/publish"
	"stawi.opportunities/pkg/searchindex"
)

type apiConfig struct {
	ServerPort        string  `env:"SERVER_PORT"        envDefault:":8082"`
	ManticoreURL      string  `env:"MANTICORE_URL"      envDefault:""`
	R2AccountID       string  `env:"R2_ACCOUNT_ID"      envDefault:""`
	R2AccessKeyID     string  `env:"R2_ACCESS_KEY_ID"   envDefault:""`
	R2SecretAccessKey string  `env:"R2_SECRET_ACCESS_KEY" envDefault:""`
	R2Bucket          string  `env:"R2_BUCKET"          envDefault:"opportunities-content"`
	R2LogBucket       string  `env:"R2_LOG_BUCKET"      envDefault:"opportunities-log"`
	R2Endpoint        string  `env:"R2_ENDPOINT"        envDefault:""`
	R2Region          string  `env:"R2_REGION"          envDefault:"auto"`
	R2DeployHookURL   string  `env:"R2_DEPLOY_HOOK_URL" envDefault:""`
	PublishMinQuality float64 `env:"PUBLISH_MIN_QUALITY" envDefault:"50"`
	AnalyticsBaseURL  string  `env:"ANALYTICS_BASE_URL" envDefault:""`
	AnalyticsOrg      string  `env:"ANALYTICS_ORG"      envDefault:"default"`
	AnalyticsUsername string  `env:"ANALYTICS_USERNAME" envDefault:""`
	AnalyticsPassword string  `env:"ANALYTICS_PASSWORD" envDefault:""`
}

func main() {
	ctx := context.Background()
	log := util.Log(ctx)

	var cfg apiConfig
	if err := env.Parse(&cfg); err != nil {
		log.WithError(err).Fatal("api: parse config failed")
	}

	// Manticore is mandatory — the service is meaningless without it.
	if cfg.ManticoreURL == "" {
		log.Fatal("api: MANTICORE_URL is required")
	}
	manticore, err := searchindex.Open(searchindex.Config{URL: cfg.ManticoreURL})
	if err != nil {
		log.WithError(err).Fatal("api: manticore open failed")
	}
	defer func() { _ = manticore.Close() }()
	jm := newJobsManticore(manticore)

	// Optional R2 publisher for /admin/backfill.
	var r2Publisher *publish.R2Publisher
	var r2LogReader *eventlog.Reader
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" {
		r2Publisher = publish.NewR2Publisher(
			cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey,
			cfg.R2Bucket, cfg.R2DeployHookURL,
		)
		log.WithField("bucket", cfg.R2Bucket).Info("R2 publisher enabled")

		// Reader targets the log bucket (opportunities-log), not the
		// content bucket (opportunities-content). Two different buckets:
		// content holds Hugo snapshots, log holds the event log Parquet.
		s3cli, err := eventlog.NewS3Client(ctx, cfg.R2Endpoint, cfg.R2Region, cfg.R2AccessKeyID, cfg.R2SecretAccessKey)
		if err == nil {
			r2LogReader = eventlog.NewReader(s3cli, cfg.R2LogBucket)
		} else {
			log.WithError(err).Warn("api: log-bucket reader disabled (backfill won't work)")
		}
	}

	analyticsClient := analytics.New(analytics.Config{
		BaseURL:  cfg.AnalyticsBaseURL,
		Org:      cfg.AnalyticsOrg,
		Username: cfg.AnalyticsUsername,
		Password: cfg.AnalyticsPassword,
	})

	mux := http.NewServeMux()

	// Health — cheap Manticore ping + R2 ping.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, req *http.Request) {
		total, err := jm.Count(req.Context(), nil)
		status := "ok"
		if err != nil {
			status = "degraded"
			total = -1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     status,
			"total_jobs": total,
		})
	})

	// v2 read endpoints — Manticore-only.
	mux.HandleFunc("GET /api/v2/search", v2SearchHandler(jm))
	mux.HandleFunc("GET /api/v2/jobs/{id}", v2JobByIDHandler(jm))
	mux.HandleFunc("GET /api/v2/jobs/top", v2TopHandler(jm))
	mux.HandleFunc("GET /api/v2/jobs/latest", v2LatestHandler(jm))
	mux.HandleFunc("GET /api/v2/categories", v2CategoriesHandler(jm))
	mux.HandleFunc("GET /api/v2/stats", v2StatsHandler(jm))
	mux.HandleFunc("GET /api/v2/feed", v2FeedHandler(jm))
	mux.HandleFunc("GET /api/v2/feed/tier", v2FeedTierHandler(jm))

	// Legacy shims — v1 paths now point to v2. Keeps existing clients
	// (browser cache, external integrations) working during the tail
	// of the rollout. Delete after 30 days per the cutover runbook.
	mux.HandleFunc("GET /api/search", v2SearchHandler(jm))
	mux.HandleFunc("GET /api/categories", v2CategoriesHandler(jm))
	mux.HandleFunc("GET /api/jobs/latest", v2LatestHandler(jm))
	mux.HandleFunc("GET /api/stats/summary", v2StatsHandler(jm))
	mux.HandleFunc("GET /api/feed", v2FeedHandler(jm))
	mux.HandleFunc("GET /api/feed/tier", v2FeedTierHandler(jm))
	mux.HandleFunc("GET /jobs/top", v2TopHandler(jm))
	mux.HandleFunc("GET /jobs/{id}", v2JobByIDHandler(jm))
	mux.HandleFunc("GET /categories", v2CategoriesHandler(jm))
	mux.HandleFunc("GET /stats", v2StatsHandler(jm))
	mux.HandleFunc("GET /search", v2SearchHandler(jm))

	// Hugo snapshot publishing — sourced from R2 Parquet.
	if r2Publisher != nil && r2LogReader != nil {
		mux.HandleFunc("POST /admin/backfill",
			backfillParquetHandler(r2LogReader, r2Publisher, cfg.PublishMinQuality))
	}

	// Analytics beacon — no DB.
	mux.HandleFunc("OPTIONS /jobs/{slug}/view", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /jobs/{slug}/view", func(w http.ResponseWriter, req *http.Request) {
		slug := req.PathValue("slug")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if slug != "" && analyticsClient != nil {
			evt := map[string]any{
				"event":      "server_view",
				"slug":       slug,
				"ip_hash":    hashIP(req),
				"user_agent": req.Header.Get("User-Agent"),
				"referer":    req.Header.Get("Referer"),
				"cf_country": req.Header.Get("CF-IPCountry"),
				"cf_ray":     req.Header.Get("CF-Ray"),
			}
			if profileID := profileIDFromJWT(req); profileID != "" {
				evt["profile_id"] = profileID
			}
			analyticsClient.Send(req.Context(), "opportunities_views", evt)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv := &http.Server{Addr: cfg.ServerPort, Handler: mux}
	go func() {
		log.WithField("port", cfg.ServerPort).Info("API server listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("http server exited")
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("shutting down API")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("API stopped")
}

// hashIP + profileIDFromJWT + parseLimit + effectiveSort + cursor
// helpers are preserved in this file from the pre-rewrite version —
// keep them. parseLimit is still used by endpoints_v2.go; effectiveSort
// and the cursor helpers can go if nothing references them.
func hashIP(req *http.Request) string {
	ip := req.Header.Get("CF-Connecting-IP")
	if ip == "" {
		ip = req.Header.Get("X-Forwarded-For")
		if i := strings.IndexByte(ip, ','); i >= 0 {
			ip = ip[:i]
		}
		ip = strings.TrimSpace(ip)
	}
	if ip == "" {
		ip = req.RemoteAddr
	}
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:12])
}

func profileIDFromJWT(req *http.Request) string {
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	token := auth[len("Bearer "):]
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Sub
}

// parseLimit is used by endpoints_v2.go.
func parseLimit(s string, def, max int) int {
	if v, err := atoiSafe(s); err == nil && v > 0 {
		if v > max {
			return max
		}
		return v
	}
	return def
}

func atoiSafe(s string) (int, error) {
	n := 0
	for i, c := range s {
		if c < '0' || c > '9' {
			if i == 0 {
				return 0, errBadInt
			}
			break
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 && s != "0" {
		return 0, errBadInt
	}
	return n, nil
}

var errBadInt = errBadIntType{}

type errBadIntType struct{}

func (errBadIntType) Error() string { return "bad int" }
```

If `pkg/eventlog.NewS3Client` doesn't exist (the writer wires S3 via its own path), inline a small constructor:

```go
// Add to pkg/eventlog/r2.go (if missing):
package eventlog

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// NewS3Client constructs a shared S3 client for R2. Used by any
// service that reads/writes the event-log bucket.
func NewS3Client(ctx context.Context, endpoint, region, accessKey, secretKey string) (*s3.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		),
	)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = true
	}), nil
}
```

- [ ] **Step 2: Delete the obsolete sibling files**

```bash
rm apps/api/cmd/search_v2.go apps/api/cmd/search_v2_test.go \
   apps/api/cmd/country_backfill.go \
   apps/api/cmd/manifest.go apps/api/cmd/manifest_test.go \
   apps/api/cmd/tiered.go apps/api/cmd/tiered_test.go
```

- [ ] **Step 3: Verify the binary still builds**

Run: `go build ./apps/api/...`
Expected: success. If the build fails on a reference to a deleted `repository.*` or `domain.*` type inside `main.go`, trace it — the rewrite should have removed every such reference.

- [ ] **Step 4: Run all apps/api tests**

Run: `go test ./apps/api/... -v`
Expected: remaining tests (manticore_client_test.go, endpoints_v2_test.go, backfill_parquet_test.go) pass; the deleted tests' files no longer exist.

- [ ] **Step 5: Commit**

```bash
git add apps/api/cmd/main.go pkg/eventlog/r2.go
git rm apps/api/cmd/search_v2.go apps/api/cmd/search_v2_test.go \
       apps/api/cmd/country_backfill.go \
       apps/api/cmd/manifest.go apps/api/cmd/manifest_test.go \
       apps/api/cmd/tiered.go apps/api/cmd/tiered_test.go
git commit -m "refactor(api): rewrite main.go — Manticore-only reads, delete Postgres endpoints"
```

---

## Task 13: Delete legacy candidate handler files + plumb model versions

**Files:**
- Delete: `apps/candidates/service/events/embedding.go`
- Delete: `apps/candidates/service/events/profile_created.go`
- Delete any `_test.go` sibling files for the above.
- Modify: `apps/worker/service/embed.go`
- Modify: `apps/worker/service/publish.go`

Phase 5 left the two legacy handler files in place (the new `main.go` just didn't register them). Now they delete cleanly — no callers remain.

Also fix the two Phase 3 inline-TODOs:
1. `apps/worker/service/embed.go` line ~1204 — `EmbeddingV1.ModelVersion` is passed as empty string; pull from the extractor.
2. `apps/worker/service/publish.go` line ~1442 — `PublishedV1.R2Version` is hardcoded to `1`; plumb actual version.

- [ ] **Step 1: Delete the legacy candidate files**

```bash
rm apps/candidates/service/events/embedding.go \
   apps/candidates/service/events/profile_created.go

# And any sibling tests — check first:
find apps/candidates/service/events -maxdepth 1 -name '*_test.go' \
     -not -path '*/v1/*' -exec rm {} +
```

- [ ] **Step 2: Verify nothing imports the deleted files**

Run: `grep -r "service/events\"" apps/candidates/ --include='*.go'`
Expected: no matches. If matches, those call sites need rewiring to `service/events/v1`.

- [ ] **Step 3: Surface model version on EmbeddingV1**

Open `apps/worker/service/embed.go`. Find the `out := eventsv1.EmbeddingV1{…}` construction (around line 1200 per the phase-3 TODO marker). Replace the `ModelVersion: ""` line with a pull from the extractor:

```go
// Before (line ~1204):
// ModelVersion: "", // TODO in Phase 6: surface model version from Extractor

// After:
ModelVersion: h.extractor.EmbedModelVersion(),
```

If `EmbedModelVersion()` doesn't exist on `pkg/extraction.Extractor`, add a narrow accessor:

```go
// pkg/extraction/extractor.go — add public method
// EmbedModelVersion returns the configured embedding model identifier
// (e.g. "text-embedding-3-small", "bge-m3"). Empty when no embedding
// backend is configured. Safe to call from handler hot paths —
// returns a snapshot of the construction-time config string.
func (e *Extractor) EmbedModelVersion() string {
	if e == nil {
		return ""
	}
	return e.config.EmbeddingModel
}
```

- [ ] **Step 4: Surface R2 version on PublishedV1**

Open `apps/worker/service/publish.go`. Find the `PublishedV1` emission (around line 1442). Replace `R2Version: 1` with the real value returned from the publish call. If the publish call currently returns `(key string, error)` and not `(key string, version int, error)`, extend it:

```go
// pkg/publish/r2.go — extend UploadPublicSnapshot to return version
func (p *R2Publisher) UploadPublicSnapshotVersioned(ctx context.Context, key string, body []byte) (version int, err error) {
	// existing body + compute version from the key's object ETag or
	// from a monotonic counter. For v1 the simplest: timestamp-based
	// version (epoch-ms). Matches the spec §7.3 intent that bumps are
	// cache-busting markers, not strictly monotonic within a slug.
	if err := p.UploadPublicSnapshot(ctx, key, body); err != nil {
		return 0, err
	}
	return int(time.Now().Unix()), nil
}
```

Update `apps/worker/service/publish.go` to call the versioned form:

```go
version, err := h.publisher.UploadPublicSnapshotVersioned(ctx, key, body)
if err != nil {
    return err
}
out := eventsv1.PublishedV1{
    CanonicalID: c.CanonicalID,
    Slug:        c.Slug,
    R2Version:   version,
    PublishedAt: time.Now().UTC(),
}
```

- [ ] **Step 5: Build and test**

Run: `go build ./... && go test ./apps/worker/... ./apps/candidates/... -v`
Expected: success.

- [ ] **Step 6: Commit**

```bash
git add apps/worker/service/embed.go apps/worker/service/publish.go \
        pkg/extraction/extractor.go pkg/publish/r2.go
git rm apps/candidates/service/events/embedding.go \
       apps/candidates/service/events/profile_created.go
git commit -m "refactor(candidates): drop legacy handler files; surface model+R2 versions on events"
```

---

## Task 14: Delete `pkg/pipeline/handlers/*` (legacy worker pipeline)

**Files:**
- Delete: `pkg/pipeline/handlers/canonical.go`
- Delete: `pkg/pipeline/handlers/dedup.go`
- Delete: `pkg/pipeline/handlers/normalize.go`
- Delete: `pkg/pipeline/handlers/payloads.go`
- Delete: `pkg/pipeline/handlers/payloads_test.go`
- Delete: `pkg/pipeline/handlers/publish.go`
- Delete: `pkg/pipeline/handlers/source_expand.go`
- Delete: `pkg/pipeline/handlers/source_quality.go`
- Delete: `pkg/pipeline/handlers/translate.go`
- Delete: `pkg/pipeline/handlers/validate.go`
- Delete: corresponding `*_test.go` files.
- Delete the entire `pkg/pipeline/` directory if empty after handler removal.

All Phase 3 replacements are in `apps/worker/service/*.go`. Apps/api's `/admin/republish` was the last importer; it deleted in Task 12.

- [ ] **Step 1: Grep for external references**

Run:

```bash
grep -r "pkg/pipeline/handlers" --include='*.go' .
```

Expected: empty. If anything matches, it needs to be fixed before the delete lands. Common straggler: the api's `main.go` — already handled in Task 12, but double check.

- [ ] **Step 2: Delete the directory**

```bash
git rm -r pkg/pipeline/handlers/
# If that was the only thing under pkg/pipeline/, remove the parent too:
[ -d pkg/pipeline ] && [ -z "$(ls pkg/pipeline)" ] && rmdir pkg/pipeline
```

- [ ] **Step 3: Build the whole tree**

Run: `go build ./...`
Expected: success. Any compile error points at a straggler import — fix before proceeding.

- [ ] **Step 4: Full test sweep**

Run: `go test ./... -count=1 -timeout 20m`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor(pipeline): delete legacy pkg/pipeline/handlers; worker is sole consumer"
```

---

## Task 15: Delete orphan packages + dead repository files + dead domain types

**Files:**
- Delete: `pkg/billing/` (entire directory — zero importers post-Phase-5)
- Delete: `pkg/repository/facets.go` + `facets_test.go` (if present)
- Delete: `pkg/repository/rerank_cache.go` + tests
- Delete: `pkg/repository/saved_job.go` + tests
- Delete: `pkg/repository/rejected.go` + tests
- Delete: `pkg/repository/retention.go` + tests (confirm nothing still calls it)
- Delete: `pkg/repository/job.go` + `materializer_watermark.go` if unused — verify before deleting
- Modify: `pkg/domain/models.go` — remove types for dropped tables
- Modify: `pkg/repository/candidate.go` — remove methods tied to dropped columns (`UpdateEmbedding`)
- Modify: `apps/crawler/cmd/main.go` — trim AutoMigrate list

- [ ] **Step 1: Grep for remaining importers**

For each candidate for deletion, grep first:

```bash
for pkg in pkg/billing pkg/repository/facets pkg/repository/rerank_cache \
           pkg/repository/saved_job pkg/repository/rejected pkg/repository/retention; do
    echo "--- $pkg ---"
    grep -r "stawi.opportunities/${pkg}" --include='*.go' .
done
```

Expected (post Task 12): `pkg/billing` has zero matches. The repository files might still have a couple of stragglers in app `main.go` AutoMigrate lists or small helpers — those are fine to update inline as part of this task.

- [ ] **Step 2: Delete `pkg/billing`**

```bash
git rm -r pkg/billing/
```

- [ ] **Step 3: Delete the dead repository files (confirming each one)**

```bash
# facets: used by api's facetRepo.Read — api rewrite dropped it
git rm pkg/repository/facets.go
[ -f pkg/repository/facets_test.go ] && git rm pkg/repository/facets_test.go

# rerank_cache: used by legacy handlers
git rm pkg/repository/rerank_cache.go
[ -f pkg/repository/rerank_cache_test.go ] && git rm pkg/repository/rerank_cache_test.go

# saved_job: user chose to drop the feature
git rm pkg/repository/saved_job.go
[ -f pkg/repository/saved_job_test.go ] && git rm pkg/repository/saved_job_test.go

# rejected_jobs: legacy worker pipeline dead-letter
git rm pkg/repository/rejected.go
[ -f pkg/repository/rejected_test.go ] && git rm pkg/repository/rejected_test.go

# retention: operates on canonical_jobs
git rm pkg/repository/retention.go
[ -f pkg/repository/retention_test.go ] && git rm pkg/repository/retention_test.go

# job: reads canonical_jobs — check importers first
grep -r "repository.JobRepository\|repository.NewJobRepository" --include='*.go' . || git rm pkg/repository/job.go
```

If `pkg/repository/job.go` still has active users (e.g., `apps/crawler` uses it somewhere), leave that one alone and note as follow-up.

- [ ] **Step 4: Trim `pkg/domain/models.go`**

Open `pkg/domain/models.go`. Remove (or comment the removal of) the Postgres-backed types for dropped tables:
- `JobVariant`, `JobCluster`, `JobClusterMember`, `CanonicalJob`
- `CrawlPageState`, `RejectedJob`, `SavedJob`
- `RerankCache` / `MVJobFacet` / related materialised-view types (if present)

Preserve: `Source`, `CandidateProfile`, `CrawlJob`, `RawPayload`, `Subscription`, `Entitlement`, anything else the remaining repositories use.

Also on `CandidateProfile`: remove the CV/preferences/embedding fields that are about to be dropped from the schema (`cv_raw`, `cv_extracted`, `cv_embedding`, `preferences`, etc. — whatever names exist). The `GORM:"-"` tag is fine for `LastContactedAt`, `MatchesSent` if those stay (they're operational, not CV).

- [ ] **Step 5: Trim `apps/crawler/cmd/main.go` AutoMigrate list**

Change the AutoMigrate call so it only enumerates the models that still have tables:

```go
// apps/crawler/cmd/main.go — AutoMigrate block
if err := migrationDB.AutoMigrate(
    &domain.Source{},
    &domain.CrawlJob{},
    &domain.RawPayload{},
); err != nil {
    util.Log(ctx).WithError(err).Fatal("crawler: auto-migrate failed")
}
```

Remove the imports of dropped models if they now fall unused.

- [ ] **Step 6: Trim `pkg/repository/candidate.go`**

Drop `UpdateEmbedding` (column is about to be dropped) and any other methods that reference columns being dropped. Keep: `Create`, `GetByID`, `GetByProfileID`, `Update`, `UpdateStatus`, `ListActive`, `Count`, `IncrementMatchesSent`, `ListPendingSubscriptions`, `ListInactiveSince`.

- [ ] **Step 7: Build**

Run: `go build ./...`
Expected: success. Compile errors point at residual callers of deleted symbols — fix inline.

- [ ] **Step 8: Commit**

```bash
git add pkg/domain/models.go pkg/repository/candidate.go apps/crawler/cmd/main.go
git commit -m "refactor(pkg): delete orphan packages (billing, dead repository adapters, dead domain types)"
```

---

## Task 16: Postgres cutover migration — drop legacy tables + candidate columns

**Files:**
- Create: `db/migrations/0003_cutover_drop_legacy.sql`
- Modify: `pkg/repository/schema.go` — if `FinalizeSchema` references any dropped objects

The migration drops: `canonical_jobs`, `job_variants`, `job_clusters`, `job_cluster_members`, `crawl_page_state`, `rerank_cache`, `mv_job_facets`, `saved_jobs`. It also drops the CV/preferences/embedding columns on `candidates` (specific column names to verify from the running schema).

Ordering matters: drop the dependent tables (`job_cluster_members` before `job_clusters`, `mv_job_facets` before `canonical_jobs`) then the independents.

The migration uses `IF EXISTS` throughout so re-runs are safe and it succeeds against a fresh database.

- [ ] **Step 1: Inspect the live schema to confirm candidate column names**

On a clean checkout of `main`, run:

```bash
psql "$DATABASE_URL" -c "\d candidates" | tee /tmp/candidates_cols.txt
```

Expected columns (from Phase 5 review): `id`, `profile_id`, `status`, `subscription`, `subscription_id`, `created_at`, `updated_at`, `matches_sent`, `last_contacted_at`, plus legacy CV/preferences/embedding columns like `cv_raw`, `cv_extracted`, `cv_embedding`, `preferences`, `target_role`, etc. **Capture the exact names** — the DROP COLUMN statements must match them.

If running against the cluster isn't an option, walk `pkg/domain/models.go` at the state before Task 15 trimmed it and list each field the Phase 5 refactor stopped writing (same set).

- [ ] **Step 2: Write the migration**

```sql
-- db/migrations/0003_cutover_drop_legacy.sql
-- Phase 6 greenfield cutover. Drops the Postgres tables and candidate
-- columns that Phases 1-5 replaced with the R2-Parquet event log +
-- Manticore derived view. Idempotent via IF EXISTS so re-runs are safe.

BEGIN;

-- Dependent rows first.
DROP TABLE IF EXISTS job_cluster_members CASCADE;
DROP TABLE IF EXISTS job_clusters         CASCADE;
DROP TABLE IF EXISTS job_variants         CASCADE;

-- Materialised view must drop before the underlying table.
DROP MATERIALIZED VIEW IF EXISTS mv_job_facets;
DROP TABLE IF EXISTS canonical_jobs CASCADE;

DROP TABLE IF EXISTS crawl_page_state  CASCADE;
DROP TABLE IF EXISTS rerank_cache      CASCADE;
DROP TABLE IF EXISTS saved_jobs        CASCADE;

-- Candidate schema trim. Column names come from Step 1's inspection.
-- Adjust the list if inspection revealed different names.
ALTER TABLE candidates
    DROP COLUMN IF EXISTS cv_raw,
    DROP COLUMN IF EXISTS cv_extracted,
    DROP COLUMN IF EXISTS cv_embedding,
    DROP COLUMN IF EXISTS cv_version,
    DROP COLUMN IF EXISTS preferences,
    DROP COLUMN IF EXISTS target_role,
    DROP COLUMN IF EXISTS seniority,
    DROP COLUMN IF EXISTS remote_preference,
    DROP COLUMN IF EXISTS salary_min,
    DROP COLUMN IF EXISTS salary_max,
    DROP COLUMN IF EXISTS currency,
    DROP COLUMN IF EXISTS preferred_locations,
    DROP COLUMN IF EXISTS excluded_companies,
    DROP COLUMN IF EXISTS score_components;

-- Any orphan indexes on the dropped columns evaporate with the columns.

COMMIT;
```

- [ ] **Step 3: Update `pkg/repository/schema.FinalizeSchema` if it references dropped objects**

Open `pkg/repository/schema.go`. If it creates any indexes or partial indexes on the dropped tables (`idx_canonical_jobs_*`, `mv_job_facets`, etc.), remove those statements. The function should now only finalize `sources`, `candidates`, `crawl_jobs`, `raw_payloads`.

- [ ] **Step 4: Apply the migration locally (dev database)**

Assuming a dev Postgres:

```bash
psql "$DATABASE_URL" -f db/migrations/0003_cutover_drop_legacy.sql
```

Expected: no errors. Re-run — expected still no errors (idempotent).

- [ ] **Step 5: Build + test**

Run: `go build ./... && go test ./pkg/repository/... -v`
Expected: green. The repository test suite should pass against the trimmed schema because Task 15 already removed the repository files that referenced dropped tables.

- [ ] **Step 6: Verify AutoMigrate on a fresh DB**

```bash
# Fresh test DB
psql "$DATABASE_URL" -c "DROP DATABASE IF EXISTS stawi_cutover_verify"
psql "$DATABASE_URL" -c "CREATE DATABASE stawi_cutover_verify"
DATABASE_URL="postgres://.../stawi_cutover_verify" DO_DATABASE_MIGRATE=true \
    go run ./apps/crawler/cmd
```

Expected: the crawler exits cleanly after AutoMigrate (Task 15 trimmed the AutoMigrate list to just `Source`, `CrawlJob`, `RawPayload`). Verify with `\dt` that only those three tables exist.

- [ ] **Step 7: Commit**

```bash
git add db/migrations/0003_cutover_drop_legacy.sql pkg/repository/schema.go
git commit -m "feat(db): cutover migration — drop legacy job tables + candidate CV columns + saved_jobs"
```

---

## Task 17: Cutover runbook

**Files:**
- Create: `docs/ops/cutover-runbook.md`

A prescriptive runbook the on-call operator follows the day of cutover. Each step is atomic, rollback-aware, and has an explicit success signal. Written as a checklist so the operator can paste progress into an incident channel.

- [ ] **Step 1: Write the runbook**

```markdown
# Phase 6 Greenfield Cutover Runbook

**Owner:** Platform / Peter Bwire
**Audience:** On-call operator running the cutover
**Estimated total time:** 2–4 hours (the pipeline-fill phase dominates)
**Rollback window:** until Step 4.3 (`DROP TABLE`) — before that, reverting to the
prior deployment restores the legacy Postgres read path.

## 0. Prerequisites

- [ ] Phase 6 plan committed to `main` and the CI build is green.
- [ ] Managed infra is up:
  - [ ] Manticore cluster reachable at `$MANTICORE_URL`
  - [ ] Valkey reachable at `$VALKEY_URL`
  - [ ] R2 bucket `opportunities-log` exists, writer key has `PutObject` + `ListObjectsV2`
  - [ ] TEI chat / embed / rerank endpoints answer `/health`
- [ ] Trustage workflows deployed: `scheduler-tick`, `compact-hourly`,
  `compact-daily`, `sources-quality-window-reset`, `sources-health-decay`,
  `candidates-matches-weekly-digest`, `candidates-cv-stale-nudge`.
- [ ] Backpressure gate default policies set via env:
  `BACKPRESSURE_MAX_DRAIN=15m`, `BACKPRESSURE_HARD_CEILING=45m`.
- [ ] `idx_opportunities_rt` schema provisioned (Phase 2 migration). Verify:
  `curl -s $MANTICORE_URL/sql?mode=raw -d "query=SHOW TABLES"`
- [ ] All six app images (`crawler`, `worker`, `writer`, `materializer`,
  `candidates`, `api`) built at the Phase 6 SHA, pushed, and deployed to
  the staging namespace for a smoke pass.

## 1. Staging smoke (T-1 day)

- [ ] Deploy Phase 6 images to staging.
- [ ] Seed 3 live sources into `sources`.
- [ ] Trigger `scheduler-tick` once manually:
  `kubectl -n stage exec deploy/trustage -- trustage run opportunities.scheduler.tick`
- [ ] Wait 60 s. Assert a variant event landed in R2 under `variants/dt=<today>/`.
- [ ] Wait 60 s. Assert a canonical landed in `canonicals/dt=<today>/`.
- [ ] Call `GET /api/v2/search?q=engineer` on staging api — expect non-empty hits.
- [ ] Call `GET /api/v2/jobs/<slug>` on one of the hits — expect 200.
- [ ] If any step fails, STOP and investigate. Do not proceed to prod.

## 2. Prod cutover

### 2.1 Pre-flight (15 min)

- [ ] Post `#stawi-ops`: "Starting greenfield cutover in 15 min".
- [ ] Pin the ops runbook in the channel.
- [ ] Pause Trustage workflows that are still pointed at the legacy crawler:
  `source-crawl-sweep.json` (should already be inactive per Phase 4) and
  `feeds-rebuild.json` (legacy feed rebuild).
- [ ] Take a manual backup of the prod database (belt-and-braces):
  `pg_dump --schema-only --no-owner $DATABASE_URL > /tmp/pre-cutover.sql`

### 2.2 Deploy the Phase 6 apps (30 min)

- [ ] `kubectl apply` the Phase 6 image tags for all six apps.
- [ ] Watch rollouts. Each pod should become `Ready`.
- [ ] Immediately verify writer + materializer + worker subscriptions:
  `kubectl -n prod logs -l app=writer --tail=50` — expect
  "writer: events manager ready" and at least one "parquet flushed" log
  within 2 min (existing in-flight events flush).
- [ ] Verify `apps/api` `/healthz` returns `ok` with `total_jobs > 0`.
  Note: at this moment the total reflects the Postgres path if the
  legacy tables are still around. The number will move once Manticore
  fills.

### 2.3 Trigger initial scheduler + compact cycle

- [ ] Fire `scheduler-tick` manually once to confirm admit/emit works
  with the live Phase 6 gate.
  `curl -XPOST $CRAWLER_URL/admin/scheduler/tick`
- [ ] Wait 5 min. Run `compact-hourly` manually against a sampled
  collection:
  `curl -XPOST $WRITER_URL/_admin/compact/hourly -d '{"collection":"variants"}'`
  Expect `rows_after > 0` in the response body.
- [ ] Run `compact-daily` against `canonicals` once Manticore has ingested
  enough to write `canonicals_current/`:
  `curl -XPOST $WRITER_URL/_admin/compact/daily -d '{"collection":"canonicals"}'`
  Expect `buckets > 0`.

### 2.4 Fill checkpoint (1–3 hours, variable)

Wait for `idx_opportunities_rt` row count to cross the launch threshold (50k
default — adjust based on staging throughput):

- [ ] Poll: `curl -s $API_URL/healthz | jq .total_jobs`
- [ ] Tail writer flushes: `kubectl logs -f -l app=writer | grep "parquet flushed"`
- [ ] Tail materializer upserts: `kubectl logs -f -l app=materializer | grep "manticore upsert"`

Every 30 minutes: post progress to `#stawi-ops`.

### 2.5 Flip the site to v2 endpoints

The api binary already mounts `/api/v2/*` and the legacy-shim paths
redirect v1 → v2 (Task 12). No additional flip is needed at the API —
the Phase 6 deploy in Step 2.2 already made them live.

- [ ] Confirm the Cloudflare route for `stawi.opportunities/api/*` points at
  the new api (unchanged — `/api/v2/*` is the new path, and the `/api/*`
  legacy paths share the same pod).
- [ ] Hit the site from a browser, exercise: search, category page,
  detail page, filter by country, filter by remote. All should return
  data.

### 2.6 Drop legacy Postgres tables

**Point of no return** — after this step, a rollback requires restoring
the `pre-cutover.sql` dump.

- [ ] `psql "$DATABASE_URL" -f db/migrations/0003_cutover_drop_legacy.sql`
- [ ] Run the migration twice to verify idempotency:
  `psql "$DATABASE_URL" -f db/migrations/0003_cutover_drop_legacy.sql`
  Expected: no errors on the second run.

### 2.7 Close out

- [ ] Post to `#stawi-ops`: "Cutover complete. Monitoring for 24h."
- [ ] Schedule a 24h post-mortem calendar slot with engineering.

## 3. Rollback (before Step 2.6 only)

If a critical issue surfaces between Steps 2.2 and 2.6:

1. `kubectl rollout undo deployment/<app> -n prod` on each of the six apps.
2. Re-enable `source-crawl-sweep` and `feeds-rebuild` in Trustage.
3. Verify `/api/search` + `/api/feed` return data from the legacy path.
4. Post-mortem once the immediate fire is out.

**After Step 2.6** rollback requires `psql < /tmp/pre-cutover.sql` followed
by a full re-crawl. The legacy code paths are deleted in this branch —
you'd also need to revert to `46a71bb` or earlier to restore them.

## 4. Post-cut verification (24h)

- [ ] `tests/k6/smoke_post_cut.js` passes against prod.
- [ ] Manticore query p95 < 100 ms (Grafana dashboard).
- [ ] Writer ack-lag p95 < 30 s.
- [ ] Materializer poll-lag p95 < 30 s.
- [ ] No unresolved alerts in `#stawi-ops`.
```

- [ ] **Step 2: Commit**

```bash
git add docs/ops/cutover-runbook.md
git commit -m "docs(ops): cutover runbook"
```

---

## Task 18: Rebuild runbooks — Manticore-from-zero, KV-from-R2, writer backlog

**Files:**
- Create: `docs/ops/runbook-manticore-rebuild.md`
- Create: `docs/ops/runbook-kv-rebuild.md`
- Create: `docs/ops/runbook-writer-backlog.md`

Three focused runbooks that the on-call operator reaches for when the corresponding failure mode lands. Each describes the symptom, the exact recovery procedure, and the success signal.

- [ ] **Step 1: Write `runbook-manticore-rebuild.md`**

```markdown
# Runbook — Rebuild Manticore from zero

**Symptom:** `idx_opportunities_rt` is corrupt, empty, or serving wildly stale data.
Serving returns 503 or wrong results.

**Cause:** Manticore node restarted without its persistent volume, or an
admin wiped the index.

**Recovery (60–120 min at current scale):**

1. Confirm R2 is intact:
   `aws s3 ls s3://opportunities-log/canonicals_current/ | wc -l` — expect ≥ 200.
2. `curl -XPOST $MANTICORE_URL/sql?mode=raw -d "query=DROP TABLE IF EXISTS idx_opportunities_rt"`
3. Re-provision the schema:
   `kubectl -n prod apply -f definitions/manticore/idx_opportunities_rt.sql` (or
   re-run the Phase 2 migration).
4. Reset the materializer watermark to epoch 0:
   `redis-cli SET mat:watermark:canonicals ""` etc. for every partition.
5. Restart the materializer: `kubectl rollout restart deploy/materializer`.
6. Watch: `kubectl logs -f deploy/materializer | grep "manticore upsert"`
   — expect ≥ 500 upserts/min for the first hour.
7. Check row count every 10 min:
   `curl -s $MANTICORE_URL/sql?mode=raw -d "query=SELECT COUNT(*) FROM idx_opportunities_rt"`
8. Lift the serving 503 when count passes the launch threshold (50k).

**Success signal:** `/api/v2/search?q=engineer` returns ≥ 10 hits.
```

- [ ] **Step 2: Write `runbook-kv-rebuild.md`**

```markdown
# Runbook — Rebuild Valkey KV from R2

**Symptom:** Dedup lookups all miss; cluster explosion in logs;
duplicate canonicals appearing in search.

**Cause:** Valkey replica loss, zone outage, or a bad `FLUSHDB`.

**Recovery (5–15 min):**

1. Confirm Valkey is reachable:
   `redis-cli -u $VALKEY_URL PING` → `PONG`
2. Call the admin endpoint:
   `curl -XPOST $WORKER_URL/_admin/kv/rebuild`
3. Watch the response — it returns counters:
   ```
   {"rows":12345,"dedup_keys_set":12345,"cluster_keys_set":12345,"files_scanned":48}
   ```
4. Verify a random key:
   `redis-cli -u $VALKEY_URL GET dedup:<any hard_key>` → returns a cluster_id.

**Success signal:** Writer logs stop emitting "dedup:miss on hard_key" at
high rate; canonical upsert rate on `jobs.canonicals.upserted.v1` returns
to normal within 10 min.
```

- [ ] **Step 3: Write `runbook-writer-backlog.md`**

```markdown
# Runbook — Writer pub/sub backlog

**Symptom:** NATS `/jsz` reports `num_pending` on the writer consumer
group above 500k; Grafana dashboard flags yellow/red on `writer_lag`.

**Cause:** R2 outage, writer pod crashloop, or sudden crawl burst above
writer HPA ceiling.

**Recovery:**

1. Check R2 reachability from a writer pod:
   `kubectl -n prod exec <writer-pod> -- curl -sI https://$R2_ENDPOINT/`
2. Check writer pod count + HPA:
   `kubectl get hpa writer -n prod`
3. If HPA is not at ceiling, scale manually to push past it:
   `kubectl scale deploy writer --replicas=20 -n prod`
4. Verify ack rate recovers:
   `kubectl logs -f -l app=writer --tail=20 | grep "parquet flushed"`
5. If R2 is the bottleneck (5xx responses in writer logs), confirm with
   Cloudflare status page. When R2 returns, ack backlog drains
   automatically.
6. If the backlog was caused by a crawl burst that's still emitting,
   dial down the `scheduler-tick` cadence via Trustage:
   `trustage update opportunities.scheduler.tick --cron "120s"` temporarily.

**Success signal:** `num_pending` drops below 100k and stays there for
15 min.
```

- [ ] **Step 4: Commit**

```bash
git add docs/ops/runbook-manticore-rebuild.md \
        docs/ops/runbook-kv-rebuild.md \
        docs/ops/runbook-writer-backlog.md
git commit -m "docs(ops): rebuild runbooks for Manticore / KV / writer-backlog"
```

---

## Task 19: Post-cut verification — E2E smoke test + k6 script

**Files:**
- Create: `tests/integration/cutover_e2e_test.go`
- Create: `tests/k6/smoke_post_cut.js`

The E2E test exercises the full pipeline end-to-end against real
testcontainers: a seed source is crawled (via a fake connector), the
worker processes the variant through every stage, the writer flushes to
MinIO, the materializer upserts to Manticore, and the api serves the
result. One integration test proves the assembly works.

The k6 script runs against prod and checks spec §11.3: search for 20
common queries, facets populate, detail returns 200, match endpoint
returns non-empty within 500 ms p95.

- [ ] **Step 1: Write the E2E integration test**

```go
// tests/integration/cutover_e2e_test.go
//go:build integration
// +build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCutoverE2E composes testcontainers for Postgres + MinIO +
// Manticore + Valkey + NATS, spins up each of the six app binaries
// in-process, seeds one source, fires scheduler-tick, and asserts the
// resulting job is returned by /api/v2/search within 90 s.
//
// The composition is intentionally heavy — this is the single "does
// the cutover stack actually work end-to-end" smoke that gets re-run
// before every prod deploy.
func TestCutoverE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("cutover e2e skipped under -short")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := bringUpEnvironment(t, ctx) // helper wires all five containers
	defer env.Close()

	// Seed one source into Postgres.
	env.SeedSource(t, ctx, seedSource{
		Type: "testdata-static", URL: "https://example.test/jobs",
	})

	// Start all six apps in-process (helpers mirror apps/<x>/cmd/main.go).
	apps := startApps(t, ctx, env)
	defer apps.Stop()

	// Fire scheduler-tick.
	require.NoError(t, apps.Crawler.Tick(ctx))

	// Poll /api/v2/search until the seeded job appears.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		hits := apps.API.Search(ctx, t, "testjob")
		if len(hits) > 0 {
			require.Equal(t, "Seed Job 1", hits[0].Title)
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("e2e: no hits after 90s")
}
```

The helpers (`bringUpEnvironment`, `startApps`, `seedSource`, `apps.*`) already exist in patterns across the Phase 3–5 test suites (`apps/worker/service/service_test.go`, `apps/crawler/service/e2e_test.go`, `apps/candidates/service/e2e_test.go`). This test composes them. If a helper is missing, add it in `tests/integration/helpers_test.go` following the existing pattern — one `*testcontainers.Container` per service, wired via env vars.

- [ ] **Step 2: Write the k6 smoke script**

```javascript
// tests/k6/smoke_post_cut.js
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';

const BASE = __ENV.API_URL || 'https://stawi.opportunities';

const queries = [
    'engineer', 'designer', 'sales', 'remote', 'kenya',
    'python', 'go', 'product manager', 'data scientist',
    'marketing', 'customer success', 'devops', 'intern',
    'senior', 'junior', 'lead', 'manager', 'analyst',
    'nurse', 'teacher',
];

const searchLatency = new Trend('search_latency');
const detailLatency = new Trend('detail_latency');

export const options = {
    scenarios: {
        search: {
            executor: 'constant-vus',
            vus: 5, duration: '2m',
            exec: 'smokeSearch',
        },
        detail: {
            executor: 'constant-vus',
            vus: 2, duration: '2m',
            exec: 'smokeDetail',
            startTime: '30s',
        },
    },
    thresholds: {
        'search_latency': ['p(95) < 500'],
        'detail_latency': ['p(95) < 300'],
        'http_req_failed': ['rate < 0.01'],
    },
};

export function smokeSearch() {
    const q = queries[Math.floor(Math.random() * queries.length)];
    const res = http.get(`${BASE}/api/v2/search?q=${encodeURIComponent(q)}&limit=20`);
    check(res, {
        '200 OK': (r) => r.status === 200,
        'has results': (r) => {
            try { return JSON.parse(r.body).results.length > 0; } catch (e) { return false; }
        },
        'has facets': (r) => {
            try { return Object.keys(JSON.parse(r.body).facets || {}).length > 0; } catch (e) { return false; }
        },
    });
    searchLatency.add(res.timings.duration);
    sleep(0.2);
}

export function smokeDetail() {
    // Seed a few slugs known to be live — populate from the repo or
    // derive from a /api/v2/jobs/latest call at setup.
    const seedSlugs = (__ENV.DETAIL_SLUGS || '').split(',').filter(Boolean);
    if (seedSlugs.length === 0) {
        const res = http.get(`${BASE}/api/v2/jobs/latest?limit=5`);
        if (res.status === 200) {
            try {
                const results = JSON.parse(res.body).results || [];
                results.forEach((r) => seedSlugs.push(r.slug));
            } catch (e) { /* skip */ }
        }
    }
    if (seedSlugs.length === 0) return;
    const slug = seedSlugs[Math.floor(Math.random() * seedSlugs.length)];
    const res = http.get(`${BASE}/api/v2/jobs/${slug}`);
    check(res, {
        '200 OK': (r) => r.status === 200,
        'has title': (r) => {
            try { return !!JSON.parse(r.body).title; } catch (e) { return false; }
        },
    });
    detailLatency.add(res.timings.duration);
    sleep(0.5);
}
```

- [ ] **Step 3: Run the integration test locally**

```bash
go test -tags=integration ./tests/integration -run TestCutoverE2E -timeout 5m -v
```

Expected: PASS. If your laptop's Docker can't host all five containers, run in CI.

- [ ] **Step 4: Dry-run k6**

```bash
k6 run --vus 1 --duration 10s -e API_URL=https://staging.stawi.opportunities tests/k6/smoke_post_cut.js
```

Expected: passes thresholds against staging. Don't run against prod until after Step 2.6 of the cutover runbook.

- [ ] **Step 5: Commit**

```bash
git add tests/integration/cutover_e2e_test.go tests/k6/smoke_post_cut.js
git commit -m "test(cutover): e2e integration + k6 smoke for §11.3 verification"
```

---

## Final sanity sweep

Run these locally before declaring the plan complete:

```bash
# 1. Full build from a clean cache
go clean -cache && go build ./...

# 2. Full test sweep including integration
go test ./... -count=1 -timeout 20m

# 3. go vet + staticcheck
go vet ./...

# 4. Migration idempotency
psql "$DATABASE_URL" -f db/migrations/0003_cutover_drop_legacy.sql
psql "$DATABASE_URL" -f db/migrations/0003_cutover_drop_legacy.sql  # twice, must succeed

# 5. Binary starts with the trimmed AutoMigrate list
DATABASE_URL="$DATABASE_URL" DO_DATABASE_MIGRATE=true go run ./apps/crawler/cmd
DATABASE_URL="$DATABASE_URL" DO_DATABASE_MIGRATE=true go run ./apps/candidates/cmd
```

After this plan ships, the platform is fully greenfield:
- Search, browse, detail, categories, stats — all from Manticore.
- CV lifecycle — event-sourced in R2 Parquet, read via `candidatestore`.
- Crawler + worker pipeline — Parquet-first, Postgres only for source metadata.
- Compaction, gate, KV rebuild — Trustage-driven and admin-endpoint-rehearsed.
- Legacy tables, orphan packages, dead handlers — gone.

Phase 6 terminal state matches the spec §4.1 diagram.
