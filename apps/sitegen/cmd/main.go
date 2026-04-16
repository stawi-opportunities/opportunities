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

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
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
	TotalJobs      int64  `json:"total_jobs"`
	TotalCompanies int64  `json:"total_companies"`
	JobsThisWeek   int64  `json:"jobs_this_week"`
	GeneratedAt    string `json:"generated_at"`
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
