# Worker Consumer-Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the worker's five-stage pipeline off its single shared NATS consumer onto three independently-scaling consumer groups, so a slow or failing stage (llama-bound validate, R2-bound publish) can never back-pressure the throughput-critical clustering stage, and each group auto-scales to its own resource profile.

**Architecture:** One worker binary, selected into a *stage group* at runtime by a `STAGE_GROUP` env var. Each group registers only its own handlers and runs as its own Deployment with its own durable JetStream consumer (independent `ack_pending` budget + position) and its own HPA. Groups: **core** (normalize→dedup→canonical, CPU/Valkey/Postgres, scales high), **validate** (llama-bound, fail-open, capped), **publish** (R2 + embed + translate, external-I/O, isolated with its own retry budget). The existing loose-mode "see-all-events, act-on-mine, ack-skip-the-rest" dispatch is preserved — we just give each group its own consumer instead of sharing one.

**Tech Stack:** Go, Frame framework (`github.com/pitabwire/frame`), NATS JetStream, Helm (colony chart) via Flux, Postgres ledger (`pipeline_variants`).

**Why this matters (observed in production):** all five stages share durable consumer `worker-events`. When `PublishHandler` Nack-stormed on an R2 outage it starved that consumer's `ack_pending`, halting `clustered→canonical` advance for ~18h (0 published). Separately, the worker was capped at `maxReplicas=2` to match llama — but the *bottleneck* stage is clustering (Valkey+PG, not llama), so an I/O-bound backlog of 244K `validated` rows couldn't auto-drain. Splitting the consumers fixes both: publish failures stay contained to the publish group, and core scales on its own CPU signal without over-driving llama.

---

## Background the implementer needs

**Current wiring** (`apps/worker/cmd/main.go` ~lines 224-246):
```go
pipelineHandlers := service.EventHandlers()        // all 5 stage handlers
svc.Init(ctx,
    frame.WithRegisterEvents(pipelineHandlers...),                                      // ONE shared consumer
    frame.WithRegisterPublisher(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL),
    frame.WithRegisterPublisher(eventsv1.SubjectWorkerTranslate, cfg.WorkerTranslateQueueURL),
    frame.WithRegisterSubscriber(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL, service.EmbedWorker()),
    frame.WithRegisterSubscriber(eventsv1.SubjectWorkerTranslate, cfg.WorkerTranslateQueueURL, service.TranslateWorker()),
    frame.WithHTTPHandler(adminMux),
)
mgr.SetStrict(false)   // loose mode: ack-skip topics this process doesn't handle
```

**Handler → topic it consumes** (the `Name()` of each handler, from `apps/worker/service/*.go`):
| Handler | Consumes (`Name()`) | Emits | Resource profile | Group |
|---|---|---|---|---|
| `NormalizeHandler` | `TopicVariantsIngested` | `TopicVariantsNormalized` | CPU/PG | **core** |
| `ValidateHandler` | `TopicVariantsNormalized` | `TopicVariantsValidated` / `Flagged` | **llama** (fail-open) | **validate** |
| `DedupHandler` | `TopicVariantsValidated` | `TopicVariantsClustered` | Valkey+PG (bottleneck) | **core** |
| `CanonicalHandler` | `TopicVariantsClustered` | `TopicCanonicalsUpserted` + enqueues embed/translate | PG | **core** |
| `PublishHandler` | `TopicCanonicalsUpserted` | `TopicPublished` | R2 (external I/O) | **publish** |
| `EmbedWorker` | `SubjectWorkerEmbed` (own consumer already) | — | TEI | **publish** |
| `TranslateWorker` | `SubjectWorkerTranslate` (own consumer already) | — | llama | **publish** |

**Key Frame facts:**
- `frame.WithRegisterEvents(handlers...)` registers N handlers on the events-manager's single consumer, whose connection string is `cfg`-derived (the `EVENTS_QUEUE_URL` env). The consumer's `consumer_durable_name` and `consumer_max_ack_pending` come from that URL's query params.
- Loose mode (`mgr.SetStrict(false)`) makes the consumer ack-and-skip any topic for which no handler is registered. This is what lets each group subscribe to the wildcard `svc.opportunities.events.>` yet act only on its own topics.
- Two consumers with **different `consumer_durable_name`** on the same stream are fully independent: each keeps its own position, redelivery, and `ack_pending`. This is exactly the embed/translate pattern (`worker-embed-events`, `worker-translate-events`).

**The plan's core trick:** keep the loose-mode wildcard subscription, but run three *separate processes* each with its own `EVENTS_QUEUE_URL` (→ own durable consumer) and each registering only its group's handlers. No new subject routing, no event-shape changes — just three consumers where there was one.

---

## File Structure

