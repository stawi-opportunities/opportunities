# Matching + Applications — Phase 1: Foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the schema foundation (10 new tables — 4 hypertables, 6 OLTP), one composite index, and the three pure Go packages (`pkg/matching/score`, `pkg/applications/state`, `pkg/applications/rules`) that the matching pipeline and applications API will depend on. End state: migrations apply cleanly on a Postgres+TimescaleDB+pgvector container, and the three packages have 100% branch coverage. Nothing user-visible changes yet.

**Architecture:** Append-only / audit data → TimescaleDB hypertables (`create_hypertable` with retention + compression policies). Current-state / OLTP CRUD → regular tables with proper FKs and unique constraints. Three packages encode the design's "single source of truth" rules: scoring function, application state machine, autonomy-rules schema validator. All deterministic, all unit-testable in isolation.

**Tech Stack:** Go 1.26, Postgres 16 + TimescaleDB + pgvector, numbered `.sql` migrations in `db/migrations/`, testify + table-driven tests, `pgregory.net/rapid` for property tests.

**Spec:** `docs/superpowers/specs/2026-05-20-continuous-matching-and-applications-design.md` (§2 data model, §3.4 scoring, §4.4 state machine, §2.4 rules schema).

**Scope boundary:** This plan does NOT add any HTTP route, JetStream consumer, or live data write. Those land in Phase 2 (matching pipeline) and Phase 3 (apps/applications service). The schema and packages here are inert until later phases call them.

---

## File Structure

**Migrations (new — `db/migrations/`):**

- `0009_matching_applications_hypertables.sql` — the four hypertables (single migration since they share Timescale setup).
- `0010_applications_oltp.sql` — `applications`, `application_notes`, `application_attachments`, `application_reminders`.
- `0011_matching_indexes.sql` — `match_rules`, `candidate_match_indexes`, `extension_grants`.
- `0012_opportunities_active_index.sql` — composite index for the fan-out filter.

**Go packages (new):**

- `pkg/matching/score.go` + `pkg/matching/score_test.go` — pure scoring function.
- `pkg/applications/state.go` + `pkg/applications/state_test.go` — state-machine transition table + validator.
- `pkg/applications/rules.go` + `pkg/applications/rules_test.go` — JSON Schema validation of the rules document.

**Test scaffolding (additive):**

- `tests/integration/testhelpers/postgres.go` — `PostgresContainer(t, ctx)` helper that boots `timescale/timescaledb-ha:pg16` (TimescaleDB + pgvector preinstalled) and applies every `db/migrations/*.sql` in order. Reused by Phase 2+ suites.
- `tests/integration/migrations_test.go` — boots the container, asserts all hypertables, indexes, and policies are present after migrations run.

No files modified outside the additions above.

---

## Task 1: Add PostgresContainer test helper

**Files:**

- Create: `tests/integration/testhelpers/postgres.go`

- [ ] **Step 1: Write the helper**

```go
//go:build integration

package testhelpers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer boots TimescaleDB + pgvector (the official ha image
// ships both) and applies every db/migrations/*.sql in numeric order.
// Returns a *sql.DB connected to the freshly migrated database.
//
// Use this in any integration suite that needs a clean DB with the
// project's schema applied. The container is torn down via t.Cleanup.
func PostgresContainer(t *testing.T, ctx context.Context, migrationsDir string) *sql.DB {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "timescale/timescaledb-ha:pg16",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "opportunities_test",
		},
		WaitingFor: tcwait.ForListeningPort("5432/tcp").
			WithStartupTimeout(90 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "start postgres container")
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)

	dsn := fmt.Sprintf("postgres://test:test@%s:%s/opportunities_test?sslmode=disable",
		host, port.Port())
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Eventually(t, func() bool { return db.PingContext(ctx) == nil },
		60*time.Second, 250*time.Millisecond, "postgres ping")

	applyMigrations(t, ctx, db, migrationsDir)
	return db
}

func applyMigrations(t *testing.T, ctx context.Context, db *sql.DB, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "read migrations dir %s", dir)

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err, "read %s", name)
		_, err = db.ExecContext(ctx, string(body))
		require.NoError(t, err, "apply %s", name)
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build -tags integration ./tests/integration/testhelpers/...`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add tests/integration/testhelpers/postgres.go
git commit -m "tests: add PostgresContainer testhelper (timescaledb-ha + auto-apply migrations)"
```

---

## Task 2: Migration 0009 — Hypertables

**Files:**

- Create: `db/migrations/0009_matching_applications_hypertables.sql`

- [ ] **Step 1: Write the migration**

```sql
-- 0009: Hypertables for matching + applications event/audit streams.
--
-- All four are append-only by design. Partition column is included in
-- every PK because TimescaleDB requires the partition column in every
-- unique constraint on a hypertable. event_id / run_id are xid-style
-- so the pair is unique in practice.
--
-- Retention + compression policies are added inline so the policies
-- are part of the schema (re-running this file is idempotent because
-- add_retention_policy + add_compression_policy are themselves
-- idempotent with `if_not_exists => TRUE`).

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- candidate_match_events: every match generated, viewed, dismissed.
CREATE TABLE IF NOT EXISTS candidate_match_events (
    event_id       TEXT        NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    candidate_id   TEXT        NOT NULL,
    opportunity_id TEXT        NOT NULL,
    canonical_id   TEXT        NOT NULL,
    kind           TEXT        NOT NULL,                -- generated|viewed|dismissed|overflow|rules_change_rematch
    path           TEXT        NOT NULL,                -- fanout|gap|candidate_change
    score          DOUBLE PRECISION,
    rerank_score   DOUBLE PRECISION,
    reranker_used  BOOLEAN     NOT NULL DEFAULT FALSE,
    data           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (event_id, occurred_at)
);
SELECT create_hypertable('candidate_match_events', 'occurred_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '7 days');
CREATE INDEX IF NOT EXISTS candidate_match_events_candidate_time_idx
    ON candidate_match_events (candidate_id, occurred_at DESC);
SELECT add_retention_policy('candidate_match_events', INTERVAL '365 days',
                            if_not_exists => TRUE);
ALTER TABLE candidate_match_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'candidate_id'
);
SELECT add_compression_policy('candidate_match_events', INTERVAL '7 days',
                              if_not_exists => TRUE);

