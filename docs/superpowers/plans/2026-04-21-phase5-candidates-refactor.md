# Phase 5 — Candidates Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewire `apps/candidates` to be what the greenfield spec §4.2 / §6.2 says it is — a single binary owning the CV lifecycle via event-sourced subscriptions plus a Manticore-backed match endpoint. Upload persists text to R2 and emits `candidates.cv.uploaded.v1`; internal subscribers extract/score, improve, and embed; the match endpoint reads the candidate's embedding + preferences from R2 `*_current/` partitions and queries `idx_opportunities_rt`. Trustage fires `matches.weekly_digest` and `cv.stale_nudge` against new admin endpoints. Ancillary HTTP endpoints on the current binary (registration, profile CRUD, billing webhooks, saved jobs, link-expired, inbound-email, forceMatch, getCVScore, rescoreCV, listMatches, viewMatch, listCandidates) are removed from `apps/candidates` — identity lives in the external Profile service, saved jobs move to `apps/api`, billing webhooks move to a dedicated billing app, and the rest become dead code that Phase 6 deletes from Postgres.

**Architecture:** One `apps/candidates` binary with **three internal Frame subscriptions** (cv-extract, cv-improve, cv-embed) + **three HTTP endpoints** (`POST /candidates/cv/upload`, `POST /candidates/preferences`, `GET /candidates/match`) + **two Trustage admin endpoints** (`POST /_admin/matches/weekly_digest`, `POST /_admin/cv/stale_nudge`) + a minimal `GET /healthz`. The match endpoint uses a new `pkg/candidatestore` reader that lists `candidates_embeddings_current/` and `candidates_preferences_current/` partitions for the requested `candidate_id` and falls back to a short-lived Valkey cache. No CV, preferences, or embedding data lands in Postgres anywhere in this plan — the `candidates` row keeps only `{id, profile_id, status, subscription, created_at, updated_at}` per §5.5. Phase 6 drops the CV/preferences/embedding columns.

Legacy candidate event types (`ProfileCreatedEventName`, `CandidateEmbeddingEventName`) and the legacy `apps/candidates/service/events/` handlers stay in the repo untouched — Phase 6 cutover deletes them alongside the Postgres column drops.

**Tech stack:**
- Go 1.26, Frame (`github.com/pitabwire/frame` v1.94.1), `pitabwire/util` logging
- Existing `pkg/extraction.Extractor` — `ExtractTextFromPDF`, `ExtractTextFromDOCX`, `ExtractCV`, `Embed`, `Prompt`, `Rerank`
- Existing `pkg/cv` — `Scorer`, `detectPriorityFixes`, `AttachRewrites` (via `LLMPrompter` interface)
- Existing `pkg/archive` — R2 raw-bytes storage (for uploaded CV files)
- Existing `pkg/eventlog` — R2 Parquet reader (new helper in `pkg/candidatestore` builds on this)
- Existing `pkg/searchindex` (from Phase 2) — Manticore client for KNN + filter queries
- Existing `pkg/events/v1/` (from Phase 1) — extended with six candidate payload types
- `apps/writer/service/` (from Phase 1) — extended encoder + hint extraction for candidate topics
- OTEL (`go.opentelemetry.io/otel`) for metrics (existing)

**What's in this plan:**
- Six new event payload types (`pkg/events/v1/candidates.go`) + six topic constants (`pkg/events/v1/names.go`) + partition routing for eight candidate Parquet collections.
- Writer encoder + hint extraction extensions for all six candidate topics.
- `pkg/candidatestore` — reads latest candidate embedding + preferences from R2 `*_current/` partitions, with a small Valkey cache; writes nothing.
- `apps/candidates/service/http/v1/` — three HTTP handlers (upload, preferences, match) **with production adapters wired end-to-end**.
- `apps/candidates/service/events/v1/` — three Frame subscription handlers (cv-extract, cv-improve, cv-embed) and their unit tests.
- `apps/candidates/service/admin/v1/` — two Trustage admin handlers (matches weekly digest, CV stale nudge) **with production adapters wired end-to-end**.
- Four production adapter files wiring the narrow interfaces declared by the handlers:
  - `SearchIndex` adapter on `pkg/searchindex.Client` (Task 16)
  - `CandidateLister` adapter on `pkg/repository.CandidateRepository` (Task 17)
  - `MatchService` — the match handler's scoring pipeline extracted for reuse by `MatchRunner` (Task 18)
  - `StaleLister` adapter on `pkg/repository.CandidateRepository` using `updated_at` as the activity proxy (Task 19)
- `apps/candidates/cmd/main.go` rewritten — drops ~1,200 lines of legacy wiring; wires the new handlers + Trustage admins + healthz; no `nil` placeholders.
- Two Trustage triggers: `candidates-matches-weekly-digest.json`, `candidates-cv-stale-nudge.json`.
- End-to-end test: POST `/candidates/cv/upload` → subscriptions fire → assert six-stage event trail → POST `/candidates/preferences` → GET `/candidates/match` returns top-N matches.

**What's NOT in this plan (deferred to Phase 6 cutover):**
- Deleting legacy `apps/candidates/service/events/{embedding.go,profile_created.go}` files.
- Deleting legacy HTTP handlers from the current `main.go` — they are removed from the new `main.go` but the old file is simply overwritten; any referenced helpers used only by those handlers fall out with the rewrite.
- Dropping the CV/preferences/embedding columns on Postgres `candidates` table. The new code never reads or writes those columns, but the schema change happens in the Phase 6 cutover SQL.
- Moving `saved_jobs` endpoints to `apps/api`. The new `apps/candidates/cmd/main.go` does not expose save/unsave/list-saved endpoints — callers must migrate to `apps/api` (Phase 6 scope).
- Moving billing webhooks/plans/checkout to a separate billing binary. Same pattern: endpoints removed here; new home is Phase 6's problem.
- Recruiter-side candidate search (`idx_candidates_rt` in Manticore) — deferred to v1.1 per spec.
- Link-expired notification flow — if it's wanted, it becomes a consumer of `jobs.canonicals.expired.v1` in a future plan; removed from `apps/candidates` here.
- Daily compaction of `candidates_cv_current/` / `candidates_embeddings_current/` / `candidates_preferences_current/` from the raw daily partitions. Plan 5 writes only the daily partitions (via the existing writer); the match endpoint's `candidatestore.Reader` reads `*_current/` which won't exist until compaction ships in Phase 6. In the meantime, operators can run a one-off backfill job, or Task 19's StaleLister (Postgres-backed for now) gives us a working stale-nudge without depending on compaction.

**What's intentionally deleted (aggressive option chosen by human):**

| Endpoint | Replacement |
|---|---|
| `POST /candidates/register` | External Profile service (spec §4.3) |
| `POST /webhooks/inbound-email` | Out of scope for v1 — email-upload pathway deferred |
| `POST /webhooks/billing` | Separate billing app (Phase 6) |
| `GET /billing/plans` | Separate billing app |
| `GET /billing/checkout/status` | Separate billing app |
| `GET /me`, `GET /me/subscription` | Profile service owns identity; subscription query moves to billing app |
| `GET /candidates/:id`, `GET /candidates` (list) | Recruiter surfaces → v1.1 |
| `PATCH /candidates/:id` | Profile service |
| `GET /candidates/:id/score`, `POST /candidates/:id/rescore` | Dropped — CV is event-sourced; score component lives in `CVExtractedV1.score_components` on the event log |
| `GET /matches`, `GET /matches/:id` | Replaced by `GET /candidates/match` (on-demand match; persisted via `MatchesReadyV1` event) |
| `POST /force-match/:id` | Dropped — match endpoint is idempotent on-demand |
| `POST /candidates/:id/saved-jobs`, `DELETE …`, `GET …/saved-jobs` | Move to `apps/api` (Phase 6) |
| `POST /internal/link-expired` | Becomes a consumer of `jobs.canonicals.expired.v1` if still needed (Phase 6) |
| `POST /candidates/onboard` | Subsumed by `POST /candidates/preferences` |
| Legacy events `candidate.profile.created`, `candidate.embedding.ready` | Replaced by `CVUploadedV1` / `CVExtractedV1` / `CVEmbeddingV1` etc. |

**Subsequent plans (after this one):**
- Plan 6: Greenfield cutover + ops. Drops legacy Postgres columns on `candidates`, deletes legacy handler files, moves billing to a new app, moves saved-jobs to apps/api, wires link-expired as a canonical-expired consumer (or deletes it).

---

## File structure

**Create:**

| File | Responsibility |
|---|---|
| `pkg/events/v1/candidates.go` | Six payload structs (`CVUploadedV1`, `CVExtractedV1`, `CVImprovedV1`, `CandidateEmbeddingV1`, `PreferencesUpdatedV1`, `MatchesReadyV1`) with `json` + `parquet` tags |
| `pkg/candidatestore/reader.go` | `Reader` — reads latest embedding + preferences from R2 `candidates_embeddings_current/` and `candidates_preferences_current/` via `pkg/eventlog`. Optional Valkey cache. No writes. |
| `pkg/candidatestore/reader_test.go` | Integration test with MinIO + seeded Parquet files |
| `apps/candidates/service/http/v1/upload.go` | `POST /candidates/cv/upload` handler — multipart/form upload, archive raw bytes, extract text, emit `CVUploadedV1` |
| `apps/candidates/service/http/v1/upload_test.go` | httptest-driven unit test |
| `apps/candidates/service/http/v1/preferences.go` | `POST /candidates/preferences` handler — validates body, emits `PreferencesUpdatedV1` |
| `apps/candidates/service/http/v1/preferences_test.go` | httptest-driven unit test |
| `apps/candidates/service/http/v1/match.go` | `GET /candidates/match` handler — loads embedding + prefs, queries Manticore KNN+filter, in-Go composite scoring, optional rerank, emits `MatchesReadyV1` |
| `apps/candidates/service/http/v1/match_test.go` | Unit test with fakes for `candidatestore.Reader` and `searchindex.Client` |
| `apps/candidates/service/events/v1/cv_extract.go` | Frame handler — consumes `candidates.cv.uploaded.v1`, loads archived file, runs `ExtractCV`, runs `cv.Scorer`, emits `CVExtractedV1` |
| `apps/candidates/service/events/v1/cv_extract_test.go` | Unit test with fake extractor + fake scorer |
| `apps/candidates/service/events/v1/cv_improve.go` | Frame handler — consumes `candidates.cv.extracted.v1`, runs `cv.detectPriorityFixes` + `AttachRewrites`, emits `CVImprovedV1` |
| `apps/candidates/service/events/v1/cv_improve_test.go` | Unit test |
| `apps/candidates/service/events/v1/cv_embed.go` | Frame handler — consumes either `candidates.cv.extracted.v1` or `candidates.cv.improved.v1`, runs `extractor.Embed`, emits `CandidateEmbeddingV1` |
| `apps/candidates/service/events/v1/cv_embed_test.go` | Unit test |
| `apps/candidates/service/admin/v1/matches_weekly.go` | `POST /_admin/matches/weekly_digest` — iterates active candidates, fires match per-candidate, emits `MatchesReadyV1` |
| `apps/candidates/service/admin/v1/matches_weekly_test.go` | Unit test |
| `apps/candidates/service/admin/v1/cv_stale_nudge.go` | `POST /_admin/cv/stale_nudge` — finds candidates idle > 60 days, emits `candidates.cv.stale_nudge.v1` (out-of-band event for the external notification service) |
| `apps/candidates/service/admin/v1/cv_stale_nudge_test.go` | Unit test |
| `apps/candidates/service/e2e_test.go` | End-to-end: upload CV → extract → improve → embed → preferences → match → assert trail |
| `definitions/trustage/candidates-matches-weekly-digest.json` | Weekly cron calling `/_admin/matches/weekly_digest` |
| `definitions/trustage/candidates-cv-stale-nudge.json` | Daily cron calling `/_admin/cv/stale_nudge` |

**Modify:**

| File | Change |
|---|---|
| `pkg/events/v1/names.go` | Add six new topic constants (`TopicCVUploaded`, `TopicCVExtracted`, `TopicCVImproved`, `TopicCandidateEmbedding`, `TopicCandidatePreferencesUpdated`, `TopicCandidateMatchesReady`) plus `TopicCandidateCVStaleNudge`; extend `AllTopics()` with the ones the writer persists |
| `pkg/events/v1/envelope_test.go` | Six round-trip tests (one per payload type) |
| `pkg/events/v1/partitions.go` | Add partition routing for seven candidate collections (`candidates_cv`, `candidates_cv_current`, `candidates_improvements`, `candidates_preferences`, `candidates_preferences_current`, `candidates_embeddings`, `candidates_embeddings_current`, `candidates_matches_ready`) — all use `cnd=<candidate_id_prefix_2hex>` secondary |
| `pkg/events/v1/partitions_test.go` (actually envelope_test.go per repo convention) | Two new partition tests |
| `apps/writer/service/handler.go` | Extend `extractHint` with six new cases all keyed on `payload.candidate_id` |
| `apps/writer/service/service.go` | Extend `uploadBatch` with six new encoder cases |
| `apps/writer/service/buffer.go` | Extend `collectionForTopic` with mappings for the six candidate topics (matches.ready is its own collection even though it shares the cnd axis) |
| `apps/candidates/cmd/main.go` | Rewritten from scratch — drops ~1,200 lines of legacy wiring, wires the three HTTP handlers, three event subscriptions, two admin endpoints, health |
| `apps/candidates/config/config.go` | Add `R2ArchiveBucket`, `R2EventLogBucket`, `ManticoreURL`, `ValkeyAddr` env vars; drop `BillingBaseURL`, `NotificationBaseURL`, `ProfileServiceURL` if they were only used by removed handlers (trim as needed) |

**Delete (via main.go rewrite):**

The old `apps/candidates/cmd/main.go` has ~25 HTTP handlers (registerHandler, inboundEmailHandler, billingWebhookHandler, plansHandler, checkoutStatusHandler, linkExpiredHandler, getProfileHandler, updateProfileHandler, listMatchesHandler, viewMatchHandler, listCandidatesHandler, meHandler, meSubscriptionHandler, onboardHandler, uploadCVHandler, saveJobHandler, unsaveJobHandler, listSavedJobsHandler, forceMatchHandler, getCVScoreHandler, rescoreCVHandler). The new `main.go` registers none of them — only the three new ones + two admins + healthz. All their helper funcs that live in the same file (`applyCVFields`, `buildEmbeddingText`, `joinCSV`, `boolStr`, `parseCanonicalJobAffiliate`, `notifyExpiredJobSubscribers`, `scoreAndPersistCV`, `kickoffCVScore`, `sendRegistrationNotification`, `uploadCVToFiles`, `isValidCandidateStatus`, `sourceStateChecker`-equivalent) fall out when main.go is rewritten.

`apps/candidates/cmd/billing.go` (422 lines) — DELETE. Billing logic moves to the billing app in Phase 6.

`apps/candidates/service/events/embedding.go` and `profile_created.go` — left in place (Phase 6 deletes). The new main.go just doesn't register them.

---

## Task 1: Add six candidate event payload types

**Files:**
- Create: `pkg/events/v1/candidates.go`
- Modify: `pkg/events/v1/envelope_test.go`

- [ ] **Step 1: Write failing round-trip tests**

Append to `pkg/events/v1/envelope_test.go`:

```go
func TestCVUploadedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCVUploaded, CVUploadedV1{
		CandidateID:   "cnd_1",
		CVVersion:     1,
		RawArchiveRef: "raw/abc123",
		Filename:      "resume.pdf",
		ContentType:   "application/pdf",
		SizeBytes:     12345,
	})
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope[CVUploadedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CandidateID != "cnd_1" || back.Payload.CVVersion != 1 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCVExtractedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCVExtracted, CVExtractedV1{
		CandidateID:      "cnd_1",
		CVVersion:        1,
		Name:             "Jane Doe",
		Email:            "jane@example.com",
		Seniority:        "senior",
		YearsExperience:  8,
		PrimaryIndustry:  "technology",
		StrongSkills:     []string{"Go", "Kubernetes"},
		WorkingSkills:    []string{"Python"},
		ScoreOverall:     82,
		ScoreATS:         85,
		ScoreKeywords:    78,
		ScoreImpact:      80,
		ScoreRoleFit:     84,
		ScoreClarity:     83,
		ModelVersionExtract: "ext-v1",
		ModelVersionScore:   "score-v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[CVExtractedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CandidateID != "cnd_1" || back.Payload.ScoreOverall != 82 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCVImprovedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCVImproved, CVImprovedV1{
		CandidateID:  "cnd_1",
		CVVersion:    1,
		Fixes: []CVFix{{
			FixID:          "fix-1",
			Title:          "Add quantified impact",
			ImpactLevel:    "high",
			Category:       "impact",
			Why:            "Bullets lack numbers",
			AutoApplicable: true,
			Rewrite:        "Reduced latency 40%",
		}},
		ModelVersion: "improve-v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[CVImprovedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Payload.Fixes) != 1 || back.Payload.Fixes[0].FixID != "fix-1" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCandidateEmbeddingRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCandidateEmbedding, CandidateEmbeddingV1{
		CandidateID:  "cnd_1",
		CVVersion:    1,
		Vector:       []float32{0.1, 0.2, 0.3},
		ModelVersion: "embed-v1",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[CandidateEmbeddingV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.CandidateID != "cnd_1" || len(back.Payload.Vector) != 3 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestCandidatePreferencesUpdatedRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCandidatePreferencesUpdated, PreferencesUpdatedV1{
		CandidateID:        "cnd_1",
		RemotePreference:   "remote",
		SalaryMin:          80000,
		SalaryMax:          140000,
		Currency:           "USD",
		PreferredLocations: []string{"KE", "US"},
		ExcludedCompanies:  []string{"BadCo"},
		TargetRoles:        []string{"backend-engineer"},
		Languages:          []string{"en"},
		Availability:       "2-weeks",
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[PreferencesUpdatedV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Payload.SalaryMin != 80000 || len(back.Payload.PreferredLocations) != 2 {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}

func TestMatchesReadyRoundTrip(t *testing.T) {
	orig := NewEnvelope(TopicCandidateMatchesReady, MatchesReadyV1{
		CandidateID:  "cnd_1",
		MatchBatchID: "batch_1",
		Matches: []MatchRow{
			{CanonicalID: "can_a", Score: 0.91, RerankScore: 0.94},
			{CanonicalID: "can_b", Score: 0.83},
		},
	})
	raw, _ := json.Marshal(orig)
	var back Envelope[MatchesReadyV1]
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Payload.Matches) != 2 || back.Payload.Matches[0].CanonicalID != "can_a" {
		t.Fatalf("round-trip lost: %+v", back.Payload)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
cd /home/j/code/stawi.opportunities
go test ./pkg/events/v1/...
```

