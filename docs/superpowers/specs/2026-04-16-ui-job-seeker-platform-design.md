# Stawi.jobs UI Platform Design

## Overview

A Hugo-based static site with Alpine.js SPA islands that serves as the primary user-facing platform for stawi.opportunities. Jobs are rendered as static HTML from generated data, while dynamic features (search, onboarding, dashboard, auth) are Alpine.js components embedded in Hugo page shells. Payments are handled via antinvestor/service-payment which integrates Polar.sh (cards/international) and M-PESA/MTN/Airtel (mobile money for East Africa).

## Goals

1. **Job seekers** can browse, search, and apply to 37K+ remote jobs with sub-second page loads
2. **Job seekers** can register via a 3-step onboarding wizard, subscribe, and receive personalized matches
3. **Employers** can post jobs and manage applicants through a minimal ATS (future sub-project)
4. **Admins** can browse backend data and view analytics (future sub-project)
5. **Accessibility** — WCAG 2.1 AA compliance throughout
6. **Professional finish** — production-grade design, not a prototype

## Architecture

### Static + SPA Islands

```
                    Build Time                          Runtime
                    ─────────                           ───────
Postgres ──→ apps/sitegen ──→ ui/data/*.json
                                    │
                                    ▼
                              Hugo Build ──→ Static HTML/CSS/JS ──→ CDN
                                                    │
                                    ┌───────────────┼───────────────┐
                                    ▼               ▼               ▼
                              Static Pages    Alpine.js Islands   Pagefind
                              (job listings,  (onboarding,        (client-side
                               categories,     dashboard,          full-text
                               job detail)     auth, live search)  search)
                                                    │
                                                    ▼
                                              Go API backends
                                              (api :8082, candidates service)
                                                    │
                                                    ▼
                                              antinvestor/service-payment
                                              (Polar.sh, M-PESA, MTN, Airtel)
```

### Why This Architecture

- **SEO**: 37K+ job pages as static HTML — perfect for Google indexing
- **Performance**: CDN-served pages load in <100ms; no SSR cold starts
- **Cost**: Static hosting is free/cheap (Cloudflare Pages, Netlify)
- **Simplicity**: No Node.js runtime in production; Hugo binary + Go backends
- **Progressive enhancement**: Pages work without JS; Alpine.js enhances them

## Technology Stack

| Layer | Technology | Rationale |
|-------|-----------|-----------|
| Static site generator | Hugo (latest, 0.147+) | Go-native, fast builds, content adapters |
| CSS | Tailwind CSS via Hugo Pipes | Utility-first, purged in production, accessible defaults |
| SPA islands | Alpine.js 3.x | Lightweight (15KB), no build step, declarative |
| Client-side search | Pagefind | WASM-based, indexes post-build, ~100KB for 37K pages |
| Semantic search | Go API `/search/semantic` | Embedding-based re-ranking for "best fit" |
| Auth | OIDC (PKCE) → external IdP | Stateless, Frame JWT middleware already wired |
| Payments | antinvestor/service-payment | Polar.sh (cards), M-PESA/MTN/Airtel (mobile money) |
| Hosting | Cloudflare Pages or k8s nginx | Static files + API proxy |

## Content Generation: `apps/sitegen`

A new Go CLI that bridges the database to Hugo's data layer.

### What It Produces

| Output File | Content | Hugo Consumption |
|-------------|---------|-----------------|
| `ui/data/jobs.json` | All active canonical jobs (id, title, company, location, salary, skills, category, posted_at, apply_url, description excerpt) | Content adapter `_content.gotmpl` |
| `ui/data/categories.json` | Category slugs, names, job counts, subcategories | Content adapter for category pages |
| `ui/data/stats.json` | Total jobs, total companies, jobs posted this week | Homepage hero stats |
| `ui/data/companies.json` | Unique companies with job counts, logos | Company listing/trust bar |

### When It Runs

- After each crawl cycle completes (triggered by scheduler)
- On demand via `make sitegen`
- CI/CD: `make sitegen && hugo build && npx pagefind --site public`

### Data Shape: jobs.json