-- application_events: canonical audit log for the applications domain.
CREATE TABLE IF NOT EXISTS application_events (
    event_id       TEXT        NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    application_id TEXT        NOT NULL,
    candidate_id   TEXT        NOT NULL,
    kind           TEXT        NOT NULL,                -- created|state_changed|submission_attempted|submission_succeeded|submission_failed|recruiter_replied|note_added|note_edited|note_deleted|attachment_added|attachment_deleted|reminder_set|reminder_done
    from_status    TEXT,
    to_status      TEXT,
    actor          TEXT        NOT NULL,                -- extension|user|system|admin
    data           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (event_id, occurred_at)
);
SELECT create_hypertable('application_events', 'occurred_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '30 days');
CREATE INDEX IF NOT EXISTS application_events_app_time_idx
    ON application_events (application_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS application_events_candidate_time_idx
    ON application_events (candidate_id, occurred_at DESC);
ALTER TABLE application_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'candidate_id'
);
SELECT add_compression_policy('application_events', INTERVAL '14 days',
                              if_not_exists => TRUE);
-- Note: no retention policy on application_events — kept indefinitely
-- (compliance + user-facing history requirement).

-- engagement_events: beacon traffic (view, click, dismiss, apply).
CREATE TABLE IF NOT EXISTS engagement_events (
    event_id       TEXT        NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    candidate_id   TEXT,                                -- nullable for anonymous beacons
    opportunity_id TEXT        NOT NULL,
    kind           TEXT        NOT NULL,                -- view|click|dismiss|apply
    source         TEXT        NOT NULL,                -- extension|web|email
    data           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (event_id, occurred_at)
);
SELECT create_hypertable('engagement_events', 'occurred_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '7 days');
CREATE INDEX IF NOT EXISTS engagement_events_opp_time_idx
    ON engagement_events (opportunity_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS engagement_events_candidate_time_idx
    ON engagement_events (candidate_id, occurred_at DESC)
    WHERE candidate_id IS NOT NULL;
SELECT add_retention_policy('engagement_events', INTERVAL '180 days',
                            if_not_exists => TRUE);
ALTER TABLE engagement_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'opportunity_id'
);
SELECT add_compression_policy('engagement_events', INTERVAL '7 days',
                              if_not_exists => TRUE);

-- match_run_events: per-run telemetry (op only).
CREATE TABLE IF NOT EXISTS match_run_events (
    run_id            TEXT        NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ,
    path              TEXT        NOT NULL,             -- fanout|gap|candidate_change
    triggered_by      TEXT        NOT NULL,             -- canonical_upserted|extension_poll|rules_changed|cv_changed|admin
    candidate_id      TEXT,
    canonical_id      TEXT,
    candidates_scanned INTEGER    NOT NULL DEFAULT 0,
    matches_written   INTEGER     NOT NULL DEFAULT 0,
    status            TEXT        NOT NULL,             -- ok|timeout|skipped|error|bad_embedding
    reranker_status   TEXT,                              -- used|skipped|down
    latency_ms        INTEGER,
    data              JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (run_id, started_at)
);
SELECT create_hypertable('match_run_events', 'started_at',
                         if_not_exists => TRUE,
                         chunk_time_interval => INTERVAL '7 days');
CREATE INDEX IF NOT EXISTS match_run_events_path_status_idx
    ON match_run_events (path, status, started_at DESC);
SELECT add_retention_policy('match_run_events', INTERVAL '90 days',
                            if_not_exists => TRUE);
ALTER TABLE match_run_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'path'
);
SELECT add_compression_policy('match_run_events', INTERVAL '7 days',
                              if_not_exists => TRUE);
```

- [ ] **Step 2: Commit**

```bash
git add db/migrations/0009_matching_applications_hypertables.sql
git commit -m "migrations: 0009 hypertables for matching + applications events"
```

---

## Task 3: Migration 0010 — Applications OLTP

**Files:**

- Create: `db/migrations/0010_applications_oltp.sql`

- [ ] **Step 1: Write the migration**

```sql
-- 0010: OLTP tables for the applications domain. Mutable current state;
-- every change appends to application_events (see 0009) for audit.
--
-- application_id is xid-generated by the service (TEXT, fixed 20 chars).
-- (candidate_id, opportunity_id) is UNIQUE — one application per pair.

CREATE TABLE IF NOT EXISTS applications (
    application_id   TEXT        PRIMARY KEY,
    candidate_id     TEXT        NOT NULL,
    opportunity_id   TEXT        NOT NULL,
    match_id         TEXT        NOT NULL,
    status           TEXT        NOT NULL,                -- see state.go
    current_stage    TEXT,
    metadata         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    submitted_at     TIMESTAMPTZ,
    last_event_id    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (candidate_id, opportunity_id)
);
CREATE INDEX IF NOT EXISTS applications_candidate_status_idx
    ON applications (candidate_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS application_notes (
    note_id          TEXT        PRIMARY KEY,
    application_id   TEXT        NOT NULL REFERENCES applications(application_id) ON DELETE CASCADE,
    body             TEXT        NOT NULL,
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS application_notes_app_idx
    ON application_notes (application_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS application_attachments (
    attachment_id    TEXT        PRIMARY KEY,
    application_id   TEXT        NOT NULL REFERENCES applications(application_id) ON DELETE CASCADE,
    r2_key           TEXT        NOT NULL,
    content_type     TEXT        NOT NULL,
    bytes            BIGINT      NOT NULL,
    filename         TEXT,
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS application_attachments_app_idx
    ON application_attachments (application_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS application_reminders (
    reminder_id      TEXT        PRIMARY KEY,
    application_id   TEXT        NOT NULL REFERENCES applications(application_id) ON DELETE CASCADE,
    due_at           TIMESTAMPTZ NOT NULL,
    status           TEXT        NOT NULL,                -- pending|done|cancelled
    note             TEXT,
    completed_at     TIMESTAMPTZ,
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS application_reminders_due_idx
    ON application_reminders (due_at)
    WHERE status = 'pending' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS application_reminders_app_idx
    ON application_reminders (application_id, due_at)
    WHERE deleted_at IS NULL;
```

- [ ] **Step 2: Commit**

```bash
git add db/migrations/0010_applications_oltp.sql
git commit -m "migrations: 0010 applications + notes + attachments + reminders"
```

---

## Task 4: Migration 0011 — Matching state + indexes + extension grants

**Files:**

- Create: `db/migrations/0011_matching_indexes.sql`

- [ ] **Step 1: Write the migration**

```sql
-- 0011: match_rules (per-candidate autonomy doc), candidate_match_indexes
-- (denormalized vector + filter bag the fan-out worker reads), and
-- extension_grants (per-install JWT claim tracking).

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS match_rules (
    candidate_id     TEXT        PRIMARY KEY,
    document         JSONB       NOT NULL,                -- validated by pkg/applications/rules
    version          INTEGER     NOT NULL DEFAULT 1,
    enabled          BOOLEAN     NOT NULL DEFAULT TRUE,   -- denormalized for cheap filter
    autoapply        BOOLEAN     NOT NULL DEFAULT FALSE,  -- denormalized
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS candidate_match_indexes (
    candidate_id     TEXT        PRIMARY KEY,
    embedding        vector(1536) NOT NULL,
    min_score        DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    daily_cap        INTEGER     NOT NULL DEFAULT 25,
    weekly_cap       INTEGER     NOT NULL DEFAULT 100,
    kinds            TEXT[]      NOT NULL DEFAULT ARRAY['job']::TEXT[],
    countries        TEXT[]      NOT NULL DEFAULT '{}'::TEXT[],
    salary_floor_usd INTEGER,
    remote_only      BOOLEAN     NOT NULL DEFAULT FALSE,
    enabled          BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- HNSW index for the fan-out KNN. Cosine distance to match pgsearch.
CREATE INDEX IF NOT EXISTS candidate_match_indexes_embedding_hnsw
    ON candidate_match_indexes
    USING hnsw (embedding vector_cosine_ops)
    WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS candidate_match_indexes_kinds_gin
    ON candidate_match_indexes USING GIN (kinds)
    WHERE enabled = TRUE;

CREATE TABLE IF NOT EXISTS extension_grants (
    grant_id             TEXT        PRIMARY KEY,
    candidate_id         TEXT        NOT NULL,
    extension_install_id TEXT        NOT NULL UNIQUE,
    user_agent           TEXT,
    issued_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at           TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS extension_grants_candidate_idx
    ON extension_grants (candidate_id, issued_at DESC);
```

- [ ] **Step 2: Commit**

```bash
git add db/migrations/0011_matching_indexes.sql
git commit -m "migrations: 0011 match_rules + candidate_match_indexes + extension_grants"
```

---

## Task 5: Migration 0012 — opportunities fan-out index

**Files:**

- Create: `db/migrations/0012_opportunities_active_index.sql`

- [ ] **Step 1: Write the migration**

```sql
-- 0012: composite partial index supporting the fan-out worker's
-- "active visible opportunities posted recently" filter (spec §2.3).
-- Partial index keeps it tiny by excluding hidden + non-active rows
-- (the vast majority over time).

CREATE INDEX IF NOT EXISTS opportunities_active_posted_idx
    ON opportunities (posted_at DESC)
    WHERE status = 'active' AND hidden = FALSE;
```

- [ ] **Step 2: Commit**

```bash
git add db/migrations/0012_opportunities_active_index.sql
git commit -m "migrations: 0012 opportunities composite index for fan-out filter"
```

---

## Task 6: Migrations smoke test

**Files:**

- Create: `tests/integration/migrations_test.go`

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package integration_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// migrationsDir resolves db/migrations relative to this test file.
// (Caller of go test can be in any cwd.)
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..", "db", "migrations")
}

func TestMigrationsApplyCleanly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	db := testhelpers.PostgresContainer(t, ctx, migrationsDir(t))

	// Hypertables registered with Timescale.
	for _, name := range []string{
		"candidate_match_events",
		"application_events",
		"engagement_events",
		"match_run_events",
	} {
		var count int
		err := db.QueryRowContext(ctx, `
			SELECT count(*)
			FROM timescaledb_information.hypertables
			WHERE hypertable_name = $1
		`, name).Scan(&count)
		require.NoError(t, err, "query hypertables for %s", name)
		require.Equal(t, 1, count, "%s should be a hypertable", name)
	}

	// Retention policy on candidate_match_events.
	var retentionDays int
	err := db.QueryRowContext(ctx, `
		SELECT EXTRACT(DAY FROM (config->>'drop_after')::INTERVAL)::INT
		FROM timescaledb_information.jobs
		WHERE proc_name = 'policy_retention'
		  AND hypertable_name = 'candidate_match_events'
	`).Scan(&retentionDays)
	require.NoError(t, err)
	require.Equal(t, 365, retentionDays)

	// Compression policy on application_events.
	var compressDays int
	err = db.QueryRowContext(ctx, `
		SELECT EXTRACT(DAY FROM (config->>'compress_after')::INTERVAL)::INT
		FROM timescaledb_information.jobs
		WHERE proc_name = 'policy_compression'
		  AND hypertable_name = 'application_events'
	`).Scan(&compressDays)
	require.NoError(t, err)
	require.Equal(t, 14, compressDays)

	// OLTP tables present.
	for _, name := range []string{
		"applications", "application_notes",
		"application_attachments", "application_reminders",
		"match_rules", "candidate_match_indexes", "extension_grants",
	} {
		var exists bool
		err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_name = $1
			)
		`, name).Scan(&exists)
		require.NoError(t, err, "check %s exists", name)
		require.True(t, exists, "%s should exist", name)
	}

	// pgvector extension present.
	var hasVector bool
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')`,
	).Scan(&hasVector))
	require.True(t, hasVector, "pgvector should be enabled")

	// HNSW index on candidate_match_indexes.
	var hnswExists bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE indexname = 'candidate_match_indexes_embedding_hnsw'
		)
	`).Scan(&hnswExists))
	require.True(t, hnswExists, "HNSW index should exist")

	// Partial opportunities index. (opportunities table itself
	// exists from earlier migration 0001/0003.)
	var idxExists bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE indexname = 'opportunities_active_posted_idx'
		)
	`).Scan(&idxExists))
	require.True(t, idxExists, "opportunities_active_posted_idx should exist")
}
```

- [ ] **Step 2: Run the test — expect it to pass**

Run: `go test -tags integration -run TestMigrationsApplyCleanly -v ./tests/integration/...`
Expected: PASS. (All migrations land cleanly on a fresh container.)

If the test FAILS:

- Read the error carefully. Most likely a missing column the partial index references (e.g. `opportunities.hidden` or `opportunities.status` not present at this point in the migration history).
- If the column is genuinely missing from earlier migrations, you've found a real schema bug — fix the earlier migration or move 0012's index into a column-checking conditional. Do NOT add the column inside 0012.

- [ ] **Step 3: Commit**

```bash
git add tests/integration/migrations_test.go
git commit -m "tests: integration smoke for new migrations (hypertables, policies, indexes)"
```

---

## Task 7: Scoring function — write the failing tests

**Files:**

- Create: `pkg/matching/score_test.go`

- [ ] **Step 1: Write the test file**

```go
package matching_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestScore_CosineOnly(t *testing.T) {
	// Two identical normalized vectors → cosine = 1, score == w1.
	cand := matching.CandidateSignal{Embedding: []float32{1, 0, 0}}
	opp := matching.OpportunitySignal{Embedding: []float32{1, 0, 0}}
	w := matching.DefaultWeights()
	got := matching.Score(cand, opp, w, time.Now())
	require.InDelta(t, w.Cosine, got.Total, 1e-6)
	require.InDelta(t, 1.0, got.Cosine, 1e-6)
}

func TestScore_OrthogonalVectors(t *testing.T) {
	cand := matching.CandidateSignal{Embedding: []float32{1, 0, 0}}
	opp := matching.OpportunitySignal{Embedding: []float32{0, 1, 0}}
	got := matching.Score(cand, opp, matching.DefaultWeights(), time.Now())
	require.InDelta(t, 0.0, got.Cosine, 1e-6)
}

func TestScore_SkillsOverlap(t *testing.T) {
	cand := matching.CandidateSignal{
		Embedding: []float32{1, 0, 0},
		Skills:    []string{"go", "postgres", "kubernetes"},
	}
	opp := matching.OpportunitySignal{
		Embedding: []float32{1, 0, 0},
		Skills:    []string{"go", "postgres"},
	}
	w := matching.DefaultWeights()
	got := matching.Score(cand, opp, w, time.Now())
	// 2 of 3 candidate skills present → 0.666...
	require.InDelta(t, 2.0/3.0, got.SkillsOverlap, 1e-6)
	require.InDelta(t, w.Cosine*1.0+w.Skills*(2.0/3.0), got.Total, 1e-6)
}

func TestScore_GeoMatch(t *testing.T) {
	tests := []struct {
		name string
		cand []string
		opp  string
		want float64
	}{
		{"exact match", []string{"KE", "UG"}, "KE", 1.0},
		{"no overlap", []string{"KE"}, "ZA", 0.0},
		{"remote token matches anywhere", []string{"remote"}, "ZA", 1.0},
		{"no opp country is neutral 1", []string{"KE"}, "", 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cand := matching.CandidateSignal{
				Embedding: []float32{1, 0, 0}, Countries: tc.cand,
			}
			opp := matching.OpportunitySignal{
				Embedding: []float32{1, 0, 0}, Country: tc.opp,
			}
			got := matching.Score(cand, opp, matching.DefaultWeights(), time.Now())
			require.Equal(t, tc.want, got.GeoMatch)
		})
	}
}

func TestScore_SalaryFit(t *testing.T) {
	floor := 50000
	tests := []struct {
		name       string
		candFloor  *int
		oppMax     *int
		wantFitMin float64
		wantFitMax float64
	}{
		{"opp max above floor — perfect", &floor, ptrInt(80000), 1.0, 1.0},
		{"opp max equals floor — perfect", &floor, ptrInt(50000), 1.0, 1.0},
		{"opp max below floor — 0", &floor, ptrInt(30000), 0.0, 0.0},
		{"no candidate floor — neutral 1", nil, ptrInt(30000), 1.0, 1.0},
		{"no opp max — neutral 1", &floor, nil, 1.0, 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cand := matching.CandidateSignal{
				Embedding: []float32{1, 0, 0}, SalaryFloorUSD: tc.candFloor,
			}
			opp := matching.OpportunitySignal{
				Embedding: []float32{1, 0, 0}, SalaryMaxUSD: tc.oppMax,
			}
			got := matching.Score(cand, opp, matching.DefaultWeights(), time.Now())
			require.GreaterOrEqual(t, got.SalaryFit, tc.wantFitMin)
			require.LessOrEqual(t, got.SalaryFit, tc.wantFitMax)
		})
	}
}

