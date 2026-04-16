package publish

import (
	"fmt"
	"strings"
	"time"

	"stawi.jobs/pkg/domain"
)

// RenderJobMarkdown produces a Hugo-compatible markdown file with YAML frontmatter
// from a CanonicalJob. The slug is the permanent URL path for the job.
func RenderJobMarkdown(job *domain.CanonicalJob) []byte {
	var b strings.Builder

	postedAt := time.Now().Format(time.RFC3339)
	if job.PostedAt != nil {
		postedAt = job.PostedAt.Format(time.RFC3339)
	}

	category := string(domain.DeriveCategory(job.Roles, job.Industry))
	skills := formatSkillsYAML(job.Skills, job.RequiredSkills)
	isFeatured := job.QualityScore >= 80

	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("title: %q\n", job.Title))
	b.WriteString(fmt.Sprintf("date: %s\n", postedAt))
	b.WriteString(fmt.Sprintf("slug: %q\n", job.Slug))
	b.WriteString("params:\n")
	b.WriteString(fmt.Sprintf("  id: %d\n", job.ID))
	b.WriteString(fmt.Sprintf("  company: %q\n", job.Company))
	b.WriteString(fmt.Sprintf("  category: %q\n", category))
	b.WriteString(fmt.Sprintf("  location_text: %q\n", job.LocationText))
	b.WriteString(fmt.Sprintf("  remote_type: %q\n", job.RemoteType))
	b.WriteString(fmt.Sprintf("  employment_type: %q\n", job.EmploymentType))
	b.WriteString(fmt.Sprintf("  salary_min: %g\n", job.SalaryMin))
	b.WriteString(fmt.Sprintf("  salary_max: %g\n", job.SalaryMax))
	b.WriteString(fmt.Sprintf("  currency: %q\n", job.Currency))
	b.WriteString(fmt.Sprintf("  seniority: %q\n", job.Seniority))
	b.WriteString(skills)
	b.WriteString(fmt.Sprintf("  apply_url: %q\n", job.ApplyURL))
	b.WriteString(fmt.Sprintf("  quality_score: %g\n", job.QualityScore))
	b.WriteString(fmt.Sprintf("  is_featured: %t\n", isFeatured))
	b.WriteString("---\n\n")

	if job.Description != "" {
		b.WriteString(job.Description)
		b.WriteString("\n")
	}

	return []byte(b.String())
}

// formatSkillsYAML renders skills as a YAML list under params.
func formatSkillsYAML(skills, requiredSkills string) string {
	raw := skills
	if raw == "" {
		raw = requiredSkills
	}
	if raw == "" {
		return "  skills: []\n"
	}

	parts := strings.Split(raw, ",")
	var cleaned []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return "  skills: []\n"
	}

	var b strings.Builder
	b.WriteString("  skills:\n")
	for _, s := range cleaned {
		b.WriteString(fmt.Sprintf("    - %q\n", s))
	}
	return b.String()
}