```json
[
  {
    "id": 12345,
    "slug": "senior-go-developer-at-acme-corp-12345",
    "title": "Senior Go Developer",
    "company": "Acme Corp",
    "company_slug": "acme-corp",
    "category": "programming",
    "subcategories": ["back-end", "golang"],
    "location_text": "Remote - Anywhere",
    "remote_type": "fully_remote",
    "employment_type": "Full-Time",
    "salary_min": 80000,
    "salary_max": 120000,
    "currency": "USD",
    "seniority": "senior",
    "skills": ["Go", "PostgreSQL", "Kubernetes"],
    "description": "Full markdown description...",
    "excerpt": "First 200 chars for listing cards...",
    "apply_url": "https://acme.com/careers/123",
    "quality_score": 85.2,
    "posted_at": "2026-04-15T10:00:00Z",
    "is_featured": false
  }
]
```

### Category Derivation

Map from existing domain fields to display categories:

| Category Slug | Derived From |
|--------------|-------------|
| `programming` | roles contains "developer", "engineer", "programmer"; industry = "technology" |
| `design` | roles contains "designer", "ux", "ui" |
| `customer-support` | roles contains "support", "customer success" |
| `marketing` | roles contains "marketing", "growth", "seo" |
| `sales` | roles contains "sales", "account executive", "business development" |
| `devops` | roles contains "devops", "sre", "infrastructure" |
| `product` | roles contains "product manager", "product owner" |
| `data` | roles contains "data scientist", "data engineer", "analyst" |
| `management` | roles contains "manager", "director", "vp", "head of" |
| `other` | fallback |

## Page Inventory

### Static Pages (Hugo-rendered)

#### Homepage (`/`)
- **Hero section**: "Find Remote Jobs in Africa and Beyond" headline, live stats from `data/stats.json` (37K+ jobs, X companies), primary CTA "Browse Jobs"
- **Category grid**: Cards for each category with job count, linked to category page
- **Recent featured jobs**: Top 10 by quality score, rendered as job cards
- **Trust bar**: Company logos from `data/companies.json` (top companies by job count)
- **How it works**: 3-step visual (Browse → Apply → Get Hired)
- **Testimonials section**: Static testimonial cards (manually curated in `data/testimonials.json`)

#### Job Listing (`/jobs/`)
- Paginated list of all jobs, 20 per page
- Sort: newest first (default), by salary, by quality score
- Client-side filters via Alpine.js: employment type, remote type, salary range, posted date
- Each job rendered via `partials/job-card.html`
- Pagefind search bar at top

#### Job Detail (`/jobs/{slug}/`)
- Full job description (markdown rendered to HTML)
- Sidebar: company info, salary range, location, employment type, skills tags, seniority badge
- "Apply Now" button (links to external apply URL)
- "Save Job" button (requires auth, Alpine.js island)
- Similar jobs section (Hugo taxonomy/related content)
- Structured data (JSON-LD `JobPosting` schema for Google Jobs)

#### Category Page (`/categories/{slug}/`)
- Category header with description and job count
- Subcategory quick-filter pills
- Job card list filtered to category
- Same card format as `/jobs/`

#### Static Content Pages
- `/about/` — About stawi.opportunities
- `/pricing/` — Subscription plans
- `/terms/` — Terms of service
- `/privacy/` — Privacy policy

### SPA Islands (Alpine.js in Hugo shells)

#### Search Island (navbar + `/search/`)
- **Navbar search**: Pagefind-powered instant search dropdown with keyboard navigation
- **Full search page** (`/search/`): Pagefind for keyword search; logged-in users see a "Best Fit" toggle that switches to `/search/semantic` API with their profile embedding for personalized ranking
- Pagefind filters: category, employment type, location, salary range (via `data-pagefind-filter` attributes on job templates)
- Results render as job cards with highlighted matching terms
- Accessible: ARIA live region announces result count, full keyboard navigation

#### Onboarding Wizard (`/onboarding/`)
Three-step wizard matching the WWR screenshots:

**Step 1 — About You**
- Greeting: "Hi, {name}" (name from `/me` → service-profile, read-only)
- CV upload (drag-and-drop + file picker, max 10MB, PDF/DOCX)
- Target job title (text input with autocomplete from categories)
- Experience level (select: Entry/Junior/Mid/Senior/Lead/Executive)
- Job status (select: Actively Looking / Open to Offers / Casually Browsing)
- Preferred salary range (select: ranges in USD)
- Checkbox: "Email me a free ATS score and resume report"
- Right panel: Stats ("Thousands of professionals hired..."), trust metrics

**Step 2 — Curate Your Search**
- Regions (multi-select with search: "Anywhere in the World", Africa, Europe, etc.)
- Preferred time zones (multi-select with search: UTC offsets with names)
- Country (select with flag icons)
- Work authorization section:
  - "Are you authorized to work in the US?" (radio: Yes/No)
  - "Will you require sponsorship?" (radio: Yes/No, shown conditionally)
- Right panel: Hero image + testimonial carousel (auto-rotating, pause on hover)

**Step 3 — Subscription**
- Plan display: pricing card showing introductory offer
- Payment method selection:
  - Card/International → initiates Polar.sh checkout (via service-payment `CreatePaymentLink`)
  - Mobile Money → initiates M-PESA/MTN/Airtel STK push (via service-payment `InitiatePrompt`)
- Order summary: subtotal, discount, tax, total
- Terms acceptance checkbox
- Right panel: same testimonial carousel

**Data flow:**
```
Prerequisites: User must be OIDC-authenticated before reaching /onboarding/
  → ProfileID available from JWT claims

Step 1 + Step 2 → collected in Alpine.js state (no API calls yet)
Step 3 "Get Full Access" click →
  1. POST /candidates/onboard (multipart: CV file + all preference fields)
     → Backend reads ProfileID from JWT claims
     → Creates CandidateProfile linked by ProfileID
     → Calls service-profile to get name/email for any welcome comms
  2. POST /billing/subscribe → service-payment BillingService.CreateSubscription
  3. POST /billing/checkout or /billing/mobile-pay → payment flow
  4. Redirect to Polar.sh checkout or M-PESA STK push waiting screen
  5. Webhook confirms payment → subscription updated to "paid"
  6. Redirect to /dashboard/
```

**"Skip for now"** link on Step 3 → creates CandidateProfile with free tier, redirects to dashboard with limited features.

**Identity note:** The onboarding form does NOT ask for name or email — these already exist in service-profile from the OIDC registration. Step 1 shows the user's name (fetched from `/me`) as read-only context. The form only collects job-seeking domain data: CV, target title, experience, salary preferences, etc.

#### Dashboard (`/dashboard/`)
Requires authentication. Full SPA experience within the Hugo shell.

**Sidebar navigation:**
- Matches (default view)
- Applications
- Saved Jobs
- Profile & Preferences
- Subscription & Billing

**Matches view:**
- Feed of matched jobs sorted by match score (from `GET /candidates/matches`)
- Each match card shows: job title, company, match score (percentage), key matching skills highlighted, salary, "View Job" and "Apply" buttons
- Match status badges: New, Viewed, Applied
- Infinite scroll pagination

**Applications view:**
- List of jobs applied to (from `CandidateApplication` table)
- Status tracking: Pending, Submitted, Interviewing, Offer, Rejected
- Timeline view per application

**Saved Jobs view:**
- Bookmarked jobs (stored via API, new endpoint needed)
- Quick apply / remove actions

**Profile & Preferences view:**
- Edit all fields from onboarding steps 1 & 2
- Re-upload CV
- Communication preferences (email, WhatsApp, Telegram, SMS toggles)
- Delete account

**Subscription & Billing view:**
- Current plan and status
- Payment history (from service-payment `SearchInvoices`)
- Upgrade/downgrade/cancel actions
- Add/change payment method

#### Auth Pages (`/auth/login/`, `/auth/callback/`)
OIDC Authorization Code + PKCE flow:

```
/auth/login/ (static Hugo page + Alpine.js):
  1. Generate code_verifier + code_challenge
  2. Store code_verifier in sessionStorage
  3. Redirect to IdP authorize endpoint with:
     - response_type=code
     - client_id
     - redirect_uri=/auth/callback/
     - scope=openid profile email
     - code_challenge + code_challenge_method=S256
     - state (CSRF)

/auth/callback/ (static Hugo page + Alpine.js):
  1. Extract code + state from URL params
  2. Verify state matches
  3. POST to IdP token endpoint with code + code_verifier
  4. Receive access_token + id_token + refresh_token
  5. Store access_token in memory (Alpine.js store)
  6. Store refresh_token in httpOnly cookie (via a thin API endpoint)
  7. Call GET /me → returns identity (name, avatar from service-profile) + candidate state
  8. If candidate is null → redirect to /onboarding/ (new user)
  9. If candidate exists → redirect to /dashboard/
```

Token refresh: Alpine.js interceptor checks token expiry before each API call; if expired, uses refresh token to get new access token silently.

## Authentication, Identity & Authorization

### Identity Model

**Principle: domain models never store identity data.** A user's name, avatar URL, and contacts are not duplicated into `CandidateProfile` or any other stawi.opportunities table. Identity is resolved at runtime from two sources:

1. **JWT claims** (via Frame's `security.ClaimsFromContext(ctx)`) provide IDs:
   - `GetProfileID()` — the user's unique ID (JWT `sub` claim)
   - `GetContactID()` — the email/phone used to log in
   - `GetTenantID()` — organization/tenant scope
   - `GetPartitionID()` — data partition
   - `GetRoles()` — role array (jobseeker, employer, admin)

2. **antinvestor/service-profile** provides display data:
   - `ProfileService.GetById(profileID)` → `ProfileObject`
   - `properties` (Struct): `name`, `avatar`, bio, etc.
   - `contacts` (repeated): type (EMAIL/MSISDN), detail, verified flag
   - `addresses`: physical addresses

**How this works in practice:**

- **Backend**: All stawi.opportunities domain models link to the external profile via `ProfileID string` (the JWT `sub` claim). When a handler needs the user's name or avatar (e.g., for a response), it calls service-profile. No identity data is persisted locally.
- **Frontend**: After OIDC login, Alpine.js calls a `/me` endpoint on the candidates service. That endpoint reads `claims.GetProfileID()`, calls service-profile, and returns `{ name, avatar_url, email, roles }`. This is cached in the Alpine.js global store for the session.

**For organizations/employers** (future sub-project): same pattern. An employer is an authenticated profile with a `tenant_id` claim. The organization's name, logo, and contacts come from service-profile scoped to that tenant.

### OIDC Provider

The IdP is external (Keycloak, Zitadel, or Auth0 — configurable via `hugo.toml` params). Frame's `SecurityManager` on the Go API side validates JWTs via JWKS endpoint. The candidates service already has `securityhttp.AuthenticationMiddleware` wired — just needs env vars:

```
OAUTH2_WELL_KNOWN_JWK=https://idp.example.com/.well-known/jwks.json
```

### Roles

JWT `roles` claim determines access:

| Role | Access |
|------|--------|
| `jobseeker` | Dashboard, matches, applications, preferences, search |
| `employer` | ATS, job posting, applicant review (future sub-project) |
| `admin` | All of above + data browser + analytics (future sub-project) |

### Auth Guard Pattern

Hugo partial `partials/auth-guard.html` wraps protected content:

```html
<div x-data="authGuard()" x-show="isAuthenticated" x-cloak>
  <!-- Protected content rendered here -->
</div>
<div x-data="authGuard()" x-show="!isAuthenticated" x-cloak>
  <p>Please <a href="/auth/login/">sign in</a> to access this page.</p>
</div>
```

## Payments via antinvestor/service-payment

### Subscription Plans

| Plan | Price | Features |
|------|-------|----------|
| Free | $0 | Browse jobs, basic search, 5 saved jobs |
| Premium | $2.95 first month, then $14.95/month (12-month) | Full search, unlimited saves, AI match scores, priority alerts, ATS resume score |

### Integration Flow

stawi.opportunities integrates with service-payment via ConnectRPC (buf.build-generated Go client). A thin API proxy in the candidates service exposes payment actions to the frontend:

**New endpoints on candidates service:**

| Endpoint | Maps To | Purpose |
|----------|---------|---------|
| `POST /billing/subscribe` | `BillingService.CreateSubscription` | Start a subscription for a candidate |
| `POST /billing/checkout` | `PaymentService.CreatePaymentLink` | Generate Polar.sh checkout URL for card payments |
| `POST /billing/mobile-pay` | `PaymentService.InitiatePrompt` | Trigger M-PESA/MTN/Airtel STK push |
| `GET /billing/subscription` | `BillingService.GetSubscription` | Current subscription status |
| `GET /billing/invoices` | `BillingService.SearchInvoices` | Payment history |
| `POST /billing/cancel` | `BillingService.CancelSubscription` | Cancel subscription |

**Webhook handler** (new endpoint on candidates service):
- `POST /webhooks/payment` — receives payment confirmations from service-payment
- Updates `CandidateProfile.Subscription` field to `paid`/`cancelled`
- Records payment timestamp

### Payment UX

**Card/International users:**
1. Click "Pay with Card" → `POST /billing/checkout` → returns Polar.sh checkout URL
2. Redirect to Polar.sh hosted checkout (handles card entry, 3DS, tax)
3. Polar.sh redirects back to `/onboarding/?payment=success`
4. Webhook confirms → subscription activated

**Mobile Money users (East Africa):**
1. Select provider (M-PESA, MTN, Airtel) and enter phone number
2. Click "Pay" → `POST /billing/mobile-pay` → triggers STK push
3. User confirms on their phone
4. Waiting screen with polling: `GET /billing/subscription` every 5s
5. Webhook confirms → subscription activated → redirect to dashboard

## Search Architecture

### Three Tiers

```
Tier 1: Pagefind (client-side, no API)
  └── Keyword search across all 37K+ jobs
  └── Faceted filters: category, type, location, salary
  └── Used by: all visitors (default search experience)

Tier 2: Go API full-text (server-side)
  └── PostgreSQL tsvector search via GET /search?q=...
  └── Used by: fallback for complex queries Pagefind can't handle
  └── Also powers "more results" when Pagefind results are insufficient

Tier 3: Semantic search (server-side, auth required)
  └── Embedding cosine similarity via GET /search/semantic?q=...
  └── Personalized: re-ranks results against candidate's profile embedding
  └── Used by: "Best Fit" toggle for premium subscribers
```

### Pagefind Configuration

Job detail templates include `data-pagefind-filter` attributes:

```html
<main data-pagefind-body>
  <h1 data-pagefind-meta="title">{{ .Title }}</h1>
  <span data-pagefind-filter="category">{{ .Params.category }}</span>
  <span data-pagefind-filter="type">{{ .Params.employment_type }}</span>
  <span data-pagefind-filter="remote">{{ .Params.remote_type }}</span>
  <span data-pagefind-filter="seniority">{{ .Params.seniority }}</span>
  <span data-pagefind-meta="company">{{ .Params.company }}</span>
  <span data-pagefind-meta="salary">{{ .Params.salary_display }}</span>
</main>
```

Build step: `npx pagefind --site public --glob "jobs/**/*.html"`

## Accessibility (WCAG 2.1 AA)

### Global Requirements

- **Color contrast**: All text meets 4.5:1 ratio (normal) / 3:1 (large text)
- **Focus indicators**: Visible focus ring on all interactive elements (3px solid, high contrast)
- **Skip navigation**: "Skip to main content" link as first focusable element
- **Landmark regions**: `<header>`, `<nav>`, `<main>`, `<aside>`, `<footer>` with ARIA labels
- **Heading hierarchy**: Single `<h1>` per page, sequential nesting
- **Responsive**: Functional from 320px to 2560px viewport width
- **Reduced motion**: `prefers-reduced-motion` disables animations/transitions
- **Dark mode**: Respects `prefers-color-scheme` (future enhancement)

### Component-Specific A11y

**Job Cards:**
- Card is a link (entire card clickable)
- Company logo has descriptive `alt` text
- Badge text (Featured, New) uses `aria-label` not just color
- Salary and location are read by screen readers in logical order

**Search:**
- Pagefind UI has built-in ARIA: `role="search"`, `aria-live="polite"` for results
- Custom result count announcement: "X jobs found for [query]"
- Filter changes announced via `aria-live`
- Keyboard: `/` focuses search, `Escape` clears, arrow keys navigate results

**Onboarding Wizard:**
- Progress bar: `role="progressbar"` with `aria-valuenow`, `aria-valuemin`, `aria-valuemax`
- Step indicator: "Step X of 3" announced on navigation
- Form validation: `aria-invalid="true"` + `aria-describedby` linking to error messages
- Required fields: `aria-required="true"`
- File upload: drag-drop zone has keyboard alternative and clear instructions
- Multi-select (regions, timezones): `role="listbox"` with `aria-multiselectable`

**Dashboard:**
- Sidebar: `role="navigation"` with `aria-current="page"` on active item
- Match cards: match score is not color-only (includes percentage text)
- Infinite scroll: "Load more" button as fallback; `aria-live` announces new items
- Status badges use text labels, not icons alone

**Auth:**
- Login button has `aria-label="Sign in with [provider]"`
- Loading states announced via `aria-live="assertive"`
- Error states use `role="alert"`

**Mobile / Touch:**
- Minimum 44x44px touch targets
- No hover-only interactions
- Swipe gestures have button alternatives

## Project Structure

```
ui/
├── hugo.toml                      # Hugo config (site params, OIDC config, API URLs)
├── package.json                   # Tailwind, Alpine.js, Pagefind deps
├── tailwind.config.js
├── postcss.config.js
├── content/
│   ├── _index.md                  # Homepage
│   ├── jobs/
│   │   └── _content.gotmpl       # Content adapter: generates pages from data/jobs.json
│   ├── categories/
│   │   └── _content.gotmpl       # Content adapter: generates category pages
│   ├── onboarding/
│   │   └── _index.md             # Wizard shell (type: onboarding)
│   ├── dashboard/
│   │   ├── _index.md             # Dashboard shell (type: dashboard)
│   │   ├── applications.md
│   │   ├── saved.md
│   │   ├── profile.md
│   │   └── billing.md
│   ├── auth/
│   │   ├── login.md              # OIDC login shell
│   │   └── callback.md           # OIDC callback shell
│   ├── search/
│   │   └── _index.md             # Full search page shell
│   ├── about.md
│   ├── pricing.md
│   ├── terms.md
│   └── privacy.md
├── data/
│   ├── jobs.json                  # Generated by apps/sitegen
│   ├── categories.json
│   ├── stats.json
│   ├── companies.json
│   └── testimonials.json          # Manually curated
├── layouts/
│   ├── _default/
│   │   ├── baseof.html            # Base: <html>, <head>, nav, footer, Alpine.js init
│   │   ├── list.html
│   │   └── single.html
│   ├── jobs/
│   │   ├── list.html              # Job listing page with filters
│   │   └── single.html            # Job detail with JSON-LD
│   ├── categories/
│   │   ├── list.html
│   │   └── single.html
│   ├── onboarding/
│   │   └── single.html            # Mounts wizard Alpine.js component
│   ├── dashboard/
│   │   └── single.html            # Mounts dashboard Alpine.js component
│   ├── auth/
│   │   └── single.html            # Mounts auth Alpine.js component
│   ├── search/
│   │   └── list.html              # Mounts Pagefind + search Alpine.js
│   ├── partials/
│   │   ├── head.html              # <head> with meta, OG tags, structured data
│   │   ├── navbar.html            # Top navigation
│   │   ├── footer.html
│   │   ├── job-card.html          # Reusable job card
│   │   ├── job-filters.html       # Alpine.js filter pills
│   │   ├── search-bar.html        # Navbar Pagefind search
│   │   ├── testimonial.html       # Testimonial card
│   │   ├── auth-guard.html        # Auth state wrapper
│   │   ├── skip-nav.html          # Skip to content link
│   │   └── breadcrumbs.html
│   ├── shortcodes/
│   │   └── salary-range.html      # Format salary display
│   └── _markup/
│       └── render-image.html      # Responsive images with lazy loading
├── assets/
│   ├── css/
│   │   ├── main.css               # Tailwind directives + custom components
│   │   └── pagefind-overrides.css # Pagefind UI theme overrides
│   └── js/
│       ├── app.js                 # Alpine.js init, global stores, API client
│       ├── auth.js                # OIDC PKCE flow (login, callback, token refresh)
│       ├── onboarding.js          # 3-step wizard state machine
│       ├── dashboard.js           # Dashboard views (matches, apps, profile, billing)
│       ├── search.js              # Search island (Pagefind + semantic fallback)
│       └── utils.js               # Shared helpers (fetch wrapper, token management)
├── static/
│   ├── images/
│   │   ├── logo.svg               # Stawi.jobs logo
│   │   ├── hero.webp              # Homepage hero image
│   │   └── og-default.png         # OpenGraph default image
│   └── robots.txt
└── .env.example                   # API_URL, OIDC_ISSUER, OIDC_CLIENT_ID, etc.
```

## New Go Components

### `apps/sitegen/cmd/main.go`

CLI tool that reads from Postgres and writes Hugo data files.

```
Usage: sitegen [flags]
  --database-url    Postgres connection string
  --output-dir      Path to ui/data/ (default: ../ui/data)
  --min-quality     Minimum quality score for inclusion (default: 50)
  --hugo-build      Run hugo build after generating data (default: false)
  --pagefind        Run pagefind indexing after hugo build (default: false)
```

### New Candidates Service Endpoints

Added to `apps/candidates/cmd/main.go`. All authenticated endpoints extract `ProfileID` from `security.ClaimsFromContext(ctx).GetProfileID()` — never from query params or request body.

```go
// Identity (composes service-profile + local domain data)
r.Get("/me", meHandler(...))                       // Returns profile identity + candidate state

// Onboarding (replaces register — user is already OIDC-authenticated)
r.Post("/candidates/onboard", onboardHandler(...)) // Creates CandidateProfile linked by ProfileID

// Billing (proxies to service-payment)
r.Post("/billing/subscribe", subscribeHandler(...))
r.Post("/billing/checkout", checkoutHandler(...))
r.Post("/billing/mobile-pay", mobilePayHandler(...))
r.Get("/billing/subscription", getSubscriptionHandler(...))
r.Get("/billing/invoices", listInvoicesHandler(...))
r.Post("/billing/cancel", cancelSubscriptionHandler(...))
r.Post("/webhooks/payment", paymentWebhookHandler(...))

// Job seeker extras (ProfileID from claims, not params)
r.Get("/jobs/{id}", getJobHandler(...))
r.Post("/jobs/{id}/save", saveJobHandler(...))
r.Get("/saved-jobs", listSavedJobsHandler(...))
```

### New API Endpoints on API Service

```go
// Category support
r.Get("/categories", listCategoriesHandler(...))
r.Get("/stats", liveStatsHandler(...))
```

## Domain Model Changes

### Design Principle: Sharp Domain Models

Domain models contain only domain-specific data. Identity (name, avatar, contacts) is never stored — it's resolved from service-profile via the `ProfileID` at runtime. This avoids stale copies and keeps models focused on their purpose.

### Changes to `CandidateProfile`

**Remove identity fields** that now come from service-profile:
- Remove `Email` as unique key (replaced by `ProfileID`)
- Remove `Name`, `Phone` (come from service-profile)
- Keep `CVRawText`, `CVUrl` (domain-specific, not identity)

**Replace with profile link:**

```go
// Identity link (from JWT sub claim — never store name/email/avatar here)
ProfileID string `gorm:"type:varchar(255);uniqueIndex;not null" json:"profile_id"`
```

**Add onboarding domain fields:**

```go
// Job search preferences (domain-specific to stawi.opportunities)
TargetJobTitle     string `gorm:"type:text" json:"target_job_title"`
ExperienceLevel    string `gorm:"type:varchar(30)" json:"experience_level"`
JobSearchStatus    string `gorm:"type:varchar(30)" json:"job_search_status"`
PreferredRegions   string `gorm:"type:text" json:"preferred_regions"`
PreferredTimezones string `gorm:"type:text" json:"preferred_timezones"`
USWorkAuth         *bool  `gorm:"type:bool" json:"us_work_auth"`
NeedsSponsorship   *bool  `gorm:"type:bool" json:"needs_sponsorship"`
WantsATSReport     bool   `gorm:"not null;default:false" json:"wants_ats_report"`

// Subscription billing (links to service-payment)
SubscriptionID string `gorm:"type:varchar(255)" json:"subscription_id"`
PlanID         string `gorm:"type:varchar(100)" json:"plan_id"`
```

**Fields that remain unchanged** (these are domain data, not identity):
- `CVUrl`, `CVRawText` — CV storage
- `CurrentTitle`, `Seniority`, `YearsExperience` — AI-extracted profile
- `Skills`, `StrongSkills`, `WorkingSkills`, `ToolsFrameworks` — competencies
- `PreferredLocations`, `PreferredCountries`, `RemotePreference` — preferences
- `SalaryMin`, `SalaryMax`, `Currency` — compensation
- `Subscription`, `AutoApply` — feature flags
- `Embedding`, `MatchesSent`, `LastMatchedAt` — matching metadata
- `CommEmail`, `CommWhatsapp`, `CommTelegram`, `CommSMS` — delivery channel *preferences* (not the actual contact details)

### New: `SavedJob`

```go
type SavedJob struct {
    ID             int64     `gorm:"primaryKey;autoIncrement"`
    ProfileID      string    `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_saved_profile_job"`
    CanonicalJobID int64     `gorm:"not null;index;uniqueIndex:idx_saved_profile_job"`
    SavedAt        time.Time `gorm:"not null"`
}
```

### New Endpoint: `GET /me`

Returns the authenticated user's display identity + stawi.opportunities domain state in one call:

```json
{
  "profile_id": "prf_abc123",
  "name": "Peter Bwire",
  "avatar_url": "https://...",
  "email": "bwire517@gmail.com",
  "roles": ["jobseeker"],
  "candidate": {
    "id": 42,
    "status": "active",
    "subscription": "paid",
    "current_title": "Full Stack Developer",
    "matches_sent": 15
  }
}
```

The `name`, `avatar_url`, and `email` come from service-profile (fetched server-side). The `candidate` block comes from the local `CandidateProfile` table. This is the only place identity and domain data are composed — at the API boundary, never in storage.

## Build & Deploy Pipeline

```
1. Crawl pipeline runs → new jobs in Postgres
2. apps/sitegen generates ui/data/*.json
3. hugo --minify builds static site to ui/public/
4. npx pagefind --site ui/public generates search index
5. Deploy ui/public/ to CDN (Cloudflare Pages / Netlify / k8s nginx)
6. Go API services deployed separately to k8s (already handled)
```

### Makefile Additions

```makefile
sitegen:
	go run ./apps/sitegen/cmd --output-dir ui/data

hugo-build: sitegen
	cd ui && hugo --minify

pagefind: hugo-build
	cd ui && npx pagefind --site public --glob "jobs/**/*.html"

