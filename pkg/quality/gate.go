package quality

import (
	"errors"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Sentinel errors returned by Check.
var (
	ErrMissingTitle     = errors.New("missing_title")
	ErrShortDescription = errors.New("short_description")
)

// Check validates an ExternalOpportunity against the quality gate rules.
// It returns the first validation error encountered, or nil if the job passes.
func Check(j domain.ExternalOpportunity) error {
	title := strings.TrimSpace(j.Title)
	if title == "" || len(title) <= 3 {
		return ErrMissingTitle
	}

	if len(strings.TrimSpace(j.Description)) <= 50 {
		return ErrShortDescription
	}

	return nil
}

// EnsureApplyURL sets job.ApplyURL to fallbackURL if it's currently empty.
func EnsureApplyURL(job *domain.ExternalOpportunity, fallbackURL string) {
	if strings.TrimSpace(job.ApplyURL) == "" && fallbackURL != "" {
		job.ApplyURL = fallbackURL
	}
}
