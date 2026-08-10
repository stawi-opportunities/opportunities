# Employer ATS (Jobs) — Design

**Date:** 2026-08-10  
**Status:** Draft for implementation  
**Product audience:** Service binary `apps/ats`, UI `ui/ats`; platform audience/permissions registered for ATS  
**Approach:** Dedicated ATS service + mobile-first SPA in the opportunities monorepo; maximum reuse of platform services under `~/code`

---

## 1. Goals and non-goals

### Goals

1. **Full employer ATS** that feels like a classic hiring tool (jobs, pipeline, interviews, AI assist, agent-operable API) with **minimal ceremony**.
2. **Hybrid talent:** Stawi matched candidates are a first-class **source** on the same pipeline objects — not a parallel product flow.
3. **Mobile-first, API-driven UI** — recruiters complete common tasks in few steps; AI assists aggressively (JD, screen summary, rank, schedule suggest, draft outreach).
4. **Agents operate the same stack as humans** via OpenAPI, using **user-delegated** identity tokens (no separate org service-account operators in v1).
5. **Reuse platform primitives** — tenancy (`tenant_id` / `partition_id`), identity, profile, files, notification, matching candidates, payment/ledger. Do not invent parallel CRM, RBAC, or org tables.
6. **Billing tied to results** — charge on hiring outcomes, not seats or “using the board.”

### Non-goals (v1)

- Two-way Google/Microsoft calendar sync (ICS export + built-in availability only).
- Standalone org service-account agents (user delegation only).
- Agency multi-client placement product (flat one employer = one partition).
- Replacing seeker-side `pkg/applications` tracking.
- Recreating profile/identity/tenancy/files/notification/payment.

---

## 2. Product decisions (locked)

| Decision | Choice |
|----------|--------|
| Product shape | Hybrid employer ATS; difference from pure employer ATS is **source + optional publish**, not a second workflow |
| v1 scope | Full surface: jobs, pipeline, interviews, Stawi source, AI, agent API, candidate self-serve (auth’d), comms, team via tenancy |
| RBAC | Identity + tenancy ReBAC; permission registration (e.g. `service_jobs`) |
| Jobs ↔ public board | **ATS-first**; optional **Publish to Stawi** projection |
| Interviews | Built-in availability → slot pick → ICS/email via notification |
| Candidate auth | Always **identity / Stawi login** (`sub === profile_id`) |
| Agents | **User-delegated** only |
| Workspace | Flat: **partition** under **tenant** = employer workspace; multi-partition membership via tenancy |
| Implementation | Dedicated `apps/ats` (service-profile layout: models/repository/business/handlers) + `ui/ats` SPA; Postgres only |
| Data tenancy fields | Frame `data.BaseModel` (`tenant_id`, `partition_id`, …) — **not** a custom `org_id` |
| People | **Profile service**; optional link to matching `candidate_profiles` / `candidate_id` |
| Billing | **Results-based** (e.g. Hired outcome) via payment/ledger |

---

## 3. Architecture

### 3.1 Planes

| Plane | Responsibility |
|-------|----------------|
| **Interaction** | `ui/ats` SPA; agents as OpenAPI clients; candidate slot-pick screens (authenticated) |
| **Control** | JWT claims (`profile_id`, `tenant_id`, `partition_id`); tenancy ReBAC; permission registration |
| **Execution** | `apps/ats`: jobs, applications, stages, interviews, availability, AI orchestration, publish/talent adapters, outbox intents |
| **Data** | ATS DB rows with Frame base model; no person PII store |
| **Integration** | Profile, files, matching, opportunities projection, notification, payment/ledger, LLM |

### 3.2 System context

```
Recruiter SPA / Agent
        │  Bearer JWT (user or user-delegated)
        ▼
   apps/ats  (OpenAPI; platform audience/permissions for ATS product)
        │
        ├── tenancy/identity  (authn claims, ReBAC check)
        ├── profile           (person, contacts)
        ├── files             (CV/attachment media ids)
        ├── matching          (talent shortlist, candidate product row)
        ├── opportunities     (publish projection)
        ├── notification      (email + ICS)
        ├── payment/ledger    (result charges)
        └── LLM               (AI assist)
```

### 3.3 Hard boundaries

1. **Seeker applications** (`pkg/applications`) remain seeker-side tracking. Employer pipeline is ATS-only.
2. **No ATS Person CRM table** — people are `profile_id` (+ optional `candidate_id` when on Stawi matching).
3. **No ATS org_id** — scope is always `tenant_id` + `partition_id` from claims / Frame base model.
4. **Publish is a projection** — job content SoT is ATS; public board is derived.
5. **Service identity** — binary is **`apps/ats`**; register platform audience/permissions for the ATS product (do not invent a parallel auth stack).

