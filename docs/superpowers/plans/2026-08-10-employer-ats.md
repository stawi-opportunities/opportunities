# Employer ATS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a tenancy-scoped employer ATS (`apps/ats` + `pkg/ats` + `ui/ats`) with jobs, pipeline, interviews, Stawi talent source hooks, AI stubs, results billing hook, and agent-operable HTTP API — reusing identity/tenancy/profile/matching/notification/payment.

**Architecture:** Domain and store in `pkg/ats` using Frame `data.BaseModel` (`tenant_id`, `partition_id`). HTTP API in `apps/ats` with JWT (or dev headers) injecting claims. People are `profile_id` only. Seeker `pkg/applications` untouched.

**Tech Stack:** Go 1.26, Frame v2, GORM/Postgres, stdlib `http.ServeMux`, problem+json, existing monorepo module `github.com/stawi-opportunities/opportunities`.

**Spec:** `docs/superpowers/specs/2026-08-10-employer-ats-design.md`

---

## File map

| Path | Responsibility |
|------|----------------|
| `pkg/ats/models.go` | Job, Application, StageEvent, Availability, Interview, Outbox, HireOutcome |
| `pkg/ats/stages.go` | Default stages, ValidateAdvance |
| `pkg/ats/slots.go` | Availability intersection / book conflict helpers |
| `pkg/ats/store.go` | GORM store: CRUD, partition filters, invariants |
| `pkg/ats/service.go` | Business orchestration (create job, advance, propose/book, hire) |
| `pkg/ats/*_test.go` | Domain + store + service tests |
| `apps/ats/config/config.go` | HTTP addr, auth flags |
| `apps/ats/cmd/main.go` | Frame boot, migrate schema, mount routes |
| `apps/ats/service/http/v1/*` | Auth, router, handlers |
| `ui/ats/*` | Mobile-first SPA (later tasks) |

---

### Task 1: Domain models + stage machine

**Files:**
- Create: `pkg/ats/models.go`
- Create: `pkg/ats/stages.go`
- Create: `pkg/ats/stages_test.go`

- [ ] **Step 1: Write stage tests**