**Created:**
- `apps/worker/service/groups.go` — `HandlersForGroup(group string) []events.EventI` returning the handler subset per group; single source of truth for the stage→group mapping.
- `apps/worker/service/groups_test.go` — asserts the mapping is exhaustive + disjoint.
- `namespaces/product-opportunities/worker-core/opportunities-worker-core.yaml` (deployments repo) — core group HelmRelease + HPA.
- `namespaces/product-opportunities/worker-core/kustomization.yaml`
- `namespaces/product-opportunities/worker-validate/opportunities-worker-validate.yaml` + kustomization.
- `namespaces/product-opportunities/worker-publish/opportunities-worker-publish.yaml` + kustomization.

**Modified:**
- `apps/worker/config/config.go` — add `StageGroup` + per-group `EventsQueueURL` already exists; reuse it (the queue URL differs per deployment via env, so no new field needed beyond `StageGroup`).
- `apps/worker/cmd/main.go` — register `HandlersForGroup(cfg.StageGroup)` instead of all handlers; only the **publish** group registers the embed/translate subscribers + publishers; only **core** runs the reaper.
- `apps/worker/service/service.go` (or wherever `EventHandlers()` lives) — keep `EventHandlers()` (used by `STAGE_GROUP=all` legacy/test path) and have `HandlersForGroup` reuse the same constructors.
- `namespaces/product-opportunities/kustomization_provider.yaml` (deployments repo) — add three Flux Kustomization CRs.
- `namespaces/product-opportunities/common/flux-image-automation.yaml` — the three new Deployments reuse the existing `opportunities-worker` image (same binary), so **no new ImagePolicy** is needed; they carry the `$imagepolicy` marker pointing at `opportunities-worker`.
- `namespaces/product-opportunities/worker/opportunities-worker.yaml` — scaled to `replicaCount: 0` (retired but retained for one release as instant rollback), OR deleted in Task 11 after the split is verified.

---

## Tasks

### Task 1: Stage-group config field

**Files:**
- Modify: `apps/worker/config/config.go`

- [ ] **Step 1: Add the `StageGroup` field**

In the `Config` struct, after the existing queue-URL fields, add:

```go
	// StageGroup selects which pipeline handlers this process registers,
	// so the worker can run as independently-scaling consumer groups:
	//   "core"     — normalize, dedup (cluster), canonical
	//   "validate" — validate (llama-bound, fail-open)
	//   "publish"  — publish (R2) + embed + translate
	//   "all"      — every handler on one consumer (legacy monolith; the
	//                default keeps existing single-deployment installs and
	//                the integration tests working unchanged).
	StageGroup string `env:"STAGE_GROUP" envDefault:"all"`
```

- [ ] **Step 2: Build**

Run: `go build ./apps/worker/...`
Expected: exit 0, no output.

- [ ] **Step 3: Commit**

```bash
git add apps/worker/config/config.go
git commit -m "feat(worker/config): STAGE_GROUP selects the consumer group"
```

---

### Task 2: `HandlersForGroup` mapping (the single source of truth)

**Files:**
- Create: `apps/worker/service/groups.go`
- Test: `apps/worker/service/groups_test.go`

- [ ] **Step 1: Write the failing test**

`apps/worker/service/groups_test.go`:

```go
package service

import (
	"sort"
	"testing"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// The five pipeline topics every variant must traverse. embed/translate
// are subject-queue workers (not events-manager handlers) and are wired
// separately, so they are intentionally absent here.
var allPipelineTopics = []string{
	eventsv1.TopicVariantsIngested,
	eventsv1.TopicVariantsNormalized,
	eventsv1.TopicVariantsValidated,
	eventsv1.TopicVariantsClustered,
	eventsv1.TopicCanonicalsUpserted,
}

func topicsOf(hs []handlerNamer) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Name())
	}
	sort.Strings(out)
	return out
}

// handlerNamer is the minimal surface we assert on (every Frame EventI
// has Name()).
type handlerNamer interface{ Name() string }

func asNamers(hs []eventI) []handlerNamer {
	out := make([]handlerNamer, 0, len(hs))
	for _, h := range hs {
		out = append(out, h)
	}
	return out
}

func TestHandlersForGroup_PartitionsAllTopicsExactlyOnce(t *testing.T) {
	seen := map[string]int{}
	for _, g := range []string{"core", "validate", "publish"} {
		for _, h := range HandlersForGroup(g) {
			seen[h.Name()]++
		}
	}
	for _, topic := range allPipelineTopics {
		if seen[topic] != 1 {
			t.Errorf("topic %q handled by %d groups; want exactly 1", topic, seen[topic])
		}
	}
}

func TestHandlersForGroup_AllEqualsUnionOfGroups(t *testing.T) {
	union := map[string]bool{}
	for _, g := range []string{"core", "validate", "publish"} {
		for _, h := range HandlersForGroup(g) {
			union[h.Name()] = true
		}
	}
	for _, h := range HandlersForGroup("all") {
		if !union[h.Name()] {
			t.Errorf("topic %q in group 'all' but in no split group", h.Name())
		}
	}
}

func TestHandlersForGroup_UnknownGroupIsEmpty(t *testing.T) {
	if got := HandlersForGroup("nope"); len(got) != 0 {
		t.Errorf("unknown group returned %d handlers; want 0", len(got))
	}
}
```

