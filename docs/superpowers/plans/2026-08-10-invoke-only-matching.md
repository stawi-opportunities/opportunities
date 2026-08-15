# Invoke-Only Matching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run match generation only via explicit `MatchInvoke` (user refresh + digests), raise the quality floor to 70%, remove in-app match-count caps, send at most 3 unseen matches per outbound digest, and add `twice_daily` cadence.

**Architecture:** Wrap existing reverse-KNN `GapFill` in `MatchInvoke` with caps forced off and optional invoke rate limits. Disable Path A fan-out by default and make Path C index-only. Digests call `MatchInvoke`, then pick top-3 unseen via notification receipts. UI drops weekly-budget language and adds twice-daily digest option.

**Tech Stack:** Go (apps/matching, pkg/matching, pkg/billing), PostgreSQL migrations, Trustage JSON cron defs, React/TS (ui/app), existing `pkg/notify`.

**Spec:** `docs/superpowers/specs/2026-08-10-invoke-only-matching-design.md`

---

## File map

| File | Responsibility |
|------|----------------|
| `pkg/matching/invoke.go` | `MatchInvoke`, reasons, rate-limit gate, maps to GapFill with zero row-caps |
| `pkg/matching/invoke_test.go` | Unit tests for invoke reasons, rate limit, no caps |
| `pkg/matching/invoke_limit.go` | `InvokeLimiter` + PG count of user invokes today (UTC) |
| `pkg/matching/gapfill.go` | Accept `TriggeredBy`; keep cap logic but callers pass 0 |
| `pkg/matching/digest_schedule.go` | `twice_daily` normalize + `ShouldSendDigest` windows |
| `pkg/matching/digest_schedule_test.go` | Cadence matrix including twice_daily |
| `pkg/matching/receipts.go` | Notification receipts store + list unseen top matches |
| `pkg/matching/receipts_test.go` | Receipt write/read/filter tests |
| `pkg/matching/store.go` | `ListTopUnseenMatchesForDigest`; keep legacy list |
| `pkg/billing/catalog.go` | `InvokeDailyLimit` on entitlements; stop match-row caps as product truth |
| `pkg/billing/billing_test.go` | Entitlement assertions |
| `apps/matching/config/config.go` | Defaults: min score 0.70, fan-out false, invoke limits, twice_daily env |
| `apps/matching/cmd/main.go` | Wire MatchInvoke path; Path C index-only; digest deps |
| `apps/matching/service/http/me/v1/handlers.go` | Refresh → rate limit + MatchInvoke |
| `apps/matching/service/matching/v1/candidate_change_consumer.go` | Index upsert only; skip `RunCandidateChange` |
| `apps/matching/service/admin/v1/matches_weekly_digest.go` | MatchInvoke + top 3 unseen + receipts |
| `apps/matching/migrations/0001/20260810_0030_match_notification_receipts.sql` | Receipts table |
| `definitions/trustage/candidates-matches-weekly-digest.json` | Cron note for twice-daily |
| `ui/app/src/utils/plans.ts` | Copy without weekly match caps |
| `ui/app/src/components/dashboard/MatchesPanel.tsx` | rate_limited / drop cap strip messaging |
| `ui/app/src/components/settings/SettingsNotifications.tsx` | twice_daily option |
| `ui/app/src/i18n/strings.ts` | New strings |
| `docs/ops/matching-pipeline.md` | Ops truth |
| `docs/ops/end-user-value-proof.md` | Product claims |

---

### Task 1: Raise default min score + config defaults

**Files:**
- Modify: `apps/matching/config/config.go`
- Modify: `apps/matching/service/http/me/v1/min_score_test.go`
- Modify: `apps/matching/service/admin/v1/min_score_test.go`
- Modify: `apps/matching/service/matching/v1/candidate_change_consumer.go` (fallback 0.45 → 0.70)

- [ ] **Step 1: Update config defaults**

In `apps/matching/config/config.go` change:

```go
MatchingFanOutEnabled bool `env:"MATCHING_FANOUT_ENABLED" envDefault:"false"`
// ...
MatchingMinScore float64 `env:"MATCHING_MIN_SCORE" envDefault:"0.70"`

// Add invoke limits:
MatchingInvokeLimitFree    int `env:"MATCHING_INVOKE_LIMIT_FREE" envDefault:"1"`
MatchingInvokeLimitStarter int `env:"MATCHING_INVOKE_LIMIT_STARTER" envDefault:"30"`
MatchingInvokeLimitManaged int `env:"MATCHING_INVOKE_LIMIT_MANAGED" envDefault:"100"`

// Digest twice-daily local hour windows (inclusive start, exclusive end), UTC if DIGEST_TIMEZONE=UTC:
DigestTwiceDailyMorningStart int `env:"DIGEST_TWICE_DAILY_MORNING_START" envDefault:"8"`
DigestTwiceDailyMorningEnd   int `env:"DIGEST_TWICE_DAILY_MORNING_END" envDefault:"10"`
DigestTwiceDailyEveningStart int `env:"DIGEST_TWICE_DAILY_EVENING_START" envDefault:"17"`
DigestTwiceDailyEveningEnd   int `env:"DIGEST_TWICE_DAILY_EVENING_END" envDefault:"19"`
```

Also update comment on `MatchingCandidateChangeEnabled` to say index-only gap-fill is product default (consumer still runs for index).

- [ ] **Step 2: Fix min_score unit tests** that hardcode 0.45 floor when both unset → expect **0.70**.

Example in `me/v1/min_score_test.go` and `admin/v1/min_score_test.go`:

```go
{"both unset → 0.70 floor", 0, 0, 0.70},
```

Ensure `effectiveMinScore` / `digestMinScore` still prefer a positive index score over default.

- [ ] **Step 3: Candidate change fallback**

In `candidate_change_consumer.go`:

```go
if defaultMin <= 0 || defaultMin > 1 {
    defaultMin = 0.70
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./apps/matching/service/http/me/v1/ ./apps/matching/service/admin/v1/ -count=1 -short
```

Expected: PASS (or only unrelated failures — fix min_score tests first).

- [ ] **Step 5: Commit**

```bash
git add apps/matching/config/config.go \
  apps/matching/service/http/me/v1/min_score_test.go \
  apps/matching/service/admin/v1/min_score_test.go \
  apps/matching/service/matching/v1/candidate_change_consumer.go
git commit -m "feat(matching): default min score 0.70 and fan-out off"
```

---

### Task 2: Entitlements → invoke limits (billing)

**Files:**
- Modify: `pkg/billing/catalog.go`
- Modify: `pkg/billing/billing_test.go`
- Grep consumers of `DailyCap`/`WeeklyCap` for follow-up (index still stores numbers; MatchInvoke ignores row caps)

- [ ] **Step 1: Write failing entitlement tests**

In `pkg/billing/billing_test.go` replace `TestEntitlementsFor` expectations:

```go
func TestEntitlementsFor(t *testing.T) {
	starter := billing.EntitlementsFor(billing.PlanStarter)
	require.Equal(t, 30, starter.InvokeDailyLimit)
	require.Equal(t, 0, starter.DailyCap)  // no match-row cap
	require.Equal(t, 0, starter.WeeklyCap)
	require.False(t, starter.AutoApply)

	pro := billing.EntitlementsFor(billing.PlanPro)
	require.Equal(t, 100, pro.InvokeDailyLimit)
	require.Equal(t, 0, pro.WeeklyCap)

	managed := billing.EntitlementsFor(billing.PlanManaged)
	require.Equal(t, 100, managed.InvokeDailyLimit)
	require.Equal(t, 0, managed.WeeklyCap)

	unknown := billing.EntitlementsFor(billing.PlanID("free"))
	require.Equal(t, 1, unknown.InvokeDailyLimit)
	require.Equal(t, 0, unknown.DailyCap)
	require.Equal(t, 0, unknown.WeeklyCap)
}
```

- [ ] **Step 2: Run test — expect FAIL** (missing field / wrong values)

```bash
go test ./pkg/billing/ -run TestEntitlementsFor -count=1
```

- [ ] **Step 3: Implement entitlements**

