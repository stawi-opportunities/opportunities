# Design: Platform Chat-Agent Service

**Date:** 2026-07-29  
**Status:** Accepted (design review)  
**Primary consumer (first):** Opportunities matching — `POST /me/chat` placement intake  
**Related systems:** `chat-drone` / `chat-gateway` (real-time messaging — **out of scope**), matching placement rebuild, files service (CV blobs)

---

## Problem

Placement intake chat is embedded in the matching service (`apps/matching/service/http/v1/me_chat.go`, ~1.7k lines). The system prompt, field schema, readiness rules, session persistence, LLM turn loop, and placement rebuild are tightly coupled to Stawi job-seeker onboarding.

That prevents reuse: another product or tenancy must fork the handler or re-ship matching to change conversational behavior. The desired model is a **platform service** where products reuse the engine and **only update prompt + field schema (context config)** when tenancy or domain context changes.

Real-time room messaging already exists as `chat-drone` / `chat-gateway` (`stawilabs/chat`). This design is for **structured AI intake / agent turns**, not Matrix-style messaging.

---

## Goals

1. Extract multi-turn structured chat into a **new platform service** (`chat-agent`).
2. Configure behavior via **prompt + field schema + readiness** (per tenant/context).
3. **Chat-agent owns sessions** (transcript + extracted fields).
4. Domain side effects (e.g. placement rebuild) via **events**, not chat-agent domain imports.
5. Matching SPA contract for `POST /me/chat` remains stable during migration.
6. Changing tenancy/copy for placement intake does not require forking chat code.

## Non-goals

- Replacing or embedding into `chat-drone` / `chat-gateway`.
- Streaming token responses (v1 is request/response).
- Owning CV file storage (files service remains source of blobs).
- Embedding job vectors or running matching (stays in matching / `pkg/placement`).
- Product-specific invent-rules shipped as core platform heuristics long-term.

---

## Approach (chosen)

**Hybrid C:** context **registry** is the production source of truth; **inline config** (or field-level overrides) is allowed at session create for migration, tests, and one-offs. Sessions snapshot resolved config. Turns are session-scoped. Domain consumers react to NATS events.

### Alternatives considered

| Option | Summary | Why not chosen |
|--------|---------|----------------|
| A — Inline only | Full config on every `CreateSession` | Weak multi-tenant ops/audit; every product redeploy to change prompts |
| B — Registry only | Only `context_id` at session create | Slower matching migration; less flexible for tests/one-offs |
| Library-only extract | Shared Go pkg, no new deployable | Does not give platform reuse across products or independent scaling |
| Extend chat-drone | AI bots inside messaging | Wrong domain boundary; couples intake to room delivery |

---

## Architecture

```
┌─────────────┐     CreateSession / Turn      ┌──────────────────────┐
│  Matching   │ ────────────────────────────► │  chat-agent service  │
│  (or other) │ ◄──────────────────────────── │                      │
└──────┬──────┘   fields, ready, messages     │  • context registry  │
       │                                      │  • sessions + turns  │
       │  NATS: turn_completed / ready        │  • LLM complete      │
       ▼                                      │  • readiness eval    │
  placement.Rebuild                           └──────────┬───────────┘
  (matching)                                      PostgreSQL + NATS
```

### Service identity

| Item | Value |
|------|--------|
| Catalog ID | `chat-agent` (`servicecatalog.ServiceChatAgent`) |
| Audience path | `/chat-agent` |
| Stack | Go, Frame, Connect RPC, PostgreSQL, NATS |
| **Implementation home** | `antinvestor/service-profile` → **`apps/chatagent`** |
| Distinct from | `chat-drone`, `chat-gateway` |

### Product model (evidence-first tool)

Chat agent is **not** a free-form chatbot. It is a tool that, given a context
definition (required fields + purpose), collects missing data through
conversation while **always re-evaluating evidence already in the system**:

- seed fields (prior draft / profile properties)
- documents (CV text, uploads already extracted)
- prior conversation turns
- structured inputs this turn

Products only change the **context**. They do not fork the engine.

### Boundaries

