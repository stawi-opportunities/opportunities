# Stawi.jobs UI Platform Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Hugo static site with Alpine.js SPA islands serving 37K+ remote job listings, 3-step onboarding, candidate dashboard, Pagefind search, OIDC auth, and payment via antinvestor/service-payment.

**Architecture:** Hugo generates static HTML for all job/category pages from JSON data produced by a Go `sitegen` CLI. Dynamic features (auth, onboarding wizard, dashboard, live search) are Alpine.js components embedded in Hugo page shells. Identity comes from OIDC claims + service-profile; domain models store only job-seeking data linked by `ProfileID`.

**Tech Stack:** Hugo (0.147+), Alpine.js 3.x, Tailwind CSS (Hugo Pipes), Pagefind, Go (sitegen CLI + API extensions), antinvestor/service-payment (Polar.sh, M-PESA/MTN/Airtel), OIDC PKCE

**Spec:** `docs/superpowers/specs/2026-04-16-ui-job-seeker-platform-design.md`

---

## File Map

### Go Backend Changes

| File | Action | Responsibility |
|------|--------|---------------|
| `pkg/domain/candidate.go` | Modify | Add `ProfileID`, onboarding fields, billing fields; remove identity fields (`Name`, `Email`, `Phone`) |
| `pkg/domain/models.go` | Modify | Add `SavedJob` model, `JobCategory` constants |
| `pkg/repository/candidate.go` | Modify | Add `GetByProfileID`, update `Create`/`Update` to use `ProfileID` |
| `pkg/repository/saved_job.go` | Create | `SavedJobRepository` with `Save`, `Delete`, `ListForProfile`, `Exists` |
| `pkg/repository/job.go` | Modify | Add `GetCanonicalByID`, `ListActiveCanonical`, `CountByCategory` |
| `apps/sitegen/cmd/main.go` | Create | CLI tool: reads Postgres, writes `ui/data/*.json` |
| `apps/candidates/config/config.go` | Modify | Add service-payment connection config |
| `apps/candidates/cmd/main.go` | Modify | Add `/me`, `/candidates/onboard`, `/billing/*`, `/jobs/{id}/save`, saved-jobs endpoints |
| `apps/api/cmd/main.go` | Modify | Add `/categories`, `/stats`, `/jobs/{id}` endpoints |

### Hugo UI

| File | Action | Responsibility |
|------|--------|---------------|
| `ui/hugo.toml` | Create | Site config, params (API URLs, OIDC config), Tailwind setup |
| `ui/package.json` | Create | Tailwind, Alpine.js, Pagefind deps |
| `ui/tailwind.config.js` | Create | Theme colors, font config |
| `ui/postcss.config.js` | Create | Tailwind PostCSS pipeline |
| `ui/assets/css/main.css` | Create | Tailwind directives + custom component classes |
| `ui/assets/js/app.js` | Create | Alpine.js init, global auth store, API client |
| `ui/assets/js/auth.js` | Create | OIDC PKCE flow (login, callback, token refresh) |
| `ui/assets/js/onboarding.js` | Create | 3-step wizard state machine |
| `ui/assets/js/dashboard.js` | Create | Dashboard views (matches, apps, profile, billing) |
| `ui/assets/js/search.js` | Create | Search island (Pagefind + semantic API fallback) |
| `ui/layouts/_default/baseof.html` | Create | Base template: html, head, nav, footer, Alpine init |
| `ui/layouts/partials/head.html` | Create | Meta tags, OG, Tailwind CSS, Alpine.js |
| `ui/layouts/partials/navbar.html` | Create | Top nav with search, auth state, dropdowns |
| `ui/layouts/partials/footer.html` | Create | Footer links, copyright |
| `ui/layouts/partials/job-card.html` | Create | Reusable job card component |
| `ui/layouts/partials/skip-nav.html` | Create | Skip to content accessibility link |
| `ui/layouts/partials/breadcrumbs.html` | Create | Breadcrumb navigation |
| `ui/layouts/partials/auth-guard.html` | Create | Authenticated content wrapper |
| `ui/layouts/index.html` | Create | Homepage layout |
| `ui/layouts/jobs/list.html` | Create | Job listing with pagination |
| `ui/layouts/jobs/single.html` | Create | Job detail page with JSON-LD |
| `ui/layouts/categories/list.html` | Create | Category listing |
| `ui/layouts/categories/single.html` | Create | Category job list |
| `ui/layouts/onboarding/single.html` | Create | Wizard mount point |
| `ui/layouts/dashboard/single.html` | Create | Dashboard SPA mount |
| `ui/layouts/auth/single.html` | Create | Auth flow mount |
| `ui/layouts/search/list.html` | Create | Full search page |
| `ui/content/_index.md` | Create | Homepage frontmatter |
| `ui/content/jobs/_content.gotmpl` | Create | Content adapter: jobs from data |
| `ui/content/categories/_content.gotmpl` | Create | Content adapter: categories |
| `ui/content/onboarding/_index.md` | Create | Wizard shell page |
| `ui/content/dashboard/_index.md` | Create | Dashboard shell |
| `ui/content/auth/login.md` | Create | Login shell |
| `ui/content/auth/callback.md` | Create | Callback shell |
| `ui/content/search/_index.md` | Create | Search page shell |
| `ui/content/about.md` | Create | About page |
| `ui/content/pricing.md` | Create | Pricing page |
| `ui/content/terms.md` | Create | Terms of service |
| `ui/content/privacy.md` | Create | Privacy policy |
| `ui/data/testimonials.json` | Create | Curated testimonials |
| `ui/static/images/` | Create | Logo, OG image |

---

## Task Dependency Graph

```
Task 1 (Domain models) ──→ Task 2 (Repositories) ──→ Task 3 (Sitegen CLI)
                                                  ──→ Task 4 (API extensions)
                                                  ──→ Task 5 (Candidates API)

Task 6 (Hugo scaffold) ── independent ──
Task 7 (Base layouts)  ──→ Task 8 (Homepage)
                       ──→ Task 9 (Job pages)
                       ──→ Task 10 (Category pages)

Task 3 done + Task 9 done ──→ Task 11 (Content adapters + sitegen integration)

Task 7 done ──→ Task 12 (Alpine.js + auth)
            ──→ Task 13 (Search + Pagefind)
            ──→ Task 14 (Onboarding wizard)

Task 5 done + Task 12 done ──→ Task 15 (Dashboard)
Task 5 done + Task 14 done ──→ Task 16 (Payments integration)

Task 16 done ──→ Task 17 (Accessibility audit + polish)
             ──→ Task 18 (Build pipeline + Makefile)
```

**Parallel tracks:**
- **Track A (Go backend):** Tasks 1→2→3→4→5
- **Track B (Hugo UI):** Tasks 6→7→8→9→10
- **Merge:** Task 11 (content adapters need both tracks)
- **Track C (SPA islands):** Tasks 12→13→14→15→16
- **Final:** Tasks 17→18

---

## Task 1: Domain Model Updates

**Files:**
- Modify: `pkg/domain/candidate.go`
- Modify: `pkg/domain/models.go`

- [ ] **Step 1: Add ProfileID and onboarding fields to CandidateProfile**

In `pkg/domain/candidate.go`, replace the identity fields with `ProfileID` and add onboarding/billing fields:

```go
// CandidateProfile stores job-seeking domain data for a registered user.
// Identity (name, avatar, contacts) is resolved from service-profile via ProfileID.
type CandidateProfile struct {
	ID           int64            `gorm:"primaryKey;autoIncrement" json:"id"`
	ProfileID    string           `gorm:"type:varchar(255);uniqueIndex;not null" json:"profile_id"`
	Status       CandidateStatus  `gorm:"type:varchar(20);not null;default:'unverified'" json:"status"`
	Subscription SubscriptionTier `gorm:"type:varchar(20);not null;default:'free'" json:"subscription"`
	AutoApply    bool             `gorm:"not null;default:false" json:"auto_apply"`

	// CV storage
	CVUrl      string `gorm:"type:text" json:"cv_url"`
	CVRawText  string `gorm:"type:text" json:"-"`

	// AI-extracted profile fields
	CurrentTitle     string `gorm:"type:text" json:"current_title"`
	Seniority        string `gorm:"type:varchar(30)" json:"seniority"`
	YearsExperience  int    `gorm:"type:int" json:"years_experience"`
	Skills           string `gorm:"type:text" json:"skills"`
	StrongSkills     string `gorm:"type:text" json:"strong_skills"`
	WorkingSkills    string `gorm:"type:text" json:"working_skills"`
	ToolsFrameworks  string `gorm:"type:text" json:"tools_frameworks"`
	Certifications   string `gorm:"type:text" json:"certifications"`
	PreferredRoles   string `gorm:"type:text" json:"preferred_roles"`
	Industries       string `gorm:"type:text" json:"industries"`
	Education        string `gorm:"type:text" json:"education"`

	// Job preferences
	PreferredLocations  string  `gorm:"type:text" json:"preferred_locations"`
	PreferredCountries  string  `gorm:"type:text" json:"preferred_countries"`
	RemotePreference    string  `gorm:"type:varchar(20)" json:"remote_preference"`
	SalaryMin           float32 `gorm:"type:real" json:"salary_min"`
	SalaryMax           float32 `gorm:"type:real" json:"salary_max"`
	Currency            string  `gorm:"type:varchar(10)" json:"currency"`

	// Onboarding domain fields
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

	// Additional profile fields
	Languages   string `gorm:"type:text" json:"languages"`
	Bio         string `gorm:"type:text" json:"bio"`
	WorkHistory string `gorm:"type:jsonb;default:'[]'" json:"work_history"`

	// Communication channel preferences (not contact details — those come from service-profile)
	CommEmail      bool   `gorm:"not null;default:true" json:"comm_email"`
	CommWhatsapp   bool   `gorm:"not null;default:false" json:"comm_whatsapp"`
	CommTelegram   bool   `gorm:"not null;default:false" json:"comm_telegram"`
	CommSMS        bool   `gorm:"not null;default:false" json:"comm_sms"`

	// Matching metadata
	Embedding       string     `gorm:"type:text" json:"-"`
	MatchesSent     int        `gorm:"not null;default:0" json:"matches_sent"`
	LastMatchedAt   *time.Time `json:"last_matched_at"`
	LastContactedAt *time.Time `json:"last_contacted_at"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
```

Note: The fields `Email`, `Name`, `Phone`, `WhatsappNumber`, `TelegramHandle` are removed. Identity comes from service-profile via `ProfileID`.

- [ ] **Step 2: Add SavedJob model and JobCategory to models.go**

Append to `pkg/domain/models.go`:

```go
// SavedJob records a job bookmarked by a user.
type SavedJob struct {
	ID             int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ProfileID      string    `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_saved_profile_job" json:"profile_id"`
	CanonicalJobID int64     `gorm:"not null;index;uniqueIndex:idx_saved_profile_job" json:"canonical_job_id"`
	SavedAt        time.Time `gorm:"not null" json:"saved_at"`
}

func (SavedJob) TableName() string { return "saved_jobs" }

// JobCategory maps display categories for the UI.
type JobCategory string

const (
	CategoryProgramming     JobCategory = "programming"
	CategoryDesign          JobCategory = "design"
	CategoryCustomerSupport JobCategory = "customer-support"
	CategoryMarketing       JobCategory = "marketing"
	CategorySales           JobCategory = "sales"
	CategoryDevOps          JobCategory = "devops"
	CategoryProduct         JobCategory = "product"
	CategoryData            JobCategory = "data"
	CategoryManagement      JobCategory = "management"
	CategoryOther           JobCategory = "other"
)

