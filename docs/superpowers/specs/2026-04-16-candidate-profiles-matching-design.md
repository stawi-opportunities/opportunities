# Candidate Profiles + Matching — Design Spec

**Date**: 2026-04-16
**Goal**: AI-driven candidate profile system with CV extraction, embedding-based job matching, and free/paid tier split for the stawi.jobs platform.

---

## 1. Context

stawi.jobs has a working job crawling framework with 11,863+ canonical jobs, AI extraction via Ollama, embedding-based search, and job quality scoring. This spec adds the candidate side — profiles, matching, and the foundation for auto-apply and communications.

### Revenue Model

Phase 1 (this spec): Candidate uploads CV → AI builds profile → matching engine finds best jobs → free users get 5 matches, paid users get unlimited + auto-apply.

Phase 2 (future): Auto-apply engine submits applications via ATS forms, email outreach, and candidate-assisted links.

Phase 3 (future): Transition to recruitment agency model with employer-side features.

---

## 2. Architecture

### New App

```
apps/
├── crawler/      ← crawls jobs, stores, scores (existing)
├── scheduler/    ← monitors due sources (existing)
├── api/          ← job search API (existing)
└── candidates/   ← NEW: profiles, matching, applications
    ├── Dockerfile
    ├── cmd/
    │   └── main.go
    ├── config/
    │   └── config.go
    └── service/
        ├── setup.go
        └── events/
            ├── profile_created.go
            ├── match_requested.go
            └── match_found.go
```

### Shared Resources

- Same PostgreSQL database (reads canonical_jobs, writes candidate tables)
- Same Ollama instance (CV extraction + candidate embeddings)
- Frame for service lifecycle, events, datastore

### New Database Tables

- `candidate_profiles` — profile data
- `candidate_matches` — match results
- `candidate_applications` — application tracking (future, schema only)

### Event Flow

```
candidate.profile.created    → triggers initial matching
candidate.profile.updated    → triggers re-matching
job.canonical.created        → triggers matching against all active candidates
candidate.match.found        → triggers notification dispatch
candidate.application.submit → triggers auto-apply engine (future)
```

### Pod Allocation

Candidates app gets its own deployment. Pod allocation to be determined by cluster capacity.

---

## 3. Candidate Profile Data Model

### Table: `candidate_profiles`

```sql
id                  BIGSERIAL PRIMARY KEY
email               TEXT UNIQUE NOT NULL
name                TEXT
phone               TEXT
status              VARCHAR(20) DEFAULT 'unverified'  -- unverified, active, paused, hired
subscription        VARCHAR(20) DEFAULT 'free'        -- free, trial, paid, cancelled
auto_apply          BOOLEAN DEFAULT false
cv_url              TEXT
cv_raw_text         TEXT

-- AI-extracted structured fields
current_title       TEXT
seniority           VARCHAR(30)
years_experience    INT
skills              TEXT          -- comma-separated (strong + working)
strong_skills       TEXT          -- comma-separated, depth-validated skills
working_skills      TEXT          -- comma-separated, mentioned-once skills
tools_frameworks    TEXT          -- comma-separated specific tech
certifications      TEXT          -- comma-separated
preferred_roles     TEXT          -- comma-separated role categories
industries          TEXT          -- comma-separated
education           TEXT          -- degree, field, institution summary
preferred_locations TEXT          -- comma-separated cities/countries
preferred_countries TEXT          -- comma-separated ISO codes
remote_preference   VARCHAR(20)  -- remote_only, hybrid_ok, onsite_ok, any
salary_min          REAL
salary_max          REAL
currency            VARCHAR(10)
languages           TEXT          -- comma-separated "language:proficiency" pairs
bio                 TEXT          -- AI-generated professional summary
work_history        JSONB         -- structured array of past roles

-- Communication preferences
comm_email          BOOLEAN DEFAULT true
comm_whatsapp       BOOLEAN DEFAULT false
comm_telegram       BOOLEAN DEFAULT false
comm_sms            BOOLEAN DEFAULT false
whatsapp_number     TEXT
telegram_handle     TEXT

-- Matching
embedding           TEXT          -- JSON float array, same vector space as jobs
matches_sent        INT DEFAULT 0 -- count of matches sent (free tier cap)
last_matched_at     TIMESTAMPTZ
last_contacted_at   TIMESTAMPTZ

-- Timestamps
created_at          TIMESTAMPTZ
updated_at          TIMESTAMPTZ
```

### Table: `candidate_matches`