```go
func TestValidateAdvance_happy(t *testing.T) {
	if err := ValidateAdvance(StageApplied, StageScreen); err != nil {
		t.Fatal(err)
	}
}
func TestValidateAdvance_skipRejected(t *testing.T) {
	if err := ValidateAdvance(StageApplied, StageRejected); err != nil {
		t.Fatal(err)
	}
}
func TestValidateAdvance_illegal(t *testing.T) {
	if err := ValidateAdvance(StageApplied, StageOffer); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Implement models + stages** (Frame `data.BaseModel`, default stages Applied→Screen→Interview→Offer→Hired, terminals Rejected/Withdrawn/Hired)

- [ ] **Step 3: Run tests**

```bash
go test ./pkg/ats/ -count=1 -run TestValidateAdvance
```

- [ ] **Step 4: Commit**

```bash
git add pkg/ats/models.go pkg/ats/stages.go pkg/ats/stages_test.go
git commit -m "feat(ats): domain models and stage transition rules"
```

---

### Task 2: Slot computation

**Files:**
- Create: `pkg/ats/slots.go`
- Create: `pkg/ats/slots_test.go`

- [ ] **Step 1: Tests for weekly intersection, exceptions, existing interviews blocking slots**
- [ ] **Step 2: Implement `ComputeSlots` and `SlotOverlaps`**
- [ ] **Step 3: `go test ./pkg/ats/ -count=1 -run Slot`**
- [ ] **Step 4: Commit** `feat(ats): interview slot intersection`

---

### Task 3: Store + service (jobs, applications, advance, hire)

**Files:**
- Create: `pkg/ats/store.go`
- Create: `pkg/ats/service.go`
- Create: `pkg/ats/store_test.go`
- Create: `pkg/ats/service_test.go`

- [ ] **Step 1: Tests with sqlite/gorm** — create job under claims context; list filtered by partition; one active application per (job, profile); advance emits StageEvent; hire idempotent
- [ ] **Step 2: Implement store using GORM AutoMigrate of Schema()**
- [ ] **Step 3: Implement Service methods: CreateJob, ListJobs, GetJob, UpdateJob, Publish/Unpublish fields, CreateApplication, Advance, Hire**
- [ ] **Step 4: `go test ./pkg/ats/ -count=1`**
- [ ] **Step 5: Commit** `feat(ats): store and business service for jobs and pipeline`

---

### Task 4: Interviews + availability in service

**Files:**
- Modify: `pkg/ats/service.go`, `pkg/ats/store.go`
- Create: `pkg/ats/interview_test.go`

- [ ] **Step 1: Tests for SetAvailability, ProposeInterview, BookInterview conflict 409-equivalent error**
- [ ] **Step 2: Implement**
- [ ] **Step 3: `go test ./pkg/ats/ -count=1`**
- [ ] **Step 4: Commit** `feat(ats): availability and interview booking`

---

### Task 5: HTTP API `apps/ats`

**Files:**
- Create: `apps/ats/config/config.go`
- Create: `apps/ats/cmd/main.go`
- Create: `apps/ats/service/http/v1/auth.go`
- Create: `apps/ats/service/http/v1/router.go`
- Create: `apps/ats/service/http/v1/handlers.go`
- Create: `apps/ats/service/http/v1/handlers_test.go`

- [ ] **Step 1: Auth middleware** — JWT via Frame authenticator; dev headers X-Profile-ID, X-Tenant-ID, X-Partition-ID when `AUTH_REQUIRE_JWT=false`; require all three for private routes
- [ ] **Step 2: Routes** under `/v1/…` as in spec (jobs, applications, advance, hire, availability, interviews, slots, book)
- [ ] **Step 3: problem+json helper; Idempotency-Key optional store for hire/book**
- [ ] **Step 4: Handler tests with httptest + claims headers**
- [ ] **Step 5: `go test ./apps/ats/... ./pkg/ats/... -count=1`**
- [ ] **Step 6: Commit** `feat(ats): HTTP API for jobs pipeline and interviews`

---

### Task 6: Talent + publish + hire billing hooks (interfaces)

**Files:**
- Create: `pkg/ats/integrations.go` (interfaces: MatchingTalent, OpportunityPublisher, BillingEmitter, Notifier)
- Modify: `pkg/ats/service.go` to call hooks
- Create: `pkg/ats/integrations_test.go` with fakes

- [ ] **Step 1: Interfaces + no-op defaults**
- [ ] **Step 2: ListTalent / AddFromTalent / Publish / Unpublish / Hire emit**
- [ ] **Step 3: Tests with fakes**
- [ ] **Step 4: Commit** `feat(ats): integration ports for talent publish notify billing`

---

### Task 7: AI assist endpoints (thin)

**Files:**
- Create: `pkg/ats/ai.go`
- Modify: HTTP handlers `POST /v1/ai/screen-summary` etc. returning structured stubs or LLM if client configured

- [ ] **Step 1: Interface AIAssistant with fake**
- [ ] **Step 2: Wire routes**
- [ ] **Step 3: Commit** `feat(ats): AI assist API surface`

---

### Task 8: Recruiter SPA skeleton `ui/ats`

**Files:**
- Create: `ui/ats/package.json`, Vite React TS app, mobile nav (Jobs, Pipeline, Today, More), API client

- [ ] **Step 1: Scaffold Vite app sharing patterns from `ui/app`**
- [ ] **Step 2: Jobs list/create against API**
- [ ] **Step 3: Pipeline board**
- [ ] **Step 4: Availability + schedule dialog**
- [ ] **Step 5: Commit** `feat(ats): mobile-first recruiter SPA shell`

---

### Task 9: Candidate slot-pick page + docs

- [ ] Candidate routes (auth required)
- [ ] Update README services table
- [ ] Ops notes for deploy
- [ ] Commit `docs: ATS ops and README`

---

## Spec coverage checklist

| Spec area | Tasks |
|-----------|-------|
| apps/ats binary | 5 |
| Frame BaseModel tenancy | 1, 3 |
| Jobs CRUD + publish fields | 3, 5, 6 |
| Applications + stages | 1, 3, 5 |
| Interviews + availability | 2, 4, 5 |
| Stawi talent | 6 |
| AI | 7 |
| Results billing hook | 6 |
| Agent API (same HTTP) | 5 |
| UI mobile | 8–9 |
| No Person CRM | all (profile_id only) |

## Execution note

Implement Tasks 1–6 fully with tests before SPA. Prefer real GORM sqlite in unit tests; postgres integration optional.