// DeriveCategory returns a JobCategory from roles and industry fields.
func DeriveCategory(roles, industry string) JobCategory {
	lower := strings.ToLower(roles + " " + industry)
	switch {
	case containsAny(lower, "developer", "engineer", "programmer", "software", "backend", "frontend", "full-stack", "fullstack"):
		return CategoryProgramming
	case containsAny(lower, "designer", "ux", "ui", "graphic", "visual"):
		return CategoryDesign
	case containsAny(lower, "support", "customer success", "customer service", "help desk"):
		return CategoryCustomerSupport
	case containsAny(lower, "marketing", "growth", "seo", "content", "social media"):
		return CategoryMarketing
	case containsAny(lower, "sales", "account executive", "business development", "revenue"):
		return CategorySales
	case containsAny(lower, "devops", "sre", "infrastructure", "platform", "reliability"):
		return CategoryDevOps
	case containsAny(lower, "product manager", "product owner", "product lead"):
		return CategoryProduct
	case containsAny(lower, "data scientist", "data engineer", "analyst", "machine learning", "ai"):
		return CategoryData
	case containsAny(lower, "manager", "director", "vp", "head of", "chief", "lead"):
		return CategoryManagement
	default:
		return CategoryOther
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Run tests to verify compilation**

Run: `go build ./...`
Expected: compiles with no errors (existing code referencing `Email`/`Name`/`Phone` on `CandidateProfile` will break — fix in next steps)

- [ ] **Step 4: Fix compilation errors in candidates service**

Update all references to `Email`, `Name`, `Phone` on `CandidateProfile` in `apps/candidates/cmd/main.go`. The `registerHandler` and `inboundEmailHandler` reference these — they will be replaced in Task 5. For now, make them compile by temporarily using `ProfileID` where `Email` was the lookup key.

In `apps/candidates/cmd/main.go`, update `applyCVFields` to remove name/phone assignments:

```go
func applyCVFields(candidate *domain.CandidateProfile, fields *extraction.CVFields) {
	// Name and Phone no longer stored — they come from service-profile
	if fields.CurrentTitle != "" {
		candidate.CurrentTitle = fields.CurrentTitle
	}
	if fields.Seniority != "" {
		candidate.Seniority = fields.Seniority
	}
	// ... rest stays the same (all other fields are domain data)
}
```

Remove `isValidEmail` function and its usages. Update `getProfileHandler` to use `ProfileID` query param instead of `email`. Update `updateProfileHandler` similarly.

- [ ] **Step 5: Verify full build passes**

Run: `go build ./...`
Expected: clean compilation

- [ ] **Step 6: Commit**

```bash
git add pkg/domain/candidate.go pkg/domain/models.go apps/candidates/cmd/main.go
git commit -m "refactor: replace identity fields with ProfileID on CandidateProfile

Add SavedJob model, JobCategory derivation, onboarding and billing
fields. Identity (name, avatar, contacts) now resolved from
service-profile at runtime."
```

---

## Task 2: Repository Updates

**Files:**
- Modify: `pkg/repository/candidate.go`
- Create: `pkg/repository/saved_job.go`
- Modify: `pkg/repository/job.go`

- [ ] **Step 1: Add GetByProfileID to CandidateRepository**

In `pkg/repository/candidate.go`, add:

```go
// GetByProfileID returns the candidate profile linked to an OIDC profile ID.
func (r *CandidateRepository) GetByProfileID(ctx context.Context, profileID string) (*domain.CandidateProfile, error) {
	var c domain.CandidateProfile
	err := r.db(ctx, true).Where("profile_id = ?", profileID).First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}
```

Add `"errors"` to the import block if not present.

- [ ] **Step 2: Create SavedJobRepository**

Create `pkg/repository/saved_job.go`:

```go
package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"stawi.opportunities/pkg/domain"
)

// SavedJobRepository manages bookmarked jobs.
type SavedJobRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewSavedJobRepository creates a SavedJobRepository.
func NewSavedJobRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *SavedJobRepository {
	return &SavedJobRepository{db: db}
}

// Save bookmarks a job for a profile. Idempotent — does nothing if already saved.
func (r *SavedJobRepository) Save(ctx context.Context, profileID string, jobID int64) error {
	sj := domain.SavedJob{
		ProfileID:      profileID,
		CanonicalJobID: jobID,
		SavedAt:        time.Now(),
	}
	result := r.db(ctx, false).
		Where("profile_id = ? AND canonical_job_id = ?", profileID, jobID).
		FirstOrCreate(&sj)
	return result.Error
}

// Delete removes a bookmark.
func (r *SavedJobRepository) Delete(ctx context.Context, profileID string, jobID int64) error {
	return r.db(ctx, false).
		Where("profile_id = ? AND canonical_job_id = ?", profileID, jobID).
		Delete(&domain.SavedJob{}).Error
}

// ListForProfile returns all saved jobs for a profile, newest first.
func (r *SavedJobRepository) ListForProfile(ctx context.Context, profileID string, limit int) ([]domain.SavedJob, error) {
	var jobs []domain.SavedJob
	err := r.db(ctx, true).
		Where("profile_id = ?", profileID).
		Order("saved_at DESC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// Exists checks if a job is saved by a profile.
func (r *SavedJobRepository) Exists(ctx context.Context, profileID string, jobID int64) (bool, error) {
	var count int64
	err := r.db(ctx, true).Model(&domain.SavedJob{}).
		Where("profile_id = ? AND canonical_job_id = ?", profileID, jobID).
		Count(&count).Error
	return count > 0, err
}
```

- [ ] **Step 3: Add GetCanonicalByID, ListActiveCanonical, CountByCategory to JobRepository**

In `pkg/repository/job.go`, add:

```go
// GetCanonicalByID returns a single canonical job by ID.
func (r *JobRepository) GetCanonicalByID(ctx context.Context, id int64) (*domain.CanonicalJob, error) {
	var j domain.CanonicalJob
	err := r.db(ctx, true).Where("id = ? AND is_active = true", id).First(&j).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &j, nil
}

// ListActiveCanonical returns active canonical jobs for sitegen export.
func (r *JobRepository) ListActiveCanonical(ctx context.Context, minQuality float64, limit, offset int) ([]*domain.CanonicalJob, error) {
	var jobs []*domain.CanonicalJob
	err := r.db(ctx, true).
		Where("is_active = true AND quality_score >= ?", minQuality).
		Order("posted_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&jobs).Error
	return jobs, err
}

// CountByCategory returns job counts grouped by derived category.
// Returns a map of category slug → count.
func (r *JobRepository) CountByCategory(ctx context.Context) (map[string]int64, error) {
	var jobs []*domain.CanonicalJob
	err := r.db(ctx, true).
		Select("roles, industry").
		Where("is_active = true").
		Find(&jobs).Error
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int64)
	for _, j := range jobs {
		cat := string(domain.DeriveCategory(j.Roles, j.Industry))
		counts[cat]++
	}
	return counts, nil
}
```

Add `"errors"` to the import block if not present.

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: clean compilation

- [ ] **Step 5: Commit**

```bash
git add pkg/repository/candidate.go pkg/repository/saved_job.go pkg/repository/job.go
git commit -m "feat: add ProfileID lookup, SavedJobRepository, category counts"
```

---

## Task 3: Sitegen CLI

**Files:**
- Create: `apps/sitegen/cmd/main.go`

- [ ] **Step 1: Create the sitegen CLI**

```bash
mkdir -p apps/sitegen/cmd
```

Create `apps/sitegen/cmd/main.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"stawi.opportunities/pkg/domain"
	"stawi.opportunities/pkg/repository"
)

type jobEntry struct {
	ID             int64    `json:"id"`
	Slug           string   `json:"slug"`
	Title          string   `json:"title"`
	Company        string   `json:"company"`
	CompanySlug    string   `json:"company_slug"`
	Category       string   `json:"category"`
	LocationText   string   `json:"location_text"`
	RemoteType     string   `json:"remote_type"`
	EmploymentType string   `json:"employment_type"`
	SalaryMin      float64  `json:"salary_min"`
	SalaryMax      float64  `json:"salary_max"`
	Currency       string   `json:"currency"`
	Seniority      string   `json:"seniority"`
	Skills         []string `json:"skills"`
	Description    string   `json:"description"`
	Excerpt        string   `json:"excerpt"`
	ApplyURL       string   `json:"apply_url"`
	QualityScore   float64  `json:"quality_score"`
	PostedAt       string   `json:"posted_at"`
	IsFeatured     bool     `json:"is_featured"`
}

type categoryEntry struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	JobCount int64  `json:"job_count"`
}

type statsEntry struct {
	TotalJobs       int64  `json:"total_jobs"`
	TotalCompanies  int64  `json:"total_companies"`
	JobsThisWeek    int64  `json:"jobs_this_week"`
	GeneratedAt     string `json:"generated_at"`
}

type companyEntry struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	JobCount int    `json:"job_count"`
}

func main() {
	databaseURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection string")
	outputDir := flag.String("output-dir", "ui/data", "Path to output directory for JSON data files")
	minQuality := flag.Float64("min-quality", 50, "Minimum quality score for inclusion")
	flag.Parse()

	if *databaseURL == "" {
		log.Fatal("--database-url or DATABASE_URL is required")
	}

	db, err := gorm.Open(postgres.Open(*databaseURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	ctx := context.Background()
	dbFn := func(_ context.Context, _ bool) *gorm.DB { return db }
	jobRepo := repository.NewJobRepository(dbFn)

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	// Export jobs
	log.Println("Exporting jobs...")
	var allJobs []jobEntry
	batchSize := 1000
	offset := 0
	companyMap := make(map[string]int)

	for {
		jobs, err := jobRepo.ListActiveCanonical(ctx, *minQuality, batchSize, offset)
		if err != nil {
			log.Fatalf("list jobs: %v", err)
		}
		if len(jobs) == 0 {
			break
		}

		for _, j := range jobs {
			slug := buildSlug(j.Title, j.Company, j.ID)
			companySlug := slugify(j.Company)
			category := string(domain.DeriveCategory(j.Roles, j.Industry))
			skills := splitCSV(j.Skills)
			if len(skills) == 0 {
				skills = splitCSV(j.RequiredSkills)
			}

			excerpt := j.Description
			if len(excerpt) > 200 {
				excerpt = excerpt[:200] + "..."
			}

			postedAt := ""
			if j.PostedAt != nil {
				postedAt = j.PostedAt.Format(time.RFC3339)
			}

			allJobs = append(allJobs, jobEntry{
				ID:             j.ID,
				Slug:           slug,
				Title:          j.Title,
				Company:        j.Company,
				CompanySlug:    companySlug,
				Category:       category,
				LocationText:   j.LocationText,
				RemoteType:     j.RemoteType,
				EmploymentType: j.EmploymentType,
				SalaryMin:      j.SalaryMin,
				SalaryMax:      j.SalaryMax,
				Currency:       j.Currency,
				Seniority:      j.Seniority,
				Skills:         skills,
				Description:    j.Description,
				Excerpt:        excerpt,
				ApplyURL:       j.ApplyURL,
				QualityScore:   j.QualityScore,
				PostedAt:       postedAt,
				IsFeatured:     j.QualityScore >= 80,
			})

			companyMap[j.Company]++
		}

		offset += batchSize
	}
	log.Printf("Exported %d jobs", len(allJobs))

	writeJSON(filepath.Join(*outputDir, "jobs.json"), allJobs)

	// Export categories
	log.Println("Exporting categories...")
	catCounts, err := jobRepo.CountByCategory(ctx)
	if err != nil {
		log.Fatalf("count categories: %v", err)
	}

	categoryNames := map[string]string{
		"programming":      "Programming",
		"design":           "Design",
		"customer-support": "Customer Support",
		"marketing":        "Marketing",
		"sales":            "Sales",
		"devops":           "DevOps & Infrastructure",
		"product":          "Product",
		"data":             "Data Science & Analytics",
		"management":       "Management & Executive",
		"other":            "Other",
	}

	var categories []categoryEntry
	for slug, count := range catCounts {
		name := categoryNames[slug]
		if name == "" {
			name = slug
		}
		categories = append(categories, categoryEntry{
			Slug:     slug,
			Name:     name,
			JobCount: count,
		})
	}
	writeJSON(filepath.Join(*outputDir, "categories.json"), categories)

	// Export stats
	log.Println("Exporting stats...")
	totalJobs, _ := jobRepo.CountCanonical(ctx)

	oneWeekAgo := time.Now().Add(-7 * 24 * time.Hour)
	var jobsThisWeek int64
	db.Model(&domain.CanonicalJob{}).
		Where("is_active = true AND created_at >= ?", oneWeekAgo).
		Count(&jobsThisWeek)

	writeJSON(filepath.Join(*outputDir, "stats.json"), statsEntry{
		TotalJobs:      totalJobs,
		TotalCompanies: int64(len(companyMap)),
		JobsThisWeek:   jobsThisWeek,
		GeneratedAt:    time.Now().Format(time.RFC3339),
	})

	// Export companies
	log.Println("Exporting companies...")
	var companies []companyEntry
	for name, count := range companyMap {
		companies = append(companies, companyEntry{
			Name:     name,
			Slug:     slugify(name),
			JobCount: count,
		})
	}
	writeJSON(filepath.Join(*outputDir, "companies.json"), companies)

	log.Println("Done. Output written to", *outputDir)
}

func writeJSON(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Fatalf("write %s: %v", path, err)
	}
	log.Printf("  wrote %s (%d bytes)", path, len(data))
}

func buildSlug(title, company string, id int64) string {
	return fmt.Sprintf("%s-at-%s-%d", slugify(title), slugify(company), id)
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		" ", "-", "/", "-", "\\", "-", ".", "-",
		",", "", "'", "", "\"", "", "(", "", ")", "",
		"&", "and", "+", "plus",
	)
	s = replacer.Replace(s)
	// Collapse multiple dashes
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./apps/sitegen/cmd`
Expected: clean compilation

- [ ] **Step 3: Commit**

```bash
git add apps/sitegen/
git commit -m "feat: add sitegen CLI for exporting job data to Hugo JSON"
```

---

## Task 4: API Service Extensions

**Files:**
- Modify: `apps/api/cmd/main.go`

- [ ] **Step 1: Add /jobs/{id}, /categories, /stats endpoints**

In `apps/api/cmd/main.go`, add after the existing route definitions:

```go
// GET /jobs/{id} — single job detail
r.Get("/jobs/{id}", func(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	idStr := chi.URLParam(req, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, `{"error":"valid job id required"}`, http.StatusBadRequest)
		return
	}

	job, err := jobRepo.GetCanonicalByID(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if job == nil {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
})

// GET /categories — category list with job counts
r.Get("/categories", func(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	counts, err := jobRepo.CountByCategory(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"categories": counts,
	})
})

// GET /stats — live site statistics
r.Get("/stats", func(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	totalJobs, _ := jobRepo.CountCanonical(ctx)
	totalSources, _ := sourceRepo.Count(ctx)

	var totalCompanies int64
	db.Model(&domain.CanonicalJob{}).
		Where("is_active = true").
		Select("COUNT(DISTINCT company)").
		Scan(&totalCompanies)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"total_jobs":      totalJobs,
		"total_companies": totalCompanies,
		"active_sources":  totalSources,
	})
})
```

- [ ] **Step 2: Verify build**

Run: `go build ./apps/api/cmd`
Expected: clean compilation

- [ ] **Step 3: Commit**

```bash
git add apps/api/cmd/main.go
git commit -m "feat: add /jobs/{id}, /categories, /stats API endpoints"
```

---

## Task 5: Candidates Service — New Endpoints

**Files:**
- Modify: `apps/candidates/cmd/main.go`
- Modify: `apps/candidates/config/config.go`

- [ ] **Step 1: Add service-payment config**

In `apps/candidates/config/config.go`:

```go
package config

import fconfig "github.com/pitabwire/frame/config"

type CandidatesConfig struct {
	fconfig.ConfigurationDefault
	OllamaURL          string `env:"OLLAMA_URL" envDefault:""`
	OllamaModel        string `env:"OLLAMA_MODEL" envDefault:"qwen2.5:1.5b"`
	MaxFreeMatches      int    `env:"MAX_FREE_MATCHES" envDefault:"5"`
	PaymentServiceURL   string `env:"PAYMENT_SERVICE_URL" envDefault:""`
	ProfileServiceURL   string `env:"PROFILE_SERVICE_URL" envDefault:""`
}
```

- [ ] **Step 2: Add /me endpoint**

In `apps/candidates/cmd/main.go`, add the `meHandler`. This reads ProfileID from JWT claims, fetches display info from service-profile, and combines with local candidate data:

```go
// meHandler returns the authenticated user's identity + candidate domain state.
func meHandler(candidateRepo *repository.CandidateRepository, profileServiceURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims := security.ClaimsFromContext(ctx)
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		profileID := claims.GetProfileID()
		if profileID == "" {
			http.Error(w, `{"error":"no profile_id in claims"}`, http.StatusUnauthorized)
			return
		}

		// Fetch display identity from service-profile
		name, avatarURL, email := fetchProfileIdentity(ctx, profileServiceURL, profileID)

		// Fetch local candidate domain data
		candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		roles := claims.GetRoles()

		response := map[string]any{
			"profile_id": profileID,
			"name":       name,
			"avatar_url": avatarURL,
			"email":      email,
			"roles":      roles,
			"candidate":  candidate, // nil if not yet onboarded
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}

// fetchProfileIdentity calls service-profile to get display name, avatar, and primary email.
// Returns empty strings on failure — the UI handles missing identity gracefully.
func fetchProfileIdentity(ctx context.Context, baseURL, profileID string) (name, avatarURL, email string) {
	if baseURL == "" {
		return "", "", ""
	}
	// TODO: Replace with ConnectRPC client call to ProfileService.GetById
	// For now, return empty — the UI will show "User" as fallback
	return "", "", ""
}
```

Add import for `"github.com/pitabwire/frame/security"` at the top.

- [ ] **Step 3: Add /candidates/onboard endpoint**

```go
// onboardHandler creates a CandidateProfile for an authenticated user after onboarding.
func onboardHandler(candidateRepo *repository.CandidateRepository, extractor *extraction.Extractor, svc *frame.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims := security.ClaimsFromContext(ctx)
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		profileID := claims.GetProfileID()

		// Check if already onboarded
		existing, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if existing != nil {
			http.Error(w, `{"error":"already onboarded"}`, http.StatusConflict)
			return
		}

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
			return
		}

		candidate := &domain.CandidateProfile{
			ProfileID:          profileID,
			Status:             domain.CandidateActive,
			TargetJobTitle:     r.FormValue("target_job_title"),
			ExperienceLevel:    r.FormValue("experience_level"),
			JobSearchStatus:    r.FormValue("job_search_status"),
			PreferredRegions:   r.FormValue("preferred_regions"),
			PreferredTimezones: r.FormValue("preferred_timezones"),
			WantsATSReport:     r.FormValue("wants_ats_report") == "true",
			Currency:           r.FormValue("currency"),
		}

		if v := r.FormValue("salary_min"); v != "" {
			if f, err := strconv.ParseFloat(v, 32); err == nil {
				candidate.SalaryMin = float32(f)
			}
		}
		if v := r.FormValue("salary_max"); v != "" {
			if f, err := strconv.ParseFloat(v, 32); err == nil {
				candidate.SalaryMax = float32(f)
			}
		}
		if v := r.FormValue("us_work_auth"); v != "" {
			b := v == "true"
			candidate.USWorkAuth = &b
		}
		if v := r.FormValue("needs_sponsorship"); v != "" {
			b := v == "true"
			candidate.NeedsSponsorship = &b
		}

		// Process CV file if provided
		file, header, fileErr := r.FormFile("cv")
		if fileErr == nil {
			defer file.Close()
			data, readErr := io.ReadAll(file)
			if readErr != nil {
				http.Error(w, `{"error":"failed to read CV file"}`, http.StatusBadRequest)
				return
			}
			cvText, extractErr := extraction.ExtractTextFromFile(data, header.Filename)
			if extractErr != nil {
				http.Error(w, fmt.Sprintf(`{"error":"CV extraction failed: %s"}`, extractErr.Error()), http.StatusBadRequest)
				return
			}
			candidate.CVRawText = cvText

			if extractor != nil && cvText != "" {
				fields, aiErr := extractor.ExtractCV(ctx, cvText)
				if aiErr == nil {
					applyCVFields(candidate, fields)
				}
			}
		}

		if err := candidateRepo.Create(ctx, candidate); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(candidate)
	}
}
```

- [ ] **Step 4: Add saved job endpoints**

```go
// saveJobHandler bookmarks a job for the authenticated user.
func saveJobHandler(savedJobRepo *repository.SavedJobRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := security.ClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		idStr := chi.URLParam(r, "id")
		jobID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || jobID <= 0 {
			http.Error(w, `{"error":"valid job id required"}`, http.StatusBadRequest)
			return
		}

		if err := savedJobRepo.Save(r.Context(), claims.GetProfileID(), jobID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}
}

// unsaveJobHandler removes a job bookmark.
func unsaveJobHandler(savedJobRepo *repository.SavedJobRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := security.ClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		idStr := chi.URLParam(r, "id")
		jobID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || jobID <= 0 {
			http.Error(w, `{"error":"valid job id required"}`, http.StatusBadRequest)
			return
		}

		if err := savedJobRepo.Delete(r.Context(), claims.GetProfileID(), jobID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}
}

// listSavedJobsHandler returns bookmarked jobs for the authenticated user.
func listSavedJobsHandler(savedJobRepo *repository.SavedJobRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := security.ClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
				limit = parsed
			}
		}

		saved, err := savedJobRepo.ListForProfile(r.Context(), claims.GetProfileID(), limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": len(saved),
			"saved": saved,
		})
	}
}
```

- [ ] **Step 5: Wire new routes in main()**

In the route setup section of `main()`, add the new repos and routes:

```go
savedJobRepo := repository.NewSavedJobRepository(dbFn)

// Add to migration list
if cfg.DoDatabaseMigrate() {
	migrationDB := dbFn(ctx, false)
	if migErr := migrationDB.AutoMigrate(
		&domain.CandidateProfile{},
		&domain.CandidateMatch{},
		&domain.CandidateApplication{},
		&domain.SavedJob{},
	); migErr != nil {
		log.WithError(migErr).Fatal("auto-migrate failed")
	}
	log.Info("migration complete")
	return
}

// Routes
r.Get("/me", meHandler(candidateRepo, cfg.ProfileServiceURL))
r.Post("/candidates/onboard", onboardHandler(candidateRepo, extractor, svc))
r.Post("/jobs/{id}/save", saveJobHandler(savedJobRepo))
r.Delete("/jobs/{id}/save", unsaveJobHandler(savedJobRepo))
r.Get("/saved-jobs", listSavedJobsHandler(savedJobRepo))
```

- [ ] **Step 6: Verify build**

Run: `go build ./apps/candidates/cmd`
Expected: clean compilation

- [ ] **Step 7: Commit**

```bash
git add apps/candidates/
git commit -m "feat: add /me, /candidates/onboard, saved-jobs endpoints

Identity resolved from JWT claims + service-profile.
CandidateProfile created on onboard, not on auth."
```

---

## Task 6: Hugo Project Scaffold

**Files:**
- Create: `ui/hugo.toml`
- Create: `ui/package.json`
- Create: `ui/tailwind.config.js`
- Create: `ui/postcss.config.js`
- Create: `ui/assets/css/main.css`
- Create: `ui/.gitignore`

- [ ] **Step 1: Initialize Hugo project**

```bash
mkdir -p ui
cd ui && hugo new site . --force
```

- [ ] **Step 2: Create hugo.toml**

Create `ui/hugo.toml`:

```toml
baseURL = "https://stawi.opportunities/"
languageCode = "en"
title = "Stawi.jobs — Remote Jobs in Africa and Beyond"

[params]
  description = "Find remote jobs from top companies hiring worldwide. Browse 37,000+ positions in programming, design, marketing, and more."
  apiURL = "http://localhost:8082"
  candidatesAPIURL = "http://localhost:8080"
  oidcIssuer = ""
  oidcClientID = ""
  oidcRedirectURI = "http://localhost:1313/auth/callback/"

[params.nav]
  logo = "/images/logo.svg"

[taxonomies]
  category = "categories"

[pagination]
  pagerSize = 20

[outputs]
  home = ["HTML"]
  section = ["HTML"]

[build]
  [build.buildStats]
    enable = true

[module]
  [module.hugoVersion]
    extended = true
    min = "0.147.0"
```

- [ ] **Step 3: Create package.json with Tailwind + Alpine.js**

Create `ui/package.json`:

```json
{
  "name": "opportunities-ui",
  "private": true,
  "scripts": {
    "dev": "hugo server --bind 0.0.0.0 --port 1313",
    "build": "hugo --minify",
    "pagefind": "npx pagefind --site public --glob 'jobs/**/*.html'"
  },
  "devDependencies": {
    "@tailwindcss/forms": "^0.5.9",
    "@tailwindcss/typography": "^0.5.15",
    "autoprefixer": "^10.4.20",
    "pagefind": "^1.3.0",
    "postcss": "^8.4.49",
    "postcss-cli": "^11.0.0",
    "tailwindcss": "^3.4.17"
  },
  "dependencies": {
    "alpinejs": "^3.14.8"
  }
}
```

- [ ] **Step 4: Create Tailwind config**

Create `ui/tailwind.config.js`:

```js
/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./layouts/**/*.html",
    "./content/**/*.md",
    "./assets/js/**/*.js",
  ],
  theme: {
    extend: {
      colors: {
        navy: {
          50: "#f0f1f8",
          100: "#d9dbed",
          200: "#b3b7db",
          300: "#8d93c9",
          400: "#676fb7",
          500: "#414ba5",
          600: "#343c84",
          700: "#272d63",
          800: "#1a1e42",
          900: "#1a1a2e",
          950: "#0d0d17",
        },
        accent: {
          50: "#fef2f2",
          100: "#fee2e2",
          200: "#fecaca",
          300: "#fca5a5",
          400: "#f87171",
          500: "#e74c3c",
          600: "#dc2626",
          700: "#b91c1c",
          800: "#991b1b",
          900: "#7f1d1d",
        },
      },
      fontFamily: {
        sans: ["Inter", "system-ui", "-apple-system", "sans-serif"],
      },
    },
  },
  plugins: [
    require("@tailwindcss/forms"),
    require("@tailwindcss/typography"),
  ],
};
```

- [ ] **Step 5: Create PostCSS config**

Create `ui/postcss.config.js`:

```js
module.exports = {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
};
```

- [ ] **Step 6: Create main CSS**

Create `ui/assets/css/main.css`:

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  html {
    scroll-behavior: smooth;
  }

  @media (prefers-reduced-motion: reduce) {
    html {
      scroll-behavior: auto;
    }
    *,
    *::before,
    *::after {
      animation-duration: 0.01ms !important;
      animation-iteration-count: 1 !important;
      transition-duration: 0.01ms !important;
    }
  }

  :focus-visible {
    outline: 3px solid #e74c3c;
    outline-offset: 2px;
  }

  body {
    @apply text-gray-800 antialiased;
  }
}

@layer components {
  .btn-primary {
    @apply inline-flex items-center justify-center rounded-lg bg-accent-500 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-accent-600 focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2;
  }

  .btn-secondary {
    @apply inline-flex items-center justify-center rounded-lg border border-gray-300 bg-white px-6 py-3 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 focus-visible:ring-2 focus-visible:ring-gray-300 focus-visible:ring-offset-2;
  }

  .badge {
    @apply inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium;
  }

  .badge-featured {
    @apply badge bg-accent-100 text-accent-700;
  }

  .badge-new {
    @apply badge bg-green-100 text-green-700;
  }

  .badge-type {
    @apply badge bg-navy-100 text-navy-700;
  }

  .card {
    @apply rounded-lg border border-gray-200 bg-white shadow-sm transition-shadow hover:shadow-md;
  }

  .input-field {
    @apply block w-full rounded-lg border border-gray-300 px-4 py-3 text-sm text-gray-900 placeholder-gray-400 focus:border-accent-500 focus:ring-1 focus:ring-accent-500;
  }

  .select-field {
    @apply input-field appearance-none bg-white;
  }
}
```

- [ ] **Step 7: Create .gitignore**

Create `ui/.gitignore`:

```
node_modules/
public/
resources/
.hugo_build.lock
pagefind/
```

- [ ] **Step 8: Install dependencies**

Run: `cd ui && npm install`
Expected: node_modules created, no errors

- [ ] **Step 9: Commit**

```bash
git add ui/
git commit -m "feat: scaffold Hugo project with Tailwind CSS and Alpine.js"
```

---

## Task 7: Base Layouts and Partials

**Files:**
- Create: `ui/layouts/_default/baseof.html`
- Create: `ui/layouts/partials/head.html`
- Create: `ui/layouts/partials/skip-nav.html`
- Create: `ui/layouts/partials/navbar.html`
- Create: `ui/layouts/partials/footer.html`
- Create: `ui/layouts/partials/breadcrumbs.html`
- Create: `ui/layouts/partials/auth-guard.html`
- Create: `ui/layouts/_default/list.html`
- Create: `ui/layouts/_default/single.html`

- [ ] **Step 1: Create baseof.html**

Create `ui/layouts/_default/baseof.html`:

```html
<!DOCTYPE html>
<html lang="en" x-data="appRoot()" x-init="init()">
<head>
  {{ partial "head.html" . }}
</head>
<body class="min-h-screen flex flex-col bg-white">
  {{ partial "skip-nav.html" . }}
  {{ partial "navbar.html" . }}

  <main id="main-content" class="flex-1" role="main">
    {{ block "main" . }}{{ end }}
  </main>

  {{ partial "footer.html" . }}

  {{ $appJS := resources.Get "js/app.js" | js.Build (dict "minify" hugo.IsProduction) }}
  {{ $authJS := resources.Get "js/auth.js" | js.Build (dict "minify" hugo.IsProduction) }}
  <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.14.8/dist/cdn.min.js"></script>
  <script src="{{ $appJS.RelPermalink }}"></script>
  <script src="{{ $authJS.RelPermalink }}"></script>
  {{ block "scripts" . }}{{ end }}
</body>
</html>
```

- [ ] **Step 2: Create head.html partial**

Create `ui/layouts/partials/head.html`:

```html
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ if .IsHome }}{{ .Site.Title }}{{ else }}{{ .Title }} — {{ .Site.Title }}{{ end }}</title>
<meta name="description" content="{{ with .Description }}{{ . }}{{ else }}{{ .Site.Params.description }}{{ end }}">