| chat-agent owns | Consumer owns |
|-----------------|---------------|
| Context definitions (prompt, schema, readiness) | Domain meaning of fields (placement profile) |
| Session transcript + field JSON | Auth of end-user (JWT/candidate) |
| LLM call + extract parse + schema sanitize | Product HTTP surface for SPA |
| Ready/missing computation from config | Side effects (embed, billing gates) |
| Session-scoped events | Subscription handlers |

**Hard rule:** chat-agent must not import `pkg/placement`, matching models, or opportunity domain packages.

---

## Core concepts

### Context definition

Reusable configuration unit:

- `tenant_id`, `context_key` (e.g. `stawi.placement.intake`), `version`
- `system_prompt` — template; may reference runtime vars (e.g. `{{.Runtime.cv_text}}` injected carefully as data, not instructions)
- `fields[]` — name, type, required, priority, optional enum, description, max length
- `reply_policy` — e.g. max sentences, “ask only highest-priority missing”
- `extract_rules_notes` — free text appended to the extract prompt (product-specific rules without code)

### Session

- Identity: `(tenant_id, subject_id, session_id)`
- Holds **config snapshot** (immutable for session lifetime)
- `fields` JSON, `ready`, `status` (`active` | `ready` | `ended`)
- `runtime` map re-applied each turn (CV text, locale, page context)
- Transcript as ordered messages

### Turn

One user message (+ optional structured inputs) → extract → merge → validate → assess ready → assistant reply → persist → events.

---

## API (Connect RPC v1)

| RPC | Purpose |
|-----|---------|
| `UpsertContext` | Create/update context definition; returns version |
| `GetContext` | By tenant + key (+ optional version) |
| `ListContexts` | Tenant listing for ops |
| `CreateSession` | Start session from `context_ref` and/or `inline_config` + subject + runtime + optional seed |
| `GetSession` | Full session state for resume UI |
| `Turn` | Conversational turn |
| `EndSession` | Mark ended (idempotent) |

### CreateSession resolution (hybrid)

1. If `context_ref` present → load registry (latest or pinned version).
2. If `inline_config` present → use as base, or **field-level override** of registry (non-empty fields win).
3. Snapshot **resolved** definition onto the session.
4. Store `runtime` separately; re-inject each turn (not frozen into system prompt only once).

Precedence: inline non-empty fields > registry > service defaults (reply policy only).

### Turn request / response (conceptual)

**Request:** `session_id`, `message`, optional structured bag (`linkedin`, `cv_text`, `cv_filename`, extra), optional client history (server is source of truth; merge policy prefers longer sanitized server transcript, client only fills gaps).

**Response:** `reply`, `fields`, `missing[]` (priority order), `ready`, `field_status`, `messages[]`, `source` (`llm` | `heuristic` | `llm+heuristic`), `session_version`.

### Auth

- Service-to-service OAuth2: matching client → chat-agent audience (`ServiceChatAgent`).
- End-user auth stays on product edge (matching JWT). Matching passes `subject_id` = candidate id.
- Tenant isolation enforced on every RPC; session access limited to owning tenant + subject.

**Platform peer mesh (normative):** see
`service-authentication` ADR 0002
(`docs/adr/0002-product-peer-mesh-not-per-tenant-grants.md`) and
`docs/ops/chat-agent-integration.md`.

- Chat-agent authorization is on the **matching service account** (recipients +
  `service_chat_agent` grants), not on each tenant or candidate.
- New customers need product access only (SPA → matching + partition membership).
  **Never** per-customer chat-agent migrations or SA grants.
- Enabling the edge requires **three gates**: deploy requested audience, Hydra
  `oauth_client_recipients`, SA ReBAC permissions. Deploy env alone is insufficient.

---

## Data model (PostgreSQL)

| Table | Role |
|-------|------|
| `chat_contexts` | tenant_id, context_key, version, definition JSONB, active, timestamps |
| `chat_sessions` | id, tenant_id, subject_id, context_key, config_snapshot JSONB, fields JSONB, runtime JSONB, status, ready, timestamps |
| `chat_messages` | session_id, seq, role, content, created_at |
| `chat_turns` (optional audit) | session_id, source, latency_ms, model, error |

Indexes: `(tenant_id, subject_id, status)`, `(tenant_id, context_key, version)` unique active, messages `(session_id, seq)`.