func TestScore_StalePenalty(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cand := matching.CandidateSignal{Embedding: []float32{1, 0, 0}}
	tests := []struct {
		name      string
		firstSeen time.Time
		wantPen   float64
	}{
		{"fresh — 0 penalty", now.Add(-1 * time.Hour), 0.0},
		{"7 days old — 0 penalty", now.Add(-7 * 24 * time.Hour), 0.0},
		{"30 days old — partial penalty", now.Add(-30 * 24 * time.Hour), 0.5},
		{"60 days old — full penalty", now.Add(-60 * 24 * time.Hour), 1.0},
		{"future seen (clock skew) — 0", now.Add(1 * time.Hour), 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opp := matching.OpportunitySignal{
				Embedding:   []float32{1, 0, 0},
				FirstSeenAt: tc.firstSeen,
			}
			got := matching.Score(cand, opp, matching.DefaultWeights(), now)
			require.InDelta(t, tc.wantPen, got.StalePenalty, 0.01)
		})
	}
}

func TestScore_Bounded(t *testing.T) {
	// Total is bounded into [0, sum(positive weights)].
	cand := matching.CandidateSignal{
		Embedding: []float32{1, 0, 0},
		Skills:    []string{"go"},
		Countries: []string{"KE"},
	}
	opp := matching.OpportunitySignal{
		Embedding:   []float32{1, 0, 0},
		Skills:      []string{"go"},
		Country:     "KE",
		FirstSeenAt: time.Now(),
	}
	w := matching.DefaultWeights()
	got := matching.Score(cand, opp, w, time.Now())
	maxPossible := w.Cosine + w.Skills + w.Geo + w.Salary
	require.GreaterOrEqual(t, got.Total, 0.0)
	require.LessOrEqual(t, got.Total, maxPossible)
}