<!-- Open Graph -->
<meta property="og:title" content="{{ .Title }}">
<meta property="og:description" content="{{ with .Description }}{{ . }}{{ else }}{{ .Site.Params.description }}{{ end }}">
<meta property="og:type" content="{{ if .IsHome }}website{{ else }}article{{ end }}">
<meta property="og:url" content="{{ .Permalink }}">
<meta property="og:image" content="{{ "/images/og-default.png" | absURL }}">
<meta property="og:site_name" content="{{ .Site.Title }}">

<!-- Twitter -->
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="{{ .Title }}">
<meta name="twitter:description" content="{{ with .Description }}{{ . }}{{ else }}{{ .Site.Params.description }}{{ end }}">

<!-- Canonical -->
<link rel="canonical" href="{{ .Permalink }}">

<!-- Favicon -->
<link rel="icon" href="/images/favicon.svg" type="image/svg+xml">

<!-- CSS -->
{{ $css := resources.Get "css/main.css" | resources.PostCSS | minify | fingerprint }}
<link rel="stylesheet" href="{{ $css.RelPermalink }}" integrity="{{ $css.Data.Integrity }}">

<!-- Preload fonts -->
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
```

- [ ] **Step 3: Create skip-nav.html**

Create `ui/layouts/partials/skip-nav.html`:

```html
<a href="#main-content"
   class="sr-only focus:not-sr-only focus:fixed focus:top-4 focus:left-4 focus:z-50 focus:rounded-lg focus:bg-accent-500 focus:px-4 focus:py-2 focus:text-white focus:shadow-lg">
  Skip to main content
</a>
```

- [ ] **Step 4: Create navbar.html**

Create `ui/layouts/partials/navbar.html`:

```html
<header class="sticky top-0 z-40 border-b border-gray-200 bg-white" role="banner">
  <nav class="mx-auto flex h-16 max-w-7xl items-center justify-between px-4 sm:px-6 lg:px-8"
       aria-label="Main navigation">
    <!-- Logo -->
    <a href="/" class="flex items-center gap-2 text-xl font-bold text-navy-900">
      STAWI.JOBS
    </a>

    <!-- Desktop Nav -->
    <div class="hidden items-center gap-6 md:flex">
      <!-- Find Jobs dropdown -->
      <div x-data="{ open: false }" class="relative">
        <button @click="open = !open"
                @keydown.escape="open = false"
                :aria-expanded="open"
                aria-haspopup="true"
                class="flex items-center gap-1 text-sm font-medium text-gray-700 hover:text-navy-900">
          Find Jobs
          <svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
          </svg>
        </button>
        <div x-show="open"
             x-transition
             @click.outside="open = false"
             class="absolute left-0 top-full mt-2 w-56 rounded-lg border border-gray-200 bg-white py-2 shadow-lg"
             role="menu">
          {{ range .Site.Data.categories }}
          <a href="/categories/{{ .slug }}/"
             class="block px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
             role="menuitem">
            {{ .name }}
            <span class="ml-1 text-xs text-gray-400">({{ .job_count }})</span>
          </a>
          {{ end }}
        </div>
      </div>

      <a href="/about/" class="text-sm font-medium text-gray-700 hover:text-navy-900">About</a>
      <a href="/pricing/" class="text-sm font-medium text-gray-700 hover:text-navy-900">Pricing</a>
    </div>

    <!-- Search + Auth -->
    <div class="flex items-center gap-4">
      <!-- Search bar -->
      <div class="hidden sm:block" id="navbar-search"></div>

      <!-- Auth state -->
      <div x-show="$store.auth.isAuthenticated" x-cloak class="flex items-center gap-3">
        <a href="/dashboard/" class="text-sm font-medium text-gray-700 hover:text-navy-900">Dashboard</a>
        <button @click="$store.auth.logout()"
                class="text-sm text-gray-500 hover:text-gray-700">
          Sign Out
        </button>
        <div class="flex h-8 w-8 items-center justify-center rounded-full bg-navy-100 text-sm font-medium text-navy-700">
          <span x-text="$store.auth.initials"></span>
        </div>
      </div>
      <div x-show="!$store.auth.isAuthenticated" x-cloak>
        <a href="/auth/login/" class="btn-primary text-sm">Sign In</a>
      </div>

      <!-- Mobile menu button -->
      <button @click="mobileMenuOpen = !mobileMenuOpen"
              class="md:hidden rounded-lg p-2 text-gray-500 hover:bg-gray-100"
              :aria-expanded="mobileMenuOpen"
              aria-label="Toggle navigation menu">
        <svg class="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/>
        </svg>
      </button>
    </div>
  </nav>

  <!-- Mobile menu -->
  <div x-show="mobileMenuOpen"
       x-transition
       @click.outside="mobileMenuOpen = false"
       class="border-t border-gray-200 bg-white md:hidden"
       role="navigation"
       aria-label="Mobile navigation">
    <div class="space-y-1 px-4 py-3">
      <a href="/jobs/" class="block rounded-lg px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">All Jobs</a>
      {{ range .Site.Data.categories }}
      <a href="/categories/{{ .slug }}/" class="block rounded-lg px-3 py-2 text-sm text-gray-600 hover:bg-gray-50">{{ .name }}</a>
      {{ end }}
      <a href="/about/" class="block rounded-lg px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">About</a>
      <a href="/pricing/" class="block rounded-lg px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">Pricing</a>
    </div>
  </div>
</header>
```

- [ ] **Step 5: Create footer.html**

Create `ui/layouts/partials/footer.html`:

```html
<footer class="border-t border-gray-200 bg-gray-50" role="contentinfo">
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <div class="grid grid-cols-2 gap-8 md:grid-cols-4">
      <div>
        <h3 class="text-sm font-semibold text-gray-900">For Job Seekers</h3>
        <ul class="mt-4 space-y-2" role="list">
          <li><a href="/jobs/" class="text-sm text-gray-600 hover:text-navy-900">Browse Jobs</a></li>
          <li><a href="/categories/" class="text-sm text-gray-600 hover:text-navy-900">Categories</a></li>
          <li><a href="/search/" class="text-sm text-gray-600 hover:text-navy-900">Search</a></li>
          <li><a href="/pricing/" class="text-sm text-gray-600 hover:text-navy-900">Pricing</a></li>
        </ul>
      </div>
      <div>
        <h3 class="text-sm font-semibold text-gray-900">Company</h3>
        <ul class="mt-4 space-y-2" role="list">
          <li><a href="/about/" class="text-sm text-gray-600 hover:text-navy-900">About</a></li>
          <li><a href="/terms/" class="text-sm text-gray-600 hover:text-navy-900">Terms of Service</a></li>
          <li><a href="/privacy/" class="text-sm text-gray-600 hover:text-navy-900">Privacy Policy</a></li>
        </ul>
      </div>
    </div>
    <div class="mt-8 border-t border-gray-200 pt-8">
      <p class="text-center text-sm text-gray-500">&copy; {{ now.Year }} Stawi.jobs. All rights reserved.</p>
    </div>
  </div>