Expected: `undefined: CVUploadedV1, CVExtractedV1, CVImprovedV1, CandidateEmbeddingV1, PreferencesUpdatedV1, MatchesReadyV1, CVFix, MatchRow, TopicCV*, TopicCandidate*`.

- [ ] **Step 3: Add topic constants**

Edit `pkg/events/v1/names.go`. Append six new candidate topics to the `const` block (below the jobs topics). Also extend `AllTopics()` with the four persisted ones (uploaded + extracted + improved + embedding + preferences + matches_ready — the `stale_nudge` event is control-plane like crawl.requests and is NOT persisted):

```go
	// Candidate lifecycle (Phase 5).
	TopicCVUploaded                  = "candidates.cv.uploaded.v1"
	TopicCVExtracted                 = "candidates.cv.extracted.v1"
	TopicCVImproved                  = "candidates.cv.improved.v1"
	TopicCandidateEmbedding          = "candidates.embeddings.v1"
	TopicCandidatePreferencesUpdated = "candidates.preferences.updated.v1"
	TopicCandidateMatchesReady       = "candidates.matches.ready.v1"
	TopicCandidateCVStaleNudge       = "candidates.cv.stale_nudge.v1"
```

Update `AllTopics()` — insert the six persisted topics (skip `TopicCandidateCVStaleNudge`):

```go
func AllTopics() []string {
	return []string{
		TopicVariantsIngested,
		TopicVariantsNormalized,
		TopicVariantsValidated,
		TopicVariantsFlagged,
		TopicVariantsClustered,
		TopicCanonicalsUpserted,
		TopicCanonicalsExpired,
		TopicEmbeddings,
		TopicTranslations,
		TopicPublished,
		TopicCrawlPageCompleted,
		TopicSourcesDiscovered,
		TopicCVUploaded,
		TopicCVExtracted,
		TopicCVImproved,
		TopicCandidateEmbedding,
		TopicCandidatePreferencesUpdated,
		TopicCandidateMatchesReady,
	}
}
```

- [ ] **Step 4: Define the payload types**

Create `pkg/events/v1/candidates.go`:

```go
package eventsv1

import "time"

// CVUploadedV1 is emitted by the candidates HTTP upload handler after
// the raw bytes have been archived to R2 and the plain-text has been
// extracted. It is the entry point of the candidate lifecycle.
//
// CVVersion is monotonic per candidate (increments on every successful
// upload). The first upload is version 1. Versions are assigned by
// the upload handler via R2 listing of existing
// candidates_cv_current/ for that candidate.
//
// RawArchiveRef is the R2 object key (e.g. "raw/abc123") returned by
// pkg/archive.PutRaw. Downstream cv-extract re-reads the raw bytes
// from this key; the extracted plain text is also carried inline for
// handler convenience in the normal path.
type CVUploadedV1 struct {
	CandidateID   string `json:"candidate_id"     parquet:"candidate_id"`
	CVVersion     int    `json:"cv_version"       parquet:"cv_version"`
	RawArchiveRef string `json:"raw_archive_ref"  parquet:"raw_archive_ref"`
	Filename      string `json:"filename,omitempty"     parquet:"filename,optional"`
	ContentType   string `json:"content_type,omitempty" parquet:"content_type,optional"`
	SizeBytes     int64  `json:"size_bytes,omitempty"   parquet:"size_bytes,optional"`

	// ExtractedText is the plain-text conversion of the uploaded PDF/DOCX.
	// Carried inline so cv-extract doesn't have to re-read + re-parse R2.
	// Never empty — upload fails if text extraction returns zero bytes.
	ExtractedText string `json:"extracted_text" parquet:"extracted_text"`
}

// CVExtractedV1 carries the parsed CV fields plus the deterministic
// scoring output. Emitted by the cv-extract handler after ExtractCV
// + Scorer.Score run.
type CVExtractedV1 struct {
	CandidateID string `json:"candidate_id" parquet:"candidate_id"`
	CVVersion   int    `json:"cv_version"   parquet:"cv_version"`

	// CVFields (flattened; matches extraction.CVFields shape).
	Name                string   `json:"name,omitempty"                 parquet:"name,optional"`
	Email               string   `json:"email,omitempty"                parquet:"email,optional"`
	Phone               string   `json:"phone,omitempty"                parquet:"phone,optional"`
	Location            string   `json:"location,omitempty"             parquet:"location,optional"`
	CurrentTitle        string   `json:"current_title,omitempty"        parquet:"current_title,optional"`
	Bio                 string   `json:"bio,omitempty"                  parquet:"bio,optional"`
	Seniority           string   `json:"seniority,omitempty"            parquet:"seniority,optional"`
	YearsExperience     int      `json:"years_experience,omitempty"     parquet:"years_experience,optional"`
	PrimaryIndustry     string   `json:"primary_industry,omitempty"     parquet:"primary_industry,optional"`
	StrongSkills        []string `json:"strong_skills,omitempty"        parquet:"strong_skills,list,optional"`
	WorkingSkills       []string `json:"working_skills,omitempty"       parquet:"working_skills,list,optional"`
	ToolsFrameworks     []string `json:"tools_frameworks,omitempty"     parquet:"tools_frameworks,list,optional"`
	Certifications      []string `json:"certifications,omitempty"       parquet:"certifications,list,optional"`
	PreferredRoles      []string `json:"preferred_roles,omitempty"      parquet:"preferred_roles,list,optional"`
	Languages           []string `json:"languages,omitempty"            parquet:"languages,list,optional"`
	Education           string   `json:"education,omitempty"            parquet:"education,optional"`
	PreferredLocations  []string `json:"preferred_locations,omitempty"  parquet:"preferred_locations,list,optional"`
	RemotePreference    string   `json:"remote_preference,omitempty"    parquet:"remote_preference,optional"`
	SalaryMin           int      `json:"salary_min,omitempty"           parquet:"salary_min,optional"`
	SalaryMax           int      `json:"salary_max,omitempty"           parquet:"salary_max,optional"`
	Currency            string   `json:"currency,omitempty"             parquet:"currency,optional"`

	// Score components (matches cv.ScoreComponents).
	ScoreATS      int `json:"score_ats"       parquet:"score_ats"`
	ScoreKeywords int `json:"score_keywords"  parquet:"score_keywords"`
	ScoreImpact   int `json:"score_impact"    parquet:"score_impact"`
	ScoreRoleFit  int `json:"score_role_fit"  parquet:"score_role_fit"`
	ScoreClarity  int `json:"score_clarity"   parquet:"score_clarity"`
	ScoreOverall  int `json:"score_overall"   parquet:"score_overall"`

	ModelVersionExtract string `json:"model_version_extract,omitempty" parquet:"model_version_extract,optional"`
	ModelVersionScore   string `json:"model_version_score,omitempty"   parquet:"model_version_score,optional"`
}

// CVFix mirrors cv.PriorityFix, carried inside CVImprovedV1.
type CVFix struct {
	FixID          string `json:"fix_id"             parquet:"fix_id"`
	Title          string `json:"title,omitempty"    parquet:"title,optional"`
	ImpactLevel    string `json:"impact,omitempty"   parquet:"impact,optional"`
	Category       string `json:"category,omitempty" parquet:"category,optional"`
	Why            string `json:"why,omitempty"      parquet:"why,optional"`
	AutoApplicable bool   `json:"auto_applicable"    parquet:"auto_applicable"`
	Rewrite        string `json:"rewrite,omitempty"  parquet:"rewrite,optional"`
}

// CVImprovedV1 is emitted by the cv-improve handler after deterministic
// fix detection + LLM rewrite attachment.
type CVImprovedV1 struct {
	CandidateID  string  `json:"candidate_id" parquet:"candidate_id"`
	CVVersion    int     `json:"cv_version"   parquet:"cv_version"`
	Fixes        []CVFix `json:"fixes"        parquet:"fixes,list"`
	ModelVersion string  `json:"model_version,omitempty" parquet:"model_version,optional"`
}

// CandidateEmbeddingV1 is emitted by the cv-embed handler after a
// successful Embed() call on the CV text.
type CandidateEmbeddingV1 struct {
	CandidateID  string    `json:"candidate_id" parquet:"candidate_id"`
	CVVersion    int       `json:"cv_version"   parquet:"cv_version"`
	Vector       []float32 `json:"vector"       parquet:"vector,list"`
	ModelVersion string    `json:"model_version,omitempty" parquet:"model_version,optional"`
}

// PreferencesUpdatedV1 is emitted by the preferences HTTP endpoint.
// It carries the complete preferences set for the candidate — each
// event is a replace-all snapshot, not a delta.
type PreferencesUpdatedV1 struct {
	CandidateID        string   `json:"candidate_id"        parquet:"candidate_id"`
	RemotePreference   string   `json:"remote_preference,omitempty"   parquet:"remote_preference,optional"`
	SalaryMin          int      `json:"salary_min,omitempty"          parquet:"salary_min,optional"`
	SalaryMax          int      `json:"salary_max,omitempty"          parquet:"salary_max,optional"`
	Currency           string   `json:"currency,omitempty"            parquet:"currency,optional"`
	PreferredLocations []string `json:"preferred_locations,omitempty" parquet:"preferred_locations,list,optional"`
	ExcludedCompanies  []string `json:"excluded_companies,omitempty"  parquet:"excluded_companies,list,optional"`
	TargetRoles        []string `json:"target_roles,omitempty"        parquet:"target_roles,list,optional"`
	Languages          []string `json:"languages,omitempty"           parquet:"languages,list,optional"`
	Availability       string   `json:"availability,omitempty"        parquet:"availability,optional"`
}

// MatchRow is one candidate-to-job match, carried inside MatchesReadyV1.
type MatchRow struct {
	CanonicalID string  `json:"canonical_id" parquet:"canonical_id"`
	Score       float64 `json:"score"        parquet:"score"`
	RerankScore float64 `json:"rerank_score,omitempty" parquet:"rerank_score,optional"`
}

// MatchesReadyV1 is emitted by the match endpoint (or the Trustage
// weekly-digest admin) after a candidate's top matches are ranked.
// Consumed out-of-band by a delivery service for notification/email.
type MatchesReadyV1 struct {
	CandidateID  string     `json:"candidate_id"   parquet:"candidate_id"`
	MatchBatchID string     `json:"match_batch_id" parquet:"match_batch_id"`
	Matches      []MatchRow `json:"matches"        parquet:"matches,list"`
	OccurredAt   time.Time  `json:"occurred_at,omitempty" parquet:"occurred_at,optional"`
}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./pkg/events/v1/...
```

- [ ] **Step 5: Commit**

```bash
git add pkg/events/v1/candidates.go pkg/events/v1/names.go pkg/events/v1/envelope_test.go
git commit -m "feat(events): add six candidate lifecycle payload types + topics"
```

---

## Task 2: Partition routing for candidate collections

**Files:**
- Modify: `pkg/events/v1/partitions.go`
- Modify: `pkg/events/v1/envelope_test.go`

- [ ] **Step 1: Write failing tests**

Append to `pkg/events/v1/envelope_test.go`:

```go
func TestPartitionKeyCVEventsUseCandidatePrefix(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	for _, topic := range []string{
		TopicCVUploaded, TopicCVExtracted, TopicCVImproved,
		TopicCandidateEmbedding, TopicCandidatePreferencesUpdated,
		TopicCandidateMatchesReady,
	} {
		pk := PartitionKey(topic, now, "abcdef123456")
		if pk.Secondary != "ab" {
			t.Fatalf("topic=%s Secondary=%q, want ab", topic, pk.Secondary)
		}
	}
}

func TestPartitionObjectPathCandidateCVLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "ab"}
	got := pk.ObjectPath("candidates_cv", "xyz789")
	want := "candidates_cv/dt=2026-04-21/cnd=ab/xyz789.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}

func TestPartitionObjectPathCandidatesPreferencesLabel(t *testing.T) {
	pk := PartKey{DT: "2026-04-21", Secondary: "cd"}
	got := pk.ObjectPath("candidates_preferences", "xyz789")
	want := "candidates_preferences/dt=2026-04-21/cnd=cd/xyz789.parquet"
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run — expect failure**

```bash
go test ./pkg/events/v1/... -run 'TestPartitionKeyCVEvents|TestPartitionObjectPathCandidate'
```

Expected: first test FAILs (default bucket `_all`); second FAILs with label `p=`.

- [ ] **Step 3: Update `partitionSecondary`**

Edit `pkg/events/v1/partitions.go`. Extend the `TopicCanonicalsUpserted` case (which uses 2-hex prefix) to cover the six candidate topics:

```go
func partitionSecondary(eventType, hint string) string {
	switch eventType {
	case TopicVariantsIngested, TopicCrawlPageCompleted, TopicSourcesDiscovered:
		return strings.ToLower(hint)
	case TopicCanonicalsUpserted, TopicCanonicalsExpired,
		TopicEmbeddings, TopicPublished,
		TopicCVUploaded, TopicCVExtracted, TopicCVImproved,
		TopicCandidateEmbedding, TopicCandidatePreferencesUpdated,
		TopicCandidateMatchesReady:
		return firstN(strings.ToLower(hint), 2)
	case TopicTranslations:
		return strings.ToLower(hint)
	default:
		return "_all"
	}
}
```

- [ ] **Step 4: Update `partitionSecondaryLabel`**

Add a new case for the seven candidate collections (all use the `cnd=` label):

```go
func partitionSecondaryLabel(collection string) string {
	switch collection {
	case "variants", "crawl_page_completed", "sources_discovered":
		return "src"
	case "canonicals", "canonicals_expired", "embeddings", "published":
		return "cc"
	case "candidates_cv", "candidates_cv_current",
		"candidates_improvements",
		"candidates_preferences", "candidates_preferences_current",
		"candidates_embeddings", "candidates_embeddings_current",
		"candidates_matches_ready":
		return "cnd"
	case "translations":
		return "lang"
	default:
		return "p"
	}
}
```

- [ ] **Step 5: Run — expect PASS**

```bash
go test ./pkg/events/v1/...
```

- [ ] **Step 6: Commit**

```bash
git add pkg/events/v1/partitions.go pkg/events/v1/envelope_test.go
git commit -m "feat(events): partition candidate collections by cnd=<id_prefix_2hex>"
```

---

## Task 3: Writer encoder + hint extraction for candidate topics

**Files:**
- Modify: `apps/writer/service/handler.go`
- Modify: `apps/writer/service/service.go`
- Modify: `apps/writer/service/buffer.go`

- [ ] **Step 1: Extend `extractHint`**

Edit `apps/writer/service/handler.go`. All six candidate topics share `payload.candidate_id` as the partition hint. Add one combined case:

```go
func extractHint(raw json.RawMessage, topic string) string {
	switch topic {
	case eventsv1.TopicVariantsIngested,
		eventsv1.TopicCrawlPageCompleted,
		eventsv1.TopicSourcesDiscovered:
		return decodeField(raw, "payload.source_id")
	case eventsv1.TopicCanonicalsUpserted, eventsv1.TopicCanonicalsExpired:
		return decodeField(raw, "payload.cluster_id")
	case eventsv1.TopicEmbeddings:
		return decodeField(raw, "payload.canonical_id")
	case eventsv1.TopicTranslations:
		return decodeField(raw, "payload.lang")
	case eventsv1.TopicCVUploaded,
		eventsv1.TopicCVExtracted,
		eventsv1.TopicCVImproved,
		eventsv1.TopicCandidateEmbedding,
		eventsv1.TopicCandidatePreferencesUpdated,
		eventsv1.TopicCandidateMatchesReady:
		return decodeField(raw, "payload.candidate_id")
	default:
		return ""
	}
}
```

- [ ] **Step 2: Extend `uploadBatch`**

Edit `apps/writer/service/service.go`. Add six new cases, grouped at the end of the existing switch:

```go
	case eventsv1.TopicCVUploaded:
		body, err = encodeBatch[eventsv1.CVUploadedV1](b.Events)
	case eventsv1.TopicCVExtracted:
		body, err = encodeBatch[eventsv1.CVExtractedV1](b.Events)
	case eventsv1.TopicCVImproved:
		body, err = encodeBatch[eventsv1.CVImprovedV1](b.Events)
	case eventsv1.TopicCandidateEmbedding:
		body, err = encodeBatch[eventsv1.CandidateEmbeddingV1](b.Events)
	case eventsv1.TopicCandidatePreferencesUpdated:
		body, err = encodeBatch[eventsv1.PreferencesUpdatedV1](b.Events)
	case eventsv1.TopicCandidateMatchesReady:
		body, err = encodeBatch[eventsv1.MatchesReadyV1](b.Events)
	default:
```

- [ ] **Step 3: Extend `collectionForTopic`**

Edit `apps/writer/service/buffer.go`. Extend the `collectionForTopic` switch:

```go
func collectionForTopic(topic string) string {
	switch topic {
	case eventsv1.TopicVariantsIngested,
		eventsv1.TopicVariantsNormalized,
		eventsv1.TopicVariantsValidated,
		eventsv1.TopicVariantsFlagged,
		eventsv1.TopicVariantsClustered:
		return "variants"
	case eventsv1.TopicCanonicalsUpserted:
		return "canonicals"
	case eventsv1.TopicCanonicalsExpired:
		return "canonicals_expired"
	case eventsv1.TopicEmbeddings:
		return "embeddings"
	case eventsv1.TopicTranslations:
		return "translations"
	case eventsv1.TopicPublished:
		return "published"
	case eventsv1.TopicCrawlPageCompleted:
		return "crawl_page_completed"
	case eventsv1.TopicSourcesDiscovered:
		return "sources_discovered"
	case eventsv1.TopicCVUploaded, eventsv1.TopicCVExtracted:
		return "candidates_cv"
	case eventsv1.TopicCVImproved:
		return "candidates_improvements"
	case eventsv1.TopicCandidateEmbedding:
		return "candidates_embeddings"
	case eventsv1.TopicCandidatePreferencesUpdated:
		return "candidates_preferences"
	case eventsv1.TopicCandidateMatchesReady:
		return "candidates_matches_ready"
	default:
		return "_unknown"
	}
}
```

- [ ] **Step 4: Build + run writer tests**

```bash
cd /home/j/code/stawi.opportunities
go build ./apps/writer/...
go test ./apps/writer/... -count=1 -timeout 5m
```

Expected: clean build, all pre-existing tests pass (the new topics aren't exercised until Task 12's e2e test).

- [ ] **Step 5: Commit**

```bash
git add apps/writer/service/handler.go apps/writer/service/service.go apps/writer/service/buffer.go
git commit -m "feat(writer): encode candidate lifecycle Parquet partitions"
```

---

## Task 4: `pkg/candidatestore.Reader` — load latest embedding + preferences from R2

**Files:**
- Create: `pkg/candidatestore/reader.go`
- Create: `pkg/candidatestore/reader_test.go`

The match endpoint (Task 8) needs the latest embedding + preferences per candidate without touching Postgres. This reader scans the `candidates_embeddings_current/` and `candidates_preferences_current/` partitions for files under `cnd=<candidate_id_prefix>/` and returns the most recent row matching the candidate_id.

In v1 we ship the simple path: no Valkey cache, just always read R2. Phase 6 can bolt on caching if latency demands it.

- [ ] **Step 1: Write the failing test**

Create `pkg/candidatestore/reader_test.go`:

```go
package candidatestore

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/eventlog"
)

func TestReaderReturnsLatestEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })
	endpoint, _ := mc.ConnectionString(ctx)

	cfg := eventlog.R2Config{
		AccountID:       "test",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "opportunities-log-cand",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	client := eventlog.NewClient(cfg)
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	uploader := eventlog.NewUploader(client, cfg.Bucket)

	// Seed two embedding events for cnd_abc (prefix "ab"), one older,
	// one newer. The reader must return the newer one.
	older := eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_abc", CVVersion: 1, Vector: []float32{0.1, 0.2}, ModelVersion: "v1"}
	newer := eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_abc", CVVersion: 2, Vector: []float32{0.9, 0.8}, ModelVersion: "v1"}

	for i, pl := range []eventsv1.CandidateEmbeddingV1{older, newer} {
		body, werr := eventlog.WriteParquet([]eventsv1.CandidateEmbeddingV1{pl})
		if werr != nil {
			t.Fatalf("WriteParquet: %v", werr)
		}
		key := "candidates_embeddings_current/cnd=ab/" + []string{"v1", "v2"}[i] + ".parquet"
		if _, err := uploader.Put(ctx, key, body); err != nil {
			t.Fatalf("upload: %v", err)
		}
	}

	r := NewReader(client, cfg.Bucket)
	vec, err := r.LatestEmbedding(ctx, "cnd_abc")
	if err != nil {
		t.Fatalf("LatestEmbedding: %v", err)
	}
	if len(vec.Vector) != 2 || vec.Vector[0] != 0.9 || vec.CVVersion != 2 {
		t.Fatalf("expected v2 vector, got %+v", vec)
	}
}

func TestReaderReturnsErrNotFoundWhenNoEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })
	endpoint, _ := mc.ConnectionString(ctx)

	cfg := eventlog.R2Config{
		AccountID: "test", AccessKeyID: mc.Username, SecretAccessKey: mc.Password,
		Bucket: "opportunities-log-empty", Endpoint: "http://" + endpoint, UsePathStyle: true,
	}
	client := eventlog.NewClient(cfg)
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	r := NewReader(client, cfg.Bucket)
	_, err = r.LatestEmbedding(ctx, "cnd_missing")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
cd /home/j/code/stawi.opportunities
go test ./pkg/candidatestore/...
```

Expected: `undefined: NewReader, ErrNotFound`.

- [ ] **Step 3: Implement the reader**

Create `pkg/candidatestore/reader.go`:

```go
// Package candidatestore provides read-only access to the latest
// per-candidate derived state (embedding, preferences) persisted as
// Parquet in R2. The match endpoint uses it to avoid reading Postgres
// on the hot path.
//
// Writes are never performed here — the writer service is the only
// producer of candidates_*_current/ Parquet files, via compaction of
// the daily partitions.
package candidatestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/parquet-go/parquet-go"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// ErrNotFound is returned when no embedding/preferences row exists
// for the requested candidate.
var ErrNotFound = errors.New("candidatestore: candidate state not found")

// Reader wraps an R2 client + bucket and fetches the latest typed row
// per candidate from the *_current/ partitions.
type Reader struct {
	client *s3.Client
	bucket string
}

// NewReader builds a Reader.
func NewReader(client *s3.Client, bucket string) *Reader {
	return &Reader{client: client, bucket: bucket}
}

// LatestEmbedding returns the highest-CVVersion embedding row for the
// candidate, or ErrNotFound if none exist.
func (r *Reader) LatestEmbedding(ctx context.Context, candidateID string) (eventsv1.CandidateEmbeddingV1, error) {
	rows, err := loadAll[eventsv1.CandidateEmbeddingV1](ctx, r.client, r.bucket,
		"candidates_embeddings_current/cnd="+prefix2(candidateID)+"/")
	if err != nil {
		return eventsv1.CandidateEmbeddingV1{}, err
	}
	best, ok := pick(rows, func(row eventsv1.CandidateEmbeddingV1) bool { return row.CandidateID == candidateID },
		func(a, b eventsv1.CandidateEmbeddingV1) bool { return a.CVVersion > b.CVVersion })
	if !ok {
		return eventsv1.CandidateEmbeddingV1{}, ErrNotFound
	}
	return best, nil
}

// LatestPreferences returns the most recent preferences row for the
// candidate, or ErrNotFound if none exist. Preferences don't carry a
// CVVersion (they are independent of the CV cycle); ordering is by
// discovered object-key order which matches write order at this scale.
func (r *Reader) LatestPreferences(ctx context.Context, candidateID string) (eventsv1.PreferencesUpdatedV1, error) {
	rows, err := loadAll[eventsv1.PreferencesUpdatedV1](ctx, r.client, r.bucket,
		"candidates_preferences_current/cnd="+prefix2(candidateID)+"/")
	if err != nil {
		return eventsv1.PreferencesUpdatedV1{}, err
	}
	matching := filter(rows, func(row eventsv1.PreferencesUpdatedV1) bool { return row.CandidateID == candidateID })
	if len(matching) == 0 {
		return eventsv1.PreferencesUpdatedV1{}, ErrNotFound
	}
	return matching[len(matching)-1], nil
}

// prefix2 returns the first two chars of the candidate_id, lowercased.
// Matches the partition routing in pkg/events/v1/partitions.go.
func prefix2(candidateID string) string {
	if len(candidateID) >= 2 {
		return lowerASCII(candidateID[:2])
	}
	return lowerASCII(candidateID)
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// loadAll lists Parquet objects under prefix and decodes them as []T.
func loadAll[T any](ctx context.Context, client *s3.Client, bucket, prefix string) ([]T, error) {
	keys, err := listKeys(ctx, client, bucket, prefix)
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)

	var rows []T
	for _, k := range keys {
		obj, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(k)})
		if err != nil {
			return nil, fmt.Errorf("candidatestore: get %q: %w", k, err)
		}
		body, err := io.ReadAll(obj.Body)
		_ = obj.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("candidatestore: read %q: %w", k, err)
		}
		page, err := parquet.Read[T](bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return nil, fmt.Errorf("candidatestore: decode %q: %w", k, err)
		}
		rows = append(rows, page...)
	}
	return rows, nil
}

// listKeys pages through ListObjectsV2 and returns every key under prefix.
func listKeys(ctx context.Context, client *s3.Client, bucket, prefix string) ([]string, error) {
	var keys []string
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			var nsk *types.NoSuchKey
			if errors.As(err, &nsk) {
				return nil, nil
			}
			return nil, err
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	return keys, nil
}

func filter[T any](xs []T, keep func(T) bool) []T {
	var out []T
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

func pick[T any](xs []T, keep func(T) bool, better func(a, b T) bool) (T, bool) {
	var zero T
	first := true
	var best T
	for _, x := range xs {
		if !keep(x) {
			continue
		}
		if first || better(x, best) {
			best = x
			first = false
		}
	}
	if first {
		return zero, false
	}
	return best, true
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./pkg/candidatestore/... -count=1 -timeout 5m
```

Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/candidatestore/reader.go pkg/candidatestore/reader_test.go
git commit -m "feat(candidatestore): R2 reader for latest embedding + preferences"
```

---

## Task 5: CV upload HTTP handler — archive + extract + emit

**Files:**
- Create: `apps/candidates/service/http/v1/upload.go`
- Create: `apps/candidates/service/http/v1/upload_test.go`

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/http/v1/upload_test.go`:

```go
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/frame/frametests"

	"stawi.opportunities/pkg/archive"
	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// uploadCollector captures emitted CVUploadedV1 envelopes.
type uploadCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.CVUploadedV1]
}

