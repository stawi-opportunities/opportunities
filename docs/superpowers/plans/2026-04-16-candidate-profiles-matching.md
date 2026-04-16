# Candidate Profiles + Matching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the candidate profile system with AI-driven CV extraction, three-stage job matching, and free/paid tier logic — fully integrated with the antinvestor platform services (authentication, profile, notification, payment, files, redirect).

**Architecture:** New Frame-based `apps/candidates/` service sharing the existing PostgreSQL and Ollama. Uses antinvestor Connect RPC clients for auth (magic links), notifications (match emails), file storage (CVs), payments (subscriptions), and redirect (tracked apply links). Matching runs asynchronously via Frame events.

**Tech Stack:** Go 1.26, Frame, GORM, Ollama, Connect RPC clients (`antinvestor/common`), chi router, PDF/DOCX parsing

**Current State:** 45,032 canonical jobs crawled from 35 sources. Scoring, embeddings, and full-text search operational.

---

## Antinvestor Service Integration Map

| Need | Service | Client Package | Key Methods |
|---|---|---|---|
| Magic link auth | service-authentication | `authenticationv1connect` | `GetLoginEvent` |
| Candidate identity | service-profile | `profilev1connect` | `Create`, `GetByContact`, `CreateContact`, `CreateContactVerification`, `CheckVerification` |
| Match emails, verification | service-notification | `notificationv1connect` | `Send`, `TemplateSave` |
| CV file storage | service-files | `filesv1connect` | `UploadContent`, `GetSignedDownloadUrl` |
| Subscription payments | service-payment | `paymentv1connect` | `CreatePaymentLink`, `Status` |
| Tracked apply/view links | redirect service | `redirectv1connect` | `CreateLink`, `GetLinkStats` |

All clients instantiated via `connection.NewServiceClient()` from `github.com/antinvestor/common/connection`.

---

## File Structure

```
pkg/domain/candidate.go                           ← domain models
pkg/repository/candidate.go                        ← candidate CRUD
pkg/repository/match.go                            ← match CRUD
pkg/extraction/cv.go                               ← CV text extraction + AI profile extraction
pkg/matching/matcher.go                            ← three-stage matching pipeline
pkg/matching/scorer.go                             ← match score computation
apps/candidates/config/config.go                   ← config with service endpoints
apps/candidates/cmd/main.go                        ← Frame service entry point
apps/candidates/service/clients.go                 ← antinvestor service client setup
apps/candidates/service/events/profile_created.go  ← triggers matching + verification
apps/candidates/service/events/embedding.go        ← candidate embedding generation
apps/candidates/service/handlers.go                ← HTTP route handlers
apps/candidates/Dockerfile                         ← Docker image
```

---

## Task 1: Domain Models

**Files:**
- Create: `pkg/domain/candidate.go`

- [ ] **Step 1: Create candidate models**

Create `pkg/domain/candidate.go` with:
- `CandidateProfile` struct with GORM tags (all fields from spec Section 3)
- `CandidateMatch` struct with GORM tags and `UNIQUE(candidate_id, canonical_job_id)`
- `CandidateApplication` struct (schema only for future auto-apply)
- Status/tier enums: `CandidateStatus`, `SubscriptionTier`, `MatchStatus`

All types follow the same pattern as existing domain models (see `pkg/domain/models.go`). Include `gorm.DeletedAt` for soft delete on profiles.

- [ ] **Step 2: Verify build, commit**

```bash
go build ./pkg/domain/...
git add pkg/domain/candidate.go
git commit -m "feat: add candidate profile, match, and application domain models"
```

---

## Task 2: Repositories

**Files:**
- Create: `pkg/repository/candidate.go`
- Create: `pkg/repository/match.go`
- Modify: `pkg/repository/job.go` (add `FilterForCandidate`)

- [ ] **Step 1: Create candidate repository**

`pkg/repository/candidate.go` with methods:
- `Create`, `GetByID`, `GetByEmail`, `Update`, `UpdateStatus`
- `UpdateEmbedding`, `IncrementMatchesSent`
- `ListActive(limit)`, `ListAll(limit, offset)`, `Count`

Constructor: `NewCandidateRepository(db func(ctx, bool) *gorm.DB)`

- [ ] **Step 2: Create match repository**

`pkg/repository/match.go` with methods:
- `Upsert` (ON CONFLICT candidate_id, canonical_job_id)
- `UpsertBatch` (CreateInBatches 100)
- `ListForCandidate(candidateID, limit)`, `ListUnsent(candidateID, limit)`
- `MarkSent(id)`, `MarkViewed(id)`, `CountForCandidate(candidateID)`

- [ ] **Step 3: Add FilterForCandidate to job repository**