func TestScore_MissingEmbeddings(t *testing.T) {
	// Empty embedding on either side → cosine = 0, score still defined.
	cand := matching.CandidateSignal{Embedding: nil}
	opp := matching.OpportunitySignal{Embedding: []float32{1, 0, 0}}
	got := matching.Score(cand, opp, matching.DefaultWeights(), time.Now())
	require.InDelta(t, 0.0, got.Cosine, 1e-9)
	require.False(t, math.IsNaN(got.Total))
}

func ptrInt(v int) *int { return &v }
```

- [ ] **Step 2: Run tests — expect compile error**

Run: `go test ./pkg/matching/... -run TestScore -v`
Expected: FAIL with `no Go files in .../pkg/matching` or `undefined: matching.Score`.

- [ ] **Step 3: Commit the failing test**

```bash
git add pkg/matching/score_test.go
git commit -m "test(matching): scoring function table tests (TDD red)"
```

---

## Task 8: Scoring function — implementation

**Files:**

- Create: `pkg/matching/score.go`

- [ ] **Step 1: Write the implementation**

```go
// Package matching contains the deterministic scoring function called
// by every matching path (fan-out, gap-filler, candidate-side rematch).
// Keep this file pure: no DB, no I/O, no time.Now() (caller passes now
// in so tests can pin it).
package matching

import (
	"math"
	"strings"
	"time"
)

// Weights configures the scoring function. Loaded from env at startup;
// changing weights between Score() calls is supported.
type Weights struct {
	Cosine float64 // w1
	Skills float64 // w2
	Geo    float64 // w3
	Salary float64 // w4
	Stale  float64 // p1 (subtracted)
}

// DefaultWeights are the starting weights. Tuned in env, not in code.
func DefaultWeights() Weights {
	return Weights{
		Cosine: 0.60,
		Skills: 0.15,
		Geo:    0.15,
		Salary: 0.10,
		Stale:  0.10,
	}
}

// CandidateSignal is the per-candidate input to scoring. Comes from
// candidate_match_indexes.
type CandidateSignal struct {
	Embedding      []float32
	Skills         []string
	Countries      []string // includes "remote" sentinel if remote-OK
	SalaryFloorUSD *int
}

// OpportunitySignal is the per-opportunity input to scoring.
type OpportunitySignal struct {
	Embedding    []float32
	Skills       []string
	Country      string // ISO 3166-1 alpha-2, or empty for unknown
	SalaryMaxUSD *int
	FirstSeenAt  time.Time
}

// Result is the score breakdown. Stored on candidate_match_events so
// operators can answer "why was this matched."
type Result struct {
	Cosine        float64
	SkillsOverlap float64
	GeoMatch      float64
	SalaryFit     float64
	StalePenalty  float64
	Total         float64
}

// Score computes a deterministic match score. now is provided by the
// caller so tests can pin time.
func Score(cand CandidateSignal, opp OpportunitySignal, w Weights, now time.Time) Result {
	r := Result{
		Cosine:        cosineSimilarity(cand.Embedding, opp.Embedding),
		SkillsOverlap: skillsOverlap(cand.Skills, opp.Skills),
		GeoMatch:      geoMatch(cand.Countries, opp.Country),
		SalaryFit:     salaryFit(cand.SalaryFloorUSD, opp.SalaryMaxUSD),
		StalePenalty:  stalePenalty(opp.FirstSeenAt, now),
	}
	r.Total = w.Cosine*r.Cosine +
		w.Skills*r.SkillsOverlap +
		w.Geo*r.GeoMatch +
		w.Salary*r.SalaryFit -
		w.Stale*r.StalePenalty
	if r.Total < 0 {
		r.Total = 0
	}
	return r
}