### 3.4 Relationship to opportunities monorepo

- New service binary and migrations live in this monorepo for product cohesion (matching, opportunities, publish).
- ATS models use **Frame `data.BaseModel`**, not `pkg/domain.BaseModel` (which intentionally omits tenant/partition for crawl data).
- Peer calls follow platform patterns (audiences, SA peer mesh for S2S; user SPA for human/agent).

---

## 4. Domain model (ATS-owned only)

All ATS entities embed Frame `data.BaseModel` → `tenant_id`, `partition_id`, audit fields. Queries always filter by claim partition.

### 4.1 Job

- Content: title, description, location, employment metadata as needed
- `status`: `draft` | `open` | `closed`
- `stage_template_id` (or inline ordered stages)
- `visibility`: `private` | `published`
- `opportunity_id` (nullable; set on successful publish)
- `published_at` (nullable)

### 4.2 StageTemplate

- Ordered stage keys/labels
- Default: Applied → Screen → Interview → Offer → Hired
- Terminals: Rejected, Withdrawn (and Hired as successful terminal)

### 4.3 Application

- `job_id`
- `profile_id` (required)
- `candidate_id` (optional; set when linked to matching seeker row)
- `stage` / `stage_id`
- `source`: `manual` | `upload` | `stawi_match` | `apply_form` | `agent`
- `source_ref` (optional opaque ref, e.g. match id)
- `status` / outcome: `active` | `rejected` | `withdrawn` | `hired`
- Optional score/summary cache from AI (not a second profile)

**Invariant:** at most one **active** application per `(partition_id, job_id, profile_id)`.

### 4.4 StageEvent

Append-only: `application_id`, `from_stage`, `to_stage`, `actor_profile_id`, `at`, optional note. No silent stage overwrites.

### 4.5 Availability

- `profile_id` (interviewer)
- Timezone, weekly rules, date exceptions
- Partition-scoped

### 4.6 Interview

- `application_id`
- `type`, `duration_min`
- `panel` = list of `profile_id`
- `status`: `proposed` | `scheduled` | `completed` | `canceled` | `no_show`
- `slot_start` / `slot_end` when scheduled
- `ics_uid`
- Location or video URL (string; no Meet API requirement in v1)

**Invariant:** interview always belongs to an application.

### 4.7 Outbox / AI audit

- Outbox for notification intents (idempotent delivery)
- `AiRun` (or equivalent audit): purpose, input hash, output ref, actor — only if not covered by an existing platform audit path

### 4.8 Explicit non-entities

| Do not create | Use instead |
|---------------|-------------|
| `org_id`, MemberRef | `tenant_id`, `partition_id`, tenancy Access/roles |
| Person name/email/phone tables | Profile + contacts |
| Parallel candidate CRM | matching `candidate_profiles` + profile |
| Custom RBAC tables | Identity + tenancy ReBAC + permission registration |
| Seat subscription engine in ATS | Payment/ledger on **Hired** (results) |

---

## 5. API surface

### 5.1 Auth context

Every request:

- `Authorization: Bearer <access_token>`
- Actor: `profile_id` (`sub`)
- Scope: `tenant_id`, `partition_id`
- Agents: **same user JWT** via delegation; actor remains the human `profile_id`
- Missing partition → 403; cross-partition resource → 404

### 5.2 Resource groups

| Area | Routes (illustrative) |
|------|------------------------|
| Jobs | `GET/POST /v1/jobs`, `GET/PATCH /v1/jobs/{id}`, `POST …/publish`, `…/unpublish`, `…/close` |
| Applications | `GET/POST /v1/jobs/{id}/applications`, `GET /v1/applications/{id}`, `POST …/advance`, `POST …/hire` |
| Interviews | `GET/POST /v1/applications/{id}/interviews`, `GET …/slots`, `POST …/book`, cancel/complete |
| Availability | `GET/PUT /v1/me/availability` |
| Talent | `GET /v1/jobs/{id}/talent`, add-to-pipeline action |
| AI | `/v1/ai/…` screen summary, rank, draft, schedule suggest |

### 5.3 Contract rules

1. OpenAPI 3 is the single contract for UI and agents.
2. Errors: `problem+json` with type, title, detail, `correlation_id`.
3. `Idempotency-Key` required (or strongly enforced) on: book, advance, publish, unpublish, hire, application create.
4. Cursor pagination; filters by stage, source, status.
5. Prefer action POSTs over ambiguous multi-purpose PATCH.
6. Register permissions via platform permission-registration pattern (namespace e.g. `service_jobs`).