Add to `pkg/repository/job.go`:
```go
func (r *JobRepository) FilterForCandidate(ctx, candidate *domain.CandidateProfile, limit int) ([]*domain.CanonicalJob, error)
```
Hard filters: remote_preference, salary floor, preferred_countries. Returns jobs ordered by quality_score DESC.

- [ ] **Step 4: Verify build, commit**

```bash
go build ./pkg/repository/...
git add pkg/repository/candidate.go pkg/repository/match.go pkg/repository/job.go
git commit -m "feat: add candidate and match repositories with job filtering"
```

---

## Task 3: CV Extraction

**Files:**
- Create: `pkg/extraction/cv.go`

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/ledongthuc/pdf@latest
go get github.com/nguyenthenguyen/docx@latest
```

- [ ] **Step 2: Create CV extraction module**

`pkg/extraction/cv.go` with:
- `CVFields` struct (name, email, phone, current_title, seniority, years_experience, strong_skills, working_skills, tools_frameworks, certifications, preferred_roles, languages, education, work_history, preferences)
- `WorkHistoryEntry` struct (company, title, start_date, end_date, summary)
- `ExtractTextFromPDF(data []byte) (string, error)` — uses `ledongthuc/pdf`
- `ExtractTextFromDOCX(data []byte) (string, error)` — uses `nguyenthenguyen/docx`
- `ExtractTextFromFile(data []byte, filename string) (string, error)` — routes by extension
- `(e *Extractor) ExtractCV(ctx, cvText string) (*CVFields, error)` — calls Ollama with CV-specific prompt

The CV prompt instructs the AI to separate strong_skills (depth indicators) from working_skills (mentioned once), extract structured work history, and identify preferences if stated.

- [ ] **Step 3: Verify build, commit**

```bash
go build ./pkg/extraction/...
git add pkg/extraction/cv.go
git commit -m "feat: add CV text extraction (PDF/DOCX) and AI profile extraction"
```

---

## Task 4: Matching Algorithm

**Files:**
- Create: `pkg/matching/scorer.go`
- Create: `pkg/matching/matcher.go`

- [ ] **Step 1: Create match scorer**

`pkg/matching/scorer.go` with pure functions:
- `ComputeMatchScore(skillsOverlap, embeddingSimilarity, qualityScore, salaryFit, recency, seniorityFit) float64` — weighted composite (0.35/0.25/0.15/0.10/0.10/0.05)
- `SkillsOverlap(candidateSkills, jobRequiredSkills string) float64` — Jaccard similarity on comma-separated strings
- `SalaryFit(candidateMin, candidateMax, jobMin, jobMax float64) float64` — overlap check with gap decay
- `Recency(jobLastSeen time.Time) float64` — linear decay over 30 days
- `SeniorityFit(candidateSeniority, jobSeniority string) float64` — exact=1.0, ±1=0.5, else=0

- [ ] **Step 2: Create matcher**

`pkg/matching/matcher.go` with:
- `Matcher` struct holding jobRepo, matchRepo, candidateRepo
- `MatchCandidateToJobs(ctx, candidate) (int, error)` — Stage 1 (SQL filter) → Stage 2 (embedding cosine) → Stage 3 (composite score) → store matches where score >= 0.6
- `MatchJobToCandidates(ctx, job) (int, error)` — same pipeline, reversed: one job against all active candidates
- Helper: `cosineSimilarity(a, b []float32) float64`
- Helper: `parseEmbedding(raw string) []float32`

- [ ] **Step 3: Verify build, commit**

```bash
go build ./pkg/matching/...
git add pkg/matching/
git commit -m "feat: add three-stage matching algorithm with composite scoring"
```

---

## Task 5: Antinvestor Service Clients

**Files:**
- Create: `apps/candidates/service/clients.go`

- [ ] **Step 1: Add antinvestor/common dependency**

```bash
go get github.com/antinvestor/common@latest
```

- [ ] **Step 2: Create service client setup**

`apps/candidates/service/clients.go` with:

```go
package service

import (
    "context"
    "github.com/antinvestor/common/connection"
    notificationv1connect "buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
    filesv1connect "buf.build/gen/go/antinvestor/files/connectrpc/go/files/v1/filesv1connect"
    paymentv1connect "buf.build/gen/go/antinvestor/payment/connectrpc/go/payment/v1/paymentv1connect"
    profilev1connect "buf.build/gen/go/antinvestor/profile/connectrpc/go/profile/v1/profilev1connect"
)

type ServiceClients struct {
    Notification notificationv1connect.NotificationServiceClient
    Files        filesv1connect.FilesServiceClient
    Payment      paymentv1connect.PaymentServiceClient
    Profile      profilev1connect.ProfileServiceClient
}