// cosineSimilarity returns 0 when either side is empty or dimensions
// mismatch. We never panic on bad embeddings — a 0 here causes the
// candidate to fall below threshold which is the correct behavior.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// skillsOverlap = |cand ∩ opp| / |cand|. Case-insensitive on the skill
// label. Returns 1.0 when candidate has no skills declared (neutral).
func skillsOverlap(cand, opp []string) float64 {
	if len(cand) == 0 {
		return 1.0
	}
	if len(opp) == 0 {
		return 0.0
	}
	want := make(map[string]struct{}, len(opp))
	for _, s := range opp {
		want[strings.ToLower(s)] = struct{}{}
	}
	var hits float64
	for _, s := range cand {
		if _, ok := want[strings.ToLower(s)]; ok {
			hits++
		}
	}
	return hits / float64(len(cand))
}

// geoMatch is 1 when the candidate accepts this country or "remote".
// 1 when the opportunity has no country (neutral). 0 otherwise.
func geoMatch(candCountries []string, oppCountry string) float64 {
	if oppCountry == "" {
		return 1.0
	}
	for _, c := range candCountries {
		if strings.EqualFold(c, oppCountry) || strings.EqualFold(c, "remote") {
			return 1.0
		}
	}
	return 0.0
}

// salaryFit is 1 when the opportunity meets or exceeds the candidate's
// floor (or either side is unknown). 0 when opp pays less than floor.
func salaryFit(candFloor, oppMax *int) float64 {
	if candFloor == nil || oppMax == nil {
		return 1.0
	}
	if *oppMax >= *candFloor {
		return 1.0
	}
	return 0.0
}

// stalePenalty ramps from 0 at age 7 days to 1 at age 60 days.
// Future timestamps (clock skew) return 0.
func stalePenalty(firstSeen, now time.Time) float64 {
	age := now.Sub(firstSeen)
	if age <= 7*24*time.Hour {
		return 0.0
	}
	if age >= 60*24*time.Hour {
		return 1.0
	}
	// Linear ramp between 7d and 60d.
	span := float64((60 - 7) * 24 * 60 * 60)
	excess := age.Seconds() - float64(7*24*60*60)
	return excess / span
}

```

- [ ] **Step 2: Run tests — expect pass**

Run: `go test ./pkg/matching/... -run TestScore -v -race`
Expected: PASS for all `TestScore_*` cases.

- [ ] **Step 3: Commit**

```bash
git add pkg/matching/score.go
git commit -m "feat(matching): deterministic scoring function (cosine + skills + geo + salary + stale)"
```

---

## Task 9: Application state machine — failing tests

**Files:**

- Create: `pkg/applications/state_test.go`

- [ ] **Step 1: Write the test file**

```go
package applications_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestState_AllStatesEnumerated(t *testing.T) {
	all := applications.AllStatuses()
	want := []applications.Status{
		applications.StatusNew,
		applications.StatusDismissed,
		applications.StatusApplying,
		applications.StatusSubmitted,
		applications.StatusScreening,
		applications.StatusInterview,
		applications.StatusOffer,
		applications.StatusAccepted,
		applications.StatusRejected,
		applications.StatusWithdrawn,
	}
	require.ElementsMatch(t, want, all)
}

func TestState_TerminalStatuses(t *testing.T) {
	for _, s := range []applications.Status{
		applications.StatusDismissed,
		applications.StatusAccepted,
		applications.StatusRejected,
		applications.StatusWithdrawn,
	} {
		require.True(t, s.IsTerminal(), "%s should be terminal", s)
	}
	for _, s := range []applications.Status{
		applications.StatusNew,
		applications.StatusApplying,
		applications.StatusSubmitted,
		applications.StatusScreening,
		applications.StatusInterview,
		applications.StatusOffer,
	} {
		require.False(t, s.IsTerminal(), "%s should not be terminal", s)
	}
}

func TestState_HappyPath(t *testing.T) {
	path := []applications.Status{
		applications.StatusNew,
		applications.StatusApplying,
		applications.StatusSubmitted,
		applications.StatusScreening,
		applications.StatusInterview,
		applications.StatusOffer,
		applications.StatusAccepted,
	}
	for i := 0; i < len(path)-1; i++ {
		require.NoError(t, applications.ValidateTransition(path[i], path[i+1]),
			"transition %s → %s should be allowed", path[i], path[i+1])
	}
}

func TestState_InvalidTransitions(t *testing.T) {
	cases := []struct {
		from, to applications.Status
	}{
		{applications.StatusNew, applications.StatusSubmitted},        // skip applying
		{applications.StatusNew, applications.StatusOffer},             // skip everything
		{applications.StatusAccepted, applications.StatusApplying},     // terminal → anything
		{applications.StatusRejected, applications.StatusApplying},     // terminal
		{applications.StatusDismissed, applications.StatusApplying},    // terminal
		{applications.StatusSubmitted, applications.StatusNew},         // can't go backwards
		{applications.StatusInterview, applications.StatusApplying},    // backwards
	}
	for _, tc := range cases {
		t.Run(string(tc.from)+"_to_"+string(tc.to), func(t *testing.T) {
			err := applications.ValidateTransition(tc.from, tc.to)
			require.Error(t, err)
			var ie *applications.InvalidTransitionError
			require.ErrorAs(t, err, &ie)
			require.Equal(t, tc.from, ie.From)
			require.Equal(t, tc.to, ie.To)
			require.NotEmpty(t, ie.Allowed, "error should list allowed next states")
		})
	}
}

func TestState_DropToRejectedOrWithdrawnFromAnyNonTerminal(t *testing.T) {
	for _, from := range []applications.Status{
		applications.StatusApplying,
		applications.StatusSubmitted,
		applications.StatusScreening,
		applications.StatusInterview,
		applications.StatusOffer,
	} {
		for _, to := range []applications.Status{
			applications.StatusRejected,
			applications.StatusWithdrawn,
		} {
			require.NoError(t, applications.ValidateTransition(from, to),
				"%s → %s should be allowed", from, to)
		}
	}
}

func TestState_NewCanGoToDismissedOrApplying(t *testing.T) {
	require.NoError(t, applications.ValidateTransition(
		applications.StatusNew, applications.StatusDismissed))
	require.NoError(t, applications.ValidateTransition(
		applications.StatusNew, applications.StatusApplying))
}

func TestState_AllowedNext(t *testing.T) {
	got := applications.AllowedNext(applications.StatusInterview)
	require.ElementsMatch(t, []applications.Status{
		applications.StatusOffer,
		applications.StatusRejected,
		applications.StatusWithdrawn,
	}, got)
}