```go
// Entitlements are server-enforced limits.
type Entitlements struct {
	// DailyCap / WeeklyCap are legacy match-row caps. Product path sets both to 0
	// (unlimited rows above min score). Kept for DB column compatibility.
	DailyCap  int
	WeeklyCap int
	// InvokeDailyLimit caps user-initiated MatchInvoke calls per UTC day.
	// 0 means treat as free-proof 1 in callers if unset — prefer explicit values.
	InvokeDailyLimit int
	AutoApply        bool
	Priority         string
}

func EntitlementsFor(plan PlanID) Entitlements {
	switch plan {
	case PlanManaged, PlanPro:
		return Entitlements{DailyCap: 0, WeeklyCap: 0, InvokeDailyLimit: 100, AutoApply: false, Priority: "agent"}
	case PlanStarter:
		return Entitlements{DailyCap: 0, WeeklyCap: 0, InvokeDailyLimit: 30, AutoApply: false, Priority: "standard"}
	default:
		return Entitlements{DailyCap: 0, WeeklyCap: 0, InvokeDailyLimit: 1, AutoApply: false, Priority: "proof"}
	}
}
```

Update catalog plan `Description` strings to quality/digest cadence language (no “5 matches/week”).

- [ ] **Step 4: Run billing tests**

```bash
go test ./pkg/billing/ -count=1
```

Expected: PASS. Fix any other tests that asserted old WeeklyCap values.

- [ ] **Step 5: Commit**

```bash
git add pkg/billing/
git commit -m "feat(billing): invoke daily limits instead of match-row caps"
```

---

### Task 3: GapFill TriggeredBy + MatchInvoke + invoke limiter

**Files:**
- Modify: `pkg/matching/gapfill.go`
- Create: `pkg/matching/invoke.go`
- Create: `pkg/matching/invoke_limit.go`
- Create: `pkg/matching/invoke_test.go`
- Modify: `pkg/matching/gapfill_test.go` if TriggeredBy assertions needed

- [ ] **Step 1: Add TriggeredBy to GapFillInput**

In `gapfill.go`:

```go
type GapFillInput struct {
	// ...existing fields...
	// TriggeredBy is stored on match_run_events (default "extension_poll").
	TriggeredBy string
}
```

In `GapFill`, when building `runEvt`:

```go
triggeredBy := strings.TrimSpace(in.TriggeredBy)
if triggeredBy == "" {
	triggeredBy = "extension_poll"
}
runEvt := MatchRunEvent{
	// ...
	TriggeredBy: triggeredBy,
	// ...
}
```

Add `"strings"` import if missing.

- [ ] **Step 2: Write failing MatchInvoke tests** (`pkg/matching/invoke_test.go`)

```go
package matching_test

import (
	"context"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// Reuse fakes from gapfill_test / fanout_test patterns in this package.

func TestMatchInvoke_ForcesZeroRowCaps(t *testing.T) {
	t.Parallel()
	// Setup KNN returning 5 hits all scoring high; store captures upserts.
	// Call MatchInvoke with reason user_refresh, InvokeLimit large.
	// Assert all written StatusNew (none overflow), MatchesWritten == 5.
}

func TestMatchInvoke_RateLimited(t *testing.T) {
	t.Parallel()
	// Limiter returns used=1, limit=1 for user_refresh.
	// Expect Reason == matching.GapReasonRateLimited (define const), MatchesWritten 0, KNN not called.
}

func TestMatchInvoke_DigestSkipsRateLimit(t *testing.T) {
	t.Parallel()
	// reason=digest, limiter would block; still runs GapFill.
}
```

Implement tests against real interfaces using package-level fakes (copy minimal fakes from `gapfill_test.go`).

- [ ] **Step 3: Add reason constant**

In `gapfill.go` (or `invoke.go`):

```go
const GapReasonRateLimited = "rate_limited"
```

- [ ] **Step 4: Implement invoke limiter**

`pkg/matching/invoke_limit.go`:

```go
package matching

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// InvokeCounter counts user-facing MatchInvoke runs for a candidate on a UTC day.
type InvokeCounter interface {
	CountUserInvokesToday(ctx context.Context, candidateID string, now time.Time) (int, error)
	// RecordUserInvoke is optional if run events already record triggered_by;
	// prefer counting match_run_events so GapFill write is the record.
}

// PGInvokeCounter counts match_run_events for user_refresh|onboard_seed since UTC midnight.
type PGInvokeCounter struct {
	DB *sql.DB
}

func (c *PGInvokeCounter) CountUserInvokesToday(ctx context.Context, candidateID string, now time.Time) (int, error) {
	dayStart := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	const q = `
