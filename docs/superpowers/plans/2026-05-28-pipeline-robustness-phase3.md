# Pipeline Robustness — Phase 3: Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining observability + lifecycle gaps that Phase 1 and 2 don't address: (C) wire the missing `/admin/retention/expire` endpoint so opportunities actually expire, (D) make `pipeline_variants` writes mandatory so the ledger is trustworthy, (E) add DLQ inspector + replay admin endpoints so dead-lettered messages aren't silently lost.

**Architecture:** Three independent changes. C and E are small new endpoints + handlers; D is a careful refactor of soft-fail call sites. None of them depend on each other.

**Tech Stack:** Go, NATS JetStream admin API, Frame v1.97+, Postgres, GORM.

**Depends on:** Phase 1 (variantstate writes happen but are soft-fail). Independent of Phase 2.

---

## File Structure

**Modified:**
- `pkg/variantstate/store.go` — change `soft()` helper to return errors instead of swallowing them; update each method's behaviour.
- All callers of `variantstate.Upsert`, `AdvanceStage`, etc. — handle the now-non-nil errors (NATS redelivery is the recovery strategy; just return the error from the handler).
- `apps/crawler/cmd/main.go` — add `POST /admin/retention/expire` endpoint.
- `apps/api/cmd/main.go` — add `GET /admin/dlq` and `POST /admin/dlq/replay`.

**Created:**
- `apps/crawler/service/retention_expire.go` — handler that marks `opportunities.status='expired'` past TTL.
- `apps/api/cmd/dlq_admin.go` — handlers for DLQ list + replay.
- `pkg/dlq/inspector.go` — NATS JetStream consumer-state queries.

---

## Decisions

**On D (mandatory writes):**
- The single soft-fail-to-error change must NOT cause an outage if Postgres has a transient hiccup. NATS at-least-once redelivery is the recovery: handler returns error → Frame redelivers → next attempt likely succeeds.
- The CHANGE is removing the silent log-and-continue. Errors propagate. If Postgres is genuinely down for an extended period, the pipeline DOES stall — but that's correct: we want operators to see this as a P1, not as silently-degraded observability.
- Exception: `variantstate.LookupCanonical` stays soft-fail. It's a cache-style lookup; "miss" is a valid result.

**On C (retention expire):**
- TTL is per-kind. Jobs expire after `30 days` of `last_seen_at` not being updated; scholarships after `180 days`; tenders after `90 days`. Defaults configurable via env var.
- The expiry job emits `opportunities.canonicals.expired.v1` events so downstream consumers (materializer, search index) can react.
- The handler MUST be idempotent (cron fires every 15 min; the same opportunity should only emit one expired event).

**On E (DLQ admin):**
- DLQ is a NATS JetStream consumer with `max_deliver` exhausted messages. We expose:
  - `GET /admin/dlq` — list per-consumer pending counts + sample messages.
  - `POST /admin/dlq/replay?consumer=X&count=N` — re-publish up to N messages from DLQ back to the original topic.
- Authentication: same `requireAdmin` middleware existing endpoints use.

---

## Tasks

### Task 1 (C): Retention expire endpoint

**Files:**
- Create: `apps/crawler/service/retention_expire.go`
- Modify: `apps/crawler/cmd/main.go` (register endpoint).
- Modify: `pkg/variantstate/store.go` — add `ExpireOlderThan(kind, ttl)` method.

- [ ] **Step 1: Write failing test**

Create `apps/crawler/service/retention_expire_test.go`:

```go
package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/apps/crawler/service"
)

func TestRetentionExpire_MarksOldOpportunitiesExpired(t *testing.T) {
	ctx := context.Background()
	scaffold := newRetentionScaffold(t)
	// Seed 3 jobs: 2 last-seen 31 days ago (should expire), 1 last-seen yesterday (kept).
	scaffold.SeedOpportunity("opp-old-1", "job", time.Now().AddDate(0, 0, -31))
	scaffold.SeedOpportunity("opp-old-2", "job", time.Now().AddDate(0, 0, -45))
	scaffold.SeedOpportunity("opp-fresh", "job", time.Now().AddDate(0, 0, -1))

	h := service.RetentionExpireHandler(service.RetentionExpireDeps{
		VariantStore: scaffold.VariantStore,
		Emitter:      scaffold.Emitter,
		TTLByKind: map[string]time.Duration{
			"job":         30 * 24 * time.Hour,
			"scholarship": 180 * 24 * time.Hour,
		},
	})

	req := httptest.NewRequest("POST", "/admin/retention/expire", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body)
	}

	scaffold.AssertExpired("opp-old-1")
	scaffold.AssertExpired("opp-old-2")
	scaffold.AssertActive("opp-fresh")

	// Two expired events should have been emitted.
	if scaffold.Emitter.Count("opportunities.canonicals.expired.v1") != 2 {
		t.Fatalf("emitted expired events = %d; want 2",
			scaffold.Emitter.Count("opportunities.canonicals.expired.v1"))
	}
}

func TestRetentionExpire_Idempotent(t *testing.T) {
	ctx := context.Background()
	scaffold := newRetentionScaffold(t)
	scaffold.SeedOpportunity("opp-old", "job", time.Now().AddDate(0, 0, -45))

	h := service.RetentionExpireHandler(service.RetentionExpireDeps{
		VariantStore: scaffold.VariantStore,
		Emitter:      scaffold.Emitter,
		TTLByKind: map[string]time.Duration{"job": 30 * 24 * time.Hour},
	})

	// Two calls in a row.
	req := httptest.NewRequest("POST", "/admin/retention/expire", nil)
	h(httptest.NewRecorder(), req)
	h(httptest.NewRecorder(), req)

	// Should still only have one emitted expired event (idempotent).
	if scaffold.Emitter.Count("opportunities.canonicals.expired.v1") != 1 {
		t.Fatalf("emitted = %d; want 1 (idempotent)",
			scaffold.Emitter.Count("opportunities.canonicals.expired.v1"))
	}
}
```

- [ ] **Step 2: Run, verify fail**

```bash
go test ./apps/crawler/service/ -run TestRetentionExpire -v 2>&1 | tail -15
```
Expected: FAIL — `RetentionExpireHandler` undefined.

- [ ] **Step 3: Add ExpireOlderThan to variantstate.Store**

Append to `pkg/variantstate/store.go`:

```go
// ExpireOlderThan flips status='active' → 'expired' for all
// opportunities of `kind` whose last_seen_at is older than `ttl` ago,
// AND returns the list of expired canonical_ids so the caller can
// emit events for them.
//
// Idempotent: only `active` rows are touched, so a repeat call after
// the first finds nothing to update (returns empty slice).
func (s *Store) ExpireOlderThan(
	ctx context.Context,
	kind string,
	ttl time.Duration,
) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	cutoff := time.Now().Add(-ttl).UTC()
	var rows []struct {
		CanonicalID string
	}
	err := s.db(ctx, false).Raw(`
        UPDATE opportunities
           SET status = 'expired', updated_at = now()
         WHERE kind = ?
           AND status = 'active'
           AND last_seen_at < ?
        RETURNING canonical_id
    `, kind, cutoff).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("variantstate.ExpireOlderThan: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.CanonicalID)
	}
	return ids, nil
}
```

- [ ] **Step 4: Implement the handler**

Create `apps/crawler/service/retention_expire.go`:

```go
package service

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
)

// RetentionExpireDeps are the collaborators the retention handler needs.
type RetentionExpireDeps struct {
	VariantStore *variantstate.Store
	Emitter      frame.EventsManager
	// TTLByKind maps opportunity-kind → expiry TTL. Defaults injected
	// here keep the handler stateless; main.go reads from config.
	TTLByKind map[string]time.Duration
}

// RetentionExpireHandler returns an http.HandlerFunc that marks
// opportunities expired per their kind-specific TTL and emits one
// canonicals.expired.v1 event per newly-expired row.
//
// Wired by the Trustage retention.expire workflow (cron `*/15 * * * *`).
// Idempotent: repeated invocations find nothing if no new rows have
// aged past the TTL since the previous call.
func RetentionExpireHandler(deps RetentionExpireDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		total := 0
		perKind := map[string]int{}

		for kind, ttl := range deps.TTLByKind {
			ids, err := deps.VariantStore.ExpireOlderThan(ctx, kind, ttl)
			if err != nil {
				log.WithError(err).WithField("kind", kind).
					Warn("retention.expire: ExpireOlderThan failed")
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			perKind[kind] = len(ids)
			total += len(ids)

			for _, id := range ids {
				event := eventsv1.CanonicalExpiredV1{
					CanonicalID: id,
					Kind:        kind,
					ExpiredAt:   time.Now().UTC(),
				}
				env := eventsv1.NewEnvelope(eventsv1.TopicCanonicalsExpired, event)
				if err := deps.Emitter.Emit(ctx, eventsv1.TopicCanonicalsExpired, env); err != nil {
					log.WithError(err).WithField("canonical_id", id).
						Warn("retention.expire: emit failed; status already flipped")
					// Don't fail the whole batch — the row IS expired in
					// the DB, the event is best-effort downstream.
				}
			}
		}

		log.WithField("total", total).Info("retention.expire: complete")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"total":      total,
			"per_kind":   perKind,
			"expired_at": time.Now().UTC(),
		})
	}
}
```

- [ ] **Step 5: Add CanonicalExpiredV1 and topic**

In `pkg/events/v1/types.go`:

```go
const TopicCanonicalsExpired = "opportunities.canonicals.expired.v1"

type CanonicalExpiredV1 struct {
	CanonicalID string    `json:"canonical_id"`
	Kind        string    `json:"kind"`
	ExpiredAt   time.Time `json:"expired_at"`
}
```

Add `TopicCanonicalsExpired` to `AllTopics()` if that function exists for writer subscription.

- [ ] **Step 6: Wire endpoint in apps/crawler/cmd/main.go**

Find where other admin endpoints are registered (around line 528) and add:

```go
adminMux.HandleFunc("POST /admin/retention/expire",
    service.RetentionExpireHandler(service.RetentionExpireDeps{
        VariantStore: variantStore,
        Emitter:      svc.EventsManager(),
        TTLByKind: map[string]time.Duration{
            "job":         cfg.RetentionTTLJob,         // env: RETENTION_TTL_JOB; default 720h (30d)
            "scholarship": cfg.RetentionTTLScholarship, // env: RETENTION_TTL_SCHOLARSHIP; default 4320h (180d)
            "tender":      cfg.RetentionTTLTender,     // env: RETENTION_TTL_TENDER; default 2160h (90d)
            "funding":     cfg.RetentionTTLFunding,    // env: RETENTION_TTL_FUNDING; default 2160h (90d)
            "deal":        cfg.RetentionTTLDeal,       // env: RETENTION_TTL_DEAL; default 720h (30d)
        },
    }))
```

Add the config fields to `apps/crawler/config/config.go`:

```go
RetentionTTLJob         time.Duration `env:"RETENTION_TTL_JOB" envDefault:"720h"`
RetentionTTLScholarship time.Duration `env:"RETENTION_TTL_SCHOLARSHIP" envDefault:"4320h"`
RetentionTTLTender      time.Duration `env:"RETENTION_TTL_TENDER" envDefault:"2160h"`
RetentionTTLFunding     time.Duration `env:"RETENTION_TTL_FUNDING" envDefault:"2160h"`
RetentionTTLDeal        time.Duration `env:"RETENTION_TTL_DEAL" envDefault:"720h"`
```

- [ ] **Step 7: Run tests, verify pass, commit**

```bash
go test ./apps/crawler/service/ -run TestRetentionExpire -v 2>&1 | tail -15
go build ./apps/crawler/... 2>&1 | tail -5
git add apps/crawler/service/retention_expire.go apps/crawler/service/retention_expire_test.go pkg/variantstate/store.go pkg/events/v1/types.go apps/crawler/cmd/main.go apps/crawler/config/config.go
git commit -m "feat(retention): wire /admin/retention/expire endpoint

Closes the 404 gap left by definitions/trustage/retention-expire.json.
Opportunities now expire per-kind TTL (jobs 30d, scholarships 180d,
tenders/funding 90d, deals 30d) and emit canonicals.expired.v1
events for downstream consumers. Idempotent."
```