</footer>
```

- [ ] **Step 6: Create breadcrumbs.html**

Create `ui/layouts/partials/breadcrumbs.html`:

```html
{{ if not .IsHome }}
<nav aria-label="Breadcrumb" class="mx-auto max-w-7xl px-4 py-3 sm:px-6 lg:px-8">
  <ol class="flex items-center gap-2 text-sm text-gray-500" role="list">
    <li><a href="/" class="hover:text-navy-900">Home</a></li>
    {{ range .Ancestors.Reverse }}
    <li class="flex items-center gap-2">
      <span aria-hidden="true">/</span>
      <a href="{{ .Permalink }}" class="hover:text-navy-900">{{ .Title }}</a>
    </li>
    {{ end }}
    <li class="flex items-center gap-2" aria-current="page">
      <span aria-hidden="true">/</span>
      <span class="font-medium text-gray-900">{{ .Title }}</span>
    </li>
  </ol>
</nav>
{{ end }}
```

- [ ] **Step 7: Create auth-guard.html**

Create `ui/layouts/partials/auth-guard.html`:

```html
<div x-show="$store.auth.isAuthenticated" x-cloak>
  {{ block "guarded" . }}{{ end }}
</div>
<div x-show="!$store.auth.isAuthenticated" x-cloak class="mx-auto max-w-lg py-20 text-center">
  <h2 class="text-2xl font-bold text-gray-900">Sign in to continue</h2>
  <p class="mt-2 text-gray-600">You need to be signed in to access this page.</p>
  <a href="/auth/login/" class="btn-primary mt-6 inline-block">Sign In</a>
</div>
```

- [ ] **Step 8: Create default list.html and single.html**

Create `ui/layouts/_default/list.html`:

```html
{{ define "main" }}
{{ partial "breadcrumbs.html" . }}
<div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
  <h1 class="text-3xl font-bold text-gray-900">{{ .Title }}</h1>
  {{ with .Content }}<div class="prose mt-4 max-w-none">{{ . }}</div>{{ end }}
  {{ .Paginate .Pages }}
  {{ range (.Paginator).Pages }}
    <article class="mt-4">
      <h2><a href="{{ .Permalink }}">{{ .Title }}</a></h2>
    </article>
  {{ end }}
</div>
{{ end }}
```

Create `ui/layouts/_default/single.html`:

```html
{{ define "main" }}
{{ partial "breadcrumbs.html" . }}
<article class="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
  <h1 class="text-3xl font-bold text-gray-900">{{ .Title }}</h1>
  <div class="prose mt-6 max-w-none">
    {{ .Content }}
  </div>
</article>
{{ end }}
```

- [ ] **Step 9: Verify Hugo builds**

Run: `cd ui && hugo`
Expected: builds with no errors (may warn about missing content, that's fine)

- [ ] **Step 10: Commit**

```bash
git add ui/layouts/
git commit -m "feat: add base Hugo layouts with navbar, footer, accessibility"
```

---

## Task 8: Homepage

**Files:**
- Create: `ui/content/_index.md`
- Create: `ui/layouts/index.html`
- Create: `ui/data/testimonials.json`
- Create: `ui/data/stats.json` (placeholder for dev)

- [ ] **Step 1: Create placeholder data files for development**

Create `ui/data/stats.json`:

```json
{
  "total_jobs": 37000,
  "total_companies": 2500,
  "jobs_this_week": 450,
  "generated_at": "2026-04-16T12:00:00Z"
}
```

Create `ui/data/categories.json`:

```json
[
  {"slug": "programming", "name": "Programming", "job_count": 12000},
  {"slug": "design", "name": "Design", "job_count": 3500},
  {"slug": "customer-support", "name": "Customer Support", "job_count": 2800},
  {"slug": "marketing", "name": "Marketing", "job_count": 2200},
  {"slug": "sales", "name": "Sales", "job_count": 1800},
  {"slug": "devops", "name": "DevOps & Infrastructure", "job_count": 2500},
  {"slug": "product", "name": "Product", "job_count": 1500},
  {"slug": "data", "name": "Data Science & Analytics", "job_count": 3000},
  {"slug": "management", "name": "Management & Executive", "job_count": 1200},
  {"slug": "other", "name": "Other", "job_count": 6500}
]
```

Create `ui/data/testimonials.json`:

```json
[
  {
    "name": "Svetlana",
    "role": "Bluetooth Engineer",
    "quote": "I applied to a few jobs and scheduled my first interview within a week. The matching is spot on."
  },
  {
    "name": "Diego Ardura",
    "role": "Data Analyst",
    "quote": "Stawi has been a great resource during my job hunt. I've discovered new companies and feel more confident about finding the right fit."
  },
  {
    "name": "Amara Osei",
    "role": "Full Stack Developer",
    "quote": "As a developer in Accra, finding quality remote jobs was hard. Stawi changed that completely."
  }
]
```

- [ ] **Step 2: Create homepage content**

Create `ui/content/_index.md`:

```markdown
---
title: "Find Remote Jobs in Africa and Beyond"
description: "Browse 37,000+ remote job listings from top companies hiring worldwide. Programming, design, marketing, and more."
---
```

- [ ] **Step 3: Create homepage layout**

Create `ui/layouts/index.html`:

```html
{{ define "main" }}
<!-- Hero -->
<section class="bg-navy-900 py-16 text-white sm:py-24">
  <div class="mx-auto max-w-7xl px-4 text-center sm:px-6 lg:px-8">
    <h1 class="text-4xl font-bold tracking-tight sm:text-5xl lg:text-6xl">
      Find Remote Jobs in Africa and Beyond
    </h1>
    <p class="mx-auto mt-6 max-w-2xl text-lg text-gray-300">
      Browse {{ with .Site.Data.stats }}{{ .total_jobs | lang.FormatNumber 0 }}{{ end }}+ remote job listings from top companies hiring worldwide.
    </p>
    <div class="mt-8 flex flex-col items-center justify-center gap-4 sm:flex-row">
      <a href="/jobs/" class="btn-primary px-8 py-4 text-base">Browse All Jobs</a>
      <a href="/onboarding/" class="btn-secondary border-white/20 bg-white/10 text-white hover:bg-white/20 px-8 py-4 text-base">
        Get Personalized Matches
      </a>
    </div>
    <!-- Trust stats -->
    <div class="mt-12 flex items-center justify-center gap-12 text-center">
      <div>
        <div class="text-3xl font-bold">{{ with .Site.Data.stats }}{{ .total_jobs | lang.FormatNumber 0 }}+{{ end }}</div>
        <div class="mt-1 text-sm text-gray-400">Jobs Posted</div>
      </div>
      <div class="h-8 w-px bg-white/20" aria-hidden="true"></div>
      <div>
        <div class="text-3xl font-bold">{{ with .Site.Data.stats }}{{ .total_companies | lang.FormatNumber 0 }}+{{ end }}</div>
        <div class="mt-1 text-sm text-gray-400">Companies Hiring</div>
      </div>
    </div>
  </div>
</section>

<!-- Categories -->
<section class="py-16" aria-labelledby="categories-heading">
  <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
    <h2 id="categories-heading" class="text-2xl font-bold text-gray-900">Browse by Category</h2>
    <div class="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {{ range .Site.Data.categories }}
      <a href="/categories/{{ .slug }}/" class="card flex items-center gap-4 p-4 hover:border-accent-300">
        <div>
          <h3 class="font-semibold text-gray-900">{{ .name }}</h3>
          <p class="text-sm text-gray-500">{{ .job_count | lang.FormatNumber 0 }} jobs</p>
        </div>
      </a>
      {{ end }}
    </div>
  </div>
</section>

<!-- Testimonials -->
<section class="bg-gray-50 py-16" aria-labelledby="testimonials-heading">
  <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
    <h2 id="testimonials-heading" class="text-center text-2xl font-bold text-gray-900">
      What Job Seekers Say
    </h2>
    <div class="mt-8 grid grid-cols-1 gap-6 md:grid-cols-3">
      {{ range .Site.Data.testimonials }}
      <blockquote class="card p-6">
        <p class="text-gray-700">"{{ .quote }}"</p>
        <footer class="mt-4">
          <p class="font-semibold text-gray-900">{{ .name }}</p>
          <p class="text-sm text-gray-500">{{ .role }}</p>
        </footer>
      </blockquote>
      {{ end }}
    </div>
  </div>
</section>

<!-- How it works -->
<section class="py-16" aria-labelledby="how-it-works-heading">
  <div class="mx-auto max-w-7xl px-4 text-center sm:px-6 lg:px-8">
    <h2 id="how-it-works-heading" class="text-2xl font-bold text-gray-900">How It Works</h2>
    <div class="mt-8 grid grid-cols-1 gap-8 md:grid-cols-3">
      <div>
        <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-accent-100 text-accent-600 text-lg font-bold" aria-hidden="true">1</div>
        <h3 class="mt-4 text-lg font-semibold text-gray-900">Browse Jobs</h3>
        <p class="mt-2 text-gray-600">Search thousands of remote positions by category, location, or keyword.</p>
      </div>
      <div>
        <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-accent-100 text-accent-600 text-lg font-bold" aria-hidden="true">2</div>
        <h3 class="mt-4 text-lg font-semibold text-gray-900">Create Your Profile</h3>
        <p class="mt-2 text-gray-600">Upload your CV and set preferences. Our AI matches you to the best opportunities.</p>
      </div>
      <div>
        <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-accent-100 text-accent-600 text-lg font-bold" aria-hidden="true">3</div>
        <h3 class="mt-4 text-lg font-semibold text-gray-900">Get Hired</h3>
        <p class="mt-2 text-gray-600">Apply directly or let our matching engine connect you with top employers.</p>
      </div>
    </div>
  </div>
</section>
{{ end }}
```

- [ ] **Step 4: Verify Hugo builds and homepage renders**

Run: `cd ui && hugo server`
Expected: homepage accessible at http://localhost:1313 with hero, categories, testimonials, how-it-works sections

- [ ] **Step 5: Commit**

```bash
git add ui/content/_index.md ui/layouts/index.html ui/data/
git commit -m "feat: add homepage with hero, categories, testimonials, trust stats"
```

---

## Task 9: Job Card Partial and Job Pages

**Files:**
- Create: `ui/layouts/partials/job-card.html`
- Create: `ui/layouts/jobs/list.html`
- Create: `ui/layouts/jobs/single.html`
- Create: `ui/content/jobs/_content.gotmpl`
- Create: `ui/data/jobs.json` (small dev sample)

- [ ] **Step 1: Create sample jobs.json for development**

Create `ui/data/jobs.json` with 3-5 sample entries:

```json
[
  {
    "id": 1,
    "slug": "senior-go-developer-at-acme-corp-1",
    "title": "Senior Go Developer",
    "company": "Acme Corp",
    "company_slug": "acme-corp",
    "category": "programming",
    "location_text": "Remote - Anywhere",
    "remote_type": "fully_remote",
    "employment_type": "Full-Time",
    "salary_min": 80000,
    "salary_max": 120000,
    "currency": "USD",
    "seniority": "senior",
    "skills": ["Go", "PostgreSQL", "Kubernetes", "gRPC"],
    "description": "We are looking for a Senior Go Developer to join our platform team. You will design and build microservices, optimize database queries, and mentor junior engineers.\n\n## Requirements\n- 5+ years Go experience\n- Strong PostgreSQL knowledge\n- Kubernetes deployment experience\n\n## Benefits\n- Fully remote, async-first\n- Competitive salary\n- Learning budget",
    "excerpt": "We are looking for a Senior Go Developer to join our platform team. You will design and build microservices...",
    "apply_url": "https://example.com/apply/1",
    "quality_score": 88.5,
    "posted_at": "2026-04-15T10:00:00Z",
    "is_featured": true
  },
  {
    "id": 2,
    "slug": "product-designer-at-safari-tech-2",
    "title": "Product Designer",
    "company": "Safari Tech",
    "company_slug": "safari-tech",
    "category": "design",
    "location_text": "Remote - Africa",
    "remote_type": "fully_remote",
    "employment_type": "Full-Time",
    "salary_min": 50000,
    "salary_max": 80000,
    "currency": "USD",
    "seniority": "mid",
    "skills": ["Figma", "User Research", "Design Systems"],
    "description": "Join our design team to create beautiful, accessible products for African markets.\n\n## Requirements\n- 3+ years product design\n- Figma expertise\n- User research experience",
    "excerpt": "Join our design team to create beautiful, accessible products for African markets...",
    "apply_url": "https://example.com/apply/2",
    "quality_score": 75.0,
    "posted_at": "2026-04-14T08:00:00Z",
    "is_featured": false
  },
  {
    "id": 3,
    "slug": "data-engineer-at-finflow-3",
    "title": "Data Engineer",
    "company": "FinFlow",
    "company_slug": "finflow",
    "category": "data",
    "location_text": "Remote - EAT timezone preferred",
    "remote_type": "fully_remote",
    "employment_type": "Contract",
    "salary_min": 60000,
    "salary_max": 90000,
    "currency": "USD",
    "seniority": "mid",
    "skills": ["Python", "Apache Spark", "dbt", "BigQuery"],
    "description": "Build data pipelines that power lending decisions across East Africa.\n\n## Requirements\n- Python + SQL\n- Spark or similar\n- dbt experience a plus",
    "excerpt": "Build data pipelines that power lending decisions across East Africa...",
    "apply_url": "https://example.com/apply/3",
    "quality_score": 72.0,
    "posted_at": "2026-04-13T14:00:00Z",
    "is_featured": false
  }
]
```

- [ ] **Step 2: Create content adapter for jobs**

Create `ui/content/jobs/_content.gotmpl`:

```html
{{ range .Site.Data.jobs }}
  {{ $content := dict
    "mediaType" "text/markdown"
    "value"     .description
  }}
  {{ $dates := dict }}
  {{ with .posted_at }}
    {{ $dates = dict "date" (time.AsTime .) "lastmod" (time.AsTime .) }}
  {{ end }}
  {{ $params := dict
    "id"              .id
    "company"         .company
    "company_slug"    .company_slug
    "category"        .category
    "location_text"   .location_text
    "remote_type"     .remote_type
    "employment_type" .employment_type
    "salary_min"      .salary_min
    "salary_max"      .salary_max
    "currency"        .currency
    "seniority"       .seniority
    "skills"          .skills
    "excerpt"         .excerpt
    "apply_url"       .apply_url
    "quality_score"   .quality_score
    "is_featured"     .is_featured
  }}
  {{ $.AddPage (dict
    "path"    .slug
    "title"   .title
    "kind"    "page"
    "content" $content
    "dates"   $dates
    "params"  $params
  ) }}
{{ end }}
```

- [ ] **Step 3: Create job-card.html partial**

Create `ui/layouts/partials/job-card.html`:

```html
{{/* Expects a page context with .Params containing job fields */}}
<article class="card p-4 sm:p-6">
  <a href="{{ .Permalink }}" class="block group">
    <div class="flex items-start justify-between gap-4">
      <div class="min-w-0 flex-1">
        <!-- Company -->
        <p class="text-sm font-medium text-gray-500">{{ .Params.company }}</p>

        <!-- Title -->
        <h3 class="mt-1 text-lg font-semibold text-gray-900 group-hover:text-accent-600 truncate">
          {{ .Title }}
        </h3>

        <!-- Meta row -->
        <div class="mt-2 flex flex-wrap items-center gap-2">
          {{ with .Params.employment_type }}
          <span class="badge-type">{{ . }}</span>
          {{ end }}

          {{ with .Params.seniority }}
          <span class="badge bg-gray-100 text-gray-600 capitalize">{{ . }}</span>
          {{ end }}

          {{ if and .Params.salary_min (gt .Params.salary_min 0.0) }}
          <span class="text-sm font-medium text-green-700">
            {{ .Params.currency }}
            {{ lang.FormatNumber 0 .Params.salary_min }}–{{ lang.FormatNumber 0 .Params.salary_max }}
          </span>
          {{ end }}
        </div>

        <!-- Location -->
        <p class="mt-2 text-sm text-gray-500">
          {{ .Params.location_text }}
        </p>

        <!-- Skills -->
        {{ with .Params.skills }}
        <div class="mt-3 flex flex-wrap gap-1.5">
          {{ range first 5 . }}
          <span class="inline-block rounded bg-navy-50 px-2 py-0.5 text-xs text-navy-700">{{ . }}</span>
          {{ end }}
        </div>
        {{ end }}
      </div>

      <!-- Right side: badges + time -->
      <div class="flex flex-col items-end gap-2 text-right">
        {{ if .Params.is_featured }}
        <span class="badge-featured">Featured</span>
        {{ end }}

        {{ if .Date }}
        <time datetime="{{ .Date.Format "2006-01-02" }}" class="text-xs text-gray-400">
          {{ .Date | time.Format ":date_medium" }}
        </time>
        {{ end }}
      </div>
    </div>
  </a>