func TestState_UnknownStatus(t *testing.T) {
	err := applications.ValidateTransition(applications.Status("bogus"), applications.StatusApplying)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run tests — expect compile error**

Run: `go test ./pkg/applications/... -run TestState -v`
Expected: FAIL with `undefined: applications.Status` etc.

- [ ] **Step 3: Commit**

```bash
git add pkg/applications/state_test.go
git commit -m "test(applications): state machine table tests (TDD red)"
```

---

## Task 10: Application state machine — implementation

**Files:**

- Create: `pkg/applications/state.go`

- [ ] **Step 1: Write the implementation**

```go
// Package applications encapsulates the rules of the applications
// domain: the state machine (this file) and the autonomy-rules
// document schema (rules.go). Pure code only — no DB, no HTTP.
package applications

import "fmt"

// Status is the lifecycle state of an application.
type Status string

const (
	StatusNew       Status = "new"
	StatusDismissed Status = "dismissed"
	StatusApplying  Status = "applying"
	StatusSubmitted Status = "submitted"
	StatusScreening Status = "screening"
	StatusInterview Status = "interview"
	StatusOffer     Status = "offer"
	StatusAccepted  Status = "accepted"
	StatusRejected  Status = "rejected"
	StatusWithdrawn Status = "withdrawn"
)

// transitions encodes the allowed-next states for each status.
// Single source of truth — referenced by ValidateTransition and
// AllowedNext.
var transitions = map[Status][]Status{
	StatusNew: {
		StatusDismissed,
		StatusApplying,
	},
	StatusApplying: {
		StatusSubmitted,
		StatusRejected,
		StatusWithdrawn,
	},
	StatusSubmitted: {
		StatusScreening,
		StatusRejected,
		StatusWithdrawn,
	},
	StatusScreening: {
		StatusInterview,
		StatusRejected,
		StatusWithdrawn,
	},
	StatusInterview: {
		StatusOffer,
		StatusRejected,
		StatusWithdrawn,
	},
	StatusOffer: {
		StatusAccepted,
		StatusRejected,
		StatusWithdrawn,
	},
	// Terminal states have no outgoing edges.
	StatusDismissed: {},
	StatusAccepted:  {},
	StatusRejected:  {},
	StatusWithdrawn: {},
}

// terminalSet for fast IsTerminal checks.
var terminalSet = map[Status]bool{
	StatusDismissed: true,
	StatusAccepted:  true,
	StatusRejected:  true,
	StatusWithdrawn: true,
}

// IsTerminal reports whether the status admits no further transitions.
func (s Status) IsTerminal() bool {
	return terminalSet[s]
}

// AllStatuses returns every defined status. Useful for enumeration in
// validators and tests.
func AllStatuses() []Status {
	return []Status{
		StatusNew,
		StatusDismissed,
		StatusApplying,
		StatusSubmitted,
		StatusScreening,
		StatusInterview,
		StatusOffer,
		StatusAccepted,
		StatusRejected,
		StatusWithdrawn,
	}
}

// AllowedNext returns the list of statuses a row currently in `from`
// is allowed to transition to. Empty slice for terminal or unknown.
func AllowedNext(from Status) []Status {
	out := transitions[from]
	cp := make([]Status, len(out))
	copy(cp, out)
	return cp
}

// InvalidTransitionError reports an attempt to move between states
// that the transition table forbids. Carries the allowed set so HTTP
// handlers can put it in the 409 response body.
type InvalidTransitionError struct {
	From    Status
	To      Status
	Allowed []Status
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf(
		"applications: invalid transition %s → %s (allowed: %v)",
		e.From, e.To, e.Allowed,
	)
}

// ValidateTransition returns nil if `from → to` is allowed by the
// transition table, otherwise an *InvalidTransitionError.
func ValidateTransition(from, to Status) error {
	allowed, known := transitions[from]
	if !known {
		return &InvalidTransitionError{From: from, To: to}
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return &InvalidTransitionError{From: from, To: to, Allowed: allowed}
}
```

- [ ] **Step 2: Run tests — expect pass**

Run: `go test ./pkg/applications/... -run TestState -v -race`
Expected: PASS for every `TestState_*` case.

- [ ] **Step 3: Commit**

```bash
git add pkg/applications/state.go
git commit -m "feat(applications): state machine + transition validator"
```

---

## Task 11: Property test for state machine

**Files:**

- Create: `pkg/applications/state_property_test.go`

- [ ] **Step 1: Add the rapid property test**

```go
package applications_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

// TestState_AnyValidSequenceLandsInReachable asserts that any path of
// valid transitions starting at StatusNew lands in a state the
// transition graph declares reachable from StatusNew. This catches
// accidental graph holes (e.g. a state with no incoming edges).
func TestState_AnyValidSequenceLandsInReachable(t *testing.T) {
	reachable := buildReachableSet(applications.StatusNew)

	rapid.Check(t, func(rt *rapid.T) {
		cur := applications.StatusNew
		// Walk up to 20 steps, picking a random allowed next.
		for step := 0; step < 20; step++ {
			next := applications.AllowedNext(cur)
			if len(next) == 0 {
				break
			}
			chosen := next[rapid.IntRange(0, len(next)-1).Draw(rt, "pick")]
			if err := applications.ValidateTransition(cur, chosen); err != nil {
				rt.Fatalf("transition %s → %s reported invalid by validator", cur, chosen)
			}
			cur = chosen
		}
		if !reachable[cur] {
			rt.Fatalf("walked into %s, which is not in the reachable set from new", cur)
		}
	})
}

func buildReachableSet(from applications.Status) map[applications.Status]bool {
	seen := map[applications.Status]bool{from: true}
	queue := []applications.Status{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, n := range applications.AllowedNext(cur) {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return seen
}
```

- [ ] **Step 2: Add the dep**

Run: `go get pgregory.net/rapid@latest && go mod tidy`
Expected: `go.mod` updated.

- [ ] **Step 3: Run tests**

Run: `go test ./pkg/applications/... -run TestState_AnyValidSequence -v`
Expected: PASS (rapid runs ~100 random sequences).

- [ ] **Step 4: Commit**

```bash
git add pkg/applications/state_property_test.go go.mod go.sum
git commit -m "test(applications): property test for state-machine reachability"
```

---

## Task 12: Rules schema validator — failing tests

**Files:**

- Create: `pkg/applications/rules_test.go`

- [ ] **Step 1: Write the test file**

```go
package applications_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestRules_Defaults(t *testing.T) {
	r := applications.DefaultRules()
	require.Equal(t, 1, r.Version)
	require.True(t, r.Enabled)
	require.Equal(t, []string{"job"}, r.Kinds)
	require.False(t, r.Autoapply.Enabled)
}

func TestRules_ValidMinimal(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"min_score":0.6,"kinds":["job"]}`)
	r, err := applications.ParseRules(body)
	require.NoError(t, err)
	require.Equal(t, []string{"job"}, r.Kinds)
	require.InDelta(t, 0.6, r.MinScore, 1e-9)
}

func TestRules_ValidFull(t *testing.T) {
	body := []byte(`{
		"version": 1,
		"enabled": true,
		"min_score": 0.62,
		"daily_cap": 25,
		"weekly_cap": 100,
		"kinds": ["job","scholarship"],
		"countries": ["KE","UG","TZ","remote"],
		"salary_floor_usd": 30000,
		"remote_only": false,
		"dismiss_after_days": 14,
		"blocklist": {"companies":["AcmeCo"],"domains":["example.com"]},
		"autoapply": {"enabled":true,"require_min_score":0.78,"kinds":["job"]}
	}`)
	r, err := applications.ParseRules(body)
	require.NoError(t, err)
	require.Equal(t, 25, r.DailyCap)
	require.True(t, r.Autoapply.Enabled)
	require.Contains(t, r.Blocklist.Companies, "AcmeCo")
}