### 5.4 Not implemented in ATS API

Profile CRUD, login, file byte upload, seeker CV/preferences writes, SMTP, checkout session creation UI — call peer services.

---

## 6. Core flows

### 6.1 Create job and hire (minimal path)

1. Recruiter creates job (optional AI JD assist) → `draft`/`open`.
2. Add applicants: manual profile link, upload→profile/files, or Stawi talent add.
3. Advance stages (events recorded).
4. Schedule interview (see 6.2).
5. Advance to Hired → **results billing event** (idempotent).

### 6.2 Interview scheduling

1. Panelists maintain `/v1/me/availability`.
2. Propose interview: type, duration, panel → `proposed`; compute free slots (intersect rules − exceptions − existing interviews).
3. Candidate authenticates; lists slots; books one → `scheduled`; `ics_uid` assigned.
4. Notification delivers email + ICS to candidate and panel.
5. Complete / no_show / cancel. Reschedule = cancel + new propose/book.

**Slot algorithm (v1):** no external calendar. Fixed duration grid. Book conflict → 409 + refreshed slots. Empty panel availability → 422 naming empty `profile_id`s. Notify failure → outbox retry; ATS row remains SoT.

### 6.3 Publish to Stawi

1. `POST …/publish` validates job content completeness.
2. Project to opportunities (existing writer/materializer patterns as applicable).
3. Persist `opportunity_id`, `visibility=published`.
4. Unpublish removes/hides listing; pipeline history unchanged.
5. Content edits require re-publish to refresh projection (no dual silent editors).

### 6.4 Stawi talent source

1. `GET …/talent` queries matching using job text/embedding.
2. Add creates Application with `source=stawi_match`, `profile_id`, optional `candidate_id` / match ref.
3. Identical pipeline and interview path thereafter.

### 6.5 AI assist

Inline tools: improve JD, screen summary, rank shortlist, draft outreach, suggest panel/duration/slots. Every run audited; never invents pipeline state without an explicit API write.

### 6.6 Results billing

- Trigger: application reaches **Hired** (or explicit hire action), idempotent per application.
- Emit billable result to payment/ledger (product SKU defined at implementation; principle is outcome-not-seat).
- Pipeline, scheduling, and AI usage are not seat-metered in this design.
- Double hire / retry must not double-charge (idempotency key = application id + outcome).

---

## 7. UI design

### 7.1 Recruiter SPA (`ui/ats` / jobs)

Mobile-first bottom navigation:

| Tab | Role |
|-----|------|
| Jobs | List, create, detail, Publish toggle |
| Pipeline | Job-scoped stages; card → profile summary + advance + schedule |
| Today | Interviews and follow-ups |
| More | Availability, partition switch, team (tenancy), outcome billing |

- Stack: Vite/React aligned with existing product SPAs; platform UI tokens where available.
- No server-side business logic in the SPA — OpenAPI client only.
- AI actions are contextual on job/application screens.

### 7.2 Candidate surfaces

- Authenticated slot selection and light application/interview status.
- Not the seeker dashboard (`ui/app`); deep links into jobs candidate routes after identity login.

---

## 8. Security model

| Concern | Mechanism |
|---------|-----------|
| Authentication | OIDC JWT; `sub === profile_id` |
| Tenancy | Claims `tenant_id` / `partition_id`; row-level filter always |
| Authorization | Tenancy ReBAC + registered method permissions |
| Agents | User-delegated tokens only; audit actor = human `profile_id` |
| Candidate access | Only applications/interviews where they are the `profile_id` participant |
| IDOR | Cross-partition → 404 |
| Idempotency | Side-effecting POSTs |

---

## 9. Observability and operations

- OpenTelemetry on HTTP handlers, peer calls, outbox processing.
- Structured logs: `correlation_id`, `profile_id`, `tenant_id`, `partition_id`, resource ids.
- Metrics: book conflict rate, outbox lag, publish success/fail, hire billing emit success.
- Deploy: ATS binary + static SPA; migrations isolated from crawl/matching schemas.

---

## 10. Testing strategy

| Layer | What |
|-------|------|
| Domain | Stage transitions, slot intersection, hire idempotency, one-active-application invariant |
| API | Authz matrix (roles), partition isolation, problem+json, Idempotency-Key replay |
| Integration | Publish projection contract; talent add → application; notification outbox; billing emit on hire |
| Concurrency | Double-book race; double-hire billing |
| UI | Mobile flows: create job, advance, schedule, candidate pick slot |