```sql
id                BIGSERIAL PRIMARY KEY
candidate_id      BIGINT NOT NULL REFERENCES candidate_profiles(id)
canonical_job_id  BIGINT NOT NULL REFERENCES canonical_jobs(id)
match_score       REAL NOT NULL
skills_overlap    REAL
embedding_similarity REAL
status            VARCHAR(20) DEFAULT 'new'  -- new, sent, viewed, applied, rejected, hired
sent_at           TIMESTAMPTZ
viewed_at         TIMESTAMPTZ
applied_at        TIMESTAMPTZ
created_at        TIMESTAMPTZ
UNIQUE(candidate_id, canonical_job_id)
```

### Table: `candidate_applications` (schema only, future)

```sql
id                BIGSERIAL PRIMARY KEY
candidate_id      BIGINT NOT NULL REFERENCES candidate_profiles(id)
match_id          BIGINT REFERENCES candidate_matches(id)
canonical_job_id  BIGINT NOT NULL REFERENCES canonical_jobs(id)
method            VARCHAR(20)   -- ats_submit, email_outreach, candidate_link
status            VARCHAR(20)   -- pending, submitted, confirmed, failed
apply_url         TEXT
cover_letter      TEXT
submitted_at      TIMESTAMPTZ
response_at       TIMESTAMPTZ
response_type     VARCHAR(20)   -- none, viewed, rejected, interview, offer
created_at        TIMESTAMPTZ
updated_at        TIMESTAMPTZ
```

---

## 4. CV Ingestion Pipeline

### Entry Points

**Email channel:**
```
Candidate emails CV to jobs@stawi.jobs
  → Email provider webhook: POST /webhooks/inbound-email
  → Extract: sender email, attachments (PDF/DOCX), body text
  → If email matches existing profile → update
  → If new email → create profile with status=unverified
  → Feed CV into extraction pipeline
```

**Web channel:**
```
Candidate authenticates → uploads CV file
  → Profile exists (authenticated)
  → Feed CV into extraction pipeline
  → Show extracted profile for inline editing
```

### Shared Extraction Pipeline

```
CV file (PDF/DOCX/TXT)
  ↓
Text extraction (PDF parser / DOCX reader → plain text)
  ↓
Store raw text in cv_raw_text, file in cv_url
  ↓
AI extraction via Ollama (CV-specific prompt)
  → Extracts: name, title, seniority, skills (strong vs working),
    experience, education, industries, languages, preferences
  ↓
Generate embedding from profile text
  (same vector space as job embeddings)
  ↓
Store structured profile fields + embedding
  ↓
Emit event: candidate.profile.created
  → Triggers initial matching
  → Triggers verification email
```

### AI CV Extraction Fields

**Identity:** name, email, phone, linkedin_url, github_url, portfolio_url, location

**Classification:** current_title, seniority, years_experience, primary_industry, role_categories

**Skills (by signal strength):**
- strong_skills: mentioned in multiple roles or with depth indicators ("led", "architected", "built")
- working_skills: mentioned once or in passing
- tools_frameworks: specific technologies
- certifications: formal certifications

**Work History:** array of {company, title, start_date, end_date, summary}

**Education:** degree, field, institution, graduation_year

**Preferences (if stated):** preferred_locations, remote_preference, salary_expectations

**Languages:** array of {language, proficiency}

### Verification Email

After extraction, send:
```
"Hi {name}, here's what we understood from your CV:

Title: {current_title}
Skills: {top skills}
Experience: {years_experience} years
Looking for: {remote_preference}, {salary range}

Is this correct? Reply YES to confirm, or reply with corrections.

[View & edit your full profile →]"
```

Candidate replies YES → status = active → matching begins.
Candidate replies with corrections → AI parses → updates profile → re-sends confirmation.

---

## 5. Matching Algorithm

### Three-Stage Matching

**Stage 1: Hard Filters (SQL — eliminates ~90%)**

Disqualify fundamentally incompatible jobs:
- remote_preference: candidate "remote_only" → exclude onsite-only jobs
- salary: job salary_max < candidate salary_min → exclude
- seniority: allow ±1 level, exclude beyond (senior candidate → no intern jobs)
- location: if candidate has preferred_countries, exclude jobs outside (skip if no preference)

Narrows from 50K+ to ~2-5K jobs.

**Stage 2: Embedding Similarity (vector — ranks by relevance)**

Cosine similarity between candidate embedding and job embeddings on the filtered set. Captures semantic matches:
- "Full-stack developer" ↔ "Software Engineer"
- "Data analyst with Python" ↔ "ML Engineer"

Take top 200 by similarity.

**Stage 3: Quality-Weighted Scoring (composite — final ranking)**