</article>
```

- [ ] **Step 4: Create jobs list layout**

Create `ui/layouts/jobs/list.html`:

```html
{{ define "main" }}
{{ partial "breadcrumbs.html" . }}
<div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
  <div class="flex items-center justify-between">
    <h1 class="text-3xl font-bold text-gray-900">All Remote Jobs</h1>
    <p class="text-sm text-gray-500">
      {{ len .Pages }} jobs
    </p>
  </div>

  <!-- Pagefind search mount -->
  <div id="job-search" class="mt-6"></div>

  <!-- Filters (Alpine.js enhanced) -->
  <div x-data="{ activeFilter: 'all' }" class="mt-6 flex flex-wrap gap-2" role="group" aria-label="Filter jobs">
    <button @click="activeFilter = 'all'"
            :class="activeFilter === 'all' ? 'bg-navy-900 text-white' : 'bg-gray-100 text-gray-700'"
            class="rounded-full px-4 py-1.5 text-sm font-medium transition-colors">
      All
    </button>
    <button @click="activeFilter = 'Full-Time'"
            :class="activeFilter === 'Full-Time' ? 'bg-navy-900 text-white' : 'bg-gray-100 text-gray-700'"
            class="rounded-full px-4 py-1.5 text-sm font-medium transition-colors">
      Full-Time
    </button>
    <button @click="activeFilter = 'Contract'"
            :class="activeFilter === 'Contract' ? 'bg-navy-900 text-white' : 'bg-gray-100 text-gray-700'"
            class="rounded-full px-4 py-1.5 text-sm font-medium transition-colors">
      Contract
    </button>
  </div>

  <!-- Job list -->
  <div class="mt-6 space-y-4">
    {{ $paginator := .Paginate .Pages }}
    {{ range $paginator.Pages }}
      {{ partial "job-card.html" . }}
    {{ end }}
  </div>

  <!-- Pagination -->
  {{ template "_internal/pagination.html" . }}
</div>
{{ end }}
```

- [ ] **Step 5: Create job detail layout with JSON-LD**

Create `ui/layouts/jobs/single.html`:

```html
{{ define "main" }}
{{ partial "breadcrumbs.html" . }}
<div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
  <div class="lg:grid lg:grid-cols-3 lg:gap-8">
    <!-- Main content -->
    <div class="lg:col-span-2">
      <header>
        <p class="text-sm font-medium text-gray-500">{{ .Params.company }}</p>
        <h1 class="mt-1 text-3xl font-bold text-gray-900">{{ .Title }}</h1>
        <div class="mt-3 flex flex-wrap items-center gap-3">
          {{ with .Params.employment_type }}<span class="badge-type">{{ . }}</span>{{ end }}
          {{ with .Params.seniority }}<span class="badge bg-gray-100 text-gray-600 capitalize">{{ . }}</span>{{ end }}
          {{ if .Params.is_featured }}<span class="badge-featured">Featured</span>{{ end }}
        </div>
      </header>

      <div class="prose mt-8 max-w-none" data-pagefind-body>
        {{ .Content }}
      </div>
    </div>

    <!-- Sidebar -->
    <aside class="mt-8 lg:mt-0">
      <div class="card sticky top-20 p-6">
        <!-- Apply button -->
        {{ with .Params.apply_url }}
        <a href="{{ . }}"
           target="_blank"
           rel="noopener noreferrer"
           class="btn-primary w-full text-center">
          Apply Now
          <span class="sr-only">for {{ $.Title }} at {{ $.Params.company }}</span>
        </a>
        {{ end }}

        <!-- Save button (requires auth) -->
        <button x-data="saveJob({{ .Params.id }})"
                @click="toggle()"
                :class="saved ? 'bg-accent-50 border-accent-300 text-accent-700' : ''"
                class="btn-secondary mt-3 w-full"
                :aria-pressed="saved">
          <span x-text="saved ? 'Saved' : 'Save Job'"></span>
        </button>

        <dl class="mt-6 space-y-4 text-sm">
          {{ with .Params.location_text }}
          <div>
            <dt class="font-medium text-gray-500">Location</dt>
            <dd class="mt-1 text-gray-900">{{ . }}</dd>
          </div>
          {{ end }}

          {{ if and .Params.salary_min (gt .Params.salary_min 0.0) }}
          <div>
            <dt class="font-medium text-gray-500">Salary</dt>
            <dd class="mt-1 text-gray-900">
              {{ .Params.currency }} {{ lang.FormatNumber 0 .Params.salary_min }}–{{ lang.FormatNumber 0 .Params.salary_max }}
            </dd>
          </div>
          {{ end }}

          {{ with .Params.remote_type }}
          <div>
            <dt class="font-medium text-gray-500">Remote</dt>
            <dd class="mt-1 text-gray-900 capitalize">{{ replace . "_" " " }}</dd>
          </div>
          {{ end }}

          {{ with .Params.skills }}
          <div>
            <dt class="font-medium text-gray-500">Skills</dt>
            <dd class="mt-2 flex flex-wrap gap-1.5">
              {{ range . }}
              <span class="inline-block rounded bg-navy-50 px-2 py-0.5 text-xs text-navy-700" data-pagefind-filter="skill">{{ . }}</span>
              {{ end }}
            </dd>
          </div>
          {{ end }}

          {{ if .Date }}
          <div>
            <dt class="font-medium text-gray-500">Posted</dt>
            <dd class="mt-1 text-gray-900">{{ .Date | time.Format ":date_long" }}</dd>
          </div>
          {{ end }}
        </dl>
      </div>
    </aside>
  </div>
</div>

<!-- JSON-LD structured data for Google Jobs -->
<script type="application/ld+json">
{
  "@context": "https://schema.org/",
  "@type": "JobPosting",
  "title": {{ .Title | jsonify }},
  "description": {{ .Content | plainify | jsonify }},
  "datePosted": "{{ with .Date }}{{ .Format "2006-01-02" }}{{ end }}",
  "employmentType": {{ with .Params.employment_type }}{{ . | jsonify }}{{ else }}"FULL_TIME"{{ end }},
  "hiringOrganization": {
    "@type": "Organization",
    "name": {{ .Params.company | jsonify }}
  },
  "jobLocationType": "TELECOMMUTE"
  {{ if and .Params.salary_min (gt .Params.salary_min 0.0) }},
  "baseSalary": {
    "@type": "MonetaryAmount",
    "currency": {{ .Params.currency | jsonify }},
    "value": {
      "@type": "QuantitativeValue",
      "minValue": {{ .Params.salary_min }},
      "maxValue": {{ .Params.salary_max }},
      "unitText": "YEAR"
    }
  }
  {{ end }}
}
</script>
{{ end }}
```

- [ ] **Step 6: Create jobs section content file**

Create `ui/content/jobs/_index.md`:

```markdown
---
title: "All Remote Jobs"
description: "Browse all remote job listings on Stawi.jobs"
---
```

- [ ] **Step 7: Verify Hugo builds with job pages**

Run: `cd ui && hugo`
Expected: builds successfully, generates job pages in `public/jobs/`

- [ ] **Step 8: Commit**

```bash
git add ui/layouts/jobs/ ui/layouts/partials/job-card.html ui/content/jobs/ ui/data/jobs.json
git commit -m "feat: add job card, listing, detail pages with JSON-LD structured data"
```

---

## Task 10: Category Pages

**Files:**
- Create: `ui/content/categories/_content.gotmpl`
- Create: `ui/content/categories/_index.md`
- Create: `ui/layouts/categories/list.html`
- Create: `ui/layouts/categories/single.html`

- [ ] **Step 1: Create category content adapter**

Create `ui/content/categories/_content.gotmpl`:

```html
{{ range .Site.Data.categories }}
  {{ $content := dict
    "mediaType" "text/markdown"
    "value"     (printf "Browse %d remote %s jobs." .job_count .name)
  }}
  {{ $params := dict
    "job_count" .job_count
    "slug"      .slug
  }}
  {{ $.AddPage (dict
    "path"    .slug
    "title"   .name
    "kind"    "page"
    "content" $content
    "params"  $params
  ) }}
{{ end }}
```

- [ ] **Step 2: Create category index**

Create `ui/content/categories/_index.md`:

```markdown
---
title: "Job Categories"
description: "Browse remote jobs by category"
---
```

- [ ] **Step 3: Create category list layout**

Create `ui/layouts/categories/list.html`:

```html
{{ define "main" }}
{{ partial "breadcrumbs.html" . }}
<div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
  <h1 class="text-3xl font-bold text-gray-900">Job Categories</h1>
  <div class="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
    {{ range .Pages }}
    <a href="{{ .Permalink }}" class="card flex items-center justify-between p-6 hover:border-accent-300">
      <h2 class="text-lg font-semibold text-gray-900">{{ .Title }}</h2>
      <span class="rounded-full bg-navy-50 px-3 py-1 text-sm font-medium text-navy-700">
        {{ .Params.job_count | lang.FormatNumber 0 }}
      </span>
    </a>
    {{ end }}
  </div>
</div>
{{ end }}
```

- [ ] **Step 4: Create category single layout (filtered job list)**

Create `ui/layouts/categories/single.html`:

```html
{{ define "main" }}
{{ partial "breadcrumbs.html" . }}
<div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-3xl font-bold text-gray-900">{{ .Title }} Jobs</h1>
      <p class="mt-1 text-gray-500">{{ .Params.job_count | lang.FormatNumber 0 }} remote positions</p>
    </div>
    <a href="/categories/" class="text-sm text-accent-600 hover:text-accent-700">All Categories</a>
  </div>

  <!-- Filtered jobs for this category -->
  <div class="mt-8 space-y-4">
    {{ $category := .Params.slug }}
    {{ $jobs := where (site.GetPage "/jobs").Pages ".Params.category" $category }}
    {{ range $jobs }}
      {{ partial "job-card.html" . }}
    {{ end }}
    {{ if eq (len $jobs) 0 }}
    <p class="py-12 text-center text-gray-500">No jobs in this category yet. Check back soon.</p>
    {{ end }}
  </div>
</div>
{{ end }}
```

- [ ] **Step 5: Verify build**

Run: `cd ui && hugo`
Expected: category pages generated at `public/categories/programming/`, etc.

- [ ] **Step 6: Commit**

```bash
git add ui/content/categories/ ui/layouts/categories/
git commit -m "feat: add category pages with filtered job listings"
```

---

## Task 11: Static Content Pages

**Files:**
- Create: `ui/content/about.md`
- Create: `ui/content/pricing.md`
- Create: `ui/content/terms.md`
- Create: `ui/content/privacy.md`
- Create: `ui/content/search/_index.md`
- Create: `ui/content/onboarding/_index.md`
- Create: `ui/content/dashboard/_index.md`
- Create: `ui/content/auth/login.md`
- Create: `ui/content/auth/callback.md`

- [ ] **Step 1: Create static content pages**

Create `ui/content/about.md`:

```markdown
---
title: "About Stawi.jobs"
description: "Learn about our mission to connect African talent with global remote opportunities."
---

## Our Mission

Stawi.jobs connects skilled professionals in Africa with top remote companies worldwide. We aggregate 37,000+ verified remote job listings, match them to your profile using AI, and help you land your next role faster.

## How We're Different

- **Curated quality** — Every listing passes our quality gate. No spam, no expired posts.
- **AI-powered matching** — Upload your CV and we'll find jobs that fit your skills and preferences.
- **Africa-first** — Built for professionals in African time zones, with mobile money payment support.

## Contact

For questions or partnerships, reach us at hello@stawi.opportunities.
```

Create `ui/content/pricing.md`:

```markdown
---
title: "Pricing"
description: "Subscription plans for Stawi.jobs"
type: "pricing"
---
```

Create `ui/content/terms.md`:

```markdown
---
title: "Terms of Service"
description: "Terms of service for Stawi.jobs"
---

_Last updated: April 2026_

By using Stawi.jobs, you agree to these terms. Please read them carefully.

## Use of Service

Stawi.jobs provides a job listing aggregation and matching platform. We do not guarantee employment outcomes.

## Subscriptions

Premium subscriptions are billed through our payment partners. Cancellation policies are outlined during checkout.

## Privacy

Your data is handled per our [Privacy Policy](/privacy/).
```

Create `ui/content/privacy.md`:

```markdown
---
title: "Privacy Policy"
description: "Privacy policy for Stawi.jobs"
---

_Last updated: April 2026_

## Data We Collect

- **Profile data**: Information you provide during onboarding (CV, preferences, experience level).
- **Usage data**: Pages visited, jobs viewed, search queries.
- **Payment data**: Processed by our payment partners (Polar.sh, M-PESA). We do not store card numbers.

## How We Use Your Data

- Match you with relevant job listings.
- Send you job alerts and match notifications.
- Improve our matching algorithms.

## Your Rights

You can delete your account and all associated data at any time from your dashboard.
```

- [ ] **Step 2: Create SPA shell pages**

Create `ui/content/search/_index.md`:

```markdown
---
title: "Search Jobs"
description: "Search remote job listings"
type: "search"
---
```

Create `ui/content/onboarding/_index.md`:

```markdown
---
title: "Get Started"
description: "Create your profile and find your perfect remote job"
type: "onboarding"
---
```

Create `ui/content/dashboard/_index.md`:

```markdown
---
title: "Dashboard"
description: "Your job matching dashboard"
type: "dashboard"
---
```

Create `ui/content/auth/login.md`:

```markdown
---
title: "Sign In"
description: "Sign in to Stawi.jobs"
type: "auth"
layout: "single"
---
```

Create `ui/content/auth/callback.md`:

```markdown
---
title: "Signing you in..."
description: "Completing authentication"
type: "auth"
layout: "single"
---
```

- [ ] **Step 3: Verify build**

Run: `cd ui && hugo`
Expected: all content pages generated

- [ ] **Step 4: Commit**

```bash
git add ui/content/
git commit -m "feat: add static content pages and SPA shell pages"
```

---

## Task 12: Alpine.js Core — App Init and Auth

**Files:**
- Create: `ui/assets/js/app.js`
- Create: `ui/assets/js/auth.js`
- Create: `ui/layouts/auth/single.html`

- [ ] **Step 1: Create app.js — Alpine init, auth store, API client**

Create `ui/assets/js/app.js`:

```js
// Alpine.js global initialization for stawi.opportunities
document.addEventListener("alpine:init", () => {
  const params = document.querySelector("meta[name=site-params]");
  const config = params ? JSON.parse(params.content) : {};

  // Global auth store
  Alpine.store("auth", {
    isAuthenticated: false,
    accessToken: null,
    profile: null,
    name: "",
    initials: "",
    roles: [],

    async init() {
      const token = sessionStorage.getItem("stawi_access_token");
      if (token && !isTokenExpired(token)) {
        this.accessToken = token;
        this.isAuthenticated = true;
        await this.fetchProfile();
      }
    },

    setToken(token) {
      this.accessToken = token;
      this.isAuthenticated = true;
      sessionStorage.setItem("stawi_access_token", token);
    },

    async fetchProfile() {
      try {
        const resp = await apiFetch("/me");
        if (resp.ok) {
          const data = await resp.json();
          this.profile = data;
          this.name = data.name || "User";
          this.initials = this.name
            .split(" ")
            .map((w) => w[0])
            .join("")
            .toUpperCase()
            .slice(0, 2);
          this.roles = data.roles || [];
        }
      } catch (e) {
        console.error("Failed to fetch profile:", e);
      }
    },

    hasRole(role) {
      return this.roles.includes(role);
    },

    logout() {
      this.accessToken = null;
      this.isAuthenticated = false;
      this.profile = null;
      this.name = "";
      this.initials = "";
      this.roles = [];
      sessionStorage.removeItem("stawi_access_token");
      window.location.href = "/";
    },
  });

  // API fetch wrapper
  window.apiFetch = async function (path, options = {}) {
    const baseURL = config.candidatesAPIURL || "";
    const token = Alpine.store("auth").accessToken;
    const headers = { ...options.headers };
    if (token) {
      headers["Authorization"] = "Bearer " + token;
    }
    if (
      !headers["Content-Type"] &&
      options.body &&
      !(options.body instanceof FormData)
    ) {
      headers["Content-Type"] = "application/json";
    }
    return fetch(baseURL + path, { ...options, headers });
  };

  // Jobs API fetch (separate base URL)
  window.jobsApiFetch = async function (path, options = {}) {
    const baseURL = config.apiURL || "";
    return fetch(baseURL + path, options);
  };

  // Root component
  window.appRoot = function () {
    return {
      mobileMenuOpen: false,
      init() {
        Alpine.store("auth").init();
      },
    };
  };

  // Save job component
  window.saveJob = function (jobId) {
    return {
      saved: false,
      jobId,
      async init() {
        if (!Alpine.store("auth").isAuthenticated) return;
        try {
          const resp = await apiFetch("/saved-jobs");
          if (resp.ok) {
            const data = await resp.json();
            this.saved = (data.saved || []).some(
              (s) => s.canonical_job_id === this.jobId
            );
          }
        } catch (e) {
          // Ignore — not critical
        }
      },
      async toggle() {
        if (!Alpine.store("auth").isAuthenticated) {
          window.location.href = "/auth/login/";
          return;
        }
        if (this.saved) {
          await apiFetch("/jobs/" + this.jobId + "/save", {
            method: "DELETE",
          });
          this.saved = false;
        } else {
          await apiFetch("/jobs/" + this.jobId + "/save", { method: "POST" });
          this.saved = true;
        }
      },
    };
  };
});

