package quality

import (
	"errors"
	"strings"

	"stawi.jobs/pkg/domain"
)

// Sentinel errors returned by Check.
var (
	ErrMissingTitle       = errors.New("missing_title")
	ErrMissingCompany     = errors.New("missing_company")
	ErrMissingLocation    = errors.New("missing_location")
	ErrShortDescription   = errors.New("short_description")
	ErrMissingApplyURL    = errors.New("missing_apply_url")
	ErrInvalidApplyURL    = errors.New("invalid_apply_url")
)

// Check validates an ExternalJob against the quality gate rules.
// It returns the first validation error encountered, or nil if the job passes.
func Check(j domain.ExternalJob) error {
	title := strings.TrimSpace(j.Title)
	if title == "" || len(title) <= 3 {
		return ErrMissingTitle
	}

	company := strings.TrimSpace(j.Company)
	if company == "" || len(company) <= 1 {
		return ErrMissingCompany
	}

	if strings.TrimSpace(j.LocationText) == "" {
		return ErrMissingLocation
	}

	if len(strings.TrimSpace(j.Description)) <= 50 {
		return ErrShortDescription
	}

	applyURL := strings.TrimSpace(j.ApplyURL)
	if applyURL == "" {
		return ErrMissingApplyURL
	}
	if !strings.HasPrefix(applyURL, "http://") && !strings.HasPrefix(applyURL, "https://") {
		return ErrInvalidApplyURL
	}

	return nil
}
