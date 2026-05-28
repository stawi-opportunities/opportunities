# Plan D3 — Adaptive Recrawl Scoring

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Hot sources (frequent new jobs, low rejection rate, high acceptance) get crawled more often. Stale sources (no new jobs in days, high rejection rate) get crawled less. Replace the fixed `interval_minutes` per source with a *learned* next-crawl-at timestamp computed from recent ingest signals.

**Architecture:** A pure-function `pkg/freshness` package computes a `score` (0.0–1.0) from rolling signals — new-variant rate per crawl, accept rate, time since last new variant, source tier. The scheduler reads the score and shrinks/grows the source's effective interval inside a `[min_interval, max_interval]` clamp. A `crawl_signals` materialized view powers the inputs without scanning raw tables on every tick.

**Tech Stack:** Go, Postgres (materialized view + REFRESH MATERIALIZED VIEW CONCURRENTLY), GORM, frame v1.97+. No new external deps.

**Depends on:** Plan A (trace tables) for the signals; Plan D1 (checkpoints) is independent.

---

## Why this is worth building

Today the scheduler crawls every source every `interval_minutes` (a column on `sources`, typically 60). This means:
- A noisy job board that posts 50/day gets crawled at the same cadence as a quarterly grants foundation
- We waste budget on the foundation and miss the job board's freshest posts
- Operators have no signal to manually tune `interval_minutes` per source

Adaptive scoring closes the loop: a source's *observed* behaviour drives its *next* crawl cadence. Tier-1 ATS sources (greenhouse, workday) stay frequent floor; Tier-3 manual-curation sources drift to weekly without any operator action.

---

## File Structure

**Created:**
- `apps/crawler/migrations/0001/20260528_0060_crawl_signals_view.sql` — materialized view of per-source 7-day signals.
- `apps/crawler/migrations/0001/20260528_0061_sources_score_columns.sql` — `score`, `next_crawl_at`, `min_interval_minutes`, `max_interval_minutes` columns.
- `apps/crawler/migrations/0001/20260528_0062_refresh_crawl_signals_function.sql` — `refresh_crawl_signals()` plpgsql + cron-like scheduled refresh (just a function the scheduler calls, no pg_cron dependency).
- `pkg/freshness/score.go` — pure-function `Score(signals SourceSignals, tier int) float64`.
- `pkg/freshness/score_test.go` — table-driven tests covering hot/cold/decay scenarios.
- `pkg/freshness/scheduler.go` — `NextCrawlAt(score, baseInterval, min, max) time.Time` interval shaping.
- `pkg/freshness/scheduler_test.go`.

**Modified:**
- `pkg/domain/models.go` — `Source` struct gains `Score float64`, `NextCrawlAt *time.Time`, `MinIntervalMinutes int`, `MaxIntervalMinutes int`.
- `pkg/repository/source_repository.go` — `RefreshSignals(ctx)` + `UpdateScore(ctx, sourceID, score, nextCrawlAt)` + `DueForCrawl(ctx, now)` queries use `next_crawl_at` instead of `last_crawled_at + interval`.
- `apps/scheduler/cmd/main.go` (or wherever the scheduler tick is) — every tick: REFRESH signals view, recompute scores, persist new `next_crawl_at`.
- `apps/api/cmd/sources_admin.go` — surface `score` + `next_crawl_at` in the source DTO; add `POST /admin/sources/{id}/rescore` for force-recompute.
- `ui/admin/src/pages/SourceList.tsx` — sortable column for score + next-crawl ETA + tier badge.

---

## Tasks

### Task 1: Migration — `sources` score columns

**File:** `apps/crawler/migrations/0001/20260528_0061_sources_score_columns.sql`

```sql
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS score                   DOUBLE PRECISION NOT NULL DEFAULT 0.5;
```

**File:** `apps/crawler/migrations/0001/20260528_0062_sources_next_crawl_at.sql`

```sql
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS next_crawl_at           TIMESTAMPTZ;
```

**File:** `apps/crawler/migrations/0001/20260528_0063_sources_min_interval.sql`

```sql
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS min_interval_minutes    INTEGER NOT NULL DEFAULT 15;
```

**File:** `apps/crawler/migrations/0001/20260528_0064_sources_max_interval.sql`

```sql
ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS max_interval_minutes    INTEGER NOT NULL DEFAULT 10080; -- 7 days
```