Note: `eventI` is a tiny local alias to the Frame event interface — define it in `groups.go` (Step 3) as `type eventI = events.EventI` so the test compiles. Replace `events.EventI` with the actual import path used by `EventHandlers()` (grep `apps/worker/service/service.go` for the existing return type — it is `[]events.EventI` where `events` is `github.com/pitabwire/frame/events`).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./apps/worker/service/ -run TestHandlersForGroup -v`
Expected: FAIL — `undefined: HandlersForGroup`.

- [ ] **Step 3: Implement `HandlersForGroup`**

`apps/worker/service/groups.go`:

```go
package service

import (
	"github.com/pitabwire/frame/events"
)

// eventI aliases Frame's event handler interface so this file (and the
// group test) doesn't repeat the import path.
type eventI = events.EventI

// HandlersForGroup returns the events-manager handlers a worker process
// should register for the given STAGE_GROUP. Splitting the pipeline
// across groups gives each its own durable consumer (independent
// ack_pending + scaling) so a slow/failing stage can't back-pressure the
// others. embed/translate are subject-queue subscribers wired in main.go,
// not events-manager handlers, so they are not returned here.
//
// Mapping (keep in sync with the deployment manifests):
//   core     → normalize, dedup (cluster), canonical   (CPU/Valkey/PG, scales high)
//   validate → validate                                (llama-bound, fail-open, capped)
//   publish  → publish                                 (R2; embed/translate wired alongside)
//   all      → every handler on one consumer           (legacy monolith / tests)
func HandlersForGroup(group string) []eventI {
	switch group {
	case "core":
		return []eventI{
			NewNormalizeHandler(),
			NewDedupHandler(),
			NewCanonicalHandler(),
		}
	case "validate":
		return []eventI{
			NewValidateHandler(),
		}
	case "publish":
		return []eventI{
			NewPublishHandler(),
		}
	case "all":
		return EventHandlers()
	default:
		return nil
	}
}
```

IMPORTANT: the constructor names above (`NewNormalizeHandler` etc.) are placeholders — grep `EventHandlers()` in `apps/worker/service/service.go` for the real constructors and use those exact names. If `EventHandlers()` builds handlers inline (e.g. `&NormalizeHandler{...}`) rather than via constructors, extract each into a `NewXxxHandler()` constructor first (one per handler) and have both `EventHandlers()` and `HandlersForGroup` call them — this keeps construction DRY and is the only refactor needed.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./apps/worker/service/ -run TestHandlersForGroup -v`
Expected: PASS (all three subtests).

- [ ] **Step 5: Run the full worker service suite to confirm no regression**

Run: `go test ./apps/worker/... -count=1 -short`
Expected: ok, no failures.

- [ ] **Step 6: Commit**

```bash
git add apps/worker/service/groups.go apps/worker/service/groups_test.go apps/worker/service/service.go
git commit -m "feat(worker): HandlersForGroup splits the pipeline into consumer groups"
```

---

### Task 3: main.go registers only the group's handlers

**Files:**
- Modify: `apps/worker/cmd/main.go` (the `svc.Init` block ~lines 226-264)

- [ ] **Step 1: Replace the handler assembly + Init block**

Replace:

```go
	pipelineHandlers := service.EventHandlers()
	if loader != nil {
		rebuild := func(ctx context.Context) error { /* ... unchanged ... */ }
		pipelineHandlers = append([]events.EventI(pipelineHandlers), definitions.NewBroadcastConsumer(loader, rebuild))
		util.Log(ctx).WithField("topic", eventsv1.TopicDefinitionsChanged).Info("definitions: broadcast consumer wired")
	}
	svc.Init(ctx,
		frame.WithRegisterEvents(pipelineHandlers...),
		frame.WithRegisterPublisher(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL),
		frame.WithRegisterPublisher(eventsv1.SubjectWorkerTranslate, cfg.WorkerTranslateQueueURL),
		frame.WithRegisterSubscriber(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL, service.EmbedWorker()),
		frame.WithRegisterSubscriber(eventsv1.SubjectWorkerTranslate, cfg.WorkerTranslateQueueURL, service.TranslateWorker()),
		frame.WithHTTPHandler(adminMux),
	)
```