function isTokenExpired(token) {
  try {
    const payload = JSON.parse(atob(token.split(".")[1]));
    return payload.exp * 1000 < Date.now();
  } catch {
    return true;
  }
}
```

- [ ] **Step 2: Create auth.js — OIDC PKCE flow**

Create `ui/assets/js/auth.js`:

```js
// OIDC Authorization Code + PKCE flow for stawi.opportunities

document.addEventListener("alpine:init", () => {
  const params = document.querySelector("meta[name=site-params]");
  const config = params ? JSON.parse(params.content) : {};

  window.oidcLogin = function () {
    return {
      loading: false,
      error: "",
      async startLogin() {
        this.loading = true;
        this.error = "";
        try {
          const codeVerifier = generateCodeVerifier();
          const codeChallenge = await generateCodeChallenge(codeVerifier);
          const state = generateState();

          sessionStorage.setItem("oidc_code_verifier", codeVerifier);
          sessionStorage.setItem("oidc_state", state);

          const authURL = new URL(
            config.oidcIssuer + "/protocol/openid-connect/auth"
          );
          authURL.searchParams.set("response_type", "code");
          authURL.searchParams.set("client_id", config.oidcClientID);
          authURL.searchParams.set("redirect_uri", config.oidcRedirectURI);
          authURL.searchParams.set("scope", "openid profile email");
          authURL.searchParams.set("code_challenge", codeChallenge);
          authURL.searchParams.set("code_challenge_method", "S256");
          authURL.searchParams.set("state", state);

          window.location.href = authURL.toString();
        } catch (e) {
          this.error = "Failed to start login. Please try again.";
          this.loading = false;
        }
      },
    };
  };

  window.oidcCallback = function () {
    return {
      loading: true,
      error: "",
      async init() {
        try {
          const urlParams = new URLSearchParams(window.location.search);
          const code = urlParams.get("code");
          const state = urlParams.get("state");
          const errorParam = urlParams.get("error");

          if (errorParam) {
            this.error = urlParams.get("error_description") || errorParam;
            this.loading = false;
            return;
          }

          if (!code || !state) {
            this.error = "Invalid callback parameters.";
            this.loading = false;
            return;
          }

          const savedState = sessionStorage.getItem("oidc_state");
          if (state !== savedState) {
            this.error = "State mismatch. Please try logging in again.";
            this.loading = false;
            return;
          }

          const codeVerifier = sessionStorage.getItem("oidc_code_verifier");
          if (!codeVerifier) {
            this.error = "Missing code verifier. Please try logging in again.";
            this.loading = false;
            return;
          }

          // Exchange code for tokens
          const tokenURL =
            config.oidcIssuer + "/protocol/openid-connect/token";
          const body = new URLSearchParams({
            grant_type: "authorization_code",
            client_id: config.oidcClientID,
            code: code,
            redirect_uri: config.oidcRedirectURI,
            code_verifier: codeVerifier,
          });

          const resp = await fetch(tokenURL, {
            method: "POST",
            headers: { "Content-Type": "application/x-www-form-urlencoded" },
            body: body.toString(),
          });

          if (!resp.ok) {
            this.error = "Token exchange failed. Please try again.";
            this.loading = false;
            return;
          }

          const tokens = await resp.json();

          // Clean up PKCE state
          sessionStorage.removeItem("oidc_code_verifier");
          sessionStorage.removeItem("oidc_state");

          // Set token in auth store
          Alpine.store("auth").setToken(tokens.access_token);
          await Alpine.store("auth").fetchProfile();

          // Route based on onboarding state
          const profile = Alpine.store("auth").profile;
          if (profile && profile.candidate) {
            window.location.href = "/dashboard/";
          } else {
            window.location.href = "/onboarding/";
          }
        } catch (e) {
          this.error = "Authentication failed. Please try again.";
          this.loading = false;
        }
      },
    };
  };
});

function generateCodeVerifier() {
  const array = new Uint8Array(32);
  crypto.getRandomValues(array);
  return base64URLEncode(array);
}

async function generateCodeChallenge(verifier) {
  const encoder = new TextEncoder();
  const data = encoder.encode(verifier);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return base64URLEncode(new Uint8Array(digest));
}

function generateState() {
  const array = new Uint8Array(16);
  crypto.getRandomValues(array);
  return base64URLEncode(array);
}

function base64URLEncode(buffer) {
  return btoa(String.fromCharCode(...buffer))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}
```

- [ ] **Step 3: Create auth layout**

Create `ui/layouts/auth/single.html`:

```html
{{ define "main" }}
<div class="flex min-h-[60vh] items-center justify-center px-4 py-16">
  {{ if eq .File.BaseFileName "login" }}
  <div x-data="oidcLogin()" class="w-full max-w-md text-center">
    <h1 class="text-3xl font-bold text-gray-900">Sign In</h1>
    <p class="mt-2 text-gray-600">Sign in to access your personalized job matches and dashboard.</p>

    <div x-show="error" x-cloak class="mt-4 rounded-lg bg-red-50 p-4 text-sm text-red-700" role="alert" x-text="error"></div>

    <button @click="startLogin()"
            :disabled="loading"
            class="btn-primary mt-8 w-full"
            :class="loading && 'opacity-50 cursor-wait'">
      <span x-show="!loading">Continue with Single Sign-On</span>
      <span x-show="loading" x-cloak>Redirecting...</span>
    </button>

    <p class="mt-6 text-sm text-gray-500">
      Don't have an account? Signing in will create one automatically.
    </p>
  </div>

  {{ else if eq .File.BaseFileName "callback" }}
  <div x-data="oidcCallback()" class="w-full max-w-md text-center">
    <div x-show="loading" class="space-y-4">
      <div class="mx-auto h-8 w-8 animate-spin rounded-full border-4 border-accent-500 border-t-transparent" aria-label="Loading"></div>
      <p class="text-gray-600">Completing sign in...</p>
    </div>

    <div x-show="error" x-cloak class="space-y-4">
      <p class="text-lg font-semibold text-gray-900">Sign in failed</p>
      <p class="text-sm text-red-600" x-text="error"></p>
      <a href="/auth/login/" class="btn-primary mt-4 inline-block">Try Again</a>
    </div>
  </div>
  {{ end }}
</div>
{{ end }}
```

- [ ] **Step 4: Add site-params meta tag to head.html**

In `ui/layouts/partials/head.html`, add before the CSS link:

```html
<!-- Site params for JS -->
<meta name="site-params" content='{{ dict "apiURL" .Site.Params.apiURL "candidatesAPIURL" .Site.Params.candidatesAPIURL "oidcIssuer" .Site.Params.oidcIssuer "oidcClientID" .Site.Params.oidcClientID "oidcRedirectURI" .Site.Params.oidcRedirectURI | jsonify }}'>
```

- [ ] **Step 5: Verify build**

Run: `cd ui && hugo`
Expected: auth pages generated, JS files built

- [ ] **Step 6: Commit**

```bash
git add ui/assets/js/app.js ui/assets/js/auth.js ui/layouts/auth/ ui/layouts/partials/head.html
git commit -m "feat: add Alpine.js core with OIDC PKCE auth flow and API client"
```

---

## Task 13: Search — Pagefind + Semantic

**Files:**
- Create: `ui/assets/js/search.js`
- Create: `ui/layouts/search/list.html`

- [ ] **Step 1: Create search.js**

Create `ui/assets/js/search.js`:

```js
document.addEventListener("alpine:init", () => {
  window.searchPage = function () {
    return {
      query: "",
      mode: "keyword",
      semanticResults: [],
      semanticLoading: false,
      semanticError: "",

      init() {
        // Initialize Pagefind on the search container
        const el = document.getElementById("pagefind-search");
        if (el && window.PagefindUI) {
          new PagefindUI({
            element: "#pagefind-search",
            pageSize: 20,
            showImages: false,
            showSubResults: false,
            showEmptyFilters: false,
            debounceTimeoutMs: 300,
            translations: {
              placeholder: "Search jobs by title, skill, or company...",
              zero_results: "No jobs found for [SEARCH_TERM]",
              many_results: "[COUNT] jobs found",
              one_result: "1 job found",
              filters: "Filters",
              load_more: "Load more results",
            },
          });
        }

        // Read query from URL
        const params = new URLSearchParams(window.location.search);
        if (params.has("q")) {
          this.query = params.get("q");
        }
      },

      async searchSemantic() {
        if (!this.query.trim()) return;
        this.semanticLoading = true;
        this.semanticError = "";
        this.semanticResults = [];

        try {
          const resp = await jobsApiFetch(
            "/search/semantic?q=" +
              encodeURIComponent(this.query) +
              "&limit=20"
          );
          if (!resp.ok) throw new Error("Search failed");
          const data = await resp.json();
          this.semanticResults = data.results || [];
        } catch (e) {
          this.semanticError = "Semantic search unavailable. Try keyword search.";
        } finally {
          this.semanticLoading = false;
        }
      },

      switchMode(newMode) {
        this.mode = newMode;
        if (newMode === "semantic" && this.query) {
          this.searchSemantic();
        }
      },

      formatSalary(job) {
        if (!job.salary_min || job.salary_min === 0) return "";
        const fmt = new Intl.NumberFormat("en-US", {
          style: "currency",
          currency: job.currency || "USD",
          maximumFractionDigits: 0,
        });
        return fmt.format(job.salary_min) + "–" + fmt.format(job.salary_max);
      },
    };
  };
});
```

- [ ] **Step 2: Create search page layout**

Create `ui/layouts/search/list.html`:

```html
{{ define "main" }}
<div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8"
     x-data="searchPage()"
     role="search"
     aria-label="Job search">

  <h1 class="text-3xl font-bold text-gray-900">Search Jobs</h1>

  <!-- Mode toggle -->
  <div class="mt-6 flex items-center gap-4" role="tablist" aria-label="Search mode">
    <button @click="switchMode('keyword')"
            :class="mode === 'keyword' ? 'bg-navy-900 text-white' : 'bg-gray-100 text-gray-700'"
            class="rounded-full px-4 py-2 text-sm font-medium transition-colors"
            role="tab"
            :aria-selected="mode === 'keyword'">
      Keyword Search
    </button>
    <button @click="switchMode('semantic')"
            x-show="$store.auth.isAuthenticated && $store.auth.profile?.candidate?.subscription === 'paid'"
            :class="mode === 'semantic' ? 'bg-navy-900 text-white' : 'bg-gray-100 text-gray-700'"
            class="rounded-full px-4 py-2 text-sm font-medium transition-colors"
            role="tab"
            :aria-selected="mode === 'semantic'">
      Best Fit
    </button>
  </div>

  <!-- Pagefind (keyword mode) -->
  <div x-show="mode === 'keyword'" class="mt-6">
    <div id="pagefind-search"></div>
    <link href="/pagefind/pagefind-ui.css" rel="stylesheet">
    <script src="/pagefind/pagefind-ui.js" defer></script>
  </div>

  <!-- Semantic search (best fit mode) -->
  <div x-show="mode === 'semantic'" x-cloak class="mt-6">
    <div class="flex gap-3">
      <input type="text"
             x-model="query"
             @keydown.enter="searchSemantic()"
             class="input-field flex-1"
             placeholder="Describe your ideal job..."
             aria-label="Semantic search query">
      <button @click="searchSemantic()"
              :disabled="semanticLoading"
              class="btn-primary">
        <span x-show="!semanticLoading">Search</span>
        <span x-show="semanticLoading" x-cloak>Searching...</span>
      </button>
    </div>

    <div x-show="semanticError" x-cloak class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700" role="alert" x-text="semanticError"></div>

    <div class="mt-6 space-y-4" aria-live="polite">
      <p x-show="semanticResults.length > 0" class="text-sm text-gray-500" x-text="semanticResults.length + ' jobs matched'"></p>
      <template x-for="job in semanticResults" :key="job.id">
        <article class="card p-4 sm:p-6">
          <a :href="'/jobs/' + job.id + '/'" class="block group">
            <p class="text-sm font-medium text-gray-500" x-text="job.company"></p>
            <h3 class="mt-1 text-lg font-semibold text-gray-900 group-hover:text-accent-600" x-text="job.title"></h3>
            <div class="mt-2 flex flex-wrap items-center gap-2">
              <span class="badge-type" x-text="job.employment_type"></span>
              <span x-show="job.salary_min > 0" class="text-sm font-medium text-green-700" x-text="formatSalary(job)"></span>
            </div>
            <p class="mt-2 text-sm text-gray-500" x-text="job.location_text"></p>
          </a>
        </article>
      </template>
    </div>
  </div>
</div>
{{ end }}