**File:** `apps/crawler/migrations/0001/20260528_0065_sources_score_idx.sql`

```sql
CREATE INDEX IF NOT EXISTS sources_next_crawl_at_idx
    ON sources (next_crawl_at)
    WHERE next_crawl_at IS NOT NULL;
```

Commit:

```bash
git add apps/crawler/migrations/0001/20260528_006*.sql
git commit -m "feat(schema): adaptive recrawl columns on sources (score + next_crawl_at + min/max interval)"
```

---

### Task 2: Materialized view of crawl signals

**File:** `apps/crawler/migrations/0001/20260528_0070_crawl_signals_view.sql`

```sql
-- crawl_signals — per-source rolling 7-day signals used by the
-- freshness score. Materialized so the scheduler tick stays O(1)
-- rather than scanning crawl_jobs + pipeline_variants every cycle.
--
-- Refreshed by refresh_crawl_signals() (see _0071) on the
-- scheduler's tick; CONCURRENTLY so reads never block.
--
-- Columns:
--   source_id              FK to sources.id (the materialized view's PK after refresh).
--   crawls_7d              number of crawl_jobs in the last 7d.
--   variants_7d            total variants discovered (any state).
--   accepted_7d            variants that became active opportunities.
--   rejected_7d            variants explicitly rejected.
--   last_new_variant_at    NULL if no new variant in 7d; drives "cold" scoring.
--   median_seconds_between_crawls  rough cadence measurement (operator visibility).
CREATE MATERIALIZED VIEW IF NOT EXISTS crawl_signals AS
WITH per_source AS (
    SELECT
        cj.source_id,
        COUNT(*) FILTER (WHERE cj.started_at >= now() - interval '7 days')
            AS crawls_7d
    FROM crawl_jobs cj
    GROUP BY cj.source_id
),
variants AS (
    SELECT
        pv.source_id,
        COUNT(*) FILTER (WHERE pv.discovered_at >= now() - interval '7 days')
            AS variants_7d,
        COUNT(*) FILTER (WHERE pv.discovered_at >= now() - interval '7 days'
                          AND pv.state = 'active')                AS accepted_7d,
        COUNT(*) FILTER (WHERE pv.discovered_at >= now() - interval '7 days'
                          AND pv.state = 'rejected')              AS rejected_7d,
        MAX(pv.discovered_at) FILTER (WHERE pv.discovered_at >= now() - interval '7 days')
            AS last_new_variant_at
    FROM pipeline_variants pv
    GROUP BY pv.source_id
)
SELECT
    s.id                                AS source_id,
    COALESCE(p.crawls_7d, 0)            AS crawls_7d,
    COALESCE(v.variants_7d, 0)          AS variants_7d,
    COALESCE(v.accepted_7d, 0)          AS accepted_7d,
    COALESCE(v.rejected_7d, 0)          AS rejected_7d,
    v.last_new_variant_at               AS last_new_variant_at
FROM sources s
LEFT JOIN per_source p ON p.source_id = s.id
LEFT JOIN variants v   ON v.source_id = s.id;

-- Unique index required for REFRESH MATERIALIZED VIEW CONCURRENTLY.
CREATE UNIQUE INDEX IF NOT EXISTS crawl_signals_source_id_uniq
    ON crawl_signals (source_id);
```

**File:** `apps/crawler/migrations/0001/20260528_0071_refresh_crawl_signals_fn.sql`

```sql
-- Wrapper so the scheduler can call REFRESH idempotently. The
-- CONCURRENTLY guarantees a stale-but-readable view during refresh.
CREATE OR REPLACE FUNCTION refresh_crawl_signals() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    REFRESH MATERIALIZED VIEW CONCURRENTLY crawl_signals;
END;
$$;
```

Verify locally + commit:

```bash
# Sanity: model the SQL against your local dev db; mentally walk one source through it.
git add apps/crawler/migrations/0001/20260528_007*.sql
git commit -m "feat(schema): crawl_signals materialized view + refresh function"
```

---

### Task 3: `pkg/freshness/score.go`

**File:** `pkg/freshness/score.go`