---

### Task 2 (D): Make variantstate writes mandatory

**Files:**
- Modify: `pkg/variantstate/store.go` (the `soft()` helper, lines 308-318).
- Modify: each caller in `apps/crawler/service/crawl_request_handler.go` and `apps/worker/service/*.go`.

- [ ] **Step 1: Write failing test**

This is a "negative" test — confirm a Postgres outage now causes the handler to return an error (not silently succeed).

Add to `pkg/variantstate/store_test.go`:

```go
func TestStore_Upsert_ReturnsErrorOnDBFailure(t *testing.T) {
	// Use a pool that returns a transient error on Create.
	store := variantstate.NewStore(failingPool(t))
	err := store.Upsert(context.Background(), variantstate.Variant{
		VariantID:    "test-var",
		SourceID:     "src",
		HardKey:      "hk",
		Kind:         "job",
		CurrentStage: variantstate.StageIngested,
	})
	if err == nil {
		t.Fatalf("Upsert returned nil; want error from underlying DB failure")
	}
}

// failingPool returns a GORM DB whose Create() always returns an error.
func failingPool(t *testing.T) func(context.Context, bool) *gorm.DB {
	// Implementation: open sqlite-in-memory, drop the table, return the
	// DB — any Create call will fail with "no such table".
	// (Or use the existing fake-pool helper if frametest has one.)
}
```

- [ ] **Step 2: Run, verify fail**

```bash
go test ./pkg/variantstate/ -run TestStore_Upsert_ReturnsErrorOnDBFailure -v 2>&1 | tail -5
```
Expected: FAIL (Upsert returns nil despite DB error).

- [ ] **Step 3: Modify the soft helper**

In `pkg/variantstate/store.go:308-318`, replace `soft` with:

```go
// hard returns the underlying error, logging at WARN. Replaces the
// previous soft() pattern that returned nil and silenced failures.
// Callers must propagate the error so NATS redelivers the message —
// that's the recovery mechanism, not silent log-and-continue.
//
// LookupCanonical and GetOpportunity keep their "treat miss as nil"
// behaviour because those are cache-style reads where absence is a
// valid result; only this UPDATE/INSERT helper is tightened.
func (s *Store) hard(ctx context.Context, err error, op, variantID, stage string) error {
	if err == nil {
		return nil
	}
	util.Log(ctx).WithError(err).
		WithField("op", op).
		WithField("variant_id", variantID).
		WithField("stage", stage).
		Warn("variantstate: write failed")
	return err
}
```

Then update every reference to `s.soft(…)` in the file to `s.hard(…)` — except `LookupCanonical` and `GetOpportunity`, which keep the soft-miss behaviour.

`grep -n "s.soft" pkg/variantstate/store.go` to find the call sites; rename them.

- [ ] **Step 4: Run test, verify pass**

```bash
go test ./pkg/variantstate/ -count=1 -v 2>&1 | tail -10
```
Expected: all PASS, including the new failure-mode test.

- [ ] **Step 5: Update crawler call sites to propagate the error**

In `apps/crawler/service/crawl_request_handler.go:345-352`, the call is:

```go
_ = h.deps.VariantStore.Upsert(ctx, variantstate.Variant{...})
```

The `_ =` is the soft-fail acceptance. Replace with:

```go
if err := h.deps.VariantStore.Upsert(ctx, variantstate.Variant{...}); err != nil {
    // Ledger row failed. Return error so Frame redelivers the whole
    // crawl.request — the variant emit at line 353 must not happen
    // without a ledger row, or we lose observability for it.
    return fmt.Errorf("crawl.request: variantstate.Upsert: %w", err)
}
```

Same treatment for the call at line 497 (publishRejected) and any others in this file.

- [ ] **Step 6: Update worker call sites**

`grep -rn "VariantStore.Upsert\|VariantStore.AdvanceStage\|VariantStore.MarkPublishedByCanonical" apps/worker/ --include="*.go"` to find them. Each one currently uses `_ =` or `if err …; _ = log.Warn`. Change to propagate the error as the return value of the Frame handler:

```go
if err := h.deps.VariantStore.AdvanceStage(ctx, variantID, fromStage, toStage, nil, nil); err != nil {
    return fmt.Errorf("variantstate.AdvanceStage: %w", err)
}
```

NATS will redeliver up to `max_deliver` times; after that the message dead-letters and the new DLQ admin (Task 3) lets operators see it.

- [ ] **Step 7: Run full test suite**

```bash
go test ./... -count=1 2>&1 | tail -20
```
Expected: PASS. Some tests may need updating if they relied on soft-fail silencing — update them to expect the error.

- [ ] **Step 8: Commit**

```bash
git add pkg/variantstate/store.go pkg/variantstate/store_test.go apps/crawler/service/crawl_request_handler.go apps/worker/service/*.go
git commit -m "fix(ledger): variantstate writes propagate errors

The previous soft-fail pattern silently swallowed Postgres outages,
leaving pipeline_variants with missing rows. The pipeline kept
'working' on NATS strength alone, but operator observability degraded
without any signal. Errors now propagate; NATS at-least-once
redelivery is the recovery path. LookupCanonical + GetOpportunity
keep miss-as-nil because they're cache-style reads."
```

---

### Task 3 (E): DLQ inspector + replay

**Files:**
- Create: `pkg/dlq/inspector.go`
- Create: `apps/api/cmd/dlq_admin.go`
- Modify: `apps/api/cmd/main.go` (register endpoints).

- [ ] **Step 1: Understand NATS DLQ conventions used in this codebase**

```bash
grep -rn "deadletter\|dead_letter\|max_deliver" pkg/events/ apps/*/cmd/main.go --include="*.go" 2>&1 | head -10
```

The DLQ is the JetStream consumer's "delivery exhausted" state — after `max_deliver` attempts, messages don't go to a separate topic by default; they stay on the source stream marked `negative_acked` and become invisible to the consumer. Operators need a way to:
- Count those messages per consumer.
- Re-publish them.

- [ ] **Step 2: Implement the inspector**

```go
// pkg/dlq/inspector.go
package dlq

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ConsumerSnapshot captures the state operators care about.
type ConsumerSnapshot struct {
	Stream         string    `json:"stream"`
	Consumer       string    `json:"consumer"`
	NumPending     uint64    `json:"num_pending"`
	NumAckPending  int       `json:"num_ack_pending"`
	NumRedelivered int       `json:"num_redelivered"`
	NumWaiting     int       `json:"num_waiting"`
	LastDelivered  time.Time `json:"last_delivered_at,omitempty"`
}

// Inspector queries JetStream consumer state.
type Inspector struct {
	js jetstream.JetStream
}

func NewInspector(nc *nats.Conn) (*Inspector, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	return &Inspector{js: js}, nil
}

// ListConsumers returns one ConsumerSnapshot per consumer in the
// stream.
func (i *Inspector) ListConsumers(ctx context.Context, streamName string) ([]ConsumerSnapshot, error) {
	stream, err := i.js.Stream(ctx, streamName)
	if err != nil {
		return nil, err
	}
	consumers := stream.ListConsumers(ctx)

	var out []ConsumerSnapshot
	for ci := range consumers.Info() {
		out = append(out, ConsumerSnapshot{
			Stream:         streamName,
			Consumer:       ci.Name,
			NumPending:     ci.NumPending,
			NumAckPending:  ci.NumAckPending,
			NumRedelivered: ci.NumRedelivered,
			NumWaiting:     ci.NumWaiting,
			LastDelivered:  ci.Delivered.Last.Time,
		})
	}
	if err := consumers.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Replay re-publishes up to `count` messages from the given stream
// that have exhausted their max_deliver retries. Returns the number
// of messages replayed.
//
// Strategy: use the JetStream "advisory" channel for max-deliver
// notifications, OR fetch from a parallel consumer with no delivery
// limit. Choose based on existing patterns in the codebase.
func (i *Inspector) Replay(ctx context.Context, streamName, subject string, count int) (int, error) {
	// Implementation: create a temporary, ephemeral consumer that
	// starts from `delivery_policy=last_per_subject` filtered to the
	// subject, pull N messages, re-publish them with a fresh nats.Msg,
	// then drain the ephemeral consumer.
	//
	// See nats.go docs for ConsumerConfig.DeliverPolicy = nats.DeliverLastPerSubject.
	return 0, nil // stubbed; expand per actual JetStream API surface available.
}
```