SELECT count(*) FROM match_run_events
 WHERE candidate_id = $1
   AND triggered_by IN ('user_refresh', 'onboard_seed')
   AND started_at >= $2`
	var n int
	if err := c.DB.QueryRowContext(ctx, q, candidateID, dayStart).Scan(&n); err != nil {
		return 0, fmt.Errorf("matching: count invokes: %w", err)
	}
	return n, nil
}
```

Note: `match_run_events` must have `candidate_id` populated in GapFill (already does). Confirm column exists via models.

- [ ] **Step 5: Implement MatchInvoke**

`pkg/matching/invoke.go`:

```go
package matching

import (
	"context"
	"fmt"
	"time"
)

const (
	InvokeUserRefresh = "user_refresh"
	InvokeDigest      = "digest"
	InvokeOnboardSeed = "onboard_seed"
)

// InvokeInput is one explicit match generation request.
type InvokeInput struct {
	CandidateID    string
	Embedding      []float32
	Skills         []string
	Countries      []string
	Kinds          []string
	SalaryFloorUSD *int
	Since          time.Time
	MinScore       float64
	QueryText      string
	Reason         string // user_refresh | digest | onboard_seed
	// InvokeLimit when > 0 and Reason is user-facing, enforces daily invoke budget.
	InvokeLimit int
}

// InvokeDeps extends GapFill with optional invoke counting.
type InvokeDeps struct {
	GapFill GapFillDeps
	// Invokes optional; when nil, rate limit is skipped.
	Invokes InvokeCounter
	Now     func() time.Time
}

func reasonConsumesBudget(reason string) bool {
	switch reason {
	case InvokeUserRefresh, InvokeOnboardSeed:
		return true
	default:
		return false
	}
}

// MatchInvoke runs reverse-KNN matching without match-row caps.
func MatchInvoke(ctx context.Context, in InvokeInput, deps InvokeDeps) (GapFillResult, error) {
	now := time.Now
	if deps.Now != nil {
		now = deps.Now
	}
	if in.MinScore <= 0 {
		in.MinScore = 0.70
	}
	if in.Since.IsZero() {
		in.Since = now().UTC().Add(-30 * 24 * time.Hour)
	}
	reason := in.Reason
	if reason == "" {
		reason = InvokeUserRefresh
	}

	if reasonConsumesBudget(reason) && in.InvokeLimit > 0 && deps.Invokes != nil {
		used, err := deps.Invokes.CountUserInvokesToday(ctx, in.CandidateID, now())
		if err != nil {
			return GapFillResult{}, fmt.Errorf("matching: invoke limit: %w", err)
		}
		if used >= in.InvokeLimit {
			return GapFillResult{
				Reason:     GapReasonRateLimited,
				WeeklyCap:  0,
				WeeklyUsed: 0,
			}, nil
		}
	}

	// Force unlimited match rows — quality floor only.
	return GapFill(ctx, GapFillInput{
		CandidateID:    in.CandidateID,
		Embedding:      in.Embedding,
		Skills:         in.Skills,
		Countries:      in.Countries,
		Kinds:          in.Kinds,
		SalaryFloorUSD: in.SalaryFloorUSD,
		Since:          in.Since,
		MinScore:       in.MinScore,
		DailyCap:       0,
		WeeklyCap:      0,
		QueryText:      in.QueryText,
		TriggeredBy:    reason,
	}, deps.GapFill)
}
```

- [ ] **Step 6: Run matching package tests**

```bash
go test ./pkg/matching/ -count=1 -short
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/matching/gapfill.go pkg/matching/invoke.go pkg/matching/invoke_limit.go pkg/matching/invoke_test.go
git commit -m "feat(matching): MatchInvoke with rate limit and no row caps"
```

---

### Task 4: Wire refresh handler + Path C index-only + main defaults

**Files:**
- Modify: `apps/matching/service/http/me/v1/handlers.go`
- Modify: `apps/matching/service/http/me/v1/handlers_test.go`
- Modify: `apps/matching/service/matching/v1/candidate_change_consumer.go`
- Modify: `apps/matching/cmd/main.go`
- Modify: `apps/matching/service/http/me/v1` Deps struct if needed