func TestRules_RejectsUnknownField(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"kinds":["job"],"foo":"bar"}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "foo")
}

func TestRules_RejectsBadVersion(t *testing.T) {
	body := []byte(`{"version":99,"enabled":true,"kinds":["job"]}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
}

func TestRules_RejectsScoreOutOfRange(t *testing.T) {
	cases := []string{
		`{"version":1,"enabled":true,"kinds":["job"],"min_score":-0.1}`,
		`{"version":1,"enabled":true,"kinds":["job"],"min_score":1.5}`,
	}
	for _, body := range cases {
		_, err := applications.ParseRules([]byte(body))
		require.Error(t, err, "should reject %s", body)
	}
}

func TestRules_RejectsEmptyKinds(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"kinds":[]}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
}

func TestRules_RejectsInvalidKind(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"kinds":["unknown_kind"]}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
}

func TestRules_RejectsCapsOutOfBounds(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"kinds":["job"],"daily_cap":0}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)

	body = []byte(`{"version":1,"enabled":true,"kinds":["job"],"daily_cap":10000}`)
	_, err = applications.ParseRules(body)
	require.Error(t, err)
}

func TestRules_AutoapplyMinScoreMustBeAtLeastMinScore(t *testing.T) {
	body := []byte(`{
		"version":1,"enabled":true,"kinds":["job"],
		"min_score":0.7,
		"autoapply":{"enabled":true,"require_min_score":0.5,"kinds":["job"]}
	}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "require_min_score")
}