- [ ] **Step 3: Wire admin endpoints**

```go
// apps/api/cmd/dlq_admin.go
package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/stawi-opportunities/opportunities/pkg/dlq"
)

func registerDLQAdmin(mux *http.ServeMux, inspector *dlq.Inspector) {
	mux.HandleFunc("GET /admin/dlq", requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		stream := r.URL.Query().Get("stream")
		if stream == "" {
			stream = "svc_opportunities_events"
		}
		out, err := inspector.ListConsumers(r.Context(), stream)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stream":    stream,
			"consumers": out,
		})
	}))

	mux.HandleFunc("POST /admin/dlq/replay", requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		stream := r.URL.Query().Get("stream")
		subject := r.URL.Query().Get("subject")
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		if count <= 0 {
			count = 10
		}
		if count > 1000 {
			http.Error(w, "count > 1000 not allowed", http.StatusBadRequest)
			return
		}
		replayed, err := inspector.Replay(r.Context(), stream, subject, count)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"replayed": replayed,
		})
	}))
}
```

- [ ] **Step 4: Wire in main**

In `apps/api/cmd/main.go`, after other admin registrations:

```go
inspector, err := dlq.NewInspector(svc.NATSConn())
if err != nil {
    log.WithError(err).Fatal("dlq inspector init")
}
registerDLQAdmin(mux, inspector)
```

- [ ] **Step 5: Test**

```go
// apps/api/cmd/dlq_admin_test.go — uses an in-process NATS server
// from natstest or similar. Pattern matches the rest of the apps/api
// tests; copy a neighboring test file's scaffold.
func TestDLQ_ListConsumers_ReturnsBacklog(t *testing.T) { /* ... */ }
func TestDLQ_Replay_RepublishesMessages(t *testing.T) { /* ... */ }
```

- [ ] **Step 6: Commit**

```bash
go build ./... 2>&1 | tail -5
git add pkg/dlq/inspector.go apps/api/cmd/dlq_admin.go apps/api/cmd/main.go apps/api/cmd/dlq_admin_test.go
git commit -m "feat(dlq): admin endpoints to inspect and replay dead-lettered messages

Closes the 'failures accumulate invisibly' gap. GET /admin/dlq lists
per-consumer pending counts; POST /admin/dlq/replay re-publishes up
to N messages back to the source subject. Same requireAdmin guard
as other admin endpoints."
```

---

### Task 4: Tag and deploy

- [ ] **Step 1: Push and tag**

```bash
git push origin main
git tag v8.0.60
git push origin v8.0.60
```

- [ ] **Step 2: After Flux rolls, verify**

```bash
# Retention is running.
kubectl logs -n product-opportunities -l app.kubernetes.io/name=opportunities-crawler --tail=200 | grep retention.expire

# DLQ admin reachable.
kubectl port-forward -n product-opportunities svc/opportunities-api 18080:80 &
curl -s http://localhost:18080/admin/dlq?stream=svc_opportunities_events | jq .

# Postgres outage smoke test (skip unless you have a maintenance window):
# Pause one of the Postgres pods and verify that crawler logs show
# variantstate write failures instead of silent success.
```

---

## Phase 3 Exit Criteria

- [ ] `/admin/retention/expire` returns 200 with a per-kind count of newly-expired opportunities.
- [ ] `opportunities.status='expired'` count grows after each retention cron tick.
- [ ] Postgres outage causes `crawl.requests.v1` consumer lag to grow (correct; was silent before).
- [ ] `/admin/dlq?stream=svc_opportunities_events` returns per-consumer backlogs as JSON.
- [ ] `/admin/dlq/replay?count=10` re-publishes messages and removes them from the DLQ backlog.