- [ ] **Step 1: Extend me/v1 Deps**

Add to `Deps` in handlers.go (or deps file):

```go
InvokeCounter matching.InvokeCounter // optional
// Keep DefaultMinScore; add plan-based limit via billing at call site
```

- [ ] **Step 2: Rewrite refreshMatches to use MatchInvoke**

Replace `matching.GapFill(...)` block with:

```go
ent := billing.EntitlementsForProfile(sub, planID)
// unpaid always free-proof even if plan_id set
if !paid {
	ent = billing.EntitlementsFor("")
}
since := time.Now().UTC().Add(-30 * 24 * time.Hour)
if !paid {
	since = time.Now().UTC().Add(-90 * 24 * time.Hour)
}
minScore := effectiveMinScore(idx.MinScore, d.DefaultMinScore)
// Prefer global floor ≥ 0.70 if index still has old 0.45
if minScore < 0.70 && d.DefaultMinScore >= 0.70 {
	minScore = d.DefaultMinScore
}

res, runErr := matching.MatchInvoke(ctx, matching.InvokeInput{
	CandidateID:    gapKey,
	Embedding:      idx.Embedding,
	Countries:      idx.Countries,
	Kinds:          idx.Kinds,
	SalaryFloorUSD: idx.SalaryFloorUSD,
	Since:          since,
	MinScore:       minScore,
	Reason:         matching.InvokeUserRefresh,
	InvokeLimit:    ent.InvokeDailyLimit,
}, matching.InvokeDeps{
	GapFill: matching.GapFillDeps{
		KNN:      d.KNN,
		Store:    d.Matches,
		EventLog: d.MatchEvents,
		Reranker: d.Reranker,
		Weights:  d.Weights,
		// DailyCap/WeekCount intentionally nil — MatchInvoke zeros caps
	},
	Invokes: d.InvokeCounter,
})
if runErr != nil {
	ProblemFromError(w, runErr)
	return
}
// If rate limited, return 200 with reason for UI (or 429 — prefer 200 + reason for toast)
w.Header().Set("Content-Type", "application/json")
_ = json.NewEncoder(w).Encode(map[string]any{
	"ok":               true,
	"matches_written":  res.MatchesWritten,
	"opps_scanned":     res.OppsScanned,
	"scored_above_min": res.ScoredAboveMin,
	"run_id":           res.RunID,
	"min_score":        minScore,
	"reason":           res.Reason,
	"invoke_limit":     ent.InvokeDailyLimit,
	"proof":            !paid,
})
```

Remove `weekly_used` / `weekly_cap` / `daily_cap` from response (or leave as 0 for compat).

- [ ] **Step 3: Update refresh handler tests**

In `handlers_test.go`:
- Free user second refresh same day → `reason=rate_limited` (use fake InvokeCounter).
- Paid refresh writes many matches without overflow.
- Remove expectations on weekly_cap truncation.

- [ ] **Step 4: Path C index-only**

In `candidate_change_consumer.go` `Handle`, after successful index upsert (and the early returns for no vector), **return nil before** `matching.RunCandidateChange(...)`.

```go
// Product: matching only via MatchInvoke (user refresh / digest).
// Keep index fresh; do not GapFill here.
util.Log(ctx).WithField("candidate_id", candidateID).
	Info("candidate_change: index updated; skip auto gap-fill")
return nil
```

Leave `RunCandidateChange` in package for tests/emergency.

- [ ] **Step 5: Wire main.go**

```go
// PG invoke counter for refresh
invokeCounter := matching.PGInvokeCounter{DB: sqlDB}

// me deps
InvokeCounter: &invokeCounter,

// Log fan-out default off
// MatchingFanOutEnabled already false from config
```

- [ ] **Step 6: Run tests**

```bash
go test ./pkg/matching/ ./pkg/billing/ ./apps/matching/service/http/me/v1/ ./apps/matching/service/matching/v1/ -count=1 -short
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add apps/matching/service/http/me/v1/ \
  apps/matching/service/matching/v1/candidate_change_consumer.go \
  apps/matching/cmd/main.go
git commit -m "feat(matching): wire MatchInvoke on refresh; Path C index-only"
```

