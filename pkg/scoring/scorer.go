// Package scoring computes quality scores for canonical jobs based on
// actionability signals. Higher scores indicate jobs more likely to convert.
package scoring

import "stawi.jobs/pkg/domain"

// Score computes a quality score (0-100) for a canonical job based on
// actionability signals. Higher score = more likely to convert to a hire.
func Score(job *domain.CanonicalJob) float64 {
	score := 0.0

	// Urgency (0-25 points)
	switch job.UrgencyLevel {
	case "urgent":
		score += 25
	case "normal":
		score += 10
	}
	if job.HiringTimeline == "immediate" {
		score += 5
	}

	// Compensation clarity (0-20 points)
	if job.SalaryMin > 0 || job.SalaryMax > 0 {
		score += 15
	}
	if job.Currency != "" {
		score += 5
	}

	// Remote/global accessibility (0-15 points)
	switch job.RemoteType {
	case "remote":
		score += 15
	case "hybrid":
		score += 8
	}
	if job.GeoRestrictions == "global" {
		score += 5
	}

	// Role clarity (0-15 points)
	if job.Title != "" {
		score += 5
	}
	if job.Seniority != "" {
		score += 5
	}
	if job.RoleScope != "" {
		score += 5
	}

	// Low funnel complexity (0-10 points)
	switch job.FunnelComplexity {
	case "low":
		score += 10
	case "medium":
		score += 5
	}

	// Company quality (0-10 points)
	switch job.CompanySize {
	case "startup", "small":
		score += 8 // faster decisions
	case "medium":
		score += 5
	case "large", "enterprise":
		score += 3
	}

	// Contact surface (0-5 points)
	if job.ContactEmail != "" {
		score += 3
	}
	if job.ContactName != "" {
		score += 2
	}

	// Cap at 100
	if score > 100 {
		score = 100
	}
	return score
}