func TestRules_AutoapplyKindsMustBeSubsetOfKinds(t *testing.T) {
	body := []byte(`{
		"version":1,"enabled":true,"kinds":["job"],
		"autoapply":{"enabled":true,"require_min_score":0.8,"kinds":["scholarship"]}
	}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "autoapply.kinds")
}

func TestRules_RoundTrip(t *testing.T) {
	r := applications.DefaultRules()
	r.MinScore = 0.7
	r.Countries = []string{"KE", "remote"}
	body, err := applications.MarshalRules(r)
	require.NoError(t, err)
	parsed, err := applications.ParseRules(body)
	require.NoError(t, err)
	require.Equal(t, r, parsed)
}
```

- [ ] **Step 2: Run — expect compile error**

Run: `go test ./pkg/applications/... -run TestRules -v`
Expected: FAIL with `undefined: applications.Rules` etc.

- [ ] **Step 3: Commit**

```bash
git add pkg/applications/rules_test.go
git commit -m "test(applications): rules schema validator tests (TDD red)"
```

---

## Task 13: Rules schema validator — implementation

**Files:**

- Create: `pkg/applications/rules.go`

- [ ] **Step 1: Write the implementation**

```go
package applications

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Rules is the per-candidate autonomy document. Stored as JSONB in
// match_rules.document, validated through ParseRules.
//
// Note: pointer-free zero values are the defaults — DefaultRules
// returns the canonical starting point.
type Rules struct {
	Version          int            `json:"version"`
	Enabled          bool           `json:"enabled"`
	MinScore         float64        `json:"min_score"`
	DailyCap         int            `json:"daily_cap"`
	WeeklyCap        int            `json:"weekly_cap"`
	Kinds            []string       `json:"kinds"`
	Countries        []string       `json:"countries,omitempty"`
	SalaryFloorUSD   int            `json:"salary_floor_usd,omitempty"`
	RemoteOnly       bool           `json:"remote_only,omitempty"`
	DismissAfterDays int            `json:"dismiss_after_days,omitempty"`
	Blocklist        Blocklist      `json:"blocklist,omitempty"`
	Autoapply        AutoapplyRules `json:"autoapply,omitempty"`
}

// Blocklist forbids specific companies or domains.
type Blocklist struct {
	Companies []string `json:"companies,omitempty"`
	Domains   []string `json:"domains,omitempty"`
}

// AutoapplyRules gates the browser extension's submission behavior.
// Distinct from Rules.Enabled so the user can keep matches flowing
// while pausing automated submissions.
type AutoapplyRules struct {
	Enabled         bool     `json:"enabled,omitempty"`
	RequireMinScore float64  `json:"require_min_score,omitempty"`
	Kinds           []string `json:"kinds,omitempty"`
}

// validKinds is the closed set of opportunity kinds this design
// supports. Extending this is intentionally a code change — bumping
// the rules `version` is the migration path.
var validKinds = map[string]bool{
	"job":         true,
	"scholarship": true,
	"funding":     true,
	"tender":      true,
	"deal":        true,
}

// DefaultRules returns the starting rule document for a brand-new
// candidate.
func DefaultRules() Rules {
	return Rules{
		Version:          1,
		Enabled:          true,
		MinScore:         0.5,
		DailyCap:         25,
		WeeklyCap:        100,
		Kinds:            []string{"job"},
		DismissAfterDays: 14,
	}
}

// ParseRules decodes the JSON document and validates it against the
// schema described in spec §2.4. Unknown fields are rejected.
func ParseRules(body []byte) (Rules, error) {
	var r Rules
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Rules{}, fmt.Errorf("rules: decode: %w", err)
	}
	if err := r.Validate(); err != nil {
		return Rules{}, err
	}
	return r, nil
}

// MarshalRules serializes a Rules back to canonical JSON.
func MarshalRules(r Rules) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

// Validate enforces the constraints documented in spec §2.4 plus the
// two cross-field invariants (autoapply.require_min_score >= min_score;
// autoapply.kinds ⊆ kinds).
func (r Rules) Validate() error {
	if r.Version != 1 {
		return fmt.Errorf("rules: unsupported version %d (only 1)", r.Version)
	}
	if r.MinScore < 0 || r.MinScore > 1 {
		return fmt.Errorf("rules: min_score %v out of [0,1]", r.MinScore)
	}
	if len(r.Kinds) == 0 {
		return errors.New("rules: kinds must be non-empty")
	}
	for _, k := range r.Kinds {
		if !validKinds[k] {
			return fmt.Errorf("rules: invalid kind %q", k)
		}
	}
	if r.DailyCap <= 0 || r.DailyCap > 1000 {
		return fmt.Errorf("rules: daily_cap %d out of (0, 1000]", r.DailyCap)
	}
	if r.WeeklyCap < r.DailyCap || r.WeeklyCap > 5000 {
		return fmt.Errorf("rules: weekly_cap %d out of [daily_cap, 5000]", r.WeeklyCap)
	}
	if r.DismissAfterDays < 0 || r.DismissAfterDays > 365 {
		return fmt.Errorf("rules: dismiss_after_days %d out of [0, 365]", r.DismissAfterDays)
	}
	if r.SalaryFloorUSD < 0 {
		return fmt.Errorf("rules: salary_floor_usd %d must be >= 0", r.SalaryFloorUSD)
	}
	if r.Autoapply.Enabled {
		if r.Autoapply.RequireMinScore < r.MinScore {
			return fmt.Errorf("rules: autoapply.require_min_score %v must be >= min_score %v",
				r.Autoapply.RequireMinScore, r.MinScore)
		}
		if r.Autoapply.RequireMinScore > 1 {
			return fmt.Errorf("rules: autoapply.require_min_score %v out of [0,1]",
				r.Autoapply.RequireMinScore)
		}
		// kinds ⊆ rules.kinds
		allowed := make(map[string]bool, len(r.Kinds))
		for _, k := range r.Kinds {
			allowed[k] = true
		}
		for _, k := range r.Autoapply.Kinds {
			if !allowed[k] {
				return fmt.Errorf("rules: autoapply.kinds[%q] is not a subset of kinds", k)
			}
		}
	}
	return nil
}

```

- [ ] **Step 2: Run tests — expect pass**

Run: `go test ./pkg/applications/... -run TestRules -v -race`
Expected: PASS for every `TestRules_*` case.

If `TestRules_RoundTrip` fails on equality:

- Likely cause: `DefaultRules()` has fields with `omitempty` JSON tags that survive a round trip. Confirm `r.Countries == parsed.Countries` (both `nil` or both empty `[]string`). The fix is either (a) initialize defaults to non-nil empty slices in `DefaultRules`, or (b) compare with `require.Empty` instead of `require.Equal` for those fields. Pick the option that matches the spec's wire contract (the spec emits `omitempty`, so nil is correct → adjust the test).

- [ ] **Step 3: Commit**

```bash
git add pkg/applications/rules.go
git commit -m "feat(applications): rules document schema + cross-field validator"
```

---

## Task 14: Wire everything into the build

**Files:**

- Modify: `Makefile` (add `make test-integration` if absent; the existing target may already exist)
- Verify: `go.sum` is consistent

- [ ] **Step 1: Check the Makefile**

Run: `grep -E '^(test|test-integration):' Makefile`

Expected output (existing or similar):

```
test:
	go test ./...
```

- [ ] **Step 2: Add integration target if missing**

If `test-integration` is not in the Makefile, append it:

```makefile
# Run integration tests (requires Docker for testcontainers).
# Slow — ~3-5 minutes for the matching+applications phase 1 suite.
test-integration:
	go test -tags integration -count=1 -timeout 10m ./tests/integration/...
```

- [ ] **Step 3: Run the full unit suite**

Run: `go test -race ./pkg/matching/... ./pkg/applications/...`
Expected: PASS, no race warnings.

- [ ] **Step 4: Run the migrations integration test**

Run: `make test-integration`
(Or: `go test -tags integration -timeout 10m -run TestMigrationsApplyCleanly ./tests/integration/...`)

Expected: PASS — the migrations test brings up Timescale/pgvector and asserts the schema is in place.

If the integration test fails because Docker isn't available locally, that's fine — the CI runs it. Skip and let CI verify.

- [ ] **Step 5: Tidy and commit**

```bash
go mod tidy
git add Makefile go.mod go.sum
git diff --cached
git commit -m "build: make test-integration target + tidy after phase 1 deps"
```

---

## Task 15: Phase 1 wrap-up — verify and document

**Files:**

- Modify: `docs/superpowers/specs/2026-05-20-continuous-matching-and-applications-design.md` (mark Phase 1 done in §7)

- [ ] **Step 1: Run the full local suite one more time**

Run: `go test -race ./pkg/...`
Expected: PASS.

- [ ] **Step 2: Confirm the deliverables**

Verify these files exist with non-empty content:

- `db/migrations/0009_matching_applications_hypertables.sql`
- `db/migrations/0010_applications_oltp.sql`
- `db/migrations/0011_matching_indexes.sql`
- `db/migrations/0012_opportunities_active_index.sql`
- `tests/integration/testhelpers/postgres.go`
- `tests/integration/migrations_test.go`
- `pkg/matching/score.go` + `pkg/matching/score_test.go`
- `pkg/applications/state.go` + `pkg/applications/state_test.go` + `pkg/applications/state_property_test.go`
- `pkg/applications/rules.go` + `pkg/applications/rules_test.go`

Run: `ls -la db/migrations/ pkg/matching/ pkg/applications/ tests/integration/testhelpers/postgres.go`

- [ ] **Step 3: Append a Phase 1 done note to the spec's §7**

Edit `docs/superpowers/specs/2026-05-20-continuous-matching-and-applications-design.md`, replace `None as of writing — every decision has a chosen option recorded in the relevant section. Questions that arise during planning should be appended here before the writing-plans transition.` with:

```
None as of writing — every decision has a chosen option recorded in the
relevant section. Questions that arise during planning should be appended
here before the writing-plans transition.

## 8. Progress log

- **Phase 1 (foundation) — done.** Schema migrations 0009–0012 land four
  hypertables, the applications OLTP tables, match_rules,
  candidate_match_indexes, and extension_grants. Three pure packages
  (`pkg/matching/score`, `pkg/applications/state`,
  `pkg/applications/rules`) carry the scoring function, state machine,
  and rules schema validator. Integration test `TestMigrationsApplyCleanly`
  smokes the schema on a fresh TimescaleDB + pgvector container.
```

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/specs/2026-05-20-continuous-matching-and-applications-design.md
git commit -m "spec: mark phase 1 (foundation) complete in progress log"
```

---

## Phase 1 acceptance

Phase 1 is complete when:

1. All four migrations apply cleanly on a fresh `timescale/timescaledb-ha:pg16` container with `vector` extension.
2. Hypertables, retention policies, compression policies, and the HNSW index are present and verified by `TestMigrationsApplyCleanly`.
3. `pkg/matching/score.go` exposes `Score(cand, opp, weights, now)` with deterministic, bounded output; tests pass `-race`.
4. `pkg/applications/state.go` exposes `Status`, `ValidateTransition`, `AllowedNext`, `IsTerminal`; transition table covers the design's state machine; property test passes.
5. `pkg/applications/rules.go` parses + validates the rule document including cross-field invariants; round-trip works; unknown fields rejected.
6. `make test-integration` passes locally (when Docker available) and in CI.
7. Spec's §8 progress log reflects Phase 1 completion.

No HTTP routes, no consumers, no user-visible behavior changes. The next plan (Phase 2 — matching pipeline) builds on top of these.

---

## Follow-on plans (not in scope here)

The remaining phases of §5.5 of the spec each get their own plan document:

- **Phase 2 — Matching pipeline** (Path A fan-out worker, Path B gap-filler endpoint, Path C candidate-side rematch, reranker integration, match_run_events plumbing, DLQ wiring, MatchingPipelineSuite).
- **Phase 3 — Applications service** (new `apps/applications` binary, HTTP CRUD endpoints, application_events emission, idempotency middleware, ApplicationsLifecycleSuite).
- **Phase 4 — Extension-facing endpoints** (`/api/me/matches*`, `/api/me/profile-fields`, `/api/me/attachments*`, `/api/me/reminders*`, OpenAPI publication, ExtensionContractSuite).
- **Phase 5 — Observability** (Prometheus metrics, Grafana dashboards, continuous aggregates, alerts).
- **Phase 6 — Legacy retirement** (`/api/v2 → /api/*` rename, delete manticore code path, delete `apps/api/cmd/endpoints_v2*.go`, drop `candidate_profiles.embedding`, retire weekly digest, drop legacy `candidate_matches` after the 30-day quiet window).

Each follow-on plan starts with a 1-paragraph context recap pointing back to the spec and to this plan.