---

### Task 5: Digest schedule `twice_daily`

**Files:**
- Modify: `pkg/matching/digest_schedule.go`
- Modify: `pkg/matching/digest_schedule_test.go`

- [ ] **Step 1: Write failing tests**

```go
{"twice_daily morning", onTwice, "auto", time.Date(2026,7,14,8,30,0,0,time.UTC), true},
{"twice_daily midday skip", onTwice, "auto", time.Date(2026,7,14,12,0,0,0,time.UTC), false},
{"twice_daily evening", onTwice, "auto", time.Date(2026,7,14,17,30,0,0,time.UTC), true},
{"normalize twice_daily", ...}
```

- [ ] **Step 2: Implement**

```go
const DigestTwiceDaily = "twice_daily"

func NormalizeDigestCadence(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case DigestDaily:
		return DigestDaily
	case DigestTwiceDaily, "twice-daily", "bidaily":
		return DigestTwiceDaily
	case DigestOff, "none", "disabled":
		return DigestOff
	case DigestWeekly, "":
		return DigestWeekly
	default:
		return DigestWeekly
	}
}
```

Extend `ShouldSendDigest` auto mode:

```go
if freq == DigestDaily {
	return true
}
if freq == DigestTwiceDaily {
	h := now.In(loc).Hour()
	// Defaults 8–10 and 17–19; windows can be parameterized later via deps.
	// For package purity, add optional params or package-level defaults:
	// morning [8,10), evening [17,19)
	if (h >= 8 && h < 10) || (h >= 17 && h < 19) {
		return true
	}
	return false
}
```

Trustage should fire at least hourly or every 30m during those windows (update JSON in Task 7). If cron is only daily at 09:00, twice_daily evening never fires — document required cron change.

Optional cleaner API:

```go
func ShouldSendDigestWithWindows(prefs DigestPrefs, cadence string, now time.Time, loc *time.Location, weeklyWeekday time.Weekday, morningStart, morningEnd, eveningStart, eveningEnd int) bool
```

Keep `ShouldSendDigest` calling defaults 8,10,17,19 for backward compat.

- [ ] **Step 3: Run tests**

```bash
go test ./pkg/matching/ -run 'Digest|Normalize' -count=1
```

- [ ] **Step 4: Commit**

```bash
git add pkg/matching/digest_schedule.go pkg/matching/digest_schedule_test.go
git commit -m "feat(matching): twice_daily digest cadence"
```

---

### Task 6: Notification receipts + top-3 unseen digests

**Files:**
- Create: `apps/matching/migrations/0001/20260810_0030_match_notification_receipts.sql`
- Create: `pkg/matching/receipts.go`
- Create: `pkg/matching/receipts_test.go` (unit with sqlmock or integration if suite exists)
- Modify: `pkg/matching/models.go` (GORM model if AutoMigrate used)
- Modify: `pkg/matching/store.go` — `ListTopUnseenMatchesForDigest`
- Modify: `apps/matching/service/admin/v1/matches_weekly_digest.go`
- Modify: `apps/matching/service/admin/v1/matches_weekly_digest_test.go`
- Modify: `apps/matching/cmd/main.go` (wire receipts)

- [ ] **Step 1: Migration SQL**

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS match_notification_receipts (
  candidate_id text NOT NULL,
  match_id text NOT NULL,
  opportunity_id text NOT NULL,
  channel text NOT NULL DEFAULT 'email',
  sent_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (candidate_id, match_id, channel)
);
CREATE INDEX IF NOT EXISTS match_notification_receipts_cand_sent_idx
  ON match_notification_receipts (candidate_id, sent_at DESC);

-- +goose Down
DROP TABLE IF EXISTS match_notification_receipts;
```

Follow the migration style of sibling files in `apps/matching/migrations/0001/` (goose vs plain — match existing header style exactly).

- [ ] **Step 2: Store methods**

```go
// ListTopUnseenMatchesForDigest returns up to limit highest-scoring matches
// with no receipt for channel (default email), status not overflow/dismissed.
func (s *Store) ListTopUnseenMatchesForDigest(ctx context.Context, candidateID, channel string, limit int) ([]DigestMatch, error)