func NewServiceClients(ctx context.Context, cfg *config.CandidatesConfig) (*ServiceClients, error) {
    // Each client uses connection.NewServiceClient with the appropriate endpoint
    // Pattern from service-profile:
    //   client, err := connection.NewServiceClient(ctx, cfg, common.ServiceTarget{
    //       Endpoint: cfg.NotificationServiceURI,
    //   }, notificationv1connect.NewNotificationServiceClient)
    ...
}
```

Read the exact `connection.NewServiceClient` signature from `/home/j/code/antinvestor/common/connection/` and the usage pattern from `/home/j/code/antinvestor/service-profile/apps/default/cmd/main.go` to get this right.

The config needs service endpoint fields:
```go
NotificationServiceURI string `env:"NOTIFICATION_SERVICE_URI" envDefault:""`
FileServiceURI         string `env:"FILE_SERVICE_URI" envDefault:""`
PaymentServiceURI      string `env:"PAYMENT_SERVICE_URI" envDefault:""`
ProfileServiceURI      string `env:"PROFILE_SERVICE_URI" envDefault:""`
RedirectServiceURI     string `env:"REDIRECT_SERVICE_URI" envDefault:""`
```

- [ ] **Step 3: Verify build, commit**

```bash
go build ./apps/candidates/...
git add apps/candidates/service/clients.go apps/candidates/config/config.go
git commit -m "feat: add antinvestor service client setup (notification, files, payment, profile)"
```

---

## Task 6: Event Handlers

**Files:**
- Create: `apps/candidates/service/events/profile_created.go`
- Create: `apps/candidates/service/events/embedding.go`

- [ ] **Step 1: Create profile created handler**

`apps/candidates/service/events/profile_created.go`:
- Event name: `candidate.profile.created`
- Payload: `{CandidateID int64}`
- Execute: loads candidate → runs `matcher.MatchCandidateToJobs` → sends verification email via notification service

The verification email uses `notificationv1connect.Send()`:
```
Subject: "Your stawi.jobs profile is ready"
Body: "Hi {name}, here's what we understood from your CV: ..."
```

Uses redirect service to create tracked links in the email (e.g., "View profile" link via `redirectv1connect.CreateLink`).

- [ ] **Step 2: Create candidate embedding handler**

`apps/candidates/service/events/embedding.go`:
- Event name: `candidate.embedding.needed`
- Payload: `{CandidateID int64, Text string}`
- Execute: calls `extractor.Embed(ctx, text)` → stores via `candidateRepo.UpdateEmbedding`

Same pattern as the crawler's `EmbeddingGenerationHandler`.

- [ ] **Step 3: Verify build, commit**

```bash
go build ./apps/candidates/...
git add apps/candidates/service/events/
git commit -m "feat: add candidate event handlers with notification and redirect integration"
```

---

## Task 7: HTTP Handlers + Main

**Files:**
- Create: `apps/candidates/service/handlers.go`
- Create: `apps/candidates/cmd/main.go`
- Create: `apps/candidates/config/config.go`

- [ ] **Step 1: Create config**

`apps/candidates/config/config.go`:
```go
type CandidatesConfig struct {
    fconfig.ConfigurationDefault
    OllamaURL              string `env:"OLLAMA_URL" envDefault:""`
    OllamaModel            string `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`
    MaxFreeMatches         int    `env:"MAX_FREE_MATCHES" envDefault:"5"`
    NotificationServiceURI string `env:"NOTIFICATION_SERVICE_URI" envDefault:""`
    FileServiceURI         string `env:"FILE_SERVICE_URI" envDefault:""`
    PaymentServiceURI      string `env:"PAYMENT_SERVICE_URI" envDefault:""`
    ProfileServiceURI      string `env:"PROFILE_SERVICE_URI" envDefault:""`
    RedirectServiceURI     string `env:"REDIRECT_SERVICE_URI" envDefault:""`
}
```

- [ ] **Step 2: Create HTTP handlers**

`apps/candidates/service/handlers.go` with functions returning `http.HandlerFunc`:

| Route | Handler | Service Integration |
|---|---|---|
| `POST /candidates/register` | `registerHandler` | Files service (CV upload), Ollama (extraction), emit profile.created event |
| `GET /candidates/profile` | `getProfileHandler` | Profile service (identity lookup) |
| `PUT /candidates/profile` | `updateProfileHandler` | Update preferences, emit profile.updated event |
| `POST /candidates/cv` | `uploadCVHandler` | Files service (store CV), Ollama (re-extract) |
| `GET /candidates/matches` | `listMatchesHandler` | Redirect service (create tracked apply links) |
| `POST /candidates/matches/:id/view` | `viewMatchHandler` | Redirect service (track view) |
| `POST /candidates/subscribe` | `subscribeHandler` | Payment service (create payment link) |
| `DELETE /candidates/subscribe` | `unsubscribeHandler` | Update subscription tier |
| `POST /webhooks/inbound-email` | `inboundEmailHandler` | Files (store attachment), Ollama (extract), emit events |
| `GET /admin/candidates` | `listCandidatesHandler` | Direct DB query |
| `POST /admin/match/run` | `forceMatchHandler` | Run matcher for all active candidates |

Key integration patterns:

**CV Upload** → Files service:
```go
filesClient.UploadContent(ctx, stream) // stream the CV file
// Store the returned file ID as cv_url
```

**Match Email** → Notification service:
```go
notificationClient.Send(ctx, &notificationv1.SendRequest{...})
```

**Tracked Apply Link** → Redirect service:
```go
redirectClient.CreateLink(ctx, &redirectv1.CreateLinkRequest{
    Data: &redirectv1.Link{
        DestinationUrl: job.ApplyURL,
        Campaign: "match_email",
        AffiliateId: fmt.Sprintf("candidate_%d", candidateID),
    },
})
// Return the short URL: /r/{slug}
```

**Subscription** → Payment service:
```go
paymentClient.CreatePaymentLink(ctx, &paymentv1.CreatePaymentLinkRequest{...})
// Redirect candidate to payment page
```

- [ ] **Step 3: Create main.go**

`apps/candidates/cmd/main.go`:
- Load config via `fconfig.FromEnv[CandidatesConfig]()`
- Create Frame service with `frame.WithConfig`, `frame.WithDatastore`
- Get DB pool, create repositories
- Create extractor (if OLLAMA_URL set)
- Create matcher
- Create antinvestor service clients (if URIs configured)
- Register Frame events (profile_created, embedding)
- Create chi router with all HTTP handlers
- Register router with `frame.WithHTTPHandler`
- AutoMigrate candidate tables
- `svc.Run(ctx, "")`

Follow the exact pattern from `apps/crawler/cmd/main.go` for Frame lifecycle.

- [ ] **Step 4: Verify full build**

```bash
go mod tidy
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/
git commit -m "feat: add candidates app with Frame service, HTTP API, and antinvestor service integration"
```

---

## Task 8: Dockerfile + K8s Deployment

**Files:**
- Create: `apps/candidates/Dockerfile`
- Create: k8s manifests in deployments repo

- [ ] **Step 1: Create Dockerfile**

Same pattern as crawler Dockerfile:
- `golang:1.26` builder, `cgr.dev/chainguard/static` final
- COPY `./apps/candidates` and `./pkg`
- Build `./apps/candidates/cmd/main.go`
- Entrypoint: `/candidates`

- [ ] **Step 2: Create k8s manifests**

In `antinvestor/deployments/manifests/namespaces/stawi-jobs/candidates/`:
- `kustomization.yaml`
- `stawi-jobs-candidates.yaml` — HelmRelease, 1 replica, 100m/256Mi, same DB credentials
- Env vars: DATABASE_URL, OLLAMA_URL, OLLAMA_MODEL, NOTIFICATION_SERVICE_URI, FILE_SERVICE_URI, PAYMENT_SERVICE_URI, PROFILE_SERVICE_URI, REDIRECT_SERVICE_URI

Update the parent `kustomization.yaml` to include `candidates/`.

- [ ] **Step 3: Update kustomization_provider.yaml if needed**

The stawi-jobs-setup kustomization already covers all subdirectories.

- [ ] **Step 4: Commit both repos**

```bash
# stawi.jobs repo
git add apps/candidates/Dockerfile
git commit -m "feat: add candidates Dockerfile"
git push origin main
git tag v0.9.0
git push origin v0.9.0

# deployments repo
git add manifests/namespaces/stawi-jobs/candidates/
git commit -m "feat: add stawi-jobs-candidates k8s deployment with antinvestor service URIs"
git push origin main
```

---

## Execution Notes

**Sequential dependencies:**
- Task 1 → Task 2 → Task 3 → Task 4 → Task 5 → Task 6 → Task 7 → Task 8

**Parallelizable:**
- Tasks 1+3 (domain models + CV extraction) can run in parallel
- Tasks 2+4 (repositories + matching) can run in parallel after 1+3
- Task 5 (service clients) can run in parallel with Task 4

**Critical integration points:**
- Task 5 requires the correct `antinvestor/common` import paths — read the actual source
- Task 6 requires understanding the notification service's `Send` method signature
- Task 7 requires the redirect service's `CreateLink` method for tracked URLs