ui-dev:
	cd ui && hugo server --bind 0.0.0.0 --port 1313

ui-build: pagefind
	@echo "Static site built at ui/public/"
```

## Design Language

### Visual Identity

- **Primary color**: Deep navy (#1a1a2e) — professional, trust
- **Accent color**: Warm red (#e74c3c) — CTAs, progress bars (matches WWR's red accent)
- **Background**: White (#ffffff) main, light gray (#f8f9fa) sections
- **Text**: Dark gray (#333333) body, black (#000000) headings
- **Typography**: Inter (headings, UI) + system font stack (body) — clean, accessible, free

### Component Design Principles

- Cards have subtle shadows (`shadow-sm`), rounded corners (`rounded-lg`)
- Buttons: solid fill for primary actions, outlined for secondary
- Form inputs: clear labels above, helpful placeholder text, inline validation
- Spacing: consistent 4px grid (Tailwind's spacing scale)
- Icons: Heroicons (MIT, designed for Tailwind, accessible)

## Out of Scope (Future Sub-projects)

1. **Employer ATS** — job posting, applicant pipeline, employer billing (% of hire)
2. **Admin Dashboard** — data browser, analytics, source health, candidate management
3. **Dark mode** — respects system preference (CSS custom properties ready)
4. **Mobile app** — PWA capabilities added to the static site as an enhancement
5. **Email notifications** — match alerts, application updates (via service-notification)
6. **Auto-apply** — automated job applications (already in domain model, needs implementation)