func (s *Store) InsertNotificationReceipts(ctx context.Context, candidateID, channel string, matchIDs []string, opportunityIDs []string) error
```

SQL for list (channel default `email`):

```sql
SELECT m.opportunity_id, COALESCE(o.apply_url,''), m.score,
       COALESCE(o.title,''), COALESCE(o.issuing_entity,''), COALESCE(o.slug,''),
       m.match_id
FROM candidate_matches m
JOIN opportunities o ON o.canonical_id = m.opportunity_id
WHERE m.candidate_id = $1
  AND m.status NOT IN ('overflow', 'dismissed')
  AND m.score >= $2  -- optional if always filtered earlier; else omit and filter in Go
  AND NOT EXISTS (
    SELECT 1 FROM match_notification_receipts r
     WHERE r.candidate_id = m.candidate_id
       AND r.match_id = m.match_id
       AND r.channel = $3
  )
ORDER BY m.score DESC, m.created_at DESC
LIMIT $4
```

Extend `DigestMatch` with `MatchID string` for receipt writes.

- [ ] **Step 3: Digest handler changes**

For each audience member after prefs check:

1. `MatchInvoke` with `Reason: matching.InvokeDigest`, `InvokeLimit: 0`, `MinScore: digestMinScore(...)` with floor ≥ 0.70, **DailyCap/WeeklyCap not used**.
2. `top, err := Store.ListTopUnseenMatchesForDigest(ctx, m.ID, "email", 3)`
3. If `len(top)==0` → skip send (`Skipped++` or new counter).
4. Build notify payload from `top` only (≤3).
5. On successful `notify.Send`, `InsertNotificationReceipts`.
6. Do **not** use `ListTopMatchesForDigest(..., 10)` for send content.

- [ ] **Step 4: Tests**

- Second digest run does not re-include receipted opportunity IDs.
- Payload length ≤ 3.
- `email_digest=off` still skips.
- `twice_daily` outside window skips.

- [ ] **Step 5: Run tests + commit**

```bash
go test ./pkg/matching/ ./apps/matching/service/admin/v1/ -count=1 -short
git add apps/matching/migrations/ pkg/matching/ apps/matching/service/admin/v1/ apps/matching/cmd/main.go
git commit -m "feat(matching): digest top-3 unseen with notification receipts"
```

---

### Task 7: Trustage cron for twice-daily windows

**Files:**
- Modify: `definitions/trustage/candidates-matches-weekly-digest.json`
- Modify: `definitions/trustage/README.md`

- [ ] **Step 1: Update cron**

Change description to note server filters by cadence. Set `cron_expr` to fire every hour (or twice daily at 08:30 and 17:30 UTC if timezone is always UTC):

```json
"cron_expr": "30 8,17 * * *"
```

Or hourly:

```json
"cron_expr": "0 * * * *"
```

Hourly is safer with `DIGEST_TIMEZONE` not UTC for all users — server windows do the rest.

- [ ] **Step 2: README table** update for twice_daily + invoke-only model.

- [ ] **Step 3: Commit**

```bash
git add definitions/trustage/
git commit -m "ops(trustage): fire match digest often enough for twice_daily"
```

---

### Task 8: UI — plans, Matches, Settings

**Files:**
- Modify: `ui/app/src/utils/plans.ts`
- Modify: `ui/app/src/components/dashboard/MatchesPanel.tsx`
- Modify: `ui/app/src/components/settings/SettingsNotifications.tsx`
- Modify: `ui/app/src/i18n/strings.ts`
- Modify: `ui/app/src/api/candidates.ts` if `MatchRefreshResult` types include old cap fields
- Grep `matchesPerWeek`, `weekly_cap`, `daily_cap` under `ui/app/src`

- [ ] **Step 1: plans.ts**

```ts
// Remove matchesPerWeek scarcity or set both to null with comment "uncapped above quality floor"
features for Starter:
  'AI matches scored at 70%+ fit',
  'Unlimited matches in your dashboard feed',
  'Email digests (daily, twice daily, or weekly) with up to 3 top new fits',
  'Find matches anytime (fair-use)',
Managed:
  'Same quality floor (70%+) and full uncapped feed',
  'Higher Find-matches allowance',
  'Priority digests / alerts posture',