with:

```go
	// Select this process's handlers by stage group. Each group runs as
	// its own Deployment with its own EVENTS_QUEUE_URL (→ own durable
	// consumer + ack_pending), so a slow/failing stage can't starve the
	// others. STAGE_GROUP=all preserves the single-deployment monolith.
	pipelineHandlers := service.HandlersForGroup(cfg.StageGroup)

	// The definitions broadcast consumer must run in EVERY group: each
	// group's registry needs live-rebuild on admin edits. It's a
	// broadcast (fanout) subscriber, not a work-queue handler, so
	// registering it in all groups is correct (each maintains its own
	// in-memory registry).
	if loader != nil {
		rebuild := func(ctx context.Context) error {
			fresh, err := opportunity.LoadFromDefinitions(ctx, loader)
			if err != nil {
				return err
			}
			reg.Replace(fresh)
			return nil
		}
		pipelineHandlers = append(pipelineHandlers, definitions.NewBroadcastConsumer(loader, rebuild))
		util.Log(ctx).WithField("topic", eventsv1.TopicDefinitionsChanged).Info("definitions: broadcast consumer wired")
	}

	initOpts := []frame.Option{
		frame.WithRegisterEvents(pipelineHandlers...),
		frame.WithHTTPHandler(adminMux),
	}

	// embed + translate are subject-queue workers owned by the publish
	// group (CanonicalHandler enqueues to them; only publish drains them).
	// Register their publisher+subscriber pair only there. The "all"
	// monolith keeps them too so legacy installs are unchanged.
	if cfg.StageGroup == "publish" || cfg.StageGroup == "all" {
		initOpts = append(initOpts,
			frame.WithRegisterPublisher(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL),
			frame.WithRegisterPublisher(eventsv1.SubjectWorkerTranslate, cfg.WorkerTranslateQueueURL),
			frame.WithRegisterSubscriber(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL, service.EmbedWorker()),
			frame.WithRegisterSubscriber(eventsv1.SubjectWorkerTranslate, cfg.WorkerTranslateQueueURL, service.TranslateWorker()),
		)
	}

	// CanonicalHandler runs in "core" but PUBLISHES to the embed/translate
	// subjects (which the publish group drains). A publisher must be
	// registered in the process that emits. So core also needs the two
	// publishers (without the subscribers).
	if cfg.StageGroup == "core" {
		initOpts = append(initOpts,
			frame.WithRegisterPublisher(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL),
			frame.WithRegisterPublisher(eventsv1.SubjectWorkerTranslate, cfg.WorkerTranslateQueueURL),
		)
	}

	svc.Init(ctx, initOpts...)
```

- [ ] **Step 2: Gate the reaper to the core group**

The reaper re-drives `ingested/normalized/validated/clustered` rows — all owned by core (plus normalized, which validate consumes, but the reaper just re-emits the stage event onto the stream; whichever group owns that topic picks it up). Running one reaper (in core) is correct and avoids three reapers triple-emitting. Change:

```go
	reaperCtx, stopReaper := context.WithCancel(ctx)
	defer stopReaper()
	go workersvc.NewReaper(svc, variantStore).Run(reaperCtx)
```

to:

```go
	// One reaper, in the core group only — three reapers would triple
	// re-emit every stuck row. core is always running, so this is a safe
	// single owner. (STAGE_GROUP=all also runs it for the monolith.)
	if cfg.StageGroup == "core" || cfg.StageGroup == "all" {
		reaperCtx, stopReaper := context.WithCancel(ctx)
		defer stopReaper()
		go workersvc.NewReaper(svc, variantStore).Run(reaperCtx)
	}
```

- [ ] **Step 3: Build + vet**

Run: `go build ./apps/worker/... && go vet ./apps/worker/...`
Expected: exit 0.

- [ ] **Step 4: Run the worker suite**

Run: `go test ./apps/worker/... -count=1 -short`
Expected: ok. (The `STAGE_GROUP` default is `all`, so existing tests exercise the monolith path unchanged.)

- [ ] **Step 5: Commit**

```bash
git add apps/worker/cmd/main.go
git commit -m "feat(worker): register handlers + subscribers per STAGE_GROUP"
```

---

### Task 4: Fix the JetStream stream subjects (embed/translate binding)

**Context:** the embed/translate publishers target `svc.opportunities.worker.embed.v1` / `.translate.v1`. The worker manifest's `EVENTS_QUEUE_URL` already declares `stream_subjects=svc.opportunities.events.>,svc.opportunities.worker.>`, but JetStream does **not** mutate an existing stream's subjects on reconnect, so the live `svc_opportunities_events` stream may still carry only `events.>` → "no response from stream" → dropped embeddings. This task makes the stream subjects converge.