Prefer real fixtures for tenancy claims over mocks where platform test harnesses exist.

---

## 11. Failure and recovery

| Risk | Mitigation |
|------|------------|
| Notification flaky | Outbox + retry; interview row is SoT |
| Matching unavailable | Talent endpoint returns empty/degraded, not hard-down for whole ATS |
| Double hire / charge | Idempotent hire + billing key |
| Publish partial failure | Job stays private until projection confirms; clear error |
| Calendar drift (no sync) | Document ICS-only v1; optional calendar later without domain rewrite |

**Robustness gate — top 3 production failures:**

1. **Outbox/notify lag** — monitor lag; recruiter UI shows “scheduled” from ATS even if email delayed.  
2. **Partition misconfiguration** — fail closed (403); onboarding checklist with tenancy.  
3. **Hire billing duplicate** — DB unique / idempotency store on outcome event.

---

## 12. Extensibility

- Calendar connect later: subtract busy from slot compute; still keep built-in availability as baseline.
- Service-account org agents later: same API, different claim shape — do not bake user-only assumptions into domain, only into v1 auth policy.
- Teams/departments later: tenancy hierarchy or partition properties — not ATS-side org trees in v1.

---

## 13. Key decisions

1. **Dedicated Jobs/ATS service** over extending seeker `applications` — clean tenancy and product boundary.  
2. **Reuse tenancy/identity/profile/matching/files/notification/payment** — ATS owns only hiring workflow state.  
3. **`tenant_id`/`partition_id` only** — no `org_id`.  
4. **Person = profile_id** — no CRM duplicate.  
5. **ATS-first publish** — optional projection to opportunities.  
6. **Stawi talent = application source** — zero ceremony delta.  
7. **Built-in slots + ICS** — ship complete scheduling without OAuth calendars.  
8. **Candidates always identity login** — single identity graph.  
9. **User-delegated agents** — same permissions and audit as the human.  
10. **Results-based billing** on Hired — not seats.  
11. **Binary `apps/ats`** — wire platform audiences/permissions for this product.

---

## 14. Open questions

Resolved during brainstorming; none blocking. Implementation may refine:

- Exact payment SKU and price for hire outcome (product/ops).  
- Opportunity projection mechanism (direct write vs event to existing writer) — choose during PR1 integration spike against current opportunities APIs.  
- Platform catalog slug (`ServiceJobs` vs new `ServiceAts`) for audience URL — decide at deploy wiring; code path is `apps/ats`.

---

## 15. PR Plan

Incremental, each PR independently reviewable. Work on an isolated worktree/branch.

| PR | Title | Scope | Depends on |
|----|-------|--------|------------|
| **PR1** | ATS service skeleton + tenancy-scoped Job CRUD | `apps/ats` boot, Frame base model, migrations, OpenAPI job CRUD, JWT partition enforcement, permission registration stub | — |
| **PR2** | Applications + stage events | Application model, advance, invariants, list/filter, idempotency | PR1 |
| **PR3** | Availability + interviews | Slot compute, book/cancel, conflict tests, outbox → notification ICS | PR2 |
| **PR4** | Stawi talent source | Matching client, talent list, add `source=stawi_match` | PR2 |
| **PR5** | Publish / unpublish projection | Opportunities integration, `opportunity_id`, failure handling | PR1 |
| **PR6** | AI assist endpoints | Screen/rank/draft/schedule suggest + audit | PR2 |
| **PR7** | Results billing on Hired | Payment/ledger emit, idempotent hire | PR2 |
| **PR8** | Recruiter SPA mobile shell | `ui/ats` auth, jobs list/create, pipeline board, today, availability | PR1–3 (can start mock against OpenAPI) |
| **PR9** | Candidate slot-pick UI + polish | Authenticated deep links, end-to-end schedule path | PR3, PR8 |
| **PR10** | Agent contract + docs | OpenAPI examples, idempotency guide, ops runbook, catalog/deploy wiring | PR1–7 |

---

## 16. Implementation notes for engineers

- Prefer library-first: Frame interceptors, common audit, servicecatalog audiences, existing matching/profile clients.  
- Do not copy seeker `pkg/applications` state machine into employer ATS; define employer stages as ATS domain (defaults above).  
- When linking Stawi seekers, resolve `profile_id` from candidate row; never treat `candidate_profiles.id` as JWT subject.  
- Billing activation for employers is outcome events — do not port seeker subscription checkout into ATS UI as the primary model.

---

## Document history

- 2026-08-10: Initial design from brainstorming (product choices + reuse constraints).