```

Update any comparison table that prints matches/week.

- [ ] **Step 2: MatchesPanel empty reasons**

```ts
case 'rate_limited':
  return res.proof
    ? 'You have used today’s free match search. Subscribe for more daily searches, or try again tomorrow.'
    : 'Match search limit reached for today. Try again tomorrow.';
case 'below_threshold':
  return 'We found roles but none reached a 70% match. Improve your CV or widen preferences, then try again.';
// Remove weekly_cap / daily_cap cases or map them to rate_limited for old servers
```

Remove UI budget strip if it shows weekly used/cap from subscription summary — search for `delivered`, `weekly`, `budget` in MatchesPanel and parent.

- [ ] **Step 3: SettingsNotifications**

```ts
type DigestCadence = 'twice_daily' | 'daily' | 'weekly' | 'off';
// DIGEST_OPTIONS include twice_daily first or after daily
// accept twice_daily from API in useEffect
```

Add i18n keys: `settings.twiceDaily`, hint text.

- [ ] **Step 4: Run UI unit tests if present**

```bash
cd ui/app && npm test -- --run 2>/dev/null || npx vitest run
```

Fix broken snapshots/assertions.

- [ ] **Step 5: Commit**

```bash
git add ui/app/src/
git commit -m "feat(ui): quality-first matching copy and twice_daily digest"
```

---

### Task 9: Ops docs + notification prefs backend accept twice_daily

**Files:**
- Modify: `docs/ops/matching-pipeline.md`
- Modify: `docs/ops/end-user-value-proof.md`
- Grep server notification prefs PATCH for email_digest validation

- [ ] **Step 1: Find prefs writer**

```bash
rg -n "email_digest|NormalizeDigestCadence" apps/matching pkg --glob '*.go'
```

Ensure PATCH accepts `twice_daily` via `NormalizeDigestCadence` (already if normalize is used on write).

- [ ] **Step 2: Rewrite matching-pipeline.md sections**

- Paths table: Path A default off; Path C index-only; MatchInvoke for refresh+digest
- Scoring floor 0.70
- No plan row caps; invoke limits table
- Digest top-3 unseen + cadences
- Env table updates

- [ ] **Step 3: Update end-user-value-proof.md** value equation table (no 5/week).

- [ ] **Step 4: Commit**

```bash
git add docs/ops/
git commit -m "docs(ops): invoke-only matching and quality-first digests"
```

---

### Task 10: Integration smoke + final verification

- [ ] **Step 1: Package tests**

```bash
go test ./pkg/matching/ ./pkg/billing/ ./apps/matching/... -count=1 -short
```

Expected: PASS (skip integration tags if Docker required).

- [ ] **Step 2: Manual checklist (document in PR)**

1. Fan-out disabled: no consumer registration log “Path A fan-out DISABLED”.
2. CV embed: index updates; no new matches until Find matches.
3. Find matches: returns ≥70% only; many rows allowed.
4. Free second Find matches same day: `rate_limited`.
5. Digest: ≤3 matches; second digest does not repeat IDs.
6. Settings: save twice_daily.

- [ ] **Step 3: Open PR** (if requested) summarizing Track A only.

---

## Spec coverage checklist

| Spec requirement | Task |
|------------------|------|
| MatchInvoke only | 3, 4 |
| Path A off default | 1, 4 |
| Path C index-only | 4 |
| Min score 0.70 | 1, 3, 4 |
| No in-app count caps | 2, 3, 4 |
| Free 1 invoke/day | 2, 3, 4 |
| Starter/Managed invoke ceilings | 2, 4 |
| Digest ≤3 unseen | 6 |
| twice_daily cadence | 5, 7, 8 |
| Receipts | 6 |
| UI copy | 8 |
| Ops docs | 9 |
| Employer ATS out of scope | — (no tasks) |

## Notes for implementers

- **Do not** reintroduce overflow for plan limits.
- **Do not** send notifications from `user_refresh`.
- Prefer UTC for invoke day boundaries (`PGInvokeCounter`).
- If `match_run_events.candidate_id` is sparse in old rows, rate limit may under-count until new invokes write events — acceptable.
- Integration tests under `tests/integration/matching_pipeline_test.go` may assert Path A happy path — mark skip when fan-out disabled or update to document emergency-only Path A.