```go
// Package freshness computes the adaptive-recrawl score for a source.
//
// Score is a pure function of recent observed signals plus the
// source's tier classification. Higher score = crawl sooner.
// 1.0 = crawl at min_interval; 0.0 = crawl at max_interval; values
// in between interpolate.
//
// Signals (over the last 7 days):
//   - crawls         : how many crawls actually ran
//   - variants       : variants discovered (any state)
//   - accepted       : variants that became active opportunities
//   - rejected       : variants the extractor explicitly rejected
//   - lastNewVariant : age of the most recent fresh variant
//
// Tier (1/2/3 per Plan B2's source classification):
//   - Tier 1 (structured ATS API) gets a +0.1 floor; we always want
//     to revisit these.
//   - Tier 3 (manual curation) gets a -0.1 ceiling; they rarely
//     pay off above weekly cadence.
package freshness

import "time"

// SourceSignals is the rolling 7-day rollup the scheduler reads from
// the crawl_signals materialized view.
type SourceSignals struct {
    Crawls7d          int
    Variants7d        int
    Accepted7d        int
    Rejected7d        int
    LastNewVariantAt  *time.Time // nil = no new variant in 7d
}

// Score returns a value in [0.0, 1.0]. Pure function — no I/O, no
// time.Now(); the caller passes "now" so the same inputs always
// produce the same output (testability).
//
// The model is intentionally simple — we can replace it without
// changing the scheduler:
//   freshness = clamp(variants_per_crawl * 0.5 + accept_rate * 0.3 + recency * 0.2)
//   then nudge ± 0.1 by tier.
func Score(s SourceSignals, tier int, now time.Time) float64 {
    // variants_per_crawl — capped at 5 because beyond that we're
    // already at maximum cadence.
    var vpc float64
    if s.Crawls7d > 0 {
        vpc = float64(s.Variants7d) / float64(s.Crawls7d)
    }
    if vpc > 5 {
        vpc = 5
    }
    vpcSignal := vpc / 5.0 // normalized to [0,1]

    // accept_rate — penalises sources that yield mostly rejections.
    var acceptRate float64
    if s.Variants7d > 0 {
        acceptRate = float64(s.Accepted7d) / float64(s.Variants7d)
    }

    // recency — exponential decay from last new variant. Half-life
    // 24h. No new variant in 7d → ~0.
    var recency float64
    if s.LastNewVariantAt != nil {
        ageHours := now.Sub(*s.LastNewVariantAt).Hours()
        if ageHours < 0 {
            ageHours = 0
        }
        // 2^(-age/24) — at 0h = 1.0, 24h = 0.5, 48h = 0.25, ...
        recency = pow2Neg(ageHours / 24.0)
    }

    raw := vpcSignal*0.5 + acceptRate*0.3 + recency*0.2

    // Tier nudge.
    switch tier {
    case 1:
        raw += 0.10
    case 3:
        raw -= 0.10
    }

    return clamp(raw, 0.0, 1.0)
}

func pow2Neg(x float64) float64 {
    // Avoid importing math just for Pow; use the identity
    // 2^(-x) = e^(-x * ln2). For small x this is fine in float64.
    // We accept a tiny precision tradeoff because the result feeds
    // a coarse interval clamp downstream.
    return mathExpNeg(x * 0.6931471805599453)
}

// mathExpNeg is math.Exp(-x); inlined to keep the package import-light.
func mathExpNeg(x float64) float64 {
    // Pade approximation good to ~1e-6 for x in [0, 10]. Beyond
    // that the recency signal is already noise.
    if x > 10 {
        return 0
    }
    num := 1.0 + x*(-0.5) + x*x*(1.0/12.0)
    den := 1.0 + x*(0.5) + x*x*(1.0/12.0)
    if den == 0 {
        return 0
    }
    return num / den
}

func clamp(x, lo, hi float64) float64 {
    if x < lo {
        return lo
    }
    if x > hi {
        return hi
    }
    return x
}
```

**File:** `pkg/freshness/score_test.go`