{{ define "scripts" }}
{{ $searchJS := resources.Get "js/search.js" | js.Build (dict "minify" hugo.IsProduction) }}
<script src="{{ $searchJS.RelPermalink }}"></script>
{{ end }}
```

- [ ] **Step 3: Verify build**

Run: `cd ui && hugo`
Expected: search page generated

- [ ] **Step 4: Commit**

```bash
git add ui/assets/js/search.js ui/layouts/search/
git commit -m "feat: add search page with Pagefind keyword search and semantic best-fit"
```

---

## Task 14: Onboarding Wizard

**Files:**
- Create: `ui/assets/js/onboarding.js`
- Create: `ui/layouts/onboarding/single.html`

- [ ] **Step 1: Create onboarding.js — 3-step wizard state machine**

Create `ui/assets/js/onboarding.js`:

```js
document.addEventListener("alpine:init", () => {
  window.onboardingWizard = function () {
    return {
      step: 1,
      loading: false,
      error: "",

      // Step 1 fields
      cvFile: null,
      cvFileName: "",
      targetJobTitle: "",
      experienceLevel: "",
      jobSearchStatus: "",
      salaryRange: "",
      wantsATSReport: false,

      // Step 2 fields
      preferredRegions: [],
      preferredTimezones: [],
      country: "",
      usWorkAuth: null,
      needsSponsorship: null,

      // Step 3
      paymentMethod: "card",
      agreeTerms: false,

      get userName() {
        return Alpine.store("auth").name || "there";
      },

      get canProceedStep1() {
        return this.targetJobTitle && this.experienceLevel && this.jobSearchStatus;
      },

      get canProceedStep2() {
        return this.preferredRegions.length > 0 && this.country;
      },

      nextStep() {
        if (this.step < 3) this.step++;
        this.$nextTick(() => {
          document.getElementById("wizard-top")?.scrollIntoView({ behavior: "smooth" });
        });
      },

      prevStep() {
        if (this.step > 1) this.step--;
      },

      handleFileSelect(event) {
        const file = event.target.files?.[0];
        if (file) {
          if (file.size > 10 * 1024 * 1024) {
            this.error = "File must be under 10MB.";
            return;
          }
          this.cvFile = file;
          this.cvFileName = file.name;
          this.error = "";
        }
      },

      handleFileDrop(event) {
        event.preventDefault();
        const file = event.dataTransfer?.files?.[0];
        if (file) {
          if (file.size > 10 * 1024 * 1024) {
            this.error = "File must be under 10MB.";
            return;
          }
          this.cvFile = file;
          this.cvFileName = file.name;
          this.error = "";
        }
      },

      toggleRegion(region) {
        const idx = this.preferredRegions.indexOf(region);
        if (idx >= 0) {
          this.preferredRegions.splice(idx, 1);
        } else {
          this.preferredRegions.push(region);
        }
      },

      toggleTimezone(tz) {
        const idx = this.preferredTimezones.indexOf(tz);
        if (idx >= 0) {
          this.preferredTimezones.splice(idx, 1);
        } else {
          this.preferredTimezones.push(tz);
        }
      },

      async submitOnboarding() {
        this.loading = true;
        this.error = "";

        try {
          const formData = new FormData();
          if (this.cvFile) formData.append("cv", this.cvFile);
          formData.append("target_job_title", this.targetJobTitle);
          formData.append("experience_level", this.experienceLevel);
          formData.append("job_search_status", this.jobSearchStatus);
          formData.append("preferred_regions", this.preferredRegions.join(", "));
          formData.append("preferred_timezones", this.preferredTimezones.join(", "));
          formData.append("wants_ats_report", String(this.wantsATSReport));
          if (this.usWorkAuth !== null) formData.append("us_work_auth", String(this.usWorkAuth));
          if (this.needsSponsorship !== null) formData.append("needs_sponsorship", String(this.needsSponsorship));

          // Parse salary range
          const salaryMap = {
            "30k-50k": [30000, 50000],
            "50k-75k": [50000, 75000],
            "75k-100k": [75000, 100000],
            "100k+": [100000, 200000],
          };
          const [min, max] = salaryMap[this.salaryRange] || [0, 0];
          formData.append("salary_min", String(min));
          formData.append("salary_max", String(max));
          formData.append("currency", "USD");

          const resp = await apiFetch("/candidates/onboard", {
            method: "POST",
            body: formData,
          });

          if (!resp.ok) {
            const data = await resp.json().catch(() => ({}));
            throw new Error(data.error || "Registration failed");
          }

          // Refresh profile
          await Alpine.store("auth").fetchProfile();

          // Go to payment or dashboard
          if (this.agreeTerms && this.paymentMethod) {
            await this.initiatePayment();
          } else {
            window.location.href = "/dashboard/";
          }
        } catch (e) {
          this.error = e.message;
        } finally {
          this.loading = false;
        }
      },

      async initiatePayment() {
        try {
          const endpoint =
            this.paymentMethod === "mobile" ? "/billing/mobile-pay" : "/billing/checkout";
          const resp = await apiFetch(endpoint, {
            method: "POST",
            body: JSON.stringify({ plan_id: "premium_monthly" }),
          });

          if (!resp.ok) throw new Error("Payment initiation failed");

          const data = await resp.json();
          if (data.checkout_url) {
            window.location.href = data.checkout_url;
          } else {
            // Mobile money — show waiting screen
            window.location.href = "/dashboard/?payment=pending";
          }
        } catch (e) {
          // Payment failed but registration succeeded — go to dashboard on free tier
          window.location.href = "/dashboard/?payment=failed";
        }
      },

      skipPayment() {
        this.submitOnboarding().then(() => {
          window.location.href = "/dashboard/";
        });
      },
    };
  };
});
```

- [ ] **Step 2: Create onboarding layout**

Create `ui/layouts/onboarding/single.html`:

```html
{{ define "main" }}
<div x-data="onboardingWizard()" id="wizard-top" class="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
  <!-- Progress bar -->
  <div class="mb-8" aria-label="Onboarding progress">
    <p class="text-sm font-semibold text-gray-900">STEP <span x-text="step"></span> OF 3</p>
    <div class="mt-2 flex gap-1" role="progressbar" :aria-valuenow="step" aria-valuemin="1" aria-valuemax="3">
      <div class="h-1.5 flex-1 rounded-full" :class="step >= 1 ? 'bg-accent-500' : 'bg-gray-200'"></div>
      <div class="h-1.5 flex-1 rounded-full" :class="step >= 2 ? 'bg-accent-500' : 'bg-gray-200'"></div>
      <div class="h-1.5 flex-1 rounded-full" :class="step >= 3 ? 'bg-accent-500' : 'bg-gray-200'"></div>
    </div>
  </div>

  <!-- Error banner -->
  <div x-show="error" x-cloak class="mb-6 rounded-lg bg-red-50 p-4 text-sm text-red-700" role="alert" x-text="error"></div>

  <div class="grid gap-8 lg:grid-cols-5">
    <!-- Left: Form -->
    <div class="lg:col-span-3">
      <!-- Step 1: About You -->
      <div x-show="step === 1">
        <h1 class="text-3xl font-bold text-gray-900">About you</h1>
        <p class="mt-2 text-gray-600">Hi, <span x-text="userName"></span>. Tell us about yourself so companies know who you are.</p>

        <div class="mt-8 space-y-6">
          <!-- CV Upload -->
          <div>
            <label class="block text-sm font-medium text-gray-700">Upload Your Resume/CV</label>
            <div class="mt-2 flex items-center justify-center rounded-lg border-2 border-dashed border-gray-300 px-6 py-8"
                 @dragover.prevent
                 @drop="handleFileDrop($event)"
                 :class="cvFileName && 'border-accent-300 bg-accent-50'">
              <div class="text-center">
                <p x-show="!cvFileName" class="text-sm text-gray-500">
                  Drag and drop your CV, or
                  <label class="cursor-pointer text-accent-600 hover:text-accent-500">
                    browse
                    <input type="file" class="sr-only" accept=".pdf,.doc,.docx" @change="handleFileSelect($event)" aria-label="Upload CV file">
                  </label>
                </p>
                <p x-show="cvFileName" x-cloak class="text-sm font-medium text-accent-700" x-text="cvFileName"></p>
              </div>
            </div>
          </div>

          <!-- Target Job Title -->
          <div>
            <label for="target-title" class="block text-sm font-medium text-gray-700">Target job title <span class="text-accent-500" aria-hidden="true">*</span></label>
            <input type="text" id="target-title" x-model="targetJobTitle" class="input-field mt-1" placeholder="e.g. Senior Developer" aria-required="true">
          </div>

          <!-- Experience Level -->
          <div>
            <label for="exp-level" class="block text-sm font-medium text-gray-700">Experience Level <span class="text-accent-500" aria-hidden="true">*</span></label>
            <select id="exp-level" x-model="experienceLevel" class="select-field mt-1" aria-required="true">
              <option value="">Select...</option>
              <option value="entry">Entry Level (0-2 years)</option>
              <option value="junior">Junior (2-4 years)</option>
              <option value="mid">Mid-Level (4-6 years)</option>
              <option value="senior">Senior (6-10 years)</option>
              <option value="lead">Lead (10+ years)</option>
              <option value="executive">Executive</option>
            </select>
          </div>

          <!-- Job Status -->
          <div>
            <label for="job-status" class="block text-sm font-medium text-gray-700">Job Status <span class="text-accent-500" aria-hidden="true">*</span></label>
            <select id="job-status" x-model="jobSearchStatus" class="select-field mt-1" aria-required="true">
              <option value="">Select...</option>
              <option value="actively_looking">Actively Looking</option>
              <option value="open_to_offers">Open to Offers</option>
              <option value="casually_browsing">Casually Browsing</option>
            </select>
          </div>

          <!-- Salary Range -->
          <div>
            <label for="salary-range" class="block text-sm font-medium text-gray-700">Preferred Salary Range</label>
            <select id="salary-range" x-model="salaryRange" class="select-field mt-1">
              <option value="">Select...</option>
              <option value="30k-50k">$30,000–$50,000 USD</option>
              <option value="50k-75k">$50,000–$75,000 USD</option>
              <option value="75k-100k">$75,000–$100,000 USD</option>
              <option value="100k+">$100,000+ USD</option>
            </select>
          </div>

          <!-- ATS Report -->
          <label class="flex items-center gap-3">
            <input type="checkbox" x-model="wantsATSReport" class="h-4 w-4 rounded border-gray-300 text-accent-500 focus:ring-accent-500">
            <span class="text-sm text-gray-700">Yes, please email me a free ATS score and resume report</span>
          </label>
        </div>

        <div class="mt-8 flex justify-end">
          <button @click="nextStep()" :disabled="!canProceedStep1" class="btn-primary" :class="!canProceedStep1 && 'opacity-50 cursor-not-allowed'">Continue</button>
        </div>
      </div>

      <!-- Step 2: Curate Search -->
      <div x-show="step === 2" x-cloak>
        <h1 class="text-3xl font-bold text-gray-900">Curate your search</h1>
        <p class="mt-2 text-gray-600">Select your preferences. You can always edit this later.</p>

        <div class="mt-8 space-y-6">
          <!-- Regions -->
          <fieldset>
            <legend class="block text-sm font-medium text-gray-700">Region(s) You're Able To Work In <span class="text-accent-500" aria-hidden="true">*</span></legend>
            <div class="mt-2 flex flex-wrap gap-2">
              {{ $regions := slice "Anywhere in the World" "Africa" "Europe" "North America" "South America" "Asia" "Oceania" }}
              {{ range $regions }}
              <button @click="toggleRegion('{{ . }}')"
                      :class="preferredRegions.includes('{{ . }}') ? 'bg-navy-900 text-white' : 'bg-gray-100 text-gray-700'"
                      class="rounded-full px-3 py-1.5 text-sm font-medium transition-colors"
                      :aria-pressed="preferredRegions.includes('{{ . }}')">
                {{ . }}
              </button>
              {{ end }}
            </div>
          </fieldset>

          <!-- Timezones -->
          <fieldset>
            <legend class="block text-sm font-medium text-gray-700">Preferred Time Zones</legend>
            <div class="mt-2 flex flex-wrap gap-2">
              {{ $timezones := slice "EAT (UTC+3)" "WAT (UTC+1)" "CAT (UTC+2)" "SAST (UTC+2)" "GMT (UTC+0)" "CET (UTC+1)" "EST (UTC-5)" "PST (UTC-8)" }}
              {{ range $timezones }}
              <button @click="toggleTimezone('{{ . }}')"
                      :class="preferredTimezones.includes('{{ . }}') ? 'bg-navy-900 text-white' : 'bg-gray-100 text-gray-700'"
                      class="rounded-full px-3 py-1.5 text-sm font-medium transition-colors"
                      :aria-pressed="preferredTimezones.includes('{{ . }}')">
                {{ . }}
              </button>
              {{ end }}
            </div>
          </fieldset>

          <!-- Country -->
          <div>
            <label for="country" class="block text-sm font-medium text-gray-700">Country <span class="text-accent-500" aria-hidden="true">*</span></label>
            <input type="text" id="country" x-model="country" class="input-field mt-1" placeholder="e.g. Kenya" aria-required="true">
          </div>

          <!-- Work Authorization -->
          <fieldset>
            <legend class="block text-sm font-medium text-gray-700">Are you authorized to work in the US?</legend>
            <div class="mt-2 flex gap-6">
              <label class="flex items-center gap-2">
                <input type="radio" x-model="usWorkAuth" value="true" class="h-4 w-4 border-gray-300 text-accent-500 focus:ring-accent-500" name="us-auth">
                <span class="text-sm text-gray-700">Yes</span>
              </label>
              <label class="flex items-center gap-2">
                <input type="radio" x-model="usWorkAuth" value="false" class="h-4 w-4 border-gray-300 text-accent-500 focus:ring-accent-500" name="us-auth">
                <span class="text-sm text-gray-700">No</span>
              </label>
            </div>
          </fieldset>

          <fieldset x-show="usWorkAuth === 'false'" x-cloak>
            <legend class="block text-sm font-medium text-gray-700">Will you require visa sponsorship?</legend>
            <div class="mt-2 flex gap-6">
              <label class="flex items-center gap-2">
                <input type="radio" x-model="needsSponsorship" value="true" class="h-4 w-4 border-gray-300 text-accent-500 focus:ring-accent-500" name="sponsorship">
                <span class="text-sm text-gray-700">Yes</span>
              </label>
              <label class="flex items-center gap-2">
                <input type="radio" x-model="needsSponsorship" value="false" class="h-4 w-4 border-gray-300 text-accent-500 focus:ring-accent-500" name="sponsorship">
                <span class="text-sm text-gray-700">No</span>
              </label>
            </div>
          </fieldset>
        </div>

        <div class="mt-8 flex justify-between">
          <button @click="prevStep()" class="btn-secondary">Previous</button>
          <button @click="nextStep()" :disabled="!canProceedStep2" class="btn-primary" :class="!canProceedStep2 && 'opacity-50 cursor-not-allowed'">Continue</button>
        </div>
      </div>

      <!-- Step 3: Subscription -->
      <div x-show="step === 3" x-cloak>
        <h1 class="text-3xl font-bold text-gray-900">Get Full Access</h1>
        <p class="mt-2 text-gray-600">Accelerate your remote job search</p>

        <!-- Plan card -->
        <div class="card mt-8 p-6">
          <p class="text-sm text-gray-500 line-through">$14.95/month</p>
          <p class="text-3xl font-bold text-gray-900">$2.95<span class="text-base font-normal text-gray-500">/first month</span></p>
          <p class="mt-2 text-sm text-accent-600">Unlock everything for just $2.95 for your first month, then $14.95/month.</p>
          <ul class="mt-4 space-y-2 text-sm text-gray-600">
            <li class="flex items-center gap-2">
              <svg class="h-4 w-4 text-green-500" fill="currentColor" viewBox="0 0 20 20" aria-hidden="true"><path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd"/></svg>
              AI-powered job matching
            </li>
            <li class="flex items-center gap-2">
              <svg class="h-4 w-4 text-green-500" fill="currentColor" viewBox="0 0 20 20" aria-hidden="true"><path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd"/></svg>
              Unlimited saved jobs
            </li>
            <li class="flex items-center gap-2">
              <svg class="h-4 w-4 text-green-500" fill="currentColor" viewBox="0 0 20 20" aria-hidden="true"><path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd"/></svg>
              ATS resume score report
            </li>
            <li class="flex items-center gap-2">
              <svg class="h-4 w-4 text-green-500" fill="currentColor" viewBox="0 0 20 20" aria-hidden="true"><path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd"/></svg>
              Priority job alerts
            </li>
          </ul>
        </div>

        <!-- Payment method -->
        <div class="mt-6">
          <label class="block text-sm font-medium text-gray-700">Payment Method</label>
          <div class="mt-2 flex gap-4">
            <button @click="paymentMethod = 'card'"
                    :class="paymentMethod === 'card' ? 'border-accent-500 bg-accent-50' : 'border-gray-200'"
                    class="card flex-1 px-4 py-3 text-center text-sm font-medium transition-colors">
              Card / International
            </button>
            <button @click="paymentMethod = 'mobile'"
                    :class="paymentMethod === 'mobile' ? 'border-accent-500 bg-accent-50' : 'border-gray-200'"
                    class="card flex-1 px-4 py-3 text-center text-sm font-medium transition-colors">
              Mobile Money
            </button>
          </div>
        </div>

        <!-- Terms -->
        <label class="mt-6 flex items-start gap-3">
          <input type="checkbox" x-model="agreeTerms" class="mt-0.5 h-4 w-4 rounded border-gray-300 text-accent-500 focus:ring-accent-500">
          <span class="text-sm text-gray-600">I agree to the <a href="/terms/" class="text-accent-600 underline">Terms & Conditions</a> and the renewal terms above.</span>
        </label>

        <div class="mt-8 flex items-center justify-between">
          <div class="flex items-center gap-4">
            <button @click="prevStep()" class="btn-secondary">Previous</button>
            <button @click="skipPayment()" class="text-sm text-gray-500 underline hover:text-gray-700">Skip for now</button>
          </div>
          <button @click="submitOnboarding()"
                  :disabled="!agreeTerms || loading"
                  class="btn-primary"
                  :class="(!agreeTerms || loading) && 'opacity-50 cursor-not-allowed'">
            <span x-show="!loading">Get Full Access</span>
            <span x-show="loading" x-cloak>Processing...</span>
          </button>
        </div>
      </div>
    </div>

    <!-- Right: Social proof panel -->
    <div class="hidden lg:col-span-2 lg:block">
      <!-- Step 1: Stats -->
      <div x-show="step === 1" class="rounded-xl bg-gray-50 p-8 text-center">
        <p class="text-lg font-semibold text-gray-900">Thousands of professionals hired and top companies posting jobs daily.</p>
        <div class="mt-8 flex justify-center gap-8">
          <div>
            <p class="text-sm text-gray-500">Trusted by jobseekers</p>
            <p class="text-2xl font-bold text-gray-900">6M+</p>
          </div>
          <div>
            <p class="text-sm text-gray-500">Jobs Posted</p>
            <p class="text-2xl font-bold text-gray-900">37,000+</p>
          </div>
        </div>
      </div>

      <!-- Steps 2-3: Testimonial -->
      <div x-show="step >= 2" x-cloak class="rounded-xl bg-navy-900 p-8 text-white">
        <p class="text-lg font-semibold">Thousands of professionals hired and top companies posting jobs daily.</p>
        <div class="mt-8 rounded-lg bg-white/10 p-6">
          <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">Testimonial</p>
          <blockquote class="mt-3 text-sm text-gray-200">
            "Stawi has been a great resource during my job hunt. I've discovered new companies and feel more confident about finding the right fit."
          </blockquote>
          <p class="mt-4 font-semibold">Diego Ardura</p>
          <p class="text-sm text-gray-400">Data Analyst</p>
        </div>
      </div>
    </div>
  </div>