Retention: configurable TTL after `EndSession` (ops detail; not blocking v1).

---

## Events (NATS)

| Event | When | Minimal payload |
|-------|------|-----------------|
| `chat.session.created` | CreateSession | session_id, tenant_id, subject_id, context_key |
| `chat.session.turn_completed` | After successful Turn | session_id, tenant_id, subject_id, fields, missing, ready, source |
| `chat.session.ready` | First transition to ready=true | session_id, tenant_id, subject_id, fields |
| `chat.session.ended` | EndSession | session_id, reason |

Matching (and future consumers) subscribe and run domain logic.  
SPA path uses synchronous Turn response for UX; events enable async workers and multi-service listeners.

**Event reliability (v1):** publish after persist; on publish failure log + metric; optional outbox table in a follow-up if at-least-once is required for placement.

---

## Turn pipeline

1. Load session; serialize concurrent turns (row/advisory lock per session).
2. Merge structured inputs into fields.
3. Build extract prompt: system prompt + schema + existing fields + missing guide + history + runtime data block.
4. LLM complete → parse JSON `{ fields, reply }` (no markdown fences).
5. Schema-validate / sanitize (types, enums, max lengths). **Never invent required identity fields.**
6. Optional **generic** fill only where config allows; product-specific heuristics remain in consumer adapters until generalized.
7. Assess readiness from required field priority order in config.
8. Compose reply: prefer model reply; **override** if model claims complete while `missing` non-empty.
9. Persist messages + fields; emit events.

**Ready is always server-computed from validated fields — never trusted from the LLM.**

### Heuristics placement

| Kind | Location |
|------|----------|
| Schema sanitize, empty strip, enum normalize | chat-agent |
| Job-title invent guards, job-type remote→Full-time, country ISO maps | matching adapter initially; migrate into `extract_rules_notes` / field metadata when stable |

---

## Matching integration

### Thin adapter for `POST /me/chat`

1. Authenticate candidate (existing middleware).
2. Resolve or create chat-agent session for `(tenant, subject, context_key=stawi.placement.intake)`.
3. Call `Turn` with message + CV/LinkedIn structured inputs.
4. Map response to existing SPA JSON shape (`reply`, `fields`, `missing`, `ready`, `field_status`, `messages`, `source`, placement summary fields).
5. **Migration window:** continue synchronous `placement.Rebuild` in the adapter after Turn until event consumer is proven.
6. **Target:** rebuild only from `chat.session.turn_completed` / `chat.session.ready`.

### Feature flags

- `CHAT_AGENT_URI` / `CHAT_AGENT_SERVICE_URI` — Connect base URL (required)  
- `CHAT_AGENT_ENABLED` — must be true; no local `MeChatHandler` fallback (503/502 on failure)  
- Optional shadow mode: dual-call and log field/ready diffs

### Prompt migration

1. Port current placement system prompt + field list into a registered context (or inline at CreateSession).
2. Register as production context under Stawi tenant.
3. Remove hard-coded prompt from matching after soak.

---

## Security

- Tenant + subject isolation on all reads/writes.
- Input limits (aligned with current matching): message length, history clamp, CV text max, max messages per session.
- User/history content treated as untrusted data in prompts.
- PII in transcripts (CV text): rely on platform encryption-at-rest defaults; retention TTL configurable.
- Rate limit turns per subject (config) to control LLM cost.

---

## Error handling

| Case | Behavior |
|------|----------|
| LLM timeout / 5xx | Fall back to best-effort field merge without inventing; return turn with `source=heuristic` when possible; else retryable 503 |
| Unknown session | 404 |
| Invalid UpsertContext schema | 400 with field paths |
| Concurrent turns | Serialized per session |
| Event publish fail after persist | Log + metric; Turn still succeeds (v1) |

---

## Testing strategy

- **Unit:** prompt assembly, merge/sanitize, readiness order, false-ready reply override.
- **Integration (testcontainers):** PG + NATS; CreateSession → Turn → GetSession; context versioning; event emission.
- **Adapter contract:** matching `POST /me/chat` JSON parity for SPA.
- **Connect contract fixtures** for Turn request/response.

Success criteria:

1. A second product can start structured intake with only a new context definition (or inline config).
2. Placement copy/tenancy text changes via context upsert without forking chat code.
3. SPA contract preserved.
4. Placement rebuild still occurs (sync then event-driven).
5. chat-agent has zero matching/placement domain imports.

---

## Rollout phases

| Phase | Deliverable |
|-------|-------------|
| P0 | Scaffold service (Frame app, proto, migrations, health, catalog) |
| P1 | Registry + CreateSession/GetSession/Turn + session store + LLM |
| P2 | NATS events |
| P3 | Matching adapter + feature flag; sync placement rebuild in adapter |
| P4 | Event-driven placement rebuild |
| P5 | Production registered context; prompt out of matching binary |
| P6 | Delete dead MeChat extract/prompt code; keep adapter + mapping only |

---

## Key decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Service location | New platform service `chat-agent` | Cross-product reuse; independent deploy/scale |
| vs messaging | Separate from chat-drone | Different domain (intake vs rooms) |
| Config model | Hybrid registry + inline overrides | Ops-friendly prompts + fast migration/tests |
| Session ownership | chat-agent | Resume, multi-client, single transcript source of truth |
| Domain side effects | Events (+ sync adapter during cutover) | Keeps chat domain-agnostic |
| Ready authority | Server schema evaluation | Prevents LLM false completion |
| Heuristics | Generic in service; product-specific in adapter first | Avoid baking Stawi job rules into platform core |

---

## Open questions (defaults applied)

| Question | Default if unresolved |
|----------|----------------------|
| Exact GitHub org/repo name | `antinvestor/service-chat-agent` |
| Multi-region session affinity | Single region v1 |
| Streaming replies | Deferred |
| Outbox for events | Deferred until loss observed |
| Context admin UI | API-only v1 |

---

## PR Plan

### PR1 — Scaffold chat-agent service
- New repo (or monorepo package) with Frame `apps/default`, config, health, Dockerfile, Makefile  
- Proto module: Context + Session + Turn stubs  
- Catalog / audience registration (`chat-agent`)  
- Empty migrations bootstrap  
- **Depends on:** none  

### PR2 — Persistence + context registry
- Tables: `chat_contexts`, `chat_sessions`, `chat_messages`  
- `UpsertContext` / `GetContext` / `ListContexts`  
- Repository tests with testcontainers  
- **Depends on:** PR1  

### PR3 — Session + Turn engine
- `CreateSession` (hybrid resolve + snapshot)  
- `Turn` pipeline with LLM interface, schema validate, ready assessment  
- `GetSession`, `EndSession`  
- Unit tests for extract/merge/ready/reply override  
- **Depends on:** PR2  

### PR4 — Events
- Emit `created`, `turn_completed`, `ready`, `ended` via Frame/NATS  
- Integration tests asserting event payload  
- **Depends on:** PR3  

### PR5 — Matching adapter (feature-flagged)
- Client in `pkg/services` for chat-agent  
- `POST /me/chat` dual path: legacy vs adapter  
- Sync placement rebuild after Turn  
- Adapter tests for SPA JSON shape  
- **Depends on:** PR3 (PR4 optional for this PR)  

### PR6 — Event-driven placement rebuild
- Matching subscriber for `turn_completed` / `ready`  
- Flag to disable sync rebuild  
- Soak metrics  
- **Depends on:** PR4, PR5  

### PR7 — Production context + cleanup
- Register `stawi.placement.intake` context (prompt + fields from current MeChat)  
- Remove hard-coded prompt/extract from matching  
- Docs: api-reference, ops notes  
- **Depends on:** PR5–PR6  

---

## Implementation notes for first context (Stawi placement)

Port from current `chatExtractWithLLM` / `requiredChatFieldOrder`:

**Required fields (priority):**  
`target_job_title`, `capabilities` (extra_info / CV), `job_types`, `salary_expectation`, `preferred_countries`, `experience_level`

**Optional:** LinkedIn, search status, languages, regions (safe defaults only where non-inventing)

**Reply policy:** 1–3 short sentences; ask single highest-priority missing item; never claim ready if missing non-empty.

This content lives in the context definition, not Go source, after PR7.