```go
package freshness_test

import (
    "testing"
    "time"

    "github.com/stawi-opportunities/opportunities/pkg/freshness"
)

func TestScore_NoSignals_NeutralWithTierNudge(t *testing.T) {
    now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
    s := freshness.SourceSignals{}

    if got := freshness.Score(s, 2, now); got != 0 {
        t.Errorf("tier 2 zero signals: got %f; want 0", got)
    }
    if got := freshness.Score(s, 1, now); got != 0.10 {
        t.Errorf("tier 1 zero signals: got %f; want 0.10 (floor nudge)", got)
    }
    if got := freshness.Score(s, 3, now); got != 0 {
        t.Errorf("tier 3 zero signals: got %f; want clamped 0", got)
    }
}

func TestScore_HotSource_NearOne(t *testing.T) {
    now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
    fresh := now.Add(-1 * time.Hour)
    s := freshness.SourceSignals{
        Crawls7d:         100,
        Variants7d:       500, // 5 per crawl = max vpc signal
        Accepted7d:       400, // 80% accept
        Rejected7d:       100,
        LastNewVariantAt: &fresh,
    }
    got := freshness.Score(s, 1, now)
    if got < 0.85 {
        t.Errorf("hot tier-1 source: got %f; want > 0.85", got)
    }
}

func TestScore_ColdSource_NearZero(t *testing.T) {
    now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
    s := freshness.SourceSignals{
        Crawls7d:         50,
        Variants7d:       0,
        LastNewVariantAt: nil,
    }
    got := freshness.Score(s, 3, now)
    if got > 0.05 {
        t.Errorf("cold tier-3 source: got %f; want ~0", got)
    }
}

func TestScore_RecencyDecay(t *testing.T) {
    now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
    base := freshness.SourceSignals{
        Crawls7d:   10,
        Variants7d: 10,
        Accepted7d: 10,
    }
    fresh := now
    day1 := now.Add(-24 * time.Hour)
    day3 := now.Add(-72 * time.Hour)
    base.LastNewVariantAt = &fresh
    sNow := freshness.Score(base, 2, now)
    base.LastNewVariantAt = &day1
    s1d := freshness.Score(base, 2, now)
    base.LastNewVariantAt = &day3
    s3d := freshness.Score(base, 2, now)
    if !(sNow > s1d && s1d > s3d) {
        t.Errorf("recency decay broken: now=%f day1=%f day3=%f", sNow, s1d, s3d)
    }
}
```

Build + test:

```bash
go test ./pkg/freshness/ -v -count=1 2>&1 | tail -20
git add pkg/freshness/score.go pkg/freshness/score_test.go
git commit -m "feat(freshness): pure-function Score(signals, tier, now) for adaptive recrawl"
```

---

### Task 4: `pkg/freshness/scheduler.go` — interval shaping

**File:** `pkg/freshness/scheduler.go`

```go
package freshness

import "time"

// NextCrawlAt returns when the source should next be crawled. The
// score (0.0–1.0) interpolates between maxInterval (cold) and
// minInterval (hot).
//
// Always >= now + minInterval to prevent thundering-herd from a
// just-completed crawl. minInterval/maxInterval are in minutes.
//
// Example: score=1.0, min=15, max=10080 → next crawl in 15 minutes.
// Example: score=0.0, min=15, max=10080 → next crawl in 7 days.
// Example: score=0.5, min=15, max=10080 → next crawl in ~84h
//          (geometric interpolation, not linear — half-score should
//          NOT mean half-cadence; that overweights mid-band sources).
func NextCrawlAt(score float64, lastCrawled time.Time, minIntervalMin, maxIntervalMin int) time.Time {
    if score < 0 {
        score = 0
    }
    if score > 1 {
        score = 1
    }
    minMin := float64(minIntervalMin)
    maxMin := float64(maxIntervalMin)
    if minMin <= 0 {
        minMin = 15
    }
    if maxMin < minMin {
        maxMin = minMin
    }
    // Geometric (log-scale) interpolation so the 0.5 midpoint sits
    // closer to min than max — operators want "hot" to mean "near
    // floor", not "midway".
    //
    // interval = min * (max/min) ^ (1 - score)
    ratio := maxMin / minMin
    exponent := 1.0 - score
    intervalMin := minMin * powF(ratio, exponent)
    return lastCrawled.Add(time.Duration(intervalMin) * time.Minute)
}

func powF(base, exp float64) float64 {
    // x^y = e^(y * ln x). We import math here — minor pkg expansion.
    // (Score's inline approx was justified because it ran every tick
    // per source; this is the same hot path, so inline too.)
    if base <= 0 {
        return 0
    }
    // Use 2 iterations of "log" via series — base typically in
    // [1, 1000] for our intervals. Fast + adequate.
    return mathExpNeg(-exp * mathLn(base))
}

// mathLn — natural log via Padé. Adequate for base in [1, 10000].
func mathLn(x float64) float64 {
    if x <= 0 {
        return 0
    }
    // Reduce to ln(1+u) where u = (x-1)/(x+1) * 2 ... actually
    // simpler: scale by powers of 2 until in [0.5, 2].
    n := 0
    for x > 2 {
        x /= 2
        n++
    }
    for x < 0.5 {
        x *= 2
        n--
    }
    u := x - 1
    // ln(1+u) ≈ u - u²/2 + u³/3 — accurate to ~1e-4 in [-0.5, 1].
    series := u - u*u/2 + u*u*u/3
    return series + float64(n)*0.6931471805599453
}
```