</div>
{{ end }}

{{ define "scripts" }}
{{ $onboardingJS := resources.Get "js/onboarding.js" | js.Build (dict "minify" hugo.IsProduction) }}
<script src="{{ $onboardingJS.RelPermalink }}"></script>
{{ end }}
```

- [ ] **Step 2: Verify build**

Run: `cd ui && hugo`
Expected: onboarding page generated

- [ ] **Step 3: Commit**

```bash
git add ui/assets/js/onboarding.js ui/layouts/onboarding/
git commit -m "feat: add 3-step onboarding wizard with CV upload and payment selection"
```

---

## Task 15: Dashboard

**Files:**
- Create: `ui/assets/js/dashboard.js`
- Create: `ui/layouts/dashboard/single.html`

- [ ] **Step 1: Create dashboard.js**

Create `ui/assets/js/dashboard.js`:

```js
document.addEventListener("alpine:init", () => {
  window.dashboardApp = function () {
    return {
      view: "matches",
      matches: [],
      savedJobs: [],
      loading: true,
      error: "",
      profile: null,

      async init() {
        const store = Alpine.store("auth");
        if (!store.isAuthenticated) {
          window.location.href = "/auth/login/";
          return;
        }
        this.profile = store.profile?.candidate;
        await this.loadMatches();
      },

      async loadMatches() {
        this.loading = true;
        try {
          const candidateId = this.profile?.id;
          if (!candidateId) return;
          const resp = await apiFetch(
            "/candidates/matches?candidate_id=" + candidateId + "&limit=50"
          );
          if (resp.ok) {
            const data = await resp.json();
            this.matches = data.matches || [];
          }
        } catch (e) {
          this.error = "Failed to load matches.";
        } finally {
          this.loading = false;
        }
      },

      async loadSaved() {
        this.loading = true;
        try {
          const resp = await apiFetch("/saved-jobs?limit=100");
          if (resp.ok) {
            const data = await resp.json();
            this.savedJobs = data.saved || [];
          }
        } catch (e) {
          this.error = "Failed to load saved jobs.";
        } finally {
          this.loading = false;
        }
      },

      async switchView(newView) {
        this.view = newView;
        this.error = "";
        if (newView === "matches") await this.loadMatches();
        else if (newView === "saved") await this.loadSaved();
      },

      matchScoreColor(score) {
        if (score >= 0.8) return "text-green-600";
        if (score >= 0.6) return "text-yellow-600";
        return "text-gray-500";
      },

      formatScore(score) {
        return Math.round(score * 100) + "%";
      },

      statusBadgeClass(status) {
        const map = {
          new: "bg-blue-100 text-blue-700",
          sent: "bg-gray-100 text-gray-700",
          viewed: "bg-yellow-100 text-yellow-700",
          applied: "bg-green-100 text-green-700",
        };
        return map[status] || "bg-gray-100 text-gray-700";
      },
    };
  };
});
```

- [ ] **Step 2: Create dashboard layout**

Create `ui/layouts/dashboard/single.html`:

```html
{{ define "main" }}
<div x-data="dashboardApp()" class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
  <div class="lg:grid lg:grid-cols-5 lg:gap-8">
    <!-- Sidebar -->
    <nav class="lg:col-span-1" role="navigation" aria-label="Dashboard navigation">
      <div class="flex gap-2 overflow-x-auto lg:flex-col lg:gap-1">
        <button @click="switchView('matches')"
                :class="view === 'matches' ? 'bg-navy-50 text-navy-900 font-semibold' : 'text-gray-600 hover:bg-gray-50'"
                :aria-current="view === 'matches' ? 'page' : false"
                class="whitespace-nowrap rounded-lg px-4 py-2 text-sm text-left transition-colors">
          Matches
        </button>
        <button @click="switchView('saved')"
                :class="view === 'saved' ? 'bg-navy-50 text-navy-900 font-semibold' : 'text-gray-600 hover:bg-gray-50'"
                :aria-current="view === 'saved' ? 'page' : false"
                class="whitespace-nowrap rounded-lg px-4 py-2 text-sm text-left transition-colors">
          Saved Jobs
        </button>
        <button @click="switchView('profile')"
                :class="view === 'profile' ? 'bg-navy-50 text-navy-900 font-semibold' : 'text-gray-600 hover:bg-gray-50'"
                :aria-current="view === 'profile' ? 'page' : false"
                class="whitespace-nowrap rounded-lg px-4 py-2 text-sm text-left transition-colors">
          Profile
        </button>
        <button @click="switchView('billing')"
                :class="view === 'billing' ? 'bg-navy-50 text-navy-900 font-semibold' : 'text-gray-600 hover:bg-gray-50'"
                :aria-current="view === 'billing' ? 'page' : false"
                class="whitespace-nowrap rounded-lg px-4 py-2 text-sm text-left transition-colors">
          Billing
        </button>
      </div>
    </nav>

    <!-- Main content -->
    <div class="mt-6 lg:col-span-4 lg:mt-0">
      <div x-show="error" x-cloak class="mb-4 rounded-lg bg-red-50 p-4 text-sm text-red-700" role="alert" x-text="error"></div>

      <!-- Loading -->
      <div x-show="loading" class="py-12 text-center">
        <div class="mx-auto h-8 w-8 animate-spin rounded-full border-4 border-accent-500 border-t-transparent" aria-label="Loading"></div>
      </div>

      <!-- Matches view -->
      <div x-show="view === 'matches' && !loading" x-cloak>
        <h1 class="text-2xl font-bold text-gray-900">Your Job Matches</h1>
        <p class="mt-1 text-sm text-gray-500" x-text="matches.length + ' matches found'"></p>

        <div class="mt-6 space-y-4" aria-live="polite">
          <template x-for="match in matches" :key="match.id">
            <article class="card p-4 sm:p-6">
              <div class="flex items-start justify-between">
                <div class="min-w-0 flex-1">
                  <h3 class="text-lg font-semibold text-gray-900" x-text="'Job #' + match.canonical_job_id"></h3>
                  <div class="mt-2 flex items-center gap-3">
                    <span class="text-lg font-bold" :class="matchScoreColor(match.match_score)" x-text="formatScore(match.match_score)">
                    </span>
                    <span class="text-sm text-gray-500">match</span>
                    <span class="badge" :class="statusBadgeClass(match.status)" x-text="match.status"></span>
                  </div>
                </div>
                <a :href="'/jobs/?id=' + match.canonical_job_id" class="btn-secondary text-sm">View Job</a>
              </div>
            </article>
          </template>

          <p x-show="matches.length === 0" class="py-12 text-center text-gray-500">
            No matches yet. We're finding the best jobs for your profile.
          </p>
        </div>
      </div>

      <!-- Saved jobs view -->
      <div x-show="view === 'saved' && !loading" x-cloak>
        <h1 class="text-2xl font-bold text-gray-900">Saved Jobs</h1>
        <div class="mt-6 space-y-4">
          <template x-for="sj in savedJobs" :key="sj.id">
            <article class="card p-4">
              <div class="flex items-center justify-between">
                <span class="text-sm text-gray-700">Job #<span x-text="sj.canonical_job_id"></span></span>
                <a :href="'/jobs/?id=' + sj.canonical_job_id" class="text-sm text-accent-600 hover:text-accent-700">View</a>
              </div>
            </article>
          </template>
          <p x-show="savedJobs.length === 0" class="py-12 text-center text-gray-500">
            No saved jobs yet. Browse jobs and save the ones you like.
          </p>
        </div>
      </div>

      <!-- Profile view -->
      <div x-show="view === 'profile' && !loading" x-cloak>
        <h1 class="text-2xl font-bold text-gray-900">Profile & Preferences</h1>
        <div class="mt-6 card p-6">
          <dl class="space-y-4 text-sm">
            <div class="flex justify-between">
              <dt class="font-medium text-gray-500">Name</dt>
              <dd class="text-gray-900" x-text="$store.auth.name"></dd>
            </div>
            <div class="flex justify-between">
              <dt class="font-medium text-gray-500">Status</dt>
              <dd class="text-gray-900 capitalize" x-text="profile?.status || 'Unknown'"></dd>
            </div>
            <div class="flex justify-between">
              <dt class="font-medium text-gray-500">Subscription</dt>
              <dd class="text-gray-900 capitalize" x-text="profile?.subscription || 'free'"></dd>
            </div>
            <div class="flex justify-between">
              <dt class="font-medium text-gray-500">Target Role</dt>
              <dd class="text-gray-900" x-text="profile?.target_job_title || 'Not set'"></dd>
            </div>
            <div class="flex justify-between">
              <dt class="font-medium text-gray-500">Experience</dt>
              <dd class="text-gray-900 capitalize" x-text="profile?.experience_level || 'Not set'"></dd>
            </div>
          </dl>
          <a href="/onboarding/" class="btn-secondary mt-6 inline-block text-sm">Edit Preferences</a>
        </div>
      </div>

      <!-- Billing view -->
      <div x-show="view === 'billing' && !loading" x-cloak>
        <h1 class="text-2xl font-bold text-gray-900">Subscription & Billing</h1>
        <div class="mt-6 card p-6">
          <div class="flex items-center justify-between">
            <div>
              <p class="font-semibold text-gray-900" x-text="(profile?.subscription === 'paid' ? 'Premium' : 'Free') + ' Plan'"></p>
              <p class="text-sm text-gray-500" x-text="profile?.subscription === 'paid' ? '$14.95/month' : 'Limited features'"></p>
            </div>
            <a x-show="profile?.subscription !== 'paid'" href="/pricing/" class="btn-primary text-sm">Upgrade</a>
          </div>
        </div>
      </div>
    </div>
  </div>
</div>
{{ end }}

{{ define "scripts" }}
{{ $dashboardJS := resources.Get "js/dashboard.js" | js.Build (dict "minify" hugo.IsProduction) }}
<script src="{{ $dashboardJS.RelPermalink }}"></script>
{{ end }}
```

- [ ] **Step 3: Verify build**

Run: `cd ui && hugo`
Expected: dashboard page generated

- [ ] **Step 4: Commit**

```bash
git add ui/assets/js/dashboard.js ui/layouts/dashboard/
git commit -m "feat: add candidate dashboard with matches, saved jobs, profile, billing views"
```

---

## Task 16: Build Pipeline and Makefile

**Files:**
- Modify: `Makefile`
- Modify: `ui/.gitignore`
- Modify: `.gitignore`

- [ ] **Step 1: Update root Makefile**

In `/home/j/code/stawi.opportunities/Makefile`, add after the existing targets:

```makefile
# UI targets
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

ui-deps:
	cd ui && npm install
```

- [ ] **Step 2: Update .gitignore**

In `/home/j/code/stawi.opportunities/.gitignore`, add:

```
ui/node_modules/
ui/public/
ui/resources/
ui/.hugo_build.lock
.superpowers/
```

- [ ] **Step 3: Verify full build**

Run: `make build && make ui-build`
Expected: Go binaries compile; Hugo site builds to `ui/public/`

- [ ] **Step 4: Commit**

```bash
git add Makefile .gitignore ui/.gitignore
git commit -m "feat: add UI build pipeline to Makefile (sitegen, hugo, pagefind)"
```

---

## Task 17: Accessibility Audit and Polish

**Files:**
- All layout files (review and fix)

- [ ] **Step 1: Verify all pages have correct heading hierarchy**

Run: `cd ui && hugo && grep -rn "<h[1-6]" public/ | head -50`
Expected: Every page has exactly one `<h1>`, sequential nesting

- [ ] **Step 2: Verify all interactive elements are keyboard-accessible**

Manual check in browser:
- Tab through navbar: logo → Find Jobs → About → Pricing → Search → Sign In
- Tab through job cards: each card is focusable, Enter navigates
- Tab through wizard: all form fields reachable, buttons work with Enter/Space
- Verify focus indicators are visible (3px accent-500 outline)

- [ ] **Step 3: Verify reduced-motion media query is active**

Check `main.css` has `prefers-reduced-motion: reduce` rules (already added in Task 6).

- [ ] **Step 4: Run Pagefind build and verify search accessibility**

Run: `cd ui && npx pagefind --site public --glob "jobs/**/*.html"`
Then open the search page and verify:
- Search input has `role="search"` wrapper
- Results are announced via `aria-live`
- Keyboard navigation through results works

- [ ] **Step 5: Add `robots.txt` and `sitemap.xml` config**

Create `ui/static/robots.txt`:

```
User-agent: *
Allow: /

Sitemap: https://stawi.opportunities/sitemap.xml
```

In `ui/hugo.toml`, ensure sitemap is enabled by adding to `[outputs]`:

```toml
[outputs]
  home = ["HTML", "RSS"]
  section = ["HTML", "RSS"]

[sitemap]
  changefreq = "daily"
  priority = 0.5
  filename = "sitemap.xml"
```

- [ ] **Step 6: Commit**

```bash
git add ui/
git commit -m "feat: accessibility audit, robots.txt, sitemap config"
```

---

## Task 18: Integration Smoke Test

- [ ] **Step 1: Start infrastructure**

Run: `make infra-up`
Expected: Postgres, Redis, OpenSearch running

- [ ] **Step 2: Run API server**

Run: `make run-api &`
Expected: API listening on :8082

- [ ] **Step 3: Generate site data**

Run: `make sitegen`
Expected: `ui/data/jobs.json`, `categories.json`, `stats.json`, `companies.json` written

- [ ] **Step 4: Build and serve Hugo site**

Run: `make ui-deps && cd ui && hugo server --port 1313 &`
Expected: Hugo dev server at http://localhost:1313

- [ ] **Step 5: Verify pages load**

Check in browser:
- Homepage: http://localhost:1313/ — hero, categories, testimonials
- Jobs: http://localhost:1313/jobs/ — job cards render
- Job detail: http://localhost:1313/jobs/senior-go-developer-at-acme-corp-1/ — full detail + JSON-LD
- Categories: http://localhost:1313/categories/ — category grid
- Search: http://localhost:1313/search/ — Pagefind loads
- Onboarding: http://localhost:1313/onboarding/ — wizard renders
- Auth: http://localhost:1313/auth/login/ — login page renders

- [ ] **Step 6: Commit final state**

```bash
git add -A
git commit -m "feat: stawi.opportunities UI platform — initial release

Hugo static site with Alpine.js SPA islands, Pagefind search,
OIDC auth, 3-step onboarding wizard, candidate dashboard,
and payment integration scaffolding."
```