```
match_score =
    0.35 × skills_overlap        (Jaccard: candidate skills ∩ required_skills)
  + 0.25 × embedding_similarity  (from stage 2)
  + 0.15 × quality_score / 100   (job quality from scorer)
  + 0.10 × salary_fit            (1.0 if in range, decay outside)
  + 0.10 × recency               (1.0 if posted today, decay over 30 days)
  + 0.05 × seniority_fit         (1.0 if exact, 0.5 if ±1 level)
```

Jobs with match_score > 0.6 are good matches.

### Match Triggers

| Trigger | Action |
|---|---|
| New candidate profile created | Match against all active jobs |
| New job crawled and stored | Match against all active candidates |
| Candidate updates preferences | Re-match against all active jobs |

Each trigger emits a Frame event so matching runs asynchronously.

---

## 6. Free vs Paid Experience

### Free Users

```
Profile created → matching runs → top 5 matches stored
  ↓
Spread delivery over 2-3 weeks:
  Week 1: 2 best matches
  Week 2: 2 more matches
  Week 3: 1 match + conversion nudge
  ↓
Then silence. Check in every 2-3 months:
  "Still looking? We have {N} new matches."
  ↓
Every touchpoint has soft conversion nudge
```

### Paid Users (Subscription)

```
Subscribe → auto_apply = true → matching runs continuously
  ↓
Every new good match (score > 0.6):
  → Auto-apply immediately
  → Notify via preferred channel:
    "We applied to {title} at {company}. Match: 85%"
  ↓
Weekly digest:
  "This week: 12 applications, 3 viewed, 1 interview"
  ↓
Until: hired, cancelled, or paused
```

### Conversion Triggers

| Trigger | Message |
|---|---|
| 5 free matches sent | "Subscribe for unlimited + auto-apply" |
| Exceptional match found (>0.85) | "95% fit found. Subscribe to apply instantly" |
| Quarterly check-in | "{N} jobs matched since last visit" |

### Subscription Tiers

| Plan | Features |
|---|---|
| Free | 5 matches total, email only, self-apply |
| Monthly | Unlimited matches, auto-apply, multi-channel, weekly digest |
| Quarterly (20% off) | Same as monthly |

---

## 7. API Endpoints

### Candidate Endpoints

```
POST   /candidates/register          ← email + CV upload, creates profile
POST   /candidates/login             ← email-based auth (magic link)
GET    /candidates/profile           ← get own profile (authenticated)
PUT    /candidates/profile           ← update preferences
POST   /candidates/cv                ← upload/replace CV
GET    /candidates/matches           ← list matches with scores
POST   /candidates/matches/:id/view  ← mark match as viewed
POST   /candidates/subscribe         ← start subscription
DELETE /candidates/subscribe         ← cancel subscription
```

### Webhook Endpoints

```
POST   /webhooks/inbound-email       ← email provider callback
```

### Internal/Admin

```
GET    /admin/candidates             ← list all candidates
GET    /admin/candidates/:id/matches ← view candidate's matches
POST   /admin/match/run              ← force matching run
```

---

## 8. Frame Integration

### Events

```go
candidate.profile.created   → MatchRequestedHandler (run matching)
candidate.profile.updated   → MatchRequestedHandler (re-run matching)
job.canonical.created       → JobMatchHandler (match new job against all candidates)
candidate.match.found       → MatchNotificationHandler (send email/notification)
candidate.embedding.needed  → EmbeddingHandler (generate candidate embedding)
```

### Service Initialization

```go
ctx, svc := frame.NewServiceWithContext(ctx,
    frame.WithConfig(&cfg),
    frame.WithDatastore(),
)

svc.Init(ctx,
    frame.WithRegisterEvents(
        events.NewProfileCreatedHandler(...),
        events.NewMatchRequestedHandler(...),
        events.NewMatchFoundHandler(...),
        events.NewCandidateEmbeddingHandler(...),
    ),
)
```

---

## 9. Deferred (separate sub-projects)

- **Auto-apply engine** — ATS form automation, email outreach, candidate-assisted links
- **Communication engine** — WhatsApp, Telegram, SMS integration (beyond email)
- **Payment/subscription** — Stripe/payment gateway integration
- **Employer dashboard** — employer-side features for recruitment agency phase
- **Application tracking** — full lifecycle from applied → interview → offer → hired

---

## 10. Dependencies

- Existing: Ollama (CV extraction + embeddings), PostgreSQL, Frame, NATS
- New: PDF text extraction library (e.g., `pdfcpu` or `unidoc` for Go)
- New: DOCX text extraction library (e.g., `baliance/gooxml`)
- New: Email inbound webhook integration (SendGrid/Mailgun/Postmark)