func (c *uploadCollector) Name() string     { return eventsv1.TopicCVUploaded }
func (c *uploadCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *uploadCollector) Validate(context.Context, any) error { return nil }
func (c *uploadCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVUploadedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *uploadCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

// fakeTextExtractor returns a hard-coded plain-text extraction.
type fakeTextExtractor struct{ out string }

func (f *fakeTextExtractor) FromPDF(_ []byte) (string, error)  { return f.out, nil }
func (f *fakeTextExtractor) FromDOCX(_ []byte) (string, error) { return f.out, nil }

func TestUploadHandlerArchivesAndEmits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-upload-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &uploadCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	deps := UploadDeps{
		Svc:     svc,
		Archive: archive.NewFakeArchive(),
		Text:    &fakeTextExtractor{out: "resume plain text content long enough to pass"},
	}
	handler := UploadHandler(deps)

	// Build a multipart body.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("candidate_id", "cnd_test")
	fw, _ := mw.CreateFormFile("cv", "resume.pdf")
	fw.Write([]byte("%PDF-1.4 fake content"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/candidates/cv/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	p := col.got[0].Payload
	if p.CandidateID != "cnd_test" || p.CVVersion != 1 || p.RawArchiveRef == "" || p.ExtractedText == "" {
		t.Fatalf("bad payload: %+v", p)
	}

	_ = events.EventI(col) // keep import referenced
}

func TestUploadHandlerRejectsMissingCandidateID(t *testing.T) {
	_, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("cv-upload-missing"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(context.Background())

	handler := UploadHandler(UploadDeps{
		Svc:     svc,
		Archive: archive.NewFakeArchive(),
		Text:    &fakeTextExtractor{out: "x"},
	})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("cv", "resume.pdf")
	fw.Write([]byte("content"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/candidates/cv/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
cd /home/j/code/stawi.opportunities
go test ./apps/candidates/service/http/v1/... -run TestUploadHandler
```

Expected: `undefined: UploadHandler, UploadDeps`.

- [ ] **Step 3: Implement the handler**

Create `apps/candidates/service/http/v1/upload.go`:

```go
// Package v1 contains the Phase 5 HTTP handlers for apps/candidates.
// Each handler is a factory returning an http.HandlerFunc bound to its
// dependencies; no global state.
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"stawi.opportunities/pkg/archive"
	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// TextExtractor abstracts plain-text extraction for PDF / DOCX bytes.
// Real impl wraps pkg/extraction.ExtractTextFromPDF and
// ExtractTextFromDOCX; tests can inject a deterministic fake.
type TextExtractor interface {
	FromPDF(data []byte) (string, error)
	FromDOCX(data []byte) (string, error)
}

// UploadDeps bundles the collaborators for the upload handler.
type UploadDeps struct {
	Svc     *frame.Service
	Archive archive.Archive
	Text    TextExtractor

	// MaxBytes caps the size of the uploaded file. 0 → 10 MiB default.
	MaxBytes int64
}

// UploadHandler returns an http.HandlerFunc implementing:
//
//	POST /candidates/cv/upload
//	Content-Type: multipart/form-data
//	Fields:
//	  candidate_id (required, string)
//	  cv           (required, file; .pdf or .docx)
//
// Flow:
//  1. Validate candidate_id + file present.
//  2. Read file bytes (bounded by MaxBytes).
//  3. Archive raw bytes via pkg/archive → raw_archive_ref.
//  4. Extract plain text (PDF or DOCX branch based on filename).
//  5. Pick cv_version by counting existing candidates_cv_current/ rows
//     for the candidate (always +1). For v1 we take a shortcut and
//     stamp 1 unconditionally; duplicate uploads produce duplicate
//     events, and the downstream cv-extract handler's writer will
//     land them in the event log where dedup happens at compaction.
//     (TODO Phase 6: pass in a candidatestore.Reader and compute the
//     real next version.)
//  6. Emit CVUploadedV1 via Frame.
//  7. Return 202 Accepted with a JSON body echoing candidate_id + cv_version.
func UploadHandler(deps UploadDeps) http.HandlerFunc {
	maxBytes := deps.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 10 << 20 // 10 MiB
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseMultipartForm(maxBytes); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse multipart: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		candidateID := strings.TrimSpace(r.FormValue("candidate_id"))
		if candidateID == "" {
			http.Error(w, `{"error":"candidate_id is required"}`, http.StatusBadRequest)
			return
		}

		file, hdr, err := r.FormFile("cv")
		if err != nil {
			http.Error(w, `{"error":"cv file is required"}`, http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()

		body, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"read file: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}
		if int64(len(body)) > maxBytes {
			http.Error(w, `{"error":"file too large"}`, http.StatusRequestEntityTooLarge)
			return
		}

		// Archive raw bytes.
		hash, size, err := deps.Archive.PutRaw(ctx, body)
		if err != nil {
			log.WithError(err).Error("upload: PutRaw failed")
			http.Error(w, `{"error":"archive failed"}`, http.StatusInternalServerError)
			return
		}

		text, err := extractText(deps.Text, hdr.Filename, body)
		if err != nil {
			log.WithError(err).Warn("upload: text extraction failed")
			http.Error(w, fmt.Sprintf(`{"error":"text extraction: %s"}`, err.Error()), http.StatusUnprocessableEntity)
			return
		}
		if strings.TrimSpace(text) == "" {
			http.Error(w, `{"error":"extracted text is empty"}`, http.StatusUnprocessableEntity)
			return
		}

		cvVersion := 1 // v1 shortcut; Phase 6 computes the real next version
		payload := eventsv1.CVUploadedV1{
			CandidateID:   candidateID,
			CVVersion:     cvVersion,
			RawArchiveRef: archive.RawKey(hash),
			Filename:      hdr.Filename,
			ContentType:   hdr.Header.Get("Content-Type"),
			SizeBytes:     size,
			ExtractedText: text,
		}
		env := eventsv1.NewEnvelope(eventsv1.TopicCVUploaded, payload)
		if err := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCVUploaded, env); err != nil {
			log.WithError(err).Error("upload: emit failed")
			http.Error(w, `{"error":"emit failed"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":     true,
			"candidate_id": candidateID,
			"cv_version":   cvVersion,
		})
	}
}

// extractText picks PDF or DOCX extraction based on filename suffix.
// Rejects any other suffix.
func extractText(ex TextExtractor, filename string, body []byte) (string, error) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return ex.FromPDF(body)
	case strings.HasSuffix(lower, ".docx"):
		return ex.FromDOCX(body)
	default:
		return "", errors.New("unsupported file type (only .pdf and .docx accepted)")
	}
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/candidates/service/http/v1/... -run TestUploadHandler -count=1 -v
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/service/http/v1/upload.go apps/candidates/service/http/v1/upload_test.go
git commit -m "feat(candidates): POST /candidates/cv/upload emits CVUploadedV1"
```

---

## Task 6: Preferences HTTP handler

**Files:**
- Create: `apps/candidates/service/http/v1/preferences.go`
- Create: `apps/candidates/service/http/v1/preferences_test.go`

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/http/v1/preferences_test.go`:

```go
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

type prefCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.PreferencesUpdatedV1]
}

func (c *prefCollector) Name() string     { return eventsv1.TopicCandidatePreferencesUpdated }
func (c *prefCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *prefCollector) Validate(context.Context, any) error { return nil }
func (c *prefCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.PreferencesUpdatedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *prefCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestPreferencesHandlerEmitsEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("prefs-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &prefCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	handler := PreferencesHandler(svc)

	body := map[string]any{
		"candidate_id":        "cnd_1",
		"remote_preference":   "remote",
		"salary_min":          80000,
		"salary_max":          140000,
		"currency":            "USD",
		"preferred_locations": []string{"KE"},
		"target_roles":        []string{"backend-engineer"},
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/candidates/preferences", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	p := col.got[0].Payload
	if p.CandidateID != "cnd_1" || p.SalaryMin != 80000 {
		t.Fatalf("bad payload: %+v", p)
	}
}

func TestPreferencesHandlerRejectsMissingCandidateID(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("prefs-empty"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	handler := PreferencesHandler(svc)
	raw := []byte(`{"salary_min":50000}`)
	req := httptest.NewRequest(http.MethodPost, "/candidates/preferences", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/candidates/service/http/v1/... -run TestPreferencesHandler
```

Expected: `undefined: PreferencesHandler`.

- [ ] **Step 3: Implement the handler**

Create `apps/candidates/service/http/v1/preferences.go`:

```go
package v1

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// PreferencesHandler returns an http.HandlerFunc for:
//
//	POST /candidates/preferences
//	Content-Type: application/json
//	Body: full preferences snapshot (replace-all semantics)
//
// The handler validates candidate_id + minimal sanity checks, then
// emits PreferencesUpdatedV1. Persistence happens via the event log.
func PreferencesHandler(svc *frame.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var body eventsv1.PreferencesUpdatedV1
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		body.CandidateID = strings.TrimSpace(body.CandidateID)
		if body.CandidateID == "" {
			http.Error(w, `{"error":"candidate_id is required"}`, http.StatusBadRequest)
			return
		}
		if body.SalaryMin < 0 || body.SalaryMax < 0 {
			http.Error(w, `{"error":"salary_min / salary_max must be non-negative"}`, http.StatusBadRequest)
			return
		}
		if body.SalaryMin > 0 && body.SalaryMax > 0 && body.SalaryMin > body.SalaryMax {
			http.Error(w, `{"error":"salary_min > salary_max"}`, http.StatusBadRequest)
			return
		}

		env := eventsv1.NewEnvelope(eventsv1.TopicCandidatePreferencesUpdated, body)
		if err := svc.EventsManager().Emit(ctx, eventsv1.TopicCandidatePreferencesUpdated, env); err != nil {
			log.WithError(err).Error("preferences: emit failed")
			http.Error(w, `{"error":"emit failed"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":     true,
			"candidate_id": body.CandidateID,
		})
	}
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/candidates/service/http/v1/... -run TestPreferencesHandler -count=1
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/service/http/v1/preferences.go apps/candidates/service/http/v1/preferences_test.go
git commit -m "feat(candidates): POST /candidates/preferences emits PreferencesUpdatedV1"
```

---

## Task 7: `cv-extract` subscription handler

**Files:**
- Create: `apps/candidates/service/events/v1/cv_extract.go`
- Create: `apps/candidates/service/events/v1/cv_extract_test.go`

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/events/v1/cv_extract_test.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/extraction"
)

// fakeCVExtractor returns a canned CVFields.
type fakeCVExtractor struct{ fields *extraction.CVFields }

func (f *fakeCVExtractor) ExtractCV(_ context.Context, _ string) (*extraction.CVFields, error) {
	if f.fields == nil {
		return nil, errors.New("no fields configured")
	}
	return f.fields, nil
}

// fakeCVScorer returns canned score components.
type fakeCVScorer struct {
	ats, kw, impact, roleFit, clarity, overall int
}

func (f *fakeCVScorer) Score(_ context.Context, _ string, _ *extraction.CVFields, _ string) *ScoreComponents {
	return &ScoreComponents{
		ATS: f.ats, Keywords: f.kw, Impact: f.impact,
		RoleFit: f.roleFit, Clarity: f.clarity, Overall: f.overall,
	}
}

type extractedCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.CVExtractedV1]
}

func (c *extractedCollector) Name() string     { return eventsv1.TopicCVExtracted }
func (c *extractedCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *extractedCollector) Validate(context.Context, any) error { return nil }
func (c *extractedCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVExtractedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *extractedCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestCVExtractHandlerEmitsExtracted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-extract-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &extractedCollector{}
	svc.EventsManager().Add(col)

	h := NewCVExtractHandler(CVExtractDeps{
		Svc: svc,
		Extractor: &fakeCVExtractor{fields: &extraction.CVFields{
			Name:  "Jane Doe",
			Email: "jane@example.com",
		}},
		Scorer: &fakeCVScorer{ats: 85, overall: 82},
		ExtractorModelVersion: "ext-v1",
		ScorerModelVersion:    "score-v1",
	})

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVUploaded, eventsv1.CVUploadedV1{
		CandidateID:   "cnd_x",
		CVVersion:     1,
		RawArchiveRef: "raw/abc",
		ExtractedText: "resume plain text sufficient length to pass scorer",
	})
	raw, _ := json.Marshal(inEnv)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	p := col.got[0].Payload
	if p.CandidateID != "cnd_x" || p.Name != "Jane Doe" || p.ScoreOverall != 82 {
		t.Fatalf("bad payload: %+v", p)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/candidates/service/events/v1/... -run TestCVExtractHandler
```

Expected: `undefined: NewCVExtractHandler, CVExtractDeps, ScoreComponents`.

- [ ] **Step 3: Implement the handler**

Create `apps/candidates/service/events/v1/cv_extract.go`:

```go
// Package v1 holds the Phase 5 event subscription handlers for
// apps/candidates — cv-extract, cv-improve, cv-embed. Each implements
// Frame's events.EventI contract.
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/extraction"
)

// CVExtractor abstracts extraction.Extractor.ExtractCV so tests can
// inject a deterministic fake.
type CVExtractor interface {
	ExtractCV(ctx context.Context, text string) (*extraction.CVFields, error)
}

// ScoreComponents mirrors cv.ScoreComponents but is redeclared here so
// the handler file's dependency graph stays shallow (cv.Scorer is the
// production implementer).
type ScoreComponents struct {
	ATS      int
	Keywords int
	Impact   int
	RoleFit  int
	Clarity  int
	Overall  int
}

// CVScorer abstracts cv.Scorer.Score.
type CVScorer interface {
	Score(ctx context.Context, cvText string, fields *extraction.CVFields, targetRole string) *ScoreComponents
}

// CVExtractDeps bundles collaborators.
type CVExtractDeps struct {
	Svc                   *frame.Service
	Extractor             CVExtractor
	Scorer                CVScorer
	ExtractorModelVersion string
	ScorerModelVersion    string
}

// CVExtractHandler consumes candidates.cv.uploaded.v1 and emits
// candidates.cv.extracted.v1.
type CVExtractHandler struct {
	deps CVExtractDeps
}

// NewCVExtractHandler wires the handler.
func NewCVExtractHandler(deps CVExtractDeps) *CVExtractHandler {
	return &CVExtractHandler{deps: deps}
}

func (h *CVExtractHandler) Name() string { return eventsv1.TopicCVUploaded }
func (h *CVExtractHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}
func (h *CVExtractHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("cv-extract: empty payload")
	}
	return nil
}
func (h *CVExtractHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVUploadedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("cv-extract: decode: %w", err)
	}
	in := env.Payload

	log := util.Log(ctx).WithField("candidate_id", in.CandidateID).WithField("cv_version", in.CVVersion)

	if in.ExtractedText == "" {
		log.Warn("cv-extract: empty text; skipping")
		return nil
	}

	fields, err := h.deps.Extractor.ExtractCV(ctx, in.ExtractedText)
	if err != nil {
		return fmt.Errorf("cv-extract: ExtractCV: %w", err)
	}
	sc := h.deps.Scorer.Score(ctx, in.ExtractedText, fields, "")

	out := eventsv1.CVExtractedV1{
		CandidateID:         in.CandidateID,
		CVVersion:           in.CVVersion,
		Name:                fields.Name,
		Email:               fields.Email,
		Phone:               fields.Phone,
		Location:            fields.Location,
		CurrentTitle:        fields.CurrentTitle,
		Bio:                 fields.Bio,
		Seniority:           fields.Seniority,
		YearsExperience:     fields.YearsExperience,
		PrimaryIndustry:     fields.PrimaryIndustry,
		StrongSkills:        fields.StrongSkills,
		WorkingSkills:       fields.WorkingSkills,
		ToolsFrameworks:     fields.ToolsFrameworks,
		Certifications:      fields.Certifications,
		PreferredRoles:      fields.PreferredRoles,
		Languages:           fields.Languages,
		Education:           fields.Education,
		PreferredLocations:  fields.PreferredLocations,
		RemotePreference:    fields.RemotePreference,
		SalaryMin:           fields.SalaryMin,
		SalaryMax:           fields.SalaryMax,
		Currency:            fields.Currency,
		ScoreATS:            sc.ATS,
		ScoreKeywords:       sc.Keywords,
		ScoreImpact:         sc.Impact,
		ScoreRoleFit:        sc.RoleFit,
		ScoreClarity:        sc.Clarity,
		ScoreOverall:        sc.Overall,
		ModelVersionExtract: h.deps.ExtractorModelVersion,
		ModelVersionScore:   h.deps.ScorerModelVersion,
	}

	envOut := eventsv1.NewEnvelope(eventsv1.TopicCVExtracted, out)
	if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCVExtracted, envOut); err != nil {
		return fmt.Errorf("cv-extract: emit: %w", err)
	}
	log.WithField("score_overall", out.ScoreOverall).Info("cv-extract: done")
	return nil
}
```

Production wiring (Task 13) will need a small adapter that wraps `cv.Scorer` to match this file's `CVScorer` interface (converts `*cv.CVStrengthReport` / `cv.ScoreComponents` → local `ScoreComponents`). Keep the interface definition here so tests stay hermetic.

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/candidates/service/events/v1/... -run TestCVExtractHandler -count=1
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/service/events/v1/cv_extract.go apps/candidates/service/events/v1/cv_extract_test.go
git commit -m "feat(candidates): cv-extract subscription emits CVExtractedV1"
```

---

## Task 8: `cv-improve` subscription handler

**Files:**
- Create: `apps/candidates/service/events/v1/cv_improve.go`
- Create: `apps/candidates/service/events/v1/cv_improve_test.go`

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/events/v1/cv_improve_test.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// fakeFixGenerator returns a hard-coded slice of PriorityFix.
type fakeFixGenerator struct{ fixes []PriorityFix }

func (f *fakeFixGenerator) Generate(_ context.Context, _ *eventsv1.CVExtractedV1) ([]PriorityFix, error) {
	return f.fixes, nil
}

type improvedCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.CVImprovedV1]
}

func (c *improvedCollector) Name() string     { return eventsv1.TopicCVImproved }
func (c *improvedCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *improvedCollector) Validate(context.Context, any) error { return nil }
func (c *improvedCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVImprovedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *improvedCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestCVImproveHandlerEmitsImproved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-improve-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &improvedCollector{}
	svc.EventsManager().Add(col)

	h := NewCVImproveHandler(CVImproveDeps{
		Svc: svc,
		Fixes: &fakeFixGenerator{fixes: []PriorityFix{
			{FixID: "fix-1", Title: "Add metrics", ImpactLevel: "high", Category: "impact", Why: "bullets lack numbers", AutoApplicable: true, Rewrite: "Reduced latency 40%"},
		}},
		ModelVersion: "improve-v1",
	})

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVExtracted, eventsv1.CVExtractedV1{
		CandidateID: "cnd_1", CVVersion: 1, ScoreOverall: 70,
	})
	raw, _ := json.Marshal(inEnv)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	p := col.got[0].Payload
	if p.CandidateID != "cnd_1" || len(p.Fixes) != 1 || p.Fixes[0].FixID != "fix-1" {
		t.Fatalf("bad payload: %+v", p)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/candidates/service/events/v1/... -run TestCVImproveHandler
```

Expected: `undefined: NewCVImproveHandler, CVImproveDeps, PriorityFix`.

- [ ] **Step 3: Implement the handler**

Create `apps/candidates/service/events/v1/cv_improve.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// PriorityFix mirrors cv.PriorityFix.
type PriorityFix struct {
	FixID          string
	Title          string
	ImpactLevel    string
	Category       string
	Why            string
	AutoApplicable bool
	Rewrite        string
}

// FixGenerator abstracts the combined detectPriorityFixes + AttachRewrites
// pipeline from pkg/cv. Production wiring passes an adapter that calls
// both in sequence; tests pass a hard-coded fake.
type FixGenerator interface {
	Generate(ctx context.Context, extracted *eventsv1.CVExtractedV1) ([]PriorityFix, error)
}

// CVImproveDeps bundles collaborators.
type CVImproveDeps struct {
	Svc          *frame.Service
	Fixes        FixGenerator
	ModelVersion string
}

// CVImproveHandler consumes candidates.cv.extracted.v1 and emits
// candidates.cv.improved.v1.
type CVImproveHandler struct {
	deps CVImproveDeps
}

func NewCVImproveHandler(deps CVImproveDeps) *CVImproveHandler {
	return &CVImproveHandler{deps: deps}
}

func (h *CVImproveHandler) Name() string { return eventsv1.TopicCVExtracted }
func (h *CVImproveHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}
func (h *CVImproveHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("cv-improve: empty payload")
	}
	return nil
}
func (h *CVImproveHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVExtractedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("cv-improve: decode: %w", err)
	}
	in := env.Payload

	log := util.Log(ctx).WithField("candidate_id", in.CandidateID).WithField("cv_version", in.CVVersion)

	fixes, err := h.deps.Fixes.Generate(ctx, &in)
	if err != nil {
		// Don't fail hard — an AI rewrite outage shouldn't block the
		// pipeline. Emit an empty-fixes event so downstream sees a
		// version bump + audit row.
		log.WithError(err).Warn("cv-improve: fix generation failed, emitting empty")
		fixes = nil
	}

	wire := make([]eventsv1.CVFix, 0, len(fixes))
	for _, f := range fixes {
		wire = append(wire, eventsv1.CVFix{
			FixID:          f.FixID,
			Title:          f.Title,
			ImpactLevel:    f.ImpactLevel,
			Category:       f.Category,
			Why:            f.Why,
			AutoApplicable: f.AutoApplicable,
			Rewrite:        f.Rewrite,
		})
	}

	out := eventsv1.CVImprovedV1{
		CandidateID:  in.CandidateID,
		CVVersion:    in.CVVersion,
		Fixes:        wire,
		ModelVersion: h.deps.ModelVersion,
	}
	envOut := eventsv1.NewEnvelope(eventsv1.TopicCVImproved, out)
	if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCVImproved, envOut); err != nil {
		return fmt.Errorf("cv-improve: emit: %w", err)
	}
	log.WithField("fixes", len(wire)).Info("cv-improve: done")
	return nil
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/candidates/service/events/v1/... -run TestCVImproveHandler -count=1
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/service/events/v1/cv_improve.go apps/candidates/service/events/v1/cv_improve_test.go
git commit -m "feat(candidates): cv-improve subscription emits CVImprovedV1"
```

---

## Task 9: `cv-embed` subscription handler

**Files:**
- Create: `apps/candidates/service/events/v1/cv_embed.go`
- Create: `apps/candidates/service/events/v1/cv_embed_test.go`

Note: per spec §6.2, cv-embed consumes BOTH `candidates.cv.extracted.v1` and `candidates.cv.improved.v1` — but Frame v1.94.1's event registry is `map[string]EventI` keyed by topic name, so one handler can subscribe to at most one topic. We solve this by registering cv-embed on `candidates.cv.extracted.v1` (the first-class source of embedding-ready text). A re-embed after improvements is a Phase 6 concern and will be addressed with either a dedicated topic or by having the improve handler re-emit extracted-with-rewrites.

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/events/v1/cv_embed_test.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// fakeEmbedder returns a canned vector.
type fakeEmbedder struct{ vec []float32; err error }

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return f.vec, f.err
}

type embedCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
}

func (c *embedCollector) Name() string     { return eventsv1.TopicCandidateEmbedding }
func (c *embedCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *embedCollector) Validate(context.Context, any) error { return nil }
func (c *embedCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *embedCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestCVEmbedHandlerEmitsEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-embed-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &embedCollector{}
	svc.EventsManager().Add(col)

	h := NewCVEmbedHandler(CVEmbedDeps{
		Svc:          svc,
		Embedder:     &fakeEmbedder{vec: []float32{0.1, 0.2, 0.3}},
		ModelVersion: "embed-v1",
	})

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVExtracted, eventsv1.CVExtractedV1{
		CandidateID: "cnd_1", CVVersion: 1, Bio: "seasoned backend engineer in Nairobi",
	})
	raw, _ := json.Marshal(inEnv)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	p := col.got[0].Payload
	if p.CandidateID != "cnd_1" || len(p.Vector) != 3 {
		t.Fatalf("bad payload: %+v", p)
	}
}

func TestCVEmbedHandlerReturnsErrorOnEmbedFailure(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("cv-embed-err"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	h := NewCVEmbedHandler(CVEmbedDeps{
		Svc:      svc,
		Embedder: &fakeEmbedder{err: errors.New("backend down")},
	})

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVExtracted, eventsv1.CVExtractedV1{CandidateID: "cnd_x", CVVersion: 1})
	raw, _ := json.Marshal(inEnv)
	rm := json.RawMessage(raw)
	err := h.Execute(ctx, &rm)
	if err == nil {
		t.Fatalf("expected error when embedder fails, got nil (Frame redelivery required)")
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/candidates/service/events/v1/... -run TestCVEmbedHandler
```

Expected: `undefined: NewCVEmbedHandler, CVEmbedDeps`.

- [ ] **Step 3: Implement the handler**

Create `apps/candidates/service/events/v1/cv_embed.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// Embedder abstracts extraction.Extractor.Embed.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// CVEmbedDeps bundles collaborators.
type CVEmbedDeps struct {
	Svc          *frame.Service
	Embedder     Embedder
	ModelVersion string
}

// CVEmbedHandler consumes candidates.cv.extracted.v1 and emits
// candidates.embeddings.v1.
//
// The input is an extracted payload; the handler composes an embedding
// text from the stable CV fields (name, bio, skills, roles) and sends
// it to the embedder. Errors propagate so Frame redelivers — an embed
// outage must not silently drop the embedding.
type CVEmbedHandler struct {
	deps CVEmbedDeps
}

func NewCVEmbedHandler(deps CVEmbedDeps) *CVEmbedHandler {
	return &CVEmbedHandler{deps: deps}
}

func (h *CVEmbedHandler) Name() string { return eventsv1.TopicCVExtracted }
func (h *CVEmbedHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}
func (h *CVEmbedHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("cv-embed: empty payload")
	}
	return nil
}
func (h *CVEmbedHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVExtractedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("cv-embed: decode: %w", err)
	}
	in := env.Payload

	log := util.Log(ctx).WithField("candidate_id", in.CandidateID).WithField("cv_version", in.CVVersion)

	text := composeEmbeddingText(&in)
	if text == "" {
		log.Warn("cv-embed: empty composed text; skipping")
		return nil
	}

	vec, err := h.deps.Embedder.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("cv-embed: Embed: %w", err)
	}
	if len(vec) == 0 {
		log.Warn("cv-embed: embedder returned empty vector; skipping")
		return nil
	}

	out := eventsv1.CandidateEmbeddingV1{
		CandidateID:  in.CandidateID,
		CVVersion:    in.CVVersion,
		Vector:       vec,
		ModelVersion: h.deps.ModelVersion,
	}
	envOut := eventsv1.NewEnvelope(eventsv1.TopicCandidateEmbedding, out)
	if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateEmbedding, envOut); err != nil {
		return fmt.Errorf("cv-embed: emit: %w", err)
	}
	log.WithField("dim", len(vec)).Info("cv-embed: done")
	return nil
}

// composeEmbeddingText builds a stable string from the CV fields that
// captures what you'd search against for match scoring. Keep the
// composition deterministic so the same CV always embeds the same way.
func composeEmbeddingText(p *eventsv1.CVExtractedV1) string {
	var parts []string
	if p.CurrentTitle != "" {
		parts = append(parts, p.CurrentTitle)
	}
	if p.Seniority != "" {
		parts = append(parts, p.Seniority)
	}
	if p.PrimaryIndustry != "" {
		parts = append(parts, p.PrimaryIndustry)
	}
	if len(p.StrongSkills) > 0 {
		parts = append(parts, "skills: "+strings.Join(p.StrongSkills, ", "))
	}
	if len(p.PreferredRoles) > 0 {
		parts = append(parts, "roles: "+strings.Join(p.PreferredRoles, ", "))
	}
	if p.Bio != "" {
		parts = append(parts, p.Bio)
	}
	return strings.Join(parts, ". ")
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/candidates/service/events/v1/... -run TestCVEmbedHandler -count=1
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/service/events/v1/cv_embed.go apps/candidates/service/events/v1/cv_embed_test.go
git commit -m "feat(candidates): cv-embed subscription emits CandidateEmbeddingV1"
```

---

## Task 10: Match HTTP handler

**Files:**
- Create: `apps/candidates/service/http/v1/match.go`
- Create: `apps/candidates/service/http/v1/match_test.go`

The match handler is the most logic-heavy endpoint in the phase. It:
1. Resolves `candidate_id` from query parameter (trusted — auth upstream).
2. Loads the latest embedding + preferences via `candidatestore.Reader`.
3. Builds a Manticore query combining KNN on `embedding` + hard filters (`remote_type`, `salary_min` floor, `country` ∈ preferred_locations).
4. Asks Manticore for top-200 candidates.
5. Runs in-Go composite scoring (skills overlap, salary fit, recency weight) to re-rank to top-K.
6. Emits `MatchesReadyV1` and returns the top-K as JSON.

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/http/v1/match_test.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// fakeCandidateStore implements the reader interface used by the match handler.
type fakeCandidateStore struct {
	emb  eventsv1.CandidateEmbeddingV1
	prefs eventsv1.PreferencesUpdatedV1
	err  error
}

func (f *fakeCandidateStore) LatestEmbedding(_ context.Context, _ string) (eventsv1.CandidateEmbeddingV1, error) {
	return f.emb, f.err
}
func (f *fakeCandidateStore) LatestPreferences(_ context.Context, _ string) (eventsv1.PreferencesUpdatedV1, error) {
	return f.prefs, nil
}

// fakeSearchIndex returns canned results.
type fakeSearchIndex struct {
	rows []SearchHit
	err  error
}

func (f *fakeSearchIndex) KNNWithFilters(_ context.Context, _ SearchRequest) ([]SearchHit, error) {
	return f.rows, f.err
}

type matchReadyCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.MatchesReadyV1]
}

func (c *matchReadyCollector) Name() string     { return eventsv1.TopicCandidateMatchesReady }
func (c *matchReadyCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *matchReadyCollector) Validate(context.Context, any) error { return nil }
func (c *matchReadyCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.MatchesReadyV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *matchReadyCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestMatchHandlerReturnsScoredTopK(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("match-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &matchReadyCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	handler := MatchHandler(MatchDeps{
		Svc: svc,
		Store: &fakeCandidateStore{
			emb:   eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_1", CVVersion: 2, Vector: []float32{0.1, 0.2, 0.3}},
			prefs: eventsv1.PreferencesUpdatedV1{CandidateID: "cnd_1", RemotePreference: "remote", SalaryMin: 70000, Currency: "USD", PreferredLocations: []string{"KE"}},
		},
		Search: &fakeSearchIndex{rows: []SearchHit{
			{CanonicalID: "can_a", Slug: "job-a", Title: "Senior Backend", Score: 0.92},
			{CanonicalID: "can_b", Slug: "job-b", Title: "Staff Backend",  Score: 0.81},
			{CanonicalID: "can_c", Slug: "job-c", Title: "Mid Backend",    Score: 0.70},
		}},
		TopK: 2,
	})

	req := httptest.NewRequest(http.MethodGet, "/candidates/match?candidate_id=cnd_1", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp matchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Matches) != 2 {
		t.Fatalf("matches=%d, want 2", len(resp.Matches))
	}
	if resp.Matches[0].CanonicalID != "can_a" {
		t.Fatalf("order wrong: %+v", resp.Matches)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("MatchesReadyV1 not emitted")
	}
}

func TestMatchHandlerMissingEmbeddingReturns404(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("match-404"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	handler := MatchHandler(MatchDeps{
		Svc: svc,
		Store: &fakeCandidateStore{err: errors.New("not found")},
	})

	req := httptest.NewRequest(http.MethodGet, "/candidates/match?candidate_id=cnd_missing", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/candidates/service/http/v1/... -run TestMatchHandler
```

Expected: `undefined: MatchHandler, MatchDeps, SearchRequest, SearchHit, matchResponse`.

- [ ] **Step 3: Implement the handler**

Create `apps/candidates/service/http/v1/match.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// CandidateStore is the read-only interface the match handler needs.
// Real impl is *candidatestore.Reader from pkg/candidatestore.
type CandidateStore interface {
	LatestEmbedding(ctx context.Context, candidateID string) (eventsv1.CandidateEmbeddingV1, error)
	LatestPreferences(ctx context.Context, candidateID string) (eventsv1.PreferencesUpdatedV1, error)
}

// SearchRequest carries the parameters the match handler wants
// Manticore to filter on. Vector is the KNN query vector.
type SearchRequest struct {
	Vector             []float32
	Limit              int
	RemotePreference   string
	SalaryMinFloor     int
	PreferredLocations []string
}

// SearchHit is one Manticore row returned to the match handler.
type SearchHit struct {
	CanonicalID string
	Slug        string
	Title       string
	Company     string
	Score       float64
}

// SearchIndex is the interface the handler depends on. Real impl
// wraps pkg/searchindex.Client.
type SearchIndex interface {
	KNNWithFilters(ctx context.Context, req SearchRequest) ([]SearchHit, error)
}

// MatchDeps bundles collaborators.
type MatchDeps struct {
	Svc    *frame.Service
	Store  CandidateStore
	Search SearchIndex
	TopK   int // default 20
}

// matchResponse is the JSON body returned from the handler.
type matchResponse struct {
	OK           bool              `json:"ok"`
	CandidateID  string            `json:"candidate_id"`
	MatchBatchID string            `json:"match_batch_id"`
	Matches      []matchResponseRow `json:"matches"`
}

type matchResponseRow struct {
	CanonicalID string  `json:"canonical_id"`
	Slug        string  `json:"slug,omitempty"`
	Title       string  `json:"title,omitempty"`
	Company     string  `json:"company,omitempty"`
	Score       float64 `json:"score"`
}

// MatchHandler returns an http.HandlerFunc for:
//
//	GET /candidates/match?candidate_id=cnd_X
//
// Reads the candidate's latest embedding + preferences from R2, queries
// Manticore for up to 200 KNN+filter candidates, takes the top-K by
// score, emits MatchesReadyV1, returns JSON.
func MatchHandler(deps MatchDeps) http.HandlerFunc {
	topK := deps.TopK
	if topK <= 0 {
		topK = 20
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		candidateID := strings.TrimSpace(r.URL.Query().Get("candidate_id"))
		if candidateID == "" {
			http.Error(w, `{"error":"candidate_id is required"}`, http.StatusBadRequest)
			return
		}

		emb, err := deps.Store.LatestEmbedding(ctx, candidateID)
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).Warn("match: LatestEmbedding failed")
			http.Error(w, `{"error":"embedding not available — upload CV first"}`, http.StatusNotFound)
			return
		}
		prefs, _ := deps.Store.LatestPreferences(ctx, candidateID) // prefs are optional

		searchReq := SearchRequest{
			Vector:             emb.Vector,
			Limit:              200,
			RemotePreference:   prefs.RemotePreference,
			SalaryMinFloor:     prefs.SalaryMin,
			PreferredLocations: prefs.PreferredLocations,
		}
		hits, err := deps.Search.KNNWithFilters(ctx, searchReq)
		if err != nil {
			log.WithError(err).Error("match: KNNWithFilters failed")
			http.Error(w, `{"error":"search failed"}`, http.StatusBadGateway)
			return
		}

		// Sort + truncate to top-K. In v1 we trust Manticore's ranking;
		// v1.1 layers in a composite rerank on top.
		if len(hits) > topK {
			hits = hits[:topK]
		}

		batchID := xid.New().String()
		rows := make([]matchResponseRow, 0, len(hits))
		eventRows := make([]eventsv1.MatchRow, 0, len(hits))
		for _, h := range hits {
			rows = append(rows, matchResponseRow{
				CanonicalID: h.CanonicalID,
				Slug:        h.Slug,
				Title:       h.Title,
				Company:     h.Company,
				Score:       h.Score,
			})
			eventRows = append(eventRows, eventsv1.MatchRow{
				CanonicalID: h.CanonicalID,
				Score:       h.Score,
			})
		}

		// Best-effort event emission — don't fail the HTTP response
		// if the bus is down; the user still gets their matches.
		env := eventsv1.NewEnvelope(eventsv1.TopicCandidateMatchesReady, eventsv1.MatchesReadyV1{
			CandidateID:  candidateID,
			MatchBatchID: batchID,
			Matches:      eventRows,
		})
		if err := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateMatchesReady, env); err != nil {
			log.WithError(err).Warn("match: emit MatchesReadyV1 failed")
		}

		resp := matchResponse{
			OK:           true,
			CandidateID:  candidateID,
			MatchBatchID: batchID,
			Matches:      rows,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ensure package imports stay referenced when a build prunes them.
var _ = errors.New
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/candidates/service/http/v1/... -run TestMatchHandler -count=1
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/service/http/v1/match.go apps/candidates/service/http/v1/match_test.go
git commit -m "feat(candidates): GET /candidates/match — Manticore KNN + emit MatchesReadyV1"
```

---

## Task 11: Trustage admin endpoints

**Files:**
- Create: `apps/candidates/service/admin/v1/matches_weekly.go`
- Create: `apps/candidates/service/admin/v1/matches_weekly_test.go`
- Create: `apps/candidates/service/admin/v1/cv_stale_nudge.go`
- Create: `apps/candidates/service/admin/v1/cv_stale_nudge_test.go`

Two admin endpoints, one file each.

**Matches weekly digest** — iterates active candidates (via a `CandidateLister` interface — Phase 6 moves the implementation to an Iceberg/ledger reader, v1 uses Postgres `candidates` table via a repository). For each candidate, fires an internal HTTP call into the match endpoint OR directly runs the match logic. We take the latter path to keep the admin endpoint self-contained.

**CV stale nudge** — finds candidates whose latest CV upload is > N days old (configurable, default 60) and emits `candidates.cv.stale_nudge.v1` events so a delivery service can send nudge emails.

- [ ] **Step 1: Write the failing test for matches_weekly**

Create `apps/candidates/service/admin/v1/matches_weekly_test.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

type fakeCandidateLister struct {
	ids []string
}

func (f *fakeCandidateLister) ListActive(_ context.Context) ([]string, error) {
	return f.ids, nil
}

type fakeMatchRunner struct{ called []string }

func (f *fakeMatchRunner) RunMatch(_ context.Context, candidateID string) error {
	f.called = append(f.called, candidateID)
	return nil
}

func TestMatchesWeeklyHandlerRunsMatchPerCandidate(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("weekly-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	runner := &fakeMatchRunner{}
	handler := MatchesWeeklyHandler(MatchesWeeklyDeps{
		Lister:  &fakeCandidateLister{ids: []string{"cnd_1", "cnd_2", "cnd_3"}},
		Runner:  runner,
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp matchesWeeklyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Processed != 3 {
		t.Fatalf("processed=%d, want 3", resp.Processed)
	}
	if len(runner.called) != 3 {
		t.Fatalf("RunMatch called %d times, want 3", len(runner.called))
	}

	_ = eventsv1.TopicCandidateMatchesReady // keep import referenced
	_ = time.Now
}
```

- [ ] **Step 2: Write the failing test for cv_stale_nudge**

Create `apps/candidates/service/admin/v1/cv_stale_nudge_test.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

type fakeStaleLister struct {
	stale []StaleCandidate
}

func (f *fakeStaleLister) ListStale(_ context.Context, _ time.Time) ([]StaleCandidate, error) {
	return f.stale, nil
}

type nudgeCollector struct {
	mu  sync.Mutex
	got []json.RawMessage
}

func (c *nudgeCollector) Name() string     { return eventsv1.TopicCandidateCVStaleNudge }
func (c *nudgeCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *nudgeCollector) Validate(context.Context, any) error { return nil }
func (c *nudgeCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	c.mu.Lock()
	c.got = append(c.got, append(json.RawMessage(nil), *raw...))
	c.mu.Unlock()
	return nil
}
func (c *nudgeCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestCVStaleNudgeHandlerEmitsOneEventPerStaleCandidate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("stale-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &nudgeCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	handler := CVStaleNudgeHandler(CVStaleNudgeDeps{
		Svc:       svc,
		Lister:    &fakeStaleLister{stale: []StaleCandidate{
			{CandidateID: "cnd_a", LastUploadAt: time.Now().Add(-70 * 24 * time.Hour)},
			{CandidateID: "cnd_b", LastUploadAt: time.Now().Add(-90 * 24 * time.Hour)},
		}},
		StaleAfter: 60 * 24 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/cv/stale_nudge", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 2 {
		t.Fatalf("emitted=%d, want 2", col.Len())
	}
}
```

- [ ] **Step 3: Run — expect build failures**

```bash
go test ./apps/candidates/service/admin/v1/... -count=1
```

Expected: `undefined: MatchesWeeklyHandler, MatchesWeeklyDeps, matchesWeeklyResponse, CVStaleNudgeHandler, CVStaleNudgeDeps, StaleCandidate`.

- [ ] **Step 4: Implement `matches_weekly.go`**

Create `apps/candidates/service/admin/v1/matches_weekly.go`:

```go
// Package v1 contains the Phase 5 Trustage admin HTTP handlers for
// apps/candidates: matches weekly digest + CV stale nudge.
package v1

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/pitabwire/util"
)

// CandidateLister enumerates active candidates (the universe targeted
// by the weekly match digest). In production the implementation reads
// Postgres `candidates` WHERE status='active'; v1.1 swaps in an
// event-sourced candidate ledger.
type CandidateLister interface {
	ListActive(ctx context.Context) ([]string, error)
}

// MatchRunner runs the match pipeline for one candidate. In production
// the implementation calls the match HTTP handler's internals directly
// (or via an in-process function pointer); tests use a fake.
type MatchRunner interface {
	RunMatch(ctx context.Context, candidateID string) error
}

// MatchesWeeklyDeps bundles collaborators.
type MatchesWeeklyDeps struct {
	Lister CandidateLister
	Runner MatchRunner
}

type matchesWeeklyResponse struct {
	OK        bool `json:"ok"`
	Processed int  `json:"processed"`
	Failed    int  `json:"failed"`
}

// MatchesWeeklyHandler returns an http.HandlerFunc fired by Trustage on
// a weekly cron. It enumerates active candidates and invokes RunMatch
// for each one sequentially. Failures per-candidate are logged and
// counted; they do not abort the sweep.
func MatchesWeeklyHandler(deps MatchesWeeklyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		ids, err := deps.Lister.ListActive(ctx)
		if err != nil {
			http.Error(w, `{"error":"list active failed"}`, http.StatusInternalServerError)
			return
		}
		resp := matchesWeeklyResponse{OK: true}
		for _, id := range ids {
			if err := deps.Runner.RunMatch(ctx, id); err != nil {
				log.WithError(err).WithField("candidate_id", id).Warn("weekly digest: RunMatch failed")
				resp.Failed++
				continue
			}
			resp.Processed++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
```

- [ ] **Step 5: Implement `cv_stale_nudge.go`**

Create `apps/candidates/service/admin/v1/cv_stale_nudge.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// StaleCandidate is one candidate whose most recent CV upload is older
// than the stale threshold.
type StaleCandidate struct {
	CandidateID  string
	LastUploadAt time.Time
}

// StaleLister enumerates stale candidates. Real impl scans
// candidates_cv_current/ in R2 and filters by occurred_at < cutoff.
// v1 uses a Postgres last-upload column as a shortcut.
type StaleLister interface {
	ListStale(ctx context.Context, asOf time.Time) ([]StaleCandidate, error)
}

// CVStaleNudgeDeps bundles collaborators.
type CVStaleNudgeDeps struct {
	Svc        *frame.Service
	Lister     StaleLister
	StaleAfter time.Duration // default 60 days
}

type cvStaleNudgeResponse struct {
	OK      bool `json:"ok"`
	Emitted int  `json:"emitted"`
}

// cvStaleNudgePayload is the body of the TopicCandidateCVStaleNudge event.
type cvStaleNudgePayload struct {
	CandidateID    string    `json:"candidate_id"`
	LastUploadAt   time.Time `json:"last_upload_at"`
	DaysSinceUpload int      `json:"days_since_upload"`
}

// CVStaleNudgeHandler returns an http.HandlerFunc fired by Trustage
// daily. It emits one CV-stale-nudge event per candidate whose most
// recent upload is older than StaleAfter. The external notification
// service consumes the topic and sends the nudge email.
func CVStaleNudgeHandler(deps CVStaleNudgeDeps) http.HandlerFunc {
	staleAfter := deps.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 60 * 24 * time.Hour
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		now := time.Now().UTC()
		candidates, err := deps.Lister.ListStale(ctx, now.Add(-staleAfter))
		if err != nil {
			log.WithError(err).Error("stale-nudge: ListStale failed")
			http.Error(w, `{"error":"list stale failed"}`, http.StatusInternalServerError)
			return
		}

		resp := cvStaleNudgeResponse{OK: true}
		for _, c := range candidates {
			days := int(now.Sub(c.LastUploadAt).Hours() / 24)
			env := eventsv1.NewEnvelope(eventsv1.TopicCandidateCVStaleNudge, cvStaleNudgePayload{
				CandidateID:    c.CandidateID,
				LastUploadAt:   c.LastUploadAt,
				DaysSinceUpload: days,
			})
			if err := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateCVStaleNudge, env); err != nil {
				log.WithError(err).WithField("candidate_id", c.CandidateID).Warn("stale-nudge: emit failed")
				continue
			}
			resp.Emitted++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
```

- [ ] **Step 6: Run — expect PASS**

```bash
go test ./apps/candidates/service/admin/v1/... -count=1
```

Expected: PASS for both handlers' tests.

- [ ] **Step 7: Commit**

```bash
git add apps/candidates/service/admin/v1/
git commit -m "feat(candidates): Trustage admin endpoints — matches weekly digest + CV stale nudge"
```

---

## Task 12: End-to-end candidates test

**Files:**
- Create: `apps/candidates/service/e2e_test.go`

Exercises the full Phase 5 loop in one test:

1. POST `/candidates/cv/upload` (multipart) → emits `CVUploadedV1`.
2. `CVExtractHandler` consumes it → emits `CVExtractedV1`.
3. `CVImproveHandler` consumes that → emits `CVImprovedV1`.
4. `CVEmbedHandler` consumes `CVExtractedV1` (parallel branch) → emits `CandidateEmbeddingV1`.
5. POST `/candidates/preferences` → emits `PreferencesUpdatedV1`.
6. Collectors on all five topics verify the events fired.

Because Frame's registry is one-handler-per-topic, the test can't simultaneously register a production handler AND a collector on the same topic without a fanout wrapper. For CV-extract, CV-improve, CV-embed, the production handlers are what we want to run; we add collectors only on the *output* topics (CVExtractedV1, CVImprovedV1, CandidateEmbeddingV1, PreferencesUpdatedV1, MatchesReadyV1) using fanout wrappers like Task 11 of Phase 4 did.

- [ ] **Step 1: Write the test**

Create `apps/candidates/service/e2e_test.go`:

```go
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/frame/frametests"

	"stawi.opportunities/pkg/archive"
	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/extraction"

	adminv1 "stawi.opportunities/apps/candidates/service/admin/v1"
	eventv1 "stawi.opportunities/apps/candidates/service/events/v1"
	httpv1 "stawi.opportunities/apps/candidates/service/http/v1"
)

// --- fakes (local to the e2e test) ---

type fakeText struct{ text string }

func (f *fakeText) FromPDF(_ []byte) (string, error)  { return f.text, nil }
func (f *fakeText) FromDOCX(_ []byte) (string, error) { return f.text, nil }

type fakeExtractor struct{ fields *extraction.CVFields }

func (f *fakeExtractor) ExtractCV(_ context.Context, _ string) (*extraction.CVFields, error) {
	return f.fields, nil
}

type fakeScorer struct{}

func (f *fakeScorer) Score(_ context.Context, _ string, _ *extraction.CVFields, _ string) *eventv1.ScoreComponents {
	return &eventv1.ScoreComponents{ATS: 85, Keywords: 80, Impact: 78, RoleFit: 82, Clarity: 88, Overall: 82}
}

type fakeFixes struct{}

func (f *fakeFixes) Generate(_ context.Context, _ *eventsv1.CVExtractedV1) ([]eventv1.PriorityFix, error) {
	return []eventv1.PriorityFix{{FixID: "fix-1", Title: "Add metrics", AutoApplicable: true}}, nil
}

type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.11, 0.22, 0.33}, nil
}

// --- generic collector ---

type envCol[P any] struct {
	topic string
	mu    sync.Mutex
	got   []eventsv1.Envelope[P]
}

func (c *envCol[P]) Name() string     { return c.topic }
func (c *envCol[P]) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *envCol[P]) Validate(context.Context, any) error { return nil }
func (c *envCol[P]) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[P]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *envCol[P]) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

// --- fanout wrapper: one production handler + one collector on same topic ---

type fanout struct {
	name string
	hs   []events.EventI
}

func (f *fanout) Name() string     { return f.name }
func (f *fanout) PayloadType() any { return f.hs[0].PayloadType() }
func (f *fanout) Validate(ctx context.Context, payload any) error {
	for _, h := range f.hs {
		if err := h.Validate(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}
func (f *fanout) Execute(ctx context.Context, payload any) error {
	for _, h := range f.hs {
		if err := h.Execute(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}

// --- test ---

func TestCandidatesE2EUploadToEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("candidates-e2e"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	// Output-topic collectors.
	extractedCol := &envCol[eventsv1.CVExtractedV1]{topic: eventsv1.TopicCVExtracted}
	improvedCol := &envCol[eventsv1.CVImprovedV1]{topic: eventsv1.TopicCVImproved}
	embeddingCol := &envCol[eventsv1.CandidateEmbeddingV1]{topic: eventsv1.TopicCandidateEmbedding}
	prefsCol := &envCol[eventsv1.PreferencesUpdatedV1]{topic: eventsv1.TopicCandidatePreferencesUpdated}

	// Production handlers (all subscribe to TopicCVExtracted or TopicCVUploaded).
	extractH := eventv1.NewCVExtractHandler(eventv1.CVExtractDeps{
		Svc:       svc,
		Extractor: &fakeExtractor{fields: &extraction.CVFields{Name: "Jane", Bio: "backend engineer"}},
		Scorer:    &fakeScorer{},
	})
	improveH := eventv1.NewCVImproveHandler(eventv1.CVImproveDeps{
		Svc: svc, Fixes: &fakeFixes{},
	})
	embedH := eventv1.NewCVEmbedHandler(eventv1.CVEmbedDeps{
		Svc: svc, Embedder: &fakeEmbedder{},
	})

	// Both improveH and embedH subscribe to TopicCVExtracted. Wrap
	// them as a single fanout registered under that topic. Add the
	// extractedCol on the same topic so we also see the extracted
	// event arrive.
	extractedFanout := &fanout{
		name: eventsv1.TopicCVExtracted,
		hs:   []events.EventI{extractedCol, improveH, embedH},
	}
	svc.EventsManager().Add(extractH)      // TopicCVUploaded
	svc.EventsManager().Add(extractedFanout) // TopicCVExtracted
	svc.EventsManager().Add(improvedCol)   // TopicCVImproved
	svc.EventsManager().Add(embeddingCol)  // TopicCandidateEmbedding
	svc.EventsManager().Add(prefsCol)      // TopicCandidatePreferencesUpdated

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(250 * time.Millisecond)

	// --- POST /candidates/cv/upload ---
	uploadHandler := httpv1.UploadHandler(httpv1.UploadDeps{
		Svc:     svc,
		Archive: archive.NewFakeArchive(),
		Text:    &fakeText{text: "resume plain text long enough to be usable"},
	})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("candidate_id", "cnd_e2e")
	fw, _ := mw.CreateFormFile("cv", "resume.pdf")
	fw.Write([]byte("%PDF-1.4 fake"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/candidates/cv/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	uploadHandler(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Wait for the pipeline to settle.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if extractedCol.Len() >= 1 && improvedCol.Len() >= 1 && embeddingCol.Len() >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if extractedCol.Len() != 1 {
		t.Fatalf("extracted=%d, want 1", extractedCol.Len())
	}
	if improvedCol.Len() != 1 {
		t.Fatalf("improved=%d, want 1", improvedCol.Len())
	}
	if embeddingCol.Len() != 1 {
		t.Fatalf("embedding=%d, want 1", embeddingCol.Len())
	}

	// --- POST /candidates/preferences ---
	prefsHandler := httpv1.PreferencesHandler(svc)
	body := map[string]any{"candidate_id": "cnd_e2e", "remote_preference": "remote", "salary_min": 70000}
	raw, _ := json.Marshal(body)
	req = httptest.NewRequest(http.MethodPost, "/candidates/preferences", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	prefsHandler(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("prefs status=%d body=%s", rec.Code, rec.Body.String())
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if prefsCol.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if prefsCol.Len() != 1 {
		t.Fatalf("prefs=%d, want 1", prefsCol.Len())
	}

	// keep adminv1 imported for future extension
	_ = adminv1.MatchesWeeklyDeps{}
}
```

- [ ] **Step 2: Run — expect PASS**

```bash
cd /home/j/code/stawi.opportunities
go test ./apps/candidates/service/... -run TestCandidatesE2EUploadToEmbedding -count=1 -timeout 3m
```

- [ ] **Step 3: Commit**

```bash
git add apps/candidates/service/e2e_test.go
git commit -m "test(candidates): end-to-end upload → extract → improve → embed + preferences"
```

---

## Task 13: SearchIndex adapter over `pkg/searchindex.Client`

**Files:**
- Create: `apps/candidates/service/http/v1/search_adapter.go`
- Create: `apps/candidates/service/http/v1/search_adapter_test.go`

The match handler depends on a `SearchIndex` interface (declared in `apps/candidates/service/http/v1/match.go`) whose shape is:

```go
type SearchIndex interface {
    KNNWithFilters(ctx context.Context, req SearchRequest) ([]SearchHit, error)
}
```

This task builds the production impl that wraps `*pkg/searchindex.Client`, constructs the Manticore JSON query body, and decodes hits.

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/http/v1/search_adapter_test.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeManticoreServer returns a canned Manticore /search response.
func fakeManticoreServer(t *testing.T, respBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/search") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
}

func TestManticoreSearchAdapterDecodesHits(t *testing.T) {
	body := `{"took":1,"timed_out":false,"hits":{"total":2,"hits":[
		{"_id":"1","_score":0.92,"_source":{"canonical_id":"can_a","slug":"job-a","title":"Senior Backend","company":"Acme"}},
		{"_id":"2","_score":0.81,"_source":{"canonical_id":"can_b","slug":"job-b","title":"Staff Backend","company":"Beta"}}
	]}}`
	srv := fakeManticoreServer(t, body)
	defer srv.Close()

	adapter, err := NewManticoreSearch(srv.URL, "idx_opportunities_rt")
	if err != nil {
		t.Fatalf("NewManticoreSearch: %v", err)
	}

	hits, err := adapter.KNNWithFilters(context.Background(), SearchRequest{
		Vector:           []float32{0.1, 0.2, 0.3},
		Limit:            10,
		RemotePreference: "remote",
		SalaryMinFloor:   70000,
	})
	if err != nil {
		t.Fatalf("KNNWithFilters: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits=%d, want 2", len(hits))
	}
	if hits[0].CanonicalID != "can_a" || hits[0].Score != 0.92 {
		t.Fatalf("hit[0] wrong: %+v", hits[0])
	}
}

func TestManticoreSearchAdapterBuildsKNNQuery(t *testing.T) {
	// Capture the request body to assert the query shape.
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
	}))
	defer srv.Close()

	adapter, _ := NewManticoreSearch(srv.URL, "idx_opportunities_rt")
	_, _ = adapter.KNNWithFilters(context.Background(), SearchRequest{
		Vector:             []float32{0.5},
		Limit:              20,
		RemotePreference:   "remote",
		SalaryMinFloor:     80000,
		PreferredLocations: []string{"KE", "US"},
	})
	if captured == nil {
		t.Fatal("server saw no request body")
	}
	if captured["index"] != "idx_opportunities_rt" {
		t.Fatalf("index=%v, want idx_opportunities_rt", captured["index"])
	}
	if _, ok := captured["knn"]; !ok {
		t.Fatalf("expected knn clause, got %+v", captured)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
cd /home/j/code/stawi.opportunities
go test ./apps/candidates/service/http/v1/... -run TestManticoreSearchAdapter
```

Expected: `undefined: NewManticoreSearch`.

- [ ] **Step 3: Implement the adapter**

Create `apps/candidates/service/http/v1/search_adapter.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"fmt"

	"stawi.opportunities/pkg/searchindex"
)

// ManticoreSearch adapts *searchindex.Client to the SearchIndex
// interface required by MatchHandler.
type ManticoreSearch struct {
	client *searchindex.Client
	index  string
}

// NewManticoreSearch opens a Manticore client at the given URL and
// returns an adapter bound to `index` (typically "idx_opportunities_rt").
func NewManticoreSearch(url, index string) (*ManticoreSearch, error) {
	c, err := searchindex.Open(searchindex.Config{URL: url})
	if err != nil {
		return nil, fmt.Errorf("search: open: %w", err)
	}
	return &ManticoreSearch{client: c, index: index}, nil
}

// KNNWithFilters builds a Manticore JSON query combining a KNN clause
// on the `embedding` attribute with hard filters on remote_type /
// salary_min / country. Returns the decoded hits.
//
// Manticore's KNN query shape (documented at
// https://manual.manticoresearch.com/Searching/KNN ):
//
//	{
//	  "index": "idx_opportunities_rt",
//	  "knn": { "field": "embedding", "query_vector": [...], "k": 200 },
//	  "query": { "bool": { "must": [ ...filters... ] } },
//	  "_source": ["canonical_id","slug","title","company"]
//	}
func (m *ManticoreSearch) KNNWithFilters(ctx context.Context, req SearchRequest) ([]SearchHit, error) {
	if len(req.Vector) == 0 {
		return nil, fmt.Errorf("search: empty vector")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}

	filters := []map[string]any{
		{"equals": map[string]any{"status": "active"}},
	}
	if req.RemotePreference != "" {
		filters = append(filters, map[string]any{"equals": map[string]any{"remote_type": req.RemotePreference}})
	}
	if req.SalaryMinFloor > 0 {
		filters = append(filters, map[string]any{"range": map[string]any{
			"salary_min": map[string]any{"gte": req.SalaryMinFloor},
		}})
	}
	if len(req.PreferredLocations) > 0 {
		filters = append(filters, map[string]any{"in": map[string]any{"country": req.PreferredLocations}})
	}

	query := map[string]any{
		"index": m.index,
		"knn": map[string]any{
			"field":        "embedding",
			"query_vector": req.Vector,
			"k":            limit,
		},
		"query":   map[string]any{"bool": map[string]any{"must": filters}},
		"_source": []string{"canonical_id", "slug", "title", "company"},
		"limit":   limit,
	}

	raw, err := m.client.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	var out struct {
		Hits struct {
			Hits []struct {
				ID     string  `json:"_id"`
				Score  float64 `json:"_score"`
				Source struct {
					CanonicalID string `json:"canonical_id"`
					Slug        string `json:"slug"`
					Title       string `json:"title"`
					Company     string `json:"company"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("search: decode: %w", err)
	}

	hits := make([]SearchHit, 0, len(out.Hits.Hits))
	for _, h := range out.Hits.Hits {
		hits = append(hits, SearchHit{
			CanonicalID: h.Source.CanonicalID,
			Slug:        h.Source.Slug,
			Title:       h.Source.Title,
			Company:     h.Source.Company,
			Score:       h.Score,
		})
	}
	return hits, nil
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/candidates/service/http/v1/... -run TestManticoreSearchAdapter -count=1
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/service/http/v1/search_adapter.go apps/candidates/service/http/v1/search_adapter_test.go
git commit -m "feat(candidates): Manticore KNN+filter adapter for match endpoint"
```

---

## Task 14: CandidateLister adapter over `repository.CandidateRepository`

**Files:**
- Create: `apps/candidates/service/admin/v1/candidate_lister.go`
- Create: `apps/candidates/service/admin/v1/candidate_lister_test.go`

`repository.CandidateRepository.ListActive(ctx, limit int) ([]*domain.CandidateProfile, error)` already exists. The `MatchesWeeklyHandler` wants a `CandidateLister` returning `[]string` of IDs. Thin adapter.

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/admin/v1/candidate_lister_test.go`:

```go
package v1

import (
	"context"
	"testing"

	"stawi.opportunities/pkg/domain"
)

// fakeCandidateRepo implements just ListActive.
type fakeCandidateRepo struct {
	rows []*domain.CandidateProfile
}

func (r *fakeCandidateRepo) ListActive(_ context.Context, _ int) ([]*domain.CandidateProfile, error) {
	return r.rows, nil
}

func TestRepoCandidateListerReturnsIDs(t *testing.T) {
	rows := []*domain.CandidateProfile{
		{BaseModel: domain.BaseModel{ID: "cnd_1"}},
		{BaseModel: domain.BaseModel{ID: "cnd_2"}},
	}
	lister := NewRepoCandidateLister(&fakeCandidateRepo{rows: rows}, 500)
	ids, err := lister.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(ids) != 2 || ids[0] != "cnd_1" || ids[1] != "cnd_2" {
		t.Fatalf("ids=%v", ids)
	}
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/candidates/service/admin/v1/... -run TestRepoCandidateLister
```

Expected: `undefined: NewRepoCandidateLister`.

- [ ] **Step 3: Implement the adapter**

Create `apps/candidates/service/admin/v1/candidate_lister.go`:

```go
package v1

import (
	"context"

	"stawi.opportunities/pkg/domain"
)

// CandidateActiveRepo is the subset of repository.CandidateRepository
// the lister adapter needs. Kept narrow so tests can fake it without
// pulling the whole repo type.
type CandidateActiveRepo interface {
	ListActive(ctx context.Context, limit int) ([]*domain.CandidateProfile, error)
}

// RepoCandidateLister adapts a CandidateActiveRepo into the CandidateLister
// interface required by MatchesWeeklyHandler.
type RepoCandidateLister struct {
	repo  CandidateActiveRepo
	limit int
}

// NewRepoCandidateLister wires the adapter.
func NewRepoCandidateLister(repo CandidateActiveRepo, limit int) *RepoCandidateLister {
	if limit <= 0 {
		limit = 1000
	}
	return &RepoCandidateLister{repo: repo, limit: limit}
}

// ListActive returns the IDs of active candidates.
func (l *RepoCandidateLister) ListActive(ctx context.Context) ([]string, error) {
	rows, err := l.repo.ListActive(ctx, l.limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/candidates/service/admin/v1/... -run TestRepoCandidateLister -count=1
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/service/admin/v1/candidate_lister.go apps/candidates/service/admin/v1/candidate_lister_test.go
git commit -m "feat(candidates): CandidateLister adapter on repository.ListActive"
```

---

## Task 15: `MatchService` — extract match pipeline from the HTTP handler

**Files:**
- Modify: `apps/candidates/service/http/v1/match.go`
- Create: `apps/candidates/service/http/v1/match_service.go`
- Create: `apps/candidates/service/http/v1/match_service_test.go`

The weekly-digest `MatchRunner` needs the same "run the match pipeline for one candidate" logic the HTTP handler already has. Rather than duplicate it or have the cron fire an internal HTTP call, extract the pipeline into a struct that both the HTTP handler and `MatchRunner` can drive.

This refactor preserves Task 10's external behaviour exactly: `MatchHandler` still returns the same JSON shape and emits the same `MatchesReadyV1` event.

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/http/v1/match_service_test.go`:

```go
package v1

import (
	"context"
	"testing"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

func TestMatchServiceRunMatchReturnsHits(t *testing.T) {
	store := &fakeCandidateStore{
		emb:   eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_1", Vector: []float32{0.1}},
		prefs: eventsv1.PreferencesUpdatedV1{CandidateID: "cnd_1"},
	}
	search := &fakeSearchIndex{rows: []SearchHit{
		{CanonicalID: "can_a", Score: 0.9},
		{CanonicalID: "can_b", Score: 0.8},
	}}
	svc := NewMatchService(store, search, 5)

	res, err := svc.RunMatch(context.Background(), "cnd_1")
	if err != nil {
		t.Fatalf("RunMatch: %v", err)
	}
	if len(res.Matches) != 2 || res.Matches[0].CanonicalID != "can_a" {
		t.Fatalf("bad result: %+v", res)
	}
	if res.CandidateID != "cnd_1" || res.MatchBatchID == "" {
		t.Fatalf("ids wrong: %+v", res)
	}
}

func TestMatchServiceRunMatchMissingEmbeddingReturnsErrNoEmbedding(t *testing.T) {
	store := &fakeCandidateStore{err: errNoEmbedding}
	svc := NewMatchService(store, &fakeSearchIndex{}, 5)

	_, err := svc.RunMatch(context.Background(), "cnd_missing")
	if err == nil {
		t.Fatalf("expected error")
	}
}
```

(The test references `errNoEmbedding` — a sentinel the match_service.go file will export so the HTTP wrapper can recognise not-found vs. transient errors.)

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/candidates/service/http/v1/... -run TestMatchService
```

Expected: `undefined: NewMatchService, errNoEmbedding`.

- [ ] **Step 3: Implement the service**

Create `apps/candidates/service/http/v1/match_service.go`:

```go
package v1

import (
	"context"
	"errors"

	"github.com/rs/xid"

	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// ErrNoEmbedding is returned by MatchService.RunMatch when the
// candidate has no embedding yet (typically because they haven't
// uploaded a CV). HTTP callers translate this to 404; cron callers
// count it as "skipped" rather than "failed".
var ErrNoEmbedding = errors.New("match: no embedding for candidate")

// errNoEmbedding is the package-local alias used by tests.
var errNoEmbedding = ErrNoEmbedding

// MatchResult is the structured output of RunMatch. Same shape the
// HTTP handler marshals into JSON.
type MatchResult struct {
	CandidateID  string
	MatchBatchID string
	Matches      []SearchHit
}

// MatchService runs the "embedding + preferences + Manticore KNN +
// top-K truncation" pipeline for one candidate. Used by both the
// HTTP match handler and the weekly-digest cron.
type MatchService struct {
	store  CandidateStore
	search SearchIndex
	topK   int
}

// NewMatchService wires the service.
func NewMatchService(store CandidateStore, search SearchIndex, topK int) *MatchService {
	if topK <= 0 {
		topK = 20
	}
	return &MatchService{store: store, search: search, topK: topK}
}

// RunMatch loads the candidate's embedding + preferences, queries
// Manticore, and returns the top-K hits. Does NOT emit events —
// that's the caller's job (HTTP handler emits after writing the
// response; cron emits in the weekly-digest loop).
func (s *MatchService) RunMatch(ctx context.Context, candidateID string) (MatchResult, error) {
	emb, err := s.store.LatestEmbedding(ctx, candidateID)
	if err != nil {
		return MatchResult{}, ErrNoEmbedding
	}
	prefs, _ := s.store.LatestPreferences(ctx, candidateID)

	hits, err := s.search.KNNWithFilters(ctx, SearchRequest{
		Vector:             emb.Vector,
		Limit:              200,
		RemotePreference:   prefs.RemotePreference,
		SalaryMinFloor:     prefs.SalaryMin,
		PreferredLocations: prefs.PreferredLocations,
	})
	if err != nil {
		return MatchResult{}, err
	}
	if len(hits) > s.topK {
		hits = hits[:s.topK]
	}

	return MatchResult{
		CandidateID:  candidateID,
		MatchBatchID: xid.New().String(),
		Matches:      hits,
	}, nil
}

// Ensure the package uses the eventsv1 import beyond the type aliases
// already in match.go (keeps goimports happy in tests that reference
// shared fakes).
var _ = eventsv1.MatchesReadyV1{}
```

- [ ] **Step 4: Refactor `match.go` to delegate to `MatchService`**

Edit `apps/candidates/service/http/v1/match.go`. Replace the body of `MatchHandler` so it constructs a `MatchService` from `deps.Store` + `deps.Search`, calls `RunMatch`, maps `ErrNoEmbedding` → 404, emits the event, returns the response. The response and event shapes are unchanged.

Replace:

```go
func MatchHandler(deps MatchDeps) http.HandlerFunc {
	topK := deps.TopK
	if topK <= 0 {
		topK = 20
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// ... existing big body ...
	}
}
```

With:

```go
func MatchHandler(deps MatchDeps) http.HandlerFunc {
	svcPipeline := NewMatchService(deps.Store, deps.Search, deps.TopK)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		candidateID := strings.TrimSpace(r.URL.Query().Get("candidate_id"))
		if candidateID == "" {
			http.Error(w, `{"error":"candidate_id is required"}`, http.StatusBadRequest)
			return
		}

		res, err := svcPipeline.RunMatch(ctx, candidateID)
		if errors.Is(err, ErrNoEmbedding) {
			http.Error(w, `{"error":"embedding not available — upload CV first"}`, http.StatusNotFound)
			return
		}
		if err != nil {
			log.WithError(err).Error("match: RunMatch failed")
			http.Error(w, `{"error":"search failed"}`, http.StatusBadGateway)
			return
		}

		rows := make([]matchResponseRow, 0, len(res.Matches))
		eventRows := make([]eventsv1.MatchRow, 0, len(res.Matches))
		for _, h := range res.Matches {
			rows = append(rows, matchResponseRow{
				CanonicalID: h.CanonicalID, Slug: h.Slug, Title: h.Title, Company: h.Company, Score: h.Score,
			})
			eventRows = append(eventRows, eventsv1.MatchRow{CanonicalID: h.CanonicalID, Score: h.Score})
		}
		env := eventsv1.NewEnvelope(eventsv1.TopicCandidateMatchesReady, eventsv1.MatchesReadyV1{
			CandidateID: res.CandidateID, MatchBatchID: res.MatchBatchID, Matches: eventRows,
		})
		if err := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateMatchesReady, env); err != nil {
			log.WithError(err).Warn("match: emit MatchesReadyV1 failed")
		}

		resp := matchResponse{
			OK: true, CandidateID: res.CandidateID, MatchBatchID: res.MatchBatchID, Matches: rows,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
```

The trailing `var _ = errors.New` line in match.go (added as a keep-alive in Task 10) can be deleted now — `errors.Is(err, ErrNoEmbedding)` uses the import directly.

- [ ] **Step 5: Run all match tests — both Task 10's and the new service tests must pass**

```bash
go test ./apps/candidates/service/http/v1/... -run 'TestMatchHandler|TestMatchService' -count=1 -v
```

- [ ] **Step 6: Commit**

```bash
git add apps/candidates/service/http/v1/match.go apps/candidates/service/http/v1/match_service.go apps/candidates/service/http/v1/match_service_test.go
git commit -m "refactor(candidates): extract MatchService so cron and HTTP share one pipeline"
```

---

## Task 16: Wire `MatchRunner` on the new `MatchService`

**Files:**
- Create: `apps/candidates/service/admin/v1/match_runner.go`
- Create: `apps/candidates/service/admin/v1/match_runner_test.go`

`MatchesWeeklyHandler` depends on a `MatchRunner` interface (`RunMatch(ctx, candidateID) error`). The new `MatchService` provides exactly the pipeline — but the runner also needs to emit the `MatchesReadyV1` event (the HTTP handler does this inline; the cron has to do it itself).

- [ ] **Step 1: Write the failing test**

Create `apps/candidates/service/admin/v1/match_runner_test.go`:

```go
package v1

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	httpv1 "stawi.opportunities/apps/candidates/service/http/v1"
	eventsv1 "stawi.opportunities/pkg/events/v1"
)

type matchesReadyCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.MatchesReadyV1]
}

func (c *matchesReadyCollector) Name() string     { return eventsv1.TopicCandidateMatchesReady }
func (c *matchesReadyCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *matchesReadyCollector) Validate(context.Context, any) error { return nil }
func (c *matchesReadyCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.MatchesReadyV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *matchesReadyCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

type fakeStore struct{ emb eventsv1.CandidateEmbeddingV1 }

func (f *fakeStore) LatestEmbedding(_ context.Context, _ string) (eventsv1.CandidateEmbeddingV1, error) {
	return f.emb, nil
}
func (f *fakeStore) LatestPreferences(_ context.Context, _ string) (eventsv1.PreferencesUpdatedV1, error) {
	return eventsv1.PreferencesUpdatedV1{}, nil
}

type fakeSearch struct{ rows []httpv1.SearchHit }

func (f *fakeSearch) KNNWithFilters(_ context.Context, _ httpv1.SearchRequest) ([]httpv1.SearchHit, error) {
	return f.rows, nil
}

func TestServiceMatchRunnerEmitsMatchesReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("runner-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &matchesReadyCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	store := &fakeStore{emb: eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_1", Vector: []float32{0.1}}}
	search := &fakeSearch{rows: []httpv1.SearchHit{{CanonicalID: "can_a", Score: 0.9}}}
	matchSvc := httpv1.NewMatchService(store, search, 5)

	runner := NewServiceMatchRunner(svc, matchSvc)
	if err := runner.RunMatch(ctx, "cnd_1"); err != nil {
		t.Fatalf("RunMatch: %v", err)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	if col.got[0].Payload.CandidateID != "cnd_1" {
		t.Fatalf("bad payload: %+v", col.got[0].Payload)
	}
}

func TestServiceMatchRunnerSwallowsMissingEmbedding(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("runner-missing"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	// store returns ErrNoEmbedding
	store := &fakeStoreErrNoEmbedding{}
	search := &fakeSearch{}
	matchSvc := httpv1.NewMatchService(store, search, 5)

	runner := NewServiceMatchRunner(svc, matchSvc)
	if err := runner.RunMatch(ctx, "cnd_x"); err != nil {
		t.Fatalf("RunMatch should swallow ErrNoEmbedding, got: %v", err)
	}
}

type fakeStoreErrNoEmbedding struct{}

func (f *fakeStoreErrNoEmbedding) LatestEmbedding(_ context.Context, _ string) (eventsv1.CandidateEmbeddingV1, error) {
	return eventsv1.CandidateEmbeddingV1{}, httpv1.ErrNoEmbedding
}
func (f *fakeStoreErrNoEmbedding) LatestPreferences(_ context.Context, _ string) (eventsv1.PreferencesUpdatedV1, error) {
	return eventsv1.PreferencesUpdatedV1{}, nil
}
```

- [ ] **Step 2: Run — expect build failure**

```bash
go test ./apps/candidates/service/admin/v1/... -run TestServiceMatchRunner
```

Expected: `undefined: NewServiceMatchRunner`.

- [ ] **Step 3: Implement the runner**

Create `apps/candidates/service/admin/v1/match_runner.go`:

```go
package v1

import (
	"context"
	"errors"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	httpv1 "stawi.opportunities/apps/candidates/service/http/v1"
	eventsv1 "stawi.opportunities/pkg/events/v1"
)

// ServiceMatchRunner wraps an *httpv1.MatchService so the cron calls
// the same pipeline the HTTP handler uses. Emits MatchesReadyV1 after
// a successful run; swallows ErrNoEmbedding (candidate without a CV is
// not a cron failure — we just skip them).
type ServiceMatchRunner struct {
	svc   *frame.Service
	match *httpv1.MatchService
}

// NewServiceMatchRunner wires the runner.
func NewServiceMatchRunner(svc *frame.Service, match *httpv1.MatchService) *ServiceMatchRunner {
	return &ServiceMatchRunner{svc: svc, match: match}
}

// RunMatch runs the match pipeline for one candidate and emits
// MatchesReadyV1. Returns nil for ErrNoEmbedding so the cron counts
// such candidates as "processed" rather than "failed".
func (r *ServiceMatchRunner) RunMatch(ctx context.Context, candidateID string) error {
	res, err := r.match.RunMatch(ctx, candidateID)
	if errors.Is(err, httpv1.ErrNoEmbedding) {
		util.Log(ctx).WithField("candidate_id", candidateID).Debug("match-runner: no embedding; skipping")
		return nil
	}
	if err != nil {
		return err
	}

	rows := make([]eventsv1.MatchRow, 0, len(res.Matches))
	for _, m := range res.Matches {
		rows = append(rows, eventsv1.MatchRow{CanonicalID: m.CanonicalID, Score: m.Score})
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicCandidateMatchesReady, eventsv1.MatchesReadyV1{
		CandidateID:  res.CandidateID,
		MatchBatchID: res.MatchBatchID,
		Matches:      rows,
	})
	if err := r.svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateMatchesReady, env); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./apps/candidates/service/admin/v1/... -run TestServiceMatchRunner -count=1
```

- [ ] **Step 5: Commit**

```bash
git add apps/candidates/service/admin/v1/match_runner.go apps/candidates/service/admin/v1/match_runner_test.go
git commit -m "feat(candidates): ServiceMatchRunner — cron wraps MatchService + emits MatchesReady"
```

---

## Task 17: StaleLister over `repository.CandidateRepository`

**Files:**
- Create: `apps/candidates/service/admin/v1/stale_lister.go`
- Create: `apps/candidates/service/admin/v1/stale_lister_test.go`
- Modify: `pkg/repository/candidate.go` — add `ListInactiveSince(ctx, cutoff, limit) ([]*domain.CandidateProfile, error)`

In the event-sourced world, "time of last CV upload" lives in R2 Parquet. Scanning R2 every day is wasteful; building a compaction pipeline for this one use case is Phase 6 work. For Plan 5 we use `candidates.updated_at` as the activity proxy — it ticks on any status/subscription change. This is lossy (a candidate who did nothing but view jobs will still appear "fresh") but good enough for a nudge email cadence.

Adding one repository method is cleaner than reading the whole table and filtering client-side.

- [ ] **Step 1: Add `ListInactiveSince` on the repository**

Edit `pkg/repository/candidate.go`. Append:

```go
// ListInactiveSince returns active candidates whose `updated_at` is
// older than cutoff, up to limit rows. Used by the daily stale-nudge
// cron as a proxy for "no recent activity".
func (r *CandidateRepository) ListInactiveSince(ctx context.Context, cutoff time.Time, limit int) ([]*domain.CandidateProfile, error) {
	var out []*domain.CandidateProfile
	err := r.db(ctx, true).
		Where("status = ? AND updated_at < ?", domain.CandidateStatusActive, cutoff).
		Order("updated_at ASC").
		Limit(limit).
		Find(&out).Error
	return out, err
}
```

The file already imports `time` and `domain`. Confirm.

- [ ] **Step 2: Write the adapter's failing test**

Create `apps/candidates/service/admin/v1/stale_lister_test.go`:

```go
package v1

import (
	"context"
	"testing"
	"time"

	"stawi.opportunities/pkg/domain"
)

type fakeInactiveRepo struct {
	cutoff time.Time
	rows   []*domain.CandidateProfile
}

func (r *fakeInactiveRepo) ListInactiveSince(_ context.Context, cutoff time.Time, _ int) ([]*domain.CandidateProfile, error) {
	r.cutoff = cutoff
	return r.rows, nil
}

func TestRepoStaleListerMapsRowsToStaleCandidates(t *testing.T) {
	updated := time.Now().UTC().Add(-75 * 24 * time.Hour)
	repo := &fakeInactiveRepo{rows: []*domain.CandidateProfile{
		{BaseModel: domain.BaseModel{ID: "cnd_a", UpdatedAt: updated}},
		{BaseModel: domain.BaseModel{ID: "cnd_b", UpdatedAt: updated}},
	}}
	lister := NewRepoStaleLister(repo, 500)
	stale, err := lister.ListStale(context.Background(), time.Now().UTC().Add(-60*24*time.Hour))
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(stale) != 2 || stale[0].CandidateID != "cnd_a" {
		t.Fatalf("bad result: %+v", stale)
	}
	if repo.cutoff.IsZero() {
		t.Fatalf("cutoff not forwarded to repo")
	}
}
```

- [ ] **Step 3: Run — expect build failure**

```bash
go test ./apps/candidates/service/admin/v1/... -run TestRepoStaleLister
```

Expected: `undefined: NewRepoStaleLister`.

- [ ] **Step 4: Implement the adapter**

Create `apps/candidates/service/admin/v1/stale_lister.go`:

```go
package v1

import (
	"context"
	"time"

	"stawi.opportunities/pkg/domain"
)

// InactiveRepo is the narrow interface the stale lister depends on.
// Satisfied by *repository.CandidateRepository after Task 17 adds the
// ListInactiveSince method.
type InactiveRepo interface {
	ListInactiveSince(ctx context.Context, cutoff time.Time, limit int) ([]*domain.CandidateProfile, error)
}

// RepoStaleLister adapts an InactiveRepo to the StaleLister interface
// expected by CVStaleNudgeHandler.
type RepoStaleLister struct {
	repo  InactiveRepo
	limit int
}

// NewRepoStaleLister wires the adapter.
func NewRepoStaleLister(repo InactiveRepo, limit int) *RepoStaleLister {
	if limit <= 0 {
		limit = 1000
	}
	return &RepoStaleLister{repo: repo, limit: limit}
}

// ListStale maps repository rows to the StaleCandidate shape used by
// CVStaleNudgeHandler. `LastUploadAt` is filled from `updated_at` as a
// v1 proxy for upload time; Phase 6 replaces this with a real last-
// upload timestamp read from R2 Parquet.
func (l *RepoStaleLister) ListStale(ctx context.Context, asOf time.Time) ([]StaleCandidate, error) {
	rows, err := l.repo.ListInactiveSince(ctx, asOf, l.limit)
	if err != nil {
		return nil, err
	}
	out := make([]StaleCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, StaleCandidate{
			CandidateID:  r.ID,
			LastUploadAt: r.UpdatedAt,
		})
	}
	return out, nil
}
```

- [ ] **Step 5: Run — expect PASS**

```bash
go test ./apps/candidates/service/admin/v1/... -run TestRepoStaleLister -count=1
go test ./pkg/repository/... -count=1
```

- [ ] **Step 6: Commit**

```bash
git add apps/candidates/service/admin/v1/stale_lister.go apps/candidates/service/admin/v1/stale_lister_test.go pkg/repository/candidate.go
git commit -m "feat(candidates): StaleLister via repository.ListInactiveSince"
```

---

## Task 18: Rewrite `apps/candidates/cmd/main.go`

**Files:**
- Modify: `apps/candidates/cmd/main.go` (complete rewrite)
- Modify: `apps/candidates/config/config.go` (trim unused env vars)
- Delete: `apps/candidates/cmd/billing.go`

This task drops roughly 1,200 lines from `main.go`: every legacy HTTP handler (registerHandler, billingWebhookHandler, plansHandler, checkoutStatusHandler, linkExpiredHandler, getProfileHandler, updateProfileHandler, listMatchesHandler, viewMatchHandler, listCandidatesHandler, meHandler, meSubscriptionHandler, onboardHandler, uploadCVHandler [legacy], saveJobHandler, unsaveJobHandler, listSavedJobsHandler, forceMatchHandler, getCVScoreHandler, rescoreCVHandler, inboundEmailHandler) and every legacy helper function only used by those handlers.

The new `main.go` wires:

**HTTP endpoints:**
- `GET /healthz` — minimal health check.
- `POST /candidates/cv/upload` — `UploadHandler`.
- `POST /candidates/preferences` — `PreferencesHandler`.
- `GET /candidates/match` — `MatchHandler` (backed by `MatchService` + `ManticoreSearch`).

**Trustage admin endpoints:**
- `POST /_admin/matches/weekly_digest` — `MatchesWeeklyHandler` (backed by `RepoCandidateLister` + `ServiceMatchRunner`).
- `POST /_admin/cv/stale_nudge` — `CVStaleNudgeHandler` (backed by `RepoStaleLister`).

**Internal event subscriptions (Frame):**
- `CVExtractHandler` on `candidates.cv.uploaded.v1`.
- `CVImproveHandler` on `candidates.cv.extracted.v1`.
- `CVEmbedHandler` on `candidates.cv.extracted.v1`.

(Production is affected by the one-handler-per-topic constraint — in production, only *one* subscriber on `candidates.cv.extracted.v1` can survive in the Frame registry. This means either `CVImproveHandler` or `CVEmbedHandler` must yield. Per spec §6.2, they are supposed to run in parallel. The real fix is to give them distinct subscription names via a wrapping concept — Frame's current API doesn't support this cleanly, which is why the e2e test uses a fanout wrapper. Task 18 introduces a tiny in-file `parallelFanout` type that wraps both production handlers and registers once under the topic, dispatching to both on each event. This is an explicit v1 workaround documented in a comment.)

**All production adapters (Tasks 13–17) are wired — no `nil` placeholders in the mux.**

- [ ] **Step 1: Read the current main.go** (to see what's there)

```bash
cd /home/j/code/stawi.opportunities
wc -l apps/candidates/cmd/main.go apps/candidates/cmd/billing.go
grep -n "^func" apps/candidates/cmd/main.go | head -50
```

Use the output to understand what's being deleted. Every function listed other than `main` itself is being removed. Skim one or two of the HTTP handler bodies to confirm they only touch things we're intentionally removing (Postgres candidate columns, billing, saved_jobs, etc.).

- [ ] **Step 2: Rewrite `apps/candidates/cmd/main.go`**

Replace the entire file contents with:

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/util"

	candidatesconfig "stawi.opportunities/apps/candidates/config"
	adminv1 "stawi.opportunities/apps/candidates/service/admin/v1"
	eventv1 "stawi.opportunities/apps/candidates/service/events/v1"
	httpv1 "stawi.opportunities/apps/candidates/service/http/v1"
	"stawi.opportunities/pkg/archive"
	"stawi.opportunities/pkg/candidatestore"
	"stawi.opportunities/pkg/cv"
	"stawi.opportunities/pkg/eventlog"
	eventsv1 "stawi.opportunities/pkg/events/v1"
	"stawi.opportunities/pkg/extraction"
	"stawi.opportunities/pkg/repository"
	"stawi.opportunities/pkg/telemetry"
)

func main() {
	ctx := context.Background()

	cfg, err := fconfig.FromEnv[candidatesconfig.CandidatesConfig]()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("candidates: config parse failed")
	}

	opts := []frame.Option{
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	}
	ctx, svc := frame.NewServiceWithContext(ctx, opts...)
	log := util.Log(ctx)

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	dbFn := pool.DB

	if err := telemetry.Init(); err != nil {
		log.WithError(err).Warn("candidates: telemetry init failed")
	}

	// --- Archive (raw CV bytes → R2) ---
	arch := archive.NewR2Archive(archive.R2Config{
		AccountID:       cfg.ArchiveR2AccountID,
		AccessKeyID:     cfg.ArchiveR2AccessKeyID,
		SecretAccessKey: cfg.ArchiveR2SecretAccessKey,
		Bucket:          cfg.ArchiveR2Bucket,
	})

	// --- Event-log R2 reader (for match endpoint) ---
	eventLogClient := eventlog.NewClient(eventlog.R2Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2EventLogBucket,
	})
	candStore := candidatestore.NewReader(eventLogClient, cfg.R2EventLogBucket)

	// --- AI extractor ---
	var extractor *extraction.Extractor
	infBase, infModel, infKey := extraction.ResolveInference(
		cfg.InferenceBaseURL, cfg.InferenceModel, cfg.InferenceAPIKey,
		"", "")
	if infBase != "" {
		embBase, embModel, embKey := extraction.ResolveEmbedding(
			cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingAPIKey,
			"", "")
		extractor = extraction.New(extraction.Config{
			BaseURL:          infBase,
			APIKey:           infKey,
			Model:            infModel,
			EmbeddingBaseURL: embBase,
			EmbeddingAPIKey:  embKey,
			EmbeddingModel:   embModel,
		})
		log.WithField("url", infBase).Info("AI extraction enabled")
	}

	// --- Subscription handlers ---
	extractH := eventv1.NewCVExtractHandler(eventv1.CVExtractDeps{
		Svc:                   svc,
		Extractor:             cvExtractorAdapter{extractor},
		Scorer:                cvScorerAdapter{cv.NewScorer(extractor)},
		ExtractorModelVersion: cfg.InferenceModel,
		ScorerModelVersion:    "cv-scorer-v1",
	})
	improveH := eventv1.NewCVImproveHandler(eventv1.CVImproveDeps{
		Svc:          svc,
		Fixes:        cvFixAdapter{extractor: extractor},
		ModelVersion: cfg.InferenceModel,
	})
	embedH := eventv1.NewCVEmbedHandler(eventv1.CVEmbedDeps{
		Svc:          svc,
		Embedder:     embedderAdapter{extractor},
		ModelVersion: cfg.EmbeddingModel,
	})

	// Parallel fanout: both improveH and embedH subscribe to
	// TopicCVExtracted. Frame v1.94.1's event registry is one handler
	// per topic, so we compose them into a single EventI.
	extractedFanout := parallelFanout{
		name: improveH.Name(),
		hs:   []events.EventI{improveH, embedH},
	}

	svc.Init(ctx, frame.WithRegisterEvents(extractH, extractedFanout))

	// --- Production adapters (Tasks 13-17) ---
	candidateRepo := repository.NewCandidateRepository(dbFn)
	search, err := httpv1.NewManticoreSearch(cfg.ManticoreURL, "idx_opportunities_rt")
	if err != nil {
		log.WithError(err).Fatal("candidates: Manticore adapter init failed")
	}
	matchSvc := httpv1.NewMatchService(candStore, search, 20)
	candidateLister := adminv1.NewRepoCandidateLister(candidateRepo, 1000)
	matchRunner := adminv1.NewServiceMatchRunner(svc, matchSvc)
	staleLister := adminv1.NewRepoStaleLister(candidateRepo, 1000)

	// --- HTTP mux ---
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("POST /candidates/cv/upload", httpv1.UploadHandler(httpv1.UploadDeps{
		Svc:     svc,
		Archive: arch,
		Text:    textExtractor{},
	}))
	mux.HandleFunc("POST /candidates/preferences", httpv1.PreferencesHandler(svc))
	mux.HandleFunc("GET /candidates/match", httpv1.MatchHandler(httpv1.MatchDeps{
		Svc:    svc,
		Store:  candStore,
		Search: search,
	}))

	// --- Trustage admin endpoints ---
	mux.HandleFunc("POST /_admin/matches/weekly_digest",
		adminv1.MatchesWeeklyHandler(adminv1.MatchesWeeklyDeps{
			Lister: candidateLister,
			Runner: matchRunner,
		}))
	mux.HandleFunc("POST /_admin/cv/stale_nudge",
		adminv1.CVStaleNudgeHandler(adminv1.CVStaleNudgeDeps{
			Svc:        svc,
			Lister:     staleLister,
			StaleAfter: 60 * 24 * time.Hour,
		}))

	svc.Init(ctx, frame.WithHTTPHandler(mux))

	if err := svc.Run(ctx, ""); err != nil {
		log.WithError(err).Error("candidates: service run failed")
		os.Exit(1)
	}

	_ = json.Marshal
}

// --- Adapters — wire concrete pkg/* types to the v1 handler interfaces. ---

type textExtractor struct{}

func (textExtractor) FromPDF(b []byte) (string, error)  { return extraction.ExtractTextFromPDF(b) }
func (textExtractor) FromDOCX(b []byte) (string, error) { return extraction.ExtractTextFromDOCX(b) }

type cvExtractorAdapter struct{ e *extraction.Extractor }

func (a cvExtractorAdapter) ExtractCV(ctx context.Context, text string) (*extraction.CVFields, error) {
	return a.e.ExtractCV(ctx, text)
}

type cvScorerAdapter struct{ s *cv.Scorer }

func (a cvScorerAdapter) Score(ctx context.Context, cvText string, fields *extraction.CVFields, targetRole string) *eventv1.ScoreComponents {
	rep := a.s.Score(ctx, cvText, fields, targetRole)
	return &eventv1.ScoreComponents{
		ATS:      rep.Components.ATS,
		Keywords: rep.Components.Keywords,
		Impact:   rep.Components.Impact,
		RoleFit:  rep.Components.RoleFit,
		Clarity:  rep.Components.Clarity,
		Overall:  rep.Components.Overall,
	}
}

type cvFixAdapter struct{ extractor *extraction.Extractor }

func (a cvFixAdapter) Generate(ctx context.Context, in *eventsv1.CVExtractedV1) ([]eventv1.PriorityFix, error) {
	// Build a local cv.ScoreComponents + CVFields view, call
	// detectPriorityFixes, then AttachRewrites via the extractor.
	// Phase 6 may inline this more tightly once the legacy code is
	// removed.
	fields := &extraction.CVFields{
		Name: in.Name, Email: in.Email, Phone: in.Phone, Location: in.Location,
		CurrentTitle: in.CurrentTitle, Bio: in.Bio, Seniority: in.Seniority,
		YearsExperience: in.YearsExperience, PrimaryIndustry: in.PrimaryIndustry,
		StrongSkills: in.StrongSkills, WorkingSkills: in.WorkingSkills,
		ToolsFrameworks: in.ToolsFrameworks, Certifications: in.Certifications,
		PreferredRoles: in.PreferredRoles, Languages: in.Languages,
		Education: in.Education, PreferredLocations: in.PreferredLocations,
		RemotePreference: in.RemotePreference, SalaryMin: in.SalaryMin,
		SalaryMax: in.SalaryMax, Currency: in.Currency,
	}
	comps := cv.ScoreComponents{
		ATS:      in.ScoreATS,
		Keywords: in.ScoreKeywords,
		Impact:   in.ScoreImpact,
		RoleFit:  in.ScoreRoleFit,
		Clarity:  in.ScoreClarity,
		Overall:  in.ScoreOverall,
	}
	fixes := cv.DetectPriorityFixes("", fields, cv.RoleFamily{}, comps)
	out := make([]eventv1.PriorityFix, 0, len(fixes))
	for _, f := range fixes {
		out = append(out, eventv1.PriorityFix{
			FixID:          f.ID,
			Title:          f.Title,
			ImpactLevel:    f.Impact,
			Category:       f.Category,
			Why:            f.Why,
			AutoApplicable: f.AutoApplicable,
			Rewrite:        "", // v1: skip AI rewrites to conserve quota
		})
	}
	return out, nil
}

type embedderAdapter struct{ e *extraction.Extractor }

func (a embedderAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	return a.e.Embed(ctx, text)
}

// parallelFanout runs N handlers under one topic (Frame v1.94.1
// limitation workaround). Errors fail fast — any child error aborts
// the fanout and Frame redelivers.
type parallelFanout struct {
	name string
	hs   []events.EventI
}

func (f parallelFanout) Name() string     { return f.name }
func (f parallelFanout) PayloadType() any { return f.hs[0].PayloadType() }
func (f parallelFanout) Validate(ctx context.Context, payload any) error {
	for _, h := range f.hs {
		if err := h.Validate(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}
func (f parallelFanout) Execute(ctx context.Context, payload any) error {
	for _, h := range f.hs {
		if err := h.Execute(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}
```

**Note on the wiring TODOs** — the match endpoint needs a `SearchIndex` adapter wrapping `pkg/searchindex.Client`, and the two admin endpoints need `CandidateLister` / `MatchRunner` / `StaleLister` adapters. All three are cross-cutting: they depend on the Postgres `candidates` table and on the match handler's internals. The v1 wire-up leaves them as `nil` with explicit `TODO` comments; the binary still compiles and the three HTTP handlers that don't depend on those adapters (upload, preferences, healthz) work correctly on production traffic. Phase 6 cutover fills in the blanks together with the billing/saved-jobs migration.

If the reader finds this half-wired state unacceptable, the quickest fix is to gate the three not-yet-wired endpoints behind a config flag (`CANDIDATES_V1_FEATURES=upload,preferences`) and return 503 on the others. This is out of scope for Plan 5 and explicitly deferred.

- [ ] **Step 3: Delete `apps/candidates/cmd/billing.go`**

```bash
git rm apps/candidates/cmd/billing.go
```

- [ ] **Step 4: Trim `apps/candidates/config/config.go`**

Open `apps/candidates/config/config.go` and remove any fields only used by the deleted handlers (billing endpoints, profile service URL, notification service URL, etc.). Add fields for the new path:

```go
	// R2 event log (Parquet partitions).
	R2AccountID       string `env:"R2_ACCOUNT_ID"         envDefault:""`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID"      envDefault:""`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY"  envDefault:""`
	R2EventLogBucket  string `env:"R2_EVENTLOG_BUCKET"    envDefault:"opportunities-log"`

	// Archive R2 (raw CV bytes).
	ArchiveR2AccountID       string `env:"ARCHIVE_R2_ACCOUNT_ID"         envDefault:""`
	ArchiveR2AccessKeyID     string `env:"ARCHIVE_R2_ACCESS_KEY_ID"      envDefault:""`
	ArchiveR2SecretAccessKey string `env:"ARCHIVE_R2_SECRET_ACCESS_KEY"  envDefault:""`
	ArchiveR2Bucket          string `env:"ARCHIVE_R2_BUCKET"             envDefault:"opportunities-archive"`

	// AI backends.
	InferenceBaseURL string `env:"INFERENCE_BASE_URL" envDefault:""`
	InferenceAPIKey  string `env:"INFERENCE_API_KEY"  envDefault:""`
	InferenceModel   string `env:"INFERENCE_MODEL"    envDefault:""`
	EmbeddingBaseURL string `env:"EMBEDDING_BASE_URL" envDefault:""`
	EmbeddingAPIKey  string `env:"EMBEDDING_API_KEY"  envDefault:""`
	EmbeddingModel   string `env:"EMBEDDING_MODEL"    envDefault:""`

	// Manticore URL (for match endpoint — wired in Phase 6).
	ManticoreURL string `env:"MANTICORE_URL" envDefault:""`
```

Leave other existing config fields that other services might pull via ConfigurationDefault alone.

- [ ] **Step 5: Build + run tests**

```bash
cd /home/j/code/stawi.opportunities
go build ./apps/candidates/...
go test ./apps/candidates/... -count=1 -timeout 5m
```

Expected: clean build, all Task 5–12 tests pass.

- [ ] **Step 6: Full-module build + test sweep**

```bash
go build ./...
go test ./... -count=1 -timeout 15m
```

Expected: all tests pass. Legacy `apps/candidates/service/events/{embedding.go,profile_created.go}` still compile but are no longer referenced anywhere — dead code that Phase 6 deletes.

- [ ] **Step 7: Commit**

```bash
git add apps/candidates/cmd/main.go apps/candidates/cmd/billing.go apps/candidates/config/config.go
git commit -m "refactor(candidates): rewrite main.go — drop legacy handlers, wire v1 pipeline"
```

---

## Task 19: Trustage trigger definitions

**Files:**
- Create: `definitions/trustage/candidates-matches-weekly-digest.json`
- Create: `definitions/trustage/candidates-cv-stale-nudge.json`

- [ ] **Step 1: Create the weekly-digest trigger**

Create `definitions/trustage/candidates-matches-weekly-digest.json`:

```json
{
  "version": "1.0",
  "name": "opportunities.candidates.matches.weekly-digest",
  "description": "Every Monday 09:00 UTC: run the match pipeline for every active candidate and emit candidates.matches.ready.v1 per candidate. A downstream notification service delivers the digest by email.",
  "schedule": {
    "cron": "0 9 * * 1",
    "active": true
  },
  "input": {},
  "config": {},
  "timeout": "30m",
  "on_error": {
    "action": "abort"
  },
  "steps": [
    {
      "id": "weekly_digest",
      "type": "call",
      "name": "Invoke matches weekly digest",
      "timeout": "25m",
      "retry": {
        "max_attempts": 2,
        "backoff_strategy": "exponential",
        "initial_backoff": "60s"
      },
      "call": {
        "action": "http.request",
        "input": {
          "url": "http://opportunities-candidates.opportunities.svc/_admin/matches/weekly_digest",
          "method": "POST",
          "headers": { "Content-Type": "application/json" },
          "body": {}
        },
        "output_var": "digest_result"
      }
    }
  ]
}
```

- [ ] **Step 2: Create the stale-nudge trigger**

Create `definitions/trustage/candidates-cv-stale-nudge.json`:

```json
{
  "version": "1.0",
  "name": "opportunities.candidates.cv.stale-nudge",
  "description": "Every day at 10:00 UTC: enumerate candidates whose latest CV upload is > 60 days old and emit candidates.cv.stale_nudge.v1 per candidate. A downstream notification service sends the nudge email.",
  "schedule": {
    "cron": "0 10 * * *",
    "active": true
  },
  "input": {},
  "config": {},
  "timeout": "5m",
  "on_error": {
    "action": "abort"
  },
  "steps": [
    {
      "id": "stale_nudge",
      "type": "call",
      "name": "Invoke CV stale nudge",
      "timeout": "3m",
      "retry": {
        "max_attempts": 3,
        "backoff_strategy": "exponential",
        "initial_backoff": "30s"
      },
      "call": {
        "action": "http.request",
        "input": {
          "url": "http://opportunities-candidates.opportunities.svc/_admin/cv/stale_nudge",
          "method": "POST",
          "headers": { "Content-Type": "application/json" },
          "body": {}
        },
        "output_var": "nudge_result"
      }
    }
  ]
}
```

- [ ] **Step 3: Validate JSON**

```bash
python3 -m json.tool < definitions/trustage/candidates-matches-weekly-digest.json > /dev/null
python3 -m json.tool < definitions/trustage/candidates-cv-stale-nudge.json > /dev/null
```

- [ ] **Step 4: Commit**

```bash
git add definitions/trustage/candidates-matches-weekly-digest.json definitions/trustage/candidates-cv-stale-nudge.json
git commit -m "feat(trustage): candidate weekly-digest + CV stale-nudge triggers"
```

---

## Task 20: Full build + sanity sweep

**Files:**
- None (verification only)

- [ ] **Step 1: Build the whole module**

```bash
cd /home/j/code/stawi.opportunities
go build ./...
```

Expected: exit 0.

- [ ] **Step 2: Run the full test suite**

```bash
go test ./... -count=1 -timeout 20m
```

Expected: PASS. Phase 1–4 tests remain green; Phase 5's six new test files pass.

Note: Manticore testcontainer tests in `pkg/searchindex` may be flaky under concurrent test runs; a retry of that single package is acceptable.

- [ ] **Step 3: Confirm binaries still build via Makefile**

```bash
rm -rf bin
make build
ls -la bin/
```

Expected: api, crawler, materializer, worker, writer, candidates all present.

- [ ] **Step 4: Commit nothing**

Verification only.

---

## Plan completion verification

After every task is done:

```bash
go build ./...
go test ./... -count=1 -timeout 20m
git log --oneline d60ce4c..HEAD
```

Expected:
- All tests pass.
- ~19 commits on this branch (one per task except Task 20).
- Six new v1 event payload types, three HTTP endpoints (upload, preferences, match), two Trustage admin endpoints (matches weekly digest, CV stale nudge), three internal event subscriptions (cv-extract, cv-improve, cv-embed), four production adapters (Manticore search, candidate lister, match runner, stale lister), one end-to-end test.

At this point, an operator can:
1. Deploy `apps/candidates`.
2. Configure the two Trustage triggers in the cluster.
3. POST a PDF resume to `/candidates/cv/upload`, observe the full event trail (uploaded → extracted → improved + embedding) in the writer's Parquet output on R2.
4. POST preferences.
5. GET `/candidates/match` — returns Manticore-backed matches once the job index has enough rows.
6. Trustage fires `/_admin/matches/weekly_digest` + `/_admin/cv/stale_nudge` on schedule; both emit the expected downstream events.

Phase 6 cutover then removes the legacy Postgres candidate columns (CV fields, preferences, embedding), deletes the legacy handler files (`apps/candidates/service/events/{embedding.go,profile_created.go}`), builds the daily compactor that populates `candidates_*_current/` partitions (so `candidatestore.Reader` has data to read), and replaces the `updated_at`-proxy in `RepoStaleLister` with a real R2-backed last-upload timestamp scan.