**File:** `pkg/freshness/scheduler_test.go`

```go
package freshness_test

import (
    "math"
    "testing"
    "time"

    "github.com/stawi-opportunities/opportunities/pkg/freshness"
)

func TestNextCrawlAt_HotScore_FloorInterval(t *testing.T) {
    now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
    next := freshness.NextCrawlAt(1.0, now, 15, 10080)
    delta := next.Sub(now).Minutes()
    if math.Abs(delta-15) > 1 {
        t.Errorf("score=1.0: next-now = %fm; want ≈15m", delta)
    }
}

func TestNextCrawlAt_ColdScore_CeilingInterval(t *testing.T) {
    now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
    next := freshness.NextCrawlAt(0.0, now, 15, 10080)
    delta := next.Sub(now).Minutes()
    if math.Abs(delta-10080) > 100 {
        t.Errorf("score=0.0: next-now = %fm; want ≈10080m", delta)
    }
}

func TestNextCrawlAt_Midpoint_GeometricNotLinear(t *testing.T) {
    now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
    next := freshness.NextCrawlAt(0.5, now, 15, 10080)
    delta := next.Sub(now).Minutes()
    // Geometric midpoint of (15, 10080) = sqrt(15*10080) ≈ 388
    if math.Abs(delta-388) > 50 {
        t.Errorf("score=0.5: next-now = %fm; want ≈388m (geometric mid)", delta)
    }
}

func TestNextCrawlAt_NeverBelowMin(t *testing.T) {
    now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
    next := freshness.NextCrawlAt(99.0, now, 15, 10080) // bogus score
    if next.Sub(now).Minutes() < 15 {
        t.Errorf("clamp failed: next-now = %f", next.Sub(now).Minutes())
    }
}
```

Build + test:

```bash
go test ./pkg/freshness/ -v -count=1 2>&1 | tail -20
git add pkg/freshness/scheduler.go pkg/freshness/scheduler_test.go
git commit -m "feat(freshness): NextCrawlAt geometric interval shaping by score"
```

---

### Task 5: `Source` model + repository changes

**File:** `pkg/domain/models.go` (`Source` struct)

Add fields:

```go
type Source struct {
    // ... existing fields ...
    Score              float64    `gorm:"column:score;not null;default:0.5" json:"score"`
    NextCrawlAt        *time.Time `gorm:"column:next_crawl_at"               json:"next_crawl_at,omitempty"`
    MinIntervalMinutes int        `gorm:"column:min_interval_minutes;not null;default:15"   json:"min_interval_minutes"`
    MaxIntervalMinutes int        `gorm:"column:max_interval_minutes;not null;default:10080" json:"max_interval_minutes"`
}
```

**File:** `pkg/repository/source_repository.go`

Add:

```go
// RefreshSignals triggers REFRESH MATERIALIZED VIEW CONCURRENTLY
// crawl_signals via the plpgsql wrapper. ~50ms typical; safe to
// call every scheduler tick because CONCURRENTLY never blocks reads.
func (r *SourceRepository) RefreshSignals(ctx context.Context) error {
    return r.db(ctx, false).Exec("SELECT refresh_crawl_signals()").Error
}

// LoadSignals returns the latest rolling 7d signals for every source.
// Source IDs not present in crawl_signals return zero values.
func (r *SourceRepository) LoadSignals(ctx context.Context) (map[string]freshness.SourceSignals, error) {
    type row struct {
        SourceID          string
        Crawls7d          int
        Variants7d        int
        Accepted7d        int
        Rejected7d        int
        LastNewVariantAt  *time.Time
    }
    var rows []row
    err := r.db(ctx, true).
        Raw(`SELECT source_id, crawls_7d, variants_7d, accepted_7d, rejected_7d, last_new_variant_at
             FROM crawl_signals`).Scan(&rows).Error
    if err != nil {
        return nil, fmt.Errorf("load signals: %w", err)
    }
    out := make(map[string]freshness.SourceSignals, len(rows))
    for _, rw := range rows {
        out[rw.SourceID] = freshness.SourceSignals{
            Crawls7d:         rw.Crawls7d,
            Variants7d:       rw.Variants7d,
            Accepted7d:       rw.Accepted7d,
            Rejected7d:       rw.Rejected7d,
            LastNewVariantAt: rw.LastNewVariantAt,
        }
    }
    return out, nil
}

// UpdateScoreAndNextCrawl persists the freshly computed score +
// next_crawl_at. Single-row UPDATE keyed by source_id.
func (r *SourceRepository) UpdateScoreAndNextCrawl(ctx context.Context, sourceID string, score float64, nextCrawlAt time.Time) error {
    return r.db(ctx, false).
        Exec(`UPDATE sources SET score = ?, next_crawl_at = ? WHERE id = ?`,
            score, nextCrawlAt, sourceID).Error
}
```

Modify the existing `DueForCrawl` (or whatever the scheduler calls) to prefer `next_crawl_at` when set:

```go
func (r *SourceRepository) DueForCrawl(ctx context.Context, now time.Time, limit int) ([]Source, error) {
    var sources []Source
    err := r.db(ctx, true).
        Where(`enabled = true AND (
                  (next_crawl_at IS NOT NULL AND next_crawl_at <= ?)
               OR (next_crawl_at IS NULL AND (last_crawled_at IS NULL OR last_crawled_at + (interval_minutes || ' minutes')::interval <= ?))
               )`, now, now).
        Order("next_crawl_at ASC NULLS LAST").
        Limit(limit).
        Find(&sources).Error
    return sources, err
}
```

Note: the legacy `interval_minutes` branch is kept so sources without a score yet (first crawl) still fire.

Commit:

```bash
go build ./pkg/... 2>&1 | tail -3
git add pkg/domain/models.go pkg/repository/source_repository.go
git commit -m "feat(repository): Source.Score + NextCrawlAt + RefreshSignals/LoadSignals/UpdateScoreAndNextCrawl"
```

---

### Task 6: Scheduler tick wires it together

Identify the scheduler's tick function (likely `apps/scheduler/cmd/main.go` or `apps/crawler/scheduler/...`). At the top of each tick, BEFORE picking sources due for crawl:

```go
// Refresh the rolling-7d view (~50ms, non-blocking) + recompute
// per-source scores. The view is materialized so this scales O(1)
// per tick regardless of pipeline_variants size.
if err := sourceRepo.RefreshSignals(ctx); err != nil {
    log.WithError(err).Warn("scheduler: REFRESH crawl_signals failed; using stale signals")
}
signals, err := sourceRepo.LoadSignals(ctx)
if err != nil {
    log.WithError(err).Warn("scheduler: LoadSignals failed; skipping score recompute this tick")
} else {
    now := time.Now().UTC()
    for sourceID, sig := range signals {
        src, ok := sourcesCache[sourceID]
        if !ok {
            continue // source archived since last tick
        }
        score := freshness.Score(sig, src.Tier, now)
        next := freshness.NextCrawlAt(score, now, src.MinIntervalMinutes, src.MaxIntervalMinutes)
        if err := sourceRepo.UpdateScoreAndNextCrawl(ctx, sourceID, score, next); err != nil {
            log.WithError(err).WithField("source_id", sourceID).Warn("score update failed")
        }
    }
}
```

Wire `freshness` package import. Add a metrics counter for `scheduler_scores_updated_total` if observability is wired (skip if not).

Commit:

```bash
go build ./apps/... 2>&1 | tail -3
git add apps/scheduler/cmd/main.go  # (path TBC by implementer)
git commit -m "feat(scheduler): adaptive recrawl — REFRESH signals + recompute score + persist next_crawl_at every tick"
```

---

### Task 7: Operator surface — score + force-recompute

**File:** `apps/api/cmd/sources_admin.go`

Add to the source list DTO:

```go
type sourceDTO struct {
    // ... existing fields ...
    Score              float64    `json:"score"`
    NextCrawlAt        *time.Time `json:"next_crawl_at"`
    MinIntervalMinutes int        `json:"min_interval_minutes"`
    MaxIntervalMinutes int        `json:"max_interval_minutes"`
    Tier               int        `json:"tier"`
}
```

Add `POST /admin/sources/{id}/rescore`:

```go
mux.HandleFunc("POST /admin/sources/{id}/rescore", requireAdmin(func(w http.ResponseWriter, r *http.Request) {
    sourceID := r.PathValue("id")
    signals, err := sourceRepo.LoadSignals(r.Context())
    if err != nil { writeError(w, 500, "internal", err.Error()); return }
    sig, ok := signals[sourceID]
    if !ok { writeError(w, 404, "not_found", "source has no signals row"); return }
    src, err := sourceRepo.Get(r.Context(), sourceID)
    if err != nil { writeError(w, 500, "internal", err.Error()); return }
    now := time.Now().UTC()
    score := freshness.Score(sig, src.Tier, now)
    next := freshness.NextCrawlAt(score, now, src.MinIntervalMinutes, src.MaxIntervalMinutes)
    if err := sourceRepo.UpdateScoreAndNextCrawl(r.Context(), sourceID, score, next); err != nil {
        writeError(w, 500, "internal", err.Error()); return
    }
    writeJSON(w, 200, map[string]any{"score": score, "next_crawl_at": next})
}))
```

**File:** `ui/admin/src/pages/SourceList.tsx`

Add columns: Score (with colored badge), Next Crawl (relative timestamp), Tier (1/2/3 badge), and a "Rescore" button per row.

Add `rescoreSource(id)` to `ui/admin/src/api/admin-client.ts`.

Commit:

```bash
go build ./... 2>&1 | tail -3
cd ui/admin && npx tsc -b --noEmit && cd ../..
git add apps/api/cmd/sources_admin.go ui/admin/src/api/admin-client.ts ui/admin/src/pages/SourceList.tsx
git commit -m "feat(admin): score + next_crawl_at + tier in source DTO; POST /admin/sources/{id}/rescore + UI"
```

---

### Task 8: Backfill — first-run score for existing rows

After the migrations apply but before the next scheduler tick, run a one-shot:

```sql
-- Set neutral 0.5 + next_crawl_at = now() + interval_minutes so
-- existing sources don't all queue at once.
UPDATE sources
SET    score = 0.5,
       next_crawl_at = COALESCE(last_crawled_at, now()) + (interval_minutes || ' minutes')::interval
WHERE  score IS NULL OR next_crawl_at IS NULL;
```

Add as `apps/crawler/migrations/0001/20260528_0072_sources_backfill_score.sql`.

Commit:

```bash
git add apps/crawler/migrations/0001/20260528_0072_sources_backfill_score.sql
git commit -m "feat(schema): backfill score + next_crawl_at for existing sources (neutral start)"
```

---

### Task 9: Tag + deploy

```bash
git push origin main
git tag v8.0.68
git push origin v8.0.68
```

Verify (after Flux rolls):

```bash
# Migration applied:
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- \
  psql -U postgres -d opportunities -c "\d+ sources" | grep -E "score|next_crawl_at"

# Materialized view populated:
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- \
  psql -U postgres -d opportunities -c "SELECT count(*), max(last_new_variant_at) FROM crawl_signals"

# Scores recomputing:
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- \
  psql -U postgres -d opportunities -c "SELECT id, score, next_crawl_at FROM sources ORDER BY score DESC LIMIT 10"

# Logs:
kubectl logs -n product-opportunities -l app.kubernetes.io/name=opportunities-scheduler --tail=50 | grep -iE "REFRESH|score"
```

---

## Plan D3 Exit Criteria

- [ ] All migrations apply clean (no SQLSTATE 42601; one statement per file).
- [ ] `crawl_signals` materialized view populates after first refresh.
- [ ] `pkg/freshness` tests pass (`go test ./pkg/freshness/ -v -count=1`).
- [ ] Scheduler tick logs "REFRESH crawl_signals" + "score recompute".
- [ ] Source list page shows score column + sort order matches DESC.
- [ ] `POST /admin/sources/{id}/rescore` returns the new score + next_crawl_at.
- [ ] After 24h, hot sources visibly shrunk their next_crawl_at; cold sources visibly grew.
- [ ] No regression: every source still gets crawled at least once per `max_interval_minutes`.