**Files:** none in-repo — this is a one-time JetStream admin operation, scripted as a Kubernetes Job so it's auditable and repeatable.

- [ ] **Step 1: Confirm the drift**

Run a `nats-box` pod with admin creds (the worker creds are pub/sub-scoped and cannot do stream admin — use the cluster's NATS admin credential; ask the operator for the secret name, commonly `nats-sys-creds` or the operator account):

```bash
kubectl run nats-admin --rm -i --restart=Never -n product-opportunities \
  --image=natsio/nats-box:0.14.5 \
  --overrides='{"spec":{"serviceAccountName":"opportunities-worker","volumes":[{"name":"c","secret":{"secretName":"<NATS_ADMIN_CREDS_SECRET>"}}],"containers":[{"name":"n","image":"natsio/nats-box:0.14.5","stdin":true,"command":["sh","-c","nats -s nats://product-opportunities-queue-headless.product-opportunities.svc:4222 --creds /c/admin.creds stream info svc_opportunities_events --json | grep -A3 subjects"],"volumeMounts":[{"name":"c","mountPath":"/c","readOnly":true}]}]}}'
```
Expected if drifted: `subjects` lists only `svc.opportunities.events.>`.

- [ ] **Step 2: Add the subject (non-destructive)**

```bash
kubectl run nats-admin --rm -i --restart=Never -n product-opportunities \
  --image=natsio/nats-box:0.14.5 \
  --overrides='{...same as above but command:...}' \
  -- nats -s nats://product-opportunities-queue-headless.product-opportunities.svc:4222 \
     --creds /c/admin.creds stream edit svc_opportunities_events \
     --subjects "svc.opportunities.events.>,svc.opportunities.worker.>" --force
```
`stream edit --subjects` is additive/replace of the subject list only — it does **not** drop retained messages. Pass the FULL list (both trees) so the existing `events.>` binding is preserved.

- [ ] **Step 3: Verify embeddings flow**

After the edit, the canonical handler's `queue publish failed ... no response from stream` warnings stop. Confirm:
```bash
kubectl logs -n product-opportunities -l app.kubernetes.io/name=opportunities-worker --since=2m | grep -c "no response from stream"
```
Expected: 0 (was a steady flood).

- [ ] **Step 4: Record the operation**

This is infra state, not code — note it in the deployment repo's runbook (`docs/` or the namespace README) so a stream recreate re-applies the subjects:
```
svc_opportunities_events stream subjects MUST include svc.opportunities.worker.>
(embed/translate queue subjects). JetStream won't auto-add on reconnect.
```

---

### Task 5: Per-group queue-URL env values (the consumer identities)

No code change — this is the env wiring each Deployment carries. Document the exact `EVENTS_QUEUE_URL` per group so Tasks 6-8 paste them verbatim. Each differs ONLY in `consumer_durable_name` and `consumer_max_ack_pending`; all share the stream + the `svc.opportunities.events.>` filter (loose mode acks non-owned topics).

- **core** (`STAGE_GROUP=core`): high throughput, deterministic work → large inflight window.
  ```
  consumer_durable_name=worker-core-events   consumer_max_ack_pending=512   consumer_ack_wait=60s
  ```
- **validate** (`STAGE_GROUP=validate`): llama-bound, fail-open → modest inflight (don't pile llama calls).
  ```
  consumer_durable_name=worker-validate-events   consumer_max_ack_pending=64   consumer_ack_wait=120s
  ```
- **publish** (`STAGE_GROUP=publish`): R2 I/O, must not block anything → its own budget.
  ```
  consumer_durable_name=worker-publish-events   consumer_max_ack_pending=128   consumer_ack_wait=60s
  ```

Full URL template (substitute the three params above):
```
nats://product-opportunities-queue-headless.product-opportunities.svc.cluster.local:4222?jetstream=true&stream_name=svc_opportunities_events&stream_subjects=svc.opportunities.events.%3E%2Csvc.opportunities.worker.%3E&stream_retention=limits&stream_max_age=86400000000000&stream_storage=file&stream_num_replicas=1&consumer_durable_name=<NAME>&consumer_ack_policy=explicit&consumer_max_deliver=0&consumer_ack_wait=<WAIT>&consumer_max_ack_pending=<PENDING>&consumer_deliver_policy=all&subject=svc.opportunities.events.>
```

No commit — these values are consumed by Tasks 6-8.

---

### Task 6: `worker-core` Deployment manifest

**Files (deployments repo `git@github.com:stawi-org/deployment.manifests`):**
- Create: `namespaces/product-opportunities/worker-core/opportunities-worker-core.yaml`
- Create: `namespaces/product-opportunities/worker-core/kustomization.yaml`

- [ ] **Step 1: Copy the existing worker manifest as the base**

Start from `namespaces/product-opportunities/worker/opportunities-worker.yaml` (it has the full env block: DB, NATS, R2, Valkey, INFERENCE, EMBEDDING, OPPORTUNITY_KINDS_DIR, the embed/translate queue URLs, the nats-creds + kinds volumes). Change only:
- `metadata.name: opportunities-worker-core`
- `image` repository imagepolicy markers → keep pointing at `opportunities-worker` (same binary):
  `# {"$imagepolicy": "product-opportunities:opportunities-worker:name"}` and `:tag`.
- `serviceAccount.name: opportunities-worker-core` (or reuse `opportunities-worker` SA — reuse is fine; create a new one only if RBAC scoping matters).
- `opentelemetry.serviceName: opportunities-worker-core`.
- Add env `- name: STAGE_GROUP` / `value: "core"`.
- Replace `EVENTS_QUEUE_URL` value with the **core** URL from Task 5 (`consumer_durable_name=worker-core-events`, `consumer_max_ack_pending=512`).
- `autoscaling`: `minReplicas: 1`, `maxReplicas: 6`, `targetCPUUtilizationPercentage: 70` (core is the throughput tier — this is the cap we already proved out on the monolith).
- Keep the embed/translate `WORKER_EMBED_QUEUE_URL` / `WORKER_TRANSLATE_QUEUE_URL` env (CanonicalHandler publishes to them; main.go registers the publishers for core).

- [ ] **Step 2: kustomization.yaml**

```yaml
---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - opportunities-worker-core.yaml
```

- [ ] **Step 3: Validate**

Run: `python3 -c "import yaml; list(yaml.safe_load_all(open('namespaces/product-opportunities/worker-core/opportunities-worker-core.yaml')))" && kubectl kustomize namespaces/product-opportunities/worker-core | grep -E 'name: opportunities-worker-core|STAGE_GROUP|worker-core-events'`
Expected: prints the name, the STAGE_GROUP env, and the core durable name.

- [ ] **Step 4: Commit**

```bash
git add namespaces/product-opportunities/worker-core/
git commit -m "feat(worker-core): core consumer group (normalize/dedup/canonical), HPA 1-6"
```

---

### Task 7: `worker-validate` Deployment manifest

**Files:**
- Create: `namespaces/product-opportunities/worker-validate/opportunities-worker-validate.yaml`
- Create: `namespaces/product-opportunities/worker-validate/kustomization.yaml`

- [ ] **Step 1: Copy + adjust**

Same base as Task 6. Changes:
- `metadata.name: opportunities-worker-validate`, `opentelemetry.serviceName` likewise.
- env `STAGE_GROUP=validate`.
- `EVENTS_QUEUE_URL` → **validate** URL (`worker-validate-events`, `consumer_max_ack_pending=64`, `consumer_ack_wait=120s`).
- `autoscaling`: `minReplicas: 1`, `maxReplicas: 2`, `targetCPUUtilizationPercentage: 70`. **Capped at 2 — this is the only llama-bound group; the cap that used to constrain the whole worker now constrains only validate.** Document this in a comment referencing the llama HPA (max 6) headroom.
- Validate does NOT publish embed/translate and does NOT run the reaper (main.go already gates those). It still needs INFERENCE_* env (it calls llama) — keep it. It does NOT need R2_* for publishing, but the shared base includes R2 creds harmlessly; leave them to keep the manifest diff minimal.

- [ ] **Step 2: kustomization.yaml** (same shape as Task 6, resource `opportunities-worker-validate.yaml`).

- [ ] **Step 3: Validate** (same kustomize-build check, grep for `worker-validate-events` + `STAGE_GROUP`).

- [ ] **Step 4: Commit**

```bash
git add namespaces/product-opportunities/worker-validate/
git commit -m "feat(worker-validate): validate consumer group (llama-bound), HPA capped 1-2"
```

---

### Task 8: `worker-publish` Deployment manifest

**Files:**
- Create: `namespaces/product-opportunities/worker-publish/opportunities-worker-publish.yaml`
- Create: `namespaces/product-opportunities/worker-publish/kustomization.yaml`

- [ ] **Step 1: Copy + adjust**

Same base. Changes:
- `metadata.name: opportunities-worker-publish`, serviceName likewise.
- env `STAGE_GROUP=publish`.
- `EVENTS_QUEUE_URL` → **publish** URL (`worker-publish-events`, `consumer_max_ack_pending=128`).
- This group runs the embed + translate subscribers (main.go registers them for publish) → it needs INFERENCE_* (translate uses llama) + EMBEDDING_* (embed uses TEI) + the embed/translate `WORKER_*_QUEUE_URL` + R2_* (publish uploads snapshots). All are in the shared base — keep them.
- `autoscaling`: `minReplicas: 1`, `maxReplicas: 3`, `targetCPUUtilizationPercentage: 70`. Publish is I/O-bound (R2 + TEI); 3 is plenty and bounded by TEI capacity.

- [ ] **Step 2: kustomization.yaml** (resource `opportunities-worker-publish.yaml`).

- [ ] **Step 3: Validate** (kustomize-build, grep `worker-publish-events` + `STAGE_GROUP`).

- [ ] **Step 4: Commit**

```bash
git add namespaces/product-opportunities/worker-publish/
git commit -m "feat(worker-publish): publish+embed+translate group, isolated R2/TEI I/O, HPA 1-3"
```

---

### Task 9: Flux Kustomization CRs for the three groups

**Files:**
- Modify: `namespaces/product-opportunities/kustomization_provider.yaml`

- [ ] **Step 1: Append three Kustomization CRs**

After the existing `product-opportunities-worker` CR, append (mirroring its `dependsOn` + sourceRef shape):

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: product-opportunities-worker-core
spec:
  dependsOn:
    - name: product-opportunities-cluster
    - name: product-opportunities-queue
  interval: 15m
  timeout: 3m
  targetNamespace: product-opportunities
  path: ./namespaces/product-opportunities/worker-core
  prune: true
  wait: true
  sourceRef:
    kind: GitRepository
    name: flux-system
    namespace: flux-system
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: product-opportunities-worker-validate
spec:
  dependsOn:
    - name: product-opportunities-cluster
    - name: product-opportunities-queue
  interval: 15m
  timeout: 3m
  targetNamespace: product-opportunities
  path: ./namespaces/product-opportunities/worker-validate
  prune: true
  wait: true
  sourceRef:
    kind: GitRepository
    name: flux-system
    namespace: flux-system
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: product-opportunities-worker-publish
spec:
  dependsOn:
    - name: product-opportunities-cluster
    - name: product-opportunities-queue
  interval: 15m
  timeout: 3m
  targetNamespace: product-opportunities
  path: ./namespaces/product-opportunities/worker-publish
  prune: true
  wait: true
  sourceRef:
    kind: GitRepository
    name: flux-system
    namespace: flux-system
```

- [ ] **Step 2: Validate**

Run: `python3 -c "import yaml; list(yaml.safe_load_all(open('namespaces/product-opportunities/kustomization_provider.yaml')))"`
Expected: no exception.

- [ ] **Step 3: Commit**

```bash
git add namespaces/product-opportunities/kustomization_provider.yaml
git commit -m "feat(flux): Kustomization CRs for worker-core/-validate/-publish"
```

---

### Task 10: Release the binary, then deploy the groups alongside the monolith

This is the careful rollout: ship the new binary first (the monolith keeps running on `STAGE_GROUP=all` default — unchanged behaviour), then bring up the three groups, then retire the monolith. At no point is the pipeline unprocessed.

- [ ] **Step 1: Tag + build the worker image** (opportunities repo)

```bash
git -C /home/j/code/stawi.opportunities push origin main
git -C /home/j/code/stawi.opportunities tag v8.0.78
git -C /home/j/code/stawi.opportunities push origin v8.0.78
# wait for the Release workflow to publish opportunities-worker:v8.0.78
gh run watch "$(gh run list --workflow=release.yaml --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status
```

- [ ] **Step 2: Apply the stream-subjects fix (Task 4) if not already done.**

- [ ] **Step 3: Bring up the three groups** (deployments repo)

```bash
git -C /home/j/code/stawi.org/deployment.manifests push origin main
flux reconcile kustomization product-opportunities-setup --with-source   # creates the 3 new Kustomization CRs
for g in core validate publish; do
  flux reconcile kustomization product-opportunities-worker-$g -n product-opportunities --with-source
done
```

At this point BOTH the monolith (`worker-events` consumer) AND the three groups (`worker-core/validate/publish-events` consumers) are consuming the stream. Because they are *different durable consumers*, each gets its own copy of every event — so a variant would be processed by BOTH the monolith and a group → **double processing**. This is acceptable for at-most a few minutes because every stage is idempotent (AdvanceStage is a no-op past the target stage; UpsertOpportunity is an UPSERT; R2 PutObject is content-addressed). Immediately proceed to Step 4 to retire the monolith and end the overlap.

- [ ] **Step 4: Retire the monolith**

Edit `namespaces/product-opportunities/worker/opportunities-worker.yaml`: set `autoscaling.enabled: false` and `replicaCount: 0` (keep the manifest for one release as instant rollback). Commit + push + reconcile:

```bash
git -C /home/j/code/stawi.org/deployment.manifests commit -am "chore(worker): scale monolith to 0 — superseded by worker-core/-validate/-publish"
git -C /home/j/code/stawi.org/deployment.manifests push origin main
flux reconcile kustomization product-opportunities-worker -n product-opportunities --with-source
```

Verify the monolith's consumer stops advancing and the group consumers take over (Task 12).

---

### Task 11: Delete the monolith (follow-up release, after a clean day)

- [ ] **Step 1:** After 24h of healthy group operation (Task 12 green), delete `namespaces/product-opportunities/worker/` and its `product-opportunities-worker` Kustomization CR from `kustomization_provider.yaml`. Also delete the now-orphaned `worker-events` durable consumer via nats-box (`nats consumer rm svc_opportunities_events worker-events`) so it stops retaining acked-position state.
- [ ] **Step 2:** Commit: `chore(worker): remove retired monolith deployment + worker-events consumer`.

---

### Task 12: Verification

- [ ] **Step 1: Three consumers, independent, all advancing**

```bash
# Each group's pods are Running:
kubectl get pods -n product-opportunities | grep -E 'worker-(core|validate|publish)'
# Each group's HPA exists with its own bounds:
kubectl get hpa -n product-opportunities | grep -E 'worker-(core|validate|publish)'
```
Expected: core HPA max 6, validate max 2, publish max 3.

- [ ] **Step 2: Pipeline advances end-to-end** (Postgres ledger)

```bash
DBPOD=product-opportunities-db-1
kubectl exec -n product-opportunities $DBPOD -c postgres -- psql -U postgres -d opportunities -c \
 "SELECT current_stage, count(*) FILTER (WHERE stage_at > now()-interval '5 min') AS last_5m
  FROM pipeline_variants GROUP BY 1 ORDER BY 1;"
```
Expected: non-zero `last_5m` at `normalized`, `validated`, `clustered`, `canonical`, `published` — every stage flowing.

- [ ] **Step 3: Isolation holds — kill publish, confirm core keeps draining**

```bash
# Scale publish to 0 (simulate an R2/publish outage):
kubectl scale deploy opportunities-worker-publish -n product-opportunities --replicas=0
sleep 120
# canonical should KEEP advancing (core unaffected); published stops (expected):
kubectl exec -n product-opportunities $DBPOD -c postgres -- psql -U postgres -d opportunities -c \
 "SELECT current_stage, count(*) FILTER (WHERE stage_at > now()-interval '2 min') AS last_2m
  FROM pipeline_variants WHERE current_stage IN ('clustered','canonical','published') GROUP BY 1;"
# Restore:
kubectl scale deploy opportunities-worker-publish -n product-opportunities --replicas=1
```
Expected: `canonical` `last_2m` > 0 while publish is down (the OLD bug would have stalled it). `published` resumes after restore + the reaper re-drives the gap.

- [ ] **Step 4: core auto-scales under backlog**

Confirm `opportunities-worker-core` HPA scales above 1 when `validated` backlog builds, and back to 1 when drained:
```bash
kubectl get hpa opportunities-worker-core -n product-opportunities -w   # watch REPLICAS track load
```

---

## Plan exit criteria

- [ ] `HandlersForGroup` partitions the 5 pipeline topics across core/validate/publish exactly once (unit test green).
- [ ] Three Deployments run, each with its own durable consumer (`worker-{core,validate,publish}-events`) and its own HPA (max 6 / 2 / 3).
- [ ] Scaling `worker-publish` to 0 does **not** stall `clustered→canonical` (isolation proven) — the original cascade-stall is impossible.
- [ ] `worker-core` auto-scales on backlog and back down when idle.
- [ ] embed/translate `no response from stream` warnings are gone (Task 4 stream-subjects fix).
- [ ] The monolith `opportunities-worker` is scaled to 0 (Task 10) then deleted (Task 11); no double-processing in steady state.
- [ ] No stage can be permanently wedged: the core-group reaper re-drives stuck `ingested/normalized/validated/clustered`; publish failures stay contained and the publish consumer redelivers within its own `ack_pending` budget.

## Scope explicitly NOT in this plan
- Canonical-stage reaper re-drive (re-emitting `CanonicalUpsertedV1` with `canonical_id` for stuck `canonical` rows) — separate follow-up; needs the canonical_id plumbed from the ledger row.
- Per-stage DLQ tables (the audit's "max-deliver → DLQ"). The split + reaper cover recovery for now; a true DLQ is a later hardening.
- llama / TEI capacity changes (separate infra/cost decision).
- Crawl-rate vs. clustering-throughput balancing (rate-limit the crawler if `core` at max 6 still can't keep up).
